package worker

import (
	"context"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
)

// The reap's defaults: a worker is offline after three default polls go
// unseen, and its run is lost after an hour, which covers a launcher's
// restart and a short outage.
//
// ref: decision 0008 § "Liveness: offline workers and lost runs"
const (
	DefaultOfflineAfter = 3 * time.Minute
	DefaultLostAfter    = time.Hour
)

// ReapBatch bounds one reap: it deletes at most this many lost workers,
// each in a transaction of its own, and fails at most this many overdue
// preparations, in one transaction. The next reap, 30 seconds on, goes on
// where it stopped.
const ReapBatch = 100

// clocks are the reap's: when a worker is offline and when lost, and the
// preparing timeout of a pool that sets none.
type clocks struct {
	offlineAfter, lostAfter, preparing time.Duration
}

func newClocks(d Deps) clocks {
	c := clocks{offlineAfter: d.OfflineAfter, lostAfter: d.LostAfter, preparing: d.PreparingTimeout}
	if c.offlineAfter == 0 {
		c.offlineAfter = DefaultOfflineAfter
	}
	if c.lostAfter == 0 {
		c.lostAfter = DefaultLostAfter
	}
	if c.preparing == 0 {
		c.preparing = pool.DefaultPreparingTimeout
	}
	return c
}

// Reaped is what one reap did: the workers it marked offline, the lost
// workers it deleted, the runs those held that it failed, and the
// preparations it failed as overdue.
type Reaped struct {
	Offline, Lost, LostRuns, Overdue int
}

// Reap is worker.reap's pass over every account's workers. It marks the
// workers unseen for worker.offline_after offline. It deletes each worker
// unseen for run.lost_after and, in the same transaction, fails the runs
// it held with the note "worker lost", so a poll that finds its worker
// gone answers 401 and no run outlives its worker's row. Then it fails
// the runs a claim moved to a preparing state that no started report
// moved on within their pool's preparing timeout. Until the runs domain's
// liveness is bound, it fails no run.
//
// ref: decision 0008 § "Liveness: offline workers and lost runs"
// ref: decision 0008 § "Timeouts and timers"
func (s *Service) Reap(ctx context.Context) (Reaped, error) {
	var out Reaped
	now := s.now()
	n, err := s.store.MarkOffline(ctx, now.Add(-s.clocks.offlineAfter))
	if err != nil {
		return out, err //nolint:wrapcheck // the store names the write
	}
	out.Offline = n
	for range ReapBatch {
		runs, lost, err := s.lose(ctx, now.Add(-s.clocks.lostAfter))
		if err != nil {
			return out, err
		}
		if !lost {
			break
		}
		out.Lost, out.LostRuns = out.Lost+1, out.LostRuns+runs
	}
	if s.liveness == nil {
		return out, nil
	}
	timeouts, err := s.store.PoolTimeouts(ctx)
	if err != nil {
		return out, err //nolint:wrapcheck // the store names the read
	}
	err = tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		out.Overdue, err = s.liveness.Overdue(ctx, now, timeouts, s.clocks.preparing, ReapBatch)
		return err //nolint:wrapcheck // the runs domain names the move
	})
	return out, err //nolint:wrapcheck // the runs domain's or the transaction's
}

// lose deletes one worker last seen before before and fails the runs it
// held, and reports false when no such worker is left.
func (s *Service) lose(ctx context.Context, before time.Time) (int, bool, error) {
	var (
		runs int
		lost bool
	)
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		var (
			id  kernel.ULID
			err error
		)
		runs = 0
		id, lost, err = s.store.Lost(ctx, before)
		if err != nil || !lost || s.liveness == nil {
			return err //nolint:wrapcheck // the store names the write
		}
		runs, err = s.liveness.Lost(ctx, id)
		return err //nolint:wrapcheck // the runs domain names the move
	})
	if err != nil {
		return 0, false, err //nolint:wrapcheck // the store's or the runs domain's
	}
	return runs, lost, nil
}

// ended fails run, whose container on the worker ended as exit, when the
// worker still holds it, as a poll that is not busy reports. A nil exit
// is a container whose end the launcher did not learn.
func (s *Service) ended(
	ctx context.Context,
	worker, run kernel.ULID,
	exit *workersapi.Exit,
) error {
	if s.liveness == nil {
		return nil
	}
	var e workersapi.Exit
	if exit != nil {
		e = *exit
	}
	//nolint:wrapcheck // the runs domain's or the transaction's
	return tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		_, err := s.liveness.Ended(ctx, worker, run, e)
		return err //nolint:wrapcheck // the runs domain names the move
	})
}
