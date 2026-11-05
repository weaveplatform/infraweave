package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// Errors of the dead-job operations.
var (
	// ErrNotDead reports a job that does not exist or is not dead.
	ErrNotDead = errors.New("no dead job with that ID")
	// ErrDuplicate reports a retry of a job whose kind and unique key a
	// pending or running job already holds.
	ErrDuplicate = errors.New("a job with the same kind and unique key is pending or running")
)

// Prune deletes succeeded and cancelled jobs that finished more than
// completed ago, and dead jobs that died more than dead ago. It returns
// how many it deleted.
func (q *Queue) Prune(ctx context.Context, completed, dead time.Duration) (int64, error) {
	tag, err := tx.Q(ctx, q.q).Exec(ctx, `
DELETE FROM jobs
WHERE (state IN ('succeeded', 'cancelled') AND finished_at < now() - $1 * interval '1 microsecond')
   OR (state = 'dead' AND finished_at < now() - $2 * interval '1 microsecond')`,
		completed.Microseconds(), dead.Microseconds())
	if err != nil {
		return 0, fmt.Errorf("prune jobs: %w", sql.Unavailable(err))
	}
	return tag.RowsAffected(), nil
}

// Dead is a dead job as the operator lists it.
type Dead struct {
	ID         int64
	Queue      Name
	Kind       string
	Payload    json.RawMessage
	Account    kernel.ULID
	UniqueKey  string
	Attempt    int
	LastError  string
	CreatedAt  time.Time
	FinishedAt time.Time
}

// ListDead lists up to limit dead jobs with IDs above after, oldest first;
// the last ID listed is the next page's after.
func (q *Queue) ListDead(ctx context.Context, after int64, limit int) ([]Dead, error) {
	rows, err := tx.Q(ctx, q.q).Query(ctx, `
SELECT id, queue, kind, payload, account_id, coalesce(unique_key, ''), attempt,
    coalesce(last_error, ''), created_at, finished_at
FROM jobs WHERE state = 'dead' AND id > $1 ORDER BY id LIMIT $2`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list dead jobs: %w", sql.Unavailable(err))
	}
	dead, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Dead, error) {
		var d Dead
		err := r.Scan(&d.ID, &d.Queue, &d.Kind, &d.Payload, &d.Account, &d.UniqueKey, &d.Attempt,
			&d.LastError, &d.CreatedAt, &d.FinishedAt)
		return d, err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return nil, fmt.Errorf("list dead jobs: %w", sql.Unavailable(err))
	}
	return dead, nil
}

// Retry makes dead job id pending again with its attempts reset, and
// wakes its queue's workers in the same statement.
func (q *Queue) Retry(ctx context.Context, id int64) error {
	var discard string
	err := tx.Q(ctx, q.q).QueryRow(ctx, `
WITH r AS (
    UPDATE jobs SET state = 'pending', attempt = 0, run_at = now(), finished_at = NULL
    WHERE id = $1 AND state = 'dead' RETURNING queue)
SELECT pg_notify($2, queue)::text FROM r`, id, notify.Jobs).Scan(&discard)
	switch {
	case noRows(err):
		return fmt.Errorf("retry job %d: %w", id, ErrNotDead)
	case uniqueViolation(err):
		return fmt.Errorf("retry job %d: %w", id, ErrDuplicate)
	case err != nil:
		return fmt.Errorf("retry job %d: %w", id, sql.Unavailable(err))
	}
	return nil
}

// Discard cancels dead job id, which pruning then deletes.
func (q *Queue) Discard(ctx context.Context, id int64) error {
	tag, err := tx.Q(ctx, q.q).Exec(ctx, `
UPDATE jobs SET state = 'cancelled', finished_at = now() WHERE id = $1 AND state = 'dead'`, id)
	if err != nil {
		return fmt.Errorf("discard job %d: %w", id, sql.Unavailable(err))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("discard job %d: %w", id, ErrNotDead)
	}
	return nil
}

// Stat is one logical queue's depth.
type Stat struct {
	Queue Name
	// Visible jobs are pending and due: ready to be claimed.
	Visible int
	// InFlight jobs are claimed and running.
	InFlight int
}

// Stats reports every logical queue's depth, in Queues order, an empty
// queue included.
func (q *Queue) Stats(ctx context.Context) ([]Stat, error) {
	rows, err := tx.Q(ctx, q.q).Query(ctx, `
SELECT queue,
    count(*) FILTER (WHERE state = 'pending' AND run_at <= now()),
    count(*) FILTER (WHERE state = 'running')
FROM jobs WHERE state IN ('pending', 'running') GROUP BY queue`)
	if err != nil {
		return nil, fmt.Errorf("queue stats: %w", sql.Unavailable(err))
	}
	counts := map[Name]Stat{}
	var s Stat
	if _, err := pgx.ForEachRow(rows, []any{&s.Queue, &s.Visible, &s.InFlight}, func() error {
		counts[s.Queue] = s
		return nil
	}); err != nil {
		return nil, fmt.Errorf("queue stats: %w", sql.Unavailable(err))
	}
	out := make([]Stat, 0, len(Queues))
	for _, n := range Queues {
		c := counts[n]
		c.Queue = n
		out = append(out, c)
	}
	return out, nil
}

func uniqueViolation(err error) bool {
	pe, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pe.Code == "23505"
}
