package push

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
)

type noUserLifecycle struct {
	*reviewCurrentRepository
	applied int
}

func (l *noUserLifecycle) Apply(context.Context, kernel.AccountID, kernel.ULID, stacksapi.Use, time.Time, []vcsapi.Event) error {
	l.applied++
	return nil
}

type countedPreparedStore struct {
	Store
	writes int
}

func (s *countedPreparedStore) PutPrepared(context.Context, kernel.AccountID, kernel.ULID, []Prepared) error {
	s.writes++
	return nil
}

func TestUnitPreparedNoUsersRetainsLifecycleAndRetriesNewUsers(t *testing.T) {
	for _, kind := range []string{"push", "tag"} {
		t.Run(kind, func(t *testing.T) {
			acct, integration := kernel.NewAccountID(), kernel.NewULID()
			repo := vcsapi.Repository{ID: "7", Namespace: "acme", Name: "infra"}
			lifecycle := &noUserLifecycle{reviewCurrentRepository: &reviewCurrentRepository{current: repo}}
			stacks, store, jobs := &repositoryStacks{}, &countedPreparedStore{}, &preparedJobs{}
			source := &pushSource{t: t, err: errors.New("provider unavailable")}
			s := New(Deps{
				Stacks: stacks, Store: store, Jobs: jobs, Sources: source, Lifecycle: lifecycle,
				Integrations: eventUse{use: stacksapi.Use{Integration: integration, Provider: "GITLAB"}, found: true},
			})
			delivery := intake.Delivery{Account: acct, ID: kernel.NewULID(), Integration: integration, Provider: "GITLAB", Deadline: time.Now().Add(time.Minute)}
			events := []vcsapi.Event{{Type: kind, Repository: repo, Head: strings.Repeat("a", 40), Branch: "main", Tag: "v1"}}
			prepare := func() intake.DispatchPlan {
				t.Helper()
				plan, err := s.Prepare(t.Context(), delivery, events)
				if err != nil || plan == nil {
					t.Fatal("no-user preparation called an unavailable provider", err)
				}
				return plan
			}
			if err := tx.Do(t.Context(), evaluationDB{}, tx.Options{}, prepare().Commit); err != nil {
				t.Fatal(err)
			}
			if lifecycle.applied != 1 || store.writes != 0 || len(jobs.inputs) != 0 || len(source.calls)+len(source.tags) != 0 {
				t.Fatal("no-user delivery lost lifecycle work or persisted unprepared facts")
			}
			plan := prepare()
			stack := kernel.NewULID()
			stacks.rows = []stacksapi.Stack{{ID: stack}}
			if err := tx.Do(t.Context(), evaluationDB{}, tx.Options{}, plan.Commit); !errors.Is(err, kernel.ErrUnavailable) {
				t.Fatal("new users did not require outside-transaction preparation", err)
			}
			if store.writes != 0 || len(jobs.inputs) != 0 || len(source.calls)+len(source.tags) != 0 {
				t.Fatal("new-user race leaked empty facts, child jobs or inline provider work")
			}
			source.err = nil
			if err := tx.Do(t.Context(), evaluationDB{}, tx.Options{}, prepare().Commit); err != nil {
				t.Fatal(err)
			}
			if store.writes != 1 || len(jobs.inputs) != 1 || jobs.inputs[0].Stack != stack || len(source.calls)+len(source.tags) != 1 {
				t.Fatal("retried preparation lost or repeated the newly created user's work")
			}
		})
	}
}
