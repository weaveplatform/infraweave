package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var _ binding.Store = (*Store)(nil)

// bindingColumns read a binding as b with its role's and space's slugs.
const bindingColumns = `SELECT b.account_id, b.id, b.actor_kind, b.actor_id, b.role_id,
		b.space_id, b.origin, b.created_at, b.created_by, b.version, r.slug, s.slug
	FROM role_bindings b
	JOIN roles r ON r.account_id = b.account_id AND r.id = b.role_id
	JOIN spaces s ON s.account_id = b.account_id AND s.id = b.space_id`

func scanBinding(row pgx.Row) (binding.Listed, error) {
	var l binding.Listed
	err := row.Scan(&l.AccountID, &l.ID, &l.ActorKind, &l.ActorID, &l.RoleID, &l.SpaceID,
		&l.Origin, &l.CreatedAt, &l.CreatedBy, &l.Version, &l.Role, &l.Space)
	return l, err //nolint:wrapcheck // callers name the read
}

func (s *Store) bindings(ctx context.Context, query string, args ...any) ([]binding.Listed, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, bindingColumns+` WHERE `+query+
		` ORDER BY b.created_at, b.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("access: list bindings: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (binding.Listed, error) {
		return scanBinding(r)
	})
	if err != nil {
		return nil, fmt.Errorf("access: list bindings: %w", sql.Unavailable(err))
	}
	return out, nil
}

// RoleID is the ID of acct's role slug.
func (s *Store) RoleID(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
) (kernel.ULID, error) {
	var id kernel.ULID
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT id FROM roles WHERE account_id = $1 AND slug = $2`,
		acct, slug).Scan(&id)
	return id, read(err, "role", slug)
}

// SpaceID is the ID of acct's space slug.
func (s *Store) SpaceID(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
) (kernel.ULID, error) {
	var id kernel.ULID
	err := tx.Q(ctx, s.db).
		QueryRow(ctx, `SELECT id FROM spaces WHERE account_id = $1 AND slug = $2`,
			acct, slug).
		Scan(&id)
	return id, read(err, "space", slug)
}

// actorTables are the query of each bindable actor kind that finds one
// of the account's, by ID: an admitted member, a group, or an API key not
// deleted.
var actorTables = map[binding.ActorKind]string{
	binding.ActorUser: `SELECT EXISTS (SELECT 1 FROM account_users
		WHERE account_id = $1 AND member_id = $2)`,
	binding.ActorGroup: `SELECT EXISTS (SELECT 1 FROM managed_user_groups
		WHERE account_id = $1 AND id = $2)`,
	binding.ActorAPIKey: `SELECT EXISTS (SELECT 1 FROM api_keys
		WHERE account_id = $1 AND id = $2 AND deleted_at IS NULL)`,
}

// HasActor reports whether acct holds the actor of kind id; a kind the
// account does not keep, a stack's or a Flows project's, is never held
// here.
func (s *Store) HasActor(
	ctx context.Context,
	acct kernel.AccountID,
	kind binding.ActorKind,
	id kernel.ULID,
) (bool, error) {
	query, ok := actorTables[kind]
	if !ok {
		return false, nil
	}
	var held bool
	if err := tx.Q(ctx, s.db).QueryRow(ctx, query, acct, id).Scan(&held); err != nil {
		return false, fmt.Errorf("access: read %s %s: %w", kind, id, sql.Unavailable(err))
	}
	return held, nil
}

// Binding reads acct's binding id of an actor of kind.
func (s *Store) Binding(
	ctx context.Context,
	acct kernel.AccountID,
	kind binding.ActorKind,
	id kernel.ULID,
) (binding.Listed, error) {
	l, err := scanBinding(tx.Q(ctx, s.db).QueryRow(ctx, bindingColumns+`
		WHERE b.account_id = $1 AND b.actor_kind = $2 AND b.id = $3`, acct, kind, id))
	return l, read(err, "binding", id)
}

// ActorBindings reads the bindings of acct's actor of kind id, oldest
// first.
func (s *Store) ActorBindings(
	ctx context.Context,
	acct kernel.AccountID,
	kind binding.ActorKind,
	id kernel.ULID,
) ([]binding.Listed, error) {
	return s.bindings(ctx, `b.account_id = $1 AND b.actor_kind = $2 AND b.actor_id = $3`,
		acct, kind, id)
}

// SpaceBindings reads the bindings in acct's space id, oldest first.
func (s *Store) SpaceBindings(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) ([]binding.Listed, error) {
	return s.bindings(ctx, `b.account_id = $1 AND b.space_id = $2`, acct, id)
}

// CreateBindings records bs in one transaction, which a context carrying
// one joins.
func (s *Store) CreateBindings(
	ctx context.Context,
	acct kernel.AccountID,
	bs []binding.Binding,
) ([]binding.Listed, error) {
	ids := make([]kernel.ULID, 0, len(bs))
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		for i, b := range bs {
			if err := insertBinding(ctx, q, b, i); err != nil {
				return err
			}
			ids = append(ids, b.ID)
		}
		return bumpAuthz(ctx, q, acct)
	})
	if err != nil {
		return nil, fmt.Errorf("create bindings: %w", err)
	}
	out, err := s.bindings(ctx, `b.account_id = $1 AND b.id = ANY($2)`, acct, ids)
	if err != nil {
		return nil, err
	}
	// The read orders by creation, which one transaction's rows share.
	byID := make(map[kernel.ULID]binding.Listed, len(out))
	for _, l := range out {
		byID[l.ID] = l
	}
	for i, id := range ids {
		out[i] = byID[id]
	}
	return out, nil
}

