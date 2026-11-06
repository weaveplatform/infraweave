package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/cron"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/idempotency"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	"github.com/weaveplatform/infraweave/internal/platform/observe"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
)

// The platform's periodic tasks, from decision 0011's table. Each runs on
// the maintenance queue with policy Ignore, so a slow run never overlaps
// the next.
var (
	PruneJobs = queue.Kind{
		Name:   "queue.prune",
		Queue:  queue.Maintenance,
		Policy: queue.Ignore,
	}
	PurgeIdempotencyKeys = queue.Kind{
		Name: "idempotency.purge", Queue: queue.Maintenance, Policy: queue.Ignore,
	}
	PruneEvents = queue.Kind{Name: "events.prune", Queue: queue.Maintenance, Policy: queue.Ignore}
)

// periodic is a periodic task: its kind, its interval and its handler.
type periodic struct {
	kind    queue.Kind
	every   time.Duration
	handler queue.Handler
}

// platformTasks are the periodic tasks of the platform's own tables.
func platformTasks(
	c config.Config,
	q *queue.Queue,
	keys *idempotency.Postgres,
	events *outbox.Outbox,
	reaper *blobs.Reaper,
	log *slog.Logger,
) []periodic {
	return []periodic{
		{PruneJobs, 10 * time.Minute, func(ctx context.Context, _ queue.Job) error {
			n, err := q.Prune(ctx, c.Queue.CompletedRetention, c.Queue.DeadRetention)
			if err == nil {
				log.Debug("finished jobs pruned", "jobs", n)
			}
			return err //nolint:wrapcheck // Prune names itself
		}},
		{PurgeIdempotencyKeys, time.Hour, func(ctx context.Context, _ queue.Job) error {
			n, err := keys.Purge(ctx)
			if err == nil {
				log.Debug("expired idempotency keys purged", "keys", n)
			}
			return err //nolint:wrapcheck // Purge names itself
		}},
		{PruneEvents, 10 * time.Minute, func(ctx context.Context, _ queue.Job) error {
			n, err := events.Prune(ctx, c.Events.Retention)
			if err == nil {
				log.Debug("fanned-out events pruned", "events", n)
			}
			return err //nolint:wrapcheck // Prune names itself
		}},
		{blobs.ExpireKind, blobs.ExpireEvery, func(ctx context.Context, _ queue.Job) error {
			n, err := reaper.Expire(ctx, blobs.ExpireBatch)
			if err == nil {
				log.Debug("expired and abandoned objects marked for deletion", "objects", n)
			}
			return err //nolint:wrapcheck // Expire names itself
		}},
	}
}

// errNotListening fails the readiness of a scheduler whose listening
// connection is down: its workers wake only on the poll interval then.
var errNotListening = errors.New("the queue's listening connection is down")

// startScheduler starts the scheduler role of decision 0020: the queue's
// workers with the platform's handlers, the outbox's fan-out and
// consumption and blob.delete over store, the one full objectstore.Store
// of the process, and the ticker over periodic_tasks, after registering the
// periodic tasks in the database. Their
// wake-ups come through l, which the caller runs. A registration that fails
// fails startup. The returned stop ends the ticker, then the workers, which
// finish or hand back their jobs within queue.shutdown_grace (decision
// 0011's shutdown); the caller calls it after the HTTP drain and before the
// pool closes.
func startScheduler(
	ctx context.Context,
	c config.Config,
	db *sql.DB,
	l *notify.Listener,
	events *outbox.Outbox,
	store objectstore.Store,
	log *slog.Logger,
	health *observe.Health,
) (stop func(), err error) {
	wake, _ := l.Subscribe(notify.Jobs) // one of notify.Channels, so never an error
	q := queue.New(db.Primary())
	reg := queue.NewRegistry()
	tasks := cron.NewPeriodicTasks(db.Primary(), q)
	keys := idempotency.NewPostgres(db.Primary(), c.Server.Idempotency)
	reaper := blobs.NewReaper(db.Primary(), q, store)
	for _, t := range platformTasks(c, q, keys, events, reaper, log) {
		if err := reg.Register(t.kind, t.handler, queue.HandlerOptions{}); err != nil {
			return nil, fmt.Errorf("scheduler: %w", err)
		}
		if err := tasks.Ensure(ctx, t.kind, t.every); err != nil {
			return nil, fmt.Errorf("scheduler: %w", err)
		}
	}
	if err := errors.Join(events.Register(reg), reaper.Register(reg)); err != nil {
		return nil, fmt.Errorf("scheduler: %w", err)
	}
	health.Ready("queue_listener", func(context.Context) error {
		if !l.Listening() {
			return errNotListening
		}
		return nil
	})
	workers := queue.NewWorkers(q, reg, c.Queue, queue.AllowAll{}, wake.C, log, owner())
	ticker := cron.NewTicker(db.Primary(), c.Queue.SchedulerTick, log, tasks)

	// The roles run on contexts of their own: ctx ends when the shutdown
	// begins, and they must keep working until the HTTP drain is over.
	tickCtx, stopTicker := context.WithCancel(context.WithoutCancel(ctx))
	workCtx, stopWorkers := context.WithCancel(context.WithoutCancel(ctx))
	var ticking, working sync.WaitGroup
	ticking.Go(func() { ticker.Run(tickCtx) })
	working.Go(func() { workers.Run(workCtx) })
	return func() {
		stopTicker()
		ticking.Wait()
		stopWorkers()
		working.Wait()
		wake.Close()
	}, nil
}

// owner names this replica on the jobs it claims, for diagnosis.
func owner() string {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	return fmt.Sprintf("%s:%d", host, os.Getpid())
}
