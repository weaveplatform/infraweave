package oidc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

var t0 = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

// idp is an OIDC issuer over TLS with an RS256 and an ES256 key. Its
// discovery document and JWKS can be changed to fail, and it counts the
// JWKS fetches.
type idp struct {
	srv     *httptest.Server
	rsa     *rsa.PrivateKey
	ec      *ecdsa.PrivateKey
	fetches atomic.Int32
	// issuer, jwksURI and jwks override the published documents when set.
	issuer, jwksURI string
	jwks            func(w http.ResponseWriter)
	// kids are the key IDs the JWKS publishes, rsa's then ec's.
	kids [2]string
}

func newIDP(t *testing.T) *idp {
	t.Helper()
	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ek, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	i := &idp{rsa: rk, ec: ek, kids: [2]string{"r1", "e1"}}
	r := chi.NewRouter()
	r.Get("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		iss, jwks := i.srv.URL, i.srv.URL+"/jwks"
		if i.issuer != "" {
			iss = i.issuer
		}
		if i.jwksURI != "" {
			jwks = i.jwksURI
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": iss, "jwks_uri": jwks})
	})
	r.Get("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		i.fetches.Add(1)
		if i.jwks != nil {
			i.jwks(w)
			return
		}
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &rk.PublicKey, KeyID: i.kids[0], Algorithm: string(jose.RS256), Use: "sig"},
			{Key: &ek.PublicKey, KeyID: i.kids[1], Algorithm: string(jose.ES256), Use: "sig"},
		}})
	})
	r.Get("/moved", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+r.Host+"/jwks", http.StatusFound)
	})
	i.srv = httptest.NewTLSServer(r)
	t.Cleanup(i.srv.Close)
	return i
}

// roots is the PEM of the issuer's certificate.
func (i *idp) roots() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: i.srv.Certificate().Raw})
}

