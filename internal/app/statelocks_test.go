package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	workerapi "github.com/weaveplatform/infraweave/internal/platform/gen/worker"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
)

// stateCalls sends a run's calls to the state backend through the API
// listener's handler, with HTTP Basic as Terraform's http backend sends
// them: user run and the run's token as the password.
type stateCalls struct {
	h     http.Handler
	token string
}

func (c stateCalls) send(
	t *testing.T,
	method, target, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	req.SetBasicAuth("run", c.token)
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	return rec
}

// lockInfo is a LockInfo of id as Terraform sends it.
func lockInfo(id string) string {
	return `{"ID":"` + id + `","Operation":"OperationTypePlan","Who":"runner@infraweave"}`
}

// TestAccStateLocks checks the state backend's locks as the server wires
// the access, runs, stacks and state domains over PostgreSQL. Two
// planning runs hold shared locks together; an applying run's exclusive
// lock is refused with the oldest holder's LockInfo as it was sent; a
// planning run may neither force-unlock nor release another run's lock;
// another stack of the account whose state infraweave does not keep
// answers 404, and a Bearer token 401 with the Basic challenge. A
// planning run's end, consumed from the outbox, releases its lock, after
// which its token may take none; the applying run's force-unlock then
// releases the other's lock and records the break on the trail.
func TestAccStateLocks(t *testing.T) {
	w := newClaimWorld(t)
	ctx := t.Context()
	stack := w.stacks["one"]
	w.exec(t, `UPDATE stacks SET manages_state = true WHERE id = $1`, stack)
	pool := w.pool(t, w.acct, nil)
	c := config.Defaults().Server
	h := API(discard(), c, memory(c), w.d, Platform{}, w.blobs)

	type claim struct {
		id    kernel.ULID
		v     int64
		run   runCalls
		state stateCalls
	}
	var claims [3]claim
	for i := range claims {
		id := w.run(t, w.acct, pool,
			runSpec{kernel.RunTypeTracked, kernel.RunPriorityNormal, false}, time.Now())
		p := w.poll(ctx, 0, w.worker(t, pool, false), false, 5)
		if claimed(t, p) != id {
			t.Fatalf("the claim took %+v, want %s", p.a, id)
		}
		claims[i] = claim{
			id: id, v: p.a.StateVersion,
			run: runCalls{
				h: h, token: p.a.RunToken, path: WorkerBaseURL + "/runs/" + id.String(),
			},
			state: stateCalls{h: h, token: p.a.RunToken},
		}
	}
	planA, planB, apply := claims[0], claims[1], claims[2]
	for _, r := range []*claim{&planA, &planB} {
		r.run.report(t, r.v, "INITIALIZING", "started", workerapi.Execute, "INITIALIZING", r.v+1)
		r.v++
		r.run.report(t, r.v, "INITIALIZING", "succeeded", workerapi.Execute, "PLANNING", r.v+1)
		r.v++
	}
	w.exec(t, `UPDATE runs SET state = 'APPLYING' WHERE id = $1`, apply.id)

	byID := stateapi.BaseURL + "/stacks/" + stack.String()
	bySlug := stateapi.BaseURL + "/stacks/one"
	var wg sync.WaitGroup
	shared := make([]*httptest.ResponseRecorder, 2)
	for i, r := range []claim{planA, planB} {
		wg.Go(func() {
			shared[i] = r.state.send(t, stateapi.MethodLock, bySlug, lockInfo(string(rune('a'+i))))
		})
	}
	wg.Wait()
	for i, rec := range shared {
		if rec.Code != http.StatusOK {
			t.Fatalf("shared lock %d = %d %s", i, rec.Code, rec.Body)
		}
	}
	var oldest string
	if err := w.q.QueryRow(ctx, `SELECT info::text FROM state_locks WHERE stack_id = $1
		ORDER BY created_at, lock_id LIMIT 1`, stack).Scan(&oldest); err != nil {
		t.Fatal(err)
	}
	rec := apply.state.send(t, stateapi.MethodLock, byID, lockInfo("c"))
	if rec.Code != http.StatusLocked || rec.Body.String() != oldest {
		t.Errorf("an exclusive lock beside shared ones = %d %s, want 423 %s", rec.Code, rec.Body,
			oldest)
	}
	wantProblem(t, planA.state.send(t, stateapi.MethodUnlock, bySlug, ""),
		http.StatusForbidden, "another run's lock")
	wantProblem(t, planA.state.send(t, stateapi.MethodUnlock, bySlug, lockInfo("b")),
		http.StatusForbidden, "another run's lock")
	wantProblem(t, planA.state.send(t, http.MethodGet, bySlug, ""),
		http.StatusNotImplemented, "state versions")
	wantProblem(t, planA.state.send(t, http.MethodGet, stateapi.BaseURL+"/stacks/two", ""),
		http.StatusNotFound, "two")
	req := httptest.NewRequestWithContext(ctx, stateapi.MethodLock, bySlug,
		strings.NewReader(lockInfo("d")))
	req.Header.Set("Authorization", "Bearer "+planA.run.token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	wantProblem(t, rec, http.StatusUnauthorized, "")
	if got := rec.Header().Get("WWW-Authenticate"); got != stateChallenge {
		t.Errorf("a Bearer token's challenge is %q", got)
	}

	ev := testEvents(t, w.q)
	if err := w.d.State.Subscribe(ev); err != nil {
		t.Fatal(err)
	}
	reg := queue.NewRegistry()
	if err := ev.Register(reg); err != nil {
		t.Fatal(err)
	}
	cfg := queue.Defaults()
	cfg.PollInterval = 20 * time.Millisecond
	workers := queue.NewWorkers(queue.New(w.q), reg, cfg, queue.AllowAll{}, nil, discard(), "test")
	running, stop := context.WithCancel(context.Background())
	wg.Go(func() { workers.Run(running) })
	t.Cleanup(func() {
		stop()
		wg.Wait()
	})
	planA.run.report(t, planA.v, "PLANNING", "failed", workerapi.Exit, "FAILED", planA.v+1)
	held := func(run kernel.ULID) int {
		var n int
		if err := w.q.QueryRow(ctx, `SELECT count(*) FROM state_locks WHERE run_id = $1`,
			run).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	deadline := time.After(15 * time.Second)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for held(planA.id) != 0 {
		select {
		case <-deadline:
			t.Fatal("the ended run's lock was never released")
		case <-tick.C:
		}
	}
	wantProblem(t, planA.state.send(t, stateapi.MethodLock, bySlug, lockInfo("a")),
		http.StatusForbidden, "may not lock")

	rec = apply.state.send(t, stateapi.MethodLock, bySlug, lockInfo("c"))
	if rec.Code != http.StatusLocked || rec.Body.String() != lockInfo("b") {
		t.Errorf("the exclusive lock beside the other = %d %s", rec.Code, rec.Body)
	}
	if rec := apply.state.send(t, stateapi.MethodUnlock, bySlug, ""); rec.Code != http.StatusOK {
		t.Fatalf("the applying run's force-unlock = %d %s", rec.Code, rec.Body)
	}
	if rec := apply.state.send(t, stateapi.MethodLock, bySlug, lockInfo("c")); rec.Code !=
		http.StatusOK {
		t.Errorf("the exclusive lock once released = %d %s", rec.Code, rec.Body)
	}
	var breaks int
	if err := w.q.QueryRow(ctx, `SELECT count(*) FROM audit_trail_entries
		WHERE account_id = $1 AND action = 'stack.state_lock_break' AND actor_run = $2
		AND affected_id = $3`, w.acct, apply.id, stack.String()).Scan(&breaks); err != nil ||
		breaks != 1 {
		t.Errorf("%d breaks recorded, %v", breaks, err)
	}
	if held(planB.id) != 0 || held(apply.id) != 1 {
		t.Errorf("planB holds %d, the applying run %d", held(planB.id), held(apply.id))
	}
}
