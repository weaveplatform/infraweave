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
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/lifecycle"
)

// HoldCheckIntegration precedes binding, resource, stack and repository holds,
// matching lifecycle updates and integration deletion's parent-first order.
func (s *Store) HoldCheckIntegration(ctx context.Context, account kernel.AccountID, id kernel.ULID) (bool, error) {
	if !tx.In(ctx) {
		return false, queue.ErrNoTransaction
	}
	var found bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT true FROM vcs_integrations WHERE account_id=$1 AND id=$2 FOR SHARE`, account, id).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("vcs: hold check integration: %w", sql.Unavailable(err))
	}
	return found, nil
}

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
		QueryRow(ctx, `SELECT binding_key,native_id,digest,owner_id,coalesce(owner_epoch,'epoch'::timestamptz),sequence,is_aggregate FROM vcs_check_resources WHERE account_id=$1 AND resource_key=$2 FOR UPDATE`, r.Account, r.Key).
		Scan(&r.Binding, &r.NativeID, &r.Digest, &r.Owner, &r.Epoch, &r.Sequence, &r.Aggregate)
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
		SET binding_key=EXCLUDED.binding_key,owner_id=EXCLUDED.owner_id,owner_epoch=EXCLUDED.owner_epoch,sequence=0,is_aggregate=false
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
		Exec(ctx, `UPDATE vcs_check_resources SET binding_key=$3,native_id=$4,digest=$5,owner_id=$6,owner_epoch=$7,sequence=$8,is_aggregate=$9 WHERE account_id=$1 AND resource_key=$2`, r.Account, r.Key, r.Binding, r.NativeID, r.Digest, r.Owner, r.Epoch, r.Sequence, r.Aggregate)
	if err != nil {
		return fmt.Errorf("vcs: acknowledge native check resource: %w", sql.Unavailable(err))
	}
	if tag.RowsAffected() != 1 {
		return feedback.ErrNoCheck
	}
	return nil
}

// ResolveCheckRepository captures a known immutable ID at selection, or follows
// that ID's current path under a shared row lock during reporting. Unknown
// historical bindings retain their original path; known removed IDs never do.
func (s *Store) ResolveCheckRepository(ctx context.Context, b feedback.CheckBinding) (feedback.CheckBinding, error) {
	if tx.In(ctx) {
		found, err := s.HoldCheckIntegration(ctx, b.Account, b.Integration)
		if err != nil {
			return b, err
		}
		if !found {
			return b, feedback.ErrNoCheck
		}
	}
	var id, namespace, repository string
	var err error
	q := tx.Q(ctx, s.db)
	if b.RepositoryID == "" {
		err = q.QueryRow(ctx, `SELECT repository_id,namespace,name FROM vcs_repository_map
		WHERE account_id=$1 AND integration_id=$2 AND
		CASE WHEN $5='GITHUB_ENTERPRISE' THEN lower(namespace)=lower($3) AND lower(name)=lower($4)
		ELSE namespace=$3 AND name=$4 END FOR SHARE`, b.Account, b.Integration, b.Namespace, b.Repository, b.Provider).Scan(&id, &namespace, &repository)
	} else {
		err = q.QueryRow(ctx, `SELECT repository_id,namespace,name FROM vcs_repository_map
		WHERE account_id=$1 AND integration_id=$2 AND repository_id=$3 FOR SHARE`, b.Account, b.Integration, lifecycle.CanonicalID(b.RepositoryID)).Scan(&id, &namespace, &repository)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if b.RepositoryID != "" {
			return b, feedback.ErrNoCheck
		}
		return b, nil
	}
	if err != nil {
		return b, fmt.Errorf("vcs: resolve check repository: %w", sql.Unavailable(err))
	}
	b.RepositoryID, b.Namespace, b.Repository = id, namespace, repository
	return b, nil
}

func (s *Store) CapChecks(ctx context.Context, b feedback.CheckBinding, kind kernel.RunType) ([]feedback.CheckBinding, error) {
	query := `SELECT binding FROM vcs_check_bindings WHERE account_id=$1 AND integration_id=$2
		AND commit_hash=$3 AND run_type=$4 AND is_cap AND `
	args := []any{b.Account, b.Integration, b.Commit, kind}
	if b.RepositoryID != "" {
		query += `binding->>'repositoryId'=$5`
		args = append(args, b.RepositoryID)
	} else {
		query += `namespace=$5 AND repository=$6`
		args = append(args, b.Namespace, b.Repository)
	}
	rows, err := tx.Q(ctx, s.db).Query(ctx, query+` ORDER BY binding_key`, args...)
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

func (s *Store) CheckPullRequests(ctx context.Context, b feedback.CheckBinding) ([]int, error) {
	rows, err := tx.Q(ctx, s.db).
		Query(ctx, `SELECT r.provider_id FROM pull_requests r
        LEFT JOIN vcs_repository_map m ON m.account_id=r.account_id AND m.integration_id=r.integration_id AND m.repository_id=`+requestRepositoryIDSQL+`
        WHERE r.account_id=$1 AND r.integration_id=$2 AND coalesce(m.namespace,r.namespace)=$3
        AND coalesce(m.name,r.repository)=$4 AND r.head_sha=$5 AND ($6='' OR `+requestRepositoryIDSQL+`=$6)
        ORDER BY r.provider_id`, b.Account, b.Integration, b.Namespace, b.Repository, b.Commit, b.RepositoryID)
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
