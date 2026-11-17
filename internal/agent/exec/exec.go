package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/handoff"
	"github.com/weaveplatform/infraweave/internal/agent/runlog"
	"github.com/weaveplatform/infraweave/internal/agent/source"
	"github.com/weaveplatform/infraweave/internal/agent/vendors"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/iac/workflow"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/mask"
)

// Reporter sends a run's reports and answers each one's next step;
// *client.Client is one.
type Reporter interface {
	Report(ctx context.Context, token, run string, r wire.RunReport) (wire.NextStep, error)
}

// Deps are what Run needs beyond the handoff: the worker API's reports,
// log uploads and artifact uploads, the runner's log, where the session's
// masked output goes, the workspace holding its checkout and mounted
// files, the environment the session starts from, and Stop, which carries each
// SIGINT the launcher sends the runner to stop the run.
type Deps struct {
	Reports   Reporter
	Logs      runlog.API
	Uploads   Uploads
	Log       *slog.Logger
	Out       io.Writer
	Workspace string
	Environ   []string
	Stop      <-chan os.Signal
}

// Report retries: a report whose answer was lost, or that the server
// could not take for now, is sent again with the same body for up to
// reportRetry, waiting from reportWait to reportWaitMax between sends.
const (
	reportRetry   = 10 * time.Minute
	reportWait    = time.Second
	reportWaitMax = 30 * time.Second
)

// ErrAction marks a next step whose action this runner does not know.
var ErrAction = errors.New("the server answered an unknown action")

// errMovedOn marks a report the server refused because the run is no
// longer in the reported state on this worker.
var errMovedOn = errors.New("the run moved on")

// transient reports whether err is a failure a later attempt may not
// meet: no answer, the server's or the store's being unavailable for now,
// or the store's refusal of a signature a new declaration replaces.
func transient(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if errors.Is(err, client.ErrSigned) {
		return true
	}
	s := client.Status(err)
	return s == 0 && !errors.Is(err, errUpload) || s == http.StatusTooManyRequests ||
		s >= http.StatusInternalServerError
}

// Run executes the run h hands over. It reports that the state the run's
// preparation leads to started, which ends the handover, and then
// follows each answer: execute runs that state's phase in the run's one
// shell session, its output in the state's log, and reports how it ended
// once the log is flushed; pause ends the runner while the run waits for
// a decision; exit runs the after_run hooks with the run's final state
// and ends it. A report refused with 409 ends the runner with no further
// report, since the run has moved on without it. A stop on d.Stop ends
// initialization or planning, as interrupt says, which the runner then
// reports stopped.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
// ref: decision 0008 § "Stop and kill"
func Run(ctx context.Context, h handoff.Handoff, d Deps) error {
	log, err := runlog.New(ctx, runlog.Deps{
		API: d.Logs, Out: d.Out, Log: d.Log, Run: h.Run, Token: h.RunToken, Masker: masker(h),
	})
	if err != nil {
		return err //nolint:wrapcheck // runlog names the step
	}
	r := &run{h: h, d: d, log: log}
	defer r.close(ctx)
	watched := make(chan struct{})
	defer close(watched)
	go r.watch(ctx, watched)
	step, err := r.report(ctx, wire.RunReport{
		StateVersion: h.StateVersion, State: h.First(), Event: wire.Started,
	})
	for err == nil {
		switch step.Action {
		case wire.Execute:
			state := wire.WorkerState(step.State)
			d.Log.InfoContext(ctx, "phase started", "run", h.Run, "state", state)
			if err := r.log.Begin(ctx, step.StateVersion); err != nil {
				d.Log.WarnContext(ctx, "log not synced", "run", h.Run, "state", state, "error", err)
			}
			rep := r.phase(ctx, state, step.StateVersion)
			if err := r.log.End(ctx); err != nil {
				d.Log.WarnContext(
					ctx,
					"log not flushed",
					"run",
					h.Run,
					"state",
					state,
					"error",
					err,
				)
			}
			chunks := r.log.Chunks()
			rep.LogChunks = &chunks
			step, err = r.report(ctx, rep)
		case wire.Pause:
			d.Log.InfoContext(ctx, "run paused", "run", h.Run, "state", step.State)
			return nil
		case wire.Exit:
			d.Log.InfoContext(ctx, "run ended", "run", h.Run, "state", step.State)
			r.afterRun(ctx, step.State)
			return nil
		default:
			return fmt.Errorf("%w: %q", ErrAction, step.Action)
		}
	}
	if errors.Is(err, errMovedOn) {
		d.Log.InfoContext(ctx, "run moved on", "run", h.Run, "error", err)
		return nil
	}
	return err
}

