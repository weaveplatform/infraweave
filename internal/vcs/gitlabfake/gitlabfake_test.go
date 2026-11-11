package gitlabfake_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
