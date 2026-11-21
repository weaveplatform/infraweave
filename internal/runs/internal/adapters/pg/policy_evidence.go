package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policiesapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
)

var _ evidence.Store = (*Store)(nil)

// RecordPolicyEvidence holds the run's version fence while adding immutable
// receipts and updating only each evaluated policy's flag ownership rows.
func (s *Store) RecordPolicyEvidence(ctx context.Context, account kernel.AccountID, run kernel.ULID, version int64, receipts []policiesapi.Receipt) error {
	if !tx.In(ctx) {
		return lifecycle.ErrNoTransaction
	}
	var current int64
	var state string
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT state,state_version FROM runs WHERE account_id=$1 AND id=$2 FOR UPDATE`, account, run).Scan(&state, &current)
	if errors.Is(err, pgx.ErrNoRows) {
		return lifecycle.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("runs: fence policy evidence: %w", sql.Unavailable(err))
	}
	if current != version {
		return &lifecycle.Conflict{Run: run, State: kernel.RunState(state), Version: current}
	}
	var ordinal int64
	if err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT coalesce(max(ordinal),0) FROM run_policy_receipts WHERE account_id=$1 AND run_id=$2`, account, run).Scan(&ordinal); err != nil {
		return fmt.Errorf("runs: order policy evidence: %w", sql.Unavailable(err))
	}
	for _, receipt := range receipts {
		ordinal++
		if err := s.recordPolicyReceipt(ctx, account, run, version, ordinal, receipt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) recordPolicyReceipt(ctx context.Context, account kernel.AccountID, run kernel.ULID, version, ordinal int64, receipt policiesapi.Receipt) error {
	if receipt.ID.IsZero() || receipt.Policy.IsZero() {
		return evidence.ErrReceipt
	}
	decision := receipt.Decision
	if len(decision) == 0 {
		decision = json.RawMessage("null")
	}
	if !json.Valid(decision) {
		return evidence.ErrReceipt
	}
	var code, message *string
	if receipt.Failure != nil {
		code, message = &receipt.Failure.Code, &receipt.Failure.Message
	}
	_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO run_policy_receipts
		(account_id,run_id,id,state_version,ordinal,policy_id,policy_name,policy_version,policy_type,engine_type,
		outcome,decision,flags,reset_flags,sample_requested,error_code,error_message,created_at,sample_evaluation_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		account, run, receipt.ID, version, ordinal, receipt.Policy, receipt.PolicyName, receipt.PolicyVersion,
		string(receipt.Type), receipt.Engine, string(receipt.Outcome), []byte(decision),
		stringsOrEmpty(receipt.Flags), stringsOrEmpty(receipt.ResetFlags), receipt.Sample, code, message, receipt.CreatedAt, receipt.SampleEvaluation)
	if err != nil {
		return fmt.Errorf("runs: record policy evidence: %w", sql.Unavailable(err))
	}
	if _, err := tx.Q(ctx, s.db).
		Exec(ctx, `DELETE FROM run_flags WHERE account_id=$1 AND run_id=$2 AND policy_id=$3 AND flag=ANY($4)`, account, run, receipt.Policy, receipt.ResetFlags); err != nil {
		return fmt.Errorf("runs: reset policy-owned flags: %w", sql.Unavailable(err))
	}
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO run_flags(account_id,run_id,policy_id,flag,created_at)
		SELECT $1,$2,$3,flag,$5 FROM unnest($4::text[]) flag ON CONFLICT DO NOTHING`, account, run, receipt.Policy, receipt.Flags, receipt.CreatedAt); err != nil {
		return fmt.Errorf("runs: add policy-owned flags: %w", sql.Unavailable(err))
	}
	return nil
}

func stringsOrEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// PolicyEvidence reads only metadata and never selects a stored raw decision.
func (s *Store) PolicyEvidence(ctx context.Context, account kernel.AccountID, runs []kernel.ULID) (map[kernel.ULID]evidence.Snapshot, error) {
	out, err := s.evidenceRuns(ctx, account, runs)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT run_id,id,state_version,policy_id,policy_name,policy_version,
		policy_type,engine_type,outcome,flags,reset_flags,sample_requested,sample_evaluation_id,error_code,error_message,created_at
		FROM run_policy_receipts WHERE account_id=$1 AND run_id=ANY($2) ORDER BY run_id,ordinal`, account, runs)
	if err != nil {
		return nil, fmt.Errorf("runs: read policy evidence: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	for rows.Next() {
		var run kernel.ULID
		var receipt evidence.Receipt
		var kind, outcome string
		var code, message *string
		if err := rows.Scan(&run, &receipt.ID, &receipt.StateVersion, &receipt.Policy, &receipt.PolicyName, &receipt.PolicyVersion,
			&kind, &receipt.Engine, &outcome, &receipt.Flags, &receipt.ResetFlags, &receipt.SampleRequested,
			&receipt.SampleEvaluation, &code, &message, &receipt.CreatedAt); err != nil {
			return nil, fmt.Errorf("runs: scan policy evidence: %w", sql.Unavailable(err))
		}
		receipt.Type, receipt.Outcome = policiesapi.Type(kind), policiesapi.Outcome(outcome)
		receipt.CreatedAt = receipt.CreatedAt.UTC()
		if code != nil && message != nil {
			receipt.Failure = &policiesapi.Failure{Code: *code, Message: *message}
		}
		snapshot := out[run]
		snapshot.Receipts = append(snapshot.Receipts, receipt)
		out[run] = snapshot
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("runs: read policy evidence rows: %w", sql.Unavailable(err))
	}
	rows.Close()
	if err := s.evidenceFlags(ctx, account, runs, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) evidenceRuns(ctx context.Context, account kernel.AccountID, runs []kernel.ULID) (map[kernel.ULID]evidence.Snapshot, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT id FROM runs WHERE account_id=$1 AND id=ANY($2)`, account, runs)
	if err != nil {
		return nil, fmt.Errorf("runs: scope policy evidence: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	out := map[kernel.ULID]evidence.Snapshot{}
	for rows.Next() {
		var run kernel.ULID
		if err := rows.Scan(&run); err != nil {
			return nil, fmt.Errorf("runs: scan evidence scope: %w", sql.Unavailable(err))
		}
		out[run] = evidence.Empty()
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("runs: read evidence scope: %w", sql.Unavailable(err))
	}
	return out, nil
}

func (s *Store) evidenceFlags(ctx context.Context, account kernel.AccountID, runs []kernel.ULID, out map[kernel.ULID]evidence.Snapshot) error {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT DISTINCT run_id,flag FROM run_flags WHERE account_id=$1 AND run_id=ANY($2) ORDER BY run_id,flag`, account, runs)
	if err != nil {
		return fmt.Errorf("runs: read policy-owned flags: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	for rows.Next() {
		var run kernel.ULID
		var flag string
		if err := rows.Scan(&run, &flag); err != nil {
			return fmt.Errorf("runs: scan policy-owned flag: %w", sql.Unavailable(err))
		}
		snapshot := out[run]
		snapshot.Flags = append(snapshot.Flags, flag)
		out[run] = snapshot
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("runs: read policy-owned flag rows: %w", sql.Unavailable(err))
	}
	return nil
}
