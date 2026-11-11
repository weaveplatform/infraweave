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
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/idempotency"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	"github.com/weaveplatform/infraweave/internal/platform/observe"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
)

// The platform's periodic tasks. Each runs on the maintenance queue with
// policy Ignore, so a slow run never overlaps the next.
//
// ref: decision 0011
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

// platformTasks are the periodic tasks of the platform's own tables.
func platformTasks(
	c config.Config,
	q *queue.Queue,
	keys *idempotency.Postgres,
	events *outbox.Outbox,
	reaper *blobs.Reaper,
	log *slog.Logger,
) []cron.Task {
	return []cron.Task{
		{
			Kind:  PruneJobs,
			Every: 10 * time.Minute,
			Handler: func(ctx context.Context, _ queue.Job) error {
				n, err := q.Prune(ctx, c.Queue.CompletedRetention, c.Queue.DeadRetention)
				if err == nil {
					log.Debug("finished jobs pruned", "jobs", n)
				}
				return err //nolint:wrapcheck // Prune names itself
			},
		},
		{
			Kind:  PurgeIdempotencyKeys,
			Every: time.Hour,
			Handler: func(ctx context.Context, _ queue.Job) error {
				n, err := keys.Purge(ctx)
				if err == nil {
					log.Debug("expired idempotency keys purged", "keys", n)
				}
				return err //nolint:wrapcheck // Purge names itself
			},
		},
		{
			Kind:  PruneEvents,
			Every: 10 * time.Minute,
			Handler: func(ctx context.Context, _ queue.Job) error {
				n, err := events.Prune(ctx, c.Events.Retention)
				if err == nil {
					log.Debug("fanned-out events pruned", "events", n)
				}
				return err //nolint:wrapcheck // Prune names itself
			},
		},
		{
			Kind:  blobs.ExpireKind,
			Every: blobs.ExpireEvery,
			Handler: func(ctx context.Context, _ queue.Job) error {
				n, err := reaper.Expire(ctx, blobs.ExpireBatch)
				if err == nil {
					log.Debug("expired and abandoned objects marked for deletion", "objects", n)
				}
				return err //nolint:wrapcheck // Expire names itself
			},
		},
	}
}

// errNotListening fails the readiness of a scheduler whose listening
// connection is down: its workers wake only on the poll interval then.
var errNotListening = errors.New("the queue's listening connection is down")

// startScheduler starts the scheduler role: the queue's workers with the
// platform's handlers, the domains' jobs, the outbox's fan-out and
// consumption and blob.delete over store, the one full objectstore.Store
// of the process, and the ticker over periodic_tasks, after registering
// the periodic tasks in the database. ent decides whether a job's account
// may run its kind's feature, so a downgraded organization's jobs
// complete as skipped. Their wake-ups come through l, which the caller
// runs. A registration that fails fails startup. The returned stop ends
// the ticker, then the workers, which finish within queue.shutdown_grace
// or hand their jobs back to pending without counting the attempt; the
// caller calls it after the HTTP drain, so another replica takes the work
// at once, and before the pool closes.
//
// ref: decisions 0011 and 0020
func startScheduler(
	ctx context.Context,
	c config.Config,
	db *sql.DB,
	keys *crypt.Keyring,
	provider kek.Provider,
	l *notify.Listener,
	events *outbox.Outbox,
	store objectstore.Store,
	domainTasks []cron.Task,
	domainJobs []queue.Registration,
	ent queue.Entitlements,
	log *slog.Logger,
	health *observe.Health,
) (stop func(), err error) {
	wake, _ := l.Subscribe(notify.Jobs) // one of notify.Channels, so never an error
	q := queue.New(db.Primary())
	reg := queue.NewRegistry()
	tasks := cron.NewPeriodicTasks(db.Primary(), q)
	idem := idempotency.NewPostgres(db.Primary(), c.Server.Idempotency, keys)
	reaper := blobs.NewReaper(db.Primary(), q, store)
	rotate, reseal := secretsTasks(c, db.Primary(), q, provider, log)
	periodic := append(platformTasks(c, q, idem, events, reaper, log), rotate)
	for _, t := range append(periodic, domainTasks...) {
		if err := reg.Register(t.Kind, t.Handler, queue.HandlerOptions{}); err != nil {
			return nil, fmt.Errorf("scheduler: %w", err)
		}
		if err := tasks.Ensure(ctx, t.Kind, t.Every); err != nil {
			return nil, fmt.Errorf("scheduler: %w", err)
		}
	}
	for _, j := range domainJobs {
		if err := reg.Register(j.Kind, j.Handler, j.Options); err != nil {
			return nil, fmt.Errorf("scheduler: %w", err)
		}
	}
	if err := errors.Join(
		// A reseal that runs out of time runs again from the start, skipping
		// what it has resealed, so the longest timeout only saves passes.
		reg.Register(ResealValues, reseal, queue.HandlerOptions{Timeout: queue.MaxTimeout}),
		events.Register(reg),
		reaper.Register(reg),
	); err != nil {
		return nil, fmt.Errorf("scheduler: %w", err)
	}
	health.Ready("queue_listener", func(context.Context) error {
		if !l.Listening() {
			return errNotListening
		}
		return nil
	})
	workers := queue.NewWorkers(q, reg, c.Queue, ent, wake.C, log, owner())
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
