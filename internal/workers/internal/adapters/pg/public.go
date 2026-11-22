package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
)

var errPublicTransaction = errors.New("workers: public pool lock requires a transaction")

// Public reads the installation pool. A transaction-scoped singleton lock also
// serializes first creation, when no row exists to lock yet.
func (s *Store) Public(ctx context.Context, lock bool) (pool.Pool, error) {
	q := tx.Q(ctx, s.db)
	if lock {
		if !tx.In(ctx) {
			return pool.Pool{}, errPublicTransaction
		}
		if _, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('worker_public_pool'), 0)`); err != nil {
			return pool.Pool{}, fmt.Errorf("workers: lock public pool: %w", sql.Unavailable(err))
		}
	}
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	return s.readPublic(ctx, suffix)
}

// PublicShare holds the current public certificate against reset while workers
// register or claim concurrently. Pool locks precede worker locks.
func (s *Store) PublicShare(ctx context.Context) (pool.Pool, error) {
	if !tx.In(ctx) {
		return pool.Pool{}, errPublicTransaction
	}
	return s.readPublic(ctx, " FOR SHARE")
}

func (s *Store) readPublic(ctx context.Context, suffix string) (pool.Pool, error) {
	p := pool.Pool{Public: true, Name: "public"}
	query := `SELECT id, certificate, serial, created_at, updated_at, version FROM worker_public_pool WHERE singleton=1` + suffix
	err := tx.Q(ctx, s.db).QueryRow(ctx, query).Scan(&p.ID, &p.Certificate, &p.Serial, &p.CreatedAt, &p.UpdatedAt, &p.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return pool.Pool{}, pool.ErrNotFound
	}
	if err != nil {
		return pool.Pool{}, fmt.Errorf("workers: read public pool: %w", sql.Unavailable(err))
	}
	return p, nil
}

// CreatePublic writes the installation singleton after its owner lock is held.
func (s *Store) CreatePublic(ctx context.Context, p pool.Pool) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO worker_public_pool
		(singleton,id,certificate,serial,created_at,updated_at,version) VALUES(1,$1,$2,$3,$4,$5,$6)`,
		p.ID, p.Certificate, p.Serial, p.CreatedAt, p.UpdatedAt, p.Version)
	if err != nil {
		return fmt.Errorf("workers: create public pool: %w", sql.Unavailable(err))
	}
	return nil
}

// WritePublic replaces the certificate and version of the locked singleton.
func (s *Store) WritePublic(ctx context.Context, p pool.Pool) error {
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE worker_public_pool SET certificate=$2,
		serial=$3, updated_at=$4, version=$5 WHERE singleton=1 AND id=$1`,
		p.ID, p.Certificate, p.Serial, p.UpdatedAt, p.Version)
	if err != nil {
		return fmt.Errorf("workers: update public pool: %w", sql.Unavailable(err))
	}
	if tag.RowsAffected() != 1 {
		return pool.ErrNotFound
	}
	return nil
}

// PublicWorkers reports whether registered slots still hold this pool identity.
func (s *Store) PublicWorkers(ctx context.Context, id kernel.ULID) (bool, error) {
	var found bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workers WHERE pool_id=$1)`, id).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("workers: read public pool registrations: %w", sql.Unavailable(err))
	}
	return found, nil
}
