//go:build unix

package exec_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/exec"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/platform/mask"
)

// TestUnitExecTask performs a task after initialization: sh -c runs its
// command between the perform hooks with the session's environment, so
// an exit in it ends neither the session nor the after_perform hooks,
// and its output reaches the log masked. The run's type, which the
// server puts in the specification, is set in every phase.
func TestUnitExecTask(t *testing.T) {
	w := newWorld(t)
	w.w.then = map[wire.WorkerState]string{wire.WorkerStateINITIALIZING: "PERFORMING"}
	runType := "TASK"
	w.h.Spec.Run.Type = wire.TASK
	w.h.Spec.Environment = []wire.RunSpecVariable{
		{Name: "TF_VAR_infraweave_run_type", Value: &runType},
	}
	w.h.Spec.Task = &wire.RunSpecTask{Command: `terraform output -json && ` +
		`echo "the token is $INFRAWEAVE_API_TOKEN" && ` +
		hook("task", "TF_VAR_infraweave_run_state", "SEEN") + `; exit 0`}
	w.h.Spec.Hooks = map[string][]string{
		"before_init": {hook("before_init", "TF_VAR_infraweave_run_type")},
		"after_init":  {hook("after_init")},
		"before_perform": {
			hook("before_perform", "TF_VAR_infraweave_run_state"),
			"export SEEN=seen",
		},
		"after_perform": {hook("after_perform", "TF_VAR_infraweave_run_type")},
		"after_run":     {hook("after_run", "TF_VAR_infraweave_final_run_state")},
	}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	equal(t, "reports", w.events(), []string{
		"INITIALIZING started 3", "INITIALIZING succeeded 4", "PERFORMING succeeded 5",
	})
	equal(t, "trace", w.traced(), []string{
		"before_init TASK",
		"init -input=false -lock-timeout=5m0s",
		"after_init",
		"before_perform PERFORMING",
		"output -json",
		"task PERFORMING seen",
		"after_perform TASK",
		"after_run FINISHED",
	})
	out := w.out.String()
	if !strings.Contains(out, "the token is "+mask.Replacement+"\n") ||
		strings.Contains(out, "api-token") {
		t.Errorf("output: %s", out)
	}
}

// taskPhase resumes directly in PERFORMING when the worker first answers
// a fresh runner, so the task executes in a session with no initialization.
type taskPhase struct{ *worker }

func (p taskPhase) Report(ctx context.Context, token, run string, r wire.RunReport) (wire.NextStep, error) {
	step, err := p.worker.Report(ctx, token, run, r)
	if err == nil && r.Event == wire.Started {
		step.State = string(wire.WorkerStatePERFORMING)
	}
	return step, err
}

// TestUnitExecTaskSkipsInitialization runs neither init hook nor command,
// in the same session or when a fresh session starts with PERFORMING.
func TestUnitExecTaskSkipsInitialization(t *testing.T) {
	for _, tt := range []struct {
		name    string
		resumed bool
	}{
		{name: "same session"},
		{name: "resumed session", resumed: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			w.w.then = map[wire.WorkerState]string{wire.WorkerStateINITIALIZING: "PERFORMING"}
			w.h.Spec.Run.Type = wire.TASK
			w.h.Spec.Task = &wire.RunSpecTask{
				Command: hook("task", "TF_VAR_infraweave_run_state"), SkipInitialization: true,
			}
			w.h.Spec.Hooks = map[string][]string{
				"before_init": {hook("before_init", "TF_VAR_infraweave_run_state")},
				"after_init":  {hook("after_init")},
			}
			var reports exec.Reporter = w.w
			wantReports := []string{
				"INITIALIZING started 3", "INITIALIZING succeeded 4", "PERFORMING succeeded 5",
			}
			if tt.resumed {
				reports = taskPhase{w.w}
				wantReports = []string{"INITIALIZING started 3", "PERFORMING succeeded 4"}
			}
			if err := exec.Run(t.Context(), w.h, exec.Deps{
				Reports: reports, Logs: w.w, Uploads: w.w, Log: slog.New(slog.DiscardHandler), Out: &w.out,
				Workspace: w.workspace, Environ: w.environ,
			}); err != nil {
				t.Fatal(err)
			}
			equal(t, "reports", w.events(), wantReports)
			equal(t, "trace", w.traced(), []string{"task PERFORMING"})
			if !tt.resumed && !strings.Contains(w.out.String(), "infraweave: initialization is skipped\n") {
				t.Errorf("skip log: %s", w.out.String())
			}
		})
	}
}

