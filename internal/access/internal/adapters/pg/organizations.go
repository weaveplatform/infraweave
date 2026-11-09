package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/breakglass"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/operator"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/audit"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var _ operator.Store = (*Store)(nil)

// CreateOrganization records org, its entitlement on the default tier
// through the features domain, its first account, its break-glass
// credential when bg is not nil, and e, in one transaction or none of
// them.
func (s *Store) CreateOrganization(
	ctx context.Context,
	org organization.Organization,
	first account.Account,
	bg *breakglass.Credential,
	e kernel.OrganizationAuditEntry,
) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if _, err := q.Exec(ctx,
			`INSERT INTO organizations (id, slug, name, created_at)
				VALUES ($1, $2, $3, coalesce($4, now()))`,
			org.ID, org.Slug, org.Name, nullTime(org.CreatedAt)); err != nil {
			return classify(err, "organization")
		}
		if s.features != nil {
			if err := s.features.CreateOrganization(ctx, org.ID); err != nil {
				return fmt.Errorf("access: %w", err)
			}
		}
		if _, err := q.Exec(
			ctx,
			`INSERT INTO accounts (organization_id, id, slug, name, created_at)
			VALUES ($1, $2, $3, $4, coalesce($5, now()))`,
			first.OrganizationID,
			first.ID,
			first.Slug,
			first.Name,
			nullTime(first.CreatedAt),
		); err != nil {
			return classify(err, "account")
		}
		if err := seedAccount(ctx, q, first); err != nil {
			return err
		}
		if bg != nil {
			if err := setBreakGlass(ctx, q, org.ID, *bg); err != nil {
				return err
			}
		}
		_, err := audit.RecordOrganization(ctx, e)
		return err //nolint:wrapcheck // names itself
	})
	if err != nil {
		return fmt.Errorf("create organization %s: %w", org.Slug, err)
	}
	return nil
}

// orgColumns are an organization's, with its break-glass login and when
// it was set, NULL for none.
const orgColumns = `o.id, o.slug, o.name, o.created_at, bg.login, bg.updated_at
	FROM organizations o LEFT JOIN organization_break_glass bg ON bg.organization_id = o.id`

