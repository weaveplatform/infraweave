package queue

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// HoldTx fences attempt metadata against the claim's generation without settling
// the job. The caller writes its metadata in this transaction, then returns its
// handler result for the worker's normal retry/dead accounting. Recovery and a
// later generation cannot pass the row lock until the metadata commits.
func (q *Queue) HoldTx(ctx context.Context, j Job) error {
	if !tx.In(ctx) {
		return ErrNoTransaction
	}
	var id int64
	err := tx.Q(ctx, q.q).QueryRow(ctx, `SELECT id FROM jobs
		WHERE id=$1 AND generation=$2 AND state='running' FOR UPDATE`, j.ID, j.Generation).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("hold job %d: %w", j.ID, sql.Unavailable(err))
	}
	return nil
}

// DeadByKeyTx locks a dead or discarded job's account-scoped identity while its domain settles
// metadata. Operator retry waits on this lock, so a live attempt cannot race the
// domain's crash reconciliation. A missing/non-dead job needs no settlement.
func (q *Queue) DeadByKeyTx(ctx context.Context, k Kind, key string, account kernel.ULID) (bool, error) {
	if !tx.In(ctx) {
		return false, ErrNoTransaction
	}
	var id int64
	err := tx.Q(ctx, q.q).QueryRow(ctx, `SELECT id FROM jobs WHERE kind=$1 AND unique_key=$2
		AND account_id IS NOT DISTINCT FROM $3::uuid AND state IN ('dead','cancelled') ORDER BY id DESC LIMIT 1 FOR UPDATE`, k.Name, key, account).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("hold dead job: %w", sql.Unavailable(err))
	}
	return true, nil
}
