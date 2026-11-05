package tx

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is what a store runs SQL through: a pool, a connection or a
// transaction.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Beginner starts transactions; *pgxpool.Pool is one.
type Beginner interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// Options choose a transaction's isolation. The zero value is READ
// COMMITTED.
type Options struct {
	// Serializable runs the transaction at SERIALIZABLE.
	Serializable bool
}

// MaxRetries is how many times Do runs fn again after a serialization
// failure or a deadlock, decision 0005's three.
const MaxRetries = 3

// retryable are the SQLSTATEs after which the whole transaction runs again:
// serialization_failure and deadlock_detected. Any other error is the
// caller's.
var retryable = map[string]bool{"40001": true, "40P01": true}

// backoff is the wait before a retry: 10 to 100 ms, jittered so that two
// transactions that collided do not collide again in step.
var backoff = func() time.Duration {
	return 10*time.Millisecond + rand.N(90*time.Millisecond) //nolint:gosec // jitter, not a secret
}

type unit struct {
	tx    pgx.Tx
	after []func(context.Context)
}

type ctxKey struct{}

// Do runs fn in a transaction carried on the context it passes, so every
// store fn calls through Q joins it, and commits when fn returns nil. A
// serialization failure or deadlock, from fn or from the commit, rolls
// back and runs fn again, up to MaxRetries times, so fn must have no effect
// outside the database: work for after the commit goes to AfterCommit.
//
// Called with a context that already carries a transaction, Do runs fn in
// that transaction, whatever opts say: the outermost Do owns the commit and
// the retries.
func Do(ctx context.Context, db Beginner, opts Options, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(ctxKey{}).(*unit); ok {
		return fn(ctx)
	}
	txOpts := pgx.TxOptions{IsoLevel: pgx.ReadCommitted}
	if opts.Serializable {
		txOpts.IsoLevel = pgx.Serializable
	}
	for attempt := 0; ; attempt++ {
		u, err := once(ctx, db, txOpts, fn)
		if err == nil {
			for _, f := range u.after {
				f(ctx)
			}
			return nil
		}
		var pgErr *pgconn.PgError
		if attempt == MaxRetries || !errors.As(err, &pgErr) || !retryable[pgErr.Code] {
			return err
		}
		t := time.NewTimer(backoff())
		select {
		case <-ctx.Done():
			t.Stop()
			return errors.Join(err, context.Cause(ctx))
		case <-t.C:
		}
	}
}

// once runs one attempt. Rollback uses a context that outlives ctx's
// cancellation, so a cancelled request still ends its transaction rather
// than leaving the connection to pgx's close-on-cancel.
func once(
	ctx context.Context,
	db Beginner,
	opts pgx.TxOptions,
	fn func(context.Context) error,
) (u *unit, err error) {
	t, err := db.BeginTx(ctx, opts)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller's error, as pgx reports it
	}
	u = &unit{tx: t}
	defer func() {
		if p := recover(); p != nil {
			_ = t.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
	}()
	if err := fn(context.WithValue(ctx, ctxKey{}, u)); err != nil {
		_ = t.Rollback(context.WithoutCancel(ctx))
		return nil, err
	}
	if err := t.Commit(ctx); err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	return u, nil
}

// Q is the transaction on ctx, or db when ctx carries none.
func Q(ctx context.Context, db Querier) Querier {
	if u, ok := ctx.Value(ctxKey{}).(*unit); ok {
		return u.tx
	}
	return db
}

// In reports whether ctx carries a transaction, for a call whose guarantee
// rests on joining its caller's, such as an enqueue that must commit or
// roll back with the change that asks for it.
func In(ctx context.Context) bool {
	_, ok := ctx.Value(ctxKey{}).(*unit)
	return ok
}

// AfterCommit runs f once the transaction on ctx commits, and never when
// it rolls back; a retried attempt drops what its failed attempts
// registered. With no transaction on ctx there is nothing to wait for, and
// f runs at once.
func AfterCommit(ctx context.Context, f func(context.Context)) {
	if u, ok := ctx.Value(ctxKey{}).(*unit); ok {
		u.after = append(u.after, f)
		return
	}
	f(ctx)
}
