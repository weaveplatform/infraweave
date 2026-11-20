package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// progressTrace writes each completed boundary immediately: a Go test timeout
// cannot run scenario cleanup or flush Godog's final Cucumber report. Records
// deliberately exclude step text, request bodies, policy source and raw errors.
func progressTrace() (func(int) error, error) {
	if os.Getenv("INFRAWEAVE_ACCEPT_PROGRESS") != "1" {
		return func(int) error { return nil }, nil
	}
	out := os.Getenv(harness.EnvOut)
	if out == "" {
		out = "out"
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		return nil, fmt.Errorf("acceptance progress directory: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(out, "progress.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("acceptance progress file: %w", err)
	}
	recorder := newProgressRecorder(f)
	areas = append(areas, recorder.register)
	return recorder.finish, nil
}

// A failed diagnostic sink cannot turn a product step into a skipped or failed
// step. Its first error is reported after the suite has completed its cleanup.
type progressRecorder struct {
	mu      sync.Mutex
	output  io.WriteCloser
	started time.Time
	failure error
	closed  bool
}

func newProgressRecorder(output io.WriteCloser) *progressRecorder {
	return &progressRecorder{output: output, started: time.Now()}
}

func (r *progressRecorder) emit(row map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.emitHeld(row)
}

func (r *progressRecorder) emitHeld(row map[string]any) {
	if r.closed || r.failure != nil {
		return
	}
	row["at"] = time.Now().UTC()
	row["pid"] = os.Getpid()
	if err := json.NewEncoder(r.output).Encode(row); err != nil {
		r.failure = fmt.Errorf("acceptance progress record: %w", err)
	}
}

func (r *progressRecorder) finish(code int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.emitHeld(map[string]any{"event": "suite_end", "seconds": time.Since(r.started).Seconds(), "exitCode": code})
		if err := r.output.Close(); err != nil {
			r.failure = errors.Join(r.failure, fmt.Errorf("acceptance progress close: %w", err))
		}
		r.closed = true
	}
	return r.failure
}

func (r *progressRecorder) register(sc *godog.ScenarioContext, w *harness.World) {
	var name, uri, id string
	var scenarioStart, stepStart time.Time
	index := 0
	record := func(event string) map[string]any {
		return map[string]any{"event": event, "scenario": name, "uri": uri, "id": id, "step": index}
	}
	sc.Before(func(ctx context.Context, scenario *godog.Scenario) (context.Context, error) {
		name, uri, id, scenarioStart, index = scenario.Name, scenario.Uri, scenario.Id, time.Now(), 0
		r.emit(record("scenario_start"))
		return ctx, nil
	})
	sc.StepContext().Before(func(ctx context.Context, _ *godog.Step) (context.Context, error) {
		index++
		stepStart = time.Now()
		r.emit(record("step_start"))
		return ctx, nil
	})
	sc.StepContext().After(func(ctx context.Context, _ *godog.Step, _ godog.StepResultStatus, err error) (context.Context, error) {
		row := record("step_end")
		row["seconds"], row["failed"] = time.Since(stepStart).Seconds(), err != nil
		if id, state := progressRun(w.Last().Body); id != "" {
			row["run"], row["state"] = id, state
		}
		r.emit(row)
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		row := record("scenario_steps_end")
		row["seconds"], row["failed"] = time.Since(scenarioStart).Seconds(), err != nil
		r.emit(row)
		return ctx, nil
	})
}

func progressRun(body []byte) (string, string) {
	var run struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if json.Unmarshal(body, &run) != nil || !api.RunState(run.State).Valid() {
		return "", ""
	}
	id, err := kernel.ParseULID(run.ID)
	if err != nil || id.IsZero() {
		return "", ""
	}
	return id.String(), run.State
}
