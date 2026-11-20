package push

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
)

func requestEvent(event vcsapi.Event) bool {
	return event.Type == "pull_request" && event.PullRequest != nil || event.Type == "comment" && event.Comment != nil
}

func (s *Service) prepareRequest(ctx context.Context, delivery intake.Delivery, event vcsapi.Event, use stacksapi.Use) (Prepared, error) {
	prepared := Prepared{Event: event}
	needed, err := s.requestPolicies(ctx, delivery.Account, use, event.Repository)
	if err != nil {
		return prepared, err
	}
	if !needed {
		return prepared, nil
	}
	if s.d.Requests == nil {
		return prepared, fmt.Errorf("%w: native request preparation is unavailable", kernel.ErrUnavailable)
	}
	facts, err := s.d.Requests.PrepareRequest(ctx, delivery.Account, delivery.Integration, delivery.Provider, event)
	if err != nil {
		return prepared, fmt.Errorf("VCS prepare policy request: %w", err)
	}
	prepared.Request = &facts
	prepared.Commit = facts.Head
	prepared.Files = facts.Files
	prepared.Event.Head, prepared.Event.Branch = facts.Head.Hash, facts.Head.Branch
	prepared.Event.PullRequest = &facts.Snapshot.Request
	return prepared, nil
}

// requestPolicies is only a preparation hint. Its short read transaction ends
// before native HTTP; the child makes the authoritative effective selection.
func (s *Service) requestPolicies(ctx context.Context, account kernel.AccountID, use stacksapi.Use, repo vcsapi.Repository) (bool, error) {
	if s.d.PolicyAllowed == nil || s.d.PolicySelection == nil {
		return false, fmt.Errorf("%w: request policy selection is unavailable", kernel.ErrUnavailable)
	}
	allowed, err := s.d.PolicyAllowed(ctx, account)
	if err != nil {
		return false, fmt.Errorf("VCS request policy entitlement: %w", err)
	}
	if !allowed {
		return false, nil
	}
	found := false
	err = tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		stacks, err := s.d.Stacks.ByVCSRepository(ctx, account, use, repo.Namespace, repo.Name)
		if err != nil {
			return fmt.Errorf("VCS request preparation targets: %w", err)
		}
		for _, stack := range stacks {
			present, err := s.d.PolicySelection.HasPolicies(
				ctx,
				policyapi.SelectionRequest{Account: account, Type: policyapi.Push, Target: policyapi.Target{Kind: policyapi.StackTarget, ID: stack.ID}},
			)
			if err != nil {
				return fmt.Errorf("VCS request policy presence: %w", err)
			}
			if present {
				found = true
				break
			}
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("VCS request preparation selection: %w", err)
	}
	return found, nil
}
