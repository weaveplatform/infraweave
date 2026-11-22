package worker_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

func (s *store) Binding(_ context.Context, id kernel.ULID) (kernel.WorkerOwner, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.down("Binding"); err != nil {
		return kernel.WorkerOwner{}, false, err
	}
	owner, ok := s.bindings[id]
	return owner, ok, nil
}

func (s *store) Bind(_ context.Context, p pool.Pool, owner kernel.WorkerOwner) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.down("Bind"); err != nil {
		return err
	}
	for _, bound := range s.bindings {
		if bound == owner {
			return worker.ErrOwnership
		}
	}
	if s.bindings == nil {
		s.bindings = map[kernel.ULID]kernel.WorkerOwner{}
	}
	s.bindings[p.ID] = owner
	return nil
}

func (s *store) Unbind(_ context.Context, id kernel.ULID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.down("Unbind"); err != nil {
		return err
	}
	delete(s.bindings, id)
	return nil
}

func (s *store) ManagedPool(_ context.Context, owner kernel.WorkerOwner) (*kernel.ULID, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.down("ManagedPool"); err != nil {
		return nil, false, err
	}
	id, ok := s.managed[owner]
	return id, ok, nil
}

func (s *store) HasWorkers(_ context.Context, id kernel.ULID) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.down("HasWorkers"); err != nil {
		return false, err
	}
	for _, w := range s.workers {
		if w.Pool == id {
			return true, nil
		}
	}
	return false, nil
}

func (s *store) SlotWorker(_ context.Context, id kernel.ULID, slot int32) (worker.Worker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.down("SlotWorker"); err != nil {
		return worker.Worker{}, err
	}
	for _, w := range s.workers {
		if w.Pool == id && w.Slot != nil && *w.Slot == slot {
			return w, nil
		}
	}
	return worker.Worker{}, worker.ErrNotFound
}

// assignments answers whether runs remain on a pool's workers.
type assignments struct {
	active bool
	err    error
}

func (a assignments) ActivePool(context.Context, kernel.ULID) (bool, error) { return a.active, a.err }

var (
	ownerA = kernel.WorkerOwner{ClusterID: "cluster-a", Namespace: "workers", ResourceUID: "uid-a"}
	ownerB = kernel.WorkerOwner{ClusterID: "cluster-b", Namespace: "workers", ResourceUID: "uid-b"}
)

// proof is an ownership proof of the fixture's pool for owner.
func (f *fixture) proof(t *testing.T, owner any) string {
	t.Helper()
	return jws(t, f.key, f.cert, map[string]any{
		"pool": f.pool.ID.String(), "iat": time.Now().Unix(), "jti": kernel.NewULID().String(), "owner": owner,
	})
}

// owned registers slot as owner.
func (f *fixture) owned(t *testing.T, owner kernel.WorkerOwner, slot int) (worker.Registration, error) {
	t.Helper()
	return f.register(t, func(p map[string]any) { p["owner"], p["slot"] = owner, slot })
}

