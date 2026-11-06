package token

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
)

// Kind is a token's iw_kind claim: the kind of credential it stands for.
type Kind string

// The iw_kind values of decision 0006.
const (
	KindUser            Kind = "user"
	KindAPIKey          Kind = "api_key"
	KindOrganizationKey Kind = "organization_key"
	KindOAuth           Kind = "oauth"
	KindRun             Kind = "run"
	KindOperator        Kind = "operator"
)

// Kinds are every Kind.
var Kinds = []Kind{KindUser, KindAPIKey, KindOrganizationKey, KindOAuth, KindRun, KindOperator}

// Leeway is the clock skew Verify allows on iat, nbf and exp, between
// replicas of one installation (decision 0006's constraints).
const Leeway = 30 * time.Second

// reloadEvery bounds how often a token naming an unknown kid reloads the
// public keys, so a flood of forged kids costs one query a second.
const reloadEvery = time.Second

var (
	// ErrToken is every reason Verify refuses a token; the error says which.
	ErrToken = errors.New("invalid token")
	// ErrRequest reports an Issue request missing a claim.
	ErrRequest = errors.New("invalid token request")
	// ErrNoKey reports a purpose with no active key after bootstrapping
	// one, which only a store that does not apply steps causes.
	ErrNoKey = errors.New("no active signing key")
)

// Store keeps the signing keys.
type Store interface {
	// Keys returns purpose's keys that are not retired.
	Keys(ctx context.Context, purpose Purpose) ([]Key, error)
	// Signer opens the private half of the key id.
	Signer(ctx context.Context, id kernel.ULID) (signer.Signer, error)
	// Rotate passes plan purpose's keys that are not retired and applies
	// the steps it returns, in order, in one transaction that no other
	// Rotate runs beside, so concurrent replicas agree on one active key.
	Rotate(ctx context.Context, purpose Purpose, plan func([]Key) []Step) error
}

// Request is a token to issue.
type Request struct {
	Kind    Kind
	Subject string
	// Session is the session the token stands for, its jti.
	Session  kernel.ULID
	Audience string
	TTL      time.Duration
}

// Claims are a verified token's claims.
type Claims struct {
	Issuer   string
	Subject  string
	Audience string
	Session  kernel.ULID
	Kind     Kind
	IssuedAt time.Time
	Expiry   time.Time
}

// wire is the JWT's claim set: the registered claims and iw_kind.
type wire struct {
	jwt.Claims

	Kind Kind `json:"iw_kind"` //nolint:tagliatelle // decision 0006's claim name
}

// Service issues and verifies the api-token purpose's JWTs and rotates its
// keys.
type Service struct {
	store  Store
	issuer string
	policy Policy
	now    func() time.Time

	signMu   sync.Mutex
	active   signer.Signer
	loadedAt time.Time

	verifyMu   sync.Mutex
	public     map[string]jose.JSONWebKey
	reloadedAt time.Time
}

// New returns the service over store. issuer is every token's iss, and
// now is the clock.
func New(store Store, issuer string, policy Policy, now func() time.Time) *Service {
	return &Service{store: store, issuer: issuer, policy: policy, now: now}
}

