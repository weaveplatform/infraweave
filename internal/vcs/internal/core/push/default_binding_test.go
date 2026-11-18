package push

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/lifecycle"
)

type reviewBindingIntegration struct {
	id, currentDefault kernel.ULID
	reads              int
}

func (i *reviewBindingIntegration) EventUse(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Use, bool, error) {
	i.reads++
	return stacksapi.Use{Integration: i.id, Provider: "GITLAB", Default: i.currentDefault == i.id}, true, nil
}

type reviewBindingRow struct {
	id, binding     kernel.ULID
	namespace, name string
}

type reviewBindingStacks struct {
	stacksapi.Stacks
	t       *testing.T
	account kernel.AccountID
	rows    []reviewBindingRow
}

// This is the owned Stacks SQL's effective binding predicate:
// vcs_integration_id=use.Integration OR (use.Default AND integration IS NULL).
// Every row has the same account/provider and is attached; only the binding
// differs, so the regression isolates a legitimate default-switch operation.
func (s *reviewBindingStacks) selected(account kernel.AccountID, use stacksapi.Use, row reviewBindingRow, namespace, name string) bool {
	return account == s.account && use.Provider == "GITLAB" && row.namespace == namespace && row.name == name &&
		(row.binding == use.Integration || use.Default && row.binding.IsZero())
}

func (s *reviewBindingStacks) RepointVCSRepository(ctx context.Context, account kernel.AccountID, use stacksapi.Use, from, to stacksapi.VCSRepository) error {
	if !tx.In(ctx) {
		s.t.Fatal("repoint outside completing transaction")
	}
	for i, row := range s.rows {
		if s.selected(account, use, row, from.Namespace, from.Name) {
			s.rows[i].namespace, s.rows[i].name = to.Namespace, to.Name
		}
	}
	return nil
}

func (s *reviewBindingStacks) ByVCSRepository(ctx context.Context, account kernel.AccountID, use stacksapi.Use, namespace, name string) ([]stacksapi.Stack, error) {
	if !tx.In(ctx) {
		s.t.Fatal("fanout outside completing transaction")
	}
	var rows []stacksapi.Stack
	for _, row := range s.rows {
		if s.selected(account, use, row, namespace, name) {
			rows = append(rows, stacksapi.Stack{Account: account, ID: row.id})
		}
	}
	return rows, nil
}

type reviewBindingStore struct {
	lifecycle.Store
	repo  lifecycle.Repository
	holds int
}

func (s *reviewBindingStore) HoldRepositoryIntegration(ctx context.Context, _ kernel.AccountID, _ kernel.ULID) (bool, error) {
	if !tx.In(ctx) {
		return false, fmt.Errorf("review integration hold requires transaction")
	}
	s.holds++
	return true, nil
}

func (s *reviewBindingStore) RepositoryIdentity(context.Context, kernel.AccountID, kernel.ULID, string) (lifecycle.Repository, bool, error) {
	return s.repo, true, nil
}

func (s *reviewBindingStore) RepositoryPath(_ context.Context, _ kernel.AccountID, _ kernel.ULID, namespace, name string) (lifecycle.Repository, bool, error) {
	return s.repo, s.repo.Namespace == namespace && s.repo.Name == name, nil
}

func (s *reviewBindingStore) PutRepositoryIdentity(_ context.Context, _ kernel.AccountID, _ kernel.ULID, repo lifecycle.Repository) error {
	s.repo = repo
	return nil
}

func (*reviewBindingStore) MoveMembership(context.Context, kernel.AccountID, kernel.ULID, lifecycle.Repository) error {
	return nil
}

func (*reviewBindingStore) MembershipRemoved(context.Context, kernel.AccountID, kernel.ULID, string, string, string) (bool, error) {
	return false, nil
}

func TestUnitReviewDefaultSwitchBetweenPrepareAndCommit(t *testing.T) {
	for _, initiallyDefault := range []bool{true, false} {
		t.Run(fmt.Sprintf("initiallyDefault=%t", initiallyDefault), func(t *testing.T) {
			account := kernel.NewAccountID()
			i1, i2 := kernel.NewULID(), kernel.NewULID()
			binding := &reviewBindingIntegration{id: i1, currentDefault: i2}
			if initiallyDefault {
				binding.currentDefault = i1
			}
			rows := &reviewBindingStacks{t: t, account: account, rows: []reviewBindingRow{
				{id: kernel.NewULID(), binding: i1, namespace: "acme", name: "infra"},
				{id: kernel.NewULID(), namespace: "acme", name: "infra"},
				{id: kernel.NewULID(), binding: i2, namespace: "acme", name: "infra"},
			}}
			now := time.Now().UTC()
			store := &reviewBindingStore{repo: lifecycle.Repository{
				Repository: vcsapi.Repository{ID: "7", Namespace: "acme", Name: "infra"}, ObservedAt: now.Add(-time.Minute),
			}}
			native := &lifecycle.Service{DB: evaluationDB{}, Store: store, Stacks: rows, Now: time.Now}
			jobs := &preparedJobs{}
			source := &pushSource{t: t}
			s := New(Deps{Integrations: binding, Lifecycle: native, Stacks: rows, Sources: source, Store: preparedStore{}, Jobs: jobs})
			delivery := intake.Delivery{Account: account, ID: kernel.NewULID(), Integration: i1, Provider: "GITLAB", ReceivedAt: now, Deadline: now.Add(time.Minute)}
			event := vcsapi.Event{Type: "push", Repository: vcsapi.Repository{ID: "7", Namespace: "acme", Name: "renamed"}, Branch: "main", Head: strings.Repeat("b", 40)}
			plan, err := s.Prepare(t.Context(), delivery, []vcsapi.Event{event})
			if err != nil {
				t.Fatal(err)
			}
			// This models the committed, atomic I1/I2 provider-default switch
			// after provider preparation and before the held completion starts.
			binding.currentDefault = i1
			if initiallyDefault {
				binding.currentDefault = i2
			}
			if err := tx.Do(t.Context(), evaluationDB{}, tx.Options{}, plan.Commit); err != nil {
				t.Fatal(err)
			}
			if store.holds != 1 || len(source.calls) != 1 {
				t.Fatal("proof lost held integration or repeated provider work", store.holds, source.calls)
			}
			for index, row := range rows.rows {
				want := "infra"
				if index == 0 || index == 1 && binding.currentDefault == i1 {
					want = "renamed"
				}
				if row.name != want {
					t.Errorf("stack index=%d binding=%s currentDefault=%s coordinates=%s/%s want=acme/%s (EventUse reads=%d)",
						index, row.binding, binding.currentDefault, row.namespace, row.name, want, binding.reads)
				}
			}
			var got []kernel.ULID
			for _, child := range jobs.inputs {
				got = append(got, child.Stack)
			}
			for index, row := range rows.rows {
				want := index == 0 || index == 1 && binding.currentDefault == i1
				if slices.Contains(got, row.id) != want {
					t.Errorf("stack index=%d fanout=%t want=%t after committed default switch (EventUse reads=%d)", index, slices.Contains(got, row.id), want, binding.reads)
				}
			}
		})
	}
}

func (i *reviewBindingIntegration) HeldEventUse(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Use, bool, error) {
	if !tx.In(ctx) {
		return stacksapi.Use{}, false, fmt.Errorf("held binding read requires transaction")
	}
	return i.EventUse(ctx, acct, id)
}
