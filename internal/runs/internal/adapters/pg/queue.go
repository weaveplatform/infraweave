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
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
)

var _ read.QueueStore = (*Store)(nil)

// publicQueued is the condition a run of the public pool's queue meets
// beyond its state, the one ProbePublicCandidate's claim reads: no private
// pool, an organization to count its quota against, normal priority and
// no drift detection.
const publicQueued = `r.worker_pool_id IS NULL AND r.organization_id IS NOT NULL
	AND NOT r.drift_detection AND r.priority = 'normal'`

// queueColumn is the SQL of each of read.QueueFields over a queue's rows.
var queueColumn = map[string]sqlsearch.Column{
	"commit":           {Expr: "coalesce(q.commit->>'hash', '')", Cast: "text", Text: true},
	"createdAt":        {Expr: "q.created_at", Cast: "timestamptz"},
	"isDriftDetection": {Expr: "q.drift_detection"},
	"isPrioritized":    {Expr: "(q.priority = 'high')", Cast: "boolean"},
	"position":         {Expr: "q.position", Cast: "bigint"},
	"priorityPreset":   {Expr: "q.priority", Cast: "text"},
	"stackName":        {Expr: "q.stack_name", Cast: "text", Text: true},
	"state":            {Expr: "q.state", Cast: "text"},
	"type":             {Expr: "q.type", Cast: "text"},
}

// queueSearch is a queue's runs as read.QueueFields search them. Its rows
// number the scope's runs in the claim's order, which orders a private
// pool's tiers first and the public pool by creation alone, so a run's
// position counts only runs the caller may read. $1 is the account, $2 to
// $5 the scope's stacks' IDs, spaces, slugs and names, $6 the states, and
// a private pool's $7 the pool.
func queueSearch(pool, order string) sqlsearch.Entity {
	e := sqlsearch.Entity{
		List: "pool-queue",
		From: `(SELECT r.*, st.space AS stack_space, st.name AS stack_name,
				row_number() OVER (ORDER BY ` + order + `) AS position
			FROM runs r JOIN unnest($2::uuid[], $3::uuid[], $4::text[], $5::text[])
				AS st(stack, space, slug, name) ON st.stack = r.stack_id
			WHERE r.account_id = $1 AND r.state = ANY($6) AND ` + pool + `) q`,
		Select: runColumns + ", position", Space: "q.stack_space", ID: "q.id::text",
		Order: []search.Order{{Field: "position"}},
	}
	for _, f := range read.QueueFields {
		c := queueColumn[f.Name]
		c.Field = f
		e.Columns = append(e.Columns, c)
	}
	return e
}

var (
	privateQueue = queueSearch("r.worker_pool_id = $7", "r.priority_tier, r.created_at, r.id")
	publicQueue  = queueSearch(publicQueued, "r.created_at, r.id")
)

// queueScope is scope as the search of acct's queue q reads it, and the
// entity that reads it.
func queueScope(acct kernel.AccountID, q read.Queue, states []kernel.RunState, scope read.Scope) (sqlsearch.Entity, sqlsearch.Scope) {
	spaces := make([]sqlsearch.SpaceNode, len(scope.Spaces))
	for i, n := range scope.Spaces {
		spaces[i] = sqlsearch.SpaceNode{Space: n.ID, Parent: n.Parent, ID: n.Slug, Display: n.Name}
	}
	ids := make([]kernel.ULID, len(scope.Stacks))
	homes := make([]kernel.ULID, len(scope.Stacks))
	slugs := make([]string, len(scope.Stacks))
	titles := make([]string, len(scope.Stacks))
	for i, st := range scope.Stacks {
		ids[i], homes[i], slugs[i], titles[i] = st.ID, st.Space, st.Slug, st.Name
	}
	out := sqlsearch.Scope{
		Args:      []any{acct, ids, homes, slugs, titles, names(states)},
		Reachable: scope.Reachable,
		Spaces:    spaces,
	}
	if q.Pool == nil {
		return publicQueue, out
	}
	out.Args = append(out.Args, *q.Pool)
	return privateQueue, out
}

