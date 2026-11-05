package outbox_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream())) }

const runFinished = `{
  "type": "object",
  "required": ["state"],
  "properties": {
    "state": {"enum": ["FINISHED", "FAILED"]},
    "note": {"type": "string"}
  }
}`

func kinds(t *testing.T) *outbox.Kinds {
	t.Helper()
	k := outbox.NewKinds()
	if err := k.Register("run.finished", 1, []byte(runFinished)); err != nil {
		t.Fatal(err)
	}
	if err := k.Register("stack.locked", 1, []byte(`{"type": "object"}`)); err != nil {
		t.Fatal(err)
	}
	return k
}

func event(account kernel.ULID) outbox.Event {
	return outbox.Event{
		Account:       account,
		Kind:          "run.finished",
		SchemaVersion: 1,
		Space:         "root",
		Subject:       outbox.Subject{Kind: "run", ID: "01JC0RUN"},
		Sequence:      3,
		Document:      json.RawMessage(`{"state":"FINISHED"}`),
	}
}

// TestUnitEventSchemas checks that a kind needs a schema that compiles and
// that a document is checked against its kind's schema before anything is
// written.
func TestUnitEventSchemas(t *testing.T) {
	k := kinds(t)
	for name, tc := range map[string]struct {
		kind    string
		version int
		schema  string
	}{
		"no name":         {"", 1, `{}`},
		"version 0":       {"run.started", 0, `{}`},
		"no schema":       {"run.started", 1, ``},
		"not JSON":        {"run.started", 1, `{`},
		"not a schema":    {"run.started", 1, `{"type": 7}`},
		"twice":           {"run.finished", 1, `{}`},
		"bad reference":   {"run.started", 1, `{"$ref": "urn:elsewhere"}`},
		"unknown dialect": {"run.started", 1, `{"$schema": "urn:no-such-draft"}`},
	} {
		if err := k.Register(
			tc.kind,
			tc.version,
			[]byte(tc.schema),
		); !errors.Is(
			err,
			outbox.ErrKind,
		) {
			t.Errorf("%s: %v, want ErrKind", name, err)
		}
	}
	if err := k.Register("run.finished", 2, []byte(`{"type": "object"}`)); err != nil {
		t.Errorf("a second version: %v", err)
	}

	for name, tc := range map[string]struct {
		kind    string
		version int
		doc     string
		want    error
	}{
		"valid":            {"run.finished", 1, `{"state":"FAILED","note":"apply"}`, nil},
		"unknown field":    {"run.finished", 1, `{"state":"FAILED","extra":true}`, nil},
		"missing required": {"run.finished", 1, `{"note":"x"}`, outbox.ErrDocument},
		"wrong value":      {"run.finished", 1, `{"state":"QUEUED"}`, outbox.ErrDocument},
		"not JSON":         {"run.finished", 1, `{`, outbox.ErrDocument},
		"unknown kind":     {"run.started", 1, `{}`, outbox.ErrKind},
		"unknown version":  {"run.finished", 3, `{"state":"FAILED"}`, outbox.ErrKind},
	} {
		if err := k.Validate(tc.kind, tc.version, []byte(tc.doc)); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}

	o := outbox.New(nil, queue.New(nil), k)
	ctx := t.Context()
	account := kernel.NewULID()
	for name, tc := range map[string]struct {
		ev   func(outbox.Event) outbox.Event
		want error
	}{
		"no account":     {func(e outbox.Event) outbox.Event { e.Account = kernel.ULID{}; return e }, outbox.ErrEvent},
		"no kind":        {func(e outbox.Event) outbox.Event { e.Kind = ""; return e }, outbox.ErrEvent},
		"no subject":     {func(e outbox.Event) outbox.Event { e.Subject.ID = ""; return e }, outbox.ErrEvent},
		"bad document":   {func(e outbox.Event) outbox.Event { e.Document = json.RawMessage(`{}`); return e }, outbox.ErrDocument},
		"unknown kind":   {func(e outbox.Event) outbox.Event { e.Kind = "run.started"; return e }, outbox.ErrKind},
		"no transaction": {func(e outbox.Event) outbox.Event { return e }, outbox.ErrNoTransaction},
	} {
		if _, err := o.Append(ctx, tc.ev(event(account))); !errors.Is(err, tc.want) {
			t.Errorf("Append, %s: %v, want %v", name, err, tc.want)
		}
	}

	h := func(context.Context, outbox.Event, queue.Job) error { return nil }
	if err := o.Subscribe("run.finished", "notifications", h); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"twice":       o.Subscribe("run.finished", "notifications", h),
		"no kind":     o.Subscribe("", "notifications", h),
		"no consumer": o.Subscribe("run.finished", "", h),
		"no handler":  o.Subscribe("run.finished", "webhooks", nil),
	} {
		if !errors.Is(err, outbox.ErrConsumer) {
			t.Errorf("Subscribe, %s: %v, want ErrConsumer", name, err)
		}
	}
}

