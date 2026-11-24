package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/linking"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/member"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/audit"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var _ linking.Store = (*Store)(nil)

// linkColumns are what scanLink reads, in its order.
const linkColumns = `organization_id, id, member_id, other_member_id, created_by, created_at,
	expires_at, member_signed_at, other_member_signed_at, completed_at`

// scanLink reads linkColumns, then extra.
func scanLink(r pgx.Row, extra ...any) (linking.Link, error) {
	var (
		l                  linking.Link
		mine, other, ended *time.Time
	)
	dest := append([]any{
		&l.OrganizationID, &l.ID, &l.MemberID, &l.OtherMemberID, &l.CreatedBy,
		&l.CreatedAt, &l.ExpiresAt, &mine, &other, &ended,
	}, extra...)
	if err := r.Scan(dest...); err != nil {
		return linking.Link{}, err //nolint:wrapcheck // the callers name the read
	}
	l.MemberSignedAt, l.OtherMemberSignedAt, l.CompletedAt = deref(mine), deref(other), deref(ended)
	return l, nil
}

// CreateLink records l and e under the organization's row lock, which
// sign-ins take too, so a link never names a member a merge is deleting.
// Each member is read with its identities and must pass
// linking.Linkable; an open link of the pair is linking.ErrLinkExists,
// and an expired one is deleted first, which the unique index of the
// pair's uncompleted links needs.
func (s *Store) CreateLink(ctx context.Context, l linking.Link, e kernel.OrganizationAuditEntry) error {
	err := s.record(ctx, e, func(ctx context.Context, q tx.Querier) error {
		if _, err := q.Exec(ctx, `SELECT 1 FROM organizations WHERE id = $1 FOR NO KEY UPDATE`,
			l.OrganizationID); err != nil {
			return fmt.Errorf("access: lock organization: %w", sql.Unavailable(err))
		}
		a, err := linkCandidate(ctx, q, l.OrganizationID, l.MemberID)
		if err != nil {
			return err
		}
		b, err := linkCandidate(ctx, q, l.OrganizationID, l.OtherMemberID)
		if err != nil {
			return err
		}
		if err := linking.Linkable(a, b); err != nil {
			return err //nolint:wrapcheck // the rule names the refusal
		}
		if _, err := q.Exec(ctx, `DELETE FROM member_links
			WHERE organization_id = $1 AND completed_at IS NULL AND expires_at <= $4
				AND least(member_id, other_member_id) = least($2::uuid, $3::uuid)
				AND greatest(member_id, other_member_id) = greatest($2::uuid, $3::uuid)`,
			l.OrganizationID, l.MemberID, l.OtherMemberID, l.CreatedAt); err != nil {
			return fmt.Errorf("access: delete expired link: %w", sql.Unavailable(err))
		}
		tag, err := q.Exec(ctx, `INSERT INTO member_links (organization_id, id, member_id,
				other_member_id, created_by, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT DO NOTHING`,
			l.OrganizationID, l.ID, l.MemberID, l.OtherMemberID, l.CreatedBy, l.CreatedAt, l.ExpiresAt)
		if err != nil {
			return fmt.Errorf("access: insert link: %w", sql.Unavailable(err))
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: members %s and %s", linking.ErrLinkExists, l.MemberID, l.OtherMemberID)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("open link %s: %w", l.ID, sql.Unavailable(err))
	}
	return nil
}

// linkCandidate reads org's member id with its status and identity count;
// a member of another organization is port.ErrNotFound.
func linkCandidate(ctx context.Context, q tx.Querier, org kernel.OrganizationID, id kernel.MemberID) (linking.Candidate, error) {
	c := linking.Candidate{ID: id}
	err := q.QueryRow(ctx, `SELECT m.status, (SELECT count(*) FROM member_identities i
			WHERE i.organization_id = m.organization_id AND i.member_id = m.id)
		FROM members m WHERE m.organization_id = $1 AND m.id = $2`, org, id).
		Scan(&c.Status, &c.Identities)
	return c, read(err, "member", id.ULID)
}

// Link reads the link id, in any organization, with its organization's
// slug.
func (s *Store) Link(ctx context.Context, id kernel.ULID) (linking.Link, kernel.Slug, error) {
	var slug kernel.Slug
	l, err := scanLink(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+linkColumns+`,
			(SELECT slug FROM organizations o WHERE o.id = l.organization_id)
		FROM member_links l WHERE l.id = $1`, id), &slug)
	return l, slug, read(err, "link", id)
}

// Identities are org's member m's identities, oldest first, with their
// providers' kinds.
func (s *Store) Identities(ctx context.Context, org kernel.OrganizationID, m kernel.MemberID) ([]linking.Federation, error) {
	return identities(ctx, tx.Q(ctx, s.db), org, m, "")
}

// identities are org's member m's identities, oldest first, locked for
// update when lock is FOR UPDATE.
func identities(ctx context.Context, q tx.Querier, org kernel.OrganizationID, m kernel.MemberID, lock string) ([]linking.Federation, error) {
	rows, err := q.Query(ctx, `SELECT i.identity_provider_id, p.kind, i.subject, i.login, i.created_at
		FROM member_identities i JOIN identity_providers p
			ON p.organization_id = i.organization_id AND p.id = i.identity_provider_id
		WHERE i.organization_id = $1 AND i.member_id = $2
		ORDER BY i.created_at, i.identity_provider_id, i.subject `+lock, org, m)
	if err != nil {
		return nil, fmt.Errorf("access: read identities: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[linking.Federation])
	if err != nil {
		return nil, fmt.Errorf("access: read identities: %w", sql.Unavailable(err))
	}
	return out, nil
}

// UnlinkIdentities removes org's member m's identities of kind that
// linking.PlanUnlink chooses, keeping the one the session sess signed in
// through, revokes the live sessions signed in through them, and records e
// with linking.UnlinkBody, in one transaction. It answers the identities
// that remain and the revoked sessions.
func (s *Store) UnlinkIdentities(
	ctx context.Context,
	org kernel.OrganizationID,
	m kernel.MemberID,
	sess kernel.ULID,
	kind sso.Kind,
	e kernel.OrganizationAuditEntry,
) ([]linking.Federation, []kernel.ULID, error) {
	var (
		left []linking.Federation
		ids  []kernel.ULID
	)
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		all, err := identities(ctx, q, org, m, "FOR UPDATE OF i")
		if err != nil {
			return err
		}
		own, err := sessionIdentity(ctx, q, sess)
		if err != nil {
			return err
		}
		remove, err := linking.PlanUnlink(all, kind, own)
		if err != nil {
			return err //nolint:wrapcheck // the rule names the refusal
		}
		for _, f := range remove {
			if _, err := q.Exec(ctx, `DELETE FROM member_identities
				WHERE organization_id = $1 AND member_id = $2 AND identity_provider_id = $3 AND subject = $4`,
				org, m, f.ProviderID, f.Subject); err != nil {
				return fmt.Errorf("access: remove identity: %w", sql.Unavailable(err))
			}
			revoked, err := revoke(ctx, q, `organization_id = $1 AND member_id = $2
				AND identity_provider_id = $3 AND idp_subject = $4`, org, m, f.ProviderID, f.Subject)
			if err != nil {
				return err
			}
			for _, ss := range revoked {
				ids = append(ids, ss.ID)
			}
		}
		left = linking.Without(all, remove)
		e.Body = linking.UnlinkBody(remove)
		_, err = audit.RecordOrganization(ctx, e)
		return err //nolint:wrapcheck // names itself
	})
	if err != nil {
		return nil, nil, fmt.Errorf("unlink member %s's %s identities: %w", m, kind, sql.Unavailable(err))
	}
	return left, ids, nil
}

// sessionIdentity is the identity the session id signed in through, nil
// for a session of none, such as an API key's.
func sessionIdentity(ctx context.Context, q tx.Querier, id kernel.ULID) (*linking.Federation, error) {
	var (
		provider *kernel.ULID
		subject  *string
	)
	err := q.QueryRow(ctx, `SELECT identity_provider_id, idp_subject FROM sessions WHERE id = $1`, id).
		Scan(&provider, &subject)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // no session signed in through no identity
	}
	if err != nil {
		return nil, fmt.Errorf("access: read session %s: %w", id, sql.Unavailable(err))
	}
	if provider == nil || subject == nil {
		return nil, nil //nolint:nilnil // as above
	}
	return &linking.Federation{ProviderID: *provider, Subject: *subject}, nil
}

