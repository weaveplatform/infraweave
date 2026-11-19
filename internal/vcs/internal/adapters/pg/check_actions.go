package pg

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/push"
)

// HasCheckAction reads successful original intent under the completion fences.
// Replay generation is deliberately absent: replay cannot repeat a past effect.
func (s *Store) HasCheckAction(ctx context.Context, account kernel.AccountID, delivery, stack kernel.ULID) (bool, error) {
	if !tx.In(ctx) {
		return false, push.ErrNoTransaction
	}
	var done bool
	err := tx.Q(ctx, s.db).
		QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM vcs_check_action_effects WHERE account_id=$1 AND delivery_id=$2 AND stack_id=$3)`, account, delivery, stack).
		Scan(&done)
	if err != nil {
		return false, fmt.Errorf("vcs: read check action effect: %w", sql.Unavailable(err))
	}
	return done, nil
}

// RecordCheckAction follows successful real run creation in the SAME transaction.
// A conflicting receipt is an error: it rolls back the new run and admission
// rather than hiding a duplicate effect behind an ignored insert.
func (s *Store) RecordCheckAction(ctx context.Context, account kernel.AccountID, delivery, stack kernel.ULID) error {
	if !tx.In(ctx) {
		return push.ErrNoTransaction
	}
	_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO vcs_check_action_effects(account_id,delivery_id,stack_id) VALUES($1,$2,$3)`, account, delivery, stack)
	if err != nil {
		return fmt.Errorf("vcs: record check action effect: %w", sql.Unavailable(err))
	}
	return nil
}