func TestUnitOutboxConfig(t *testing.T) {
	if c := outbox.Defaults(); c.Retention != 72*time.Hour || c.Validate() != nil {
		t.Errorf("defaults %+v: %v", c, c.Validate())
	}
	for _, r := range []time.Duration{0, -time.Hour} {
		err := outbox.Config{Retention: r}.Validate()
		if !errors.Is(err, outbox.ErrConfig) || !strings.Contains(err.Error(), "events.retention") {
			t.Errorf("retention %s: %v", r, err)
		}
	}
}

// TestUnitOutboxUnreachable checks that the outbox's reads and writes on a
// server that refuses connections report kernel.ErrUnavailable.
func TestUnitOutboxUnreachable(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), "postgres://iw:iw@127.0.0.1:1/iw?connect_timeout=5")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	o := outbox.New(pool, queue.New(pool), kinds(t))
	if _, err := o.Prune(t.Context(), time.Hour); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("Prune: %v, want kernel.ErrUnavailable", err)
	}
	reg := queue.NewRegistry()
	if err := o.Register(reg); err != nil {
		t.Fatal(err)
	}
	if err := o.Register(reg); err == nil {
		t.Error("registering the outbox's kinds twice succeeded")
	}
}

// logs is a concurrency-safe buffer for the workers' JSON logs.
type logs struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p) //nolint:wrapcheck // bytes.Buffer never fails
}

type harness struct {
	o      *outbox.Outbox
	q      *queue.Queue
	pool   *pgxpool.Pool
	events *notify.Subscription
	// run starts the workers, once every consumer has subscribed.
	run func()
}

// open gives each test a database of its own, with a listener on
// infraweave_jobs and infraweave_events that is listening on return.
func open(t *testing.T) *harness {
	t.Helper()
	db := sqltest.Open(t)
	pool := db.Primary()
	q := queue.New(pool)
	h := &harness{o: outbox.New(pool, q, kinds(t)), q: q, pool: pool}
	log := slog.New(slog.NewJSONHandler(&logs{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	l := notify.NewListener(db.ConnConfig, log)
	jobs, err := l.Subscribe(notify.Jobs)
	if err != nil {
		t.Fatal(err)
	}
	if h.events, err = l.Subscribe(notify.Events); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})
	wg.Go(func() { l.Run(ctx) })
	eventually(t, "the listener", l.Listening)
	cfg := queue.Defaults()
	cfg.PollInterval = 20 * time.Millisecond
	cfg.ShutdownGrace = 5 * time.Second
	h.run = func() {
		reg := queue.NewRegistry()
		if err := h.o.Register(reg); err != nil {
			t.Fatal(err)
		}
		w := queue.NewWorkers(q, reg, cfg, queue.AllowAll{}, jobs.C, log, "test-replica")
		wg.Go(func() { w.Run(ctx) })
	}
	return h
}

// eventually fails the test unless cond holds within 15 seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(15 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !cond() {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		case <-tick.C:
		}
	}
}

// appendIn appends ev in a transaction that fail, when set, rolls back.
func (h *harness) appendIn(t *testing.T, ev outbox.Event, fail error) (outbox.Event, error) {
	t.Helper()
	var stored outbox.Event
	err := tx.Do(t.Context(), h.pool, tx.Options{}, func(ctx context.Context) error {
		var err error
		if stored, err = h.o.Append(ctx, ev); err != nil {
			return err
		}
		return fail
	})
	return stored, err
}

func (h *harness) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := h.pool.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func (h *harness) exec(t *testing.T, stmt string, args ...any) {
	t.Helper()
	if _, err := h.pool.Exec(t.Context(), stmt, args...); err != nil {
		t.Fatalf("%s: %v", stmt, err)
	}
}

// notified waits for the infraweave_events notification carrying payload
// and returns the payloads delivered before it.
func (h *harness) notified(t *testing.T, payload string) []string {
	t.Helper()
	var before []string
	deadline := time.After(15 * time.Second)
	for {
		select {
		case m := <-h.events.C:
			if m.Resync {
				continue
			}
			if m.Payload == payload {
				return before
			}
			before = append(before, m.Payload)
		case <-deadline:
			t.Fatalf("no notification %s; saw %v", payload, before)
		}
	}
}

