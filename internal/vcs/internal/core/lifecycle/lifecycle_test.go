package lifecycle

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type memoryStore struct {
	Store
	repos    map[string]Repository
	installs map[string]Installation
	members  map[string]member
	found    bool
	failure  error
}
type member struct {
	installation string
	repo         Repository
	present      bool
}

func (m *memoryStore) HoldRepositoryIntegration(context.Context, kernel.AccountID, kernel.ULID) (bool, error) {
	return m.found, m.failure
}

func (m *memoryStore) RepositoryIdentity(_ context.Context, _ kernel.AccountID, _ kernel.ULID, id string) (Repository, bool, error) {
	r, ok := m.repos[id]
	return r, ok, m.failure
}

func (m *memoryStore) RepositoryPath(_ context.Context, _ kernel.AccountID, _ kernel.ULID, namespace, name string) (Repository, bool, error) {
	for _, r := range m.repos {
		if r.Namespace == namespace && r.Name == name {
			return r, true, m.failure
		}
	}
	return Repository{}, false, m.failure
}

func (m *memoryStore) OwnerRepositories(_ context.Context, _ kernel.AccountID, _ kernel.ULID, id, previous string) ([]Repository, error) {
	var out []Repository
	for _, r := range m.repos {
		if r.OwnerID == id || r.OwnerID == "" && r.Namespace == previous {
			out = append(out, r)
		}
	}
	return out, m.failure
}

func (m *memoryStore) PutRepositoryIdentity(_ context.Context, _ kernel.AccountID, _ kernel.ULID, r Repository) error {
	m.repos[r.ID] = r
	return m.failure
}

func (m *memoryStore) DropRepositoryIdentity(_ context.Context, _ kernel.AccountID, _ kernel.ULID, id string) error {
	delete(m.repos, id)
	return m.failure
}

func (m *memoryStore) Installations(context.Context, kernel.AccountID, kernel.ULID) ([]Installation, error) {
	out := make([]Installation, 0, len(m.installs))
	for _, in := range m.installs {
		out = append(out, in)
	}
	slices.SortFunc(out, func(a, b Installation) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return out, m.failure
}

func (m *memoryStore) PutInstallation(_ context.Context, _ kernel.AccountID, _ kernel.ULID, in Installation) error {
	if in.Current {
		for id, old := range m.installs {
			if id != in.ID && old.Owner.ID == in.Owner.ID {
				old.Current = false
				m.installs[id] = old
			}
		}
	}
	m.installs[in.ID] = in
	return m.failure
}

func (m *memoryStore) PutMembership(_ context.Context, _ kernel.AccountID, _ kernel.ULID, installation string, r Repository, present bool) (bool, error) {
	if old, found := m.members[r.ID]; found && old.repo.ObservedAt.After(r.ObservedAt) {
		return false, nil
	}
	m.members[r.ID] = member{repo: r, present: present, installation: installation}
	return true, m.failure
}

func (m *memoryStore) MembershipRemoved(_ context.Context, _ kernel.AccountID, _ kernel.ULID, ns, name, native string) (bool, error) {
	for _, membership := range m.members {
		if (membership.repo.Namespace == ns && membership.repo.Name == name || native != "" && membership.repo.ID == native) && !membership.present &&
			m.installs[membership.installation].Current {
			return true, nil
		}
	}
	return false, m.failure
}

type (
	moved      struct{ from, to stacksapi.VCSRepository }
	stackStore struct {
		stacksapi.Stacks
		moves []moved
		err   error
	}
)

func (s *stackStore) RepointVCSRepository(ctx context.Context, _ kernel.AccountID, _ stacksapi.Use, from, to stacksapi.VCSRepository) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	s.moves = append(s.moves, moved{from, to})
	return s.err
}

type (
	database    struct{}
	transaction struct{ pgx.Tx }
)

func (database) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return transaction{}, nil }
func (transaction) Commit(context.Context) error                        { return nil }
func (transaction) Rollback(context.Context) error                      { return nil }

type world struct {
	s      *Service
	store  *memoryStore
	stacks *stackStore
	acct   kernel.AccountID
	id     kernel.ULID
	use    stacksapi.Use
	now    time.Time
}

func newWorld() *world {
	w := &world{acct: kernel.NewAccountID(), id: kernel.NewULID(), now: time.Unix(1800000000, 0)}
	w.store = &memoryStore{repos: map[string]Repository{}, installs: map[string]Installation{}, members: map[string]member{}, found: true}
	w.stacks = &stackStore{}
	w.use = stacksapi.Use{Integration: w.id, Provider: "GITHUB_ENTERPRISE", Default: true, Aliases: []string{"GITHUB"}}
	w.s = &Service{DB: database{}, Store: w.store, Stacks: w.stacks, Now: func() time.Time { return w.now }}
	return w
}

