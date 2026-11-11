package azuredevopsfake_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
)

// TestUnitRoutes checks the answers the azuredevops provider never
// draws: a commit route given a branch's name, a file at a version that
// is not a commit, a diff of a ref that is not a SHA, an unknown
// organization, an unknown tag, a request with no credential, and the
// server's URLs and certificate.
func TestUnitRoutes(t *testing.T) {
	f := azuredevopsfake.New()
	tok := f.AddToken("infraweave")
	f.Grant("Platform", "infraweave", azuredevopsfake.Contributor)
	r := f.Repo("Platform", "infra")
	if f.Repo("Platform", "infra") != r {
		t.Error("Repo made a second Platform/infra")
	}
	head := r.Commit("main", "first", map[string]string{"a/b.tf": "x"})
	r.Branch("light", head)
	h := f.Handler()
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		req.SetBasicAuth("", tok)
		h.ServeHTTP(w, req)
		return w
	}
	repo := "/acme/Platform/_apis/git/repositories/infra"
	for path, want := range map[string]int{
		repo + "/commits/main": http.StatusNotFound,
		repo + "/items?path=/a/b.tf&versionDescriptor.version=main":                                              http.StatusNotFound,
		repo + "/items?path=/a/b.tf&versionDescriptor.version=" + head + "&versionDescriptor.versionType=commit": http.StatusOK,
		repo + "/diffs/commits?baseVersion=main&targetVersion=" + head:                                           http.StatusNotFound,
		"/other/_apis/projects":         http.StatusNotFound,
		repo + "/refs?filter=tags/nope": http.StatusOK,
	} {
		if w := get(path); w.Code != want {
			t.Errorf("GET %s = %d, want %d", path, w.Code, want)
		}
	}
	if w := get(repo + "/diffs/commits?baseVersion=" + head + "&targetVersion=" + head +
		"&$top=x"); !strings.Contains(w.Body.String(), `"allChangesIncluded":true`) {
		t.Errorf("an empty diff = %s", w.Body)
	}
	anon := httptest.NewRecorder()
	h.ServeHTTP(anon, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/acme/_apis/projects", nil))
	if anon.Code != http.StatusNonAuthoritativeInfo ||
		!strings.HasPrefix(anon.Header().Get("Content-Type"), "text/html") {
		t.Errorf("no credential = %d %s", anon.Code, anon.Header().Get("Content-Type"))
	}
	srv := f.Start()
	defer srv.Close()
	if !strings.HasPrefix(string(srv.CA), "-----BEGIN CERTIFICATE-----") ||
		!strings.HasPrefix(srv.URL, "https://") ||
		srv.OrganizationURL != srv.URL+"/"+azuredevopsfake.Organization || srv.Pool() == nil {
		t.Errorf("the server = %s, %s with CA %.30s", srv.URL, srv.OrganizationURL, srv.CA)
	}
}
