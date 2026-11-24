package deps

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

const (
	testClient   = "infraweave"
	testSecret   = "s3cret/with:colon"
	testCallback = "https://rp.example/auth/oidc/callback"
)

// issuer starts an issuer with the test client registered.
func issuer(t *testing.T) *Issuer {
	t.Helper()
	iss, err := StartIssuer()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(iss.Close)
	iss.RegisterClient(testClient, testSecret)
	return iss
}

func flow(iss *Issuer) *CodeFlow {
	return &CodeFlow{
		Issuer: iss.URL(), ClientID: testClient, Secret: testSecret, RedirectURI: testCallback,
		Client: iss.Client(),
	}
}

// signIn runs a code flow for id and answers the token endpoint's answer.
func signIn(t *testing.T, iss *Issuer, f *CodeFlow, id Identity) TokenAnswer {
	t.Helper()
	ctx := t.Context()
	iss.SignInNext(id)
	at, err := f.AuthorizeURL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	back, err := SignIn(ctx, iss.Client(), at, Login{})
	if err != nil {
		t.Fatal(err)
	}
	if got := back.Scheme + "://" + back.Host + back.Path; got != testCallback {
		t.Fatalf("the issuer redirected to %s, not the redirect URI", back)
	}
	answer, err := f.Exchange(ctx, back)
	if err != nil {
		t.Fatal(err)
	}
	return answer
}

func TestUnitIssuerCodeFlow(t *testing.T) {
	iss := issuer(t)
	f := flow(iss)
	f.Extra = url.Values{"max_age": {"0"}, "prompt": {"login"}}
	authAt := time.Unix(1_700_000_000, 0)
	answer := signIn(t, iss, f, Identity{
		Subject:  "alice",
		AuthTime: authAt,
		SID:      "session-1",
		Claims:   map[string]any{"email": "alice@example.com", "email_verified": true, "groups": []string{"ops", "dev"}},
	})
	if answer.Status != http.StatusOK || answer.IDToken == "" {
		t.Fatalf("exchange answered %+v", answer)
	}
	header, claims, err := f.Verify(t.Context(), answer.IDToken)
	if err != nil {
		t.Fatal(err)
	}
	if header["kid"] != oidcKeyID || header["alg"] != "RS256" {
		t.Errorf("header %v", header)
	}
	for k, want := range map[string]any{
		"iss": iss.URL(), "aud": testClient, "sub": "alice", "nonce": f.Nonce, "sid": "session-1",
		"email": "alice@example.com", "email_verified": true, "auth_time": float64(authAt.Unix()),
	} {
		if claims[k] != want {
			t.Errorf("%s = %v, want %v", k, claims[k], want)
		}
	}
	if g, ok := claims["groups"].([]any); !ok || len(g) != 2 || g[0] != "ops" {
		t.Errorf("groups = %v", claims["groups"])
	}
	reqs := iss.Requests()
	if len(reqs) != 1 || reqs[0].MaxAge != 0 || reqs[0].Prompt != "login" || reqs[0].Nonce != f.Nonce ||
		reqs[0].CodeChallengeMethod != "S256" || reqs[0].Scope != "openid profile email" {
		t.Errorf("requests %+v", reqs)
	}
	issued := iss.Issued()
	if len(issued) != 1 || issued[0].SID != "session-1" || issued[0].Subject != "alice" || issued[0].Token != answer.IDToken {
		t.Errorf("issued %+v", issued)
	}
}

