package pg

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/feedback"
)

func (s *Store) BindCheck(ctx context.Context, b feedback.CheckBinding) (feedback.CheckBinding, error) {
	if !tx.In(ctx) {
		return b, queue.ErrNoTransaction
	}
	var kept feedback.CheckBinding
	err := tx.Q(ctx, s.db).QueryRow(ctx, `WITH owner AS (SELECT id FROM vcs_integrations WHERE account_id=$1 AND id=$5 FOR KEY SHARE)
        INSERT INTO vcs_check_bindings(account_id,binding_key,run_id,stack_id,integration_id,namespace,repository,commit_hash,run_type,is_cap,binding)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11 FROM owner
		ON CONFLICT(account_id,binding_key) DO UPDATE SET binding=CASE WHEN vcs_check_bindings.is_cap AND (vcs_check_bindings.binding->>'createdAt')::timestamptz <= (EXCLUDED.binding->>'createdAt')::timestamptz THEN EXCLUDED.binding ELSE vcs_check_bindings.binding END
		RETURNING binding`, b.Account, b.Key, b.Run, b.Stack, b.Integration, b.Namespace, b.Repository, b.Commit, b.Type, b.Cap, b).Scan(&kept)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, feedback.ErrNoCheck
	}
	if err != nil {
		return b, fmt.Errorf("vcs: bind commit check: %w", sql.Unavailable(err))
	}
	return kept, nil
}

// CheckBinding retains the row while a report runs, so integration/stack purge
// cannot commit and then have an old report resurrect its local metadata.
func (s *Store) CheckBinding(ctx context.Context, acct kernel.AccountID, key string) (feedback.CheckBinding, error) {
	var b feedback.CheckBinding
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT binding FROM vcs_check_bindings WHERE account_id=$1 AND binding_key=$2 FOR KEY SHARE`, acct, key).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, feedback.ErrNoCheck
	}
	if err != nil {
		return b, fmt.Errorf("vcs: read commit check binding: %w", sql.Unavailable(err))
	}
	return b, nil
}

func (s *Store) RequestCheck(ctx context.Context, r feedback.CheckRequest) (bool, error) {
	if !tx.In(ctx) {
		return false, queue.ErrNoTransaction
	}
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO vcs_check_requests(account_id,binding_key,check_name,is_aggregate,sequence) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT(account_id,binding_key,check_name,is_aggregate) DO UPDATE SET sequence=EXCLUDED.sequence
		WHERE vcs_check_requests.sequence<EXCLUDED.sequence`, r.Binding.Account, r.Binding.Key, r.Name, r.Aggregate, r.Sequence)
	if err != nil {
		return false, fmt.Errorf("vcs: request commit check: %w", sql.Unavailable(err))
	}
	return tag.RowsAffected() > 0, nil
}

func (s *Store) HoldCheck(ctx context.Context, r feedback.CheckResource) (feedback.CheckResource, error) {
	if !tx.In(ctx) {
		return r, queue.ErrNoTransaction
	}
	if _, err := tx.Q(ctx, s.db).
		Exec(ctx, `INSERT INTO vcs_check_resources(account_id,resource_key,binding_key) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, r.Account, r.Key, r.Binding); err != nil {
		return r, fmt.Errorf("vcs: establish native check resource: %w", sql.Unavailable(err))
	}
	err := tx.Q(ctx, s.db).
		QueryRow(ctx, `SELECT binding_key,native_id,digest,owner_id,coalesce(owner_epoch,'epoch'::timestamptz),sequence FROM vcs_check_resources WHERE account_id=$1 AND resource_key=$2 FOR UPDATE`, r.Account, r.Key).
		Scan(&r.Binding, &r.NativeID, &r.Digest, &r.Owner, &r.Epoch, &r.Sequence)
	if err != nil {
		return r, fmt.Errorf("vcs: hold native check resource: %w", sql.Unavailable(err))
	}
	return r, nil
}

// ReserveCheck transfers a shared native status to the newest run before its
// job executes. A newer job that is retrying cannot be overwritten by an old
// run finishing. Its provider ID survives an ownership transfer.
func (s *Store) ReserveCheck(ctx context.Context, r feedback.CheckResource) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO vcs_check_resources(account_id,resource_key,binding_key,owner_id,owner_epoch)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT(account_id,resource_key) DO UPDATE
		SET binding_key=EXCLUDED.binding_key,owner_id=EXCLUDED.owner_id,owner_epoch=EXCLUDED.owner_epoch,sequence=0
		WHERE (coalesce(vcs_check_resources.owner_epoch,'epoch'::timestamptz),coalesce(vcs_check_resources.owner_id,'00000000-0000-0000-0000-000000000000'::uuid)) < (EXCLUDED.owner_epoch,EXCLUDED.owner_id)`, r.Account, r.Key, r.Binding, r.Owner, r.Epoch)
	if err != nil {
		return fmt.Errorf("vcs: reserve native check owner: %w", sql.Unavailable(err))
	}
	return nil
}

