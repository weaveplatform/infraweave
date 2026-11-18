package port

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
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
	// Presentation supplies pure rendering and body-budget facts without IO.
	Presentation() CommentPresentation
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

// CommentPresentation describes the adapter's accepted body budget and rendering.
// Zero means that dimension imposes no additional bound. Budgets include the
// complete body and marker; they need not equal an undocumented native maximum.
type CommentPresentation struct {
	MaxBytes int
	MaxRunes int
	MaxUTF16 int
	Details  bool
}

// CommentMarker constructs the final footer from a bounded plain identifier.
// Restricting the alphabet prevents HTML comment delimiters and line injection.
func CommentMarker(key string) (string, error) {
	if len(key) == 0 || len(key) > 256 || strings.Contains(key, "--") {
		return "", ErrCommentInput
	}
	for _, c := range key {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune(":._/-", c) {
			continue
		}
		return "", ErrCommentInput
	}
	return "<!-- infraweave:" + key + " -->", nil
}

// HasCommentMarker matches only a whole final footer line, optionally followed
// by one newline. Quoted markers and examples within a planning log do not match.
func HasCommentMarker(body, key string) bool {
	marker, err := CommentMarker(key)
	if err != nil {
		return false
	}
	body = strings.TrimSuffix(body, "\n")
	return body == marker || strings.HasSuffix(body, "\n"+marker)
}

// ValidateCommentRequest refuses malformed input before authentication or IO.
func ValidateCommentRequest(comment CommentRequest, presentation CommentPresentation) error {
	id, err := strconv.ParseInt(comment.PullRequest, 10, 64)
	if err != nil || id <= 0 || !utf8.ValidString(comment.Body) || !HasCommentMarker(comment.Body, comment.DedupKey) ||
		presentation.MaxBytes > 0 && len(comment.Body) > presentation.MaxBytes ||
		presentation.MaxRunes > 0 && utf8.RuneCountInString(comment.Body) > presentation.MaxRunes {
		return ErrCommentInput
	}
	if presentation.MaxUTF16 > 0 {
		units := 0
		for _, c := range comment.Body {
			units++
			if c > 0xffff {
				units++
			}
		}
		if units > presentation.MaxUTF16 {
			return ErrCommentInput
		}
	}
	return nil
}

// ErrCommentSearchIncomplete refuses malformed or unreliable recovery
// responses. Exhausting the normal newest-first window alone permits a fresh
// create; a known recorded reference is read directly outside that window.
var ErrCommentSearchIncomplete = errors.New("vcs: pull request comment recovery incomplete")

// ErrCommentResponse refuses native success without a usable comment identity.
var ErrCommentResponse = errors.New("vcs: invalid pull request comment response")

// CommentSearchPages bounds recovery requests independently of the call timeout.
const CommentSearchPages = 10
