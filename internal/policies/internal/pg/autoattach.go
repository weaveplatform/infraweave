package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
)

// LockAutoattach serializes account policy writes before any policy row lock.
// Reconciliation and explicit promotion must agree on one effective relation.
func (s *Store) LockAutoattach(ctx context.Context, acct kernel.AccountID) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('policy_attachments'),hashtext($1::text))`, acct)
	if err != nil {
		return fmt.Errorf("policies: lock automatic attachments: %w", sql.Unavailable(err))
	}
	return nil
}

// AutoAttach never replaces an existing explicit relation or its identity.
func (s *Store) AutoAttach(ctx context.Context, a core.Attachment) (bool, error) {
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO policy_stack_attachments
 (account_id,id,policy_id,stack_id,is_autoattached,created_at) VALUES ($1,$2,$3,$4,true,$5)
 ON CONFLICT (account_id,policy_id,stack_id) DO NOTHING`, a.Account, a.ID, a.Policy, a.Stack, a.CreatedAt)
	if err != nil {
		return false, fmt.Errorf("policies: attach automatically: %w", sql.Unavailable(err))
	}
	return tag.RowsAffected() == 1, nil
}

// Labelled selects stack policy candidates inside the supplied account and reach.
func (s *Store) Labelled(ctx context.Context, acct kernel.AccountID, spaces []kernel.ULID, labels []string) ([]core.Policy, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, "SELECT "+columns+` FROM policies
 WHERE account_id=$1 AND space_id=ANY($2) AND labels && $3::text[]
 AND type IN ('APPROVAL','GIT_PUSH','INITIALIZATION','PLAN','TASK','TRIGGER') ORDER BY id`, acct, spaces, labels)
	if err != nil {
		return nil, fmt.Errorf("policies: find automatic policies: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (core.Policy, error) { return scan(row) })
	if err != nil {
		return nil, fmt.Errorf("policies: collect automatic policies: %w", sql.Unavailable(err))
	}
	return out, nil
}

// AutomaticPolicies includes obsolete automatic relations as well as new labels,
// so reconciliation also removes relations whose source no longer matches.
func (s *Store) AutomaticPolicies(ctx context.Context, acct kernel.AccountID) ([]core.Policy, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, "SELECT "+columns+` FROM policies p WHERE account_id=$1 AND
 (EXISTS(SELECT 1 FROM unnest(p.labels) l WHERE l LIKE 'autoattach:%') OR
 EXISTS(SELECT 1 FROM policy_stack_attachments a WHERE a.account_id=p.account_id AND a.policy_id=p.id AND a.is_autoattached))
 ORDER BY id`, acct)
	if err != nil {
		return nil, fmt.Errorf("policies: read automatic policies: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (core.Policy, error) { return scan(row) })
	if err != nil {
		return nil, fmt.Errorf("policies: collect automatic policies: %w", sql.Unavailable(err))
	}
	return out, nil
}

// AccountAttachments is the owned relation inventory for structural guards.
func (s *Store) AccountAttachments(ctx context.Context, acct kernel.AccountID) ([]core.Attachment, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, "SELECT "+attachmentColumns+" FROM policy_stack_attachments WHERE account_id=$1 ORDER BY policy_id,stack_id,id", acct)
	if err != nil {
		return nil, fmt.Errorf("policies: read account attachments: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (core.Attachment, error) { return scanAttachment(row) })
	if err != nil {
		return nil, fmt.Errorf("policies: collect account attachments: %w", sql.Unavailable(err))
	}
	return out, nil
}

// DeleteStack joins the stack deletion transaction; no relation outlives it.
func (s *Store) DeleteStack(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error {
	if _, err := tx.Q(ctx, s.db).Exec(ctx, "DELETE FROM policy_stack_attachments WHERE account_id=$1 AND stack_id=$2", acct, id); err != nil {
		return fmt.Errorf("policies: remove deleted stack attachments: %w", sql.Unavailable(err))
	}
	return nil
}
