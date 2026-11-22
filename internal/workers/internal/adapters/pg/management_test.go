package pg_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
)

func managedOwner() kernel.WorkerOwner {
	return kernel.WorkerOwner{ClusterID: "lab-cluster", Namespace: "workers", ResourceUID: kernel.NewULID().String()}
}

func TestAccManagedPoolConcurrentCreationAndResponseRecovery(t *testing.T) {
	f := newFixture(t)
	owner := managedOwner()
	const replicas = 6
	results := make(chan error, replicas)
	var wg sync.WaitGroup
	for range replicas {
		wg.Go(func() {
			_, err := f.svc.Create(t.Context(), f.admin, pool.Input{Name: kernel.NewULID().String(), Space: "team", Management: &owner}, f.csr)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, pool.ErrOwnership) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("creation winners=%d", winners)
	}
	first, err := f.svc.Managed(t.Context(), f.admin, owner)
	if err != nil || first.Management == nil || *first.Management != owner || first.Version != 1 {
		t.Fatalf("owner recovery: %+v %v", first, err)
	}
	second, err := f.svc.Managed(t.Context(), f.admin, owner)
	if err != nil || second.ID != first.ID || f.svc.Token(second) != f.svc.Token(first) {
		t.Fatalf("response recovery changed pool: %+v %v", second, err)
	}
	for _, table := range []string{"worker_pools", "worker_pool_management", "worker_pool_bindings"} {
		var count int
		if err := f.db.QueryRow(t.Context(), `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s count=%d error=%v", table, count, err)
		}
	}
	var serial int64
	if err := f.db.QueryRow(t.Context(), `SELECT next_serial FROM worker_pool_ca`).Scan(&serial); err != nil || serial != first.Serial+1 {
		t.Fatalf("losing creates consumed CA serials: %d %v", serial, err)
	}
}

func TestAccManagedPoolLookupAuthorizationAndRetainedIdentity(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	owner := managedOwner()
	created, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "managed", Space: "hidden", Management: &owner}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Managed(ctx, f.reader, owner); !errors.Is(err, pool.ErrNotFound) {
		t.Fatalf("hidden owner lookup: %v", err)
	}
	foreign := f.admin
	foreign.AccountID = kernel.NewAccountID()
	if _, err := f.svc.Managed(ctx, foreign, owner); !errors.Is(err, pool.ErrNotFound) {
		t.Fatalf("cross-account owner lookup: %v", err)
	}
	invalid := owner
	invalid.ResourceUID = ""
	if _, err := f.svc.Managed(ctx, f.admin, invalid); err == nil {
		t.Fatal("empty UID accepted")
	}
	down := f.deps
	down.Authz = authz{err: errDown}
	if _, err := pool.New(down).Managed(ctx, f.admin, owner); !errors.Is(err, errDown) {
		t.Fatalf("authorization outage hidden: %v", err)
	}
	if _, err := f.svc.Delete(ctx, f.admin, created.ID.String(), kernel.ExpectVersions(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Managed(ctx, f.admin, owner); !errors.Is(err, pool.ErrNotFound) {
		t.Fatalf("deleted pool recovered: %v", err)
	}
	var poolID *kernel.ULID
	if err := f.db.QueryRow(ctx, `SELECT pool_id FROM worker_pool_management WHERE resource_uid=$1`, owner.ResourceUID).Scan(&poolID); err != nil || poolID != nil {
		t.Fatalf("tombstone lost: %v %v", poolID, err)
	}
	if _, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "replacement", Space: "hidden", Management: &owner}, f.csr); !errors.Is(err, pool.ErrOwnership) {
		t.Fatalf("deleted owner created replacement: %v", err)
	}
	var count int
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM worker_pools`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed retry leaked a pool: %d %v", count, err)
	}
}

func TestAccManagedSettingsRequireExactOwnerAndVersion(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	owner := managedOwner()
	created, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "managed", Space: "team", Management: &owner}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	wrong := owner
	wrong.ResourceUID = kernel.NewULID().String()
	for _, test := range []struct {
		name    string
		owner   *kernel.WorkerOwner
		version kernel.Expect
		space   string
		want    error
	}{
		{"no owner", nil, kernel.ExpectVersions(1), "team", pool.ErrOwnership},
		{"wrong owner", &wrong, kernel.ExpectVersions(1), "team", pool.ErrOwnership},
		{"unconditional", &owner, kernel.Expect{}, "team", pool.ErrConditionRequired},
		{"stale", &owner, kernel.ExpectVersions(2), "team", kernel.ErrStale},
		{"move", &owner, kernel.ExpectVersions(1), "acme", pool.ErrOwnership},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := f.svc.Update(ctx, f.admin, created.ID.String(), test.version, pool.Input{Name: "changed", Space: test.space, Management: test.owner})
			if !errors.Is(err, test.want) {
				t.Fatalf("unsafe update: %v want %v", err, test.want)
			}
		})
	}
	updated, err := f.svc.Update(ctx, f.admin, created.ID.String(), kernel.ExpectVersions(1), pool.Input{Name: "maintained", Space: "team", Management: &owner})
	if err != nil || updated.Version != 2 || updated.Management == nil || *updated.Management != owner {
		t.Fatalf("owned update: %+v %v", updated, err)
	}
	reset, err := f.svc.Reset(ctx, f.admin, created.ID.String(), request(t), false)
	if err != nil || reset.Serial == created.Serial || reset.Management == nil || *reset.Management != owner {
		t.Fatalf("authorized recovery reset lost owner: %+v %v", reset, err)
	}
	ordinary, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "ordinary", Space: "team"}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Update(
		ctx,
		f.admin,
		ordinary.ID.String(),
		kernel.ExpectVersions(1),
		pool.Input{Name: "adopted", Space: "team", Management: &owner},
	); !errors.Is(
		err,
		pool.ErrOwnership,
	) {
		t.Fatalf("ordinary pool adopted: %v", err)
	}
}

