package app

import (
	"bytes"
	"cmp"
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	workerapi "github.com/weaveplatform/infraweave/internal/platform/gen/worker"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/test/acceptance"
)

// claimWorld is two replicas of the server over one database, each waking
// its held polls from infraweave_runs, with the hold world's account, its
// organization, and stacks for runs to name.
type claimWorld struct {
	holdWorld
	replicas [2]Domains
	org      kernel.OrganizationID
	stacks   map[string]kernel.ULID
}

func newClaimWorld(t *testing.T) *claimWorld {
	t.Helper()
	h := newHoldWorld(t)
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})
	b, err := newDomains(config.Defaults(), h.db, h.keys, h.blobs.Objects, nil, testEvents(t, h.q),
		discard())
	if err != nil {
		t.Fatal(err)
	}
	w := &claimWorld{holdWorld: h, replicas: [2]Domains{h.d, b}, stacks: map[string]kernel.ULID{}}
	for _, d := range w.replicas {
		l := notify.NewListener(h.db.ConnConfig, discard())
		sub, _ := l.Subscribe(notify.Runs) // one of notify.Channels, so never an error
		wg.Go(func() { l.Run(ctx) })
		wg.Go(func() { d.Workers.WatchPolls(ctx, sub) })
	}
	if err := h.q.QueryRow(t.Context(), `SELECT organization_id FROM accounts WHERE id = $1`,
		h.acct).Scan(&w.org); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "two", "three", "wake"} {
		space := "kept"
		if _, err := h.d.Stacks.API().StackCreate(h.admin, api.StackCreateRequestObject{
			Body: &api.StackCreateRequest{Input: api.StackInput{
				Name: name, Repository: "infra", Branch: "main", Space: &space,
			}},
		}); err != nil {
			t.Fatal(err)
		}
		var id kernel.ULID
		if err := h.q.QueryRow(t.Context(), `SELECT id FROM stacks
			WHERE account_id = $1 AND slug = $2`, h.acct, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		w.stacks[name] = id
	}
	return w
}

func (w *claimWorld) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := w.q.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

// pool inserts a pool of acct, its drift detection run limit limit.
func (w *claimWorld) pool(t *testing.T, acct kernel.AccountID, limit *int) kernel.ULID {
	t.Helper()
	id := kernel.NewULID()
	w.exec(t, `INSERT INTO worker_pools (account_id, organization_id, id, name, space_id,
		certificate, serial, drift_detection_run_limit)
		VALUES ($1, $2, $3, $4, $5, '\x00', 1, $6)`,
		acct, w.org, id, id.String(), w.kept, limit)
	return id
}

// worker inserts an idle worker of the account's pool, drained when
// drained.
func (w *claimWorld) worker(t *testing.T, pool kernel.ULID, drained bool) kernel.ULID {
	t.Helper()
	id := kernel.NewULID()
	w.exec(t, `INSERT INTO workers (account_id, organization_id, id, pool_id, serial, drained)
		VALUES ($1, $2, $3, $4, 1, $5)`, w.acct, w.org, id, pool, drained)
	return id
}

// runSpec is a run's type, priority and drift flag.
type runSpec struct {
	typ   kernel.RunType
	p     kernel.RunPriority
	drift bool
}

func (s runSpec) tier() int { return kernel.RunTier(s.p, s.typ, s.drift) }

// run inserts a READY run of acct waiting in pool, created at, on the
// stack one.
func (w *claimWorld) run(
	t *testing.T,
	acct kernel.AccountID,
	pool kernel.ULID,
	s runSpec,
	at time.Time,
) kernel.ULID {
	t.Helper()
	id := kernel.NewULID()
	w.exec(t, `INSERT INTO runs (account_id, id, stack_id, type, state, commit, triggered_by,
		priority, drift_detection, priority_tier, worker_pool_id, created_at)
		VALUES ($1, $2, $3, $4, 'READY', '{"hash":"abc"}', 'alice', $5, $6, $7, $8, $9)`,
		acct, id, w.stacks["one"], string(s.typ), string(s.p), s.drift, s.tier(), pool, at)
	return id
}

// polled is a poll's answer.
type polled struct {
	a    *workerapi.Assignment
	err  error
	took time.Duration
}

// poll polls as the idle worker through replica r, with the drain flag
// drained, asking to be held wait seconds.
func (w *claimWorld) poll(
	ctx context.Context,
	r int,
	worker kernel.ULID,
	drained bool,
	wait int32,
) polled {
	start := time.Now()
	ctx = accessapi.WithBearer(ctx,
		accessapi.Bearer{ID: worker, Expires: time.Now().Add(time.Hour)})
	res, err := w.replicas[r%len(w.replicas)].Workers.Protocol().WorkerPoll(ctx,
		workerapi.WorkerPollRequestObject{
			Body: &workerapi.PollRequest{Drained: &drained, Wait: &wait},
		})
	if err != nil {
		return polled{err: err, took: time.Since(start)}
	}
	ok, _ := res.(workerapi.WorkerPoll200JSONResponse)
	return polled{a: ok.Body.Assignment, took: time.Since(start)}
}

