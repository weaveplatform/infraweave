package pg_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

type publicAssignments struct {
	active bool
	err    error
}

func (a publicAssignments) ActivePublicPool(context.Context, kernel.ULID) (bool, error) {
	return a.active, a.err
}

func TestAccPublicPoolCredentials(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	if _, err := f.svc.Public(ctx); !errors.Is(err, pool.ErrNotFound) {
		t.Fatalf("missing public pool: %v", err)
	}
	if _, err := f.store.Public(ctx, true); err == nil {
		t.Fatal("singleton locking outside a transaction succeeded")
	}
	if _, err := f.svc.CreatePublic(ctx, "invalid"); err == nil {
		t.Fatal("invalid CSR accepted")
	}
	const replicas = 4
	results := make(chan error, replicas)
	var wg sync.WaitGroup
	for range replicas {
		wg.Go(func() { _, err := f.svc.CreatePublic(ctx, f.csr); results <- err })
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, pool.ErrPublicExists) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("singleton creation winners=%d", winners)
	}
	first, err := f.svc.Public(ctx)
	if err != nil || !first.Public || !first.Account.IsZero() || !first.Organization.IsZero() || !first.Space.IsZero() || first.Version != 1 {
		t.Fatalf("installation identity=%+v error=%v", first, err)
	}
	if _, err := f.svc.Read(ctx, f.acct, first.ID.String()); !errors.Is(err, pool.ErrNotFound) {
		t.Fatalf("account read reached installation credentials: %v", err)
	}
	if _, err := f.svc.ResetPublic(ctx, kernel.ExpectVersions(2), f.csr, false); !errors.Is(err, kernel.ErrStale) {
		t.Fatalf("stale reset: %v", err)
	}
	if _, err := f.svc.ResetPublic(ctx, kernel.ExpectVersions(1), f.csr, true); err == nil {
		t.Fatal("ownership release accepted the same key")
	}
	if _, err := f.db.Exec(ctx, `ALTER TABLE worker_public_pool ADD CONSTRAINT refuse_next_version CHECK(version=1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ResetPublic(ctx, kernel.ExpectVersions(1), request(t), false); err == nil {
		t.Fatal("credential reset ignored the database constraint")
	} else if failure, ok := errors.AsType[*pgconn.PgError](err); !ok || failure.Code != "23514" {
		t.Fatalf("unexpected persistence failure: %v", err)
	}
	unchanged, err := f.svc.Public(ctx)
	if err != nil || unchanged.Version != first.Version || unchanged.Serial != first.Serial || !bytes.Equal(unchanged.Certificate, first.Certificate) {
		t.Fatalf("failed reset changed credentials: %+v %v", unchanged, err)
	}
	if _, err := f.db.Exec(ctx, `ALTER TABLE worker_public_pool DROP CONSTRAINT refuse_next_version`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ResetPublic(ctx, kernel.ExpectVersions(1), request(t), true); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("unbound assignment reader allowed release: %v", err)
	}
	for _, assignments := range []publicAssignments{{active: true}, {err: errDown}} {
		f.svc.BindPublicAssignments(assignments)
		_, err := f.svc.ResetPublic(ctx, kernel.ExpectVersions(1), request(t), true)
		want := pool.ErrPublicBusy
		if assignments.err != nil {
			want = errDown
		}
		if !errors.Is(err, want) {
			t.Fatalf("unsafe ownership release: %v, want %v", err, want)
		}
	}
	f.svc.BindPublicAssignments(publicAssignments{})
	reset, err := f.svc.ResetPublic(ctx, kernel.ExpectVersions(1), request(t), true)
	if err != nil || reset.ID != first.ID || reset.Version != 2 || reset.Serial == first.Serial || f.svc.Token(reset) == f.svc.Token(first) {
		t.Fatalf("replacement credentials=%+v error=%v", reset, err)
	}
}

func TestAccPublicWorkerStorage(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	p, err := f.svc.CreatePublic(ctx, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	w := worker.Worker{
		Public:      true,
		ID:          kernel.NewULID(),
		Pool:        p.ID,
		Serial:      p.Serial,
		Version:     "1.0.0",
		OS:          "linux",
		Arch:        "arm64",
		CreatedAt:   now,
		LastSeenAt:  now,
		AvailableAt: &now,
	}
	if err := f.store.CreateWorker(ctx, w); err != nil {
		t.Fatal(err)
	}
	got, serial, err := f.store.Worker(ctx, w.ID, false)
	if err != nil || !got.Public || !got.Account.IsZero() || !got.Organization.IsZero() || got.Pool != p.ID || serial != p.Serial {
		t.Fatalf("public worker=%+v serial=%d error=%v", got, serial, err)
	}
	var tenant, organization *string
	if err := f.db.QueryRow(ctx, `SELECT account_id::text,organization_id::text FROM workers WHERE id=$1`, w.ID).
		Scan(&tenant, &organization); err != nil || tenant != nil ||
		organization != nil {
		t.Fatalf("installation worker has a persisted tenant: %v %v %v", tenant, organization, err)
	}
	u, err := f.store.Count(ctx, f.org, f.acct)
	if err != nil || u.Account != 0 || u.Organization != 0 {
		t.Fatalf("public registration consumed private quota: %+v %v", u, err)
	}
	if got, _, err := f.store.Seen(ctx, w.ID, w.Serial, true, now); err != nil || !got.Public || !got.Busy || got.AvailableAt != nil {
		t.Fatalf("busy public worker=%+v error=%v", got, err)
	}
	w.Metadata = map[string]string{"resumed": "yes"}
	if err := f.store.Resume(ctx, w); err != nil {
		t.Fatal(err)
	}
	for _, want := range []bool{true, false} {
		if fresh, err := f.store.Nonce(ctx, kernel.AccountID{}, p.ID, "public-registration", now.Add(time.Hour)); err != nil || fresh != want {
			t.Fatalf("public replay check=%t want=%t error=%v", fresh, want, err)
		}
	}
	if _, err := f.svc.ResetPublic(ctx, kernel.ExpectVersions(1), request(t), true); !errors.Is(err, pool.ErrPublicBusy) {
		t.Fatalf("released pool with registered workers: %v", err)
	}
	if err := f.store.Remove(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	f.svc.BindPublicAssignments(publicAssignments{})
	if _, err := f.svc.ResetPublic(ctx, kernel.ExpectVersions(1), request(t), true); err != nil {
		t.Fatal(err)
	}
}

func TestAccPublicResetDoesNotRefreshObsoleteWorkers(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	p, err := f.svc.CreatePublic(ctx, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	w := worker.Worker{
		Public:      true,
		ID:          kernel.NewULID(),
		Pool:        p.ID,
		Serial:      p.Serial,
		Version:     "1.0.0",
		OS:          "linux",
		Arch:        "arm64",
		CreatedAt:   now,
		LastSeenAt:  now,
		AvailableAt: &now,
	}
	if err := f.store.CreateWorker(ctx, w); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ResetPublic(ctx, kernel.ExpectVersions(1), request(t), false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store.Seen(ctx, w.ID, w.Serial, true, now.Add(time.Minute)); !errors.Is(err, worker.ErrNotFound) {
		t.Fatalf("obsolete public worker updated: %v", err)
	}
	got, _, err := f.store.Worker(ctx, w.ID, false)
	if err != nil || !got.LastSeenAt.Equal(now) || got.Busy {
		t.Fatalf("obsolete traffic changed liveness: %+v %v", got, err)
	}
}
