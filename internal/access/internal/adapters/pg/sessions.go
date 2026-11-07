package pg

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var (
	_ port.Sessions = (*Store)(nil)
	_ port.APIKeys  = (*Store)(nil)
)

// sessionColumns are what scanSession reads, in its order.
const sessionColumns = `organization_id, account_id, id, channel, actor_kind, actor_id,
	member_id, login, name, email, admin, teams, scopes, coalesce(host(ip_address), ''),
	created_at, expires_at, idle_expires_at, revoked_at, secret_hash, parent_session_id`

// Each write's transaction may fail to begin or commit with pgx's own
// error, which the writes mark as kernel.ErrUnavailable when PostgreSQL is
// unreachable, so the request answers 503 rather than 500.

// CreateSession inserts s, first revoking the member's live sessions in
// s's channel when s is a member's session in a channel but UNKNOWN.
func (s *Store) CreateSession(ctx context.Context, ss session.Session) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if !ss.MemberID.IsZero() && ss.Channel != session.ChannelUnknown {
			if _, err := revoke(ctx, q, `organization_id = $1 AND member_id = $2 AND channel = $3`,
				ss.OrganizationID, ss.MemberID, ss.Channel); err != nil {
				return err
			}
		}
		var idle *time.Time
		if !ss.IdleExpiresAt.IsZero() {
			idle = &ss.IdleExpiresAt
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO sessions (organization_id, account_id, id, channel, actor_kind,
				actor_id, member_id, login, name, email, admin, teams, scopes, ip_address,
				expires_at, idle_expires_at, secret_hash, parent_session_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
				NULLIF($14, '')::inet, $15, $16, $17, $18)`,
			ss.OrganizationID, ss.AccountID, ss.ID, ss.Channel, ss.ActorKind, ss.ActorID,
			ss.MemberID, ss.Login, ss.Name, ss.Email, ss.Admin, nonNil(ss.Teams),
			nonNil(ss.Scopes), ss.IPAddress, ss.ExpiresAt, idle, ss.SecretHash,
			ss.ParentID); err != nil {
			return fmt.Errorf("access: insert session: %w", sql.Unavailable(err))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("create session %s: %w", ss.ID, sql.Unavailable(err))
	}
	return nil
}

// Session reads the session id.
func (s *Store) Session(ctx context.Context, id kernel.ULID) (session.Session, error) {
	ss, err := scanSession(tx.Q(ctx, s.db).QueryRow(ctx,
		`SELECT `+sessionColumns+` FROM sessions WHERE id = $1`, id))
	return ss, read(err, "session", id)
}

// SessionBySecret reads the browser session whose cookie secret hashes
// to hash.
func (s *Store) SessionBySecret(ctx context.Context, hash []byte) (session.Session, error) {
	ss, err := scanSession(tx.Q(ctx, s.db).QueryRow(ctx,
		`SELECT `+sessionColumns+` FROM sessions WHERE secret_hash = $1`, hash))
	return ss, read(err, "session", "by its secret")
}

// TouchSession records a use of the session id and moves its idle end to
// idle. A revoked session stays as it is.
func (s *Store) TouchSession(ctx context.Context, id kernel.ULID, idle time.Time) error {
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE sessions
		SET last_seen_at = now(), idle_expires_at = $2
		WHERE id = $1 AND revoked_at IS NULL`, id, idle); err != nil {
		return fmt.Errorf("access: touch session %s: %w", id, sql.Unavailable(err))
	}
	return nil
}

// EndSession revokes the session id, whatever its account or none.
// Ending an ended session succeeds and sends the message again.
func (s *Store) EndSession(ctx context.Context, id kernel.ULID) (session.Session, error) {
	var ss session.Session
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		var err error
		ss, err = scanSession(q.QueryRow(ctx, `UPDATE sessions
			SET revoked_at = coalesce(revoked_at, now())
			WHERE id = $1 RETURNING `+sessionColumns, id))
		if err != nil {
			return read(err, "session", id)
		}
		return send(ctx, q, id)
	})
	if err != nil {
		return session.Session{}, fmt.Errorf("end session %s: %w", id, sql.Unavailable(err))
	}
	return ss, nil
}

