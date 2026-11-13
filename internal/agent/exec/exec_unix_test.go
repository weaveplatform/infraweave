//go:build unix

package exec_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/backend"
	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/exec"
	"github.com/weaveplatform/infraweave/internal/agent/handoff"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// fakeTool is a terraform that appends its arguments to $TRACE, prints
// $PLAN for show, and exits with $EXIT_<subcommand>, 0 when unset.
const fakeTool = `#!/bin/sh
printf '%s\n' "$*" >> "$TRACE"
[ "$1" = show ] && cat "$PLAN"
eval "exit \${EXIT_$1:-0}"
`

// planJSON is show -json of a plan that adds one resource.
const planJSON = `{"format_version":"1.2","resource_changes":[` +
	`{"address":"terraform_data.x","mode":"managed","change":{"actions":["create"]}}]}`

// worker is a fake worker API that holds one run: it applies a report
// that names the run's state version, answers a repeated report the step
// it answered before, refuses any other with 409, and can lose answers.
// It takes the run's log chunks, and keeps the order in which it took
// them and the reports.
type worker struct {
	mu         sync.Mutex
	version    int64
	autodeploy bool
	lose       int
	refuse     int
	reports    []wire.RunReport
	last       wire.RunReport
	step       wire.NextStep
	objects    map[string][]byte
	taken      []string
}

func (w *worker) UploadURLs(
	_ context.Context, _, _ string, in wire.UploadURLsRequest,
) ([]wire.SignedUpload, error) {
	out := make([]wire.SignedUpload, 0, len(in.Uploads))
	for _, u := range in.Uploads {
		out = append(out, wire.SignedUpload{
			Kind: u.Kind, Name: u.Name, Key: strconv.FormatInt(in.StateVersion, 10) + "/" + u.Name,
		})
	}
	return out, nil
}

func (w *worker) Put(_ context.Context, u wire.SignedUpload, body []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.objects == nil {
		w.objects = map[string][]byte{}
	}
	w.objects[u.Key] = bytes.Clone(body)
	return nil
}

func (w *worker) LogChunks(_ context.Context, _, _ string, in wire.LogChunksRequest) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, c := range in.Chunks {
		key := strconv.FormatInt(in.StateVersion, 10) + "/" + strconv.FormatInt(c.Seq, 10)
		w.taken = append(w.taken, "log "+key+": "+string(w.objects[key]))
	}
	return nil
}

var errLost = errors.New("the answer was lost")

func (w *worker) Report(
	_ context.Context,
	token, run string,
	r wire.RunReport,
) (wire.NextStep, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reports = append(w.reports, r)
	w.taken = append(w.taken, "report "+string(r.State)+" "+string(r.Event))
	if token != "run-token" || run != "run-1" {
		return wire.NextStep{}, &client.Error{Op: "report", Status: 401}
	}
	if w.refuse != 0 {
		return wire.NextStep{}, &client.Error{Op: "report", Status: w.refuse}
	}
	if len(w.reports) > 1 && reflect.DeepEqual(r, w.last) {
		return w.step, nil
	}
	if r.StateVersion != w.version {
		return wire.NextStep{}, &client.Error{Op: "report", Status: 409}
	}
	w.last, w.step = r, w.decide(r)
	w.version = w.step.StateVersion
	if w.lose > 0 {
		w.lose--
		return wire.NextStep{}, errLost
	}
	return w.step, nil
}

// decide is the next step of r: the phase a start names, the next phase
// after a success, and the run's end after a failure.
func (w *worker) decide(r wire.RunReport) wire.NextStep {
	next := func(a wire.NextStepAction, state string) wire.NextStep {
		return wire.NextStep{Action: a, State: state, StateVersion: r.StateVersion + 1}
	}
	switch {
	case r.Event == wire.Started:
		return next(wire.Execute, string(r.State))
	case r.Event == wire.Failed:
		return next(wire.Exit, "FAILED")
	case r.State == wire.WorkerStateINITIALIZING:
		return next(wire.Execute, "PLANNING")
	case r.State == wire.WorkerStatePLANNING && w.autodeploy:
		return next(wire.Execute, "APPLYING")
	case r.State == wire.WorkerStatePLANNING:
		return next(wire.Pause, "UNCONFIRMED")
	}
	return next(wire.Exit, "FINISHED")
}