func TestAccManagedCreationFailureRollsBackAllOwnership(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	owner := managedOwner()
	if _, err := f.db.Exec(ctx, `ALTER TABLE worker_pool_bindings ADD CONSTRAINT refuse_binding CHECK(false)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "refused", Space: "team", Management: &owner}, f.csr); err == nil {
		t.Fatal("binding constraint ignored")
	}
	for _, table := range []string{"worker_pool_ca", "worker_pools", "worker_pool_management", "worker_pool_bindings"} {
		var count int
		if err := f.db.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial ownership: %s=%d %v", table, count, err)
		}
	}
	if err := f.store.Create(ctx, pool.Pool{Account: f.acct, ID: kernel.NewULID(), Management: &owner}); err == nil {
		t.Fatal("managed storage accepted no transaction")
	}
}

func TestAccManagedAccountPurgeClearsOnlyItsRegistry(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	owner := managedOwner()
	first, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "retained", Space: "team", Management: &owner}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Delete(ctx, f.admin, first.ID.String(), kernel.ExpectVersions(1)); err != nil {
		t.Fatal(err)
	}
	other := managedOwner()
	if _, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "live", Space: "team", Management: &other}, f.csr); err != nil {
		t.Fatal(err)
	}
	// A borrowed public binding must survive this private account purge.
	publicOwner := managedOwner()
	publicID := kernel.NewULID()
	if _, err := f.db.Exec(
		ctx,
		`INSERT INTO worker_pool_bindings(pool_id,cluster_id,namespace,resource_uid) VALUES($1,$2,$3,$4)`,
		publicID,
		publicOwner.ClusterID,
		publicOwner.Namespace,
		publicOwner.ResourceUID,
	); err != nil {
		t.Fatal(err)
	}
	done := false
	for range 10 {
		done, err = f.svc.Purge(ctx, f.acct, 1)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	if !done {
		t.Fatal("bounded purge did not finish")
	}
	var count int
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM worker_pool_management`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("registry retained after account purge: %d %v", count, err)
	}
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM worker_pool_bindings WHERE pool_id=$1`, publicID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("another owner's public binding removed: %d %v", count, err)
	}
}
