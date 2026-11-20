package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/review"
)

func reviewRecord(w *world, r lifecycle.Run) review.Record {
	p := w.alice()
	roles := []policyapi.RoleFacts{{ID: "space-writer", Slug: "space-writer", Name: "Original writer", ULID: kernel.NewULID().String()}}
	return review.Record{
		Account: w.acct, Run: r.ID, ID: kernel.NewULID(), AuthorKey: p.Caller(), Caller: p.Caller(), Session: kernel.NewULID(),
		Decision: review.Approve, State: r.State, StateVersion: r.Version, CreatedAt: w.now, RequestID: "request-review",
		Facts: policyapi.ReviewFacts{Author: p.Login, State: r.State, StateVersion: r.Version, AuthorRoles: policyapi.Fact[[]policyapi.RoleFacts]{Known: true, Value: &roles}},
	}
}

func TestAccReviewStorageFencesMetadataAndAccount(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	r := mustCreate(t, w, kernel.RunTypeProposed, "review")
	first := reviewRecord(w, r)
	first.Note = new("review complete")
	first.Facts.Note = policyapi.Fact[string]{Known: true, Value: first.Note}
	if err := tx.Do(ctx, w.db, tx.Options{}, func(ctx context.Context) error { return w.store.RecordReview(ctx, first) }); err != nil {
		t.Fatal(err)
	}
	(*first.Facts.AuthorRoles.Value)[0].Name = "Changed after recording"
	got, err := w.store.Reviews(ctx, w.acct, []kernel.ULID{r.ID})
	if err != nil || len(got[r.ID]) != 1 || got[r.ID][0].ID != first.ID || got[r.ID][0].StateVersion != r.Version ||
		got[r.ID][0].Facts.AuthorRoles.Value == nil || (*got[r.ID][0].Facts.AuthorRoles.Value)[0].Name != "Original writer" {
		t.Fatalf("immutable review history missing or changed: %+v %v", got, err)
	}
	saved := got[r.ID][0]
	original, err := json.Marshal(saved.Facts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.Exec(ctx, `UPDATE run_reviews SET facts='{"private-metadata-value":"never-echo-this"}'::jsonb WHERE account_id=$1 AND id=$2`, w.acct, first.ID); err != nil {
		t.Fatal(err)
	}
	if invalid, err := w.store.Reviews(
		ctx,
		w.acct,
		[]kernel.ULID{r.ID},
	); !errors.Is(err, kernel.ErrUnavailable) || invalid != nil ||
		strings.Contains(err.Error(), "never-echo-this") {
		t.Fatalf("corrupt review metadata did not fail safely: %+v %v", invalid, err)
	}
	if _, err := w.db.Exec(ctx, `UPDATE run_reviews SET facts=$3 WHERE account_id=$1 AND id=$2`, w.acct, first.ID, original); err != nil {
		t.Fatal(err)
	}
	inconsistent := first
	inconsistent.ID = kernel.NewULID()
	inconsistent.Note = new("another note")
	if err := tx.Do(ctx, w.db, tx.Options{}, func(ctx context.Context) error { return w.store.RecordReview(ctx, inconsistent) }); !errors.Is(err, review.ErrReview) {
		t.Fatalf("inconsistent review/snapshot note accepted: %v", err)
	}
	if foreign, err := w.store.Reviews(ctx, kernel.NewAccountID(), []kernel.ULID{r.ID}); err != nil || len(foreign) != 0 {
		t.Fatalf("review crossed account: %+v %v", foreign, err)
	}
	r = w.mustMove(
		t,
		r,
		kernel.RunStateReady,
		machine.DriverScheduler,
		machine.GuardStackLockFree,
		machine.GuardApproved,
		machine.GuardUpstreamFinished,
		machine.GuardExternalSettled,
		machine.GuardStackEnabled,
	)
	stale := first
	stale.ID = kernel.NewULID()
	err = tx.Do(ctx, w.db, tx.Options{}, func(ctx context.Context) error { return w.store.RecordReview(ctx, stale) })
	if !errors.Is(err, lifecycle.ErrConflict) {
		t.Fatalf("stale review accepted: %v", err)
	}
	if err := w.store.RecordReview(ctx, stale); !errors.Is(err, lifecycle.ErrNoTransaction) {
		t.Fatalf("review write escaped its transaction: %v", err)
	}
	other := mustCreate(t, w, kernel.RunTypeProposed, "review-rollback")
	pending := reviewRecord(w, other)
	err = tx.Do(ctx, w.db, tx.Options{}, func(ctx context.Context) error {
		if err := w.store.RecordReview(ctx, pending); err != nil {
			return err
		}
		return errInjected
	})
	if !errors.Is(err, errInjected) {
		t.Fatal(err)
	}
	got, err = w.store.Reviews(ctx, w.acct, []kernel.ULID{other.ID})
	if err != nil || len(got[other.ID]) != 0 {
		t.Fatalf("rolled-back review escaped: %+v %v", got, err)
	}
}
