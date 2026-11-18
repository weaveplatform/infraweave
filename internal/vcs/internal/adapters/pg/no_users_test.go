package pg_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/push"
)

func TestAccNoUserDeliverySeedsIdentityAndRetriesNewUsers(t *testing.T) {
	for _, appeared := range []bool{false, true} {
		t.Run(fmt.Sprintf("users-appeared-%t", appeared), func(t *testing.T) {
			w := newWebhookWorld(t)
			targets := &routedTargets{}
			source := &dispatchSource{}
			native := &lifecycle.Service{DB: w.f.db, Store: w.f.store, Stacks: targets, Now: time.Now}
			repo := vcsapi.Repository{ID: "7", Namespace: "acme", Name: "infra"}
			deps := w.deps
			deps.Providers["GITHUB_ENTERPRISE"] = dispatchProvider{events: []vcsapi.Event{{Type: "push", Repository: repo, Branch: "main", Head: strings.Repeat("b", 40)}}}
			dispatcher := push.New(push.Deps{
				DB: w.f.db, Store: w.f.store, Stacks: targets, Integrations: dispatchIntegration{w.id}, Sources: source, Jobs: w.queue, Lifecycle: native,
			})
			barrier := preparationBarrier{Dispatcher: dispatcher, ready: make(chan struct{}), proceed: make(chan struct{})}
			deps.Dispatcher = dispatcher
			if appeared {
				deps.Dispatcher = barrier
			}
			w.svc = intake.New(deps)
			w.receive(t, "no-users", webhookBody(128))
			job := w.claim(t, w.queue, "initial-parent")
			if appeared {
				done := make(chan error, 1)
				go func() { done <- w.svc.Processed(t.Context(), job) }()
				select {
				case <-barrier.ready:
				case <-t.Context().Done():
					t.Fatal(context.Cause(t.Context()))
				}
				targets.rows = []stacksapi.Stack{{ID: kernel.NewULID()}}
				close(barrier.proceed)
				err := <-done
				if !errors.Is(err, kernel.ErrUnavailable) {
					t.Fatal("new users did not require fresh provider preparation", err)
				}
				facts, readErr := w.f.store.Prepared(t.Context(), w.f.acct, w.delivery(t, "no-users").ID)
				_, found, mapErr := w.f.store.RepositoryIdentity(t.Context(), w.f.acct, w.id, repo.ID)
				var children int
				if queryErr := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE account_id=$1 AND kind=$2`, w.f.acct, push.Evaluate.Name).
					Scan(&children); queryErr != nil {
					t.Fatal(queryErr)
				}
				if readErr != nil || mapErr != nil || found || len(facts) != 0 || children != 0 || source.calls != 0 || w.delivery(t, "no-users").State != intake.Received {
					t.Fatal("new-user refusal leaked lifecycle/facts/children/settlement or read provider inline", facts, children, found, source.calls, readErr, mapErr)
				}
				if dead, err := w.queue.FailAfter(t.Context(), job, err, 0); err != nil || dead {
					t.Fatal("new-user refusal was not retryable", dead, err)
				}
				deps.Dispatcher = dispatcher
				w.svc = intake.New(deps)
				job = w.claim(t, w.queue, "retried-parent")
				if job.Attempt != 2 {
					t.Fatal("proof did not run a genuine second queue attempt", job.Attempt)
				}
			}
			if err := w.svc.Processed(t.Context(), job); err != nil {
				t.Fatal(err)
			}
			delivery := w.delivery(t, "no-users")
			mapped, found, err := w.f.store.RepositoryIdentity(t.Context(), w.f.acct, w.id, repo.ID)
			if err != nil || !found || mapped.Repository != repo || delivery.State != intake.Dispatched {
				t.Fatal("no-user/retried delivery lost native identity or completion", mapped, delivery.State, err)
			}
			facts, err := w.f.store.Prepared(t.Context(), w.f.acct, delivery.ID)
			var children int
			if queryErr := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE account_id=$1 AND kind=$2`, w.f.acct, push.Evaluate.Name).
				Scan(&children); queryErr != nil {
				t.Fatal(queryErr)
			}
			want := 0
			if appeared {
				want = 1
			}
			if err != nil || len(facts) != want || children != want || source.calls != want {
				t.Fatal("completion lost facts/children or did unnecessary/repeated provider work", len(facts), children, source.calls, err)
			}
		})
	}
}
