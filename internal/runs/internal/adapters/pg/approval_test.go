package pg_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
)

func TestAccApprovalAggregateIsStateScopedAndTransactional(t *testing.T) {
	w := newWorld(t)
	r := mustCreate(t, w, kernel.RunTypeProposed, "approval-state")
	gate := evidence.Approval{State: r.State, StateVersion: r.Version, HasPolicies: true, Outcome: policyapi.Undecided, At: w.now}
	write := func(value evidence.Approval) error {
		return tx.Do(t.Context(), w.db, tx.Options{}, func(ctx context.Context) error { return w.store.RecordApproval(ctx, w.acct, r.ID, value) })
	}
	if err := w.store.RecordApproval(t.Context(), w.acct, r.ID, gate); !errors.Is(err, lifecycle.ErrNoTransaction) {
		t.Fatalf("unfenced gate: %v", err)
	}
	if err := write(gate); err != nil {
		t.Fatal(err)
	}
	got, err := w.store.Approvals(t.Context(), w.acct, []kernel.ULID{r.ID})
	if err != nil || !got[r.ID].Waiting() {
		t.Fatalf("waiting aggregate: %+v %v", got, err)
	}
	if got, err := w.store.Approvals(t.Context(), kernel.NewAccountID(), []kernel.ULID{r.ID}); err != nil || len(got) != 0 {
		t.Fatalf("foreign aggregate: %+v %v", got, err)
	}
	gate.HasPolicies, gate.Outcome = false, policyapi.NoPolicies
	if err := write(gate); err != nil {
		t.Fatal(err)
	}
	got, err = w.store.Approvals(t.Context(), w.acct, []kernel.ULID{r.ID})
	if err != nil || got[r.ID].HasPolicies || got[r.ID].Waiting() || got[r.ID].Outcome != policyapi.NoPolicies {
		t.Fatalf("removed policy remained pending: %+v %v", got, err)
	}
	invalid := gate
	invalid.Outcome = policyapi.Approve
	if err := write(invalid); !errors.Is(err, evidence.ErrReceipt) {
		t.Fatalf("inconsistent aggregate: %v", err)
	}
	gate.HasPolicies, gate.Outcome = true, policyapi.Approve
	rollback := tx.Do(t.Context(), w.db, tx.Options{}, func(ctx context.Context) error {
		if err := w.store.RecordApproval(ctx, w.acct, r.ID, gate); err != nil {
			return err
		}
		return errInjected
	})
	if !errors.Is(rollback, errInjected) {
		t.Fatal(rollback)
	}
	got, err = w.store.Approvals(t.Context(), w.acct, []kernel.ULID{r.ID})
	if err != nil || got[r.ID].Outcome != policyapi.NoPolicies {
		t.Fatalf("rolled-back approval escaped: %+v %v", got, err)
	}
	r = w.mustMove(t, r, kernel.RunStateReady, machine.DriverScheduler, machine.GuardStackLockFree, machine.GuardApproved,
		machine.GuardUpstreamFinished, machine.GuardExternalSettled, machine.GuardStackEnabled)
	if err := write(gate); !errors.Is(err, lifecycle.ErrConflict) {
		t.Fatalf("stale approval accepted: %v", err)
	}
	got, err = w.store.Approvals(t.Context(), w.acct, []kernel.ULID{r.ID})
	if err != nil || len(got) != 0 {
		t.Fatalf("old state gate leaked: %+v %v", got, err)
	}
}

func TestAccApprovalScanPagesFromAbsentCursor(t *testing.T) {
	w := newWorld(t)
	ids := make([]kernel.ULID, 30)
	for i := range ids {
		ids[i] = kernel.NewULID()
		insertRun(t, w.db, w.acct, ids[i], false)
	}
	insertRun(t, w.db, w.acct, ids[0], false)
	if _, err := w.db.Exec(t.Context(), `UPDATE runs SET state='QUEUED' WHERE account_id=$1`, w.acct); err != nil {
		t.Fatal(err)
	}
	insertRun(t, w.db, kernel.NewAccountID(), kernel.NewULID(), false)
	slices.SortFunc(ids, func(a, b kernel.ULID) int { return bytes.Compare(a[:], b[:]) })
	var after kernel.ULID
	got := []kernel.ULID{}
	for {
		page, err := w.store.WaitingApprovalStacks(t.Context(), w.acct, after, 7)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		got = append(got, page...)
		after = page[len(page)-1]
		if len(got) > len(ids) {
			t.Fatal("approval scan repeated a page")
		}
	}
	if !slices.Equal(ids, got) {
		t.Fatalf("approval scan lost or duplicated waiting stacks: got %v want %v", got, ids)
	}
	if foreign, err := w.store.WaitingApprovalStacks(t.Context(), kernel.NewAccountID(), kernel.ULID{}, 7); err != nil || len(foreign) != 0 {
		t.Fatalf("approval scan crossed account: %+v %v", foreign, err)
	}
}
