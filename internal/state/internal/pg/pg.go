package pg

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/audit"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/state/internal/core"
)

// StreamName is the state stream's name in schema_migrations.
const StreamName = "state"

// files are the state stream's migrations.
//
//go:embed migrations/*.sql
var files embed.FS

// Stream is the state domain's migration stream, which internal/app
// registers after runs'.
func Stream() migrate.Stream {
	sub, _ := fs.Sub(files, "migrations") // a valid path, so never an error
	return migrate.Stream{Name: StreamName, Files: sub}
}

// Held are the state columns that hold other domains' rows: a workspace's
// account, the stack each workspace, lock and version names, and the run
// that holds each lock and wrote each version. A lock's and a version's
// account is its workspace's, which they go with.
func Held() []integrity.Held {
	stack := []string{"account_id", "stack_id"}
	run := []string{"account_id", "run_id"}
	return []integrity.Held{
		{Entity: "account", Table: "state_workspaces", Columns: []string{"account_id"}},
		{Entity: "stack", Table: "state_workspaces", Columns: stack},
		{Entity: "stack", Table: "state_locks", Columns: stack},
		{Entity: "stack", Table: "managed_state_versions", Columns: stack},
		{Entity: "run", Table: "state_locks", Columns: run},
		{Entity: "run", Table: "managed_state_versions", Columns: run},
	}
}

// Store is the state domain's storage over a database.
type Store struct{ db tx.Querier }

// New returns the store over db.
func New(db tx.Querier) *Store { return &Store{db: db} }

// OpenWorkspace makes acct's stack's workspace name exist, in the
// context's transaction, leaving one that exists as it is.
func (s *Store) OpenWorkspace(
	ctx context.Context,
	acct kernel.AccountID,
	stack kernel.ULID,
	name string,
) error {
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO state_workspaces
		(account_id, stack_id, name) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		acct, stack, name); err != nil {
		return fmt.Errorf("state: open workspace %q of stack %s: %w", name, stack,
			sql.Unavailable(err))
	}
	return nil
}

// LockWorkspace locks t's workspace's row FOR UPDATE until the context's
// transaction ends; false when the workspace does not exist.
func (s *Store) LockWorkspace(ctx context.Context, t core.Target) (bool, error) {
	var found bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT true FROM state_workspaces
		WHERE account_id = $1 AND stack_id = $2 AND name = $3 FOR UPDATE`,
		t.Account, t.Stack, t.Workspace).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("state: lock workspace %q of stack %s: %w", t.Workspace, t.Stack,
			sql.Unavailable(err))
	}
	return true, nil
}

// Locks are the locks held on t's workspace, oldest first, in the order
// of their IDs within one instant.
func (s *Store) Locks(ctx context.Context, t core.Target) ([]core.Lock, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT lock_id, mode, run_id, info, created_at
		FROM state_locks WHERE account_id = $1 AND stack_id = $2 AND workspace = $3
		ORDER BY created_at, lock_id`, t.Account, t.Stack, t.Workspace)
	if err != nil {
		return nil, fmt.Errorf("state: read the locks of workspace %q of stack %s: %w",
			t.Workspace, t.Stack, sql.Unavailable(err))
	}
	locks, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (core.Lock, error) {
		var l core.Lock
		err := row.Scan(&l.ID, &l.Mode, &l.Run, &l.Info, &l.CreatedAt)
		return l, err //nolint:wrapcheck // CollectRows' caller names the read
	})
	if err != nil {
		return nil, fmt.Errorf("state: read the locks of workspace %q of stack %s: %w",
			t.Workspace, t.Stack, sql.Unavailable(err))
	}
	return locks, nil
}

// AddLock records l on t's workspace.
func (s *Store) AddLock(ctx context.Context, t core.Target, l core.Lock) error {
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO state_locks
		(account_id, stack_id, workspace, lock_id, mode, run_id, info)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		t.Account, t.Stack, t.Workspace, l.ID, l.Mode, l.Run, string(l.Info)); err != nil {
		return fmt.Errorf("state: lock workspace %q of stack %s: %w", t.Workspace, t.Stack,
			sql.Unavailable(err))
	}
	return nil
}

// RemoveLocks deletes the locks on t's workspace whose IDs ids holds.
func (s *Store) RemoveLocks(ctx context.Context, t core.Target, ids []string) error {
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM state_locks
		WHERE account_id = $1 AND stack_id = $2 AND workspace = $3 AND lock_id = ANY($4)`,
		t.Account, t.Stack, t.Workspace, ids); err != nil {
		return fmt.Errorf("state: unlock workspace %q of stack %s: %w", t.Workspace, t.Stack,
			sql.Unavailable(err))
	}
	return nil
}

// ReleaseRun deletes every lock run holds in acct.
func (s *Store) ReleaseRun(ctx context.Context, acct kernel.AccountID, run kernel.ULID) error {
	if _, err := tx.Q(ctx, s.db).Exec(ctx,
		`DELETE FROM state_locks WHERE account_id = $1 AND run_id = $2`, acct, run); err != nil {
		return fmt.Errorf("state: release the locks of run %s: %w", run, sql.Unavailable(err))
	}
	return nil
}

// Audit records e on its account's trail, in the context's transaction.
func (s *Store) Audit(ctx context.Context, e kernel.AuditEntry) error {
	_, err := audit.Record(ctx, e)
	return err //nolint:wrapcheck // the trail names the write
}

// purged are the state tables a purge empties, each with the key it
// deletes a batch by within an account and what a deleted row answers:
// the versions' object keys, the others nothing. A workspace goes last,
// after the rows that name it.
var purged = []struct{ table, key, returning string }{
	{"managed_state_versions", "id", "object_key"},
	{"state_locks", "stack_id, workspace, lock_id", "''"},
	{"state_workspaces", "stack_id, name", "''"},
}

// Purge deletes up to batch of acct's rows, of stack alone when it is
// set, from the first table of purged that holds any, and answers the
// object keys of the versions it deleted; true once none holds any.
func (s *Store) Purge(
	ctx context.Context,
	acct kernel.AccountID,
	stack *kernel.ULID,
	batch int,
) ([]string, bool, error) {
	for _, p := range purged {
		rows, err := tx.Q(ctx, s.db).Query(ctx, `DELETE FROM `+p.table+
			` WHERE account_id = $1 AND (`+p.key+`) IN (
			SELECT `+p.key+` FROM `+p.table+`
			WHERE account_id = $1 AND ($2::uuid IS NULL OR stack_id = $2) LIMIT $3)
			RETURNING `+p.returning, acct, stack, batch)
		if err != nil {
			return nil, false, fmt.Errorf("state: purge account %s's %s: %w", acct, p.table,
				sql.Unavailable(err))
		}
		keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return nil, false, fmt.Errorf("state: purge account %s's %s: %w", acct, p.table,
				sql.Unavailable(err))
		}
		if len(keys) > 0 {
			if p.returning == "''" {
				keys = nil
			}
			return keys, false, nil
		}
	}
	return nil, true, nil
}
