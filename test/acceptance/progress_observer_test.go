package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

type progressTestWriter struct {
	bytes.Buffer
	failAt         int
	writeErr       error
	closeErr       error
	writes, closes int
}

func (w *progressTestWriter) Write(body []byte) (int, error) {
	w.writes++
	if w.failAt > 0 && w.writes >= w.failAt {
		return 0, w.writeErr
	}
	return w.Buffer.Write(body)
}

func (w *progressTestWriter) Close() error {
	w.closes++
	return w.closeErr
}

type progressContextKey struct{}

type progressFixtureResult struct {
	status                  int
	steps                   int
	cleanup, suiteCleanup   bool
	contextUnchanged        bool
	stepErrors              []error
	scenarioError, original error
	output                  string
}

// This miniature suite runs actual Godog hooks around successful or failing
// product steps. The observer receives the same World used by those steps.
func runProgressFixture(t *testing.T, register func(*godog.ScenarioContext, *harness.World), body []byte, failed bool) progressFixtureResult {
	t.Helper()
	result := progressFixtureResult{contextUnchanged: true, original: errors.New("product-error-sentinel")}
	w := &harness.World{Tokens: map[string]string{"person": "credential-sentinel"}}
	var output bytes.Buffer
	result.status = godog.TestSuite{
		TestSuiteInitializer: func(sc *godog.TestSuiteContext) {
			sc.AfterSuite(func() { result.suiteCleanup = true })
		},
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				return context.WithValue(ctx, progressContextKey{}, "retained"), nil
			})
			register(sc, w)
			sc.Step(`^a product step with "([^"]+)"$`, func(ctx context.Context, _ string) error {
				result.contextUnchanged = result.contextUnchanged && ctx.Value(progressContextKey{}) == "retained"
				result.steps++
				w.Sent = append(w.Sent, harness.Response{Body: body})
				return nil
			})
			sc.Step(`^the final product step$`, func(ctx context.Context) error {
				result.contextUnchanged = result.contextUnchanged && ctx.Value(progressContextKey{}) == "retained"
				result.steps++
				if failed {
					return result.original
				}
				return nil
			})
			sc.StepContext().After(func(ctx context.Context, _ *godog.Step, _ godog.StepResultStatus, err error) (context.Context, error) {
				result.stepErrors = append(result.stepErrors, err)
				return ctx, nil
			})
			sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
				result.cleanup, result.scenarioError = true, err
				return ctx, nil
			})
		},
		Options: &godog.Options{
			Format: "progress", Output: &output, Strict: true,
			FeatureContents: []godog.Feature{{Name: "observer.feature", Contents: []byte(
				"Feature: diagnostic observation\n  Scenario: original product outcome\n" +
					"    Given a product step with \"step-text-sentinel\"\n    And the final product step\n",
			)}},
		},
	}.Run()
	result.output = output.String()
	return result
}

func TestUnitProgressWriterFailurePreservesGodogOutcomes(t *testing.T) {
	for _, failed := range []bool{false, true} {
		for _, failAt := range []int{1, 4} {
			t.Run(fmt.Sprintf("productFailed=%t/write=%d", failed, failAt), func(t *testing.T) {
				cause := errors.New("trace-io-sentinel")
				writer := &progressTestWriter{failAt: failAt, writeErr: cause}
				recorder := newProgressRecorder(writer)
				result := runProgressFixture(t, recorder.register, nil, failed)
				if result.steps != 2 || !result.cleanup || !result.suiteCleanup || !result.contextUnchanged {
					t.Fatalf("observer changed execution or cleanup: %+v", result)
				}
				if (result.status != 0) != failed || len(result.stepErrors) != 2 || result.stepErrors[0] != nil {
					t.Fatalf("observer changed original outcome: %+v", result)
				}
				if errors.Is(result.stepErrors[1], result.original) != failed || errors.Is(result.scenarioError, result.original) != failed {
					t.Fatalf("observer replaced original error: %+v", result)
				}
				if errors.Is(result.scenarioError, cause) || strings.Contains(result.output, cause.Error()) || writer.closes != 0 {
					t.Fatal("trace error entered Godog or closed before cleanup", result.output)
				}
				if err := recorder.finish(result.status); !errors.Is(err, cause) || writer.closes != 1 || writer.writes != failAt {
					t.Fatal("finish did not report the latched error and close once", err, writer.closes, writer.writes)
				}
				if err := recorder.finish(result.status); !errors.Is(err, cause) || writer.closes != 1 {
					t.Fatal("repeated finish lost error or repeated close", err, writer.closes)
				}
			})
		}
	}
}

func progressRows(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	var rows []map[string]any
	for {
		var row map[string]any
		err := decoder.Decode(&row)
		if errors.Is(err, io.EOF) {
			return rows
		}
		if err != nil {
			t.Fatal("progress is not complete JSONL", err)
		}
		rows = append(rows, row)
	}
}

