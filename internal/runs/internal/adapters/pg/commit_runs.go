package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
)

// OnCommits selects only the run facts needed for commit membership, without
// decoding full run views or opening any sealed data.
func (s *Store) OnCommits(ctx context.Context, account kernel.AccountID, stack kernel.ULID, hashes []string) ([]runsapi.CommitRun, error) {
	if len(hashes) == 0 {
		return []runsapi.CommitRun{}, nil
	}
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT id,commit->>'hash',created_at FROM runs
		WHERE account_id=$1 AND stack_id=$2 AND commit->>'hash'=ANY($3::text[])
		ORDER BY created_at DESC,id DESC`, account, stack, hashes)
	if err != nil {
		return nil, fmt.Errorf("runs: read commits of stack %s: %w", stack, sql.Unavailable(err))
	}
	runs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (runsapi.CommitRun, error) {
		var run runsapi.CommitRun
		err := row.Scan(&run.ID, &run.CommitSHA, &run.CreatedAt)
		run.CreatedAt = run.CreatedAt.UTC()
		return run, err //nolint:wrapcheck // the caller names the read
	})
	if err != nil {
		return nil, fmt.Errorf("runs: read commit rows of stack %s: %w", stack, sql.Unavailable(err))
	}
	return runs, nil
}

var _ runsapi.CommitRuns = (*Store)(nil)
