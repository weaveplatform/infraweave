package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var _ accessapi.StackRoleReader = (*Store)(nil)

// StackRoles projects current role descriptions through account-owned direct
// stack bindings. Binding a role in two spaces does not repeat that role.
func (s *Store) StackRoles(ctx context.Context, account kernel.AccountID, stack kernel.ULID) ([]accessapi.PolicyRole, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT DISTINCT r.id,r.slug,r.name
		FROM role_bindings b JOIN roles r ON r.account_id=b.account_id AND r.id=b.role_id
		WHERE b.account_id=$1 AND b.actor_kind='stack' AND b.actor_id=$2 ORDER BY r.id`, account, stack)
	if err != nil {
		return nil, fmt.Errorf("access: read stack policy roles: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (accessapi.PolicyRole, error) {
		var role accessapi.PolicyRole
		err := row.Scan(&role.ID, &role.Slug, &role.Name)
		return role, err //nolint:wrapcheck // the collecting caller names the failed projection
	})
	if err != nil {
		return nil, fmt.Errorf("access: scan stack policy roles: %w", sql.Unavailable(err))
	}
	return out, nil
}
