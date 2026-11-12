package pg_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

// wakes listens for the workers' wake-ups, and answers the payloads
// notified since the last call.
func (f *fixture) wakes(t *testing.T) func() []string {
	t.Helper()
	conn, err := f.db.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Release)
	if _, err := conn.Exec(t.Context(), "LISTEN "+notify.Runs); err != nil {
		t.Fatal(err)
	}
	return func() []string {
		t.Helper()
		if _, err := f.db.Exec(
			t.Context(),
			`SELECT pg_notify($1, 'marker')`,
			notify.Runs,
		); err != nil {
			t.Fatal(err)
		}
		var got []string
		for {
			n, err := conn.Conn().WaitForNotification(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if n.Payload == "marker" {
				return got
			}
			got = append(got, n.Payload)
		}
	}
}

// newWorker is an idle worker of p, created at at.
func (f *fixture) newWorker(t *testing.T, p pool.Pool, at time.Time) worker.Worker {
	t.Helper()
	w := worker.Worker{
		Account: p.Account, Organization: f.org, ID: kernel.NewULID(), Pool: p.ID,
		Serial: p.Serial, Version: "1.0.0", OS: "linux", Arch: "amd64",
		Metadata: map[string]string{"host": "a"}, LastSeenAt: at, AvailableAt: &at, CreatedAt: at,
	}
	if err := f.store.CreateWorker(t.Context(), w); err != nil {
		t.Fatal(err)
	}
	return w
}

