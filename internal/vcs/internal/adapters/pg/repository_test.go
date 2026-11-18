package pg_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
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
			events := []vcsapi.Event{
				{
					Type:       "push",
					Repository: vcsapi.Repository{ID: "7", Namespace: "new-owner", Name: "renamed"},
					Branch:     "main",
					Before:     strings.Repeat("a", 40),
					Head:       strings.Repeat("b", 40),
				},
			}
			source := &dispatchSource{}
			deps := w.deps
			deps.Providers["GITHUB_ENTERPRISE"] = dispatchProvider{events: events}
			deps.Dispatcher = push.New(
				push.Deps{DB: w.f.db, Store: w.f.store, Stacks: targets, Integrations: dispatchIntegration{w.id}, Sources: source, Jobs: w.queue, Lifecycle: native},
			)
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
	if err := apply(
		now,
		vcsapi.Event{Repository: vcsapi.Repository{ID: "7", Namespace: "acme", Name: "other"}},
		vcsapi.Event{Repository: vcsapi.Repository{ID: "8", Namespace: "acme", Name: "infra"}},
	); err != nil {
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
	install := vcsapi.Installation{
		ID:           "71",
		Action:       "created",
		Owner:        vcsapi.Owner{ID: "41", Login: "acme"},
		Repositories: []vcsapi.Repository{{ID: "7", Namespace: "acme", Name: "new-1"}},
	}
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

type routedTargets struct {
	stacksapi.Stacks
	rows  []stacksapi.Stack
	reads int
}

func (s *routedTargets) ByVCSRepository(context.Context, kernel.AccountID, stacksapi.Use, string, string) ([]stacksapi.Stack, error) {
	s.reads++
	return s.rows, nil
}

func (*routedTargets) RepointVCSRepository(context.Context, kernel.AccountID, stacksapi.Use, stacksapi.VCSRepository, stacksapi.VCSRepository) error {
	return nil
}

func TestAccRepositoryCollisionNeverTargetsOccupant(t *testing.T) {
	w := newWebhookWorld(t)
	targets := &routedTargets{rows: []stacksapi.Stack{{ID: kernel.NewULID()}}}
	native := &lifecycle.Service{DB: w.f.db, Store: w.f.store, Stacks: targets, Now: func() time.Time { return time.Now().Add(-time.Minute) }}
	use := stacksapi.Use{Integration: w.id, Provider: "GITHUB_ENTERPRISE"}
	for _, repo := range []vcsapi.Repository{{ID: "7", Namespace: "acme", Name: "original"}, {ID: "8", Namespace: "acme", Name: "occupied"}} {
		if err := native.Seed(t.Context(), w.f.acct, w.id, use, repo); err != nil {
			t.Fatal(err)
		}
	}
	deps := w.deps
	deps.Providers["GITHUB_ENTERPRISE"] = dispatchProvider{
		events: []vcsapi.Event{{Type: "push", Repository: vcsapi.Repository{ID: "7", Namespace: "acme", Name: "occupied"}, Branch: "main", Head: strings.Repeat("b", 40)}},
	}
	deps.Dispatcher = push.New(
		push.Deps{DB: w.f.db, Store: w.f.store, Stacks: targets, Integrations: dispatchIntegration{w.id}, Sources: &dispatchSource{}, Jobs: w.queue, Lifecycle: native},
	)
	w.svc = intake.New(deps)
	if status := w.receive(t, "collision", webhookBody(128)); status != 202 {
		t.Fatal(status)
	}
	if err := w.svc.Processed(t.Context(), w.claim(t, w.queue, "collision-parent")); err != nil {
		t.Fatal(err)
	}
	var children int
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE account_id=$1 AND kind=$2`, w.f.acct, push.Evaluate.Name).Scan(&children); err != nil {
		t.Fatal(err)
	}
	if children != 0 || targets.reads != 0 {
		t.Fatal("refused rename targeted the occupying repository", children, targets.reads)
	}
	delivery := w.delivery(t, "collision")
	facts, err := w.f.store.Prepared(t.Context(), w.f.acct, delivery.ID)
	if err != nil || len(facts) != 0 || delivery.State != intake.Dispatched {
		t.Fatal("collision left runnable facts or failed parent settlement", facts, delivery.State, err)
	}
	for id, want := range map[string]string{"7": "original", "8": "occupied"} {
		current, _, err := w.f.store.RepositoryIdentity(t.Context(), w.f.acct, w.id, id)
		if err != nil || current.Name != want {
			t.Fatal("collision changed identity map", current, err)
		}
	}
}

type preparationBarrier struct {
	intake.Dispatcher
	ready   chan struct{}
	proceed chan struct{}
}

func (b preparationBarrier) Prepare(ctx context.Context, d intake.Delivery, e []vcsapi.Event) (intake.DispatchPlan, error) {
	plan, err := b.Dispatcher.Prepare(ctx, d, e)
	if err != nil {
		return nil, err
	}
	close(b.ready)
	select {
	case <-b.proceed:
		return plan, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

func TestAccNewerRenameBetweenPreparationAndCommit(t *testing.T) {
	w := newWebhookWorld(t)
	targets := &lifecycleTargets{f: w.f, integration: w.id, stack: kernel.NewULID()}
	now := time.Now().UTC().Add(-time.Minute)
	native := &lifecycle.Service{DB: w.f.db, Store: w.f.store, Stacks: targets, Now: func() time.Time { return now }}
	use := stacksapi.Use{Integration: w.id, Provider: "GITHUB_ENTERPRISE"}
	old := vcsapi.Repository{ID: "7", Namespace: "acme", Name: "original"}
	if err := native.Seed(t.Context(), w.f.acct, w.id, use, old); err != nil {
		t.Fatal(err)
	}
	source := &dispatchSource{}
	deps := w.deps
	deps.Providers["GITHUB_ENTERPRISE"] = dispatchProvider{events: []vcsapi.Event{{Type: "push", Repository: old, Branch: "main", Head: strings.Repeat("b", 40)}}}
	barrier := preparationBarrier{
		Dispatcher: push.New(push.Deps{DB: w.f.db, Store: w.f.store, Stacks: targets, Integrations: dispatchIntegration{w.id}, Sources: source, Jobs: w.queue, Lifecycle: native}),
		ready:      make(chan struct{}),
		proceed:    make(chan struct{}),
	}
	deps.Dispatcher = barrier
	w.svc = intake.New(deps)
	if status := w.receive(t, "concurrent-rename", webhookBody(128)); status != 202 {
		t.Fatal(status)
	}
	delivery := w.delivery(t, "concurrent-rename")
	job := w.claim(t, w.queue, "original-push")
	done := make(chan error, 1)
	go func() { done <- w.svc.Processed(t.Context(), job) }()
	select {
	case <-barrier.ready:
	case <-t.Context().Done():
		t.Fatal(context.Cause(t.Context()))
	}
	renamed := vcsapi.Repository{ID: "7", Namespace: "platform", Name: "renamed"}
	err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		return native.Apply(ctx, w.f.acct, w.id, use, delivery.ReceivedAt.Add(time.Second), []vcsapi.Event{{Repository: renamed}})
	})
	close(barrier.proceed)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	facts, err := w.f.store.Prepared(t.Context(), w.f.acct, delivery.ID)
	if err != nil || len(facts) != 1 || facts[0].Event.Repository != renamed || source.calls != 1 {
		t.Fatal("completion lost current identity or reread provider", facts, source.calls, err)
	}
	children, err := w.queue.Claim(t.Context(), queue.VCSInbox, []string{push.Evaluate.Name}, 2, time.Minute, "renamed-child")
	if err != nil || len(children) != 1 {
		t.Fatal("push lost after concurrent rename", children, err)
	}
}

func TestAccInstallationReplacementAndMembershipClocks(t *testing.T) {
	w := newWebhookWorld(t)
	targets := &routedTargets{}
	native := &lifecycle.Service{DB: w.f.db, Store: w.f.store, Stacks: targets, Now: time.Now}
	use := stacksapi.Use{Integration: w.id, Provider: "GITHUB_ENTERPRISE"}
	at := time.Now().UTC().Truncate(time.Microsecond)
	owner := vcsapi.Owner{ID: "41", Login: "acme"}
	repository := vcsapi.Repository{ID: "7", OwnerID: owner.ID, Namespace: owner.Login, Name: "infra"}
	apply := func(id, action string, offset time.Duration) {
		t.Helper()
		event := vcsapi.Installation{ID: id, Owner: owner, Action: action}
		switch action {
		case "created", "added":
			event.Added = []vcsapi.Repository{repository}
		case "removed":
			event.Removed = []vcsapi.Repository{repository}
		}
		if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
			return native.Apply(ctx, w.f.acct, w.id, use, at.Add(offset), []vcsapi.Event{{Installation: &event}})
		}); err != nil {
			t.Fatal(err)
		}
	}
	apply("1", "created", 0)
	// An availability-only API refresh is newer than a queued membership removal.
	if reason, err := native.Refresh(
		t.Context(),
		w.f.acct,
		w.id,
		at.Add(3*time.Second),
		[]port.InstallationState{{ID: "1", Owner: owner, State: "active"}},
	); err != nil ||
		reason != "" {
		t.Fatal(reason, err)
	}
	apply("1", "removed", 2*time.Second)
	if err := native.Check(t.Context(), w.f.acct, w.id, "acme", "infra"); err == nil {
		t.Fatal("state-only refresh suppressed queued removal")
	}
	apply("1", "added", 4*time.Second)
	apply("1", "removed", 2500*time.Millisecond)
	if err := native.Check(t.Context(), w.f.acct, w.id, "acme", "infra"); err != nil {
		t.Fatal("stale removal replaced newer add", err)
	}
	if _, found, err := w.f.store.RepositoryIdentity(t.Context(), w.f.acct, w.id, "7"); err != nil || !found {
		t.Fatal("stale removal deleted replacement map", found, err)
	}
	// Generic repository observations carry no membership fact. Their newer
	// map time must not mask this installation's queued removal.
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		return native.Apply(ctx, w.f.acct, w.id, use, at.Add(6*time.Second), []vcsapi.Event{{Repository: repository}})
	}); err != nil {
		t.Fatal(err)
	}
	apply("1", "removed", 5*time.Second)
	if _, found, err := w.f.store.RepositoryIdentity(t.Context(), w.f.acct, w.id, "7"); err != nil || found {
		t.Fatal("map-only observation masked removal", found, err)
	}
	apply("1", "deleted", 6*time.Second)
	// The replacement's created event need not contain repositories. Its unknown
	// membership must retain the native lookup instead of inheriting an old deny.
	event := vcsapi.Installation{ID: "2", Owner: owner, Action: "created"}
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		return native.Apply(ctx, w.f.acct, w.id, use, at.Add(7*time.Second), []vcsapi.Event{{Installation: &event}})
	}); err != nil {
		t.Fatal(err)
	}
	if err := native.Check(t.Context(), w.f.acct, w.id, "acme", "infra"); err != nil {
		t.Fatal("old-ID tombstone poisoned reinstall", err)
	}
	if err := native.SeedAt(t.Context(), w.f.acct, w.id, use, repository, at.Add(8*time.Second)); err != nil {
		t.Fatal(err)
	}
	for index, action := range []string{"suspend", "deleted", "removed", "unsuspend", "created"} {
		apply("1", action, time.Duration(index+9)*time.Second)
		if err := native.Check(t.Context(), w.f.acct, w.id, "acme", "infra"); err != nil {
			t.Fatal("superseded installation changed replacement availability", action, err)
		}
		if _, found, err := w.f.store.RepositoryIdentity(t.Context(), w.f.acct, w.id, "7"); err != nil || !found {
			t.Fatal("old removal dropped replacement map", found, err)
		}
	}
	if reason, err := native.Refresh(
		t.Context(),
		w.f.acct,
		w.id,
		at.Add(20*time.Second),
		[]port.InstallationState{{ID: "2", Owner: owner, State: "active"}},
	); err != nil ||
		reason != "" {
		t.Fatal("active native replacement remains unhealthy", reason, err)
	}
	installations, err := w.f.store.Installations(t.Context(), w.f.acct, w.id)
	if err != nil || len(installations) != 2 || installations[0].Current || !installations[1].Current {
		t.Fatal("current-owner identity did not persist", installations, err)
	}
}

func TestAccMembershipRestorationKeepsNewerCoordinates(t *testing.T) {
	w := newWebhookWorld(t)
	native := &lifecycle.Service{DB: w.f.db, Store: w.f.store, Stacks: &routedTargets{}, Now: time.Now}
	use := stacksapi.Use{Integration: w.id, Provider: "GITHUB_ENTERPRISE"}
	base := time.Now().UTC().Truncate(time.Microsecond)
	owner := vcsapi.Owner{ID: "9", Login: "old"}
	original := vcsapi.Repository{ID: "7", Namespace: owner.Login, Name: "infra", OwnerID: owner.ID}
	apply := func(offset time.Duration, event vcsapi.Event) {
		t.Helper()
		if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
			return native.Apply(ctx, w.f.acct, w.id, use, base.Add(offset), []vcsapi.Event{event})
		}); err != nil {
			t.Fatal(err)
		}
	}
	apply(0, vcsapi.Event{Installation: &vcsapi.Installation{ID: "1", Owner: owner, Action: "created", Repositories: []vcsapi.Repository{original}}})
	apply(time.Second, vcsapi.Event{Installation: &vcsapi.Installation{ID: "1", Owner: owner, Action: "removed", Removed: []vcsapi.Repository{original}}})
	apply(3*time.Second, vcsapi.Event{OwnerRename: &vcsapi.OwnerRename{ID: owner.ID, PreviousLogin: "old", Login: "new"}})
	tombstone, found, err := w.f.store.MembershipIdentity(t.Context(), w.f.acct, w.id, original.ID)
	if err != nil || !found || tombstone.Namespace != "new" || tombstone.Present || !tombstone.ObservedAt.Equal(base.Add(time.Second)) ||
		!tombstone.CoordinatesAt.Equal(base.Add(3*time.Second)) {
		t.Fatal("rename lost independent tombstone clocks", tombstone, found, err)
	}
	apply(2*time.Second, vcsapi.Event{Installation: &vcsapi.Installation{ID: "1", Owner: owner, Action: "added", Added: []vcsapi.Repository{original}}})
	mapped, found, err := w.f.store.RepositoryIdentity(t.Context(), w.f.acct, w.id, original.ID)
	if err != nil || !found || mapped.Namespace != "new" || !mapped.ObservedAt.Equal(base.Add(3*time.Second)) {
		t.Fatal("delayed add restored obsolete source coordinates", mapped, found, err)
	}
	member, found, err := w.f.store.MembershipIdentity(t.Context(), w.f.acct, w.id, original.ID)
	if err != nil || !found || member.Namespace != "new" || !member.Present || !member.ObservedAt.Equal(base.Add(2*time.Second)) ||
		!member.CoordinatesAt.Equal(base.Add(3*time.Second)) {
		t.Fatal("presence and coordinate freshness were conflated", member, found, err)
	}
	if err := native.Check(t.Context(), w.f.acct, w.id, "new", "infra"); err != nil {
		t.Fatal("restored membership remains unavailable", err)
	}
	current, err := native.EventRepository(t.Context(), w.f.acct, w.id, base.Add(2*time.Second), original)
	if err != nil || current.Namespace != "new" {
		t.Fatal("old retained event lost current route", current, err)
	}
}

func TestAccObservedInstallationCanLaterActivate(t *testing.T) {
	w := newWebhookWorld(t)
	base := time.Now().UTC().Truncate(time.Microsecond)
	native := &lifecycle.Service{DB: w.f.db, Store: w.f.store, Stacks: &routedTargets{}, Now: time.Now}
	use := stacksapi.Use{Integration: w.id, Provider: "GITHUB_ENTERPRISE"}
	owner := vcsapi.Owner{ID: "9", Login: "acme"}
	repository := vcsapi.Repository{ID: "7", Namespace: owner.Login, Name: "infra", OwnerID: owner.ID}
	steps := []vcsapi.Installation{
		{ID: "1", Owner: owner, Action: "deleted"},
		{ID: "2", Owner: owner, Action: "added", Added: []vcsapi.Repository{repository}},
		{ID: "2", Owner: owner, Action: "created", Repositories: []vcsapi.Repository{repository}},
	}
	for n, event := range steps {
		if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
			return native.Apply(ctx, w.f.acct, w.id, use, base.Add(time.Duration(n)*time.Second), []vcsapi.Event{{Installation: &event}})
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := native.Check(t.Context(), w.f.acct, w.id, "acme", "infra"); err != nil {
		t.Fatal("observed new ID remained blocked", err)
	}
	states, err := w.f.store.Installations(t.Context(), w.f.acct, w.id)
	if err != nil || len(states) != 2 || states[0].Current || !states[0].Superseded || !states[1].Current || states[1].Superseded {
		t.Fatal("pending/superseded identity did not round trip", states, err)
	}
}

func TestAccGitHubCaseAliasesRetainKnownUnavailability(t *testing.T) {
	for _, action := range []string{"suspend", "deleted", "removed"} {
		t.Run(action, func(t *testing.T) {
			w := newWebhookWorld(t)
			base := time.Now().UTC().Truncate(time.Microsecond)
			native := &lifecycle.Service{DB: w.f.db, Store: w.f.store, Stacks: &routedTargets{}, Now: time.Now}
			use := stacksapi.Use{Integration: w.id, Provider: "GITHUB_ENTERPRISE"}
			owner := vcsapi.Owner{ID: "9", Login: "Acme"}
			repository := vcsapi.Repository{ID: "7", OwnerID: owner.ID, Namespace: owner.Login, Name: "Infra"}
			events := []vcsapi.Installation{{ID: "1", Owner: owner, Action: "created", Repositories: []vcsapi.Repository{repository}}, {ID: "1", Owner: owner, Action: action}}
			if action == "removed" {
				events[1].Removed = []vcsapi.Repository{repository}
			}
			for n, event := range events {
				if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
					return native.Apply(ctx, w.f.acct, w.id, use, base.Add(time.Duration(n)*time.Second), []vcsapi.Event{{Installation: &event}})
				}); err != nil {
					t.Fatal(err)
				}
			}
			for _, path := range [][2]string{{"Acme", "Infra"}, {"ACME", "INFRA"}, {"acme", "infra"}} {
				if err := native.Check(t.Context(), w.f.acct, w.id, path[0], path[1]); err == nil {
					t.Fatal("GitHub case alias bypassed durable unavailability", action, path)
				}
			}
			// Case folding is limited to App installation knowledge. Native repository
			// path indexing remains exact for case-sensitive providers such as GitLab.
			if _, found, err := w.f.store.RepositoryPath(t.Context(), w.f.acct, w.id, "ACME", "INFRA"); err != nil || found {
				t.Fatal("repository map became case insensitive", found, err)
			}
		})
	}
}

func TestAccSourceAliasAssociationIsAtomic(t *testing.T) {
	for _, fault := range []string{"none", "requested occupied", "canonical occupied", "repoint failure"} {
		t.Run(fault, func(t *testing.T) {
			w := newWebhookWorld(t)
			targets := &lifecycleTargets{f: w.f, integration: w.id}
			native := &lifecycle.Service{DB: w.f.db, Store: w.f.store, Stacks: targets, Now: time.Now}
			use := stacksapi.Use{Integration: w.id, Provider: "GITHUB_ENTERPRISE"}
			requested := stacksapi.VCSRepository{Namespace: "alias", Name: "before"}
			resolved := vcsapi.Repository{ID: "7", Namespace: "canonical", Name: "renamed", OwnerID: "9"}
			if strings.Contains(fault, "occupied") {
				ns, name := requested.Namespace, requested.Name
				if fault == "canonical occupied" {
					ns, name = resolved.Namespace, resolved.Name
				}
				if err := native.Seed(t.Context(), w.f.acct, w.id, use, vcsapi.Repository{ID: "8", Namespace: ns, Name: name}); err != nil {
					t.Fatal(err)
				}
			}
			cause := errors.New("alias repoint unavailable")
			if fault == "repoint failure" {
				targets.err = cause
			}
			err := native.SeedReferenceAt(t.Context(), w.f.acct, w.id, use, requested, resolved, time.Now())
			if fault == "repoint failure" && !errors.Is(err, cause) || fault != "repoint failure" && err != nil {
				t.Fatal(err)
			}
			mapped, found, err := w.f.store.RepositoryIdentity(t.Context(), w.f.acct, w.id, "7")
			if err != nil {
				t.Fatal(err)
			}
			if fault == "none" {
				if !found || mapped.Namespace != "canonical" || len(targets.moves) != 1 {
					t.Fatal("alias was stranded", mapped, found, targets.moves)
				}
				return
			}
			if found {
				t.Fatal("refused/failed alias leaked native map", fault, mapped)
			}
			if strings.Contains(fault, "occupied") && len(targets.moves) != 0 {
				t.Fatal("occupied alias was repointed", targets.moves)
			}
		})
	}
}

type (
	liveBindingRow struct {
		id, binding     kernel.ULID
		namespace, name string
	}
	liveBindingStacks struct {
		stacksapi.Stacks
		account                 kernel.AccountID
		rows                    []liveBindingRow
		renamedUse, selectedUse stacksapi.Use
	}
)

func (s *liveBindingStacks) RepointVCSRepository(ctx context.Context, acct kernel.AccountID, use stacksapi.Use, from, to stacksapi.VCSRepository) error {
	if !tx.In(ctx) || acct != s.account {
		return errDown
	}
	s.renamedUse = use
	for n, row := range s.rows {
		if (row.binding == use.Integration || row.binding.IsZero() && use.Default) && row.namespace == from.Namespace && row.name == from.Name {
			s.rows[n].namespace, s.rows[n].name = to.Namespace, to.Name
		}
	}
	return nil
}

func (s *liveBindingStacks) ByVCSRepository(ctx context.Context, acct kernel.AccountID, use stacksapi.Use, ns, name string) ([]stacksapi.Stack, error) {
	if !tx.In(ctx) || acct != s.account {
		return nil, errDown
	}
	s.selectedUse = use
	var out []stacksapi.Stack
	for _, row := range s.rows {
		if (row.binding == use.Integration || row.binding.IsZero() && use.Default) && row.namespace == ns && row.name == name {
			out = append(out, stacksapi.Stack{ID: row.id})
		}
	}
	return out, nil
}

func TestAccCompletingDeliveryReadsHeldCurrentDefault(t *testing.T) {
	for _, initial := range []bool{true, false} {
		t.Run(fmt.Sprintf("initial-default-%t", initial), func(t *testing.T) {
			w := newWebhookWorld(t)
			other := kernel.NewULID()
			now := time.Now().UTC()
			if err := w.f.store.Create(
				t.Context(),
				integration.Integration{
					Account:   w.f.acct,
					ID:        other,
					Provider:  "GITHUB_ENTERPRISE",
					Slug:      "other",
					Name:      "Other",
					Space:     w.f.acct.ULID,
					Labels:    []string{},
					Checks:    integration.Checks{Mode: "INDIVIDUAL"},
					CreatedAt: now,
					UpdatedAt: now,
					Version:   1,
				},
				integration.Secrets{},
			); err != nil {
				t.Fatal(err)
			}
			setDefault := func(id kernel.ULID) error {
				return tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
					if _, err := tx.Q(ctx, w.f.db).
						Exec(ctx, `UPDATE vcs_integrations SET is_default=false WHERE account_id=$1 AND provider='GITHUB_ENTERPRISE'`, w.f.acct); err != nil {
						return err
					}
					_, err := tx.Q(ctx, w.f.db).Exec(ctx, `UPDATE vcs_integrations SET is_default=true WHERE account_id=$1 AND id=$2`, w.f.acct, id)
					return err
				})
			}
			initialID, finalID := other, w.id
			if initial {
				initialID, finalID = w.id, other
			}
			if err := setDefault(initialID); err != nil {
				t.Fatal(err)
			}
			targets := &liveBindingStacks{
				account: w.f.acct,
				rows: []liveBindingRow{
					{id: kernel.NewULID(), namespace: "old", name: "infra"},
					{id: kernel.NewULID(), binding: w.id, namespace: "old", name: "infra"},
					{id: kernel.NewULID(), binding: other, namespace: "old", name: "infra"},
				},
			}
			native := &lifecycle.Service{DB: w.f.db, Store: w.f.store, Stacks: targets, Now: func() time.Time { return now.Add(-time.Minute) }}
			use, _, err := w.f.svc.EventUse(t.Context(), w.f.acct, w.id)
			if err != nil {
				t.Fatal(err)
			}
			if err := native.Seed(t.Context(), w.f.acct, w.id, use, vcsapi.Repository{ID: "7", Namespace: "old", Name: "infra"}); err != nil {
				t.Fatal(err)
			}
			source := &dispatchSource{}
			deps := w.deps
			deps.Providers["GITHUB_ENTERPRISE"] = dispatchProvider{
				events: []vcsapi.Event{{Type: "push", Repository: vcsapi.Repository{ID: "7", Namespace: "new", Name: "infra"}, Branch: "main", Head: strings.Repeat("b", 40)}},
			}
			barrier := preparationBarrier{
				Dispatcher: push.New(push.Deps{DB: w.f.db, Store: w.f.store, Stacks: targets, Integrations: w.f.svc, Sources: source, Jobs: w.queue, Lifecycle: native}),
				ready:      make(chan struct{}),
				proceed:    make(chan struct{}),
			}
			deps.Dispatcher = barrier
			w.svc = intake.New(deps)
			if status := w.receive(t, "binding-switch", webhookBody(128)); status != 202 {
				t.Fatal(status)
			}
			job := w.claim(t, w.queue, "binding-parent")
			done := make(chan error, 1)
			go func() { done <- w.svc.Processed(t.Context(), job) }()
			select {
			case <-barrier.ready:
			case <-t.Context().Done():
				t.Fatal(context.Cause(t.Context()))
			}
			err = setDefault(finalID)
			close(barrier.proceed)
			if err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			want := !initial
			if targets.renamedUse.Default != want || targets.selectedUse.Default != want {
				t.Fatal("rename/fanout used stale or different binding", targets.renamedUse, targets.selectedUse)
			}
			for n, row := range targets.rows {
				changed := n == 1 || n == 0 && want
				if (row.namespace == "new") != changed {
					t.Fatal("default switch crossed or missed stack binding", n, row)
				}
			}
			var children int
			if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE account_id=$1 AND kind=$2`, w.f.acct, push.Evaluate.Name).Scan(&children); err != nil {
				t.Fatal(err)
			}
			expected := 1
			if want {
				expected++
			}
			if children != expected || source.calls != 1 {
				t.Fatal("current binding lost fanout or repeated provider read", children, source.calls)
			}
		})
	}
}

