package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitReviewReinstalledOwnerRecoversWithNewInstallationID(t *testing.T) {
	w := newWorld()
	owner := vcsapi.Owner{ID: "9", Login: "acme"}
	repo := vcsapi.Repository{ID: "7", OwnerID: owner.ID, Namespace: owner.Login, Name: "infra"}
	for _, event := range []vcsapi.Installation{
		{ID: "1", Owner: owner, Action: "created", Repositories: []vcsapi.Repository{repo}},
		{ID: "1", Owner: owner, Action: "deleted"},
		{ID: "2", Owner: owner, Action: "created", Repositories: []vcsapi.Repository{repo}},
	} {
		w.now = w.now.Add(time.Second)
		if err := w.apply(t, vcsapi.Event{Installation: &event}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.s.Check(t.Context(), w.acct, w.id, repo.Namespace, repo.Name); err != nil {
		t.Errorf("active replacement installation cannot resolve its repository: %v", err)
	}
	reason, err := w.s.Refresh(t.Context(), w.acct, w.id, w.now.Add(time.Second), []port.InstallationState{
		{ID: "2", Owner: owner, State: "active"},
	})
	if err != nil || reason != "" {
		t.Errorf("authoritative active replacement remains unhealthy: reason=%q err=%v", reason, err)
	}
}

func TestUnitReplacementIgnoresOldInstallationJobsAndMembership(t *testing.T) {
	w := newWorld()
	owner := vcsapi.Owner{ID: "9", Login: "acme"}
	repository := vcsapi.Repository{ID: "7", OwnerID: owner.ID, Namespace: owner.Login, Name: "infra"}
	apply := func(id, action string, removed bool) {
		t.Helper()
		w.now = w.now.Add(time.Second)
		event := vcsapi.Installation{ID: id, Owner: owner, Action: action}
		if removed {
			event.Removed = []vcsapi.Repository{repository}
		}
		if err := w.apply(t, vcsapi.Event{Installation: &event}); err != nil {
			t.Fatal(err)
		}
	}
	apply("1", "created", false)
	apply("1", "removed", true)
	apply("1", "deleted", false)
	// A native reinstall may not list repositories in its created event. The old
	// removal says nothing about the replacement's unknown membership.
	apply("2", "created", false)
	if err := w.s.Check(t.Context(), w.acct, w.id, repository.Namespace, repository.Name); err != nil {
		t.Fatal("old membership blocks replacement", err)
	}
	if err := w.s.SeedAt(t.Context(), w.acct, w.id, w.use, repository, w.now); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"suspend", "deleted", "removed", "unsuspend", "new_permissions_accepted", "created"} {
		apply("1", action, action == "removed")
		if err := w.s.Check(t.Context(), w.acct, w.id, repository.Namespace, repository.Name); err != nil {
			t.Fatal("old installation job poisoned replacement", action, err)
		}
		if _, found := w.store.repos["7"]; !found {
			t.Fatal("old removal dropped replacement map")
		}
		if !w.store.installs["2"].Current || w.store.installs["1"].Current {
			t.Fatal("old ID became current", w.store.installs)
		}
	}
}

func TestUnitFencedFanoutUsesIdentityAndCollisionResult(t *testing.T) {
	cases := []struct {
		name    string
		current Repository
		input   vcsapi.Repository
		allowed bool
		failure bool
	}{
		{name: "accepted", current: Repository{Repository: repo("7", "acme", "infra")}, input: repo("7", "acme", "infra"), allowed: true},
		{name: "collision", current: Repository{Repository: repo("7", "acme", "infra")}, input: repo("7", "acme", "occupied")},
		{name: "newer", current: Repository{Repository: repo("7", "platform", "renamed"), ObservedAt: time.Unix(1800000001, 0)}, input: repo("7", "acme", "infra"), allowed: true},
		{name: "missing", input: repo("7", "acme", "infra")},
		{name: "unavailable", input: repo("7", "acme", "infra"), failure: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld()
			if tc.current.ID != "" {
				w.store.repos["7"] = tc.current
			}
			if tc.failure {
				w.s.Store = &failingStore{Store: w.store, operation: "identity", remaining: 1}
			}
			var allowed bool
			err := tx.Do(t.Context(), database{}, tx.Options{}, func(ctx context.Context) error {
				_, ok, err := w.s.FanoutRepository(ctx, w.acct, w.id, w.now, tc.input)
				allowed = ok
				return err
			})
			if tc.failure {
				if !errors.Is(err, errStorage) {
					t.Fatal(err)
				}
				return
			}
			if err != nil || allowed != tc.allowed {
				t.Fatal(allowed, err)
			}
		})
	}
}

func TestUnitInstallationSelectionFences(t *testing.T) {
	owner := vcsapi.Owner{ID: "9", Login: "acme"}
	now := time.Unix(1800000000, 0)
	cases := []struct {
		name           string
		existing       []Installation
		id, action     string
		apply, current bool
		state          string
	}{
		{name: "first suspended", id: "1", action: "suspend", apply: true, current: true, state: "suspended"},
		{name: "first deletion", id: "1", action: "deleted", apply: true, current: true, state: "deleted"},
		{
			name:     "unknown old suspension",
			existing: []Installation{{ID: "2", Owner: owner, State: "active", Current: true, ObservedAt: now}},
			id:       "1",
			action:   "suspend",
			apply:    true,
			state:    "suspended",
		},
		{
			name:     "unknown old deletion",
			existing: []Installation{{ID: "2", Owner: owner, State: "active", Current: true, ObservedAt: now}},
			id:       "1",
			action:   "deleted",
			apply:    true,
			state:    "deleted",
		},
		{
			name:     "stale same ID",
			existing: []Installation{{ID: "1", Owner: owner, State: "suspended", Current: true, ObservedAt: now.Add(time.Minute)}},
			id:       "1",
			action:   "unsuspend",
			apply:    true, current: true, state: "suspended",
		},
		{
			name:     "stale replacement",
			existing: []Installation{{ID: "2", Owner: owner, State: "active", Current: true, ObservedAt: now.Add(time.Minute)}},
			id:       "1",
			action:   "created",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, apply := nextInstallation(tc.existing, vcsapi.Installation{ID: tc.id, Action: tc.action, Owner: owner}, now)
			if apply != tc.apply || apply && (next.Current != tc.current || next.State != tc.state) {
				t.Fatal(next, apply)
			}
		})
	}
}

func TestUnitLateSnapshotCannotReplaceNewerInstallation(t *testing.T) {
	w := newWorld()
	owner := vcsapi.Owner{ID: "9", Login: "acme"}
	w.store.installs["2"] = Installation{ID: "2", Owner: owner, State: "suspended", Current: true, ObservedAt: w.now.Add(time.Minute)}
	reason, err := w.s.Refresh(t.Context(), w.acct, w.id, w.now, []port.InstallationState{{ID: "1", Owner: owner, State: "active"}})
	if err != nil || reason != "The GitHub App installation on acme is suspended." || len(w.store.installs) != 1 || !w.store.installs["2"].Current {
		t.Fatal("late provider snapshot replaced newer installation identity", reason, err, w.store.installs)
	}
}