// proveLink records the link sign-in rec of the member m, the member its
// identity names, under the organization's lock SignInIdentity holds, and
// answers the member the sign-in signs in as: m, or, when the sign-in is
// the second of the link's members and merges them, the link's member.
// The link's clock is the sign-in's, rec.Session.CreatedAt. A link that is
// gone, as an expired link is once authn.sessions.expire runs, has ended.
//
// ref: decision 0006 § "Members and identity providers"
func (s *Store) proveLink(ctx context.Context, q tx.Querier, rec sso.SignInRecord, m kernel.MemberID) (kernel.MemberID, error) {
	org, now := rec.Provider.OrganizationID, rec.Session.CreatedAt
	l, err := scanLink(q.QueryRow(ctx, `SELECT `+linkColumns+` FROM member_links
		WHERE organization_id = $1 AND id = $2 FOR UPDATE`, org, rec.Link))
	if errors.Is(err, pgx.ErrNoRows) {
		return kernel.MemberID{}, fmt.Errorf("%w: link %s", linking.ErrEnded, rec.Link)
	}
	if err != nil {
		return kernel.MemberID{}, fmt.Errorf("access: read link %s: %w", rec.Link, sql.Unavailable(err))
	}
	l, done, err := linking.Prove(l, m, rec.Prover, now)
	if err != nil {
		return kernel.MemberID{}, err //nolint:wrapcheck // the rule names the refusal
	}
	if done {
		if err := s.merge(ctx, q, l); err != nil {
			return kernel.MemberID{}, err
		}
		m = l.MemberID
	}
	if _, err := q.Exec(ctx, `UPDATE member_links SET member_signed_at = $2,
			other_member_signed_at = $3, completed_at = $4 WHERE id = $1`,
		l.ID, nullTime(l.MemberSignedAt), nullTime(l.OtherMemberSignedAt), nullTime(l.CompletedAt)); err != nil {
		return kernel.MemberID{}, fmt.Errorf("access: record link %s's sign-in: %w", l.ID, sql.Unavailable(err))
	}
	if done {
		if _, err := audit.RecordOrganization(ctx, linking.MergeEntry(l, rec.Session.IPAddress, now)); err != nil {
			return kernel.MemberID{}, err //nolint:wrapcheck // names itself
		}
	}
	return m, nil
}

