package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

// BuildRunInput uses the same field models and validation as the published
// contract. It deliberately does not read a database or recover secret values.
func (*Service) BuildRunInput(ctx context.Context, facts policyapi.RunInputFacts) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("build policy input: %w", err)
	}
	if facts.Account.IsZero() || facts.At.IsZero() || facts.Stack.ID.IsZero() {
		return nil, policyapi.ErrInput
	}
	b := &inputBuilder{}
	stack := b.stack(facts.Stack)
	run := b.run(facts.Run)
	var document any
	switch facts.Type {
	case policyapi.Approval:
		if facts.Plan != nil || facts.PreviousRun.Known || facts.PreviousRun.Value != nil || facts.Trigger != nil {
			return nil, policyapi.ErrInput
		}
		document = inputs.ApprovalInput{
			Run: inputs.Value(run), Stack: inputs.Value(stack),
			Reviews: convertFact(b, facts.Reviews, b.reviews),
		}
	case policyapi.Plan:
		if facts.Reviews.Known || facts.Reviews.Value != nil || facts.Trigger != nil {
			return nil, policyapi.ErrInput
		}
		planRun := toPlanRun(run)
		context := inputs.PlanContext{
			Run: inputs.Value(planRun), Stack: inputs.Value(stack),
			Commit: run.Commit, Request: inputs.Value(inputs.PlanRequest{TimestampNS: inputs.Value(facts.At.UnixNano())}),
			PreviousRun: convertFact(b, facts.PreviousRun, func(previous policyapi.RunFacts) inputs.PlanRun { return toPlanRun(b.run(previous)) }),
		}
		plan := inputs.PlanInput{Infraweave: inputs.Value(context)}
		if facts.Plan != nil {
			if err := inputs.ApplyPlan(&plan, facts.Plan.Vendor, bytes.NewReader(facts.Plan.Document)); err != nil {
				return nil, policyapi.ErrInput
			}
		}
		document = plan
	case policyapi.Initialization:
		initialization, err := initializationDocument(facts, run, stack)
		if err != nil {
			return nil, err
		}
		document = initialization
	case policyapi.Trigger:
		if facts.Plan != nil || facts.PreviousRun.Known || facts.PreviousRun.Value != nil || facts.Reviews.Known || facts.Reviews.Value != nil {
			return nil, policyapi.ErrInput
		}
		trigger, err := b.trigger(facts, run, stack)
		if err != nil {
			return nil, err
		}
		document = trigger
	default:
		return nil, policyapi.ErrInput
	}
	if b.invalid {
		return nil, policyapi.ErrInput
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode policy input: %w", policyapi.ErrInput)
	}
	if err := inputs.Validate(string(facts.Type), raw); err != nil {
		return nil, policyapi.ErrInput
	}
	return raw, nil
}

func initializationDocument(facts policyapi.RunInputFacts, run inputs.ApprovalRun, stack inputs.Stack) (inputs.InitializationInput, error) {
	if facts.Plan != nil || facts.PreviousRun.Known || facts.PreviousRun.Value != nil || facts.Reviews.Known || facts.Reviews.Value != nil || facts.Trigger != nil {
		return inputs.InitializationInput{}, policyapi.ErrInput
	}
	return inputs.InitializationInput{
		Commit: run.Commit, Run: inputs.Value(toPlanRun(run)), Stack: inputs.Value(stack),
		Request: inputs.Value(inputs.InitializationRequest{TimestampNS: inputs.Value(facts.At.UnixNano())}),
	}, nil
}

type inputBuilder struct{ invalid bool }

func fact[T any](b *inputBuilder, f policyapi.Fact[T]) inputs.Field[T] {
	return convertFact(b, f, func(v T) T { return v })
}

func convertFact[A, B any](b *inputBuilder, f policyapi.Fact[A], convert func(A) B) inputs.Field[B] {
	if !f.Known {
		b.invalid = b.invalid || f.Value != nil
		return inputs.Field[B]{}
	}
	if f.Value == nil {
		return inputs.Null[B]()
	}
	return inputs.Value(convert(*f.Value))
}

func (b *inputBuilder) commit(c policyapi.CommitFacts) inputs.Commit {
	if c.At.IsZero() {
		b.invalid = true
	}
	return inputs.Commit{
		Author: inputs.Value(c.Author), Branch: inputs.Value(c.Branch), Message: inputs.Value(c.Message),
		CreatedAt: inputs.Value(c.At.UnixNano()), Hash: fact(b, c.Hash), Tag: fact(b, c.Tag), URL: fact(b, c.URL),
		ExistOnTrackedBranch: fact(b, c.ExistsOnTrackedBranch), PullRequestID: fact(b, c.PullRequestID),
	}
}

