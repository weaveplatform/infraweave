package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/breakglass"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/audit"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var (
	_ port.BreakGlass   = (*Store)(nil)
	_ session.Admission = (*Store)(nil)
)

// BreakGlassCredential reads the organization slug names and its
// credential with the hash, which only a sign-in reads.
func (s *Store) BreakGlassCredential(
	ctx context.Context,
	slug kernel.Slug,
) (organization.Organization, *breakglass.Credential, error) {
	var (
		o           organization.Organization
		login, hash *string
	)
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT o.id, o.slug, o.name, o.created_at,
			bg.login, bg.password_hash
		FROM organizations o LEFT JOIN organization_break_glass bg ON bg.organization_id = o.id
		WHERE o.slug = $1`, slug).Scan(&o.ID, &o.Slug, &o.Name, &o.CreatedAt, &login, &hash)
	if err != nil {
		return organization.Organization{}, nil, read(err, "organization", slug)
	}
	if login == nil {
		return o, nil, nil
	}
	h, err := breakglass.Parse("password_hash", *hash)
	if err != nil {
		// The operator API refuses such a hash, so only a row written
		// around it holds one; it signs no one in.
		return o, nil, nil
	}
	return o, &breakglass.Credential{Login: *login, Hash: h}, nil
}

// SignIn inserts the break-glass session s and records e in one
// transaction.
func (s *Store) SignIn(
	ctx context.Context,
	ss session.Session,
	e kernel.OrganizationAuditEntry,
) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		if err := s.CreateSession(ctx, ss); err != nil {
			return err
		}
		_, err := audit.RecordOrganization(ctx, e)
		return err //nolint:wrapcheck // names the entry
	})
	if err != nil {
		return fmt.Errorf("sign in to organization %s: %w", ss.OrganizationID, sql.Unavailable(err))
	}
	return nil
}

// RefuseSignIn records e in a transaction of its own.
func (s *Store) RefuseSignIn(ctx context.Context, e kernel.OrganizationAuditEntry) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		_, err := audit.RecordOrganization(ctx, e)
		return err //nolint:wrapcheck // names the entry
	})
	if err != nil {
		return fmt.Errorf("record a refused sign-in: %w", sql.Unavailable(err))
	}
	return nil
}

// Accounts are the accounts of org whose account_users admit member, in
// slug order.
func (s *Store) Accounts(
	ctx context.Context,
	org kernel.OrganizationID,
	member kernel.MemberID,
) ([]session.Admitted, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT a.id, a.slug, a.name
		FROM account_users u JOIN accounts a
			ON a.organization_id = u.organization_id AND a.id = u.account_id
		WHERE u.organization_id = $1 AND u.member_id = $2
		ORDER BY a.slug`, org, member)
	if err != nil {
		return nil, fmt.Errorf("access: list admitting accounts: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (session.Admitted, error) {
		var a session.Admitted
		err := r.Scan(&a.ID, &a.Slug, &a.Name)
		return a, err //nolint:wrapcheck // named below
	})
	if err != nil {
		return nil, fmt.Errorf("access: list admitting accounts: %w", sql.Unavailable(err))
	}
	return out, nil
}

// Admits reads acct when an account_users row of org admits member to it.
func (s *Store) Admits(
	ctx context.Context,
	org kernel.OrganizationID,
	member kernel.MemberID,
	acct kernel.AccountID,
) (session.Admitted, bool, error) {
	var a session.Admitted
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT a.id, a.slug, a.name
		FROM account_users u JOIN accounts a
			ON a.organization_id = u.organization_id AND a.id = u.account_id
		WHERE u.organization_id = $1 AND u.member_id = $2 AND u.account_id = $3`,
		org, member, acct).Scan(&a.ID, &a.Slug, &a.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return session.Admitted{}, false, nil
	}
	if err != nil {
		return session.Admitted{}, false, fmt.Errorf("access: admit to account %s: %w", acct,
			sql.Unavailable(err))
	}
	return a, true, nil
}
