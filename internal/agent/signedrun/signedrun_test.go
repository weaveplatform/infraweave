package signedrun_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/weaveplatform/infraweave/internal/agent/signedrun"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

const (
	issuer  = "https://iw.example.com"
	run     = "01KRUN00000000000000000001"
	session = "session-token"
)

var now = time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)

// signing is one signing key of an installation.
type signing struct {
	kid string
	key *ecdsa.PrivateKey
}

func newSigning(t *testing.T, kid string) signing {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return signing{kid: kid, key: key}
}

// public is s's public key as the worker API answers it.
func (s signing) public(t *testing.T) wire.RunTokenKey {
	t.Helper()
	raw, err := json.Marshal(jose.JSONWebKey{Key: &s.key.PublicKey, KeyID: s.kid, Algorithm: "ES256", Use: "sig"})
	if err != nil {
		t.Fatal(err)
	}
	var k wire.RunTokenKey
	if err := json.Unmarshal(raw, &k); err != nil {
		t.Fatal(err)
	}
	return k
}

// claims are a run token's claims, as the server issues them.
type claims struct {
	jwt.Claims

	Kind string `json:"iw_kind,omitempty"` //nolint:tagliatelle // the claim's name on the wire
}

func runClaims() claims {
	return claims{
		Claims: jwt.Claims{
			Issuer: issuer, Subject: run, Audience: jwt.Audience{signedrun.Audience}, ID: "01KJTI00000000000000000001",
			IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now),
			Expiry: jwt.NewNumericDate(now.Add(24 * time.Hour)),
		},
		Kind: signedrun.Kind,
	}
}

// sign is c signed by s under alg.
func (s signing) sign(t *testing.T, c any) string {
	t.Helper()
	js, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: s.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader(jose.HeaderKey("kid"), s.kid))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jwt.Signed(js).Claims(c).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// keys is signedrun.Keys over an answer it may change, counting reads.
type keys struct {
	answer  wire.RunTokenKeys
	err     error
	reads   int
	session string
	ctx     context.Context //nolint:containedctx // the last read's, for its deadline
}

func (k *keys) RunTokenKeys(ctx context.Context, session string) (wire.RunTokenKeys, error) {
	k.reads++
	k.session, k.ctx = session, ctx
	return k.answer, k.err
}

