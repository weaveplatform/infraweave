package pg

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
)

var _ evidence.PlanStore = (*Store)(nil)

// PlanOutcomes groups committed receipts by report transition. No-policy
// reports need no synthetic receipt: the latest PLANNING transition makes
// earlier outcomes historical, so removing policies cannot retain an old warn.
func (s *Store) PlanOutcomes(ctx context.Context, account kernel.AccountID, ids []kernel.ULID) (map[kernel.ULID][]evidence.PlanOutcome, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT p.run_id,p.id,p.state_version,p.created_at,
		CASE WHEN p.error_code IS NULL THEN p.decision->'deny' END,
		CASE WHEN p.error_code IS NULL THEN p.decision->'warn' END,
		CASE WHEN p.error_code IS NULL THEN p.decision->'info' END,
		p.error_code,p.error_message,t.state,
		coalesce(p.state_version=(SELECT max(sequence)+1 FROM run_state_transitions latest
			WHERE latest.account_id=p.account_id AND latest.run_id=p.run_id AND latest.state='PLANNING'),false)
		FROM run_policy_receipts p
		LEFT JOIN run_state_transitions t ON t.account_id=p.account_id AND t.run_id=p.run_id
			AND t.sequence=p.state_version-1
		WHERE p.account_id=$1 AND p.run_id=ANY($2) AND p.policy_type='PLAN'
		ORDER BY p.run_id,p.state_version,p.ordinal`, account, ids)
	if err != nil {
		return nil, fmt.Errorf("runs: read plan outcome messages: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	out := map[kernel.ULID][]evidence.PlanOutcome{}
	for rows.Next() {
		var run kernel.ULID
		var row evidence.PlanOutcome
		var raw [3][]byte
		var code, message, source *string
		if err := rows.Scan(&run, &row.ID, &row.StateVersion, &row.CreatedAt,
			&raw[0], &raw[1], &raw[2], &code, &message, &source, &row.Current); err != nil {
			return nil, fmt.Errorf("runs: scan plan outcome messages: %w", sql.Unavailable(err))
		}
		if source == nil || *source != string(kernel.RunStatePlanning) {
			return nil, fmt.Errorf("runs: invalid plan outcome source: %w", kernel.ErrUnavailable)
		}
		messages, err := planMessages(raw, code, message)
		if err != nil {
			return nil, err
		}
		row.CreatedAt = row.CreatedAt.UTC()
		out[run] = appendPlanOutcome(out[run], row, messages)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("runs: read plan outcome rows: %w", sql.Unavailable(err))
	}
	return out, nil
}

func planMessages(raw [3][]byte, code, message *string) ([3][]string, error) {
	out := [3][]string{{}, {}, {}}
	if code != nil || message != nil {
		if code == nil || message == nil || *code == "" || *message == "" {
			return out, fmt.Errorf("runs: invalid plan failure metadata: %w", kernel.ErrUnavailable)
		}
		out[0] = []string{*message}
		return out, nil
	}
	for i := range raw {
		if json.Unmarshal(raw[i], &out[i]) != nil || out[i] == nil {
			return out, fmt.Errorf("runs: invalid plan message metadata: %w", kernel.ErrUnavailable)
		}
	}
	return out, nil
}

func appendPlanOutcome(prior []evidence.PlanOutcome, row evidence.PlanOutcome, messages [3][]string) []evidence.PlanOutcome {
	if len(prior) == 0 || prior[len(prior)-1].StateVersion != row.StateVersion {
		prior = append(prior, row)
	}
	last := &prior[len(prior)-1]
	merge := func(current, next []string) []string {
		values := append(append([]string{}, current...), next...)
		slices.Sort(values)
		return slices.Compact(values)
	}
	last.Deny, last.Warn, last.Info = merge(last.Deny, messages[0]), merge(last.Warn, messages[1]), merge(last.Info, messages[2])
	return prior
}