// run is one run's execution: its handoff, its log, its prepared
// environment and session, the plan's JSON file, and whether the session
// has initialized the workspace.
type run struct {
	h           handoff.Handoff
	d           Deps
	log         *runlog.Log
	ready       bool
	initialized bool
	failure     string
	dir         string
	env         []string
	workflow    *vendors.Workflow
	options     vendors.Options
	temp        string
	shell       *session
	profiled    []profiled
	profiles    int
	stop        stop
}

// stop is what a run's stop acts on: the phase running, "" between
// phases; whether a stop was taken; whether the run has ended, after
// which none is; and the session its SIGINT goes to.
type stop struct {
	mu    sync.Mutex
	phase wire.WorkerState
	taken bool
	ended bool
	shell *session
}

// stoppable are the phases a stop ends, those of the states it is valid
// in; any other ignores it, as PERFORMING must.
var stoppable = map[wire.WorkerState]bool{
	wire.WorkerStateINITIALIZING: true,
	wire.WorkerStatePLANNING:     true,
}

// watch takes each stop on the run's Stop until done is closed.
func (r *run) watch(ctx context.Context, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case _, ok := <-r.d.Stop:
			if !ok {
				return
			}
			r.interrupt(ctx)
		}
	}
}

// interrupt takes a stop: in a phase it ends, or between phases, it marks
// the run stopped and sends the session's process group SIGINT, once,
// since a second makes the tool exit mid-operation. A stop taken between
// phases ends the next one before its first step, when that one is
// stoppable. Any other phase, a run that has ended, and a second stop
// ignore it.
func (r *run) interrupt(ctx context.Context) {
	r.stop.mu.Lock()
	defer r.stop.mu.Unlock()
	phase := r.stop.phase
	if r.stop.taken || r.stop.ended || phase != "" && !stoppable[phase] {
		r.d.Log.InfoContext(ctx, "stop ignored", "run", r.h.Run, "state", phase)
		return
	}
	r.stop.taken = true
	r.d.Log.InfoContext(ctx, "stopping", "run", r.h.Run, "state", phase)
	if r.stop.shell == nil {
		return
	}
	if err := r.stop.shell.interrupt(); err != nil {
		r.d.Log.WarnContext(ctx, "stop not passed on", "run", r.h.Run, "error", err)
	}
}

// begin marks state's phase running; a stop taken between phases that
// state's does not end is dropped.
func (r *run) begin(state wire.WorkerState) {
	r.stop.mu.Lock()
	defer r.stop.mu.Unlock()
	r.stop.phase = state
	if !stoppable[state] {
		r.stop.taken = false
	}
}

// finish marks the phase ended.
func (r *run) finish() {
	r.stop.mu.Lock()
	defer r.stop.mu.Unlock()
	r.stop.phase = ""
}

// attach makes s the session a stop interrupts.
func (r *run) attach(s *session) {
	r.stop.mu.Lock()
	defer r.stop.mu.Unlock()
	r.stop.shell = s
}

// halted reports whether a stop ended the phase running.
func (r *run) halted() bool {
	r.stop.mu.Lock()
	defer r.stop.mu.Unlock()
	return r.stop.taken && stoppable[r.stop.phase]
}

