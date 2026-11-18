package pg_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/push"
)

type refusedCapAudit struct {
	push.LimitStore
	err error
}

func (s refusedCapAudit) Audit(context.Context, kernel.AuditEntry) error { return s.err }

func TestAccEventRunLimitSettings(t *testing.T) {
	f := newFixture(t)
	limits := &push.Limits{DB: f.db, Store: f.store, Spaces: f.deps.Spaces, Ceiling: 2, Now: f.deps.Now}
	if got, err := limits.Get(t.Context(), f.acct); err != nil || got != nil {
		t.Fatal("unset account cap is not null", got, err)
	}
	for _, value := range []int{2, 1} {
		got, err := limits.Set(t.Context(), f.admin, &value, "192.0.2.4")
		if err != nil || got == nil || *got != value {
			t.Fatal("account cap write failed", got, err)
		}
		// Another replica uses its own service/store, without stale settings.
		replica := &push.Limits{Store: pg.New(f.db, f.keys), Ceiling: 2}
		if got, err := replica.Effective(t.Context(), f.acct); err != nil || got != value {
			t.Fatal("replica cap read is stale", got, err)
		}
	}
	cause := errors.New("audit unavailable")
	refused := *limits
	refused.Store = refusedCapAudit{LimitStore: f.store, err: cause}
	if _, err := refused.Set(t.Context(), f.admin, new(2), "192.0.2.4"); !errors.Is(err, cause) {
		t.Fatal("audit failure was hidden", err)
	}
	if got, err := limits.Get(t.Context(), f.acct); err != nil || got == nil || *got != 1 {
		t.Fatal("failed audit committed its cap", got, err)
	}
	if got, err := limits.Set(t.Context(), f.admin, nil, "192.0.2.4"); err != nil || got != nil {
		t.Fatal("clear did not return null", got, err)
	}
	if got, err := limits.Get(t.Context(), f.acct); err != nil || got != nil {
		t.Fatal("clear retained a configured cap", got, err)
	}
	var count int
	var correct bool
	if err := f.db.QueryRow(t.Context(), `SELECT count(*),bool_and(actor_username=$2 AND affected_type='ACCOUNT'
		AND affected_id=$3 AND remote_ip='192.0.2.4') FROM audit_trail_entries
		WHERE account_id=$1 AND action='account.update_vcs_event_triggered_runs_limit'`,
		f.acct, f.admin.Login, f.acct.String()).Scan(&count, &correct); err != nil || count != 3 || !correct {
		t.Fatal("cap changes lack atomic caller-attributed audits", count, correct, err)
	}
	foreign := kernel.NewAccountID()
	if err := f.store.SetRunLimit(t.Context(), foreign, new(2)); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetRunLimit(t.Context(), f.acct, new(1)); err != nil {
		t.Fatal(err)
	}
	if err := f.store.PurgeRunLimit(t.Context(), f.acct); err != nil {
		t.Fatal(err)
	}
	if got, err := limits.Get(t.Context(), f.acct); err != nil || got != nil {
		t.Fatal("purge retained the deleted account cap", got, err)
	}
	if got, err := limits.Get(t.Context(), foreign); err != nil || got == nil || *got != 2 {
		t.Fatal("purge crossed the account boundary", got, err)
	}
}

