package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// ErrCommentReport contains no native response body or credential.
var ErrCommentReport = errors.New("VCS comment report failed")

type commentJob struct {
	Account kernel.AccountID `json:"account"`
	ID      kernel.ULID      `json:"id"`
}

// Commented owns a non-retrying completion transaction and a bounded native
// effect. A later claim recovers a lost response by the body's final marker.
func (s *Service) Commented(ctx context.Context, job queue.Job) error {
	if tx.In(ctx) {
		return fmt.Errorf("%w: inherited transaction", ErrCommentReport)
	}
	var request commentJob
	if s.comments == nil || json.Unmarshal(job.Payload, &request) != nil || request.Account.IsZero() ||
		request.ID.IsZero() || request.Account.ULID != job.Account {
		return queue.Permanent(ErrCommentReport) //nolint:wrapcheck // queue control classification
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return tx.Do(ctx, s.comments.DB, tx.Options{NoRetry: true}, func(ctx context.Context) error { //nolint:wrapcheck // boundaries name each failure
		if s.hold == nil {
			return ErrAccountHold
		}
		if err := s.hold(ctx, request.Account); err != nil {
			return fmt.Errorf("VCS comment owner hold: %w", err)
		}
		if err := s.jobs.HoldTx(ctx, job); err != nil {
			return err //nolint:wrapcheck // queue names its generation fence
		}
		c, err := s.comments.Store.HoldComment(ctx, request.Account, request.ID)
		if errors.Is(err, ErrNoComment) {
			return s.jobs.CompleteTx(ctx, job) //nolint:wrapcheck // deleted metadata has no effect
		}
		if err != nil {
			return err //nolint:wrapcheck // store names its lock
		}
		if c.Reported >= c.Revision || !c.Run.IsZero() && (!c.Target.Open || c.Target.Head != c.Commit) {
			return s.jobs.CompleteTx(ctx, job) //nolint:wrapcheck // stale heads and acknowledged retries settle atomically
		}
		provider := s.comments.Providers[c.Target.Provider]
		if provider == nil {
			return queue.Permanent(ErrCommentReport) //nolint:wrapcheck // queue control classification
		}
		in, found, err := s.comments.Integrations.Use(ctx, c.Account, c.Target.Provider, &c.Target.Integration)
		if err != nil {
			return ErrCommentReport
		}
		if !found {
			return s.jobs.CompleteTx(ctx, job) //nolint:wrapcheck // deleted integration has no effect
		}
		if commentHost(in.Settings) != c.NativeHost {
			return queue.Permanent(ErrCommentReport) //nolint:wrapcheck // retargeting cannot move a recorded effect
		}
		repo := port.Repository{Namespace: c.Target.Namespace, Name: c.Target.Repository, Integration: &in}
		body, err := s.commentBody(ctx, c, provider.Presentation())
		if err != nil {
			return err
		}
		input := port.CommentRequest{PullRequest: c.Target.Number, DedupKey: c.DedupKey, Body: body}
		if c.Reference.ID != "" {
			input.Existing = &c.Reference
		}
		post := provider.Post
		if c.Run.IsZero() {
			post = provider.Upsert
		}
		ref, err := post(ctx, repo, input)
		if err != nil {
			return commentFailure(err)
		}
		if ref.ID == "" {
			return ErrCommentReport
		}
		if err := s.comments.Store.RecordComment(ctx, c, ref); err != nil {
			return err //nolint:wrapcheck // store names its write
		}
		if !c.Run.IsZero() {
			if err := s.minimizePlans(ctx, c, repo, provider); err != nil {
				return err
			}
		}
		return s.jobs.CompleteTx(ctx, job) //nolint:wrapcheck // native identity and completion commit together
	})
}

func (s *Service) commentBody(ctx context.Context, c Comment, presentation port.CommentPresentation) (string, error) {
	if c.Run.IsZero() {
		input := port.CommentRequest{PullRequest: c.Target.Number, DedupKey: c.DedupKey, Body: c.Body}
		if port.ValidateCommentRequest(input, presentation) != nil {
			return "", queue.Permanent(ErrCommentReport) //nolint:wrapcheck // queue control classification
		}
		return c.Body, nil
	}
	if s.comments.Logs == nil {
		return "", ErrCommentReport
	}
	log, err := s.comments.Logs.PlanningLog(ctx, c.Account, c.Run, c.PlanVersion, runsapi.MaxPlanningLogBytes)
	if err != nil {
		return "", ErrCommentReport
	}
	body, err := planCommentBody(planHeading{Public: s.comments.Public, Stack: c.StackSlug, Run: c.Run, State: c.State, Version: c.PlanVersion},
		PlanText{Text: log.Text, CutLines: log.CutLines, PartialLine: log.PartialLine, Unavailable: log.Unavailable}, presentation)
	if err != nil {
		return "", queue.Permanent(ErrCommentReport) //nolint:wrapcheck // queue control classification
	}
	return body, nil
}

func (s *Service) minimizePlans(ctx context.Context, c Comment, repo port.Repository, provider port.Comments) error {
	older, err := s.comments.Store.SupersededComments(ctx, c)
	if err != nil {
		return err //nolint:wrapcheck // store names its read
	}
	for _, previous := range older {
		if err := provider.Minimize(ctx, repo, previous.Reference); err != nil {
			return commentFailure(err)
		}
		if err := s.comments.Store.MinimizedComment(ctx, c.Account, previous.ID); err != nil {
			return err //nolint:wrapcheck // store names its write
		}
	}
	return nil
}

func commentFailure(err error) error {
	if limit, ok := errors.AsType[*port.RateLimited](err); ok {
		return queue.Snooze(max(time.Until(limit.Reset), time.Second)) //nolint:wrapcheck // queue control classification
	}
	return ErrCommentReport
}
