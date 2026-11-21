package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/settings"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/audit"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// SetAuthorizationScheme commits a strategy change and all its security effects
// together. The account lock orders concurrent switches and session issuance;
// rechecking the caller after that lock prevents an already-revoked second
// switch from preserving another administrative session.
func (s *Store) SetAuthorizationScheme(ctx context.Context, p kernel.Principal, next accessapi.AuthorizationScheme, entry kernel.AuditEntry) ([]kernel.ULID, error) {
	if !next.Valid() {
		return nil, fmt.Errorf("%w: unknown authorization scheme %q", settings.ErrInvalid, next)
	}
	var ids []kernel.ULID
	// READ COMMITTED is intentional: a snapshot taken while waiting for an
	// issuing session's share lock would miss that session after it commits.
	// Every authorization writer bumps the locked account before committing,
	// so the lock also keeps the caller's authorization rows stable here.
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		ids = nil // each transaction retry collects only its committed revocations
		q := tx.Q(ctx, s.db)
		var previous accessapi.AuthorizationScheme
		if err := q.QueryRow(ctx, `SELECT authorization_scheme FROM accounts
			WHERE id = $1 FOR NO KEY UPDATE`, p.AccountID).Scan(&previous); err != nil {
			return read(err, "account", p.AccountID.ULID)
		}
		if err := s.schemeCaller(ctx, p); err != nil {
			return err
		}
		if previous == next {
			return nil
		}
		if err := retainSchemeCaller(ctx, q, p); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `UPDATE accounts SET authorization_scheme = $2 WHERE id = $1`,
			p.AccountID, next); err != nil {
			return fmt.Errorf("access: change authorization scheme: %w", sql.Unavailable(err))
		}
		revoked, err := revoke(ctx, q, `account_id = $1 AND id <> $2`, p.AccountID, p.SessionID)
		if err != nil {
			return err
		}
		for _, ss := range revoked {
			ids = append(ids, ss.ID)
		}
		if err := s.bumpAuthz(ctx, q, p.AccountID); err != nil {
			return err
		}
		entry.Body, err = json.Marshal(accessapi.SchemeChanged{Previous: previous, Current: next})
		if err != nil {
			return fmt.Errorf("access: encode authorization scheme change: %w", err)
		}
		if _, err := audit.Record(ctx, entry); err != nil {
			return fmt.Errorf("access: record authorization scheme change: %w", err)
		}
		return s.schemeChanged(ctx, q, p.AccountID, entry.Body)
	})
	if err != nil {
		return nil, fmt.Errorf("change authorization scheme of account %s: %w", p.AccountID, sql.Unavailable(err))
	}
	return ids, nil
}

// schemeCaller trusts neither the request's cached admin decision nor a session
// that another switch ended while this request waited for the account lock.
func (s *Store) schemeCaller(ctx context.Context, p kernel.Principal) error {
	var live bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+liveSession+` FROM sessions
		WHERE account_id = $1 AND id = $2 AND actor_kind = $3 AND actor_id = $4 FOR UPDATE`,
		p.AccountID, p.SessionID, p.Kind, p.ActorID).Scan(&live)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !live {
		return settings.ErrSessionEnded
	}
	if err != nil {
		return fmt.Errorf("access: read scheme caller: %w", sql.Unavailable(err))
	}
	d, err := s.AuthzData(ctx, p.AccountID)
	if err != nil {
		return err
	}
	if !authz.Build(d).Admin(authz.WhoOf(p)) {
		return settings.ErrNotAdmin
	}
	return nil
}

func retainSchemeCaller(ctx context.Context, q tx.Querier, p kernel.Principal) error {
	_, err := q.Exec(ctx, `INSERT INTO session_grants (account_id, id, session_id, role_id, space_id, origin)
		SELECT $1, $2, $3, r.id, $1, 'SCHEME_SWITCH' FROM roles r
		WHERE r.account_id = $1 AND r.is_system AND r.slug = 'space-admin'
		ON CONFLICT (session_id, role_id, space_id, origin) DO NOTHING`,
		p.AccountID, kernel.NewULID(), p.SessionID)
	if err != nil {
		return fmt.Errorf("access: retain scheme caller's grant: %w", sql.Unavailable(err))
	}
	return nil
}

func (s *Store) schemeChanged(ctx context.Context, q tx.Querier, acct kernel.AccountID, body json.RawMessage) error {
	if s.events == nil {
		return nil
	}
	var version int64
	if err := q.QueryRow(ctx, `SELECT authz_version FROM accounts WHERE id = $1`, acct).Scan(&version); err != nil {
		return read(err, "account", acct.ULID)
	}
	_, err := s.events.Append(ctx, outbox.Event{
		Account: acct.ULID, Kind: accessapi.SchemeChangedKind, SchemaVersion: accessapi.SchemeChangedVersion,
		Subject: outbox.Subject{Kind: "account", ID: acct.String()}, Sequence: version, Document: body,
	})
	if err != nil {
		return fmt.Errorf("access: publish authorization scheme change: %w", err)
	}
	return nil
}
