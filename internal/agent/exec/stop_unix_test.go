//go:build unix

package exec_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/exec"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// stopped runs the world's run with stop as its Stop and log as its log,
// in the background, and answers Run's error once it returns.
func (w *world) stopped(reports exec.Reporter, stop <-chan os.Signal, log slog.Handler) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- exec.Run(w.t.Context(), w.h, exec.Deps{
			Reports: reports, Logs: w.w, Uploads: w.w, Log: slog.New(log),
			Out: &w.out, Workspace: w.workspace, Environ: w.environ, Stop: stop,
		})
	}()
	return done
}

// fifo makes a FIFO named name and answers its path and the FIFO, open
// for reading and writing before the run starts, so a hook's open of it
// neither waits for the test nor meets it in open(2): on darwin a reader
// whose blocking open restarts after a signal, such as the SIGCHLD of a
// step's syntax check, misses a writer that opens and closes meanwhile
// and waits for one that never comes. Linux and darwin open a FIFO for
// reading and writing at once, and a read of it waits for a line rather
// than ending when no writer is left.
func fifo(t *testing.T, name string) (string, *os.File) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	return path, f
}

// reached makes a FIFO that a hook writes a line to with `echo > "$path"`
// once it is running, and answers its path and a channel closed once a
// hook wrote it.
func reached(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	path, f := fifo(t, "reached")
	ch := make(chan struct{})
	go func() {
		defer f.Close()
		if _, err := f.Read(make([]byte, 1)); err == nil {
			close(ch)
		}
	}()
	return path, ch
}

// gate makes a FIFO a hook waits on with
// `until read line < "$path"; do :; done`, and answers its path and a
// function that lets the hook go on. The hook runs no command while it
// waits, so a stop's SIGINT reaches the shell and not a child between its
// fork and its exec, and a read a trapped SIGINT interrupts is read again.
func gate(t *testing.T) (string, func()) {
	t.Helper()
	path, f := fifo(t, "gate")
	t.Cleanup(func() { _ = f.Close() })
	return path, func() {
		t.Helper()
		if _, err := f.WriteString("\n"); err != nil {
			t.Fatal(err)
		}
	}
}

// await receives from ch, or fails the test with the run's error, which
// done answers, when the run ends first. It sets no deadline a slow host
// could pass: the test binary's timeout bounds a run that does not end.
func await(t *testing.T, what string, ch <-chan struct{}, done <-chan error) {
	t.Helper()
	select {
	case <-ch:
	case err := <-done:
		t.Fatalf("the run ended before %s: %v", what, err)
	}
}

// decisions is a slog.Handler that sends on its channel each time the
// runner logs that it took a stop or ignored one, which it does once it
// decided on it. A decision the channel has no room for is dropped, so a
// runner that decides more often than the test expects does not block.
type decisions chan struct{}

func (decisions) Enabled(context.Context, slog.Level) bool { return true }

func (d decisions) Handle(_ context.Context, r slog.Record) error {
	if r.Message == "stopping" || r.Message == "stop ignored" {
		select {
		case d <- struct{}{}:
		default:
		}
	}
	return nil
}

func (d decisions) WithAttrs([]slog.Attr) slog.Handler { return d }

func (d decisions) WithGroup(string) slog.Handler { return d }

func TestUnitExecStop(t *testing.T) {
	w := newWorld(t)
	fifo, holding := reached(t)
	// One child writes the FIFO and becomes the sleep, so the stop finds a
	// command to end: a SIGINT between two of the hook's own commands is
	// trapped by the session's shell, which goes on to the next.
	w.h.Spec.Hooks = map[string][]string{
		"before_plan": {
			hook("holding") + `; sh -c 'echo > "$0" && exec sleep 60' '` + fifo + `'`,
			hook("not reached"),
		},
		"after_run": {hook("after_run", "TF_VAR_infraweave_final_run_state")},
	}
	stop := make(chan os.Signal, 1)
	done := w.stopped(w.w, stop, slog.DiscardHandler)
	await(t, "the hook was reached", holding, done)
	stop <- os.Interrupt
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// A stop that missed the sleep lets the hook succeed after it, and
	// the phase reports no failed hook.
	events := w.events()
	if len(events) != 3 || !strings.HasPrefix(events[2],
		"PLANNING stopped 5: Stopped on request: The before_plan hook 1 of 2 failed") {
		t.Errorf("reports: %q", events)
	}
	equal(t, "trace", w.traced(), []string{
		"init -input=false -lock-timeout=5m0s", "holding", "after_run STOPPED",
	})
	if !strings.Contains(w.out.String(), "infraweave: the run stopped on request\n") {
		t.Errorf("output: %s", w.out.String())
	}
}