// TestUnitExecDestroys destroys a destroy run's resources after it plans
// the destroy, and a test run's after it applies, between the destroy
// hooks; a failed destroy or task fails its phase, naming its exit
// status, and runs no after-hook of it.
func TestUnitExecDestroys(t *testing.T) {
	for name, c := range map[string]struct {
		typ    wire.RunSpecRunType
		then   map[wire.WorkerState]string
		task   *wire.RunSpecTask
		env    []string
		events []string
		traced []string
	}{
		"a destroy run": {
			typ:  wire.DESTROY,
			then: map[wire.WorkerState]string{wire.WorkerStatePLANNING: "DESTROYING"},
			events: []string{
				"INITIALIZING started 3", "INITIALIZING succeeded 4", "PLANNING succeeded 5",
				"DESTROYING succeeded 6",
			},
			traced: []string{
				"init -input=false -lock-timeout=5m0s",
				"plan -input=false -lock-timeout=5m0s -lock=true -destroy -out=infraweave.plan",
				"show -json infraweave.plan",
				"before_destroy DESTROYING",
				"destroy -auto-approve -input=false -lock-timeout=5m0s",
				"after_destroy",
				"after_run FINISHED",
			},
		},
		"a destroy that fails": {
			typ:  wire.DESTROY,
			then: map[wire.WorkerState]string{wire.WorkerStateINITIALIZING: "DESTROYING"},
			env:  []string{"EXIT_destroy=1"},
			events: []string{
				"INITIALIZING started 3", "INITIALIZING succeeded 4",
				"DESTROYING failed 5: The destroy command failed with exit status 1.",
			},
			traced: []string{
				"init -input=false -lock-timeout=5m0s",
				"before_destroy DESTROYING",
				"destroy -auto-approve -input=false -lock-timeout=5m0s",
				"after_run FAILED",
			},
		},
		"a test run": {
			typ: wire.TESTING,
			then: map[wire.WorkerState]string{
				wire.WorkerStatePLANNING: "APPLYING", wire.WorkerStateAPPLYING: "DESTROYING",
			},
			events: []string{
				"INITIALIZING started 3", "INITIALIZING succeeded 4", "PLANNING succeeded 5",
				"APPLYING succeeded 6", "DESTROYING succeeded 7",
			},
			traced: []string{
				"init -input=false -lock-timeout=5m0s",
				"plan -input=false -lock-timeout=5m0s -lock=true -out=infraweave.plan",
				"show -json infraweave.plan",
				"apply -input=false -lock-timeout=5m0s infraweave.plan",
				"output -json",
				"before_destroy DESTROYING",
				"destroy -auto-approve -input=false -lock-timeout=5m0s",
				"after_destroy",
				"after_run FINISHED",
			},
		},
		"a task that fails": {
			typ:  wire.TASK,
			then: map[wire.WorkerState]string{wire.WorkerStateINITIALIZING: "PERFORMING"},
			task: &wire.RunSpecTask{Command: "echo failing; exit 3"},
			events: []string{
				"INITIALIZING started 3", "INITIALIZING succeeded 4",
				"PERFORMING failed 5: The task's command failed with exit status 3.",
			},
			traced: []string{"init -input=false -lock-timeout=5m0s", "after_run FAILED"},
		},
		"a task run with no task": {
			typ:  wire.TASK,
			then: map[wire.WorkerState]string{wire.WorkerStateINITIALIZING: "PERFORMING"},
			events: []string{
				"INITIALIZING started 3", "INITIALIZING succeeded 4",
				"PERFORMING failed 5: The run's specification names no task to perform.",
			},
			traced: []string{"init -input=false -lock-timeout=5m0s", "after_run FAILED"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.w.then = c.then
			w.environ = append(w.environ, c.env...)
			w.h.Spec.Run.Type, w.h.Spec.Task = c.typ, c.task
			w.h.Spec.Hooks = map[string][]string{
				"before_destroy": {hook("before_destroy", "TF_VAR_infraweave_run_state")},
				"after_destroy":  {hook("after_destroy")},
				"after_perform":  {hook("after_perform")},
				"after_run":      {hook("after_run", "TF_VAR_infraweave_final_run_state")},
			}
			if err := w.run(); err != nil {
				t.Fatal(err)
			}
			equal(t, "reports", w.events(), c.events)
			equal(t, "trace", w.traced(), c.traced)
		})
	}
}
