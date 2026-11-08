package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/audit"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var _ orgadmin.Store = (*Store)(nil)

// RenameOrganization renames the organization org and records e.
func (s *Store) RenameOrganization(
	ctx context.Context,
	org kernel.OrganizationID,
	name string,
	e kernel.OrganizationAuditEntry,
) (organization.Organization, error) {
	var o organization.Organization
	err := s.record(ctx, e, func(ctx context.Context, q tx.Querier) error {
		return read(q.QueryRow(ctx, `UPDATE organizations SET name = $2 WHERE id = $1
			RETURNING id, slug, name, created_at`, org, name).
			Scan(&o.ID, &o.Slug, &o.Name, &o.CreatedAt), "organization", org.ULID)
	})
	if err != nil {
		return organization.Organization{}, fmt.Errorf("rename organization %s: %w", org, err)
	}
	return o, nil
}

// IsOrganizationAdmin reports whether the member holds organization-admin
// in org.
func (s *Store) IsOrganizationAdmin(
	ctx context.Context,
	org kernel.OrganizationID,
	member kernel.MemberID,
) (bool, error) {
	var ok bool
	if err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM organization_admins
		WHERE organization_id = $1 AND member_id = $2)`, org, member).Scan(&ok); err != nil {
		return false, fmt.Errorf("access: read organization admins: %w", sql.Unavailable(err))
	}
	return ok, nil
}

// accountColumns are what scanAccount reads, in its order.
const accountColumns = `organization_id, id, slug, name, claimed_at, created_at`

func scanAccount(r pgx.Row) (account.Account, error) {
	var (
		a       account.Account
		claimed *time.Time
	)
	err := r.Scan(&a.OrganizationID, &a.ID, &a.Slug, &a.Name, &claimed, &a.CreatedAt)
	if claimed != nil {
		a.ClaimedAt = *claimed
	}
	return a, err //nolint:wrapcheck // the caller names the read
}

// OrganizationAccounts are up to limit of org's accounts in ID order from
// the first after after.
func (s *Store) OrganizationAccounts(
	ctx context.Context,
	org kernel.OrganizationID,
	after kernel.AccountID,
	limit int,
) ([]account.Account, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+accountColumns+` FROM accounts
		WHERE organization_id = $1 AND ($2::uuid IS NULL OR id > $2) ORDER BY id LIMIT $3`,
		org, after, limit)
	if err != nil {
		return nil, fmt.Errorf("access: list accounts: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (account.Account, error) {
		return scanAccount(r)
	})
	if err != nil {
		return nil, fmt.Errorf("access: list accounts: %w", sql.Unavailable(err))
	}
	return out, nil
}

// AccountBySlug reads org's account slug; another organization's is not
// found.
func (s *Store) AccountBySlug(
	ctx context.Context,
	org kernel.OrganizationID,
	slug kernel.Slug,
) (account.Account, error) {
	a, err := scanAccount(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+accountColumns+`
		FROM accounts WHERE organization_id = $1 AND slug = $2`, org, slug))
	return a, read(err, "account", slug)
}

// CreateAccount records a, claimed, admitting admin with space-admin on its
// top space, and records e, in one transaction. The seats limit checks the
// admission.
func (s *Store) CreateAccount(
	ctx context.Context,
	a account.Account,
	admin kernel.MemberID,
	e kernel.OrganizationAuditEntry,
) error {
	err := s.record(ctx, e, func(ctx context.Context, q tx.Querier) error {
		if _, err := q.Exec(ctx, `INSERT INTO accounts (organization_id, id, slug, name,
				claimed_at, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
			a.OrganizationID, a.ID, a.Slug, a.Name, a.ClaimedAt, a.CreatedAt); err != nil {
			return classify(err, "account")
		}
		if err := seedAccount(ctx, q, a); err != nil {
			return err
		}
		if err := s.admit(ctx, q, a.OrganizationID, a.ID, admin); err != nil {
			return err
		}
		return bindAccountAdmin(ctx, q, a.ID, admin, e)
	})
	if err != nil {
		return fmt.Errorf("create account %s: %w", a.Slug, err)
	}
	return nil
}

