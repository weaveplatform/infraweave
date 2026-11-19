package vcslab

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// GitCredentials are the run's already-existing seed credentials; they are
// deliberately separate from the integration's narrower provider credential.
type GitCredentials struct{ User, Token, CAFile string }

func (GitCredentials) String() string             { return "[lab Git credential]" }
func (GitCredentials) Format(s fmt.State, _ rune) { _, _ = s.Write([]byte("[lab Git credential]")) }

func (n *Native) cloneURL() string {
	ns, repo := url.PathEscape(n.Namespace), url.PathEscape(n.Repository)
	switch n.Provider {
	case "github":
		return "https://github.com/" + ns + "/" + repo + ".git"
	case "bitbucket-cloud":
		return "https://bitbucket.org/" + ns + "/" + repo + ".git"
	case "bitbucket-dc":
		return n.Host + "/scm/" + strings.ToLower(ns) + "/" + repo + ".git"
	case "azure-devops":
		return n.Host + "/" + ns + "/_git/" + repo
	default:
		return n.Host + "/" + n.Namespace + "/" + repo + ".git"
	}
}

// Push creates a genuine Git commit and push, letting the provider generate and
// sign its delivery. Source tests run before these mutations; Terraform destroys
// the disposable repository afterwards. No synthetic webhook replaces this path.
func (n *Native) Push(ctx context.Context, parent, branch, marker string, credential GitCredentials) (string, error) {
	if credential.User == "" || credential.Token == "" || plumbing.NewBranchReferenceName(branch).Validate() != nil {
		return "", ErrNative
	}
	dir, err := os.MkdirTemp(parent, "native-push-")
	if err != nil {
		return "", ErrNative
	}
	defer func() { _ = os.RemoveAll(dir) }()
	var ca []byte
	if credential.CAFile != "" {
		ca, err = os.ReadFile(credential.CAFile)
		if err != nil {
			return "", ErrNative
		}
	}
	auth := &githttp.BasicAuth{Username: credential.User, Password: credential.Token}
	repo, err := git.PlainCloneContext(ctx, dir, false, &git.CloneOptions{URL: n.cloneURL(), ReferenceName: plumbing.NewBranchReferenceName("main"), Auth: auth, CABundle: ca})
	if err != nil {
		return "", fmt.Errorf("%w: clone disposable repository", ErrNative)
	}
	work, err := repo.Worktree()
	if err != nil {
		return "", ErrNative
	}
	if branch != "main" {
		head, err := repo.Head()
		if err != nil {
			return "", ErrNative
		}
		hash := head.Hash()
		if remote, err := repo.Reference(plumbing.NewRemoteReferenceName("origin", branch), true); err == nil {
			hash = remote.Hash()
		}
		if err := work.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName(branch), Create: true, Hash: hash}); err != nil {
			return "", ErrNative
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte("resource \"terraform_data\" \"lab\" {\n  input = \""+marker+"\"\n}\n"), 0o600); err != nil {
		return "", ErrNative
	}
	if _, err := work.Add("main.tf"); err != nil {
		return "", ErrNative
	}
	hash, err := work.Commit("VCS lab verification", &git.CommitOptions{Author: &object.Signature{Name: "infraweave lab", Email: "lab@example.com", When: time.Now().UTC()}})
	if err != nil {
		return "", ErrNative
	}
	ref := plumbing.NewBranchReferenceName(branch).String()
	if err := repo.PushContext(ctx, &git.PushOptions{Auth: auth, CABundle: ca, RefSpecs: []config.RefSpec{config.RefSpec(ref + ":" + ref)}}); err != nil {
		return "", fmt.Errorf("%w: push disposable repository", ErrNative)
	}
	return hash.String(), nil
}
