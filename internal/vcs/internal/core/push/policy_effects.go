package push

import (
	"context"
	"fmt"
	"slices"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func (s *Service) advanceHead(ctx context.Context, input Evaluation, stack stacksapi.Settings, event Prepared, evaluation policyapi.Evaluation) error {
	if event.Event.Type == "push" {
		if !evaluation.HasPolicies {
			if err := s.supersede(ctx, input, event); err != nil {
				return err
			}
		}
		if event.Event.Branch == stack.Branch {
			head := event.Commit
			head.Branch = ""
			if err := s.d.Stacks.SetTracked(ctx, input.Account, input.Stack, &head, nil, ""); err != nil {
				return fmt.Errorf("VCS branch head projection: %w", err)
			}
		}
	}
	if evaluation.HasPolicies && evaluation.Push.Track && !evaluation.Push.Ignore && evaluation.Push.NoTrigger &&
		(event.Event.PullRequest == nil || !event.Event.PullRequest.Fork || evaluation.Push.AllowFork) {
		commit := event.Commit
		commit.Branch = ""
		if err := s.d.Stacks.SetTracked(ctx, input.Account, input.Stack, nil, &commit, "infraweave"); err != nil {
			return fmt.Errorf("VCS no-trigger selected head: %w", err)
		}
	}
	return nil
}

func (s *Service) policyFeedback(ctx context.Context, input Evaluation, stack stacksapi.Settings, event Prepared, integration kernel.ULID, evaluation policyapi.Evaluation) error {
	if !evaluation.HasPolicies || evaluation.Push == nil {
		return nil
	}
	p := evaluation.Push
	if !p.Notify && !p.Fail && !p.FailedCheck {
		return nil
	}
	if s.d.PolicyChecks == nil {
		return fmt.Errorf("%w: VCS push check owner is unavailable", kernel.ErrUnavailable)
	}
	kind := kernel.RunTypeProposed
	if p.Track || event.Event.PullRequest == nil && event.Event.Branch == stack.Branch {
		kind = kernel.RunTypeTracked
	}
	names := make([]string, 0, len(evaluation.Receipts))
	for _, receipt := range evaluation.Receipts {
		if receipt.Failure != nil {
			names = append(names, receipt.PolicyName)
		}
	}
	slices.Sort(names)
	names = slices.Compact(names)
	if err := s.d.PolicyChecks.ReportPush(ctx, port.PushFeedback{
		Account: input.Account, Delivery: input.Delivery, Integration: integration, Stack: input.Stack,
		Commit: event.Commit.Hash, Type: kind, Failed: p.Fail || p.FailedCheck, Messages: slices.Clone(p.Messages), Policies: names,
	}); err != nil {
		return fmt.Errorf("VCS push check request: %w", err)
	}
	return nil
}

// applyPolicyEffects runs only for a new run inside the queue completion
// transaction. The scheduler cannot observe that run before these effects.
func (s *Service) applyPolicyEffects(ctx context.Context, input Evaluation, event Prepared, replacement kernel.ULID, p policyapi.PushDecision) error {
	if p.Lock != "" || p.Unlock != "" {
		if s.d.PolicyLocks == nil {
			return fmt.Errorf("%w: VCS policy lock owner is unavailable", kernel.ErrUnavailable)
		}
		if err := s.d.PolicyLocks.UpdatePushLock(ctx, input.Account, input.Stack, p.Lock, p.Unlock); err != nil {
			return fmt.Errorf("VCS policy lock: %w", err)
		}
	}
	if event.Event.Type == "push" {
		if err := s.supersede(ctx, input, event); err != nil {
			return err
		}
	}
	_, err := s.cancelForPolicy(ctx, input, replacement, p)
	return err
}

func (s *Service) supersede(ctx context.Context, input Evaluation, event Prepared) error {
	if s.d.Supersede == nil {
		return fmt.Errorf("%w: event superseding is unavailable", kernel.ErrUnavailable)
	}
	if _, err := s.d.Supersede.Supersede(ctx, input.Account, input.Stack, event.Event.Branch, event.Commit.Hash); err != nil {
		return fmt.Errorf("VCS supersede older plans: %w", err)
	}
	return nil
}
