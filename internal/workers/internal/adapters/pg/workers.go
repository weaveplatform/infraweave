package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	sqlsearch "github.com/weaveplatform/infraweave/internal/platform/sql/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

// PoolByID reads the pool id of whichever account holds it.
func (s *Store) PoolByID(ctx context.Context, id kernel.ULID) (pool.Pool, error) {
	p, err := scan(tx.Q(ctx, s.db).QueryRow(ctx,
		`SELECT `+columns+` FROM worker_pools p WHERE p.id = $1`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return p, fmt.Errorf("%w: %s", pool.ErrNotFound, id)
	case err != nil:
		return p, fmt.Errorf("workers: read worker pool %s: %w", id, sql.Unavailable(err))
	}
	return p, nil
}

// CA reads the pool CA's certificate, nil while there is none.
func (s *Store) CA(ctx context.Context) ([]byte, error) {
	var cert []byte
	err := tx.Q(ctx, s.db).QueryRow(ctx,
		`SELECT certificate FROM worker_pool_ca WHERE id = 1`).Scan(&cert)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("workers: read the pool CA: %w", sql.Unavailable(err))
	}
	return cert, nil
}

// Nonce records the pool's jti until expires, after dropping the pool's
// expired ones, so a jti seen again once it expired is fresh again.
func (s *Store) Nonce(
	ctx context.Context,
	acct kernel.AccountID,
	pool kernel.ULID,
	jti string,
	expires time.Time,
) (bool, error) {
	q := tx.Q(ctx, s.db)
	if _, err := q.Exec(ctx, `DELETE FROM worker_nonces
		WHERE account_id = $1 AND pool_id = $2 AND expires_at < now()`, acct, pool); err != nil {
		return false, fmt.Errorf("workers: expire pool %s's nonces: %w", pool, sql.Unavailable(err))
	}
	tag, err := q.Exec(ctx, `INSERT INTO worker_nonces (account_id, pool_id, jti, expires_at)
		VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, acct, pool, jti, expires)
	if err != nil {
		return false, fmt.Errorf("workers: record pool %s's nonce: %w", pool, sql.Unavailable(err))
	}
	return tag.RowsAffected() == 1, nil
}

// Count counts the registered workers of org and of acct.
func (s *Store) Count(
	ctx context.Context,
	org kernel.OrganizationID,
	acct kernel.AccountID,
) (featuresapi.Usage, error) {
	var u featuresapi.Usage
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE account_id = $2)
		FROM workers WHERE organization_id = $1`, org, acct).Scan(&u.Organization, &u.Account)
	if err != nil {
		return u, fmt.Errorf("workers: count organization %s's workers: %w", org,
			sql.Unavailable(err))
	}
	return u, nil
}

// workerColumns are a worker's columns as scanWorker reads them, over
// workers w.
const workerColumns = `w.account_id, w.organization_id, w.id, w.pool_id, w.serial, w.version,
	w.os, w.arch, w.metadata, w.busy, w.drained, w.cycle, w.last_seen_at, w.available_at,
	w.created_at`

// scanWorker reads workerColumns, then into extra.
func scanWorker(row pgx.Row, extra ...any) (worker.Worker, error) {
	var w worker.Worker
	err := row.Scan(append([]any{
		&w.Account, &w.Organization, &w.ID, &w.Pool, &w.Serial, &w.Version, &w.OS, &w.Arch,
		&w.Metadata, &w.Busy, &w.Drained, &w.Cycle, &w.LastSeenAt, &w.AvailableAt, &w.CreatedAt,
	}, extra...)...)
	return w, err //nolint:wrapcheck // callers name the read
}

// workerError names a failed read or write of the worker id.
func workerError(id kernel.ULID, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s", worker.ErrNotFound, id)
	}
	return fmt.Errorf("workers: worker %s: %w", id, sql.Unavailable(err))
}

