package tx_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/test/acceptance"
)

func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, acceptance.ImagePostgres)) }

// raise makes the statement fail with SQLSTATE code.
func raise(ctx context.Context, q tx.Querier, code string) error {
	_, err := q.Exec(ctx, "DO $$BEGIN RAISE EXCEPTION 'injected' USING ERRCODE = '"+code+"'; END$$")
	return err
}

func setup(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := sqltest.Open(t).Primary()
	if _, err := pool.Exec(t.Context(), "CREATE TABLE t (k int PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	return pool
}

func rows(t *testing.T, pool *pgxpool.Pool) []int {
	t.Helper()
	r, err := pool.Query(t.Context(), "SELECT k FROM t ORDER BY k")
	if err != nil {
		t.Fatal(err)
	}
	ks, err := pgx.CollectRows(r, pgx.RowTo[int])
	if err != nil {
		t.Fatal(err)
	}
	return ks
}

func TestAccTxRetry(t *testing.T) {
	tx.SetBackoff(t, time.Millisecond)
	for _, tc := range []struct {
		name     string
		code     string
		failures int
		attempts int
		commits  bool
	}{
		{"serialization failure twice", "40001", 2, 3, true},
		{"deadlock once", "40P01", 1, 2, true},
		{"serialization failure on every retry", "40001", 4, 4, false},
		{"another error", "23505", 1, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := setup(t)
			attempts, after := 0, 0
			err := tx.Do(t.Context(), pool, tx.Options{}, func(ctx context.Context) error {
				attempts++
				q := tx.Q(ctx, pool)
				if _, err := q.Exec(ctx, "INSERT INTO t VALUES (1)"); err != nil {
					return err
				}
				tx.AfterCommit(ctx, func(context.Context) { after++ })
				if attempts <= tc.failures {
					return raise(ctx, q, tc.code)
				}
				return nil
			})
			if attempts != tc.attempts {
				t.Errorf("attempts = %d, want %d", attempts, tc.attempts)
			}
			var pgErr *pgconn.PgError
			switch {
			case tc.commits && err != nil:
				t.Fatalf("err = %v", err)
			case !tc.commits && (!errors.As(err, &pgErr) || pgErr.Code != tc.code):
				t.Fatalf("err = %v, want SQLSTATE %s", err, tc.code)
			}
			wantRows, wantAfter := 0, 0
			if tc.commits {
				wantRows, wantAfter = 1, 1
			}
			if got := len(rows(t, pool)); got != wantRows {
				t.Errorf("rows = %d, want %d", got, wantRows)
			}
			if after != wantAfter {
				t.Errorf("AfterCommit ran %d times, want %d", after, wantAfter)
			}
		})
	}
}

// Two SERIALIZABLE transactions that each read the table and insert its
// count conflict; the loser retries, reads the winner's row and commits.
func TestAccTxSerializableConflict(t *testing.T) {
	tx.SetBackoff(t, time.Millisecond)
	pool := setup(t)
	var attempts atomic.Int32
	var read sync.WaitGroup
	read.Add(2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			first := true
			err := tx.Do(
				t.Context(),
				pool,
				tx.Options{Serializable: true},
				func(ctx context.Context) error {
					attempts.Add(1)
					q := tx.Q(ctx, pool)
					var n int
					if err := q.QueryRow(ctx, "SELECT count(*) FROM t").Scan(&n); err != nil {
						return err
					}
					if first {
						first = false
						read.Done()
						read.Wait()
					}
					_, err := q.Exec(ctx, "INSERT INTO t VALUES ($1)", n)
					return err
				},
			)
			if err != nil {
				t.Errorf("tx.Do: %v", err)
			}
		})
	}
	wg.Wait()
	if got := rows(t, pool); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Errorf("rows = %v, want [0 1]", got)
	}
	if attempts.Load() < 3 {
		t.Errorf("attempts = %d: no transaction was retried", attempts.Load())
	}
}

func TestAccTxNestedAndPanic(t *testing.T) {
	pool := setup(t)
	inner := errors.New("roll back")
	err := tx.Do(t.Context(), pool, tx.Options{}, func(ctx context.Context) error {
		err := tx.Do(ctx, pool, tx.Options{Serializable: true}, func(ctx context.Context) error {
			_, err := tx.Q(ctx, pool).Exec(ctx, "INSERT INTO t VALUES (1)")
			return err
		})
		if err != nil {
			return err
		}
		return inner
	})
	if !errors.Is(err, inner) {
		t.Fatalf("err = %v", err)
	}
	if got := rows(t, pool); len(got) != 0 {
		t.Errorf("the outer rollback kept the inner insert: %v", got)
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic did not reach the caller")
			}
		}()
		_ = tx.Do(t.Context(), pool, tx.Options{}, func(ctx context.Context) error {
			if _, err := tx.Q(ctx, pool).Exec(ctx, "INSERT INTO t VALUES (2)"); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	if got := rows(t, pool); len(got) != 0 {
		t.Errorf("a panic committed: %v", got)
	}
}

func TestAccTxCancelledWhileWaiting(t *testing.T) {
	tx.SetBackoff(t, time.Hour)
	pool := setup(t)
	ctx, cancel := context.WithCancel(t.Context())
	err := tx.Do(ctx, pool, tx.Options{}, func(ctx context.Context) error {
		defer cancel()
		return raise(ctx, tx.Q(ctx, pool), "40001")
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the failure and the cancellation", err)
	}
}

type failingBeginner struct{ err error }

func (f failingBeginner) BeginTx(
	context.Context,
	pgx.TxOptions,
) (pgx.Tx, error) {
	return nil, f.err
}

type noQuerier struct{ tx.Querier }

func TestUnitTxOutsideATransaction(t *testing.T) {
	down := errors.New("database down")
	ran := false
	err := tx.Do(t.Context(), failingBeginner{down}, tx.Options{}, func(context.Context) error {
		ran = true
		return nil
	})
	if !errors.Is(err, down) || ran {
		t.Errorf("Do = %v, ran = %v", err, ran)
	}

	db := noQuerier{}
	if tx.Q(t.Context(), db) != db {
		t.Error("Q without a transaction is not the given querier")
	}
	now := false
	tx.AfterCommit(t.Context(), func(context.Context) { now = true })
	if !now {
		t.Error("AfterCommit without a transaction did not run at once")
	}
}
