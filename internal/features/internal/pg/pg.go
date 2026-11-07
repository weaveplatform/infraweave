package pg

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	core "github.com/weaveplatform/infraweave/internal/features/internal/features"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
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

// CreateOrganization gives org its entitlement on the default tier,
// Enterprise while the operator has named none.
func (s *Store) CreateOrganization(ctx context.Context, org kernel.OrganizationID) error {
	_, err := tx.Q(ctx, s.db).
		Exec(ctx, `INSERT INTO organization_entitlements (organization_id, tier)
		SELECT $1, coalesce(default_tier, $2) FROM billing`, org, features.Enterprise)
	return write(err, "tier "+features.Enterprise)
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

// Billing reads the billing switch.
func (s *Store) Billing(ctx context.Context) (bool, error) {
	var on bool
	if err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT enabled FROM billing`).Scan(&on); err != nil {
		return false, fmt.Errorf("features: %w", sql.Unavailable(err))
	}
	return on, nil
}

// tiers reads every tier's content.
const tiers = `
SELECT t.name,
  ARRAY(SELECT feature FROM tier_features f WHERE f.tier = t.name ORDER BY 1),
  coalesce((SELECT jsonb_object_agg(limit_name, value) FROM tier_limits l
            WHERE l.tier = t.name), '{}')
FROM tiers t ORDER BY t.name`

// Tiers reads every tier's content, in name order.
func (s *Store) Tiers(ctx context.Context) ([]features.Tier, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, tiers)
	if err != nil {
		return nil, fmt.Errorf("features: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (features.Tier, error) {
		var t features.Tier
		err := r.Scan(&t.Name, &t.Features, &t.Limits)
		return t, err //nolint:wrapcheck // CollectRows' caller wraps it
	})
	if err != nil {
		return nil, fmt.Errorf("features: %w", sql.Unavailable(err))
	}
	return out, nil
}

// Seed installs each of seeds as a tier the first time, and later replaces
// a seeded tier's content only while it equals what the last seed gave it.
// It holds a lock on tiers that keeps replicas starting together, and any
// tier edit, from interleaving.
func (s *Store) Seed(ctx context.Context, seeds []features.Tier) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if _, err := q.Exec(ctx, `LOCK TABLE tiers IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return fmt.Errorf("features: %w", sql.Unavailable(err))
		}
		current, err := s.Tiers(ctx)
		if err != nil {
			return err
		}
		changed := false
		for _, seed := range seeds {
			applied, err := s.seed(ctx, seed, current)
			if err != nil {
				return err
			}
			changed = changed || applied
		}
		if !changed {
			return nil
		}
		// Delivered on commit, so a replica already serving drops the
		// tiers and entitlements it read before this release's seeds.
		if err := notify.Notify(ctx, q, notify.Invalidate, core.TiersKey); err != nil {
			return fmt.Errorf("features: %w", sql.Unavailable(err))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("seed the tiers: %w", err)
	}
	return nil
}

// seed applies one seeded tier, given every tier's current content, and
// reports whether it wrote one whose content differs from what was there.
func (s *Store) seed(
	ctx context.Context,
	seed features.Tier,
	current []features.Tier,
) (bool, error) {
	q := tx.Q(ctx, s.db)
	var last features.Tier
	err := q.QueryRow(ctx, `SELECT features, limits FROM tier_seeds WHERE tier = $1`, seed.Name).
		Scan(&last.Features, &last.Limits)
	seeded := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("features: %w", sql.Unavailable(err))
	}
	i := slices.IndexFunc(current, func(t features.Tier) bool { return t.Name == seed.Name })
	switch {
	case seeded && i < 0:
		// The operator deleted it.
		return false, nil
	case seeded && !same(current[i], last):
		// The operator edited it.
		return false, nil
	case !seeded && i >= 0:
		// The operator made a tier of the same name first; it stays theirs.
		return false, nil
	case seeded && same(last, seed):
		return false, nil
	}
	stmts := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO tiers (name) VALUES ($1) ON CONFLICT DO NOTHING`, []any{seed.Name}},
		{`DELETE FROM tier_features WHERE tier = $1`, []any{seed.Name}},
		{`DELETE FROM tier_limits WHERE tier = $1`, []any{seed.Name}},
		{
			`INSERT INTO tier_features (tier, feature) SELECT $1, unnest($2::text[])`,
			[]any{seed.Name, seed.Features},
		},
		{`INSERT INTO tier_limits (tier, limit_name, value)
			SELECT $1, key, value::bigint FROM jsonb_each_text($2)`, []any{seed.Name, seed.Limits}},
		{
			`INSERT INTO tier_seeds (tier, features, limits) VALUES ($1, $2, $3)
			ON CONFLICT (tier) DO UPDATE
			SET features = EXCLUDED.features, limits = EXCLUDED.limits, seeded_at = now()`,
			[]any{seed.Name, seed.Features, seed.Limits},
		},
	}
	for _, st := range stmts {
		if _, err := q.Exec(ctx, st.sql, st.args...); err != nil {
			return false, fmt.Errorf("features: %w", sql.Unavailable(err))
		}
	}
	return true, nil
}

// same reports whether two tiers' contents are equal.
func same(a, b features.Tier) bool {
	return slices.Equal(
		slices.Sorted(slices.Values(a.Features)),
		slices.Sorted(slices.Values(b.Features)),
	) &&
		maps.Equal(a.Limits, b.Limits)
}