// Organizations are up to limit organizations in ID order from the first
// after after, with their accounts and break-glass logins.
func (s *Store) Organizations(
	ctx context.Context,
	after kernel.OrganizationID,
	limit int,
) ([]operator.Organization, error) {
	q := tx.Q(ctx, s.db)
	rows, err := q.Query(ctx, `SELECT `+orgColumns+`
		WHERE $1::uuid IS NULL OR o.id > $1 ORDER BY o.id LIMIT $2`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("access: list organizations: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (operator.Organization, error) {
		return scanOrganization(r)
	})
	if err != nil {
		return nil, fmt.Errorf("access: list organizations: %w", sql.Unavailable(err))
	}
	if err := s.accountsOf(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// OrganizationBySlug reads the organization slug, with its accounts and
// break-glass login.
func (s *Store) OrganizationBySlug(
	ctx context.Context,
	slug kernel.Slug,
) (operator.Organization, error) {
	o, err := scanOrganization(tx.Q(ctx, s.db).QueryRow(ctx,
		`SELECT `+orgColumns+` WHERE o.slug = $1`, slug))
	if errors.Is(err, pgx.ErrNoRows) {
		return operator.Organization{}, fmt.Errorf("%w: organization %s", port.ErrNotFound, slug)
	}
	if err != nil {
		return operator.Organization{}, fmt.Errorf("access: read organization %s: %w", slug,
			sql.Unavailable(err))
	}
	out := []operator.Organization{o}
	if err := s.accountsOf(ctx, out); err != nil {
		return operator.Organization{}, err
	}
	return out[0], nil
}

func scanOrganization(r pgx.Row) (operator.Organization, error) {
	var (
		o     operator.Organization
		login *string
		set   *time.Time
	)
	if err := r.Scan(&o.ID, &o.Slug, &o.Name, &o.CreatedAt, &login, &set); err != nil {
		return operator.Organization{}, err //nolint:wrapcheck // the caller names the read
	}
	if login != nil {
		o.BreakGlass = &breakglass.Credential{Login: *login, UpdatedAt: *set}
	}
	return o, nil
}

// accountsOf reads the accounts of orgs into them, oldest first.
func (s *Store) accountsOf(ctx context.Context, orgs []operator.Organization) error {
	ids := make([]string, 0, len(orgs))
	at := make(map[kernel.OrganizationID]int, len(orgs))
	for i, o := range orgs {
		ids = append(ids, o.ID.UUID())
		at[o.ID] = i
		orgs[i].Accounts = []account.Account{}
	}
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT organization_id, id, slug, name, claimed_at,
		created_at FROM accounts WHERE organization_id = ANY($1::uuid[]) ORDER BY id`, ids)
	if err != nil {
		return fmt.Errorf("access: read accounts: %w", sql.Unavailable(err))
	}
	accts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (account.Account, error) {
		var (
			a       account.Account
			claimed *time.Time
		)
		err := r.Scan(&a.OrganizationID, &a.ID, &a.Slug, &a.Name, &claimed, &a.CreatedAt)
		if claimed != nil {
			a.ClaimedAt = *claimed
		}
		return a, err //nolint:wrapcheck // named below
	})
	if err != nil {
		return fmt.Errorf("access: read accounts: %w", sql.Unavailable(err))
	}
	for _, a := range accts {
		i := at[a.OrganizationID]
		orgs[i].Accounts = append(orgs[i].Accounts, a)
	}
	return nil
}

// deleteOrganization are the deletes of everything the access domain holds
// for an organization, $1, in the order its foreign keys allow: a session
// may name a parent session, so the links go first, and a binding names
// its role and space and a space's closure rows name it, so they go
// before the roles and spaces.
var deleteOrganization = []string{
	`UPDATE sessions SET parent_session_id = NULL
		WHERE organization_id = $1 AND parent_session_id IS NOT NULL`,
	`DELETE FROM sessions WHERE organization_id = $1`,
	`DELETE FROM api_keys WHERE account_id IN (SELECT id FROM accounts WHERE organization_id = $1)`,
	`DELETE FROM account_invitations
		WHERE account_id IN (SELECT id FROM accounts WHERE organization_id = $1)`,
	`DELETE FROM managed_user_groups
		WHERE account_id IN (SELECT id FROM accounts WHERE organization_id = $1)`,
	`DELETE FROM account_users WHERE organization_id = $1`,
	`DELETE FROM account_settings
		WHERE account_id IN (SELECT id FROM accounts WHERE organization_id = $1)`,
	`DELETE FROM role_bindings
		WHERE account_id IN (SELECT id FROM accounts WHERE organization_id = $1)`,
	`DELETE FROM role_actions
		WHERE account_id IN (SELECT id FROM accounts WHERE organization_id = $1)`,
	`DELETE FROM roles WHERE account_id IN (SELECT id FROM accounts WHERE organization_id = $1)`,
	`DELETE FROM space_closure
		WHERE account_id IN (SELECT id FROM accounts WHERE organization_id = $1)`,
	`DELETE FROM spaces WHERE account_id IN (SELECT id FROM accounts WHERE organization_id = $1)`,
	`DELETE FROM member_identities WHERE organization_id = $1`,
	`DELETE FROM organization_keys WHERE organization_id = $1`,
	`DELETE FROM organization_admins WHERE organization_id = $1`,
	`DELETE FROM members WHERE organization_id = $1`,
	`DELETE FROM organization_break_glass WHERE organization_id = $1`,
	`DELETE FROM accounts WHERE organization_id = $1`,
}

// DeleteOrganization deletes the organization id with its accounts,
// members, organization admins, API keys, organization keys, sessions and
// break-glass credential, and records e, in one transaction. Each of its
// live sessions is revoked first, so every replica drops it from its
// cache, and returned.
func (s *Store) DeleteOrganization(
	ctx context.Context,
	id kernel.OrganizationID,
	e kernel.OrganizationAuditEntry,
) ([]kernel.ULID, error) {
	var ids []kernel.ULID
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		revoked, err := revoke(ctx, q, `organization_id = $1`, id)
		if err != nil {
			return err
		}
		for _, ss := range revoked {
			ids = append(ids, ss.ID)
		}
		rows, err := q.Query(ctx,
			`SELECT id FROM accounts WHERE organization_id = $1 ORDER BY id`, id)
		if err != nil {
			return fmt.Errorf("access: %w", sql.Unavailable(err))
		}
		accts, err := pgx.CollectRows(rows, pgx.RowTo[kernel.AccountID])
		if err != nil {
			return fmt.Errorf("access: read accounts: %w", sql.Unavailable(err))
		}
		for _, stmt := range deleteOrganization {
			if _, err := q.Exec(ctx, stmt, id); err != nil {
				return fmt.Errorf("access: %w", sql.Unavailable(err))
			}
		}
		tag, err := q.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("access: %w", sql.Unavailable(err))
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: organization %s", port.ErrNotFound, id)
		}
		if err := s.deleted(ctx, id, accts, true); err != nil {
			return err
		}
		_, err = audit.RecordOrganization(ctx, e)
		return err //nolint:wrapcheck // names itself
	})
	if err != nil {
		return nil, fmt.Errorf("delete organization %s: %w", id, err)
	}
	return ids, nil
}

// SetBreakGlass sets or replaces the break-glass credential of the
// organization id and records e.
func (s *Store) SetBreakGlass(
	ctx context.Context,
	id kernel.OrganizationID,
	c breakglass.Credential,
	e kernel.OrganizationAuditEntry,
) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		if err := setBreakGlass(ctx, tx.Q(ctx, s.db), id, c); err != nil {
			return err
		}
		_, err := audit.RecordOrganization(ctx, e)
		return err //nolint:wrapcheck // names itself
	})
	if err != nil {
		return fmt.Errorf("set the break-glass credential of organization %s: %w", id, err)
	}
	return nil
}

func setBreakGlass(
	ctx context.Context,
	q tx.Querier,
	id kernel.OrganizationID,
	c breakglass.Credential,
) error {
	_, err := q.Exec(ctx, `INSERT INTO organization_break_glass (organization_id, login,
		password_hash, updated_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (organization_id) DO UPDATE SET login = EXCLUDED.login,
			password_hash = EXCLUDED.password_hash, updated_at = EXCLUDED.updated_at`,
		id, c.Login, c.Hash.String(), c.UpdatedAt)
	if pe, ok := errors.AsType[*pgconn.PgError](err); ok && pe.Code == "23503" {
		return fmt.Errorf("%w: organization %s", port.ErrNotFound, id)
	}
	if err != nil {
		return fmt.Errorf("access: set break-glass credential: %w", sql.Unavailable(err))
	}
	return nil
}

// DeleteBreakGlass removes the break-glass credential of the organization
// id, if it has one, and records e.
func (s *Store) DeleteBreakGlass(
	ctx context.Context,
	id kernel.OrganizationID,
	e kernel.OrganizationAuditEntry,
) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		if _, err := tx.Q(ctx, s.db).Exec(ctx,
			`DELETE FROM organization_break_glass WHERE organization_id = $1`, id); err != nil {
			return fmt.Errorf("access: %w", sql.Unavailable(err))
		}
		_, err := audit.RecordOrganization(ctx, e)
		return err //nolint:wrapcheck // names itself
	})
	if err != nil {
		return fmt.Errorf("delete the break-glass credential of organization %s: %w", id, err)
	}
	return nil
}
