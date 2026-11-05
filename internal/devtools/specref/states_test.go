package specref

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// statesCapabilities is a capabilities register over parityReference. Five
// records claim more than testdata/states/cucumber.json shows.
const statesCapabilities = `{"records": [
  {"id": "tag:Stacks", "product": "deploy", "infraweave": {"state": "implemented"}},
  {"id": "tag:Account", "product": "deploy", "infraweave": {"state": "partial"}},
  {"id": "tag:Contexts", "product": "deploy", "infraweave": {"state": "stub"}},
  {"id": "tag:Policies", "product": "deploy", "infraweave": {"state": "superseded"}},
  {"id": "tag:Absent", "product": "deploy", "infraweave": {"state": "missing"}},
  {"id": "operation:sessionDeleteAll", "product": "deploy", "infraweave": {"state": "missing"}},
  {"id": "operation:stackLock", "product": "deploy", "infraweave": {"state": "stub"}},
  {"id": "operation:stackCreate", "product": "deploy", "infraweave": {"state": "partial"}},
  {"id": "operation:nothing", "product": "deploy", "infraweave": {"state": "missing"}},
  {"id": "resource:spacelift_stack", "product": "deploy", "infraweave": {"state": "implemented"}},
  {"id": "data-source:spacelift_stack", "product": "deploy", "spacelift": {"calls": ["stacks"]},
   "infraweave": {"state": "implemented"}},
  {"id": "data-source:spacelift_stacks", "product": "deploy", "infraweave": {"state": "partial"}},
  {"id": "resource:spacelift_context", "product": "deploy", "infraweave": {"state": "superseded"}},
  {"id": "resource:spacelift_outputs", "product": "deploy", "infraweave": {"state": "stub"}},
  {"id": "tier-feature:AUDITING", "product": "deploy", "infraweave": {"delivers": "x"}},
  {"id": "tag:Flows Projects", "product": "flows", "infraweave": {"state": "missing"}}]}`

// statesTerraform is terraform-operations.json for the register's rows;
// outputs is a field of Stack, not a root field, so it maps to nothing.
const statesTerraform = `{
  "resources": {"spacelift_stack": ["stack", "stacks", "notARootField"], "spacelift_outputs": ["outputs"]},
  "dataSources": {"spacelift_stack": ["stack"], "spacelift_stacks": ["stacks"]}}`

func readStates(t *testing.T) []CapabilityState {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "states", "cucumber.json"))
	if err != nil {
		t.Fatal(err)
	}
	scenarios, err := ReadCucumber(data)
	if err != nil {
		t.Fatal(err)
	}
	records, err := ReadCapabilities([]byte(statesCapabilities), "deploy")
	if err != nil {
		t.Fatal(err)
	}
	tf, err := ReadTerraformOperations([]byte(statesTerraform))
	if err != nil {
		t.Fatal(err)
	}
	return compareFixture(t).States(records, scenarios, tf)
}

func TestUnitReadCucumber(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "states", "cucumber.json"))
	if err != nil {
		t.Fatal(err)
	}
	scenarios, err := ReadCucumber(data)
	if err != nil {
		t.Fatal(err)
	}
	var passed []string
	for _, s := range scenarios {
		if s.Passed {
			passed = append(passed, s.Name)
		}
	}
	// The background is not a scenario; a failed or skipped step, or no
	// step at all, fails one.
	want := []string{
		"list stacks",
		"list stacks without a role",
		"list stacks with a bad cursor",
		"read a stack",
		"read a stack without a role",
		"create a stack",
		"read the AI switch",
		"read the AI switch without a role",
		"read the AI switch unauthenticated",
		"apply, import and destroy a stack",
	}
	if d := cmp.Diff(want, passed); d != "" {
		t.Errorf("passed (-want +got):\n%s", d)
	}
	if len(scenarios) != 14 {
		t.Errorf("%d scenarios, want 14", len(scenarios))
	}
	// A scenario carries its feature's tags.
	if d := cmp.Diff(
		[]string{"@postgres", "@op:stacks", "@case:success"},
		scenarios[0].Tags,
	); d != "" {
		t.Errorf("tags (-want +got):\n%s", d)
	}
	if _, err := ReadCucumber([]byte("{")); err == nil {
		t.Error("decoded a report that is not JSON")
	}
}