// TestUnitPoolBindingOwnsRegistrations checks that one owner binds a pool,
// that binding again is idempotent, that registrations then need that
// owner and resume each slot's worker, and that release waits for the
// workers and runs to go.
func TestUnitPoolBindingOwnsRegistrations(t *testing.T) {
	f := newFixture(t, worker.Deps{})
	f.svc.BindAssignments(assignments{})
	ctx := t.Context()
	if _, err := f.owned(t, ownerA, 0); !errors.Is(err, worker.ErrOwnership) {
		t.Fatalf("unbound owner registration = %v", err)
	}
	for range 2 {
		if err := f.svc.Bind(ctx, f.proof(t, ownerA)); err != nil {
			t.Fatalf("bind = %v", err)
		}
	}
	if err := f.svc.Bind(ctx, f.proof(t, ownerB)); !errors.Is(err, worker.ErrOwnership) {
		t.Fatalf("competing bind = %v", err)
	}
	if _, err := f.register(t, nil); !errors.Is(err, worker.ErrOwnership) {
		t.Fatalf("ownerless registration of a bound pool = %v", err)
	}
	if _, err := f.owned(t, ownerB, 0); !errors.Is(err, worker.ErrOwnership) {
		t.Fatalf("foreign owner registration = %v", err)
	}
	var invalid *kernel.Invalid
	if _, err := f.register(t, func(p map[string]any) { p["owner"] = ownerA }); !errors.As(err, &invalid) {
		t.Fatalf("slotless owner registration = %v", err)
	}
	if _, err := f.register(t, func(p map[string]any) { p["slot"] = 0 }); !errors.As(err, &invalid) {
		t.Fatalf("ownerless slot registration = %v", err)
	}
	first, err := f.owned(t, ownerA, 0)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.owned(t, ownerA, 0)
	if err != nil || again.Worker != first.Worker || len(f.store.workers) != 1 {
		t.Fatalf("retried slot = %+v, %v; workers %d", again, err, len(f.store.workers))
	}
	second, err := f.owned(t, ownerA, 1)
	if err != nil || second.Worker == first.Worker {
		t.Fatalf("second slot = %+v, %v", second, err)
	}
	if err := f.svc.Release(ctx, f.proof(t, ownerA)); !errors.Is(err, worker.ErrOwnership) {
		t.Fatalf("release with workers = %v", err)
	}
	f.store.workers = map[kernel.ULID]worker.Worker{}
	f.svc.BindAssignments(assignments{active: true})
	if err := f.svc.Release(ctx, f.proof(t, ownerA)); !errors.Is(err, worker.ErrOwnership) {
		t.Fatalf("release with runs on workers = %v", err)
	}
	f.svc.BindAssignments(assignments{})
	if err := f.svc.Release(ctx, f.proof(t, ownerB)); !errors.Is(err, worker.ErrOwnership) {
		t.Fatalf("foreign release = %v", err)
	}
	for range 2 {
		if err := f.svc.Release(ctx, f.proof(t, ownerA)); err != nil {
			t.Fatalf("release = %v", err)
		}
	}
	if err := f.svc.Bind(ctx, f.proof(t, ownerB)); err != nil {
		t.Fatalf("bind after release = %v", err)
	}
}