func (w *world) apply(t *testing.T, events ...vcsapi.Event) error {
	t.Helper()
	return tx.Do(t.Context(), database{}, tx.Options{}, func(ctx context.Context) error { return w.s.Apply(ctx, w.acct, w.id, w.use, w.now, events) })
}

func repo(id, ns, name string) vcsapi.Repository {
	return vcsapi.Repository{ID: id, Namespace: ns, Name: name}
}

func TestUnitRepositoryRenameAndCollision(t *testing.T) {
	w := newWorld()
	original := repo("1", "acme", "infra")
	if err := w.s.Seed(t.Context(), w.acct, w.id, w.use, original); err != nil {
		t.Fatal(err)
	}
	renamed := repo("1", "platform", "cloud")
	if err := w.apply(t, vcsapi.Event{Type: "push", Repository: renamed}); err != nil {
		t.Fatal(err)
	}
	if got := w.store.repos["1"]; got.Repository != renamed || len(w.stacks.moves) != 1 || w.stacks.moves[0].from.Name != "infra" {
		t.Fatal(got, w.stacks.moves)
	}
	if err := w.apply(t, vcsapi.Event{Type: "repository_renamed", Repository: renamed}); err != nil {
		t.Fatal(err)
	}
	if len(w.stacks.moves) != 1 {
		t.Fatal("duplicate event repointed twice")
	}
	// A true swap has two occupied destinations. Neither identity guesses a move.
	w.store.repos["2"] = Repository{Repository: repo("2", "platform", "other"), ObservedAt: w.now}
	if err := w.apply(t, vcsapi.Event{Repository: repo("1", "platform", "other")}, vcsapi.Event{Repository: repo("2", "platform", "cloud")}); err != nil {
		t.Fatal(err)
	}
	if w.store.repos["1"].Name != "cloud" || w.store.repos["2"].Name != "other" || len(w.stacks.moves) != 1 {
		t.Fatal("occupied path was reassigned", w.store.repos)
	}
	// An unambiguous move frees the old destination for a later observation.
	if err := w.apply(t, vcsapi.Event{Repository: repo("2", "platform", "free")}, vcsapi.Event{Repository: repo("1", "platform", "other")}); err != nil {
		t.Fatal(err)
	}
	if w.store.repos["1"].Name != "other" || w.store.repos["2"].Name != "free" {
		t.Fatal(w.store.repos)
	}
}

func TestUnitInstallationStateAndRemoval(t *testing.T) {
	for _, action := range []string{"created", "unsuspend", "new_permissions_accepted"} {
		t.Run(action, func(t *testing.T) {
			w := newWorld()
			r := repo("1", "acme", "infra")
			event := vcsapi.Event{
				Type:         "installation",
				Installation: &vcsapi.Installation{ID: "71", Action: action, Owner: vcsapi.Owner{ID: "41", Login: "acme"}, Repositories: []vcsapi.Repository{r}},
			}
			if err := w.apply(t, event); err != nil {
				t.Fatal(err)
			}
			if err := w.s.Check(t.Context(), w.acct, w.id, "acme", "infra"); err != nil {
				t.Fatal(err)
			}
			event.Installation.Action = "suspend"
			if err := w.apply(t, event); err != nil {
				t.Fatal(err)
			}
			if err := w.s.Check(t.Context(), w.acct, w.id, "acme", "infra"); err == nil || detail(err) != "The GitHub App installation on acme is suspended." {
				t.Fatal(err)
			}
			event.Installation.Action = "unsuspend"
			if err := w.apply(t, event); err != nil {
				t.Fatal(err)
			}
			event.Installation.Action = "removed"
			event.Installation.Repositories = nil
			event.Installation.Removed = []vcsapi.Repository{r}
			if err := w.apply(t, event); err != nil {
				t.Fatal(err)
			}
			if len(w.store.repos) != 0 {
				t.Fatal("removed repository remains mapped")
			}
			if err := w.s.Seed(t.Context(), w.acct, w.id, w.use, r); err == nil || len(w.store.repos) != 0 {
				t.Fatal("track restored removed membership", err)
			}
			event.Installation.Action = "added"
			event.Installation.Removed = nil
			event.Installation.Added = []vcsapi.Repository{r}
			if err := w.apply(t, event); err != nil {
				t.Fatal(err)
			}
			if err := w.s.Check(t.Context(), w.acct, w.id, "acme", "infra"); err != nil || len(w.store.repos) != 1 {
				t.Fatal(err)
			}
			event.Installation.Action = "deleted"
			event.Installation.Added = nil
			if err := w.apply(t, event); err != nil {
				t.Fatal(err)
			}
			if err := w.s.Check(t.Context(), w.acct, w.id, "acme", "infra"); err == nil || detail(err) != "The GitHub App installation on acme is deleted." {
				t.Fatal(err)
			}
			if err := w.s.Check(t.Context(), w.acct, w.id, "unknown", "infra"); err != nil {
				t.Fatal("unknown installation lost native fallback", err)
			}
		})
	}
}

