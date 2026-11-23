package pg

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// Applied reports whether a tracked run of acct's stack entered APPLYING
// after the stack's last destroy run finished, or ever when none has, in
// the context's transaction: whether the stack manages resources that a
// destroy run would remove.
func (s *Store) Applied(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) (bool, error) {
	var applied bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM run_state_transitions t
		JOIN runs r ON r.account_id = t.account_id AND r.id = t.run_id
		WHERE r.account_id = $1 AND r.stack_id = $2 AND r.type = $3 AND t.state = $4
			AND t.timestamp > coalesce((
				SELECT max(d.timestamp) FROM run_state_transitions d
				JOIN runs dr ON dr.account_id = d.account_id AND dr.id = d.run_id
				WHERE dr.account_id = $1 AND dr.stack_id = $2 AND dr.type = $5
					AND d.state = $6), '-infinity'))`,
		acct, stack, string(kernel.RunTypeTracked), string(kernel.RunStateApplying),
		string(kernel.RunTypeDestroy), string(kernel.RunStateFinished)).Scan(&applied)
	if err != nil {
		return false, fmt.Errorf("runs: read whether stack %s applied changes: %w", stack,
			sql.Unavailable(err))
	}
	return applied, nil
}
