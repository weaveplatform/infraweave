package core

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

func TestUnitInitializationInputUsesAssignmentFacts(t *testing.T) {
	facts := inputFacts()
	facts.Type, facts.Run.State = policyapi.Initialization, kernel.RunStateReady
	raw, err := (&Service{}).BuildRunInput(t.Context(), facts)
	if err != nil {
		t.Fatal(err)
	}
	var in inputs.InitializationInput
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	request, _ := in.Request.Get()
	at, _ := request.TimestampNS.Get()
	run, _ := in.Run.Get()
	state, _ := run.State.Get()
	commit, _ := in.Commit.Get()
	hash, _ := commit.Hash.Get()
	if at != facts.At.UnixNano() || state != "READY" || hash != "abc" {
		t.Fatalf("assignment facts changed: %s", raw)
	}
	for name, change := range map[string]func(*policyapi.RunInputFacts){
		"plan":           func(f *policyapi.RunInputFacts) { f.Plan = &policyapi.SanitizedPlan{} },
		"previous":       func(f *policyapi.RunInputFacts) { f.PreviousRun.Known = true },
		"review":         func(f *policyapi.RunInputFacts) { f.Reviews.Known = true },
		"trigger":        func(f *policyapi.RunInputFacts) { f.Trigger = &policyapi.TriggerContext{} },
		"missing commit": func(f *policyapi.RunInputFacts) { f.Run.Commit = policyapi.Fact[policyapi.CommitFacts]{} },
	} {
		t.Run(name, func(t *testing.T) {
			f := facts
			change(&f)
			if _, err := (&Service{}).BuildRunInput(t.Context(), f); !errors.Is(err, policyapi.ErrInput) {
				t.Fatalf("unsupported/unavailable initialization fact: %v", err)
			}
		})
	}
}
