package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
)

// Effective reads one immutable version snapshot per policy. Current labels
// cover the interval before the asynchronous attachment reconciler catches up;
// obsolete automatic rows do not override a lost label match. No policy lock is
// taken after the caller's stack/run locks, avoiding a write-time lock inversion.
func (s *Store) Effective(ctx context.Context, acct kernel.AccountID, kind rego.Type, spaces []kernel.ULID, stack *kernel.ULID, labels []string) ([]core.Policy, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, "SELECT "+columns+` FROM policies p
 WHERE p.account_id=$1 AND p.type=$2 AND p.space_id=ANY($3)
 AND ($4::uuid IS NULL OR p.labels && $5::text[] OR EXISTS (
 SELECT 1 FROM policy_stack_attachments a WHERE a.account_id=p.account_id
 AND a.policy_id=p.id AND a.stack_id=$4 AND NOT a.is_autoattached)) ORDER BY p.id`, acct, kind, spaces, stack, labels)
	if err != nil {
		return nil, fmt.Errorf("policies: select effective programs: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (core.Policy, error) { return scan(row) })
	if err != nil {
		return nil, fmt.Errorf("policies: collect effective programs: %w", sql.Unavailable(err))
	}
	return out, nil
}
