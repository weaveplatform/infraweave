//go:build unix

package exec_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

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

// reached makes a FIFO that a hook writes with `: > "$path"` once it is
// running, and answers its path and a channel closed once a hook wrote
// it. Opening a FIFO blocks until both ends are open, so the hook goes on
// only once the test knows it is running.
func reached(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "reached")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	ch := make(chan struct{})
	go func() {
		_, _ = os.ReadFile(path)
		close(ch)
	}()
	return path, ch
}

// await waits until ch is closed, or fails the test.
func await(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(30 * time.Second):
		t.Fatal("the hook was not reached")
	}
}

// decided is a slog.Handler whose channel is closed once the runner logs
// that it took a stop or ignored one, which it does once it decided.
type decided struct {
	once *sync.Once
	ch   chan struct{}
}

func newDecided() decided {
	return decided{once: &sync.Once{}, ch: make(chan struct{})}
}

func (decided) Enabled(context.Context, slog.Level) bool { return true }

func (d decided) Handle(_ context.Context, r slog.Record) error {
	if r.Message == "stopping" || r.Message == "stop ignored" {
		d.once.Do(func() { close(d.ch) })
	}
	return nil
}

func (d decided) WithAttrs([]slog.Attr) slog.Handler { return d }

func (d decided) WithGroup(string) slog.Handler { return d }

// ended waits for done's error, or fails the test.
func ended(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(30 * time.Second):
		t.Fatal("the run did not end")
		return nil
	}
}

func TestUnitExecStop(t *testing.T) {
	w := newWorld(t)
	fifo, holding := reached(t)
	// One child writes the FIFO and becomes the sleep, so the stop finds a
	// command to end: a SIGINT between two of the hook's own commands is
	// trapped by the session's shell, which goes on to the next.
	w.h.Spec.Hooks = map[string][]string{
		"before_plan": {
			hook("holding") + `; sh -c ': > "$0" && exec sleep 60' '` + fifo + `'`,
			hook("not reached"),
		},
		"after_run": {hook("after_run", "TF_VAR_infraweave_final_run_state")},
	}
	stop := make(chan os.Signal, 1)
	done := w.stopped(w.w, stop, slog.DiscardHandler)
	await(t, holding)
	start := time.Now()
	stop <- os.Interrupt
	if err := ended(t, done); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 20*time.Second {
		t.Errorf("the stop took %v; the hook's sleep ran on", took)
	}
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
	// The hook traps SIGINT itself and goes on, so the stop ends the phase
	// once it returns; each SIGINT it receives is a line of the trace.
	w.h.Spec.Hooks = map[string][]string{
		"before_plan": {
			`trap 'echo interrupted >> "$TRACE"' INT; ` + hook("holding") + `; : > '` + fifo + `'` +
				`; i=0; while [ $i -lt 20 ]; do sleep 0.1; i=$((i+1)); done; ` + hook("held"),
		},
	}
	stop := make(chan os.Signal, 2)
	done := w.stopped(w.w, stop, slog.DiscardHandler)
	await(t, holding)
	stop <- os.Interrupt
	stop <- os.Interrupt
	if err := ended(t, done); err != nil {
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
	w.w.autodeploy = true
	w.h.Spec.Hooks = map[string][]string{
		"before_apply": {hook("holding") + `; : > '` + fifo + `'; sleep 1; ` + hook("held")},
	}
	stop := make(chan os.Signal, 1)
	done := w.stopped(w.w, stop, slog.DiscardHandler)
	await(t, holding)
	stop <- os.Interrupt
	if err := ended(t, done); err != nil {
		t.Fatal(err)
	}
	equal(t, "reports", w.events(), []string{
		"INITIALIZING started 3", "INITIALIZING succeeded 4", "PLANNING succeeded 5",
		"APPLYING succeeded 6",
	})
	if tr := w.traced(); !slices.Contains(tr, "held") ||
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
		case <-time.After(30 * time.Second):
			return wire.NextStep{}, errors.New("the runner did not decide on the stop")
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
			log := newDecided()
			done := w.stopped(stopAfter{w.w, state, wire.Succeeded, stop, log.ch}, stop, log)
			if err := ended(t, done); err != nil {
				t.Fatal(err)
			}
			equal(t, "reports", w.events(), tc.want)
			if planned := slices.Contains(w.traced(), "before_plan"); planned != tc.autodeploy {
				t.Errorf("trace: %q", w.traced())
			}
		})
	}
}