// TestUnitPoolBindingRefusesUnsafeOwners checks that a bind is refused for
// unbound workers or runs, a pool managed for another resource, an owner
// that manages or hosts another pool, an ownerless proof, an invalid
// owner and an unbound runs reader.
func TestUnitPoolBindingRefusesUnsafeOwners(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t, worker.Deps{})
	if err := f.svc.Bind(ctx, f.proof(t, ownerA)); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("bind without a runs reader = %v", err)
	}
	f.svc.BindAssignments(assignments{active: true})
	if err := f.svc.Bind(ctx, f.proof(t, ownerA)); !errors.Is(err, worker.ErrOwnership) {
		t.Fatalf("bind with runs on workers = %v", err)
	}
	f.svc.BindAssignments(assignments{err: errDown})
	if err := f.svc.Bind(ctx, f.proof(t, ownerA)); !errors.Is(err, errDown) {
		t.Fatalf("bind with runs unknown = %v", err)
	}
	f.svc.BindAssignments(assignments{})
	if _, err := f.register(t, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Bind(ctx, f.proof(t, ownerA)); !errors.Is(err, worker.ErrOwnership) {
		t.Fatalf("bind over unbound workers = %v", err)
	}
	f.store.workers = map[kernel.ULID]worker.Worker{}

	managed := f.pool
	managed.Management = &ownerB
	f.store.pools[f.pool.ID] = managed
	if err := f.svc.Bind(ctx, f.proof(t, ownerA)); !errors.Is(err, worker.ErrOwnership) {
		t.Fatalf("bind of another resource's managed pool = %v", err)
	}
	f.store.pools[f.pool.ID] = f.pool
	elsewhere := kernel.NewULID()
	f.store.managed = map[kernel.WorkerOwner]*kernel.ULID{ownerA: &elsewhere, ownerB: nil}
	for _, owner := range []kernel.WorkerOwner{ownerA, ownerB} {
		if err := f.svc.Bind(ctx, f.proof(t, owner)); !errors.Is(err, worker.ErrOwnership) {
			t.Fatalf("bind by %+v, which manages another pool = %v", owner, err)
		}
	}
	f.store.managed = nil
	f.store.bindings = map[kernel.ULID]kernel.WorkerOwner{elsewhere: ownerA}
	if err := f.svc.Bind(ctx, f.proof(t, ownerA)); !errors.Is(err, worker.ErrOwnership) {
		t.Fatalf("bind by an owner hosting another pool = %v", err)
	}
	var invalid *kernel.Invalid
	for _, owner := range []any{nil, map[string]string{"clusterID": "Upper", "namespace": "workers", "resourceUID": "uid"}} {
		if err := f.svc.Bind(ctx, f.proof(t, owner)); !errors.As(err, &invalid) {
			t.Fatalf("bind naming %v = %v", owner, err)
		}
		if err := f.svc.Release(ctx, f.proof(t, owner)); !errors.As(err, &invalid) {
			t.Fatalf("release naming %v = %v", owner, err)
		}
	}
	if err := f.svc.Bind(ctx, "not a proof"); !errors.Is(err, worker.ErrUnauthenticated) {
		t.Fatalf("unsigned bind = %v", err)
	}
	if err := f.svc.Release(ctx, "not a proof"); !errors.Is(err, worker.ErrUnauthenticated) {
		t.Fatalf("unsigned release = %v", err)
	}
	replay := f.proof(t, ownerB)
	if err := f.svc.Bind(ctx, replay); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Bind(ctx, replay); !errors.Is(err, worker.ErrUnauthenticated) {
		t.Fatalf("replayed proof = %v", err)
	}
	if err := f.svc.Release(ctx, f.proof(t, ownerB)); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Release(ctx, f.proof(t, ownerB)); err != nil {
		t.Fatalf("release of an unbound pool = %v", err)
	}
	if _, err := f.register(t, func(p map[string]any) { p["owner"], p["slot"] = ownerA, -1 }); !errors.As(err, &invalid) {
		t.Fatalf("negative slot = %v", err)
	}
	if _, err := f.register(t, func(p map[string]any) {
		p["owner"], p["slot"] = map[string]string{"clusterID": "", "namespace": "workers", "resourceUID": "uid"}, 0
	}); !errors.As(err, &invalid) {
		t.Fatalf("invalid registration owner = %v", err)
	}
}

// TestUnitPoolBindingStoreFailures checks that each store failure of a
// bind, a release and an owned registration is answered.
func TestUnitPoolBindingStoreFailures(t *testing.T) {
	ctx := t.Context()
	for _, method := range []string{"Binding", "Bind", "ManagedPool", "HasWorkers", "PoolByID"} {
		f := newFixture(t, worker.Deps{})
		f.svc.BindAssignments(assignments{})
		f.store.fail = method
		if err := f.svc.Bind(ctx, f.proof(t, ownerA)); !errors.Is(err, errDown) {
			t.Errorf("bind with %s down = %v", method, err)
		}
	}
	for _, method := range []string{"Binding", "Unbind", "HasWorkers", "CA"} {
		f := newFixture(t, worker.Deps{})
		f.svc.BindAssignments(assignments{})
		f.store.bindings = map[kernel.ULID]kernel.WorkerOwner{f.pool.ID: ownerA}
		f.store.fail = method
		if err := f.svc.Release(ctx, f.proof(t, ownerA)); !errors.Is(err, errDown) {
			t.Errorf("release with %s down = %v", method, err)
		}
	}
	for _, method := range []string{"Binding", "SlotWorker"} {
		f := newFixture(t, worker.Deps{})
		f.store.bindings = map[kernel.ULID]kernel.WorkerOwner{f.pool.ID: ownerA}
		f.store.fail = method
		if _, err := f.owned(t, ownerA, 0); !errors.Is(err, errDown) {
			t.Errorf("owned registration with %s down = %v", method, err)
		}
	}
}
