package runtimeconfig

import (
	"testing"

	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

func TestUnitRepositoryDiagnosticsReplay(t *testing.T) {
	note := "stacks.infra.before_plan: must be a list"
	spec := workersapi.Spec{Notices: []string{"unknown key stacks.infra.befor_plan"}, Refusal: &note}
	record, err := New(spec, false)
	if err != nil {
		t.Fatal(err)
	}
	var restored workersapi.Spec
	record.Apply(&restored)
	if restored.Refusal == nil || *restored.Refusal != note || len(restored.Notices) != 1 || restored.Notices[0] != spec.Notices[0] {
		t.Fatalf("repository diagnostics lost on replay: %+v", restored)
	}
	*restored.Refusal = "mutated"
	restored.Notices[0] = "mutated"
	*spec.Refusal = "input mutated"
	spec.Notices[0] = "input mutated"
	if *record.Execution.Refusal == "mutated" || *record.Execution.Refusal == "input mutated" || record.Execution.Notices[0] != "unknown key stacks.infra.befor_plan" {
		t.Fatal("a specification mutated the recorded diagnostics")
	}
}
