package pg

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	sqlsearch "github.com/weaveplatform/infraweave/internal/platform/sql/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/policies/internal/catalogue"
)

// templateRow contains only the public metadata used by the shared query engine.
// Lowercase tags are required by PostgreSQL's case-sensitive recordset mapping.
type templateRow struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Slug        string   `json:"slug"`
	Description string   `json:"description"`
	Type        string   `json:"type"`
	Labels      []string `json:"labels"`
}

var templateSearch = sqlsearch.Entity{
	List:   "policy-templates:" + catalogue.Revision,
	From:   "jsonb_to_recordset($1::jsonb) AS t(id text,name text,slug text,description text,type text,labels text[])",
	Select: "t.id", Space: "$2::uuid", ID: "t.id",
	Columns: []sqlsearch.Column{
		{Field: search.Field{Name: "name", Type: search.String, Orderable: true}, Expr: "t.name", Cast: "text", Text: true},
		{Field: search.Field{Name: "slug", Type: search.String, Orderable: true}, Expr: "t.slug", Cast: "text", Text: true},
		{Field: search.Field{Name: "description", Type: search.String}, Expr: "t.description", Text: true},
		{Field: search.Field{Name: "label", Type: search.String}, Expr: "t.labels", Many: true, Text: true},
		{
			Field: search.Field{
				Name:   "type",
				Type:   search.Enum,
				Values: []string{"APPROVAL", "GIT_PUSH", "INITIALIZATION", "INTENT", "LOGIN", "NOTIFICATION", "PLAN", "TASK", "TRIGGER"},
			},
			Expr: "t.type",
			Cast: "text",
		},
	},
	Order: []search.Order{{Field: "name"}},
}

func templateScope(account kernel.AccountID, templates []catalogue.Template) (sqlsearch.Scope, error) {
	rows := make([]templateRow, 0, len(templates))
	for _, template := range templates {
		labels := template.Labels
		if labels == nil {
			labels = []string{}
		}
		rows = append(rows, templateRow{ID: template.ID, Name: template.Name, Slug: template.Slug, Description: template.Description, Type: string(template.Type), Labels: labels})
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return sqlsearch.Scope{}, fmt.Errorf("policies: encode catalogue metadata: %w", err)
	}
	// Catalogue membership is immutable and public. The account sentinel adapts
	// the shared search engine's space predicate; it grants no root-space rights.
	// Core authorizes SPACE_READ in any real space before reaching this adapter.
	return sqlsearch.Scope{Args: []any{raw, account}, Reachable: []kernel.ULID{account.ULID}}, nil
}

// SearchTemplates reuses typed filtering and bidirectional keyset pagination
// over a parameterized public recordset, without persisting another catalogue.
func (s *Store) SearchTemplates(ctx context.Context, account kernel.AccountID, templates []catalogue.Template, in search.Input) (search.Result[catalogue.Template], error) {
	scope, err := templateScope(account, templates)
	if err != nil {
		return search.Result[catalogue.Template]{}, err
	}
	scan := func(row pgx.Row, extra ...any) (catalogue.Template, error) {
		var out catalogue.Template
		err := row.Scan(append([]any{&out.ID}, extra...)...)
		return out, err //nolint:wrapcheck // the shared search names the query
	}
	out, err := sqlsearch.Find(ctx, tx.Q(ctx, s.db), templateSearch, scope, in, scan)
	if err != nil {
		return out, fmt.Errorf("policies: search catalogue: %w", err)
	}
	return out, nil
}

// SuggestTemplates counts the same supported rows and filters as search.
func (s *Store) SuggestTemplates(ctx context.Context, account kernel.AccountID, templates []catalogue.Template, in search.SuggestionsInput) (search.Suggestions, error) {
	scope, err := templateScope(account, templates)
	if err != nil {
		return search.Suggestions{}, err
	}
	out, err := sqlsearch.Suggest(ctx, tx.Q(ctx, s.db), templateSearch, scope, in)
	if err != nil {
		return out, fmt.Errorf("policies: suggest catalogue: %w", err)
	}
	return out, nil
}
