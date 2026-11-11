package gitlabfake_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
)

// TestUnitRoutes checks the answers the gitlab provider never draws: a
// request with no or a bad token, a project named by its ID, a bearer
// token, a project the token's user is no member of, and a token's
// identity.
func TestUnitRoutes(t *testing.T) {
	f := gitlabfake.New()
	tok := f.AddToken("infraweave", "api")
	f.AddMember("acme/platform", "infra", "infraweave", gitlabfake.Maintainer)
	r := f.Repo("acme/platform", "infra")
	if f.Repo("acme/platform", "infra") != r {
		t.Error("Repo made a second acme/platform/infra")
	}
	r.Commit("main", "first", map[string]string{"a/b.tf": "x"})
	f.Repo("acme", "hidden")
	h := f.Handler()
	get := func(path, auth string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		if name, value, ok := strings.Cut(auth, ": "); ok {
			req.Header.Set(name, value)
		}
		h.ServeHTTP(w, req)
		return w
	}
	for path, want := range map[string]int{
		"/api/v4/user":              http.StatusUnauthorized,
		"/api/v4/projects/1/branch": http.StatusUnauthorized,
	} {
		if w := get(path, "PRIVATE-TOKEN: glpat-nope"); w.Code != want {
			t.Errorf("GET %s = %d, want %d", path, w.Code, want)
		}
	}
	var p map[string]any
	w := get("/api/v4/projects/acme%2Fplatform%2Finfra", "Authorization: Bearer "+tok)
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || w.Code != http.StatusOK ||
		p["path_with_namespace"] != "acme/platform/infra" {
		t.Fatalf("GET the project by path = %d %s", w.Code, w.Body)
	}
	if w := get(
		"/api/v4/projects/"+fmt.Sprint(p["id"])+"/repository/branches",
		"PRIVATE-TOKEN: "+tok,
	); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "main") {
		t.Errorf("GET branches by ID = %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/api/v4/projects/acme%2Fhidden", "/api/v4/projects/999999"} {
		if w := get(path, "PRIVATE-TOKEN: "+tok); w.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, w.Code)
		}
	}
	if w := get("/api/v4/user", "PRIVATE-TOKEN: "+tok); !strings.Contains(
		w.Body.String(), `"username":"infraweave"`) {
		t.Errorf("GET /user = %s", w.Body)
	}
	if w := get("/api/v4/projects/acme%2Fplatform%2Finfra/repository/tags/v9",
		"PRIVATE-TOKEN: "+tok); w.Code != http.StatusNotFound {
		t.Errorf("GET an unknown tag = %d", w.Code)
	}
	srv := f.Start()
	defer srv.Close()
	if !strings.HasPrefix(string(srv.CA), "-----BEGIN CERTIFICATE-----") ||
		!strings.HasPrefix(srv.URL, "https://") {
		t.Errorf("the server = %s with CA %.30s", srv.URL, srv.CA)
	}
}

// TestUnitAccessTokens checks the mint requests the gitlab provider never
// sends, a body that is not JSON, one naming no scope, a past expiry and
// a level above the minter's, and a revoke by a Developer.
func TestUnitAccessTokens(t *testing.T) {
	f := gitlabfake.New()
	tok := f.AddToken("infraweave", "api")
	dev := f.AddToken("dev", "api")
	f.AddMember("acme", "infra", "infraweave", gitlabfake.Maintainer)
	f.AddMember("acme", "infra", "dev", gitlabfake.Developer)
	h := f.Handler()
	send := func(method, path, auth, body string) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
		req.Header.Set("PRIVATE-TOKEN", auth)
		h.ServeHTTP(w, req)
		return w.Code
	}
	tomorrow := time.Now().Add(48 * time.Hour).UTC().Format(time.DateOnly)
	for body, want := range map[string]int{
		"{": http.StatusBadRequest,
		`{"name":"run","expires_at":"` + tomorrow + `"}`:                                                http.StatusBadRequest,
		`{"name":"run","scopes":["read_repository"],"expires_at":"2001-01-01"}`:                         http.StatusBadRequest,
		`{"name":"run","scopes":["api"],"access_level":50,"expires_at":"` + tomorrow + `"}`:             http.StatusBadRequest,
		`{"name":"run","scopes":["read_repository"],"access_level":20,"expires_at":"` + tomorrow + `"}`: http.StatusCreated,
	} {
		if got := send(
			http.MethodPost,
			"/api/v4/projects/acme%2Finfra/access_tokens",
			tok,
			body,
		); got != want {
			t.Errorf("mint %s = %d, want %d", body, got, want)
		}
	}
	id := fmt.Sprint(f.Minted()[0].ID)
	if got := send(http.MethodDelete, "/api/v4/projects/acme%2Finfra/access_tokens/"+id, dev,
		""); got != http.StatusForbidden {
		t.Errorf("a Developer's revoke = %d", got)
	}
	if got := send(http.MethodDelete, "/api/v4/projects/acme%2Finfra/access_tokens/"+id, tok,
		""); got != http.StatusNoContent || !f.Minted()[0].Revoked {
		t.Errorf("revoke = %d, %+v", got, f.Minted())
	}
}