// Worker reads the worker id with its pool's current serial.
func (s *Store) Worker(
	ctx context.Context,
	id kernel.ULID,
	lock bool,
) (worker.Worker, int64, error) {
	query := `SELECT ` + workerColumns + `, p.serial FROM workers w
		JOIN worker_pools p ON p.account_id = w.account_id AND p.id = w.pool_id
		WHERE w.id = $1`
	if lock {
		query += ` FOR UPDATE OF w`
	}
	var serial int64
	w, err := scanWorker(tx.Q(ctx, s.db).QueryRow(ctx, query, id), &serial)
	if err != nil {
		return w, 0, workerError(id, err)
	}
	return w, serial, nil
}

// metadata is m as the metadata column holds it.
func metadata(m map[string]string) ([]byte, error) {
	if m == nil {
		m = map[string]string{}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("workers: metadata: %w", err)
	}
	return b, nil
}

// CreateWorker writes a new worker.
func (s *Store) CreateWorker(ctx context.Context, w worker.Worker) error {
	md, err := metadata(w.Metadata)
	if err != nil {
		return err
	}
	_, err = tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO workers (account_id, organization_id, id,
			pool_id, serial, version, os, arch, metadata, busy, drained, cycle, last_seen_at,
			available_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		w.Account, w.Organization, w.ID, w.Pool, w.Serial, w.Version, w.OS, w.Arch, md, w.Busy,
		w.Drained, w.Cycle, w.LastSeenAt, w.AvailableAt, w.CreatedAt)
	if err != nil {
		return workerError(w.ID, err)
	}
	return nil
}

// Resume rewrites w's registration and marks it seen.
func (s *Store) Resume(ctx context.Context, w worker.Worker) error {
	md, err := metadata(w.Metadata)
	if err != nil {
		return err
	}
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE workers SET serial = $3, version = $4,
			os = $5, arch = $6, metadata = $7, last_seen_at = $8
		WHERE account_id = $1 AND id = $2`,
		w.Account, w.ID, w.Serial, w.Version, w.OS, w.Arch, md, w.LastSeenAt)
	if err == nil && tag.RowsAffected() == 0 {
		err = pgx.ErrNoRows
	}
	if err != nil {
		return workerError(w.ID, err)
	}
	return nil
}

// Seen marks the worker id seen at now with busy. available_at is set
// when the worker becomes idle and cleared while it is busy.
func (s *Store) Seen(
	ctx context.Context,
	id kernel.ULID,
	busy bool,
	now time.Time,
) (worker.Worker, int64, error) {
	var serial int64
	w, err := scanWorker(tx.Q(ctx, s.db).QueryRow(ctx, `UPDATE workers w SET last_seen_at = $2,
			busy = $3,
			available_at = CASE WHEN $3 THEN NULL
				WHEN w.busy OR w.available_at IS NULL THEN $2 ELSE w.available_at END
		FROM worker_pools p
		WHERE w.id = $1 AND p.account_id = w.account_id AND p.id = w.pool_id
		RETURNING `+workerColumns+`, p.serial`, id, now, busy), &serial)
	if err != nil {
		return w, 0, workerError(id, err)
	}
	return w, serial, nil
}

// Remove deletes the worker id.
func (s *Store) Remove(ctx context.Context, id kernel.ULID) error {
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM workers WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = pgx.ErrNoRows
	}
	if err != nil {
		return workerError(id, err)
	}
	return nil
}

// wake notifies every replica's held polls of pool, on commit when ctx
// carries a transaction.
func (s *Store) wake(ctx context.Context, pool kernel.ULID) error {
	if err := notify.Notify(ctx, tx.Q(ctx, s.db), notify.Runs, pool.String()); err != nil {
		return fmt.Errorf("workers: wake pool %s's polls: %w", pool, sql.Unavailable(err))
	}
	return nil
}

// Drain sets the drain flag of acct's worker id of pool and wakes the
// pool's held polls.
func (s *Store) Drain(
	ctx context.Context,
	acct kernel.AccountID,
	pool, id kernel.ULID,
	drain bool,
) (worker.Worker, error) {
	w, err := scanWorker(tx.Q(ctx, s.db).QueryRow(ctx, `UPDATE workers w SET drained = $4
		WHERE w.account_id = $1 AND w.pool_id = $2 AND w.id = $3
		RETURNING `+workerColumns, acct, pool, id, drain))
	if err != nil {
		return w, workerError(id, err)
	}
	return w, s.wake(ctx, pool)
}

// Cycle marks every worker of acct's pool to cycle and wakes the pool's
// held polls.
func (s *Store) Cycle(ctx context.Context, acct kernel.AccountID, pool kernel.ULID) error {
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE workers SET cycle = true
		WHERE account_id = $1 AND pool_id = $2`, acct, pool); err != nil {
		return fmt.Errorf("workers: cycle pool %s: %w", pool, sql.Unavailable(err))
	}
	return s.wake(ctx, pool)
}