// report sends rep, sending it again with the same body while its answer
// is lost or the server is unavailable, so the server, which answers a
// repeated report with the step it answered before, moves the run once.
func (r *run) report(ctx context.Context, rep wire.RunReport) (wire.NextStep, error) {
	deadline := time.Now().Add(reportRetry)
	wait := reportWait
	for {
		step, err := r.d.Reports.Report(ctx, r.h.RunToken, r.h.Run, rep)
		status := client.Status(err)
		switch {
		case err == nil:
			return step, nil
		case status == http.StatusConflict:
			return wire.NextStep{}, fmt.Errorf("%w: %w", errMovedOn, err)
		case status != 0 && status != http.StatusTooManyRequests &&
			status < http.StatusInternalServerError,
			time.Now().Add(wait).After(deadline):
			return wire.NextStep{}, err //nolint:wrapcheck // client names the report
		}
		r.d.Log.WarnContext(ctx, "report not taken", "run", r.h.Run, "state", rep.State,
			"event", rep.Event, "error", err, "retry", wait)
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return wire.NextStep{}, fmt.Errorf("report %s %s: %w", rep.State, rep.Event, ctx.Err())
		case <-t.C:
		}
		wait = min(wait*2, reportWaitMax)
	}
}

// phase executes state's phase in the session: the state's before-hooks,
// its workflow's commands and its after-hooks, each in a group of its own,
// stopping at the first that fails. A session that did not initialize the
// workspace, as when a run resumes in a new container to apply or replan
// its saved workspace, runs initialization's hooks and commands first.
// It answers the report of how the phase ended at version; planning's
// success carries the plan's delta, once its results are saved. The
// phase's CPU profile, when the run profiles, is uploaded before the
// report whatever the phase's end. A task that skips initialization runs
// neither its commands nor its hooks, even in a fresh session, and logs
// the skip in INITIALIZING.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
// ref: decision 0008 § "skipInitialization"
func (r *run) phase(ctx context.Context, state wire.WorkerState, version int64) wire.RunReport {
	r.begin(state)
	defer r.finish()
	defer r.saveProfile(ctx, state, version)
	rep := wire.RunReport{StateVersion: version, State: state, Event: wire.Succeeded}
	// A failed or stopped report carries no delta, even once the plan was
	// read: the worker API refuses a delta on any report but planning's
	// success.
	fail := func(detail string) wire.RunReport {
		rep.Event, rep.Details, rep.Delta = wire.Failed, &detail, nil
		return rep
	}
	// A step that fails once a stop was taken failed for it, mostly, so a
	// stopped phase reports stopped whatever its steps answered.
	end := func(detail string) wire.RunReport {
		if !r.halted() {
			return fail(detail)
		}
		_, _ = fmt.Fprintln(r.log, "infraweave: the run stopped on request")
		if detail == "" {
			detail = "Stopped on request."
		} else {
			detail = "Stopped on request: " + detail
		}
		rep.Event, rep.Details, rep.Delta = wire.Stopped, &detail, nil
		return rep
	}
	if state == wire.WorkerStateINITIALIZING {
		if notices := r.h.Spec.Notices; notices != nil {
			for _, notice := range *notices {
				_, _ = fmt.Fprintln(r.log, "infraweave: .infraweave/config.yml: "+notice)
			}
		}
		if refusal := r.h.Spec.Refusal; refusal != nil {
			_, _ = fmt.Fprintln(r.log, "infraweave: .infraweave/config.yml: "+*refusal)
			return fail(*refusal)
		}
	}
	if !r.ready {
		r.prepare(ctx)
	}
	if r.failure != "" {
		return fail(r.failure)
	}
	if state == wire.WorkerStateINITIALIZING {
		r.announce()
	}
	commands, err := r.commands(ctx, state)
	switch {
	case errors.Is(err, vendors.ErrState):
		return fail("This runner executes no " + string(state) + " phase.")
	case errors.Is(err, errNoTask):
		return fail("The run's specification names no task to perform.")
	}
	reinit := !r.initialized && state != wire.WorkerStateINITIALIZING && !r.skipsInit()
	var init []step
	if err == nil && reinit {
		init, err = r.commands(ctx, wire.WorkerStateINITIALIZING)
	}
	if err != nil {
		return fail("The workflow's commands cannot be rendered: " +
			strings.TrimSuffix(err.Error(), ".") + ".")
	}
	lists, after := r.phaseSteps(state, commands, init, reinit)
	if d := r.steps(ctx, lists...); d != "" || r.halted() {
		return end(d)
	}
	r.initialized = true
	// A Terragrunt run keeps no outputs, so its applying reads and saves
	// none.
	outputs := state == wire.WorkerStateAPPLYING && !r.terragrunt()
	var p plan.Plan
	switch {
	case state == wire.WorkerStatePLANNING && r.terragrunt():
		var failure *planFailure
		if p, failure = r.terragruntPlan(ctx); failure != nil {
			if failure.step {
				return end(failure.note)
			}
			return fail(failure.note)
		}
		delta := deltaOf(p.Delta)
		rep.Delta = &delta
	case state == wire.WorkerStatePLANNING:
		if p, err = r.plan(); err != nil {
			return fail("The plan's JSON could not be read: " + err.Error())
		}
		delta := deltaOf(p.Delta)
		rep.Delta = &delta
	case outputs:
		if err := r.outputs(); err != nil {
			return fail("The outputs' JSON could not be read: " + err.Error())
		}
	}
	if d := r.steps(ctx, after); d != "" || r.halted() {
		return end(d)
	}
	switch {
	case state == wire.WorkerStatePLANNING:
		if d := r.save(ctx, version, p); d != "" {
			return fail(d)
		}
	case outputs:
		if d := r.saveOutputs(ctx, version); d != "" {
			return fail(d)
		}
	}
	return rep
}

