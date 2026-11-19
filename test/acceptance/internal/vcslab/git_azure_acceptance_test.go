package vcslab

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/test/deps"
)

func TestAccNativeAzureCloneFallback(t *testing.T) {
	if err := deps.DockerReachable(t.Context()); err != nil {
		if os.Getenv(sqltest.RequireDocker) != "1" {
			t.Skipf("Gitea unavailable: %v", err)
		}
		t.Fatal(err)
	}
	var dependencies deps.Dependencies
	t.Cleanup(func() { _ = dependencies.Close(context.Background()) })
	g, err := dependencies.Gitea(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.CreateRepository(t.Context(), "native-push", map[string]string{"main.tf": "# seed\n"}); err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(g.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := &httputil.ReverseProxy{Transport: g.Client.Transport, Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(target)
		r.Out.URL.Path = strings.Replace(r.Out.URL.Path, "/project/_git/native-push", "/infraweave/native-push.git", 1)
		r.Out.SetBasicAuth("infraweave", "infraweave-gitea")
	}}
	const token = "private-lab-clone-credential"
	var refused atomic.Int32
	router := chi.NewRouter()
	router.Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "lab" || password != token {
			w.Header().Set("WWW-Authenticate", `Basic realm="lab"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Preserve discovery and push, but reproduce Azure's HTTP 400 on
		// go-git's full upload-pack negotiation against a real Git server.
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/git-upload-pack") && strings.Contains(r.UserAgent(), "go-git") {
			refused.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	server := httptest.NewTLSServer(router)
	defer server.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	native := &Native{Provider: "azure-devops", Host: server.URL, Namespace: "project", Repository: "native-push"}
	parent := t.TempDir()
	for i, branch := range []string{"main", "feature", "feature"} {
		marker := []string{"main-proof", "feature-first", "feature-next"}[i]
		head, err := native.Push(t.Context(), parent, branch, marker, GitCredentials{User: "lab", Token: token, CAFile: ca})
		if err != nil {
			t.Fatal(err)
		}
		actual, err := g.Head(t.Context(), "native-push", branch)
		if err != nil || head == "" || actual != head {
			t.Fatal("pushed commit is not the real Git branch head", err)
		}
		files, err := g.Files(t.Context(), "native-push", head)
		if err != nil || !strings.Contains(files["main.tf"], marker) {
			t.Fatal("pushed tree does not contain the verification marker", err)
		}
	}
	if refused.Load() != 3 {
		t.Fatalf("fallback was exercised %d times, want 3", refused.Load())
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatal("native pushes left owned workspaces behind", err)
	}
}
