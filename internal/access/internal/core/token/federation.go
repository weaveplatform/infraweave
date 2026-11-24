package token

import (
	"context"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// FederationService signs OIDC federation tokens with the federation
// purpose's active key and answers its published keys, those a relying
// party verifies the tokens with. It rotates the federation keys apart
// from the api-token keys, under the same schedule.
//
// ref: decision 0006 § "Signing keys"
type FederationService struct {
	keys *keySet
}

// NewFederation returns the federation keys over store. policy's
// Algorithm is auth.federation.algorithm, its TokenLife the federation
// token's lifetime and its Refs auth.federation.kms_keys; now is the
// clock.
func NewFederation(store Store, policy Policy, now func() time.Time) *FederationService {
	return &FederationService{keys: newKeySet(store, Federation, policy, now)}
}

// Bootstrap creates the first federation key when there is none, so the
// published keys are never empty once a replica has started; replicas
// that start together on an empty table agree on one key.
func (f *FederationService) Bootstrap(ctx context.Context) error {
	_, err := f.keys.activeKey(ctx)
	return err
}

// Rotate applies the federation purpose's rotation due now, one half of
// authn.keys.rotate.
func (f *FederationService) Rotate(ctx context.Context) error { return f.keys.rotate(ctx) }

// Sign signs claims as a JWT with the active federation key, under its
// algorithm, its kid in the header. The caller sets every claim.
func (f *FederationService) Sign(ctx context.Context, claims any) (string, error) {
	return f.keys.sign(ctx, claims)
}

// FederationKeys are the published federation keys, next, active and
// retiring, oldest first, as RFC 7517 JWKs with kid, use sig and alg and
// the public members alone. A replica caches them and rereads them once
// the cache is SignerRefresh old.
func (f *FederationService) FederationKeys(ctx context.Context) ([]jose.JSONWebKey, error) {
	return f.keys.publishedKeys(ctx)
}

// FederationKey is the published federation key kid names, from the
// replica's cache, which an unknown kid reloads at most once a second.
func (f *FederationService) FederationKey(ctx context.Context, kid string) (jose.JSONWebKey, bool, error) {
	key, ok, err := f.keys.publicKey(ctx, kid)
	if ok {
		key = key.Public()
		key.Use = "sig"
	}
	return key, ok, err
}
