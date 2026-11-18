package source

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type aliasNativeSource struct {
	port.Source
	t *testing.T
}

func (s aliasNativeSource) ResolveRepositoryRef(ctx context.Context, _ port.Repository, _ string) (port.Resolution, error) {
	if tx.In(ctx) {
		s.t.Fatal("native identity read held a transaction")
	}
	return port.Resolution{Commit: vcsapi.Commit{Hash: "native-head"}, Repository: vcsapi.Repository{ID: "7", Namespace: "canonical", Name: "renamed", OwnerID: "9"}}, nil
}

type aliasBinding struct {
	integrations
	use stacksapi.Use
}

func (b aliasBinding) EventUse(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Use, bool, error) {
	return b.use, true, nil
}

type aliasRow struct {
	account                   kernel.AccountID
	binding                   *kernel.ULID
	provider, namespace, name string
	version                   int
}
type aliasStacks struct {
	stacksapi.Stacks
	rows         map[kernel.ULID]aliasRow
	tracked      int
	failTracked  bool
	head, commit *stacksapi.Commit
}

func (s *aliasStacks) Settings(_ context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Settings, error) {
	row, ok := s.rows[id]
	if !ok || row.account != acct {
		return stacksapi.Settings{}, stacksapi.ErrNotFound
	}
	return stacksapi.Settings{Provider: row.provider, Namespace: row.namespace, Repository: row.name, Branch: "main", VCSIntegration: row.binding}, nil
}

func (s *aliasStacks) SetTracked(_ context.Context, _ kernel.AccountID, _ kernel.ULID, head, commit *stacksapi.Commit, by string) error {
	s.tracked++
	if s.failTracked {
		s.failTracked = false
		return errOffline
	}
	if by != "infraweave" {
		return errOffline
	}
	s.head, s.commit = head, commit
	return nil
}

func (s *aliasStacks) RepointVCSRepository(ctx context.Context, acct kernel.AccountID, use stacksapi.Use, from, to stacksapi.VCSRepository) error {
	if !tx.In(ctx) {
		return errOffline
	}
	for id, row := range s.rows {
		equivalent := row.provider == use.Provider
		for _, alias := range use.Aliases {
			equivalent = equivalent || row.provider == alias
		}
		bound := row.binding != nil && *row.binding == use.Integration || row.binding == nil && use.Default
		if row.account == acct && equivalent && bound && row.namespace == from.Namespace && row.name == from.Name && from != to {
			row.namespace, row.name, row.version = to.Namespace, to.Name, row.version+1
			s.rows[id] = row
		}
	}
	return nil
}

type aliasStore struct {
	lifecycle.Store
	repos map[string]lifecycle.Repository
}

func (*aliasStore) HoldRepositoryIntegration(context.Context, kernel.AccountID, kernel.ULID) (bool, error) {
	return true, nil
}

func (*aliasStore) MembershipRemoved(context.Context, kernel.AccountID, kernel.ULID, string, string, string) (bool, error) {
	return false, nil
}

func (*aliasStore) Installations(context.Context, kernel.AccountID, kernel.ULID) ([]lifecycle.Installation, error) {
	return nil, nil
}

func (s *aliasStore) RepositoryIdentity(_ context.Context, _ kernel.AccountID, _ kernel.ULID, id string) (lifecycle.Repository, bool, error) {
	r, ok := s.repos[id]
	return r, ok, nil
}

func (s *aliasStore) RepositoryPath(_ context.Context, _ kernel.AccountID, _ kernel.ULID, namespace, name string) (lifecycle.Repository, bool, error) {
	for _, r := range s.repos {
		if r.Namespace == namespace && r.Name == name {
			return r, true, nil
		}
	}
	return lifecycle.Repository{}, false, nil
}

func (s *aliasStore) PutRepositoryIdentity(_ context.Context, _ kernel.AccountID, _ kernel.ULID, r lifecycle.Repository) error {
	s.repos[r.ID] = r
	return nil
}

func (*aliasStore) MoveMembership(context.Context, kernel.AccountID, kernel.ULID, lifecycle.Repository) error {
	return nil
}

type (
	aliasDB struct{}
	aliasTx struct{ pgx.Tx }
)

func (aliasDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return aliasTx{}, nil }
func (aliasTx) Commit(context.Context) error                           { return nil }
func (aliasTx) Rollback(context.Context) error                         { return nil }

