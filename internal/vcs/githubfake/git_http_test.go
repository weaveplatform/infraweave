package githubfake_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
)

func TestUnitGitHTTP(t *testing.T) {
	f := newDeploymentFixture(t)
	response := f.send(t, http.MethodPost, fmt.Sprintf("/app/installations/%d/access_tokens", f.install), f.jwt,
		`{"repositories":["infra"],"permissions":{"contents":"read"}}`)
	var token struct {
		Value string `json:"token"`
	}
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &token) != nil || token.Value == "" {
		t.Fatalf("mint repository reader: status %d", response.Code)
	}
	metadata := f.token(t, `{"metadata":"read"}`)
	deployment := f.token(t, `{"deployments":"write"}`)
	repo := f.f.Repo("acme", "infra")
	head := repo.Commit("main", "later", map[string]string{"a.tf": "later"})
	server := f.f.Start()
	t.Cleanup(server.Close)
	clone, err := git.CloneContext(t.Context(), memory.NewStorage(), memfs.New(), &git.CloneOptions{
		URL: server.URL + "/acme/infra.git", CABundle: server.CA,
		Auth: &githttp.BasicAuth{Username: "x-access-token", Password: token.Value},
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := clone.Head()
	if err != nil || current.Hash().String() != head {
		t.Fatalf("clone does not contain the current branch head: %v", err)
	}
	first, err := clone.CommitObject(plumbing.NewHash(f.sha))
	if err != nil {
		t.Fatal(err)
	}
	file, err := first.File("a.tf")
	if err != nil {
		t.Fatal(err)
	}
	contents, err := file.Contents()
	if err != nil || contents != "test" {
		t.Fatalf("historical blob contents=%q error=%v", contents, err)
	}
	refs := "/acme/infra.git/info/refs?service=git-upload-pack"
	for _, test := range []struct {
		name, method, path, token string
		status                    int
	}{
		{"reader", http.MethodGet, refs, token.Value, http.StatusOK},
		{"missing", http.MethodGet, refs, "", http.StatusUnauthorized},
		{"invalid", http.MethodGet, refs, "ghs-invalid", http.StatusUnauthorized},
		{"metadata-only", http.MethodGet, refs, metadata, http.StatusForbidden},
		{"deployment-only", http.MethodGet, refs, deployment, http.StatusForbidden},
		{"wrong-repository", http.MethodGet, "/acme/web.git/info/refs?service=git-upload-pack", token.Value, http.StatusNotFound},
		{"wrong-owner", http.MethodGet, "/elsewhere/infra.git/info/refs?service=git-upload-pack", token.Value, http.StatusNotFound},
		{"receive-advertisement", http.MethodGet, "/acme/infra.git/info/refs?service=git-receive-pack", token.Value, http.StatusNotFound},
		{"receive-pack", http.MethodPost, "/acme/infra.git/git-receive-pack", token.Value, http.StatusNotFound},
		{"invalid-upload", http.MethodPost, "/acme/infra.git/git-upload-pack", token.Value, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), test.method, test.path, strings.NewReader("broken"))
			if test.token != "" {
				request.SetBasicAuth("x-access-token", test.token)
			}
			response := httptest.NewRecorder()
			f.handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("HTTP status=%d want=%d", response.Code, test.status)
			}
		})
	}
}
