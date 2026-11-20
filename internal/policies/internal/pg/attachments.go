package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
)

const attachmentColumns = `account_id,id,policy_id,stack_id,is_autoattached,created_at`

func scanAttachment(row pgx.Row) (core.Attachment, error) {
	var a core.Attachment
	err := row.Scan(&a.Account, &a.ID, &a.Policy, &a.Stack, &a.Autoattached, &a.CreatedAt)
	return a, err //nolint:wrapcheck // callers name the operation
}

// Attach creates an effective relation or makes the existing one explicit.
func (s *Store) Attach(ctx context.Context, a core.Attachment) (core.Attachment, error) {
	out, err := scanAttachment(tx.Q(ctx, s.db).QueryRow(ctx, `INSERT INTO policy_stack_attachments
 (account_id,id,policy_id,stack_id,is_autoattached,created_at) VALUES ($1,$2,$3,$4,false,$5)
 ON CONFLICT (account_id,policy_id,stack_id) DO UPDATE SET is_autoattached=false RETURNING `+attachmentColumns, a.Account, a.ID, a.Policy, a.Stack, a.CreatedAt))
	if err != nil {
		return core.Attachment{}, fmt.Errorf("policies: attach stack: %w", sql.Unavailable(err))
	}
	return out, nil
}

// Attachment reads one account's immutable attachment identity.
func (s *Store) Attachment(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (core.Attachment, error) {
	out, err := scanAttachment(tx.Q(ctx, s.db).QueryRow(ctx, "SELECT "+attachmentColumns+" FROM policy_stack_attachments WHERE account_id=$1 AND id=$2", acct, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Attachment{}, core.ErrNotFound
	}
	if err != nil {
		return core.Attachment{}, fmt.Errorf("policies: read attachment: %w", sql.Unavailable(err))
	}
	return out, nil
}

// Detach removes only the account-qualified attachment.
func (s *Store) Detach(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error {
	if _, err := tx.Q(ctx, s.db).Exec(ctx, "DELETE FROM policy_stack_attachments WHERE account_id=$1 AND id=$2", acct, id); err != nil {
		return fmt.Errorf("policies: detach stack: %w", sql.Unavailable(err))
	}
	return nil
}

// Attachments reads relations in one batch, keyed by policy or stack.
func (s *Store) Attachments(ctx context.Context, acct kernel.AccountID, ids []kernel.ULID, byStack bool) (map[kernel.ULID][]core.Attachment, error) {
	by := "policy_id"
	if byStack {
		by = "stack_id"
	}
	rows, err := tx.Q(ctx, s.db).
		Query(ctx, "SELECT "+attachmentColumns+" FROM policy_stack_attachments WHERE account_id=$1 AND "+by+"=ANY($2) ORDER BY policy_id,stack_id,id", acct, ids)
	if err != nil {
		return nil, fmt.Errorf("policies: read attachments: %w", sql.Unavailable(err))
	}
	all, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (core.Attachment, error) { return scanAttachment(row) })
	if err != nil {
		return nil, fmt.Errorf("policies: collect attachments: %w", sql.Unavailable(err))
	}
	out := map[kernel.ULID][]core.Attachment{}
	for _, a := range all {
		key := a.Policy
		if byStack {
			key = a.Stack
		}
		out[key] = append(out[key], a)
	}
	return out, nil
}

// ByIDs batches policy projection reads without crossing the account boundary.
func (s *Store) ByIDs(ctx context.Context, acct kernel.AccountID, ids []kernel.ULID) (map[kernel.ULID]core.Policy, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, "SELECT "+columns+" FROM policies WHERE account_id=$1 AND id=ANY($2)", acct, ids)
	if err != nil {
		return nil, fmt.Errorf("policies: read selected policies: %w", sql.Unavailable(err))
	}
	all, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (core.Policy, error) { return scan(row) })
	if err != nil {
		return nil, fmt.Errorf("policies: collect selected policies: %w", sql.Unavailable(err))
	}
	out := make(map[kernel.ULID]core.Policy, len(all))
	for _, p := range all {
		out[p.ID] = p
	}
	return out, nil
}
