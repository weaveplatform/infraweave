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

// OwnsEvaluation resolves the requested tuple without selecting a private blob
// key, source, input, result or print. Policy ownership is checked separately.
func (s *Store) OwnsEvaluation(ctx context.Context, account kernel.AccountID, policy, evaluation kernel.ULID) (bool, error) {
	var found bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM policy_evaluations
        WHERE account_id=$1 AND policy_id=$2 AND id=$3)`, account, policy, evaluation).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("policies: evaluation ownership: %w", err)
	}
	return found, nil
}

func evaluationSearch(kind string) sqlsearch.Entity {
	column := func(name, expression string, typ search.Type, text bool) sqlsearch.Column {
		return sqlsearch.Column{Field: search.Field{Name: name, Type: typ}, Expr: expression, Text: text}
	}
	cols := []sqlsearch.Column{
		{Field: search.Field{Name: "createdAt", Type: search.Time, Orderable: true}, Expr: "e.created_at", Cast: "timestamptz"},
		column("outcome", "lower(e.outcome)", search.String, true),
	}
	switch kind {
	case "GIT_PUSH":
		cols = append(cols, column("pullRequestId", "e.pull_request_id::text", search.String, false), column("branch", "e.push_branch", search.String, true))
	case "PLAN", "NOTIFICATION":
		cols = append(cols, column("runType", "e.run_type", search.String, true), column("runDriftDetection", "e.run_drift_detection", search.Boolean, false))
		if kind == "NOTIFICATION" {
			cols = append(cols, column("module", "e.module_event", search.Boolean, false), column("moduleId", "e.module_id", search.String, true))
		}
	}
	if kind == "PLAN" || kind == "GIT_PUSH" || kind == "NOTIFICATION" {
		cols = append(cols, column("stackName", "e.stack_name", search.String, true), column("stackRepository", "e.stack_repository", search.String, true))
	}
	return sqlsearch.Entity{
		List: "policy-evaluations", From: "policy_evaluations e JOIN policies p ON p.account_id=e.account_id AND p.id=e.policy_id",
		Select: "e.id,lower(e.outcome),e.created_at", Space: "p.space_id", ID: "e.id::text", Columns: cols,
		Order: []search.Order{{Field: "createdAt", Desc: true}},
	}
}

func evaluationScope(account kernel.AccountID, policy core.Policy) sqlsearch.Scope {
	return sqlsearch.Scope{Where: "e.account_id=$1 AND e.policy_id=$2", Args: []any{account, policy.ID}, Reachable: []kernel.ULID{policy.Space}}
}

// SearchEvaluations applies account, parent and current-space predicates before
// filtering and keyset pagination. Metadata searches never open sealed objects.
func (s *Store) SearchEvaluations(ctx context.Context, account kernel.AccountID, policy core.Policy, in search.Input) (search.Result[core.EvaluationRecord], error) {
	scan := func(row pgx.Row, extra ...any) (core.EvaluationRecord, error) {
		var out core.EvaluationRecord
		err := row.Scan(append([]any{&out.ID, &out.Outcome, &out.At}, extra...)...)
		return out, err //nolint:wrapcheck // the query names the database operation
	}
	out, err := sqlsearch.Find(ctx, tx.Q(ctx, s.db), evaluationSearch(string(policy.Type)), evaluationScope(account, policy), in, scan)
	if err != nil {
		return out, fmt.Errorf("policies: search evaluations: %w", err)
	}
	return out, nil
}

// SuggestEvaluations counts the same fixed parent and readable space as search.
func (s *Store) SuggestEvaluations(ctx context.Context, account kernel.AccountID, policy core.Policy, in search.SuggestionsInput) (search.Suggestions, error) {
	out, err := sqlsearch.Suggest(ctx, tx.Q(ctx, s.db), evaluationSearch(string(policy.Type)), evaluationScope(account, policy), in)
	if err != nil {
		return out, fmt.Errorf("policies: suggest evaluations: %w", err)
	}
	return out, nil
}
