package tx_test

import (
	"context"
	"errors"
	"os"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

func TestMain(m *testing.M) { os.Exit(sqltest.Main(m)) }

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

// A fenced external effect cannot be replayed inside Do after a database
// conflict, including one raised by a deferred trigger during COMMIT.
func TestAccTxNoRetry(t *testing.T) {
	for _, code := range []string{"40001", "40P01"} {
		for _, stage := range []string{"callback", "commit"} {
			t.Run(code+" at "+stage, func(t *testing.T) {
				pool := setup(t)
				if stage == "commit" {
					if _, err := pool.Exec(t.Context(), `CREATE FUNCTION fail_commit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'injected at commit' USING ERRCODE = '`+code+`'; END$$;
CREATE CONSTRAINT TRIGGER fail_commit AFTER INSERT ON t DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION fail_commit()`); err != nil {
						t.Fatal(err)
					}
				}
				effects, after := 0, 0
				err := tx.Do(t.Context(), pool, tx.Options{NoRetry: true}, func(ctx context.Context) error {
					q := tx.Q(ctx, pool)
					if _, err := q.Exec(ctx, "INSERT INTO t VALUES (1)"); err != nil {
						return err
					}
					effects++
					tx.AfterCommit(ctx, func(context.Context) { after++ })
					if stage == "callback" {
						return raise(ctx, q, code)
					}
					return nil
				})
				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) || pgErr.Code != code || effects != 1 || after != 0 {
					t.Fatalf("error=%v effects=%d after=%d; want %s and one effect without after-commit", err, effects, after, code)
				}
				if got := rows(t, pool); len(got) != 0 {
					t.Fatalf("failed transaction kept rows: %v", got)
				}
				if acquired := pool.Stat().AcquiredConns(); acquired != 0 {
					t.Fatalf("failed transaction retained %d acquired connections", acquired)
				}
			})
		}
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
		if !tx.In(ctx) {
			t.Error("In misses the transaction Do began")
		}
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

type capturedBeginner struct {
	tx.Beginner
	transaction pgx.Tx
}

func (b *capturedBeginner) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	transaction, err := b.Beginner.BeginTx(ctx, opts)
	b.transaction = transaction
	return transaction, err
}

func TestAccTxGoexit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		nested    bool
		cancelled bool
	}{
		{name: "direct"},
		{name: "nested", nested: true},
		{name: "cancelled", cancelled: true},
	} {
		t.Run(tc.name, func(t *testing.T) { testGoexit(t, tc.nested, tc.cancelled) })
	}
}

