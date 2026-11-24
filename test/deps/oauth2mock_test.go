package deps

import (
	"net/http"
	"os"
	"testing"
)

// TestAccOAuth2Mock signs in through the navikt container with PKCE: its
// discovery document names its endpoints, its sign-in form takes the
// subject and claims, and the ID token verifies against its JWKS. Without
// Docker it skips, except where INFRAWEAVE_ACCEPT_REQUIRE_DOCKER is set.
func TestAccOAuth2Mock(t *testing.T) {
	ctx := t.Context()
	if err := DockerReachable(ctx); err != nil {
		if os.Getenv("INFRAWEAVE_ACCEPT_REQUIRE_DOCKER") != "" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
	d := &Dependencies{}
	t.Cleanup(func() { _ = d.Close(ctx) })
	m, err := d.OAuth2Mock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := d.OAuth2Mock(ctx); again != m || err != nil {
		t.Error("a second call started another container")
	}
	if len(m.CertPEM()) == 0 {
		t.Error("no certificate")
	}
	f := &CodeFlow{
		Issuer: m.Issuer("navikt-flow"), ClientID: "infraweave", Secret: "any",
		RedirectURI: testCallback, Client: m.Client(),
	}
	at, err := f.AuthorizeURL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	endSession, _ := f.Discovery()
	if endSession != f.Issuer+"/endsession" {
		t.Errorf("end_session_endpoint %s", endSession)
	}
	back, err := SignIn(ctx, m.Client(), at, Login{Subject: "alice", Claims: map[string]any{
		"email": "alice@example.com", "email_verified": true, "groups": []string{"ops"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	answer, err := f.Exchange(ctx, back)
	if err != nil || answer.Status != http.StatusOK {
		t.Fatalf("exchange: %+v, %v", answer, err)
	}
	_, claims, err := f.Verify(ctx, answer.IDToken)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{
		"iss": f.Issuer, "sub": "alice", "aud": "infraweave", "nonce": f.Nonce,
		"email": "alice@example.com", "email_verified": true,
	} {
		if claims[k] != want {
			t.Errorf("%s = %v, want %v", k, claims[k], want)
		}
	}
	if g, ok := claims["groups"].([]any); !ok || len(g) != 1 || g[0] != "ops" {
		t.Errorf("groups = %v", claims["groups"])
	}

	// navikt checks the verifier too.
	at, err = f.AuthorizeURL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	back, err = SignIn(ctx, m.Client(), at, Login{Subject: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if a, err := f.ExchangeWith(ctx, back, randomToken()); err != nil || a.Error != "invalid_grant" {
		t.Errorf("a wrong verifier: %+v, %v", a, err)
	}
}
