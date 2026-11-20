package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
)

// PreviousPolicyRun defines infraweave's previous_run as the immediately older
// run of this account and stack, ordered by creation time and immutable ID.
// It excludes later rows even when evaluation of an older run is retried.
func (s *Store) PreviousPolicyRun(ctx context.Context, run lifecycle.Run) (lifecycle.Run, bool, error) {
	previous, err := scanRun(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+runColumns+` FROM runs
		WHERE account_id=$1 AND stack_id=$2 AND (created_at,id)<($3,$4)
		ORDER BY created_at DESC,id DESC LIMIT 1`, run.Account, run.Stack, run.CreatedAt, run.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		return lifecycle.Run{}, false, nil
	}
	if err != nil {
		return lifecycle.Run{}, false, fmt.Errorf("runs: read previous policy run: %w", sql.Unavailable(err))
	}
	return previous, true, nil
}
