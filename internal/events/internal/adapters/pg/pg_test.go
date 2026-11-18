package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/weaveplatform/infraweave/internal/events/internal/adapters/metrics"
	"github.com/weaveplatform/infraweave/internal/events/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/events/internal/core/delivery"
	"github.com/weaveplatform/infraweave/internal/events/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/queue/queuetest"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/test/deps"
)

func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream(), pg.Stream())) }

var (
	errStopped = errors.New("feedback enqueue stopped")
	targetKind = queue.Kind{Name: "test.feedback", Queue: queue.Deliveries, Policy: queue.Ignore}
)

type feedback struct {
	jobs    *queue.Queue
	keys    []string
	compute error
	mu      sync.Mutex
	failure string
	calls   int
}

func (*feedback) Kinds() []string { return []string{"run_updated"} }
func (f *feedback) Targets(_ context.Context, event outbox.Event) ([]vcsapi.FeedbackTarget, error) {
	if f.compute != nil {
		return nil, f.compute
	}
	out := make([]vcsapi.FeedbackTarget, 0, len(f.keys))
	for _, key := range f.keys {
		out = append(out, vcsapi.FeedbackTarget{Account: event.Account, EventID: event.ID, Key: key, Job: targetKind.Name, Payload: json.RawMessage(`{}`)})
	}
	return out, nil
}

func (f *feedback) Enqueue(ctx context.Context, target vcsapi.FeedbackTarget) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failure == "after insert" {
		return errStopped
	}
	_, err := f.jobs.EnqueueTx(ctx, targetKind, target.Payload, queue.Account(target.Account), queue.UniqueKey(fmt.Sprintf("%s:%s:%s", target.Account, target.EventID, target.Key)))
	if err != nil {
		return err
	}
	if f.failure == "after enqueue" {
		return errStopped
	}
	if f.failure == "retry" && f.calls == 1 {
		return &pgconn.PgError{Code: "40001", Message: "retry the transaction"}
	}
	return nil
}

type fixture struct {
	pool     *pgxpool.Pool
	jobs     *queue.Queue
	store    *pg.Store
	feedback *feedback
	service  *delivery.Service
	reader   *sdkmetric.ManualReader
	event    outbox.Event
}

func open(t *testing.T) *fixture {
	t.Helper()
	pool := sqltest.Open(t).Primary()
	q := queue.New(pool)
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	metric, err := metrics.New(provider.Meter("event-store-test"))
	if err != nil {
		t.Fatal(err)
	}
	feedback := &feedback{jobs: q, keys: []string{"check-a", "check-b"}}
	store := pg.New(pool)
	service := delivery.New(delivery.Deps{
		Store: store, Feedback: feedback, Metrics: metric, Complete: q.CompleteTx,
		Atomic: func(ctx context.Context, run func(context.Context) error) error {
			return tx.Do(ctx, pool, tx.Options{}, run)
		},
	})
	return &fixture{
		pool:     pool,
		jobs:     q,
		store:    store,
		feedback: feedback,
		service:  service,
		reader:   reader,
		event: outbox.Event{
			Account:       kernel.NewULID(),
			ID:            kernel.NewULID(),
			Kind:          "run_updated",
			SchemaVersion: 1,
			Subject:       outbox.Subject{Kind: "run", ID: "r"},
			Document:      json.RawMessage(`{}`),
		},
	}
}

func (f *fixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *fixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) job(t *testing.T) queue.Job {
	t.Helper()
	if _, err := f.jobs.Enqueue(t.Context(), outbox.ConsumeKind, struct{}{}, queue.Account(f.event.Account)); err != nil {
		t.Fatal(err)
	}
	jobs, err := f.jobs.Claim(t.Context(), queue.Events, []string{outbox.ConsumeKind.Name}, 1, time.Minute, "test-consumer")
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim %+v: %v", jobs, err)
	}
	return jobs[0]
}

func (f *fixture) metric(ctx context.Context, t *testing.T, outcome string) int64 {
	t.Helper()
	var data metricdata.ResourceMetrics
	if err := f.reader.Collect(ctx, &data); err != nil {
		t.Fatal(err)
	}
	var n int64
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "infraweave.events.deliveries" {
				continue
			}
			for _, point := range m.Data.(metricdata.Sum[int64]).DataPoints {
				value, _ := point.Attributes.Value(attribute.Key("outcome"))
				if value.AsString() == outcome {
					n += point.Value
				}
			}
		}
	}
	return n
}

func (f *fixture) assertTargets(t *testing.T, want int) {
	t.Helper()
	if rows := f.count(t, "SELECT count(*) FROM event_deliveries"); rows != want {
		t.Fatalf("delivery rows %d, want %d", rows, want)
	}
	if jobs := f.count(t, "SELECT count(*) FROM jobs WHERE kind=$1", targetKind.Name); jobs != want {
		t.Fatalf("target jobs %d, want %d", jobs, want)
	}
	if got := f.metric(t.Context(), t, "delivered"); got != int64(want) {
		t.Fatalf("committed metric %d, want %d", got, want)
	}
}

