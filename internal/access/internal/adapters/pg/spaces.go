package pg

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/space"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

const spaceColumns = `s.account_id, s.id, s.slug, s.name, s.description, s.labels,
	s.inherit_entities, s.parent_id, s.created_at`

func scanSpace(row pgx.Row) (space.Space, error) {
	var (
		s      space.Space
		parent *kernel.ULID
	)
	err := row.Scan(&s.AccountID, &s.ID, &s.Slug, &s.Name, &s.Description, &s.Labels,
		&s.InheritEntities, &parent, &s.CreatedAt)
	if parent != nil {
		s.ParentID = *parent
	}
	return s, err //nolint:wrapcheck // callers name the read
}

// seedSpaces records a's top space and legacy with their closure rows, in
// the transaction that creates a.
func seedSpaces(ctx context.Context, q tx.Querier, a account.Account) error {
	top, legacy := space.Seed(a)
	for _, s := range []space.Space{top, legacy} {
		if err := insertSpace(ctx, q, s); err != nil {
			return err
		}
	}
	return nil
}

// insertSpace records s and its closure rows: its parent's ancestors one
// level further, and itself at depth 0.
func insertSpace(ctx context.Context, q tx.Querier, s space.Space) error {
	var parent *kernel.ULID
	if !s.Top() {
		parent = &s.ParentID
	}
	if _, err := q.Exec(ctx, `INSERT INTO spaces (account_id, id, slug, name, description,
			labels, inherit_entities, parent_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, coalesce($9, now()))`,
		s.AccountID, s.ID, s.Slug, s.Name, s.Description, nonNil(s.Labels),
		s.InheritEntities, parent, nullTime(s.CreatedAt)); err != nil {
		return classify(err, "space")
	}
	if _, err := q.Exec(ctx, `INSERT INTO space_closure (account_id, ancestor_id,
			descendant_id, depth)
		SELECT $1::uuid, ancestor_id, $2::uuid, depth + 1 FROM space_closure WHERE descendant_id = $3
		UNION ALL SELECT $1::uuid, $2::uuid, $2::uuid, 0`, s.AccountID, s.ID, parent); err != nil {
		return fmt.Errorf("access: record space %s: %w", s.Slug, sql.Unavailable(err))
	}
	return nil
}

// lockTree locks acct's tree through its top space's row, so changes of
// one tree run one at a time. An account with no tree is not found.
func lockTree(ctx context.Context, q tx.Querier, acct kernel.AccountID) error {
	var id kernel.ULID
	err := q.QueryRow(ctx, `SELECT id FROM spaces
		WHERE account_id = $1 AND parent_id IS NULL FOR UPDATE`, acct).Scan(&id)
	return read(err, "account", acct.ULID)
}

// Spaces lists acct's spaces, top space first, then by depth and slug.
func (s *Store) Spaces(ctx context.Context, acct kernel.AccountID) ([]space.Space, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+spaceColumns+`
		FROM spaces s JOIN space_closure c
			ON c.descendant_id = s.id AND c.ancestor_id = $1
		WHERE s.account_id = $1 ORDER BY c.depth, s.slug`, acct)
	if err != nil {
		return nil, fmt.Errorf("access: list spaces: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (space.Space, error) {
		return scanSpace(r)
	})
	if err != nil {
		return nil, fmt.Errorf("access: list spaces: %w", sql.Unavailable(err))
	}
	return out, nil
}

// Space reads acct's space slug.
func (s *Store) Space(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
) (space.Space, error) {
	sp, err := scanSpace(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+spaceColumns+`
		FROM spaces s WHERE s.account_id = $1 AND s.slug = $2`, acct, slug))
	return sp, read(err, "space", slug)
}

// CreateSpace records sp below its parent, which must be a space of its
// account.
func (s *Store) CreateSpace(ctx context.Context, sp space.Space) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if err := lockTree(ctx, q, sp.AccountID); err != nil {
			return err
		}
		var parent kernel.ULID
		err := q.QueryRow(ctx, `SELECT id FROM spaces WHERE account_id = $1 AND id = $2`,
			sp.AccountID, sp.ParentID).Scan(&parent)
		if err != nil {
			return read(err, "space", sp.ParentID)
		}
		if err := insertSpace(ctx, q, sp); err != nil {
			return err
		}
		return bumpAuthz(ctx, q, sp.AccountID)
	})
	if err != nil {
		return fmt.Errorf("create space %s: %w", sp.Slug, err)
	}
	return nil
}

