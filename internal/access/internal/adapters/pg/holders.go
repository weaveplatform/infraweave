package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/holder"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	sqlsearch "github.com/weaveplatform/infraweave/internal/platform/sql/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var _ holder.Store = (*Store)(nil)

// liveSession is the condition of a session that still authenticates.
const liveSession = `revoked_at IS NULL AND expires_at > now()
	AND (idle_expires_at IS NULL OR idle_expires_at > now())`

// candidateQuery is each list's candidates of the account $1, as the ID
// the list names, the principal's kind, actor and member, its teams and
// a session's start.
var candidateQuery = map[holder.List]string{
	holder.Users: `SELECT member_id, 'user', member_id, member_id, latest_groups, created_at
		FROM account_users WHERE account_id = $1`,
	holder.Keys: `SELECT id, 'api_key', id, NULL::uuid, teams, created_at
		FROM api_keys WHERE account_id = $1 AND deleted_at IS NULL AND NOT is_personal`,
	holder.UserSessions: `SELECT member_id, actor_kind, actor_id, member_id, teams, created_at
		FROM sessions WHERE account_id = $1 AND actor_kind = 'user' AND ` + liveSession,
	holder.KeySessions: `SELECT actor_id, actor_kind, actor_id, member_id, teams, created_at
		FROM sessions WHERE account_id = $1 AND actor_kind = 'api_key' AND ` + liveSession,
}

// Candidates reads list's candidates of acct.
func (s *Store) Candidates(
	ctx context.Context,
	acct kernel.AccountID,
	list holder.List,
) ([]holder.Candidate, error) {
	query, ok := candidateQuery[list]
	if !ok {
		return []holder.Candidate{}, nil
	}
	rows, err := tx.Q(ctx, s.db).Query(ctx, query, acct)
	if err != nil {
		return nil, fmt.Errorf("access: %s candidates: %w", list, sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (holder.Candidate, error) {
		var c holder.Candidate
		err := r.Scan(&c.ID, &c.Kind, &c.Actor, &c.Member, &c.Teams, &c.At)
		return c, err //nolint:wrapcheck // named below
	})
	if err != nil {
		return nil, fmt.Errorf("access: %s candidates: %w", list, sql.Unavailable(err))
	}
	return out, nil
}

// A holder list's rows have no space: Space names the row's ID, so the
// scope's Reachable, which every compiled query ANDs, holds a page to the
// holders the evaluator found.
var (
	memberFrom = `account_users au JOIN members m
		ON m.organization_id = au.organization_id AND m.id = au.member_id`
	memberSelect = `au.member_id, m.login, m.name, m.email, ''`
	keySelect    = `k.id, '', k.name, '', k.description`
)

// holderSearch is each list's rows as holder.Fields search them, and the
// condition holding them to the account $1.
var holderSearch = map[holder.List]struct {
	entity sqlsearch.Entity
	where  string
}{
	holder.Users: {holderEntity(holder.Users, sqlsearch.Entity{
		From: memberFrom, Select: memberSelect, Space: "au.member_id", ID: "au.member_id::text",
	}, map[string]sqlsearch.Column{
		"username": {Expr: "m.login", Cast: "text", Text: true},
	}), "au.account_id = $1"},
	holder.UserSessions: {holderEntity(holder.UserSessions, sqlsearch.Entity{
		From: memberFrom, Select: memberSelect, Space: "au.member_id", ID: "au.member_id::text",
	}, map[string]sqlsearch.Column{
		"login": {Expr: "m.login", Cast: "text", Text: true},
		"name":  {Expr: "m.name", Text: true},
	}), "au.account_id = $1"},
	holder.Groups: {holderEntity(holder.Groups, sqlsearch.Entity{
		From: "managed_user_groups g", Select: `g.id, '', g.name, '', g.description`,
		Space: "g.id", ID: "g.id::text",
	}, map[string]sqlsearch.Column{
		"groupName": {Expr: "g.name", Cast: "text", Text: true},
	}), "g.account_id = $1"},
	holder.Keys: {holderEntity(holder.Keys, sqlsearch.Entity{
		From: "api_keys k", Select: keySelect, Space: "k.id", ID: "k.id::text",
	}, map[string]sqlsearch.Column{
		"name": {Expr: "k.name", Cast: "text", Text: true},
	}), "k.account_id = $1"},
	holder.KeySessions: {holderEntity(holder.KeySessions, sqlsearch.Entity{
		From: "api_keys k", Select: keySelect, Space: "k.id", ID: "k.id::text",
	}, map[string]sqlsearch.Column{
		"name": {Expr: "k.name", Cast: "text", Text: true},
	}), "k.account_id = $1"},
}

// holderEntity is e, listing list's rows, with a column of SQL for each of
// holder.Fields[list], ordered by the first.
func holderEntity(
	list holder.List,
	e sqlsearch.Entity,
	sqlOf map[string]sqlsearch.Column,
) sqlsearch.Entity {
	e.List = "holders:" + string(list)
	for _, f := range holder.Fields[list] {
		c := sqlOf[f.Name]
		c.Field = f
		e.Columns = append(e.Columns, c)
	}
	e.Order = []search.Order{{Field: holder.Fields[list][0].Name}}
	return e
}

func scanHolder(row pgx.Row, extra ...any) (holder.Row, error) {
	var r holder.Row
	err := row.Scan(append([]any{&r.ID, &r.Login, &r.Name, &r.Email, &r.Description}, extra...)...)
	return r, err //nolint:wrapcheck // sqlsearch names the read
}

// SearchHolders reads a page of list's rows of acct among ids that in
// keeps.
func (s *Store) SearchHolders(
	ctx context.Context,
	acct kernel.AccountID,
	list holder.List,
	in search.Input,
	ids []kernel.ULID,
) (search.Result[holder.Row], error) {
	h := holderSearch[list]
	out, err := sqlsearch.Find(ctx, tx.Q(ctx, s.db), h.entity, sqlsearch.Scope{
		Where: h.where, Args: []any{acct}, Reachable: ids,
	}, in, scanHolder)
	if err != nil {
		return out, fmt.Errorf("access: %w", err)
	}
	return out, nil
}

// SuggestHolders lists the values list's rows of acct among ids that in
// keeps hold.
func (s *Store) SuggestHolders(
	ctx context.Context,
	acct kernel.AccountID,
	list holder.List,
	in search.SuggestionsInput,
	ids []kernel.ULID,
) (search.Suggestions, error) {
	h := holderSearch[list]
	out, err := sqlsearch.Suggest(ctx, tx.Q(ctx, s.db), h.entity, sqlsearch.Scope{
		Where: h.where, Args: []any{acct}, Reachable: ids,
	}, in)
	if err != nil {
		return out, fmt.Errorf("access: %w", err)
	}
	return out, nil
}

// HolderGroups reads acct's groups among ids as rows.
func (s *Store) HolderGroups(
	ctx context.Context,
	acct kernel.AccountID,
	ids []kernel.ULID,
) ([]holder.Row, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT g.id, '', g.name, '', g.description
		FROM managed_user_groups g WHERE g.account_id = $1 AND g.id = ANY($2::uuid[]) ORDER BY g.id`,
		acct, ids)
	if err != nil {
		return nil, fmt.Errorf("access: read groups: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (holder.Row, error) {
		return scanHolder(r)
	})
	if err != nil {
		return nil, fmt.Errorf("access: read groups: %w", sql.Unavailable(err))
	}
	return out, nil
}
