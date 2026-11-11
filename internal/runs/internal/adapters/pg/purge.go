package pg

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// purged are the tables Purge empties of an account, each with the key
// that names a row within the account, in order: the rows a run's delete
// would cascade to come first, so no one statement deletes more than its
// batch.
var purged = []struct{ table, key string }{
	{"run_state_transitions", "run_id, sequence"},
	{"stack_outputs", "stack_id, name"},
	{"stack_run_state", "stack_id"},
	{"stack_run_locks", "stack_id"},
	{"runs", "id"},
}

// Purge deletes up to batch of the deleted account acct's rows from the
// first of its transitions, outputs, tracked runs, run locks and runs that
// has any, in one statement, and reports whether none is left. Each call
// commits on its own unless ctx carries a transaction, so a purge stopped
// between calls keeps what it deleted and resumes from there.
func (s *Store) Purge(ctx context.Context, acct kernel.AccountID, batch int) (bool, error) {
	for _, p := range purged {
		tag, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM `+p.table+
			` WHERE account_id = $1 AND (`+p.key+`) IN (
			SELECT `+p.key+` FROM `+p.table+` WHERE account_id = $1 LIMIT $2)`, acct, batch)
		if err != nil {
			return false, fmt.Errorf("runs: purge account %s's %s: %w", acct, p.table,
				sql.Unavailable(err))
		}
		if tag.RowsAffected() > 0 {
			return false, nil
		}
	}
	return true, nil
}
