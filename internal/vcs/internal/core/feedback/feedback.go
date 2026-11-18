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

// ReportCheck coalesces pending states for a named commit check. The VCS
// handler owns provider effects; events retains only the target reference.
var ReportCheck = queue.Kind{Name: "vcs.report_check", Queue: queue.Deliveries, Policy: queue.Coalesce}

// Service owns VCS feedback jobs; the events domain owns fan-out records.
type Service struct {
	jobs        *queue.Queue
	hold        func(context.Context, kernel.AccountID) error
	deployments *DeploymentDeps
	checks      *CheckDeps
	comments    *CommentDeps
}

// ErrAccountHold refuses enqueueing when its owner fence is not configured.
var ErrAccountHold = errors.New("VCS feedback account hold is not configured")

// New enqueues feedback through jobs in its caller's transaction.
func New(jobs *queue.Queue, hold func(context.Context, kernel.AccountID) error) *Service {
	return &Service{jobs: jobs, hold: hold}
}

// Kinds names the run updates the Commit checks sink consumes.
func (*Service) Kinds() []string { return []string{runsapi.UpdatedKind} }

// Targets selects deployment effects from run facts without calling providers.
func (s *Service) Targets(ctx context.Context, event outbox.Event) ([]vcsapi.FeedbackTarget, error) {
	targets, err := s.deploymentTargets(ctx, event)
	if err != nil {
		return nil, err
	}
	checks, err := s.checkTargets(ctx, event)
	if err != nil {
		return nil, err
	}
	comments, err := s.commentTargets(ctx, event)
	if err != nil {
		return nil, err
	}
	return append(append(targets, checks...), comments...), nil
}

func valid(target vcsapi.FeedbackTarget) bool {
	return !target.Account.IsZero() && !target.EventID.IsZero() && target.Key != "" &&
		(target.Job == ReportCheck.Name || target.Job == ReportDeployment.Name || target.Job == ReportComment.Name) && json.Valid(target.Payload)
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
	if target.Job == ReportDeployment.Name {
		return s.enqueueDeployment(ctx, target)
	}
	if target.Job == ReportComment.Name {
		return s.enqueueComment(ctx, target)
	}
	if target.Job == ReportCheck.Name && s.checks != nil {
		return s.enqueueCheck(ctx, target)
	}
	key := fmt.Sprintf("%s:%s:commit_checks:%s:0", target.Account, target.EventID, target.Key)
	if _, err := s.jobs.EnqueueTx(ctx, ReportCheck, target.Payload,
		queue.Account(target.Account), queue.UniqueKey(key)); err != nil {
		return fmt.Errorf("enqueue VCS feedback: %w", err)
	}
	return nil
}

var _ vcsapi.Feedback = (*Service)(nil)
