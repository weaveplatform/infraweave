package pg

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var _ apikey.Store = (*Store)(nil)

// keyColumns are what scanKey reads, in its order, from api_keys as k
// joined to accounts as a.
const keyColumns = `a.organization_id, k.account_id, k.id, k.name, k.description, k.type,
	k.secret_hash, coalesce(k.oidc_issuer, ''), coalesce(k.oidc_client_id, ''),
	coalesce(k.oidc_subject_expression, ''), coalesce(k.oidc_claim_mapping, '[]'::jsonb),
	k.teams, k.is_machine, k.creator, k.expires_at, k.disabled_at,
	coalesce(k.disabled_reason, ''), k.last_used_at, k.created_at, k.deleted_at`

// mapping is a claim mapping entry as oidc_claim_mapping stores it.
type mapping struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// APIKey reads the key id with its account's organization, whatever its
// account, deleted or not.
func (s *Store) APIKey(ctx context.Context, id kernel.ULID) (apikey.Key, error) {
	k, err := scanKey(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+keyColumns+`
		FROM api_keys k JOIN accounts a ON a.id = k.account_id WHERE k.id = $1`, id))
	return k, read(err, "API key", id)
}

// SeatWindow is how long an API key holds a seat after its last use:
// Spacelift's rolling 30 days (report 14, section 4.4).
const SeatWindow = 30 * 24 * time.Hour

// seats counts decision 0021's seats in organization $1 and account $2 as
// the first use of key $4 in SeatWindow ($3) finds them: each active or
// pending member once, however many of the organization's accounts admit
// it, and each other live key that is not personal and was used within
// the window. A personal key rides on its owner's seat. Leaving $4 out lets
// two first uses of one key share its seat rather than the second count
// the first; a NULL $4 leaves no key out, as the seats read counts them.
const seats = `
WITH keys AS (
  SELECT k.account_id FROM api_keys k JOIN accounts a ON a.id = k.account_id
  WHERE a.organization_id = $1 AND k.id IS DISTINCT FROM $4 AND k.deleted_at IS NULL
    AND k.disabled_at IS NULL AND NOT k.is_personal
    AND (k.expires_at IS NULL OR k.expires_at > now())
    AND k.last_used_at > now() - $3::interval)
SELECT
  (SELECT count(DISTINCT u.member_id) FROM account_users u
     JOIN members m ON m.id = u.member_id
     WHERE u.organization_id = $1 AND m.status IN ('active', 'pending'))
    + (SELECT count(*) FROM keys),
  (SELECT count(*) FROM account_users u JOIN members m ON m.id = u.member_id
     WHERE u.account_id = $2 AND m.status IN ('active', 'pending'))
    + (SELECT count(*) FROM keys WHERE account_id = $2)`

// TouchAPIKey records a use of the key id: it sets last_used_at unless it
// is under a minute old, so a busy key writes its row once a minute, not
// once an exchange. A key that is not personal and was not used within
// SeatWindow takes a seat with this use: with the features domain held,
// the seats limit is checked in the touching transaction, whose lock on
// the organization's entitlement holds until the use commits, and a use
// beyond it is refused with a *featuresapi.LimitError.
func (s *Store) TouchAPIKey(ctx context.Context, id kernel.ULID) error {
	//nolint:wrapcheck // the check and the update name themselves
	return tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		if s.features != nil {
			if err := s.takeSeat(ctx, id); err != nil {
				return err
			}
		}
		if _, err := q.Exec(ctx, `UPDATE api_keys SET last_used_at = now()
			WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')`,
			id); err != nil {
			return fmt.Errorf("access: touch API key %s: %w", id, sql.Unavailable(err))
		}
		return nil
	})
}

// takeSeat checks the seats limit for the key id unless it already holds
// a seat or rides on its owner's.
func (s *Store) takeSeat(ctx context.Context, id kernel.ULID) error {
	var (
		org    kernel.OrganizationID
		acct   kernel.AccountID
		seated bool
	)
	err := tx.Q(ctx, s.db).QueryRow(ctx, `
		SELECT a.organization_id, k.account_id,
			k.is_personal OR coalesce(k.last_used_at > now() - $2::interval, false)
		FROM api_keys k JOIN accounts a ON a.id = k.account_id WHERE k.id = $1`,
		id, SeatWindow).Scan(&org, &acct, &seated)
	if err != nil {
		return read(err, "API key", id)
	}
	if seated {
		return nil
	}
	//nolint:wrapcheck // the features domain names the refusal
	return s.features.CheckLimit(ctx, org, acct, featuresapi.Seats,
		func(ctx context.Context) (featuresapi.Usage, error) {
			return s.countSeats(ctx, org, acct, id)
		})
}

