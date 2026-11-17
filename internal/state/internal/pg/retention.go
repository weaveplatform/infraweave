package pg

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/state/internal/core"
)

// expired selects the versions retention may delete, after the version
// $1, $2 in the order of account and ID, and $4 of them at most. A
// version stopped being current when the next version of its workspace
// was written, so it may go once any newer version is older than the
// retention, $3 microseconds: the next one is no newer than that one.
// A version is newer by its created_at, then its ID within one instant.
// The workspace's current version is never one, even beside a version
// written later in time that a slower transaction recorded first. The
// version's own created_at bounds the scan, since no newer version is
// older than it.
const expired = `
SELECT v.account_id, v.id, v.run_id
FROM managed_state_versions v
JOIN state_workspaces w
    ON w.account_id = v.account_id AND w.stack_id = v.stack_id AND w.name = v.workspace
WHERE ($1::uuid IS NULL OR (v.account_id, v.id) > ($1::uuid, $2::uuid))
    AND v.created_at < now() - $3 * interval '1 microsecond'
    AND w.current_version_id IS DISTINCT FROM v.id
    AND EXISTS (
        SELECT 1 FROM managed_state_versions n
        WHERE n.account_id = v.account_id AND n.stack_id = v.stack_id
            AND n.workspace = v.workspace AND (n.created_at, n.id) > (v.created_at, v.id)
            AND n.created_at < now() - $3 * interval '1 microsecond')
ORDER BY v.account_id, v.id
LIMIT $4`

// Expired reads up to batch of the versions retention may delete, with
// the run that wrote each, in the order of their accounts and IDs and
// after the version after names when its ID is set.
func (s *Store) Expired(ctx context.Context, after core.Expired, retention time.Duration, batch int) ([]core.Expired, error) {
	// A failed Query returns rows that carry its error to CollectRows.
	rows, _ := tx.Q(ctx, s.db).Query(ctx, expired, after.Account, after.ID,
		retention.Microseconds(), batch)
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (core.Expired, error) {
		var e core.Expired
		err := row.Scan(&e.Account, &e.ID, &e.Run)
		return e, err //nolint:wrapcheck // CollectRows' caller names the read
	})
	if err != nil {
		return nil, fmt.Errorf("state: read the versions past their retention: %w",
			sql.Unavailable(err))
	}
	return out, nil
}

// DeleteVersions deletes those of acct's versions ids that are not their
// workspace's current version, and answers the object keys of those it
// deleted. The check of the current version stands at the delete itself,
// whatever the caller read before.
func (s *Store) DeleteVersions(ctx context.Context, acct kernel.AccountID, ids []kernel.ULID) ([]string, error) {
	rows, _ := tx.Q(ctx, s.db).Query(ctx, `DELETE FROM managed_state_versions v
		WHERE v.account_id = $1 AND v.id = ANY($2)
			AND NOT EXISTS (SELECT 1 FROM state_workspaces w
				WHERE w.account_id = v.account_id AND w.stack_id = v.stack_id
					AND w.name = v.workspace AND w.current_version_id = v.id)
		RETURNING v.object_key`, acct, ids)
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("state: delete %d versions of account %s: %w", len(ids), acct,
			sql.Unavailable(err))
	}
	return keys, nil
}
