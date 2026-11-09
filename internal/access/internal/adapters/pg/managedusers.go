package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/group"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/member"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/user"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/audit"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// userSelect reads managed users from account_users as u, each with its
// member, how many of the account's groups its latest groups match, and
// its latest invitation to the account; the caller adds the WHERE.
const userSelect = `SELECT u.account_id, m.organization_id, m.id, m.login, m.name, m.email,
	m.status, m.created_at, m.last_seen_at, coalesce(u.slack_member_id, ''), u.latest_groups,
	(SELECT count(*) FROM managed_user_groups g
		WHERE g.account_id = u.account_id AND g.name = ANY(u.latest_groups)),
	i.id, i.email, i.created_at, i.sent_at, u.version
FROM account_users u JOIN members m ON m.id = u.member_id
LEFT JOIN LATERAL (SELECT id, email, created_at, sent_at FROM account_invitations
	WHERE account_id = u.account_id AND member_id = u.member_id
	ORDER BY created_at DESC, id DESC LIMIT 1) i ON true`

func scanUser(r pgx.Row) (user.User, error) {
	var (
		u       user.User
		groups  int64
		invID   *kernel.ULID
		invMail *string
		created *time.Time
		sent    *time.Time
	)
	err := r.Scan(&u.AccountID, &u.Member.OrganizationID, &u.Member.ID, &u.Member.Login,
		&u.Member.Name, &u.Member.Email, &u.Member.Status, &u.Member.CreatedAt,
		&u.Member.LastSeenAt, &u.SlackMemberID, &u.LatestGroups, &groups,
		&invID, &invMail, &created, &sent, &u.Version)
	if err != nil {
		return user.User{}, err //nolint:wrapcheck // the caller names the read
	}
	u.GroupCount = int(groups)
	if invID != nil {
		u.Invitation = &user.Invitation{
			ID:        *invID,
			Email:     *invMail,
			CreatedAt: *created,
			SentAt:    *sent,
		}
	}
	return u, nil
}

// Users are up to limit of acct's managed users in member ID order from
// the first after after.
func (s *Store) Users(
	ctx context.Context,
	acct kernel.AccountID,
	after kernel.MemberID,
	limit int,
) ([]user.User, error) {
	return s.users(ctx, userSelect+`
		WHERE u.account_id = $1 AND ($2::uuid IS NULL OR u.member_id > $2)
		ORDER BY u.member_id LIMIT $3`, acct, after, limit)
}

func (s *Store) users(ctx context.Context, query string, args ...any) ([]user.User, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("access: list managed users: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (user.User, error) {
		return scanUser(r)
	})
	if err != nil {
		return nil, fmt.Errorf("access: list managed users: %w", sql.Unavailable(err))
	}
	return out, nil
}

// User reads the managed user id of acct.
func (s *Store) User(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.MemberID,
) (user.User, error) {
	u, err := scanUser(tx.Q(ctx, s.db).QueryRow(ctx, userSelect+`
		WHERE u.account_id = $1 AND u.member_id = $2`, acct, id))
	return u, read(err, "managed user", id)
}

