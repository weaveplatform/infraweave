package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/sampling"
)

var sampleStaging = crypt.Column{Table: "policy_input_objects", Name: "staging_sealed", Key: []string{"account_id", "id"}}

// SampleSealer uses the installation's local keyring, not another connection.
type SampleSealer interface {
	SealColumn(context.Context, crypt.Column, []string, []byte) ([]byte, error)
	OpenColumn(context.Context, crypt.Column, []string, []byte) (crypt.Value, error)
}

// NewWithSamples binds row-identity sealing during domain construction.
func NewWithSamples(db DB, sealer SampleSealer) *Store { return &Store{db: db, seal: sealer} }

// SealedColumns participate in key rotation even while an upload is pending.
func SealedColumns() []crypt.Column { return []crypt.Column{sampleStaging} }

// Reserve performs one conditional upsert against the database's UTC hour.
// A competing replica waits on that bucket row, then rechecks the same limit.
func (s *Store) Reserve(ctx context.Context, account kernel.AccountID, kind policyapi.Type) (bool, error) {
	if !tx.In(ctx) {
		return false, queue.ErrNoTransaction
	}
	bucket, limit := "OTHER", 500
	if kind == policyapi.Notification || kind == policyapi.Push {
		bucket, limit = string(kind), 200
	}
	var used int
	err := tx.Q(ctx, s.db).QueryRow(ctx, `INSERT INTO policy_sample_budgets(account_id,hour,bucket,used)
        VALUES($1,date_trunc('hour',statement_timestamp(),'UTC'),$2,1)
        ON CONFLICT(account_id,hour,bucket) DO UPDATE SET used=policy_sample_budgets.used+1
        WHERE policy_sample_budgets.used<$3 RETURNING used`, account, bucket, limit).Scan(&used)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("policies: reserve sample budget: %w", sql.Unavailable(err))
	}
	return true, nil
}

// CreateInput writes only ciphertext into staging, under this exact account/ID.
func (s *Store) CreateInput(ctx context.Context, input sampling.Input, body []byte) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	if s.seal == nil {
		return kernel.ErrUnavailable
	}
	sealed, err := s.seal.SealColumn(ctx, sampleStaging, []string{input.Account.UUID(), input.ID.UUID()}, body)
	if err != nil {
		return fmt.Errorf("policies: seal input staging: %w", err)
	}
	_, err = tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO policy_input_objects(account_id,id,object_key,state,staging_sealed,created_at,expires_at)
        VALUES($1,$2,$3,'pending',$4,$5,$6)`, input.Account, input.ID, input.Key, sealed, input.CreatedAt, input.ExpiresAt)
	if err != nil {
		return fmt.Errorf("policies: stage input: %w", sql.Unavailable(err))
	}
	return nil
}

// RecordEvaluation retains no plaintext input, source, result or print.
func (s *Store) RecordEvaluation(ctx context.Context, req policyapi.EvaluationRequest, space kernel.ULID, r policyapi.Receipt, input *sampling.Input) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	var id *kernel.ULID
	var key *string
	if input != nil {
		id, key = &input.ID, &input.Key
	}
	_, err := tx.Q(ctx, s.db).
		Exec(ctx, `INSERT INTO policy_evaluations(account_id,id,policy_id,policy_name,policy_version,space_id,type,engine_type,outcome,target_kind,target_id,run_id,sample_requested,sample_input_id,blob_key,created_at)
        VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, req.Account, r.ID, r.Policy, r.PolicyName, r.PolicyVersion, space, string(r.Type), r.Engine, string(r.Outcome), string(req.Target.Kind), req.Target.ID, req.Run, r.Sample, id, key, r.CreatedAt)
	if err != nil {
		return fmt.Errorf("policies: record evaluation metadata: %w", sql.Unavailable(err))
	}
	return nil
}

