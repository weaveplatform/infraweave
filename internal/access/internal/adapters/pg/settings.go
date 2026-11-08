package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/settings"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/audit"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var _ settings.Store = (*Store)(nil)

// Settings reads the account's settings. An account with no
// account_settings row reads each column's default, which Defaults
// mirrors; LastAccount counts the organization's accounts.
func (s *Store) Settings(ctx context.Context, acct kernel.AccountID) (settings.Settings, error) {
	var (
		st                settings.Settings
		marked            *time.Time
		reason, described *string
	)
	err := tx.Q(ctx, s.db).QueryRow(ctx, `
		SELECT coalesce(st.personal_api_keys, true),
			coalesce(st.api_key_non_human_managers, false), a.api_key_non_admin_managers,
			coalesce(st.security_email, ''), coalesce(st.oauth_redirect_hosts, '{}'),
			coalesce(st.survey_completed, false), st.survey_response,
			st.marked_at, st.mark_reason, st.mark_description,
			(SELECT count(*) FROM accounts o WHERE o.organization_id = a.organization_id) = 1
		FROM accounts a LEFT JOIN account_settings st ON st.account_id = a.id
		WHERE a.id = $1`, acct).
		Scan(&st.PersonalAPIKeys, &st.NonHumanKeyManagers, &st.NonAdminKeyManagers,
			&st.SecurityEmail, &st.RedirectHosts, &st.Survey.Completed, &st.Survey.Response,
			&marked, &reason, &described, &st.LastAccount)
	if marked != nil {
		st.Mark = &settings.Mark{At: *marked, Reason: reason, Description: described}
	}
	return st, read(err, "account", acct.ULID)
}

// SetPersonalAPIKeys records enabled and, with disable, disables the
// account's personal keys for ACCOUNT and revokes their sessions in the
// same transaction.
func (s *Store) SetPersonalAPIKeys(
	ctx context.Context,
	acct kernel.AccountID,
	enabled, disable bool,
) ([]kernel.ULID, error) {
	var ids []kernel.ULID
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		// A personal key's creation holds a share lock on the account, so
		// it either commits before the setting changes, and is disabled
		// below, or reads the new setting.
		if _, err := tx.Q(ctx, s.db).Exec(ctx,
			`SELECT 1 FROM accounts WHERE id = $1 FOR NO KEY UPDATE`, acct); err != nil {
			return fmt.Errorf("access: lock account: %w", sql.Unavailable(err))
		}
		if err := s.upsert(ctx, acct, "personal_api_keys", enabled); err != nil {
			return err
		}
		if !disable {
			return nil
		}
		q := tx.Q(ctx, s.db)
		rows, err := q.Query(ctx, `UPDATE api_keys SET disabled_at = coalesce(disabled_at, now()),
				disabled_reason = coalesce(disabled_reason, 'ACCOUNT')
			WHERE account_id = $1 AND is_personal AND deleted_at IS NULL RETURNING id`, acct)
		if err != nil {
			return fmt.Errorf("access: disable personal keys: %w", sql.Unavailable(err))
		}
		keys, err := pgx.CollectRows(rows, pgx.RowTo[kernel.ULID])
		if err != nil {
			return fmt.Errorf("access: disable personal keys: %w", sql.Unavailable(err))
		}
		revoked, err := revoke(ctx, q, `actor_kind = 'api_key' AND actor_id = ANY($1)`, keys)
		for _, ss := range revoked {
			ids = append(ids, ss.ID)
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("set personal API keys of account %s: %w", acct, err)
	}
	return ids, nil
}

