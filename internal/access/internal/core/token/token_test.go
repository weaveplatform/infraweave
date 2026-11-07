package token_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
)

const (
	issuer   = "https://iw.example.com"
	audience = "https://iw.example.com/api/v1"
)

var start = time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)

func setup(t *testing.T) (*token.Service, *memStore, *clock) {
	t.Helper()
	store, c := newMemStore(), &clock{t: start}
	return token.New(store, issuer, token.Policy{TokenLife: 10 * time.Hour}, c.now), store, c
}

func request() token.Request {
	return token.Request{
		Kind:     token.KindAPIKey,
		Subject:  "api::" + kernel.NewULID().String(),
		Session:  kernel.NewULID(),
		Audience: audience,
		TTL:      10 * time.Hour,
	}
}

// segment decodes part i of a compact JWS as JSON.
func segment(t *testing.T, raw string, i int) map[string]any {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(strings.Split(raw, ".")[i])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// forge signs claims with the private half of the key kid, or with key
// when it is set, under alg.
func forge(
	t *testing.T,
	s *memStore,
	kid string,
	alg jose.SignatureAlgorithm,
	key any,
	claims any,
) string {
	t.Helper()
	if key == nil {
		id, err := kernel.ParseULID(kid)
		if err != nil {
			t.Fatal(err)
		}
		der, err := s.signers[id].PrivateKey()
		if err != nil {
			t.Fatal(err)
		}
		k, err := x509.ParsePKCS8PrivateKey(der)
		if err != nil {
			t.Fatal(err)
		}
		key = k.(*ecdsa.PrivateKey)
	}
	js, err := jose.NewSigner(jose.SigningKey{Algorithm: alg, Key: key},
		(&jose.SignerOptions{}).WithHeader(jose.HeaderKey("kid"), kid))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jwt.Signed(js).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestUnitTokenClaims(t *testing.T) {
	svc, store, c := setup(t)
	ctx := context.Background()
	r := request()
	raw, issued, err := svc.Issue(ctx, r)
	if err != nil {
		t.Fatal(err)
	}

	head := segment(t, raw, 0)
	kid, _ := head["kid"].(string)
	if head["alg"] != "ES256" || head["typ"] != "JWT" || store.state(kid) != token.StateActive {
		t.Errorf("header = %v", head)
	}
	body := segment(t, raw, 1)
	want := []string{"aud", "exp", "iat", "iss", "iw_kind", "jti", "nbf", "sub"}
	if got := slices.Sorted(maps.Keys(body)); !slices.Equal(got, want) {
		t.Errorf("claims = %v, want exactly %v", got, want)
	}
	iat := float64(start.Unix())
	if body["aud"] != audience || body["iss"] != issuer || body["sub"] != r.Subject ||
		body["jti"] != r.Session.String() || body["iw_kind"] != "api_key" ||
		body["iat"] != iat || body["nbf"] != iat || body["exp"] != iat+36000 {
		t.Errorf("claims = %v", body)
	}

	got, err := svc.Verify(ctx, raw, audience)
	if err != nil || got != issued {
		t.Fatalf("Verify = %+v, %v; want %+v", got, err, issued)
	}
	r.Kind, r.Scope = token.KindOAuth, "mcp:read mcp:write"
	raw, issued, err = svc.Issue(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if body := segment(t, raw, 1); body["scope"] != r.Scope || body["iw_kind"] != "oauth" {
		t.Errorf("an OAuth token's claims = %v", body)
	}
	if got, err := svc.Verify(ctx, raw, audience); err != nil || got != issued ||
		got.Scope != r.Scope {
		t.Errorf("Verify an OAuth token = %+v, %v; want %+v", got, err, issued)
	}

	valid := func() jwt.Claims {
		now := jwt.NewNumericDate(c.now())
		return jwt.Claims{
			Issuer: issuer, Subject: "api::k", Audience: jwt.Audience{audience},
			ID: kernel.NewULID().String(), IssuedAt: now, NotBefore: now,
			Expiry: jwt.NewNumericDate(c.now().Add(time.Hour)),
		}
	}
	type claims struct {
		jwt.Claims

		Kind string `json:"iw_kind"` //nolint:tagliatelle // decision 0006's claim name
	}
	with := func(f func(*claims)) claims {
		cl := claims{Claims: valid(), Kind: "api_key"}
		f(&cl)
		return cl
	}
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"x"}`))
	refused := map[string]string{
		"garbage": "not.a.token",
		"alg none": base64.RawURLEncoding.EncodeToString(
			[]byte(`{"alg":"none","kid":"`+kid+`"}`),
		) + "." + payload + ".",
		"HS256": forge(
			t,
			store,
			kid,
			jose.HS256,
			[]byte(strings.Repeat("k", 32)),
			with(func(*claims) {}),
		),
		"unknown kid": forge(
			t,
			store,
			kernel.NewULID().String(),
			jose.HS256,
			[]byte(strings.Repeat("k", 32)),
			with(func(*claims) {}),
		),
		"wrong audience": forge(
			t,
			store,
			kid,
			jose.ES256,
			nil,
			with(func(c *claims) { c.Audience = jwt.Audience{"https://other.example.com"} }),
		),
		"two audiences": forge(
			t,
			store,
			kid,
			jose.ES256,
			nil,
			with(func(c *claims) { c.Audience = jwt.Audience{audience, "https://other"} }),
		),
		"wrong issuer": forge(
			t,
			store,
			kid,
			jose.ES256,
			nil,
			with(func(c *claims) { c.Issuer = "https://evil.example.com" }),
		),
		"no exp": forge(
			t,
			store,
			kid,
			jose.ES256,
			nil,
			with(func(c *claims) { c.Expiry = nil }),
		),
		"no nbf": forge(
			t,
			store,
			kid,
			jose.ES256,
			nil,
			with(func(c *claims) { c.NotBefore = nil }),
		),
		"bad jti": forge(
			t,
			store,
			kid,
			jose.ES256,
			nil,
			with(func(c *claims) { c.ID = "session" }),
		),
		"no sub": forge(
			t,
			store,
			kid,
			jose.ES256,
			nil,
			with(func(c *claims) { c.Subject = "" }),
		),
		"unknown kind": forge(
			t,
			store,
			kid,
			jose.ES256,
			nil,
			with(func(c *claims) { c.Kind = "admin" }),
		),
	}
	// A signature by another key under this key's kid.
	other, _, err := token.New(newMemStore(), issuer, token.Policy{}, c.now).Issue(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	parts, mine := strings.Split(other, "."), strings.Split(raw, ".")
	refused["foreign signature"] = mine[0] + "." + mine[1] + "." + parts[2]
	for name, raw := range refused {
		if _, err := svc.Verify(ctx, raw, audience); !errors.Is(err, token.ErrToken) {
			t.Errorf("%s: Verify = %v, want ErrToken", name, err)
		}
	}

	// exp and iat allow 30 seconds of skew, and no more.
	c.advance(10*time.Hour + 29*time.Second)
	if _, err := svc.Verify(ctx, raw, audience); err != nil {
		t.Errorf("29s past exp: %v", err)
	}
	c.advance(2 * time.Second)
	if _, err := svc.Verify(ctx, raw, audience); !errors.Is(err, token.ErrToken) {
		t.Errorf("31s past exp: Verify = %v, want ErrToken", err)
	}
	ahead := func(d time.Duration) string {
		return forge(t, store, kid, jose.ES256, nil, with(func(cl *claims) {
			at := jwt.NewNumericDate(c.now().Add(d))
			cl.IssuedAt, cl.NotBefore = at, at
		}))
	}
	if _, err := svc.Verify(ctx, ahead(29*time.Second), audience); err != nil {
		t.Errorf("issued 29s ahead: %v", err)
	}
	if _, err := svc.Verify(ctx, ahead(31*time.Second), audience); !errors.Is(err, token.ErrToken) {
		t.Errorf("issued 31s ahead: Verify = %v, want ErrToken", err)
	}
}

func TestUnitIssueRequest(t *testing.T) {
	svc, _, _ := setup(t)
	for name, f := range map[string]func(*token.Request){
		"kind":     func(r *token.Request) { r.Kind = "break_glass" },
		"subject":  func(r *token.Request) { r.Subject = "" },
		"session":  func(r *token.Request) { r.Session = kernel.ULID{} },
		"audience": func(r *token.Request) { r.Audience = "" },
		"ttl":      func(r *token.Request) { r.TTL = 0 },
	} {
		r := request()
		f(&r)
		if _, _, err := svc.Issue(context.Background(), r); !errors.Is(err, token.ErrRequest) {
			t.Errorf("%s: Issue = %v, want ErrRequest", name, err)
		}
	}
}

func TestUnitTokenStoreFailures(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := setup(t)
	store.err = errDown
	if _, _, err := svc.Issue(ctx, request()); !errors.Is(err, errDown) {
		t.Errorf("Issue = %v, want the store's error", err)
	}
	if err := svc.Rotate(ctx); !errors.Is(err, errDown) {
		t.Errorf("Rotate = %v, want the store's error", err)
	}
	// An unreadable store is not a bad token.
	raw := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","kid":"k"}`)) + ".e30.c2ln"
	if _, err := svc.Verify(
		ctx,
		raw,
		audience,
	); !errors.Is(err, errDown) ||
		errors.Is(err, token.ErrToken) {
		t.Errorf("Verify = %v, want the store's error and not ErrToken", err)
	}

	// A store that applies no step leaves no key to sign with.
	svc, store, _ = setup(t)
	store.inert = true
	if _, _, err := svc.Issue(ctx, request()); !errors.Is(err, token.ErrNoKey) {
		t.Errorf("Issue = %v, want ErrNoKey", err)
	}

	// The key list reads but the private half does not open.
	svc, store, _ = setup(t)
	if err := svc.Rotate(ctx); err != nil {
		t.Fatal(err)
	}
	failing := &failingSigner{memStore: store}
	if _, _, err := token.New(failing, issuer, token.Policy{}, time.Now).
		Issue(ctx, request()); !errors.Is(
		err,
		errDown,
	) {
		t.Errorf("Issue = %v, want the signer's error", err)
	}
}

type failingSigner struct{ *memStore }

func (f *failingSigner) Signer(context.Context, kernel.ULID) (signer.Signer, error) {
	return nil, errDown
}

// An unknown kid reloads the keys at most once a second.
func TestUnitUnknownKidReload(t *testing.T) {
	ctx := context.Background()
	svc, store, c := setup(t)
	stranger, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	forged := forge(t, store, kernel.NewULID().String(), jose.ES256, stranger, jwt.Claims{})
	for range 3 {
		if _, err := svc.Verify(ctx, forged, audience); !errors.Is(err, token.ErrToken) {
			t.Fatal(err)
		}
	}
	if store.reads != 1 {
		t.Errorf("%d reloads within a second, want 1", store.reads)
	}
	c.advance(time.Second)
	if _, err := svc.Verify(ctx, forged, audience); !errors.Is(err, token.ErrToken) {
		t.Fatal(err)
	}
	if store.reads != 2 {
		t.Errorf("%d reloads after a second, want 2", store.reads)
	}
}
