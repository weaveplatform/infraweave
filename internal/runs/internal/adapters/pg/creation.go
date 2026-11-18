package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
)

// OnCommit reads any run of this stack, type and commit, including terminal
// runs and those created with local workspaces or overrides. Create holds the
// stack row, so a simultaneous creation cannot pass this read twice.
func (s *Store) OnCommit(ctx context.Context, acct kernel.AccountID, stack kernel.ULID, typ kernel.RunType, hash string) (lifecycle.Run, bool, error) {
	run, err := scanRun(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+runColumns+` FROM runs
		WHERE account_id=$1 AND stack_id=$2 AND type=$3 AND commit->>'hash'=$4
		ORDER BY id LIMIT 1`, acct, stack, string(typ), hash))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return lifecycle.Run{}, false, nil
	case err != nil:
		return lifecycle.Run{}, false, fmt.Errorf("runs: read prior %s run of stack %s: %w", typ, stack, sql.Unavailable(err))
	}
	return run, true, nil
}

// PlanningProposed locks only older runs still eligible for a branch's stop
// request. PostgreSQL rechecks these predicates after waiting on a worker's
// concurrent transition, and repeats omit an already requested stop.
func (s *Store) PlanningProposed(ctx context.Context, acct kernel.AccountID, stack kernel.ULID, branch, newHash string) ([]lifecycle.Run, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+runColumns+` FROM runs
		WHERE account_id=$1 AND stack_id=$2 AND type='PROPOSED' AND state='PLANNING'
			AND commit->>'branch'=$3 AND commit->>'hash'<>$4 AND stop_requested_at IS NULL
		ORDER BY id FOR UPDATE`, acct, stack, branch, newHash)
	if err != nil {
		return nil, fmt.Errorf("runs: read superseded proposals of stack %s: %w", stack, sql.Unavailable(err))
	}
	runs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (lifecycle.Run, error) {
		return scanRun(row)
	})
	if err != nil {
		return nil, fmt.Errorf("runs: read superseded proposals of stack %s: %w", stack, sql.Unavailable(err))
	}
	return runs, nil
}