func TestAccDeliveryQueueContract(t *testing.T) {
	f := open(t)
	err := queuetest.ConsumeTwice(
		t.Context(),
		f.jobs,
		outbox.ConsumeKind,
		f.event.Account,
		func(ctx context.Context, job queue.Job) error { return f.service.Consume(ctx, f.event, job) },
	)
	if err != nil {
		t.Fatal(err)
	}
	f.assertTargets(t, 2)
	if got := f.count(t, "SELECT count(*) FROM jobs WHERE kind=$1 AND state='succeeded'", outbox.ConsumeKind.Name); got != 2 {
		t.Fatalf("completed consumers %d", got)
	}
}

func TestAccDeliveryAtomicFailures(t *testing.T) {
	for _, mode := range []string{"after insert", "after enqueue", "lost lease", "outer rollback", "retry"} {
		t.Run(mode, func(t *testing.T) {
			f := open(t)
			job := f.job(t)
			f.feedback.failure = mode
			var err error
			switch mode {
			case "lost lease":
				stale := job
				stale.Generation--
				err = f.service.Consume(t.Context(), f.event, stale)
			case "outer rollback":
				err = tx.Do(t.Context(), f.pool, tx.Options{}, func(ctx context.Context) error {
					if e := f.service.Consume(ctx, f.event, job); e != nil {
						return e
					}
					return errStopped
				})
			default:
				err = f.service.Consume(t.Context(), f.event, job)
			}
			if mode == "retry" {
				if err != nil {
					t.Fatal(err)
				}
				f.assertTargets(t, 2)
				return
			}
			if err == nil {
				t.Fatal("failure was not returned")
			}
			if mode == "lost lease" && !errors.Is(err, queue.ErrLeaseLost) {
				t.Fatal(err)
			}
			f.assertTargets(t, 0)
			if state := f.count(t, "SELECT count(*) FROM jobs WHERE id=$1 AND state='running'", job.ID); state != 1 {
				t.Fatal("failed consume committed its completion")
			}
		})
	}
}

func TestAccDeliveryConcurrentConsumers(t *testing.T) {
	for _, sameClaim := range []bool{false, true} {
		t.Run(fmt.Sprintf("same-claim-%t", sameClaim), func(t *testing.T) {
			f := open(t)
			first := f.job(t)
			second := first
			if !sameClaim {
				second = f.job(t)
			}
			start := make(chan struct{})
			results := make(chan error, 2)
			for _, job := range []queue.Job{first, second} {
				go func() { <-start; results <- f.service.Consume(t.Context(), f.event, job) }()
			}
			close(start)
			successes := 0
			for range 2 {
				err := <-results
				if err == nil {
					successes++
				} else if !sameClaim || !errors.Is(err, queue.ErrLeaseLost) {
					t.Fatal(err)
				}
			}
			want := 2
			if sameClaim {
				want = 1
			}
			if successes != want {
				t.Fatalf("successful attempts %d, want %d", successes, want)
			}
			f.assertTargets(t, 2)
		})
	}
}

func TestAccDeliveryEmptyTargets(t *testing.T) {
	f := open(t)
	f.feedback.keys = nil
	if err := f.service.Consume(t.Context(), f.event, f.job(t)); err != nil {
		t.Fatal(err)
	}
	f.assertTargets(t, 0)
	if n := f.metric(t.Context(), t, "skipped"); n != 1 {
		t.Fatalf("skipped %d", n)
	}
}

func TestAccDeliveryRetentionAndAccountPurge(t *testing.T) {
	f := open(t)
	other := kernel.NewULID()
	f.exec(t, `INSERT INTO event_deliveries(account_id,event_id,sink,target_key,rule_index,created_at)
SELECT $1,$2,'commit_checks','old-'||i,0,now()-interval '8 days' FROM generate_series(1,501)i`, f.event.Account, f.event.ID)
	f.exec(t, `INSERT INTO event_deliveries(account_id,event_id,sink,target_key,rule_index,created_at)
VALUES($1,$2,'commit_checks','fresh',0,now()-interval '6 days'),($3,$2,'commit_checks','fresh',0,now())`, f.event.Account, f.event.ID, other)
	if err := f.service.Prune(t.Context(), 7*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, "SELECT count(*) FROM event_deliveries"); n != 2 {
		t.Fatalf("retained %d", n)
	}
	if err := f.service.Purge(t.Context(), f.event.Account); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, "SELECT count(*) FROM event_deliveries WHERE account_id=$1", other); n != 1 {
		t.Fatalf("purge touched another account: %d", n)
	}
	if err := f.service.Purge(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, "SELECT count(*) FROM event_deliveries"); n != 0 {
		t.Fatalf("purge left %d", n)
	}
}