func (s *Store) ReportedCheck(ctx context.Context, r feedback.CheckResource) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	tag, err := tx.Q(ctx, s.db).
		Exec(ctx, `UPDATE vcs_check_resources SET binding_key=$3,native_id=$4,digest=$5,owner_id=$6,owner_epoch=$7,sequence=$8 WHERE account_id=$1 AND resource_key=$2`, r.Account, r.Key, r.Binding, r.NativeID, r.Digest, r.Owner, r.Epoch, r.Sequence)
	if err != nil {
		return fmt.Errorf("vcs: acknowledge native check resource: %w", sql.Unavailable(err))
	}
	if tag.RowsAffected() != 1 {
		return feedback.ErrNoCheck
	}
	return nil
}

func (s *Store) CapChecks(ctx context.Context, acct kernel.AccountID, integration kernel.ULID, namespace, repo, sha string, kind kernel.RunType) ([]feedback.CheckBinding, error) {
	rows, err := tx.Q(ctx, s.db).
		Query(ctx, `SELECT binding FROM vcs_check_bindings WHERE account_id=$1 AND integration_id=$2 AND namespace=$3 AND repository=$4 AND commit_hash=$5 AND run_type=$6 AND is_cap ORDER BY binding_key`, acct, integration, namespace, repo, sha, kind)
	if err != nil {
		return nil, fmt.Errorf("vcs: read capped commit checks: %w", sql.Unavailable(err))
	}
	bindings, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (feedback.CheckBinding, error) {
		var b feedback.CheckBinding
		err := r.Scan(&b)
		return b, err //nolint:wrapcheck // the collector boundary names row decoding
	})
	if err != nil {
		return nil, fmt.Errorf("vcs: read capped check row: %w", sql.Unavailable(err))
	}
	return bindings, nil
}

func (s *Store) CheckPullRequests(ctx context.Context, acct kernel.AccountID, integration kernel.ULID, namespace, repo, sha string) ([]int, error) {
	rows, err := tx.Q(ctx, s.db).
		Query(ctx, `SELECT provider_id FROM pull_requests WHERE account_id=$1 AND integration_id=$2 AND namespace=$3 AND repository=$4 AND head_sha=$5 ORDER BY provider_id`, acct, integration, namespace, repo, sha)
	if err != nil {
		return nil, fmt.Errorf("vcs: read check pull request heads: %w", sql.Unavailable(err))
	}
	requests, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (int, error) {
		var number string
		if err := r.Scan(&number); err != nil {
			return 0, fmt.Errorf("decode check pull request: %w", err)
		}
		n, err := strconv.Atoi(number)
		if err != nil || n <= 0 {
			return 0, feedback.ErrCheckReport
		}
		return n, nil
	})
	if err != nil {
		return nil, fmt.Errorf("vcs: read check pull request number: %w", sql.Unavailable(err))
	}
	return requests, nil
}

func (s *Store) DeleteStackChecks(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	_, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM vcs_check_bindings WHERE account_id=$1 AND stack_id=$2`, acct, stack)
	if err != nil {
		return fmt.Errorf("vcs: delete stack commit checks: %w", sql.Unavailable(err))
	}
	return nil
}

func (s *Store) PurgeChecks(ctx context.Context, acct kernel.AccountID, batch int) (bool, error) {
	var count int64
	for _, table := range []string{"vcs_check_bindings", "vcs_check_resources"} {
		tag, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM `+table+` WHERE ctid IN (SELECT ctid FROM `+table+` WHERE account_id=$1 LIMIT $2)`, acct, batch)
		if err != nil {
			return false, fmt.Errorf("vcs: purge commit checks: %w", sql.Unavailable(err))
		}
		count += tag.RowsAffected()
	}
	return count == 0, nil
}

var _ feedback.CheckStore = (*Store)(nil)
