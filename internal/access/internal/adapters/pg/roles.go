package pg

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// seedAccount records what creating a gives it, in the transaction that
// creates it: its top space and legacy, and its system roles.
func seedAccount(ctx context.Context, q tx.Querier, a account.Account) error {
	if err := seedSpaces(ctx, q, a); err != nil {
		return err
	}
	for _, r := range role.Seed(a.ID) {
		if _, err := q.Exec(
			ctx,
			`INSERT INTO roles (account_id, id, slug, name, description,
				is_system, created_at) VALUES ($1, $2, $3, $4, $5, $6, coalesce($7, now()))`,
			r.AccountID,
			r.ID,
			r.Slug,
			r.Name,
			r.Description,
			r.System,
			nullTime(a.CreatedAt),
		); err != nil {
			return fmt.Errorf("access: seed role %s: %w", r.Slug, sql.Unavailable(err))
		}
	}
	return nil
}

// bindAccountAdmin binds space-admin on acct's top space to the member
// admin, the account admin that claiming or creating acct names, made by
// the organization admin e records. It is an ordinary binding, which
// account admins may later remove.
func bindAccountAdmin(
	ctx context.Context,
	q tx.Querier,
	acct kernel.AccountID,
	admin kernel.MemberID,
	e kernel.OrganizationAuditEntry,
) error {
	if _, err := q.Exec(ctx, `INSERT INTO role_bindings (account_id, id, actor_kind, actor_id,
			role_id, space_id, origin, created_by)
		SELECT $1, $2, $3, $4, id, $1, $5, $6 FROM roles WHERE account_id = $1 AND slug = $7`,
		acct, kernel.NewULID(), role.ActorUser, admin, role.OriginBinding,
		string(e.ActorKind)+":"+e.ActorID, role.SpaceAdmin); err != nil {
		return fmt.Errorf("access: bind the account admin: %w", sql.Unavailable(err))
	}
	return nil
}