func TestUnitTrackAssociatesRequestedAliasWithNativeCoordinates(t *testing.T) {
	for _, collision := range []string{"none", "requested occupied", "canonical occupied"} {
		t.Run(collision, func(t *testing.T) {
			acct, id, other := kernel.NewAccountID(), kernel.NewULID(), kernel.NewULID()
			own, named, foreignAccount, foreignIntegration, foreignProvider := kernel.NewULID(), kernel.NewULID(), kernel.NewULID(), kernel.NewULID(), kernel.NewULID()
			rows := &aliasStacks{rows: map[kernel.ULID]aliasRow{
				own:                {account: acct, provider: "GITHUB", namespace: "alias", name: "before", version: 1},
				named:              {account: acct, binding: &id, provider: "GITHUB_ENTERPRISE", namespace: "alias", name: "before", version: 1},
				foreignAccount:     {account: kernel.NewAccountID(), binding: &id, provider: "GITHUB_ENTERPRISE", namespace: "alias", name: "before", version: 1},
				foreignIntegration: {account: acct, binding: &other, provider: "GITHUB_ENTERPRISE", namespace: "alias", name: "before", version: 1},
				foreignProvider:    {account: acct, binding: &id, provider: "GITLAB", namespace: "alias", name: "before", version: 1},
			}}
			store := &aliasStore{repos: map[string]lifecycle.Repository{}}
			if collision != "none" {
				ns, name := "alias", "before"
				if collision == "canonical occupied" {
					ns, name = "canonical", "renamed"
				}
				store.repos["8"] = lifecycle.Repository{Repository: vcsapi.Repository{ID: "8", Namespace: ns, Name: name}, ObservedAt: now}
			}
			use := stacksapi.Use{Integration: id, Provider: "GITHUB_ENTERPRISE", Aliases: []string{"GITHUB"}, Default: true}
			identities := &lifecycle.Service{DB: aliasDB{}, Store: store, Stacks: rows, Now: func() time.Time { return now }}
			svc := New(
				Deps{
					Stacks:       rows,
					Providers:    map[string]port.Source{"GITHUB": aliasNativeSource{t: t}},
					Identities:   identities,
					Integrations: aliasBinding{integrations: integrations{in: port.Integration{ID: id, Provider: "GITHUB_ENTERPRISE"}, found: true}, use: use},
					Now:          func() time.Time { return now },
				},
			)
			if err := svc.Track(t.Context(), acct, own); err != nil {
				t.Fatal(err)
			}
			if rows.tracked != 1 || rows.head == nil || rows.commit == nil || rows.head.Hash != "native-head" || *rows.head != *rows.commit {
				t.Fatal("initial source tracking lost resolved pointers after canonical repoint", rows.tracked, rows.head, rows.commit)
			}
			for stack, row := range rows.rows {
				affected := collision == "none" && (stack == own || stack == named)
				if affected && (row.namespace != "canonical" || row.name != "renamed" || row.version != 2) {
					t.Fatal("native canonical identity stranded requested alias", stack, row)
				}
				if !affected && (row.namespace != "alias" || row.name != "before" || row.version != 1) {
					t.Fatal("alias association crossed occupancy/account/integration/provider scope", stack, row)
				}
			}
			if collision != "none" {
				if _, inserted := store.repos["7"]; inserted {
					t.Fatal("occupied source path was reassigned")
				}
			}
		})
	}
}

func TestUnitTrackRetriesPointerInitializationAfterCanonicalRepoint(t *testing.T) {
	acct, id, stack := kernel.NewAccountID(), kernel.NewULID(), kernel.NewULID()
	rows := &aliasStacks{failTracked: true, rows: map[kernel.ULID]aliasRow{
		stack: {account: acct, binding: &id, provider: "GITHUB_ENTERPRISE", namespace: "alias", name: "before", version: 1},
	}}
	store := &aliasStore{repos: map[string]lifecycle.Repository{}}
	use := stacksapi.Use{Integration: id, Provider: "GITHUB_ENTERPRISE"}
	identities := &lifecycle.Service{DB: aliasDB{}, Store: store, Stacks: rows, Now: func() time.Time { return now }}
	svc := New(Deps{
		Stacks:       rows,
		Providers:    map[string]port.Source{"GITHUB_ENTERPRISE": aliasNativeSource{t: t}},
		Identities:   identities,
		Integrations: aliasBinding{integrations: integrations{in: port.Integration{ID: id, Provider: "GITHUB_ENTERPRISE"}, found: true}, use: use},
		Now:          func() time.Time { return now },
	})
	if err := svc.Track(t.Context(), acct, stack); !errors.Is(err, errOffline) {
		t.Fatal("initial pointer write did not fail", err)
	}
	if row := rows.rows[stack]; row.version != 2 || row.namespace != "canonical" || row.name != "renamed" || rows.commit != nil {
		t.Fatal("canonical repoint was not committed before failed pointer write", row, rows.commit)
	}
	if err := svc.Track(t.Context(), acct, stack); err != nil {
		t.Fatal(err)
	}
	if rows.rows[stack].version != 2 || rows.tracked != 2 || rows.head == nil || rows.commit == nil || rows.head.Hash != "native-head" || *rows.head != *rows.commit {
		t.Fatal("retry lost pointer initialization or repeated version bump", rows.rows[stack], rows.tracked, rows.head, rows.commit)
	}
}
