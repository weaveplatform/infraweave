package oauth_test

import (
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/oauth"
)

func TestUnitPKCE(t *testing.T) {
	verifier := strings.Repeat("v", 43)
	challenge := oauth.Challenge(verifier)
	if err := oauth.CheckChallenge(challenge, oauth.MethodS256); err != nil {
		t.Errorf("an S256 challenge is refused: %v", err)
	}
	if !oauth.VerifyPKCE(challenge, verifier) {
		t.Error("the verifier of the challenge fails")
	}
	for name, c := range map[string][2]string{
		"plain":               {verifier, "plain"},
		"no method, so plain": {challenge, ""},
		"no challenge":        {"", oauth.MethodS256},
		"not a SHA-256":       {"short", oauth.MethodS256},
		"not base64url":       {strings.Repeat("!", 43), oauth.MethodS256},
	} {
		var e *oauth.Error
		if err := oauth.CheckChallenge(c[0], c[1]); !errors.As(err, &e) ||
			e.Code != oauth.CodeInvalidRequest {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, v := range map[string]string{
		"another verifier":     strings.Repeat("w", 43),
		"the challenge itself": challenge,
		"too short":            strings.Repeat("v", 42),
		"too long":             strings.Repeat("v", 129),
		"a reserved character": strings.Repeat("v", 42) + "/",
		"empty":                "",
	} {
		if oauth.VerifyPKCE(challenge, v) {
			t.Errorf("%s verifies", name)
		}
	}
	long := strings.Repeat("a-._~Z9", 19)[:128]
	if !oauth.VerifyPKCE(oauth.Challenge(long), long) {
		t.Error("a 128-character verifier of every unreserved class fails")
	}
}

func TestUnitRedirectURIs(t *testing.T) {
	for _, u := range []string{
		"http://127.0.0.1/callback", "http://127.0.0.1:53682/callback", "http://[::1]:8080/cb",
		"http://localhost:3000/", "http://LOCALHOST/cb", "https://app.example.com/oauth/cb",
		"https://app.example.com:8443/cb?x=1",
	} {
		if _, err := oauth.CheckRedirectURI(u); err != nil {
			t.Errorf("%s is refused: %v", u, err)
		}
	}
	for _, u := range []string{
		"http://app.example.com/cb", "http://127.0.0.2/cb", "ftp://localhost/cb",
		"com.example.app:/cb", "/relative", "https:///no-host", "https://u:p@app.example.com/cb",
		"https://app.example.com/cb#frag", "http://localhost.example.com/cb",
	} {
		var e *oauth.Error
		_, err := oauth.CheckRedirectURI(u)
		if !errors.As(err, &e) || e.Code != oauth.CodeInvalidRedirectURI ||
			!strings.Contains(e.Description, u) {
			t.Errorf("%s: %v", u, err)
		}
	}

	hosts := []string{"app.example.com", "*.corp.example.org"}
	for u, want := range map[string]bool{
		"http://127.0.0.1:9/cb":            true,
		"http://localhost/cb":              true,
		"https://app.example.com/cb":       true,
		"https://APP.example.com:8443/cb":  true,
		"https://x.corp.example.org/cb":    true,
		"https://a.b.corp.example.org/cb":  true,
		"https://corp.example.org/cb":      false,
		"https://evilcorp.example.org/cb":  false,
		"https://other.example.com/cb":     false,
		"https://app.example.com.evil/cb":  false,
		"http://app.example.com/cb":        false,
		"https://x.corp.example.org.e/cb":  false,
		"://not a URI":                     false,
		"https://sub.app.example.com/cb":   false,
		"http://x.corp.example.org:80/cb":  false,
		"https://x.corp.example.org:1/cb?": true,
	} {
		if got := oauth.AllowedRedirect(u, hosts); got != want {
			t.Errorf("AllowedRedirect(%s) = %v, want %v", u, got, want)
		}
	}
	if oauth.AllowedRedirect("https://app.example.com/cb", nil) {
		t.Error("an empty allowlist admits an https host")
	}

	for reg, given := range map[string]string{
		"http://127.0.0.1/cb":         "http://127.0.0.1:49152/cb",
		"http://localhost:1/cb?a=b":   "http://localhost:2/cb?a=b",
		"https://app.example.com/cb":  "https://app.example.com/cb",
		"http://[::1]:1/callback":     "http://[::1]:65535/callback",
		"http://LOCALHOST:1/callback": "http://localhost:2/callback",
	} {
		if !oauth.SameRedirect(reg, given) {
			t.Errorf("%s does not match %s", given, reg)
		}
	}
	for reg, given := range map[string]string{
		"http://127.0.0.1/cb":        "http://127.0.0.1:1/other",
		"http://127.0.0.1/cb?a=b":    "http://127.0.0.1:1/cb?a=c",
		"https://app.example.com/cb": "https://app.example.com:8443/cb",
		"http://localhost/cb":        "http://127.0.0.1/cb",
		"http://localhost/cb%":       "http://localhost:1/cb%",
	} {
		if oauth.SameRedirect(reg, given) {
			t.Errorf("%s matches %s", given, reg)
		}
	}
	if oauth.RedirectHost("https://app.example.com:8443/cb") != "app.example.com" ||
		oauth.RedirectHost("::") != "" {
		t.Error("RedirectHost does not answer the host alone")
	}
}

func TestUnitScopes(t *testing.T) {
	for param, want := range map[string][]string{
		"":                     {oauth.ScopeRead},
		"mcp:read":             {oauth.ScopeRead},
		"mcp:write":            {oauth.ScopeRead, oauth.ScopeWrite},
		"mcp:write mcp:read":   {oauth.ScopeRead, oauth.ScopeWrite},
		" mcp:write mcp:write": {oauth.ScopeRead, oauth.ScopeWrite},
	} {
		if got, err := oauth.Scopes(param); err != nil || !slices.Equal(got, want) {
			t.Errorf("Scopes(%q) = %v, %v", param, got, err)
		}
	}
	var e *oauth.Error
	if _, err := oauth.Scopes("mcp:read admin"); !errors.As(err, &e) ||
		e.Code != oauth.CodeInvalidScope {
		t.Errorf("an unknown scope: %v", err)
	}
	if !oauth.Within([]string{oauth.ScopeRead}, []string{oauth.ScopeRead, oauth.ScopeWrite}) ||
		oauth.Within([]string{oauth.ScopeWrite}, []string{oauth.ScopeRead}) {
		t.Error("Within does not answer containment")
	}
}

func TestUnitResources(t *testing.T) {
	const public = "https://iw.example.com"
	for r, want := range map[string]bool{
		public + "/mcp":              true,
		public + "/flows/mcp":        true,
		public + "/flows/mcp/deploy": true,
		public + "/flows/mcp/":       false,
		public + "/flows/mcp/a/b":    false,
		public + "/flows/mcp/a?x":    false,
		public + "/mcp/x":            false,
		public + "/api/v1":           false,
		"https://other.example/mcp":  false,
		public + "x/mcp":             false,
	} {
		if got := oauth.Resource(public+"/", r); got != want {
			t.Errorf("Resource(%s) = %v, want %v", r, got, want)
		}
	}
}

func TestUnitRedirect(t *testing.T) {
	got := oauth.Redirect("http://127.0.0.1:9/cb?keep=1", "s t",
		&oauth.Error{Code: oauth.CodeAccessDenied, Description: "no"})
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("keep") != "1" || q.Get("error") != oauth.CodeAccessDenied ||
		q.Get("error_description") != "no" || q.Get("state") != "s t" {
		t.Errorf("Redirect = %s", got)
	}
	if got := oauth.Redirect("http://127.0.0.1/cb", "", nil, "code", "c"); got !=
		"http://127.0.0.1/cb?code=c" {
		t.Errorf("Redirect without a state = %s", got)
	}
	if got := oauth.Redirect("://", "", nil); got != "://" {
		t.Errorf("an unparsable URI = %s", got)
	}
}
