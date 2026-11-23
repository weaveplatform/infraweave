package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	sqlsearch "github.com/weaveplatform/infraweave/internal/platform/sql/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

var _ core.UserStore = (*Store)(nil)

// userColumn is the SQL of each of core.UserFields. A stack is never a
// module; its space orders by the space's ID, since the tree's slugs
// belong to the access domain.
var userColumn = map[string]sqlsearch.Column{
	"isModule": {Expr: "false", Cast: "boolean"},
	"label":    stackColumn["label"],
	"name":     {Expr: "s.name", Cast: "text", Text: true},
	"slug":     {Expr: "s.slug", Cast: "text", Text: true},
	"space":    {Expr: "s.space_id", Cast: "uuid", SpaceTree: true},
}

// userSearch is the stacks table as core.UserFields search it.
var userSearch = func() sqlsearch.Entity {
	e := sqlsearch.Entity{
		List: "pool-users", From: "stacks s",
		Select: refColumns, Space: "s.space_id", ID: "s.slug",
		Order: []search.Order{{Field: "slug"}},
	}
	for _, f := range core.UserFields {
		c := userColumn[f.Name]
		c.Field = f
		e.Columns = append(e.Columns, c)
	}
	return e
}()

// usersScope is scope as the search of acct's stacks that select u's pool
// reads it.
func usersScope(acct kernel.AccountID, u core.Users, scope core.Scope) sqlsearch.Scope {
	spaces := make([]sqlsearch.SpaceNode, len(scope.Spaces))
	for i, n := range scope.Spaces {
		spaces[i] = sqlsearch.SpaceNode{Space: n.ID, Parent: n.Parent, ID: n.Slug, Display: n.Name}
	}
	out := sqlsearch.Scope{
		Where:     "s.account_id = $1 AND s.worker_pool_id IS NULL",
		Args:      []any{acct},
		Reachable: scope.Reachable,
		Spaces:    spaces,
	}
	if u.Pool != nil {
		out.Where, out.Args = "s.account_id = $1 AND s.worker_pool_id = $2", append(out.Args, *u.Pool)
	}
	return out
}

// SearchUsers reads a page of acct's stacks in the scope that select u's
// pool and that in keeps.
func (s *Store) SearchUsers(ctx context.Context, acct kernel.AccountID, u core.Users, in search.Input, scope core.Scope) (search.Result[stacksapi.Stack], error) {
	out, err := sqlsearch.Find(ctx, tx.Q(ctx, s.db), userSearch, usersScope(acct, u, scope), in,
		func(row pgx.Row, extra ...any) (stacksapi.Stack, error) {
			return scanRef(acct, keyedRow{row, extra})
		})
	if err != nil {
		return out, fmt.Errorf("stacks: %w", err)
	}
	return out, nil
}

// SuggestUsers lists the values acct's stacks in the scope that select
// u's pool and that in keeps hold.
func (s *Store) SuggestUsers(ctx context.Context, acct kernel.AccountID, u core.Users, in search.SuggestionsInput, scope core.Scope) (search.Suggestions, error) {
	out, err := sqlsearch.Suggest(ctx, tx.Q(ctx, s.db), userSearch, usersScope(acct, u, scope), in)
	if err != nil {
		return out, fmt.Errorf("stacks: %w", err)
	}
	return out, nil
}

// keyedRow is a row whose Scan reads a stack reference's columns and then
// extra.
type keyedRow struct {
	pgx.Row
	extra []any
}

func (k keyedRow) Scan(dest ...any) error {
	return k.Row.Scan(append(dest, k.extra...)...) //nolint:wrapcheck // scanRef's caller names the read
}

// PoolUsers lists acct's stacks in reachable that name each of pools, by
// slug.
func (s *Store) PoolUsers(ctx context.Context, acct kernel.AccountID, pools, reachable []kernel.ULID) (map[kernel.ULID][]workersapi.PoolUser, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT worker_pool_id, slug, name, space_id FROM stacks
		WHERE account_id = $1 AND worker_pool_id = ANY($2) AND space_id = ANY($3)
		ORDER BY worker_pool_id, slug`, acct, pools, reachable)
	if err != nil {
		return nil, fmt.Errorf("stacks: read the pools' stacks: %w", sql.Unavailable(err))
	}
	out := map[kernel.ULID][]workersapi.PoolUser{}
	var (
		pool kernel.ULID
		u    workersapi.PoolUser
	)
	if _, err := pgx.ForEachRow(rows, []any{&pool, &u.Slug, &u.Name, &u.Space}, func() error {
		out[pool] = append(out[pool], u)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("stacks: read the pools' stacks: %w", sql.Unavailable(err))
	}
	return out, nil
}

// CountPublicUsers counts acct's stacks in reachable that name no private
// pool.
func (s *Store) CountPublicUsers(ctx context.Context, acct kernel.AccountID, reachable []kernel.ULID) (int, error) {
	var n int
	if err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT count(*) FROM stacks
		WHERE account_id = $1 AND worker_pool_id IS NULL AND space_id = ANY($2)`,
		acct, reachable).Scan(&n); err != nil {
		return 0, fmt.Errorf("stacks: count the public pool's stacks: %w", sql.Unavailable(err))
	}
	return n, nil
}
