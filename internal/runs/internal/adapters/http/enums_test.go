package http_test

import (
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
)

// names are values' strings, sorted.
func names[T ~string](values []T) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	slices.Sort(out)
	return out
}

// TestUnitRunEnums holds internal/kernel's run states, run types, stack
// states and run priorities to the served enums both ways: each kernel
// value is one the API's type accepts, and the spec's schema lists every
// kernel value and no other.
func TestUnitRunEnums(t *testing.T) {
	spec, err := api.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		schema string
		kernel []string
		valid  func(string) bool
	}{
		{"RunState", names(kernel.RunStates()), func(s string) bool { return api.RunState(s).Valid() }},
		{"RunType", names(kernel.RunTypes()), func(s string) bool { return api.RunType(s).Valid() }},
		{
			"StackState", names(kernel.StackStates()),
			func(s string) bool { return api.StackState(s).Valid() },
		},
		{
			"RunPriorityPreset", names(kernel.RunPriorities()),
			func(s string) bool { return api.RunPriorityPreset(s).Valid() },
		},
	} {
		ref, ok := spec.Components.Schemas[c.schema]
		if !ok {
			t.Errorf("the spec has no %s", c.schema)
			continue
		}
		served := make([]string, 0, len(ref.Value.Enum))
		for _, v := range ref.Value.Enum {
			s, _ := v.(string)
			served = append(served, s)
		}
		slices.Sort(served)
		if !slices.Equal(c.kernel, served) {
			t.Errorf("%s: kernel %v, served %v", c.schema, c.kernel, served)
		}
		for _, v := range c.kernel {
			if !c.valid(v) {
				t.Errorf("%s does not accept the kernel's %s", c.schema, v)
			}
		}
	}
	for _, s := range kernel.RunStates() {
		st, ok := kernel.StackStateOf(s)
		if ok != api.StackState(s).Valid() || ok && string(st) != string(s) {
			t.Errorf("StackStateOf(%s) = %s, %v", s, st, ok)
		}
	}
}
