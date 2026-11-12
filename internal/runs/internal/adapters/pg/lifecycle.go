package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/schedule"
)

var (
	_ lifecycle.Store = (*Store)(nil)
	_ schedule.Store  = (*Store)(nil)
)

// runColumns are the columns scanRun reads, in its order.
const runColumns = `account_id, id, stack_id, type, state, state_version, state_changed_at,
	commit, triggered_by, priority, drift_detection, runtime_config,
	coalesce(force_apply_mode, ''), local_preview, worker_pool_id, worker_id, created_at,
	updated_at`

// insertColumns are runColumns as an INSERT names them, with the
// priority_tier the claim orders by.
const insertColumns = `account_id, id, stack_id, type, state, state_version, state_changed_at,
	commit, triggered_by, priority, drift_detection, runtime_config, force_apply_mode,
	local_preview, worker_pool_id, worker_id, created_at, updated_at, priority_tier`

// scanRun reads one row of runColumns.
func scanRun(row pgx.Row) (lifecycle.Run, error) {
	var (
		r                    lifecycle.Run
		typ, state, priority string
		commit               []byte
	)
	err := row.Scan(&r.Account, &r.ID, &r.Stack, &typ, &state, &r.Version, &r.StateChangedAt,
		&commit, &r.TriggeredBy, &priority, &r.DriftDetection, &r.RuntimeConfig, &r.ForceApply,
		&r.LocalPreview, &r.WorkerPool, &r.Worker, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return lifecycle.Run{}, err //nolint:wrapcheck // the caller names the read
	}
	if err := json.Unmarshal(commit, &r.Commit); err != nil {
		return lifecycle.Run{}, fmt.Errorf("runs: decode the commit of run %s: %w", r.ID, err)
	}
	r.Type, r.State, r.Priority = kernel.RunType(typ), kernel.RunState(state),
		kernel.RunPriority(priority)
	r.StateChangedAt, r.CreatedAt, r.UpdatedAt = r.StateChangedAt.UTC(), r.CreatedAt.UTC(),
		r.UpdatedAt.UTC()
	return r, nil
}

// Lock reads acct's run id and locks its row FOR UPDATE.
func (s *Store) Lock(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (lifecycle.Run, error) {
	r, err := scanRun(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+runColumns+` FROM runs
		WHERE account_id = $1 AND id = $2 FOR UPDATE`, acct, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return lifecycle.Run{}, fmt.Errorf("%w: %s", lifecycle.ErrNotFound, id)
	case err != nil:
		return lifecycle.Run{}, fmt.Errorf("runs: lock run %s: %w", id, sql.Unavailable(err))
	}
	return r, nil
}

// Move writes run's new state, state_version and state_changed_at from
// step, and appends step to its transitions.
func (s *Store) Move(ctx context.Context, run lifecycle.Run, step lifecycle.Step) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE runs
		SET state = $3, state_version = $4, state_changed_at = $5, updated_at = $5
		WHERE account_id = $1 AND id = $2`,
		run.Account, run.ID, string(step.State), step.Sequence, step.At)
	if err != nil {
		return fmt.Errorf("runs: move run %s: %w", run.ID, sql.Unavailable(err))
	}
	return s.step(ctx, run, step)
}

// Insert writes run and its first transition, step.
func (s *Store) Insert(ctx context.Context, run lifecycle.Run, step lifecycle.Step) error {
	commit, err := json.Marshal(run.Commit)
	if err != nil {
		return fmt.Errorf("runs: encode the commit of run %s: %w", run.ID, err)
	}
	_, err = tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO runs (`+insertColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, nullif($13, ''), $14, $15,
			$16, $17, $18, $19)`,
		run.Account, run.ID, run.Stack, string(run.Type), string(run.State), run.Version,
		run.StateChangedAt, commit, run.TriggeredBy, string(run.Priority), run.DriftDetection,
		[]byte(run.RuntimeConfig), run.ForceApply, run.LocalPreview, run.WorkerPool, run.Worker,
		run.CreatedAt, run.UpdatedAt, kernel.RunTier(run.Priority, run.Type, run.DriftDetection))
	if err != nil {
		return fmt.Errorf("runs: insert run %s: %w", run.ID, sql.Unavailable(err))
	}
	return s.step(ctx, run, step)
}

// step appends step to run's transitions.
func (s *Store) step(ctx context.Context, run lifecycle.Run, step lifecycle.Step) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO run_state_transitions
		(account_id, run_id, sequence, state, timestamp, until, username, note, terminal)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		run.Account, run.ID, step.Sequence, string(step.State), step.At, step.Until,
		step.Username, step.Note, step.State.Terminal())
	if err != nil {
		return fmt.Errorf("runs: record the transition of run %s to %s: %w",
			run.ID, step.State, sql.Unavailable(err))
	}
	return nil
}

// Active reads acct's oldest run of stack of type typ on commit hash that
// has not ended.
func (s *Store) Active(
	ctx context.Context,
	acct kernel.AccountID,
	stack kernel.ULID,
	typ kernel.RunType,
	hash string,
) (lifecycle.Run, bool, error) {
	var terminal []string
	for _, st := range kernel.RunStates() {
		if st.Terminal() {
			terminal = append(terminal, string(st))
		}
	}
	r, err := scanRun(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+runColumns+` FROM runs
		WHERE account_id = $1 AND stack_id = $2 AND type = $3 AND commit->>'hash' = $4
			AND state <> ALL($5)
		ORDER BY id LIMIT 1`, acct, stack, string(typ), hash, terminal))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return lifecycle.Run{}, false, nil
	case err != nil:
		return lifecycle.Run{}, false, fmt.Errorf("runs: read the active %s runs of stack %s: %w",
			typ, stack, sql.Unavailable(err))
	}
	return r, true, nil
}

// Track makes run its stack's tracked run.
func (s *Store) Track(ctx context.Context, run lifecycle.Run) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO stack_run_state
		(account_id, stack_id, tracked_run_id) VALUES ($1, $2, $3)
		ON CONFLICT (account_id, stack_id) DO UPDATE SET tracked_run_id = excluded.tracked_run_id`,
		run.Account, run.Stack, run.ID)
	if err != nil {
		return fmt.Errorf("runs: track run %s: %w", run.ID, sql.Unavailable(err))
	}
	return nil
}

