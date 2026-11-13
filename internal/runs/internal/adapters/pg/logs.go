package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/logs"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/report"
)

var _ logs.Store = (*Store)(nil)

// Entered reads the latest state version at which acct's run entered
// state, and false when it never did.
func (s *Store) Entered(
	ctx context.Context,
	acct kernel.AccountID,
	run kernel.ULID,
	state kernel.RunState,
) (int64, bool, error) {
	var v *int64
	if err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT max(sequence) FROM run_state_transitions
		WHERE account_id = $1 AND run_id = $2 AND state = $3`, acct, run, state).
		Scan(&v); err != nil {
		return 0, false, fmt.Errorf("runs: read when run %s entered %s: %w", run, state,
			sql.Unavailable(err))
	}
	if v == nil {
		return 0, false, nil
	}
	return *v, true, nil
}

// Chunks reads up to limit of acct's run's log chunks of state version
// version, from sequence number from on, in sequence order.
func (s *Store) Chunks(
	ctx context.Context,
	acct kernel.AccountID,
	run kernel.ULID,
	version, from int64,
	limit int,
) ([]report.Chunk, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT state_version, state, seq, object_key, bytes,
		first_at, last_at FROM run_log_chunks
		WHERE account_id = $1 AND run_id = $2 AND state_version = $3 AND seq >= $4
		ORDER BY seq LIMIT $5`, acct, run, version, from, limit)
	if err != nil {
		return nil, fmt.Errorf("runs: read run %s's log chunks: %w", run, sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (report.Chunk, error) {
		var c report.Chunk
		err := r.Scan(&c.Version, &c.State, &c.Seq, &c.Key, &c.Bytes, &c.FirstAt, &c.LastAt)
		return c, err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return nil, fmt.Errorf("runs: read run %s's log chunks: %w", run, sql.Unavailable(err))
	}
	return out, nil
}

// LockChunks reads the sequence numbers of acct's run's log chunks of
// state version version, in order, and locks their rows until the
// transaction ends, so a purge's delete of them waits.
func (s *Store) LockChunks(
	ctx context.Context,
	acct kernel.AccountID,
	run kernel.ULID,
	version int64,
) ([]int64, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT seq FROM run_log_chunks
		WHERE account_id = $1 AND run_id = $2 AND state_version = $3
		ORDER BY seq FOR UPDATE`, acct, run, version)
	if err != nil {
		return nil, fmt.Errorf("runs: lock run %s's log chunks: %w", run, sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, fmt.Errorf("runs: lock run %s's log chunks: %w", run, sql.Unavailable(err))
	}
	return out, nil
}
