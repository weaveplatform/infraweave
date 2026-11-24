package token_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
)

// federationLife is the federation token's default lifetime.
const federationLife = time.Hour

func federation(store *memStore, c *clock, alg jose.SignatureAlgorithm, refs ...string) *token.FederationService {
	return token.NewFederation(store, token.Policy{
		TokenLife: federationLife, Algorithm: alg, Refs: refs,
	}, c.now)
}

// federationClaims are a federation token's claims at now, as a relying
// party checks them.
func federationClaims(now time.Time) jwt.Claims {
	return jwt.Claims{
		Issuer:    issuer,
		Subject:   "space:root:stack:s:run_type:TRACKED:scope:write",
		Audience:  jwt.Audience{"iw.example.com/acme"},
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		Expiry:    jwt.NewNumericDate(now.Add(federationLife)),
	}
}

// verifyPublished checks raw with go-jose against the published key its
// kid names, as a relying party does with the JWKS, accepting alg alone.
func verifyPublished(t *testing.T, f *token.FederationService, raw string, alg jose.SignatureAlgorithm) (jwt.Claims, error) {
	t.Helper()
	keys, err := f.FederationKeys(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	tok, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{alg})
	if err != nil {
		return jwt.Claims{}, err //nolint:wrapcheck // a test's
	}
	set := jose.JSONWebKeySet{Keys: keys}
	matching := set.Key(tok.Headers[0].KeyID)
	if len(matching) != 1 {
		return jwt.Claims{}, errors.New("no published key names the token's kid")
	}
	var claims jwt.Claims
	return claims, tok.Claims(matching[0].Key, &claims) //nolint:wrapcheck // a test's
}

// TestUnitFederationAlgorithms checks that the first read of the published
// keys creates the first federation key, once, of the configured
// algorithm, RSA 3072 for RS256 and PS256 and P-256 for ES256, that a
// token it signs names it and verifies with go-jose against its published
// JWK alone, and that the api-token keys are untouched.
func TestUnitFederationAlgorithms(t *testing.T) {
	for _, alg := range signer.Algorithms {
		t.Run(string(alg), func(t *testing.T) {
			ctx := context.Background()
			store, c := newMemStore(), &clock{t: start}
			f := federation(store, c, alg)
			for range 2 {
				if keys, err := f.FederationKeys(ctx); err != nil || len(keys) != 1 {
					t.Fatalf("FederationKeys = %+v, %v", keys, err)
				}
			}
			live, _ := store.Keys(ctx, token.Federation)
			if len(live) != 1 || live[0].State != token.StateActive || live[0].Public.Algorithm != string(alg) {
				t.Fatalf("federation keys = %+v, want one active %s key", live, alg)
			}
			if api, _ := store.Keys(ctx, token.APIToken); len(api) != 0 {
				t.Errorf("the federation keys made api-token keys: %+v", api)
			}
			switch pub := live[0].Public.Key.(type) {
			case *rsa.PublicKey:
				if alg == signer.ES256 || pub.N.BitLen() != signer.RSABits {
					t.Errorf("%s key is RSA %d", alg, pub.N.BitLen())
				}
			case *ecdsa.PublicKey:
				if alg != signer.ES256 || pub.Curve != elliptic.P256() {
					t.Errorf("%s key is ECDSA %s", alg, pub.Curve.Params().Name)
				}
			default:
				t.Errorf("%s key is %T", alg, pub)
			}

			raw, err := f.Sign(ctx, federationClaims(c.now()))
			if err != nil {
				t.Fatal(err)
			}
			header := segment(t, raw, 0)
			if header["alg"] != string(alg) || header["kid"] != live[0].ID.String() || header["typ"] != "JWT" {
				t.Errorf("header = %v", header)
			}
			got, err := verifyPublished(t, f, raw, alg)
			if err != nil || got.Subject != federationClaims(c.now()).Subject {
				t.Errorf("verify against the published key = %+v, %v", got, err)
			}
			keys, err := f.FederationKeys(ctx)
			if err != nil || len(keys) != 1 {
				t.Fatalf("FederationKeys = %+v, %v", keys, err)
			}
			encoded, err := json.Marshal(keys[0])
			if err != nil {
				t.Fatal(err)
			}
			var members map[string]any
			if err := json.Unmarshal(encoded, &members); err != nil {
				t.Fatal(err)
			}
			if members["kid"] != live[0].ID.String() || members["use"] != "sig" || members["alg"] != string(alg) ||
				members["d"] != nil || members["p"] != nil {
				t.Errorf("published JWK = %s", encoded)
			}
			// Another algorithm's verifier refuses the token.
			other := signer.RS256
			if alg == signer.RS256 {
				other = signer.PS256
			}
			if _, err := verifyPublished(t, f, raw, other); err == nil {
				t.Errorf("a %s token verified as %s", alg, other)
			}
		})
	}
}

