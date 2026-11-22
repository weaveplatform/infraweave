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
)

var errManagedTransaction = errors.New("workers: managed creation needs a transaction")

// createManagement runs inside the pool creation transaction, reserving both
// management and hosting identity before any pool response can be returned.
func (s *Store) createManagement(ctx context.Context, p pool.Pool) error {
	owner := p.Management
	for _, statement := range []string{
		`INSERT INTO worker_pool_management (account_id,pool_id,cluster_id,namespace,resource_uid)
		 VALUES ($1,$2,$3,$4,$5)`,
		`INSERT INTO worker_pool_bindings (account_id,pool_id,cluster_id,namespace,resource_uid)
		 VALUES ($1,$2,$3,$4,$5)`,
	} {
		_, err := tx.Q(ctx, s.db).Exec(ctx, statement, p.Account, p.ID, owner.ClusterID, owner.Namespace, owner.ResourceUID)
		if err != nil {
			if pe, ok := errors.AsType[*pgconn.PgError](err); ok && pe.Code == "23505" {
				return fmt.Errorf("%w: resource is already reserved", pool.ErrOwnership)
			}
			return fmt.Errorf("workers: reserve pool ownership: %w", sql.Unavailable(err))
		}
	}
	return nil
}

// Managed selects the live pool rather than resurrecting a retained resource
// tombstone. Account filtering also applies when another tenant knows the UID.
func (s *Store) Managed(ctx context.Context, acct kernel.AccountID, owner kernel.WorkerOwner) (pool.Pool, error) {
	out, err := scan(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+columns+` FROM worker_pools p
		JOIN worker_pool_management owner ON owner.pool_id=p.id AND owner.account_id=p.account_id
		WHERE p.account_id=$1 AND owner.cluster_id=$2 AND owner.namespace=$3
		AND owner.resource_uid=$4 AND NOT owner.revoked`, acct, owner.ClusterID, owner.Namespace, owner.ResourceUID))
	if errors.Is(err, pgx.ErrNoRows) {
		return pool.Pool{}, pool.ErrNotFound
	}
	if err != nil {
		return pool.Pool{}, fmt.Errorf("workers: read managed pool: %w", sql.Unavailable(err))
	}
	return out, nil
}
