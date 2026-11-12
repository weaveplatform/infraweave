package pg

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/sample"
)

var _ sample.Store = (*Store)(nil)

// Sample writes at's sample of each pool of every account in one
// statement. It inserts in key order, so two replicas sampling the same
// minute at once wait on each other's rows in the same order, and the
// later one writes nothing rather than deadlocking.
func (s *Store) Sample(ctx context.Context, at time.Time, queued map[kernel.ULID]int) (int, error) {
	pools := make([]kernel.ULID, 0, len(queued))
	counts := make([]int64, 0, len(queued))
	for p, n := range queued {
		pools, counts = append(pools, p), append(counts, int64(n))
	}
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO worker_pool_samples (account_id,
			worker_pool_id, sampled_at, busy_workers, registered_workers, queued_runs)
		SELECT p.account_id, p.id, $1,
			(SELECT count(*) FROM workers w
				WHERE w.account_id = p.account_id AND w.pool_id = p.id AND w.busy),
			(SELECT count(*) FROM workers w
				WHERE w.account_id = p.account_id AND w.pool_id = p.id),
			coalesce((SELECT q.n FROM unnest($2::uuid[], $3::bigint[]) AS q(pool, n)
				WHERE q.pool = p.id), 0)
		FROM worker_pools p
		ORDER BY p.account_id, p.id
		ON CONFLICT DO NOTHING`, at, pools, counts)
	if err != nil {
		return 0, fmt.Errorf("workers: sample the pools at %s: %w", at, sql.Unavailable(err))
	}
	return int(tag.RowsAffected()), nil
}

// Prune deletes up to batch of the samples taken before before, of every
// account. The index worker_pool_samples_at serves it.
func (s *Store) Prune(ctx context.Context, before time.Time, batch int) (int, error) {
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM worker_pool_samples
		WHERE (account_id, worker_pool_id, sampled_at) IN (
			SELECT account_id, worker_pool_id, sampled_at FROM worker_pool_samples
			WHERE sampled_at < $1 LIMIT $2)`, before, batch)
	if err != nil {
		return 0, fmt.Errorf("workers: prune the pool samples before %s: %w", before,
			sql.Unavailable(err))
	}
	return int(tag.RowsAffected()), nil
}

// Between reads acct's samples of pool taken from from up to, not
// including, to, oldest first, along the primary key.
func (s *Store) Between(
	ctx context.Context,
	acct kernel.AccountID,
	pool kernel.ULID,
	from, to time.Time,
) ([]workersapi.Sample, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT sampled_at, busy_workers,
			registered_workers, queued_runs FROM worker_pool_samples
		WHERE account_id = $1 AND worker_pool_id = $2 AND sampled_at >= $3 AND sampled_at < $4
		ORDER BY sampled_at`, acct, pool, from, to)
	if err != nil {
		return nil, fmt.Errorf("workers: read pool %s's samples: %w", pool, sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (workersapi.Sample, error) {
		var v workersapi.Sample
		err := r.Scan(&v.At, &v.Busy, &v.Registered, &v.Queued)
		return v, err //nolint:wrapcheck // CollectRows' caller names the read
	})
	if err != nil {
		return nil, fmt.Errorf("workers: read pool %s's samples: %w", pool, sql.Unavailable(err))
	}
	return out, nil
}
