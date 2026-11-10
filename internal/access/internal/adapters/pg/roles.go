package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
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
	return bumpAuthz(ctx, q, a.ID)
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

var _ role.Store = (*Store)(nil)

// roleColumns read a role with its stored actions, which a system role
// has none of, and its count of bindings.
const roleColumns = `r.account_id, r.id, r.slug, r.name, r.description, r.is_system, r.version,
	coalesce((SELECT array_agg(a.action ORDER BY a.action) FROM role_actions a
		WHERE a.account_id = r.account_id AND a.role_id = r.id), '{}'),
	(SELECT count(*) FROM role_bindings b WHERE b.account_id = r.account_id AND b.role_id = r.id)`

func scanRole(row pgx.Row) (role.Role, error) {
	var (
		r       role.Role
		actions []string
	)
	err := row.Scan(&r.AccountID, &r.ID, &r.Slug, &r.Name, &r.Description, &r.System, &r.Version,
		&actions, &r.Bindings)
	for _, a := range actions {
		r.Actions = append(r.Actions, accessapi.Action(a))
	}
	return r, err //nolint:wrapcheck // callers name the read
}

// Roles reads up to limit of acct's roles after the slug after, by slug.
func (s *Store) Roles(
	ctx context.Context,
	acct kernel.AccountID,
	after string,
	limit int,
) ([]role.Role, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+roleColumns+` FROM roles r
		WHERE r.account_id = $1 AND r.slug > $2 ORDER BY r.slug LIMIT $3`, acct, after, limit)
	if err != nil {
		return nil, fmt.Errorf("access: list roles: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (role.Role, error) {
		return scanRole(r)
	})
	if err != nil {
		return nil, fmt.Errorf("access: list roles: %w", sql.Unavailable(err))
	}
	return out, nil
}

// Role reads acct's role slug.
func (s *Store) Role(ctx context.Context, acct kernel.AccountID, slug string) (role.Role, error) {
	r, err := scanRole(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+roleColumns+` FROM roles r
		WHERE r.account_id = $1 AND r.slug = $2`, acct, slug))
	return r, read(err, "role", slug)
}

// CreateRole records the custom role r and its actions.
func (s *Store) CreateRole(ctx context.Context, r role.Role) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if _, err := q.Exec(ctx, `INSERT INTO roles (account_id, id, slug, name, description,
				version) VALUES ($1, $2, $3, $4, $5, $6)`,
			r.AccountID, r.ID, r.Slug, r.Name, r.Description, r.Version); err != nil {
			return classify(err, "role")
		}
		if err := writeActions(ctx, q, r); err != nil {
			return err
		}
		return bumpAuthz(ctx, q, r.AccountID)
	})
	if err != nil {
		return fmt.Errorf("create role %s: %w", r.Slug, err)
	}
	return nil
}

// UpdateRole changes acct's role slug as edit answers. edit sees the role
// read under its row's lock, so the version it checks is the one the
// change replaces.
func (s *Store) UpdateRole(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
	edit func(role.Role) (role.Role, error),
) (role.Role, error) {
	var out role.Role
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		cur, err := lockRole(ctx, q, acct, slug)
		if err != nil {
			return err
		}
		if out, err = edit(cur); err != nil {
			return err
		}
		if err := q.QueryRow(ctx, `UPDATE roles SET name = $3, description = $4,
				version = version + 1 WHERE account_id = $1 AND id = $2 RETURNING version`,
			acct, cur.ID, out.Name, out.Description).Scan(&out.Version); err != nil {
			return fmt.Errorf("access: %w", sql.Unavailable(err))
		}
		if _, err := q.Exec(ctx, `DELETE FROM role_actions WHERE account_id = $1 AND role_id = $2`,
			acct, cur.ID); err != nil {
			return fmt.Errorf("access: %w", sql.Unavailable(err))
		}
		if err := writeActions(ctx, q, out); err != nil {
			return err
		}
		return bumpAuthz(ctx, q, acct)
	})
	if err != nil {
		return role.Role{}, fmt.Errorf("update role %s: %w", slug, err)
	}
	return out, nil
}

// DeleteRole deletes acct's role slug and its actions when check accepts
// it and it has no bindings.
func (s *Store) DeleteRole(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
	check func(role.Role) error,
) (role.Role, error) {
	var cur role.Role
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		var err error
		if cur, err = lockRole(ctx, q, acct, slug); err != nil {
			return err
		}
		if err := check(cur); err != nil {
			return err
		}
		// The lock on the role's row holds off a new binding, whose
		// foreign key reads the row with a share lock, so the count read
		// under it stays true until the delete.
		if cur.Bindings > 0 {
			return fmt.Errorf("%w: %s has %d", role.ErrInUse, slug, cur.Bindings)
		}
		if _, err := q.Exec(ctx, `DELETE FROM roles WHERE account_id = $1 AND id = $2`,
			acct, cur.ID); err != nil {
			return fmt.Errorf("access: %w", sql.Unavailable(err))
		}
		return bumpAuthz(ctx, q, acct)
	})
	if err != nil {
		return role.Role{}, fmt.Errorf("delete role %s: %w", slug, err)
	}
	return cur, nil
}

// RoleBindings reads the bindings of acct's role id to users, groups and
// API keys, oldest first, each with its space's slug.
func (s *Store) RoleBindings(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) ([]role.Listed, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT b.account_id, b.id, b.actor_kind, b.actor_id,
			b.role_id, b.space_id, b.origin, b.created_at, b.created_by, s.slug
		FROM role_bindings b JOIN spaces s ON s.account_id = b.account_id AND s.id = b.space_id
		WHERE b.account_id = $1 AND b.role_id = $2 AND b.actor_kind = ANY($3)
		ORDER BY b.created_at, b.id`, acct, id,
		[]string{string(role.ActorUser), string(role.ActorGroup), string(role.ActorAPIKey)})
	if err != nil {
		return nil, fmt.Errorf("access: list role bindings: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (role.Listed, error) {
		var l role.Listed
		err := r.Scan(&l.AccountID, &l.ID, &l.ActorKind, &l.ActorID, &l.RoleID, &l.SpaceID,
			&l.Origin, &l.CreatedAt, &l.CreatedBy, &l.Space)
		return l, err //nolint:wrapcheck // named below
	})
	if err != nil {
		return nil, fmt.Errorf("access: list role bindings: %w", sql.Unavailable(err))
	}
	return out, nil
}

// lockRole reads acct's role slug and locks its row.
func lockRole(
	ctx context.Context,
	q tx.Querier,
	acct kernel.AccountID,
	slug string,
) (role.Role, error) {
	r, err := scanRole(q.QueryRow(ctx, `SELECT `+roleColumns+` FROM roles r
		WHERE r.account_id = $1 AND r.slug = $2 FOR UPDATE`, acct, slug))
	return r, read(err, "role", slug)
}

// writeActions records r's actions, which have none stored.
func writeActions(ctx context.Context, q tx.Querier, r role.Role) error {
	actions := make([]string, 0, len(r.Actions))
	for _, a := range r.Actions {
		actions = append(actions, string(a))
	}
	if _, err := q.Exec(ctx, `INSERT INTO role_actions (account_id, role_id, action)
		SELECT $1, $2, unnest($3::text[])`, r.AccountID, r.ID, actions); err != nil {
		return fmt.Errorf("access: record the actions of role %s: %w", r.Slug, sql.Unavailable(err))
	}
	return nil
}
