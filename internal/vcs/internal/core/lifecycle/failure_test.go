package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var errStorage = errors.New("lifecycle storage unavailable")

// failingStore injects a named boundary failure without changing the effective
// native facts. The real-PG completion test proves SQL rollback separately.
type failingStore struct {
	Store
	operation string
	remaining int
}

func (f *failingStore) fail(operation string) error {
	if operation != f.operation {
		return nil
	}
	f.remaining--
	if f.remaining <= 0 {
		return errStorage
	}
	return nil
}

func (f *failingStore) HoldRepositoryIntegration(ctx context.Context, a kernel.AccountID, i kernel.ULID) (bool, error) {
	if err := f.fail("hold"); err != nil {
		return false, err
	}
	return f.Store.HoldRepositoryIntegration(ctx, a, i)
}

func (f *failingStore) RepositoryIdentity(ctx context.Context, a kernel.AccountID, i kernel.ULID, native string) (Repository, bool, error) {
	if err := f.fail("identity"); err != nil {
		return Repository{}, false, err
	}
	return f.Store.RepositoryIdentity(ctx, a, i, native)
}

func (f *failingStore) RepositoryPath(ctx context.Context, a kernel.AccountID, i kernel.ULID, ns, name string) (Repository, bool, error) {
	if err := f.fail("path"); err != nil {
		return Repository{}, false, err
	}
	return f.Store.RepositoryPath(ctx, a, i, ns, name)
}

func (f *failingStore) OwnerRepositories(ctx context.Context, a kernel.AccountID, i kernel.ULID, owner, previous string) ([]Repository, error) {
	if err := f.fail("owner"); err != nil {
		return nil, err
	}
	return f.Store.OwnerRepositories(ctx, a, i, owner, previous)
}

func (f *failingStore) PutRepositoryIdentity(ctx context.Context, a kernel.AccountID, i kernel.ULID, r Repository) error {
	if err := f.fail("put"); err != nil {
		return err
	}
	return f.Store.PutRepositoryIdentity(ctx, a, i, r)
}

func (f *failingStore) DropRepositoryIdentity(ctx context.Context, a kernel.AccountID, i kernel.ULID, native string) error {
	if err := f.fail("drop"); err != nil {
		return err
	}
	return f.Store.DropRepositoryIdentity(ctx, a, i, native)
}

func (f *failingStore) Installations(ctx context.Context, a kernel.AccountID, i kernel.ULID) ([]Installation, error) {
	if err := f.fail("installations"); err != nil {
		return nil, err
	}
	return f.Store.Installations(ctx, a, i)
}

func (f *failingStore) PutInstallation(ctx context.Context, a kernel.AccountID, i kernel.ULID, in Installation) error {
	if err := f.fail("put-installation"); err != nil {
		return err
	}
	return f.Store.PutInstallation(ctx, a, i, in)
}

func (f *failingStore) PutMembership(ctx context.Context, a kernel.AccountID, i kernel.ULID, installation string, m Membership) (bool, error) {
	if err := f.fail("membership"); err != nil {
		return false, err
	}
	return f.Store.PutMembership(ctx, a, i, installation, m)
}

func (f *failingStore) MembershipIdentity(ctx context.Context, a kernel.AccountID, i kernel.ULID, native string) (Membership, bool, error) {
	if err := f.fail("member-identity"); err != nil {
		return Membership{}, false, err
	}
	return f.Store.MembershipIdentity(ctx, a, i, native)
}

func (f *failingStore) MembershipRemoved(ctx context.Context, a kernel.AccountID, i kernel.ULID, ns, name, native string) (bool, error) {
	if err := f.fail("removed"); err != nil {
		return false, err
	}
	return f.Store.MembershipRemoved(ctx, a, i, ns, name, native)
}

