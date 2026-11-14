package pg

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/schedule"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

var _ schedule.SignalStore = (*Store)(nil)

// requests are the statements that record a request of each signal.
var requests = map[workersapi.Signal]string{
	workersapi.SignalStop: `UPDATE runs SET stop_requested_at = $3,
		stop_requested_by = nullif($4, ''), stop_note = nullif($5, ''), updated_at = $3
		WHERE account_id = $1 AND id = $2`,
	workersapi.SignalKill: `UPDATE runs SET kill_requested_at = $3,
		kill_requested_by = nullif($4, ''), kill_note = nullif($5, ''), updated_at = $3
		WHERE account_id = $1 AND id = $2`,
}

// Request records sig as run's request of kind, and notifies the held
// polls of run's pool with the pool's ID alone, which wakes every one, as
// the poll to wake is the busy worker's, not an idle one's.
func (s *Store) Request(
	ctx context.Context,
	run lifecycle.Run,
	kind workersapi.Signal,
	sig lifecycle.Signal,
) error {
	q := tx.Q(ctx, s.db)
	if _, err := q.Exec(ctx, requests[kind], run.Account, run.ID, sig.At, sig.By,
		sig.Note); err != nil {
		return fmt.Errorf("runs: record the %s of run %s: %w", kind, run.ID, sql.Unavailable(err))
	}
	if run.WorkerPool == nil {
		return nil
	}
	if err := notify.Notify(ctx, q, notify.Runs, run.WorkerPool.String()); err != nil {
		return fmt.Errorf("runs: announce the %s of run %s: %w", kind, run.ID,
			sql.Unavailable(err))
	}
	return nil
}

// Signalled reads and locks the runs worker holds with a request no poll
// delivered. The partial index runs_worker serves it.
func (s *Store) Signalled(ctx context.Context, worker kernel.ULID) ([]lifecycle.Run, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+runColumns+` FROM runs
		WHERE worker_id = $1 AND state = ANY($2)
			AND (stop_requested_at IS NOT NULL AND stop_delivered_at IS NULL
				OR kill_requested_at IS NOT NULL AND kill_delivered_at IS NULL)
		ORDER BY id FOR UPDATE`, worker, onWorker)
	if err != nil {
		return nil, fmt.Errorf("runs: read the signals of worker %s: %w", worker,
			sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (lifecycle.Run, error) {
		return scanRun(r)
	})
	if err != nil {
		return nil, fmt.Errorf("runs: read the signals of worker %s: %w", worker,
			sql.Unavailable(err))
	}
	return out, nil
}

// deliveries are the statements that record a delivery of each signal; a
// kill's records its stop's too, which it goes in place of.
var deliveries = map[workersapi.Signal]string{
	workersapi.SignalStop: `UPDATE runs SET stop_delivered_at = $3
		WHERE account_id = $1 AND id = $2`,
	workersapi.SignalKill: `UPDATE runs SET kill_delivered_at = $3,
		stop_delivered_at = coalesce(stop_delivered_at, $3)
		WHERE account_id = $1 AND id = $2`,
}

// Delivered records the delivery of run's request of kind at at.
func (s *Store) Delivered(
	ctx context.Context,
	run lifecycle.Run,
	kind workersapi.Signal,
	at time.Time,
) error {
	if _, err := tx.Q(ctx, s.db).Exec(ctx, deliveries[kind], run.Account, run.ID,
		at); err != nil {
		return fmt.Errorf("runs: record the delivery of run %s's %s: %w", run.ID, kind,
			sql.Unavailable(err))
	}
	return nil
}
