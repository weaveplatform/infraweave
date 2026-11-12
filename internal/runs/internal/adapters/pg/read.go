package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	sqlsearch "github.com/weaveplatform/infraweave/internal/platform/sql/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/control"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
)

var (
	_ read.Store    = (*Store)(nil)
	_ control.Store = (*Store)(nil)
)

// Get reads acct's run id.
func (s *Store) Get(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (lifecycle.Run, error) {
	r, err := scanRun(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+runColumns+` FROM runs
		WHERE account_id = $1 AND id = $2`, acct, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return lifecycle.Run{}, fmt.Errorf("%w: %s", read.ErrNotFound, id)
	case err != nil:
		return lifecycle.Run{}, fmt.Errorf("runs: read run %s: %w", id, sql.Unavailable(err))
	}
	return r, nil
}

// Histories reads the transitions of acct's runs, oldest first, keyed by
// run.
func (s *Store) Histories(
	ctx context.Context,
	acct kernel.AccountID,
	runs []kernel.ULID,
) (map[kernel.ULID][]lifecycle.Step, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT run_id, sequence, state, timestamp, until,
		username, note, has_logs FROM run_state_transitions
		WHERE account_id = $1 AND run_id = ANY($2) ORDER BY run_id, sequence`, acct, runs)
	if err != nil {
		return nil, fmt.Errorf("runs: read the runs' histories: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	out := make(map[kernel.ULID][]lifecycle.Step, len(runs))
	for rows.Next() {
		var (
			run   kernel.ULID
			st    lifecycle.Step
			state string
		)
		if err := rows.Scan(&run, &st.Sequence, &state, &st.At, &st.Until, &st.Username, &st.Note,
			&st.HasLogs); err != nil {
			return nil, fmt.Errorf("runs: read a run's history: %w", sql.Unavailable(err))
		}
		st.State, st.At = kernel.RunState(state), st.At.UTC()
		out[run] = append(out[run], st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("runs: read the runs' histories: %w", sql.Unavailable(err))
	}
	return out, nil
}

// List reads up to q.Limit+1 of acct's stack's runs of types, newest
// first: those older than q.After and q.Before when set, and, when q.To
// is set, those created from q.From until q.To.
func (s *Store) List(
	ctx context.Context,
	acct kernel.AccountID,
	stack kernel.ULID,
	types []kernel.RunType,
	q read.Query,
) ([]lifecycle.Run, error) {
	var from, to *time.Time
	if !q.To.IsZero() {
		from, to = &q.From, &q.To
	}
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+runColumns+` FROM runs
		WHERE account_id = $1 AND stack_id = $2 AND type = ANY($3)
			AND ($4::uuid IS NULL OR id < $4) AND ($5::uuid IS NULL OR id < $5)
			AND ($6::timestamptz IS NULL OR created_at >= $6)
			AND ($7::timestamptz IS NULL OR created_at < $7)
		ORDER BY id DESC LIMIT $8`,
		acct, stack, names(types), q.After, q.Before, from, to, q.Limit+1)
	if err != nil {
		return nil, fmt.Errorf("runs: list the runs of stack %s: %w", stack, sql.Unavailable(err))
	}
	defer rows.Close()
	var out []lifecycle.Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("runs: list a run of stack %s: %w", stack, sql.Unavailable(err))
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("runs: list the runs of stack %s: %w", stack, sql.Unavailable(err))
	}
	return out, nil
}

func names[T ~string](all []T) []string {
	out := make([]string, len(all))
	for i, v := range all {
		out[i] = string(v)
	}
	return out
}

// runColumn is the SQL of each of read.Fields. A run's stack and space
// belong to the stacks domain, so both read the scope's stacks, which $2
// to $4 bind as their IDs, spaces and slugs; a stack moved to another
// space carries its runs with it.
var runColumn = map[string]sqlsearch.Column{
	"id":             {Expr: "r.id::text", Text: true},
	"stack":          {Expr: "st.slug", Cast: "text", Text: true},
	"space":          {Expr: "st.space", SpaceTree: true},
	"type":           {Expr: "r.type"},
	"state":          {Expr: "r.state", Cast: "text"},
	"commit":         {Expr: "r.commit->>'hash'", Text: true},
	"triggeredBy":    {Expr: "r.triggered_by", Cast: "text", Text: true},
	"priority":       {Expr: "r.priority"},
	"driftDetection": {Expr: "r.drift_detection"},
	"createdAt":      {Expr: "r.created_at", Cast: "timestamptz"},
}

// runSearch is the runs table as read.Fields search it, newest first.
var runSearch = func() sqlsearch.Entity {
	e := sqlsearch.Entity{
		List: "runs",
		From: `runs r JOIN unnest($2::uuid[], $3::uuid[], $4::text[]) AS st(stack, space, slug)
			ON st.stack = r.stack_id`,
		Select: runColumns, Space: "st.space", ID: "r.id::text",
		Order: []search.Order{{Field: "createdAt", Desc: true}},
	}
	for _, f := range read.Fields {
		c := runColumn[f.Name]
		c.Field = f
		e.Columns = append(e.Columns, c)
	}
	return e
}()

// searchScope is scope as the search of acct's runs reads it.
func searchScope(acct kernel.AccountID, scope read.Scope) sqlsearch.Scope {
	spaces := make([]sqlsearch.SpaceNode, len(scope.Spaces))
	for i, n := range scope.Spaces {
		spaces[i] = sqlsearch.SpaceNode{Space: n.ID, Parent: n.Parent, ID: n.Slug, Display: n.Name}
	}
	ids := make([]kernel.ULID, len(scope.Stacks))
	homes := make([]kernel.ULID, len(scope.Stacks))
	slugs := make([]string, len(scope.Stacks))
	for i, st := range scope.Stacks {
		ids[i], homes[i], slugs[i] = st.ID, st.Space, st.Slug
	}
	return sqlsearch.Scope{
		Where:     "r.account_id = $1",
		Args:      []any{acct, ids, homes, slugs},
		Reachable: scope.Reachable,
		Spaces:    spaces,
	}
}

// scanFound reads a run of runSearch, then the keys the search reads back.
func scanFound(row pgx.Row, extra ...any) (lifecycle.Run, error) {
	return scanRun(keyed{row, extra})
}

// keyed is a row whose Scan reads the run's columns and then extra.
type keyed struct {
	pgx.Row
	extra []any
}

func (k keyed) Scan(dest ...any) error {
	return k.Row.Scan(append(dest, k.extra...)...) //nolint:wrapcheck // scanRun names the read
}

// Search reads a page of acct's runs in scope that in keeps.
func (s *Store) Search(
	ctx context.Context,
	acct kernel.AccountID,
	in search.Input,
	scope read.Scope,
) (search.Result[lifecycle.Run], error) {
	out, err := sqlsearch.Find(ctx, tx.Q(ctx, s.db), runSearch, searchScope(acct, scope), in,
		scanFound)
	if err != nil {
		return out, fmt.Errorf("runs: %w", err)
	}
	return out, nil
}

// Count counts acct's runs in scope that in keeps.
func (s *Store) Count(
	ctx context.Context,
	acct kernel.AccountID,
	in search.Input,
	scope read.Scope,
) (int, error) {
	n, err := sqlsearch.Count(ctx, tx.Q(ctx, s.db), runSearch, searchScope(acct, scope), in)
	if err != nil {
		return 0, fmt.Errorf("runs: %w", err)
	}
	return n, nil
}

// Suggest lists the values acct's runs in scope that in keeps hold.
func (s *Store) Suggest(
	ctx context.Context,
	acct kernel.AccountID,
	in search.SuggestionsInput,
	scope read.Scope,
) (search.Suggestions, error) {
	out, err := sqlsearch.Suggest(ctx, tx.Q(ctx, s.db), runSearch, searchScope(acct, scope), in)
	if err != nil {
		return out, fmt.Errorf("runs: %w", err)
	}
	return out, nil
}

// Holder reads the run that holds acct's stack's run lock, false when
// none does.
func (s *Store) Holder(
	ctx context.Context,
	acct kernel.AccountID,
	stack kernel.ULID,
) (kernel.ULID, bool, error) {
	var run kernel.ULID
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT run_id FROM stack_run_locks
		WHERE account_id = $1 AND stack_id = $2`, acct, stack).Scan(&run)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return kernel.ULID{}, false, nil
	case err != nil:
		return kernel.ULID{}, false, fmt.Errorf("runs: read the run lock of stack %s: %w",
			stack, sql.Unavailable(err))
	}
	return run, true, nil
}

// SetPriority writes run's priority, the priority_tier it puts the run
// in, and its updated_at, at.
func (s *Store) SetPriority(
	ctx context.Context,
	run lifecycle.Run,
	p kernel.RunPriority,
	at time.Time,
) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE runs
		SET priority = $3, priority_tier = $4, updated_at = $5
		WHERE account_id = $1 AND id = $2`,
		run.Account, run.ID, string(p), kernel.RunTier(p, run.Type, run.DriftDetection), at)
	if err != nil {
		return fmt.Errorf("runs: set the priority of run %s: %w", run.ID, sql.Unavailable(err))
	}
	return nil
}
