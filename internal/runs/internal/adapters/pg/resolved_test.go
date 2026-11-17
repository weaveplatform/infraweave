package pg_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runhttp "github.com/weaveplatform/infraweave/internal/runs/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/runtimeconfig"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

func resolvedRecord(t *testing.T) *runtimeconfig.Record {
	t.Helper()
	root, image := "live", "runner:fixed"
	r, err := runtimeconfig.New(workersapi.Spec{
		Stack:       workersapi.SpecStack{Vendor: "terraform", VendorConfig: json.RawMessage(`{"workspace":"production"}`), ProjectRoot: &root},
		RunnerImage: &image, Tool: &workersapi.SpecTool{Name: "terraform", Version: "1.17.2", Download: true},
		Hooks: map[string][]string{"before_apply": {"echo original"}},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAccResolvedConfiguration(t *testing.T) {
	db := pool(t)
	s := pg.New(db, nil)
	ctx := t.Context()
	acct := kernel.NewAccountID()
	id := insertRun(t, db, acct, kernel.NewULID(), false)
	run, err := s.Handed(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	record := resolvedRecord(t)
	if err := s.RecordResolved(ctx, run, record); !errors.Is(err, lifecycle.ErrNoTransaction) {
		t.Fatalf("a record outside a transaction = %v", err)
	}
	abort := errors.New("outer specification failed")
	err = tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		if err := s.RecordResolved(ctx, run, record); err != nil {
			return err
		}
		inside, err := s.Handed(ctx, id)
		if err != nil || inside.ResolvedConfig == nil {
			t.Errorf("record not written inside the transaction: %+v, %v", inside, err)
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	if got, err := s.Handed(ctx, id); err != nil || got.ResolvedConfig != nil {
		t.Fatalf("a failed outer transaction left a record: %+v, %v", got.ResolvedConfig, err)
	}
	if err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		locked, err := s.Handed(ctx, id)
		if err != nil {
			return err
		}
		return s.RecordResolved(ctx, locked, record)
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := s.Handed(ctx, id)
	if err != nil || stored.ResolvedConfig == nil ||
		!reflect.DeepEqual(stored.ResolvedConfig.Configuration, record.Configuration) ||
		stored.ResolvedConfig.Execution.Tool == nil || *stored.ResolvedConfig.Execution.Tool != *record.Execution.Tool {
		t.Fatalf("record round trip: %+v, %v", stored.ResolvedConfig, err)
	}
	changed := *record
	changed.Configuration.ProjectRoot = "changed"
	if err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		return s.RecordResolved(ctx, stored, &changed)
	}); !errors.Is(err, lifecycle.ErrConflict) {
		t.Fatalf("a second record replaced the first: %v", err)
	}
	if got, err := s.Handed(ctx, id); err != nil || got.ResolvedConfig.Configuration.ProjectRoot != "live" {
		t.Fatalf("the record changed: %+v, %v", got.ResolvedConfig, err)
	}
	for name, edit := range map[string]func(*lifecycle.Run){
		"another account": func(r *lifecycle.Run) { r.Account = kernel.NewAccountID() },
		"another version": func(r *lifecycle.Run) { r.Version++ },
	} {
		freshID := insertRun(t, db, acct, kernel.NewULID(), false)
		t.Run(name, func(t *testing.T) {
			fresh, err := s.Handed(ctx, freshID)
			if err != nil {
				t.Fatal(err)
			}
			edit(&fresh)
			if err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
				return s.RecordResolved(ctx, fresh, record)
			}); !errors.Is(err, lifecycle.ErrConflict) {
				t.Errorf("refused record = %v", err)
			}
		})
	}
	if err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		return s.RecordResolved(cancelled, run, record)
	}); err == nil {
		t.Error("a cancelled record write succeeded")
	}
}

func TestAccResolvedConfigurationConcurrent(t *testing.T) {
	db := pool(t)
	s := pg.New(db, nil)
	id := insertRun(t, db, kernel.NewAccountID(), kernel.NewULID(), false)
	first, second := resolvedRecord(t), resolvedRecord(t)
	second.Configuration.ProjectRoot = "second"
	written, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	one, two := make(chan error, 1), make(chan error, 1)
	go func() {
		one <- tx.Do(t.Context(), db, tx.Options{}, func(ctx context.Context) error {
			run, err := s.Handed(ctx, id)
			if err != nil {
				return err
			}
			if err := s.RecordResolved(ctx, run, first); err != nil {
				return err
			}
			close(written)
			<-release
			return nil
		})
	}()
	select {
	case <-written:
	case err := <-one:
		t.Fatalf("first writer failed: %v", err)
	}
	go func() {
		two <- tx.Do(t.Context(), db, tx.Options{}, func(ctx context.Context) error {
			run, err := s.Handed(ctx, id)
			if err != nil {
				return err
			}
			return s.RecordResolved(ctx, run, second)
		})
	}()
	waitForLock(t, db)
	close(release)
	if err := <-one; err != nil {
		t.Fatal(err)
	}
	if err := <-two; !errors.Is(err, lifecycle.ErrConflict) {
		t.Fatalf("second writer = %v", err)
	}
	if got, err := s.Handed(t.Context(), id); err != nil || got.ResolvedConfig.Configuration.ProjectRoot != "live" {
		t.Fatalf("concurrent handovers replaced the record: %+v, %v", got.ResolvedConfig, err)
	}
}