// Invite admits the organization's member with in.Login to acct, creating
// a pending member when there is none, records inv and e, and checks the
// member's seat, in one transaction. The organization's row lock orders
// invites, so two cannot both create a member for one login or both pass
// one e-mail address's pending invitation check.
func (s *Store) Invite(
	ctx context.Context,
	org kernel.OrganizationID,
	acct kernel.AccountID,
	in user.Invite,
	inv user.Invitation,
	e kernel.AuditEntry,
) (user.User, error) {
	var id kernel.MemberID
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if _, err := q.Exec(ctx, `SELECT 1 FROM organizations WHERE id = $1 FOR NO KEY UPDATE`,
			org); err != nil {
			return fmt.Errorf("access: lock organization: %w", sql.Unavailable(err))
		}
		var err error
		if id, err = invitee(ctx, q, org, in); err != nil {
			return err
		}
		var admitted, seated, emailPending bool
		if err := q.QueryRow(ctx, `SELECT
				EXISTS (SELECT 1 FROM account_users WHERE account_id = $1 AND member_id = $2),
				EXISTS (SELECT 1 FROM account_users WHERE organization_id = $3 AND member_id = $2),
				$4 <> '' AND EXISTS (SELECT 1 FROM account_invitations i
					JOIN members m ON m.id = i.member_id
					WHERE i.account_id = $1 AND i.email = $4 AND m.status = 'pending')`,
			acct, id, org, in.Email).Scan(&admitted, &seated, &emailPending); err != nil {
			return fmt.Errorf("access: read admission: %w", sql.Unavailable(err))
		}
		switch {
		case admitted:
			return fmt.Errorf(
				"%w: %q is a managed user of the account",
				user.ErrInvalidState,
				in.Login,
			)
		case emailPending:
			return fmt.Errorf("%w: %q has a pending invitation to the account",
				user.ErrInvalidState, in.Email)
		}
		if err := s.checkSeat(ctx, org, acct, seated); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `INSERT INTO account_users
				(account_id, organization_id, member_id, slack_member_id)
				VALUES ($1, $2, $3, nullif($4, ''))`, acct, org, id, in.SlackMemberID); err != nil {
			return fmt.Errorf("access: admit member %s: %w", id, sql.Unavailable(err))
		}
		if _, err := q.Exec(ctx, `INSERT INTO account_invitations
				(account_id, id, member_id, email, created_at, sent_at)
				VALUES ($1, $2, $3, $4, $5, $6)`,
			acct, inv.ID, id, inv.Email, inv.CreatedAt, inv.SentAt); err != nil {
			return fmt.Errorf("access: record invitation: %w", sql.Unavailable(err))
		}
		if err := bumpAuthz(ctx, q, acct); err != nil {
			return err
		}
		e.AffectedResource.ID = id.String()
		_, err = audit.Record(ctx, e)
		return err //nolint:wrapcheck // names itself
	})
	if err != nil {
		return user.User{}, fmt.Errorf("invite %q: %w", in.Login, err)
	}
	return s.User(ctx, acct, id)
}

// invitee is the organization's member with in.Login, created pending when
// there is none. Two members may share a login, two identities being two
// members until linked, so a login of several is refused rather than one
// picked.
//
// ref: decision 0006
func invitee(ctx context.Context, q tx.Querier, org kernel.OrganizationID, in user.Invite) (
	kernel.MemberID,
	error,
) {
	rows, err := q.Query(ctx, `SELECT id, status FROM members
		WHERE organization_id = $1 AND login = $2 ORDER BY id LIMIT 2`, org, in.Login)
	if err != nil {
		return kernel.MemberID{}, fmt.Errorf("access: find member: %w", sql.Unavailable(err))
	}
	type found struct {
		ID     kernel.MemberID
		Status member.Status
	}
	ms, err := pgx.CollectRows(rows, pgx.RowToStructByPos[found])
	if err != nil {
		return kernel.MemberID{}, fmt.Errorf("access: find member: %w", sql.Unavailable(err))
	}
	switch {
	case len(ms) > 1:
		return kernel.MemberID{}, fmt.Errorf("%w: several members have the login %q",
			user.ErrInvalidState, in.Login)
	case len(ms) == 1 && ms[0].Status == member.Deactivated:
		return kernel.MemberID{}, fmt.Errorf("%w: the member %q is deactivated",
			user.ErrInvalidState, in.Login)
	case len(ms) == 1:
		return ms[0].ID, nil
	}
	id := kernel.NewMemberID()
	if _, err := q.Exec(ctx, `INSERT INTO members (organization_id, id, login, name, email, status)
		VALUES ($1, $2, $3, $3, $4, 'pending')`, org, id, in.Login, in.Email); err != nil {
		return kernel.MemberID{}, fmt.Errorf("access: create member: %w", sql.Unavailable(err))
	}
	return id, nil
}

// SetSlackMemberID sets the managed user's Slack member, none when empty,
// and records e, unless expect does not accept the admission's version.
func (s *Store) SetSlackMemberID(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.MemberID,
	expect kernel.Expect,
	slack string,
	e kernel.AuditEntry,
) (user.User, error) {
	err := s.recordAccount(ctx, e, func(ctx context.Context, q tx.Querier) error {
		if err := lockUser(ctx, q, acct, id, expect); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `UPDATE account_users
			SET slack_member_id = nullif($3, ''), version = version + 1
			WHERE account_id = $1 AND member_id = $2`, acct, id, slack); err != nil {
			return fmt.Errorf("access: set Slack member: %w", sql.Unavailable(err))
		}
		return nil
	})
	if err != nil {
		return user.User{}, fmt.Errorf("update managed user %s: %w", id, err)
	}
	return s.User(ctx, acct, id)
}

