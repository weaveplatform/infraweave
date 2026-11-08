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
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	sqlsearch "github.com/weaveplatform/infraweave/internal/platform/sql/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

const spaceColumns = `s.account_id, s.id, s.slug, s.name, s.description, s.labels,
	s.inherit_entities, s.parent_id, s.created_at`

// scanSpace reads spaceColumns, then into extra.
func scanSpace(row pgx.Row, extra ...any) (space.Space, error) {
	var (
		s      space.Space
		parent *kernel.ULID
	)
	err := row.Scan(append([]any{
		&s.AccountID, &s.ID, &s.Slug, &s.Name, &s.Description, &s.Labels,
		&s.InheritEntities, &parent, &s.CreatedAt,
	}, extra...)...)
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

// HoldSpace takes FOR KEY SHARE on acct's space id in the context's
// transaction, the lock PostgreSQL's own key check takes: a delete or
// move, which locks the row FOR UPDATE before asking its guards, waits
// for the holder to commit and then sees what it wrote.
//
// ref: decision 0004
func (s *Store) HoldSpace(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error {
	var got kernel.ULID
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT id FROM spaces
		WHERE account_id = $1 AND id = $2 FOR KEY SHARE`, acct, id).Scan(&got)
	return read(err, "space", id)
}

// lockSpaces locks acct's spaces ids FOR UPDATE, after the tree, so a
// delete or move waits for every transaction holding one, and the guards
// it asks next, in statements of their own, see what those committed.
func lockSpaces(ctx context.Context, q tx.Querier, acct kernel.AccountID, ids []kernel.ULID) error {
	if _, err := q.Exec(ctx, `SELECT id FROM spaces WHERE account_id = $1 AND id = ANY($2)
		ORDER BY id FOR UPDATE`, acct, ids); err != nil {
		return fmt.Errorf("access: lock spaces: %w", sql.Unavailable(err))
	}
	return nil
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

// spaceColumn is the SQL of each of space.Fields.
var spaceColumn = map[string]sqlsearch.Column{
	"id":              {Expr: "s.slug", Cast: "text", Text: true},
	"name":            {Expr: "s.name", Cast: "text", Text: true},
	"description":     {Expr: "s.description", Text: true},
	"label":           {Expr: "s.labels", Many: true, Text: true},
	"inheritEntities": {Expr: "s.inherit_entities", Cast: "boolean"},
	"parentSpace":     {Expr: "p.slug"},
	// A node is a space by slug, and $1 is the account the scope names,
	// since every account has a legacy.
	"space": {
		Expr: "s.id",
		Tree: `SELECT c.descendant_id FROM space_closure c JOIN spaces a ON a.id = c.ancestor_id
			WHERE a.account_id = $1 AND a.slug = ANY(%[1]s::text[]) AND (%[2]s::boolean OR c.depth = 0)`,
		// Each node's parent is its nearest reachable ancestor, so no
		// slug the caller may not read is named.
		Nodes: `SELECT n.id::text, n.slug, (SELECT a.slug FROM space_closure c
				JOIN spaces a ON a.id = c.ancestor_id
				WHERE c.descendant_id = n.id AND c.depth > 0 AND a.id = ANY(%[1]s::uuid[])
				ORDER BY c.depth LIMIT 1), n.name
			FROM spaces n WHERE n.id = ANY(%[1]s::uuid[]) ORDER BY n.slug`,
	},
}

// spaceSearch is the spaces table as space.Fields search it.
var spaceSearch = func() sqlsearch.Entity {
	e := sqlsearch.Entity{
		List: "spaces", From: "spaces s LEFT JOIN spaces p ON p.id = s.parent_id",
		Select: spaceColumns, Space: "s.id", ID: "s.slug",
		Order: []search.Order{{Field: "id"}},
	}
	for _, f := range space.Fields {
		c := spaceColumn[f.Name]
		c.Field = f
		e.Columns = append(e.Columns, c)
	}
	return e
}()

// Search reads a page of acct's spaces among reachable that in keeps.
func (s *Store) Search(
	ctx context.Context,
	acct kernel.AccountID,
	in search.Input,
	reachable []kernel.ULID,
) (search.Result[space.Space], error) {
	out, err := sqlsearch.Find(ctx, tx.Q(ctx, s.db), spaceSearch, sqlsearch.Scope{
		Where: "s.account_id = $1", Args: []any{acct}, Reachable: reachable,
	}, in, scanSpace)
	if err != nil {
		return out, fmt.Errorf("access: %w", err)
	}
	return out, nil
}

// Suggest lists the values acct's spaces among reachable that in keeps
// hold.
func (s *Store) Suggest(
	ctx context.Context,
	acct kernel.AccountID,
	in search.SuggestionsInput,
	reachable []kernel.ULID,
) (search.Suggestions, error) {
	out, err := sqlsearch.Suggest(ctx, tx.Q(ctx, s.db), spaceSearch, sqlsearch.Scope{
		Where: "s.account_id = $1", Args: []any{acct}, Reachable: reachable,
	}, in)
	if err != nil {
		return out, fmt.Errorf("access: %w", err)
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

// UpdateSpace changes acct's space slug as edit answers, and moves it
// below parentSlug when that names a new parent: the closure rows that
// link the subtree to its old ancestors are replaced by the cross product
// of the new ancestors and the subtree. A move or a change of inheritance
// changes what grants reach, so it bumps the account's authorization.
func (s *Store) UpdateSpace(
	ctx context.Context,
	acct kernel.AccountID,
	slug, parentSlug string,
	edit func(
		ctx context.Context,
		current, parent space.Space,
		m *accessapi.SpaceMove,
	) (space.Space, error),
) (space.Space, error) {
	var out space.Space
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if err := lockTree(ctx, q, acct); err != nil {
			return err
		}
		cur, err := s.Space(ctx, acct, slug)
		if err != nil {
			return err
		}
		var parent space.Space
		if parentSlug != "" {
			if parent, err = s.Space(ctx, acct, parentSlug); err != nil {
				return err
			}
		}
		var m *accessapi.SpaceMove
		if parentSlug != "" && parent.ID != cur.ParentID {
			if m, err = move(ctx, q, cur, parent); err != nil {
				return err
			}
			if err := lockSpaces(ctx, q, acct, m.Subtree); err != nil {
				return err
			}
		}
		if out, err = edit(ctx, cur, parent, m); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `UPDATE spaces SET name = $3, description = $4, labels = $5,
				inherit_entities = $6 WHERE id = $1 AND account_id = $2`,
			cur.ID, acct, out.Name, out.Description, nonNil(out.Labels),
			out.InheritEntities); err != nil {
			return fmt.Errorf("access: %w", sql.Unavailable(err))
		}
		if m != nil {
			if err := reparent(ctx, q, acct, cur.ID, parent.ID); err != nil {
				return err
			}
		}
		if m == nil && out.InheritEntities == cur.InheritEntities {
			return nil
		}
		return bumpAuthz(ctx, q, acct)
	})
	if err != nil {
		return space.Space{}, fmt.Errorf("update space %s: %w", slug, err)
	}
	return out, nil
}

// move is the move of moved's subtree below parent, refused when parent
// is inside it.
func move(
	ctx context.Context,
	q tx.Querier,
	moved, parent space.Space,
) (*accessapi.SpaceMove, error) {
	m := accessapi.SpaceMove{Account: moved.AccountID, Space: moved.ID}
	var err error
	if m.Subtree, err = ids(ctx, q, `SELECT descendant_id FROM space_closure
		WHERE ancestor_id = $1 ORDER BY depth, descendant_id`, moved.ID); err != nil {
		return nil, err
	}
	if slices.Contains(m.Subtree, parent.ID) {
		return nil, &space.Refusal{Reasons: []string{fmt.Sprintf(
			"space %s cannot move below %s, which is inside it", moved.Slug, parent.Slug)}}
	}
	ancestors := `SELECT ancestor_id FROM space_closure
		WHERE descendant_id = $1 AND depth > $2 ORDER BY depth`
	if m.From, err = ids(ctx, q, ancestors, moved.ID, 0); err != nil {
		return nil, err
	}
	if m.To, err = ids(ctx, q, ancestors, parent.ID, -1); err != nil {
		return nil, err
	}
	return &m, nil
}

// reparent moves the subtree of acct's space id below parent.
func reparent(
	ctx context.Context,
	q tx.Querier,
	acct kernel.AccountID,
	id, parent kernel.ULID,
) error {
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{
			`DELETE FROM space_closure
			WHERE descendant_id IN (SELECT descendant_id FROM space_closure WHERE ancestor_id = $1)
			AND ancestor_id NOT IN (SELECT descendant_id FROM space_closure WHERE ancestor_id = $1)`,
			[]any{id},
		},
		{
			`INSERT INTO space_closure (account_id, ancestor_id, descendant_id, depth)
			SELECT $3, a.ancestor_id, d.descendant_id, a.depth + d.depth + 1
			FROM space_closure a, space_closure d
			WHERE a.descendant_id = $2 AND d.ancestor_id = $1`,
			[]any{id, parent, acct},
		},
		{
			`UPDATE spaces SET parent_id = $2 WHERE id = $1 AND account_id = $3`,
			[]any{id, parent, acct},
		},
	} {
		if _, err := q.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			return fmt.Errorf("access: %w", sql.Unavailable(err))
		}
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
		if err := lockSpaces(ctx, q, acct, []kernel.ULID{sp.ID}); err != nil {
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

// NonAdminCreation reads acct's allowNonAdminSpaceCreation.
func (s *Store) NonAdminCreation(ctx context.Context, acct kernel.AccountID) (bool, error) {
	var on bool
	err := tx.Q(ctx, s.db).QueryRow(ctx,
		`SELECT allow_non_admin_space_creation FROM accounts WHERE id = $1`, acct).Scan(&on)
	return on, read(err, "account", acct.ULID)
}

// ToggleNonAdminCreation flips the setting in one statement, so two flips
// at once are two flips.
func (s *Store) ToggleNonAdminCreation(ctx context.Context, acct kernel.AccountID) (bool, error) {
	var on bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, `UPDATE accounts
		SET allow_non_admin_space_creation = NOT allow_non_admin_space_creation
		WHERE id = $1 RETURNING allow_non_admin_space_creation`, acct).Scan(&on)
	return on, read(err, "account", acct.ULID)
}