// Seats counts the seats in use in org and in acct.
func (s *Store) Seats(
	ctx context.Context,
	org kernel.OrganizationID,
	acct kernel.AccountID,
) (featuresapi.Usage, error) {
	return s.countSeats(ctx, org, acct, kernel.ULID{})
}

// countSeats is the seats query leaving out the key except, none when zero.
func (s *Store) countSeats(
	ctx context.Context,
	org kernel.OrganizationID,
	acct kernel.AccountID,
	except kernel.ULID,
) (featuresapi.Usage, error) {
	var u featuresapi.Usage
	if err := tx.Q(ctx, s.db).QueryRow(ctx, seats, org, acct, SeatWindow, except).
		Scan(&u.Organization, &u.Account); err != nil {
		return u, fmt.Errorf("access: count seats: %w", sql.Unavailable(err))
	}
	return u, nil
}

// CreateAPIKey inserts k. A new key takes no seat until its first use.
func (s *Store) CreateAPIKey(ctx context.Context, k apikey.Key) error {
	var issuer, client, subject *string
	var claims []byte
	if k.Type == apikey.TypeOIDC {
		issuer, client, subject = &k.OIDC.Issuer, &k.OIDC.ClientID, &k.OIDC.SubjectExpression
		claims = mappings(k.OIDC.ClaimMapping)
	}
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `
		INSERT INTO api_keys (account_id, id, name, description, type, secret_hash,
			oidc_issuer, oidc_client_id, oidc_subject_expression, oidc_claim_mapping, teams,
			is_machine, creator, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		k.AccountID, k.ID, k.Name, k.Description, k.Type, k.SecretHash, issuer, client, subject,
		claims, nonNil(k.Teams), k.Machine, k.Creator, nullTime(k.ExpiresAt), k.CreatedAt,
	); err != nil {
		return fmt.Errorf("access: create API key %s: %w", k.ID, sql.Unavailable(err))
	}
	return nil
}

// AccountAPIKeys reads a page of the account's keys that are not deleted,
// in ID order.
func (s *Store) AccountAPIKeys(
	ctx context.Context,
	acct kernel.AccountID,
	after kernel.ULID,
	limit int,
) ([]apikey.Key, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+keyColumns+`
		FROM api_keys k JOIN accounts a ON a.id = k.account_id
		WHERE k.account_id = $1 AND k.deleted_at IS NULL AND ($2::uuid IS NULL OR k.id > $2)
		ORDER BY k.id LIMIT $3`, acct, after, limit)
	if err != nil {
		return nil, fmt.Errorf("access: list API keys: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (apikey.Key, error) {
		return scanKey(r)
	})
	if err != nil {
		return nil, fmt.Errorf("access: list API keys: %w", sql.Unavailable(err))
	}
	return out, nil
}

// AccountAPIKey reads the account's key id unless it is deleted.
func (s *Store) AccountAPIKey(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (apikey.Key, error) {
	k, err := scanKey(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+keyColumns+`
		FROM api_keys k JOIN accounts a ON a.id = k.account_id
		WHERE k.account_id = $1 AND k.id = $2 AND k.deleted_at IS NULL`, acct, id))
	return k, read(err, "API key", id)
}

// UpdateAPIKey applies c to the account's key id unless it is deleted.
func (s *Store) UpdateAPIKey(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	c apikey.Change,
) (apikey.Key, error) {
	var claims []byte
	if c.ClaimMapping != nil {
		claims = mappings(*c.ClaimMapping)
	}
	k, err := scanKey(tx.Q(ctx, s.db).QueryRow(ctx, `WITH k AS (UPDATE api_keys SET
			name = coalesce($3, name),
			description = coalesce($4, description),
			teams = coalesce($5, teams),
			oidc_claim_mapping = coalesce($6, oidc_claim_mapping)
		WHERE account_id = $1 AND id = $2 AND deleted_at IS NULL RETURNING *)
		SELECT `+keyColumns+` FROM k JOIN accounts a ON a.id = k.account_id`,
		acct, id, c.Name, c.Description, c.Teams, claims))
	if err := read(err, "API key", id); err != nil {
		return apikey.Key{}, fmt.Errorf("update API key %s: %w", id, err)
	}
	return k, nil
}

// ResetAPIKey replaces the account's secret key's hash and revokes the
// key's sessions.
func (s *Store) ResetAPIKey(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	hash []byte,
) (apikey.Key, []kernel.ULID, error) {
	return s.revokeKey(ctx, "reset", id, `secret_hash = $3`, acct, id, hash)
}

// DisableAPIKey disables the account's key and revokes its sessions. A
// disabled key keeps the time and reason it was first disabled with.
func (s *Store) DisableAPIKey(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	reason string,
) (apikey.Key, []kernel.ULID, error) {
	return s.revokeKey(ctx, "disable", id, `disabled_at = coalesce(disabled_at, now()),
		disabled_reason = coalesce(disabled_reason, $3)`, acct, id, reason)
}

// DeleteAPIKey marks the account's key deleted and revokes its sessions.
func (s *Store) DeleteAPIKey(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (apikey.Key, []kernel.ULID, error) {
	return s.revokeKey(ctx, "delete", id, `deleted_at = now()`, acct, id)
}

// SetNonAdminManagers records the account's setting.
func (s *Store) SetNonAdminManagers(ctx context.Context, acct kernel.AccountID, enable bool) error {
	tag, err := tx.Q(ctx, s.db).Exec(ctx,
		`UPDATE accounts SET api_key_non_admin_managers = $2 WHERE id = $1`, acct, enable)
	if err != nil {
		return fmt.Errorf("access: set API key managers: %w", sql.Unavailable(err))
	}
	if tag.RowsAffected() == 0 {
		return read(pgx.ErrNoRows, "account", acct.ULID)
	}
	return nil
}

// revokeKey applies set, with args, to the account's key unless it is
// deleted, and revokes every live session of the key in the same
// transaction, so no token of the old key outlives the change's commit.
func (s *Store) revokeKey(
	ctx context.Context,
	what string,
	id kernel.ULID,
	set string,
	args ...any,
) (apikey.Key, []kernel.ULID, error) {
	var (
		k   apikey.Key
		ids []kernel.ULID
	)
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		var err error
		k, err = scanKey(q.QueryRow(ctx, `WITH k AS (UPDATE api_keys SET `+set+`
			WHERE account_id = $1 AND id = $2 AND deleted_at IS NULL RETURNING *)
			SELECT `+keyColumns+` FROM k JOIN accounts a ON a.id = k.account_id`, args...))
		if err != nil {
			return read(err, "API key", id)
		}
		revoked, err := revoke(ctx, q, `actor_kind = 'api_key' AND actor_id = $1`, id)
		for _, ss := range revoked {
			ids = append(ids, ss.ID)
		}
		return err
	})
	if err != nil {
		return apikey.Key{}, nil, fmt.Errorf("%s API key %s: %w", what, id, sql.Unavailable(err))
	}
	return k, ids, nil
}

// scanKey reads keyColumns.
func scanKey(r pgx.Row) (apikey.Key, error) {
	var (
		k                           apikey.Key
		claims                      []byte
		expires, off, used, deleted *time.Time
	)
	err := r.Scan(&k.OrganizationID, &k.AccountID, &k.ID, &k.Name, &k.Description, &k.Type,
		&k.SecretHash, &k.OIDC.Issuer, &k.OIDC.ClientID, &k.OIDC.SubjectExpression, &claims,
		&k.Teams, &k.Machine, &k.Creator, &expires, &off, &k.DisabledReason, &used,
		&k.CreatedAt, &deleted)
	if err != nil {
		return apikey.Key{}, err //nolint:wrapcheck // the callers name the read
	}
	var m []mapping
	if err := json.Unmarshal(claims, &m); err != nil {
		return apikey.Key{}, fmt.Errorf("access: API key %s's claim mapping: %w", k.ID, err)
	}
	k.OIDC.ClaimMapping = make([]apikey.Mapping, 0, len(m))
	for _, e := range m {
		k.OIDC.ClaimMapping = append(k.OIDC.ClaimMapping, apikey.Mapping(e))
	}
	k.ExpiresAt, k.DisabledAt, k.LastUsedAt, k.DeletedAt = deref(expires), deref(off),
		deref(used), deref(deleted)
	k.CreatedAt = k.CreatedAt.UTC()
	return k, nil
}

// mappings is m as oidc_claim_mapping stores it.
func mappings(m []apikey.Mapping) []byte {
	out := make([]mapping, 0, len(m))
	for _, e := range m {
		out = append(out, mapping(e))
	}
	b, _ := json.Marshal(out) // two strings per entry, so never an error
	return b
}

// nullTime is t, or NULL for the zero time.
func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