// insertBinding records b, the index'th binding of its write; one its
// actor holds already is a *binding.Duplicate.
func insertBinding(ctx context.Context, q tx.Querier, b binding.Binding, index int) error {
	_, err := q.Exec(ctx, `INSERT INTO role_bindings (account_id, id, actor_kind, actor_id,
			role_id, space_id, origin, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		b.AccountID, b.ID, b.ActorKind, b.ActorID, b.RoleID, b.SpaceID, b.Origin, b.CreatedBy)
	if duplicate(err) {
		return &binding.Duplicate{Index: index}
	}
	if err != nil {
		return fmt.Errorf("access: record binding: %w", sql.Unavailable(err))
	}
	return nil
}

func duplicate(err error) bool {
	pe, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pe.Code == "23505"
}

// UpdateBinding changes acct's binding id as edit answers. edit sees the
// binding read under its row's lock, so the version it checks is the one
// the change replaces.
func (s *Store) UpdateBinding(
	ctx context.Context,
	acct kernel.AccountID,
	kind binding.ActorKind,
	id kernel.ULID,
	edit func(binding.Listed) (binding.Binding, error),
) (binding.Listed, error) {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		cur, err := lockBinding(ctx, q, acct, kind, id)
		if err != nil {
			return err
		}
		b, err := edit(cur)
		if err != nil {
			return err
		}
		_, err = q.Exec(ctx, `UPDATE role_bindings SET role_id = $3, space_id = $4,
				version = version + 1 WHERE account_id = $1 AND id = $2`,
			acct, id, b.RoleID, b.SpaceID)
		if duplicate(err) {
			return &binding.Duplicate{}
		}
		if err != nil {
			return fmt.Errorf("access: %w", sql.Unavailable(err))
		}
		return bumpAuthz(ctx, q, acct)
	})
	if err != nil {
		return binding.Listed{}, fmt.Errorf("update binding %s: %w", id, err)
	}
	return s.Binding(ctx, acct, kind, id)
}

// DeleteBinding deletes acct's binding id when check accepts it.
func (s *Store) DeleteBinding(
	ctx context.Context,
	acct kernel.AccountID,
	kind binding.ActorKind,
	id kernel.ULID,
	check func(binding.Listed) error,
) (binding.Listed, error) {
	var cur binding.Listed
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		var err error
		if cur, err = lockBinding(ctx, q, acct, kind, id); err != nil {
			return err
		}
		if err := check(cur); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `DELETE FROM role_bindings WHERE account_id = $1 AND id = $2`,
			acct, id); err != nil {
			return fmt.Errorf("access: %w", sql.Unavailable(err))
		}
		return bumpAuthz(ctx, q, acct)
	})
	if err != nil {
		return binding.Listed{}, fmt.Errorf("delete binding %s: %w", id, err)
	}
	return cur, nil
}

// ReplaceAccessRules replaces the access_rule bindings of acct's actor of
// kind id with bs, in one transaction.
func (s *Store) ReplaceAccessRules(
	ctx context.Context,
	acct kernel.AccountID,
	kind binding.ActorKind,
	id kernel.ULID,
	bs []binding.Binding,
) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if _, err := q.Exec(ctx, `DELETE FROM role_bindings WHERE account_id = $1
				AND actor_kind = $2 AND actor_id = $3 AND origin = $4`,
			acct, kind, id, binding.OriginAccessRule); err != nil {
			return fmt.Errorf("access: %w", sql.Unavailable(err))
		}
		for i, b := range bs {
			if err := insertBinding(ctx, q, b, i); err != nil {
				return err
			}
		}
		return bumpAuthz(ctx, q, acct)
	})
	if err != nil {
		return fmt.Errorf("replace the access rules of %s %s: %w", kind, id, err)
	}
	return nil
}

// DeleteActorBindings deletes every binding of acct's actor of kind id.
func (s *Store) DeleteActorBindings(
	ctx context.Context,
	acct kernel.AccountID,
	kind binding.ActorKind,
	id kernel.ULID,
) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if err := unbindActor(ctx, q, acct, kind, id); err != nil {
			return err
		}
		return bumpAuthz(ctx, q, acct)
	})
	if err != nil {
		return fmt.Errorf("delete the bindings of %s %s: %w", kind, id, err)
	}
	return nil
}

// unbindActor deletes the bindings of acct's actor of kind id, in the
// transaction that removes the actor.
func unbindActor(
	ctx context.Context,
	q tx.Querier,
	acct kernel.AccountID,
	kind binding.ActorKind,
	id kernel.ULID,
) error {
	if _, err := q.Exec(ctx, `DELETE FROM role_bindings
		WHERE account_id = $1 AND actor_kind = $2 AND actor_id = $3`, acct, kind, id); err != nil {
		return fmt.Errorf(
			"access: delete the bindings of %s %s: %w",
			kind,
			id,
			sql.Unavailable(err),
		)
	}
	return nil
}

// lockBinding reads acct's binding id of an actor of kind and locks its
// row.
func lockBinding(
	ctx context.Context,
	q tx.Querier,
	acct kernel.AccountID,
	kind binding.ActorKind,
	id kernel.ULID,
) (binding.Listed, error) {
	l, err := scanBinding(q.QueryRow(ctx, bindingColumns+`
		WHERE b.account_id = $1 AND b.actor_kind = $2 AND b.id = $3 FOR UPDATE OF b`,
		acct, kind, id))
	return l, read(err, "binding", id)
}

// Atomic runs fn in one transaction, which every store call fn makes
// through the context it passes joins, so an actor and the bindings its
// input carries are written together or not at all.
func (s *Store) Atomic(ctx context.Context, fn func(context.Context) error) error {
	return tx.Do(ctx, s.db, tx.Options{}, fn) //nolint:wrapcheck // fn's calls name their errors
}