// TestAccWorkers checks the worker store: reads by ID across accounts,
// nonces, counts, polls, drains, cycles and removals.
func TestAccWorkers(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	p, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "Builders"}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := f.store.PoolByID(ctx, p.ID); err != nil || got.Organization != f.org ||
		got.Account != f.acct {
		t.Errorf("PoolByID = %+v, %v", got, err)
	}
	if _, err := f.store.PoolByID(ctx, kernel.NewULID()); !errors.Is(err, pool.ErrNotFound) {
		t.Errorf("PoolByID of none = %v", err)
	}
	if ca, err := f.store.CA(ctx); err != nil || len(ca) == 0 {
		t.Errorf("CA = %d bytes, %v", len(ca), err)
	}

	later := time.Now().Add(time.Hour)
	for i, want := range []bool{true, false} {
		if fresh, err := f.store.Nonce(ctx, f.acct, p.ID, "j", later); err != nil || fresh != want {
			t.Errorf("Nonce %d = %t, %v", i, fresh, err)
		}
	}
	if _, err := f.db.Exec(
		ctx,
		`UPDATE worker_nonces SET expires_at = now() - interval '1s'`,
	); err != nil {
		t.Fatal(err)
	}
	if fresh, err := f.store.Nonce(ctx, f.acct, p.ID, "j", later); err != nil || !fresh {
		t.Errorf("Nonce once expired = %t, %v", fresh, err)
	}

	at := time.Unix(1_700_000_000, 0).UTC()
	w := f.newWorker(t, p, at)
	other := f.newWorker(t, p, at.Add(time.Second))
	if u, err := f.store.Count(ctx, f.org, f.acct); err != nil || u.Organization != 2 ||
		u.Account != 2 {
		t.Errorf("Count = %+v, %v", u, err)
	}
	if u, err := f.store.Count(ctx, f.org, kernel.NewAccountID()); err != nil ||
		u.Organization != 2 || u.Account != 0 {
		t.Errorf("Count of another account = %+v, %v", u, err)
	}
	got, serial, err := f.store.Worker(ctx, w.ID, true)
	if err != nil || serial != p.Serial || got.Metadata["host"] != "a" || !got.CreatedAt.Equal(at) {
		t.Errorf("Worker = %+v, %d, %v", got, serial, err)
	}
	if _, _, err := f.store.Worker(
		ctx,
		kernel.NewULID(),
		false,
	); !errors.Is(
		err,
		worker.ErrNotFound,
	) {
		t.Errorf("Worker of none = %v", err)
	}

	w.Version, w.Metadata, w.LastSeenAt = "1.1.0", nil, at.Add(time.Minute)
	if err := f.store.Resume(ctx, w); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := f.store.Worker(ctx, w.ID, false); got.Version != "1.1.0" ||
		len(got.Metadata) != 0 || !got.LastSeenAt.Equal(w.LastSeenAt) {
		t.Errorf("resumed %+v", got)
	}
	if err := f.store.Resume(ctx, worker.Worker{Account: f.acct, ID: kernel.NewULID()}); !errors.Is(
		err, worker.ErrNotFound) {
		t.Errorf("Resume of none = %v", err)
	}

	// available_at is cleared while busy, set when the worker becomes idle
	// and kept while it stays idle.
	for i, step := range []struct {
		busy  bool
		want  *time.Time
		after time.Duration
	}{
		{busy: true},
		{busy: false, after: 2 * time.Minute},
		{busy: false, after: 3 * time.Minute},
	} {
		now := at.Add(step.after)
		got, _, err := f.store.Seen(ctx, w.ID, step.busy, now)
		if err != nil || got.Busy != step.busy || !got.LastSeenAt.Equal(now) {
			t.Fatalf("Seen %d = %+v, %v", i, got, err)
		}
		switch {
		case step.busy && got.AvailableAt != nil:
			t.Errorf("busy available at %v", got.AvailableAt)
		case !step.busy && (got.AvailableAt == nil || !got.AvailableAt.Equal(at.Add(2*time.Minute))):
			t.Errorf("Seen %d available at %v", i, got.AvailableAt)
		}
	}
	if _, _, err := f.store.Seen(
		ctx,
		kernel.NewULID(),
		false,
		at,
	); !errors.Is(
		err,
		worker.ErrNotFound,
	) {
		t.Errorf("Seen of none = %v", err)
	}

	wakes := f.wakes(t)
	if err := tx.Do(ctx, f.db, tx.Options{}, func(ctx context.Context) error {
		got, err := f.store.Drain(ctx, f.acct, p.ID, w.ID, true)
		if err != nil || !got.Drained {
			t.Errorf("Drain = %+v, %v", got, err)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := wakes(); !slices.Equal(got, []string{p.ID.String()}) {
		t.Errorf("drain woke %v", got)
	}
	if _, err := f.store.Drain(ctx, kernel.NewAccountID(), p.ID, w.ID, false); !errors.Is(err,
		worker.ErrNotFound) {
		t.Errorf("Drain of another account's = %v", err)
	}
	if err := f.store.Cycle(ctx, f.acct, p.ID); err != nil {
		t.Fatal(err)
	}
	if got := wakes(); !slices.Equal(got, []string{p.ID.String()}) {
		t.Errorf("cycle woke %v", got)
	}

	of, err := f.store.OfPools(ctx, f.acct, []kernel.ULID{p.ID, kernel.NewULID()})
	if err != nil || len(of[p.ID]) != 2 || of[p.ID][0].ID != w.ID || of[p.ID][1].ID != other.ID ||
		!of[p.ID][0].Cycle || !of[p.ID][1].Cycle || !of[p.ID][0].Drained {
		t.Errorf("OfPools = %+v, %v", of, err)
	}

	if err := f.store.Remove(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Remove(ctx, w.ID); !errors.Is(err, worker.ErrNotFound) {
		t.Errorf("Remove twice = %v", err)
	}
}

// TestAccWorkerSearch checks a search of a pool's workers by status and
// its suggestions.
func TestAccWorkerSearch(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	p, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "Builders"}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_700_000_000, 0).UTC()
	idle := f.newWorker(t, p, at)
	busy := f.newWorker(t, p, at.Add(time.Second))
	if _, _, err := f.store.Seen(ctx, busy.ID, true, at); err != nil {
		t.Fatal(err)
	}
	drained := f.newWorker(t, p, at.Add(2*time.Second))
	if _, err := f.store.Drain(ctx, f.acct, p.ID, drained.ID, true); err != nil {
		t.Fatal(err)
	}

	all, err := f.store.SearchWorkers(ctx, f.acct, p.ID, p.Space, search.Input{})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]kernel.ULID, 0, len(all.Edges))
	for _, e := range all.Edges {
		ids = append(ids, e.Node.ID)
	}
	if !slices.Equal(ids, []kernel.ULID{drained.ID, busy.ID, idle.ID}) {
		t.Errorf("newest first = %v", ids)
	}
	for status, want := range map[string]kernel.ULID{
		"IDLE": idle.ID, "BUSY": busy.ID, "DRAINED": drained.ID,
	} {
		got, err := f.store.SearchWorkers(ctx, f.acct, p.ID, p.Space, search.Input{
			Predicates: []search.Predicate{{
				Field: "status", Constraint: search.Constraint{EnumEquals: []string{status}},
			}},
		})
		if err != nil || len(got.Edges) != 1 || got.Edges[0].Node.ID != want {
			t.Errorf("status %s = %+v, %v", status, got.Edges, err)
		}
	}
	if got, err := f.store.SearchWorkers(ctx, kernel.NewAccountID(), p.ID, p.Space,
		search.Input{}); err != nil || len(got.Edges) != 0 {
		t.Errorf("another account's = %+v, %v", got.Edges, err)
	}
	if _, err := f.store.SearchWorkers(ctx, f.acct, p.ID, p.Space,
		search.Input{After: "nope"}); err == nil {
		t.Error("a bad cursor searched")
	}

	sug, err := f.store.SuggestWorkers(ctx, f.acct, p.ID, p.Space,
		search.SuggestionsInput{Fields: []string{"status"}})
	if err != nil || sug.Count != 3 {
		t.Fatalf("SuggestWorkers = %+v, %v", sug, err)
	}
	i := slices.IndexFunc(
		sug.Fields,
		func(s search.Suggestion) bool { return s.Field.Name == "status" },
	)
	if i < 0 || len(sug.Fields[i].Values) != 3 {
		t.Errorf("status suggestions = %+v", sug.Fields)
	}
}

