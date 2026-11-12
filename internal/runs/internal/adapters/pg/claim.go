package pg

import (
	"context"
	"errors"
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

var _ schedule.ClaimStore = (*Store)(nil)

// onWorker are the states in which a worker holds a run.
var onWorker = func() []string {
	var out []string
	for _, s := range kernel.RunStates() {
		if s.OnWorker() {
			out = append(out, string(s))
		}
	}
	return out
}()

// Announce notifies the held polls of run's pool, which run.WorkerPool
// names, that run waits, on commit of the transaction ctx carries.
func (s *Store) Announce(ctx context.Context, run lifecycle.Run) error {
	err := notify.Notify(
		ctx,
		tx.Q(ctx, s.db),
		notify.Runs,
		workersapi.Waiting(*run.WorkerPool, run.ID),
	)
	if err != nil {
		return fmt.Errorf("runs: announce run %s: %w", run.ID, sql.Unavailable(err))
	}
	return nil
}

// Holding reports whether acct's worker holds a run: one it claimed last
// that is in a state on a worker.
func (s *Store) Holding(
	ctx context.Context,
	acct kernel.AccountID,
	worker kernel.ULID,
) (bool, error) {
	var holds bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM runs
		WHERE account_id = $1 AND worker_id = $2 AND state = ANY($3))`,
		acct, worker, onWorker).Scan(&holds)
	if err != nil {
		return false, fmt.Errorf(
			"runs: read the run of worker %s: %w",
			worker,
			sql.Unavailable(err),
		)
	}
	return holds, nil
}

// Drifting counts acct's pool's drift detection runs in a state on a
// worker.
func (s *Store) Drifting(
	ctx context.Context,
	acct kernel.AccountID,
	pool kernel.ULID,
) (int, error) {
	var n int
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT count(*) FROM runs
		WHERE account_id = $1 AND worker_pool_id = $2 AND drift_detection AND state = ANY($3)`,
		acct, pool, onWorker).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("runs: count the drift runs of pool %s: %w", pool,
			sql.Unavailable(err))
	}
	return n, nil
}

// TakeDrift takes the transaction's advisory lock on acct's pool's drift
// detection runs.
func (s *Store) TakeDrift(ctx context.Context, acct kernel.AccountID, pool kernel.ULID) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtext('runs_drift'), hashtext($1::text || '/' || $2::text))`,
		acct, pool)
	if err != nil {
		return fmt.Errorf("runs: lock the drift runs of pool %s: %w", pool, sql.Unavailable(err))
	}
	return nil
}

// Next reads and locks the run of acct's pool a worker takes next. The
// partial index runs_claimable serves its order.
func (s *Store) Next(
	ctx context.Context,
	acct kernel.AccountID,
	pool kernel.ULID,
	states []kernel.RunState,
	last int,
) (lifecycle.Run, bool, error) {
	r, err := scanRun(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+runColumns+` FROM runs
		WHERE account_id = $1 AND worker_pool_id = $2 AND state = ANY($3)
			AND priority_tier <= $4
		ORDER BY priority_tier, created_at, id
		LIMIT 1 FOR UPDATE SKIP LOCKED`, acct, pool, names(states), last))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return lifecycle.Run{}, false, nil
	case err != nil:
		return lifecycle.Run{}, false, fmt.Errorf("runs: claim a run of pool %s: %w", pool,
			sql.Unavailable(err))
	}
	return r, true, nil
}

// Assign writes worker as run's worker.
func (s *Store) Assign(ctx context.Context, run lifecycle.Run, worker kernel.ULID) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE runs SET worker_id = $3
		WHERE account_id = $1 AND id = $2`, run.Account, run.ID, worker)
	if err != nil {
		return fmt.Errorf("runs: assign run %s to worker %s: %w", run.ID, worker,
			sql.Unavailable(err))
	}
	return nil
}

var _ schedule.LivenessStore = (*Store)(nil)

// Held reads and locks the runs worker holds. The partial index
// runs_worker serves it.
func (s *Store) Held(ctx context.Context, worker kernel.ULID) ([]lifecycle.Run, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+runColumns+` FROM runs
		WHERE worker_id = $1 AND state = ANY($2)
		ORDER BY id FOR UPDATE`, worker, onWorker)
	if err != nil {
		return nil, fmt.Errorf("runs: read the runs of worker %s: %w", worker,
			sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (lifecycle.Run, error) {
		return scanRun(r)
	})
	if err != nil {
		return nil, fmt.Errorf("runs: read the runs of worker %s: %w", worker,
			sql.Unavailable(err))
	}
	return out, nil
}

// preparing are the states a claim moves a run to, which its first
// started report moves it out of.
var preparing = []string{
	string(kernel.RunStatePreparing), string(kernel.RunStatePreparingApply),
	string(kernel.RunStatePreparingReplan),
}

// Overdue reads and locks the runs left in preparation past their pool's
// preparing timeout. The partial index runs_preparing serves it.
func (s *Store) Overdue(
	ctx context.Context,
	now time.Time,
	timeouts []workersapi.PoolTimeout,
	fallback time.Duration,
	batch int,
) ([]lifecycle.Run, error) {
	pools := make([]kernel.ULID, 0, len(timeouts))
	micros := make([]int64, 0, len(timeouts))
	for _, t := range timeouts {
		pools, micros = append(pools, t.Pool), append(micros, t.Timeout.Microseconds())
	}
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+runColumns+` FROM runs
		WHERE state = ANY($1) AND state_changed_at < $2::timestamptz -
			coalesce((SELECT t.micros FROM unnest($3::uuid[], $4::bigint[]) AS t(pool, micros)
				WHERE t.pool = runs.worker_pool_id), $5) * interval '1 microsecond'
		ORDER BY state_changed_at, id
		LIMIT $6 FOR UPDATE SKIP LOCKED`,
		preparing, now, pools, micros, fallback.Microseconds(), batch)
	if err != nil {
		return nil, fmt.Errorf("runs: read the overdue preparations: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (lifecycle.Run, error) {
		return scanRun(r)
	})
	if err != nil {
		return nil, fmt.Errorf("runs: read the overdue preparations: %w", sql.Unavailable(err))
	}
	return out, nil
}
