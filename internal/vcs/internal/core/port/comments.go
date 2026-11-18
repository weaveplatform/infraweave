package port

import (
	"context"
	"errors"
)

// ErrCommentInput refuses an incomplete comment target or deduplication key.
var ErrCommentInput = errors.New("vcs: invalid pull request comment input")

// CommentReference retains native identities needed to edit or minimize a
// comment. NodeID is GitHub's GraphQL identity; ThreadID is Azure DevOps's
// containing thread. Version is Data Center's optimistic edit version.
// None of these fields contains a credential or a local account identity.
type CommentReference struct {
	ID       string `json:"id"`
	NodeID   string `json:"nodeId,omitempty"`
	ThreadID string `json:"threadId,omitempty"`
	Version  int64  `json:"version,omitempty"`
}

// CommentRequest identifies one native pull request and one logical comment.
// Body includes its final hidden marker. Existing is an owned recorded
// reference when available; an uncertain earlier post is recovered by marker.
type CommentRequest struct {
	PullRequest string
	DedupKey    string
	Body        string
	Existing    *CommentReference
}

// Comments performs native pull request comment effects. Only queued feedback
// handlers call these methods; event target selection performs no provider IO.
// Implementations bound requests and marker-search pagination, searching newest
// first, and retain the provider identities needed for later updates.
type Comments interface {
	// Post recovers an existing marker before creating a new comment. A retry
	// answers the same reference instead of posting a second comment.
	Post(ctx context.Context, repo Repository, comment CommentRequest) (CommentReference, error)
	// Upsert edits a recorded or marker-recovered comment, or creates it when
	// none exists. Notification-policy callers use their own stable keys.
	Upsert(ctx context.Context, repo Repository, comment CommentRequest) (CommentReference, error)
	// Minimize marks GitHub comments outdated. Providers without an equivalent
	// return success without changing the existing comment.
	Minimize(ctx context.Context, repo Repository, comment CommentReference) error
}
