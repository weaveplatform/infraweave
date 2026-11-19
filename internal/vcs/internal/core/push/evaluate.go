package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
)

var (
	errEvaluation = errors.New("vcs: invalid event evaluation job")
	errExpired    = errors.New("vcs: event evaluation expired")
	errChanged    = errors.New("vcs: event stack changed while acquiring its owner")
)

// Evaluated applies a stack's ordered prepared events and completes its queue
// claim in one transaction. A later capped item preserves earlier admitted runs.
func (s *Service) Evaluated(ctx context.Context, job queue.Job) error {
	var input Evaluation
	if json.Unmarshal(job.Payload, &input) != nil || input.Account.IsZero() || input.Stack.IsZero() || input.Delivery.IsZero() ||
		input.Deadline.IsZero() || input.Account.ULID != job.Account {
		return queue.Permanent(errEvaluation) //nolint:wrapcheck // a fixed malformed-payload refusal
	}
	if !s.d.Now().Before(input.Deadline) {
		return s.finish(ctx, job, input, Expired)
	}
	work, cancel := context.WithTimeout(ctx, input.Deadline.Sub(s.d.Now()))
	defer cancel()
	err := tx.Do(work, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		if err := s.d.Jobs.HoldTx(ctx, job); err != nil {
			return err //nolint:wrapcheck // the queue names the lost claim
		}
		return s.evaluate(ctx, job, input)
	})
	if errors.Is(err, errExpired) || errors.Is(err, context.DeadlineExceeded) && !s.d.Now().Before(input.Deadline) {
		return s.finish(ctx, job, input, Expired)
	}
	if err != nil && !errors.Is(err, queue.ErrLeaseLost) && job.MaxAttempts > 0 && job.Attempt >= job.MaxAttempts {
		if mark := s.failed(ctx, job, input); mark != nil {
			return mark
		}
	}
	return err //nolint:wrapcheck // each fenced step names its boundary
}

func (s *Service) evaluate(ctx context.Context, job queue.Job, input Evaluation) error {
	meta, found, err := s.d.Store.EventContext(ctx, input.Account, input.Delivery)
	if err != nil {
		return err //nolint:wrapcheck // the store names the owner check
	}
	if !found || meta.Replays != input.Replays || meta.State != intake.Dispatched || !s.d.Now().Before(meta.Deadline) {
		return errExpired
	}
	if meta.Deadline.Before(input.Deadline) {
		input.Deadline = meta.Deadline
	}
	events, err := s.d.Store.Prepared(ctx, input.Account, input.Delivery)
	if err != nil {
		return err //nolint:wrapcheck // the store names the prepared read
	}
	if len(events) == 0 {
		return errExpired
	}
	// Lifecycle changes hold the integration before repointing stacks. Keep
	// that order through cap feedback's identity lookup in this transaction.
	use, found, err := s.d.Integrations.HeldEventUse(ctx, input.Account, meta.Integration)
	if err != nil {
		return err //nolint:wrapcheck // integrations names the binding hold
	}
	if !found || use.Provider != meta.Provider {
		return errExpired
	}
	stack, err := s.lockedStack(ctx, input)
	if err != nil {
		return err
	}
	if !uses(stack, use) {
		return errExpired
	}
	matched := false
	for _, event := range events {
		if !s.d.Now().Before(input.Deadline) {
			return errExpired
		}
		if event.Event.Repository.Namespace != stack.Namespace || event.Event.Repository.Name != stack.Repository {
			continue
		}
		matched = true
		decision, err := s.apply(ctx, input, stack, event, meta.Integration)
		if err != nil {
			return err
		}
		if recordIgnored(event.Event, stack, decision) {
			if s.d.Ignored == nil {
				return fmt.Errorf("%w: ignored trigger recording is unavailable", kernel.ErrUnavailable)
			}
			if err := s.d.Ignored.Record(ctx, input.Account, input.Stack, event.Commit, decision.Reason); err != nil {
				return fmt.Errorf("vcs: record ignored evaluation: %w", err)
			}
		}
		s.report(ctx, input, decision)
	}
	if !matched {
		return errExpired
	}
	return s.d.Jobs.CompleteTx(ctx, job) //nolint:wrapcheck // the queue names the atomic completion
}

func (s *Service) lockedStack(ctx context.Context, input Evaluation) (stacksapi.Settings, error) {
	stack, err := s.d.Stacks.Settings(ctx, input.Account, input.Stack)
	if errors.Is(err, stacksapi.ErrNotFound) {
		return stack, errExpired
	}
	if err != nil {
		return stack, err //nolint:wrapcheck // stacks names the initial owner read
	}
	if s.d.Spaces == nil {
		return stack, kernel.ErrUnavailable
	}
	if err := s.d.Spaces.Hold(ctx, input.Account, stack.Space); err != nil {
		if errors.Is(err, accessapi.ErrNoSpace) || errors.Is(err, accessapi.ErrNoAccount) {
			return stack, errExpired
		}
		return stack, err //nolint:wrapcheck // access names the owner hold
	}
	locked, err := s.d.Stacks.LockForRun(ctx, input.Account, input.Stack)
	if errors.Is(err, stacksapi.ErrNotFound) {
		return locked, errExpired
	}
	if err != nil {
		return locked, err //nolint:wrapcheck // stacks names the creation lock
	}
	if locked.VCSDetached {
		return locked, errExpired
	}
	if locked.Space != stack.Space {
		return locked, errChanged
	}
	return locked, nil
}

