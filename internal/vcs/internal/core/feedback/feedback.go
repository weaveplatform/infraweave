package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

// ReportCheck is the Commit checks delivery job. Target selection remains
// empty until its provider handler is implemented, so no unfinished job
// is produced by a run's state transition.
var ReportCheck = queue.Kind{Name: "vcs.report_check", Queue: queue.Deliveries, Policy: queue.Ignore}

// Service owns VCS feedback jobs; the events domain owns fan-out records.
type Service struct {
	jobs *queue.Queue
	hold func(context.Context, kernel.AccountID) error
}

// ErrAccountHold refuses enqueueing when its owner fence is not configured.
var ErrAccountHold = errors.New("VCS feedback account hold is not configured")

// New enqueues feedback through jobs in its caller's transaction.
func New(jobs *queue.Queue, hold func(context.Context, kernel.AccountID) error) *Service {
	return &Service{jobs: jobs, hold: hold}
}

// Kinds names the run updates the Commit checks sink consumes.
func (*Service) Kinds() []string { return []string{runsapi.UpdatedKind} }

// Targets is empty until the provider-specific feedback handlers select
// checks, deployments and comments. Subscribing the sink does not advertise
// or enqueue a provider effect before its implementation exists.
func (*Service) Targets(context.Context, outbox.Event) ([]vcsapi.FeedbackTarget, error) {
	return nil, nil
}

func valid(target vcsapi.FeedbackTarget) bool {
	return !target.Account.IsZero() && !target.EventID.IsZero() && target.Key != "" &&
		target.Job == ReportCheck.Name && json.Valid(target.Payload)
}

// Enqueue creates the target's job in the transaction on ctx. The event,
// sink, target and rule index also name its queue uniqueness key.
func (s *Service) Enqueue(ctx context.Context, target vcsapi.FeedbackTarget) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	if !valid(target) {
		return vcsapi.ErrFeedbackTarget
	}
	if s.hold == nil {
		return ErrAccountHold
	}
	if err := s.hold(ctx, kernel.AccountID{ULID: target.Account}); err != nil {
		return fmt.Errorf("hold VCS feedback account: %w", err)
	}
	key := fmt.Sprintf("%s:%s:commit_checks:%s:0", target.Account, target.EventID, target.Key)
	if _, err := s.jobs.EnqueueTx(ctx, ReportCheck, target.Payload,
		queue.Account(target.Account), queue.UniqueKey(key)); err != nil {
		return fmt.Errorf("enqueue VCS feedback: %w", err)
	}
	return nil
}

var _ vcsapi.Feedback = (*Service)(nil)