// lockUser locks acct's admission of member id FOR UPDATE, so the version
// expect checks is the one the change replaces; a version it does not
// accept is kernel.ErrStale.
func lockUser(
	ctx context.Context,
	q tx.Querier,
	acct kernel.AccountID,
	id kernel.MemberID,
	expect kernel.Expect,
) error {
	var version int64
	err := q.QueryRow(ctx, `SELECT version FROM account_users
		WHERE account_id = $1 AND member_id = $2 FOR UPDATE`, acct, id).Scan(&version)
	if err != nil {
		return read(err, "managed user", id)
	}
	return expect.Check(version) //nolint:wrapcheck // kernel.ErrStale, answered as it is
}

// Resend moves the latest invitation's sent_at of acct's pending managed
// user id to at and records e.
func (s *Store) Resend(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.MemberID,
	at time.Time,
	e kernel.AuditEntry,
) (user.User, error) {
	err := s.recordAccount(ctx, e, func(ctx context.Context, q tx.Querier) error {
		var (
			pending bool
			inv     *kernel.ULID
		)
		err := q.QueryRow(ctx, `SELECT m.status = 'pending',
				(SELECT i.id FROM account_invitations i
					WHERE i.account_id = u.account_id AND i.member_id = u.member_id
					ORDER BY i.created_at DESC, i.id DESC LIMIT 1)
			FROM account_users u JOIN members m ON m.id = u.member_id
			WHERE u.account_id = $1 AND u.member_id = $2 FOR UPDATE OF u`,
			acct, id).Scan(&pending, &inv)
		if err != nil {
			return read(err, "managed user", id)
		}
		if !pending || inv == nil {
			return fmt.Errorf("%w: managed user %s has no pending invitation",
				user.ErrInvalidState, id)
		}
		if _, err := q.Exec(ctx, `UPDATE account_invitations SET sent_at = $2 WHERE id = $1`,
			*inv, at); err != nil {
			return fmt.Errorf("access: resend invitation: %w", sql.Unavailable(err))
		}
		return nil
	})
	if err != nil {
		return user.User{}, fmt.Errorf("resend the invitation of %s: %w", id, err)
	}
	return s.User(ctx, acct, id)
}

// Remove stops acct admitting member id: it deletes the admission and the
// member's invitations to acct, disables their personal keys in acct as
// DEPROVISIONED and revokes their sessions in acct and those keys', and
// records e, in one transaction, returning the revoked sessions; an
// admission whose version expect does not accept stays.
func (s *Store) Remove(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.MemberID,
	expect kernel.Expect,
	e kernel.AuditEntry,
) ([]kernel.ULID, error) {
	var ids []kernel.ULID
	err := s.recordAccount(ctx, e, func(ctx context.Context, q tx.Querier) error {
		// A personal key's creation holds a share lock on the admission, so
		// locking it first lets the key commit before the keys are
		// disabled below, or find no admission.
		if err := lockUser(ctx, q, acct, id, expect); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `DELETE FROM account_invitations
			WHERE account_id = $1 AND member_id = $2`, acct, id); err != nil {
			return fmt.Errorf("access: delete invitations: %w", sql.Unavailable(err))
		}
		rows, err := q.Query(ctx, `UPDATE api_keys SET disabled_at = coalesce(disabled_at, now()),
				disabled_reason = coalesce(disabled_reason, 'DEPROVISIONED'), version = version + 1
			WHERE account_id = $1 AND owner_user_id = $2 AND deleted_at IS NULL RETURNING id`,
			acct, id)
		if err != nil {
			return fmt.Errorf("access: disable personal keys: %w", sql.Unavailable(err))
		}
		keys, err := pgx.CollectRows(rows, pgx.RowTo[kernel.ULID])
		if err != nil {
			return fmt.Errorf("access: disable personal keys: %w", sql.Unavailable(err))
		}
		revoked, err := revoke(ctx, q, `account_id = $1 AND (member_id = $2
			OR (actor_kind = 'api_key' AND actor_id = ANY($3)))`, acct, id, keys)
		if err != nil {
			return err
		}
		for _, ss := range revoked {
			ids = append(ids, ss.ID)
		}
		if _, err := q.Exec(ctx,
			`DELETE FROM account_users WHERE account_id = $1 AND member_id = $2`,
			acct, id); err != nil {
			return fmt.Errorf("access: remove managed user: %w", sql.Unavailable(err))
		}
		return bumpAuthz(ctx, q, acct)
	})
	if err != nil {
		return nil, fmt.Errorf("remove managed user %s: %w", id, err)
	}
	return ids, nil
}

