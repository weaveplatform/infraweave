package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/report"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

var _ report.Store = (*Store)(nil)

// StateAt reads the state acct's run entered at state version version;
// lifecycle.ErrNotFound when it has no such transition.
func (s *Store) StateAt(
	ctx context.Context,
	acct kernel.AccountID,
	run kernel.ULID,
	version int64,
) (kernel.RunState, error) {
	var state kernel.RunState
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT state FROM run_state_transitions
		WHERE account_id = $1 AND run_id = $2 AND sequence = $3`, acct, run, version).Scan(&state)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", fmt.Errorf("%w: %s at state version %d", lifecycle.ErrNotFound, run, version)
	case err != nil:
		return "", fmt.Errorf("runs: read run %s's state at %d: %w", run, version,
			sql.Unavailable(err))
	}
	return state, nil
}

// Indexed reports whether run's log chunk seq of state version version is
// indexed.
func (s *Store) Indexed(ctx context.Context, run lifecycle.Run, version, seq int64) (bool, error) {
	var ok bool
	if err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM run_log_chunks
		WHERE account_id = $1 AND run_id = $2 AND state_version = $3 AND seq = $4)`,
		run.Account, run.ID, version, seq).Scan(&ok); err != nil {
		return false, fmt.Errorf("runs: read run %s's log chunk %d: %w", run.ID, seq,
			sql.Unavailable(err))
	}
	return ok, nil
}

// IndexChunk indexes c, a log chunk of run.
func (s *Store) IndexChunk(ctx context.Context, run lifecycle.Run, c report.Chunk) error {
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO run_log_chunks (account_id, run_id,
		state_version, seq, state, object_key, bytes, first_at, last_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, run.Account, run.ID, c.Version, c.Seq,
		c.State, c.Key, c.Bytes, c.FirstAt, c.LastAt); err != nil {
		return fmt.Errorf("runs: index run %s's log chunk %d: %w", run.ID, c.Seq,
			sql.Unavailable(err))
	}
	return nil
}

// Recorded reports whether run recorded the object of kind named name in
// state version version.
func (s *Store) Recorded(
	ctx context.Context,
	run lifecycle.Run,
	version int64,
	kind workersapi.UploadKind,
	name string,
) (bool, error) {
	var ok bool
	if err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM run_artifacts
		WHERE account_id = $1 AND run_id = $2 AND state_version = $3 AND kind = $4
		AND name = $5)`, run.Account, run.ID, version, kind, name).Scan(&ok); err != nil {
		return false, fmt.Errorf("runs: read run %s's %s %s: %w", run.ID, kind, name,
			sql.Unavailable(err))
	}
	return ok, nil
}

// Record records a, an uploaded object of run.
func (s *Store) Record(ctx context.Context, run lifecycle.Run, a report.Artifact) error {
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO run_artifacts (account_id, run_id,
		state_version, kind, name, object_key, size, sha256)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, run.Account, run.ID, a.Version, a.Kind, a.Name,
		a.Key, a.Size, a.SHA256[:]); err != nil {
		return fmt.Errorf("runs: record run %s's %s %s: %w", run.ID, a.Kind, a.Name,
			sql.Unavailable(err))
	}
	return nil
}

// SetDelta keeps d, run's plan's delta, on the run.
func (s *Store) SetDelta(ctx context.Context, run lifecycle.Run, d workersapi.Delta) error {
	// Integers and a boolean, so never an error.
	doc, _ := json.Marshal(delta{
		Add: d.Add, Change: d.Change, Delete: d.Delete, Import: d.Import, Move: d.Move,
		Forget: d.Forget, Resources: d.Resources, Outputs: d.Outputs,
	})
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE runs SET delta = $3
		WHERE account_id = $1 AND id = $2`, run.Account, run.ID, doc); err != nil {
		return fmt.Errorf("runs: keep run %s's delta: %w", run.ID, sql.Unavailable(err))
	}
	return nil
}

// delta is runs.delta's document.
type delta struct {
	Add       int  `json:"add"`
	Change    int  `json:"change"`
	Delete    int  `json:"delete"`
	Import    int  `json:"import"`
	Move      int  `json:"move"`
	Forget    int  `json:"forget"`
	Resources int  `json:"resources"`
	Outputs   bool `json:"outputs"`
}

// Workspace reads the key of the workspace run recorded last, and false
// when it recorded none.
func (s *Store) Workspace(ctx context.Context, run lifecycle.Run) (string, bool, error) {
	var key string
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT object_key FROM run_artifacts
		WHERE account_id = $1 AND run_id = $2 AND kind = 'workspace'
		ORDER BY state_version DESC, recorded_at DESC, name DESC LIMIT 1`,
		run.Account, run.ID).Scan(&key)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("runs: read run %s's workspace: %w", run.ID,
			sql.Unavailable(err))
	}
	return key, true, nil
}

// stores are the stores of run_artifacts' kinds.
const stores = `CASE kind WHEN 'artifact' THEN 'run-artifacts'
	WHEN 'workspace' THEN 'workspaces' ELSE 'run-observability' END`

// PurgeObjects deletes up to batch of the deleted account acct's log
// chunks, or, once it has none, of its other recorded objects, and
// answers the objects they recorded, which the caller deletes in the same
// transaction; none once both are empty.
func (s *Store) PurgeObjects(
	ctx context.Context,
	acct kernel.AccountID,
	batch int,
) ([]report.Object, error) {
	for _, q := range []string{
		`DELETE FROM run_log_chunks WHERE account_id = $1
			AND (run_id, state_version, seq) IN (SELECT run_id, state_version, seq
			FROM run_log_chunks WHERE account_id = $1 LIMIT $2)
			RETURNING 'run-logs', object_key`,
		`DELETE FROM run_artifacts WHERE account_id = $1
			AND (run_id, state_version, kind, name) IN (SELECT run_id, state_version, kind, name
			FROM run_artifacts WHERE account_id = $1 LIMIT $2)
			RETURNING ` + stores + `, object_key`,
	} {
		rows, err := tx.Q(ctx, s.db).Query(ctx, q, acct, batch)
		if err != nil {
			return nil, fmt.Errorf("runs: purge account %s's objects: %w", acct,
				sql.Unavailable(err))
		}
		objs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (report.Object, error) {
			var o report.Object
			err := r.Scan(&o.Store, &o.Key)
			return o, err //nolint:wrapcheck // the purge names the read
		})
		if err != nil {
			return nil, fmt.Errorf("runs: purge account %s's objects: %w", acct,
				sql.Unavailable(err))
		}
		if len(objs) > 0 {
			return objs, nil
		}
	}
	return nil, nil
}