// job is the state, attempt and last error of the only job of kind whose
// unique key is key.
func (h *harness) job(
	t *testing.T,
	kind, key string,
) (state string, attempt int, lastError string) {
	t.Helper()
	err := h.pool.QueryRow(t.Context(), `
SELECT state, attempt, coalesce(last_error, '') FROM jobs WHERE kind = $1 AND unique_key = $2`,
		kind, key).Scan(&state, &attempt, &lastError)
	if err != nil {
		t.Fatalf("job %s %s: %v", kind, key, err)
	}
	return state, attempt, lastError
}

// TestAccOutboxRollback checks that an event appended in a transaction
// that rolls back leaves no event, no fan-out job and no notification,
// and that a committed one leaves all three, stored as given.
func TestAccOutboxRollback(t *testing.T) {
	h := open(t)
	account := kernel.NewULID()
	rolledBack, err := h.appendIn(t, event(account), errors.New("the change failed"))
	if err == nil || rolledBack.ID.IsZero() {
		t.Fatalf("rolled-back append: %v, ID %s", err, rolledBack.ID)
	}
	ev := event(account)
	ev.Space = ""
	ev.OccurredAt = time.Date(2026, 11, 5, 12, 0, 0, 123456789, time.UTC)
	stored, err := h.appendIn(t, ev, nil)
	if err != nil {
		t.Fatal(err)
	}

	before := h.notified(t, account.String()+":"+stored.ID.String())
	if len(before) != 0 {
		t.Errorf("notifications before the committed event's: %v", before)
	}
	if n := h.count(t, `SELECT count(*) FROM events WHERE id = $1`, rolledBack.ID); n != 0 {
		t.Errorf("rolled-back event stored %d times", n)
	}
	if n := h.count(t, `SELECT count(*) FROM jobs WHERE kind = 'events.fanout'`); n != 1 {
		t.Errorf("%d fan-out jobs, want the committed event's only", n)
	}
	state, _, _ := h.job(t, outbox.FanoutKind.Name, stored.ID.String())
	if state != "pending" {
		t.Errorf("fan-out job %s before any worker runs", state)
	}

	var (
		kind, space, subjectKind, subjectID string
		version                             int
		sequence, occurred                  int64
		document                            string
		fannedOut                           bool
	)
	err = h.pool.QueryRow(t.Context(), `
SELECT kind, schema_version, coalesce(space, '<null>'), subject_kind, subject_id, sequence,
    occurred_at, document::text, fanned_out_at IS NOT NULL
FROM events WHERE account_id = $1 AND id = $2`, account, stored.ID).Scan(&kind, &version, &space,
		&subjectKind, &subjectID, &sequence, &occurred, &document, &fannedOut)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "run.finished" || version != 1 || space != "<null>" || subjectKind != "run" ||
		subjectID != "01JC0RUN" || sequence != 3 || occurred != ev.OccurredAt.UnixNano() ||
		document != `{"state": "FINISHED"}` || fannedOut {
		t.Errorf("stored %s v%d space %s subject %s %s seq %d at %d %s fanned out %v",
			kind, version, space, subjectKind, subjectID, sequence, occurred, document, fannedOut)
	}
	if _, err := h.appendIn(
		t,
		stored,
		nil,
	); err == nil ||
		!strings.Contains(err.Error(), "append run.finished") {
		t.Errorf("appending an event's ID twice: %v", err)
	}
	if stored.OccurredAt != ev.OccurredAt {
		t.Errorf("OccurredAt %v, want the given %v", stored.OccurredAt, ev.OccurredAt)
	}
}

type delivery struct {
	ev  outbox.Event
	job queue.Job
}

// recorder is a consumer that records what it is given.
type recorder struct {
	mu   sync.Mutex
	seen []delivery
}

func (r *recorder) handle(_ context.Context, ev outbox.Event, job queue.Job) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, delivery{ev, job})
	return nil
}

func (r *recorder) deliveries() []delivery {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]delivery(nil), r.seen...)
}

func subscribe(t *testing.T, o *outbox.Outbox, kind, consumer string, h outbox.Handler) {
	t.Helper()
	if err := o.Subscribe(kind, consumer, h); err != nil {
		t.Fatal(err)
	}
}

