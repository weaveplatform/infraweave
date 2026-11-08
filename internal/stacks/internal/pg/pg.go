package pg

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/stacks"
)

// StreamName is the stacks stream's name in schema_migrations.
const StreamName = "stacks"

// files are the stacks stream's migrations.
//
//go:embed migrations/*.sql
var files embed.FS

// Stream is the stacks domain's migration stream, which internal/app
// registers after access'.
func Stream() migrate.Stream {
	sub, _ := fs.Sub(files, "migrations") // a valid path, so never an error
	return migrate.Stream{Name: StreamName, Files: sub}
}

// Store keeps stacks in PostgreSQL. Every method joins the transaction ctx
// carries.
type Store struct{ db tx.Querier }

// New returns the store on db.
func New(db tx.Querier) *Store { return &Store{db: db} }

var _ stacks.Store = (*Store)(nil)

// Get reads acct's stack id.
func (s *Store) Get(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (stacksapi.Stack, error) {
	st := stacksapi.Stack{Account: acct}
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT id, slug, space_id FROM stacks
		WHERE account_id = $1 AND id = $2`, acct, id).Scan(&st.ID, &st.Slug, &st.Space)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return stacksapi.Stack{}, fmt.Errorf("%w: stack %s", stacksapi.ErrNotFound, id)
	case err != nil:
		return stacksapi.Stack{}, fmt.Errorf("stacks: read stack %s: %w", id, sql.Unavailable(err))
	}
	return st, nil
}

// SpaceInUse reports whether a stack of acct lives in space.
func (s *Store) SpaceInUse(
	ctx context.Context,
	acct kernel.AccountID,
	space kernel.ULID,
) (bool, error) {
	var used bool
	if err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM stacks
		WHERE account_id = $1 AND space_id = $2)`, acct, space).Scan(&used); err != nil {
		return false, fmt.Errorf("stacks: read stacks in space %s: %w", space, sql.Unavailable(err))
	}
	return used, nil
}