func TestAccHeldEventUseFencesConcurrentBindingWrite(t *testing.T) {
	w := newWebhookWorld(t)
	// A store without a sealer proves this held projection reads metadata alone.
	metadata := integration.New(integration.Deps{Store: pg.New(w.f.db, nil)})
	if _, _, err := metadata.HeldEventUse(t.Context(), w.f.acct, w.id); !errors.Is(err, queue.ErrNoTransaction) {
		t.Fatal(err)
	}
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		if _, found, err := metadata.HeldEventUse(ctx, w.f.acct, w.id); err != nil || !found {
			return fmt.Errorf("held binding missing: %w", err)
		}
		blocked := tx.Do(t.Context(), w.f.db, tx.Options{NoRetry: true}, func(other context.Context) error {
			if _, err := tx.Q(other, w.f.db).Exec(other, `SET LOCAL lock_timeout='40ms'`); err != nil {
				return err
			}
			_, err := tx.Q(other, w.f.db).Exec(other, `UPDATE vcs_integrations SET is_default=NOT is_default WHERE account_id=$1 AND id=$2`, w.f.acct, w.id)
			return err
		})
		if cause, ok := errors.AsType[*pgconn.PgError](blocked); !ok || cause.Code != "55P03" {
			return fmt.Errorf("binding writer bypassed held row: %w", blocked)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.f.db.Exec(t.Context(), `UPDATE vcs_integrations SET is_default=true WHERE account_id=$1 AND id=$2`, w.f.acct, w.id); err != nil {
		t.Fatal("released binding stayed locked", err)
	}
}