// MoveSpace moves acct's space slug below parentSlug. The closure rows
// that link the subtree to its old ancestors are replaced by the cross
// product of the new ancestors and the subtree.
func (s *Store) MoveSpace(
	ctx context.Context,
	acct kernel.AccountID,
	slug, parentSlug string,
	check func(ctx context.Context, moved, parent space.Space, m accessapi.SpaceMove) error,
) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if err := lockTree(ctx, q, acct); err != nil {
			return err
		}
		moved, err := s.Space(ctx, acct, slug)
		if err != nil {
			return err
		}
		parent, err := s.Space(ctx, acct, parentSlug)
		if err != nil {
			return err
		}
		m := accessapi.SpaceMove{Account: acct, Space: moved.ID}
		if m.Subtree, err = ids(ctx, q, `SELECT descendant_id FROM space_closure
			WHERE ancestor_id = $1 ORDER BY depth, descendant_id`, moved.ID); err != nil {
			return err
		}
		if slices.Contains(m.Subtree, parent.ID) {
			return &space.Refusal{Reasons: []string{fmt.Sprintf(
				"space %s cannot move below %s, which is inside it", moved.Slug, parent.Slug)}}
		}
		ancestors := `SELECT ancestor_id FROM space_closure
			WHERE descendant_id = $1 AND depth > $2 ORDER BY depth`
		if m.From, err = ids(ctx, q, ancestors, moved.ID, 0); err != nil {
			return err
		}
		if m.To, err = ids(ctx, q, ancestors, parent.ID, -1); err != nil {
			return err
		}
		if err := check(ctx, moved, parent, m); err != nil {
			return err
		}
		if moved.ParentID == parent.ID {
			return nil
		}
		for _, stmt := range []struct {
			sql  string
			args []any
		}{
			{
				`DELETE FROM space_closure
				WHERE descendant_id IN (SELECT descendant_id FROM space_closure WHERE ancestor_id = $1)
				AND ancestor_id NOT IN (SELECT descendant_id FROM space_closure WHERE ancestor_id = $1)`,
				[]any{moved.ID},
			},
			{
				`INSERT INTO space_closure (account_id, ancestor_id, descendant_id, depth)
				SELECT $3, a.ancestor_id, d.descendant_id, a.depth + d.depth + 1
				FROM space_closure a, space_closure d
				WHERE a.descendant_id = $2 AND d.ancestor_id = $1`,
				[]any{moved.ID, parent.ID, acct},
			},
			{
				`UPDATE spaces SET parent_id = $2 WHERE id = $1 AND account_id = $3`,
				[]any{moved.ID, parent.ID, acct},
			},
		} {
			if _, err := q.Exec(ctx, stmt.sql, stmt.args...); err != nil {
				return fmt.Errorf("access: %w", sql.Unavailable(err))
			}
		}
		return bumpAuthz(ctx, q, acct)
	})
	if err != nil {
		return fmt.Errorf("move space %s: %w", slug, err)
	}
	return nil
}

// DeleteSpace deletes acct's space slug once check allows it and it has
// no children. Only its own closure row remains by then.
func (s *Store) DeleteSpace(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
	check func(ctx context.Context, sp space.Space) error,
) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if err := lockTree(ctx, q, acct); err != nil {
			return err
		}
		sp, err := s.Space(ctx, acct, slug)
		if err != nil {
			return err
		}
		if err := check(ctx, sp); err != nil {
			return err
		}
		children, err := ids(ctx, q, `SELECT id FROM spaces
			WHERE account_id = $1 AND parent_id = $2 ORDER BY slug LIMIT 1`, acct, sp.ID)
		if err != nil {
			return err
		}
		if len(children) > 0 {
			return &space.Refusal{Reasons: []string{fmt.Sprintf(
				"space %s has child spaces", sp.Slug)}}
		}
		for _, stmt := range []string{
			`DELETE FROM space_closure WHERE descendant_id = $1`,
			`DELETE FROM spaces WHERE id = $1`,
		} {
			if _, err := q.Exec(ctx, stmt, sp.ID); err != nil {
				return fmt.Errorf("access: %w", sql.Unavailable(err))
			}
		}
		return bumpAuthz(ctx, q, acct)
	})
	if err != nil {
		return fmt.Errorf("delete space %s: %w", slug, err)
	}
	return nil
}

// ids runs query and collects its one column of IDs.
func ids(ctx context.Context, q tx.Querier, query string, args ...any) ([]kernel.ULID, error) {
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("access: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[kernel.ULID])
	if err != nil {
		return nil, fmt.Errorf("access: %w", sql.Unavailable(err))
	}
	return out, nil
}
