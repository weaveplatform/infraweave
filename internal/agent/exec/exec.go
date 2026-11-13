package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/handoff"
	"github.com/weaveplatform/infraweave/internal/agent/runlog"
	"github.com/weaveplatform/infraweave/internal/agent/vendors"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/platform/mask"
)

// Reporter sends a run's reports and answers each one's next step;
// *client.Client is one.
type Reporter interface {
	Report(ctx context.Context, token, run string, r wire.RunReport) (wire.NextStep, error)
}

// Deps are what Run needs beyond the handoff: the worker API's reports and
// log uploads, the runner's log, where the session's masked output goes,
// the workspace the source is checked out in, and the environment the
// session starts from.
type Deps struct {
	Reports   Reporter
	Logs      runlog.API
	Log       *slog.Logger
	Out       io.Writer
	Workspace string
	Environ   []string
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

// Run executes the run h hands over. It reports that the state the run's
// preparation leads to started, which ends the handover, and then
// follows each answer: execute runs that state's phase in the run's one
// shell session, its output in the state's log, and reports how it ended
// once the log is flushed; pause ends the runner while the run waits for
// a decision; exit runs the after_run hooks with the run's final state
// and ends it. A report refused with 409 ends the runner with no further
// report, since the run has moved on without it.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
func Run(ctx context.Context, h handoff.Handoff, d Deps) error {
	log, err := runlog.New(ctx, runlog.Deps{
		API: d.Logs, Out: d.Out, Log: d.Log, Run: h.Run, Token: h.RunToken, Masker: masker(h),
	})
	if err != nil {
		return err //nolint:wrapcheck // runlog names the step
	}
	r := &run{h: h, d: d, log: log}
	defer r.close(ctx)
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
// environment and session, and the plan's JSON file.
type run struct {
	h       handoff.Handoff
	d       Deps
	log     *runlog.Log
	ready   bool
	failure string
	dir     string
	env     []string
	vendor  vendors.Vendor
	options vendors.Options
	temp    string
	shell   *session
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
// its vendor's commands and its after-hooks, each in a group of its own,
// stopping at the first that fails. It answers the report of how the
// phase ended at version; planning's success carries the plan's delta.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
func (r *run) phase(ctx context.Context, state wire.WorkerState, version int64) wire.RunReport {
	rep := wire.RunReport{StateVersion: version, State: state, Event: wire.Succeeded}
	fail := func(detail string) wire.RunReport {
		rep.Event, rep.Details = wire.Failed, &detail
		return rep
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
	commands, err := r.vendor.Commands(state, r.options)
	if err != nil {
		return fail("This runner executes no " + string(state) + " phase.")
	}
	name := hookPhases[state]
	if d := r.steps(ctx, []step{
		{text: "export TF_VAR_infraweave_run_state=" + quote(string(state)), what: "Setting " +
			"TF_VAR_infraweave_run_state"},
	}, r.hooks("before_"+name), commandSteps(commands)); d != "" {
		return fail(d)
	}
	if state == wire.WorkerStatePLANNING {
		delta, err := r.delta()
		if err != nil {
			return fail("The plan's JSON could not be read: " + err.Error())
		}
		rep.Delta = &delta
	}
	if d := r.steps(ctx, r.hooks("after_"+name)); d != "" {
		return fail(d)
	}
	return rep
}

// hookPhases names each state's hooks: before_<name> and after_<name>,
// as the specification keys them.
var hookPhases = map[wire.WorkerState]string{
	wire.WorkerStateINITIALIZING: "init",
	wire.WorkerStatePLANNING:     "plan",
	wire.WorkerStateAPPLYING:     "apply",
}

// step is one hook or command and how a failure names it.
type step struct {
	text, what string
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

// commandSteps are the steps of a vendor's commands.
func commandSteps(commands []vendors.Command) []step {
	out := make([]step, len(commands))
	for i, c := range commands {
		what := "The command " + c.Args[0]
		if len(c.Args) > 1 {
			what += " " + c.Args[1]
		}
		out[i] = step{text: line(c.Args, c.Stdout), what: what}
	}
	return out
}

// steps runs each list's steps in order in the session, and answers the
// report's details of the first that fails; empty when all succeed.
func (r *run) steps(ctx context.Context, lists ...[]step) string {
	for _, list := range lists {
		for _, s := range list {
			code, err := r.shell.run(ctx, s.text)
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

// delta reads the plan's JSON, which planning's commands wrote.
func (r *run) delta() (wire.PlanDelta, error) {
	f, err := os.Open(r.options.PlanJSON)
	if err != nil {
		return wire.PlanDelta{}, fmt.Errorf("open it: %w", err)
	}
	defer f.Close()
	p, err := plan.Parse(f)
	if err != nil {
		return wire.PlanDelta{}, err //nolint:wrapcheck // plan names the document
	}
	d := p.Delta
	move, forget, resources := count(d.Move), count(d.Forget), count(d.Resources)
	return wire.PlanDelta{
		Add: count(d.Add), Change: count(d.Change), Delete: count(d.Delete),
		Import: count(d.Import), Move: &move, Forget: &forget, Resources: &resources,
		Outputs: d.Outputs,
	}, nil
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
// session's last line, and removes the run's temporary files.
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

// project is the run's project root under the workspace.
func (r *run) project() (string, error) {
	p := r.h.Spec.Stack.ProjectRoot
	if p == nil || *p == "" || *p == "." {
		return r.d.Workspace, nil
	}
	rel := filepath.FromSlash(*p)
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%w: %q", errProjectRoot, *p)
	}
	return filepath.Join(r.d.Workspace, rel), nil
}