func TestUnitProgressRecordsBoundedFactsWithoutPrivateValues(t *testing.T) {
	id := (kernel.ULID{15: 1}).String()
	for _, check := range []struct {
		name, body string
		wantRun    bool
	}{
		{"run", `{"id":"` + id + `","state":"QUEUED","token":"credential-sentinel","plan":"policy-source-sentinel","error":"response-error-sentinel"}`, true},
		{"invalid ID", `{"id":"response-secret-sentinel","state":"QUEUED"}`, false},
		{"zero ID", `{"id":"00000000000000000000000000","state":"QUEUED"}`, false},
		{"invalid state", `{"id":"` + id + `","state":"private-state-sentinel"}`, false},
		{"missing state", `{"id":"` + id + `"}`, false},
		{"malformed response", `body-secret-sentinel`, false},
	} {
		t.Run(check.name, func(t *testing.T) {
			writer := &progressTestWriter{}
			recorder := newProgressRecorder(writer)
			result := runProgressFixture(t, recorder.register, []byte(check.body), true)
			if result.status == 0 || !result.cleanup || !result.suiteCleanup || writer.closes != 0 {
				t.Fatal("original failure/cleanup missing", result.output)
			}
			if err := recorder.finish(result.status); err != nil || writer.closes != 1 {
				t.Fatal("finish failed after product cleanup", err, writer.closes)
			}
			rows := progressRows(t, writer.Bytes())
			if len(rows) != 7 || rows[len(rows)-1]["event"] != "suite_end" || rows[len(rows)-1]["exitCode"] != float64(result.status) {
				t.Fatalf("missing completed boundaries or final outcome: %v", rows)
			}
			allowed := []string{"at", "pid", "event", "scenario", "uri", "id", "step", "seconds", "failed", "run", "state", "exitCode"}
			for _, row := range rows {
				for key := range row {
					if !slices.Contains(allowed, key) {
						t.Fatalf("unbounded field %q in trace", key)
					}
				}
				if row["event"] == "step_end" {
					_, gotRun := row["run"]
					if gotRun != check.wantRun || gotRun && (row["run"] != id || row["state"] != "QUEUED") {
						t.Fatalf("invalid run projection: %v", row)
					}
				}
			}
			for _, sentinel := range []string{"step-text-sentinel", "credential-sentinel", "policy-source-sentinel", "response-error-sentinel", "response-secret-sentinel", "private-state-sentinel", "body-secret-sentinel", "product-error-sentinel"} {
				if strings.Contains(writer.String(), sentinel) {
					t.Fatalf("private value %q entered trace", sentinel)
				}
			}
		})
	}
}

func TestUnitProgressFinishRetainsWriteAndCloseErrors(t *testing.T) {
	writeErr, closeErr := errors.New("trace write"), errors.New("trace close")
	for _, failAt := range []int{0, 1} {
		t.Run(fmt.Sprintf("write=%d", failAt), func(t *testing.T) {
			writer := &progressTestWriter{failAt: failAt, writeErr: writeErr, closeErr: closeErr}
			recorder := newProgressRecorder(writer)
			err := recorder.finish(0)
			if !errors.Is(err, closeErr) || errors.Is(err, writeErr) != (failAt != 0) || writer.closes != 1 {
				t.Fatal("finish lost an I/O cause", err, writer.closes)
			}
			if again := recorder.finish(0); !errors.Is(again, err) || writer.closes != 1 || writer.writes != 1 {
				t.Fatal("finish is not stable", again, writer.closes, writer.writes)
			}
		})
	}
}

func TestUnitProgressTraceDisabled(t *testing.T) {
	t.Setenv("INFRAWEAVE_ACCEPT_PROGRESS", "0")
	out := filepath.Join(t.TempDir(), "untouched")
	t.Setenv(harness.EnvOut, out)
	before := len(areas)
	finish, err := progressTrace()
	if err != nil || finish == nil {
		t.Fatal(err)
	}
	if err := finish(7); err != nil || len(areas) != before {
		t.Fatal("disabled tracing changed registration or result", err)
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("disabled tracing created output", err)
	}
}

func TestUnitProgressTraceFileLifecycle(t *testing.T) {
	t.Setenv("INFRAWEAVE_ACCEPT_PROGRESS", "1")
	out := t.TempDir()
	t.Setenv(harness.EnvOut, out)
	saved := slices.Clone(areas)
	t.Cleanup(func() { areas = saved })
	finish, err := progressTrace()
	if err != nil || len(areas) != len(saved)+1 {
		t.Fatal("enabled tracing did not register", err)
	}
	result := runProgressFixture(t, areas[len(saved)], nil, false)
	if result.status != 0 || !result.cleanup || !result.suiteCleanup {
		t.Fatal(result.output)
	}
	if err := finish(result.status); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(out, "progress.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	rows := progressRows(t, body)
	if len(rows) != 7 || rows[6]["event"] != "suite_end" {
		t.Fatal("trace did not close after the complete suite", rows)
	}
}

func TestUnitProgressTraceSetupFailure(t *testing.T) {
	t.Setenv("INFRAWEAVE_ACCEPT_PROGRESS", "1")
	for _, failure := range []string{"directory", "file"} {
		t.Run(failure, func(t *testing.T) {
			out := t.TempDir()
			if failure == "directory" {
				path := filepath.Join(out, "occupied")
				if err := os.WriteFile(path, []byte("file"), 0o600); err != nil {
					t.Fatal(err)
				}
				out = filepath.Join(path, "child")
			} else if err := os.Mkdir(filepath.Join(out, "progress.jsonl"), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv(harness.EnvOut, out)
			before := len(areas)
			finish, err := progressTrace()
			if err == nil || finish != nil || len(areas) != before {
				t.Fatal("trace setup failure registered hooks or lost error", err)
			}
		})
	}
}
