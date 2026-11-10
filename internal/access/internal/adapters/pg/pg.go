package pg

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
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

// Keys are how the access domain finds the rows other domains hold by ID:
// an account, an organization, and a space within its account.
func Keys() []integrity.Key {
	return []integrity.Key{
		{Entity: "account", Table: "accounts", Columns: []string{"id"}},
		{Entity: "organization", Table: "organizations", Columns: []string{"id"}},
		{Entity: "space", Table: "spaces", Columns: []string{"account_id", "id"}},
	}
}

// DB is what the store needs of PostgreSQL: queries, and transactions for
// a write of several rows.
type DB interface {
	tx.Querier
	tx.Beginner
}

// Store is port.Tenancy on PostgreSQL.
type Store struct {
	db DB
	// features checks the seats limit; nil checks none.
	features featuresapi.Entitlements
	// events receives an accessapi.Deleted for each account a deletion
	// removes, which the domains that keep the account's rows consume; nil
	// appends none, and leaves those rows to the nightly orphan count.
	events *outbox.Outbox
	// forget drops this replica's authorization snapshot of an account
	// once a transaction that changes its authorization commits; nil
	// leaves the snapshot to the account's authz:<account> message.
	forget func(kernel.AccountID)
}

var _ port.Tenancy = (*Store)(nil)

// New returns the store on db.
func New(db DB) *Store { return &Store{db: db} }

// WithFeatures returns s checking the seats limit through f in the
// transaction that records an API key's first use in SeatWindow.
func (s *Store) WithFeatures(f featuresapi.Entitlements) *Store {
	s.features = f
	return s
}

// WithEvents returns s appending an accessapi.Deleted to events in the
// transaction that deletes each account.
func (s *Store) WithEvents(events *outbox.Outbox) *Store {
	s.events = events
	return s
}

// WithSnapshots returns s dropping this replica's authorization snapshot
// of an account through forget once a transaction that changes the
// account's authorization commits.
func (s *Store) WithSnapshots(forget func(kernel.AccountID)) *Store {
	s.forget = forget
	return s
}

// deleted has every replica forget the snapshot of each of org's accounts
// accts, and appends an accessapi.Deleted for each, with whole set when
// the organization goes with them.
func (s *Store) deleted(
	ctx context.Context,
	org kernel.OrganizationID,
	accts []kernel.AccountID,
	whole bool,
) error {
	for _, acct := range accts {
		if err := s.forgetAuthz(ctx, tx.Q(ctx, s.db), acct); err != nil {
			return err
		}
		if s.events == nil {
			continue
		}
		doc, err := json.Marshal(accessapi.Deleted{
			Account: acct.ULID, Organization: org.ULID, OrganizationDeleted: whole,
		})
		if err != nil {
			return fmt.Errorf("access: %w", err)
		}
		if _, err := s.events.Append(ctx, outbox.Event{
			Account:       acct.ULID,
			Kind:          accessapi.DeletedKind,
			SchemaVersion: accessapi.DeletedVersion,
			Subject:       outbox.Subject{Kind: "account", ID: acct.String()},
			Sequence:      1,
			Document:      doc,
		}); err != nil {
			return fmt.Errorf("access: %w", err)
		}
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
func read(err error, what string, id any) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("%w: %s %s", port.ErrNotFound, what, id)
	}
	return fmt.Errorf("access: read %s %s: %w", what, id, sql.Unavailable(err))
}