func TestUnitExecStopOnce(t *testing.T) {
	w := newWorld(t)
	fifo, holding := reached(t)
	held, release := gate(t)
	// The hook traps SIGINT itself and goes on, so the stop ends the phase
	// once it returns; each SIGINT it receives is a line of the trace. It
	// holds until the runner decided on both stops, so a second SIGINT
	// would reach it too.
	w.h.Spec.Hooks = map[string][]string{
		"before_plan": {
			`trap 'echo interrupted >> "$TRACE"' INT; ` + hook("holding") + `; echo > '` + fifo + `'` +
				`; until read line < '` + held + `'; do :; done; ` + hook("held"),
		},
	}
	stop := make(chan os.Signal, 2)
	decided := make(decisions, 2)
	done := w.stopped(w.w, stop, decided)
	await(t, "the hook was reached", holding, done)
	stop <- os.Interrupt
	stop <- os.Interrupt
	await(t, "the runner decided on the first stop", decided, done)
	await(t, "the runner decided on the second stop", decided, done)
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	equal(t, "reports", w.events(), []string{
		"INITIALIZING started 3", "INITIALIZING succeeded 4",
		"PLANNING stopped 5: Stopped on request.",
	})
	equal(t, "trace", w.traced(), []string{
		"init -input=false -lock-timeout=5m0s", "holding", "interrupted", "held",
	})
}

func TestUnitExecApplyIgnoresStop(t *testing.T) {
	w := newWorld(t)
	fifo, holding := reached(t)
	held, release := gate(t)
	w.w.autodeploy = true
	// The hook holds until the runner decided on the stop, which it takes
	// while applying runs; a SIGINT that reached the session would be a
	// line of the trace.
	w.h.Spec.Hooks = map[string][]string{
		"before_apply": {
			`trap 'echo interrupted >> "$TRACE"' INT; ` + hook("holding") + `; echo > '` + fifo + `'` +
				`; until read line < '` + held + `'; do :; done; ` + hook("held"),
		},
	}
	stop := make(chan os.Signal, 1)
	decided := make(decisions, 1)
	done := w.stopped(w.w, stop, decided)
	await(t, "the hook was reached", holding, done)
	stop <- os.Interrupt
	await(t, "the runner decided on the stop", decided, done)
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	equal(t, "reports", w.events(), []string{
		"INITIALIZING started 3", "INITIALIZING succeeded 4", "PLANNING succeeded 5",
		"APPLYING succeeded 6",
	})
	if tr := w.traced(); !slices.Contains(tr, "held") || slices.Contains(tr, "interrupted") ||
		!slices.Contains(tr, "apply -input=false -lock-timeout=5m0s infraweave.plan") {
		t.Errorf("trace: %q", tr)
	}
}

// stopAfter is the world's worker, which sends a stop when it takes the
// report of state's event and answers once the runner decided on it.
type stopAfter struct {
	*worker
	state   wire.WorkerState
	event   wire.RunReportEvent
	stop    chan<- os.Signal
	decided <-chan struct{}
}

func (s stopAfter) Report(ctx context.Context, token, run string, r wire.RunReport) (wire.NextStep, error) {
	if r.State == s.state && r.Event == s.event {
		s.stop <- os.Interrupt
		select {
		case <-s.decided:
		case <-ctx.Done():
			return wire.NextStep{}, fmt.Errorf("the runner did not decide on the stop: %w", ctx.Err())
		}
	}
	return s.worker.Report(ctx, token, run, r)
}

func TestUnitExecStopBetweenPhases(t *testing.T) {
	for _, tc := range []struct {
		name       string
		autodeploy bool
		want       []string
	}{
		{
			name: "planning stops before its first step",
			want: []string{
				"INITIALIZING started 3", "INITIALIZING succeeded 4",
				"PLANNING stopped 5: Stopped on request.",
			},
		},
		{
			name: "applying drops it", autodeploy: true,
			want: []string{
				"INITIALIZING started 3", "INITIALIZING succeeded 4", "PLANNING succeeded 5",
				"APPLYING succeeded 6",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			w.w.autodeploy = tc.autodeploy
			w.h.Spec.Hooks = map[string][]string{"before_plan": {hook("before_plan")}}
			stop := make(chan os.Signal, 1)
			state := wire.WorkerStateINITIALIZING
			if tc.autodeploy {
				state = wire.WorkerStatePLANNING
			}
			decided := make(decisions, 1)
			done := w.stopped(stopAfter{w.w, state, wire.Succeeded, stop, decided}, stop, decided)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			equal(t, "reports", w.events(), tc.want)
			if planned := slices.Contains(w.traced(), "before_plan"); planned != tc.autodeploy {
				t.Errorf("trace: %q", w.traced())
			}
		})
	}
}
