package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/features/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var _ core.Store = (*Store)(nil)

// SQLSTATEs of the operator's writes: a tier name in use, and a tier an
// ON DELETE RESTRICT reference still holds, which PostgreSQL reports as
// 23001 rather than the foreign key violation's 23503.
const (
	uniqueViolation   = "23505"
	restrictViolation = "23001"
)

// HeldTiers reads every tier with how many organizations hold it, in name
// order.
func (s *Store) HeldTiers(ctx context.Context) ([]core.Held, error) {
	ts, err := s.Tiers(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Q(ctx, s.db).Query(ctx,
		`SELECT tier, count(*) FROM organization_entitlements GROUP BY tier`)
	if err != nil {
		return nil, fmt.Errorf("features: %w", sql.Unavailable(err))
	}
	held := map[string]int64{}
	var (
		tier string
		n    int64
	)
	if _, err := pgx.ForEachRow(rows, []any{&tier, &n}, func() error {
		held[tier] = n
		return nil
	}); err != nil {
		return nil, fmt.Errorf("features: %w", sql.Unavailable(err))
	}
	out := make([]core.Held, 0, len(ts))
	for _, t := range ts {
		out = append(out, core.Held{Tier: t, Organizations: held[t.Name]})
	}
	return out, nil
}

// lockTiers takes the lock Seed takes, so a release's seed and the
// operator's edit never interleave.
func lockTiers(ctx context.Context, q tx.Querier) error {
	if _, err := q.Exec(ctx, `LOCK TABLE tiers IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return fmt.Errorf("features: %w", sql.Unavailable(err))
	}
	return nil
}

// content writes t's features and limits, replacing what the tier held.
func content(ctx context.Context, q tx.Querier, t features.Tier) error {
	for _, st := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM tier_features WHERE tier = $1`, []any{t.Name}},
		{`DELETE FROM tier_limits WHERE tier = $1`, []any{t.Name}},
		{
			`INSERT INTO tier_features (tier, feature) SELECT $1, unnest($2::text[])`,
			[]any{t.Name, t.Features},
		},
		{`INSERT INTO tier_limits (tier, limit_name, value)
			SELECT $1, key, value::bigint FROM jsonb_each_text($2)`, []any{t.Name, t.Limits}},
	} {
		if _, err := q.Exec(ctx, st.sql, st.args...); err != nil {
			return fmt.Errorf("features: %w", sql.Unavailable(err))
		}
	}
	return nil
}

// CreateTier creates t; a name another tier has is core.ErrExists.
func (s *Store) CreateTier(ctx context.Context, t features.Tier) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if err := lockTiers(ctx, q); err != nil {
			return err
		}
		_, err := q.Exec(ctx, `INSERT INTO tiers (name) VALUES ($1)`, t.Name)
		if pe, ok := errors.AsType[*pgconn.PgError](err); ok && pe.Code == uniqueViolation {
			return fmt.Errorf("%w: %q", core.ErrExists, t.Name)
		}
		if err != nil {
			return fmt.Errorf("features: %w", sql.Unavailable(err))
		}
		return content(ctx, q, t)
	})
	if err != nil {
		return fmt.Errorf("create tier %q: %w", t.Name, err)
	}
	return nil
}