// OfPools reads the workers of acct's pools, by pool, oldest first.
func (s *Store) OfPools(
	ctx context.Context,
	acct kernel.AccountID,
	pools []kernel.ULID,
) (map[kernel.ULID][]worker.Worker, error) {
	out := map[kernel.ULID][]worker.Worker{}
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+workerColumns+` FROM workers w
		WHERE w.account_id = $1 AND w.pool_id = ANY($2) ORDER BY w.created_at, w.id`, acct, pools)
	if err != nil {
		return nil, fmt.Errorf("workers: read pools' workers: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	for rows.Next() {
		w, err := scanWorker(rows)
		if err != nil {
			return nil, fmt.Errorf("workers: read pools' workers: %w", sql.Unavailable(err))
		}
		out[w.Pool] = append(out[w.Pool], w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workers: read pools' workers: %w", sql.Unavailable(err))
	}
	return out, nil
}

// workerStatus is worker.Status in SQL over workers w.
const workerStatus = `CASE WHEN w.drained THEN 'DRAINED' WHEN w.busy THEN 'BUSY' ELSE 'IDLE' END`

// workerColumn is the SQL of each of worker.Fields.
var workerColumn = map[string]sqlsearch.Column{
	"id":        {Expr: "w.id::text", Cast: "text", Text: true},
	"status":    {Expr: workerStatus, Cast: "text"},
	"busy":      {Expr: "w.busy", Cast: "boolean"},
	"drained":   {Expr: "w.drained", Cast: "boolean"},
	"createdAt": {Expr: "w.created_at", Cast: "timestamptz"},
}

// workerSearch is the workers table as worker.Fields search it. A worker
// lives in its pool's space.
var workerSearch = func() sqlsearch.Entity {
	e := sqlsearch.Entity{
		List: "workers", Select: workerColumns,
		From:  "workers w JOIN worker_pools p ON p.account_id = w.account_id AND p.id = w.pool_id",
		Space: "p.space_id", ID: "w.id::text",
		Order: []search.Order{{Field: "createdAt", Desc: true}},
	}
	for _, f := range worker.Fields {
		c := workerColumn[f.Name]
		c.Field = f
		e.Columns = append(e.Columns, c)
	}
	return e
}()

// workerScope is the workers of acct's pool, which lives in space.
func workerScope(acct kernel.AccountID, pool, space kernel.ULID) sqlsearch.Scope {
	return sqlsearch.Scope{
		Where: "w.account_id = $1 AND w.pool_id = $2", Args: []any{acct, pool},
		Reachable: []kernel.ULID{space},
	}
}

// SearchWorkers reads a page of the workers of acct's pool that in keeps.
func (s *Store) SearchWorkers(
	ctx context.Context,
	acct kernel.AccountID,
	pool, space kernel.ULID,
	in search.Input,
) (search.Result[worker.Worker], error) {
	out, err := sqlsearch.Find(ctx, tx.Q(ctx, s.db), workerSearch, workerScope(acct, pool, space),
		in, scanWorker)
	if err != nil {
		return out, fmt.Errorf("workers: %w", err)
	}
	return out, nil
}

// SuggestWorkers lists the values the workers of acct's pool hold.
func (s *Store) SuggestWorkers(
	ctx context.Context,
	acct kernel.AccountID,
	pool, space kernel.ULID,
	in search.SuggestionsInput,
) (search.Suggestions, error) {
	out, err := sqlsearch.Suggest(ctx, tx.Q(ctx, s.db), workerSearch,
		workerScope(acct, pool, space), in)
	if err != nil {
		return out, fmt.Errorf("workers: %w", err)
	}
	return out, nil
}