func (f *failingStore) MoveMembership(ctx context.Context, a kernel.AccountID, i kernel.ULID, r Repository) error {
	if err := f.fail("move"); err != nil {
		return err
	}
	return f.Store.MoveMembership(ctx, a, i, r)
}

func (f *failingStore) OwnerMemberships(ctx context.Context, a kernel.AccountID, i kernel.ULID, owner, previous string) ([]Membership, error) {
	if err := f.fail("owner-members"); err != nil {
		return nil, err
	}
	return f.Store.OwnerMemberships(ctx, a, i, owner, previous)
}

func TestUnitLifecycleBoundaryFailuresRemainFailures(t *testing.T) {
	cases := []struct {
		name       string
		operations []string
		run        func(*testing.T, *world) error
	}{
		{"check", []string{"removed", "path", "installations"}, func(t *testing.T, w *world) error { return w.s.Check(t.Context(), w.acct, w.id, "old", "infra") }},
		{"seed", []string{"hold", "removed", "path", "installations", "identity", "put", "move"}, func(t *testing.T, w *world) error {
			return w.s.SeedAt(t.Context(), w.acct, w.id, w.use, repo("1", "new", "infra"), w.now)
		}},
		{"observation", []string{"hold", "removed", "identity", "path", "put", "move"}, func(t *testing.T, w *world) error {
			return w.apply(t, vcsapi.Event{Repository: repo("1", "new", "infra")})
		}},
		{"owner", []string{"hold", "owner-members", "owner", "identity", "path", "put", "move", "installations", "put-installation"}, func(t *testing.T, w *world) error {
			return w.apply(t, vcsapi.Event{OwnerRename: &vcsapi.OwnerRename{ID: "41", PreviousLogin: "old", Login: "new"}})
		}},
		{
			"installation",
			[]string{"hold", "installations", "put-installation", "membership", "member-identity", "identity", "path", "put", "move"},
			func(t *testing.T, w *world) error {
				return w.apply(
					t,
					vcsapi.Event{
						Installation: &vcsapi.Installation{
							ID:           "71",
							Owner:        vcsapi.Owner{ID: "41", Login: "old"},
							Action:       "created",
							Repositories: []vcsapi.Repository{repo("1", "old", "infra")},
						},
					},
				)
			},
		},
		{"removal", []string{"hold", "installations", "put-installation", "membership", "member-identity", "drop"}, func(t *testing.T, w *world) error {
			return w.apply(
				t,
				vcsapi.Event{
					Installation: &vcsapi.Installation{
						ID:      "71",
						Owner:   vcsapi.Owner{ID: "41", Login: "old"},
						Action:  "removed",
						Removed: []vcsapi.Repository{repo("1", "old", "infra")},
					},
				},
			)
		}},
		{"refresh", []string{"hold", "installations", "put-installation"}, func(t *testing.T, w *world) error {
			_, err := w.s.Refresh(t.Context(), w.acct, w.id, w.now, nil)
			return err
		}},
		{"current-path", []string{"identity"}, func(t *testing.T, w *world) error {
			_, err := w.s.EventRepository(t.Context(), w.acct, w.id, w.now, repo("1", "old", "infra"))
			return err
		}},
	}
	for _, tc := range cases {
		for _, operation := range tc.operations {
			t.Run(tc.name+"/"+operation, func(t *testing.T) {
				w := newWorld()
				r := repo("1", "old", "infra")
				r.OwnerID = "41"
				w.store.repos["1"] = Repository{Repository: r, ObservedAt: w.now.Add(-time.Minute)}
				w.store.installs["71"] = Installation{ID: "71", Owner: vcsapi.Owner{ID: "41", Login: "old"}, State: "active", Current: true, ObservedAt: w.now.Add(-time.Minute)}
				w.s.Store = &failingStore{Store: w.store, operation: operation, remaining: 1}
				if err := tc.run(t, w); !errors.Is(err, errStorage) {
					t.Fatal("boundary error was hidden/replaced", err)
				}
			})
		}
	}
}

