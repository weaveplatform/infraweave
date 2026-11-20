package api

import (
	"context"
	"encoding/json"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Type identifies a current server policy language contract. ACCESS and the
// superseded aliases are deliberately absent from the evaluator's vocabulary.
type Type string

const (
	Approval       Type = "APPROVAL"
	Push           Type = "GIT_PUSH"
	Initialization Type = "INITIALIZATION"
	Intent         Type = "INTENT"
	Login          Type = "LOGIN"
	Notification   Type = "NOTIFICATION"
	Plan           Type = "PLAN"
	Task           Type = "TASK"
	Trigger        Type = "TRIGGER"
)

// TargetKind leaves module and Intent execution explicit for their owning
// phases. An unavailable target kind fails rather than selecting no policies.
type TargetKind string

const (
	StackTarget  TargetKind = "stack"
	SpaceTarget  TargetKind = "space"
	ModuleTarget TargetKind = "module"
	IntentTarget TargetKind = "intent_project"
)

// Target is an immutable account-owned resource, never an unqualified slug.
type Target struct {
	Kind TargetKind  `json:"kind"`
	ID   kernel.ULID `json:"id"`
}

// EvaluationRequest supplies a committed input snapshot. Run is absent for
// evaluations before a run exists; callers must not manufacture a run row.
type EvaluationRequest struct {
	Account kernel.AccountID
	Type    Type
	Target  Target
	Input   json.RawMessage
	// BuildInput runs once, in the joined transaction, only when policies
	// apply. It is mutually exclusive with Input; policy-free workflows do
	// not need to materialize facts required only by policy evaluation.
	BuildInput func(context.Context) (json.RawMessage, error)
	Run        *kernel.ULID
}

// Outcome distinguishes the existing policy-free behavior from an attached
// program whose preamble produces defaults because no user rule matched.
type Outcome string

const (
	NoPolicies        Outcome = "NO_POLICIES"
	Allow             Outcome = "ALLOW"
	Deny              Outcome = "DENY"
	Approve           Outcome = "APPROVE"
	Reject            Outcome = "REJECT"
	Undecided         Outcome = "UNDECIDED"
	Ignore            Outcome = "IGNORE"
	PushRun           Outcome = "PUSH"
	TriggerRuns       Outcome = "TRIGGER"
	RouteNotification Outcome = "NOTIFY"
)

// Failure retains a safe category and message without copying policy input,
// source or print output into plaintext receipts and notification events.
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Receipt is one independent program's evidence. Flags retain their policy
// owner; runs applies reset and set operations in its own transition transaction.
type Receipt struct {
	ID            kernel.ULID     `json:"id"`
	Policy        kernel.ULID     `json:"policy"`
	PolicyName    string          `json:"policyName"`
	PolicyVersion int64           `json:"policyVersion"`
	Type          Type            `json:"type"`
	Engine        string          `json:"engineType"`
	Outcome       Outcome         `json:"outcome"`
	Decision      json.RawMessage `json:"decision"`
	Flags         []string        `json:"flags"`
	ResetFlags    []string        `json:"resetFlags"`
	Sample        bool            `json:"sample"`
	Failure       *Failure        `json:"failure,omitempty"`
	CreatedAt     time.Time       `json:"createdAt"`
}

// PlanDecision separates failure, review warnings and informational messages.
type PlanDecision struct {
	Deny []string `json:"deny"`
	Warn []string `json:"warn"`
	Info []string `json:"info"`
}

// ApprovalDecision is the resolved gate, with all decision notes retained.
type ApprovalDecision struct {
	Approve      bool     `json:"approve"`
	Reject       bool     `json:"reject"`
	ApproveNotes []string `json:"approveNotes"`
	RejectNotes  []string `json:"rejectNotes"`
}

// Evaluation is the typed aggregate and its stable per-policy evidence. No
// policies is an explicit instruction for the caller to retain its own default.
type Evaluation struct {
	Type        Type              `json:"type"`
	HasPolicies bool              `json:"hasPolicies"`
	Outcome     Outcome           `json:"outcome"`
	Plan        *PlanDecision     `json:"plan,omitempty"`
	Approval    *ApprovalDecision `json:"approval,omitempty"`
	Receipts    []Receipt         `json:"receipts"`
	Failures    []Failure         `json:"failures"`
}

// Evaluator joins its caller's transaction. Policy failures return a fail-closed
// Evaluation; storage errors return an error and the caller must roll back.
// It never writes run receipts/flags or invokes a VCS or notification provider.
type Evaluator interface {
	Evaluate(context.Context, EvaluationRequest) (Evaluation, error)
}