// TestAccWorkersUnavailable checks that every worker read and write on a
// closed pool fails, and not as a missing row.
func TestAccWorkersUnavailable(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "Builders"}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	w := f.newWorker(t, p, time.Now())
	f.db.Close()
	s, acct := f.store, f.acct
	for name, err := range map[string]error{
		"PoolByID": func() error { _, err := s.PoolByID(ctx, p.ID); return err }(),
		"CA":       func() error { _, err := s.CA(ctx); return err }(),
		"Nonce": func() error {
			_, err := s.Nonce(ctx, acct, p.ID, "j", time.Now())
			return err
		}(),
		"Count":        func() error { _, err := s.Count(ctx, f.org, acct); return err }(),
		"Worker":       func() error { _, _, err := s.Worker(ctx, w.ID, false); return err }(),
		"CreateWorker": s.CreateWorker(ctx, w),
		"Resume":       s.Resume(ctx, w),
		"Seen":         func() error { _, _, err := s.Seen(ctx, w.ID, false, time.Now()); return err }(),
		"Remove":       s.Remove(ctx, w.ID),
		"Drain":        func() error { _, err := s.Drain(ctx, acct, p.ID, w.ID, true); return err }(),
		"Cycle":        s.Cycle(ctx, acct, p.ID),
		"OfPools": func() error {
			_, err := s.OfPools(ctx, acct, []kernel.ULID{p.ID})
			return err
		}(),
		"SearchWorkers": func() error {
			_, err := s.SearchWorkers(ctx, acct, p.ID, p.Space, search.Input{})
			return err
		}(),
		"SuggestWorkers": func() error {
			_, err := s.SuggestWorkers(ctx, acct, p.ID, p.Space, search.SuggestionsInput{})
			return err
		}(),
		"MarkOffline":  func() error { _, err := s.MarkOffline(ctx, time.Now()); return err }(),
		"Lost":         func() error { _, _, err := s.Lost(ctx, time.Now()); return err }(),
		"PoolTimeouts": func() error { _, err := s.PoolTimeouts(ctx); return err }(),
	} {
		if err == nil || errors.Is(err, worker.ErrNotFound) || errors.Is(err, pool.ErrNotFound) {
			t.Errorf("%s on a closed pool = %v", name, err)
		}
	}
}