func TestAccDeliveryRunAdmission(t *testing.T) {
	w := newWebhookWorld(t)
	w.receive(t, "capped", webhookBody(128))
	delivery := w.delivery(t, "capped")
	if _, err := w.f.store.ReserveRun(t.Context(), w.f.acct, delivery.ID, 1); !errors.Is(err, push.ErrNoTransaction) {
		t.Fatal("admission ran outside a creation transaction", err)
	}
	cause := errors.New("run insertion failed")
	err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		allowed, err := w.f.store.ReserveRun(ctx, w.f.acct, delivery.ID, 1)
		if err != nil || !allowed {
			t.Fatal("first admission was refused", allowed, err)
		}
		return cause
	})
	if !errors.Is(err, cause) || w.delivery(t, "capped").RunsCreated != 0 {
		t.Fatal("rolled-back run consumed a slot", err)
	}
	var admitted atomic.Int64
	var workers sync.WaitGroup
	start := make(chan struct{})
	for range 2 {
		workers.Go(func() {
			<-start
			allowed := false
			err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
				var err error
				allowed, err = w.f.store.ReserveRun(ctx, w.f.acct, delivery.ID, 1)
				return err
			})
			if err != nil {
				t.Error(err)
			} else if allowed {
				admitted.Add(1)
			}
		})
	}
	close(start)
	workers.Wait()
	if admitted.Load() != 1 || w.delivery(t, "capped").RunsCreated != 1 {
		t.Fatal("concurrent admission exceeded the shared budget", admitted.Load())
	}
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		allowed, err := w.f.store.ReserveRun(ctx, kernel.NewAccountID(), delivery.ID, 2)
		if allowed {
			t.Error("foreign account consumed a delivery slot")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAccDeliveryRunAdmissionFailureRollsBackCompletion(t *testing.T) {
	w := newWebhookWorld(t)
	w.receive(t, "reservation-failure", webhookBody(128))
	delivery := w.delivery(t, "reservation-failure")
	job := w.claim(t, w.queue, "admission")
	if _, err := w.f.db.Exec(t.Context(), `CREATE FUNCTION refuse_event_run_reservation() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN RAISE EXCEPTION 'reservation storage unavailable' USING ERRCODE='P0001'; END $$;
	CREATE TRIGGER refuse_event_run_reservation BEFORE UPDATE OF runs_created ON vcs_deliveries
	FOR EACH ROW EXECUTE FUNCTION refuse_event_run_reservation()`); err != nil {
		t.Fatal(err)
	}
	allowed, attempts := false, 0
	err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		attempts++
		if err := w.queue.HoldTx(ctx, job); err != nil {
			return err
		}
		// Write completion first so the reservation failure must roll back an
		// actual acknowledgement, rather than skip one that was never written.
		if err := w.queue.CompleteTx(ctx, job); err != nil {
			return err
		}
		var err error
		allowed, err = w.f.store.ReserveRun(ctx, w.f.acct, delivery.ID, 1)
		return err
	})
	if cause, ok := errors.AsType[*pgconn.PgError](err); !ok || cause.Code != "P0001" || allowed || attempts != 1 {
		t.Fatalf("reservation failure became a cap or was retried: allowed=%t attempts=%d error=%v", allowed, attempts, err)
	}
	if got := w.delivery(t, "reservation-failure").RunsCreated; got != 0 {
		t.Fatalf("failed reservation consumed %d slots", got)
	}
	var state string
	var finished, sameAttempt bool
	if err := w.f.db.QueryRow(t.Context(), `SELECT state,finished_at IS NOT NULL,generation=$2 AND attempt=$3
		FROM jobs WHERE id=$1`, job.ID, job.Generation, job.Attempt).Scan(&state, &finished, &sameAttempt); err != nil ||
		state != "running" || finished || !sameAttempt {
		t.Fatalf("failed reservation committed completion: state=%s finished=%t same_attempt=%t error=%v", state, finished, sameAttempt, err)
	}
	if _, err := w.f.db.Exec(t.Context(), `DROP TRIGGER refuse_event_run_reservation ON vcs_deliveries;
		DROP FUNCTION refuse_event_run_reservation()`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		if err := w.queue.HoldTx(ctx, job); err != nil {
			return err
		}
		var err error
		allowed, err = w.f.store.ReserveRun(ctx, w.f.acct, delivery.ID, 1)
		if err != nil {
			return err
		}
		if !allowed {
			return errors.New("failed reservation left no slot for retry")
		}
		return w.queue.CompleteTx(ctx, job)
	}); err != nil {
		t.Fatal("same-attempt admission retry failed", err)
	}
	if got := w.delivery(t, "reservation-failure").RunsCreated; got != 1 {
		t.Fatalf("recovered reservation consumed %d slots, want 1", got)
	}
	if err := w.f.db.QueryRow(t.Context(), `SELECT state,finished_at IS NOT NULL,generation=$2 AND attempt=$3
		FROM jobs WHERE id=$1`, job.ID, job.Generation, job.Attempt).Scan(&state, &finished, &sameAttempt); err != nil ||
		state != "succeeded" || !finished || !sameAttempt {
		t.Fatalf("recovered reservation did not complete its attempt: state=%s finished=%t same_attempt=%t error=%v", state, finished, sameAttempt, err)
	}
}