func TestUnitOwnerRenameAndStaleObservation(t *testing.T) {
	w := newWorld()
	r := repo("1", "old", "infra")
	r.OwnerID = "41"
	if err := w.s.Seed(t.Context(), w.acct, w.id, w.use, r); err != nil {
		t.Fatal(err)
	}
	w.store.installs["71"] = Installation{ID: "71", Owner: vcsapi.Owner{ID: "41", Login: "old"}, State: "suspended", Current: true, ObservedAt: w.now}
	if err := w.apply(t, vcsapi.Event{OwnerRename: &vcsapi.OwnerRename{ID: "41", PreviousLogin: "old", Login: "new"}}); err != nil {
		t.Fatal(err)
	}
	if w.store.repos["1"].Namespace != "new" || w.store.installs["71"].Owner.Login != "new" {
		t.Fatal(w.store.repos, w.store.installs)
	}
	w.now = w.now.Add(-time.Minute)
	if err := w.apply(t, vcsapi.Event{Repository: r}); err != nil {
		t.Fatal(err)
	}
	if w.store.repos["1"].Namespace != "new" {
		t.Fatal("old delivery reversed rename")
	}
	if err := w.s.Apply(t.Context(), w.acct, w.id, w.use, w.now, nil); !errors.Is(err, queue.ErrNoTransaction) {
		t.Fatal(err)
	}
}

func TestUnitInstallationRefresh(t *testing.T) {
	w := newWorld()
	owner := vcsapi.Owner{ID: "41", Login: "acme"}
	w.store.installs["71"] = Installation{ID: "71", Owner: owner, State: "active", Current: true, ObservedAt: w.now.Add(-time.Minute)}
	reason, err := w.s.Refresh(t.Context(), w.acct, w.id, w.now, nil)
	if err != nil || reason != "The GitHub App installation on acme is deleted." {
		t.Fatal(reason, err)
	}
	reason, err = w.s.Refresh(t.Context(), w.acct, w.id, w.now, []port.InstallationState{{ID: "71", Owner: owner, State: "active"}})
	if err != nil || reason != "" {
		t.Fatal(reason, err)
	}
	w.store.installs["71"] = Installation{ID: "71", Owner: owner, State: "suspended", Current: true, ObservedAt: w.now.Add(time.Minute)}
	reason, err = w.s.Refresh(t.Context(), w.acct, w.id, w.now, []port.InstallationState{{ID: "71", Owner: owner, State: "active"}})
	if err != nil || reason != "The GitHub App installation on acme is suspended." {
		t.Fatal("stale API result overwrote newer webhook", reason, err)
	}
}

func (m *memoryStore) MoveMembership(_ context.Context, _ kernel.AccountID, _ kernel.ULID, r Repository) error {
	if member, ok := m.members[r.ID]; ok {
		observed := member.repo.ObservedAt
		member.repo = r
		member.repo.ObservedAt = observed
		m.members[r.ID] = member
	}
	return m.failure
}

func (m *memoryStore) OwnerMemberships(_ context.Context, _ kernel.AccountID, _ kernel.ULID, owner, previous string) ([]Membership, error) {
	var out []Membership
	for _, member := range m.members {
		if (member.repo.OwnerID == owner || member.repo.OwnerID == "" && member.repo.Namespace == previous) && m.installs[member.installation].Current {
			out = append(out, Membership{member.repo, member.present})
		}
	}
	return out, m.failure
}

func detail(err error) string {
	if invalid, ok := errors.AsType[*kernel.Invalid](err); ok {
		return invalid.Detail
	}
	return ""
}

func TestUnitOldDeliveryUsesCurrentNativePath(t *testing.T) {
	w := newWorld()
	old := repo("1", "acme", "infra")
	newer := repo("1", "platform", "renamed")
	w.store.repos["1"] = Repository{Repository: newer, ObservedAt: w.now}
	actual, err := w.s.EventRepository(t.Context(), w.acct, w.id, w.now.Add(-time.Second), old)
	if err != nil || actual != newer {
		t.Fatal("replayed source still uses old path", actual, err)
	}
	actual, err = w.s.EventRepository(t.Context(), w.acct, w.id, w.now.Add(time.Second), old)
	if err != nil || actual != old {
		t.Fatal("new native observation was replaced by stale map", actual, err)
	}
}