// Unlock deletes the stack run lock run holds, and reports whether it held
// one.
func (s *Store) Unlock(ctx context.Context, acct kernel.AccountID, run kernel.ULID) (bool, error) {
	tag, err := tx.Q(ctx, s.db).Exec(ctx,
		`DELETE FROM stack_run_locks WHERE account_id = $1 AND run_id = $2`, acct, run)
	if err != nil {
		return false, fmt.Errorf(
			"runs: release the run lock of run %s: %w",
			run,
			sql.Unavailable(err),
		)
	}
	return tag.RowsAffected() > 0, nil
}

// Projection reads stack's state, the last state of its tracked run that
// is one of states, NONE without one, and whether a run holds its run
// lock.
func (s *Store) Projection(
	ctx context.Context,
	acct kernel.AccountID,
	stack kernel.ULID,
	states []kernel.StackState,
) (kernel.StackState, bool, error) {
	names := make([]string, len(states))
	for i, st := range states {
		names[i] = string(st)
	}
	var (
		state   string
		blocked bool
	)
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT
		coalesce((SELECT t.state FROM stack_run_state r
			JOIN run_state_transitions t
				ON t.account_id = r.account_id AND t.run_id = r.tracked_run_id
			WHERE r.account_id = $1 AND r.stack_id = $2 AND t.state = ANY($3)
			ORDER BY t.sequence DESC LIMIT 1), $4),
		EXISTS (SELECT 1 FROM stack_run_locks WHERE account_id = $1 AND stack_id = $2)`,
		acct, stack, names, string(kernel.StackStateNone)).Scan(&state, &blocked)
	if err != nil {
		return "", false, fmt.Errorf("runs: read the run state of stack %s: %w",
			stack, sql.Unavailable(err))
	}
	return kernel.StackState(state), blocked, nil
}

// Queued reads acct's stack's QUEUED runs, oldest first.
func (s *Store) Queued(
	ctx context.Context,
	acct kernel.AccountID,
	stack kernel.ULID,
) ([]lifecycle.Run, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+runColumns+` FROM runs
		WHERE account_id = $1 AND stack_id = $2 AND state = $3 ORDER BY id`,
		acct, stack, string(kernel.RunStateQueued))
	if err != nil {
		return nil, fmt.Errorf(
			"runs: read the queued runs of stack %s: %w",
			stack,
			sql.Unavailable(err),
		)
	}
	defer rows.Close()
	var out []lifecycle.Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf(
				"runs: read a queued run of stack %s: %w",
				stack,
				sql.Unavailable(err),
			)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"runs: read the queued runs of stack %s: %w",
			stack,
			sql.Unavailable(err),
		)
	}
	return out, nil
}

// TakeLock makes run hold its stack's run lock when no run holds it, and
// reports whether it does.
func (s *Store) TakeLock(ctx context.Context, run lifecycle.Run) (bool, error) {
	tag, err := tx.Q(ctx, s.db).
		Exec(ctx, `INSERT INTO stack_run_locks (account_id, stack_id, run_id)
		VALUES ($1, $2, $3) ON CONFLICT (account_id, stack_id) DO NOTHING`,
			run.Account, run.Stack, run.ID)
	if err != nil {
		return false, fmt.Errorf("runs: lock stack %s for run %s: %w", run.Stack, run.ID,
			sql.Unavailable(err))
	}
	return tag.RowsAffected() == 1, nil
}
