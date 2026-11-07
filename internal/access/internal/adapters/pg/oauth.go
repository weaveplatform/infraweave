package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/oauth"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var _ oauth.Store = (*Store)(nil)

// CreateClient records c.
func (s *Store) CreateClient(ctx context.Context, c oauth.Client) error {
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO oauth_clients
		(id, name, redirect_uris, created_at, last_used_at) VALUES ($1, $2, $3, $4, $4)`,
		c.ID, c.Name, c.RedirectURIs, c.CreatedAt); err != nil {
		return fmt.Errorf("access: register OAuth client: %w", sql.Unavailable(err))
	}
	return nil
}

// Client reads the client id.
func (s *Store) Client(ctx context.Context, id kernel.ULID) (oauth.Client, error) {
	var c oauth.Client
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT id, name, redirect_uris, created_at
		FROM oauth_clients WHERE id = $1`, id).
		Scan(&c.ID, &c.Name, &c.RedirectURIs, &c.CreatedAt)
	c.CreatedAt = c.CreatedAt.UTC()
	return c, read(err, "OAuth client", id)
}

// CreateRequest records r and marks its client used.
func (s *Store) CreateRequest(ctx context.Context, r oauth.Request) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if _, err := q.Exec(ctx, `INSERT INTO oauth_requests (id, client_id, redirect_uri,
				resource, scopes, state, code_challenge, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			r.ID, r.Client.ID, r.RedirectURI, r.Resource, r.Scopes, r.State, r.Challenge,
			r.CreatedAt, r.ExpiresAt); err != nil {
			return fmt.Errorf("access: insert: %w", sql.Unavailable(err))
		}
		return touchClient(ctx, q, r.Client.ID)
	})
	if err != nil {
		return fmt.Errorf("record authorization request %s: %w", r.ID, sql.Unavailable(err))
	}
	return nil
}

// touchClient records a use of the client id.
func touchClient(ctx context.Context, q tx.Querier, id kernel.ULID) error {
	if _, err := q.Exec(ctx, `UPDATE oauth_clients SET last_used_at = now() WHERE id = $1`,
		id); err != nil {
		return fmt.Errorf("access: mark OAuth client %s used: %w", id, sql.Unavailable(err))
	}
	return nil
}

// requestColumns are what scanRequest reads, of oauth_requests r joined
// to its client c.
const requestColumns = `r.id, c.id, c.name, c.redirect_uris, c.created_at, r.redirect_uri,
	r.resource, r.scopes, r.state, r.code_challenge, r.created_at, r.expires_at,
	r.decided_at IS NOT NULL, r.organization_id, r.account_id, r.member_id, r.code_hash,
	r.code_expires_at`

// requestFrom is requestColumns' FROM clause.
const requestFrom = ` FROM oauth_requests r JOIN oauth_clients c ON c.id = r.client_id`

// scanRequest reads requestColumns, then extra.
func scanRequest(row pgx.Row, extra ...any) (oauth.Request, error) {
	var (
		r       oauth.Request
		org     *kernel.OrganizationID
		acct    *kernel.AccountID
		member  *kernel.MemberID
		codeEnd *time.Time
	)
	err := row.Scan(append([]any{
		&r.ID, &r.Client.ID, &r.Client.Name, &r.Client.RedirectURIs,
		&r.Client.CreatedAt, &r.RedirectURI, &r.Resource, &r.Scopes, &r.State, &r.Challenge,
		&r.CreatedAt, &r.ExpiresAt, &r.Decided, &org, &acct, &member, &r.CodeHash,
		&codeEnd,
	}, extra...)...)
	if err != nil {
		return oauth.Request{}, err //nolint:wrapcheck // the callers name the read
	}
	if org != nil {
		r.OrganizationID = *org
	}
	if acct != nil {
		r.AccountID = *acct
	}
	if member != nil {
		r.MemberID = *member
	}
	r.Client.CreatedAt, r.CreatedAt = r.Client.CreatedAt.UTC(), r.CreatedAt.UTC()
	r.ExpiresAt, r.CodeExpiresAt = r.ExpiresAt.UTC(), deref(codeEnd)
	return r, nil
}

// Request reads the request id with its client.
func (s *Store) Request(ctx context.Context, id kernel.ULID) (oauth.Request, error) {
	r, err := scanRequest(tx.Q(ctx, s.db).QueryRow(ctx,
		`SELECT `+requestColumns+requestFrom+` WHERE r.id = $1`, id))
	return r, read(err, "authorization request", id)
}

// Decide records the decision d on the request d.ID while it is
// undecided and before its end at now.
func (s *Store) Decide(ctx context.Context, d oauth.Request, now time.Time) error {
	var org, acct, member any
	var codeEnd *time.Time
	if d.CodeHash != nil {
		org, acct, member, codeEnd = d.OrganizationID, d.AccountID, d.MemberID, &d.CodeExpiresAt
	}
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE oauth_requests
		SET decided_at = $2, organization_id = $3, account_id = $4, member_id = $5,
			code_hash = $6, code_expires_at = $7
		WHERE id = $1 AND decided_at IS NULL AND expires_at > $2`,
		d.ID, now, org, acct, member, d.CodeHash, codeEnd)
	if err != nil {
		return fmt.Errorf("access: decide authorization request %s: %w", d.ID,
			sql.Unavailable(err))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: undecided authorization request %s", port.ErrNotFound, d.ID)
	}
	return nil
}