func uses(stack stacksapi.Settings, use stacksapi.Use) bool {
	if stack.VCSDetached {
		return false
	}
	if stack.VCSIntegration != nil {
		return *stack.VCSIntegration == use.Integration
	}
	return use.Default && (stack.Provider == use.Provider || slices.Contains(use.Aliases, stack.Provider))
}

func (s *Service) apply(ctx context.Context, input Evaluation, stack stacksapi.Settings, event Prepared, integration kernel.ULID) (Decision, error) {
	decision := Default(event.Event, stack, event.Files)
	if event.Event.Type == "push" {
		if s.d.Supersede == nil {
			return decision, fmt.Errorf("%w: event superseding is unavailable", kernel.ErrUnavailable)
		}
		if _, err := s.d.Supersede.Supersede(ctx, input.Account, input.Stack, event.Event.Branch, event.Commit.Hash); err != nil {
			return decision, err //nolint:wrapcheck // runs names the existing stop-request write
		}
		if event.Event.Branch == stack.Branch {
			head := event.Commit
			head.Branch = ""
			if err := s.d.Stacks.SetTracked(ctx, input.Account, input.Stack, &head, nil, ""); err != nil {
				return decision, err //nolint:wrapcheck // stacks names the head-only projection
			}
		}
	}
	if decision.Outcome != Track && decision.Outcome != Propose {
		return decision, nil
	}
	if s.d.Creator == nil || s.d.Limits == nil {
		return decision, fmt.Errorf("%w: event run creation is unavailable", kernel.ErrUnavailable)
	}
	kind := kernel.RunTypeProposed
	if decision.Outcome == Track {
		kind = kernel.RunTypeTracked
	}
	created, err := s.d.Creator.Create(ctx, runsapi.CreateRequest{
		Account: input.Account, Stack: input.Stack, Type: kind, Commit: runsapi.Commit(event.Commit),
		TriggeredBy: "vcs/commit", Mode: runsapi.OncePerCommit,
		BeforeCreate: func(ctx context.Context) error { return s.admit(ctx, input) },
	})
	switch {
	case errors.Is(err, ErrCapped):
		if s.d.Checks != nil {
			cap, ok := errors.AsType[*capRefusal](err)
			if !ok {
				return decision, fmt.Errorf("%w: cap has no budget", kernel.ErrUnavailable)
			}
			if err := s.d.Checks.Capped(ctx, input.Account, integration, input.Stack, event.Commit.Hash, kind, cap.limit); err != nil {
				return decision, fmt.Errorf("VCS capped check: %w", err)
			}
		}
		return Decision{Capped, RunLimitReached}, nil
	case errors.Is(err, runsapi.ErrCreationRefused):
		return Decision{Outcome: Refused}, nil
	case err != nil:
		return decision, err //nolint:wrapcheck // runs names unexpected creation failures
	}
	if decision.Outcome == Track {
		commit := event.Commit
		commit.Branch = ""
		if err := s.d.Stacks.SetTracked(ctx, input.Account, input.Stack, nil, &commit, "infraweave"); err != nil {
			return decision, err //nolint:wrapcheck // stacks names the tracked projection
		}
	}
	if !created.Created {
		return Decision{Outcome: Duplicate}, nil
	}
	return decision, nil
}

func (s *Service) admit(ctx context.Context, input Evaluation) error {
	if !s.d.Now().Before(input.Deadline) {
		return errExpired
	}
	limit, err := s.d.Limits.Effective(ctx, input.Account)
	if err != nil {
		return err
	}
	allowed, err := s.d.Limits.Store.ReserveRun(ctx, input.Account, input.Delivery, input.Replays, limit)
	if err != nil {
		return err //nolint:wrapcheck // the store names the conditional admission
	}
	if !allowed {
		return &capRefusal{limit: limit}
	}
	return nil
}

func (s *Service) report(ctx context.Context, input Evaluation, decision Decision) {
	tx.AfterCommit(ctx, func(ctx context.Context) {
		args := []any{"delivery", input.Delivery.String(), "stack", input.Stack.String(), "outcome", decision.Outcome, "reason", decision.Reason}
		if decision.Outcome == Refused {
			s.d.Log.WarnContext(ctx, "VCS event run refused by stack", args...)
			return
		}
		// A duplicate is silent above debug, including after the run cap.
		s.d.Log.DebugContext(ctx, "VCS event evaluated", args...)
	})
}

func (s *Service) finish(ctx context.Context, job queue.Job, input Evaluation, outcome string) error {
	return tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error { //nolint:wrapcheck // the queue names completion failures
		if err := s.d.Jobs.CompleteTx(ctx, job); err != nil {
			return err //nolint:wrapcheck // the queue names the lost claim
		}
		s.report(ctx, input, Decision{Outcome: outcome})
		return nil
	})
}

// failed records an exhausted child without changing the state siblings use.
//
//nolint:wrapcheck // queue and store errors retain their fenced boundary
func (s *Service) failed(ctx context.Context, job queue.Job, input Evaluation) error {
	return tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		if err := s.d.Jobs.HoldTx(ctx, job); err != nil {
			return err
		}
		return s.d.Store.EvaluationFailed(ctx, input.Account, input.Delivery, input.Replays)
	})
}

// capRefusal retains the exact budget checked by the conditional reservation;
// reading the mutable account setting again could report a different limit.
type capRefusal struct{ limit int }

func (*capRefusal) Error() string { return ErrCapped.Error() }
func (*capRefusal) Unwrap() error { return ErrCapped }