// phaseSteps groups the state's export, session initialization and phase
// hooks and commands, keeping the after-hooks for after outputs are read.
func (r *run) phaseSteps(state wire.WorkerState, commands, init []step, reinit bool) ([][]step, []step) {
	lists := [][]step{{{
		text: "export " + kernel.RunStateVariable + "=" + quote(string(state)),
		what: "Setting TF_VAR_infraweave_run_state",
	}}}
	if reinit {
		lists = append(lists, r.hooks("before_init"), init, r.hooks("after_init"))
	}
	if state == wire.WorkerStateINITIALIZING && r.skipsInit() {
		_, _ = fmt.Fprintln(r.log, "infraweave: initialization is skipped")
		return lists, nil
	}
	name := hookPhases[state]
	return append(lists, r.hooks("before_"+name), commands), r.hooks("after_" + name)
}

// hookPhases names each state's hooks: before_<name> and after_<name>,
// as the specification keys them.
var hookPhases = map[wire.WorkerState]string{
	wire.WorkerStateINITIALIZING: "init",
	wire.WorkerStatePLANNING:     "plan",
	wire.WorkerStateAPPLYING:     "apply",
	wire.WorkerStateDESTROYING:   "destroy",
	wire.WorkerStatePERFORMING:   "perform",
}

// errNoTask marks a PERFORMING phase whose specification names no task.
var errNoTask = errors.New("the specification names no task")

// skipsInit reports whether the task omits initialization's commands and
// hooks, including when it resumes in a fresh session.
func (r *run) skipsInit() bool {
	return r.h.Spec.Task != nil && r.h.Spec.Task.SkipInitialization
}

