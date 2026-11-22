package pg

import (
	"context"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
	"github.com/weaveplatform/infraweave/internal/policies/internal/sampling"
)

// EvaluationPreviews returns the newest bounded safe history for each current
// readable policy. An attachment view additionally fixes the actual stack target.
func (s *Store) EvaluationPreviews(
	ctx context.Context,
	account kernel.AccountID,
	policies, readable []kernel.ULID,
	stack *kernel.ULID,
	at time.Time,
) (map[kernel.ULID][]core.EvaluationRecord, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT policy_id,id,outcome,created_at FROM (
 SELECT e.policy_id,e.id,lower(e.outcome) AS outcome,e.created_at,
 row_number() OVER(PARTITION BY e.policy_id ORDER BY e.created_at DESC,e.id ASC) AS position
 FROM policy_evaluations e JOIN policies p ON p.account_id=e.account_id AND p.id=e.policy_id
 WHERE e.account_id=$1 AND e.policy_id=ANY($2::uuid[]) AND p.space_id=ANY($3::uuid[])
 AND ($4::uuid IS NULL OR (e.target_kind=$5 AND e.target_id=$4))
 AND e.created_at>$6::timestamptz-interval '7 days'
 ) records WHERE position<=100 ORDER BY policy_id,created_at DESC,id ASC`, account, policies, readable, stack, string(policyapi.StackTarget), at)
	if err != nil {
		return nil, fmt.Errorf("policies: read evaluation previews: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	out := map[kernel.ULID][]core.EvaluationRecord{}
	for rows.Next() {
		var policy kernel.ULID
		var record core.EvaluationRecord
		if err := rows.Scan(&policy, &record.ID, &record.Outcome, &record.At); err != nil {
			return nil, fmt.Errorf("policies: scan evaluation preview: %w", sql.Unavailable(err))
		}
		out[policy] = append(out[policy], record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("policies: evaluation preview rows: %w", sql.Unavailable(err))
	}
	return out, nil
}

// CountableInputs pages all live candidates; the blob owner decides availability.
func (s *Store) CountableInputs(
	ctx context.Context,
	account kernel.AccountID,
	policies, readable []kernel.ULID,
	after *kernel.ULID,
	at time.Time,
	limit int,
) ([]sampling.CountableInput, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT e.id,e.policy_id,i.object_key
 FROM policy_evaluations e JOIN policies p ON p.account_id=e.account_id AND p.id=e.policy_id
 JOIN policy_input_objects i ON i.account_id=e.account_id AND i.id=e.sample_input_id
 WHERE e.account_id=$1 AND e.policy_id=ANY($2::uuid[]) AND p.space_id=ANY($3::uuid[])
 AND ($4::uuid IS NULL OR e.id>$4) AND i.published_at IS NOT NULL AND i.state='live'
 AND i.expires_at>$5 AND i.space_snapshot_id IS NULL ORDER BY e.id LIMIT $6`, account, policies, readable, after, at, limit)
	if err != nil {
		return nil, fmt.Errorf("policies: read retained count batch: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	var out []sampling.CountableInput
	for rows.Next() {
		var row sampling.CountableInput
		if err := rows.Scan(&row.ID, &row.Policy, &row.Key); err != nil {
			return nil, fmt.Errorf("policies: scan retained count batch: %w", sql.Unavailable(err))
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("policies: retained count batch rows: %w", sql.Unavailable(err))
	}
	return out, nil
}
