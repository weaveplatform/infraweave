package pg

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
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
