package pg

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// StreamName is the runs stream's name in schema_migrations.
const StreamName = "runs"

// files are the runs stream's migrations.
//
//go:embed migrations/*.sql
var files embed.FS

// Stream is the runs domain's migration stream, which internal/app
// registers after configuration's.
func Stream() migrate.Stream {
	sub, _ := fs.Sub(files, "migrations") // a valid path, so never an error
	return migrate.Stream{Name: StreamName, Files: sub}
}

// Held are the runs columns that hold other domains' rows: a run's
// account, and the stack each run, run lock, tracked run and output
// names.
func Held() []integrity.Held {
	stack := []string{"account_id", "stack_id"}
	return []integrity.Held{
		{Entity: "account", Table: "runs", Columns: []string{"account_id"}},
		{Entity: "stack", Table: "runs", Columns: stack},
		{Entity: "stack", Table: "stack_run_locks", Columns: stack},
		{Entity: "stack", Table: "stack_run_state", Columns: stack},
		{Entity: "stack", Table: "stack_outputs", Columns: stack},
	}
}

// Keys declares the runs' key, which other domains' held columns name.
func Keys() []integrity.Key {
	return []integrity.Key{{Entity: "run", Table: "runs", Columns: []string{"account_id", "id"}}}
}

// Store is the runs domain's storage over a database, sealing its sealed
// column through seal.
type Store struct {
	db   tx.Querier
	seal Sealer
}

// Sealer seals and opens a column's values; crypt.Keyring is one.
type Sealer interface {
	SealColumn(ctx context.Context, c crypt.Column, pk []string, plaintext []byte) ([]byte, error)
	OpenColumn(ctx context.Context, c crypt.Column, pk []string, sealed []byte) (crypt.Value, error)
}

// New returns the store over db, sealing through seal.
func New(db tx.Querier, seal Sealer) *Store { return &Store{db: db, seal: seal} }

// Blockers reads which run holds the run lock of each of acct's stacks,
// keyed by stack ID; a stack whose lock no run holds is absent.
func (s *Store) Blockers(
	ctx context.Context,
	acct kernel.AccountID,
	stacks []kernel.ULID,
) (map[kernel.ULID]kernel.ULID, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT stack_id, run_id FROM stack_run_locks
		WHERE account_id = $1 AND stack_id = ANY($2)`, acct, stacks)
	if err != nil {
		return nil, fmt.Errorf("runs: read the stacks' run locks: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	out := make(map[kernel.ULID]kernel.ULID)
	for rows.Next() {
		var stack, run kernel.ULID
		if err := rows.Scan(&stack, &run); err != nil {
			return nil, fmt.Errorf("runs: read a stack's run lock: %w", sql.Unavailable(err))
		}
		out[stack] = run
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("runs: read the stacks' run locks: %w", sql.Unavailable(err))
	}
	return out, nil
}

// StackOf reads the stack of acct's run, false when acct lacks it.
func (s *Store) StackOf(
	ctx context.Context,
	acct kernel.AccountID,
	run kernel.ULID,
) (kernel.ULID, bool, error) {
	var stack kernel.ULID
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT stack_id FROM runs
		WHERE account_id = $1 AND id = $2`, acct, run).Scan(&stack)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return kernel.ULID{}, false, nil
	case err != nil:
		return kernel.ULID{}, false, fmt.Errorf("runs: read the stack of run %s: %w", run,
			sql.Unavailable(err))
	}
	return stack, true, nil
}

// DeleteStack deletes acct's stack's outputs, tracked run, run lock and
// runs, whose transitions go with them, in the context's transaction. The
// rows keyed by the stack go by its ID, not by cascade alone, so none is
// left even when the run it names belongs to another stack.
func (s *Store) DeleteStack(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) error {
	q := tx.Q(ctx, s.db)
	for _, stmt := range []string{
		`DELETE FROM stack_outputs WHERE account_id = $1 AND stack_id = $2`,
		`DELETE FROM stack_run_state WHERE account_id = $1 AND stack_id = $2`,
		`DELETE FROM stack_run_locks WHERE account_id = $1 AND stack_id = $2`,
		`DELETE FROM runs WHERE account_id = $1 AND stack_id = $2`,
	} {
		if _, err := q.Exec(ctx, stmt, acct, stack); err != nil {
			return fmt.Errorf("runs: delete the runs of stack %s: %w", stack, sql.Unavailable(err))
		}
	}
	return nil
}
