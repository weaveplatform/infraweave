package policytrigger

import (
	"context"
	"encoding/json"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/policyinput"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func known[T any](value T) policyapi.Fact[T] { return policyapi.Fact[T]{Known: true, Value: &value} }

func (s *Service) input(ctx context.Context, run lifecycle.Run, owners []stacksapi.Stack) (json.RawMessage, error) {
	snapshot, err := policyinput.Current(ctx, s.d.Store, run.Account, run.ID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the run owner names the snapshot read
	}
	ids := make([]kernel.ULID, 0, len(owners))
	for _, owner := range owners {
		ids = append(ids, owner.ID)
	}
	counts, err := s.d.Store.QueuedForPolicyTrigger(ctx, run.Account, ids)
	if err != nil {
		return nil, err //nolint:wrapcheck // the run owner names the queue read
	}
	context := policyapi.TriggerContext{Priority: run.Priority, TriggerAbsent: true, Stacks: []policyapi.TriggerStackFacts{}}
	var source policyapi.StackFacts
	var vendor string
	for _, owner := range owners {
		stack, err := s.d.Snapshots.PolicySnapshot(ctx, run.Account, owner.ID)
		if err != nil {
			return nil, err //nolint:wrapcheck // the stack owner names the snapshot read
		}
		facts, err := s.d.Metadata.Stack(ctx, lifecycle.Run{Account: run.Account, Stack: owner.ID, WorkerPool: stack.Settings.WorkerPool}, stack)
		if err != nil {
			return nil, err //nolint:wrapcheck // metadata reads retain their owner errors
		}
		count, found := counts[owner.ID]
		if !found {
			return nil, kernel.ErrUnavailable
		}
		context.Stacks = append(context.Stacks, policyapi.TriggerStackFacts{Stack: facts, QueuedRuns: known(count)})
		if owner.ID == run.Stack {
			source = facts
			vendor = stack.Settings.Vendor
		}
	}
	histories, err := s.d.Store.Histories(ctx, run.Account, []kernel.ULID{run.ID})
	if err != nil {
		return nil, err //nolint:wrapcheck // the run owner names the committed history read
	}
	history, found := histories[run.ID]
	if !found || len(history) == 0 || history[len(history)-1].Sequence != run.Version || history[len(history)-1].State != run.State {
		return nil, kernel.ErrUnavailable
	}
	transitions := make([]policyapi.TransitionFacts, 0, len(history))
	for _, step := range history {
		transition := policyapi.TransitionFacts{State: step.State}
		if step.Note != nil {
			transition.Note = known(*step.Note)
		}
		if step.Username != nil {
			transition.Username = known(*step.Username)
		}
		transitions = append(transitions, transition)
	}
	context.History = known(transitions)
	changes, err := s.d.Metadata.Changes(ctx, run, vendor)
	if err != nil {
		return nil, err //nolint:wrapcheck // the run artifact owner names the read
	}
	return s.d.Inputs.BuildRunInput(ctx, policyapi.RunInputFacts{ //nolint:wrapcheck // the contract owner validates its projection
		Account: run.Account, Type: policyapi.Trigger, At: s.d.Now().UTC(), Stack: source,
		Run: policyinput.Facts(snapshot, snapshot.Environment, changes), Trigger: &context,
	})
}
