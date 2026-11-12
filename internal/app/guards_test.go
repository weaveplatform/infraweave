package app

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/idempotency"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/test/deps"
)

// TestAccGuardedReplicas runs two routers on one database with serve's
// controls, as two replicas run: a request through one is replayed through
// the other, a request in flight on one holds its key on both, both draw on
// one budget, and the operator's switch in the table turns both off.
func TestAccGuardedReplicas(t *testing.T) {
	db := sqltest.Open(t)
	c := config.Defaults().Server
	c.RateLimit.Standard = ratelimit.Limit{Rate: 1e-6, Burst: 100}
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()

	keys := testKeyring(t, db.Primary())
	var runs atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	replica := func() *chi.Mux {
		l := notify.NewListener(db.ConnConfig, discard())
		g := postgresGuards(ctx, &wg, c, db, keys, l)
		wg.Go(func() { l.Run(ctx) })
		r := Guarded(
			discard(),
			c,
			Operations{"POST /things": {Cost: ratelimit.Standard}},
			g,
			byAddress{},
			nil,
			nil,
		)
		r.Post("/things", func(w http.ResponseWriter, req *http.Request) {
			runs.Add(1)
			if req.Header.Get(idempotency.Header) == "slow" {
				close(entered)
				<-release
			}
			w.Header().Set("Location", "/things/1")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"1"}`))
		})
		return r
	}
	a, b := replica(), replica()
	const me = "192.0.2.1:1"

	if rec := send(a, http.MethodPost, "/things", me, "k1"); rec.Code != http.StatusCreated {
		t.Fatalf("through a: %d %s", rec.Code, rec.Body)
	}
	rec := send(b, http.MethodPost, "/things", me, "k1")
	if rec.Code != http.StatusCreated || rec.Header().Get(idempotency.ReplayedHeader) != "true" ||
		rec.Header().Get("Location") != "/things/1" || rec.Body.String() != `{"id":"1"}` ||
		runs.Load() != 1 {
		t.Errorf(
			"replayed through b: %d %v %s after %d runs",
			rec.Code,
			rec.Header(),
			rec.Body,
			runs.Load(),
		)
	}

	done := make(chan int, 1)
	go func() { done <- send(a, http.MethodPost, "/things", me, "slow").Code }()
	<-entered
	wantTyped(t, send(b, http.MethodPost, "/things", me, "slow"), problem.IdempotencyKeyInFlight)
	close(release)
	if code := <-done; code != http.StatusCreated {
		t.Errorf("the slow request: %d", code)
	}

	// Alternating replicas count down one budget.
	const other = "198.51.100.7:1"
	for i, r := range []*chi.Mux{a, b, a, b} {
		rec := send(r, http.MethodGet, "/things", other, "")
		if got, want := rec.Header().Get("X-RateLimit-Remaining"), strconv.Itoa(99-i); got != want {
			t.Errorf("request %d: X-RateLimit-Remaining %s, want %s", i+1, got, want)
		}
	}

	const off = "203.0.113.9"
	for _, r := range []*chi.Mux{a, b} {
		if rec := send(
			r,
			http.MethodGet,
			"/things",
			off+":1",
			"",
		); rec.Code == http.StatusServiceUnavailable {
			t.Fatal("disabled before the table says so")
		}
	}
	if _, err := db.Primary().Exec(t.Context(),
		`INSERT INTO api_disabled_callers (caller, reason) VALUES ($1, 'test')`,
		addressCaller(off)); err != nil {
		t.Fatal(err)
	}
	if err := notify.Notify(
		t.Context(),
		db.Primary(),
		notify.Invalidate,
		ratelimit.DisabledCallersKey,
	); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]*chi.Mux{"a": a, "b": b} {
		err := deps.Eventually(t.Context(), name+" to disable the caller",
			func(context.Context) (bool, error) {
				return send( //nolint:contextcheck // send builds a request of its own
					r,
					http.MethodGet,
					"/things",
					off+":1",
					"",
				).Code == http.StatusServiceUnavailable, nil
			})
		if err != nil {
			t.Error(err)
		}
	}
	if rec := send(
		a,
		http.MethodGet,
		"/things",
		me,
		"",
	); rec.Code == http.StatusServiceUnavailable {
		t.Error("another caller was disabled")
	}
}
