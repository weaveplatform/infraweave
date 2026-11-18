package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/feedback"
)

// Deployment reads the first recorded binding without opening credentials.
func (s *Store) Deployment(ctx context.Context, acct kernel.AccountID, run kernel.ULID) (feedback.Deployment, error) {
	var body []byte
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT binding FROM vcs_deployment_runs WHERE account_id=$1 AND run_id=$2`, acct, run).Scan(&body)
	return deployment(body, err)
}

func deployment(body []byte, err error) (feedback.Deployment, error) {
	var d feedback.Deployment
	if errors.Is(err, pgx.ErrNoRows) {
		return d, feedback.ErrNoDeployment
	}
	if err != nil {
		return d, fmt.Errorf("VCS deployment read: %w", sql.Unavailable(err))
	}
	if err := json.Unmarshal(body, &d); err != nil {
		return d, fmt.Errorf("VCS deployment binding: %w", err)
	}
	return d, nil
}

// BindDeployment fixes the first binding and returns the winning record.
func (s *Store) BindDeployment(ctx context.Context, d feedback.Deployment) (feedback.Deployment, error) {
	if !tx.In(ctx) {
		return feedback.Deployment{}, queue.ErrNoTransaction
	}
	body, err := json.Marshal(d)
	if err != nil {
		return feedback.Deployment{}, fmt.Errorf("VCS deployment encode: %w", err)
	}
	_, err = tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO vcs_deployment_runs(account_id,run_id,binding)
		VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, d.Account, d.Run.ID, body)
	if err != nil {
		return feedback.Deployment{}, fmt.Errorf("VCS deployment bind: %w", sql.Unavailable(err))
	}
	return s.Deployment(ctx, d.Account, d.Run.ID)
}

// RequestDeployment records durable per-run/state uniqueness in its enqueue transaction.
func (s *Store) RequestDeployment(ctx context.Context, r feedback.DeploymentRequest) (bool, error) {
	if !tx.In(ctx) {
		return false, queue.ErrNoTransaction
	}
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO vcs_deployment_states(account_id,run_id,state)
		VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, r.Deployment.Account, r.Deployment.Run.ID, r.State)
	if err != nil {
		return false, fmt.Errorf("VCS deployment request: %w", sql.Unavailable(err))
	}
	return tag.RowsAffected() == 1, nil
}

// ReserveDeployment advances the shared resource owner by first eligibility.
func (s *Store) ReserveDeployment(ctx context.Context, d feedback.Deployment, resource string) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO vcs_deployment_resources(account_id,resource,epoch_at,created_at,run_id)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT(account_id,resource) DO UPDATE SET
		epoch_at=excluded.epoch_at,created_at=excluded.created_at,run_id=excluded.run_id,sequence=0
		WHERE (excluded.epoch_at,excluded.created_at,excluded.run_id) >
		(vcs_deployment_resources.epoch_at,vcs_deployment_resources.created_at,vcs_deployment_resources.run_id)`,
		d.Account, resource, d.Epoch, d.CreatedAt, d.Run.ID)
	if err != nil {
		return fmt.Errorf("VCS deployment reserve: %w", sql.Unavailable(err))
	}
	return nil
}

// HoldDeployment serializes a bounded provider effect against later reservations.
func (s *Store) HoldDeployment(ctx context.Context, acct kernel.AccountID, resource string) (feedback.DeploymentCursor, error) {
	if !tx.In(ctx) {
		return feedback.DeploymentCursor{}, queue.ErrNoTransaction
	}
	var c feedback.DeploymentCursor
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT run_id,sequence FROM vcs_deployment_resources
		WHERE account_id=$1 AND resource=$2 FOR UPDATE`, acct, resource).Scan(&c.Run, &c.Sequence)
	if err != nil {
		return c, fmt.Errorf("VCS deployment hold: %w", sql.Unavailable(err))
	}
	return c, nil
}

// ReportedDeployment commits the acknowledged sequence beside fenced job completion.
func (s *Store) ReportedDeployment(ctx context.Context, acct kernel.AccountID, resource string, sequence int64) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	_, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE vcs_deployment_resources SET sequence=$3 WHERE account_id=$1 AND resource=$2`, acct, resource, sequence)
	if err != nil {
		return fmt.Errorf("VCS deployment acknowledge: %w", sql.Unavailable(err))
	}
	return nil
}

// PurgeDeployments removes the deleted owner's report metadata in bounded batches.
func (s *Store) PurgeDeployments(ctx context.Context, acct kernel.AccountID, batch int) (bool, error) {
	var count int64
	for _, table := range []string{"vcs_deployment_states", "vcs_deployment_runs", "vcs_deployment_resources"} {
		tag, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM `+table+` WHERE ctid IN (SELECT ctid FROM `+table+` WHERE account_id=$1 LIMIT $2)`, acct, batch)
		if err != nil {
			return false, fmt.Errorf("VCS deployment purge: %w", sql.Unavailable(err))
		}
		count += tag.RowsAffected()
	}
	return count == 0, nil
}

var _ feedback.DeploymentStore = (*Store)(nil)