func testGoexit(t *testing.T, nested, cancelled bool) {
	t.Helper()
	dsn := sqltest.DSN(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "CREATE TABLE t (k int PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	db := &capturedBeginner{Beginner: pool}
	done := make(chan struct{})
	var callbackErr error
	returned, after := false, false
	go func() {
		defer close(done)
		abort := func(ctx context.Context) error {
			if _, err := tx.Q(ctx, pool).Exec(ctx, "INSERT INTO t VALUES (1)"); err != nil {
				return err
			}
			tx.AfterCommit(ctx, func(context.Context) { after = true })
			if cancelled {
				cancel()
			}
			runtime.Goexit()
			return nil
		}
		callbackErr = tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
			if nested {
				return tx.Do(ctx, db, tx.Options{Serializable: true, NoRetry: true}, abort)
			}
			return abort(ctx)
		})
		returned = true
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("transaction goroutine did not exit before its deadline")
	}
	// A broken implementation must still release its real lease before pool
	// cleanup, so the regression reports the leak instead of hanging teardown.
	defer func() {
		if db.transaction != nil {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer stop()
			if err := db.transaction.Rollback(cleanup); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
				t.Errorf("baseline transaction cleanup: %v", err)
			}
		}
	}()
	if callbackErr != nil || returned || after {
		t.Fatalf("Goexit returned=%v error=%v after-commit=%v", returned, callbackErr, after)
	}
	if acquired := pool.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("Goexit retained %d acquired PostgreSQL connections, want zero", acquired)
	}
	if got := rows(t, pool); len(got) != 0 {
		t.Fatalf("Goexit kept uncommitted rows: %v", got)
	}
	if err := tx.Do(t.Context(), pool, tx.Options{}, func(ctx context.Context) error {
		_, err := tx.Q(ctx, pool).Exec(ctx, "INSERT INTO t VALUES (2)")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := rows(t, pool); len(got) != 1 || got[0] != 2 {
		t.Fatalf("subsequent transaction rows=%v, want [2]", got)
	}
}

func TestAccTxCleanupOutcomes(t *testing.T) {
	for _, outcome := range []string{"commit", "callback error", "panic", "cancelled commit"} {
		t.Run(outcome, func(t *testing.T) {
			pool := setup(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			marker := errors.New("original callback failure")
			panicMarker := &struct{ message string }{message: "original panic"}
			after := 0
			var err error
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				err = tx.Do(ctx, pool, tx.Options{}, func(ctx context.Context) error {
					if _, err := tx.Q(ctx, pool).Exec(ctx, "INSERT INTO t VALUES (1)"); err != nil {
						return err
					}
					tx.AfterCommit(ctx, func(context.Context) { after++ })
					switch outcome {
					case "callback error":
						return marker
					case "panic":
						panic(panicMarker)
					case "cancelled commit":
						cancel()
					}
					return nil
				})
			}()
			wantRows, wantAfter := 0, 0
			switch outcome {
			case "commit":
				wantRows, wantAfter = 1, 1
				if err != nil {
					t.Errorf("commit: %v", err)
				}
			case "callback error":
				if !errors.Is(err, marker) {
					t.Errorf("callback error=%v, want original marker", err)
				}
			case "panic":
				if recovered != panicMarker {
					t.Errorf("panic=%v, want original marker", recovered)
				}
			case "cancelled commit":
				if !errors.Is(err, context.Canceled) {
					t.Errorf("cancelled commit=%v, want cancellation", err)
				}
			}
			if outcome != "panic" && recovered != nil {
				t.Errorf("unexpected panic: %v", recovered)
			}
			assertReleased(t, pool)
			if got := rows(t, pool); len(got) != wantRows || after != wantAfter {
				t.Errorf("%s rows=%v after=%d, want %d rows and %d hooks", outcome, got, after, wantRows, wantAfter)
			}
		})
	}
}

func assertReleased(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	// A cancelled COMMIT can destroy its connection. pgxpool's asynchronous
	// destruction must finish before its acquired-connection count falls.
	for pool.Stat().AcquiredConns() != 0 {
		select {
		case <-ctx.Done():
			t.Fatalf("transaction cleanup retained %d acquired connections", pool.Stat().AcquiredConns())
		case <-tick.C:
		}
	}
}

type failingBeginner struct{ err error }

func (f failingBeginner) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return nil, f.err
}

type noQuerier struct{ tx.Querier }

type observedTx struct {
	pgx.Tx
	commitError        error
	rollbackContextErr error
	commits, rollbacks int
}

func (o *observedTx) Commit(context.Context) error {
	o.commits++
	return o.commitError
}

func (o *observedTx) Rollback(ctx context.Context) error {
	o.rollbacks++
	o.rollbackContextErr = ctx.Err()
	return nil
}

type observedBeginner struct {
	transactions []*observedTx
	commitError  error
}

func (o *observedBeginner) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	t := &observedTx{commitError: o.commitError}
	o.transactions = append(o.transactions, t)
	return t, nil
}

func TestUnitTxNoRetry(t *testing.T) {
	for _, stage := range []string{"callback", "commit", "success"} {
		t.Run(stage, func(t *testing.T) {
			conflict := &pgconn.PgError{Code: "40001"}
			db := &observedBeginner{}
			if stage == "commit" {
				db.commitError = conflict
			}
			calls, after := 0, 0
			err := tx.Do(t.Context(), db, tx.Options{NoRetry: true}, func(ctx context.Context) error {
				calls++
				if !tx.In(ctx) || tx.Q(ctx, nil) != db.transactions[0] {
					t.Fatal("callback lost its transaction fence")
				}
				tx.AfterCommit(ctx, func(context.Context) { after++ })
				if stage == "callback" {
					return conflict
				}
				return nil
			})
			if calls != 1 || len(db.transactions) != 1 {
				t.Fatalf("callback calls=%d transactions=%d; want one each", calls, len(db.transactions))
			}
			if stage == "success" {
				if err != nil || after != 1 || db.transactions[0].commits != 1 {
					t.Fatalf("successful transaction: error=%v after=%d commits=%d", err, after, db.transactions[0].commits)
				}
			} else if !errors.Is(err, conflict) || after != 0 {
				t.Fatalf("failed transaction: error=%v after=%d", err, after)
			}
			if db.transactions[0].rollbacks != 1 {
				t.Fatalf("%s cleanup rolled back %d times", stage, db.transactions[0].rollbacks)
			}
		})
	}
}