// RedirectHosts are the account's oauthRedirectAllowedHosts.
func (s *Store) RedirectHosts(ctx context.Context, acct kernel.AccountID) ([]string, error) {
	hosts := []string{}
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT oauth_redirect_hosts FROM account_settings
		WHERE account_id = $1`, acct).Scan(&hosts)
	if errors.Is(err, pgx.ErrNoRows) {
		return []string{}, nil
	}
	return hosts, read(err, "redirect hosts of account", acct)
}

// grantOwner reads the member acct admits as active, share-locking the
// admission and the member: removal locks the admission and deactivation
// updates the member before each revokes the member's sessions, so a
// grant written under these locks is one they revoke, and one they
// precede is refused here.
func grantOwner(
	ctx context.Context,
	q tx.Querier,
	acct kernel.AccountID,
	member kernel.MemberID,
) (oauth.Owner, error) {
	var o oauth.Owner
	err := q.QueryRow(ctx, `SELECT m.login, m.name, u.latest_groups
		FROM account_users u JOIN members m ON m.id = u.member_id
		WHERE u.account_id = $1 AND u.member_id = $2 AND m.status = 'active'
		FOR SHARE OF u, m`, acct, member).Scan(&o.Login, &o.Name, &o.Teams)
	return o, read(err, "admitted member", member.ULID)
}

// Redeem spends the code whose SHA-256 is hash for the grant session
// grant builds, under grantOwner's locks.
func (s *Store) Redeem(
	ctx context.Context,
	hash []byte,
	grant func(oauth.Request, oauth.Owner) (session.Session, error),
) (session.Session, error) {
	var (
		out    session.Session
		reused bool
	)
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		var (
			redeemed bool
			sid      *kernel.ULID
		)
		r, err := scanRequest(q.QueryRow(ctx, `SELECT `+requestColumns+`,
			r.redeemed_at IS NOT NULL, r.session_id`+requestFrom+`
			WHERE r.code_hash = $1 FOR UPDATE OF r`, hash), &redeemed, &sid)
		if err != nil {
			return read(err, "authorization code", "by its hash")
		}
		if redeemed {
			reused = true
			if sid == nil {
				return nil
			}
			revoked, err := revoke(ctx, q, `id = $1`, *sid)
			if len(revoked) > 0 {
				out = revoked[0]
			}
			return err
		}
		o, err := grantOwner(ctx, q, r.AccountID, r.MemberID)
		if err != nil {
			return err
		}
		ss, err := grant(r, o)
		if err != nil {
			return err
		}
		if err := insertSession(ctx, q, ss); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `UPDATE oauth_requests SET redeemed_at = now(), session_id = $2
			WHERE id = $1`, r.ID, ss.ID); err != nil {
			return fmt.Errorf("access: redeem: %w", sql.Unavailable(err))
		}
		out = ss
		return touchClient(ctx, q, r.Client.ID)
	})
	switch {
	case err != nil:
		return session.Session{}, fmt.Errorf("redeem an authorization code: %w",
			sql.Unavailable(err))
	case reused:
		return out, oauth.ErrReused
	}
	return out, nil
}

// Refresh rotates the grant whose current refresh token hashes to hash,
// under grantOwner's locks.
func (s *Store) Refresh(
	ctx context.Context,
	hash []byte,
	rotate func(session.Session, oauth.Owner) (session.Session, error),
) (session.Session, error) {
	var (
		out     session.Session
		outcome error
	)
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		outcome = nil
		cur, err := scanSession(q.QueryRow(ctx, `SELECT `+sessionColumns+` FROM sessions
			WHERE secret_hash = $1 AND oauth_client_id IS NOT NULL AND revoked_at IS NULL
			FOR UPDATE`, hash))
		if errors.Is(err, pgx.ErrNoRows) {
			return s.spent(ctx, q, hash, &out, &outcome)
		}
		if err != nil {
			return read(err, "OAuth grant", "by its refresh token")
		}
		o, err := grantOwner(ctx, q, cur.AccountID, cur.MemberID)
		if errors.Is(err, port.ErrNotFound) {
			revoked, err := revoke(ctx, q, `id = $1`, cur.ID)
			out, outcome = cur, fmt.Errorf("%w: the grant's member", port.ErrNotFound)
			if len(revoked) > 0 {
				out = revoked[0]
			}
			return err
		}
		if err != nil {
			return err
		}
		next, err := rotate(cur, o)
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `UPDATE sessions
			SET secret_hash = $2, expires_at = $3, login = $4, name = $5, teams = $6,
				last_seen_at = now()
			WHERE id = $1`, cur.ID, next.SecretHash, next.ExpiresAt, next.Login, next.Name,
			nonNil(next.Teams)); err != nil {
			return fmt.Errorf("access: rotate: %w", sql.Unavailable(err))
		}
		if _, err := q.Exec(ctx, `INSERT INTO oauth_spent_refresh_tokens (hash, session_id)
			VALUES ($1, $2)`, hash, cur.ID); err != nil {
			return fmt.Errorf("access: spend: %w", sql.Unavailable(err))
		}
		out = next
		// sessions.oauth_client_id holds the client's ULID as text.
		client, err := kernel.ParseULID(cur.ClientID)
		if err != nil {
			return fmt.Errorf("access: grant %s names the client %q: %w", cur.ID, cur.ClientID,
				err)
		}
		return touchClient(ctx, q, client)
	})
	if err != nil {
		return session.Session{}, fmt.Errorf("refresh an OAuth grant: %w", sql.Unavailable(err))
	}
	return out, outcome
}

// spent revokes the grant that spent the refresh token hashing to hash,
// answering it and ErrReused through out and outcome, or port.ErrNotFound
// when no grant spent it.
func (s *Store) spent(
	ctx context.Context,
	q tx.Querier,
	hash []byte,
	out *session.Session,
	outcome *error,
) error {
	var sid kernel.ULID
	err := q.QueryRow(ctx, `SELECT session_id FROM oauth_spent_refresh_tokens WHERE hash = $1`,
		hash).Scan(&sid)
	if errors.Is(err, pgx.ErrNoRows) {
		*outcome = fmt.Errorf("%w: refresh token", port.ErrNotFound)
		return nil
	}
	if err != nil {
		return read(err, "spent refresh token", "by its hash")
	}
	*outcome = oauth.ErrReused
	revoked, err := revoke(ctx, q, `id = $1`, sid)
	if len(revoked) > 0 {
		*out = revoked[0]
	}
	return err
}

// ExpireOAuth deletes the requests that ended before requests and the
// clients last used before clients, with their requests.
func (s *Store) ExpireOAuth(ctx context.Context, requests, clients time.Time) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if _, err := q.Exec(ctx, `DELETE FROM oauth_requests WHERE expires_at < $1`,
			requests); err != nil {
			return fmt.Errorf("access: delete: %w", sql.Unavailable(err))
		}
		if _, err := q.Exec(ctx, `DELETE FROM oauth_clients WHERE last_used_at < $1`,
			clients); err != nil {
			return fmt.Errorf("access: delete: %w", sql.Unavailable(err))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("expire OAuth requests and clients: %w", sql.Unavailable(err))
	}
	return nil
}