// pollAll polls as each worker at once, alternating the replicas, the
// drained ones with their drain flag, each held at most MinWait.
func (w *claimWorld) pollAll(
	ctx context.Context,
	workers []kernel.ULID,
	drained ...kernel.ULID,
) []polled {
	out := make([]polled, len(workers))
	var wg sync.WaitGroup
	for i, id := range workers {
		wg.Go(func() { out[i] = w.poll(ctx, i, id, slices.Contains(drained, id), 5) })
	}
	wg.Wait()
	return out
}

// claimed is the run p assigns, failing the test when it assigns none,
// failed, or assigns a run not in PREPARING or with no run token.
func claimed(t *testing.T, p polled) kernel.ULID {
	t.Helper()
	if p.err != nil || p.a == nil {
		t.Errorf("no run assigned: %+v", p)
		return kernel.ULID{}
	}
	run, err := kernel.ParseULID(p.a.Run)
	if err != nil || p.a.State != workerapi.AssignmentStatePREPARING || p.a.RunToken == "" {
		t.Errorf("assignment %+v", *p.a)
	}
	return run
}

// waiting checks that each of runs is READY with no worker.
func (w *claimWorld) waiting(t *testing.T, runs ...kernel.ULID) {
	t.Helper()
	for _, id := range runs {
		var (
			state  string
			worker *kernel.ULID
		)
		if err := w.q.QueryRow(t.Context(), `SELECT state, worker_id FROM runs WHERE id = $1`,
			id).Scan(&state, &worker); err != nil {
			t.Fatal(err)
		}
		if state != string(kernel.RunStateReady) || worker != nil {
			t.Errorf("run %s is %s, claimed by %v", id, state, worker)
		}
	}
}

