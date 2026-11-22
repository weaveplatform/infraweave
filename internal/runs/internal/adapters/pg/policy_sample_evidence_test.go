package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policiesapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
)

// The adapter preserves an opaque sample-owner link. Publication and availability
// are separate policies-owned facts and are not implied by this stored UUID.
func linkedPolicyReceipt(t *testing.T, w *world, run lifecycle.Run) (policiesapi.Receipt, map[kernel.ULID]evidence.Snapshot) {
	t.Helper()
	r := policyReceipt(w, kernel.NewULID())
	r.SampleEvaluation = new(kernel.NewULID())
	if err := w.in(t, func(ctx context.Context) error {
		return w.store.RecordPolicyEvidence(ctx, w.acct, run.ID, run.Version, []policiesapi.Receipt{r})
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := w.store.PolicyEvidence(t.Context(), w.acct, []kernel.ULID{run.ID})
	if err != nil || len(snapshot[run.ID].Receipts) != 1 {
		t.Fatalf("linked receipt metadata: %+v %v", snapshot, err)
	}
	stored := snapshot[run.ID].Receipts[0]
	if stored.ID != r.ID || stored.Policy != r.Policy || stored.SampleEvaluation == nil || *stored.SampleEvaluation != *r.SampleEvaluation || !stored.SampleRequested {
		t.Fatalf("opaque sample link did not round-trip: %+v", stored)
	}
	return r, snapshot
}

func assertLinkedPolicyReceiptUnchanged(t *testing.T, w *world, run lifecycle.Run, before map[kernel.ULID]evidence.Snapshot) {
	t.Helper()
	after, err := w.store.PolicyEvidence(t.Context(), w.acct, []kernel.ULID{run.ID})
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed receipt batch changed the sample link or flags: %+v %v", after, err)
	}
	stored, err := w.store.Get(t.Context(), w.acct, run.ID)
	if err != nil || stored.State != run.State || stored.Version != run.Version || len(w.updates(t, run.ID)) != 1 || len(w.steps(t, run.ID)) != 1 {
		t.Fatalf("failed receipt batch changed run history: %+v %v", stored, err)
	}
}

func TestAccPolicySampleLinkSurvivesRejectedReceiptBatch(t *testing.T) {
	w := newWorld(t)
	run := mustCreate(t, w, kernel.RunTypeTracked, "linked-policy-evidence")
	recorded, before := linkedPolicyReceipt(t, w, run)
	first := policyReceipt(w, recorded.Policy)
	first.Flags, first.ResetFlags = []string{"replacement"}, []string{"reviewed"}
	first.SampleEvaluation = new(kernel.NewULID())
	for _, invalid := range []struct {
		name   string
		change func(*policiesapi.Receipt)
	}{
		{"missing receipt ID", func(r *policiesapi.Receipt) { r.ID = kernel.ULID{} }},
		{"missing policy ID", func(r *policiesapi.Receipt) { r.Policy = kernel.ULID{} }},
		{"malformed decision", func(r *policiesapi.Receipt) { r.Decision = json.RawMessage(`{"approve":`) }},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			last := policyReceipt(w, recorded.Policy)
			invalid.change(&last)
			err := w.in(t, func(ctx context.Context) error {
				return w.store.RecordPolicyEvidence(ctx, w.acct, run.ID, run.Version, []policiesapi.Receipt{first, last})
			})
			if !errors.Is(err, evidence.ErrReceipt) {
				t.Fatalf("invalid receipt accepted: %v", err)
			}
			assertLinkedPolicyReceiptUnchanged(t, w, run, before)
		})
	}
	if err := w.store.RecordPolicyEvidence(t.Context(), w.acct, run.ID, run.Version, []policiesapi.Receipt{first}); !errors.Is(err, lifecycle.ErrNoTransaction) {
		t.Fatalf("sample link escaped its caller transaction: %v", err)
	}
	for _, owner := range []struct {
		name    string
		account kernel.AccountID
		run     kernel.ULID
		version int64
		want    error
	}{
		{"missing run", w.acct, kernel.NewULID(), run.Version, lifecycle.ErrNotFound},
		{"foreign account", kernel.NewAccountID(), run.ID, run.Version, lifecycle.ErrNotFound},
		{"wrong state version", w.acct, run.ID, run.Version + 1, lifecycle.ErrConflict},
	} {
		t.Run(owner.name, func(t *testing.T) {
			err := w.in(t, func(ctx context.Context) error {
				return w.store.RecordPolicyEvidence(ctx, owner.account, owner.run, owner.version, []policiesapi.Receipt{first})
			})
			if !errors.Is(err, owner.want) {
				t.Fatalf("wrong receipt owner/version accepted: %v", err)
			}
			assertLinkedPolicyReceiptUnchanged(t, w, run, before)
		})
	}
	assertLinkedPolicyReceiptUnchanged(t, w, run, before)
}

func TestAccPolicySampleLinkSurvivesBackendLoss(t *testing.T) {
	w := newWorld(t)
	run := mustCreate(t, w, kernel.RunTypeTracked, "disconnected-policy-link")
	_, before := linkedPolicyReceipt(t, w, run)
	terminated := false
	err := w.in(t, func(ctx context.Context) error {
		var backend int64
		if err := tx.Q(ctx, w.db).QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&backend); err != nil {
			return err
		}
		if err := w.db.QueryRow(ctx, `SELECT pg_terminate_backend($1::integer,5000)`, backend).Scan(&terminated); err != nil {
			return err
		}
		return w.store.RecordPolicyEvidence(ctx, w.acct, run.ID, run.Version, []policiesapi.Receipt{policyReceipt(w, kernel.NewULID())})
	})
	if !terminated || !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("lost PostgreSQL connection acknowledged evidence: terminated=%t error=%v", terminated, err)
	}
	assertLinkedPolicyReceiptUnchanged(t, w, run, before)
}

func TestAccClosedPolicyEvidenceStorageCannotReturnEmptyMetadata(t *testing.T) {
	w := newWorld(t)
	run := mustCreate(t, w, kernel.RunTypeTracked, "unavailable-policy-link")
	linkedPolicyReceipt(t, w, run)
	w.db.Close()
	if got, err := w.store.PolicyEvidence(t.Context(), w.acct, []kernel.ULID{run.ID}); err == nil || got != nil {
		t.Fatalf("closed metadata storage returned a policy-free snapshot: %+v %v", got, err)
	}
}
