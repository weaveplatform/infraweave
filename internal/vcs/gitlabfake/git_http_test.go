package gitlabfake_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
)

// TestUnitGitHTTP fetches the actual seeded graph with the launcher's exact-SHA
// request and full-history fallback, then checks token scope and revocation.
func TestUnitGitHTTP(t *testing.T) {
	fake := gitlabfake.New()
	owner := fake.AddToken("owner", "api")
	reader := fake.AddToken("owner", "read_repository")
	metadata := fake.AddToken("owner", "read_api")
	guest := fake.AddToken("guest", "read_repository")
	reporter := fake.AddToken("reporter", "read_repository")
	outsider := fake.AddToken("outsider", "read_repository")
	fake.AddMember("acme/platform", "infra", "owner", gitlabfake.Maintainer)
	fake.AddMember("acme/platform", "infra", "guest", gitlabfake.Guest)
	fake.AddMember("acme/platform", "infra", "reporter", gitlabfake.Reporter)
	fake.AddMember("acme", "other", "owner", gitlabfake.Maintainer)
	repo := fake.Repo("acme/platform", "infra")
	first := repo.Commit("main", "first", map[string]string{"infra/main.tf": "first"})
	repo.Tag("v1", first, "first release")
	repo.Commit("main", "second", map[string]string{"infra/main.tf": "second"})
	fake.Repo("acme", "other").Commit("main", "other", map[string]string{"main.tf": "other"})
	handler := fake.Handler()
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
		if token != "" {
			r.SetBasicAuth("oauth2", token)
			r.Header.Set("PRIVATE-TOKEN", token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	expires := time.Now().Add(48 * time.Hour).UTC().Format(time.DateOnly)
	mint := request(http.MethodPost, "/api/v4/projects/acme%2Fplatform%2Finfra/access_tokens", owner,
		`{"name":"run","scopes":["read_repository"],"access_level":20,"expires_at":"`+expires+`"}`)
	var token struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(mint.Body.Bytes(), &token); err != nil || mint.Code != http.StatusCreated || token.Token == "" {
		t.Fatalf("mint project token: status %d, decode error %v", mint.Code, err)
	}
	guestMint := request(http.MethodPost, "/api/v4/projects/acme%2Fplatform%2Finfra/access_tokens", owner,
		`{"name":"guest","scopes":["read_repository"],"access_level":10,"expires_at":"`+expires+`"}`)
	var guestToken struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(guestMint.Body.Bytes(), &guestToken); err != nil ||
		guestMint.Code != http.StatusCreated || guestToken.Token == "" {
		t.Fatalf("mint Guest token: status %d, decode error %v", guestMint.Code, err)
	}
	server := fake.Start()
	t.Cleanup(server.Close)
	clone, err := git.Init(memory.NewStorage(), memfs.New())
	if err != nil {
		t.Fatal(err)
	}
	remote := git.NewRemote(clone.Storer, &config.RemoteConfig{
		Name: "origin", URLs: []string{server.URL + "/acme/platform/infra.git"},
	})
	opts := &git.FetchOptions{
		RefSpecs: []config.RefSpec{config.RefSpec(first + ":refs/heads/infraweave")},
		Depth:    1,
		Tags:     git.NoTags,
		CABundle: server.CA,
		Auth:     &githttp.BasicAuth{Username: "oauth2", Password: token.Token},
	}
	if err := remote.FetchContext(t.Context(), opts); !errors.Is(err, git.ErrExactSHA1NotSupported) {
		t.Fatalf("initial exact-SHA request: %v, want full-fetch fallback", err)
	}
	opts.RefSpecs = []config.RefSpec{"+refs/heads/*:refs/remotes/origin/*"}
	opts.Depth = 0
	if err := remote.FetchContext(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	commit, err := clone.CommitObject(plumbing.NewHash(first))
	if err != nil {
		t.Fatal(err)
	}
	file, err := commit.File("infra/main.tf")
	if err != nil {
		t.Fatal(err)
	}
	contents, err := file.Contents()
	if err != nil || contents != "first" {
		t.Fatalf("fetched delivered commit contents = %q, error %v", contents, err)
	}
	refs := "/acme/platform/infra.git/info/refs?service=git-upload-pack"
	for _, test := range []struct {
		name, method, path, token string
		status                    int
	}{
		{"missing", http.MethodGet, refs, "", http.StatusUnauthorized},
		{"bad", http.MethodGet, refs, "glpat-invalid", http.StatusUnauthorized},
		{"metadata-only", http.MethodGet, refs, metadata, http.StatusForbidden},
		{"reader", http.MethodGet, refs, reader, http.StatusOK},
		{"reporter", http.MethodGet, refs, reporter, http.StatusOK},
		{"guest", http.MethodGet, refs, guest, http.StatusNotFound},
		{"minted-guest", http.MethodGet, refs, guestToken.Token, http.StatusNotFound},
		{"nonmember", http.MethodGet, refs, outsider, http.StatusNotFound},
		{"minted", http.MethodGet, refs, token.Token, http.StatusOK},
		{"wrong-project", http.MethodGet, "/acme/other.git/info/refs?service=git-upload-pack", token.Token, http.StatusNotFound},
		{"missing-project", http.MethodGet, "/acme/missing.git/info/refs?service=git-upload-pack", reader, http.StatusNotFound},
		{"push-advertisement", http.MethodGet, "/acme/platform/infra.git/info/refs?service=git-receive-pack", reader, http.StatusNotFound},
		{"push", http.MethodPost, "/acme/platform/infra.git/git-receive-pack", reader, http.StatusNotFound},
		{"invalid-upload", http.MethodPost, "/acme/platform/infra.git/git-upload-pack", reader, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := request(test.method, test.path, test.token, "broken").Code; got != test.status {
				t.Fatalf("HTTP status %d, want %d", got, test.status)
			}
		})
	}
	revoked := request(http.MethodDelete,
		fmt.Sprintf("/api/v4/projects/acme%%2Fplatform%%2Finfra/access_tokens/%d", token.ID), owner, "")
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("revoke: status %d", revoked.Code)
	}
	if got := request(http.MethodGet, refs, token.Token, "").Code; got != http.StatusUnauthorized {
		t.Fatalf("revoked token status %d, want 401", got)
	}
	if got, err := repo.File(first, "infra/main.tf"); err != nil || string(got) != "first" {
		t.Fatalf("original repository changed: %q, error %v", got, err)
	}
}