// mint signs claims with the issuer's key of alg, under kid.
func (i *idp) mint(
	t *testing.T,
	alg jose.SignatureAlgorithm,
	kid string,
	claims map[string]any,
) string {
	t.Helper()
	var key any = i.rsa
	if alg == jose.ES256 {
		key = i.ec
	}
	opts := (&jose.SignerOptions{}).WithType("JWT")
	if kid != "" {
		opts = opts.WithHeader(jose.HeaderKey("kid"), kid)
	}
	s, err := jose.NewSigner(jose.SigningKey{Algorithm: alg, Key: key}, opts)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jwt.Signed(s).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// claims are good claims for the issuer, with over replacing them; a nil
// value removes the claim.
func (i *idp) claims(over map[string]any) map[string]any {
	c := map[string]any{
		"iss": i.srv.URL, "aud": []string{"infraweave", "other"}, "sub": "repo:main",
		"exp": t0.Add(time.Hour).Unix(), "nbf": t0.Add(-time.Minute).Unix(), "iat": t0.Unix(),
	}
	for k, v := range over {
		if v == nil {
			delete(c, k)
		} else {
			c[k] = v
		}
	}
	return c
}

func newVerifier(t *testing.T, i *idp, now *time.Time) *Verifier {
	t.Helper()
	v, err := New(Options{RootCAs: i.roots(), Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestUnitVerify checks a good token of each algorithm, and refuses a
// wrong iss, a wrong aud, an expired token, one not yet valid, one with
// no exp, alg none, HS256 and a signature of another key.
func TestUnitVerify(t *testing.T) {
	i := newIDP(t)
	now := t0
	v := newVerifier(t, i, &now)
	ctx := t.Context()
	for _, alg := range []jose.SignatureAlgorithm{jose.RS256, jose.ES256} {
		kid := i.kids[0]
		if alg == jose.ES256 {
			kid = i.kids[1]
		}
		c, err := v.Verify(ctx, i.srv.URL, "infraweave", i.mint(t, alg, kid, i.claims(nil)))
		if err != nil || c["sub"] != "repo:main" {
			t.Errorf("%s: %v, %v", alg, c, err)
		}
	}
	// No kid tries every key.
	if _, err := v.Verify(
		ctx,
		i.srv.URL,
		"infraweave",
		i.mint(t, jose.ES256, "", i.claims(nil)),
	); err != nil {
		t.Errorf("no kid: %v", err)
	}
	hs, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.HS256, Key: []byte(strings.Repeat("k", 32))},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	hsRaw, err := jwt.Signed(hs).Claims(i.claims(nil)).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.RawURLEncoding.EncodeToString
	payload, _ := json.Marshal(i.claims(nil))
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	forged, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: other},
		(&jose.SignerOptions{}).WithHeader(jose.HeaderKey("kid"), i.kids[0]))
	forgedRaw, _ := jwt.Signed(forged).Claims(i.claims(nil)).Serialize()
	for name, raw := range map[string]string{
		"wrong iss":     i.mint(t, jose.RS256, "r1", i.claims(map[string]any{"iss": "https://evil.test"})),
		"wrong aud":     i.mint(t, jose.RS256, "r1", i.claims(map[string]any{"aud": "other"})),
		"expired":       i.mint(t, jose.RS256, "r1", i.claims(map[string]any{"exp": t0.Add(-2 * time.Minute).Unix()})),
		"not yet valid": i.mint(t, jose.RS256, "r1", i.claims(map[string]any{"nbf": t0.Add(2 * time.Minute).Unix()})),
		"no exp":        i.mint(t, jose.RS256, "r1", i.claims(map[string]any{"exp": nil})),
		"alg none":      b64([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + b64(payload) + ".",
		"HS256":         hsRaw,
		"another key":   forgedRaw,
		"not a JWT":     "nope",
	} {
		if _, err := v.Verify(ctx, i.srv.URL, "infraweave", raw); !errors.Is(err, ErrToken) {
			t.Errorf("%s: %v, want ErrToken", name, err)
		}
	}
	// Within the leeway, an expired token still verifies.
	now = t0.Add(time.Hour + 30*time.Second)
	if _, err := v.Verify(
		ctx,
		i.srv.URL,
		"infraweave",
		i.mint(t, jose.RS256, "r1", i.claims(nil)),
	); err != nil {
		t.Errorf("30 seconds past exp: %v", err)
	}
}

// TestUnitJWKSCache checks that the JWKS is fetched once for many tokens,
// again after an hour, and for an unknown kid at most once a minute.
func TestUnitJWKSCache(t *testing.T) {
	i := newIDP(t)
	now := t0
	v := newVerifier(t, i, &now)
	ctx := t.Context()
	verify := func(kid string) error {
		_, err := v.Verify(
			ctx,
			i.srv.URL,
			"infraweave",
			i.mint(t, jose.RS256, kid, i.claims(map[string]any{
				"exp": now.Add(time.Hour).Unix(), "nbf": nil, "iat": nil,
			})),
		)
		return err
	}
	for range 3 {
		if err := verify("r1"); err != nil {
			t.Fatal(err)
		}
	}
	if n := i.fetches.Load(); n != 1 {
		t.Fatalf("%d fetches for three tokens, want 1", n)
	}
	// An unknown kid refetches, then not again within the minute.
	for range 3 {
		if err := verify("r2"); !errors.Is(err, ErrToken) {
			t.Errorf("unknown kid: %v", err)
		}
	}
	if n := i.fetches.Load(); n != 1 {
		t.Errorf("%d fetches, want the first only: the last was under a minute ago", n)
	}
	now = now.Add(RefetchEvery)
	i.kids[0] = "r2" // the issuer rotated its key
	if err := verify("r2"); err != nil {
		t.Errorf("the rotated kid after a minute: %v", err)
	}
	if n := i.fetches.Load(); n != 2 {
		t.Errorf("%d fetches, want 2", n)
	}
	now = now.Add(CacheFor)
	if err := verify("r2"); err != nil || i.fetches.Load() != 3 {
		t.Errorf("after an hour: %v, %d fetches", err, i.fetches.Load())
	}
}

// TestUnitIssuerRefused checks that an issuer whose documents cannot be
// fetched over HTTPS, are not its own, or are too large is ErrIssuer.
func TestUnitIssuerRefused(t *testing.T) {
	ctx := t.Context()
	for name, tc := range map[string]struct {
		edit   func(*idp)
		issuer func(*idp) string
	}{
		"plain HTTP":       {nil, func(i *idp) string { return strings.Replace(i.srv.URL, "https", "http", 1) }},
		"unreachable":      {nil, func(*idp) string { return "https://127.0.0.1:1" }},
		"another issuer":   {func(i *idp) { i.issuer = "https://evil.test" }, nil},
		"jwks over HTTP":   {func(i *idp) { i.jwksURI = strings.Replace(i.srv.URL, "https", "http", 1) + "/jwks" }, nil},
		"redirect to HTTP": {func(i *idp) { i.jwksURI = i.srv.URL + "/moved" }, nil},
		"jwks missing":     {func(i *idp) { i.jwksURI = i.srv.URL + "/nope" }, nil},
		"jwks not JSON": {func(i *idp) {
			i.jwks = func(w http.ResponseWriter) { _, _ = w.Write([]byte("<html>")) }
		}, nil},
		"jwks too large": {func(i *idp) {
			i.jwks = func(w http.ResponseWriter) {
				_, _ = w.Write([]byte(`{"keys":[],"pad":"` + strings.Repeat("x", MaxBody) + `"}`))
			}
		}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			i := newIDP(t)
			if tc.edit != nil {
				tc.edit(i)
			}
			iss := i.srv.URL
			if tc.issuer != nil {
				iss = tc.issuer(i)
			}
			now := t0
			v := newVerifier(t, i, &now)
			_, err := v.Verify(ctx, iss, "infraweave",
				i.mint(t, jose.RS256, "r1", i.claims(map[string]any{"iss": iss})))
			if !errors.Is(err, ErrIssuer) {
				t.Errorf("%v, want ErrIssuer", err)
			}
		})
	}
	if _, err := New(Options{RootCAs: []byte("junk")}); !errors.Is(err, ErrIssuer) {
		t.Errorf("junk roots: %v", err)
	}
	if v, err := New(Options{}); err != nil || v.now == nil {
		t.Errorf("no options: %v", err)
	}
}
