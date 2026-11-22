package pg_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
	"github.com/weaveplatform/infraweave/internal/policies/internal/pg"
)

func TestAccCaptureMinuteGateIsAtomicAndIndependentOfSampleQuota(t *testing.T) {
	f := newFixture(t)
	other, err := pgxpool.NewWithConfig(t.Context(), f.db.Config())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(other.Close)
	stores := []*pg.Store{f.store, pg.New(other)}
	pools := []*pgxpool.Pool{f.db, other}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	failures := make(chan error, 32)
	for i := range 32 {
		wg.Go(func() {
			err := tx.Do(t.Context(), pools[i%2], tx.Options{}, func(ctx context.Context) error {
				_, ok, err := stores[i%2].CaptureSlot(ctx, f.principal.AccountID, f.principal.AccountID.ULID, "PLAN")
				if err == nil && ok {
					tx.AfterCommit(ctx, func(context.Context) { accepted.Add(1) })
				}
				return err
			})
			if err != nil {
				failures <- err
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if accepted.Load() != 1 {
		t.Fatalf("concurrent minute slots: %d", accepted.Load())
	}
	var budgets int
	if err := f.db.QueryRow(t.Context(), `SELECT count(*) FROM policy_sample_budgets WHERE account_id=$1`, f.principal.AccountID).Scan(&budgets); err != nil || budgets != 0 {
		t.Fatalf("capture consumed a sample budget: %d %v", budgets, err)
	}
	if _, err := f.db.Exec(
		t.Context(),
		`UPDATE policy_input_capture_heads SET reserved_at=statement_timestamp()-interval '2 minutes' WHERE account_id=$1`,
		f.principal.AccountID,
	); err != nil {
		t.Fatal(err)
	}
	if err := tx.Do(t.Context(), f.db, tx.Options{}, func(ctx context.Context) error {
		for _, who := range []kernel.AccountID{f.principal.AccountID, kernel.NewAccountID()} {
			for _, kind := range []string{"PLAN", "APPROVAL"} {
				if _, ok, err := f.store.CaptureSlot(ctx, who, who.ULID, kind); err != nil || !ok {
					t.Fatalf("account/kind/minute slot refused: %s/%s %v", who, kind, err)
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func (w *sampleWorld) capturePolicy(t *testing.T, engine rego.EngineType) {
	t.Helper()
	body := "package infraweave\nsample := false\ndeny contains \"captured denial\" if { input.infraweave.run.id != \"\" }\n"
	if engine == rego.V0 {
		body = "package infraweave\nsample := false\ndeny[\"captured denial\"] { input.infraweave.run.id != \"\" }\n"
	}
	if _, err := w.f.svc.Create(
		t.Context(),
		w.f.principal,
		core.Input{Name: "Capture without sample", Type: rego.Plan, Engine: engine, Body: body, Labels: []string{"autoattach:*"}},
	); err != nil {
		t.Fatal(err)
	}
}

func (w *sampleWorld) captureEvaluate(t *testing.T, id string) {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(evaluationInput(t, "plan-tracked-warn.json"), &document); err != nil {
		t.Fatal(err)
	}
	run := document["infraweave"].(map[string]any)["run"].(map[string]any)
	run["id"] = id
	run["command"] = "capture-private-command"
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	out, err := w.f.svc.Evaluate(t.Context(), fixtureEvaluationRequest(w.f, policyapi.Plan, raw))
	if err != nil || out.Outcome != policyapi.Deny || len(out.Samples) != 0 {
		t.Fatalf("real OPA false-sample capture decision: %+v %v", out, err)
	}
}

func (w *sampleWorld) captureHead(t *testing.T) (*kernel.ULID, *kernel.ULID) {
	t.Helper()
	var current, pending *kernel.ULID
	if err := w.f.db.QueryRow(t.Context(), `SELECT current_input_id,pending_input_id FROM policy_input_capture_heads WHERE account_id=$1 AND space_snapshot_id=$2 AND capture_kind='PLAN'`, w.f.principal.AccountID, w.f.principal.AccountID.ULID).
		Scan(&current, &pending); err != nil {
		t.Fatal(err)
	}
	return current, pending
}

func TestAccCaptureKeepsCurrentOnUploadFailureAndPublishesReplacementAtomically(t *testing.T) {
	for _, engine := range []rego.EngineType{rego.V0, rego.V1} {
		t.Run(string(engine), func(t *testing.T) {
			w := newSampleWorld(t)
			w.capturePolicy(t, engine)
			// An exhausted OTHER bucket cannot stop independent capture retention.
			if _, err := w.f.db.Exec(
				t.Context(),
				`INSERT INTO policy_sample_budgets(account_id,hour,bucket,used) VALUES($1,date_trunc('hour',statement_timestamp(),'UTC'),'OTHER',500)`,
				w.f.principal.AccountID,
			); err != nil {
				t.Fatal(err)
			}
			w.captureEvaluate(t, "first")
			current, pending := w.captureHead(t)
			if current != nil || pending == nil {
				t.Fatal("pending capture was advertised as published")
			}
			first := *pending
			if err := w.samples.Upload(t.Context(), w.claim(t)); err != nil {
				t.Fatal(err)
			}
			current, pending = w.captureHead(t)
			if current == nil || *current != first || pending != nil {
				t.Fatal("capture publication did not settle exact pending input")
			}
			firstInput, err := w.f.store.Input(t.Context(), w.f.principal.AccountID, first, false)
			if err != nil {
				t.Fatal(err)
			}
			readCurrent := func() {
				t.Helper()
				reader, err := w.objects.Open(t.Context(), objectstore.PolicyInputs, firstInput.Key)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := io.ReadAll(reader)
				_ = reader.Close()
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(raw, []byte("capture-private-command")) || !bytes.Contains(raw, []byte(`"id":"first"`)) {
					t.Fatal("capture lost structural identity or retained a sensitive command")
				}
			}
			readCurrent()
			w.captureEvaluate(t, "same-minute")
			current, pending = w.captureHead(t)
			if current == nil || *current != first || pending != nil {
				t.Fatal("same-minute evaluation replaced capture")
			}
			age := func() {
				t.Helper()
				if _, err := w.f.db.Exec(
					t.Context(),
					`UPDATE policy_input_capture_heads SET reserved_at=statement_timestamp()-interval '2 minutes' WHERE account_id=$1`,
					w.f.principal.AccountID,
				); err != nil {
					t.Fatal(err)
				}
			}
			age()
			w.captureEvaluate(t, "failed-replacement")
			current, pending = w.captureHead(t)
			if current == nil || *current != first || pending == nil {
				t.Fatal("staging replacement discarded current")
			}
			failed := *pending
			readCurrent()
			// A real filesystem permission failure blocks only the replacement;
			// reading the previous immutable object remains possible.
			directory := filepath.Dir(filepath.Join(w.root, string(objectstore.PolicyInputs), firstInput.Key))
			if err := os.Chmod(directory, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(directory, 0o700) })
			if err := w.samples.Upload(t.Context(), w.claim(t)); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			current, pending = w.captureHead(t)
			if current == nil || *current != first || pending != nil {
				t.Fatal("failed replacement erased current or kept pending")
			}
			failedInput, err := w.f.store.Input(t.Context(), w.f.principal.AccountID, failed, false)
			if err != nil || failedInput.State != "failed" || len(failedInput.Staging.Reveal()) != 0 {
				t.Fatalf("failed replacement remains readable/staged: %s %v", failedInput.State, err)
			}
			readCurrent()
			age()
			w.captureEvaluate(t, "published-replacement")
			_, pending = w.captureHead(t)
			if pending == nil {
				t.Fatal("replacement not staged")
			}
			replacement := *pending
			if err := w.samples.Upload(t.Context(), w.claim(t)); err != nil {
				t.Fatal(err)
			}
			current, pending = w.captureHead(t)
			if current == nil || *current != replacement || pending != nil {
				t.Fatal("replacement not atomically published")
			}
			old, err := w.f.store.Input(t.Context(), w.f.principal.AccountID, first, false)
			if err != nil || old.State != "superseded" {
				t.Fatalf("old capture not retired: %s %v", old.State, err)
			}
			if _, err := w.objects.Open(t.Context(), objectstore.PolicyInputs, firstInput.Key); err == nil {
				t.Fatal("superseded input stayed readable")
			}
			var used, requested int
			if err := w.f.db.QueryRow(t.Context(), `SELECT (SELECT used FROM policy_sample_budgets WHERE account_id=$1 AND bucket='OTHER'),(SELECT count(*) FROM policy_evaluations WHERE account_id=$1 AND sample_input_id IS NOT NULL)`, w.f.principal.AccountID).
				Scan(&used, &requested); err != nil ||
				used != 500 ||
				requested != 0 {
				t.Fatalf("capture spent sample quota or created a false sample: %d/%d %v", used, requested, err)
			}
		})
	}
}