// Issue signs a token for r with the active key, creating the first key
// if there is none. Its times are whole seconds, as the claims carry them.
func (s *Service) Issue(ctx context.Context, r Request) (string, Claims, error) {
	if !slices.Contains(Kinds, r.Kind) || r.Subject == "" || r.Session.IsZero() ||
		r.Audience == "" || r.TTL < time.Second {
		return "", Claims{}, fmt.Errorf("%w: %+v", ErrRequest, r)
	}
	sg, err := s.signer(ctx)
	if err != nil {
		return "", Claims{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	c := Claims{
		Issuer:   s.issuer,
		Subject:  r.Subject,
		Audience: r.Audience,
		Session:  r.Session,
		Kind:     r.Kind,
		IssuedAt: now,
		Expiry:   now.Add(r.TTL),
	}
	js, err := jose.NewSigner(
		jose.SigningKey{Algorithm: signer.ES256, Key: opaque{ctx: ctx, s: sg}},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		return "", Claims{}, fmt.Errorf("issue token: %w", err)
	}
	raw, err := jwt.Signed(js).Claims(wire{
		Claims: jwt.Claims{
			Issuer:    c.Issuer,
			Subject:   c.Subject,
			Audience:  jwt.Audience{c.Audience},
			ID:        c.Session.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Expiry:    jwt.NewNumericDate(c.Expiry),
		},
		Kind: c.Kind,
	}).Serialize()
	if err != nil {
		return "", Claims{}, fmt.Errorf("issue token: %w", err)
	}
	return raw, c, nil
}

// signer returns the active key's signer, reading which key is active at
// most every SignerRefresh.
func (s *Service) signer(ctx context.Context) (signer.Signer, error) {
	s.signMu.Lock()
	defer s.signMu.Unlock()
	now := s.now()
	if s.active != nil && now.Sub(s.loadedAt) < SignerRefresh {
		return s.active, nil
	}
	id, err := s.activeKey(ctx)
	if err != nil {
		return nil, err
	}
	sg, err := s.store.Signer(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("open signing key %s: %w", id, err)
	}
	s.active, s.loadedAt = sg, now
	return sg, nil
}

// activeKey returns the active key's ID, creating the first key when the
// purpose has none. Bootstrapping goes through Rotate, so replicas that
// start together on an empty table agree on one key.
func (s *Service) activeKey(ctx context.Context) (kernel.ULID, error) {
	for range 2 {
		keys, err := s.store.Keys(ctx, APIToken)
		if err != nil {
			return kernel.ULID{}, fmt.Errorf("read signing keys: %w", err)
		}
		for _, k := range keys {
			if k.State == StateActive {
				return k.ID, nil
			}
		}
		if err := s.store.Rotate(ctx, APIToken, func(keys []Key) []Step {
			return Policy{Manual: true, Refs: s.policy.Refs}.Plan(keys, s.now())
		}); err != nil {
			return kernel.ULID{}, fmt.Errorf("create the first signing key: %w", err)
		}
	}
	return kernel.ULID{}, ErrNoKey
}

// Rotate is authn.keys.rotate: it applies the rotation due now.
func (s *Service) Rotate(ctx context.Context) error {
	now := s.now()
	if err := s.store.Rotate(ctx, APIToken, func(keys []Key) []Step {
		return s.policy.Plan(keys, now)
	}); err != nil {
		return fmt.Errorf("rotate signing keys: %w", err)
	}
	return nil
}

// Verify checks raw's signature against the key its kid names, and its
// claims: ES256 only, s's issuer, exactly the one audience given, a
// session ULID, a known iw_kind, and iat, nbf and exp within Leeway of now.
// Every refusal wraps ErrToken; a store that cannot be read is not one.
func (s *Service) Verify(ctx context.Context, raw, audience string) (Claims, error) {
	tok, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{signer.ES256})
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrToken, err)
	}
	kid := tok.Headers[0].KeyID // a compact JWS has exactly one signature
	key, ok, err := s.publicKey(ctx, kid)
	if err != nil {
		return Claims{}, err
	}
	if !ok {
		return Claims{}, fmt.Errorf("%w: unknown key %q", ErrToken, kid)
	}
	var w wire
	if err := tok.Claims(key.Key, &w); err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrToken, err)
	}
	session, err := kernel.ParseULID(w.ID)
	switch {
	case w.IssuedAt == nil || w.NotBefore == nil || w.Expiry == nil:
		return Claims{}, fmt.Errorf("%w: iat, nbf and exp are required", ErrToken)
	case len(w.Audience) != 1 || w.Audience[0] != audience:
		return Claims{}, fmt.Errorf(
			"%w: aud %q is not %q",
			ErrToken,
			[]string(w.Audience),
			audience,
		)
	case err != nil:
		return Claims{}, fmt.Errorf("%w: jti: %w", ErrToken, err)
	case w.Subject == "":
		return Claims{}, fmt.Errorf("%w: no sub", ErrToken)
	case !slices.Contains(Kinds, w.Kind):
		return Claims{}, fmt.Errorf("%w: iw_kind %q", ErrToken, w.Kind)
	}
	if err := w.ValidateWithLeeway(jwt.Expected{
		Issuer:      s.issuer,
		AnyAudience: jwt.Audience{audience},
		Time:        s.now(),
	}, Leeway); err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrToken, err)
	}
	return Claims{
		Issuer:   w.Issuer,
		Subject:  w.Subject,
		Audience: audience,
		Session:  session,
		Kind:     w.Kind,
		IssuedAt: w.IssuedAt.Time().UTC(),
		Expiry:   w.Expiry.Time().UTC(),
	}, nil
}

// publicKey returns the public key kid names from this replica's cache,
// reloading the cache when kid is unknown and the last reload is at least
// reloadEvery old.
func (s *Service) publicKey(ctx context.Context, kid string) (jose.JSONWebKey, bool, error) {
	s.verifyMu.Lock()
	defer s.verifyMu.Unlock()
	if k, ok := s.public[kid]; ok {
		return k, true, nil
	}
	now := s.now()
	if s.public != nil && now.Sub(s.reloadedAt) < reloadEvery {
		return jose.JSONWebKey{}, false, nil
	}
	keys, err := s.store.Keys(ctx, APIToken)
	if err != nil {
		return jose.JSONWebKey{}, false, fmt.Errorf("read signing keys: %w", err)
	}
	s.public = make(map[string]jose.JSONWebKey, len(keys))
	for _, k := range keys {
		s.public[k.Public.KeyID] = k.Public
	}
	s.reloadedAt = now
	k, ok := s.public[kid]
	return k, ok, nil
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
