package oidc

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
)

// TestUnitDiscover reads the endpoints at the issuer and at its discovery
// URL, keeps them for CacheFor, and refuses another issuer, an endpoint
// that is not HTTPS and an unreachable document.
func TestUnitDiscover(t *testing.T) {
	i := newIDP(t)
	now := t0
	v := newVerifier(t, i, &now)
	ctx := t.Context()
	want := sso.Endpoints{
		Issuer: i.srv.URL, Authorization: i.srv.URL + "/authorize", Token: i.srv.URL + "/token",
		EndSession: i.srv.URL + "/logout",
	}
	for _, host := range []string{i.srv.URL, i.srv.URL + discoveryPath} {
		if ep, err := v.Discover(ctx, host); err != nil || ep != want {
			t.Errorf("Discover(%s) = %+v, %v", host, ep, err)
		}
	}
	if _, err := v.Discover(ctx, i.srv.URL); err != nil || i.discoveries.Load() != 2 {
		t.Errorf("a cached read fetched again: %d, %v", i.discoveries.Load(), err)
	}
	now = now.Add(CacheFor)
	if _, err := v.Discover(ctx, i.srv.URL); err != nil || i.discoveries.Load() != 3 {
		t.Errorf("a stale read did not fetch: %d, %v", i.discoveries.Load(), err)
	}
	i.doc = map[string]string{"end_session_endpoint": ""}
	now = now.Add(CacheFor)
	if ep, err := v.Discover(ctx, i.srv.URL); err != nil || ep.EndSession != "" {
		t.Errorf("no end_session_endpoint: %+v, %v", ep, err)
	}
	for name, doc := range map[string]map[string]string{
		"another issuer":         {"issuer": "https://evil.test"},
		"plain authorization":    {"authorization_endpoint": "http://idp.test/authorize"},
		"no token endpoint":      {"token_endpoint": ""},
		"plain end session":      {"end_session_endpoint": "http://idp.test/logout"},
		"relative authorization": {"authorization_endpoint": "/authorize"},
	} {
		i.doc = doc
		now = now.Add(CacheFor)
		if ep, err := v.Discover(ctx, i.srv.URL); !errors.Is(err, ErrIssuer) {
			t.Errorf("%s: %+v, %v", name, ep, err)
		}
	}
	if _, err := v.Discover(ctx, i.srv.URL+"/missing"); !errors.Is(err, ErrIssuer) {
		t.Errorf("no document: %v", err)
	}
}

// TestUnitExchange redeems a code with its PKCE verifier and the client's
// credentials and keeps only the ID token; an error or an answer without
// one is refused.
func TestUnitExchange(t *testing.T) {
	i := newIDP(t)
	now := t0
	v := newVerifier(t, i, &now)
	ep, err := v.Discover(t.Context(), i.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	var form map[string]string
	answer := map[string]any{"access_token": "at", "token_type": "Bearer", "refresh_token": "rt", "id_token": "the-id-token"}
	i.token = func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id, secret, _ := r.BasicAuth()
		form = map[string]string{
			"grant_type": r.PostForm.Get("grant_type"), "code": r.PostForm.Get("code"),
			"code_verifier": r.PostForm.Get("code_verifier"), "redirect_uri": r.PostForm.Get("redirect_uri"),
			"client": id + ":" + secret,
		}
		w.Header().Set("Content-Type", "application/json")
		if answer == nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(answer)
	}
	c := sso.Client{ID: "infraweave", Secret: "s3cret", RedirectURI: "https://iw.test/auth/oidc/callback"}
	raw, err := v.Exchange(t.Context(), ep, c, "code-1", "verifier-1")
	if err != nil || raw != "the-id-token" {
		t.Fatalf("Exchange = %q, %v", raw, err)
	}
	for k, want := range map[string]string{
		"grant_type": "authorization_code", "code": "code-1", "code_verifier": "verifier-1",
		"redirect_uri": c.RedirectURI, "client": "infraweave:s3cret",
	} {
		if form[k] != want {
			t.Errorf("%s = %q, want %q", k, form[k], want)
		}
	}
	delete(answer, "id_token")
	if _, err := v.Exchange(t.Context(), ep, c, "code-1", "verifier-1"); !errors.Is(err, ErrIssuer) {
		t.Errorf("no id_token: %v", err)
	}
	answer = nil
	if _, err := v.Exchange(t.Context(), ep, c, "code-1", "verifier-1"); !errors.Is(err, ErrIssuer) {
		t.Errorf("invalid_grant: %v", err)
	}
}

