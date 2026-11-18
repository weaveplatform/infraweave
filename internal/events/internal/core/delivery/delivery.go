package delivery

import (
	"context"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/events/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

// Consumer is the sinks consumer's stable outbox name.
const Consumer = "events.sinks"

// CommitChecks is the sink whose targets VCS feedback selects.
const CommitChecks = "commit_checks"

// Batch bounds each cleanup statement.
const Batch = 500

// Deps are the fan-out's persistence, feedback, completion and metrics.
type Deps struct {
	Store    port.Store
	Feedback vcsapi.Feedback
	Atomic   func(context.Context, func(context.Context) error) error
	Complete func(context.Context, queue.Job) error
	Metrics  port.Metrics
	Now      func() time.Time
}

// Service records one delivery per target and enqueues it atomically.
type Service struct{ deps Deps }

// New constructs the sink over d. Its clock defaults to the process clock.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{deps: d}
}

// Consume computes targets before opening a transaction. Delivery records,
// jobs and the fenced consume completion then commit together, so neither
// a duplicate event nor a lost lease can enqueue a second delivery.
func (s *Service) Consume(ctx context.Context, event outbox.Event, job queue.Job) error {
	targets, err := s.deps.Feedback.Targets(ctx, event)
	if err != nil {
		return fmt.Errorf("select Commit checks targets: %w", err)
	}
	for _, target := range targets {
		if event.Account.IsZero() || event.ID.IsZero() || target.Account != event.Account ||
			target.EventID != event.ID || target.Key == "" || target.Job == "" {
			return vcsapi.ErrFeedbackTarget
		}
	}
	//nolint:wrapcheck // each operation names its failure
	return s.deps.Atomic(ctx, func(ctx context.Context) error {
		var delivered int64
		for _, target := range targets {
			inserted, err := s.deps.Store.Insert(ctx, port.Delivery{
				Account: event.Account, EventID: event.ID, Sink: CommitChecks,
				Target: target.Key, RuleIndex: 0, CreatedAt: s.deps.Now(),
			})
			if err != nil {
				return fmt.Errorf("record Commit checks delivery: %w", err)
			}
			if !inserted {
				continue
			}
			if err := s.deps.Feedback.Enqueue(ctx, target); err != nil {
				return fmt.Errorf("enqueue Commit checks delivery: %w", err)
			}
			delivered++
		}
		if err := s.deps.Complete(ctx, job); err != nil {
			return fmt.Errorf("complete sinks consumption: %w", err)
		}
		tx.AfterCommit(ctx, func(ctx context.Context) {
			if s.deps.Metrics == nil {
				return
			}
			if delivered > 0 {
				s.deps.Metrics.Add(ctx, "delivered", delivered)
			}
			if len(targets) == 0 {
				s.deps.Metrics.Add(ctx, "skipped", 1)
			}
		})
		return nil
	})
}

// Prune removes expired records in bounded statements.
func (s *Service) Prune(ctx context.Context, retention time.Duration) error {
	before := s.deps.Now().Add(-retention)
	for {
		n, err := s.deps.Store.Prune(ctx, before, Batch)
		if err != nil || n < Batch {
			return err //nolint:wrapcheck // the store names the cleanup
		}
	}
}

// Purge removes a deleted account's records without touching another's.
func (s *Service) Purge(ctx context.Context, account kernel.ULID) error {
	for {
		n, err := s.deps.Store.Purge(ctx, account, Batch)
		if err != nil || n < Batch {
			return err //nolint:wrapcheck // the store names the purge
		}
	}
}
