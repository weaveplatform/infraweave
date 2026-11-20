package core

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

func terminalInputFacts() policyapi.RunInputFacts {
	facts := inputFacts()
	facts.Type, facts.Run.State = policyapi.Trigger, kernel.RunStateFinished
	facts.Run.Flags = known([]string{"source-plan"})
	target := facts.Stack
	target.ID, target.Slug = kernel.NewULID(), "target"
	commit := *facts.Run.Commit.Value
	commit.Hash = known("target-head")
	target.TrackedCommit = known(commit)
	facts.Trigger = &policyapi.TriggerContext{
		Priority: kernel.RunPriorityHigh, TriggerAbsent: true,
		History: known([]policyapi.TransitionFacts{{State: kernel.RunStateQueued}, {State: kernel.RunStateFinished, Note: known("completed")}}),
		Stacks:  []policyapi.TriggerStackFacts{{Stack: target, QueuedRuns: known(int64(3))}},
	}
	return facts
}

func TestUnitTriggerInputPreservesTerminalOwnerFacts(t *testing.T) {
	for _, state := range []kernel.RunState{kernel.RunStateFinished, kernel.RunStateFailed, kernel.RunStateDiscarded, kernel.RunStateCanceled, kernel.RunStateStopped} {
		t.Run(string(state), func(t *testing.T) {
			facts := terminalInputFacts()
			facts.Run.State = state
			(*facts.Trigger.History.Value)[1].State = state
			raw, err := (&Service{}).BuildRunInput(t.Context(), facts)
			if err != nil {
				t.Fatalf("terminal trigger facts were refused: %v", err)
			}
			var input inputs.TriggerInput
			if err := json.Unmarshal(raw, &input); err != nil {
				t.Fatal(err)
			}
			run, _ := input.Run.Get()
			priority, _ := run.Priority.Get()
			flags, _ := run.Flags.Get()
			if got, _ := run.State.Get(); got != string(state) || priority != "high" || !run.Trigger.IsNull() || !reflect.DeepEqual(flags, []string{"source-plan"}) {
				t.Fatal("terminal state, priority, trigger absence or committed flags were lost")
			}
			stacks, _ := input.Stacks.Get()
			if len(stacks) != 1 {
				t.Fatal("owner target inventory was lost")
			}
			queued, _ := stacks[0].QueuedRuns.Get()
			commit, _ := stacks[0].TrackedCommit.Get()
			head, _ := commit.Hash.Get()
			if queued != 3 || head != "target-head" || input.Workflow.Present() || input.Module.Present() {
				t.Fatal("target queue/commit facts changed or an unimplemented graph/module input was invented")
			}
			history, _ := run.StatesHistory.Get()
			if len(history) != 2 {
				t.Fatal("committed terminal history was lost")
			}
		})
	}
}

func TestUnitTriggerInputRefusesUnavailableOrContradictoryFacts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*policyapi.RunInputFacts)
	}{
		{name: "missing context", change: func(f *policyapi.RunInputFacts) { f.Trigger = nil }},
		{name: "nonterminal run", change: func(f *policyapi.RunInputFacts) { f.Run.State = kernel.RunStatePlanning }},
		{name: "unknown priority", change: func(f *policyapi.RunInputFacts) { f.Trigger.Priority = "unknown" }},
		{name: "negative queue", change: func(f *policyapi.RunInputFacts) { f.Trigger.Stacks[0].QueuedRuns = known(int64(-1)) }},
		{name: "unavailable queue", change: func(f *policyapi.RunInputFacts) { f.Trigger.Stacks[0].QueuedRuns = policyapi.Fact[int64]{} }},
		{name: "duplicate target", change: func(f *policyapi.RunInputFacts) { f.Trigger.Stacks = append(f.Trigger.Stacks, f.Trigger.Stacks[0]) }},
		{name: "plan supplied", change: func(f *policyapi.RunInputFacts) { f.Plan = &policyapi.SanitizedPlan{Vendor: "terraform"} }},
		{name: "review supplied", change: func(f *policyapi.RunInputFacts) { f.Reviews = known(policyapi.ReviewsFacts{}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := terminalInputFacts()
			tc.change(&facts)
			if _, err := (&Service{}).BuildRunInput(t.Context(), facts); !errors.Is(err, policyapi.ErrInput) {
				t.Fatalf("invalid terminal facts were accepted: %v", err)
			}
		})
	}
	facts := terminalInputFacts()
	facts.Trigger.TriggerAbsent = false
	facts.Trigger.History = policyapi.Fact[[]policyapi.TransitionFacts]{}
	raw, err := (&Service{}).BuildRunInput(t.Context(), facts)
	if err != nil {
		t.Fatal(err)
	}
	var input inputs.TriggerInput
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	run, _ := input.Run.Get()
	if run.Trigger.Present() || run.StatesHistory.Present() {
		t.Fatal("unknown ancestry/history became a fabricated empty value")
	}
}