// TestUnitVerifyIDToken checks an ID token through go-oidc over the key
// cache, and the times within Leeway.
func TestUnitVerifyIDToken(t *testing.T) {
	i := newIDP(t)
	now := t0
	v := newVerifier(t, i, &now)
	ctx := t.Context()
	good := i.mint(t, jose.ES256, "e1", i.claims(map[string]any{"nonce": "n"}))
	c, err := v.VerifyIDToken(ctx, i.srv.URL, "infraweave", good)
	if err != nil || c["sub"] != "repo:main" || c["nonce"] != "n" {
		t.Fatalf("VerifyIDToken = %v, %v", c, err)
	}
	if _, err := v.VerifyIDToken(ctx, i.srv.URL, "infraweave", i.mint(t, jose.RS256, "", i.claims(nil))); err != nil {
		t.Errorf("no kid: %v", err)
	}
	for name, raw := range map[string]string{
		"wrong iss":     i.mint(t, jose.RS256, "r1", i.claims(map[string]any{"iss": "https://evil.test"})),
		"wrong aud":     i.mint(t, jose.RS256, "r1", i.claims(map[string]any{"aud": "other"})),
		"expired":       i.mint(t, jose.RS256, "r1", i.claims(map[string]any{"exp": t0.Add(-2 * time.Minute).Unix()})),
		"not yet valid": i.mint(t, jose.RS256, "r1", i.claims(map[string]any{"nbf": t0.Add(2 * time.Minute).Unix()})),
		"no exp":        i.mint(t, jose.RS256, "r1", i.claims(map[string]any{"exp": nil})),
		"another kid":   i.mint(t, jose.RS256, "e1", i.claims(nil)),
		"not a JWT":     "a.b.c",
	} {
		if _, err := v.VerifyIDToken(ctx, i.srv.URL, "infraweave", raw); !errors.Is(err, ErrToken) {
			t.Errorf("%s: %v, want ErrToken", name, err)
		}
	}
	now = t0.Add(time.Hour + 30*time.Second)
	if _, err := v.VerifyIDToken(ctx, i.srv.URL, "infraweave", good); err != nil {
		t.Errorf("within the leeway: %v", err)
	}
	// A key set that cannot be read refuses the token.
	if _, err := v.VerifyIDToken(ctx, "https://127.0.0.1:1", "infraweave", good); !errors.Is(err, ErrToken) {
		t.Errorf("an unreachable key set: %v", err)
	}
}

// TestUnitVerifyLogout checks a back-channel logout token and its own
// rules: the event, no nonce, a jti, and its expiry within Leeway.
func TestUnitVerifyLogout(t *testing.T) {
	i := newIDP(t)
	now := t0
	v := newVerifier(t, i, &now)
	ctx := t.Context()
	event := map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}}
	logout := func(over map[string]any) string {
		c := i.claims(map[string]any{"events": event, "jti": "j-1", "sid": "s-1", "nbf": nil})
		for k, val := range over {
			if val == nil {
				delete(c, k)
			} else {
				c[k] = val
			}
		}
		return i.mint(t, jose.RS256, "r1", c)
	}
	tok, err := v.VerifyLogout(ctx, i.srv.URL, "infraweave", logout(nil))
	if err != nil || tok != (sso.LogoutToken{ID: "j-1", Subject: "repo:main", SID: "s-1", Expiry: time.Unix(t0.Add(time.Hour).Unix(), 0)}) {
		t.Fatalf("VerifyLogout = %+v, %v", tok, err)
	}
	for name, raw := range map[string]string{
		"no event":  logout(map[string]any{"events": nil}),
		"a nonce":   logout(map[string]any{"nonce": "n"}),
		"no jti":    logout(map[string]any{"jti": nil}),
		"expired":   logout(map[string]any{"exp": t0.Add(-2 * time.Minute).Unix()}),
		"wrong aud": logout(map[string]any{"aud": "other"}),
	} {
		if _, err := v.VerifyLogout(ctx, i.srv.URL, "infraweave", raw); !errors.Is(err, ErrToken) {
			t.Errorf("%s: %v, want ErrToken", name, err)
		}
	}
}
