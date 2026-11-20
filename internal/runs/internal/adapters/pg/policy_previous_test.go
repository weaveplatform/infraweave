package pg_test

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policiesapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/policyinput"
)

func TestAccPolicyInputSnapshotsCurrentAndPreviousOwnedFlags(t *testing.T) {
	w := newWorld(t)
	first := mustCreate(t, w, kernel.RunTypeTracked, "first")
	second := mustCreate(t, w, kernel.RunTypeTracked, "second")
	third := mustCreate(t, w, kernel.RunTypeTracked, "third")
	runs := []lifecycle.Run{first, second, third}
	// Creation time equality is deliberate: immutable ID supplies the stable
	// tie break, independently of creation/evaluation order in the test.
	sort.Slice(runs, func(i, j int) bool { return runs[i].ID.String() < runs[j].ID.String() })
	for i := range runs {
		r := policyReceipt(w, kernel.NewULID())
		r.Flags = []string{[]string{"older", "middle", "later"}[i]}
		if err := w.in(t, func(ctx context.Context) error {
			return w.store.RecordPolicyEvidence(ctx, w.acct, runs[i].ID, runs[i].Version, []policiesapi.Receipt{r})
		}); err != nil {
			t.Fatal(err)
		}
	}
	// These newer rows would win an unqualified account/stack lookup.
	foreign := insertRun(t, w.db, kernel.NewAccountID(), w.stack, false)
	otherStack := insertRun(t, w.db, w.acct, kernel.NewULID(), false)
	for _, id := range []kernel.ULID{foreign, otherStack} {
		if _, err := w.db.Exec(t.Context(), `UPDATE runs SET created_at=$2 WHERE id=$1`, id, w.now.Add(-time.Microsecond)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.in(t, func(ctx context.Context) error {
		oldest, err := policyinput.Read(ctx, w.store, w.acct, runs[0].ID)
		if err != nil || oldest.Previous != nil {
			t.Fatalf("first evaluation has a previous run: %+v %v", oldest, err)
		}
		middle, err := policyinput.Read(ctx, w.store, w.acct, runs[1].ID)
		if err != nil || middle.Previous == nil || middle.Previous.Run.ID != runs[0].ID ||
			!reflect.DeepEqual(middle.Flags, []string{"middle"}) || !reflect.DeepEqual(middle.Previous.Flags, []string{"older"}) {
			t.Fatalf("same-time predecessor/flags/isolation changed: %+v %v", middle, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	w.now = w.now.Add(time.Second)
	next := mustCreate(t, w, kernel.RunTypeTracked, "policy-triggered")
	if err := w.in(t, func(ctx context.Context) error {
		snapshot, err := policyinput.Read(ctx, w.store, w.acct, next.ID)
		if err != nil || len(snapshot.Flags) != 0 || snapshot.Previous == nil || snapshot.Previous.Run.ID != runs[2].ID ||
			!reflect.DeepEqual(snapshot.Previous.Flags, []string{"later"}) {
			t.Fatalf("new run copied owners or lost previous snapshot: %+v %v", snapshot, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
