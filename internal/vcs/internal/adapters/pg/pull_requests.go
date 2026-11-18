package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/pullrequest"
)

// Upsert joins the inbox fence and holds the integration, fencing account purge.
// An older snapshot contributes history without reverting current metadata.
func (s *Store) Upsert(ctx context.Context, r pullrequest.Request) error {
	if !tx.In(ctx) {
		return pullrequest.ErrNoTransaction
	}
	validHead := port.ValidCommit(r.HeadSHA)
	if !validHead {
		if r.State == pullrequest.Open {
			return pullrequest.ErrHead
		}
		// A terminal notification can close an existing request after its source
		// branch disappeared. It contributes no head and preserves the old one.
		r.HeadSHA = ""
	}
	var integration kernel.ULID
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT id FROM vcs_integrations WHERE account_id=$1 AND id=$2 FOR KEY SHARE`, r.Account, r.Integration).Scan(&integration)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("vcs: hold request integration: %w", sql.Unavailable(err))
	}
	var id kernel.ULID
	err = tx.Q(ctx, s.db).QueryRow(ctx, `INSERT INTO pull_requests
		(account_id,id,integration_id,repository_id,namespace,repository,provider_id,base_branch,head_branch,head_sha,author_login,title,url,head_branch_url,draft,state,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		ON CONFLICT(integration_id,repository_id,provider_id) DO UPDATE SET
		namespace=excluded.namespace,repository=excluded.repository,base_branch=excluded.base_branch,
		head_branch=excluded.head_branch,head_sha=CASE WHEN excluded.head_sha='' THEN pull_requests.head_sha ELSE excluded.head_sha END,
		author_login=CASE WHEN excluded.author_login='' THEN pull_requests.author_login ELSE excluded.author_login END,
		title=excluded.title,url=excluded.url,head_branch_url=excluded.head_branch_url,draft=excluded.draft,
		state=excluded.state,updated_at=excluded.updated_at
		WHERE excluded.updated_at>=pull_requests.updated_at RETURNING id`, r.Account, r.ID, r.Integration, r.RepositoryID, r.Namespace, r.Repository, r.Number,
		r.BaseBranch, r.HeadBranch, r.HeadSHA, r.AuthorLogin, r.Title, r.URL, r.HeadBranchURL, r.Draft, r.State, r.CreatedAt, r.UpdatedAt).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.Q(ctx, s.db).
			QueryRow(ctx, `SELECT id FROM pull_requests WHERE account_id=$1 AND integration_id=$2 AND repository_id=$3 AND provider_id=$4`, r.Account, r.Integration, r.RepositoryID, r.Number).
			Scan(&id)
	}
	if err != nil {
		return fmt.Errorf("vcs: upsert pull request: %w", sql.Unavailable(err))
	}
	if !validHead {
		return nil
	}
	_, err = tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO pull_request_heads(account_id,request_id,sha,became_head_at) VALUES($1,$2,$3,$4)
		ON CONFLICT(account_id,request_id,sha) DO UPDATE SET became_head_at=least(pull_request_heads.became_head_at,excluded.became_head_at)`, r.Account, id, r.HeadSHA, r.UpdatedAt)
	if err != nil {
		return fmt.Errorf("vcs: record request head: %w", sql.Unavailable(err))
	}
	return nil
}

// Author reads only the immutable creator projection, without credentials.
func (s *Store) Author(ctx context.Context, acct kernel.AccountID, integration kernel.ULID, repository, number string) (string, error) {
	var author string
	err := tx.Q(ctx, s.db).
		QueryRow(ctx, `SELECT author_login FROM pull_requests WHERE account_id=$1 AND integration_id=$2 AND repository_id=$3 AND provider_id=$4`, acct, integration, repository, number).
		Scan(&author)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("vcs: read request author: %w", sql.Unavailable(err))
	}
	return author, nil
}

// Candidates pages only this repository's open requests against the tracked branch.
func (s *Store) Candidates(
	ctx context.Context,
	acct kernel.AccountID,
	integration kernel.ULID,
	namespace, repository, branch string,
	after *pullrequest.Cursor,
	limit int,
) ([]pullrequest.Request, error) {
	var at *time.Time
	var id *kernel.ULID
	if after != nil {
		at = &after.UpdatedAt
		id = &after.ID
	}
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT id,repository_id,provider_id,base_branch,head_branch,head_sha,author_login,title,url,head_branch_url,draft,created_at,updated_at
		FROM pull_requests WHERE account_id=$1 AND integration_id=$2 AND namespace=$3 AND repository=$4 AND base_branch=$5 AND state='open'
		AND ($6::timestamptz IS NULL OR (updated_at,id)<($6,$7::uuid)) ORDER BY updated_at DESC,id DESC LIMIT $8`, acct, integration, namespace, repository, branch, at, id, limit)
	if err != nil {
		return nil, fmt.Errorf("vcs: read request candidates: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (pullrequest.Request, error) {
		r := pullrequest.Request{Account: acct, Integration: integration, Namespace: namespace, Repository: repository, State: pullrequest.Open}
		err := row.Scan(
			&r.ID,
			&r.RepositoryID,
			&r.Number,
			&r.BaseBranch,
			&r.HeadBranch,
			&r.HeadSHA,
			&r.AuthorLogin,
			&r.Title,
			&r.URL,
			&r.HeadBranchURL,
			&r.Draft,
			&r.CreatedAt,
			&r.UpdatedAt,
		)
		return r, err //nolint:wrapcheck // CollectRows is wrapped at the query boundary below
	})
	if err != nil {
		return nil, fmt.Errorf("vcs: collect request candidates: %w", sql.Unavailable(err))
	}
	return out, nil
}

// Heads returns every recorded SHA; no run type or state belongs to this table.
func (s *Store) Heads(ctx context.Context, acct kernel.AccountID, ids []kernel.ULID) (map[kernel.ULID][]string, error) {
	out := map[kernel.ULID][]string{}
	if len(ids) == 0 {
		return out, nil
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = id.UUID()
	}
	rows, err := tx.Q(ctx, s.db).
		Query(ctx, `SELECT request_id,sha FROM pull_request_heads WHERE account_id=$1 AND request_id=ANY($2::uuid[]) ORDER BY request_id,became_head_at,sha`, acct, keys)
	if err != nil {
		return nil, fmt.Errorf("vcs: read request heads: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	for rows.Next() {
		var id kernel.ULID
		var sha string
		if err := rows.Scan(&id, &sha); err != nil {
			return nil, fmt.Errorf("vcs: scan request head: %w", err)
		}
		out[id] = append(out[id], sha)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vcs: collect request heads: %w", sql.Unavailable(err))
	}
	return out, nil
}

// Prune removes bounded closed/merged history, or all rows of a purged account.
func (s *Store) Prune(ctx context.Context, acct *kernel.AccountID, before *time.Time, batch int) (int, error) {
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM pull_requests WHERE ctid IN (SELECT ctid FROM pull_requests
		WHERE ($1::uuid IS NULL OR account_id=$1) AND ($2::timestamptz IS NULL OR (state IN ('closed','merged') AND updated_at<$2))
		ORDER BY updated_at,id LIMIT $3)`, acct, before, batch)
	if err != nil {
		return 0, fmt.Errorf("vcs: prune pull requests: %w", sql.Unavailable(err))
	}
	return int(tag.RowsAffected()), nil
}

var _ pullrequest.Store = (*Store)(nil)
