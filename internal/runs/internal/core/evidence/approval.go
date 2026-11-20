package evidence

import (
	"context"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
)

// Approval is the latest aggregate for one state version. Historical receipts
// alone cannot distinguish a removed policy from a policy still awaiting a vote.
type Approval struct {
	State        kernel.RunState
	StateVersion int64
	HasPolicies  bool
	Outcome      policyapi.Outcome
	At           time.Time
}

// Waiting reports a policy gate that has not approved this exact state.
func (a Approval) Waiting() bool {
	return a.HasPolicies && a.Outcome != policyapi.Approve
}

// Valid accepts only approval outcomes and states that have an approval gate.
func (a Approval) Valid() bool {
	state := a.State == kernel.RunStateQueued || a.State == kernel.RunStateUnconfirmed || a.State == kernel.RunStatePendingReview
	outcome := a.Outcome == policyapi.Approve || a.Outcome == policyapi.Reject || a.Outcome == policyapi.Undecided
	return state && a.StateVersion > 0 && !a.At.IsZero() &&
		(a.HasPolicies && outcome || !a.HasPolicies && a.Outcome == policyapi.NoPolicies)
}

// ApprovalStore writes under the current run fence and reads only aggregates
// matching each run's current state/version. Old gates never approve a replan.
type ApprovalStore interface {
	RecordApproval(context.Context, kernel.AccountID, kernel.ULID, Approval) error
	Approvals(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]Approval, error)
}