// ClaimAccount claims org's unclaimed account acct at at, admitting admin
// with space-admin on its top space, and records e, in one transaction; a claimed account is
// orgadmin.ErrInvalidState. The row lock makes the claim a single
// transition: of two claims at once, the second reads the first's.
func (s *Store) ClaimAccount(
	ctx context.Context,
	org kernel.OrganizationID,
	acct kernel.AccountID,
	admin kernel.MemberID,
	at time.Time,
	e kernel.OrganizationAuditEntry,
) (account.Account, error) {
	var a account.Account
	err := s.record(ctx, e, func(ctx context.Context, q tx.Querier) error {
		var claimed bool
		err := q.QueryRow(ctx, `SELECT claimed_at IS NOT NULL FROM accounts
			WHERE organization_id = $1 AND id = $2 FOR UPDATE`, org, acct).Scan(&claimed)
		if err != nil {
			return read(err, "account", acct.ULID)
		}
		if claimed {
			return fmt.Errorf("%w: the account is claimed", orgadmin.ErrInvalidState)
		}
		if err := s.admit(ctx, q, org, acct, admin); err != nil {
			return err
		}
		if err := bindAccountAdmin(ctx, q, acct, admin, e); err != nil {
			return err
		}
		// Admitting a member and binding them change the account's
		// authorization.
		a, err = scanAccount(q.QueryRow(ctx, `UPDATE accounts SET claimed_at = $2,
				authz_version = authz_version + 1
			WHERE id = $1 RETURNING `+accountColumns, acct, at))
		return read(err, "account", acct.ULID)
	})
	if err != nil {
		return account.Account{}, fmt.Errorf("claim account %s: %w", acct, err)
	}
	return a, nil
}

// admit admits the member admin to org's account acct. The member must be
// active in org, or orgadmin.ErrNoAdmin, and with the features domain held
// the admission's seat is checked against the seats limit.
func (s *Store) admit(
	ctx context.Context,
	q tx.Querier,
	org kernel.OrganizationID,
	acct kernel.AccountID,
	admin kernel.MemberID,
) error {
	var seated bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM account_users
			WHERE organization_id = $1 AND member_id = $2)
		FROM members WHERE organization_id = $1 AND id = $2 AND status = 'active' FOR SHARE`,
		org, admin).Scan(&seated)
	if errors.Is(err, pgx.ErrNoRows) {
		return orgadmin.ErrNoAdmin
	}
	if err != nil {
		return fmt.Errorf("access: read member %s: %w", admin, sql.Unavailable(err))
	}
	if err := s.checkSeat(ctx, org, acct, seated); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `INSERT INTO account_users (account_id, organization_id, member_id)
		VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, acct, org, admin); err != nil {
		return fmt.Errorf("access: admit member %s: %w", admin, sql.Unavailable(err))
	}
	return nil
}

// checkSeat checks the seat admitting a member to acct takes against the
// seats limit while the features domain is held; seated is whether
// another account of org admits the member already.
func (s *Store) checkSeat(
	ctx context.Context,
	org kernel.OrganizationID,
	acct kernel.AccountID,
	seated bool,
) error {
	if s.features == nil {
		return nil
	}
	//nolint:wrapcheck // the features domain names the refusal
	return s.features.CheckLimit(ctx, org, acct, featuresapi.Seats,
		func(ctx context.Context) (featuresapi.Usage, error) {
			u, err := s.countSeats(ctx, org, acct, kernel.ULID{})
			// A member another account admits holds its seat already, so
			// the organization's count does not grow with this admission;
			// CheckLimit asks whether one more fits.
			if seated {
				u.Organization--
			}
			return u, err
		})
}