func TestAccDeliveryPurgeWaitsForLockedRows(t *testing.T) {
	f := open(t)
	f.exec(t, `INSERT INTO event_deliveries(account_id,event_id,sink,target_key,rule_index)VALUES($1,$2,'commit_checks','locked',0)`, f.event.Account, f.event.ID)
	conn, err := f.pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	locked, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = locked.Rollback(context.WithoutCancel(t.Context())) }()
	if _, err := locked.Exec(t.Context(), "SELECT * FROM event_deliveries WHERE account_id=$1 FOR UPDATE", f.event.Account); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- f.service.Purge(t.Context(), f.event.Account) }()
	if err := deps.Eventually(t.Context(), "purge waits for locked rows", func(ctx context.Context) (bool, error) {
		var waiting bool
		err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, conn.Conn().PgConn().PID()).Scan(&waiting)
		return waiting, err
	}); err != nil {
		t.Fatal(err)
	}
	if err := locked.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, "SELECT count(*) FROM event_deliveries"); n != 0 {
		t.Fatalf("purge left locked row %d", n)
	}
}

func (f *fixture) outbox(t *testing.T) *outbox.Outbox {
	t.Helper()
	kinds := outbox.NewKinds()
	if err := kinds.Register(f.event.Kind, 1, []byte(`{"type":"object"}`)); err != nil {
		t.Fatal(err)
	}
	o := outbox.New(f.pool, f.jobs, kinds)
	if err := o.Subscribe(f.event.Kind, delivery.Consumer, f.service.Consume); err != nil {
		t.Fatal(err)
	}
	return o
}

func (f *fixture) start(t *testing.T, o *outbox.Outbox) {
	t.Helper()
	registry := queue.NewRegistry()
	if err := o.Register(registry); err != nil {
		t.Fatal(err)
	}
	cfg := queue.Defaults()
	cfg.PollInterval = 10 * time.Millisecond
	cfg.ShutdownGrace = time.Second
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	workers := queue.NewWorkers(f.jobs, registry, cfg, queue.AllowAll{}, nil, slog.New(slog.DiscardHandler), "fanout-test")
	go func() { defer close(done); workers.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
}

func (f *fixture) append(t *testing.T, o *outbox.Outbox) {
	t.Helper()
	if err := tx.Do(t.Context(), f.pool, tx.Options{}, func(ctx context.Context) error { _, err := o.Append(ctx, f.event); return err }); err != nil {
		t.Fatal(err)
	}
}

func TestAccDeliveryOutboxDuplicateFanout(t *testing.T) {
	f := open(t)
	o := f.outbox(t)
	f.append(t, o)
	f.start(t, o)
	if err := deps.Eventually(t.Context(), "first sink delivery", func(ctx context.Context) (bool, error) {
		var n int
		err := f.pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE kind=$1 AND state='succeeded'", outbox.ConsumeKind.Name).Scan(&n)
		return n == 1 && f.metric(ctx, t, "delivered") == 2, err
	}); err != nil {
		t.Fatal(err)
	}
	payload := map[string]kernel.ULID{"account": f.event.Account, "id": f.event.ID}
	if _, err := f.jobs.Enqueue(t.Context(), outbox.FanoutKind, payload, queue.Account(f.event.Account), queue.UniqueKey(f.event.ID.String())); err != nil {
		t.Fatal(err)
	}
	if err := deps.Eventually(t.Context(), "duplicate fanout complete", func(ctx context.Context) (bool, error) {
		var n int
		err := f.pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE kind=$1 AND state='succeeded'", outbox.FanoutKind.Name).Scan(&n)
		return n == 2, err
	}); err != nil {
		t.Fatal(err)
	}
	f.assertTargets(t, 2)
	if n := f.count(t, "SELECT count(*) FROM event_consumptions"); n != 1 {
		t.Fatalf("consumptions %d", n)
	}
	if n := f.count(t, "SELECT count(*) FROM jobs WHERE kind=$1", outbox.ConsumeKind.Name); n != 1 {
		t.Fatalf("consumer jobs %d", n)
	}
}

func TestAccDeliveryConsumerIsolation(t *testing.T) {
	f := open(t)
	f.feedback.compute = errStopped
	o := f.outbox(t)
	for _, name := range []string{"runs.independent", "state.independent"} {
		if err := o.Subscribe(f.event.Kind, name, func(context.Context, outbox.Event, queue.Job) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	f.append(t, o)
	f.start(t, o)
	if err := deps.Eventually(t.Context(), "other consumers succeed while sinks retry", func(ctx context.Context) (bool, error) {
		var n int
		err := f.pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind=$1 AND state='succeeded' AND payload->>'consumer'<>'events.sinks'`, outbox.ConsumeKind.Name).Scan(&n)
		if err != nil || n != 2 {
			return false, err
		}
		var retried bool
		err = f.pool.QueryRow(ctx, `SELECT attempt>0 AND state IN('pending','running') FROM jobs WHERE kind=$1 AND payload->>'consumer'='events.sinks'`, outbox.ConsumeKind.Name).
			Scan(&retried)
		return retried, err
	}); err != nil {
		t.Fatal(err)
	}
	f.assertTargets(t, 0)
}

func TestUnitDeliveryInsertNeedsTransaction(t *testing.T) {
	if _, err := pg.New(nil).Insert(t.Context(), port.Delivery{}); !errors.Is(err, queue.ErrNoTransaction) {
		t.Fatalf("insert = %v", err)
	}
}
