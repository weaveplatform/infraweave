package pg

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// StreamName is the features stream's name in schema_migrations.
const StreamName = "features"

// files are the features stream's migrations.
//
//go:embed migrations/*.sql
var files embed.FS

// Stream is the features domain's migration stream, which internal/app
// registers first after the platform's, ahead of access.
func Stream() migrate.Stream {
	sub, _ := fs.Sub(files, "migrations") // a valid path, so never an error
	return migrate.Stream{Name: StreamName, Files: sub}
}

// DB is what the store needs of PostgreSQL: queries, and transactions for
// a write of several rows.
type DB interface {
	tx.Querier
	tx.Beginner
}

// Store keeps tiers, entitlements, grants and account limits in
// PostgreSQL. Every method joins the transaction ctx carries.
type Store struct{ db DB }

// New returns the store on db.
func New(db DB) *Store { return &Store{db: db} }

// foreignKeyViolation is SQLSTATE 23503: here a tier or an entitlement
// that does not exist.
const foreignKeyViolation = "23503"

// write classifies err of a write: a missing tier or entitlement is
// ErrNotFound naming what, and anything else an error of the store.
func write(err error, what string) error {
	if err == nil {
		return nil
	}
	if pe, ok := errors.AsType[*pgconn.PgError](err); ok && pe.Code == foreignKeyViolation {
		return fmt.Errorf("%w: %s", featuresapi.ErrNotFound, what)
	}
	return fmt.Errorf("features: %w", sql.Unavailable(err))
}

// CreateOrganization gives org its entitlement on tier.
func (s *Store) CreateOrganization(
	ctx context.Context,
	org kernel.OrganizationID,
	tier string,
) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx,
		`INSERT INTO organization_entitlements (organization_id, tier) VALUES ($1, $2)`, org, tier)
	return write(err, "tier "+tier)
}

// deleteOrganization deletes an organization's rows, referencing tables
// first, since every foreign key restricts.
var deleteOrganization = []string{
	`DELETE FROM account_limits WHERE organization_id = $1`,
	`DELETE FROM organization_limit_grants WHERE organization_id = $1`,
	`DELETE FROM organization_feature_grants WHERE organization_id = $1`,
	`DELETE FROM organization_entitlements WHERE organization_id = $1`,
}

// DeleteOrganization deletes org's entitlement, grants and account limits.
func (s *Store) DeleteOrganization(ctx context.Context, org kernel.OrganizationID) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		for _, stmt := range deleteOrganization {
			if _, err := q.Exec(ctx, stmt, org); err != nil {
				return fmt.Errorf("features: %w", sql.Unavailable(err))
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete organization %s: %w", org, err)
	}
	return nil
}

// SetAccountLimit sets acct's limit on limit.
func (s *Store) SetAccountLimit(
	ctx context.Context,
	org kernel.OrganizationID,
	acct kernel.AccountID,
	limit string,
	value int64,
) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `
		INSERT INTO account_limits (organization_id, account_id, limit_name, value)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (account_id, limit_name)
		DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, org, acct, limit, value)
	return write(err, "entitlement of organization "+org.String())
}

// DeleteAccount deletes acct's limits.
func (s *Store) DeleteAccount(ctx context.Context, acct kernel.AccountID) error {
	if _, err := tx.Q(ctx, s.db).Exec(ctx,
		`DELETE FROM account_limits WHERE account_id = $1`, acct); err != nil {
		return fmt.Errorf("features: %w", sql.Unavailable(err))
	}
	return nil
}

// entitlement reads an organization's entitlement in one statement: its
// features are its tier's and its grants', and each limit's value is the
// grant's when one exists, else the tier's, a grant of NULL lifting it.
const entitlement = `
SELECT e.tier, e.entitlement_version,
  ARRAY(SELECT f.feature FROM (
      SELECT feature FROM tier_features WHERE tier = e.tier
      UNION
      SELECT feature FROM organization_feature_grants WHERE organization_id = e.organization_id
    ) f ORDER BY 1),
  coalesce((SELECT jsonb_object_agg(l.limit_name, l.value) FROM (
      SELECT limit_name, CASE WHEN g.limit_name IS NULL THEN t.value ELSE g.value END AS value
      FROM (SELECT limit_name, value FROM tier_limits WHERE tier = e.tier) t
      FULL JOIN (SELECT limit_name, value FROM organization_limit_grants
                 WHERE organization_id = e.organization_id) g USING (limit_name)
    ) l WHERE l.value IS NOT NULL), '{}')
FROM organization_entitlements e WHERE e.organization_id = $1`

// Entitlement reads org's entitlement, locking its row until ctx's
// transaction ends when lock is set.
func (s *Store) Entitlement(
	ctx context.Context,
	org kernel.OrganizationID,
	lock bool,
) (featuresapi.Entitlement, error) {
	query := entitlement
	if lock {
		query += ` FOR UPDATE OF e`
	}
	var e featuresapi.Entitlement
	err := tx.Q(ctx, s.db).QueryRow(ctx, query, org).
		Scan(&e.Tier, &e.Version, &e.Features, &e.Limits)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return e, fmt.Errorf("%w: entitlement of organization %s", featuresapi.ErrNotFound, org)
	case err != nil:
		return e, fmt.Errorf("features: %w", sql.Unavailable(err))
	}
	return e, nil
}

// AccountLimit reads acct's limit on limit; ok is false when it has none.
func (s *Store) AccountLimit(
	ctx context.Context,
	acct kernel.AccountID,
	limit string,
) (value int64, ok bool, err error) {
	err = tx.Q(ctx, s.db).QueryRow(ctx,
		`SELECT value FROM account_limits WHERE account_id = $1 AND limit_name = $2`,
		acct, limit).Scan(&value)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("features: %w", sql.Unavailable(err))
	}
	return value, true, nil
}
