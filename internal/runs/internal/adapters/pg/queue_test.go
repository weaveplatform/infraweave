package pg_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// TestAccQueueSearchesAndCounts reads a private pool's queue in priority
// tier then creation order, the public pool's in creation order without
// its prohibited runs, filters keeping positions, the counts of both, and
// a scope without the stack reading none.
func TestAccQueueSearchesAndCounts(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	pool, other := kernel.NewULID(), kernel.NewULID()
	org := kernel.NewOrganizationID()
	low := w.waits(t, w.acct, pool, kernel.RunStateReady, kernel.RunTypeTracked, kernel.RunPriorityNormal, false, w.now.Add(-3*time.Minute))
	high := w.waits(t, w.acct, pool, kernel.RunStateReady, kernel.RunTypeTracked, kernel.RunPriorityHigh, false, w.now)
	w.waits(t, w.acct, other, kernel.RunStateReady, kernel.RunTypeTracked, kernel.RunPriorityNormal, false, w.now)
	w.waits(t, w.acct, pool, kernel.RunStatePlanning, kernel.RunTypeTracked, kernel.RunPriorityNormal, false, w.now)
	first := publicWaiting(t, w, w.acct, org, w.now.Add(-2*time.Minute))
	second := publicWaiting(t, w, w.acct, org, w.now.Add(-time.Minute))
	drift := publicWaiting(t, w, w.acct, org, w.now.Add(-time.Hour))
	held := publicWaiting(t, w, w.acct, org, w.now.Add(-time.Hour))
	if _, err := w.db.Exec(ctx, `UPDATE runs SET drift_detection = true WHERE id = $1`, drift); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.Exec(ctx, `UPDATE runs SET state = 'PLANNING', public_worker_pool_id = $2 WHERE id = $1`,
		held, kernel.NewULID()); err != nil {
		t.Fatal(err)
	}
	space := kernel.NewULID()
	scope := read.Scope{
		Reachable: []kernel.ULID{space},
		Stacks:    []stacksapi.Stack{{ID: w.stack, Account: w.acct, Slug: "network", Name: "Network", Space: space}},
	}
	positions := func(q read.Queue, in search.Input) []string {
		t.Helper()
		res, err := w.store.SearchQueue(ctx, w.acct, q, machine.Claimable(), in, scope)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(res.Edges))
		for _, e := range res.Edges {
			out = append(out, e.Node.Run.ID.String()+"@"+string(rune('0'+e.Node.Position)))
		}
		return out
	}
	at := func(id kernel.ULID, n int) string { return id.String() + "@" + string(rune('0'+n)) }
	private, public := read.Queue{Pool: &pool}, read.Queue{}
	for _, c := range []struct {
		what      string
		got, want []string
	}{
		{"the private queue", positions(private, search.Input{}), []string{at(high, 1), at(low, 2)}},
		{"the public queue", positions(public, search.Input{}), []string{at(first, 1), at(second, 2)}},
		{"the private queue's normal runs", positions(private, search.Input{Predicates: []search.Predicate{{
			Field: "isPrioritized", Constraint: search.Constraint{BooleanEquals: []bool{false}},
		}}}), []string{at(low, 2)}},
		{
			"the public queue newest first", positions(public, search.Input{OrderBy: []search.Order{{Field: "createdAt", Desc: true}}}),
			[]string{at(second, 2), at(first, 1)},
		},
		{"the public queue's drift runs", positions(public, search.Input{Predicates: []search.Predicate{{
			Field: "isDriftDetection", Constraint: search.Constraint{BooleanEquals: []bool{true}},
		}}}), []string{}},
	} {
		if !slices.Equal(c.got, c.want) {
			t.Errorf("%s = %v, want %v", c.what, c.got, c.want)
		}
	}
	sug, err := w.store.SuggestQueue(ctx, w.acct, public, machine.Claimable(), search.SuggestionsInput{Fields: []string{"state", "position"}}, scope)
	if err != nil || sug.Count != 2 {
		t.Errorf("SuggestQueue = %+v, %v", sug, err)
	}
	counts, err := w.store.CountQueues(ctx, w.acct, []kernel.ULID{pool, other, kernel.NewULID()}, machine.Claimable(), scope)
	if want := map[kernel.ULID]int{pool: 2, other: 1}; err != nil || !maps.Equal(counts, want) {
		t.Errorf("CountQueues = %v, %v; want %v", counts, err, want)
	}
	waiting, busy, err := w.store.CountPublic(ctx, w.acct, machine.Claimable(), []kernel.RunState{kernel.RunStatePlanning}, scope)
	if err != nil || waiting != 2 || busy != 1 {
		t.Errorf("CountPublic = %d waiting, %d busy, %v; want 2 and 1", waiting, busy, err)
	}
	none := read.Scope{Reachable: scope.Reachable}
	if res, err := w.store.SearchQueue(ctx, w.acct, private, machine.Claimable(), search.Input{}, none); err != nil || len(res.Edges) != 0 {
		t.Errorf("a queue of no readable stack = %+v, %v", res, err)
	}
	if _, err := w.store.SearchQueue(ctx, w.acct, public, machine.Claimable(), search.Input{Predicates: []search.Predicate{{
		Field: "position", Constraint: search.Constraint{StringEquals: []string{"1"}},
	}}}, scope); !errors.As(err, new(*search.Invalid)) {
		t.Errorf("a filter on position = %v, want *search.Invalid", err)
	}
	w.db.Close()
	if _, err := w.store.CountQueues(ctx, w.acct, []kernel.ULID{pool}, machine.Claimable(), scope); err == nil {
		t.Error("CountQueues with PostgreSQL closed succeeded")
	}
	if _, _, err := w.store.CountPublic(ctx, w.acct, machine.Claimable(), nil, scope); err == nil {
		t.Error("CountPublic with PostgreSQL closed succeeded")
	}
	if _, err := w.store.SearchQueue(ctx, w.acct, public, machine.Claimable(), search.Input{}, scope); err == nil {
		t.Error("SearchQueue with PostgreSQL closed succeeded")
	}
	if _, err := w.store.SuggestQueue(ctx, w.acct, public, machine.Claimable(), search.SuggestionsInput{}, scope); err == nil {
		t.Error("SuggestQueue with PostgreSQL closed succeeded")
	}
}

