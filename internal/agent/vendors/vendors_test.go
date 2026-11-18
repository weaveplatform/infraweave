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
// to the plan file, then apply of PlanFile, and destroy with no prompt,
// each that locks waiting the lock timeout; planning without the lock,
// without refreshing and against targets adds their flags. PERFORMING,
// whose command is the task's, has no workflow phase.
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
				{Key: workflow.GetOutputs, Line: bin + " output -json", Stdout: "/o"},
			}},
			{state: wire.WorkerStateDESTROYING, p: params(), want: []vendors.Command{
				{Key: workflow.Destroy, Line: bin + " destroy -auto-approve -input=false " +
					"-lock-timeout='5m0s'"},
			}},
		} {
			got, err := w.Commands(
				t.Context(),
				c.state,
				vendors.Options{Params: c.p, PlanJSON: "/p", OutputsJSON: "/o"},
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

// TestUnitVendorsTerragruntCommands renders a Terragrunt run's built-in
// workflow: each phase's command through terragrunt run --all, planning
// with TG_JSON_OUT_DIR naming the units' plans' directory and no
// showPlan, applying with no saved plan and no getOutputs, and destroying
// as Terragrunt approves it. A workflow file's templates render in the
// same phases.
func TestUnitVendorsTerragruntCommands(t *testing.T) {
	for _, runAll := range []bool{false, true} {
		w, err := vendors.Terragrunt(runAll)
		if err != nil {
			t.Fatal(err)
		}
		p := params()
		p.PlanFileName = workflow.TerragruntPlanFile
		o := vendors.Options{Params: p, PlanJSON: "/p", OutputsJSON: "/o", UnitPlans: "/plans"}
		all, approve := "terragrunt run -- ", "-auto-approve "
		if runAll {
			all, approve = "terragrunt run --all -- ", ""
		}
		for _, c := range []struct {
			state wire.WorkerState
			want  []vendors.Command
		}{
			{wire.WorkerStateINITIALIZING, []vendors.Command{
				{Key: workflow.Init, Line: all + "init -input=false -lock-timeout='5m0s'"},
			}},
			{wire.WorkerStatePLANNING, []vendors.Command{{
				Key: workflow.Plan, Line: all + "plan -input=false -lock-timeout='5m0s' " +
					"-lock='true'  -out='tfplan.tfplan' ",
				Env: []string{"TG_JSON_OUT_DIR=/plans"},
			}}},
			{wire.WorkerStateAPPLYING, []vendors.Command{
				{Key: workflow.Apply, Line: all + "apply " + approve + "-input=false -lock-timeout='5m0s'"},
			}},
			{wire.WorkerStateDESTROYING, []vendors.Command{
				{Key: workflow.Destroy, Line: all + "destroy " + approve + "-input=false -lock-timeout='5m0s'"},
			}},
		} {
			got, err := w.Commands(t.Context(), c.state, o)
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Errorf("%s = %q, %v", c.state, got, err)
			}
		}
		if _, err := w.Commands(t.Context(), wire.WorkerStatePERFORMING, o); !errors.Is(err,
			vendors.ErrState) {
			t.Errorf("PERFORMING: %v", err)
		}
		f, err := vendors.Read([]byte(file(workflow.Plan, "echo ${{ planFileName }}")))
		if err != nil {
			t.Fatal(err)
		}
		got, err := f.Commands(t.Context(), wire.WorkerStatePLANNING, o)
		if err != nil || len(got) != 1 || got[0].Line != "echo 'tfplan.tfplan'" ||
			!reflect.DeepEqual(got[0].Env, []string{"TG_JSON_OUT_DIR=/plans"}) {
			t.Errorf("a workflow file's planning = %q, %v", got, err)
		}
		command, err := w.Command(t.Context(), workflow.ShowPlan, o)
		if err != nil || command.Line != "terragrunt run -- show -json 'tfplan.tfplan'" || command.Stdout != "/p" {
			t.Errorf("showPlan fallback = %+v, %v", command, err)
		}
		custom := strings.Replace(file(workflow.Plan, "echo ${{ planFileName }}"), "showPlan: true", "showPlan: echo forbidden-show-plan", 1)
		custom = strings.Replace(custom, "getOutputs: true", "getOutputs: echo forbidden-get-outputs", 1)
		f, err = vendors.Read([]byte(custom))
		if err != nil {
			t.Fatal(err)
		}
		for _, phase := range []struct {
			state wire.WorkerState
			key   workflow.Key
		}{
			{wire.WorkerStateINITIALIZING, workflow.Init},
			{wire.WorkerStatePLANNING, workflow.Plan},
			{wire.WorkerStateAPPLYING, workflow.Apply},
			{wire.WorkerStateDESTROYING, workflow.Destroy},
		} {
			commands, err := f.Commands(t.Context(), phase.state, o)
			if err != nil || len(commands) != 1 || commands[0].Key != phase.key {
				t.Errorf("runAll=%t custom %s ran a read command: %+v, %v", runAll, phase.state, commands, err)
			}
		}
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
