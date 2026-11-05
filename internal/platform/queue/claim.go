package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// ErrLeaseLost reports a transition of a job that is no longer the
// caller's: its lease lapsed and it was recovered or claimed again. The
// caller's result is discarded.
var ErrLeaseLost = errors.New("job lease lost")

// MaxLastError is the most of an error's text a job keeps.
const MaxLastError = 4 << 10

// Job is a claimed job. ID and Generation fence every transition, so only
// the claim that produced this value can complete, fail or extend it.
type Job struct {
	ID          int64
	Kind        string
	Queue       Name
	Payload     json.RawMessage
	Account     kernel.ULID
	Attempt     int
	MaxAttempts int
	Generation  int64

	// done is set once CompleteTx's transaction commits, so the worker
	// records nothing more; it is shared by every copy of the Job.
	done *atomic.Bool
}

func (j Job) completed() bool { return j.done != nil && j.done.Load() }

// claim takes up to $4 due pending jobs of queue $1 whose kinds are in $2,
// without waiting on rows another claim holds. A job with an ordering key
// waits while another job with its key runs or an older one is pending, so
// jobs sharing a key run one at a time in insertion order.
const claim = `
WITH due AS (
    SELECT j.id FROM jobs j
    WHERE j.state = 'pending' AND j.queue = $1 AND j.kind = ANY($2) AND j.run_at <= now()
      AND (j.ordering_key IS NULL OR NOT EXISTS (
          SELECT 1 FROM jobs o
          WHERE o.ordering_key = j.ordering_key AND o.id <> j.id
            AND (o.state = 'running' OR (o.state = 'pending' AND o.id < j.id))))
    ORDER BY j.priority, j.run_at, j.id
    LIMIT $4
    FOR UPDATE SKIP LOCKED)
UPDATE jobs j SET state = 'running', attempt = j.attempt + 1, generation = j.generation + 1,
    lease_until = now() + $5 * interval '1 microsecond', lease_owner = $3
FROM due WHERE j.id = due.id
RETURNING j.id, j.kind, j.queue, j.payload, j.account_id, j.attempt, j.max_attempts, j.generation`

// Claim takes up to n due jobs of queue whose kind is one of kinds, for a
// lease of the given length, and names owner on them for diagnosis. Jobs
// come in priority, run time and insertion order.
func (q *Queue) Claim(
	ctx context.Context,
	queue Name,
	kinds []string,
	n int,
	lease time.Duration,
	owner string,
) ([]Job, error) {
	rows, err := tx.Q(ctx, q.q).Query(ctx, claim, queue, kinds, owner, n, lease.Microseconds())
	if err != nil {
		return nil, fmt.Errorf("claim from %s: %w", queue, sql.Unavailable(err))
	}
	jobs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Job, error) {
		j := Job{done: new(atomic.Bool)}
		err := r.Scan(&j.ID, &j.Kind, &j.Queue, &j.Payload, &j.Account, &j.Attempt, &j.MaxAttempts,
			&j.Generation)
		return j, err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return nil, fmt.Errorf("claim from %s: %w", queue, sql.Unavailable(err))
	}
	return jobs, nil
}

// released ends a transition that takes j out of running.
const released = ", lease_until = NULL, lease_owner = NULL"

// transition runs set on job j only while j's claim still holds it; set's
// own arguments start at $3.
func (q *Queue) transition(ctx context.Context, op string, j Job, set string, args ...any) error {
	stmt := "UPDATE jobs SET " + set + " WHERE id = $1 AND generation = $2 AND state = 'running'"
	tag, err := tx.Q(ctx, q.q).Exec(ctx, stmt, append([]any{j.ID, j.Generation}, args...)...)
	if err != nil {
		return fmt.Errorf("%s job %d: %w", op, j.ID, sql.Unavailable(err))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%s job %d: %w", op, j.ID, ErrLeaseLost)
	}
	return nil
}

// Extend renews j's lease to the given length from now.
func (q *Queue) Extend(ctx context.Context, j Job, lease time.Duration) error {
	return q.transition(ctx, "extend", j,
		"lease_until = now() + $3 * interval '1 microsecond'", lease.Microseconds())
}

// complete marks a job succeeded, or pending again from attempt zero when a
// duplicate arrived while it ran.
const complete = `state = CASE WHEN rerun THEN 'pending' ELSE 'succeeded' END,
    attempt = CASE WHEN rerun THEN 0 ELSE attempt END,
    run_at = CASE WHEN rerun THEN now() ELSE run_at END,
    finished_at = CASE WHEN rerun THEN NULL ELSE now() END,
    rerun = false` + released

