package ratelimit

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/test/deps"
)

func TestMain(m *testing.M) {
	os.Exit(sqltest.Main(m, schema.Stream()))
}

func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(t.Context(), sqltest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func exec(t *testing.T, p *pgxpool.Pool, stmt string, args ...any) {
	t.Helper()
	if _, err := p.Exec(t.Context(), stmt, args...); err != nil {
		t.Fatal(err)
	}
}

// TestAccPostgresBucket checks the refill arithmetic on the database clock.
// Time passes by moving refilled_at back rather than by waiting, so each
// expectation allows for the milliseconds the test itself takes.
func TestAccPostgresBucket(t *testing.T) {
	p := pool(t)
	s := NewPostgres(p)
	l := Limit{Rate: 2, Burst: 3}
	k := Key{Caller: "a", Class: Standard}
	take := func() State {
		t.Helper()
		st, err := s.Take(t.Context(), k, l)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	age := func(d string) {
		t.Helper()
		exec(t, p, `UPDATE rate_limit_buckets SET refilled_at = refilled_at - $1::interval
WHERE caller = 'a' AND class = 'standard'`, d)
	}

	for want := 2; want >= 0; want-- {
		if st := take(); !st.Allowed || st.Remaining != want {
			t.Fatalf("burst: %+v, want allowed with %d left", st, want)
		}
	}
	st := take()
	if st.Allowed || st.Remaining != 0 || st.RetryAfter <= 400*time.Millisecond ||
		st.RetryAfter > 500*time.Millisecond || st.Reset > 1500*time.Millisecond {
		t.Fatalf("empty: %+v, want a wait of just under 500ms", st)
	}
	age("500 milliseconds")
	if st := take(); !st.Allowed || st.Remaining != 0 {
		t.Fatalf("after half a second at 2 a second: %+v", st)
	}
	age("1 hour")
	if st := take(); !st.Allowed || st.Remaining != 2 {
		t.Fatalf("refill stops at the burst: %+v", st)
	}
	// A refilled_at in the future, as after the clock stepped back, refills
	// nothing and stays where it is.
	exec(t, p, `UPDATE rate_limit_buckets SET refilled_at = now() + interval '1 minute'`)
	if st := take(); !st.Allowed || st.Remaining != 1 {
		t.Fatalf("clock stepped back: %+v", st)
	}
	var ahead bool
	if err := p.QueryRow(
		t.Context(),
		`SELECT refilled_at > now() FROM rate_limit_buckets WHERE caller = 'a'`,
	).Scan(&ahead); err != nil || !ahead {
		t.Errorf("refilled_at moved back: %v %v", ahead, err)
	}

	for _, other := range []Key{{"a", Expensive}, {"b", Standard}} {
		if st, err := s.Take(
			t.Context(),
			other,
			Limit{Rate: 1, Burst: 1},
		); err != nil || !st.Allowed ||
			st.Remaining != 0 {
			t.Errorf("%+v: %+v %v", other, st, err)
		}
	}
}

// TestAccPostgresSharedBudget takes from one bucket through two stores, as
// two replicas do, and concurrently: exactly the burst is admitted.
func TestAccPostgresSharedBudget(t *testing.T) {
	p := pool(t)
	stores := []*Postgres{NewPostgres(p), NewPostgres(p)}
	l := Limit{Rate: 1e-9, Burst: 10}
	var (
		wg      sync.WaitGroup
		allowed atomic.Int32
		errs    atomic.Int32
	)
	start := make(chan struct{})
	for i := range 40 {
		wg.Go(func() {
			<-start
			st, err := stores[i%2].Take(context.Background(), Key{"a", Standard}, l)
			if err != nil {
				errs.Add(1)
			}
			if st.Allowed {
				allowed.Add(1)
			}
		})
	}
	close(start)
	wg.Wait()
	if allowed.Load() != 10 || errs.Load() != 0 {
		t.Errorf("%d admitted and %d failed, want the burst of 10", allowed.Load(), errs.Load())
	}
}

func TestAccPostgresUnreachable(t *testing.T) {
	p := pool(t)
	s, table := NewPostgres(p), NewTable(p, time.Now)
	p.Close()
	if _, err := s.Take(context.Background(), Key{"a", Standard}, Defaults().Standard); err == nil {
		t.Error("Take on a closed pool")
	}
	if _, err := table.Disabled(context.Background(), "a"); err == nil {
		t.Error("Disabled on a closed pool")
	}
}

// TestAccTable checks the copy each replica keeps of api_disabled_callers:
// it is read once, reread after an invalidation, and reread once the
// earliest until passes.
func TestAccTable(t *testing.T) {
	p := pool(t)
	c := newClock()
	table := NewTable(p, c.now)
	disabled := func(caller string) bool {
		t.Helper()
		off, err := table.Disabled(t.Context(), caller)
		if err != nil {
			t.Fatal(err)
		}
		return off
	}

	exec(t, p, `INSERT INTO api_disabled_callers (caller, reason) VALUES ('a', 'abuse')`)
	if !disabled("a") || disabled("b") {
		t.Fatal("first read")
	}
	exec(
		t,
		p,
		`INSERT INTO api_disabled_callers (caller, until) VALUES ('b', now() + interval '1 hour')`,
	)
	if disabled("b") {
		t.Error("the copy was reread without an invalidation")
	}
	table.Invalidate()
	if !disabled("b") {
		t.Error("an invalidation did not reread the table")
	}
	// The database decides when b's hour is over; the replica's clock only
	// says when to ask.
	exec(
		t,
		p,
		`UPDATE api_disabled_callers SET until = now() - interval '1 second' WHERE caller = 'b'`,
	)
	if !disabled("b") {
		t.Error("the copy was reread before its expiry")
	}
	c.advance(time.Hour)
	if disabled("b") || !disabled("a") {
		t.Error("the copy was not reread at its expiry")
	}
	exec(
		t,
		p,
		`INSERT INTO api_disabled_callers (caller, until) VALUES ('c', now() - interval '1 second')`,
	)
	table.Invalidate()
	if disabled("c") {
		t.Error("a caller whose until has passed is disabled")
	}
}

// TestAccTableWatch checks that the copy follows NOTIFY on the listener.
func TestAccTableWatch(t *testing.T) {
	dsn := sqltest.DSN(t)
	p, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	l := notify.NewListener(notify.Static(p.Config().ConnConfig), slog.New(slog.DiscardHandler))
	sub, err := l.Subscribe(notify.Invalidate)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	table := NewTable(p, time.Now)
	go l.Run(ctx)
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		table.Watch(ctx, sub)
	}()

	if off, err := table.Disabled(t.Context(), "a"); off || err != nil {
		t.Fatalf("before: %v %v", off, err)
	}
	exec(t, p, `INSERT INTO api_disabled_callers (caller) VALUES ('a')`)
	if err := notify.Notify(t.Context(), p, notify.Invalidate, "something_else"); err != nil {
		t.Fatal(err)
	}
	if err := notify.Notify(t.Context(), p, notify.Invalidate, DisabledCallersKey); err != nil {
		t.Fatal(err)
	}
	if err := deps.Eventually(t.Context(), "the switch to follow NOTIFY",
		func(ctx context.Context) (bool, error) { return table.Disabled(ctx, "a") }); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-watched
}
