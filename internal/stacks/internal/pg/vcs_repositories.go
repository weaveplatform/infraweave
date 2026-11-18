package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// VCSRepositories deliberately selects no stack identities or settings:
// admin diagnostics must check all users of an integration across spaces.
func (s *Store) VCSRepositories(ctx context.Context, acct kernel.AccountID, u stacksapi.Use) ([]stacksapi.VCSRepository, error) {
	providers := append([]string{u.Provider}, u.Aliases...)
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT DISTINCT namespace,repository FROM stacks
		WHERE account_id=$1 AND NOT vcs_detached AND
		(vcs_integration_id=$2 OR ($3 AND vcs_integration_id IS NULL AND provider=ANY($4::text[])))
		ORDER BY namespace,repository`, acct, u.Integration, u.Default, providers)
	if err != nil {
		return nil, fmt.Errorf("stacks: repositories using VCS integration: %w", sql.Unavailable(err))
	}
	repositories, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (stacksapi.VCSRepository, error) {
		var repo stacksapi.VCSRepository
		err := row.Scan(&repo.Namespace, &repo.Name)
		return repo, err //nolint:wrapcheck // the collecting caller names the read
	})
	if err != nil {
		return nil, fmt.Errorf("stacks: collect VCS repositories: %w", sql.Unavailable(err))
	}
	return repositories, nil
}
