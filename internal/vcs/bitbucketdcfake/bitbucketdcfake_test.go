package bitbucketdcfake_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketdcfake"
)

// TestUnitRoutes checks the answers the bitbucketdc provider never draws:
// a request with no or a wrong token, a repository the user holds no role
// on, an unknown tag, commit or file, a compare that names one commit, an
// unknown permission, the REPO_WRITE permission, and the server's URL and
// certificate.
func TestUnitRoutes(t *testing.T) {
	f := bitbucketdcfake.New()
	tok := f.AddToken("infraweave")
	f.Grant("ACME", "infra", "infraweave", bitbucketdcfake.Write)
	r := f.Repo("ACME", "infra")
	if f.Repo("ACME", "infra") != r {
		t.Error("Repo made a second ACME/infra")
	}
	head := r.Commit("main", "first", map[string]string{"a/b.tf": "x"})
	f.Repo("ACME", "hidden")
	h := f.Handler()
	get := func(path, token string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		h.ServeHTTP(w, req)
		return w
	}
	for name, token := range map[string]string{"none": "", "unknown token": "BBDC-nope"} {
		if w := get(bitbucketdcfake.API+"/repos", token); w.Code != http.StatusUnauthorized {
			t.Errorf("%s: GET /repos = %d, want 401", name, w.Code)
		}
	}
	repo := bitbucketdcfake.API + "/projects/ACME/repos/infra"
	for path, want := range map[string]int{
		bitbucketdcfake.API + "/projects/ACME/repos/hidden/branches": http.StatusNotFound,
		repo + "/tags/v9":                           http.StatusNotFound,
		repo + "/commits/abc":                       http.StatusNotFound,
		repo + "/raw/nope.tf?at=" + head:            http.StatusNotFound,
		repo + "/compare/changes?from=" + head:      http.StatusNotFound,
		repo + "/raw/a/b.tf?at=" + head:             http.StatusOK,
		bitbucketdcfake.API + "/repos?permission=X": http.StatusBadRequest,
	} {
		if w := get(path, tok); w.Code != want {
			t.Errorf("GET %s = %d, want %d", path, w.Code, want)
		}
	}
	for perm, want := range map[string]bool{"REPO_WRITE": true, "REPO_ADMIN": false, "": true} {
		w := get(bitbucketdcfake.API+"/repos?permission="+perm, tok)
		if got := strings.Contains(w.Body.String(), `"slug":"infra"`); got != want {
			t.Errorf("permission %q lists infra: %v, want %v", perm, got, want)
		}
	}
	srv := f.Start()
	defer srv.Close()
	if !strings.HasPrefix(string(srv.CA), "-----BEGIN CERTIFICATE-----") ||
		!strings.HasPrefix(srv.URL, "https://") {
		t.Errorf("the server = %s with CA %.30s", srv.URL, srv.CA)
	}
}