func TestUnitIssuerRefusesCodeMisuse(t *testing.T) {
	ctx := t.Context()
	iss := issuer(t)
	f := flow(iss)
	iss.SignInNext(Identity{Subject: "alice"})
	at, err := f.AuthorizeURL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	back, err := SignIn(ctx, iss.Client(), at, Login{})
	if err != nil {
		t.Fatal(err)
	}
	if a, err := f.ExchangeWith(ctx, back, randomToken()); err != nil || a.Error != "invalid_grant" {
		t.Errorf("a wrong verifier: %+v, %v", a, err)
	}
	if a, err := f.Exchange(ctx, back); err != nil || a.Error != "invalid_grant" {
		t.Errorf("a code used once already: %+v, %v", a, err)
	}

	iss.SignInNext(Identity{Subject: "alice"})
	at, err = f.AuthorizeURL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	back, err = SignIn(ctx, iss.Client(), at, Login{})
	if err != nil {
		t.Fatal(err)
	}
	wrong := *f
	wrong.Secret = "wrong"
	if a, err := wrong.Exchange(ctx, back); err != nil || a.Status != http.StatusUnauthorized || a.Error != "invalid_client" {
		t.Errorf("a wrong secret: %+v, %v", a, err)
	}

	for name, extra := range map[string]url.Values{
		"a plain challenge":    {"code_challenge_method": {"plain"}},
		"a missing challenge":  {"code_challenge": {""}},
		"an implicit response": {"response_type": {"id_token"}},
	} {
		iss.SignInNext(Identity{Subject: "alice"})
		g := flow(iss)
		g.Extra = extra
		at, err := g.AuthorizeURL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		back, err := SignIn(ctx, iss.Client(), at, Login{})
		if err != nil {
			t.Fatal(err)
		}
		if a, err := g.Exchange(ctx, back); err != nil || a.Error == "" {
			t.Errorf("%s: %+v, %v", name, a, err)
		}
	}
}

func TestUnitIssuerRefusesUntrustedRedirects(t *testing.T) {
	ctx := t.Context()
	iss := issuer(t)
	for name, f := range map[string]*CodeFlow{
		"an unknown client":      {Issuer: iss.URL(), ClientID: "other", RedirectURI: testCallback, Client: iss.Client()},
		"a relative redirectURI": {Issuer: iss.URL(), ClientID: testClient, RedirectURI: "/cb", Client: iss.Client()},
	} {
		at, err := f.AuthorizeURL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := SignIn(ctx, iss.Client(), at, Login{}); !errors.Is(err, ErrIssuer) ||
			!strings.Contains(err.Error(), "400") {
			t.Errorf("%s: %v", name, err)
		}
	}
	f := flow(iss)
	at, err := f.AuthorizeURL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	back, err := SignIn(ctx, iss.Client(), at, Login{})
	if err != nil {
		t.Fatal(err)
	}
	if back.Query().Get("error") != "login_required" {
		t.Errorf("with nobody queued the issuer answered %s", back)
	}
}

