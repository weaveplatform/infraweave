package policyinput

import (
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac/plan"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
)

func TestUnitStructuralChangesPreservesAvailableMetadata(t *testing.T) {
	for kind, action := range policyActions {
		out, err := StructuralChanges(
			"terraform",
			[]plan.Change{{Address: `module.net["eu.west"].local_file.main[0]`, Metadata: plan.Metadata{Type: kind, Result: plan.ResultPlanned, Moved: true}}},
		)
		if err != nil || len(out) != 1 || out[0].Action != action || *out[0].ResourceType.Value != "local_file" ||
			*out[0].Name.Value != "main[0]" || *out[0].Module.Value != `module.net["eu.west"]` || !*out[0].Moved.Value || out[0].Phase != "plan" {
			t.Fatalf("structural change: %+v %v", out, err)
		}
	}
	for _, vendor := range []string{"opentofu", "terragrunt"} {
		out, err := StructuralChanges(vendor, []plan.Change{{Address: "opaque-address", Metadata: plan.Metadata{Type: plan.TypeAdd, Result: plan.ResultPlanned}}})
		if err != nil || len(out) != 1 || out[0].ResourceType.Known || out[0].Module.Known || out[0].Name.Known {
			t.Fatalf("unparsed address invented metadata: %+v %v", out, err)
		}
	}
	for _, test := range []struct {
		vendor string
		change plan.Change
	}{
		{"unknown", plan.Change{}},
		{"terraform", plan.Change{Address: "a.b", Metadata: plan.Metadata{Type: "UNKNOWN", Result: plan.ResultPlanned}}},
		{"terraform", plan.Change{Address: "a.b", Metadata: plan.Metadata{Type: plan.TypeAdd, Result: "APPLIED"}}},
		{"terraform", plan.Change{Metadata: plan.Metadata{Type: plan.TypeAdd, Result: plan.ResultPlanned}}},
	} {
		if _, err := StructuralChanges(test.vendor, []plan.Change{test.change}); !errors.Is(err, policyapi.ErrInput) {
			t.Fatalf("unsupported structural facts accepted: %v", err)
		}
	}
	if out, err := StructuralChanges("terraform", nil); err != nil || out == nil || len(out) != 0 {
		t.Fatalf("known empty changes are not an empty array: %+v %v", out, err)
	}
}
