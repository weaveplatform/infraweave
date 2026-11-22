package pg

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/policies/internal/sampling"
)

// AttachUploadJob keeps a snapshot, not an FK: queue pruning may remove a
// settled delivery without erasing historical sample metadata.
func (s *Store) AttachUploadJob(ctx context.Context, account kernel.AccountID, id kernel.ULID, job int64) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	_, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE policy_input_objects SET upload_job_id=$3 WHERE account_id=$1 AND id=$2`, account, id, job)
	if err != nil {
		return fmt.Errorf("policies: bind input upload delivery: %w", sql.Unavailable(err))
	}
	return nil
}

const retirementDue = `(i.expires_at<=$1
    OR (i.state IN ('pending','uploading') AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.id=i.upload_job_id AND j.account_id=i.account_id AND j.kind='policies.input-upload' AND j.state IN ('pending','running')))
    OR EXISTS(SELECT 1 FROM policy_evaluations e LEFT JOIN policies p ON p.account_id=e.account_id AND p.id=e.policy_id WHERE e.account_id=i.account_id AND e.sample_input_id=i.id AND p.id IS NULL))`

// DueInputs probes without row locks. Cleanup acquires each head before its input
// in its own transaction, then rechecks time and delivery/policy state.
func (s *Store) DueInputs(ctx context.Context, at time.Time, limit int) ([]sampling.Input, error) {
	rows, err := tx.Q(ctx, s.db).
		Query(ctx, `SELECT i.account_id,i.id FROM policy_input_objects i WHERE i.state IN ('pending','uploading','live') AND `+retirementDue+` ORDER BY i.expires_at,i.account_id,i.id LIMIT $2`, at, limit)
	if err != nil {
		return nil, fmt.Errorf("policies: probe due retained inputs: %w", sql.Unavailable(err))
	}
	return inputIdentities(rows)
}

// RetirementDue runs after the head/input fence, so a worker that won publication
// is observed before a dead-delivery probe is allowed to remove its sample.
func (s *Store) RetirementDue(ctx context.Context, input sampling.Input, at time.Time) (bool, error) {
	if !tx.In(ctx) {
		return false, queue.ErrNoTransaction
	}
	var due bool
	err := tx.Q(ctx, s.db).
		QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM policy_input_objects i WHERE i.account_id=$2 AND i.id=$3 AND i.state IN ('pending','uploading','live') AND `+retirementDue+`)`, at, input.Account, input.ID).
		Scan(&due)
	if err != nil {
		return false, fmt.Errorf("policies: recheck retained input cleanup: %w", sql.Unavailable(err))
	}
	return due, nil
}

func (s *Store) PolicyInputs(ctx context.Context, account kernel.AccountID, policy kernel.ULID, limit int) ([]sampling.Input, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT i.account_id,i.id FROM policy_input_objects i JOIN policy_evaluations e ON e.account_id=i.account_id AND e.sample_input_id=i.id
        WHERE e.account_id=$1 AND e.policy_id=$2 AND i.state IN ('pending','uploading','live') ORDER BY i.id LIMIT $3`, account, policy, limit)
	if err != nil {
		return nil, fmt.Errorf("policies: probe deleted policy samples: %w", sql.Unavailable(err))
	}
	return inputIdentities(rows)
}

func (s *Store) AccountInputs(ctx context.Context, account kernel.AccountID, limit int) ([]sampling.Input, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT account_id,id FROM policy_input_objects WHERE account_id=$1 ORDER BY id LIMIT $2`, account, limit)
	if err != nil {
		return nil, fmt.Errorf("policies: probe account input purge: %w", sql.Unavailable(err))
	}
	return inputIdentities(rows)
}

func inputIdentities(rows pgx.Rows) ([]sampling.Input, error) {
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (sampling.Input, error) {
		var input sampling.Input
		if err := row.Scan(&input.Account, &input.ID); err != nil {
			return input, fmt.Errorf("policies: scan retained input identity: %w", err)
		}
		return input, nil
	})
	if err != nil {
		return nil, fmt.Errorf("policies: read retained input probes: %w", sql.Unavailable(err))
	}
	return out, nil
}

// DropInput removes owned references before its row. The caller schedules blob
// deletion in this same transaction; capture head order is already held.
func (s *Store) DropInput(ctx context.Context, input sampling.Input) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	if input.SpaceSnapshot != nil {
		_, err := tx.Q(ctx, s.db).
			Exec(ctx, `UPDATE policy_input_capture_heads SET current_input_id=CASE WHEN current_input_id=$4 THEN NULL ELSE current_input_id END,pending_input_id=CASE WHEN pending_input_id=$4 THEN NULL ELSE pending_input_id END
            WHERE account_id=$1 AND space_snapshot_id=$2 AND capture_kind=$3`, input.Account, input.SpaceSnapshot, input.CaptureKind, input.ID)
		if err != nil {
			return fmt.Errorf("policies: unlink purged capture: %w", sql.Unavailable(err))
		}
	}
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM policy_evaluations WHERE account_id=$1 AND sample_input_id=$2`, input.Account, input.ID); err != nil {
		return fmt.Errorf("policies: unlink purged sample: %w", sql.Unavailable(err))
	}
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM policy_input_objects WHERE account_id=$1 AND id=$2`, input.Account, input.ID); err != nil {
		return fmt.Errorf("policies: remove purged input: %w", sql.Unavailable(err))
	}
	return nil
}

// PurgeMetadata removes bounded account-owned metadata after its inputs are gone.
// Policy deletion never resets account quotas; only account deletion removes them.
func (s *Store) PurgeMetadata(ctx context.Context, account kernel.AccountID, limit int) (bool, error) {
	if !tx.In(ctx) {
		return false, queue.ErrNoTransaction
	}
	for _, query := range []string{
		`DELETE FROM policy_evaluations WHERE account_id=$1 AND id IN (SELECT id FROM policy_evaluations WHERE account_id=$1 LIMIT $2)`,
		`DELETE FROM policy_input_capture_heads WHERE account_id=$1 AND (space_snapshot_id,capture_kind) IN (SELECT space_snapshot_id,capture_kind FROM policy_input_capture_heads WHERE account_id=$1 LIMIT $2)`,
		`DELETE FROM policy_sample_budgets WHERE account_id=$1 AND (hour,bucket) IN (SELECT hour,bucket FROM policy_sample_budgets WHERE account_id=$1 LIMIT $2)`,
	} {
		tag, err := tx.Q(ctx, s.db).Exec(ctx, query, account, limit)
		if err != nil {
			return false, fmt.Errorf("policies: purge evidence metadata: %w", sql.Unavailable(err))
		}
		if tag.RowsAffected() > 0 {
			return false, nil
		}
	}
	return true, nil
}
