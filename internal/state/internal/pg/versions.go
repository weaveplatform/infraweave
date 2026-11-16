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
	"github.com/weaveplatform/infraweave/internal/state/internal/core"
)

// runText is the text of v's run's ID as the API answers it, the
// Crockford base32 kernel.ULID.String writes, so a constraint names a run
// as its answers do: the uuid's 128 bits behind two zero bits, read five
// at a time. A version written by no run has none, which no constraint on
// it keeps.
const runText = `(SELECT string_agg(substr('0123456789ABCDEFGHJKMNPQRSTVWXYZ',
	substring(u.bits FROM n * 5 + 1 FOR 5)::bit(5)::int + 1, 1), '' ORDER BY n)
	FROM (SELECT B'00' || ('x' || translate(v.run_id::text, '-', ''))::bit(128) AS bits) u
	CROSS JOIN generate_series(0, 25) AS n)`

// versionColumn is the SQL of each of core.VersionFields.
var versionColumn = map[string]sqlsearch.Column{
	"createdAt": {Expr: "v.created_at", Cast: "timestamptz"},
	"run":       {Expr: runText, Text: true},
	"rollback":  {Expr: "v.rollback"},
	"imported":  {Expr: "v.imported"},
}

// versionSearch is the managed_state_versions table as core.VersionFields
// search it, newest first. A version's space is its stack's, which belongs
// to the stacks domain, so the scope binds it as $4.
var versionSearch = func() sqlsearch.Entity {
	e := sqlsearch.Entity{
		List: "managed-state-versions", From: "managed_state_versions v",
		Select: versionColumns, Space: "$4::uuid", ID: "v.id::text",
		Order: []search.Order{{Field: "createdAt", Desc: true}},
	}
	for _, f := range core.VersionFields {
		c := versionColumn[f.Name]
		c.Field = f
		e.Columns = append(e.Columns, c)
	}
	return e
}()

// versionScope is the versions of scope's workspace, which the caller
// may read in its stack's space.
func versionScope(scope core.VersionScope) sqlsearch.Scope {
	t := scope.Target
	return sqlsearch.Scope{
		Where:     "v.account_id = $1 AND v.stack_id = $2 AND v.workspace = $3",
		Args:      []any{t.Account, t.Stack, t.Workspace, scope.Space},
		Reachable: []kernel.ULID{scope.Space},
	}
}

// SearchVersions reads a page of the versions of scope's workspace that
// in keeps.
func (s *Store) SearchVersions(ctx context.Context, in search.Input, scope core.VersionScope) (search.Result[core.Version], error) {
	out, err := sqlsearch.Find(ctx, tx.Q(ctx, s.db), versionSearch, versionScope(scope), in,
		scanVersion)
	if err != nil {
		return out, fmt.Errorf("state: %w", err)
	}
	return out, nil
}

// RolledBack lists those of acct's stacks whose default workspace's
// current version is a rollback.
func (s *Store) RolledBack(ctx context.Context, acct kernel.AccountID, stacks []kernel.ULID) ([]kernel.ULID, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT w.stack_id FROM state_workspaces w
		JOIN managed_state_versions v ON v.account_id = w.account_id AND v.id = w.current_version_id
		WHERE w.account_id = $1 AND w.stack_id = ANY($2) AND w.name = '' AND v.rollback`,
		acct, stacks)
	if err != nil {
		return nil, fmt.Errorf("state: read the stacks rolled back: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[kernel.ULID])
	if err != nil {
		return nil, fmt.Errorf("state: read the stacks rolled back: %w", sql.Unavailable(err))
	}
	return out, nil
}