// TestAccWorkerReap checks the reap's queries: workers unseen since a
// time are marked offline once, of every account, listed as OFFLINE, and
// a poll or a resume clears the mark; lost workers are deleted longest
// unseen first, and a second reap skips the worker a first holds, so two
// reaps delete each once; and the pools that set their own preparing
// timeout are read with it.
func TestAccWorkerReap(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	fiveMinutes := int32(300)
	p, err := f.svc.Create(ctx, f.admin, pool.Input{
		Name: "Builders", PreparingTimeoutSeconds: &fiveMinutes,
	}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	q, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "Runners"}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	base := now.Add(-2 * time.Hour)
	old, fresh := f.newWorker(t, p, base), f.newWorker(t, q, now)
	for i, want := range []int{1, 0} {
		if n, err := f.store.MarkOffline(ctx, now.Add(-time.Hour)); err != nil || n != want {
			t.Errorf("MarkOffline %d = %d, %v, want %d", i, n, err, want)
		}
	}
	got, _, err := f.store.Worker(ctx, old.ID, false)
	if err != nil || got.Status() != worker.StatusOffline {
		t.Errorf("the unseen worker is %s, %v", got.Status(), err)
	}
	offline, err := f.store.SearchWorkers(ctx, f.acct, p.ID, p.Space, search.Input{
		Predicates: []search.Predicate{{
			Field: "status", Constraint: search.Constraint{EnumEquals: []string{"OFFLINE"}},
		}},
	})
	if err != nil || len(offline.Edges) != 1 || offline.Edges[0].Node.ID != old.ID {
		t.Errorf("status OFFLINE = %+v, %v", offline.Edges, err)
	}
	if got, _, err := f.store.Seen(ctx, old.ID, true, now); err != nil || got.Offline {
		t.Errorf("a poll left the worker offline: %+v, %v", got, err)
	}
	if n, err := f.store.MarkOffline(ctx, now.Add(time.Minute)); err != nil || n != 2 {
		t.Errorf("MarkOffline of both = %d, %v", n, err)
	}
	old.LastSeenAt = now
	if err := f.store.Resume(ctx, old); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[kernel.ULID]bool{old.ID: false, fresh.ID: true} {
		if got, _, err := f.store.Worker(ctx, id, false); err != nil || got.Offline != want {
			t.Errorf("worker %s offline %t, %v; want %t", id, got.Offline, err, want)
		}
	}

	first, second := f.newWorker(t, p, base), f.newWorker(t, q, base.Add(time.Second))
	before := base.Add(time.Hour)
	lost := func(ctx context.Context) (kernel.ULID, bool) {
		id, ok, err := f.store.Lost(ctx, before)
		if err != nil {
			t.Error(err)
		}
		return id, ok
	}
	took, release, done := make(chan kernel.ULID, 1), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- tx.Do(ctx, f.db, tx.Options{}, func(ctx context.Context) error {
			id, _ := lost(ctx)
			took <- id
			<-release
			return nil
		})
	}()
	if id := <-took; id != first.ID {
		t.Errorf("the first reap deleted %s, want the longest unseen %s", id, first.ID)
	}
	if id, ok := lost(ctx); !ok || id != second.ID {
		t.Errorf("the second reap, beside the first, = %s, %t; want %s", id, ok, second.ID)
	}
	if id, ok := lost(ctx); ok {
		t.Errorf("a third reap, beside the first, deleted %s", id)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if id, ok := lost(ctx); ok {
		t.Errorf("a reap after both deleted %s", id)
	}
	for _, id := range []kernel.ULID{first.ID, second.ID} {
		if _, _, err := f.store.Worker(ctx, id, false); !errors.Is(err, worker.ErrNotFound) {
			t.Errorf("lost worker %s: %v", id, err)
		}
	}

	timeouts, err := f.store.PoolTimeouts(ctx)
	if err != nil || len(timeouts) != 1 || timeouts[0].Pool != p.ID ||
		timeouts[0].Timeout != 5*time.Minute {
		t.Errorf("PoolTimeouts = %+v, %v", timeouts, err)
	}
}
