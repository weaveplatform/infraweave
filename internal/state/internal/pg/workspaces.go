package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	sqlsearch "github.com/weaveplatform/infraweave/internal/platform/sql/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/state/internal/core"
)

// holder joins each workspace w to the lock that holds it as h: the
// exclusive lock when one is held, else the oldest, the lock core's
// holder picks for a conflict, so lockedBy names the run a 423 names. A
// workspace no lock holds has a NULL h.triggered_by.
//
// ref: decision 0010 § "Storage"
const holder = `LEFT JOIN LATERAL (SELECT l.triggered_by FROM state_locks l
	WHERE l.account_id = w.account_id AND l.stack_id = w.stack_id AND l.workspace = w.name
	ORDER BY l.mode = 'exclusive' DESC, l.created_at, l.lock_id LIMIT 1) h ON true`

// workspaceColumns are a workspace's columns as scanWorkspace reads them,
// over state_workspaces w and holder.
const workspaceColumns = `w.id, w.stack_id, w.name, w.created_at, h.triggered_by`

// scanWorkspace reads workspaceColumns, then into extra.
func scanWorkspace(row pgx.Row, extra ...any) (core.Workspace, error) {
	var (
		w  core.Workspace
		by *string
	)
	if err := row.Scan(append([]any{&w.ID, &w.Stack, &w.Name, &w.CreatedAt, &by},
		extra...)...); err != nil {
		return core.Workspace{}, err //nolint:wrapcheck // the caller names the read
	}
	w.CreatedAt = w.CreatedAt.UTC()
	if by != nil {
		w.Locked, w.TriggeredBy = true, *by
	}
	return w, nil
}

// WorkspaceByID reads acct's workspace id with the lock that holds it;
// false when acct lacks it.
func (s *Store) WorkspaceByID(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (core.Workspace, bool, error) {
	w, err := scanWorkspace(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+workspaceColumns+`
		FROM state_workspaces w `+holder+` WHERE w.account_id = $1 AND w.id = $2`, acct, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return core.Workspace{}, false, nil
	case err != nil:
		return core.Workspace{}, false, fmt.Errorf("state: read workspace %s: %w", id,
			sql.Unavailable(err))
	}
	return w, true, nil
}

// LockStackWorkspaces reads acct's stack's workspaces by name, locking
// each row FOR UPDATE in that order until the context's transaction ends,
// so two releases of one stack never wait on each other's rows. Each
// answers unlocked: the caller reads its locks once its row is held.
func (s *Store) LockStackWorkspaces(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) ([]core.Workspace, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT w.id, w.stack_id, w.name, w.created_at,
		NULL::text FROM state_workspaces w WHERE w.account_id = $1 AND w.stack_id = $2
		ORDER BY w.name FOR UPDATE`, acct, stack)
	if err != nil {
		return nil, fmt.Errorf("state: lock the workspaces of stack %s: %w", stack,
			sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (core.Workspace, error) {
		return scanWorkspace(row)
	})
	if err != nil {
		return nil, fmt.Errorf("state: lock the workspaces of stack %s: %w", stack,
			sql.Unavailable(err))
	}
	return out, nil
}

// workspaceColumn is the SQL of each of core.Fields. A workspace's stack
// and its space belong to the stacks domain, so both read the scope's
// stacks, which $2 to $4 bind as their IDs, spaces and slugs. The default
// workspace's empty name answers core.DefaultName, so a search filters
// and orders it by the name it answers.
var workspaceColumn = map[string]sqlsearch.Column{
	"name": {
		Expr: `CASE w.name WHEN '' THEN '` + core.DefaultName + `' ELSE w.name END`,
		Cast: "text", Text: true,
	},
	"stack":    {Expr: "st.slug", Cast: "text", Text: true},
	"isLocked": {Expr: "(h.triggered_by IS NOT NULL)"},
}

// workspaceSearch is the state_workspaces table as core.Fields search it,
// by name.
var workspaceSearch = func() sqlsearch.Entity {
	e := sqlsearch.Entity{
		List: "tofu-workspaces",
		From: `state_workspaces w JOIN unnest($2::uuid[], $3::uuid[], $4::text[])
			AS st(stack, space, slug) ON st.stack = w.stack_id ` + holder,
		Select: workspaceColumns, Space: "st.space", ID: "w.id::text",
		Order: []search.Order{{Field: "name"}},
	}
	for _, f := range core.Fields {
		c := workspaceColumn[f.Name]
		c.Field = f
		e.Columns = append(e.Columns, c)
	}
	return e
}()

// workspaceScope is scope as the search of acct's workspaces reads it.
func workspaceScope(acct kernel.AccountID, scope core.WorkspaceScope) sqlsearch.Scope {
	ids := make([]kernel.ULID, len(scope.Stacks))
	homes := make([]kernel.ULID, len(scope.Stacks))
	slugs := make([]string, len(scope.Stacks))
	for i, st := range scope.Stacks {
		ids[i], homes[i], slugs[i] = st.ID, st.Space, st.Slug
	}
	return sqlsearch.Scope{
		Where:     "w.account_id = $1",
		Args:      []any{acct, ids, homes, slugs},
		Reachable: scope.Reachable,
	}
}

// SearchWorkspaces reads a page of acct's workspaces in scope that in
// keeps.
func (s *Store) SearchWorkspaces(ctx context.Context, acct kernel.AccountID, in search.Input, scope core.WorkspaceScope) (search.Result[core.Workspace], error) {
	out, err := sqlsearch.Find(ctx, tx.Q(ctx, s.db), workspaceSearch,
		workspaceScope(acct, scope), in, scanWorkspace)
	if err != nil {
		return out, fmt.Errorf("state: %w", err)
	}
	return out, nil
}

// SuggestWorkspaces lists the values acct's workspaces in scope that in
// keeps hold.
func (s *Store) SuggestWorkspaces(ctx context.Context, acct kernel.AccountID, in search.SuggestionsInput, scope core.WorkspaceScope) (search.Suggestions, error) {
	out, err := sqlsearch.Suggest(ctx, tx.Q(ctx, s.db), workspaceSearch,
		workspaceScope(acct, scope), in)
	if err != nil {
		return out, fmt.Errorf("state: %w", err)
	}
	return out, nil
}