func TestUnitStates(t *testing.T) {
	got := map[string]State{}
	for _, s := range readStates(t) {
		got[s.ID] = s.Computed
	}
	want := map[string]State{
		// stacks and stack have all three kinds; stackCreate only a
		// success, and stackOutputs is missing.
		"tag:Stacks": StatePartial,
		// The rename's scenarios name it by either operationId.
		"tag:Account":                StateImplemented,
		"tag:Contexts":               StateMissing,
		"tag:Policies":               StateSuperseded,
		"tag:Absent":                 StateMissing,
		"operation:sessionDeleteAll": StateMissing,
		"operation:stackLock":        StateStub,
		"operation:stackCreate":      StateStub,
		"operation:nothing":          StateMissing,
		// Implemented operations and a passing provider scenario.
		"resource:spacelift_stack": StateImplemented,
		// The record's own calls replace the operation map's; its provider
		// scenario fails.
		"data-source:spacelift_stack": StatePartial,
		// No provider scenario at all.
		"data-source:spacelift_stacks": StatePartial,
		"resource:spacelift_context":   StateMissing,
		"resource:spacelift_outputs":   StateMissing,
	}
	if d := cmp.Diff(want, got); d != "" {
		t.Errorf("computed states (-want +got):\n%s", d)
	}
}

func TestUnitStateClaims(t *testing.T) {
	st := readStates(t)
	want := []string{
		"data-source:spacelift_stack claims implemented, and the acceptance results show partial over 1 operations",
		"operation:stackCreate claims partial, and the acceptance results show stub over 1 operations",
		"resource:spacelift_outputs claims stub, and the acceptance results show missing over 0 operations",
		"tag:Contexts claims stub, and the acceptance results show missing over 1 operations",
		"tag:Stacks claims implemented, and the acceptance results show partial over 6 operations",
	}
	if d := cmp.Diff(want, StateClaims(st)); d != "" {
		t.Errorf("claims (-want +got):\n%s", d)
	}
	text := StatesText(st)
	for _, line := range []string{
		"tag:Stacks: recorded implemented, computed partial  claims more\n",
		"tag:Policies: recorded superseded, computed superseded\n",
		"resource:spacelift_context: recorded superseded, computed missing\n",
	} {
		if !strings.Contains(text, line) {
			t.Errorf("text lacks %q:\n%s", line, text)
		}
	}
}

// TestUnitStatesWithoutEvidence proves that with no acceptance results every
// served operation is a stub at best, and a product without Terraform calls
// leaves its Terraform rows missing.
func TestUnitStatesWithoutEvidence(t *testing.T) {
	records, err := ReadCapabilities([]byte(statesCapabilities), "deploy")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range compareFixture(t).States(records, nil, nil) {
		switch s.ID {
		case "tag:Stacks":
			if s.Computed != StateStub && s.Computed != StateMissing {
				t.Errorf("%s computed %s", s.ID, s.Computed)
			}
		case "resource:spacelift_stack", "data-source:spacelift_stacks":
			if s.Computed != StateMissing {
				t.Errorf("%s computed %s without Terraform calls", s.ID, s.Computed)
			}
		case "data-source:spacelift_stack":
			if s.Computed != StateStub {
				t.Errorf("%s computed %s from its own calls", s.ID, s.Computed)
			}
		}
	}
}

func TestUnitReadCapabilities(t *testing.T) {
	deploy, err := ReadCapabilities([]byte(statesCapabilities), "deploy")
	if err != nil {
		t.Fatal(err)
	}
	if len(deploy) != 14 || deploy[10].ID != "data-source:spacelift_stack" ||
		!slices.Equal(deploy[10].Calls, []string{"stacks"}) {
		t.Errorf("deploy records %+v", deploy)
	}
	flows, err := ReadCapabilities([]byte(statesCapabilities), "flows")
	if err != nil || len(flows) != 1 || flows[0].State != StateMissing {
		t.Errorf("flows records %+v %v", flows, err)
	}
	if _, err := ReadCapabilities([]byte("{"), "deploy"); err == nil {
		t.Error("decoded a register that is not JSON")
	}
	if _, err := ReadTerraformOperations([]byte("[")); err == nil {
		t.Error("decoded an operation map that is not JSON")
	}
}
