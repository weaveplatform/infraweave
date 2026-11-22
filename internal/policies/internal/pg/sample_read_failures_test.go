package pg_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/policies/internal/sampling"
)

func TestAccSampleReadBoundsAndAuthenticatesActualStoredBytes(t *testing.T) {
	w := newSampleWorld(t)
	id := w.evaluate(t).Receipts[0].ID
	w.uploadAll(t)
	input, err := w.f.store.Input(t.Context(), w.f.principal.AccountID, id, false)
	if err != nil {
		t.Fatal(err)
	}
	var size int64
	if err := w.f.db.QueryRow(t.Context(), `SELECT size FROM blob_objects WHERE store='policy-inputs' AND key=$1`, input.Key).Scan(&size); err != nil {
		t.Fatal(err)
	}
	if _, err := w.f.db.Exec(t.Context(), `UPDATE blob_objects SET size=$2 WHERE store='policy-inputs' AND key=$1`, input.Key, sampling.MaxReadBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := w.samples.Read(t.Context(), input.Account, id); !errors.Is(err, objectstore.ErrSize) {
		t.Fatalf("oversized retained document bypassed decode ceiling: %v", err)
	}
	if _, err := w.f.db.Exec(t.Context(), `UPDATE blob_objects SET size=$2 WHERE store='policy-inputs' AND key=$1`, input.Key, size); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(w.root, string(objectstore.PolicyInputs), input.Key)
	sealed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := bytes.Clone(sealed)
	corrupt[len(corrupt)-1] ^= 1
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.samples.Read(t.Context(), input.Account, id); err == nil || errors.Is(err, sampling.ErrNotFound) || strings.Contains(err.Error(), "private-retained") {
		t.Fatalf("unauthenticated sample succeeded, disappeared or leaked input: %v", err)
	}
	if err := os.WriteFile(path, sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.samples.Read(t.Context(), input.Account, id); err != nil {
		t.Fatalf("authentication failure damaged owner metadata: %v", err)
	}
}

func TestAccSampleReadRejectsAuthenticatedButInvalidHistoricalPayload(t *testing.T) {
	w := newSampleWorld(t)
	id := w.evaluate(t).Receipts[0].ID
	input, err := w.f.store.Input(t.Context(), w.f.principal.AccountID, id, false)
	if err != nil {
		t.Fatal(err)
	}
	// A valid seal is insufficient when an older writer's private document
	// does not match the current payload contract.
	raw := []byte(`{"input":{"private":"private-historical-value"},"body":[]}`)
	if _, err := w.objects.Put(t.Context(), objectstore.PolicyInputs, input.Key, bytes.NewReader(raw), int64(len(raw))); err != nil {
		t.Fatal(err)
	}
	job := w.claim(t)
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		if err := w.q.HoldTx(ctx, job); err != nil {
			return err
		}
		if err := w.objects.CommitUntil(ctx, objectstore.PolicyInputs, input.Key, input.ExpiresAt); err != nil {
			return err
		}
		if err := w.f.store.FinishInput(ctx, input.Account, id, "live", w.f.now); err != nil {
			return err
		}
		return w.q.CompleteTx(ctx, job)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.samples.Read(t.Context(), input.Account, id); err == nil || errors.Is(err, sampling.ErrNotFound) || strings.Contains(err.Error(), "private-historical-value") {
		t.Fatalf("invalid retained payload succeeded, disappeared or leaked private bytes: %v", err)
	}
}

func TestAccInterruptedSampleBeforePutIsDiscardedWithoutAnImmutableRewrite(t *testing.T) {
	w := newSampleWorld(t)
	id := w.evaluate(t).Receipts[0].ID
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		return w.f.store.StartUpload(ctx, w.f.principal.AccountID, id)
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.samples.Upload(t.Context(), w.claim(t)); err != nil {
		t.Fatal(err)
	}
	w.inputState(t, id, "failed", false)
	var blobs int
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM blob_objects WHERE store='policy-inputs'`).Scan(&blobs); err != nil || blobs != 0 {
		t.Fatalf("ambiguous upload was attempted again: %d %v", blobs, err)
	}
}

func TestAccExpiredUploadUsesTheDefaultClockWithoutRetainingPrivateInput(t *testing.T) {
	w := newSampleWorld(t)
	id := w.evaluate(t).Receipts[0].ID
	if _, err := w.f.db.Exec(
		t.Context(),
		`UPDATE policy_input_objects SET expires_at=statement_timestamp()-interval '1 minute' WHERE account_id=$1 AND id=$2`,
		w.f.principal.AccountID,
		id,
	); err != nil {
		t.Fatal(err)
	}
	svc := sampling.New(sampling.Deps{DB: w.f.db, Store: w.f.store, Queue: w.q, Objects: w.objects})
	if err := svc.Upload(t.Context(), w.claim(t)); err != nil {
		t.Fatal(err)
	}
	w.inputState(t, id, "failed", false)
	statuses, err := svc.SampleStatuses(t.Context(), w.f.principal.AccountID, []kernel.ULID{id})
	if err != nil || statuses[id].Sampled || statuses[id].Expired {
		t.Fatalf("expired request invented a retained sample: %+v %v", statuses, err)
	}
}

func TestAccSampleExpiringDuringExternalPutCannotPublish(t *testing.T) {
	w := newSampleWorld(t)
	id := w.evaluate(t).Receipts[0].ID
	job := w.claim(t)
	objects, svc := w.pausedUpload(t)
	finished := make(chan error, 1)
	go func() { finished <- svc.Upload(t.Context(), job) }()
	awaitUpload(t, objects.completed)
	if _, err := w.f.db.Exec(
		t.Context(),
		`UPDATE policy_input_objects SET expires_at=$3 WHERE account_id=$1 AND id=$2`,
		w.f.principal.AccountID,
		id,
		w.f.now.Add(-time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	close(objects.resume)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	w.inputState(t, id, "failed", false)
}

func TestAccSampleReadReportsMissingStorageWithoutPrivateErrorContents(t *testing.T) {
	for _, missing := range []string{"metadata table", "blob table", "object file"} {
		t.Run(missing, func(t *testing.T) {
			w := newSampleWorld(t)
			id := w.evaluate(t).Receipts[0].ID
			w.uploadAll(t)
			input, err := w.f.store.Input(t.Context(), w.f.principal.AccountID, id, false)
			if err != nil {
				t.Fatal(err)
			}
			switch missing {
			case "metadata table":
				_, err = w.f.db.Exec(t.Context(), `ALTER TABLE policy_evaluations RENAME TO unavailable_evaluations`)
			case "blob table":
				_, err = w.f.db.Exec(t.Context(), `ALTER TABLE blob_objects RENAME TO unavailable_blobs`)
			case "object file":
				err = os.Remove(filepath.Join(w.root, string(objectstore.PolicyInputs), input.Key))
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.samples.Read(t.Context(), input.Account, id); err == nil || strings.Contains(err.Error(), "private-retained") {
				t.Fatalf("read hid missing storage or leaked private input: %v", err)
			}
			if missing == "blob table" {
				if _, err := w.samples.SampleStatuses(t.Context(), input.Account, []kernel.ULID{id}); err == nil {
					t.Fatal("blob storage outage became a successful sample status")
				}
			}
		})
	}
}
