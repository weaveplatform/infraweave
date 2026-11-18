package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/feedback"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

const commentRequestColumns = `r.id,r.integration_id,` + requestRepositoryIDSQL +
	`,r.provider_id,coalesce(m.namespace,r.namespace),coalesce(m.name,r.repository),r.head_sha,r.state='open',i.provider,m.repository_id IS NOT NULL`

const commentRequestFrom = ` FROM pull_requests r JOIN vcs_integrations i ON (i.account_id,i.id)=(r.account_id,r.integration_id)
	LEFT JOIN vcs_repository_map m ON m.account_id=r.account_id AND m.integration_id=r.integration_id AND m.repository_id=` + requestRepositoryIDSQL

func commentRequest(row pgx.Row) (feedback.CommentPullRequest, error) {
	var p feedback.CommentPullRequest
	err := row.Scan(&p.ID, &p.Integration, &p.RepositoryID, &p.Number, &p.Namespace, &p.Repository, &p.Head, &p.Open, &p.Provider, &p.Mapped)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, feedback.ErrNoComment
	}
	if err != nil {
		return p, fmt.Errorf("VCS comment request read: %w", sql.Unavailable(err))
	}
	return p, nil
}

// CommentPullRequests reads every open current head, with no historical-head
// join or branch restriction: shared heads can target several pull requests.
// Native IDs follow repository renames and canonicalize historical UUID forms.
func (s *Store) CommentPullRequests(ctx context.Context, acct kernel.AccountID, integration kernel.ULID, namespace, repository, sha string) ([]feedback.CommentPullRequest, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+commentRequestColumns+commentRequestFrom+`
		WHERE r.account_id=$1 AND r.integration_id=$2 AND coalesce(m.namespace,r.namespace)=$3 AND coalesce(m.name,r.repository)=$4
		AND r.head_sha=$5 AND r.state='open' ORDER BY r.id`, acct, integration, namespace, repository, sha)
	if err != nil {
		return nil, fmt.Errorf("VCS comment requests: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (feedback.CommentPullRequest, error) { return commentRequest(row) })
	if err != nil {
		return nil, fmt.Errorf("VCS comment requests: %w", err)
	}
	return out, nil
}

// CommentPullRequest resolves a local target only within its owning account.
func (s *Store) CommentPullRequest(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (feedback.CommentPullRequest, error) {
	return commentRequest(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+commentRequestColumns+commentRequestFrom+`
		WHERE r.account_id=$1 AND r.id=$2`, acct, id))
}

// BindComment reserves one logical native comment. Only policy comments update
// their stored body and revision; a run's first plan binding remains immutable.
func (s *Store) BindComment(ctx context.Context, c feedback.Comment) (feedback.Comment, error) {
	if !tx.In(ctx) {
		return feedback.Comment{}, queue.ErrNoTransaction
	}
	body, err := json.Marshal(c)
	if err != nil {
		return feedback.Comment{}, fmt.Errorf("VCS comment encode: %w", err)
	}
	var id kernel.ULID
	err = tx.Q(ctx, s.db).QueryRow(ctx, `INSERT INTO vcs_pr_comments
		(account_id,id,integration_id,repository_id,pull_request,request_id,dedup_key,stack_id,run_id,run_created_at,binding)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT(account_id,integration_id,repository_id,pull_request,dedup_key) DO UPDATE SET
		binding=jsonb_set(CASE WHEN vcs_pr_comments.run_id IS NULL THEN jsonb_set(vcs_pr_comments.binding,'{body}',excluded.binding->'body') ELSE vcs_pr_comments.binding END,
		'{target,mapped}',to_jsonb(coalesce((vcs_pr_comments.binding#>>'{target,mapped}')::boolean,false) OR (excluded.binding#>>'{target,mapped}')::boolean)),
		revision=vcs_pr_comments.revision+CASE WHEN vcs_pr_comments.run_id IS NULL AND
		vcs_pr_comments.binding->>'body' IS DISTINCT FROM excluded.binding->>'body' THEN 1 ELSE 0 END
		RETURNING id`, c.Account, c.ID, c.Target.Integration, c.Target.RepositoryID, c.Target.Number, c.Target.ID, c.DedupKey,
		nullCommentID(c.Stack), nullCommentID(c.Run), nullCommentTime(c), body).Scan(&id)
	if err != nil {
		return feedback.Comment{}, fmt.Errorf("VCS comment bind: %w", sql.Unavailable(err))
	}
	return s.readComment(ctx, c.Account, id, false)
}

func nullCommentID(id kernel.ULID) *kernel.ULID {
	if id.IsZero() {
		return nil
	}
	return &id
}

func nullCommentTime(c feedback.Comment) any {
	if c.Run.IsZero() {
		return nil
	}
	return c.RunCreatedAt
}

func (s *Store) readComment(ctx context.Context, acct kernel.AccountID, id kernel.ULID, lock bool) (feedback.Comment, error) {
	var c feedback.Comment
	var body, reference []byte
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT binding,reference,revision,reported_revision FROM vcs_pr_comments WHERE account_id=$1 AND id=$2`+suffix,
		acct, id).Scan(&body, &reference, &c.Revision, &c.Reported)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, feedback.ErrNoComment
	}
	if err != nil {
		return c, fmt.Errorf("VCS comment read: %w", sql.Unavailable(err))
	}
	revision, reported := c.Revision, c.Reported
	if err := json.Unmarshal(body, &c); err != nil {
		return c, fmt.Errorf("VCS comment decode: %w", err)
	}
	c.Revision, c.Reported = revision, reported
	if err := json.Unmarshal(reference, &c.Reference); err != nil {
		return c, fmt.Errorf("VCS comment reference: %w", err)
	}
	return c, nil
}

// HoldComment fences integration retargeting/deletion, request head movement,
// all comments on that request, and the specific mapping before any native IO.
func (s *Store) HoldComment(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (feedback.Comment, error) {
	if !tx.In(ctx) {
		return feedback.Comment{}, queue.ErrNoTransaction
	}
	c, err := s.readComment(ctx, acct, id, false)
	if err != nil {
		return c, err
	}
	var held kernel.ULID
	err = tx.Q(ctx, s.db).QueryRow(ctx, `SELECT id FROM vcs_integrations WHERE account_id=$1 AND id=$2 FOR UPDATE`, acct, c.Target.Integration).Scan(&held)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, feedback.ErrNoComment
	}
	if err != nil {
		return c, fmt.Errorf("VCS comment integration hold: %w", sql.Unavailable(err))
	}
	pr, err := commentRequest(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+commentRequestColumns+commentRequestFrom+`
		WHERE r.account_id=$1 AND r.integration_id=$2 AND `+requestRepositoryIDSQL+`=$3 AND r.provider_id=$4 FOR UPDATE OF r`,
		acct, c.Target.Integration, c.Target.RepositoryID, c.Target.Number))
	if err != nil {
		return c, err
	}
	c, err = s.readComment(ctx, acct, id, true)
	if err != nil {
		return c, err
	}
	if pr.Integration != c.Target.Integration || pr.RepositoryID != c.Target.RepositoryID || pr.Number != c.Target.Number || pr.Provider != c.Target.Provider ||
		c.Target.Mapped && !pr.Mapped {
		return c, feedback.ErrNoComment
	}
	c.Target = pr
	return c, nil
}

// RecordComment acknowledges the native reference beside fenced completion.
func (s *Store) RecordComment(ctx context.Context, c feedback.Comment, reference port.CommentReference) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	body, err := json.Marshal(reference)
	if err != nil {
		return fmt.Errorf("VCS comment reference encode: %w", err)
	}
	_, err = tx.Q(ctx, s.db).Exec(ctx, `UPDATE vcs_pr_comments SET reference=$3,reported_revision=$4,
		binding=jsonb_set(binding,'{target,mapped}',to_jsonb(coalesce((binding#>>'{target,mapped}')::boolean,false) OR $5::boolean))
		WHERE account_id=$1 AND id=$2`, c.Account, c.ID, body, c.Revision, c.Target.Mapped)
	if err != nil {
		return fmt.Errorf("VCS comment record: %w", sql.Unavailable(err))
	}
	return nil
}

// SupersededComments excludes the newest recorded run by creation time and ID,
// including when an older run's delayed job posts after a newer run's comment.
// Notification comments have no stack/run and never enter this query.
func (s *Store) SupersededComments(ctx context.Context, c feedback.Comment) ([]feedback.Comment, error) {
	if !tx.In(ctx) {
		return nil, queue.ErrNoTransaction
	}
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT id,reference FROM vcs_pr_comments
		WHERE account_id=$1 AND integration_id=$2 AND repository_id=$3 AND pull_request=$4 AND stack_id=$5
		AND NOT minimized AND reference->>'id' IS NOT NULL AND (run_created_at,run_id)<
		(SELECT run_created_at,run_id FROM vcs_pr_comments WHERE account_id=$1 AND integration_id=$2 AND repository_id=$3
		AND pull_request=$4 AND stack_id=$5 AND reference->>'id' IS NOT NULL ORDER BY run_created_at DESC,run_id DESC LIMIT 1)
		ORDER BY run_created_at,run_id FOR UPDATE`, c.Account, c.Target.Integration, c.Target.RepositoryID, c.Target.Number, c.Stack)
	if err != nil {
		return nil, fmt.Errorf("VCS superseded comments: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (feedback.Comment, error) {
		var previous feedback.Comment
		var reference []byte
		if err := row.Scan(&previous.ID, &reference); err != nil {
			return previous, err //nolint:wrapcheck // collect boundary wraps it
		}
		err := json.Unmarshal(reference, &previous.Reference)
		return previous, err //nolint:wrapcheck // collect boundary wraps it
	})
	if err != nil {
		return nil, fmt.Errorf("VCS superseded comment decode: %w", err)
	}
	return out, nil
}

// MinimizedComment records successful minimization (or the provider's no-op).
func (s *Store) MinimizedComment(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	_, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE vcs_pr_comments SET minimized=true WHERE account_id=$1 AND id=$2`, acct, id)
	if err != nil {
		return fmt.Errorf("VCS comment minimize record: %w", sql.Unavailable(err))
	}
	return nil
}

// PurgeComments deletes only the removed account's rows in bounded batches.
func (s *Store) PurgeComments(ctx context.Context, acct kernel.AccountID, batch int) (bool, error) {
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM vcs_pr_comments WHERE ctid IN
		(SELECT ctid FROM vcs_pr_comments WHERE account_id=$1 ORDER BY id LIMIT $2)`, acct, batch)
	if err != nil {
		return false, fmt.Errorf("VCS comment purge: %w", sql.Unavailable(err))
	}
	return tag.RowsAffected() == 0, nil
}

var _ feedback.CommentStore = (*Store)(nil)