// ReplaceTier replaces an existing tier's content, bumps the version of
// every organization that holds it, and tells every replica.
func (s *Store) ReplaceTier(ctx context.Context, t features.Tier) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if err := lockTiers(ctx, q); err != nil {
			return err
		}
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tiers WHERE name = $1)`, t.Name).
			Scan(&exists); err != nil {
			return fmt.Errorf("features: %w", sql.Unavailable(err))
		}
		if !exists {
			return fmt.Errorf("%w: tier %q", featuresapi.ErrNotFound, t.Name)
		}
		if err := content(ctx, q, t); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `UPDATE organization_entitlements
			SET entitlement_version = entitlement_version + 1 WHERE tier = $1`, t.Name); err != nil {
			return fmt.Errorf("features: %w", sql.Unavailable(err))
		}
		return tell(ctx, q, core.TiersKey)
	})
	if err != nil {
		return fmt.Errorf("replace tier %q: %w", t.Name, err)
	}
	return nil
}

// DeleteTier deletes a tier, its features and its limits; an organization
// or the default holding it is core.ErrInvalidState. Its tier_seeds row
// stays, so a release never installs a deleted seeded tier again.
func (s *Store) DeleteTier(ctx context.Context, name string) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if err := lockTiers(ctx, q); err != nil {
			return err
		}
		for _, stmt := range []string{
			`DELETE FROM tier_features WHERE tier = $1`,
			`DELETE FROM tier_limits WHERE tier = $1`,
		} {
			if _, err := q.Exec(ctx, stmt, name); err != nil {
				return fmt.Errorf("features: %w", sql.Unavailable(err))
			}
		}
		tag, err := q.Exec(ctx, `DELETE FROM tiers WHERE name = $1`, name)
		if pe, ok := errors.AsType[*pgconn.PgError](err); ok && pe.Code == restrictViolation {
			return fmt.Errorf("%w: an organization or the default holds tier %q",
				core.ErrInvalidState, name)
		}
		switch {
		case err != nil:
			return fmt.Errorf("features: %w", sql.Unavailable(err))
		case tag.RowsAffected() == 0:
			return fmt.Errorf("%w: tier %q", featuresapi.ErrNotFound, name)
		}
		return tell(ctx, q, core.TiersKey)
	})
	if err != nil {
		return fmt.Errorf("delete tier %q: %w", name, err)
	}
	return nil
}

// BillingState reads the switch and the default tier, Enterprise while
// the operator has named none.
func (s *Store) BillingState(ctx context.Context) (core.Billing, error) {
	var b core.Billing
	err := tx.Q(ctx, s.db).QueryRow(ctx,
		`SELECT enabled, coalesce(default_tier, $1) FROM billing`, features.Enterprise).
		Scan(&b.Enabled, &b.DefaultTier)
	if err != nil {
		return b, fmt.Errorf("features: %w", sql.Unavailable(err))
	}
	return b, nil
}

// SetBilling sets the switch and, when defaultTier is not nil, the default
// tier, which must exist.
func (s *Store) SetBilling(ctx context.Context, enabled bool, defaultTier *string) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		_, err := q.Exec(ctx, `UPDATE billing SET enabled = $1,
			default_tier = CASE WHEN $2 THEN $3 ELSE default_tier END, updated_at = now()`,
			enabled, defaultTier != nil, defaultTier)
		if err != nil {
			return write(err, "tier "+deref(defaultTier))
		}
		return tell(ctx, q, core.BillingKey)
	})
	if err != nil {
		return fmt.Errorf("set billing: %w", err)
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Grants reads org's grants; an organization without an entitlement is
// featuresapi.ErrNotFound.
func (s *Store) Grants(ctx context.Context, org kernel.OrganizationID) (core.Grants, error) {
	g := core.Grants{Limits: map[string]*int64{}}
	var exists bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM organization_entitlements WHERE organization_id = $1),
			ARRAY(SELECT feature FROM organization_feature_grants
				WHERE organization_id = $1 ORDER BY feature COLLATE "C")`, org).Scan(&exists, &g.Features)
	if err != nil {
		return g, fmt.Errorf("features: %w", sql.Unavailable(err))
	}
	if !exists {
		return g, fmt.Errorf("%w: entitlement of organization %s", featuresapi.ErrNotFound, org)
	}
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT limit_name, value
		FROM organization_limit_grants WHERE organization_id = $1`, org)
	if err != nil {
		return g, fmt.Errorf("features: %w", sql.Unavailable(err))
	}
	var (
		name  string
		value *int64
	)
	if _, err := pgx.ForEachRow(rows, []any{&name, &value}, func() error {
		g.Limits[name] = value
		return nil
	}); err != nil {
		return g, fmt.Errorf("features: %w", sql.Unavailable(err))
	}
	return g, nil
}

// touch bumps org's entitlement version under its row lock and tells
// every replica; an organization without an entitlement is
// featuresapi.ErrNotFound. The lock orders the write after a limit check
// already counting under it.
func touch(ctx context.Context, q tx.Querier, org kernel.OrganizationID) error {
	tag, err := q.Exec(ctx, `UPDATE organization_entitlements
		SET entitlement_version = entitlement_version + 1 WHERE organization_id = $1`, org)
	switch {
	case err != nil:
		return fmt.Errorf("features: %w", sql.Unavailable(err))
	case tag.RowsAffected() == 0:
		return fmt.Errorf("%w: entitlement of organization %s", featuresapi.ErrNotFound, org)
	}
	return tell(ctx, q, core.InvalidateKey(org))
}

// tell notifies infraweave_invalidate with payload, delivered when the
// transaction commits.
func tell(ctx context.Context, q tx.Querier, payload string) error {
	if err := notify.Notify(ctx, q, notify.Invalidate, payload); err != nil {
		return fmt.Errorf("features: %w", sql.Unavailable(err))
	}
	return nil
}

// change runs fn on org's entitlement after touch, in one transaction.
func (s *Store) change(
	ctx context.Context,
	org kernel.OrganizationID,
	what string,
	fn func(context.Context, tx.Querier) error,
) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if err := touch(ctx, q, org); err != nil {
			return err
		}
		return fn(ctx, q)
	})
	if err != nil {
		return fmt.Errorf("%s of organization %s: %w", what, org, err)
	}
	return nil
}

// SetTier moves org to tier, which must exist.
func (s *Store) SetTier(ctx context.Context, org kernel.OrganizationID, tier string) error {
	return s.change(ctx, org, "set the tier", func(ctx context.Context, q tx.Querier) error {
		_, err := q.Exec(ctx,
			`UPDATE organization_entitlements SET tier = $2 WHERE organization_id = $1`, org, tier)
		if pe, ok := errors.AsType[*pgconn.PgError](err); ok && pe.Code == foreignKeyViolation {
			return fmt.Errorf("%w: %q", core.ErrUnknownTier, tier)
		}
		return write(err, "tier "+tier)
	})
}

// GrantFeature grants org feature; granting it again changes nothing.
func (s *Store) GrantFeature(ctx context.Context, org kernel.OrganizationID, feature string) error {
	return s.change(ctx, org, "grant "+feature, func(ctx context.Context, q tx.Querier) error {
		_, err := q.Exec(ctx, `INSERT INTO organization_feature_grants (organization_id, feature)
			VALUES ($1, $2) ON CONFLICT DO NOTHING`, org, feature)
		return write(err, "entitlement of organization "+org.String())
	})
}

// RevokeFeature removes org's grant of feature; one org does not hold is
// featuresapi.ErrNotFound.
func (s *Store) RevokeFeature(
	ctx context.Context,
	org kernel.OrganizationID,
	feature string,
) error {
	return s.change(ctx, org, "revoke "+feature, func(ctx context.Context, q tx.Querier) error {
		return removed(q.Exec(ctx, `DELETE FROM organization_feature_grants
			WHERE organization_id = $1 AND feature = $2`, org, feature))("grant of " + feature)
	})
}

// GrantLimit sets org's value of limit, nil lifting it.
func (s *Store) GrantLimit(
	ctx context.Context,
	org kernel.OrganizationID,
	limit string,
	value *int64,
) error {
	return s.change(ctx, org, "grant "+limit, func(ctx context.Context, q tx.Querier) error {
		_, err := q.Exec(ctx, `
			INSERT INTO organization_limit_grants (organization_id, limit_name, value)
			VALUES ($1, $2, $3)
			ON CONFLICT (organization_id, limit_name) DO UPDATE SET value = EXCLUDED.value`,
			org, limit, value)
		return write(err, "entitlement of organization "+org.String())
	})
}

// RevokeLimit removes org's grant of limit; one org does not hold is
// featuresapi.ErrNotFound.
func (s *Store) RevokeLimit(ctx context.Context, org kernel.OrganizationID, limit string) error {
	return s.change(ctx, org, "revoke "+limit, func(ctx context.Context, q tx.Querier) error {
		return removed(q.Exec(ctx, `DELETE FROM organization_limit_grants
			WHERE organization_id = $1 AND limit_name = $2`, org, limit))("grant of " + limit)
	})
}

// removed classifies a DELETE of one grant: none deleted is
// featuresapi.ErrNotFound naming what.
func removed(tag pgconn.CommandTag, err error) func(what string) error {
	return func(what string) error {
		switch {
		case err != nil:
			return fmt.Errorf("features: %w", sql.Unavailable(err))
		case tag.RowsAffected() == 0:
			return fmt.Errorf("%w: %s", featuresapi.ErrNotFound, what)
		}
		return nil
	}
}