// RenameAccount renames org's account acct and records e.
func (s *Store) RenameAccount(
	ctx context.Context,
	org kernel.OrganizationID,
	acct kernel.AccountID,
	name string,
	e kernel.OrganizationAuditEntry,
) (account.Account, error) {
	var a account.Account
	err := s.record(ctx, e, func(ctx context.Context, q tx.Querier) error {
		var err error
		a, err = scanAccount(q.QueryRow(ctx, `UPDATE accounts SET name = $3
			WHERE organization_id = $1 AND id = $2 RETURNING `+accountColumns, org, acct, name))
		return read(err, "account", acct.ULID)
	})
	if err != nil {
		return account.Account{}, fmt.Errorf("rename account %s: %w", acct, err)
	}
	return a, nil
}

// RemoveAccount deletes org's account acct whether or not it is marked for
// deletion; the organization's last account is orgadmin.ErrInvalidState.
func (s *Store) RemoveAccount(
	ctx context.Context,
	org kernel.OrganizationID,
	acct kernel.AccountID,
	e kernel.OrganizationAuditEntry,
) ([]kernel.ULID, error) {
	return s.removeAccount(ctx, org, acct, false, orgadmin.ErrInvalidState, e)
}

// memberColumns are what scanMember reads, in its order, from members as
// m.
const memberColumns = `m.id, m.login, m.name, m.email, m.status, m.created_at,
	EXISTS (SELECT 1 FROM organization_admins r
		WHERE r.organization_id = m.organization_id AND r.member_id = m.id)`

func scanMember(r pgx.Row) (orgadmin.Member, error) {
	var m orgadmin.Member
	err := r.Scan(&m.ID, &m.Login, &m.Name, &m.Email, &m.Status, &m.CreatedAt, &m.Admin)
	return m, err //nolint:wrapcheck // the caller names the read
}

// Members are up to limit of org's members in ID order from the first
// after after.
func (s *Store) Members(
	ctx context.Context,
	org kernel.OrganizationID,
	after kernel.MemberID,
	limit int,
) ([]orgadmin.Member, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+memberColumns+` FROM members m
		WHERE m.organization_id = $1 AND ($2::uuid IS NULL OR m.id > $2)
		ORDER BY m.id LIMIT $3`, org, after, limit)
	if err != nil {
		return nil, fmt.Errorf("access: list members: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (orgadmin.Member, error) {
		return scanMember(r)
	})
	if err != nil {
		return nil, fmt.Errorf("access: list members: %w", sql.Unavailable(err))
	}
	return out, nil
}

// Member reads org's member id.
func (s *Store) Member(
	ctx context.Context,
	org kernel.OrganizationID,
	id kernel.MemberID,
) (orgadmin.Member, error) {
	m, err := scanMember(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+memberColumns+`
		FROM members m WHERE m.organization_id = $1 AND m.id = $2`, org, id))
	return m, read(err, "member", id.ULID)
}

