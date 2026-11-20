package policytrigger

import (
	"context"
	"encoding/json"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func (s *Service) directives(ctx context.Context, run lifecycle.Run, result *policyapi.Evaluation, targets map[string]stacksapi.Settings, tree []accessapi.SpaceNode) error {
	ids := make([]kernel.ULID, 0, len(result.Receipts))
	for _, receipt := range result.Receipts {
		ids = append(ids, receipt.Policy)
	}
	identities, err := s.d.Identities.Identities(ctx, run.Account, ids)
	if err != nil {
		return err //nolint:wrapcheck // the policy owner names the live metadata read
	}
	nodes := make(map[kernel.ULID]accessapi.SpaceNode, len(tree))
	for _, node := range tree {
		nodes[node.ID] = node
	}
	fatal := false
	for i := range result.Receipts {
		receipt := &result.Receipts[i]
		identity, found := identities[receipt.Policy]
		if !found || identity.Space.IsZero() || identity.Type != policyapi.Trigger || identity.Version != receipt.PolicyVersion {
			return kernel.ErrUnavailable
		}
		if receipt.Failure != nil {
			continue
		}
		var directive struct {
			Trigger  []string `json:"trigger"`
			Priority string   `json:"priority"`
		}
		if json.Unmarshal(receipt.Decision, &directive) != nil {
			return kernel.ErrUnavailable
		}
		for _, name := range directive.Trigger {
			target, exists := targets[name]
			if !exists || target.TrackedCommit == nil || !reaches(identity.Space, target.Space, nodes) {
				receipt.Failure = &policyapi.Failure{Code: "TARGET", Message: "A trigger target is unavailable or outside the policy's reach."}
				receipt.Outcome, receipt.Flags, receipt.ResetFlags = policyapi.Ignore, []string{"policy.internal_error"}, []string{}
				fatal = true
				break
			}
		}
		for _, failure := range result.Failures {
			if failure.Code == "PRIORITY_CONFLICT" && directive.Priority != "" && receipt.Failure == nil {
				copy := failure
				receipt.Failure = &copy
			}
		}
	}
	if fatal {
		result.Outcome, result.Trigger.Targets = policyapi.Ignore, []string{}
	}
	return nil
}

func reaches(ancestor, target kernel.ULID, nodes map[kernel.ULID]accessapi.SpaceNode) bool {
	for left := len(nodes); left > 0 && !target.IsZero(); left-- {
		if target == ancestor {
			return true
		}
		node, found := nodes[target]
		if !found || !node.Inherit {
			return false
		}
		target = node.Parent
	}
	return false
}
