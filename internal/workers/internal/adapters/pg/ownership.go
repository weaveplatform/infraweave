package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

var errBindingTransaction = errors.New("workers: a hosting binding is read in a transaction")

// Binding takes the pool's transaction-scoped binding lock before reading,
// so a bind, a release and a registration of one pool run one at a time
// even while the pool has no binding row to lock.
func (s *Store) Binding(ctx context.Context, id kernel.ULID) (kernel.WorkerOwner, bool, error) {
	if !tx.In(ctx) {
		return kernel.WorkerOwner{}, false, errBindingTransaction
	}
	q := tx.Q(ctx, s.db)
	if _, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('worker_pool_bindings'), hashtext($1::text))`, id); err != nil {
		return kernel.WorkerOwner{}, false, fmt.Errorf("workers: lock pool %s's binding: %w", id, sql.Unavailable(err))
	}
	var owner kernel.WorkerOwner
	err := q.QueryRow(ctx, `SELECT cluster_id, namespace, resource_uid FROM worker_pool_bindings
		WHERE pool_id = $1 FOR UPDATE`, id).Scan(&owner.ClusterID, &owner.Namespace, &owner.ResourceUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return kernel.WorkerOwner{}, false, nil
	}
	if err != nil {
		return kernel.WorkerOwner{}, false, fmt.Errorf("workers: read pool %s's binding: %w", id, sql.Unavailable(err))
	}
	return owner, true, nil
}

// Bind records owner as p's hosting owner. The owner's unique identity
// refuses a second pool.
func (s *Store) Bind(ctx context.Context, p pool.Pool, owner kernel.WorkerOwner) error {
	var account any = p.Account
	if p.Public {
		account = nil
	}
	_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO worker_pool_bindings
		(account_id, pool_id, cluster_id, namespace, resource_uid) VALUES ($1, $2, $3, $4, $5)`,
		account, p.ID, owner.ClusterID, owner.Namespace, owner.ResourceUID)
	if pe, ok := errors.AsType[*pgconn.PgError](err); ok && pe.Code == "23505" {
		return fmt.Errorf("%w: the owner hosts another pool", worker.ErrOwnership)
	}
	if err != nil {
		return fmt.Errorf("workers: bind pool %s: %w", p.ID, sql.Unavailable(err))
	}
	return nil
}

// Unbind removes the pool's hosting binding.
func (s *Store) Unbind(ctx context.Context, id kernel.ULID) error {
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM worker_pool_bindings WHERE pool_id = $1`, id); err != nil {
		return fmt.Errorf("workers: release pool %s: %w", id, sql.Unavailable(err))
	}
	return nil
}

// ManagedPool reads the reservation of owner's management identity, which
// a deleted pool leaves without a pool.
func (s *Store) ManagedPool(ctx context.Context, owner kernel.WorkerOwner) (*kernel.ULID, bool, error) {
	var id *kernel.ULID
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT pool_id FROM worker_pool_management
		WHERE cluster_id = $1 AND namespace = $2 AND resource_uid = $3`,
		owner.ClusterID, owner.Namespace, owner.ResourceUID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("workers: read management reservation: %w", sql.Unavailable(err))
	}
	return id, true, nil
}

// HasWorkers reports whether a worker of any account is registered in the
// pool id.
func (s *Store) HasWorkers(ctx context.Context, id kernel.ULID) (bool, error) {
	var found bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workers WHERE pool_id = $1)`, id).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("workers: read pool %s's workers: %w", id, sql.Unavailable(err))
	}
	return found, nil
}

// SlotWorker reads the worker registered in the pool's slot, locked FOR
// UPDATE.
func (s *Store) SlotWorker(ctx context.Context, id kernel.ULID, slot int32) (worker.Worker, error) {
	w, err := scanWorker(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+workerColumns+` FROM workers w
		WHERE w.pool_id = $1 AND w.slot = $2 FOR UPDATE`, id, slot))
	if errors.Is(err, pgx.ErrNoRows) {
		return worker.Worker{}, fmt.Errorf("%w: pool %s slot %d", worker.ErrNotFound, id, slot)
	}
	if err != nil {
		return worker.Worker{}, fmt.Errorf("workers: read pool %s slot %d: %w", id, slot, sql.Unavailable(err))
	}
	return w, nil
}
