package pg

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
)

// WaitingApprovalStacks pages stable stack identities, without policy joins.
func (s *Store) WaitingApprovalStacks(ctx context.Context, account kernel.AccountID, after kernel.ULID, limit int) ([]kernel.ULID, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT DISTINCT stack_id FROM runs WHERE account_id=$1 AND ($2::uuid IS NULL OR stack_id>$2)
		AND state IN ('QUEUED','UNCONFIRMED','PENDING_REVIEW') ORDER BY stack_id LIMIT $3`, account, after, limit)
	if err != nil {
		return nil, fmt.Errorf("runs: select approval stacks: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	out := []kernel.ULID{}
	for rows.Next() {
		var id kernel.ULID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("runs: scan approval stack: %w", sql.Unavailable(err))
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("runs: read approval stack rows: %w", sql.Unavailable(err))
	}
	return out, nil
}

// WaitingApprovals excludes QUEUED, whose scheduling owns the first gate.
func (s *Store) WaitingApprovals(ctx context.Context, account kernel.AccountID, stack kernel.ULID) ([]lifecycle.Run, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+runColumns+` FROM runs WHERE account_id=$1 AND stack_id=$2
		AND state IN ('UNCONFIRMED','PENDING_REVIEW') ORDER BY id`, account, stack)
	if err != nil {
		return nil, fmt.Errorf("runs: select waiting approvals: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	out := []lifecycle.Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("runs: scan waiting approval: %w", sql.Unavailable(err))
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("runs: read waiting approval rows: %w", sql.Unavailable(err))
	}
	return out, nil
}