// TestUnitFederationRotation runs authn.keys.rotate's federation half over
// a key lifetime in virtual time: it creates no first key, the first
// signing does, the successor is published from day 83 and signs from day
// 90, and the retiring key stays published until its last token's expiry
// plus RetireGrace.
func TestUnitFederationRotation(t *testing.T) {
	ctx := context.Background()
	store, c := newMemStore(), &clock{t: start}
	f := federation(store, c, signer.RS256)
	rotate := func() {
		t.Helper()
		if err := f.Rotate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	sign := func() string {
		t.Helper()
		raw, err := f.Sign(ctx, federationClaims(c.now()))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	published := func() []string {
		t.Helper()
		keys, err := f.FederationKeys(ctx)
		if err != nil {
			t.Fatal(err)
		}
		kids := make([]string, 0, len(keys))
		for _, k := range keys {
			kids = append(kids, k.KeyID)
		}
		return kids
	}

	rotate()
	if live, _ := store.Keys(ctx, token.Federation); len(live) != 0 {
		t.Fatalf("the first rotation created %+v, want no federation key before its first use", live)
	}
	a := kidOf(t, sign())
	for range 82 {
		c.advance(day)
		rotate()
	}
	if kids := published(); len(kids) != 1 || kids[0] != a {
		t.Fatalf("day 82: published %v", kids)
	}
	c.advance(day)
	rotate()
	// The replica's published keys are reread once SignerRefresh old.
	c.advance(token.SignerRefresh)
	kids := published()
	if len(kids) != 2 || kids[0] != a || store.state(kids[1]) != token.StateNext {
		t.Fatalf("day 83: published %v, want the active key and a next key", kids)
	}
	b := kids[1]
	if kidOf(t, sign()) != a {
		t.Error("the next key signs before it takes over")
	}

	lifetime := 90 * day
	c.at(lifetime - 30*time.Second)
	rotate()
	last := sign()
	c.at(lifetime)
	rotate()
	if store.state(a) != token.StateRetiring || store.state(b) != token.StateActive {
		t.Fatalf("day 90: a %s, b %s", store.state(a), store.state(b))
	}
	c.at(lifetime - 30*time.Second + token.SignerRefresh)
	if kidOf(t, sign()) != b {
		t.Error("the new key does not sign after SignerRefresh")
	}
	c.at(lifetime - 30*time.Second + federationLife)
	if _, err := verifyPublished(t, f, last, signer.RS256); err != nil {
		t.Errorf("the old key's last token stopped verifying before it expired: %v", err)
	}

	// The retiring key stays published until its last possible token
	// expires plus RetireGrace.
	retired := lifetime + token.SignerRefresh + federationLife + token.RetireGrace
	c.at(retired - time.Second)
	rotate()
	if kids := published(); len(kids) != 2 || kids[0] != a {
		t.Fatalf("a second early: published %v", kids)
	}
	c.at(retired)
	rotate()
	c.advance(token.SignerRefresh)
	if kids := published(); len(kids) != 1 || kids[0] != b {
		t.Fatalf("on time: published %v, want b alone", kids)
	}
	if api, _ := store.Keys(ctx, token.APIToken); len(api) != 0 {
		t.Errorf("federation rotation made api-token keys: %+v", api)
	}
}

// TestUnitFederationKMSRotation checks the federation keys under the kms
// backend: an added reference is published as the next key 48 hours
// before it signs.
func TestUnitFederationKMSRotation(t *testing.T) {
	ctx := context.Background()
	store, c := newMemStore(), &clock{t: start}
	if _, err := federation(store, c, signer.PS256, "awskms:a").FederationKeys(ctx); err != nil {
		t.Fatal(err)
	}
	f := federation(store, c, signer.PS256, "awskms:a", "awskms:b")
	if err := f.Rotate(ctx); err != nil {
		t.Fatal(err)
	}
	live, _ := store.Keys(ctx, token.Federation)
	if len(live) != 2 || live[0].Ref != "awskms:a" || live[1].Ref != "awskms:b" ||
		live[1].State != token.StateNext || live[1].Public.Algorithm != "PS256" {
		t.Fatalf("the added reference: %+v", live)
	}
	if keys, _ := f.FederationKeys(ctx); len(keys) != 2 {
		t.Errorf("the next key is not published: %+v", keys)
	}
	c.advance(token.MinPublished - time.Second)
	if err := f.Rotate(ctx); err != nil {
		t.Fatal(err)
	}
	if raw, _ := f.Sign(ctx, federationClaims(c.now())); kidOf(t, raw) != live[0].ID.String() {
		t.Error("the next key signs before it has been published for 48 hours")
	}
	c.advance(time.Second)
	if err := f.Rotate(ctx); err != nil {
		t.Fatal(err)
	}
	c.advance(token.SignerRefresh)
	raw, err := f.Sign(ctx, federationClaims(c.now()))
	if err != nil || kidOf(t, raw) != live[1].ID.String() {
		t.Errorf("after 48 hours: kid %s, %v", kidOf(t, raw), err)
	}
}

// TestUnitFederationKeyCache checks that the published keys are read once
// per SignerRefresh, and that a kid the cache lacks reloads it at most
// once a second.
func TestUnitFederationKeyCache(t *testing.T) {
	ctx := context.Background()
	store, c := newMemStore(), &clock{t: start}
	f := federation(store, c, signer.ES256)
	if _, err := f.FederationKeys(ctx); err != nil {
		t.Fatal(err)
	}
	c.advance(token.SignerRefresh)
	reads := store.reads
	for range 3 {
		if _, err := f.FederationKeys(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if store.reads != reads+1 {
		t.Errorf("%d reads for three answers within SignerRefresh, want 1", store.reads-reads)
	}
	keys, _ := f.FederationKeys(ctx)
	key, ok, err := f.FederationKey(ctx, keys[0].KeyID)
	if err != nil || !ok || !key.IsPublic() || key.KeyID != keys[0].KeyID || key.Use != "sig" {
		t.Errorf("FederationKey(active) = %+v, %v, %v", key, ok, err)
	}
	reads = store.reads
	c.advance(time.Second)
	for range 3 {
		if _, ok, err := f.FederationKey(ctx, "unknown"); ok || err != nil {
			t.Fatalf("FederationKey(unknown) = %v, %v", ok, err)
		}
	}
	if store.reads != reads+1 {
		t.Errorf("%d reloads for an unknown kid within a second, want 1", store.reads-reads)
	}
	c.advance(token.SignerRefresh)
	if _, err := f.FederationKeys(ctx); err != nil || store.reads != reads+2 {
		t.Errorf("after SignerRefresh: %d reads, %v", store.reads-reads, err)
	}
}

// TestUnitFederationStoreFailures checks that an unreadable store fails
// every read and write, naming the purpose.
func TestUnitFederationStoreFailures(t *testing.T) {
	ctx := context.Background()
	store, c := newMemStore(), &clock{t: start}
	f := federation(store, c, signer.RS256)
	store.err = errDown
	if err := f.Rotate(ctx); !errors.Is(err, errDown) || !strings.Contains(err.Error(), "federation") {
		t.Errorf("Rotate = %v", err)
	}
	if _, err := f.Sign(ctx, federationClaims(c.now())); !errors.Is(err, errDown) {
		t.Errorf("Sign = %v", err)
	}
	if _, err := f.FederationKeys(ctx); !errors.Is(err, errDown) {
		t.Errorf("FederationKeys = %v", err)
	}
	if _, _, err := f.FederationKey(ctx, "k"); !errors.Is(err, errDown) {
		t.Errorf("FederationKey = %v", err)
	}
	store.err = nil
	store.inert = true
	if _, err := f.FederationKeys(ctx); !errors.Is(err, token.ErrNoKey) {
		t.Errorf("FederationKeys on a store that applies nothing = %v, want ErrNoKey", err)
	}
	// Claims that do not marshal are not signed.
	store.inert = false
	if _, err := f.Sign(ctx, map[string]any{"bad": func() {}}); err == nil {
		t.Error("Sign of claims that do not marshal succeeded")
	}
}
