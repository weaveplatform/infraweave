package token

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
)

// keySet is one purpose's keys as a replica sees them: the active key's
// signer, reread every SignerRefresh, and the public keys of the keys that
// verify, cached by kid.
type keySet struct {
	store   Store
	purpose Purpose
	policy  Policy
	now     func() time.Time

	signMu   sync.Mutex
	active   signer.Signer
	loadedAt time.Time

	verifyMu   sync.Mutex
	public     map[string]jose.JSONWebKey
	published  []jose.JSONWebKey
	reloadedAt time.Time
}

func newKeySet(store Store, purpose Purpose, policy Policy, now func() time.Time) *keySet {
	return &keySet{store: store, purpose: purpose, policy: policy, now: now}
}

// signer returns the active key's signer, reading which key is active at
// most every SignerRefresh.
func (k *keySet) signer(ctx context.Context) (signer.Signer, error) {
	k.signMu.Lock()
	defer k.signMu.Unlock()
	now := k.now()
	if k.active != nil && now.Sub(k.loadedAt) < SignerRefresh {
		return k.active, nil
	}
	id, err := k.activeKey(ctx)
	if err != nil {
		return nil, err
	}
	sg, err := k.store.Signer(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("open %s signing key %s: %w", k.purpose, id, err)
	}
	k.active, k.loadedAt = sg, now
	return sg, nil
}

// activeKey returns the active key's ID, creating the first key when the
// purpose has none. Bootstrapping goes through Rotate, so replicas that
// start together on an empty table agree on one key.
func (k *keySet) activeKey(ctx context.Context) (kernel.ULID, error) {
	for range 2 {
		keys, err := k.store.Keys(ctx, k.purpose)
		if err != nil {
			return kernel.ULID{}, fmt.Errorf("read %s signing keys: %w", k.purpose, err)
		}
		for _, key := range keys {
			if key.State == StateActive {
				return key.ID, nil
			}
		}
		first := k.policy
		first.Manual = true
		if err := k.store.Rotate(ctx, k.purpose, func(keys []Key) []Step {
			return first.Plan(keys, k.now())
		}); err != nil {
			return kernel.ULID{}, fmt.Errorf("create the first %s signing key: %w", k.purpose, err)
		}
	}
	return kernel.ULID{}, fmt.Errorf("%w: %s", ErrNoKey, k.purpose)
}

// rotate applies the rotation due now.
func (k *keySet) rotate(ctx context.Context) error {
	now := k.now()
	if err := k.store.Rotate(ctx, k.purpose, func(keys []Key) []Step {
		return k.policy.Plan(keys, now)
	}); err != nil {
		return fmt.Errorf("rotate %s signing keys: %w", k.purpose, err)
	}
	return nil
}

// sign signs claims as a JWT, with typ JWT, with the active key under its
// algorithm.
func (k *keySet) sign(ctx context.Context, claims any) (string, error) {
	sg, err := k.signer(ctx)
	if err != nil {
		return "", err
	}
	js, err := jose.NewSigner(
		jose.SigningKey{
			Algorithm: jose.SignatureAlgorithm(sg.Public().Algorithm),
			Key:       opaque{ctx: ctx, s: sg},
		},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		return "", fmt.Errorf("sign %s token: %w", k.purpose, err)
	}
	raw, err := jwt.Signed(js).Claims(claims).Serialize()
	if err != nil {
		return "", fmt.Errorf("sign %s token: %w", k.purpose, err)
	}
	return raw, nil
}

// publicKey returns the public key kid names from this replica's cache,
// reloading the cache when kid is unknown and the last reload is at least
// reloadEvery old.
func (k *keySet) publicKey(ctx context.Context, kid string) (jose.JSONWebKey, bool, error) {
	k.verifyMu.Lock()
	defer k.verifyMu.Unlock()
	if key, ok := k.public[kid]; ok {
		return key, true, nil
	}
	if k.public != nil && k.now().Sub(k.reloadedAt) < reloadEvery {
		return jose.JSONWebKey{}, false, nil
	}
	if err := k.reload(ctx); err != nil {
		return jose.JSONWebKey{}, false, err
	}
	key, ok := k.public[kid]
	return key, ok, nil
}

// publishedKeys returns the public halves of the keys that verify, oldest
// first, from this replica's cache, reloading it once it is SignerRefresh
// old: a next key verifies for MinPublished before it signs, far longer.
func (k *keySet) publishedKeys(ctx context.Context) ([]jose.JSONWebKey, error) {
	k.verifyMu.Lock()
	defer k.verifyMu.Unlock()
	if k.public == nil || k.now().Sub(k.reloadedAt) >= SignerRefresh {
		if err := k.reload(ctx); err != nil {
			return nil, err
		}
	}
	return append([]jose.JSONWebKey(nil), k.published...), nil
}

// reload reads the keys that verify into the cache; verifyMu is held.
func (k *keySet) reload(ctx context.Context) error {
	keys, err := k.store.Keys(ctx, k.purpose)
	if err != nil {
		return fmt.Errorf("read %s signing keys: %w", k.purpose, err)
	}
	k.public = make(map[string]jose.JSONWebKey, len(keys))
	k.published = make([]jose.JSONWebKey, 0, len(keys))
	for _, key := range keys {
		pub := key.Public.Public()
		pub.Use = "sig"
		k.public[key.Public.KeyID] = key.Public
		k.published = append(k.published, pub)
	}
	k.reloadedAt = k.now()
	return nil
}

// opaque signs a JWS through a Signer: go-jose hands it the signing input,
// and it signs the input's SHA-256.
type opaque struct {
	ctx context.Context //nolint:containedctx // go-jose's OpaqueSigner takes no context
	s   signer.Signer
}

func (o opaque) Public() *jose.JSONWebKey {
	k := o.s.Public()
	return &k
}

func (o opaque) Algs() []jose.SignatureAlgorithm {
	return []jose.SignatureAlgorithm{jose.SignatureAlgorithm(o.s.Public().Algorithm)}
}

func (o opaque) SignPayload(payload []byte, _ jose.SignatureAlgorithm) ([]byte, error) {
	digest := sha256.Sum256(payload)
	return o.s.Sign(o.ctx, digest[:]) //nolint:wrapcheck // go-jose wraps it
}