// world is one run's workspace, tool and worker.
type world struct {
	t         *testing.T
	h         handoff.Handoff
	w         *worker
	workspace string
	trace     string
	plan      string
	environ   []string
	out       lockedBuffer
}

// lockedBuffer is the session's output: the shell's and a syntax check's
// output are copied by goroutines of their own.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p) //nolint:wrapcheck // a test double
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newWorld(t *testing.T) *world {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	ws := filepath.Join(dir, "workspace")
	for _, d := range []string{bin, ws} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "terraform"), []byte(fakeTool), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(plan, []byte(planJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(dir, "trace")
	return &world{
		t: t, workspace: ws, trace: trace, plan: plan,
		w: &worker{version: 3},
		environ: []string{
			"PATH=" + bin + ":/usr/bin:/bin", "TRACE=" + trace, "PLAN=" + plan,
		},
		h: handoff.Handoff{
			Endpoint: "http://server.test/worker/v1", Run: "run-1", RunToken: "run-token",
			Assigned: wire.AssignmentStatePREPARING, StateVersion: 3,
			StateLockTimeout: 5 * time.Minute,
			Spec: wire.RunSpec{
				Commit: wire.RunSpecCommit{Hash: "abc123"},
				Tool:   &wire.RunSpecTool{Name: wire.Terraform, Version: "1.17.2"},
				Hooks:  map[string][]string{},
			},
			Secrets: wire.RunSpecSecrets{ApiToken: "api-token"},
		},
	}
}

// run runs the world's run and answers Run's error.
func (w *world) run() error {
	w.t.Helper()
	return exec.Run(w.t.Context(), w.h, exec.Deps{
		Reports: w.w, Logs: w.w, Log: slog.New(slog.DiscardHandler), Out: &w.out,
		Workspace: w.workspace, Environ: w.environ,
	})
}

// traced is what the tool and the hooks wrote to $TRACE, by line.
func (w *world) traced() []string {
	w.t.Helper()
	b, err := os.ReadFile(w.trace)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		w.t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

// events are the reports as STATE event version.
func (w *world) events() []string {
	out := make([]string, 0, len(w.w.reports))
	for _, r := range w.w.reports {
		e := string(r.State) + " " + string(r.Event) + " " + strconv.FormatInt(r.StateVersion, 10)
		if r.Details != nil {
			e += ": " + *r.Details
		}
		out = append(out, e)
	}
	return out
}

func equal(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

// hook appends name and the values of vars to $TRACE.
func hook(name string, vars ...string) string {
	s := `printf '%s\n' "` + name
	for _, v := range vars {
		s += ` $` + v
	}
	return s + `" >> "$TRACE"`
}

func TestUnitExecAutodeploy(t *testing.T) {
	w := newWorld(t)
	w.w.autodeploy = true
	root := "infra"
	if err := os.Mkdir(filepath.Join(w.workspace, root), 0o755); err != nil {
		t.Fatal(err)
	}
	script, mode, greeting, input := []byte("echo hi\n"), "700", "hello", "false"
	w.h.Spec.Stack.ProjectRoot = &root
	w.h.Spec.StateBackend = &wire.RunSpecStateBackend{Path: "/state/v1/stacks/infra"}
	w.h.Spec.Environment = []wire.RunSpecVariable{
		{Name: "TF_VAR_greeting", Value: &greeting},
		{Name: "SECRET", WriteOnly: true},
		{Name: "TF_INPUT", Value: &input},
	}
	w.h.Spec.Files = []wire.RunSpecFile{
		{Path: "infra/scripts/run.sh", Content: &script, FileMode: &mode},
		{Path: "secret.txt", WriteOnly: true},
		{Path: "empty"},
	}
	w.h.Secrets.Environment = map[string]string{"SECRET": "s3cret"}
	w.h.Secrets.Files = map[string][]byte{"secret.txt": []byte("hidden")}
	w.h.Spec.Hooks = map[string][]string{
		"before_init": {
			hook("before_init", "TF_VAR_infraweave_run_state"), "export SEEN=from-init",
			hook("env", "TF_VAR_greeting", "SECRET", "INFRAWEAVE_API_TOKEN", "TF_IN_AUTOMATION",
				"TF_INPUT", "TF_HTTP_USERNAME", "TF_HTTP_PASSWORD"),
		},
		"after_init":   {hook("after_init")},
		"before_plan":  {hook("before_plan", "TF_VAR_infraweave_run_state")},
		"after_plan":   {hook("after_plan", "SEEN")},
		"before_apply": {hook("before_apply", "TF_VAR_infraweave_run_state")},
		"after_apply":  {hook("after_apply")},
		"after_run":    {hook("after_run", "TF_VAR_infraweave_final_run_state", "SEEN")},
	}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	equal(t, "reports", w.events(), []string{
		"INITIALIZING started 3", "INITIALIZING succeeded 4", "PLANNING succeeded 5",
		"APPLYING succeeded 6",
	})
	if d := w.w.reports[2].Delta; d == nil || d.Add != 1 || *d.Resources != 0 {
		t.Errorf("planning's delta: %+v", d)
	}
	for i, r := range w.w.reports {
		if (r.Delta != nil) != (i == 2) {
			t.Errorf("report %d's delta: %+v", i, r.Delta)
		}
	}
	equal(t, "trace", w.traced(), []string{
		"before_init INITIALIZING",
		"env hello s3cret api-token 1 false run run-token",
		"init -input=false -lock-timeout=5m0s",
		"after_init",
		"before_plan PLANNING",
		"plan -input=false -lock-timeout=5m0s -out=infraweave.plan",
		"show -json infraweave.plan",
		"after_plan from-init",
		"before_apply APPLYING",
		"apply -input=false -lock-timeout=5m0s infraweave.plan",
		"after_apply",
		"after_run FINISHED from-init",
	})
	if !strings.Contains(w.out.String(),
		"infraweave: working on commit abc123, read from its repository with no credential\n") {
		t.Errorf("output: %s", w.out.String())
	}
	for name, want := range map[string]fs.FileMode{
		"infra/scripts/run.sh": 0o700, "secret.txt": 0o600, "empty": 0o644,
		"infra/" + backend.File: 0o600,
	} {
		info, err := os.Stat(filepath.Join(w.workspace, name))
		if err != nil || info.Mode().Perm() != want {
			t.Errorf("%s: %v, %v", name, info, err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(w.workspace, "secret.txt")); string(b) != "hidden" ||
		err != nil {
		t.Errorf("secret.txt: %q, %v", b, err)
	}
}

func TestUnitExecPausesAndResumes(t *testing.T) {
	w := newWorld(t)
	w.h.Spec.Hooks = map[string][]string{"after_run": {hook("after_run")}}
	w.environ = append(w.environ, exec.DisableStateLock+"=false")
	w.h.Spec.Environment = []wire.RunSpecVariable{{Name: exec.DisableStateLock, WriteOnly: true}}
	w.h.Secrets.Environment = map[string]string{exec.DisableStateLock: "true"}
	w.h.StateLockTimeout = 90 * time.Second
	w.h.Spec.Source.Narrowed = true
	w.h.Secrets.SourceCredential = &wire.RunSpecSourceCredential{Username: "x", Token: "y"}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.out.String(), "infraweave: working on commit abc123, read from its "+
		"repository with a credential narrowed to the repository and the run\n") {
		t.Errorf("output: %s", w.out.String())
	}
	equal(t, "reports", w.events(), []string{
		"INITIALIZING started 3", "INITIALIZING succeeded 4", "PLANNING succeeded 5",
	})
	equal(t, "trace", w.traced(), []string{
		"init -input=false -lock-timeout=1m30s",
		"plan -input=false -lock-timeout=1m30s -lock=false -out=infraweave.plan",
		"show -json infraweave.plan",
	})

	// The run resumes in a new container after confirmation.
	a := newWorld(t)
	a.h.Assigned, a.h.StateVersion, a.w.version = wire.AssignmentStatePREPARINGAPPLY, 8, 8
	a.h.Spec.Hooks = map[string][]string{
		"after_run": {hook("after_run", "TF_VAR_infraweave_final_run_state")},
	}
	if err := a.run(); err != nil {
		t.Fatal(err)
	}
	equal(t, "resumed reports", a.events(), []string{
		"APPLYING started 8", "APPLYING succeeded 9",
	})
	equal(t, "resumed trace", a.traced(), []string{
		"apply -input=false -lock-timeout=5m0s infraweave.plan", "after_run FINISHED",
	})
	if strings.Contains(a.out.String(), "working on commit") {
		t.Errorf("a resumed run announced its commit: %s", a.out.String())
	}
}

// TestUnitExecFailures checks that the first hook or command that fails
// fails its phase, naming it and its exit status, that nothing after it
// in the phase runs, and that after_run runs with the final state.
func TestUnitExecFailures(t *testing.T) {
	for name, c := range map[string]struct {
		hooks  map[string][]string
		env    []string
		want   string
		traced []string
	}{
		"a before-hook": {
			hooks:  map[string][]string{"before_plan": {"true", "exit_code=3; (exit 3)"}},
			want:   "PLANNING failed 5: The before_plan hook 2 of 2 failed with exit status 3.",
			traced: []string{"init -input=false -lock-timeout=5m0s", "after_run FAILED set"},
		},
		"a command": {
			env:  []string{"EXIT_plan=2"},
			want: "PLANNING failed 5: The command terraform plan failed with exit status 2.",
			traced: []string{
				"init -input=false -lock-timeout=5m0s",
				"plan -input=false -lock-timeout=5m0s -out=infraweave.plan", "after_run FAILED set",
			},
		},
		"an after-hook": {
			hooks:  map[string][]string{"after_init": {"false", hook("never")}},
			want:   "INITIALIZING failed 4: The after_init hook 1 of 2 failed with exit status 1.",
			traced: []string{"init -input=false -lock-timeout=5m0s", "after_run FAILED set"},
		},
		"a hook the shell cannot parse": {
			hooks:  map[string][]string{"before_init": {`echo "unterminated`}},
			want:   "INITIALIZING failed 4: The before_init hook 2 of 2 failed with exit status 2.",
			traced: []string{"after_run FAILED set"},
		},
		"a hook that exits the shell": {
			hooks: map[string][]string{"before_init": {"exit 4"}},
			want: "INITIALIZING failed 4: The before_init hook 2 of 2 ended the run's shell " +
				"with exit status 4.",
			traced: []string{"after_run FAILED "},
		},
		"a hook that exits the shell with 0": {
			hooks: map[string][]string{"after_init": {"exit 0"}},
			want: "INITIALIZING failed 4: The after_init hook 1 of 1 ended the run's shell " +
				"with exit status 0.",
		},
		"a plan whose JSON is not a plan": {
			env:    []string{"PLAN=/dev/null"},
			want:   "PLANNING failed 5: The plan's JSON could not be read: ",
			traced: nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.environ = append(w.environ, c.env...)
			w.h.Spec.Hooks = map[string][]string{
				"before_init": {"export SET=set"},
				"after_run":   {hook("after_run", "TF_VAR_infraweave_final_run_state", "SET")},
			}
			for k, v := range c.hooks {
				w.h.Spec.Hooks[k] = append(w.h.Spec.Hooks[k], v...)
			}
			if err := w.run(); err != nil {
				t.Fatal(err)
			}
			events := w.events()
			if last := events[len(events)-1]; !strings.HasPrefix(last, c.want) {
				t.Errorf("the last report: %s, want %s", last, c.want)
			}
			if c.traced != nil {
				equal(t, "trace", w.traced(), c.traced)
			}
		})
	}
}

// TestUnitExecPreparationFails checks that a run the runner cannot
// prepare fails its first phase with a note that says why, running no
// hook.
func TestUnitExecPreparationFails(t *testing.T) {
	outside, missing, bad := "../elsewhere", "missing", "rwx"
	for name, c := range map[string]struct {
		edit func(w *world)
		want string
	}{
		"no tool": {
			edit: func(w *world) { w.h.Spec.Tool = nil },
			want: "The run's specification names no tool to run.",
		},
		"another tool": {
			edit: func(w *world) { w.h.Spec.Tool.Name = "pulumi" },
			want: "The runner has no commands for pulumi.",
		},
		"a project root outside the workspace": {
			edit: func(w *world) { w.h.Spec.Stack.ProjectRoot = &outside },
			want: "The project root cannot be used: ",
		},
		"a project root that is missing": {
			edit: func(w *world) { w.h.Spec.Stack.ProjectRoot = &missing },
			want: "The project root missing is not a directory of the workspace.",
		},
		"a file outside the workspace": {
			edit: func(w *world) { w.h.Spec.Files = []wire.RunSpecFile{{Path: "../x"}} },
			want: "The mounted files cannot be written: a mounted file cannot be written: " +
				"../x is not under the workspace.",
		},
		"a write-only file without its content": {
			edit: func(w *world) { w.h.Spec.Files = []wire.RunSpecFile{{Path: "x", WriteOnly: true}} },
			want: "The mounted files cannot be written: a mounted file cannot be written: " +
				"x's content is not in the sealed part.",
		},
		"a file mode that is not octal": {
			edit: func(w *world) {
				w.h.Spec.Files = []wire.RunSpecFile{{Path: "x", FileMode: &bad}}
			},
			want: `The mounted files cannot be written: a mounted file cannot be written: ` +
				`x's mode "rwx" is not octal permission bits.`,
		},
		"a file under a file": {
			edit: func(w *world) {
				if err := os.WriteFile(filepath.Join(w.workspace, "f"), nil, 0o600); err != nil {
					w.t.Fatal(err)
				}
				w.h.Spec.Files = []wire.RunSpecFile{{Path: "f/g"}}
			},
			want: "The mounted files cannot be written: a mounted file cannot be written: f/g: ",
		},
		"a file over a directory": {
			edit: func(w *world) {
				if err := os.MkdirAll(filepath.Join(w.workspace, "d", "e"), 0o755); err != nil {
					w.t.Fatal(err)
				}
				w.h.Spec.Files = []wire.RunSpecFile{{Path: "d"}}
			},
			want: "The mounted files cannot be written: a mounted file cannot be written: d: ",
		},
		"a state backend the endpoint cannot reach": {
			edit: func(w *world) {
				w.h.Endpoint = "http://server.test/elsewhere"
				w.h.Spec.StateBackend = &wire.RunSpecStateBackend{Path: "/state/v1/stacks/infra"}
			},
			want: "The state backend cannot be configured: ",
		},
		"no shell": {
			edit: func(w *world) { w.environ = []string{"PATH=" + w.t.TempDir()} },
			want: "The run's image has no sh to run its phases in.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.h.Spec.Hooks = map[string][]string{
				"before_init": {hook("before_init")}, "after_run": {hook("after_run")},
			}
			c.edit(w)
			if err := w.run(); err != nil {
				t.Fatal(err)
			}
			equal(t, "reports", w.events()[:1], []string{"INITIALIZING started 3"})
			if got := w.events()[1]; !strings.HasPrefix(got, "INITIALIZING failed 4: ") ||
				!strings.Contains(got, c.want) {
				t.Errorf("failed: %s, want %s", got, c.want)
			}
			if got := w.traced(); slices.Contains(got, "before_init") {
				t.Errorf("trace: %q", got)
			}
		})
	}
}

// TestUnitExecReports checks a lost answer sent again with the same body,
// which moves the run once; a 409, which ends the runner with no further
// report; another refusal, which Run answers; and a phase the runner has
// no commands for.
func TestUnitExecReports(t *testing.T) {
	w := newWorld(t)
	w.w.lose = 1
	w.h.Spec.Hooks = map[string][]string{"after_run": {hook("after_run")}}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	equal(t, "reports", w.events(), []string{
		"INITIALIZING started 3", "INITIALIZING started 3", "INITIALIZING succeeded 4",
		"PLANNING succeeded 5",
	})

	w = newWorld(t)
	w.h.Spec.Hooks = map[string][]string{
		"after_init": {"exit_code=0"}, "after_run": {hook("after_run")},
	}
	w.w.version = 2
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	equal(t, "moved on", w.events(), []string{"INITIALIZING started 3"})
	equal(t, "moved on trace", w.traced(), nil)

	w = newWorld(t)
	w.h.Secrets.SourceCredential = &wire.RunSpecSourceCredential{Username: "x", Token: "y"}
	w.w.refuse = 400
	if err := w.run(); client.Status(err) != 400 {
		t.Errorf("a refused report: %v", err)
	}

	w = newWorld(t)
	w.h.Assigned = wire.AssignmentStatePREPARINGREPLAN
	w.h.Spec.Tool.Name = wire.Tofu
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	if got := w.events(); len(got) != 2 || !strings.HasPrefix(got[1],
		"PLANNING failed 4: The command tofu plan failed with exit status 127.") {
		t.Errorf("tofu, which the image lacks: %q", got)
	}
}

// actionless answers a step whose action the runner does not know.
type actionless struct{}

func (actionless) Report(context.Context, string, string, wire.RunReport) (wire.NextStep, error) {
	return wire.NextStep{Action: "dance", State: "PLANNING", StateVersion: 4}, nil
}

func TestUnitExecUnknownAction(t *testing.T) {
	w := newWorld(t)
	err := exec.Run(t.Context(), w.h, exec.Deps{
		Reports: actionless{}, Logs: w.w, Log: slog.New(slog.DiscardHandler), Out: &w.out,
		Workspace: w.workspace, Environ: w.environ,
	})
	if !errors.Is(err, exec.ErrAction) {
		t.Fatalf("Run: %v", err)
	}
}

// performing answers execute PERFORMING, which this runner has no
// commands for.
type performing struct{ reports []wire.RunReport }

func (p *performing) Report(
	_ context.Context, _, _ string, r wire.RunReport,
) (wire.NextStep, error) {
	p.reports = append(p.reports, r)
	if r.Event == wire.Started {
		return wire.NextStep{Action: wire.Execute, State: "PERFORMING", StateVersion: 4}, nil
	}
	return wire.NextStep{Action: wire.Exit, State: "FAILED", StateVersion: 5}, nil
}

func TestUnitExecNoCommands(t *testing.T) {
	w := newWorld(t)
	p := &performing{}
	if err := exec.Run(t.Context(), w.h, exec.Deps{
		Reports: p, Logs: w.w, Log: slog.New(slog.DiscardHandler), Out: &w.out,
		Workspace: w.workspace, Environ: w.environ,
	}); err != nil {
		t.Fatal(err)
	}
	if len(p.reports) != 2 || *p.reports[1].Details != "This runner executes no PERFORMING phase." {
		t.Fatalf("reports: %+v", p.reports)
	}
}

// TestUnitExecLog masks a write-only variable, a line of a write-only
// file, a variable launcher.mask_envs names, an ::add-mask word, the run's
// tokens and a well-known token in standard output and in every chunk,
// leaves a value under 4 bytes visible, and commits each phase's lines
// before its report; the after_run hooks' output goes to standard output
// alone.
func TestUnitExecLog(t *testing.T) {
	w := newWorld(t)
	w.w.autodeploy = true
	plain, short := "plainpass", "ab"
	w.h.MaskEnvs = []string{"PLAIN_PASS", "UNSET"}
	w.h.Spec.Stack.EnableWellKnownSecretMasking = true
	w.h.Spec.StateBackend = &wire.RunSpecStateBackend{Path: "/state/v1/stacks/infra"}
	w.h.Spec.Environment = []wire.RunSpecVariable{
		{Name: "SECRET", WriteOnly: true},
		{Name: "SHORT", WriteOnly: true},
		{Name: "PLAIN_PASS", Value: &plain},
		{Name: "VISIBLE", Value: &short},
	}
	w.h.Spec.Files = []wire.RunSpecFile{{Path: "key.txt", WriteOnly: true}}
	w.h.Secrets.Environment = map[string]string{"SECRET": "hunter22", "SHORT": short}
	w.h.Secrets.Files = map[string][]byte{"key.txt": []byte("first-line\nsecond-line\n")}
	w.h.Spec.Hooks = map[string][]string{
		"before_init": {
			`echo "secret $SECRET short $SHORT plain $PLAIN_PASS"`,
			`cat key.txt`,
			`printf 'split hun'; printf 'ter22\n'`,
			`echo "tokens $INFRAWEAVE_API_TOKEN $TF_HTTP_PASSWORD"`,
		},
		"before_plan": {
			`echo "::add-mask swordfish"`, `echo "fish swordfish" >&2`,
			`echo "gh""p_` + strings.Repeat("a", 36) + `"`,
		},
		"before_apply": {`printf 'no newline'`},
		"after_run":    {`echo "after $SECRET"`},
	}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	init := "infraweave: working on commit abc123, read from its repository with no credential\n" +
		"secret ***** short ab plain *****\n*****\n*****\nsplit *****\ntokens ***** *****\n"
	plan := "::add-mask *****\nfish *****\n*****\n"
	equal(t, "taken", w.w.taken, []string{
		"report INITIALIZING started",
		"log 4/0: " + init, "report INITIALIZING succeeded",
		"log 5/0: " + plan, "report PLANNING succeeded",
		"log 6/0: no newline\n", "report APPLYING succeeded",
	})
	if got, want := w.out.String(), init+plan+"no newline\nafter *****\n"; got != want {
		t.Errorf("standard output:\n got %q\nwant %q", got, want)
	}
}
