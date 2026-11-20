package report

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/changes"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/policyinput"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

func (s *Service) evaluatePlan(ctx context.Context, run lifecycle.Run) (policyapi.Evaluation, error) {
	if s.d.Policies == nil {
		return policyapi.Evaluation{Type: policyapi.Plan, Outcome: policyapi.NoPolicies}, nil
	}
	result, err := s.d.Policies.Evaluate(ctx, policyapi.EvaluationRequest{
		Account: run.Account, Type: policyapi.Plan, Target: policyapi.Target{Kind: policyapi.StackTarget, ID: run.Stack}, Run: &run.ID,
		BuildInput: func(ctx context.Context) (json.RawMessage, error) { return s.planInput(ctx, run) },
	})
	if err != nil {
		return policyapi.Evaluation{}, fmt.Errorf("runs: evaluate plan policies: %w", err)
	}
	valid := result.HasPolicies && result.Plan != nil && (result.Outcome == policyapi.Allow || result.Outcome == policyapi.Deny) ||
		!result.HasPolicies && result.Outcome == policyapi.NoPolicies
	if result.Type != policyapi.Plan || !valid {
		return policyapi.Evaluation{}, evidence.ErrReceipt
	}
	return result, nil
}

func knownPlan[T any](value T) policyapi.Fact[T] {
	return policyapi.Fact[T]{Known: true, Value: &value}
}

func (s *Service) planInput(ctx context.Context, run lifecycle.Run) (json.RawMessage, error) {
	snapshot, err := policyinput.Read(ctx, s.d.PolicyStore, run.Account, run.ID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the owner names the snapshot read
	}
	stack, err := s.d.PolicyStacks.PolicySnapshot(ctx, run.Account, run.Stack)
	if err != nil {
		return nil, fmt.Errorf("runs: read plan policy stack: %w", err)
	}
	st, err := s.d.PolicyMetadata.Stack(ctx, run, stack)
	if err != nil {
		return nil, fmt.Errorf("runs: build plan policy stack: %w", err)
	}
	// Both artifacts must belong to this exact PLANNING version. Selecting the
	// latest plan would let a targeted replan reuse its earlier approved input.
	for _, name := range []string{PlanDocument, changes.Document} {
		found, err := s.d.Store.Recorded(ctx, run, run.Version, workersapi.UploadArtifact, name)
		if err != nil {
			return nil, fmt.Errorf("runs: locate current policy artifact: %w", err)
		}
		if !found {
			return nil, policyapi.ErrInput
		}
	}
	document, err := s.d.Objects.Open(ctx, objectstore.RunArtifacts, key(run, PlanDocument))
	if err != nil {
		return nil, policyapi.ErrInput
	}
	defer document.Close()
	raw, err := io.ReadAll(io.LimitReader(document, plan.MaxSize+1))
	if err != nil || len(raw) > plan.MaxSize {
		return nil, policyapi.ErrInput
	}
	changeFacts, err := s.d.PolicyMetadata.Changes(ctx, run, stack.Settings.Vendor)
	if err != nil {
		return nil, err //nolint:wrapcheck // the owner names the structural artifact read
	}
	facts := policyapi.RunInputFacts{
		Account: run.Account, Type: policyapi.Plan, At: s.d.Now().UTC(),
		Run: policyinput.Facts(snapshot, snapshot.Environment, changeFacts), Stack: st,
		Plan:        &policyapi.SanitizedPlan{Vendor: stack.Settings.Vendor, Document: raw},
		PreviousRun: policyapi.Fact[policyapi.RunFacts]{Known: true},
	}
	if snapshot.Previous != nil {
		previous := snapshot.Previous
		facts.PreviousRun = knownPlan(policyinput.Facts(*previous, previous.Environment, policyapi.Fact[[]policyapi.ChangeFacts]{}))
	}
	return s.d.Inputs.BuildRunInput(ctx, facts) //nolint:wrapcheck // typed builder owns document validation
}

// planTarget preserves the policy-free state table except for an actual deny
// or warning. Approval presence is selected without evaluating it in PLANNING;
// the lifecycle evaluates its rules after entering the review's own version.
func (s *Service) planTarget(
	ctx context.Context,
	run lifecycle.Run,
	delta kernel.PlanDelta,
	stack stacksapi.Settings,
	note string,
	result policyapi.Evaluation,
) (kernel.RunState, string, error) {
	if result.Outcome == policyapi.Deny {
		return kernel.RunStateFailed, "denied by plan policy", nil
	}
	next := Decision(run.Type, delta, stack)
	if result.Plan == nil || len(result.Plan.Warn) == 0 {
		return next, note, nil
	}
	if note == "" {
		note = strings.Join(result.Plan.Warn, "\n")
	}
	switch {
	case run.Type == kernel.RunTypeTracked && delta.Changes():
		next = kernel.RunStateUnconfirmed
	case run.Type == kernel.RunTypeProposed:
		if s.d.Selection == nil {
			return "", "", kernel.ErrUnavailable
		}
		found, err := s.d.Selection.HasPolicies(ctx, policyapi.SelectionRequest{
			Account: run.Account, Type: policyapi.Approval, Target: policyapi.Target{Kind: policyapi.StackTarget, ID: run.Stack},
		})
		if err != nil {
			return "", "", fmt.Errorf("runs: select approval policies for proposed warning: %w", err)
		}
		if found {
			next = kernel.RunStatePendingReview
		}
	}
	return next, note, nil
}