// Sessions reads a page of the account's live sessions in ID order.
func (s *Store) Sessions(
	ctx context.Context,
	acct kernel.AccountID,
	after kernel.ULID,
	limit int,
) ([]session.Session, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+sessionColumns+` FROM sessions
		WHERE account_id = $1 AND revoked_at IS NULL AND expires_at > now()
			AND (idle_expires_at IS NULL OR idle_expires_at > now())
			AND ($2::uuid IS NULL OR id > $2)
		ORDER BY id LIMIT $3`, acct, after, limit)
	if err != nil {
		return nil, fmt.Errorf("access: list sessions: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (session.Session, error) {
		return scanSession(r)
	})
	if err != nil {
		return nil, fmt.Errorf("access: list sessions: %w", sql.Unavailable(err))
	}
	return out, nil
}

// RevokeSession revokes the account's session id. Revoking a revoked
// session succeeds and sends the message again.
func (s *Store) RevokeSession(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (session.Session, error) {
	var ss session.Session
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		var err error
		ss, err = scanSession(q.QueryRow(ctx, `UPDATE sessions
			SET revoked_at = coalesce(revoked_at, now())
			WHERE account_id = $1 AND id = $2 RETURNING `+sessionColumns, acct, id))
		if err != nil {
			return read(err, "session", id)
		}
		return send(ctx, q, id)
	})
	if err != nil {
		return session.Session{}, fmt.Errorf("revoke session %s: %w", id, sql.Unavailable(err))
	}
	return ss, nil
}

// RevokeSessionsExcept revokes the account's live sessions but except.
func (s *Store) RevokeSessionsExcept(
	ctx context.Context,
	acct kernel.AccountID,
	except kernel.ULID,
) ([]session.Session, error) {
	var out []session.Session
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		var err error
		out, err = revoke(ctx, tx.Q(ctx, s.db), `account_id = $1 AND id <> $2`, acct, except)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("revoke the sessions of account %s: %w", acct, sql.Unavailable(err))
	}
	return out, nil
}

// ExpireSessions deletes the sessions whose end, the earliest of their
// revocation and their absolute and idle ends, is before before. A
// browser session waits for the CLI sessions it parents, which reference
// it, to go first.
func (s *Store) ExpireSessions(ctx context.Context, before time.Time) (int64, error) {
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM sessions s
		WHERE least(s.revoked_at, s.expires_at, s.idle_expires_at) < $1
			AND NOT EXISTS (SELECT 1 FROM sessions c WHERE c.parent_session_id = s.id)`, before)
	if err != nil {
		return 0, fmt.Errorf("access: expire sessions: %w", sql.Unavailable(err))
	}
	return tag.RowsAffected(), nil
}

// revoke revokes the live sessions where holds, with args, sends each
// one's message, in the caller's transaction, and returns them in ID
// order.
func revoke(
	ctx context.Context,
	q tx.Querier,
	where string,
	args ...any,
) ([]session.Session, error) {
	rows, err := q.Query(ctx, `WITH revoked AS (UPDATE sessions SET revoked_at = now()
		WHERE revoked_at IS NULL AND `+where+` RETURNING *)
		SELECT `+sessionColumns+` FROM revoked ORDER BY id`, args...)
	if err != nil {
		return nil, fmt.Errorf("access: revoke sessions: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (session.Session, error) {
		return scanSession(r)
	})
	if err != nil {
		return nil, fmt.Errorf("access: revoke sessions: %w", sql.Unavailable(err))
	}
	for _, ss := range out {
		if err := send(ctx, q, ss.ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// send tells every replica to drop the session id from its cache.
func send(ctx context.Context, q tx.Querier, id kernel.ULID) error {
	if err := notify.Notify(ctx, q, notify.Invalidate, session.Key(id)); err != nil {
		return fmt.Errorf("access: %w", sql.Unavailable(err))
	}
	return nil
}

// scanSession reads sessionColumns.
func scanSession(r pgx.Row) (session.Session, error) {
	var (
		ss            session.Session
		idle, revoked *time.Time
	)
	err := r.Scan(&ss.OrganizationID, &ss.AccountID, &ss.ID, &ss.Channel, &ss.ActorKind,
		&ss.ActorID, &ss.MemberID, &ss.Login, &ss.Name, &ss.Email, &ss.Admin, &ss.Teams,
		&ss.Scopes, &ss.IPAddress, &ss.CreatedAt, &ss.ExpiresAt, &idle, &revoked, &ss.SecretHash,
		&ss.ParentID)
	if err != nil {
		return session.Session{}, err //nolint:wrapcheck // the callers name the read
	}
	ss.IdleExpiresAt, ss.RevokedAt = deref(idle), deref(revoked)
	return ss, nil
}

// deref is t, or the zero time for NULL.
func deref(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

// nonNil is s, or an empty array for nil, as the NOT NULL text[] columns
// need.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
