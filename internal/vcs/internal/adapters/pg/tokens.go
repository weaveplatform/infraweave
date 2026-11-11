package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/source"
)

// Tokens keeps the tokens minted for runs in vcs_run_tokens, in the
// transaction the context carries when it carries one.
type Tokens struct{ db tx.Querier }

var _ source.Tokens = Tokens{}

// NewTokens returns the run tokens over db.
func NewTokens(db tx.Querier) Tokens { return Tokens{db: db} }

// Add keeps t; adding it again changes nothing.
func (s Tokens) Add(ctx context.Context, t source.Token) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO vcs_run_tokens (account_id, run_id,
			token_id, stack_id, integration_id, provider, namespace, repository, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) ON CONFLICT DO NOTHING`,
		t.Account, t.Run, t.ID, t.Stack, t.Integration, t.Provider, t.Namespace, t.Repository,
		t.ExpiresAt)
	if err != nil {
		return fmt.Errorf("vcs: keep run %s's token %s: %w", t.Run, t.ID, sql.Unavailable(err))
	}
	return nil
}

// List lists acct's run's tokens, oldest first.
func (s Tokens) List(
	ctx context.Context,
	acct kernel.AccountID,
	run kernel.ULID,
) ([]source.Token, error) {
	rows, _ := tx.Q(ctx, s.db).Query(ctx, `SELECT account_id, run_id, token_id, stack_id,
			integration_id, provider, namespace, repository, expires_at
		FROM vcs_run_tokens WHERE account_id = $1 AND run_id = $2
		ORDER BY created_at, token_id`, acct, run)
	ts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (source.Token, error) {
		var t source.Token
		err := r.Scan(&t.Account, &t.Run, &t.ID, &t.Stack, &t.Integration, &t.Provider,
			&t.Namespace, &t.Repository, &t.ExpiresAt)
		t.ExpiresAt = t.ExpiresAt.UTC()
		return t, err //nolint:wrapcheck // named below
	})
	if err != nil {
		return nil, fmt.Errorf("vcs: read run %s's tokens: %w", run, sql.Unavailable(err))
	}
	return ts, nil
}

// Remove forgets acct's run's token id; forgetting it again changes
// nothing.
func (s Tokens) Remove(
	ctx context.Context,
	acct kernel.AccountID,
	run kernel.ULID,
	id string,
) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM vcs_run_tokens
		WHERE account_id = $1 AND run_id = $2 AND token_id = $3`, acct, run, id)
	if err != nil {
		return fmt.Errorf("vcs: forget run %s's token %s: %w", run, id, sql.Unavailable(err))
	}
	return nil
}
