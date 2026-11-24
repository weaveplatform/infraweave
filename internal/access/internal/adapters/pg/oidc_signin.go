package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/linking"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/audit"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var _ sso.SignInStore = (*Store)(nil)

// requestVerifier is a started sign-in's PKCE verifier, bound to the
// request's id as its uuid text.
var requestVerifier = crypt.Column{
	Table: "sso_requests",
	Name:  "pkce_verifier_sealed",
	Key:   []string{"id"},
}

// sessionIDToken is the ID token a browser session was signed in with,
// bound to the session's id as its uuid text.
var sessionIDToken = crypt.Column{
	Table: "sessions",
	Name:  "idp_id_token_sealed",
	Key:   []string{"id"},
}

// SignInOrganization reads the organization slug names.
func (s *Store) SignInOrganization(ctx context.Context, slug kernel.Slug) (organization.Organization, error) {
	var o organization.Organization
	err := tx.Q(ctx, s.db).QueryRow(ctx,
		`SELECT id, slug, name, created_at FROM organizations WHERE slug = $1`, slug).
		Scan(&o.ID, &o.Slug, &o.Name, &o.CreatedAt)
	return o, read(err, "organization", slug)
}

// SoleOrganization is the installation's only organization's slug, and
// false when it has none or several.
func (s *Store) SoleOrganization(ctx context.Context) (kernel.Slug, bool, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT slug FROM organizations ORDER BY id LIMIT 2`)
	if err != nil {
		return "", false, fmt.Errorf("access: list organizations: %w", sql.Unavailable(err))
	}
	slugs, err := pgx.CollectRows(rows, pgx.RowTo[kernel.Slug])
	if err != nil {
		return "", false, fmt.Errorf("access: list organizations: %w", sql.Unavailable(err))
	}
	if len(slugs) != 1 {
		return "", false, nil
	}
	return slugs[0], true, nil
}

// CreateSignInRequest stores the started sign-in r with its PKCE verifier
// sealed to its id.
func (s *Store) CreateSignInRequest(ctx context.Context, r sso.Request) error {
	sealed, err := s.seal.SealColumn(ctx, requestVerifier, []string{r.ID.UUID()}, []byte(r.Verifier))
	if err != nil {
		return fmt.Errorf("access: seal sign-in request %s: %w", r.ID, err)
	}
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO sso_requests (id, organization_id,
			identity_provider_id, purpose, link_id, state_hash, nonce, pkce_verifier_sealed,
			browser_hash, return_to, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		r.ID, r.OrganizationID, r.ProviderID, r.Purpose, r.LinkID, r.StateHash, r.Nonce, sealed,
		r.BrowserHash, r.ReturnTo, r.CreatedAt, r.ExpiresAt); err != nil {
		return fmt.Errorf("access: insert sign-in request %s: %w", r.ID, sql.Unavailable(err))
	}
	return nil
}

// ConsumeSignInRequest marks the live request of stateHash and browserHash
// consumed and returns it with its verifier opened. The UPDATE's row lock
// makes a second callback of the request wait for the first and then find
// it consumed, so the request is consumed once.
func (s *Store) ConsumeSignInRequest(ctx context.Context, stateHash, browserHash []byte) (sso.Request, error) {
	var (
		r      sso.Request
		sealed []byte
	)
	err := tx.Q(ctx, s.db).QueryRow(ctx, `UPDATE sso_requests SET consumed_at = now()
		WHERE state_hash = $1 AND browser_hash = $2 AND consumed_at IS NULL AND expires_at > now()
		RETURNING id, organization_id, identity_provider_id, purpose, link_id, state_hash, nonce,
			pkce_verifier_sealed, browser_hash, return_to, created_at, expires_at`,
		stateHash, browserHash).Scan(&r.ID, &r.OrganizationID, &r.ProviderID, &r.Purpose, &r.LinkID,
		&r.StateHash, &r.Nonce, &sealed, &r.BrowserHash, &r.ReturnTo, &r.CreatedAt, &r.ExpiresAt)
	if err := read(err, "sign-in request", "by its state"); err != nil {
		return sso.Request{}, err
	}
	v, err := s.seal.OpenColumn(ctx, requestVerifier, []string{r.ID.UUID()}, sealed)
	if err != nil {
		return sso.Request{}, fmt.Errorf("access: open sign-in request %s: %w", r.ID, err)
	}
	r.Verifier = string(v.Reveal())
	return r, nil
}

// SignInIdentity resolves rec's identity to a member, records the identity and
// the member's last_seen_at and last_sign_in_kind, and creates rec.Session for the member, in
// one transaction. The organization's row lock, which invites take too,
// orders sign-ins and invites of one login, so two never both create a
// member for one identity or both pass the seats check for the last seat.
// A link's sign-in needs an identity a member holds already and signs in
// the member proveLink answers.
//
// ref: decision 0006 § "Human login"
func (s *Store) SignInIdentity(ctx context.Context, rec sso.SignInRecord) (session.Session, error) {
	ss := rec.Session
	org, p, id := rec.Provider.OrganizationID, rec.Provider.ID, rec.Identity
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if _, err := q.Exec(ctx, `SELECT 1 FROM organizations WHERE id = $1 FOR NO KEY UPDATE`,
			org); err != nil {
			return fmt.Errorf("access: lock organization: %w", sql.Unavailable(err))
		}
		bound, pending, err := candidates(ctx, q, org, p, id)
		if err != nil {
			return err
		}
		res, m, err := sso.ResolveMember(bound, pending)
		if err != nil {
			return err //nolint:wrapcheck // the rule names the refusal
		}
		if !rec.Member.IsZero() && m != rec.Member {
			return sso.ErrOtherMember
		}
		if !rec.Link.IsZero() {
			if bound == nil {
				return fmt.Errorf("%w: no member holds the identity", linking.ErrNotLinked)
			}
			merged, err := s.proveLink(ctx, q, rec, m)
			if err != nil {
				return err
			}
			if merged != m {
				m, res = merged, sso.ResolveExisting
			}
		}
		if m, err = s.admitMember(ctx, q, org, res, m, id); err != nil {
			return err
		}
		if bound == nil {
			if _, err := q.Exec(ctx, `INSERT INTO member_identities
					(organization_id, member_id, identity_provider_id, subject, login)
				VALUES ($1, $2, $3, $4, $5)`, org, m, p, id.Subject, id.Login); err != nil {
				return fmt.Errorf("access: record identity: %w", sql.Unavailable(err))
			}
		}
		if _, err := q.Exec(ctx, `UPDATE members SET last_seen_at = now(), last_sign_in_kind = $3
			WHERE organization_id = $1 AND id = $2`, org, m, rec.Provider.Kind); err != nil {
			return fmt.Errorf("access: record sign-in: %w", sql.Unavailable(err))
		}
		ss.MemberID, ss.ActorID = m, m.ULID
		if err := s.CreateSession(ctx, ss); err != nil {
			return err
		}
		if err := s.sealIDToken(ctx, q, ss.ID, rec.IDToken); err != nil {
			return err
		}
		if rec.Enable == nil {
			return nil
		}
		if _, err := q.Exec(ctx, `UPDATE identity_providers SET enabled_at = now(), admin_login = $3
			WHERE organization_id = $1 AND id = $2`, org, p, id.Login); err != nil {
			return fmt.Errorf("access: enable identity provider %s: %w", p, sql.Unavailable(err))
		}
		e := *rec.Enable
		e.ActorID = m.String()
		_, err = audit.RecordOrganization(ctx, e)
		return err //nolint:wrapcheck // names itself
	})
	if err != nil {
		return session.Session{}, fmt.Errorf("sign in through identity provider %s: %w", p, sql.Unavailable(err))
	}
	return ss, nil
}

// candidates are the member the provider's subject names, locked, or nil
// when none does, and otherwise the pending members invited under the
// login, locked.
func candidates(ctx context.Context, q tx.Querier, org kernel.OrganizationID, p kernel.ULID, id sso.Identity) (*sso.Candidate, []sso.Candidate, error) {
	var c sso.Candidate
	err := q.QueryRow(ctx, `SELECT m.id, m.status FROM member_identities i
		JOIN members m ON m.organization_id = i.organization_id AND m.id = i.member_id
		WHERE i.identity_provider_id = $1 AND i.subject = $2 FOR UPDATE OF m`, p, id.Subject).
		Scan(&c.ID, &c.Status)
	switch {
	case err == nil:
		return &c, nil, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, nil, fmt.Errorf("access: read identity: %w", sql.Unavailable(err))
	}
	rows, err := q.Query(ctx, `SELECT id, status FROM members
		WHERE organization_id = $1 AND login = $2 AND status = 'pending' ORDER BY id LIMIT 2 FOR UPDATE`,
		org, id.Login)
	if err != nil {
		return nil, nil, fmt.Errorf("access: read pending members: %w", sql.Unavailable(err))
	}
	pending, err := pgx.CollectRows(rows, pgx.RowToStructByPos[sso.Candidate])
	if err != nil {
		return nil, nil, fmt.Errorf("access: read pending members: %w", sql.Unavailable(err))
	}
	return nil, pending, nil
}

// admitMember carries out res for the member m: it creates an active
// member of the identity after the seats check, which counts it as no
// account admits it yet, or makes a pending member active. It returns the
// member.
func (s *Store) admitMember(ctx context.Context, q tx.Querier, org kernel.OrganizationID, res sso.Resolution, m kernel.MemberID, id sso.Identity) (kernel.MemberID, error) {
	switch res {
	case sso.ResolveCreate:
		if err := s.checkSeat(ctx, org, kernel.AccountID{}, false); err != nil {
			return kernel.MemberID{}, err
		}
		m = kernel.NewMemberID()
		if _, err := q.Exec(ctx, `INSERT INTO members (organization_id, id, login, name, email, status)
			VALUES ($1, $2, $3, $4, $5, 'active')`, org, m, id.Login, id.Name, id.Email); err != nil {
			return kernel.MemberID{}, fmt.Errorf("access: create member: %w", sql.Unavailable(err))
		}
	case sso.ResolveActivate:
		if _, err := q.Exec(ctx, `UPDATE members SET status = 'active'
			WHERE organization_id = $1 AND id = $2`, org, m); err != nil {
			return kernel.MemberID{}, fmt.Errorf("access: activate member %s: %w", m, sql.Unavailable(err))
		}
	case sso.ResolveExisting:
	}
	return m, nil
}

// sealIDToken stores raw, sealed to the session id, beside it; an empty
// raw stores nothing.
func (s *Store) sealIDToken(ctx context.Context, q tx.Querier, id kernel.ULID, raw string) error {
	if raw == "" {
		return nil
	}
	sealed, err := s.seal.SealColumn(ctx, sessionIDToken, []string{id.UUID()}, []byte(raw))
	if err != nil {
		return fmt.Errorf("access: seal session %s's ID token: %w", id, err)
	}
	if _, err := q.Exec(ctx, `UPDATE sessions SET idp_id_token_sealed = $2 WHERE id = $1`,
		id, sealed); err != nil {
		return fmt.Errorf("access: store session %s's ID token: %w", id, sql.Unavailable(err))
	}
	return nil
}

// carryIDToken copies the ID token from's session holds, if any, to the
// session to, sealed to its own id, as an account switch replaces a
// session with another.
func (s *Store) carryIDToken(ctx context.Context, q tx.Querier, from, to kernel.ULID) error {
	tok, ok, err := s.sessionIDToken(ctx, q, from)
	if err != nil || !ok {
		return err
	}
	return s.sealIDToken(ctx, q, to, string(tok.Reveal()))
}

// SessionIDToken opens the ID token the session id was signed in with,
// and false when it holds none.
func (s *Store) SessionIDToken(ctx context.Context, id kernel.ULID) (crypt.Value, bool, error) {
	return s.sessionIDToken(ctx, tx.Q(ctx, s.db), id)
}

func (s *Store) sessionIDToken(ctx context.Context, q tx.Querier, id kernel.ULID) (crypt.Value, bool, error) {
	var sealed []byte
	err := q.QueryRow(ctx, `SELECT idp_id_token_sealed FROM sessions WHERE id = $1`, id).Scan(&sealed)
	if err := read(err, "session", id); err != nil {
		return crypt.Value{}, false, err
	}
	if sealed == nil {
		return crypt.Value{}, false, nil
	}
	v, err := s.seal.OpenColumn(ctx, sessionIDToken, []string{id.UUID()}, sealed)
	if err != nil {
		return crypt.Value{}, false, fmt.Errorf("access: open session %s's ID token: %w", id, err)
	}
	return v, true, nil
}

// ProvidersByClientID are the OIDC providers of every organization whose
// client ID is one of ids, oldest first.
func (s *Store) ProvidersByClientID(ctx context.Context, ids []string) ([]sso.Provider, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+providerColumns+` FROM identity_providers
		WHERE kind = 'OIDC' AND settings->>'clientId' = ANY($1) ORDER BY created_at, id`, ids)
	if err != nil {
		return nil, fmt.Errorf("access: find identity providers: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (sso.Provider, error) {
		return scanProvider(r)
	})
	if err != nil {
		return nil, fmt.Errorf("access: find identity providers: %w", sql.Unavailable(err))
	}
	return out, nil
}

// AcceptLogoutToken records the provider's logout token jti until
// expires, and reports false when it is recorded already. The primary
// key decides between two replicas given one token at once.
func (s *Store) AcceptLogoutToken(ctx context.Context, provider kernel.ULID, jti string, expires time.Time) (bool, error) {
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO oidc_logout_tokens (identity_provider_id, jti, expires_at)
		VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, provider, jti, expires)
	if err != nil {
		return false, fmt.Errorf("access: record logout token: %w", sql.Unavailable(err))
	}
	return tag.RowsAffected() == 1, nil
}

// RevokeIdentitySessions revokes the live sessions signed in through the
// provider with the provider session sid, or with the subject when sid is
// empty, sending each one's message, in one transaction.
func (s *Store) RevokeIdentitySessions(ctx context.Context, provider kernel.ULID, sid, subject string) ([]kernel.ULID, error) {
	var ids []kernel.ULID
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		where, arg := `identity_provider_id = $1 AND idp_sid = $2`, sid
		if sid == "" {
			where, arg = `identity_provider_id = $1 AND idp_subject = $2`, subject
		}
		out, err := revoke(ctx, tx.Q(ctx, s.db), where, provider, arg)
		for _, ss := range out {
			ids = append(ids, ss.ID)
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("revoke the sessions of identity provider %s: %w", provider, sql.Unavailable(err))
	}
	return ids, nil
}

// ExpireSignIns deletes the sign-in requests that ended, consumed or
// expired, before before, the logout tokens expired before it, and the
// links that expired before it without completing.
func (s *Store) ExpireSignIns(ctx context.Context, before time.Time) (int64, error) {
	q := tx.Q(ctx, s.db)
	reqs, err := q.Exec(ctx, `DELETE FROM sso_requests
		WHERE least(consumed_at, expires_at) < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("access: expire sign-in requests: %w", sql.Unavailable(err))
	}
	toks, err := q.Exec(ctx, `DELETE FROM oidc_logout_tokens WHERE expires_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("access: expire logout tokens: %w", sql.Unavailable(err))
	}
	links, err := q.Exec(ctx, `DELETE FROM member_links
		WHERE completed_at IS NULL AND expires_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("access: expire links: %w", sql.Unavailable(err))
	}
	return reqs.RowsAffected() + toks.RowsAffected() + links.RowsAffected(), nil
}