func answer(t *testing.T, s ...signing) wire.RunTokenKeys {
	t.Helper()
	out := wire.RunTokenKeys{Issuer: issuer, Audience: signedrun.Audience}
	for _, k := range s {
		out.Keys = append(out.Keys, k.public(t))
	}
	return out
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func refused(t *testing.T, err error, reason string) {
	t.Helper()
	var e *signedrun.Error
	if !errors.As(err, &e) || !errors.Is(err, signedrun.ErrRefused) || e.Reason != reason {
		t.Errorf("Verify = %v, want refused: %s", err, reason)
	}
}

// A genuine run token is admitted with the trust that admitted it; one
// whose signature, claims, issuer, audience, kind, run or times fail is
// refused with a fixed reason that names nothing of it.
func TestUnitVerifyClaims(t *testing.T) {
	ctx := t.Context()
	active, other := newSigning(t, "active"), newSigning(t, "other")
	src := &keys{answer: answer(t, active)}
	c := &clock{t: now.Add(time.Minute)}
	v := signedrun.New(src, c.now)
	raw := active.sign(t, runClaims())
	trust, err := v.Verify(ctx, session, raw, run)
	if err != nil || trust.Issuer != issuer || trust.Audience != signedrun.Audience || src.session != session {
		t.Fatalf("Verify = %+v, %v", trust, err)
	}
	var set jose.JSONWebKeySet
	if err := json.Unmarshal([]byte(trust.JWKS), &set); err != nil || len(set.Keys) != 1 ||
		set.Keys[0].KeyID != "active" || !set.Keys[0].IsPublic() || strings.Contains(trust.JWKS, `"d"`) {
		t.Errorf("JWKS = %s, %v", trust.JWKS, err)
	}
	if deadline, ok := src.ctx.Deadline(); !ok || deadline.After(time.Now().Add(signedrun.FetchTimeout)) {
		t.Errorf("read deadline = %v, %v", deadline, ok)
	}

	mutate := func(f func(*claims)) string {
		c := runClaims()
		f(&c)
		return active.sign(t, c)
	}
	parts := strings.Split(raw, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(payload), run, "01KRUN00000000000000000002", 1)
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	sig[10] ^= 1
	hs, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: []byte("0123456789abcdef0123456789abcdef")},
		(&jose.SignerOptions{}).WithHeader(jose.HeaderKey("kid"), "active"))
	if err != nil {
		t.Fatal(err)
	}
	hmac, err := jwt.Signed(hs).Claims(runClaims()).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		raw, reason string
	}{
		"garbage":            {"not.a.jwt", "the run token is not an ES256 JWT"},
		"HMAC":               {hmac, "the run token is not an ES256 JWT"},
		"tampered claims":    {parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(tampered)) + "." + parts[2], "the run token's signature does not verify"},
		"tampered signature": {parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(sig), "the run token's signature does not verify"},
		"unrelated key":      {other.sign(t, runClaims()), "the run token names no key the server verifies with"},
		"key ID reused":      {signing{kid: "active", key: other.key}.sign(t, runClaims()), "the run token's signature does not verify"},
		"issuer":             {mutate(func(c *claims) { c.Issuer = "https://other.example.com" }), "the run token's issuer is not the server's"},
		"audience":           {mutate(func(c *claims) { c.Audience = jwt.Audience{"https://iw.example.com/api/v1"} }), "the run token's audience is not the runner's"},
		"two audiences":      {mutate(func(c *claims) { c.Audience = append(c.Audience, "x") }), "the run token's audience is not the runner's"},
		"worker kind":        {mutate(func(c *claims) { c.Kind = "worker" }), "the token is not a run token"},
		"another run":        {mutate(func(c *claims) { c.Subject = "01KRUN00000000000000000002" }), "the run token names another run"},
		"no jti":             {mutate(func(c *claims) { c.ID = "" }), "the run token lacks its ID or time claims"},
		"no exp":             {mutate(func(c *claims) { c.Expiry = nil }), "the run token lacks its ID or time claims"},
		"expired": {mutate(func(c *claims) {
			c.IssuedAt, c.NotBefore = jwt.NewNumericDate(now.Add(-25*time.Hour)), jwt.NewNumericDate(now.Add(-25*time.Hour))
			c.Expiry = jwt.NewNumericDate(now.Add(-time.Hour))
		}), "the run token has expired"},
		"early": {mutate(func(c *claims) {
			c.IssuedAt, c.NotBefore = jwt.NewNumericDate(now.Add(time.Hour)), jwt.NewNumericDate(now.Add(time.Hour))
		}), "the run token is not yet valid"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := v.Verify(ctx, session, tc.raw, run)
			refused(t, err, tc.reason)
			if err != nil && strings.Contains(err.Error(), tc.raw) {
				t.Errorf("the refusal names the token: %v", err)
			}
		})
	}
	// Leeway admits 30 seconds of skew either way.
	c.t = now.Add(24*time.Hour + 29*time.Second)
	if _, err := v.Verify(ctx, session, raw, run); err != nil {
		t.Errorf("within the leeway after exp = %v", err)
	}
	c.t = now.Add(24*time.Hour + 31*time.Second)
	_, err = v.Verify(ctx, session, raw, run)
	refused(t, err, "the run token has expired")
}

// Keys are read once for CacheFor; a token naming a kid the cache lacks
// reads them once more, so a rotated key is found and an unrelated one
// refused after a single read.
func TestUnitVerifyKeyCache(t *testing.T) {
	ctx := t.Context()
	old, next := newSigning(t, "old"), newSigning(t, "next")
	src := &keys{answer: answer(t, old)}
	c := &clock{t: now}
	v := signedrun.New(src, c.now)
	for range 3 {
		if _, err := v.Verify(ctx, session, old.sign(t, runClaims()), run); err != nil {
			t.Fatal(err)
		}
	}
	if src.reads != 1 {
		t.Errorf("%d reads within CacheFor, want 1", src.reads)
	}
	// The rotation publishes and promotes next: its first token reads the
	// keys again and is admitted, and the trust names both keys.
	src.answer = answer(t, old, next)
	trust, err := v.Verify(ctx, session, next.sign(t, runClaims()), run)
	if err != nil || src.reads != 2 || !strings.Contains(trust.JWKS, `"kid":"old"`) ||
		!strings.Contains(trust.JWKS, `"kid":"next"`) {
		t.Fatalf("rotated = %v after %d reads, %s", err, src.reads, trust.JWKS)
	}
	// An unrelated key reads once and is refused.
	_, err = v.Verify(ctx, session, newSigning(t, "stranger").sign(t, runClaims()), run)
	refused(t, err, "the run token names no key the server verifies with")
	if src.reads != 3 {
		t.Errorf("%d reads after an unknown kid, want 3", src.reads)
	}
	// Past CacheFor the keys are read again, and a retired key's token is
	// then refused.
	src.answer = answer(t, next)
	c.t = now.Add(signedrun.CacheFor)
	_, err = v.Verify(ctx, session, old.sign(t, runClaims()), run)
	refused(t, err, "the run token names no key the server verifies with")
	// A clock that moved back reads again rather than trusting the cache.
	c.t = now.Add(-time.Second)
	before := src.reads
	if _, err := v.Verify(ctx, session, next.sign(t, runClaims()), run); err != nil || src.reads != before+1 {
		t.Errorf("clock moved back = %v, %d reads", err, src.reads-before)
	}
}

