package pg_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/push"
)

type lifecycleTargets struct {
	stacksapi.Stacks
	f           *fixture
	integration kernel.ULID
	stack       kernel.ULID
	mu          sync.Mutex
	moves       []stacksapi.VCSRepository
	err         error
}

func (s *lifecycleTargets) RepointVCSRepository(ctx context.Context, acct kernel.AccountID, use stacksapi.Use, from, to stacksapi.VCSRepository) error {
	if !tx.In(ctx) || acct != s.f.acct || use.Integration != s.integration {
		return errors.New("repoint lost transaction/binding")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.moves = append(s.moves, to)
	return s.err
}
func (s *lifecycleTargets) ByVCSRepository(ctx context.Context, acct kernel.AccountID, use stacksapi.Use, namespace, name string) ([]stacksapi.Stack, error) {
	if !tx.In(ctx) || acct != s.f.acct || use.Integration != s.integration {
		return nil, errors.New("affected stack lookup preceded completing transaction")
	}
	// The account/integration map already contains the event's new coordinates
	// when the affected-stack lookup runs, including on the renamed push itself.
	repo, found, err := s.f.store.RepositoryIdentity(ctx, acct, s.integration, "7")
	if err != nil {
		return nil, err
	}
	if !found || repo.Namespace != namespace || repo.Name != name {
		return nil, errors.New("fanout lookup preceded repository repoint")
	}
	return []stacksapi.Stack{{ID: s.stack}}, nil
}

func TestAccRepositoryRenameCommitsBeforeFanout(t *testing.T) {
	for _, fault := range []string{"none", "completion rollback", "repoint refusal"} {
		t.Run(fault, func(t *testing.T) {
			w := newWebhookWorld(t)
			targets := &lifecycleTargets{f: w.f, integration: w.id, stack: kernel.NewULID()}
			now := time.Now().UTC().Add(-time.Minute)
			native := &lifecycle.Service{DB: w.f.db, Store: w.f.store, Stacks: targets, Now: func() time.Time { return now }}
			use := stacksapi.Use{Integration: w.id, Provider: "GITHUB_ENTERPRISE"}
			if err := native.Seed(t.Context(), w.f.acct, w.id, use, vcsapi.Repository{ID: "7", Namespace: "acme", Name: "infra"}); err != nil {
				t.Fatal(err)
			}
			events := []vcsapi.Event{{Type: "push", Repository: vcsapi.Repository{ID: "7", Namespace: "new-owner", Name: "renamed"}, Branch: "main", Before: strings.Repeat("a", 40), Head: strings.Repeat("b", 40)}}
			source := &dispatchSource{}
			deps := w.deps
			deps.Providers["GITHUB_ENTERPRISE"] = dispatchProvider{events: events}
			deps.Dispatcher = push.New(push.Deps{DB: w.f.db, Store: w.f.store, Stacks: targets, Integrations: dispatchIntegration{w.id}, Sources: source, Jobs: w.queue, Lifecycle: native})
			cause := errors.New("completion refused")
			if fault == "completion rollback" {
				deps.Jobs = refusedDispatchCompletion{Queue: w.queue, err: cause}
			}
			if fault == "repoint refusal" {
				targets.err = cause
			}
			w.svc = intake.New(deps)
			if status := w.receive(t, "rename", webhookBody(128)); status != 202 {
				t.Fatal(status)
			}
			job := w.claim(t, w.queue, "rename-parent")
			err := w.svc.Processed(t.Context(), job)
			if fault == "none" && err != nil || fault != "none" && !errors.Is(err, cause) {
				t.Fatal("wrong completion", err)
			}
			mapped, found, err := w.f.store.RepositoryIdentity(t.Context(), w.f.acct, w.id, "7")
			if err != nil || !found {
				t.Fatal(mapped, found, err)
			}
			delivery := w.delivery(t, "rename")
			prepared, err := w.f.store.Prepared(t.Context(), w.f.acct, delivery.ID)
			if err != nil {
				t.Fatal(err)
			}
			var children int
			if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE account_id=$1 AND kind=$2`, w.f.acct, push.Evaluate.Name).Scan(&children); err != nil {
				t.Fatal(err)
			}
			if fault != "none" {
				if mapped.Name != "infra" || mapped.Namespace != "acme" || len(prepared) != 0 || children != 0 || delivery.State != intake.Received {
					t.Fatal("failed completion leaked map/prepared/children/state", mapped, len(prepared), children, delivery.State)
				}
				return
			}
			if mapped.Name != "renamed" || mapped.Namespace != "new-owner" || len(prepared) != 1 || children != 1 || source.calls != 1 || delivery.State != intake.Dispatched {
				t.Fatal(mapped, len(prepared), children, source.calls, delivery.State)
			}
			if len(targets.moves) != 1 || targets.moves[0].Name != "renamed" {
				t.Fatal("repoint lost", targets.moves)
			}
		})
	}
}

func TestAccRepositoryMapIsolationCollisionAndPurge(t *testing.T) {
	w := newWebhookWorld(t)
	targets := &lifecycleTargets{f: w.f, integration: w.id}
	now := time.Now().UTC().Truncate(time.Microsecond)
	native := &lifecycle.Service{DB: w.f.db, Store: w.f.store, Stacks: targets, Now: func() time.Time { return now }}
	use := stacksapi.Use{Integration: w.id, Provider: "GITHUB_ENTERPRISE"}
	for _, repo := range []vcsapi.Repository{{ID: "7", Namespace: "acme", Name: "infra"}, {ID: "8", Namespace: "acme", Name: "other"}} {
		if err := native.Seed(t.Context(), w.f.acct, w.id, use, repo); err != nil {
			t.Fatal(err)
		}
	}
	apply := func(at time.Time, events ...vcsapi.Event) error {
		return tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return native.Apply(ctx, w.f.acct, w.id, use, at, events) })
	}
	if err := apply(now, vcsapi.Event{Repository: vcsapi.Repository{ID: "7", Namespace: "acme", Name: "other"}}, vcsapi.Event{Repository: vcsapi.Repository{ID: "8", Namespace: "acme", Name: "infra"}}); err != nil {
		t.Fatal(err)
	}
	r, _, err := w.f.store.RepositoryIdentity(t.Context(), w.f.acct, w.id, "7")
	if err != nil || r.Name != "infra" || len(targets.moves) != 0 {
		t.Fatal("occupied path was reassigned", r, err)
	}
	for _, acct := range []kernel.AccountID{kernel.NewAccountID(), w.f.acct} {
		integration := w.id
		if acct == w.f.acct {
			integration = kernel.NewULID()
		}
		if _, found, err := w.f.store.RepositoryIdentity(t.Context(), acct, integration, "7"); err != nil || found {
			t.Fatal("identity crossed account/integration", found, err)
		}
	}
	// Two independent connections serialize on the integration row; a late old
	// delivery cannot reverse the newer native observation.
	errs := make(chan error, 2)
	for i := range 2 {
		go func() {
			name := fmt.Sprintf("new-%d", i)
			errs <- apply(now.Add(time.Duration(i+1)*time.Second), vcsapi.Event{Repository: vcsapi.Repository{ID: "7", Namespace: "acme", Name: name}})
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	r, _, err = w.f.store.RepositoryIdentity(t.Context(), w.f.acct, w.id, "7")
	if err != nil || r.Name != "new-1" {
		t.Fatal("stale observation won", r, err)
	}
	install := vcsapi.Installation{ID: "71", Action: "created", Owner: vcsapi.Owner{ID: "41", Login: "acme"}, Repositories: []vcsapi.Repository{{ID: "7", Namespace: "acme", Name: "new-1"}}}
	if err := apply(now.Add(3*time.Second), vcsapi.Event{Installation: &install}); err != nil {
		t.Fatal(err)
	}
	install.Action = "removed"
	install.Removed = install.Repositories
	install.Repositories = nil
	if err := apply(now.Add(4*time.Second), vcsapi.Event{Installation: &install}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := w.f.store.RepositoryIdentity(t.Context(), w.f.acct, w.id, "7"); err != nil || found {
		t.Fatal("removed map persisted", found, err)
	}
	if err := native.Seed(t.Context(), w.f.acct, w.id, use, vcsapi.Repository{ID: "7", Namespace: "acme", Name: "new-1"}); err == nil {
		t.Fatal("track restored removed repository")
	}
	if _, err := w.f.db.Exec(t.Context(), `DELETE FROM vcs_integrations WHERE account_id=$1 AND id=$2`, w.f.acct, w.id); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"vcs_repository_map", "vcs_installations", "vcs_installation_repositories"} {
		var count int
		if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM `+table+` WHERE account_id=$1 AND integration_id=$2`, w.f.acct, w.id).Scan(&count); err != nil || count != 0 {
			t.Fatal("integration deletion leaked lifecycle rows", table, count, err)
		}
	}
	if _, err := w.f.store.HoldRepositoryIntegration(t.Context(), w.f.acct, w.id); !errors.Is(err, queue.ErrNoTransaction) {
		t.Fatal(err)
	}
}
