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
type Store struct{ db DB }

var _ port.Tenancy = (*Store)(nil)

// New returns the store on db.
func New(db DB) *Store { return &Store{db: db} }

// CreateOrganization inserts org and first in one transaction, or neither.
func (s *Store) CreateOrganization(
	ctx context.Context,
	org organization.Organization,
	first account.Account,
) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if _, err := q.Exec(ctx,
			`INSERT INTO organizations (id, slug, name) VALUES ($1, $2, $3)`,
			org.ID, org.Slug, org.Name); err != nil {
			return classify(err, "organization")
		}
		if _, err := q.Exec(ctx,
			`INSERT INTO accounts (organization_id, id, slug, name) VALUES ($1, $2, $3, $4)`,
			first.OrganizationID, first.ID, first.Slug, first.Name); err != nil {
			return classify(err, "account")
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("create organization %s: %w", org.Slug, err)
	}
	return nil
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
func read(err error, what string, id kernel.ULID) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("%w: %s %s", port.ErrNotFound, what, id)
	}
	return fmt.Errorf("access: read %s %s: %w", what, id, sql.Unavailable(err))
}
