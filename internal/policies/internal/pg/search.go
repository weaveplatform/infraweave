package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	sqlsearch "github.com/weaveplatform/infraweave/internal/platform/sql/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
)

var policyColumns = map[string]sqlsearch.Column{
	"name":        {Expr: "p.name", Cast: "text", Text: true},
	"description": {Expr: "p.description", Text: true},
	"label":       {Expr: "p.labels", Many: true, Text: true},
	"space":       {Expr: "p.space_id", SpaceTree: true},
	"type":        {Expr: "p.type", Cast: "text"},
	"engineType":  {Expr: "p.engine_type", Cast: "text"},
	"createdAt":   {Expr: "p.created_at", Cast: "timestamptz"},
	"updatedAt":   {Expr: "p.updated_at", Cast: "timestamptz"},
}

var policySearch = func() sqlsearch.Entity {
	e := sqlsearch.Entity{List: "policies", From: "policies p", Select: columns, Space: "p.space_id", ID: "p.id::text", Order: []search.Order{{Field: "name"}}}
	for _, field := range core.Fields {
		column := policyColumns[field.Name]
		column.Field = field
		e.Columns = append(e.Columns, column)
	}
	return e
}()

func searchScope(acct kernel.AccountID, scope core.Scope) sqlsearch.Scope {
	spaces := make([]sqlsearch.SpaceNode, len(scope.Spaces))
	for i, n := range scope.Spaces {
		spaces[i] = sqlsearch.SpaceNode{Space: n.ID, Parent: n.Parent, ID: n.Slug, Display: n.Name}
	}
	return sqlsearch.Scope{Where: "p.account_id=$1", Args: []any{acct}, Reachable: scope.Reachable, Spaces: spaces}
}

func scan(row pgx.Row, extra ...any) (core.Policy, error) {
	var p core.Policy
	args := make([]any, 0, 13+len(extra))
	args = append(args, &p.Account, &p.ID, &p.Space, &p.Name, &p.Body, &p.Description, &p.Labels, &p.Type, &p.Engine, &p.Template, &p.CreatedAt, &p.UpdatedAt, &p.Version)
	err := row.Scan(append(args, extra...)...)
	return p, err //nolint:wrapcheck // callers name and classify the database failure
}

// Search filters readable rows in SQL before keyset pagination.
func (s *Store) Search(ctx context.Context, acct kernel.AccountID, in search.Input, scope core.Scope) (search.Result[core.Policy], error) {
	out, err := sqlsearch.Find(ctx, tx.Q(ctx, s.db), policySearch, searchScope(acct, scope), in, scan)
	if err != nil {
		return out, fmt.Errorf("policies: search: %w", err)
	}
	return out, nil
}

// Suggest counts only policies in the account and readable scope.
func (s *Store) Suggest(ctx context.Context, acct kernel.AccountID, in search.SuggestionsInput, scope core.Scope) (search.Suggestions, error) {
	out, err := sqlsearch.Suggest(ctx, tx.Q(ctx, s.db), policySearch, searchScope(acct, scope), in)
	if err != nil {
		return out, fmt.Errorf("policies: suggestions: %w", err)
	}
	return out, nil
}