func TestUnitIssuerFaults(t *testing.T) {
	iss := issuer(t)
	for _, tc := range []struct {
		fault Fault
		check func(t *testing.T, f *CodeFlow, a TokenAnswer)
	}{
		{FaultWrongNonce, claimIs("nonce", func(f *CodeFlow) any { return "not-" + f.Nonce })},
		{FaultWrongIssuer, claimIs("iss", func(*CodeFlow) any { return iss.URL() + "/another" })},
		{FaultWrongAudience, claimIs("aud", func(*CodeFlow) any { return "another-client" })},
		{FaultEmptySubject, claimIs("sub", func(*CodeFlow) any { return "" })},
		{FaultNoAuthTime, claimIs("auth_time", func(*CodeFlow) any { return nil })},
		{FaultAudiencesWithoutAZP, func(t *testing.T, f *CodeFlow, a TokenAnswer) {
			_, c := verified(t, f, a)
			aud, ok := c["aud"].([]any)
			if !ok || len(aud) != 2 || aud[0] != testClient || c["azp"] != nil {
				t.Errorf("aud %v, azp %v", c["aud"], c["azp"])
			}
		}},
		{FaultOldAuthTime, func(t *testing.T, f *CodeFlow, a TokenAnswer) {
			_, c := verified(t, f, a)
			at, _ := c["auth_time"].(float64)
			if age := time.Since(time.Unix(int64(at), 0)); age < 59*time.Minute {
				t.Errorf("auth_time is %s old", age)
			}
		}},
		{FaultGroupsOverage, func(t *testing.T, f *CodeFlow, a TokenAnswer) {
			_, c := verified(t, f, a)
			names, _ := c["_claim_names"].(map[string]any)
			if c["groups"] != nil || names["groups"] != "src1" || c["_claim_sources"] == nil {
				t.Errorf("claims %v", c)
			}
		}},
		{FaultHS256, unverifiable("HS256")},
		{FaultAlgNone, unverifiable("none")},
		{FaultUnknownKey, func(t *testing.T, f *CodeFlow, a TokenAnswer) {
			h, _, err := f.Verify(t.Context(), a.IDToken)
			if err == nil || h["kid"] != "unpublished" {
				t.Errorf("kid %v verified: %v", h["kid"], err)
			}
		}},
		{FaultTokenUnavailable, func(t *testing.T, _ *CodeFlow, a TokenAnswer) {
			if a.Status != http.StatusServiceUnavailable || a.IDToken != "" {
				t.Errorf("answer %+v", a)
			}
		}},
	} {
		t.Run(string(tc.fault), func(t *testing.T) {
			if Faults[string(tc.fault)] != tc.fault {
				t.Fatal("the fault is not listed by name")
			}
			f := flow(iss)
			tc.check(t, f, signIn(t, iss, f, Identity{
				Subject: "alice", Claims: map[string]any{"groups": []string{"ops"}}, Faults: []Fault{tc.fault},
			}))
		})
	}
}

func TestUnitIssuerSlowToken(t *testing.T) {
	ctx := t.Context()
	iss := issuer(t)
	f := flow(iss)
	iss.SignInNext(Identity{Subject: "alice", Faults: []Fault{FaultSlowToken}})
	at, err := f.AuthorizeURL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	back, err := SignIn(ctx, iss.Client(), at, Login{})
	if err != nil {
		t.Fatal(err)
	}
	impatient := *iss.Client()
	impatient.Timeout = 200 * time.Millisecond
	f.Client = &impatient
	start := time.Now()
	if _, err := f.Exchange(ctx, back); err == nil || time.Since(start) > 10*time.Second {
		t.Errorf("the slow token endpoint answered in %s: %v", time.Since(start), err)
	}
}

func TestUnitIssuerKeys(t *testing.T) {
	ctx := t.Context()
	iss := issuer(t)
	f := flow(iss)
	before := signIn(t, iss, f, Identity{Subject: "alice"})
	if _, _, err := f.Verify(ctx, before.IDToken); err != nil {
		t.Fatal(err)
	}
	if err := iss.Rotate(); err != nil {
		t.Fatal(err)
	}
	if iss.KeyID() != oidcKeyID+"-1" {
		t.Errorf("the rotated key is %q", iss.KeyID())
	}
	if _, _, err := f.Verify(ctx, before.IDToken); err == nil {
		t.Error("a token of the retired key still verifies")
	}
	after := signIn(t, iss, f, Identity{Subject: "alice"})
	if h, _, err := f.Verify(ctx, after.IDToken); err != nil || h["kid"] != iss.KeyID() {
		t.Errorf("the new key: %v, %v", h, err)
	}

	jwks := func() map[string]any {
		var set struct {
			Keys []map[string]any `json:"keys"`
		}
		if err := f.getJSON(ctx, iss.URL()+"/jwks", &set); err != nil || len(set.Keys) != 1 {
			t.Fatalf("jwks %v: %v", set, err)
		}
		return set.Keys[0]
	}
	if k := jwks(); k["x5c"] == nil || k["x5t#S256"] == nil || k["x5t"] != nil {
		t.Errorf("by default the JWKS carries %v", k)
	}
	iss.PublishCertificates(false)
	if k := jwks(); k["x5c"] != nil || k["x5t#S256"] != nil {
		t.Errorf("without certificates the JWKS carries %v", k)
	}
}

