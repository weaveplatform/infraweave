package push

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

const (
	CustomPolicy          = "CUSTOM_POLICY"
	CustomPolicyNoTrigger = "CUSTOM_POLICY_NO_TRIGGER"
	DisabledByLabel       = "DISABLED_BY_LABEL"
	disableDefaultPush    = "feature:disable_default_push_policy"
)

func (s *Service) policyDecision(ctx context.Context, input Evaluation, stack stacksapi.Settings, event Prepared, integration kernel.ULID) (Decision, policyapi.Evaluation, error) {
	var evaluation policyapi.Evaluation
	if s.d.PolicyAllowed == nil || s.d.Policies == nil {
		return Decision{}, evaluation, fmt.Errorf("%w: VCS policy evaluator is unavailable", kernel.ErrUnavailable)
	}
	allowed, err := s.d.PolicyAllowed(ctx, input.Account)
	if err != nil {
		return Decision{}, evaluation, fmt.Errorf("VCS policy entitlement: %w", err)
	}
	if allowed {
		evaluation, err = s.d.Policies.Evaluate(ctx, policyapi.EvaluationRequest{
			Account: input.Account, Type: policyapi.Push, Target: policyapi.Target{Kind: policyapi.StackTarget, ID: input.Stack},
			BuildInput: func(ctx context.Context) (json.RawMessage, error) {
				if requestEvent(event.Event) && event.Request == nil {
					return nil, errRequestPreparation
				}
				return s.policyInput(ctx, input, event, integration)
			},
		})
		if err != nil {
			return Decision{}, evaluation, fmt.Errorf("VCS push policy evaluation: %w", err)
		}
	}
	if !evaluation.HasPolicies {
		defaultDecision := Default(event.Event, stack, event.Files)
		if defaultDecision.Outcome == NoAction {
			return defaultDecision, evaluation, nil
		}
		if s.d.PolicyStacks == nil {
			return Decision{}, evaluation, fmt.Errorf("%w: VCS policy stack metadata is unavailable", kernel.ErrUnavailable)
		}
		snapshot, err := s.d.PolicyStacks.PolicySnapshot(ctx, input.Account, input.Stack)
		if err != nil {
			return Decision{}, evaluation, fmt.Errorf("VCS default push labels: %w", err)
		}
		if slices.Contains(snapshot.Labels, disableDefaultPush) {
			return Decision{Ignored, DisabledByLabel}, evaluation, nil
		}
		return defaultDecision, evaluation, nil
	}
	p := evaluation.Push
	if p == nil {
		return Decision{}, evaluation, fmt.Errorf("%w: VCS push evaluation has no decision", kernel.ErrUnavailable)
	}
	if p.Ignore || len(evaluation.Failures) > 0 {
		return Decision{Ignored, CustomPolicy}, evaluation, nil
	}
	if event.Event.PullRequest != nil && event.Event.PullRequest.Fork && !p.AllowFork {
		return Decision{Ignored, CustomPolicyNoTrigger}, evaluation, nil
	}
	if p.Track && !p.NoTrigger {
		return Decision{Outcome: Track}, evaluation, nil
	}
	if p.Propose {
		return Decision{Outcome: Propose}, evaluation, nil
	}
	return Decision{Ignored, CustomPolicyNoTrigger}, evaluation, nil
}

func receiptFacts(receipts []policyapi.Receipt) []runsapi.PolicyReceipt {
	out := make([]runsapi.PolicyReceipt, 0, len(receipts))
	for _, r := range receipts {
		fact := runsapi.PolicyReceipt{
			ID: r.ID, Policy: r.Policy, PolicyName: r.PolicyName, PolicyVersion: r.PolicyVersion,
			Type: string(r.Type), Engine: r.Engine, Outcome: string(r.Outcome), Decision: append(json.RawMessage(nil), r.Decision...),
			Flags: slices.Clone(r.Flags), ResetFlags: slices.Clone(r.ResetFlags), Sample: r.Sample, CreatedAt: r.CreatedAt,
		}
		if r.Failure != nil {
			fact.Failure = &runsapi.PolicyFailure{Code: r.Failure.Code, Message: r.Failure.Message}
		}
		out = append(out, fact)
	}
	return out
}
