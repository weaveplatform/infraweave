package feedback

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Upsert queues the owned pull request's notification comment. Plan keys are
// reserved so policy updates cannot overwrite or minimize a run's plan comment.
func (s *Service) Upsert(ctx context.Context, acct kernel.AccountID, input vcsapi.PullRequestComment) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	marker, err := port.CommentMarker(input.DedupKey)
	if err != nil || acct.IsZero() || input.PullRequest.IsZero() || input.Body == "" || strings.HasPrefix(input.DedupKey, "plan:") || s.comments == nil {
		return vcsapi.ErrCommentInput
	}
	if s.hold == nil {
		return ErrAccountHold
	}
	if err := s.hold(ctx, acct); err != nil {
		return fmt.Errorf("VCS comment owner hold: %w", err)
	}
	pr, err := s.comments.Store.CommentPullRequest(ctx, acct, input.PullRequest)
	if errors.Is(err, ErrNoComment) {
		return vcsapi.ErrCommentNotFound
	}
	if err != nil {
		return err //nolint:wrapcheck // store names its read
	}
	provider := s.comments.Providers[pr.Provider]
	if provider == nil {
		return vcsapi.ErrCommentInput
	}
	body := input.Body
	if !port.HasCommentMarker(body, input.DedupKey) {
		body = strings.TrimRight(body, "\n") + "\n\n" + marker
	}
	if port.ValidateCommentRequest(port.CommentRequest{PullRequest: pr.Number, DedupKey: input.DedupKey, Body: body}, provider.Presentation()) != nil {
		return vcsapi.ErrCommentInput
	}
	in, found, err := s.comments.Integrations.Binding(ctx, acct, pr.Provider, &pr.Integration)
	if err != nil {
		return fmt.Errorf("VCS comment binding: %w", err)
	}
	if !found {
		return vcsapi.ErrCommentNotFound
	}
	c := Comment{
		Account: acct, ID: kernel.NewULID(), Target: pr, DedupKey: input.DedupKey,
		NativeHost: commentHost(in.Settings), Body: body, Revision: 1,
	}
	return s.queueComment(ctx, c)
}

var _ vcsapi.Comments = (*Service)(nil)
