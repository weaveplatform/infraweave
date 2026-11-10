package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var _ authz.Store = (*Store)(nil)

// bumpAuthz marks acct's authorization changed in the transaction that
// changes it: authz_version moves, and forgetAuthz has every replica
// rebuild its snapshot once the transaction commits. Every write to
// spaces, roles, role_actions, role_bindings or managed_user_groups that
// a snapshot reads, and every admission, calls it.
func (s *Store) bumpAuthz(ctx context.Context, q tx.Querier, acct kernel.AccountID) error {
	if _, err := q.Exec(ctx, `UPDATE accounts SET authz_version = authz_version + 1
		WHERE id = $1`, acct); err != nil {
		return fmt.Errorf("access: bump authorization: %w", sql.Unavailable(err))
	}
	return s.forgetAuthz(ctx, q, acct)
}

// forgetAuthz sends authz:<account> on infraweave_invalidate, which
// PostgreSQL delivers when the transaction commits, and has this replica
// forget its own snapshot of acct at the commit rather than when the
// message comes back, so the change's own answer and the caller's next
// request here are decided on the committed rows. A deleted account has
// no version left to move, so its deletion calls this alone.
func (s *Store) forgetAuthz(ctx context.Context, q tx.Querier, acct kernel.AccountID) error {
	if err := notify.Notify(ctx, q, notify.Invalidate, authz.Key(acct)); err != nil {
		return fmt.Errorf("access: %w", err)
	}
	if s.forget != nil {
		tx.AfterCommit(ctx, func(context.Context) { s.forget(acct) })
	}
	return nil
}

// AuthzData reads acct's authorization rows in one serializable
// transaction, so its version and rows agree; an account with no tree is
// port.ErrNotFound.
func (s *Store) AuthzData(ctx context.Context, acct kernel.AccountID) (authz.Data, error) {
	d := authz.Data{Account: acct}
	err := tx.Do(ctx, s.db, tx.Options{Serializable: true}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if err := q.QueryRow(ctx, `SELECT authz_version FROM accounts WHERE id = $1`, acct).
			Scan(&d.Version); err != nil {
			return read(err, "account", acct.ULID)
		}
		var err error
		if d.Spaces, err = collect(
			ctx,
			q,
			`SELECT id, coalesce(parent_id, '00000000-0000-0000-0000-000000000000'),
				inherit_entities FROM spaces WHERE account_id = $1`,
			acct,
			func(r pgx.CollectableRow) (authz.Space, error) {
				var sp authz.Space
				return sp, r.Scan(&sp.ID, &sp.Parent, &sp.Inherit)
			},
		); err != nil {
			return err
		}
		if d.Roles, err = collect(ctx, q, `SELECT r.id, r.slug, r.is_system,
				coalesce(array_agg(a.action ORDER BY a.action) FILTER (WHERE a.action IS NOT NULL), '{}')
			FROM roles r LEFT JOIN role_actions a ON a.role_id = r.id
			WHERE r.account_id = $1 GROUP BY r.id`, acct,
			func(r pgx.CollectableRow) (authz.Role, error) {
				var x authz.Role
				return x, r.Scan(&x.ID, &x.Slug, &x.System, &x.Actions)
			}); err != nil {
			return err
		}
		if d.Bindings, err = collect(ctx, q, `SELECT account_id, id, actor_kind, actor_id, role_id,
				space_id, origin, created_at, created_by
			FROM role_bindings WHERE account_id = $1 ORDER BY id`, acct,
			func(r pgx.CollectableRow) (binding.Binding, error) {
				var b binding.Binding
				return b, r.Scan(
					&b.AccountID,
					&b.ID,
					&b.ActorKind,
					&b.ActorID,
					&b.RoleID,
					&b.SpaceID,
					&b.Origin,
					&b.CreatedAt,
					&b.CreatedBy,
				)
			}); err != nil {
			return err
		}
		d.Groups, err = collect(ctx, q, `SELECT id, name FROM managed_user_groups
			WHERE account_id = $1`, acct,
			func(r pgx.CollectableRow) (authz.Group, error) {
				var g authz.Group
				return g, r.Scan(&g.ID, &g.Name)
			})
		return err
	})
	if err != nil {
		return authz.Data{}, fmt.Errorf("read authorization of account %s: %w", acct, err)
	}
	return d, nil
}

// AuthzVersions reads the authz_version of each account in accts that
// still exists.
func (s *Store) AuthzVersions(
	ctx context.Context,
	accts []kernel.AccountID,
) (map[kernel.AccountID]int64, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT id, authz_version FROM accounts
		WHERE id = ANY($1)`, accts)
	if err != nil {
		return nil, fmt.Errorf("access: read authorization versions: %w", sql.Unavailable(err))
	}
	out := map[kernel.AccountID]int64{}
	var (
		id kernel.AccountID
		v  int64
	)
	if _, err := pgx.ForEachRow(rows, []any{&id, &v}, func() error {
		out[id] = v
		return nil
	}); err != nil {
		return nil, fmt.Errorf("access: read authorization versions: %w", sql.Unavailable(err))
	}
	return out, nil
}

// collect runs query and scans each row with scan.
func collect[T any](
	ctx context.Context,
	q tx.Querier,
	query string,
	acct kernel.AccountID,
	scan func(pgx.CollectableRow) (T, error),
) ([]T, error) {
	rows, err := q.Query(ctx, query, acct)
	if err != nil {
		return nil, fmt.Errorf("access: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, scan)
	if err != nil {
		return nil, fmt.Errorf("access: %w", sql.Unavailable(err))
	}
	return out, nil
}
