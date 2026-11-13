package trigger_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/trigger"
)

// TestUnitCheckOverrides accepts none and good overrides, and refuses
// each bad name and value at its pointer with its reason, all at once.
func TestUnitCheckOverrides(t *testing.T) {
	if err := trigger.CheckOverrides(nil); err != nil {
		t.Errorf("no overrides = %v", err)
	}
	good := []lifecycle.Override{{Name: "REGION", Value: "us"}, {Name: "TF_VAR_size"}}
	if err := trigger.CheckOverrides(good); err != nil {
		t.Errorf("good overrides = %v", err)
	}
	err := trigger.CheckOverrides([]lifecycle.Override{
		{Name: ""},
		{Name: "A\x00B"},
		{Name: "A=B"},
		{Name: "TF_VAR_infraweave_run_id"},
		{Name: "INFRAWEAVE_API_TOKEN"},
		{Name: "REGION", Value: "eu"},
		{Name: "REGION", Value: "us"},
		{Name: "NUL", Value: "a\x00"},
	})
	inv, ok := errors.AsType[*kernel.Invalid](err)
	if !ok {
		t.Fatalf("bad overrides = %v", err)
	}
	got := make([]string, 0, len(inv.Errors))
	for _, e := range inv.Errors {
		got = append(got, e.Pointer+" "+e.Reason)
	}
	want := []string{
		"/environmentVarsOverrides/0/key " + kernel.ReasonRequired,
		"/environmentVarsOverrides/1/key " + kernel.ReasonRule,
		"/environmentVarsOverrides/2/key " + kernel.ReasonRule,
		"/environmentVarsOverrides/3/key " + kernel.ReasonRule,
		"/environmentVarsOverrides/4/key " + kernel.ReasonRule,
		"/environmentVarsOverrides/6/key " + kernel.ReasonDuplicate,
		"/environmentVarsOverrides/7/value " + kernel.ReasonRule,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("refusals:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
