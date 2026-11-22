package initialization

import (
	"context"
	"encoding/json"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/policyinput"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Store supplies actual run snapshots and commits policy evidence in its fence.
type Store interface {
	policyinput.Store
	evidence.Store
}

// Deps are the same owner-backed models used by the other workflow policies.
type Deps struct {
	Store     Store
	Evaluator policyapi.Evaluator
	Inputs    policyapi.RunInputBuilder
	Stacks    stacksapi.PolicySnapshots
	Metadata  policyinput.Metadata
	Now       func() time.Time
}

// Service records the decision; claims owns refusal or assignment transitions.
type Service struct{ d Deps }

// New creates the server gate; no launcher file, exec builtin or JWT is involved.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d}
}

// Evaluate joins the owner/run fences acquired by candidate selection.
func (s *Service) Evaluate(ctx context.Context, run lifecycle.Run) (policyapi.Evaluation, error) {
	if !tx.In(ctx) {
		return policyapi.Evaluation{}, lifecycle.ErrNoTransaction
	}
	current, err := s.d.Store.Lock(ctx, run.Account, run.ID)
	if err != nil {
		return policyapi.Evaluation{}, err //nolint:wrapcheck // the owner names the read
	}
	if current.State != kernel.RunStateReady || current.State != run.State || current.Version != run.Version {
		return policyapi.Evaluation{}, &lifecycle.Conflict{Run: run.ID, State: current.State, Version: current.Version}
	}
	result, err := s.d.Evaluator.Evaluate(ctx, policyapi.EvaluationRequest{
		Account: run.Account, Type: policyapi.Initialization,
		Target: policyapi.Target{Kind: policyapi.StackTarget, ID: run.Stack}, Run: &run.ID,
		BuildInput: func(ctx context.Context) (json.RawMessage, error) {
			snapshot, err := policyinput.Current(ctx, s.d.Store, run.Account, run.ID)
			if err != nil {
				return nil, err //nolint:wrapcheck // the owner names the snapshot
			}
			stack, err := s.d.Stacks.PolicySnapshot(ctx, run.Account, run.Stack)
			if err != nil {
				return nil, err //nolint:wrapcheck // the owner names the snapshot
			}
			facts, err := s.d.Metadata.Stack(ctx, run, stack)
			if err != nil {
				return nil, err //nolint:wrapcheck // the owner names the facts
			}
			return s.d.Inputs.BuildRunInput(ctx, policyapi.RunInputFacts{
				Account: run.Account, Type: policyapi.Initialization, At: s.d.Now(), Stack: facts,
				Run: policyinput.Facts(snapshot, snapshot.Environment, policyapi.Fact[[]policyapi.ChangeFacts]{}),
			}) //nolint:wrapcheck // the typed builder owns validation
		},
	})
	if err != nil {
		return policyapi.Evaluation{}, err //nolint:wrapcheck // the evaluator names the operational failure
	}
	if err := s.d.Store.RecordPolicyEvidence(ctx, run.Account, run.ID, run.Version, result.Receipts); err != nil {
		return policyapi.Evaluation{}, err //nolint:wrapcheck // the owner names the write
	}
	return result, nil
}
