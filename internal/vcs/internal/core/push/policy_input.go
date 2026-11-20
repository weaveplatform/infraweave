package push

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func known[T any](value T) policyapi.Fact[T] {
	return policyapi.Fact[T]{Known: true, Value: &value}
}

func (s *Service) policyInput(ctx context.Context, input Evaluation, event Prepared, integration kernel.ULID) (json.RawMessage, error) {
	if s.d.PolicyInputs == nil || s.d.PolicyStacks == nil || s.d.PolicySpaces == nil ||
		s.d.PolicyRoles == nil || s.d.PolicyContexts == nil || s.d.PolicyIntegrations == nil || s.d.PolicyRuns == nil {
		return nil, fmt.Errorf("%w: VCS policy input owners are unavailable", kernel.ErrUnavailable)
	}
	if event.AffectedStacksCount == nil {
		return nil, policyapi.ErrInput
	}
	if requestEvent(event.Event) && event.Request == nil {
		return nil, policyapi.ErrInput
	}
	snapshot, err := s.d.PolicyStacks.PolicySnapshot(ctx, input.Account, input.Stack)
	if err != nil {
		return nil, fmt.Errorf("VCS policy stack facts: %w", err)
	}
	stack := snapshot.Settings
	facts := policyapi.PushInputFacts{Account: input.Account, Stack: policyapi.StackFacts{
		ID: stack.ID, Slug: stack.Slug, Name: stack.Name, Branch: stack.Branch, Namespace: stack.Namespace,
		Repository: stack.Repository, State: snapshot.State, Autodeploy: stack.Autodeploy,
		Labels: known(snapshot.Labels), AdditionalProjectGlobs: known(stack.AdditionalProjectGlobs),
		LockedBy: policyapi.Fact[string]{Known: true}, TrackedCommit: policyapi.Fact[policyapi.CommitFacts]{Known: true},
	}}
	if stack.ProjectRoot != nil {
		facts.Stack.ProjectRoot = *stack.ProjectRoot
	}
	if stack.Lock != nil {
		facts.Stack.LockedBy = known(stack.Lock.By)
	}
	if stack.TerraformVersion != nil {
		facts.Stack.TerraformVersion = known(*stack.TerraformVersion)
	}
	tool, err := workflowTool(stack)
	if err != nil {
		return nil, err
	}
	if tool != "" {
		facts.Stack.WorkflowTool = known(tool)
	}
	if stack.TrackedCommit != nil {
		commit := *stack.TrackedCommit
		commit.Branch = stack.Branch
		facts.Stack.TrackedCommit = known(commitFacts(commit))
	}
	space, err := s.d.PolicySpaces.PolicySpace(ctx, input.Account, stack.Space)
	if err != nil {
		return nil, fmt.Errorf("VCS policy space facts: %w", err)
	}
	facts.Stack.Space = known(policyapi.SpaceFacts{ID: space.Slug, Name: space.Name, Labels: known(space.Labels)})
	contexts, err := s.d.PolicyContexts.PolicyContextSlugs(ctx, input.Account, input.Stack)
	if err != nil {
		return nil, fmt.Errorf("VCS policy context facts: %w", err)
	}
	facts.Stack.Contexts = known(contexts)
	roles, err := s.d.PolicyRoles.StackRoles(ctx, input.Account, input.Stack)
	if err != nil {
		return nil, fmt.Errorf("VCS policy role facts: %w", err)
	}
	roleFacts := make([]policyapi.RoleFacts, 0, len(roles))
	for _, role := range roles {
		roleFacts = append(roleFacts, policyapi.RoleFacts{ID: role.ID, Slug: role.Slug, Name: role.Name})
	}
	facts.Stack.Roles = known(roleFacts)
	if stack.WorkerPool != nil {
		if s.d.PolicyPools == nil {
			return nil, fmt.Errorf("%w: VCS policy worker pool owner is unavailable", kernel.ErrUnavailable)
		}
		pool, err := s.d.PolicyPools.PoolMetadata(ctx, input.Account, *stack.WorkerPool)
		if err != nil {
			return nil, fmt.Errorf("VCS policy worker pool facts: %w", err)
		}
		facts.Stack.WorkerPool = known(policyapi.WorkerPoolFacts{ID: known(pool.ID.String()), Name: known(pool.Name), Labels: known(pool.Labels), Public: pool.Public})
	}
	integrationFacts, err := s.d.PolicyIntegrations.PolicyIntegration(ctx, input.Account, integration)
	if err != nil {
		return nil, fmt.Errorf("VCS policy integration facts: %w", err)
	}
	facts.VCSIntegration = known(integrationFacts)
	current, err := s.d.PolicyRuns.PushInProgress(ctx, input.Account, input.Stack)
	if err != nil {
		return nil, fmt.Errorf("VCS policy in-progress facts: %w", err)
	}
	facts.InProgress = known(inProgressFacts(current))
	if event.Request != nil {
		request := requestFacts(*event.Request, event.Event.Action, event.Event.Comment != nil, *event.AffectedStacksCount)
		facts.Push.Known = true
		facts.PullRequest = known(request)
		return s.d.PolicyInputs.BuildPushInput(ctx, facts) //nolint:wrapcheck // policy owner reports a safe typed input failure
	}
	commit := commitFacts(event.Commit)
	facts.Push = known(policyapi.PushFacts{
		Author: commit.Author, Branch: commit.Branch, Hash: event.Commit.Hash,
		Message: commit.Message, At: commit.At, AffectedFiles: event.Files, AffectedStacksCount: *event.AffectedStacksCount, Tag: commit.Tag,
	})
	facts.PullRequest.Known = true
	return s.d.PolicyInputs.BuildPushInput(ctx, facts) //nolint:wrapcheck // the policy owner reports a safe typed input failure
}

func workflowTool(stack stacksapi.Settings) (string, error) {
	var settings struct {
		WorkflowTool string `json:"workflowTool"`
		Tool         string `json:"tool"`
	}
	if len(stack.VendorConfig) != 0 && json.Unmarshal(stack.VendorConfig, &settings) != nil {
		return "", policyapi.ErrInput
	}
	switch stack.Vendor {
	case "terraform":
		if settings.WorkflowTool != "" {
			return settings.WorkflowTool, nil
		}
		return "TERRAFORM_FOSS", nil
	case "opentofu":
		if settings.WorkflowTool == "CUSTOM" {
			return "CUSTOM", nil
		}
		return "OPEN_TOFU", nil
	case "terragrunt":
		if settings.Tool != "" {
			return settings.Tool, nil
		}
		return "TERRAFORM_FOSS", nil
	}
	return "", nil
}

func commitFacts(commit stacksapi.Commit) policyapi.CommitFacts {
	author := commit.AuthorName
	if commit.AuthorLogin != nil && *commit.AuthorLogin != "" {
		author = *commit.AuthorLogin
	}
	facts := policyapi.CommitFacts{Author: author, Branch: commit.Branch, Message: commit.Message, At: commit.Timestamp, Hash: known(commit.Hash)}
	if commit.Tag != "" {
		facts.Tag = known(commit.Tag)
	}
	if commit.URL != "" {
		facts.URL = known(commit.URL)
	}
	return facts
}
