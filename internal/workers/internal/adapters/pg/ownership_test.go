package pg_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

// inTx runs fn in a transaction of the fixture's database.
func (f *fixture) inTx(t *testing.T, fn func(ctx context.Context) error) error {
	t.Helper()
	return tx.Do(t.Context(), f.db, tx.Options{}, fn) //nolint:wrapcheck // the test reads the cause
}

// TestAccPoolBindingStorage checks bindings, management reservations, the
// worker presence read and one worker per slot against PostgreSQL.
func TestAccPoolBindingStorage(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	p, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "hosted", Space: "team"}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "other", Space: "team"}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	owner := managedOwner()
	if _, _, err := f.store.Binding(ctx, p.ID); err == nil {
		t.Fatal("binding read outside a transaction")
	}
	err = f.inTx(t, func(ctx context.Context) error {
		if _, ok, err := f.store.Binding(ctx, p.ID); err != nil || ok {
			t.Fatalf("unbound pool = %v, %v", ok, err)
		}
		return f.store.Bind(ctx, p, owner)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.inTx(t, func(ctx context.Context) error { return f.store.Bind(ctx, other, owner) }); !errors.Is(err, worker.ErrOwnership) {
		t.Fatalf("one owner bound two pools: %v", err)
	}
	err = f.inTx(t, func(ctx context.Context) error {
		got, ok, err := f.store.Binding(ctx, p.ID)
		if err != nil || !ok || got != owner {
			t.Fatalf("binding = %+v %v %v", got, ok, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var account *kernel.AccountID
	if err := f.db.QueryRow(ctx, `SELECT account_id FROM worker_pool_bindings WHERE pool_id=$1`, p.ID).Scan(&account); err != nil || account == nil || *account != f.acct {
		t.Fatalf("private binding account = %v %v", account, err)
	}
	if busy, err := f.store.HasWorkers(ctx, p.ID); err != nil || busy {
		t.Fatalf("empty pool has workers: %v %v", busy, err)
	}
	slot := int32(3)
	w := worker.Worker{
		Account: p.Account, Organization: f.org, ID: kernel.NewULID(), Pool: p.ID, Serial: p.Serial,
		Metadata: map[string]string{}, LastSeenAt: time.Now(), CreatedAt: time.Now(), Slot: &slot,
	}
	if err := f.store.CreateWorker(ctx, w); err != nil {
		t.Fatal(err)
	}
	duplicate := w
	duplicate.ID = kernel.NewULID()
	if err := f.store.CreateWorker(ctx, duplicate); err == nil {
		t.Fatal("two workers registered one slot")
	}
	if busy, err := f.store.HasWorkers(ctx, p.ID); err != nil || !busy {
		t.Fatalf("hosted pool has no workers: %v %v", busy, err)
	}
	err = f.inTx(t, func(ctx context.Context) error {
		got, err := f.store.SlotWorker(ctx, p.ID, slot)
		if err != nil || got.ID != w.ID || got.Slot == nil || *got.Slot != slot {
			t.Fatalf("slot worker = %+v %v", got, err)
		}
		if _, err := f.store.SlotWorker(ctx, p.ID, slot+1); !errors.Is(err, worker.ErrNotFound) {
			t.Fatalf("empty slot = %v", err)
		}
		return f.store.Unbind(ctx, p.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	if id, ok, err := f.store.ManagedPool(ctx, owner); err != nil || ok || id != nil {
		t.Fatalf("unmanaged owner reserved: %v %v %v", id, ok, err)
	}
	managed := managedOwner()
	created, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "managed", Space: "team", Management: &managed}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	if id, ok, err := f.store.ManagedPool(ctx, managed); err != nil || !ok || id == nil || *id != created.ID {
		t.Fatalf("managed reservation = %v %v %v", id, ok, err)
	}
	if _, err := f.svc.Delete(ctx, f.admin, created.ID.String(), kernel.Expect{}); err != nil {
		t.Fatal(err)
	}
	if id, ok, err := f.store.ManagedPool(ctx, managed); err != nil || !ok || id != nil {
		t.Fatalf("deleted reservation = %v %v %v", id, ok, err)
	}
	var bindings int
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM worker_pool_bindings WHERE pool_id=$1`, created.ID).Scan(&bindings); err != nil || bindings != 0 {
		t.Fatalf("deleted pool kept its binding: %d %v", bindings, err)
	}
}

// TestAccPoolBindingSerializesConcurrentBinds checks that concurrent binds
// of one pool by different owners leave exactly one binding.
func TestAccPoolBindingSerializesConcurrentBinds(t *testing.T) {
	f := newFixture(t)
	p, err := f.svc.Create(t.Context(), f.admin, pool.Input{Name: "contended", Space: "team"}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	const owners = 6
	var wg sync.WaitGroup
	results := make(chan bool, owners)
	for range owners {
		owner := managedOwner()
		wg.Go(func() {
			bound := false
			err := f.inTx(t, func(ctx context.Context) error {
				_, ok, err := f.store.Binding(ctx, p.ID)
				if err != nil || ok {
					return err
				}
				bound = true
				return f.store.Bind(ctx, p, owner)
			})
			results <- err == nil && bound
		})
	}
	wg.Wait()
	close(results)
	winners := 0
	for won := range results {
		if won {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("binders = %d", winners)
	}
}

// TestAccPoolResetReleasesBinding checks that a private reset releases an
// abandoned binding only for a replacement key and an idle pool.
func TestAccPoolResetReleasesBinding(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	owner := managedOwner()
	p, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "abandoned", Space: "team", Management: &owner}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Reset(ctx, f.admin, p.ID.String(), request(t), true); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("release without a runs reader = %v", err)
	}
	f.svc.BindPoolAssignments(poolAssignments{})
	var invalid *kernel.Invalid
	if _, err := f.svc.Reset(ctx, f.admin, p.ID.String(), f.csr, true); !errors.As(err, &invalid) {
		t.Fatalf("release with the same key = %v", err)
	}
	w := f.newWorker(t, p, time.Now())
	if _, err := f.svc.Reset(ctx, f.admin, p.ID.String(), request(t), true); !errors.Is(err, pool.ErrBusy) {
		t.Fatalf("release with a worker = %v", err)
	}
	if err := f.store.Remove(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	for _, a := range []poolAssignments{{active: true}, {err: errDown}} {
		f.svc.BindPoolAssignments(a)
		want := pool.ErrBusy
		if a.err != nil {
			want = errDown
		}
		if _, err := f.svc.Reset(ctx, f.admin, p.ID.String(), request(t), true); !errors.Is(err, want) {
			t.Fatalf("release with runs %+v = %v", a, err)
		}
	}
	f.svc.BindPoolAssignments(poolAssignments{})
	reset, err := f.svc.Reset(ctx, f.admin, p.ID.String(), request(t), true)
	if err != nil || reset.Serial == p.Serial || reset.Management == nil || *reset.Management != owner {
		t.Fatalf("release reset = %+v %v", reset, err)
	}
	var bindings int
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM worker_pool_bindings WHERE pool_id=$1`, p.ID).Scan(&bindings); err != nil || bindings != 0 {
		t.Fatalf("binding kept: %d %v", bindings, err)
	}
}
