package queue

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream())) }

// TestAccWorkersWakeUp checks that a committed job wakes an idle queue's
// workers through infraweave_jobs: with an hour's poll interval nothing
// else could claim it in time.
func TestAccWorkersWakeUp(t *testing.T) {
	db := sqltest.Open(t)
	q := New(db.Primary())
	cfg := Defaults()
	cfg.PollInterval = time.Hour
	k := Kind{Name: "test.wake", Queue: Events, Policy: Ignore}
	ran := make(chan time.Time, 1)
	r := NewRegistry()
	if err := r.Register(k, func(context.Context, Job) error {
		ran <- time.Now()
		return nil
	}, HandlerOptions{}); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	l := notify.NewListener(db.ConnConfig, log)
	sub, err := l.Subscribe(notify.Jobs)
	if err != nil {
		t.Fatal(err)
	}
	// probe sees the listener connect, after which nothing sent is lost.
	probe, err := l.Subscribe(notify.Jobs)
	if err != nil {
		t.Fatal(err)
	}
	idle := make(chan Name, 8)
	w := NewWorkers(q, r, cfg, AllowAll{}, sub.C, log, "test")
	w.waiting = func(n Name) {
		select {
		case idle <- n:
		default:
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { l.Run(ctx) })
	wg.Go(func() { w.Run(ctx) })
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})
	if m := <-probe.C; !m.Resync {
		t.Fatalf("first message %+v, want the connection's resync", m)
	}
	<-idle
	sent := time.Now()
	if _, err := q.Enqueue(t.Context(), k, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case at := <-ran:
		t.Logf("claimed %s after the commit", at.Sub(sent))
	case <-time.After(10 * time.Second):
		t.Fatal("the job was not claimed: no notification woke the workers")
	}
}

// TestUnitWorkersClaimFailure checks that a queue whose claims fail logs it
// once rather than every poll, and keeps polling.
func TestUnitWorkersClaimFailure(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), "postgres://iw:iw@127.0.0.1:1/iw?connect_timeout=5")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	r := NewRegistry()
	if err := r.Register(Kind{Name: "test.ok", Queue: Default, Policy: Ignore},
		func(context.Context, Job) error { return nil }, HandlerOptions{}); err != nil {
		t.Fatal(err)
	}
	cfg := Defaults()
	cfg.PollInterval = 5 * time.Millisecond
	var out syncBuffer
	w := NewWorkers(
		New(pool),
		r,
		cfg,
		AllowAll{},
		nil,
		slog.New(slog.NewTextHandler(&out, nil)),
		"t",
	)
	polls := make(chan Name, 1)
	var n atomic.Int32
	w.waiting = func(q Name) {
		if n.Add(1) == 5 {
			polls <- q
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	<-polls
	cancel()
	<-done
	if got := strings.Count(out.String(), "queue claim failed"); got != 1 {
		t.Errorf("%d failure lines over five polls:\n%s", got, out.String())
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p) //nolint:wrapcheck // strings.Builder never fails
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
