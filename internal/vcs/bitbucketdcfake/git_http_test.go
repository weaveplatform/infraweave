package bitbucketdcfake_test

import (
	"encoding/json"
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

	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketdcfake"
)

func TestUnitAuthenticatedGit(t *testing.T) {
	t.Parallel()
	fake := bitbucketdcfake.New()
	owner := fake.AddToken("owner")
	outsider := fake.AddToken("outsider")
	fake.Grant("ACME", "infra", "owner", bitbucketdcfake.Read)
	fake.Repo("ACME", "hidden").Commit("main", "hidden", map[string]string{"main.tf": "hidden"})
	repo := fake.Repo("ACME", "infra")
	first := repo.Commit("main", "first", map[string]string{"infra/main.tf": "first"})
	repo.Commit("main", "second", map[string]string{"infra/main.tf": "second"})
	server := fake.Start()
	t.Cleanup(server.Close)
	clone, err := git.Init(memory.NewStorage(), memfs.New())
	if err != nil {
		t.Fatal(err)
	}
	remote := git.NewRemote(clone.Storer, &config.RemoteConfig{Name: "origin", URLs: []string{server.URL + "/scm/acme/infra.git"}})
	opts := &git.FetchOptions{
		RefSpecs: []config.RefSpec{config.RefSpec(first + ":refs/heads/infraweave")},
		Depth:    1,
		Tags:     git.NoTags,
		CABundle: server.CA,
		Auth:     &githttp.BasicAuth{Username: "owner", Password: owner},
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
	refs := "/scm/acme/infra.git/info/refs?service=git-upload-pack"
	for _, tc := range []struct {
		name, path, user, password string
		want                       int
	}{
		{"missing", refs, "", "", 401},
		{"wrong token", refs, "owner", "wrong", 401},
		{"reader", refs, "owner", owner, 200},
		{"nonmember", refs, "owner", outsider, 404},
		{"hidden", "/scm/acme/hidden.git/info/refs?service=git-upload-pack", "owner", owner, 404},
		{"push advertisement", "/scm/acme/infra.git/info/refs?service=git-receive-pack", "owner", owner, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := request(http.MethodGet, tc.path, tc.user, tc.password); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
	if got := request(http.MethodPost, "/scm/acme/infra.git/git-receive-pack", "owner", owner); got != 404 {
		t.Fatal("write route exists", got)
	}
	if got := request(http.MethodPost, "/scm/acme/infra.git/git-upload-pack", "owner", owner); got != 400 {
		t.Fatal("invalid upload accepted", got)
	}
	if got, err := repo.File(first, "infra/main.tf"); err != nil || string(got) != "first" {
		t.Fatal("serving mutated original graph", string(got), err)
	}
}

func TestUnitGitRepositoryTokenScopeAndRevocation(t *testing.T) {
	fake := bitbucketdcfake.New()
	owner := fake.AddToken("owner")
	fake.Grant("ACME", "infra", "owner", bitbucketdcfake.Admin)
	fake.Repo("ACME", "infra").Commit("main", "first", map[string]string{"main.tf": "first"})
	fake.Repo("ACME", "other").Commit("main", "other", map[string]string{"main.tf": "other"})
	request := func(method, path, user, password, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
		if user == "bearer" {
			r.Header.Set("Authorization", "Bearer "+password)
		} else {
			r.SetBasicAuth(user, password)
		}
		w := httptest.NewRecorder()
		fake.Handler().ServeHTTP(w, r)
		return w
	}
	mint := request(http.MethodPut, "/rest/access-tokens/1.0/projects/ACME/repos/infra", "bearer", owner, `{"name":"run","permissions":["REPO_READ"],"expiryDays":1}`)
	var token struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(mint.Body.Bytes(), &token); err != nil || mint.Code != 200 || token.Token == "" {
		t.Fatal(mint.Code, err)
	}
	refs := "/scm/acme/infra.git/info/refs?service=git-upload-pack"
	if got := request(http.MethodGet, refs, "x-token-auth", token.Token, "").Code; got != 200 {
		t.Fatal("minted repository reader refused", got)
	}
	fake.Grant("ACME", "other", "access-token-"+token.ID, bitbucketdcfake.Read)
	if got := request(http.MethodGet, "/scm/acme/other.git/info/refs?service=git-upload-pack", "x-token-auth", token.Token, "").Code; got != 404 {
		t.Fatal("minted token escaped repository scope", got)
	}
	revoked := request(http.MethodDelete, "/rest/access-tokens/1.0/projects/ACME/repos/infra/"+token.ID, "bearer", owner, "")
	if revoked.Code != 204 {
		t.Fatal(revoked.Code)
	}
	if got := request(http.MethodGet, refs, "x-token-auth", token.Token, "").Code; got != 401 {
		t.Fatal("revoked repository token still clones", got)
	}
}
