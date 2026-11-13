package source

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// git checks commit out into dir from the repository at url. The launcher
// runs on the host, whose image may hold no git, so go-git fetches in
// process, hashing with its own SHA-1 rather than crypto/sha1, which
// fips140=only refuses. The remote is never saved, so .git/config names
// neither the URL nor the credential, which go-git sends as the request's
// Authorization header alone. Only the commit is fetched, with no history
// before it, from a server that serves a commit by its hash, as GitHub,
// GitLab and Gitea do; from one that does not, every branch is fetched,
// whose histories hold the commit of any run on one of them.
func (f *Fetcher) git(
	ctx context.Context,
	dir, url, commit string,
	cred *wire.RunSpecSourceCredential,
) error {
	if !plumbing.IsHash(commit) {
		return fmt.Errorf("%w: %q", ErrCommit, commit)
	}
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		return fmt.Errorf("the workspace: %w", err)
	}
	remote := git.NewRemote(repo.Storer, &config.RemoteConfig{Name: "origin", URLs: []string{url}})
	opts := &git.FetchOptions{
		RefSpecs: []config.RefSpec{config.RefSpec(commit + ":refs/heads/infraweave")},
		Depth:    f.depth,
		Tags:     git.NoTags,
		CABundle: f.ca,
	}
	if cred != nil {
		opts.Auth = &githttp.BasicAuth{Username: cred.Username, Password: cred.Token}
	}
	err = remote.FetchContext(ctx, opts)
	if errors.Is(err, git.ErrExactSHA1NotSupported) {
		opts.RefSpecs = []config.RefSpec{"+refs/heads/*:refs/remotes/origin/*"}
		opts.Depth = 0
		err = remote.FetchContext(ctx, opts)
	}
	if err != nil {
		return err //nolint:wrapcheck // the caller names the step and the host
	}
	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("the workspace: %w", err)
	}
	if err := wt.Checkout(
		&git.CheckoutOptions{Hash: plumbing.NewHash(commit), Force: true},
	); err != nil {
		return fmt.Errorf("check out: %w", err)
	}
	return nil
}