// TestAccOutboxFanout checks that a committed event reaches each consumer
// of its kind through a job of its own: a consumer that keeps failing
// retries alone, while the other consumes the event once, as stored.
func TestAccOutboxFanout(t *testing.T) {
	h := open(t)
	notifications := &recorder{}
	other := &recorder{}
	var failures atomic.Int32
	subscribe(t, h.o, "run.finished", "notifications", notifications.handle)
	subscribe(
		t,
		h.o,
		"run.finished",
		"webhooks",
		func(context.Context, outbox.Event, queue.Job) error {
			failures.Add(1)
			return errors.New("webhook endpoint answered 500")
		},
	)
	subscribe(t, h.o, "stack.locked", "notifications", other.handle)
	h.run()

	account := kernel.NewULID()
	ev := event(account)
	ev.OccurredAt = time.Unix(0, 1_762_344_000_123_456_789)
	stored, err := h.appendIn(t, ev, nil)
	if err != nil {
		t.Fatal(err)
	}
	eventually(
		t,
		"the notifications consumer",
		func() bool { return len(notifications.deliveries()) == 1 },
	)

	got := notifications.deliveries()[0]
	if got.ev.ID != stored.ID || got.ev.Account != account || got.ev.Kind != "run.finished" ||
		got.ev.SchemaVersion != 1 || got.ev.Space != "root" || got.ev.Subject != ev.Subject ||
		got.ev.Sequence != 3 || !got.ev.OccurredAt.Equal(ev.OccurredAt) ||
		string(got.ev.Document) != `{"state": "FINISHED"}` {
		t.Errorf("consumed %+v, want the stored event %+v", got.ev, stored)
	}
	if got.job.Kind != outbox.ConsumeKind.Name || got.job.Account != account {
		t.Errorf("consumed in job %s for %s", got.job.Kind, got.job.Account)
	}
	if len(other.deliveries()) != 0 {
		t.Errorf("a consumer of another kind got %d events", len(other.deliveries()))
	}

	if state, _, _ := h.job(t, outbox.FanoutKind.Name, stored.ID.String()); state != "succeeded" {
		t.Errorf("fan-out job %s", state)
	}
	key := stored.ID.String() + ":"
	eventually(t, "the notifications job to succeed", func() bool {
		state, _, _ := h.job(t, outbox.ConsumeKind.Name, key+"notifications")
		return state == "succeeded"
	})
	// A claim counts the attempt before the handler runs, so only a
	// pending job with its last error has had its failure recorded.
	eventually(t, "the failing consumer's job to await its retry", func() bool {
		state, attempt, lastError := h.job(t, outbox.ConsumeKind.Name, key+"webhooks")
		return state == "pending" && attempt >= 1 && strings.Contains(lastError, "answered 500")
	})
	if failures.Load() < 1 {
		t.Error("the failing consumer never ran")
	}
	if n := h.count(
		t,
		`SELECT count(*) FROM event_consumptions WHERE event_id = $1`,
		stored.ID,
	); n != 2 {
		t.Errorf("%d consumption records, want 2", n)
	}
	if n := h.count(t, `SELECT count(*) FROM events WHERE fanned_out_at IS NOT NULL`); n != 1 {
		t.Errorf("%d events fanned out, want 1", n)
	}
}

// TestAccOutboxFanoutTwice checks that a fan-out that runs again, as after
// a lost lease, enqueues only the consumers that have no job yet.
func TestAccOutboxFanoutTwice(t *testing.T) {
	h := open(t)
	first, second := &recorder{}, &recorder{}
	subscribe(t, h.o, "run.finished", "notifications", first.handle)
	h.run()
	account := kernel.NewULID()
	stored, err := h.appendIn(t, event(account), nil)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the first consumer", func() bool { return len(first.deliveries()) == 1 })
	eventually(t, "the first fan-out to succeed", func() bool {
		state, _, _ := h.job(t, outbox.FanoutKind.Name, stored.ID.String())
		return state == "succeeded"
	})

	// A consumer added since, as by a newer release, gets the event when
	// it is fanned out again; the first consumer does not get it twice.
	subscribe(t, h.o, "run.finished", "webhooks", second.handle)
	h.exec(t, `DELETE FROM jobs WHERE kind = 'events.fanout'`)
	payload := map[string]string{"account": account.String(), "id": stored.ID.String()}
	if _, err := h.q.Enqueue(t.Context(), outbox.FanoutKind, payload, queue.Account(account),
		queue.UniqueKey(stored.ID.String())); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the second consumer", func() bool { return len(second.deliveries()) == 1 })
	eventually(t, "the second fan-out to succeed", func() bool {
		state, _, _ := h.job(t, outbox.FanoutKind.Name, stored.ID.String())
		return state == "succeeded"
	})
	if n := len(first.deliveries()); n != 1 {
		t.Errorf("the first consumer got the event %d times", n)
	}
	if n := h.count(t, `SELECT count(*) FROM jobs WHERE kind = 'events.consume'`); n != 2 {
		t.Errorf("%d consume jobs, want one per consumer", n)
	}
}

