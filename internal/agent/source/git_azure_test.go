package source

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

func TestUnitAzureCloneBoundary(t *testing.T) {
	badRequest := plumbing.NewUnexpectedError(&githttp.Err{Response: &http.Response{StatusCode: http.StatusBadRequest}})
	for _, host := range []string{"dev.azure.com", "organization.visualstudio.com", "DEV.AZURE.COM"} {
		if !azureCloneRequired("https://"+host+"/project/_git/repo", badRequest) {
			t.Fatal("Azure HTTP 400 did not select the full clone", host)
		}
	}
	for _, host := range []string{"gitlab.com", "dev.azure.com.attacker.example", "visualstudio.com.attacker.example", "%"} {
		if azureCloneRequired("https://"+host+"/repo", badRequest) {
			t.Fatal("selected an unrelated host", host)
		}
	}
	for _, err := range []error{
		nil, errors.New("status code: 400"), transport.ErrAuthenticationRequired,
		&githttp.Err{}, &githttp.Err{Response: &http.Response{StatusCode: http.StatusInternalServerError}},
	} {
		if azureCloneRequired("https://dev.azure.com/org/repo", err) {
			t.Fatal("selected an unrelated error", err)
		}
	}
}

func TestUnitCloneAzure(t *testing.T) {
	r := newRepo(t)
	g := newGitServerWithAzure(t, r.s, true, true)
	url := g.srv.URL + "/repo.git"
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	opts := &git.FetchOptions{
		Auth:     &githttp.BasicAuth{Username: user, Password: token},
		RefSpecs: []config.RefSpec{"+refs/heads/*:refs/remotes/origin/*"},
	}
	remote := git.NewRemote(repo.Storer, &config.RemoteConfig{Name: "origin", URLs: []string{url}})
	if err := remote.FetchContext(t.Context(), opts); !azureCloneRequired("https://dev.azure.com/org/repo", err) {
		t.Fatal("ordinary negotiation did not reproduce Azure's HTTP 400", err)
	}
	before := slices.Clone(transport.UnsupportedCapabilities)
	if err := cloneAzure(t.Context(), repo, url, opts); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(before, transport.UnsupportedCapabilities) {
		t.Fatal("clone changed global capabilities")
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.Checkout(&git.CheckoutOptions{Hash: plumbing.NewHash(r.first)}); err != nil {
		t.Fatal("the pack omitted the requested ancestor", err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "main.tf")); err != nil || string(data) != "one\n" {
		t.Fatal("the requested commit was not checked out", err)
	}
	holdsNo(t, dir, token)
	holdsNo(t, dir, url)
	wrong := &git.FetchOptions{Auth: &githttp.BasicAuth{Username: user, Password: "wrong"}}
	if err := cloneAzure(t.Context(), repo, url, wrong); !errors.Is(err, transport.ErrAuthenticationRequired) {
		t.Fatal("invalid credentials were accepted", err)
	}
}
