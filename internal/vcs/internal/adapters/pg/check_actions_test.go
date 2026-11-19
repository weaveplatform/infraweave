package pg_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/push"
	"github.com/weaveplatform/infraweave/test/deps"
)

func actionEffect(t *testing.T, w *webhookWorld, account kernel.AccountID, delivery, stack kernel.ULID) bool {
	t.Helper()
	var found bool
	err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		var err error
		found, err = w.f.store.HasCheckAction(ctx, account, delivery, stack)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestAccCheckActionEffectAtomicityAndOwnership(t *testing.T) {
	w := newWebhookWorld(t)
	w.receive(t, "intent", webhookBody(128))
	delivery := w.delivery(t, "intent")
	stack := kernel.NewULID()
	if found, err := w.f.store.HasCheckAction(t.Context(), w.f.acct, delivery.ID, stack); found || !errors.Is(err, push.ErrNoTransaction) {
		t.Fatal("effect lookup escaped transaction", found, err)
	}
	if err := w.f.store.RecordCheckAction(t.Context(), w.f.acct, delivery.ID, stack); !errors.Is(err, push.ErrNoTransaction) {
		t.Fatal("effect write escaped transaction", err)
	}
	job := w.claim(t, w.queue, "effect")
	complete := func(ctx context.Context) error {
		if err := w.queue.HoldTx(ctx, job); err != nil {
			return err
		}
		allowed, err := w.f.store.ReserveRun(ctx, w.f.acct, delivery.ID, 0, 1)
		if err != nil {
			return err
		}
		if !allowed {
			return errors.New("first intent admission refused")
		}
		if err := w.f.store.RecordCheckAction(ctx, w.f.acct, delivery.ID, stack); err != nil {
			return err
		}
		return w.queue.CompleteTx(ctx, job)
	}
	cause := errors.New("completion aborted")
	err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		if err := complete(ctx); err != nil {
			return err
		}
		return cause
	})
	if !errors.Is(err, cause) || actionEffect(t, w, w.f.acct, delivery.ID, stack) || w.delivery(t, "intent").RunsCreated != 0 {
		t.Fatal("failed completion retained effect or cap", err)
	}
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, complete); err != nil {
		t.Fatal("same claim did not recover", err)
	}
	if !actionEffect(t, w, w.f.acct, delivery.ID, stack) || actionEffect(t, w, kernel.NewAccountID(), delivery.ID, stack) ||
		actionEffect(t, w, w.f.acct, delivery.ID, kernel.NewULID()) {
		t.Fatal("successful effect lost ownership scope")
	}
	// A conflict must abort; ignoring it could commit a second run behind the
	// first receipt. The original receipt and completed claim remain untouched.
	err = tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.f.store.RecordCheckAction(ctx, w.f.acct, delivery.ID, stack) })
	if pg, ok := errors.AsType[*pgconn.PgError](err); !ok || pg.Code != "23505" {
		t.Fatal("duplicate receipt was silently ignored", err)
	}
	err = tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		return w.f.store.RecordCheckAction(ctx, kernel.NewAccountID(), delivery.ID, stack)
	})
	if pg, ok := errors.AsType[*pgconn.PgError](err); !ok || pg.Code != "23503" {
		t.Fatal("foreign receipt escaped delivery ownership", err)
	}
	if !actionEffect(t, w, w.f.acct, delivery.ID, stack) || w.delivery(t, "intent").RunsCreated != 1 {
		t.Fatal("refused writes damaged original intent")
	}
}

func TestAccCheckActionEffectRetentionAndAccountCascade(t *testing.T) {
	w := newWebhookWorld(t)
	foreignFixture := *w.f
	foreignFixture.acct = kernel.NewAccountID()
	foreign := newWebhookWorldOn(t, &foreignFixture)
	stack := kernel.NewULID()
	for _, name := range []string{"old", "new"} {
		w.receive(t, name, webhookBody(128))
		id := w.delivery(t, name).ID
		if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.f.store.RecordCheckAction(ctx, w.f.acct, id, stack) }); err != nil {
			t.Fatal(err)
		}
	}
	foreign.receive(t, "foreign", webhookBody(128))
	other := foreign.delivery(t, "foreign")
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		return w.f.store.RecordCheckAction(ctx, foreign.f.acct, other.ID, stack)
	}); err != nil {
		t.Fatal(err)
	}
	old, newer := w.delivery(t, "old"), w.delivery(t, "new")
	if _, err := w.f.db.Exec(t.Context(), `UPDATE vcs_deliveries SET received_at=now()-interval '8 days' WHERE account_id=$1 AND id=$2`, w.f.acct, old.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Pruned(t.Context(), queue.Job{}); err != nil {
		t.Fatal(err)
	}
	if actionEffect(t, w, w.f.acct, old.ID, stack) || !actionEffect(t, w, w.f.acct, newer.ID, stack) {
		t.Fatal("delivery retention did not own its exact effect lifetime")
	}
	if err := w.svc.Purge(t.Context(), w.f.acct); err != nil {
		t.Fatal(err)
	}
	if actionEffect(t, w, w.f.acct, newer.ID, stack) || !actionEffect(t, w, foreign.f.acct, other.ID, stack) {
		t.Fatal("account purge leaked or crossed receipt ownership")
	}
}

func TestAccCheckActionEffectCompletionHoldFencesRetention(t *testing.T) {
	w := newWebhookWorld(t)
	w.receive(t, "held", webhookBody(128))
	delivery := w.delivery(t, "held")
	stack := kernel.NewULID()
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.f.store.RecordCheckAction(ctx, w.f.acct, delivery.ID, stack) }); err != nil {
		t.Fatal(err)
	}
	connection, err := w.f.db.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	held, release, owner := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		owner <- tx.Do(t.Context(), connection, tx.Options{}, func(ctx context.Context) error {
			_, found, err := w.f.store.EventContext(ctx, w.f.acct, delivery.ID)
			if err != nil {
				return err
			}
			if !found {
				return errors.New("delivery missing")
			}
			done, err := w.f.store.HasCheckAction(ctx, w.f.acct, delivery.ID, stack)
			if err != nil {
				return err
			}
			if !done {
				return errors.New("effect missing")
			}
			close(held)
			<-release
			return nil
		})
	}()
	select {
	case <-held:
	case err := <-owner:
		t.Fatal(err)
	}
	pruned := make(chan error, 1)
	go func() {
		_, count, err := w.store.Drop(t.Context(), &w.f.acct, nil, 1)
		if err == nil && count != 1 {
			err = fmt.Errorf("pruned %d deliveries", count)
		}
		pruned <- err
	}()
	wait, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	blocked := deps.Eventually(wait, "retention waits for successful-action completion delivery hold", func(ctx context.Context) (bool, error) {
		var waiting bool
		err := w.f.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, connection.Conn().PgConn().PID()).Scan(&waiting)
		return waiting, err
	})
	close(release)
	if err := <-owner; err != nil {
		t.Fatal(err)
	}
	if err := <-pruned; err != nil {
		t.Fatal(err)
	}
	if blocked != nil || actionEffect(t, w, w.f.acct, delivery.ID, stack) {
		t.Fatal("retention bypassed completion or left an effect", blocked)
	}
}