// commands are the steps of state's phase: for PERFORMING, the task's
// command, which sh -c runs so that neither an exit nor a cd in it ends
// or moves the session the after_perform hooks run in; none for
// INITIALIZING when the task skips initialization, whose hooks are also
// omitted; and for every other phase its workflow's commands, rendered now.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
func (r *run) commands(ctx context.Context, state wire.WorkerState) ([]step, error) {
	task := r.h.Spec.Task
	switch {
	case state == wire.WorkerStatePERFORMING && task == nil:
		return nil, errNoTask
	case state == wire.WorkerStatePERFORMING:
		return []step{{
			text: Shell + " -c " + quote(task.Command), what: "The task's command",
			command: taskCommand,
		}}, nil
	case state == wire.WorkerStateINITIALIZING && r.skipsInit():
		return nil, nil
	}
	commands, err := r.workflow.Commands(ctx, state, r.options)
	if err != nil {
		return nil, err //nolint:wrapcheck // vendors names the state or the key
	}
	return commandSteps(commands), nil
}

// step is one hook or command, how a failure names it, the tool command
// a CPU profile labels its samples with, empty for a hook, and the
// variables set for it alone, each NAME=value.
type step struct {
	text, what, command string
	env                 []string
}

// hooks are the steps of the specification's hooks of phase, in order.
func (r *run) hooks(phase string) []step {
	texts := r.h.Spec.Hooks[phase]
	out := make([]step, len(texts))
	for i, t := range texts {
		out[i] = step{text: t, what: fmt.Sprintf("The %s hook %d of %d", phase, i+1, len(texts))}
	}
	return out
}

// commandSteps are the steps of a workflow's commands. A command whose
// output goes to a file is grouped first, so a line of several commands
// writes all of their output there.
func commandSteps(commands []vendors.Command) []step {
	out := make([]step, len(commands))
	for i, c := range commands {
		text := c.Line
		if c.Stdout != "" {
			text = "{ " + text + "\n} >" + quote(c.Stdout)
		}
		out[i] = step{
			text: text, what: "The " + string(c.Key) + " command", command: string(c.Key),
			env: c.Env,
		}
	}
	return out
}

// steps runs each list's steps in order in the session, and answers the
// report's details of the first that fails; empty when all succeed, or
// when a stop ended the phase before a step.
func (r *run) steps(ctx context.Context, lists ...[]step) string {
	for _, list := range lists {
		for _, s := range list {
			if r.halted() {
				return ""
			}
			code, err := r.exec(ctx, s)
			switch {
			case errors.Is(err, ErrExited):
				return fmt.Sprintf("%s ended the run's shell with exit status %d.", s.what, code)
			case err != nil:
				return fmt.Sprintf("%s could not run: %v.", s.what, err)
			case code != 0:
				return fmt.Sprintf("%s failed with exit status %d.", s.what, code)
			}
		}
	}
	return ""
}

// plan reads the plan's JSON, which planning's commands wrote.
func (r *run) plan() (plan.Plan, error) {
	f, err := os.Open(r.options.PlanJSON)
	if err != nil {
		return plan.Plan{}, fmt.Errorf("open it: %w", err)
	}
	defer f.Close()
	return plan.Parse(f) //nolint:wrapcheck // plan names the document
}

// planFailure distinguishes a failed read or render from a command step
// that ended the phase: a step still needs the phase's stop-aware end path.
type planFailure struct {
	note string
	step bool
}

// terragruntPlan reads the selected units' JSON. Releases that write it
// only under --all still save the root unit's binary plan, so a single-unit
// run can use showPlan. A malformed written document never falls back.
func (r *run) terragruntPlan(ctx context.Context) (plan.Plan, *planFailure) {
	p, err := r.unitPlans()
	if err == nil {
		return p, nil
	}
	if !errors.Is(err, errNoUnitPlans) || r.h.Spec.Terragrunt.RunAll {
		return plan.Plan{}, &planFailure{note: "The units' plans could not be read: " + err.Error()}
	}
	command, err := r.workflow.Command(ctx, workflow.ShowPlan, r.options)
	if err != nil {
		return plan.Plan{}, &planFailure{note: "The workflow's commands cannot be rendered: " +
			strings.TrimSuffix(err.Error(), ".") + "."}
	}
	if detail := r.steps(ctx, commandSteps([]vendors.Command{command})); detail != "" || r.halted() {
		return plan.Plan{}, &planFailure{note: detail, step: true}
	}
	p, err = r.plan()
	if err != nil {
		return plan.Plan{}, &planFailure{note: "The units' plans could not be read: " + err.Error()}
	}
	return p, nil
}

