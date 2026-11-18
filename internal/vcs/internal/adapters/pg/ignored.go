package pg

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	sqlsearch "github.com/weaveplatform/infraweave/internal/platform/sql/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/ignored"
)

// RecordIgnored is idempotent across deliveries and administrative replays.
// The caller holds the stack row, fencing its same-transaction deletion guard.
func (s *Store) RecordIgnored(ctx context.Context, r ignored.Record) error {
	if !tx.In(ctx) {
		return ignored.ErrNoTransaction
	}
	_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO vcs_ignored_triggers(account_id,id,stack_id,commit_hash,commit,reason,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(account_id,stack_id,commit_hash,reason) DO NOTHING`, r.Account, r.ID, r.Stack, r.Commit.Hash, r.Commit, r.Reason, r.CreatedAt)
	if err != nil {
		return fmt.Errorf("vcs: insert ignored trigger: %w", sql.Unavailable(err))
	}
	return nil
}

var ignoredSearch = func() sqlsearch.Entity {
	e := sqlsearch.Entity{
		List:   "vcs-ignored-triggers",
		From:   "vcs_ignored_triggers i",
		Select: "i.id,i.commit,i.reason,i.created_at",
		Space:  "$3::uuid",
		ID:     "i.id::text",
		Order:  []search.Order{{Field: "createdAt", Desc: true}},
	}
	e.Columns = []sqlsearch.Column{{Field: ignored.Fields[0], Expr: "i.created_at", Cast: "timestamptz"}, {Field: ignored.Fields[1], Expr: "i.reason", Cast: "text"}}
	return e
}()

// SearchIgnored scopes rows to one live stack. Its space is supplied by the
// stacks port rather than copied into rows or read from a peer domain's table.
func (s *Store) SearchIgnored(ctx context.Context, scope ignored.Scope, in search.Input) (search.Result[ignored.Record], error) {
	e := ignoredSearch
	e.List += "/" + scope.Account.String() + "/" + scope.Stack.String()
	rows, err := sqlsearch.Find(
		ctx,
		tx.Q(ctx, s.db),
		e,
		sqlsearch.Scope{
			Where:     "i.account_id=$1 AND i.stack_id=$2 AND i.created_at >= $4",
			Args:      []any{scope.Account, scope.Stack, scope.Space, scope.Since},
			Reachable: []kernel.ULID{scope.Space},
		},
		in,
		func(row pgx.Row, extra ...any) (ignored.Record, error) {
			r := ignored.Record{Account: scope.Account, Stack: scope.Stack}
			args := append([]any{&r.ID, &r.Commit, &r.Reason, &r.CreatedAt}, extra...)
			err := row.Scan(args...)
			return r, err //nolint:wrapcheck // Find wraps the scan at its query boundary
		},
	)
	if err != nil {
		return rows, fmt.Errorf("vcs: search ignored triggers: %w", err)
	}
	return rows, nil
}

func (s *Store) PruneIgnored(ctx context.Context, account *kernel.AccountID, before *time.Time, batch int) (int, error) {
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM vcs_ignored_triggers WHERE ctid IN (SELECT ctid FROM vcs_ignored_triggers
		WHERE ($1::uuid IS NULL OR account_id=$1) AND ($2::timestamptz IS NULL OR created_at<$2) ORDER BY created_at,id LIMIT $3)`, account, before, batch)
	if err != nil {
		return 0, fmt.Errorf("vcs: prune ignored triggers: %w", sql.Unavailable(err))
	}
	return int(tag.RowsAffected()), nil
}

func (s *Store) DeleteIgnoredStack(ctx context.Context, account kernel.AccountID, stack kernel.ULID) error {
	if !tx.In(ctx) {
		return ignored.ErrNoTransaction
	}
	_, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM vcs_ignored_triggers WHERE account_id=$1 AND stack_id=$2`, account, stack)
	if err != nil {
		return fmt.Errorf("vcs: delete stack ignored triggers: %w", sql.Unavailable(err))
	}
	return nil
}
