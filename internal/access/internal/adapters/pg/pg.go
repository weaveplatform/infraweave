package pg

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// StreamName is the access stream's name in schema_migrations.
const StreamName = "access"

// files are the access stream's migrations.
//
//go:embed migrations/*.sql
var files embed.FS

// Stream is the access domain's migration stream, which internal/app
// registers first after the platform's.
func Stream() migrate.Stream {
	sub, _ := fs.Sub(files, "migrations") // a valid path, so never an error
	return migrate.Stream{Name: StreamName, Files: sub}
}

// SealedColumns are the access stream's sealed columns, which Reseal walks
// with the platform's.
func SealedColumns() []crypt.Column { return []crypt.Column{privateKey} }

// DB is what the store needs of PostgreSQL: queries, and transactions for
// a write of several rows.
type DB interface {
	tx.Querier
	tx.Beginner
}

// Store is port.Tenancy on PostgreSQL.
type Store struct {
	db DB
	// features holds the features domain's rows of each organization;
	// nil leaves them to whoever holds them.
	features featuresapi.Entitlements
}

var _ port.Tenancy = (*Store)(nil)

// New returns the store on db.
func New(db DB) *Store { return &Store{db: db} }

// WithFeatures returns s deleting an organization's rows in the features
// domain, through f, in the transaction that deletes the organization.
func (s *Store) WithFeatures(f featuresapi.Entitlements) *Store {
	s.features = f
	return s
}

// Organization reads the organization id.
func (s *Store) Organization(
	ctx context.Context,
	id kernel.OrganizationID,
) (organization.Organization, error) {
	var o organization.Organization
	err := tx.Q(ctx, s.db).QueryRow(ctx,
		`SELECT id, slug, name, created_at FROM organizations WHERE id = $1`, id).
		Scan(&o.ID, &o.Slug, &o.Name, &o.CreatedAt)
	return o, read(err, "organization", id.ULID)
}

// Account reads the account id.
func (s *Store) Account(ctx context.Context, id kernel.AccountID) (account.Account, error) {
	var (
		a       account.Account
		claimed *time.Time
	)
	err := tx.Q(ctx, s.db).QueryRow(ctx,
		`SELECT organization_id, id, slug, name, claimed_at, created_at FROM accounts WHERE id = $1`, id).
		Scan(&a.OrganizationID, &a.ID, &a.Slug, &a.Name, &claimed, &a.CreatedAt)
	if claimed != nil {
		a.ClaimedAt = *claimed
	}
	return a, read(err, "account", id.ULID)
}

// classify turns a unique violation on a slug into port.ErrSlugTaken
// naming what of, and marks an unreachable database.
func classify(err error, what string) error {
	if pe, ok := errors.AsType[*pgconn.PgError](err); ok && pe.Code == "23505" {
		return fmt.Errorf("%w: the %s slug is in use", port.ErrSlugTaken, what)
	}
	return fmt.Errorf("access: insert %s: %w", what, sql.Unavailable(err))
}

// read turns no row into port.ErrNotFound naming the ID.
func read(err error, what string, id any) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("%w: %s %s", port.ErrNotFound, what, id)
	}
	return fmt.Errorf("access: read %s %s: %w", what, id, sql.Unavailable(err))
}