// Complete records that j succeeded.
func (q *Queue) Complete(ctx context.Context, j Job) error {
	return q.transition(ctx, "complete", j, complete)
}

// CompleteTx records that j succeeded inside the transaction on ctx, the
// one that holds the handler's writes, so they commit exactly when the job
// does: a lost lease returns ErrLeaseLost, which rolls them back. Without a
// transaction it returns ErrNoTransaction.
func (q *Queue) CompleteTx(ctx context.Context, j Job) error {
	if !tx.In(ctx) {
		return ErrNoTransaction
	}
	if err := q.Complete(ctx, j); err != nil {
		return err
	}
	tx.AfterCommit(ctx, func(context.Context) {
		if j.done != nil {
			j.done.Store(true)
		}
	})
	return nil
}

// Fail records j's failed attempt: the job is retried after Backoff, or is
// dead once it has made MaxAttempts attempts. It reports whether j is dead.
func (q *Queue) Fail(ctx context.Context, j Job, cause error) (dead bool, err error) {
	return q.FailAfter(ctx, j, cause, Backoff(j.Attempt))
}

// FailAfter is Fail with the retry delay a kind chose.
func (q *Queue) FailAfter(
	ctx context.Context,
	j Job,
	cause error,
	delay time.Duration,
) (dead bool, err error) {
	dead = j.Attempt >= j.MaxAttempts
	if dead {
		return true, q.Kill(ctx, j, cause)
	}
	return false, q.transition(
		ctx,
		"fail",
		j,
		"state = 'pending', run_at = now() + $3 * interval '1 microsecond', last_error = $4"+released,
		delay.Microseconds(),
		lastError(cause),
	)
}

// Kill makes j dead at once, for a failure no retry can mend.
func (q *Queue) Kill(ctx context.Context, j Job, cause error) error {
	return q.transition(ctx, "kill", j,
		"state = 'dead', finished_at = now(), last_error = $3"+released, lastError(cause))
}

// Snooze returns j to pending for d without counting the attempt.
func (q *Queue) Snooze(ctx context.Context, j Job, d time.Duration) error {
	return q.transition(ctx, "snooze", j,
		"state = 'pending', attempt = attempt - 1, run_at = now() + $3 * interval '1 microsecond'"+
			released,
		d.Microseconds())
}

// Release hands j back as pending at once without counting the attempt,
// for a replica that is shutting down.
func (q *Queue) Release(ctx context.Context, j Job) error {
	return q.transition(ctx, "release", j,
		"state = 'pending', attempt = attempt - 1, run_at = now()"+released)
}

// Recover returns every running job whose lease has lapsed to pending, the
// lapsed attempt counted, or makes it dead when that was its last. Every
// replica's poll runs it, so a crashed replica's work is taken over within
// one lease. It returns how many jobs it recovered.
func (q *Queue) Recover(ctx context.Context) (int64, error) {
	tag, err := tx.Q(ctx, q.q).Exec(ctx, `
UPDATE jobs SET
    state = CASE WHEN attempt >= max_attempts THEN 'dead' ELSE 'pending' END,
    finished_at = CASE WHEN attempt >= max_attempts THEN now() END,
    run_at = now(), last_error = 'lease lapsed', lease_until = NULL, lease_owner = NULL
WHERE state = 'running' AND lease_until < now()`)
	if err != nil {
		return 0, fmt.Errorf("recover lapsed jobs: %w", sql.Unavailable(err))
	}
	return tag.RowsAffected(), nil
}

// Backoff is the delay before the attempt after attempt: a random duration
// in [1 s, min(15 min, 2^(attempt-1) s)], so retries of many jobs that
// failed together spread out.
func Backoff(attempt int) time.Duration {
	ceiling := 15 * time.Minute
	if attempt < 11 { // 2^10 s already passes 15 minutes
		ceiling = min(ceiling, time.Second<<max(attempt-1, 0))
	}
	if ceiling <= time.Second {
		return time.Second
	}
	return time.Second + rand.N(ceiling-time.Second+1) //nolint:gosec // jitter, not a secret
}

func lastError(err error) string {
	s := "unknown error"
	if err != nil {
		s = err.Error()
	}
	if len(s) > MaxLastError {
		// A cut inside a multi-byte character would be text PostgreSQL
		// refuses, so the partial character goes too.
		s = strings.ToValidUTF8(s[:MaxLastError], "")
	}
	return s
}

func noRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
