package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
)

// vcsUsers keeps reads and provider-driven writes on the same effective
// integration binding, including aliases and stacks using a provider default.
const vcsUsers = `account_id=$1 AND NOT vcs_detached AND provider=ANY($4::text[])
	AND (vcs_integration_id=$2 OR ($3 AND vcs_integration_id IS NULL))`

// VCSRepositories deliberately selects no stack identities or settings:
// admin diagnostics must check all users of an integration across spaces.
func (s *Store) VCSRepositories(ctx context.Context, acct kernel.AccountID, u stacksapi.Use) ([]stacksapi.VCSRepository, error) {
	providers := append([]string{u.Provider}, u.Aliases...)
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT DISTINCT namespace,repository FROM stacks WHERE `+vcsUsers+`
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

// ByVCSRepository preserves the integration/default binding rule while matching
// exact repository coordinates across branches. Detached stacks receive no events.
func (s *Store) ByVCSRepository(ctx context.Context, acct kernel.AccountID, u stacksapi.Use, namespace, repository string) ([]stacksapi.Stack, error) {
	providers := append([]string{u.Provider}, u.Aliases...)
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+refColumns+` FROM stacks WHERE `+vcsUsers+`
		AND namespace=$5 AND repository=$6 ORDER BY slug`, acct, u.Integration, u.Default, providers, namespace, repository)
	if err != nil {
		return nil, fmt.Errorf("stacks: stacks using VCS repository: %w", sql.Unavailable(err))
	}
	stacks, err := refs(acct, rows)
	if err != nil {
		return nil, fmt.Errorf("stacks: collect VCS repository stacks: %w", sql.Unavailable(err))
	}
	return stacks, nil
}

// RepointVCSRepository locks source users in ID order before the bulk update.
// Its minimal return values let the core append events without loading settings.
func (s *Store) RepointVCSRepository(ctx context.Context, acct kernel.AccountID, u stacksapi.Use, from, to stacksapi.VCSRepository) ([]core.Stack, error) {
	if !tx.In(ctx) {
		return nil, stacksapi.ErrNoTransaction
	}
	if from == to {
		return nil, nil
	}
	providers := append([]string{u.Provider}, u.Aliases...)
	rows, err := tx.Q(ctx, s.db).Query(ctx, `WITH targets AS (
		SELECT id FROM stacks WHERE `+vcsUsers+` AND namespace=$5 AND repository=$6 ORDER BY id FOR UPDATE
	) UPDATE stacks s SET namespace=$7,repository=$8,version=s.version+1,updated_at=clock_timestamp() FROM targets t
		WHERE s.account_id=$1 AND s.id=t.id RETURNING s.account_id,s.id,s.slug,s.space_id,s.version`,
		acct, u.Integration, u.Default, providers, from.Namespace, from.Name, to.Namespace, to.Name)
	if err != nil {
		return nil, fmt.Errorf("stacks: repoint VCS repository: %w", sql.Unavailable(err))
	}
	stacks, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (core.Stack, error) {
		var stack core.Stack
		err := row.Scan(&stack.Account, &stack.ID, &stack.Slug, &stack.Space, &stack.Version)
		return stack, err //nolint:wrapcheck // the collecting caller names the write
	})
	if err != nil {
		return nil, fmt.Errorf("stacks: collect repointed stacks: %w", sql.Unavailable(err))
	}
	return stacks, nil
}
