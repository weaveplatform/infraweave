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

// WakeClaims is delivered only after the claim transaction commits and
// releases owner fences that may have made another idle poll skip its work.
func (s *Store) WakeClaims(ctx context.Context, pool kernel.ULID) error {
	if err := notify.Notify(ctx, tx.Q(ctx, s.db), notify.Runs, pool.String()); err != nil {
		return fmt.Errorf("runs: wake contended claims: %w", sql.Unavailable(err))
	}
	return nil
}

// Holding reports whether acct's worker holds a run: one it claimed last
// that is in a state on a worker.
func (s *Store) Holding(ctx context.Context, acct kernel.AccountID, worker kernel.ULID) (bool, error) {
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
func (s *Store) Drifting(ctx context.Context, acct kernel.AccountID, pool kernel.ULID) (int, error) {
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

// ProbeCandidate uses runs_claimable's order without acquiring the run lock.
func (s *Store) ProbeCandidate(
	ctx context.Context,
	acct kernel.AccountID,
	pool kernel.ULID,
	states []kernel.RunState,
	last int,
	after *schedule.ClaimCursor,
	excluded []kernel.ULID,
) (lifecycle.Run, bool, error) {
	if excluded == nil {
		excluded = []kernel.ULID{}
	}
	var tier *int
	var at *time.Time
	var id *kernel.ULID
	if after != nil {
		tier, at, id = &after.Tier, &after.CreatedAt, &after.ID
	}
	r, err := scanRun(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+runColumns+` FROM runs
		WHERE account_id = $1 AND worker_pool_id = $2 AND state = ANY($3)
			AND priority_tier <= $4 AND NOT (stack_id=ANY($8::uuid[]))
			AND ($5::int IS NULL OR (priority_tier,created_at,id)>($5,$6::timestamptz,$7::uuid))
		ORDER BY priority_tier, created_at, id
		LIMIT 1`, acct, pool, names(states), last, tier, at, id, excluded))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return lifecycle.Run{}, false, nil
	case err != nil:
		return lifecycle.Run{}, false, fmt.Errorf("runs: claim a run of pool %s: %w", pool,
			sql.Unavailable(err))
	}
	return r, true, nil
}

// LockCandidate revalidates the exact probed queue entry after owner fencing.
func (s *Store) LockCandidate(ctx context.Context, candidate lifecycle.Run, pool kernel.ULID, states []kernel.RunState, last int) (lifecycle.Run, bool, error) {
	tier := kernel.RunTier(candidate.Priority, candidate.Type, candidate.DriftDetection)
	r, err := scanRun(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+runColumns+` FROM runs
		WHERE account_id=$1 AND id=$2 AND stack_id=$3 AND worker_pool_id=$4
		AND state_version=$5 AND state=$6 AND state=ANY($7) AND priority_tier=$8 AND priority_tier<=$9
		FOR UPDATE SKIP LOCKED`, candidate.Account, candidate.ID, candidate.Stack, pool,
		candidate.Version, string(candidate.State), names(states), tier, last))
	if errors.Is(err, pgx.ErrNoRows) {
		return lifecycle.Run{}, false, nil
	}
	if err != nil {
		return lifecycle.Run{}, false, fmt.Errorf("runs: lock claim candidate %s: %w", candidate.ID, sql.Unavailable(err))
	}
	return r, true, nil
}

// CandidateAttempt scopes only nonblocking owner locks and row revalidation.
// A missed candidate must release those locks before trying another stack.
func (s *Store) CandidateAttempt(ctx context.Context, fn func(context.Context) (bool, error)) (bool, error) {
	if !tx.In(ctx) {
		return false, lifecycle.ErrNoTransaction
	}
	q := tx.Q(ctx, s.db)
	if _, err := q.Exec(ctx, `SAVEPOINT runs_claim_candidate`); err != nil {
		return false, fmt.Errorf("runs: begin claim candidate: %w", sql.Unavailable(err))
	}
	finished := false
	defer func() {
		if !finished {
			_, _ = q.Exec(context.WithoutCancel(ctx), `ROLLBACK TO SAVEPOINT runs_claim_candidate`)
			_, _ = q.Exec(context.WithoutCancel(ctx), `RELEASE SAVEPOINT runs_claim_candidate`)
		}
	}()
	held, err := fn(ctx)
	if err != nil || !held {
		if _, rollback := q.Exec(context.WithoutCancel(ctx), `ROLLBACK TO SAVEPOINT runs_claim_candidate`); rollback != nil {
			return false, fmt.Errorf("runs: release missed claim candidate: %w", errors.Join(err, sql.Unavailable(rollback)))
		}
	}
	if _, release := q.Exec(context.WithoutCancel(ctx), `RELEASE SAVEPOINT runs_claim_candidate`); release != nil {
		return false, fmt.Errorf("runs: finish claim candidate: %w", errors.Join(err, sql.Unavailable(release)))
	}
	finished = true
	return held, err
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

// Waiting counts the runs in one of states of each private pool. The
// partial index runs_claimable serves it for the claimable states.
func (s *Store) Waiting(ctx context.Context, states []kernel.RunState) (map[kernel.ULID]int, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT worker_pool_id, count(*) FROM runs
		WHERE state = ANY($1) AND worker_pool_id IS NOT NULL
		GROUP BY worker_pool_id`, names(states))
	if err != nil {
		return nil, fmt.Errorf("runs: count the queued runs: %w", sql.Unavailable(err))
	}
	out := map[kernel.ULID]int{}
	var (
		pool kernel.ULID
		n    int
	)
	_, err = pgx.ForEachRow(rows, []any{&pool, &n}, func() error {
		out[pool] = n
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("runs: count the queued runs: %w", sql.Unavailable(err))
	}
	return out, nil
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
func (s *Store) Overdue(ctx context.Context, now time.Time, timeouts []workersapi.PoolTimeout, fallback time.Duration, batch int) ([]lifecycle.Run, error) {
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
