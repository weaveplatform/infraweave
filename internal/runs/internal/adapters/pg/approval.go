package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
)

var _ evidence.ApprovalStore = (*Store)(nil)

// ApprovalAt reads historical evidence for a report whose response was lost.
func (s *Store) ApprovalAt(ctx context.Context, account kernel.AccountID, id kernel.ULID, version int64) (evidence.Approval, bool, error) {
	var gate evidence.Approval
	var state, outcome string
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT state,state_version,has_policies,outcome,evaluated_at
		FROM run_approval_gates WHERE account_id=$1 AND run_id=$2 AND state_version=$3`, account, id, version).
		Scan(&state, &gate.StateVersion, &gate.HasPolicies, &outcome, &gate.At)
	if errors.Is(err, pgx.ErrNoRows) {
		return gate, false, nil
	}
	if err != nil {
		return gate, false, fmt.Errorf("runs: read historical approval gate: %w", sql.Unavailable(err))
	}
	gate.State, gate.Outcome, gate.At = kernel.RunState(state), policyapi.Outcome(outcome), gate.At.UTC()
	if !gate.Valid() {
		return evidence.Approval{}, false, fmt.Errorf("runs: invalid historical approval gate: %w", kernel.ErrUnavailable)
	}
	return gate, true, nil
}

// RecordApproval replaces the aggregate without rewriting immutable receipts.
func (s *Store) RecordApproval(ctx context.Context, account kernel.AccountID, id kernel.ULID, gate evidence.Approval) error {
	if !tx.In(ctx) {
		return lifecycle.ErrNoTransaction
	}
	if !gate.Valid() {
		return evidence.ErrReceipt
	}
	run, err := s.Lock(ctx, account, id)
	if err != nil {
		return err //nolint:wrapcheck // the owner names the fence
	}
	if run.State != gate.State || run.Version != gate.StateVersion {
		return &lifecycle.Conflict{Run: id, State: run.State, Version: run.Version}
	}
	_, err = tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO run_approval_gates
		(account_id,run_id,state_version,state,has_policies,outcome,evaluated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (account_id,run_id,state_version) DO UPDATE SET
		has_policies=excluded.has_policies,outcome=excluded.outcome,evaluated_at=excluded.evaluated_at`,
		account, id, gate.StateVersion, string(gate.State), gate.HasPolicies, string(gate.Outcome), gate.At)
	if err != nil {
		return fmt.Errorf("runs: record approval gate: %w", sql.Unavailable(err))
	}
	return nil
}

// Approvals excludes historical states and scopes both sides of the join.
func (s *Store) Approvals(ctx context.Context, account kernel.AccountID, ids []kernel.ULID) (map[kernel.ULID]evidence.Approval, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT g.run_id,g.state,g.state_version,g.has_policies,g.outcome,g.evaluated_at
		FROM run_approval_gates g JOIN runs r ON r.account_id=g.account_id AND r.id=g.run_id
		AND r.state_version=g.state_version AND r.state=g.state
		WHERE g.account_id=$1 AND g.run_id=ANY($2)`, account, ids)
	if err != nil {
		return nil, fmt.Errorf("runs: read approval gates: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	out := map[kernel.ULID]evidence.Approval{}
	for rows.Next() {
		var id kernel.ULID
		var gate evidence.Approval
		var state, outcome string
		if err := rows.Scan(&id, &state, &gate.StateVersion, &gate.HasPolicies, &outcome, &gate.At); err != nil {
			return nil, fmt.Errorf("runs: scan approval gate: %w", sql.Unavailable(err))
		}
		gate.State, gate.Outcome, gate.At = kernel.RunState(state), policyapi.Outcome(outcome), gate.At.UTC()
		if !gate.Valid() {
			return nil, fmt.Errorf("runs: invalid stored approval gate: %w", kernel.ErrUnavailable)
		}
		out[id] = gate
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("runs: read approval gate rows: %w", sql.Unavailable(err))
	}
	return out, nil
}