func (b *inputBuilder) stack(s policyapi.StackFacts) inputs.Stack {
	return inputs.Stack{
		Administrative: inputs.Value(false), Autodeploy: inputs.Value(s.Autodeploy), Branch: inputs.Value(s.Branch),
		ID: inputs.Value(s.Slug), Name: inputs.Value(s.Name), Namespace: inputs.Value(s.Namespace),
		ProjectRoot: inputs.Value(s.ProjectRoot), Repository: inputs.Value(s.Repository), State: inputs.Value(string(s.State)),
		Labels: fact(b, s.Labels), AdditionalProjectGlobs: fact(b, s.AdditionalProjectGlobs), Contexts: fact(b, s.Contexts),
		LockedBy: fact(b, s.LockedBy), TerraformVersion: fact(b, s.TerraformVersion), TerraformWorkflowTool: fact(b, s.WorkflowTool),
		TrackedCommit: convertFact(b, s.TrackedCommit, b.commit),
		Space: convertFact(b, s.Space, func(s policyapi.SpaceFacts) inputs.Space {
			return inputs.Space{ID: inputs.Value(s.ID), Name: inputs.Value(s.Name), Labels: fact(b, s.Labels)}
		}),
		WorkerPool: convertFact(b, s.WorkerPool, func(p policyapi.WorkerPoolFacts) inputs.WorkerPool {
			return inputs.WorkerPool{ID: fact(b, p.ID), Name: fact(b, p.Name), Labels: fact(b, p.Labels), Public: inputs.Value(p.Public)}
		}),
		Roles: convertFact(b, s.Roles, func(roles []policyapi.RoleFacts) []inputs.Role {
			out := make([]inputs.Role, 0, len(roles))
			for _, r := range roles {
				if r.ID.IsZero() || r.Slug == "" || r.Name == "" {
					b.invalid = true
				}
				out = append(out, inputs.Role{ID: inputs.Value(r.Slug), Slug: inputs.Value(r.Slug), Name: inputs.Value(r.Name), ULID: inputs.Value(r.ID.String())})
			}
			return out
		}),
	}
}

func (b *inputBuilder) run(r policyapi.RunFacts) inputs.ApprovalRun {
	if r.ID.IsZero() || r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() {
		b.invalid = true
	}
	return inputs.ApprovalRun{
		ID: inputs.Value(r.ID.String()), Type: inputs.Value(string(r.Type)), State: inputs.Value(string(r.State)),
		CreatedAt: inputs.Value(r.CreatedAt.UnixNano()), UpdatedAt: inputs.Value(r.UpdatedAt.UnixNano()),
		TriggeredBy: fact(b, r.TriggeredBy), Branch: fact(b, r.Branch), Commit: convertFact(b, r.Commit, b.commit),
		Flags: fact(b, r.Flags), BasedOnLocalWorkspace: fact(b, r.BasedOnLocalWorkspace), DriftDetection: fact(b, r.DriftDetection),
		ForceApplyMode: fact(b, r.ForceApplyMode), Command: fact(b, r.Command), UserProvidedMetadata: fact(b, r.UserProvidedMetadata),
		RuntimeConfig: convertFact(b, r.RuntimeConfig, b.runtime),
		Changes: convertFact(b, r.Changes, func(changes []policyapi.ChangeFacts) []inputs.Change {
			out := make([]inputs.Change, 0, len(changes))
			for _, c := range changes {
				entity := inputs.Entity{Address: inputs.Value(c.Address), Type: fact(b, c.ResourceType), Name: fact(b, c.Name), EntityVendor: inputs.Value(c.Vendor)}
				module := fact(b, c.Module)
				if module.Present() {
					entity.Data = inputs.Value(inputs.EntityData{Address: inputs.Value(c.Address), Module: module})
				}
				out = append(out, inputs.Change{Phase: inputs.Value(c.Phase), Action: inputs.Value(c.Action), Moved: fact(b, c.Moved), Entity: inputs.Value(entity)})
			}
			return out
		}),
	}
}

func toPlanRun(r inputs.ApprovalRun) inputs.PlanRun {
	return inputs.PlanRun{
		ID: r.ID, Type: r.Type, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		TriggeredBy: r.TriggeredBy, Branch: r.Branch, Commit: r.Commit, Flags: r.Flags,
		BasedOnLocalWorkspace: r.BasedOnLocalWorkspace, DriftDetection: r.DriftDetection,
		ForceApplyMode: r.ForceApplyMode, Command: r.Command, RuntimeConfig: r.RuntimeConfig,
		Changes: r.Changes, UserProvidedMetadata: r.UserProvidedMetadata,
	}
}

func (b *inputBuilder) runtime(c policyapi.RuntimeConfigFacts) inputs.RuntimeConfig {
	return inputs.RuntimeConfig{
		AfterApply: fact(b, c.AfterApply), AfterDestroy: fact(b, c.AfterDestroy), AfterInit: fact(b, c.AfterInit),
		AfterPerform: fact(b, c.AfterPerform), AfterPlan: fact(b, c.AfterPlan), AfterRun: fact(b, c.AfterRun),
		BeforeApply: fact(b, c.BeforeApply), BeforeDestroy: fact(b, c.BeforeDestroy), BeforeInit: fact(b, c.BeforeInit),
		BeforePerform: fact(b, c.BeforePerform), BeforePlan: fact(b, c.BeforePlan), GitSparseCheckoutPaths: fact(b, c.GitSparseCheckoutPaths),
		Environment: fact(b, c.Environment), ProjectRoot: inputs.Value(c.ProjectRoot), RunnerImage: inputs.Value(c.RunnerImage),
		TerraformVersion: fact(b, c.TerraformVersion),
	}
}