// PersonalAPIKeys are up to limit of member owner's personal keys in acct,
// deleted ones left out, in ID order from the first after after.
func (s *Store) PersonalAPIKeys(
	ctx context.Context,
	acct kernel.AccountID,
	owner kernel.MemberID,
	after kernel.ULID,
	limit int,
) ([]apikey.Key, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+keyColumns+`
		FROM api_keys k JOIN accounts a ON a.id = k.account_id
		WHERE k.account_id = $1 AND k.owner_user_id = $2 AND k.deleted_at IS NULL
			AND ($3::uuid IS NULL OR k.id > $3)
		ORDER BY k.id LIMIT $4`, acct, owner, after, limit)
	if err != nil {
		return nil, fmt.Errorf("access: list personal API keys: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (apikey.Key, error) {
		return scanKey(r)
	})
	if err != nil {
		return nil, fmt.Errorf("access: list personal API keys: %w", sql.Unavailable(err))
	}
	return out, nil
}

// RecordRequest records e on its account's trail.
func (s *Store) RecordRequest(ctx context.Context, e kernel.AuditEntry) error {
	if err := s.recordAccount(ctx, e, nil); err != nil {
		return fmt.Errorf("record %s: %w", e.Action, err)
	}
	return nil
}

// recordAccount runs write, unless nil, and records e on its account's
// trail, in one transaction.
func (s *Store) recordAccount(
	ctx context.Context,
	e kernel.AuditEntry,
	write func(context.Context, tx.Querier) error,
) error {
	//nolint:wrapcheck // the write and the trail name themselves
	return tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		if write != nil {
			if err := write(ctx, tx.Q(ctx, s.db)); err != nil {
				return err
			}
		}
		_, err := audit.Record(ctx, e)
		return err //nolint:wrapcheck // as above
	})
}

// groupSelect reads groups as g with their members' logins.
const groupSelect = `SELECT g.account_id, g.id, g.name, g.description, g.created_at,
	ARRAY(SELECT m.login FROM account_users u JOIN members m ON m.id = u.member_id
		WHERE u.account_id = g.account_id AND g.name = ANY(u.latest_groups) ORDER BY m.login),
	g.version
FROM managed_user_groups g`

func scanGroup(r pgx.Row) (group.Group, error) {
	var g group.Group
	err := r.Scan(&g.AccountID, &g.ID, &g.Name, &g.Description, &g.CreatedAt, &g.Members,
		&g.Version)
	return g, err //nolint:wrapcheck // the caller names the read
}

// Groups are up to limit of acct's groups in ID order from the first after
// after.
func (s *Store) Groups(
	ctx context.Context,
	acct kernel.AccountID,
	after kernel.ULID,
	limit int,
) ([]group.Group, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, groupSelect+`
		WHERE g.account_id = $1 AND ($2::uuid IS NULL OR g.id > $2) ORDER BY g.id LIMIT $3`,
		acct, after, limit)
	if err != nil {
		return nil, fmt.Errorf("access: list groups: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (group.Group, error) {
		return scanGroup(r)
	})
	if err != nil {
		return nil, fmt.Errorf("access: list groups: %w", sql.Unavailable(err))
	}
	return out, nil
}

