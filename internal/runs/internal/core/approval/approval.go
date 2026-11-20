package approval

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/changes"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/policyinput"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/review"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// Store reads immutable input history and writes the aggregate in its fence.
type Store interface {
	policyinput.Store
	review.Store
	evidence.Store
	evidence.ApprovalStore
	Changes(context.Context, lifecycle.Run) (string, bool, error)
}

// Deps are owner ports; no policy input is taken from transport placeholders.
type Deps struct {
	Store      Store
	Evaluator  policyapi.Evaluator
	Inputs     policyapi.RunInputBuilder
	Stacks     stacksapi.PolicySnapshots
	Pools      workersapi.PoolMetadataReader
	Roles      accessapi.StackRoleReader
	SpaceFacts accessapi.PolicySpaceReader
	Contexts   configapi.PolicyContextReader
	Objects    changes.Objects
	Now        func() time.Time
}

// Service records decisions without driving transitions or confirming a plan.
type Service struct{ d Deps }

// New constructs the evaluator used by scheduling and lifecycle entry.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d}
}

// Evaluate joins the caller transaction. Input materializes only after policy
// selection; unavailable facts yield safe receipts, storage errors roll back.
func (s *Service) Evaluate(ctx context.Context, run lifecycle.Run) (policyapi.Evaluation, error) {
	if !tx.In(ctx) {
		return policyapi.Evaluation{}, lifecycle.ErrNoTransaction
	}
	current, err := s.d.Store.Lock(ctx, run.Account, run.ID)
	if err != nil {
		return policyapi.Evaluation{}, err //nolint:wrapcheck // the owner names the failing operation
	}
	if current.Version != run.Version || current.State != run.State {
		return policyapi.Evaluation{}, &lifecycle.Conflict{Run: run.ID, State: current.State, Version: current.Version}
	}
	result, err := s.d.Evaluator.Evaluate(ctx, policyapi.EvaluationRequest{
		Account: run.Account, Type: policyapi.Approval, Target: policyapi.Target{Kind: policyapi.StackTarget, ID: run.Stack}, Run: &run.ID,
		BuildInput: func(ctx context.Context) (json.RawMessage, error) { return s.input(ctx, run) },
	})
	if err != nil {
		return policyapi.Evaluation{}, err //nolint:wrapcheck // the owner names the failing operation
	}
	gate := evidence.Approval{State: run.State, StateVersion: run.Version, HasPolicies: result.HasPolicies, Outcome: result.Outcome, At: s.d.Now().UTC()}
	if result.Type != policyapi.Approval || !gate.Valid() {
		return policyapi.Evaluation{}, evidence.ErrReceipt
	}
	if err := s.d.Store.RecordApproval(ctx, run.Account, run.ID, gate); err != nil {
		return policyapi.Evaluation{}, err //nolint:wrapcheck // the owner names the failing operation
	}
	if err := s.d.Store.RecordPolicyEvidence(ctx, run.Account, run.ID, run.Version, result.Receipts); err != nil {
		return policyapi.Evaluation{}, err //nolint:wrapcheck // the owner names the failing operation
	}
	return result, nil
}

func known[T any](v T) policyapi.Fact[T] { return policyapi.Fact[T]{Known: true, Value: &v} }

func (s *Service) input(ctx context.Context, run lifecycle.Run) (json.RawMessage, error) {
	snapshot, err := policyinput.Current(ctx, s.d.Store, run.Account, run.ID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the owner names the failing operation
	}
	stack, err := s.d.Stacks.PolicySnapshot(ctx, run.Account, run.Stack)
	if err != nil {
		return nil, err //nolint:wrapcheck // the owner names the failing operation
	}
	records, err := s.d.Store.Reviews(ctx, run.Account, []kernel.ULID{run.ID})
	if err != nil {
		return nil, err //nolint:wrapcheck // the owner names the failing operation
	}
	reviews, err := review.Effective(records[run.ID], run.State, run.Version)
	if err != nil {
		return nil, fmt.Errorf("runs: invalid approval review history: %w", kernel.ErrUnavailable)
	}
	metadata := policyinput.Metadata{Store: s.d.Store, Objects: s.d.Objects, Pools: s.d.Pools, Roles: s.d.Roles, SpaceFacts: s.d.SpaceFacts, Contexts: s.d.Contexts}
	st, err := metadata.Stack(ctx, run, stack)
	if err != nil {
		return nil, fmt.Errorf("runs: build approval stack facts: %w", err)
	}
	changeFacts, err := metadata.Changes(ctx, run, stack.Settings.Vendor)
	if err != nil {
		return nil, fmt.Errorf("runs: build approval change facts: %w", err)
	}
	return s.d.Inputs.BuildRunInput(ctx, policyapi.RunInputFacts{ //nolint:wrapcheck // typed builder owns fact validation
		Account: run.Account, Type: policyapi.Approval, At: s.d.Now().UTC(),
		Run: policyinput.Facts(snapshot, snapshot.Environment, changeFacts), Stack: st, Reviews: known(reviews),
	})
}