func TestAccResolvedConfigurationReadPaths(t *testing.T) {
	w := newWorld(t)
	s := runhttp.New(nil, w.reader(w.access()), nil, nil, nil)
	ctx := kernel.WithPrincipal(t.Context(), w.alice())
	tracked := mustCreate(t, w, kernel.RunTypeTracked, "recorded-tracked")
	proposed := mustCreate(t, w, kernel.RunTypeProposed, "recorded-proposed")
	task := mustCreate(t, w, kernel.RunTypeTask, "recorded-task")
	record := resolvedRecord(t)
	runtime, err := runtimeconfig.Parse(json.RawMessage(`{"beforePlan":["echo custom"],
		"environment":[{"key":"TOKEN","value":"secret-never-in-run-read"}],"gitSparseCheckoutPaths":["infra"]}`), "terraform")
	if err != nil {
		t.Fatal(err)
	}
	record.Configuration.Environment = runtime.Keys()
	record.Configuration.GitSparseCheckoutPaths = []string{"infra"}
	record.Configuration.YAML = record.Configuration.RuntimeYAML()
	if r, err := s.Run(ctx, api.RunRequestObject{RunId: tracked.ID.String()}); err != nil ||
		r.(api.Run200JSONResponse).Body.RuntimeConfig != nil {
		t.Fatalf("an unrecorded run answers configuration: %v", err)
	}
	for _, run := range []lifecycle.Run{tracked, proposed, task} {
		if err := tx.Do(ctx, w.db, tx.Options{}, func(ctx context.Context) error {
			if _, err := w.db.Exec(ctx, `UPDATE runs SET runtime_config = $3
				WHERE account_id = $1 AND id = $2`, w.acct, run.ID, runtime.Stored()); err != nil {
				return err
			}
			locked, err := w.store.Handed(ctx, run.ID)
			if err != nil {
				return err
			}
			return w.store.RecordResolved(ctx, locked, record)
		}); err != nil {
			t.Fatal(err)
		}
	}
	ids := map[string]bool{tracked.ID.String(): true, proposed.ID.String(): true, task.ID.String(): true}
	for _, tc := range []struct {
		name  string
		count int
		refs  bool
		visit func(http.ResponseWriter) error
	}{
		{"run", 1, false, func(out http.ResponseWriter) error {
			r, err := s.Run(ctx, api.RunRequestObject{RunId: tracked.ID.String()})
			if err != nil {
				return err
			}
			return r.VisitRunResponse(out)
		}},
		{"stack run", 1, false, func(out http.ResponseWriter) error {
			r, err := s.StackRun(ctx, api.StackRunRequestObject{StackId: "network", Params: api.StackRunParams{Id: proposed.ID.String()}})
			if err != nil {
				return err
			}
			return r.VisitStackRunResponse(out)
		}},
		{"stack runs", 1, false, func(out http.ResponseWriter) error {
			r, err := s.StackRuns(ctx, api.StackRunsRequestObject{StackId: "network"})
			if err != nil {
				return err
			}
			return r.VisitStackRunsResponse(out)
		}},
		{"proposals", 1, false, func(out http.ResponseWriter) error {
			r, err := s.StackProposedRuns(ctx, api.StackProposedRunsRequestObject{StackId: "network"})
			if err != nil {
				return err
			}
			return r.VisitStackProposedRunsResponse(out)
		}},
		{"tasks", 1, false, func(out http.ResponseWriter) error {
			r, err := s.StackTasks(ctx, api.StackTasksRequestObject{StackId: "network"})
			if err != nil {
				return err
			}
			return r.VisitStackTasksResponse(out)
		}},
		{"range", 3, false, func(out http.ResponseWriter) error {
			r, err := s.StackRunsInRange(ctx, api.StackRunsInRangeRequestObject{StackId: "network", Params: api.StackRunsInRangeParams{Start: strconv.FormatInt(w.now.Unix()-60, 10), End: strconv.FormatInt(w.now.Unix()+60, 10)}})
			if err != nil {
				return err
			}
			return r.VisitStackRunsInRangeResponse(out)
		}},
		{"search references", 3, true, func(out http.ResponseWriter) error {
			r, err := s.SearchRuns(ctx, api.SearchRunsRequestObject{Body: &api.SearchRunsRequest{}})
			if err != nil {
				return err
			}
			return r.VisitSearchRunsResponse(out)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := httptest.NewRecorder()
			if err := tc.visit(out); err != nil || out.Code != http.StatusOK {
				t.Fatalf("read = %d %s, %v", out.Code, out.Body.String(), err)
			}
			var body any
			if bytes.Contains(out.Body.Bytes(), []byte("secret-never-in-run-read")) {
				t.Fatal("run read exposed a runtime variable value")
			}
			if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			found := 0
			var visit func(any)
			visit = func(value any) {
				switch node := value.(type) {
				case map[string]any:
					if id, ok := node["id"].(string); ok && ids[id] {
						found++
						cfg, exists := node["runtimeConfig"]
						if tc.refs {
							if exists || node["customRuntimeConfig"] != nil {
								t.Error("search expanded a RunRef")
							}
						} else if c, ok := cfg.(map[string]any); !ok || c["projectRoot"] != "live" ||
							c["terraformVersion"] != "1.17.2" || c["execution"] != nil || c["environment"] == nil {
							t.Errorf("run record = %#v", cfg)
						}
						if !tc.refs {
							custom, ok := node["customRuntimeConfig"].(map[string]any)
							if !ok || custom["environment"] == nil || custom["yaml"] == nil ||
								!reflect.DeepEqual(custom["beforePlan"], []any{"echo custom"}) {
								t.Errorf("custom configuration missing from full run: %#v", node["customRuntimeConfig"])
							}
						}
					}
					for _, child := range node {
						visit(child)
					}
				case []any:
					for _, child := range node {
						visit(child)
					}
				}
			}
			visit(body)
			if found != tc.count {
				t.Errorf("found %d runs, want %d", found, tc.count)
			}
		})
	}
}
