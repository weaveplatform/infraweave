package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/control"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
)

var _ control.Store = (*Store)(nil)

// Comment records c on run.
func (s *Store) Comment(ctx context.Context, run lifecycle.Run, c read.Comment) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO run_comments
		(account_id, run_id, id, username, body, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		run.Account, run.ID, c.ID, c.Username, c.Body, c.At)
	if err != nil {
		return fmt.Errorf("runs: comment on run %s: %w", run.ID, sql.Unavailable(err))
	}
	return nil
}

// SetTargets writes run's replan_targets.
func (s *Store) SetTargets(ctx context.Context, run lifecycle.Run, targets []string) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE runs SET replan_targets = $3
		WHERE account_id = $1 AND id = $2`, run.Account, run.ID, targets)
	if err != nil {
		return fmt.Errorf("runs: set the replan targets of run %s: %w", run.ID,
			sql.Unavailable(err))
	}
	return nil
}

// Comments reads the comments on acct's runs, oldest first.
func (s *Store) Comments(ctx context.Context, acct kernel.AccountID, runs []kernel.ULID) (map[kernel.ULID][]read.Comment, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT run_id, id, username, body, created_at
		FROM run_comments WHERE account_id = $1 AND run_id = ANY($2) ORDER BY run_id, id`,
		acct, runs)
	if err != nil {
		return nil, fmt.Errorf("runs: read the runs' comments: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	out := make(map[kernel.ULID][]read.Comment, len(runs))
	for rows.Next() {
		var (
			run kernel.ULID
			c   read.Comment
		)
		if err := rows.Scan(&run, &c.ID, &c.Username, &c.Body, &c.At); err != nil {
			return nil, fmt.Errorf("runs: read a run's comment: %w", sql.Unavailable(err))
		}
		c.At = c.At.UTC()
		out[run] = append(out[run], c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("runs: read the runs' comments: %w", sql.Unavailable(err))
	}
	return out, nil
}

// Discardable reads acct's stack's runs in QUEUED, READY or UNCONFIRMED,
// oldest first.
func (s *Store) Discardable(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) ([]lifecycle.Run, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+runColumns+` FROM runs
		WHERE account_id = $1 AND stack_id = $2 AND state IN ('QUEUED', 'READY', 'UNCONFIRMED')
		ORDER BY created_at, id`, acct, stack)
	if err != nil {
		return nil, fmt.Errorf("runs: read stack %s's discardable runs: %w", stack,
			sql.Unavailable(err))
	}
	runs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (lifecycle.Run, error) {
		return scanRun(row)
	})
	if err != nil {
		return nil, fmt.Errorf("runs: read stack %s's discardable runs: %w", stack,
			sql.Unavailable(err))
	}
	return runs, nil
}
