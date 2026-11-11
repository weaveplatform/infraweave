package bitbucketcloudfake_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketcloudfake"
)

// TestUnitRoutes checks the answers the bitbucketcloud provider never
// draws: a request with no, a wrong or another account's credentials, a
// repository the account holds no role on, an unknown branch, tag, commit
// or file, a range that is not one, the contributor role, and the
// server's URLs and certificate.
func TestUnitRoutes(t *testing.T) {
	f := bitbucketcloudfake.New()
	tok := f.AddToken("infraweave@example.com")
	other := f.AddToken("someone@example.com")
	f.Grant("acme", "infra", "infraweave@example.com", bitbucketcloudfake.Write)
	r := f.Repo("acme", "infra")
	if f.Repo("acme", "infra") != r {
		t.Error("Repo made a second acme/infra")
	}
	head := r.Commit("main", "first", map[string]string{"a/b.tf": "x"})
	f.Repo("acme", "hidden")
	h := f.Handler()
	get := func(path, email, token string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		if email != "" {
			req.SetBasicAuth(email, token)
		}
		h.ServeHTTP(w, req)
		return w
	}
	for name, cred := range map[string][2]string{
		"none":            {"", ""},
		"unknown token":   {"infraweave@example.com", "ATATTnope"},
		"another account": {"infraweave@example.com", other},
	} {
		if w := get("/2.0/user", cred[0], cred[1]); w.Code != http.StatusUnauthorized {
			t.Errorf("%s: GET /user = %d, want 401", name, w.Code)
		}
	}
	me := func(path string) *httptest.ResponseRecorder {
		return get(path, "infraweave@example.com", tok)
	}
	if w := me("/2.0/user"); !strings.Contains(w.Body.String(), `"nickname":"infraweave"`) {
		t.Errorf("GET /user = %s", w.Body)
	}
	for path, want := range map[string]int{
		"/2.0/repositories/acme/hidden/refs/branches":           http.StatusNotFound,
		"/2.0/repositories/acme/infra/refs/branches/nope":       http.StatusNotFound,
		"/2.0/repositories/acme/infra/refs/tags/v9":             http.StatusNotFound,
		"/2.0/repositories/acme/infra/commit/abc":               http.StatusNotFound,
		"/2.0/repositories/acme/infra/src/" + head + "/nope.tf": http.StatusNotFound,
		"/2.0/repositories/acme/infra/diffstat/" + head:         http.StatusNotFound,
		"/2.0/repositories/acme/infra/src/" + head + "/a/b.tf":  http.StatusOK,
	} {
		if w := me(path); w.Code != want {
			t.Errorf("GET %s = %d, want %d", path, w.Code, want)
		}
	}
	for role, want := range map[string]bool{"contributor": true, "admin": false, "": true} {
		w := me("/2.0/repositories/acme?role=" + role)
		if got := strings.Contains(w.Body.String(), `"slug":"infra"`); got != want {
			t.Errorf("role %q lists infra: %v, want %v", role, got, want)
		}
	}
	srv := f.Start()
	defer srv.Close()
	if !strings.HasPrefix(string(srv.CA), "-----BEGIN CERTIFICATE-----") ||
		!strings.HasPrefix(srv.URL, "https://") || srv.APIHost != srv.URL+"/2.0" {
		t.Errorf("the server = %s, %s with CA %.30s", srv.URL, srv.APIHost, srv.CA)
	}
}