// TestAccOutboxDeadJobs checks that outbox jobs whose event or payload no
// retry can read die at once, and that a consumer this replica does not
// subscribe is retried for a replica that does.
func TestAccOutboxDeadJobs(t *testing.T) {
	h := open(t)
	h.run()
	account := kernel.NewULID()
	missing := map[string]string{"account": account.String(), "id": kernel.NewULID().String()}
	for _, j := range []struct {
		kind    queue.Kind
		payload any
		key     string
		want    string
	}{
		{outbox.FanoutKind, missing, "missing fan-out", "no longer stored"},
		{outbox.ConsumeKind, map[string]string{
			"account": missing["account"], "id": missing["id"],
			"consumer": "notifications",
		}, "missing consume", "no longer stored"},
		{outbox.FanoutKind, "not an object", "bad fan-out", "events.fanout payload"},
		{outbox.ConsumeKind, []int{1}, "bad consume", "events.consume payload"},
	} {
		if _, err := h.q.Enqueue(
			t.Context(),
			j.kind,
			j.payload,
			queue.UniqueKey(j.key),
		); err != nil {
			t.Fatal(err)
		}
		eventually(t, j.key+" to die", func() bool {
			state, _, _ := h.job(t, j.kind.Name, j.key)
			return state == "dead"
		})
		if _, attempt, lastError := h.job(t, j.kind.Name, j.key); attempt != 1 ||
			!strings.Contains(lastError, j.want) {
			t.Errorf("%s: attempt %d, last error %q, want %q", j.key, attempt, lastError, j.want)
		}
	}

	stored, err := h.appendIn(t, event(account), nil)
	if err != nil {
		t.Fatal(err)
	}
	unknown := map[string]string{
		"account": account.String(), "id": stored.ID.String(),
		"consumer": "billing",
	}
	if _, err := h.q.Enqueue(
		t.Context(),
		outbox.ConsumeKind,
		unknown,
		queue.UniqueKey("unknown"),
	); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the unknown consumer's job to await its retry", func() bool {
		state, attempt, lastError := h.job(t, outbox.ConsumeKind.Name, "unknown")
		return state == "pending" && attempt >= 1 &&
			strings.Contains(lastError, `"billing" is not subscribed`)
	})
}

// TestAccOutboxPrune checks that Prune deletes the events fanned out longer
// than the retention ago, with their consumption records, and never
// an event not yet fanned out, however old.
func TestAccOutboxPrune(t *testing.T) {
	h := open(t)
	account := kernel.NewULID()
	ids := map[string]kernel.ULID{}
	for _, name := range []string{"old", "recent", "unfanned"} {
		ev, err := h.appendIn(t, event(account), nil)
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = ev.ID
		h.exec(t, `INSERT INTO event_consumptions (account_id, event_id, consumer)
VALUES ($1, $2, 'notifications')`, account, ev.ID)
	}
	h.exec(
		t,
		`UPDATE events SET fanned_out_at = now() - interval '73 hours' WHERE id = $1`,
		ids["old"],
	)
	h.exec(
		t,
		`UPDATE events SET fanned_out_at = now() - interval '71 hours' WHERE id = $1`,
		ids["recent"],
	)
	h.exec(t, `UPDATE events SET occurred_at = 0 WHERE id = $1`, ids["unfanned"])

	// No workers run, so the unfanned event stays unfanned.
	if n, err := h.o.Prune(t.Context(), 72*time.Hour); err != nil || n != 1 {
		t.Fatalf("Prune(72h) = %d, %v; want the old event", n, err)
	}
	for name, want := range map[string]int{"old": 0, "recent": 1, "unfanned": 1} {
		if n := h.count(t, `SELECT count(*) FROM events WHERE id = $1`, ids[name]); n != want {
			t.Errorf("%s event: %d rows, want %d", name, n, want)
		}
		if n := h.count(t, `SELECT count(*) FROM event_consumptions WHERE event_id = $1`,
			ids[name]); n != want {
			t.Errorf("%s event: %d consumption records, want %d", name, n, want)
		}
	}
	if n, err := h.o.Prune(t.Context(), time.Nanosecond); err != nil || n != 1 {
		t.Errorf("Prune(1ns) = %d, %v; want the recent event only", n, err)
	}
	if n := h.count(t, `SELECT count(*) FROM events WHERE id = $1`, ids["unfanned"]); n != 1 {
		t.Errorf("the unfanned event was pruned")
	}
}