// SearchQueue reads a page of acct's queue q's runs in states and scope
// that in keeps.
func (s *Store) SearchQueue(
	ctx context.Context,
	acct kernel.AccountID,
	q read.Queue,
	states []kernel.RunState,
	in search.Input,
	scope read.Scope,
) (search.Result[read.Queued], error) {
	e, sc := queueScope(acct, q, states, scope)
	out, err := sqlsearch.Find(ctx, tx.Q(ctx, s.db), e, sc, in,
		func(row pgx.Row, extra ...any) (read.Queued, error) {
			var v read.Queued
			r, err := scanRun(keyed{row, append([]any{&v.Position}, extra...)})
			v.Run = r
			return v, err
		})
	if err != nil {
		return out, fmt.Errorf("runs: %w", err)
	}
	return out, nil
}

// SuggestQueue lists the values acct's queue q's runs in states and scope
// that in keeps hold.
func (s *Store) SuggestQueue(
	ctx context.Context,
	acct kernel.AccountID,
	q read.Queue,
	states []kernel.RunState,
	in search.SuggestionsInput,
	scope read.Scope,
) (search.Suggestions, error) {
	e, sc := queueScope(acct, q, states, scope)
	out, err := sqlsearch.Suggest(ctx, tx.Q(ctx, s.db), e, sc, in)
	if err != nil {
		return out, fmt.Errorf("runs: %w", err)
	}
	return out, nil
}

// stackIDs are the IDs of scope's stacks.
func stackIDs(scope read.Scope) []kernel.ULID {
	ids := make([]kernel.ULID, len(scope.Stacks))
	for i, st := range scope.Stacks {
		ids[i] = st.ID
	}
	return ids
}

// CountQueues counts acct's runs in states on scope's stacks for each of
// pools.
func (s *Store) CountQueues(ctx context.Context, acct kernel.AccountID, pools []kernel.ULID, states []kernel.RunState, scope read.Scope) (map[kernel.ULID]int, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT r.worker_pool_id, count(*) FROM runs r
		WHERE r.account_id = $1 AND r.stack_id = ANY($2) AND r.state = ANY($3)
			AND r.worker_pool_id = ANY($4)
		GROUP BY r.worker_pool_id`, acct, stackIDs(scope), names(states), pools)
	if err != nil {
		return nil, fmt.Errorf("runs: count the pools' queues: %w", sql.Unavailable(err))
	}
	out := map[kernel.ULID]int{}
	var (
		pool kernel.ULID
		n    int
	)
	if _, err := pgx.ForEachRow(rows, []any{&pool, &n}, func() error {
		out[pool] = n
		return nil
	}); err != nil {
		return nil, fmt.Errorf("runs: count the pools' queues: %w", sql.Unavailable(err))
	}
	return out, nil
}

// CountPublic counts acct's public runs on scope's stacks that wait in
// states, and those a public worker holds in one of held.
func (s *Store) CountPublic(ctx context.Context, acct kernel.AccountID, states, held []kernel.RunState, scope read.Scope) (int, int, error) {
	var waiting, busy int
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT
			count(*) FILTER (WHERE r.state = ANY($3) AND `+publicQueued+`),
			count(*) FILTER (WHERE r.state = ANY($4) AND r.worker_pool_id IS NULL
				AND r.public_worker_pool_id IS NOT NULL)
		FROM runs r WHERE r.account_id = $1 AND r.stack_id = ANY($2)`,
		acct, stackIDs(scope), names(states), names(held)).Scan(&waiting, &busy)
	if err != nil {
		return 0, 0, fmt.Errorf("runs: count the public pool's runs: %w", sql.Unavailable(err))
	}
	return waiting, busy, nil
}