// slugged is an access domain that also names its tree's spaces.
type slugged struct{ *fakeAccess }

func (s slugged) Slugs(context.Context, kernel.AccountID) (map[kernel.ULID]string, error) {
	out := map[kernel.ULID]string{}
	for _, n := range s.tree {
		out[n.ID] = n.Slug
	}
	return out, s.err["Slugs"]
}

// TestAccQueueService reads the queues as a caller does: each run with
// its stack, the stack's provider and its space's slug; the counts the
// workers domain reads; and the public queue empty while public execution
// is switched off, though its runs still count as pending.
func TestAccQueueService(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	pool := kernel.NewULID()
	org := kernel.NewOrganizationID()
	private := w.waits(t, w.acct, pool, kernel.RunStateReady, kernel.RunTypeTracked, kernel.RunPriorityNormal, false, w.now)
	public := publicWaiting(t, w, w.acct, org, w.now)
	st := w.stacks.settings[w.stack]
	st.Provider = "GITHUB"
	w.stacks.settings[w.stack] = st
	a := slugged{w.access()}
	p := kernel.Principal{AccountID: w.acct}
	for _, enabled := range []bool{true, false} {
		svc := read.New(read.Deps{
			DB: w.db, Store: w.store, Stacks: w.stacks, Spaces: a, Authz: a, Queue: w.store, PublicPoolEnabled: enabled,
		})
		res, err := svc.SearchQueue(ctx, p, read.Queue{Pool: &pool}, search.Input{})
		if err != nil || len(res.Edges) != 1 {
			t.Fatalf("SearchQueue = %+v, %v", res, err)
		}
		v := res.Edges[0].Node
		if v.Run.ID != private || v.Position != 1 || v.Stack.Slug != "network" || v.Provider != "GITHUB" || v.Space != "legacy" {
			t.Errorf("the queued run = %+v", v)
		}
		res, err = svc.SearchQueue(ctx, p, read.Queue{}, search.Input{})
		if want := map[bool]int{true: 1, false: 0}[enabled]; err != nil || len(res.Edges) != want {
			t.Errorf("enabled %t: the public queue = %+v, %v; want %d runs", enabled, res, err, want)
		} else if enabled && res.Edges[0].Node.Run.ID != public {
			t.Errorf("the public queue = %+v", res)
		}
		sug, err := svc.SuggestQueue(ctx, p, read.Queue{}, search.SuggestionsInput{})
		if want := map[bool]int{true: 1, false: 0}[enabled]; err != nil || sug.Count != want {
			t.Errorf("enabled %t: the public queue's suggestions = %+v, %v", enabled, sug, err)
		}
		counts, err := svc.PoolQueues(ctx, p, []kernel.ULID{pool})
		if err != nil || counts[pool] != (workersapi.QueueCounts{Pending: 1, Schedulable: 1}) {
			t.Errorf("PoolQueues = %v, %v", counts, err)
		}
		pub, err := svc.PublicQueue(ctx, p)
		want := workersapi.QueueCounts{Pending: 1, Schedulable: map[bool]int{true: 1, false: 0}[enabled]}
		if err != nil || pub != want {
			t.Errorf("enabled %t: PublicQueue = %+v, %v; want %+v", enabled, pub, err, want)
		}
	}
	svc := read.New(read.Deps{DB: w.db, Store: w.store, Stacks: w.stacks, Spaces: a, Authz: a, Queue: w.store})
	if got, err := svc.PoolQueues(ctx, p, nil); err != nil || len(got) != 0 {
		t.Errorf("PoolQueues of no pool = %v, %v", got, err)
	}
	for _, key := range []string{"Reachable", "Slugs"} {
		a.err[key] = kernel.ErrUnavailable
		if _, err := svc.SearchQueue(ctx, p, read.Queue{Pool: &pool}, search.Input{}); !errors.Is(err, kernel.ErrUnavailable) {
			t.Errorf("SearchQueue with %s failing = %v", key, err)
		}
		delete(a.err, key)
	}
	a.err["Reachable"] = kernel.ErrUnavailable
	if _, err := svc.SuggestQueue(ctx, p, read.Queue{}, search.SuggestionsInput{}); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("SuggestQueue with the access domain down = %v", err)
	}
	if _, err := svc.PoolQueues(ctx, p, []kernel.ULID{pool}); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("PoolQueues with the access domain down = %v", err)
	}
	if _, err := svc.PublicQueue(ctx, p); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("PublicQueue with the access domain down = %v", err)
	}
	delete(a.err, "Reachable")
	w.stacks.err["Settings"] = kernel.ErrUnavailable
	if _, err := svc.SearchQueue(ctx, p, read.Queue{Pool: &pool}, search.Input{}); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("SearchQueue with the stacks domain down = %v", err)
	}
	delete(w.stacks.err, "Settings")
	if _, err := svc.SearchQueue(ctx, p, read.Queue{}, search.Input{After: "nope"}); err == nil {
		t.Error("SearchQueue after a cursor it never issued succeeded")
	}
	w.db.Close()
	if _, err := svc.PoolQueues(ctx, p, []kernel.ULID{pool}); err == nil {
		t.Error("PoolQueues with PostgreSQL closed succeeded")
	}
	if _, err := svc.PublicQueue(ctx, p); err == nil {
		t.Error("PublicQueue with PostgreSQL closed succeeded")
	}
}
