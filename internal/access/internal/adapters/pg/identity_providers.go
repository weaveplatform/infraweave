package pg

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var _ sso.Store = (*Store)(nil)

// providerSecret is an identity provider's sealed secret: the OIDC client
// secret, the SAML service provider's private key or the GitHub OAuth
// client secret, bound to the provider's id as its uuid text.
var providerSecret = crypt.Column{
	Table: "identity_providers",
	Name:  "secret_sealed",
	Key:   []string{"id"},
}

// WithSealer returns s sealing identity providers' secrets through seal.
func (s *Store) WithSealer(seal Sealer) *Store {
	s.seal = seal
	return s
}

// providerColumns are what scanProvider reads, in its order.
const providerColumns = `organization_id, id, kind, name, settings, is_default, enabled_at,
	coalesce(admin_login, ''), created_at, updated_at`

func scanProvider(r pgx.Row) (sso.Provider, error) {
	var (
		p        sso.Provider
		settings []byte
		enabled  *time.Time
	)
	if err := r.Scan(&p.OrganizationID, &p.ID, &p.Kind, &p.Name, &settings, &p.Default, &enabled,
		&p.AdminLogin, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return sso.Provider{}, err //nolint:wrapcheck // the caller names the read
	}
	if enabled != nil {
		p.EnabledAt = *enabled
	}
	var err error
	if p.Settings, err = sso.ParseSettings(settings); err != nil {
		return sso.Provider{}, fmt.Errorf("access: identity provider %s: %w", p.ID, err)
	}
	return p, nil
}

// Providers are org's identity providers, oldest first.
func (s *Store) Providers(ctx context.Context, org kernel.OrganizationID) ([]sso.Provider, error) {
	return providers(ctx, tx.Q(ctx, s.db), org)
}

func providers(ctx context.Context, q tx.Querier, org kernel.OrganizationID) ([]sso.Provider, error) {
	rows, err := q.Query(ctx, `SELECT `+providerColumns+` FROM identity_providers
		WHERE organization_id = $1 ORDER BY created_at, id`, org)
	if err != nil {
		return nil, fmt.Errorf("access: list identity providers: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (sso.Provider, error) {
		return scanProvider(r)
	})
	if err != nil {
		return nil, fmt.Errorf("access: list identity providers: %w", sql.Unavailable(err))
	}
	return out, nil
}

// Provider reads org's identity provider id; another organization's is not
// found.
func (s *Store) Provider(ctx context.Context, org kernel.OrganizationID, id kernel.ULID) (sso.Provider, error) {
	p, err := scanProvider(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+providerColumns+`
		FROM identity_providers WHERE organization_id = $1 AND id = $2`, org, id))
	return p, read(err, "identity provider", id)
}