// unitPlanFile is the name of each unit's plan's JSON under the
// directory a Terragrunt run's plan command names: the unit's path from
// the project root, then this name.
//
// ref: https://docs.terragrunt.com/reference/cli/commands/run/#json-out-dir
const unitPlanFile = "tfplan.json"

// unitPlans reads each unit's plan's JSON, which a Terragrunt run's plan
// command wrote under the options' UnitPlans, opened as a root, and
// answers their delta summed: each count added, and the outputs changing
// when any unit's do. A plan that does not parse is an error naming its
// unit, and planning that wrote none is an error too, since every unit
// Terragrunt planned writes one.
func (r *run) unitPlans() (plan.Plan, error) {
	root, err := os.OpenRoot(r.options.UnitPlans)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return plan.Plan{}, errNoUnitPlans
	case err != nil:
		return plan.Plan{}, fmt.Errorf("open their directory: %w", err)
	}
	defer root.Close()
	var out plan.Plan
	units := 0
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != unitPlanFile {
			return err
		}
		f, err := root.Open(filepath.FromSlash(p))
		if err != nil {
			return err //nolint:wrapcheck // names the path
		}
		defer f.Close()
		u, err := plan.Parse(f)
		if err != nil {
			return fmt.Errorf("the unit %s: %w", unitName(path.Dir(p)), err)
		}
		units++
		out.Delta = sum(out.Delta, u.Delta)
		return nil
	})
	switch {
	case err != nil:
		return plan.Plan{}, err //nolint:wrapcheck // names the unit or the path
	case units == 0:
		return plan.Plan{}, errNoUnitPlans
	}
	return out, nil
}

// errNoUnitPlans is planning that wrote no unit's plan.
var errNoUnitPlans = errors.New("no unit's plan was written")

// unitName is how a note names the unit whose path from the project root
// is rel, with / separators, "." for the project root.
func unitName(rel string) string {
	if rel == "." {
		return "at the project root"
	}
	return strconv.Quote(rel)
}

// sum is the delta of a's and b's changes together.
func sum(a, b plan.Delta) plan.Delta {
	return plan.Delta{
		Add: a.Add + b.Add, Change: a.Change + b.Change, Delete: a.Delete + b.Delete,
		Import: a.Import + b.Import, Move: a.Move + b.Move, Forget: a.Forget + b.Forget,
		Resources: a.Resources + b.Resources, Outputs: a.Outputs || b.Outputs,
	}
}

// outputs checks the outputs' JSON, which applying's commands wrote, so
// the server reads what the runner records.
func (r *run) outputs() error {
	f, err := os.Open(r.options.OutputsJSON)
	if err != nil {
		return fmt.Errorf("open it: %w", err)
	}
	defer f.Close()
	_, err = plan.ReadOutputs(f)
	return err //nolint:wrapcheck // plan names the document
}

// deltaOf is d as the worker API reports it.
func deltaOf(d plan.Delta) wire.PlanDelta {
	move, forget, resources := count(d.Move), count(d.Forget), count(d.Resources)
	return wire.PlanDelta{
		Add: count(d.Add), Change: count(d.Change), Delete: count(d.Delete),
		Import: count(d.Import), Move: &move, Forget: &forget, Resources: &resources,
		Outputs: d.Outputs,
	}
}

