package pg

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
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

// DeleteLogs deletes run's log chunks and observability artifacts,
// records at as when its logs were deleted, and answers the objects they
// recorded, with the compacted log of each state that had chunks.
func (s *Store) DeleteLogs(
	ctx context.Context,
	run lifecycle.Run,
	at time.Time,
) ([]report.Object, error) {
	q := tx.Q(ctx, s.db)
	rows, err := q.Query(ctx, `DELETE FROM run_log_chunks WHERE account_id = $1 AND run_id = $2
		RETURNING state, state_version, object_key`, run.Account, run.ID)
	if err != nil {
		return nil, fmt.Errorf("runs: delete run %s's logs: %w", run.ID, sql.Unavailable(err))
	}
	var compacted []report.Object
	objs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (report.Object, error) {
		var (
			state   kernel.RunState
			version int64
		)
		o := report.Object{Store: objectstore.RunLogs}
		err := r.Scan(&state, &version, &o.Key)
		k := report.Object{
			Store: objectstore.RunLogs, Key: report.LogKey(run.Account, run.ID, state, version),
		}
		if err == nil && !slices.Contains(compacted, k) {
			compacted = append(compacted, k)
		}
		return o, err //nolint:wrapcheck // the delete names the read
	})
	if err != nil {
		return nil, fmt.Errorf("runs: delete run %s's logs: %w", run.ID, sql.Unavailable(err))
	}
	rows, err = q.Query(ctx, `DELETE FROM run_artifacts WHERE account_id = $1 AND run_id = $2
		AND kind = 'observability' RETURNING object_key`, run.Account, run.ID)
	if err != nil {
		return nil, fmt.Errorf("runs: delete run %s's logs: %w", run.ID, sql.Unavailable(err))
	}
	observed, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (report.Object, error) {
		o := report.Object{Store: objectstore.RunObservability}
		return o, r.Scan(&o.Key) //nolint:wrapcheck // the delete names the read
	})
	if err != nil {
		return nil, fmt.Errorf("runs: delete run %s's logs: %w", run.ID, sql.Unavailable(err))
	}
	if _, err := q.Exec(ctx, `UPDATE runs SET logs_deleted_at = $3, updated_at = $3
		WHERE account_id = $1 AND id = $2`, run.Account, run.ID, at); err != nil {
		return nil, fmt.Errorf("runs: delete run %s's logs: %w", run.ID, sql.Unavailable(err))
	}
	return append(append(objs, compacted...), observed...), nil
}
