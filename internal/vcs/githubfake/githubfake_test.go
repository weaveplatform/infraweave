package githubfake_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
)

// TestUnitRefusals checks the answers the github provider never draws: a
// request with no or a bad credential, another App's installation, a
// repository the token does not reach, and a comparison it cannot parse.
func TestUnitRefusals(t *testing.T) {
	f := githubfake.New()
	app := f.AddApp("infraweave", "acme")
	f.Install(app.ID, "acme", "Organization", "infra")
	r := f.Repo("acme", "infra")
	if f.Repo("acme", "infra") != r {
		t.Error("Repo made a second acme/infra")
	}
	r.Commit("main", "first", map[string]string{"a/b.tf": "x"})
	h := f.Handler()
	for path, want := range map[string]int{
		"/api/v3/app":                            http.StatusUnauthorized,
		"/api/v3/installation/repositories":      http.StatusUnauthorized,
		"/api/v3/app-manifests/nope/conversions": http.StatusMethodNotAllowed,
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer not-a-jwt")
		h.ServeHTTP(w, req)
		if w.Code != want {
			t.Errorf("GET %s = %d, want %d", path, w.Code, want)
		}
	}
	if !strings.HasPrefix(string(f.Start().CA), "-----BEGIN CERTIFICATE-----") {
		t.Error("the server's CA is not a PEM certificate")
	}
}

// TestUnitNarrowedTokens checks the installation tokens a request
// narrows: repositories the installation lacks and permissions the App
// lacks are refused, a write grants a read, a token without contents
// reads no repository, one narrowed to a repository lists that one
// alone, and a body that is not JSON is refused.
func TestUnitNarrowedTokens(t *testing.T) {
	f := githubfake.New()
	app := f.AddApp("infraweave", "acme")
	id := f.Install(app.ID, "acme", "Organization", "infra", "web")
	f.Repo("acme", "infra").Commit("main", "first", map[string]string{"a.tf": "x"})
	key, err := jwt.ParseRSAPrivateKeyFromPEM(app.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": strconv.FormatInt(app.ID, 10), "exp": time.Now().Add(time.Minute).Unix(),
	}).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	h := f.Handler()
	send := func(method, path, auth, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
		req.Header.Set("Authorization", auth)
		h.ServeHTTP(w, req)
		return w
	}
	mint := func(body string) (int, string) {
		w := send(http.MethodPost,
			"/api/v3/app/installations/"+strconv.FormatInt(id, 10)+"/access_tokens",
			"Bearer "+signed, body)
		var out struct {
			Token string `json:"token"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out.Token
	}
	for body, want := range map[string]int{
		`{"repositories":["elsewhere"]}`:            http.StatusUnprocessableEntity,
		`{"permissions":{"administration":"read"}}`: http.StatusUnprocessableEntity,
		`{"permissions":{"contents":"write"}}`:      http.StatusUnprocessableEntity,
		`{"permissions":{"checks":"read"}}`:         http.StatusCreated,
		`not json`:                                  http.StatusBadRequest,
	} {
		if code, _ := mint(body); code != want {
			t.Errorf("mint %s = %d, want %d", body, code, want)
		}
	}
	_, metadata := mint(`{"permissions":{"metadata":"read"}}`)
	if w := send(http.MethodGet, "/api/v3/repos/acme/infra/commits/main",
		"token "+metadata, ""); w.Code != http.StatusForbidden {
		t.Errorf("a token without contents reads a commit: %d", w.Code)
	}
	_, infra := mint(`{"repositories":["infra"],"permissions":{"contents":"read"}}`)
	w := send(http.MethodGet, "/api/v3/installation/repositories", "token "+infra, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"infra"`) ||
		strings.Contains(w.Body.String(), `"web"`) {
		t.Errorf("a token narrowed to infra lists %d %s", w.Code, w.Body)
	}
	got := f.TokenRequests()
	if len(got) != 3 || got[2].Installation != id || len(got[2].Repositories) != 1 ||
		got[2].Permissions["contents"] != "read" || got[0].Permissions["checks"] != "read" {
		t.Errorf("token requests = %+v", got)
	}
}
