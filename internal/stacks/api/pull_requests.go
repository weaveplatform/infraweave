package api

import (
	"context"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// PullRequestReader supplies the open requests that have runs on each stack.
// The owning VCS domain implements this back edge; the stack read has already
// checked reach. Input settings select the current repository and binding.
type PullRequestReader interface {
	PullRequests(context.Context, kernel.AccountID, []Settings) (map[kernel.ULID][]PullRequest, error)
}

// PullRequest is a request's public metadata and the matching run identities.
// Runs are ordered with the current head first, then newest first.
type PullRequest struct {
	ID, AuthorLogin, Title, URL, HeadBranch, HeadBranchURL, HeadSHA string
	Draft                                                           bool
	CreatedAt, UpdatedAt                                            time.Time
	Runs                                                            []kernel.ULID
}
