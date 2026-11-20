package push

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
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