// Input locks immutable upload metadata when requested. Private staging never
// enters an error or metadata projection.
func (s *Store) Input(ctx context.Context, account kernel.AccountID, id kernel.ULID, lock bool) (sampling.Input, error) {
	query := `SELECT account_id,id,object_key,state,staging_sealed,created_at,expires_at,published_at FROM policy_input_objects WHERE account_id=$1 AND id=$2`
	if lock {
		if !tx.In(ctx) {
			return sampling.Input{}, queue.ErrNoTransaction
		}
		query += ` FOR UPDATE`
	}
	var out sampling.Input
	var sealed []byte
	err := tx.Q(ctx, s.db).QueryRow(ctx, query, account, id).Scan(&out.Account, &out.ID, &out.Key, &out.State, &sealed, &out.CreatedAt, &out.ExpiresAt, &out.PublishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, sampling.ErrNotFound
	}
	if err != nil {
		return out, fmt.Errorf("policies: read input upload: %w", sql.Unavailable(err))
	}
	if sealed != nil {
		if s.seal == nil {
			return sampling.Input{}, kernel.ErrUnavailable
		}
		out.Staging, err = s.seal.OpenColumn(ctx, sampleStaging, []string{account.UUID(), id.UUID()}, sealed)
		if err != nil {
			return sampling.Input{}, fmt.Errorf("policies: open input staging: %w", err)
		}
	}
	return out, nil
}

// StartUpload persists the one-shot marker before any external effect.
func (s *Store) StartUpload(ctx context.Context, account kernel.AccountID, id kernel.ULID) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	tag, err := tx.Q(ctx, s.db).
		Exec(ctx, `UPDATE policy_input_objects SET state='uploading',upload_started_at=statement_timestamp() WHERE account_id=$1 AND id=$2 AND state='pending'`, account, id)
	if err != nil {
		return fmt.Errorf("policies: start input upload: %w", sql.Unavailable(err))
	}
	if tag.RowsAffected() != 1 {
		return sampling.ErrNotFound
	}
	return nil
}

// FinishInput clears staging in the same fenced transaction as publication or
// retirement; a tombstone preserves whether a sample was previously retained.
func (s *Store) FinishInput(ctx context.Context, account kernel.AccountID, id kernel.ULID, state string, at time.Time) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	_, err := tx.Q(ctx, s.db).
		Exec(ctx, `UPDATE policy_input_objects SET state=$3,staging_sealed=NULL,published_at=CASE WHEN $3='live' THEN $4 ELSE published_at END WHERE account_id=$1 AND id=$2`, account, id, state, at)
	if err != nil {
		return fmt.Errorf("policies: finish input retention: %w", sql.Unavailable(err))
	}
	return nil
}

// References never opens staging or selects raw input. Missing and foreign IDs
// are absent; a deleted policy can retain an expired historical receipt link.
func (s *Store) References(ctx context.Context, account kernel.AccountID, ids []kernel.ULID) (map[kernel.ULID]sampling.Reference, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT e.id,i.id,i.object_key,i.state,i.created_at,i.expires_at,i.published_at,p.id IS NOT NULL
        FROM policy_evaluations e JOIN policy_input_objects i ON i.account_id=e.account_id AND i.id=e.sample_input_id
        LEFT JOIN policies p ON p.account_id=e.account_id AND p.id=e.policy_id
        WHERE e.account_id=$1 AND e.id=ANY($2)`, account, ids)
	if err != nil {
		return nil, fmt.Errorf("policies: read sample references: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	out := make(map[kernel.ULID]sampling.Reference)
	for rows.Next() {
		var id kernel.ULID
		var ref sampling.Reference
		ref.Input.Account = account
		if err := rows.Scan(
			&id,
			&ref.Input.ID,
			&ref.Input.Key,
			&ref.Input.State,
			&ref.Input.CreatedAt,
			&ref.Input.ExpiresAt,
			&ref.Input.PublishedAt,
			&ref.PolicyExists,
		); err != nil {
			return nil, fmt.Errorf("policies: scan sample references: %w", sql.Unavailable(err))
		}
		out[id] = ref
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("policies: read sample reference rows: %w", sql.Unavailable(err))
	}
	return out, nil
}