func TestUnitRemovedRepositoryOwnerRename(t *testing.T) {
	for _, fault := range []string{"none", "path", "move", "repoint"} {
		t.Run(fault, func(t *testing.T) {
			w := newWorld()
			r := repo("1", "old", "infra")
			r.OwnerID = "41"
			w.store.members["1"] = member{repo: Repository{Repository: r, ObservedAt: w.now.Add(-time.Minute)}, present: false, installation: "71"}
			w.store.installs["71"] = Installation{ID: "71", Owner: vcsapi.Owner{ID: "41", Login: "old"}, State: "active", Current: true, ObservedAt: w.now.Add(-time.Minute)}
			if fault == "repoint" {
				w.stacks.err = errStorage
			} else if fault != "none" {
				w.s.Store = &failingStore{Store: w.store, operation: fault, remaining: 1}
			}
			err := w.apply(t, vcsapi.Event{OwnerRename: &vcsapi.OwnerRename{ID: "41", PreviousLogin: "old", Login: "new"}})
			if fault != "none" {
				if !errors.Is(err, errStorage) {
					t.Fatal(err)
				}
				return
			}
			if err != nil || len(w.store.repos) != 0 || len(w.stacks.moves) != 1 {
				t.Fatal("removed repository was restored or left behind", err, w.store.repos, w.stacks.moves)
			}
			if err := w.s.Check(t.Context(), w.acct, w.id, "new", "infra"); err == nil {
				t.Fatal("owner rename lost the removal tombstone")
			}
		})
	}
}

func TestUnitNativeIdentityControls(t *testing.T) {
	w := newWorld()
	if CanonicalID("{7BF80420-965B-4F23-90C0-EC114B064264}") != "7bf80420-965b-4f23-90c0-ec114b064264" {
		t.Fatal("native UUID spellings diverge")
	}
	if err := w.s.SeedAt(t.Context(), w.acct, w.id, w.use, vcsapi.Repository{}, w.now); err != nil {
		t.Fatal(err)
	}
	w.store.found = false
	if err := w.apply(t, vcsapi.Event{Repository: repo("1", "old", "infra")}); err != nil || len(w.store.repos) != 0 {
		t.Fatal("deleted integration mutated metadata", err)
	}
	if err := w.s.SeedAt(t.Context(), w.acct, w.id, w.use, repo("1", "old", "infra"), w.now); err != nil || len(w.store.repos) != 0 {
		t.Fatal(err)
	}
	if reason, err := w.s.Refresh(t.Context(), w.acct, w.id, w.now, nil); err != nil || reason != "" {
		t.Fatal(reason, err)
	}
	if out, err := w.s.EventRepository(t.Context(), w.acct, w.id, w.now, vcsapi.Repository{}); err != nil || out.ID != "" {
		t.Fatal(out, err)
	}
	// A refresh's second read must not turn a failed final availability check into
	// a successful test after it has written the installation snapshot.
	w = newWorld()
	w.s.Store = &failingStore{Store: w.store, operation: "installations", remaining: 2}
	if _, err := w.s.Refresh(
		t.Context(),
		w.acct,
		w.id,
		w.now,
		[]port.InstallationState{{ID: "71", Owner: vcsapi.Owner{ID: "41", Login: "old"}, State: "active"}},
	); !errors.Is(
		err,
		errStorage,
	) {
		t.Fatal(err)
	}
	w = newWorld()
	w.stacks.err = errStorage
	w.store.repos["1"] = Repository{Repository: repo("1", "old", "infra"), ObservedAt: w.now}
	if err := tx.Do(t.Context(), database{}, tx.Options{}, func(ctx context.Context) error {
		return w.s.observe(ctx, w.acct, w.id, w.use, repo("1", "new", "infra"), w.now)
	}); !errors.Is(err, errStorage) {
		t.Fatal(err)
	}
}