// count is n as the worker API counts.
func count(n int) int32 {
	return int32(min(n, math.MaxInt32)) //nolint:gosec // clamped
}

// announce writes initialization's line: the commit the run works on, and
// how its source was read.
func (r *run) announce() {
	how := "with no credential"
	switch {
	case r.h.Secrets.SourceCredential != nil && r.h.Spec.Source.Narrowed:
		how = "with a credential narrowed to the repository and the run"
	case r.h.Secrets.SourceCredential != nil:
		how = "with the integration's own credential"
	}
	_, _ = fmt.Fprintf(r.log, "infraweave: working on commit %s, read from its repository %s\n",
		r.h.Spec.Commit.Hash, how)
}

// afterRun runs the after_run hooks with the run's final state, in the
// session or, when a step ended it, a new one. Their failure changes
// nothing, since the run has ended; it is logged. Their output, read
// while no state is open, goes to standard output alone. A runner that
// executed no phase runs none.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
func (r *run) afterRun(ctx context.Context, final string) {
	r.stop.mu.Lock()
	r.stop.ended = true
	r.stop.mu.Unlock()
	if !r.ready || r.env == nil {
		return
	}
	if r.shell == nil || r.shell.cmd == nil {
		s, err := start(ctx, r.dir, r.env, r.log.File())
		if err != nil {
			r.d.Log.WarnContext(ctx, "after_run hooks not run", "run", r.h.Run, "error", err)
			return
		}
		r.shell = s
	}
	if d := r.steps(ctx, []step{{
		text: "export TF_VAR_infraweave_final_run_state=" + quote(final),
		what: "Setting TF_VAR_infraweave_final_run_state",
	}}, r.hooks("after_run")); d != "" {
		r.d.Log.WarnContext(ctx, "after_run hooks failed", "run", r.h.Run, "details", d)
	}
}

// close ends the session and then the log, once it has read the
// session's last line, removes the run's temporary files, and hands the
// workspace back to its launcher.
func (r *run) close(ctx context.Context) {
	if r.shell != nil {
		_ = r.shell.close()
	}
	if err := r.log.Close(ctx); err != nil {
		r.d.Log.WarnContext(ctx, "log not flushed", "run", r.h.Run, "error", err)
	}
	if r.temp != "" {
		_ = os.RemoveAll(r.temp)
	}
	if r.d.Workspace == "" {
		return
	}
	if err := HandBack(r.d.Workspace); err != nil {
		r.d.Log.WarnContext(ctx, "workspace not handed back", "run", r.h.Run, "error", err)
	}
}

// masker masks the run's secrets in its log: every write-only variable's
// value and file's content, the in-run API token, the run token the state
// backend's credentials carry, the source credential's token, and, when
// the stack enables them, the well-known patterns.
//
// ref: decision 0013 § "Masking"
func masker(h handoff.Handoff) *mask.Masker {
	secrets := [][]byte{[]byte(h.RunToken), []byte(h.Secrets.ApiToken)}
	for _, v := range h.Secrets.Environment {
		secrets = append(secrets, []byte(v))
	}
	for _, f := range h.Secrets.Files {
		secrets = append(secrets, f)
	}
	if c := h.Secrets.SourceCredential; c != nil {
		secrets = append(secrets, []byte(c.Token))
	}
	m := mask.New(secrets...)
	if h.Spec.Stack.EnableWellKnownSecretMasking {
		m = m.WellKnown()
	}
	return m
}

// project is the run's project root inside the workspace's checkout.
func (r *run) project() (string, error) {
	p := r.h.Spec.Stack.ProjectRoot
	checkout := filepath.Join(r.d.Workspace, source.Checkout)
	if p == nil || *p == "" || *p == "." {
		return checkout, nil
	}
	rel := filepath.FromSlash(*p)
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%w: %q", errProjectRoot, *p)
	}
	return filepath.Join(checkout, rel), nil
}