// merge moves l's other member into its member and deletes the other
// member, in the caller's transaction. Its sessions, and those of its
// personal keys, are revoked on every replica and then name the member,
// as the revoked rows stay for the audit trail. Its identities, account
// users with their invitations, user role bindings, personal keys,
// organization role and created organization keys move to the member;
// where the member holds the same account user, binding or role already,
// the member's stays and the other's is dropped. The other member's
// feature preview setting in each account that admits it is removed
// through the features domain. Every account whose
// admissions or bindings change has its authorization snapshot rebuilt.
// Both members must be live: a deactivated one ends the sign-in with
// sso.ErrDeactivated, and one that is gone with linking.ErrEnded.
//
// ref: decision 0006 § "Members and identity providers"
func (s *Store) merge(ctx context.Context, q tx.Querier, l linking.Link) error {
	org, keep, gone := l.OrganizationID, l.MemberID, l.OtherMemberID
	rows, err := q.Query(ctx, `SELECT status FROM members
		WHERE organization_id = $1 AND id IN ($2, $3) ORDER BY id FOR UPDATE`, org, keep, gone)
	if err != nil {
		return fmt.Errorf("access: lock linked members: %w", sql.Unavailable(err))
	}
	statuses, err := pgx.CollectRows(rows, pgx.RowTo[member.Status])
	if err != nil {
		return fmt.Errorf("access: lock linked members: %w", sql.Unavailable(err))
	}
	if len(statuses) != 2 {
		return fmt.Errorf("%w: a member of link %s is gone", linking.ErrEnded, l.ID)
	}
	for _, st := range statuses {
		if st == member.Deactivated {
			return fmt.Errorf("%w: a member of link %s", sso.ErrDeactivated, l.ID)
		}
	}
	rows, err = q.Query(ctx, `SELECT account_id FROM account_users WHERE organization_id = $1 AND member_id = $2
		UNION SELECT account_id FROM role_bindings WHERE actor_kind = 'user' AND actor_id = $2
		UNION SELECT account_id FROM api_keys WHERE owner_user_id = $2`, org, gone)
	if err != nil {
		return fmt.Errorf("access: read merged member's accounts: %w", sql.Unavailable(err))
	}
	accounts, err := pgx.CollectRows(rows, pgx.RowTo[kernel.AccountID])
	if err != nil {
		return fmt.Errorf("access: read merged member's accounts: %w", sql.Unavailable(err))
	}
	rows, err = q.Query(ctx, `SELECT account_id FROM account_users WHERE organization_id = $1 AND member_id = $2`,
		org, gone)
	if err != nil {
		return fmt.Errorf("access: read merged member's admissions: %w", sql.Unavailable(err))
	}
	admitted, err := pgx.CollectRows(rows, pgx.RowTo[kernel.AccountID])
	if err != nil {
		return fmt.Errorf("access: read merged member's admissions: %w", sql.Unavailable(err))
	}
	revoked, err := revoke(ctx, q, `organization_id = $1 AND (member_id = $2
		OR (actor_kind = 'api_key' AND actor_id IN (SELECT id FROM api_keys WHERE owner_user_id = $2)))`,
		org, gone)
	if err != nil {
		return err
	}
	for _, ss := range revoked {
		if s.dropSession != nil {
			tx.AfterCommit(ctx, func(context.Context) { s.dropSession(ss.ID) })
		}
	}
	for _, st := range []struct {
		what, query string
		args        []any
	}{
		{
			"sessions", `UPDATE sessions SET member_id = $2 WHERE organization_id = $1 AND member_id = $3`,
			[]any{org, keep, gone},
		},
		{
			"authorization requests", `UPDATE oauth_requests SET member_id = $1 WHERE member_id = $2`,
			[]any{keep, gone},
		},
		{"identities", `UPDATE member_identities SET member_id = $2
			WHERE organization_id = $1 AND member_id = $3`, []any{org, keep, gone}},
		{
			"duplicate invitations", `DELETE FROM account_invitations i USING account_users u
			WHERE i.member_id = $2 AND u.account_id = i.account_id AND u.member_id = $1`,
			[]any{keep, gone},
		},
		{"account users", `INSERT INTO account_users (account_id, organization_id, member_id,
				created_at, latest_groups, slack_member_id)
			SELECT account_id, organization_id, $2, created_at, latest_groups, slack_member_id
			FROM account_users WHERE organization_id = $1 AND member_id = $3
			ON CONFLICT (account_id, member_id) DO NOTHING`, []any{org, keep, gone}},
		{
			"invitations", `UPDATE account_invitations SET member_id = $1 WHERE member_id = $2`,
			[]any{keep, gone},
		},
		{
			"merged account users", `DELETE FROM account_users WHERE organization_id = $1 AND member_id = $2`,
			[]any{org, gone},
		},
		{"duplicate bindings", `DELETE FROM role_bindings g WHERE g.actor_kind = 'user' AND g.actor_id = $2
			AND EXISTS (SELECT 1 FROM role_bindings k WHERE k.actor_kind = 'user' AND k.actor_id = $1
				AND k.role_id = g.role_id AND k.space_id = g.space_id)`, []any{keep, gone}},
		{"bindings", `UPDATE role_bindings SET actor_id = $1, version = version + 1
			WHERE actor_kind = 'user' AND actor_id = $2`, []any{keep, gone}},
		{"personal keys", `UPDATE api_keys SET owner_user_id = $1, version = version + 1
			WHERE owner_user_id = $2`, []any{keep, gone}},
		{"organization role", `INSERT INTO organization_admins (organization_id, member_id, created_by, created_at)
			SELECT organization_id, $2, created_by, created_at FROM organization_admins
			WHERE organization_id = $1 AND member_id = $3 ON CONFLICT DO NOTHING`, []any{org, keep, gone}},
		{"merged organization role", `DELETE FROM organization_admins
			WHERE organization_id = $1 AND member_id = $2`, []any{org, gone}},
		{"organization keys", `UPDATE organization_keys SET creator_member_id = $2
			WHERE organization_id = $1 AND creator_member_id = $3`, []any{org, keep, gone}},
		{"other links", `DELETE FROM member_links WHERE organization_id = $1
			AND (member_id = $2 OR other_member_id = $2) AND id <> $3`, []any{org, gone, l.ID}},
		{
			"merged member", `DELETE FROM members WHERE organization_id = $1 AND id = $2`,
			[]any{org, gone},
		},
	} {
		if _, err := q.Exec(ctx, st.query, st.args...); err != nil {
			return fmt.Errorf("access: merge member %s's %s: %w", gone, st.what, sql.Unavailable(err))
		}
	}
	// The features domain keys a member's feature preview setting by the
	// member's ID without a foreign key, so the merged member's would
	// outlive it; the member keeps its own setting in each account.
	if s.features != nil {
		for _, acct := range admitted {
			if err := s.features.RemoveMember(ctx, acct, gone); err != nil {
				return fmt.Errorf("access: remove the merged member's feature preview setting: %w", err)
			}
		}
	}
	for _, acct := range accounts {
		if err := s.bumpAuthz(ctx, q, acct); err != nil {
			return err
		}
	}
	return nil
}
