package queuetest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

func TestAccKeyStatusScopeAndTerminalLocks(t *testing.T) {
	q, pool := open(t)
	account := kernel.NewULID()
	const prefix = "delivery%_\\:"
	jobIDs := make([]int64, 0, 5)
	var deadID int64
	for _, state := range []string{"dead", "cancelled", "succeeded", "running", "pending"} {
		id := enqueue(t, q, work, nil, queue.Account(account), queue.UniqueKey(prefix+state), queue.MaxAttempts(1))
		jobIDs = append(jobIDs, id)
		if state == "pending" {
			continue
		}
		j := claimOne(t, q)
		switch state {
		case "dead", "cancelled":
			if dead, err := q.Fail(t.Context(), j, errors.New("failure")); err != nil || !dead {
				t.Fatal("job did not become dead", dead, err)
			}
			if state == "cancelled" {
				if err := q.Discard(t.Context(), j.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				deadID = id
			}
		case "succeeded":
			if err := q.Complete(t.Context(), j); err != nil {
				t.Fatal(err)
			}
		}
	}
	enqueue(t, q, work, nil, queue.Account(kernel.NewULID()), queue.UniqueKey(prefix+"foreign"))
	enqueue(t, q, refresh, nil, queue.Account(account), queue.UniqueKey(prefix+"other-kind"))
	enqueue(t, q, work, nil, queue.Account(account), queue.UniqueKey("deliveryXX:other"))
	enqueue(t, q, work, nil, queue.UniqueKey(prefix+"global"))
	want := queue.KeyStatus{Pending: 1, Running: 1, Succeeded: 1, Dead: 1, Cancelled: 1}
	got, err := q.StatusByKeyPrefixes(t.Context(), account, []queue.Kind{work}, []string{prefix, prefix, "missing"})
	if err != nil || len(got) != 2 || got[prefix] != want || got["missing"] != (queue.KeyStatus{}) {
		t.Fatal("counts ignored literal/account/kind scope or duplicated prefixes", got, err)
	}
	global, err := q.StatusByKeyPrefixes(t.Context(), kernel.ULID{}, []queue.Kind{work}, []string{prefix})
	if err != nil || global[prefix] != (queue.KeyStatus{Pending: 1}) {
		t.Fatal("global lookup crossed account scope", global, err)
	}
	other, err := pgxpool.NewWithConfig(t.Context(), pool.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	err = tx.Do(t.Context(), pool, tx.Options{}, func(ctx context.Context) error {
		held, err := q.HoldByKeyPrefixTx(ctx, account, []queue.Kind{work}, prefix)
		if err != nil || held != want {
			return fmt.Errorf("held status=%+v: %w", held, err)
		}
		for _, id := range jobIDs {
			//nolint:contextcheck // this independent transaction must contend with the held row
			blocked := tx.Do(t.Context(), other, tx.Options{}, func(inner context.Context) error {
				if _, err := tx.Q(inner, other).Exec(inner, `SET LOCAL lock_timeout='50ms'`); err != nil {
					return err
				}
				_, err := tx.Q(inner, other).Exec(inner, `UPDATE jobs SET last_error='contending' WHERE id=$1`, id)
				return err
			})
			if cause, ok := errors.AsType[*pgconn.PgError](blocked); !ok || cause.Code != "55P03" {
				return fmt.Errorf("job %d was not locked: %w", id, blocked)
			}
		}
		//nolint:contextcheck // operator retry uses another transaction, not the caller's held one
		blocked := tx.Do(t.Context(), other, tx.Options{}, func(inner context.Context) error {
			if _, err := tx.Q(inner, other).Exec(inner, `SET LOCAL lock_timeout='50ms'`); err != nil {
				return err
			}
			return queue.New(other).Retry(inner, deadID)
		})
		if cause, ok := errors.AsType[*pgconn.PgError](blocked); !ok || cause.Code != "55P03" {
			return fmt.Errorf("operator retry passed terminal fence: %w", blocked)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.New(other).Retry(t.Context(), deadID); err != nil {
		t.Fatal("released terminal job could not retry", err)
	}
}

// A locking statement that waits for a running parent has an older snapshot than
// the children inserted by that parent's completing transaction. The fresh pass
// must see and lock those children before reporting whether any work is active.
func TestAccKeyPrefixSeesChildrenCommittedDuringLockWait(t *testing.T) {
	q, pool := open(t)
	account := kernel.NewULID()
	const prefix = "delivery:r1:"
	enqueue(t, q, work, nil, queue.Account(account), queue.UniqueKey(prefix+"parent"))
	parent := claimOne(t, q)
	config := pool.Config().Copy()
	app := "key-prefix-" + kernel.NewULID().String()
	config.ConnConfig.RuntimeParams["application_name"] = app
	other, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	type result struct {
		status queue.KeyStatus
		err    error
	}
	done := make(chan result, 1)
	started := false
	err = tx.Do(ctx, pool, tx.Options{}, func(parentCtx context.Context) error {
		if err := q.HoldTx(parentCtx, parent); err != nil {
			return err
		}
		started = true
		go func() {
			var status queue.KeyStatus
			//nolint:contextcheck // joining the parent's context would avoid the row-lock contention
			err := tx.Do(ctx, other, tx.Options{}, func(inner context.Context) error {
				var err error
				status, err = queue.New(other).HoldByKeyPrefixTx(inner, account, []queue.Kind{work, refresh}, prefix)
				return err
			})
			done <- result{status: status, err: err}
		}()
		if err := awaitKeyPrefixLock(ctx, pool, app); err != nil {
			return err
		}
		if _, err := q.EnqueueTx(parentCtx, refresh, nil, queue.Account(account), queue.UniqueKey(prefix+"child")); err != nil {
			return err
		}
		return q.CompleteTx(parentCtx, parent)
	})
	if err != nil {
		cancel()
		if started {
			<-done
		}
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || got.status != (queue.KeyStatus{Pending: 1, Succeeded: 1}) {
			t.Fatal("locking snapshot missed the new child", got.status, got.err)
		}
	case <-ctx.Done():
		cancel()
		<-done
		t.Fatal("prefix lock did not finish", context.Cause(ctx))
	}
}

func awaitKeyPrefixLock(ctx context.Context, pool *pgxpool.Pool, app string) error {
	for ctx.Err() == nil {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
			WHERE datname=current_database() AND application_name=$1 AND wait_event_type='Lock')`, app).Scan(&waiting); err != nil {
			return err
		}
		if waiting {
			return nil
		}
	}
	return context.Cause(ctx)
}
