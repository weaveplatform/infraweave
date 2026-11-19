package azuredevopsfake_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
)

func TestUnitAuthenticatedGit(t *testing.T) {
	t.Parallel()
	fake := azuredevopsfake.New()
	owner := fake.AddToken("owner", azuredevopsfake.ScopeCode)
	outsider := fake.AddToken("outsider", azuredevopsfake.ScopeCode)
	fake.Grant("Platform", "owner", azuredevopsfake.Reader)
	fake.Repo("Hidden", "infra").Commit("main", "hidden", map[string]string{"main.tf": "hidden"})
	repo := fake.Repo("Platform", "infra")
	first := repo.Commit("main", "first", map[string]string{"infra/main.tf": "first"})
	repo.Commit("main", "second", map[string]string{"infra/main.tf": "second"})
	server := fake.Start()
	t.Cleanup(server.Close)
	clone, err := git.Init(memory.NewStorage(), memfs.New())
	if err != nil {
		t.Fatal(err)
	}
	remote := git.NewRemote(clone.Storer, &config.RemoteConfig{Name: "origin", URLs: []string{server.URL + "/acme/Platform/_git/infra"}})
	opts := &git.FetchOptions{
		RefSpecs: []config.RefSpec{config.RefSpec(first + ":refs/heads/infraweave")},
		Depth:    1,
		Tags:     git.NoTags,
		CABundle: server.CA,
		Auth:     &githttp.BasicAuth{Username: "pat", Password: owner},
	}
	if err := remote.FetchContext(t.Context(), opts); !errors.Is(err, git.ErrExactSHA1NotSupported) {
		t.Fatal("expected launcher's full-fetch fallback", err)
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
	content, err := file.Contents()
	if err != nil || content != "first" {
		t.Fatal("delivered immutable commit lost", content, err)
	}
	request := func(method, path, user, password string) int {
		r := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader("broken"))
		if password != "" {
			r.SetBasicAuth(user, password)
		}
		w := httptest.NewRecorder()
		fake.Handler().ServeHTTP(w, r)
		return w.Code
	}
	refs := "/acme/Platform/_git/infra/info/refs?service=git-upload-pack"
	for _, tc := range []struct {
		name, path, user, password string
		want                       int
	}{
		{"missing", refs, "", "", 401},
		{"wrong token", refs, "pat", "wrong", 401},
		{"reader", refs, "pat", owner, 200},
		{"nonmember", refs, "pat", outsider, 404},
		{"hidden", "/acme/Hidden/_git/infra/info/refs?service=git-upload-pack", "pat", owner, 404},
		{"push advertisement", "/acme/Platform/_git/infra/info/refs?service=git-receive-pack", "pat", owner, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := request(http.MethodGet, tc.path, tc.user, tc.password); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
	if got := request(http.MethodPost, "/acme/Platform/_git/infra/git-receive-pack", "pat", owner); got != 404 {
		t.Fatal("write route exists", got)
	}
	if got := request(http.MethodPost, "/acme/Platform/_git/infra/git-upload-pack", "pat", owner); got != 400 {
		t.Fatal("invalid upload accepted", got)
	}
	if got, err := repo.File(first, "infra/main.tf"); err != nil || string(got) != "first" {
		t.Fatal("serving mutated original graph", string(got), err)
	}
}

func TestUnitGitRequiresCodeScope(t *testing.T) {
	fake := azuredevopsfake.New()
	metadata := fake.AddToken("owner", azuredevopsfake.ScopeProject)
	fake.Grant("Platform", "owner", azuredevopsfake.Reader)
	fake.Repo("Platform", "infra").Commit("main", "first", map[string]string{"main.tf": "first"})
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/acme/Platform/_git/infra/info/refs?service=git-upload-pack", nil)
	r.SetBasicAuth("pat", metadata)
	w := httptest.NewRecorder()
	fake.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("metadata-only token can clone", w.Code)
	}
}
