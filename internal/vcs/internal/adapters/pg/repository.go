package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/lifecycle"
)

var _ lifecycle.Store = (*Store)(nil)

// HoldRepositoryIntegration serializes path occupancy and installation updates
// with integration deletion. A missing integration has no lifecycle effects.
func (s *Store) HoldRepositoryIntegration(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (bool, error) {
	if !tx.In(ctx) {
		return false, queue.ErrNoTransaction
	}
	var found bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT true FROM vcs_integrations WHERE account_id=$1 AND id=$2 FOR UPDATE`, acct, id).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return found, repositoryError(err)
}

func repositoryError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("vcs: repository lifecycle: %w", sql.Unavailable(err))
}

func readRepository(row pgx.Row) (lifecycle.Repository, bool, error) {
	var r lifecycle.Repository
	err := row.Scan(&r.ID, &r.Namespace, &r.Name, &r.OwnerID, &r.ObservedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, repositoryError(err)
}

// RepositoryIdentity finds the current path of one native repository.
func (s *Store) RepositoryIdentity(ctx context.Context, acct kernel.AccountID, id kernel.ULID, native string) (lifecycle.Repository, bool, error) {
	return readRepository(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT repository_id,namespace,name,owner_id,observed_at FROM vcs_repository_map
 WHERE account_id=$1 AND integration_id=$2 AND repository_id=$3`, acct, id, native))
}

// RepositoryPath finds the identity occupying a current path.
func (s *Store) RepositoryPath(ctx context.Context, acct kernel.AccountID, id kernel.ULID, namespace, name string) (lifecycle.Repository, bool, error) {
	return readRepository(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT repository_id,namespace,name,owner_id,observed_at FROM vcs_repository_map
 WHERE account_id=$1 AND integration_id=$2 AND namespace=$3 AND name=$4`, acct, id, namespace, name))
}

// OwnerRepositories reads stable owner identity, with path fallback only for
// records learned before the provider supplied an owner ID.
func (s *Store) OwnerRepositories(ctx context.Context, acct kernel.AccountID, id kernel.ULID, owner, previous string) ([]lifecycle.Repository, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT repository_id,namespace,name,owner_id,observed_at FROM vcs_repository_map
 WHERE account_id=$1 AND integration_id=$2 AND (owner_id=$3 OR (owner_id='' AND namespace=$4)) ORDER BY repository_id`, acct, id, owner, previous)
	if err != nil {
		return nil, repositoryError(err)
	}
	defer rows.Close()
	var out []lifecycle.Repository
	for rows.Next() {
		r, _, err := readRepository(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, repositoryError(rows.Err())
}

// PutRepositoryIdentity writes a path after the integration lock/occupancy check.
func (s *Store) PutRepositoryIdentity(ctx context.Context, acct kernel.AccountID, id kernel.ULID, r lifecycle.Repository) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO vcs_repository_map(account_id,integration_id,repository_id,namespace,name,owner_id,observed_at)
 VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(account_id,integration_id,repository_id) DO UPDATE
 SET namespace=EXCLUDED.namespace,name=EXCLUDED.name,owner_id=EXCLUDED.owner_id,observed_at=EXCLUDED.observed_at`, acct, id, r.ID, r.Namespace, r.Name, r.OwnerID, r.ObservedAt)
	return repositoryError(err)
}

// DropRepositoryIdentity drops the current map but retains removed membership.
func (s *Store) DropRepositoryIdentity(ctx context.Context, acct kernel.AccountID, id kernel.ULID, native string) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM vcs_repository_map WHERE account_id=$1 AND integration_id=$2 AND repository_id=$3`, acct, id, native)
	return repositoryError(err)
}

// Installations returns availability records without opening any credential.
func (s *Store) Installations(ctx context.Context, acct kernel.AccountID, id kernel.ULID) ([]lifecycle.Installation, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT installation_id,owner_id,owner_login,state,observed_at FROM vcs_installations
 WHERE account_id=$1 AND integration_id=$2 ORDER BY installation_id`, acct, id)
	if err != nil {
		return nil, repositoryError(err)
	}
	defer rows.Close()
	var out []lifecycle.Installation
	for rows.Next() {
		var in lifecycle.Installation
		if err := rows.Scan(&in.ID, &in.Owner.ID, &in.Owner.Login, &in.State, &in.ObservedAt); err != nil {
			return nil, repositoryError(err)
		}
		out = append(out, in)
	}
	return out, repositoryError(rows.Err())
}

// PutInstallation records the last native state observation.
func (s *Store) PutInstallation(ctx context.Context, acct kernel.AccountID, id kernel.ULID, in lifecycle.Installation) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO vcs_installations(account_id,integration_id,installation_id,owner_id,owner_login,state,observed_at)
 VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(account_id,integration_id,installation_id) DO UPDATE
 SET owner_id=EXCLUDED.owner_id,owner_login=EXCLUDED.owner_login,state=EXCLUDED.state,observed_at=EXCLUDED.observed_at`, acct, id, in.ID, in.Owner.ID, in.Owner.Login, in.State, in.ObservedAt)
	return repositoryError(err)
}

// PutMembership preserves both known membership and explicit removal.
func (s *Store) PutMembership(ctx context.Context, acct kernel.AccountID, id kernel.ULID, installation string, r lifecycle.Repository, present bool) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO vcs_installation_repositories(account_id,integration_id,installation_id,repository_id,namespace,name,present,observed_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(account_id,integration_id,repository_id) DO UPDATE
 SET installation_id=EXCLUDED.installation_id,namespace=EXCLUDED.namespace,name=EXCLUDED.name,present=EXCLUDED.present,observed_at=EXCLUDED.observed_at
 WHERE vcs_installation_repositories.observed_at <= EXCLUDED.observed_at`, acct, id, installation, r.ID, r.Namespace, r.Name, present, r.ObservedAt)
	return repositoryError(err)
}

// MembershipRemoved answers only positively known absence, never an unknown App.
func (s *Store) MembershipRemoved(ctx context.Context, acct kernel.AccountID, id kernel.ULID, namespace, name string) (bool, error) {
	var removed bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM vcs_installation_repositories WHERE account_id=$1 AND integration_id=$2
 AND namespace=$3 AND name=$4 AND NOT present)`, acct, id, namespace, name).Scan(&removed)
	return removed, repositoryError(err)
}