func TestUnitIssuerLogout(t *testing.T) {
	ctx := t.Context()
	iss := issuer(t)
	f := flow(iss)
	if _, err := f.AuthorizeURL(ctx); err != nil {
		t.Fatal(err)
	}
	endSession, jwksURI := f.Discovery()
	if endSession != iss.URL()+"/logout" || jwksURI != iss.URL()+"/jwks" {
		t.Errorf("discovery names %s and %s", endSession, jwksURI)
	}
	back, err := SignIn(ctx, iss.Client(), endSession+"?"+url.Values{
		"id_token_hint": {"hint"}, "client_id": {testClient},
		"post_logout_redirect_uri": {"https://rp.example/signed-out"}, "state": {"st"},
	}.Encode(), Login{})
	if err != nil || back.String() != "https://rp.example/signed-out?state=st" {
		t.Errorf("RP-initiated logout went to %v: %v", back, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endSession, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := iss.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("a logout without a redirect answered %d", resp.StatusCode)
	}
	if l := iss.Logouts(); len(l) != 2 || l[0].IDTokenHint != "hint" || l[0].ClientID != testClient {
		t.Errorf("logouts %+v", l)
	}

	var got url.Values
	r := chi.NewRouter()
	r.Post("/backchannel", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = r.PostForm
		w.WriteHeader(http.StatusOK)
	})
	rp := httptest.NewServer(r)
	defer rp.Close()
	token, err := iss.LogoutToken(testClient, "alice", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	status, err := SendLogout(ctx, rp.Client(), rp.URL+"/backchannel", token)
	if err != nil || status != http.StatusOK {
		t.Fatalf("back-channel logout: %d, %v", status, err)
	}
	h, c, err := f.Verify(ctx, got.Get("logout_token"))
	if err != nil {
		t.Fatal(err)
	}
	events, _ := c["events"].(map[string]any)
	if h["typ"] != "logout+jwt" || c["sub"] != "alice" || c["sid"] != "session-1" || c["jti"] == nil ||
		c["aud"] != testClient || c["nonce"] != nil || events[BackchannelLogoutEvent] == nil {
		t.Errorf("logout token %v %v", h, c)
	}
	if bare, err := iss.LogoutToken(testClient, "", ""); err != nil {
		t.Fatal(err)
	} else if _, c, _ := Decode(bare); c["sub"] != nil || c["sid"] != nil {
		t.Errorf("a token for neither names %v", c)
	}
	if _, err := SendLogout(ctx, rp.Client(), "http://127.0.0.1:1/backchannel", token); !errors.Is(err, ErrIssuer) {
		t.Errorf("an unreachable relying party: %v", err)
	}
}

// TestUnitSignInFillsTheForm proves SignIn against an issuer that shows a
// sign-in form, as navikt's does, posting the subject and claims to it.
func TestUnitSignInFillsTheForm(t *testing.T) {
	var username, claims string
	r := chi.NewRouter()
	r.Get("/i/authorize", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<form method="post"><input name="username"><textarea name="claims"></textarea></form>`))
	})
	r.Post("/i/authorize", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		username, claims = r.PostForm.Get("username"), r.PostForm.Get("claims")
		http.Redirect(w, r, "/i/hop", http.StatusFound)
	})
	r.Get("/i/hop", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, testCallback+"?code=c&state=s", http.StatusFound)
	})
	r.Get("/i/loop", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/i/loop", http.StatusFound)
	})
	r.Get("/i/broken", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "%zz")
		w.WriteHeader(http.StatusFound)
	})
	srv := httptest.NewTLSServer(r)
	defer srv.Close()
	ctx := t.Context()
	back, err := SignIn(ctx, srv.Client(), srv.URL+"/i/authorize", Login{
		Subject: "alice", Claims: map[string]any{"email": "alice@example.com"},
	})
	if err != nil || back.Query().Get("code") != "c" {
		t.Fatalf("SignIn answered %v, %v", back, err)
	}
	if username != "alice" || claims != `{"email":"alice@example.com"}` {
		t.Errorf("the form got %q and %q", username, claims)
	}
	if _, err := SignIn(ctx, srv.Client(), srv.URL+"/i/authorize", Login{Subject: "bob"}); err != nil || claims != "{}" {
		t.Errorf("no claims posted %q: %v", claims, err)
	}
	for _, path := range []string{"/i/loop", "/i/broken", "/i/missing"} {
		if _, err := SignIn(ctx, srv.Client(), srv.URL+path, Login{}); !errors.Is(err, ErrIssuer) {
			t.Errorf("%s: %v", path, err)
		}
	}
	if _, err := SignIn(ctx, srv.Client(), "%zz", Login{}); !errors.Is(err, ErrIssuer) {
		t.Errorf("an unparseable URL: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := SignIn(cancelled, srv.Client(), srv.URL+"/i/authorize", Login{}); !errors.Is(err, ErrIssuer) {
		t.Errorf("a cancelled sign-in: %v", err)
	}
}

func TestUnitCodeFlowRefusals(t *testing.T) {
	ctx := t.Context()
	iss := issuer(t)
	f := &CodeFlow{Issuer: iss.URL() + "/", ClientID: testClient, Client: iss.Client()}
	if _, err := f.AuthorizeURL(ctx); !errors.Is(err, ErrIssuer) {
		t.Errorf("a discovery document naming another issuer: %v", err)
	}
	f = flow(iss)
	if _, err := f.AuthorizeURL(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Exchange(ctx, &url.URL{RawQuery: "state=another"}); !errors.Is(err, ErrIssuer) {
		t.Errorf("a callback with another state: %v", err)
	}
	if _, _, err := Decode("a.b"); !errors.Is(err, ErrIssuer) {
		t.Errorf("two parts: %v", err)
	}
	if _, _, err := Decode("!.e30.x"); !errors.Is(err, ErrIssuer) {
		t.Errorf("a header that is not base64url: %v", err)
	}
	if _, _, err := Decode("e30.bm90IGpzb24.x"); !errors.Is(err, ErrIssuer) {
		t.Errorf("claims that are not JSON: %v", err)
	}
	if _, _, err := f.Verify(ctx, "e30.e30.!"); !errors.Is(err, ErrIssuer) {
		t.Errorf("a signature that is not base64url: %v", err)
	}
	broken := &CodeFlow{Issuer: "https://127.0.0.1:1", Client: iss.Client()}
	if _, err := broken.AuthorizeURL(ctx); !errors.Is(err, ErrIssuer) {
		t.Errorf("an unreachable issuer: %v", err)
	}
	missing := &CodeFlow{Issuer: iss.URL() + "/nowhere", Client: iss.Client()}
	if _, err := missing.AuthorizeURL(ctx); !errors.Is(err, ErrIssuer) {
		t.Errorf("an issuer without discovery: %v", err)
	}
}

func claimIs(name string, want func(*CodeFlow) any) func(*testing.T, *CodeFlow, TokenAnswer) {
	return func(t *testing.T, f *CodeFlow, a TokenAnswer) {
		t.Helper()
		_, c := verified(t, f, a)
		if got := c[name]; got != want(f) {
			t.Errorf("%s = %v, want %v", name, got, want(f))
		}
	}
}

func unverifiable(alg string) func(*testing.T, *CodeFlow, TokenAnswer) {
	return func(t *testing.T, f *CodeFlow, a TokenAnswer) {
		t.Helper()
		h, _, err := f.Verify(t.Context(), a.IDToken)
		if err == nil || h["alg"] != alg {
			t.Errorf("alg %v verified: %v", h["alg"], err)
		}
	}
}

func verified(t *testing.T, f *CodeFlow, a TokenAnswer) (header, claims map[string]any) {
	t.Helper()
	h, c, err := f.Verify(t.Context(), a.IDToken)
	if err != nil {
		t.Fatalf("%+v: %v", a, err)
	}
	return h, c
}
