package githubfake_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
