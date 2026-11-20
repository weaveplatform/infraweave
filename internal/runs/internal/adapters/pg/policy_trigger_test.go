package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
)

func TestAccTerminalTriggerJobMatchesItsCommittedSource(t *testing.T) {
	for _, typ := range []kernel.RunType{kernel.RunTypeTracked, kernel.RunTypeTask, kernel.RunTypeProposed} {
		t.Run(string(typ), func(t *testing.T) {
			w := newWorld(t)
			run := mustCreate(t, w, typ, "terminal-trigger")
			moved := w.mustMove(t, run, kernel.RunStateCanceled, machine.DriverAPI)
			var count int
			if err := w.db.QueryRow(t.Context(), `SELECT count(*) FROM jobs
				WHERE account_id=$1 AND kind='policy.trigger'`, w.acct).Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 1
			if typ == kernel.RunTypeProposed {
				want = 0
			}
			if count != want {
				t.Fatalf("terminal %s queued %d trigger jobs, want %d", typ, count, want)
			}
			if want == 0 {
				return
			}
			var body []byte
			if err := w.db.QueryRow(t.Context(), `SELECT payload FROM jobs
				WHERE account_id=$1 AND kind='policy.trigger'`, w.acct).Scan(&body); err != nil {
				t.Fatal(err)
			}
			var payload lifecycle.PolicyTrigger
			if err := json.Unmarshal(body, &payload); err != nil || payload.Run != run.ID || payload.Stack != run.Stack || payload.StateVersion != moved.Version {
				t.Fatal("terminal work lost its immutable source/version")
			}
			if err := w.in(t, func(ctx context.Context) error { return w.lc.Refresh(ctx, moved) }); err != nil {
				t.Fatal(err)
			}
			if err := w.db.QueryRow(t.Context(), `SELECT count(*) FROM jobs
				WHERE account_id=$1 AND kind='policy.trigger'`, w.acct).Scan(&count); err != nil || count != 1 {
				t.Fatal("evidence refresh enqueued another terminal decision")
			}
		})
	}
}

func TestAccTerminalTriggerEnqueueFailureRollsBackTheTransition(t *testing.T) {
	w := newWorld(t)
	run := mustCreate(t, w, kernel.RunTypeTracked, "trigger-enqueue-rollback")
	failure := failing{Store: w.store, events: w.events, jobs: w.jobs, fail: "EnqueueTx policy.trigger"}
	w.lc = w.lifecycle(w.store, w.events, failure)
	before := len(w.updates(t, run.ID))
	if _, err := w.move(t, run, kernel.RunStateCanceled, machine.DriverAPI); !errors.Is(err, errInjected) {
		t.Fatalf("enqueue fault did not reach the terminal caller: %v", err)
	}
	stored, err := w.store.Get(t.Context(), w.acct, run.ID)
	if err != nil || stored.State != kernel.RunStateQueued || stored.Version != run.Version || len(w.updates(t, run.ID)) != before {
		t.Fatal("terminal state/notification escaped without its durable policy job")
	}
	var count int
	if err := w.db.QueryRow(t.Context(), `SELECT count(*) FROM jobs
		WHERE account_id=$1 AND kind='policy.trigger'`, w.acct).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed terminal transition leaked a trigger job")
	}
}

func TestAccTriggerQueueCountsRemainAccountQualified(t *testing.T) {
	w := newWorld(t)
	first := mustCreate(t, w, kernel.RunTypeTracked, "queue-one")
	mustCreate(t, w, kernel.RunTypeTask, "queue-two")
	proposed := mustCreate(t, w, kernel.RunTypeProposed, "queue-three")
	w.mustMove(t, proposed, kernel.RunStateCanceled, machine.DriverAPI)
	other, unknown := kernel.NewULID(), kernel.NewULID()
	foreignAccount := kernel.NewAccountID()
	foreign := insertRun(t, w.db, foreignAccount, w.stack, false)
	if _, err := w.db.Exec(t.Context(), `UPDATE runs SET state='QUEUED' WHERE account_id=$1 AND id=$2`, foreignAccount, foreign); err != nil {
		t.Fatal(err)
	}
	got, err := w.store.QueuedForPolicyTrigger(t.Context(), w.acct, []kernel.ULID{first.Stack, other, unknown})
	if err != nil || len(got) != 3 || got[first.Stack] != 2 || got[other] != 0 || got[unknown] != 0 {
		t.Fatalf("queue snapshot mixed states/accounts or lost explicit zeros: %+v %v", got, err)
	}
	if err := w.store.HoldPolicyTriggers(t.Context(), w.acct); !errors.Is(err, lifecycle.ErrNoTransaction) {
		t.Fatal("trigger owner fence escaped its transaction")
	}
	if err := w.in(t, func(ctx context.Context) error { return w.store.HoldPolicyTriggers(ctx, w.acct) }); err != nil {
		t.Fatal(err)
	}
	w.db.Close()
	if got, err := w.store.QueuedForPolicyTrigger(t.Context(), w.acct, []kernel.ULID{w.stack}); err == nil || got != nil {
		t.Fatal("unavailable queue storage became a fabricated zero")
	}
	if err := w.in(t, func(ctx context.Context) error { return w.store.HoldPolicyTriggers(ctx, w.acct) }); err == nil {
		t.Fatal("unavailable trigger fence succeeded")
	}
}
