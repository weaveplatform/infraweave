package cron

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// Task is a periodic task: the kind it enqueues, how often, and the
// handler that runs it. The platform and each domain list theirs, and the
// scheduler registers them all.
type Task struct {
	Kind    queue.Kind
	Every   time.Duration
	Handler queue.Handler
}

// ErrTask reports a periodic task that cannot be registered.
var ErrTask = errors.New("invalid periodic task")

// PeriodicTasks is the Source of infraweave's own recurring work: rows of
// periodic_tasks, each the name of a job kind and how often it runs.
type PeriodicTasks struct {
	db    tx.Querier
	queue *queue.Queue

	mu    sync.Mutex
	kinds map[string]queue.Kind
}

// NewPeriodicTasks keeps periodic tasks in db and enqueues them on q.
func NewPeriodicTasks(db tx.Querier, q *queue.Queue) *PeriodicTasks {
	return &PeriodicTasks{db: db, queue: q, kinds: map[string]queue.Kind{}}
}

// Name is the table's name.
func (*PeriodicTasks) Name() string { return "periodic_tasks" }

// ensure adds a task due at once, or sets a known task's interval. A
// shorter interval brings the next run forward to at most one interval
// away; otherwise the next run stays, so restarts never delay a task.
const ensure = `
INSERT INTO periodic_tasks AS p (name, interval, next_run_at)
VALUES ($1, $2 * interval '1 microsecond', now())
ON CONFLICT (name) DO UPDATE SET
    interval = excluded.interval,
    next_run_at = least(p.next_run_at, now() + excluded.interval)`

// Ensure registers kind k to run every interval, and records it in the
// table at startup. Its name is the task's, and it must use policy Ignore,
// so a task still pending or running when it next falls due is not queued
// again. Only the tasks a replica registered fire on it, so a task added by
// a newer release waits for a replica that knows its kind.
func (p *PeriodicTasks) Ensure(ctx context.Context, k queue.Kind, every time.Duration) error {
	if k.Policy != queue.Ignore || every < time.Microsecond {
		return fmt.Errorf("%w: %s every %s: want policy Ignore and a positive interval",
			ErrTask, k.Name, every)
	}
	if _, err := tx.Q(ctx, p.db).Exec(ctx, ensure, k.Name, every.Microseconds()); err != nil {
		return fmt.Errorf("ensure periodic task %s: %w", k.Name, sql.Unavailable(err))
	}
	p.mu.Lock()
	p.kinds[k.Name] = k
	p.mu.Unlock()
	return nil
}

// fire moves each due task's next run past now by whole intervals, so a
// task that missed several runs fires once.
const fire = `
WITH due AS (
    SELECT d.name, d.interval, d.next_run_at
    FROM periodic_tasks d
    WHERE d.next_run_at <= now() AND d.name = ANY($1)
    ORDER BY d.next_run_at
    LIMIT $2
    FOR UPDATE SKIP LOCKED)
UPDATE periodic_tasks p
SET next_run_at = due.next_run_at + due.interval *
    (floor(extract(epoch FROM now() - due.next_run_at) / extract(epoch FROM due.interval)) + 1)
FROM due
WHERE p.name = due.name
RETURNING p.name`

// Fire enqueues each due registered task as a job of its kind with the
// task's name as unique key.
func (p *PeriodicTasks) Fire(ctx context.Context, limit int) (int, error) {
	p.mu.Lock()
	kinds := maps.Clone(p.kinds)
	p.mu.Unlock()
	if len(kinds) == 0 {
		return 0, nil
	}
	// A failed Query returns rows that carry its error to CollectRows.
	rows, _ := tx.Q(ctx, p.db).Query(ctx, fire, slices.Collect(maps.Keys(kinds)), limit)
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, fmt.Errorf("claim periodic tasks: %w", sql.Unavailable(err))
	}
	for _, n := range names {
		if _, err := p.queue.EnqueueTx(ctx, kinds[n], struct{}{}, queue.UniqueKey(n)); err != nil {
			return 0, fmt.Errorf("fire periodic task %s: %w", n, err)
		}
	}
	return len(names), nil
}
