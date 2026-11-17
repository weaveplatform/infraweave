package terragrunt_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac/terragrunt"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// TestUnitRuntimeRunAll distinguishes an absent block from a present block
// without use_run_all, and accepts only a YAML boolean for the switch.
func TestUnitRuntimeRunAll(t *testing.T) {
	for _, tc := range []struct {
		name, yaml             string
		present, want, refused bool
	}{
		{name: "no block", yaml: "version: 1"},
		{name: "empty document"},
		{name: "comment-only document", yaml: "# no runtime settings\n"},
		{name: "end marker and comments", yaml: "# settings\nterragrunt: {use_run_all: true}\n...\n# trailing comment\n", present: true, want: true},
		{name: "run all", yaml: "terragrunt: {use_run_all: true}", present: true, want: true},
		{name: "single unit", yaml: "terragrunt: {use_run_all: false}", present: true},
		{name: "empty block", yaml: "terragrunt: {}", present: true},
		{name: "other keys", yaml: "terragrunt: {use_state_management: false}", present: true},
		{name: "boolean alias", yaml: "mode: &mode true\nterragrunt: {use_run_all: *mode}", present: true, want: true},
		{name: "invalid YAML", yaml: "terragrunt: [", refused: true},
		{name: "malformed trailing document", yaml: "terragrunt: {}\n---\n[", refused: true},
		{name: "second document", yaml: "terragrunt: {}\n---\nversion: 1", refused: true},
		{name: "second empty document", yaml: "terragrunt: {}\n---\n", refused: true},
		{name: "not a block", yaml: "terragrunt: 3", refused: true},
		{name: "null block", yaml: "terragrunt: null", refused: true},
		{name: "quoted boolean", yaml: "terragrunt: {use_run_all: \"true\"}", refused: true},
		{name: "quoted yes", yaml: "terragrunt: {use_run_all: \"yes\"}", refused: true},
		{name: "legacy yes", yaml: "terragrunt: {use_run_all: yes}", refused: true},
		{name: "number", yaml: "terragrunt: {use_run_all: 1}", refused: true},
		{name: "invalid explicit boolean", yaml: "terragrunt: {use_run_all: !!bool invalid}", refused: true},
		{name: "duplicate key", yaml: "terragrunt: {use_run_all: true, use_run_all: false}", refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(map[string]string{"yaml": tc.yaml})
			if err != nil {
				t.Fatal(err)
			}
			mode, err := terragrunt.RuntimeRunAll(raw)
			if tc.refused {
				inv, ok := errors.AsType[*kernel.Invalid](err)
				if !ok || len(inv.Errors) != 1 || inv.Errors[0].Pointer != "/runtimeConfig/yaml" {
					t.Fatalf("mode %v, refusal %v", mode, err)
				}
				return
			}
			if err != nil || (mode != nil) != tc.present || mode != nil && *mode != tc.want {
				t.Fatalf("mode %v, error %v; want present %t, value %t", mode, err, tc.present, tc.want)
			}
		})
	}
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`{"runnerImage":"custom"}`)} {
		if mode, err := terragrunt.RuntimeRunAll(raw); mode != nil || err != nil {
			t.Errorf("no YAML: mode %v, error %v", mode, err)
		}
	}
	if _, err := terragrunt.RuntimeRunAll(json.RawMessage(`{`)); err == nil {
		t.Error("invalid stored JSON accepted")
	}
}
