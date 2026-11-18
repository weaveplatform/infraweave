package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/push"
)

// RunLimit is the account setting, nil without a configured cap.
func (s *Store) RunLimit(ctx context.Context, acct kernel.AccountID) (*int, error) {
	var limit int
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT event_runs_limit FROM vcs_account_settings WHERE account_id=$1`, acct).Scan(&limit)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // no configured row is the public nullable account cap
	}
	if err != nil {
		return nil, fmt.Errorf("vcs: read event run limit: %w", sql.Unavailable(err))
	}
	return &limit, nil
}

// SetRunLimit joins the account setting's write/audit transaction.
func (s *Store) SetRunLimit(ctx context.Context, acct kernel.AccountID, limit *int) error {
	var err error
	if limit == nil {
		_, err = tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM vcs_account_settings WHERE account_id=$1`, acct)
	} else {
		_, err = tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO vcs_account_settings(account_id,event_runs_limit) VALUES($1,$2)
			ON CONFLICT(account_id) DO UPDATE SET event_runs_limit=EXCLUDED.event_runs_limit`, acct, *limit)
	}
	if err != nil {
		return fmt.Errorf("vcs: write event run limit: %w", sql.Unavailable(err))
	}
	return nil
}

// ReserveRun conditionally increments the shared counter. It must run after
// lifecycle deduplication and in the same transaction as insertion/completion.
func (s *Store) ReserveRun(ctx context.Context, acct kernel.AccountID, delivery kernel.ULID, limit int) (bool, error) {
	if !tx.In(ctx) {
		return false, push.ErrNoTransaction
	}
	result, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE vcs_deliveries SET runs_created=runs_created+1
		WHERE account_id=$1 AND id=$2 AND runs_created<$3`, acct, delivery, limit)
	if err != nil {
		return false, fmt.Errorf("vcs: reserve event run slot: %w", sql.Unavailable(err))
	}
	return result.RowsAffected() == 1, nil
}

// PurgeRunLimit removes only the deleted account's cap.
func (s *Store) PurgeRunLimit(ctx context.Context, acct kernel.AccountID) error {
	return s.SetRunLimit(ctx, acct, nil)
}