func TestUnitTxGoexit(t *testing.T) {
	db := &observedBeginner{}
	done := make(chan struct{})
	returned, after := false, false
	go func() {
		defer close(done)
		_ = tx.Do(t.Context(), db, tx.Options{}, func(ctx context.Context) error {
			tx.AfterCommit(ctx, func(context.Context) { after = true })
			runtime.Goexit()
			return nil
		})
		returned = true
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("transaction goroutine did not exit")
	}
	if len(db.transactions) != 1 || returned || after {
		t.Fatalf("Goexit transactions=%d returned=%v after-commit=%v", len(db.transactions), returned, after)
	}
	if got := db.transactions[0]; got.commits != 0 || got.rollbacks != 1 {
		t.Fatalf("Goexit commits=%d rollbacks=%d, want zero and one", got.commits, got.rollbacks)
	}
}

func TestUnitTxCancelledCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	db := &observedBeginner{}
	err := tx.Do(ctx, db, tx.Options{}, func(context.Context) error {
		cancel()
		return context.Canceled
	})
	if !errors.Is(err, context.Canceled) || len(db.transactions) != 1 {
		t.Fatalf("cancelled transaction: error=%v transactions=%d", err, len(db.transactions))
	}
	got := db.transactions[0]
	if got.commits != 0 || got.rollbacks != 1 || got.rollbackContextErr != nil {
		t.Fatalf("cancelled cleanup: commits=%d rollbacks=%d context=%v", got.commits, got.rollbacks, got.rollbackContextErr)
	}
}

func TestUnitTxCommitRetriesCleanEveryAttempt(t *testing.T) {
	tx.SetBackoff(t, time.Millisecond)
	conflict := &pgconn.PgError{Code: "40001"}
	db := &observedBeginner{commitError: conflict}
	after := 0
	err := tx.Do(t.Context(), db, tx.Options{}, func(ctx context.Context) error {
		tx.AfterCommit(ctx, func(context.Context) { after++ })
		return nil
	})
	if !errors.Is(err, conflict) || after != 0 || len(db.transactions) != tx.MaxRetries+1 {
		t.Fatalf("commit retries: error=%v after=%d attempts=%d", err, after, len(db.transactions))
	}
	for i, got := range db.transactions {
		if got.commits != 1 || got.rollbacks != 1 {
			t.Errorf("attempt %d: commits=%d rollbacks=%d", i, got.commits, got.rollbacks)
		}
	}
}

func TestUnitTxOutsideATransaction(t *testing.T) {
	if tx.In(t.Context()) {
		t.Error("In reports a transaction on a bare context")
	}
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

// TestAccTxDetached checks that a Do under a detached context commits on
// its own, so its row survives the caller's rollback, while the context
// keeps its other values.
func TestAccTxDetached(t *testing.T) {
	pool := setup(t)
	type key struct{}
	refused := errors.New("refused")
	err := tx.Do(t.Context(), pool, tx.Options{}, func(ctx context.Context) error {
		if _, err := tx.Q(ctx, pool).Exec(ctx, `INSERT INTO t (k) VALUES (1)`); err != nil {
			return err
		}
		inner := tx.Detached(context.WithValue(ctx, key{}, "kept"))
		if tx.In(inner) || inner.Value(key{}) != "kept" {
			t.Errorf("detached: in a transaction %v, value %v", tx.In(inner), inner.Value(key{}))
		}
		if err := tx.Do(inner, pool, tx.Options{}, func(ctx context.Context) error {
			_, err := tx.Q(ctx, pool).Exec(ctx, `INSERT INTO t (k) VALUES (2)`)
			return err
		}); err != nil {
			return err
		}
		return refused
	})
	if !errors.Is(err, refused) {
		t.Fatal(err)
	}
	if got := rows(t, pool); !slices.Equal(got, []int{2}) {
		t.Errorf("rows = %v, want the detached insert alone", got)
	}
}