// DeactivateMember deactivates org's member id, disables its personal keys
// as DEPROVISIONED, revokes its sessions and those keys', and records e,
// in one transaction. It returns the revoked sessions.
func (s *Store) DeactivateMember(
	ctx context.Context,
	org kernel.OrganizationID,
	id kernel.MemberID,
	e kernel.OrganizationAuditEntry,
) ([]kernel.ULID, error) {
	var ids []kernel.ULID
	err := s.record(ctx, e, func(ctx context.Context, q tx.Querier) error {
		tag, err := q.Exec(ctx, `UPDATE members SET status = 'deactivated'
			WHERE organization_id = $1 AND id = $2`, org, id)
		if err != nil {
			return fmt.Errorf("access: deactivate member: %w", sql.Unavailable(err))
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: member %s", port.ErrNotFound, id)
		}
		rows, err := q.Query(ctx, `UPDATE api_keys SET disabled_at = coalesce(disabled_at, now()),
				disabled_reason = coalesce(disabled_reason, 'DEPROVISIONED')
			WHERE owner_user_id = $1 AND deleted_at IS NULL RETURNING id`, id)
		if err != nil {
			return fmt.Errorf("access: disable personal keys: %w", sql.Unavailable(err))
		}
		keys, err := pgx.CollectRows(rows, pgx.RowTo[kernel.ULID])
		if err != nil {
			return fmt.Errorf("access: disable personal keys: %w", sql.Unavailable(err))
		}
		revoked, err := revoke(ctx, q, `organization_id = $1 AND (member_id = $2
			OR (actor_kind = 'api_key' AND actor_id = ANY($3)))`, org, id, keys)
		for _, ss := range revoked {
			ids = append(ids, ss.ID)
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("deactivate member %s: %w", id, err)
	}
	return ids, nil
}

// SetOrganizationAdmin grants org's member id organization-admin, by the
// principal by names, or revokes it, and records e, in one transaction.
func (s *Store) SetOrganizationAdmin(
	ctx context.Context,
	org kernel.OrganizationID,
	id kernel.MemberID,
	admin bool,
	by string,
	e kernel.OrganizationAuditEntry,
) error {
	err := s.record(ctx, e, func(ctx context.Context, q tx.Querier) error {
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members
			WHERE organization_id = $1 AND id = $2)`, org, id).Scan(&exists); err != nil {
			return fmt.Errorf("access: read member: %w", sql.Unavailable(err))
		}
		if !exists {
			return fmt.Errorf("%w: member %s", port.ErrNotFound, id)
		}
		stmt := `DELETE FROM organization_admins WHERE organization_id = $1 AND member_id = $2`
		args := []any{org, id}
		if admin {
			stmt = `INSERT INTO organization_admins (organization_id, member_id, created_by)
				VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`
			args = append(args, by)
		}
		if _, err := q.Exec(ctx, stmt, args...); err != nil {
			return fmt.Errorf("access: set organization admin: %w", sql.Unavailable(err))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("set organization admin %s: %w", id, err)
	}
	return nil
}

// orgKeyColumns are what scanOrgKey reads, in its order.
const orgKeyColumns = `organization_id, id, name, secret_hash, creator_member_id, expires_at,
	disabled_at, last_used_at, created_at`

func scanOrgKey(r pgx.Row) (apikey.OrganizationKey, error) {
	var (
		k                          apikey.OrganizationKey
		creator                    *kernel.MemberID
		expires, disabled, lastUse *time.Time
	)
	err := r.Scan(&k.OrganizationID, &k.ID, &k.Name, &k.SecretHash, &creator, &expires,
		&disabled, &lastUse, &k.CreatedAt)
	if creator != nil {
		k.CreatorMemberID = *creator
	}
	for _, t := range []struct {
		from *time.Time
		to   *time.Time
	}{{expires, &k.ExpiresAt}, {disabled, &k.DisabledAt}, {lastUse, &k.LastUsedAt}} {
		if t.from != nil {
			*t.to = *t.from
		}
	}
	return k, err //nolint:wrapcheck // the caller names the read
}

// OrganizationKeys are up to limit of org's keys in ID order from the
// first after after.
func (s *Store) OrganizationKeys(
	ctx context.Context,
	org kernel.OrganizationID,
	after kernel.ULID,
	limit int,
) ([]apikey.OrganizationKey, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+orgKeyColumns+` FROM organization_keys
		WHERE organization_id = $1 AND ($2::uuid IS NULL OR id > $2) ORDER BY id LIMIT $3`,
		org, after, limit)
	if err != nil {
		return nil, fmt.Errorf("access: list organization keys: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (apikey.OrganizationKey, error) {
		return scanOrgKey(r)
	})
	if err != nil {
		return nil, fmt.Errorf("access: list organization keys: %w", sql.Unavailable(err))
	}
	return out, nil
}

// OrganizationKey reads the organization key id, whatever its
// organization, as the exchange must before it knows the organization.
func (s *Store) OrganizationKey(
	ctx context.Context,
	id kernel.ULID,
) (apikey.OrganizationKey, error) {
	k, err := scanOrgKey(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+orgKeyColumns+`
		FROM organization_keys WHERE id = $1`, id))
	return k, read(err, "organization key", id)
}

// TouchOrganizationKey records a use of the key id unless its last is
// under a minute old, so a busy key writes its row once a minute. An
// organization key takes no seat.
func (s *Store) TouchOrganizationKey(ctx context.Context, id kernel.ULID) error {
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE organization_keys SET last_used_at = now()
		WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')`,
		id); err != nil {
		return fmt.Errorf("access: touch organization key %s: %w", id, sql.Unavailable(err))
	}
	return nil
}

// CreateOrganizationKey inserts k and records e.
func (s *Store) CreateOrganizationKey(
	ctx context.Context,
	k apikey.OrganizationKey,
	e kernel.OrganizationAuditEntry,
) error {
	var creator *kernel.MemberID
	if !k.CreatorMemberID.IsZero() {
		creator = &k.CreatorMemberID
	}
	err := s.record(ctx, e, func(ctx context.Context, q tx.Querier) error {
		if _, err := q.Exec(ctx, `INSERT INTO organization_keys (organization_id, id, name,
				secret_hash, creator_member_id, expires_at, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, k.OrganizationID, k.ID, k.Name, k.SecretHash,
			creator, nullTime(k.ExpiresAt), k.CreatedAt); err != nil {
			return fmt.Errorf("access: insert organization key: %w", sql.Unavailable(err))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("create organization key %s: %w", k.ID, err)
	}
	return nil
}

// DisableOrganizationKey disables org's key id unless it is, revokes its
// sessions and records e, in one transaction.
func (s *Store) DisableOrganizationKey(
	ctx context.Context,
	org kernel.OrganizationID,
	id kernel.ULID,
	e kernel.OrganizationAuditEntry,
) (apikey.OrganizationKey, []kernel.ULID, error) {
	return s.endOrgKey(ctx, org, id, e, `UPDATE organization_keys
		SET disabled_at = coalesce(disabled_at, now())
		WHERE organization_id = $1 AND id = $2 RETURNING `+orgKeyColumns)
}

// DeleteOrganizationKey deletes org's key id, revokes its sessions and
// records e, in one transaction.
func (s *Store) DeleteOrganizationKey(
	ctx context.Context,
	org kernel.OrganizationID,
	id kernel.ULID,
	e kernel.OrganizationAuditEntry,
) (apikey.OrganizationKey, []kernel.ULID, error) {
	return s.endOrgKey(ctx, org, id, e, `DELETE FROM organization_keys
		WHERE organization_id = $1 AND id = $2 RETURNING `+orgKeyColumns)
}

// endOrgKey runs stmt, which returns org's key id, and revokes the key's
// sessions.
func (s *Store) endOrgKey(
	ctx context.Context,
	org kernel.OrganizationID,
	id kernel.ULID,
	e kernel.OrganizationAuditEntry,
	stmt string,
) (apikey.OrganizationKey, []kernel.ULID, error) {
	var (
		k   apikey.OrganizationKey
		ids []kernel.ULID
	)
	err := s.record(ctx, e, func(ctx context.Context, q tx.Querier) error {
		var err error
		if k, err = scanOrgKey(q.QueryRow(ctx, stmt, org, id)); err != nil {
			return read(err, "organization key", id)
		}
		revoked, err := revoke(ctx, q, `actor_kind = 'organization' AND actor_id = $1`, id)
		for _, ss := range revoked {
			ids = append(ids, ss.ID)
		}
		return err
	})
	if err != nil {
		return apikey.OrganizationKey{}, nil, fmt.Errorf("organization key %s: %w", id, err)
	}
	return k, ids, nil
}

// Record runs write and records e in one transaction.
func (s *Store) Record(
	ctx context.Context,
	e kernel.OrganizationAuditEntry,
	write func(context.Context) error,
) error {
	return s.record(ctx, e, func(ctx context.Context, _ tx.Querier) error { return write(ctx) })
}

// record runs write, then records e, in one transaction.
func (s *Store) record(
	ctx context.Context,
	e kernel.OrganizationAuditEntry,
	write func(context.Context, tx.Querier) error,
) error {
	//nolint:wrapcheck // the write and the trail name themselves
	return tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		if err := write(ctx, tx.Q(ctx, s.db)); err != nil {
			return err
		}
		_, err := audit.RecordOrganization(ctx, e)
		return err //nolint:wrapcheck // as above
	})
}
