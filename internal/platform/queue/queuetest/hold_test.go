package queuetest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

func TestAccHoldTx(t *testing.T) {
	q, pool := open(t)
	enqueue(t, q, work, nil)
	j := claimOne(t, q)
	other, err := pgxpool.NewWithConfig(t.Context(), pool.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	otherQueue := queue.New(other)
	err = tx.Do(t.Context(), pool, tx.Options{}, func(ctx context.Context) error {
		if err := q.HoldTx(ctx, j); err != nil {
			return err
		}
		//nolint:contextcheck // sharing ctx would join the first pool's transaction instead of contending on its row
		blocked := tx.Do(
			t.Context(),
			other,
			tx.Options{},
			func(inner context.Context) error {
				// Start without the outer transaction's context: the second pool
				// must contend on the actual row, not silently join the first.
				if _, err := tx.Q(inner, other).Exec(inner, `SET LOCAL lock_timeout='50ms'`); err != nil {
					return err
				}
				return otherQueue.HoldTx(inner, j)
			},
		)
		if blocked == nil || errors.Is(blocked, queue.ErrLeaseLost) {
			return fmt.Errorf("hold did not lock live generation: %w", blocked)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Complete(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	err = tx.Do(t.Context(), pool, tx.Options{}, func(ctx context.Context) error { return q.HoldTx(ctx, j) })
	if !errors.Is(err, queue.ErrLeaseLost) {
		t.Fatal("settled claim retained fence", err)
	}
}

func TestAccDeadKeyFence(t *testing.T) {
	q, pool := open(t)
	enqueue(t, q, work, nil, queue.UniqueKey("delivery"), queue.MaxAttempts(1))
	j := claimOne(t, q)
	if dead, err := q.Fail(t.Context(), j, errors.New("safe failure")); err != nil || !dead {
		t.Fatal(dead, err)
	}
	other, err := pgxpool.NewWithConfig(t.Context(), pool.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	otherQueue := queue.New(other)
	err = tx.Do(t.Context(), pool, tx.Options{}, func(ctx context.Context) error {
		dead, err := q.DeadByKeyTx(ctx, work, "delivery", kernel.ULID{})
		if err != nil || !dead {
			return fmt.Errorf("dead lookup=%t: %w", dead, err)
		}
		for _, retry := range []bool{false, true} {
			//nolint:contextcheck // this independent transaction must contend with the first pool's lock
			blocked := tx.Do(
				t.Context(),
				other,
				tx.Options{},
				func(inner context.Context) error {
					if _, err := tx.Q(inner, other).Exec(inner, `SET LOCAL lock_timeout='50ms'`); err != nil {
						return err
					}
					if retry {
						return otherQueue.Retry(inner, j.ID)
					}
					_, err := otherQueue.DeadByKeyTx(inner, work, "delivery", kernel.ULID{})
					return err
				},
			)
			if blocked == nil {
				return errors.New("dead job was not locked against lookup/retry")
			}
		}
		dead, err = q.DeadByKeyTx(ctx, work, "delivery", kernel.NewULID())
		if err != nil || dead {
			return fmt.Errorf("dead lookup ignored account: %t: %w", dead, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := otherQueue.Retry(t.Context(), j.ID); err != nil {
		t.Fatal(err)
	}
	err = tx.Do(t.Context(), pool, tx.Options{}, func(ctx context.Context) error {
		dead, err := q.DeadByKeyTx(ctx, work, "delivery", kernel.ULID{})
		if err != nil || dead {
			return fmt.Errorf("retried job was treated as dead: %t: %w", dead, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fresh := claimOne(t, q)
	if dead, err := q.Fail(t.Context(), fresh, errors.New("failed again")); err != nil || !dead {
		t.Fatal(dead, err)
	}
	if err := q.Discard(t.Context(), fresh.ID); err != nil {
		t.Fatal(err)
	}
	err = tx.Do(t.Context(), pool, tx.Options{}, func(ctx context.Context) error {
		dead, err := q.DeadByKeyTx(ctx, work, "delivery", kernel.ULID{})
		if err != nil || !dead {
			return fmt.Errorf("discarded dead work was not terminal: %t: %w", dead, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