// Keys that cannot be read, or that are no ES256 public keys of the run
// tokens' audience, fail admission without refusing the token, and a
// failed read keeps the cache.
func TestUnitVerifyKeysFail(t *testing.T) {
	ctx := t.Context()
	k := newSigning(t, "k")
	raw := k.sign(t, runClaims())
	down := errors.New("worker API down")
	src := &keys{err: down}
	v := signedrun.New(src, func() time.Time { return now })
	_, err := v.Verify(ctx, session, raw, run)
	var e *signedrun.Error
	if !errors.As(err, &e) || !errors.Is(err, signedrun.ErrFailed) || !errors.Is(err, down) ||
		e.Error() != "signed run admission failed: the server's run token keys could not be read" {
		t.Errorf("Verify = %v", err)
	}
	good := k.public(t)
	bad := func(f func(*wire.RunTokenKey)) wire.RunTokenKey {
		out := good
		f(&out)
		return out
	}
	for name, a := range map[string]wire.RunTokenKeys{
		"no issuer":        {Audience: signedrun.Audience, Keys: []wire.RunTokenKey{good}},
		"another audience": {Issuer: issuer, Audience: "infraweave-worker", Keys: []wire.RunTokenKey{good}},
		"too many":         {Issuer: issuer, Audience: signedrun.Audience, Keys: make([]wire.RunTokenKey, 17)},
		"duplicate":        {Issuer: issuer, Audience: signedrun.Audience, Keys: []wire.RunTokenKey{good, good}},
		"no kid":           {Issuer: issuer, Audience: signedrun.Audience, Keys: []wire.RunTokenKey{bad(func(k *wire.RunTokenKey) { k.Kid = "" })}},
		"off the curve":    {Issuer: issuer, Audience: signedrun.Audience, Keys: []wire.RunTokenKey{bad(func(k *wire.RunTokenKey) { k.X = good.Y })}},
		"another alg":      {Issuer: issuer, Audience: signedrun.Audience, Keys: []wire.RunTokenKey{bad(func(k *wire.RunTokenKey) { k.Alg = "ES384" })}},
		"another use":      {Issuer: issuer, Audience: signedrun.Audience, Keys: []wire.RunTokenKey{bad(func(k *wire.RunTokenKey) { k.Use = "enc" })}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := signedrun.New(&keys{answer: a}, func() time.Time { return now }).Verify(ctx, session, raw, run)
			if !errors.Is(err, signedrun.ErrFailed) ||
				err.Error() != "signed run admission failed: the server's run token keys are invalid" {
				t.Errorf("Verify = %v", err)
			}
		})
	}
	// A failed refresh for an unknown kid keeps the earlier keys.
	src = &keys{answer: answer(t, k)}
	v = signedrun.New(src, func() time.Time { return now })
	if _, err := v.Verify(ctx, session, raw, run); err != nil {
		t.Fatal(err)
	}
	src.err = down
	if _, err := v.Verify(ctx, session, newSigning(t, "new").sign(t, runClaims()), run); !errors.Is(err, signedrun.ErrFailed) {
		t.Errorf("refresh down = %v", err)
	}
	if _, err := v.Verify(ctx, session, raw, run); err != nil {
		t.Errorf("cached key after a failed refresh = %v", err)
	}
}
