package lifecycle

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
)

// EvaluateApproval records fresh evidence in the caller's transaction. A
// policy-free lifecycle retains the established behavior of its callers.
func (l *Lifecycle) EvaluateApproval(ctx context.Context, run Run) (policyapi.Evaluation, error) {
	if !tx.In(ctx) {
		return policyapi.Evaluation{}, ErrNoTransaction
	}
	if l.d.Approvals == nil {
		return policyapi.Evaluation{Type: policyapi.Approval, Outcome: policyapi.NoPolicies}, nil
	}
	return l.d.Approvals.Evaluate(ctx, run) //nolint:wrapcheck // evaluator names the decision failure
}

// Approved accepts the combiner's affirmative result or the legacy default.
func Approved(result policyapi.Evaluation) bool {
	return result.Type == policyapi.Approval && (result.Outcome == policyapi.Approve && result.HasPolicies ||
		result.Outcome == policyapi.NoPolicies && !result.HasPolicies)
}

// ApplyApproval consumes the fresh result in the same transaction. UNCONFIRMED
// approval deliberately leaves the separate human confirmation action pending.
func (l *Lifecycle) ApplyApproval(ctx context.Context, run Run, result policyapi.Evaluation) (Run, error) {
	if !tx.In(ctx) {
		return Run{}, ErrNoTransaction
	}
	to, note := kernel.RunState(""), ""
	switch {
	case result.Type == policyapi.Approval && result.HasPolicies && result.Outcome == policyapi.Reject:
		to, note = kernel.RunStateFailed, "Run rejected by approval policies"
	case run.State == kernel.RunStatePendingReview && Approved(result):
		to = kernel.RunStateFinished
	default:
		return run, nil
	}
	return l.Transition(ctx, Request{Account: run.Account, Run: run.ID, Version: run.Version, To: to, Driver: machine.DriverApproval, Note: note})
}

// Refresh publishes receipts and flags when a review changes evidence without
// entering another state. The state transition sequence itself stays unchanged.
func (l *Lifecycle) Refresh(ctx context.Context, run Run) error {
	if !tx.In(ctx) {
		return ErrNoTransaction
	}
	return l.updated(ctx, run, Step{Sequence: run.Version, State: run.State, At: l.now()}, "")
}
