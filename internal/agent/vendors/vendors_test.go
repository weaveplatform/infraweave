package vendors_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/vendors"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

func TestUnitVendorsCommands(t *testing.T) {
	o := vendors.Options{LockTimeout: 5 * time.Minute, PlanJSON: "/tmp/plan.json"}
	for _, tool := range []wire.RunSpecToolName{wire.Terraform, wire.Tofu} {
		v, err := vendors.Lookup(tool)
		if err != nil {
			t.Fatal(err)
		}
		bin := string(tool)
		for _, c := range []struct {
			state  wire.WorkerState
			noLock bool
			want   []vendors.Command
		}{
			{state: wire.WorkerStateINITIALIZING, want: []vendors.Command{
				{Args: []string{bin, "init", "-input=false", "-lock-timeout=5m0s"}},
			}},
			{state: wire.WorkerStatePLANNING, want: []vendors.Command{
				{Args: []string{
					bin, "plan", "-input=false", "-lock-timeout=5m0s", "-out=infraweave.plan",
				}},
				{Args: []string{bin, "show", "-json", "infraweave.plan"}, Stdout: "/tmp/plan.json"},
			}},
			{state: wire.WorkerStatePLANNING, noLock: true, want: []vendors.Command{
				{Args: []string{
					bin, "plan", "-input=false", "-lock-timeout=5m0s", "-lock=false",
					"-out=infraweave.plan",
				}},
				{Args: []string{bin, "show", "-json", "infraweave.plan"}, Stdout: "/tmp/plan.json"},
			}},
			{state: wire.WorkerStateAPPLYING, noLock: true, want: []vendors.Command{
				{Args: []string{bin, "apply", "-input=false", "-lock-timeout=5m0s", "infraweave.plan"}},
			}},
		} {
			o.NoLock = c.noLock
			got, err := v.Commands(c.state, o)
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Errorf("%s %s, no lock %t = %q, %v", tool, c.state, c.noLock, got, err)
			}
		}
		if _, err := v.Commands(wire.WorkerStatePERFORMING, o); !errors.Is(err, vendors.ErrState) {
			t.Errorf("%s PERFORMING: %v", tool, err)
		}
	}
	if _, err := vendors.Lookup("pulumi"); !errors.Is(err, vendors.ErrTool) {
		t.Errorf("an unknown tool: %v", err)
	}
}