// TestAccRunClaim checks the claim as the server wires the runs and
// workers domains, on two replicas of one database. Fifty idle workers
// polling at once through both claim the pool's first fifty runs by tier,
// creation and ID, one each, and later workers claim the rest in that
// order; a worker that holds a run claims none, nor does a drained one,
// and no worker claims another pool's or another account's run. A pool's
// drift runs below the first tier wait while its drift runs on workers
// reach its limit, which two claims at once do not pass, while its other
// tiers go on. A run made ready while a poll is held is claimed by it
// long before the poll's hold ends.
func TestAccRunClaim(t *testing.T) {
	w := newClaimWorld(t)
	ctx := t.Context()
	specs := []runSpec{
		{kernel.RunTypeProposed, kernel.RunPriorityHigh, false},
		{kernel.RunTypeTracked, kernel.RunPriorityNormal, false},
		{kernel.RunTypeProposed, kernel.RunPriorityNormal, false},
		{kernel.RunTypeTracked, kernel.RunPriorityLow, false},
		{kernel.RunTypeProposed, kernel.RunPriorityLow, false},
		{kernel.RunTypeProposed, kernel.RunPriorityNormal, true},
		{kernel.RunTypeProposed, kernel.RunPriorityLow, true},
	}
	type queued struct {
		id   kernel.ULID
		tier int
		at   time.Time
	}
	main := w.pool(t, w.acct, nil)
	base := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	runs := make([]queued, 0, 60)
	for i := range 60 {
		// Runs are created out of order, two to a second, so creation and
		// then the ID order each tier.
		s, at := specs[i%len(specs)], base.Add(time.Duration(i*37%60/2)*time.Second)
		runs = append(runs, queued{id: w.run(t, w.acct, main, s, at), tier: s.tier(), at: at})
	}
	slices.SortFunc(runs, func(a, b queued) int {
		return cmp.Or(
			cmp.Compare(a.tier, b.tier),
			a.at.Compare(b.at),
			bytes.Compare(a.id[:], b.id[:]),
		)
	})
	foreign := w.run(t, kernel.NewAccountID(), main, specs[0], base)
	other := w.run(t, w.acct, w.pool(t, w.acct, nil), specs[0], base)

	workers := make([]kernel.ULID, 50)
	for i := range workers {
		workers[i] = w.worker(t, main, false)
	}
	holder := map[kernel.ULID]kernel.ULID{}
	for i, p := range w.pollAll(ctx, workers) {
		run := claimed(t, p)
		if prev, ok := holder[run]; ok {
			t.Errorf("run %s claimed by %s and %s", run, prev, workers[i])
		}
		holder[run] = workers[i]
	}
	for i, r := range runs[:50] {
		var (
			state  string
			worker *kernel.ULID
		)
		if err := w.q.QueryRow(ctx, `SELECT state, worker_id FROM runs WHERE id = $1`,
			r.id).Scan(&state, &worker); err != nil {
			t.Fatal(err)
		}
		if h, ok := holder[r.id]; !ok || state != string(kernel.RunStatePreparing) ||
			worker == nil || *worker != h {
			t.Errorf("run %d, of tier %d, is %s by %v, not claimed first by %v", i, r.tier,
				state, worker, h)
		}
	}
	var busy int
	if err := w.q.QueryRow(ctx, `SELECT count(*) FROM workers WHERE id = ANY($1) AND busy`,
		workers).Scan(&busy); err != nil || busy != 50 {
		t.Errorf("%d of the claimants busy: %v", busy, err)
	}

	// A launcher that lost its answer polls idle again, and claims none of
	// the ten runs left while its run is on a worker.
	drained := w.worker(t, main, true)
	for i, p := range w.pollAll(ctx, append(slices.Clone(workers), drained), drained) {
		if p.err != nil || p.a != nil {
			t.Errorf("poll %d of a worker that may not claim = %+v", i, p)
		}
	}
	for i, r := range runs[50:] {
		if got := claimed(t, w.poll(ctx, i, w.worker(t, main, false), false, 5)); got != r.id {
			t.Errorf("claim %d took %s, want %s of tier %d", 50+i, got, r.id, r.tier)
		}
	}
	if p := w.poll(ctx, 0, w.worker(t, main, false), false, 5); p.err != nil || p.a != nil {
		t.Errorf("a claim of another pool's or account's run = %+v", p)
	}
	w.waiting(t, foreign, other)

	// The drift limit: a high drift run takes the one slot, and the
	// pool's other tiers go on.
	{
		limit := 1
		drift := w.pool(t, w.acct, &limit)
		high := w.run(
			t,
			w.acct,
			drift,
			runSpec{kernel.RunTypeProposed, kernel.RunPriorityHigh, true},
			base,
		)
		normal := w.run(t, w.acct, drift, specs[5], base)
		low := w.run(t, w.acct, drift, specs[6], base)
		proposal := w.run(t, w.acct, drift, specs[4], base.Add(time.Hour))
		for i, want := range []kernel.ULID{high, proposal} {
			if got := claimed(t, w.poll(ctx, i, w.worker(t, drift, false), false, 5)); got != want {
				t.Errorf("claim %d took %s, want %s", i, got, want)
			}
		}
		if p := w.poll(ctx, 0, w.worker(t, drift, false), false, 5); p.err != nil || p.a != nil {
			t.Errorf("a drift run past the limit = %+v", p)
		}
		w.exec(t, `UPDATE runs SET state = 'FINISHED' WHERE id = $1`, high)
		var took []kernel.ULID
		for _, p := range w.pollAll(ctx, []kernel.ULID{
			w.worker(t, drift, false), w.worker(t, drift, false),
		}) {
			if p.err != nil {
				t.Errorf("a racing claim = %+v", p)
			} else if p.a != nil {
				took = append(took, claimed(t, p))
			}
		}
		if len(took) != 1 {
			t.Fatalf("two claims at once took %v under a limit of one", took)
		}
		left := normal
		if took[0] == normal {
			left = low
		}
		w.waiting(t, left)
	}

	// A held poll's wake-up: the run a trigger makes ready.
	{
		pool := w.pool(t, w.acct, nil)
		w.exec(t, `UPDATE stacks SET worker_pool_id = $1,
			tracked_commit = '{"hash":"abc","message":"m","authorName":"a","timestamp":"2026-01-01T00:00:00Z"}'
			WHERE id = $2`, pool, w.stacks["wake"])
		worker := w.worker(t, pool, false)
		held := make(chan polled, 1)
		start := time.Now()
		go func() { held <- w.poll(ctx, 1, worker, false, 60) }()
		if err := acceptance.Eventually(
			ctx,
			"the poll is seen",
			func(ctx context.Context) (bool, error) {
				var seen bool
				err := w.q.QueryRow(ctx, `SELECT last_seen_at >= $2 FROM workers WHERE id = $1`,
					worker, start).Scan(&seen)
				return seen, err
			},
		); err != nil {
			t.Fatal(err)
		}
		proposed := api.RunTypePROPOSED
		res, err := w.d.Runs.API().RunTrigger(w.admin, api.RunTriggerRequestObject{
			StackId: "wake", Body: &api.RunTriggerRequest{RunType: &proposed},
		})
		if err != nil {
			t.Fatal(err)
		}
		run := res.(api.RunTrigger200JSONResponse).Body.Id
		sched := w.d.Runs.Jobs()[0]
		jobs, err := queue.New(w.q).Claim(ctx, sched.Kind.Queue, []string{sched.Kind.Name}, 10,
			time.Minute, "test")
		if err != nil || len(jobs) != 1 {
			t.Fatalf("schedule jobs %v: %v", jobs, err)
		}
		if err := sched.Handler(ctx, jobs[0]); err != nil {
			t.Fatal(err)
		}
		p := <-held
		if got := claimed(t, p); got.String() != run || p.took >= 30*time.Second {
			t.Errorf("the held poll answered %s after %s, want %s", got, p.took, run)
		}
	}
}
