package vendors_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/vendors"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/iac/workflow"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
)

// params are a run's parameters with the defaults the runner gives.
func params() workflow.Params {
	return workflow.Params{
		PlanFileName: vendors.PlanFile, Lock: true, Refresh: true, WorkspaceName: "default",
		LockTimeout: 5 * time.Minute,
	}
}

// TestUnitVendorsCommands renders the built-in workflow of Terraform
// and OpenTofu for each phase: init, then plan to PlanFile and its JSON
// to the plan file, then apply of PlanFile, each that locks waiting the
// lock timeout; planning without the lock, without refreshing and
// against targets adds their flags.
func TestUnitVendorsCommands(t *testing.T) {
	for _, tool := range []wire.RunSpecToolName{wire.Terraform, wire.Tofu} {
		w, err := vendors.Defaults(tool)
		if err != nil {
			t.Fatal(err)
		}
		bin := string(tool)
		replan := params()
		replan.Lock, replan.Refresh, replan.Targets = false, false, []string{"a.b", "c[0]"}
		replan.LockTimeout = 90 * time.Second
		for _, c := range []struct {
			state wire.WorkerState
			p     workflow.Params
			want  []vendors.Command
		}{
			{state: wire.WorkerStateINITIALIZING, p: params(), want: []vendors.Command{
				{Key: workflow.Init, Line: bin + " init -input=false -lock-timeout='5m0s'"},
			}},
			{state: wire.WorkerStatePLANNING, p: params(), want: []vendors.Command{
				{Key: workflow.Plan, Line: bin + " plan -input=false -lock-timeout='5m0s' " +
					"-lock='true'  -out='infraweave.plan' "},
				{Key: workflow.ShowPlan, Line: bin + " show -json 'infraweave.plan'", Stdout: "/p"},
			}},
			{state: wire.WorkerStatePLANNING, p: replan, want: []vendors.Command{
				{Key: workflow.Plan, Line: bin + " plan -input=false -lock-timeout='1m30s' " +
					"-lock='false' '-refresh=false' -out='infraweave.plan' '-target=a.b' " +
					"'-target=c[0]'"},
				{Key: workflow.ShowPlan, Line: bin + " show -json 'infraweave.plan'", Stdout: "/p"},
			}},
			{state: wire.WorkerStateAPPLYING, p: params(), want: []vendors.Command{
				{Key: workflow.Apply, Line: bin + " apply -input=false -lock-timeout='5m0s' " +
					"'infraweave.plan'"},
			}},
		} {
			got, err := w.Commands(
				t.Context(),
				c.state,
				vendors.Options{Params: c.p, PlanJSON: "/p"},
			)
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Errorf("%s %s = %q, %v", tool, c.state, got, err)
			}
		}
		_, err = w.Commands(t.Context(), wire.WorkerStatePERFORMING, vendors.Options{})
		if !errors.Is(err, vendors.ErrState) {
			t.Errorf("%s PERFORMING: %v", tool, err)
		}
	}
	if _, err := vendors.Defaults("pulumi"); !errors.Is(err, vendors.ErrTool) {
		t.Errorf("an unknown tool: %v", err)
	}
}

// file is a workflow file whose key's value is replaced by value.
func file(key workflow.Key, value string) string {
	var b strings.Builder
	for _, k := range workflow.Keys {
		v := "true"
		if k == key {
			v = value
		}
		b.WriteString(string(k) + ": " + v + "\n")
	}
	return b.String()
}

// TestUnitVendorsRead reads a workflow file, checking every key before
// any renders: a file that is not a workflow is workflow.ErrFile, and a
// template that does not compile is ErrCheck naming its key, its line and
// the range within it. No template reads now or random.*, which the
// workflow surface does not have.
func TestUnitVendorsRead(t *testing.T) {
	w, err := vendors.Read([]byte(file(workflow.Plan, "echo ${{ planFileName }}")))
	if err != nil {
		t.Fatal(err)
	}
	got, err := w.Commands(t.Context(), wire.WorkerStatePLANNING, vendors.Options{Params: params()})
	if err != nil || got[0].Line != "echo 'infraweave.plan'" || got[1].Line != "true" {
		t.Errorf("commands %q, %v", got, err)
	}
	for name, c := range map[string]struct {
		data string
		is   error
		want string
	}{
		"a missing key": {
			data: "init: true\n", is: workflow.ErrFile,
			want: "workspaceSelect, workspaceNew, plan, showState, showPlan, getOutputs, " +
				"apply, destroy missing",
		},
		"a type error": {
			data: file(workflow.Plan, "plan ${{ lock + 1 }}"), is: vendors.ErrCheck,
			want: "plan (line 4): 1:15: found no matching overload for '_+_'",
		},
		"a syntax error": {
			data: file(workflow.Apply, `apply ${{ planFileName + }}`), is: vendors.ErrCheck,
			want: "apply (line 8): 1:",
		},
		"now": {
			data: file(workflow.Init, "${{ now }}"), is: vendors.ErrCheck,
			want: "init (line 1): 1:5: undeclared reference to 'now'",
		},
		"random": {
			data: file(workflow.Destroy, "${{ random.string(4) }}"), is: vendors.ErrCheck,
			want: "destroy (line 9): 1:",
		},
		"an unknown variable": {
			data: file(workflow.ShowState, "${{ secrets.TOKEN }}"), is: vendors.ErrCheck,
			want: "showState (line 5): 1:5: undeclared reference to 'secrets'",
		},
	} {
		_, err := vendors.Read([]byte(c.data))
		if !errors.Is(err, c.is) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %v naming %s", name, err, c.is, c.want)
		}
	}
}

// TestUnitVendorsRenderFails fails a template whose evaluation fails, or
// whose value has no shell word, naming its key and range.
func TestUnitVendorsRenderFails(t *testing.T) {
	for name, c := range map[string]struct {
		template string
		p        func(*workflow.Params)
		want     string
	}{
		"fail": {
			template: `apply ${{ fail("stop") }}`, p: func(*workflow.Params) {},
			want: "apply: 1:15: stop",
		},
		"a NUL byte": {
			template: "apply ${{ workspaceName }}",
			p:        func(p *workflow.Params) { p.WorkspaceName = "a\x00b" },
			want:     "apply: 1:11: A value holding a NUL byte cannot be a shell word.",
		},
	} {
		w, err := vendors.Read([]byte(file(workflow.Apply, c.template)))
		if err != nil {
			t.Fatal(err)
		}
		p := params()
		c.p(&p)
		_, err = w.Commands(t.Context(), wire.WorkerStateAPPLYING, vendors.Options{Params: p})
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: %v, want %s", name, err, c.want)
		}
	}
}

// TestUnitVendorsSurface declares the run parameters on a shell surface
// with no context, so the runner's registry has the variables the
// editor's does.
func TestUnitVendorsSurface(t *testing.T) {
	s := vendors.Surface()
	if s.Name != workflow.Surface || !s.Shell || s.Env != nil ||
		len(s.Variables) != len(workflow.Variables) {
		t.Fatalf("surface %+v", s)
	}
	for i, v := range workflow.Variables {
		if !reflect.DeepEqual(s.Variables[i], expr.Variable{Name: v.Name, Schema: v.Schema}) {
			t.Errorf("variable %d: %+v", i, s.Variables[i])
		}
	}
}
