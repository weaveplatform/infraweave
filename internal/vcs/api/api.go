package api

import (
	"context"
	"errors"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrRefNotFound reports a branch, tag or commit a stack's repository
// lacks.
var ErrRefNotFound = errors.New("vcs: ref not found")

// Commit is a commit of a stack's repository as runs and the stack view
// read it. Message is the first line of its message; AuthorLogin is nil
// when the provider knows no login for its author, as raw Git never does.
type Commit struct {
	Hash        string
	Tag         string
	URL         string
	Message     string
	AuthorName  string
	AuthorLogin *string
	Timestamp   time.Time
}

// Sources is what other domains call of the vcs domain to reach a stack's
// source. Each method reads the stack in the transaction its context
// carries; stacksapi.ErrNotFound when the account lacks it. A stack whose
// provider infraweave reads no source of is a *kernel.Invalid naming
// /provider.
type Sources interface {
	// Resolve resolves ref, a branch, a tag or a full commit SHA, on the
	// repository of acct's stack id; ErrRefNotFound when the repository
	// lacks it.
	Resolve(
		ctx context.Context,
		acct kernel.AccountID,
		stack kernel.ULID,
		ref string,
	) (Commit, error)
	// SourceURL is the URL a worker fetches commit of acct's stack id
	// from; the server never proxies a source.
	SourceURL(
		ctx context.Context,
		acct kernel.AccountID,
		stack kernel.ULID,
		commit string,
	) (string, error)
}