// ProviderSecret opens org's identity provider id's secret, which only a
// sign-in through the provider reads.
func (s *Store) ProviderSecret(ctx context.Context, org kernel.OrganizationID, id kernel.ULID) (crypt.Value, error) {
	var sealed []byte
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT secret_sealed FROM identity_providers
		WHERE organization_id = $1 AND id = $2`, org, id).Scan(&sealed)
	if err := read(err, "identity provider", id); err != nil {
		return crypt.Value{}, err
	}
	v, err := s.seal.OpenColumn(ctx, providerSecret, []string{id.UUID()}, sealed)
	if err != nil {
		return crypt.Value{}, fmt.Errorf("access: open identity provider %s's secret: %w", id, err)
	}
	return v, nil
}

// lockProviders takes org's row lock, which every write of its providers
// takes first, so two writes never both see no default of a kind, then
// reads its providers. FOR NO KEY UPDATE leaves the row's key share to
// the inserts that reference it.
func lockProviders(ctx context.Context, q tx.Querier, org kernel.OrganizationID) ([]sso.Provider, error) {
	var id kernel.OrganizationID
	err := q.QueryRow(ctx, `SELECT id FROM organizations WHERE id = $1 FOR NO KEY UPDATE`, org).
		Scan(&id)
	if err := read(err, "organization", org.ULID); err != nil {
		return nil, err
	}
	return providers(ctx, q, org)
}

// CreateProvider inserts p, its kind's default when it is the first of its
// kind, with secret sealed, and records e, in one transaction.
func (s *Store) CreateProvider(ctx context.Context, p sso.Provider, secret []byte, e kernel.OrganizationAuditEntry) (sso.Provider, error) {
	sealed, err := s.seal.SealColumn(ctx, providerSecret, []string{p.ID.UUID()}, secret)
	if err != nil {
		return sso.Provider{}, fmt.Errorf("access: seal identity provider %s's secret: %w", p.ID, err)
	}
	settings, err := json.Marshal(p.Settings)
	if err != nil {
		return sso.Provider{}, fmt.Errorf("access: identity provider %s: %w", p.ID, err)
	}
	var out sso.Provider
	err = s.record(ctx, e, func(ctx context.Context, q tx.Querier) error {
		existing, err := lockProviders(ctx, q, p.OrganizationID)
		if err != nil {
			return err
		}
		out, err = scanProvider(q.QueryRow(ctx, `INSERT INTO identity_providers
				(organization_id, id, kind, name, settings, secret_sealed, is_default, created_at,
				updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING `+providerColumns,
			p.OrganizationID, p.ID, p.Kind, p.Name, settings, sealed,
			sso.FirstOfKind(existing, p.Kind), p.CreatedAt, p.UpdatedAt))
		if err != nil {
			return fmt.Errorf("access: insert identity provider: %w", sql.Unavailable(err))
		}
		return nil
	})
	if err != nil {
		return sso.Provider{}, fmt.Errorf("create identity provider %s: %w", p.ID, err)
	}
	return out, nil
}

// UpdateProvider writes p's name, settings and update time, and secret
// sealed unless it is nil, and records e, in one transaction.
func (s *Store) UpdateProvider(ctx context.Context, p sso.Provider, secret []byte, e kernel.OrganizationAuditEntry) (sso.Provider, error) {
	var sealed []byte
	if secret != nil {
		var err error
		if sealed, err = s.seal.SealColumn(ctx, providerSecret, []string{p.ID.UUID()}, secret); err != nil {
			return sso.Provider{}, fmt.Errorf("access: seal identity provider %s's secret: %w", p.ID, err)
		}
	}
	settings, err := json.Marshal(p.Settings)
	if err != nil {
		return sso.Provider{}, fmt.Errorf("access: identity provider %s: %w", p.ID, err)
	}
	var out sso.Provider
	err = s.record(ctx, e, func(ctx context.Context, q tx.Querier) error {
		var err error
		out, err = scanProvider(q.QueryRow(ctx, `UPDATE identity_providers
			SET name = $3, settings = $4, secret_sealed = coalesce($5, secret_sealed),
				updated_at = $6
			WHERE organization_id = $1 AND id = $2 RETURNING `+providerColumns,
			p.OrganizationID, p.ID, p.Name, settings, sealed, p.UpdatedAt))
		return read(err, "identity provider", p.ID)
	})
	if err != nil {
		return sso.Provider{}, fmt.Errorf("update identity provider %s: %w", p.ID, err)
	}
	return out, nil
}

// DeleteProvider deletes org's identity provider id and the identities
// that sign in through it, revokes every session signed in through it,
// makes the oldest remaining provider of its kind the default when it
// was, and records e, in one transaction. It returns the provider as it
// was and the revoked sessions.
func (s *Store) DeleteProvider(ctx context.Context, org kernel.OrganizationID, id kernel.ULID, e kernel.OrganizationAuditEntry) (sso.Provider, []kernel.ULID, error) {
	var (
		gone sso.Provider
		ids  []kernel.ULID
	)
	err := s.record(ctx, e, func(ctx context.Context, q tx.Querier) error {
		existing, err := lockProviders(ctx, q, org)
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `DELETE FROM member_identities
			WHERE organization_id = $1 AND identity_provider_id = $2`, org, id); err != nil {
			return fmt.Errorf("access: delete identities: %w", sql.Unavailable(err))
		}
		if gone, err = scanProvider(q.QueryRow(ctx, `DELETE FROM identity_providers
			WHERE organization_id = $1 AND id = $2 RETURNING `+providerColumns, org, id)); err != nil {
			return read(err, "identity provider", id)
		}
		revoked, err := revoke(ctx, q, `organization_id = $1 AND identity_provider_id = $2`, org, id)
		if err != nil {
			return err
		}
		for _, ss := range revoked {
			ids = append(ids, ss.ID)
		}
		if !gone.Default {
			return nil
		}
		remaining := make([]sso.Provider, 0, len(existing))
		for _, p := range existing {
			if p.ID != id {
				remaining = append(remaining, p)
			}
		}
		next, ok := sso.Successor(remaining, gone.Kind)
		if !ok {
			return nil
		}
		if _, err := q.Exec(ctx, `UPDATE identity_providers SET is_default = true
			WHERE id = $1`, next.ID); err != nil {
			return fmt.Errorf("access: promote identity provider %s: %w", next.ID, sql.Unavailable(err))
		}
		return nil
	})
	if err != nil {
		return sso.Provider{}, nil, fmt.Errorf("delete identity provider %s: %w", id, err)
	}
	return gone, ids, nil
}

// SetDefaultProvider makes org's identity provider id its kind's default,
// in place of the one that was, and records e, in one transaction. The
// default index allows one default per kind at every statement, so the
// one that was is cleared first.
func (s *Store) SetDefaultProvider(ctx context.Context, org kernel.OrganizationID, id kernel.ULID, e kernel.OrganizationAuditEntry) (sso.Provider, error) {
	var out sso.Provider
	err := s.record(ctx, e, func(ctx context.Context, q tx.Querier) error {
		if _, err := lockProviders(ctx, q, org); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `UPDATE identity_providers SET is_default = false
			WHERE organization_id = $1 AND is_default AND id <> $2
				AND kind = (SELECT kind FROM identity_providers WHERE organization_id = $1 AND id = $2)`,
			org, id); err != nil {
			return fmt.Errorf("access: clear the default: %w", sql.Unavailable(err))
		}
		var err error
		out, err = scanProvider(q.QueryRow(ctx, `UPDATE identity_providers SET is_default = true
			WHERE organization_id = $1 AND id = $2 RETURNING `+providerColumns, org, id))
		return read(err, "identity provider", id)
	})
	if err != nil {
		return sso.Provider{}, fmt.Errorf("set default identity provider %s: %w", id, err)
	}
	return out, nil
}
