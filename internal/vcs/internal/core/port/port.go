package port

import (
	"context"
	"errors"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

var (
	// ErrNotFound reports a repository, ref or path a Source cannot find.
	ErrNotFound = errors.New("vcs: not found")
	// ErrRepositoryURL reports a repository URL a Source refuses to reach,
	// such as one that is not https or that carries a credential.
	ErrRepositoryURL = errors.New("vcs: repository URL refused")
	// ErrTooLarge reports a file larger than ReadFile returns.
	ErrTooLarge = errors.New("vcs: file too large")
)

// MaxFileSize is the largest file ReadFile returns.
const MaxFileSize = 1 << 20

// Repository is a stack's repository as a Source reaches it: its
// namespace and name, and URL, the repository URL the stack names, which
// a provider without an API, such as raw Git, needs to reach it.
type Repository struct {
	Namespace string
	Name      string
	URL       string
}

// Source reads a provider's repositories. Every provider implements it;
// the server checks capabilities, not provider names. A provider it cannot
// reach is kernel.ErrUnavailable, and a repository URL it refuses is
// ErrRepositoryURL.
//
// ref: decision 0012
type Source interface {
	// ResolveRef resolves ref, a branch, a tag or a full commit SHA, of
	// repo to its commit; ErrNotFound when repo or ref is missing.
	ResolveRef(ctx context.Context, repo Repository, ref string) (vcsapi.Commit, error)
	// ReadFile reads path at ref of repo; ErrNotFound when any of them is
	// missing, and ErrTooLarge when the file exceeds MaxFileSize.
	ReadFile(ctx context.Context, repo Repository, ref, path string) ([]byte, error)
	// AffectedFiles lists the paths that differ between the commits base
	// and head of repo; ErrNotFound when either is missing.
	AffectedFiles(ctx context.Context, repo Repository, base, head string) ([]string, error)
	// SourceURL is the URL a worker fetches commit of repo from.
	SourceURL(repo Repository, commit string) string
}
