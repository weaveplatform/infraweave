package core

import (
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

func (b *inputBuilder) trigger(facts policyapi.RunInputFacts, run inputs.ApprovalRun, stack inputs.Stack) (inputs.TriggerInput, error) {
	context := facts.Trigger
	if context == nil || !context.Priority.Valid() || !facts.Run.State.Terminal() {
		return inputs.TriggerInput{}, policyapi.ErrInput
	}
	terminal := inputs.TriggerRun{
		ID: run.ID, Type: run.Type, State: run.State, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
		TriggeredBy: run.TriggeredBy, Branch: run.Branch, Commit: run.Commit, Flags: run.Flags,
		BasedOnLocalWorkspace: run.BasedOnLocalWorkspace, DriftDetection: run.DriftDetection,
		ForceApplyMode: run.ForceApplyMode, Command: run.Command, UserProvidedMetadata: run.UserProvidedMetadata,
		RuntimeConfig: run.RuntimeConfig, Changes: run.Changes, Priority: inputs.Value(string(context.Priority)),
		StatesHistory: convertFact(b, context.History, func(history []policyapi.TransitionFacts) []inputs.StateTransition {
			out := make([]inputs.StateTransition, 0, len(history))
			for _, transition := range history {
				out = append(out, inputs.StateTransition{
					NewState: inputs.Value(string(transition.State)), Note: fact(b, transition.Note), Username: fact(b, transition.Username),
				})
			}
			return out
		}),
	}
	if context.TriggerAbsent {
		terminal.Trigger = inputs.Null[*inputs.RunTrigger]()
	}
	stacks := make([]inputs.TriggerStack, 0, len(context.Stacks))
	ids, slugs := map[string]bool{}, map[string]bool{}
	for _, target := range context.Stacks {
		id := target.Stack.ID.String()
		if target.Stack.ID.IsZero() || target.Stack.Slug == "" || !target.QueuedRuns.Known || target.QueuedRuns.Value == nil || *target.QueuedRuns.Value < 0 || ids[id] ||
			slugs[target.Stack.Slug] {
			return inputs.TriggerInput{}, policyapi.ErrInput
		}
		ids[id], slugs[target.Stack.Slug] = true, true
		stacks = append(stacks, triggerStack(b.stack(target.Stack), *target.QueuedRuns.Value))
	}
	return inputs.TriggerInput{Run: inputs.Value(terminal), Stack: inputs.Value(stack), Stacks: inputs.Value(stacks)}, nil
}

func triggerStack(stack inputs.Stack, queued int64) inputs.TriggerStack {
	return inputs.TriggerStack{
		Administrative: stack.Administrative, Autodeploy: stack.Autodeploy, Branch: stack.Branch,
		ID: stack.ID, Name: stack.Name, Namespace: stack.Namespace, ProjectRoot: stack.ProjectRoot,
		Repository: stack.Repository, State: stack.State, Labels: stack.Labels,
		AdditionalProjectGlobs: stack.AdditionalProjectGlobs, Contexts: stack.Contexts, LockedBy: stack.LockedBy,
		TerraformVersion: stack.TerraformVersion, TerraformWorkflowTool: stack.TerraformWorkflowTool,
		TrackedCommit: stack.TrackedCommit, Space: stack.Space, WorkerPool: stack.WorkerPool, Roles: stack.Roles,
		QueuedRuns: inputs.Value(queued),
	}
}