// NonHumanKeyManagers reads whether the account lets API keys manage API
// keys; an account with no settings row does not.
func (s *Store) NonHumanKeyManagers(ctx context.Context, acct kernel.AccountID) (bool, error) {
	var on bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT coalesce((SELECT api_key_non_human_managers
		FROM account_settings WHERE account_id = $1), false)`, acct).Scan(&on)
	if err != nil {
		return false, fmt.Errorf("access: read API key managers of account %s: %w", acct,
			sql.Unavailable(err))
	}
	return on, nil
}

// NonAdminKeyManagers reads whether the account lets the admins of the
// spaces below its top space manage API keys.
func (s *Store) NonAdminKeyManagers(ctx context.Context, acct kernel.AccountID) (bool, error) {
	var on bool
	err := tx.Q(ctx, s.db).QueryRow(ctx,
		`SELECT api_key_non_admin_managers FROM accounts WHERE id = $1`, acct).Scan(&on)
	if err != nil {
		return false, read(err, "account", acct.ULID)
	}
	return on, nil
}

// ToggleNonHumanKeyManagers flips the setting in one statement, so two
// flips at once are two flips.
func (s *Store) ToggleNonHumanKeyManagers(
	ctx context.Context,
	acct kernel.AccountID,
) (bool, error) {
	var on bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, `
		INSERT INTO account_settings (account_id, api_key_non_human_managers) VALUES ($1, true)
		ON CONFLICT (account_id) DO UPDATE
			SET api_key_non_human_managers = NOT account_settings.api_key_non_human_managers
		RETURNING api_key_non_human_managers`, acct).Scan(&on)
	if err != nil {
		return false, written(err, "API key managers", acct)
	}
	return on, nil
}

// SetSecurityEmail records the account's security contact.
func (s *Store) SetSecurityEmail(ctx context.Context, acct kernel.AccountID, email string) error {
	return s.upsert(ctx, acct, "security_email", email)
}

// SetRedirectHosts replaces the account's OAuth redirect allowlist.
func (s *Store) SetRedirectHosts(ctx context.Context, acct kernel.AccountID, hosts []string) error {
	return s.upsert(ctx, acct, "oauth_redirect_hosts", hosts)
}

// SetSurvey records the account's survey answer.
func (s *Store) SetSurvey(ctx context.Context, acct kernel.AccountID, sv settings.Survey) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `
		INSERT INTO account_settings (account_id, survey_completed, survey_response)
		VALUES ($1, $2, $3)
		ON CONFLICT (account_id) DO UPDATE
			SET survey_completed = EXCLUDED.survey_completed,
				survey_response = EXCLUDED.survey_response`, acct, sv.Completed, sv.Response)
	return written(err, "survey", acct)
}

// ToggleDeletionMark marks the account with m, or clears its mark, in one
// statement.
func (s *Store) ToggleDeletionMark(
	ctx context.Context,
	acct kernel.AccountID,
	m settings.Mark,
) (bool, error) {
	var marked bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, `
		INSERT INTO account_settings (account_id, marked_at, mark_reason, mark_description)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (account_id) DO UPDATE SET
			marked_at = CASE WHEN account_settings.marked_at IS NULL THEN EXCLUDED.marked_at END,
			mark_reason = CASE WHEN account_settings.marked_at IS NULL
				THEN EXCLUDED.mark_reason END,
			mark_description = CASE WHEN account_settings.marked_at IS NULL
				THEN EXCLUDED.mark_description END
		RETURNING marked_at IS NOT NULL`, acct, m.At, m.Reason, m.Description).Scan(&marked)
	if err != nil {
		return false, written(err, "deletion mark", acct)
	}
	return marked, nil
}

// RequestPersonalAPIKeys records e on the account's trail.
func (s *Store) RequestPersonalAPIKeys(ctx context.Context, e kernel.AuditEntry) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		_, err := audit.Record(ctx, e)
		return err //nolint:wrapcheck // names itself
	})
	if err != nil {
		return fmt.Errorf("request personal API keys: %w", err)
	}
	return nil
}

// deleteAccount deletes an account's rows, children first. A CLI session
// outlives the browser session it came from, so a session of another
// account may name one of these as its parent.
var deleteAccount = []string{
	`UPDATE sessions SET parent_session_id = NULL
		WHERE parent_session_id IN (SELECT id FROM sessions WHERE account_id = $1)`,
	`DELETE FROM sessions WHERE account_id = $1`,
	`DELETE FROM api_keys WHERE account_id = $1`,
	`DELETE FROM account_invitations WHERE account_id = $1`,
	`DELETE FROM managed_user_groups WHERE account_id = $1`,
	`DELETE FROM account_users WHERE account_id = $1`,
	`DELETE FROM account_settings WHERE account_id = $1`,
	`DELETE FROM role_bindings WHERE account_id = $1`,
	`DELETE FROM role_actions WHERE account_id = $1`,
	`DELETE FROM roles WHERE account_id = $1`,
	`DELETE FROM space_closure WHERE account_id = $1`,
	`DELETE FROM spaces WHERE account_id = $1`,
}

// DeleteAccount deletes the account once it is marked for deletion, under
// its organization's row lock, so it counts the organization's accounts
// with every other deletion done.
func (s *Store) DeleteAccount(
	ctx context.Context,
	acct kernel.AccountID,
	e kernel.OrganizationAuditEntry,
) ([]kernel.ULID, error) {
	return s.removeAccount(ctx, kernel.OrganizationID{}, acct, true, settings.ErrInvalidState, e)
}

// removeAccount deletes the account acct, of org unless org is zero,
// refusing with invalid an account not marked when marked is true and an
// organization's last account. It returns the sessions it revoked.
func (s *Store) removeAccount(
	ctx context.Context,
	org kernel.OrganizationID,
	acct kernel.AccountID,
	marked bool,
	invalid error,
	e kernel.OrganizationAuditEntry,
) ([]kernel.ULID, error) {
	var ids []kernel.ULID
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		var held kernel.OrganizationID
		err := q.QueryRow(ctx, `SELECT o.id FROM organizations o
			JOIN accounts a ON a.organization_id = o.id
			WHERE a.id = $1 AND ($2::uuid IS NULL OR o.id = $2) FOR UPDATE OF o`, acct, org).
			Scan(&held)
		if err != nil {
			return read(err, "account", acct.ULID)
		}
		// A statement that waited for the lock still reads the snapshot it
		// started with, which holds an account another deletion has since
		// removed, so the state is read by the next statement, after it.
		var exists, isMarked, last bool
		err = q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE id = $1),
				EXISTS (SELECT 1 FROM account_settings
					WHERE account_id = $1 AND marked_at IS NOT NULL),
				(SELECT count(*) FROM accounts WHERE organization_id = $2) = 1`, acct, held).
			Scan(&exists, &isMarked, &last)
		switch {
		case err != nil:
			return read(err, "account", acct.ULID)
		case !exists:
			return fmt.Errorf("%w: account %s", port.ErrNotFound, acct)
		case marked && !isMarked:
			return fmt.Errorf("%w: the account is not marked for deletion", invalid)
		case last:
			return fmt.Errorf("%w: the account is its organization's last", invalid)
		}
		revoked, err := revoke(ctx, q, `account_id = $1`, acct)
		if err != nil {
			return err
		}
		for _, ss := range revoked {
			ids = append(ids, ss.ID)
		}
		for _, stmt := range deleteAccount {
			if _, err := q.Exec(ctx, stmt, acct); err != nil {
				return fmt.Errorf("access: %w", sql.Unavailable(err))
			}
		}
		if s.features != nil {
			if err := s.features.DeleteAccount(ctx, acct); err != nil {
				return fmt.Errorf("access: %w", err)
			}
		}
		if _, err := q.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, acct); err != nil {
			return fmt.Errorf("access: %w", sql.Unavailable(err))
		}
		_, err = audit.RecordOrganization(ctx, e)
		return err //nolint:wrapcheck // names itself
	})
	if err != nil {
		return nil, fmt.Errorf("delete account %s: %w", acct, err)
	}
	return ids, nil
}

// upsert sets one column of the account's settings row, creating the row
// with every other column's default.
func (s *Store) upsert(ctx context.Context, acct kernel.AccountID, column string, v any) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO account_settings (account_id, `+column+`)
		VALUES ($1, $2) ON CONFLICT (account_id) DO UPDATE SET `+column+` = EXCLUDED.`+column,
		acct, v)
	return written(err, column, acct)
}

// written turns a write's foreign key violation, an account that does not
// exist, into port.ErrNotFound, and marks an unreachable database.
func written(err error, what string, acct kernel.AccountID) error {
	switch pe, ok := errors.AsType[*pgconn.PgError](err); {
	case err == nil:
		return nil
	case ok && pe.Code == "23503":
		return fmt.Errorf("%w: account %s", port.ErrNotFound, acct)
	}
	return fmt.Errorf("access: set %s of account %s: %w", what, acct, sql.Unavailable(err))
}