// Group reads acct's group id.
func (s *Store) Group(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (group.Group, error) {
	g, err := scanGroup(tx.Q(ctx, s.db).QueryRow(ctx, groupSelect+`
		WHERE g.account_id = $1 AND g.id = $2`, acct, id))
	return g, read(err, "managed user group", id)
}

// CreateGroup creates g and records e; a name another group of the
// account holds is port.ErrSlugTaken.
func (s *Store) CreateGroup(
	ctx context.Context,
	g group.Group,
	e kernel.AuditEntry,
) (group.Group, error) {
	err := s.recordAccount(ctx, e, func(ctx context.Context, q tx.Querier) error {
		_, err := q.Exec(ctx, `INSERT INTO managed_user_groups
			(account_id, id, name, description, created_at) VALUES ($1, $2, $3, $4, $5)`,
			g.AccountID, g.ID, g.Name, g.Description, g.CreatedAt)
		if err != nil {
			if errors.Is(classify(err, "group"), port.ErrSlugTaken) {
				return fmt.Errorf(
					"%w: a group of the account is named %q",
					port.ErrSlugTaken,
					g.Name,
				)
			}
			return fmt.Errorf("access: create group: %w", sql.Unavailable(err))
		}
		return bumpAuthz(ctx, q, g.AccountID)
	})
	if err != nil {
		return group.Group{}, fmt.Errorf("create group %q: %w", g.Name, err)
	}
	return s.Group(ctx, g.AccountID, g.ID)
}

// SetDescription sets the description of acct's group id and records e,
// unless expect does not accept the group's version.
func (s *Store) SetDescription(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	expect kernel.Expect,
	description string,
	e kernel.AuditEntry,
) (group.Group, error) {
	err := s.recordAccount(ctx, e, func(ctx context.Context, q tx.Querier) error {
		if err := lockGroup(ctx, q, acct, id, expect); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `UPDATE managed_user_groups
			SET description = $3, version = version + 1
			WHERE account_id = $1 AND id = $2`, acct, id, description); err != nil {
			return fmt.Errorf("access: update group: %w", sql.Unavailable(err))
		}
		return nil
	})
	if err != nil {
		return group.Group{}, fmt.Errorf("update group %s: %w", id, err)
	}
	return s.Group(ctx, acct, id)
}

// DeleteGroup deletes acct's group id and records e, unless expect does
// not accept the group's version.
func (s *Store) DeleteGroup(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	expect kernel.Expect,
	e kernel.AuditEntry,
) error {
	err := s.recordAccount(ctx, e, func(ctx context.Context, q tx.Querier) error {
		if err := lockGroup(ctx, q, acct, id, expect); err != nil {
			return err
		}
		if _, err := q.Exec(
			ctx,
			`DELETE FROM managed_user_groups WHERE account_id = $1 AND id = $2`,
			acct,
			id,
		); err != nil {
			return fmt.Errorf("access: delete group: %w", sql.Unavailable(err))
		}
		return bumpAuthz(ctx, q, acct)
	})
	if err != nil {
		return fmt.Errorf("delete group %s: %w", id, err)
	}
	return nil
}

// lockGroup locks acct's group id FOR UPDATE, so the version expect
// checks is the one the change replaces; a version it does not accept is
// kernel.ErrStale.
func lockGroup(
	ctx context.Context,
	q tx.Querier,
	acct kernel.AccountID,
	id kernel.ULID,
	expect kernel.Expect,
) error {
	var version int64
	err := q.QueryRow(ctx, `SELECT version FROM managed_user_groups
		WHERE account_id = $1 AND id = $2 FOR UPDATE`, acct, id).Scan(&version)
	if err != nil {
		return read(err, "managed user group", id)
	}
	return expect.Check(version) //nolint:wrapcheck // kernel.ErrStale, answered as it is
}

// GroupUsers are up to limit of the managed users of acct whose latest
// groups hold group id's name, in member ID order from the first after
// after.
func (s *Store) GroupUsers(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	after kernel.MemberID,
	limit int,
) ([]user.User, error) {
	return s.users(ctx, userSelect+`
		WHERE u.account_id = $1 AND ($3::uuid IS NULL OR u.member_id > $3)
			AND (SELECT name FROM managed_user_groups WHERE account_id = $1 AND id = $2)
				= ANY(u.latest_groups)
		ORDER BY u.member_id LIMIT $4`, acct, id, after, limit)
}

// AvailableGroups are up to limit of the group names acct's managed users'
// latest sign-ins reported, in name order from the first after after.
func (s *Store) AvailableGroups(
	ctx context.Context,
	acct kernel.AccountID,
	after string,
	limit int,
) ([]string, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT DISTINCT n FROM account_users u,
			unnest(u.latest_groups) n
		WHERE u.account_id = $1 AND n > $2 ORDER BY n LIMIT $3`, acct, after, limit)
	if err != nil {
		return nil, fmt.Errorf("access: list available groups: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("access: list available groups: %w", sql.Unavailable(err))
	}
	return out, nil
}

var (
	_ user.Store  = (*Store)(nil)
	_ group.Store = (*Store)(nil)
)
