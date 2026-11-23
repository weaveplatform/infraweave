package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/features/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// FeaturePreviews reads acct's opt-in to feature previews; an account
// without a row has not opted in.
func (s *Store) FeaturePreviews(ctx context.Context, acct kernel.AccountID) (bool, error) {
	var on bool
	err := tx.Q(ctx, s.db).QueryRow(ctx,
		`SELECT enabled FROM account_feature_previews WHERE account_id = $1`, acct).Scan(&on)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("features: %w", sql.Unavailable(err))
	}
	return on, nil
}

// SetFeaturePreviews sets acct's opt-in and, on commit, tells every
// replica to drop its snapshot of it.
func (s *Store) SetFeaturePreviews(ctx context.Context, acct kernel.AccountID, enabled bool) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if _, err := q.Exec(ctx, `
			INSERT INTO account_feature_previews (account_id, enabled) VALUES ($1, $2)
			ON CONFLICT (account_id)
			DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = now()`, acct, enabled); err != nil {
			return fmt.Errorf("features: %w", sql.Unavailable(err))
		}
		return tell(ctx, q, core.AccountKey(acct))
	})
	if err != nil {
		return fmt.Errorf("set the feature previews of account %s: %w", acct, err)
	}
	return nil
}

// PersonalFeaturePreviews reads member's setting in acct: true unless the
// member has opted out.
func (s *Store) PersonalFeaturePreviews(ctx context.Context, acct kernel.AccountID, member kernel.MemberID) (bool, error) {
	var out bool
	if err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT NOT EXISTS (
		SELECT 1 FROM member_feature_preview_opt_outs WHERE account_id = $1 AND member_id = $2)`,
		acct, member).Scan(&out); err != nil {
		return false, fmt.Errorf("features: %w", sql.Unavailable(err))
	}
	return out, nil
}

// SetPersonalFeaturePreviews sets member's setting in acct: an opt-out is
// a row, so opting back in deletes it.
func (s *Store) SetPersonalFeaturePreviews(ctx context.Context, acct kernel.AccountID, member kernel.MemberID, enabled bool) error {
	if enabled {
		return s.RemoveMember(ctx, acct, member)
	}
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `
		INSERT INTO member_feature_preview_opt_outs (account_id, member_id) VALUES ($1, $2)
		ON CONFLICT (account_id, member_id) DO UPDATE SET updated_at = now()`,
		acct, member); err != nil {
		return fmt.Errorf("features: %w", sql.Unavailable(err))
	}
	return nil
}

// RemoveMember deletes member's setting in acct.
func (s *Store) RemoveMember(ctx context.Context, acct kernel.AccountID, member kernel.MemberID) error {
	if _, err := tx.Q(ctx, s.db).Exec(ctx,
		`DELETE FROM member_feature_preview_opt_outs WHERE account_id = $1 AND member_id = $2`,
		acct, member); err != nil {
		return fmt.Errorf("features: %w", sql.Unavailable(err))
	}
	return nil
}
