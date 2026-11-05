package notify_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/test/acceptance"
)

func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, acceptance.ImagePostgres)) }

// within is how long a delivery may take before the test fails.
const within = 10 * time.Second

func next(t *testing.T, s *notify.Subscription) notify.Message {
	t.Helper()
	select {
	case m := <-s.C:
		return m
	case <-time.After(within):
		t.Fatal("no message")
		return notify.Message{}
	}
}

func expect(t *testing.T, s *notify.Subscription, want notify.Message) {
	t.Helper()
	if got := next(t, s); got != want {
		t.Fatalf("message = %+v, want %+v", got, want)
	}
}

func quiet(t *testing.T, s *notify.Subscription) {
	t.Helper()
	select {
	case m := <-s.C:
		t.Fatalf("unexpected message %+v", m)
	default:
	}
}

// records collects warnings, so a test waits for the listener to fail.
type records struct{ msgs chan string }

func (r *records) Enabled(context.Context, slog.Level) bool { return true }
func (r *records) Handle(_ context.Context, rec slog.Record) error {
	select {
	case r.msgs <- rec.Message:
	default:
	}
	return nil
}
func (r *records) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *records) WithGroup(string) slog.Handler      { return r }

func start(t *testing.T, cfg *pgx.ConnConfig, log *slog.Logger) *notify.Listener {
	t.Helper()
	l := notify.NewListener(cfg, log)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); l.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return l
}

func TestAccNotifyReconnect(t *testing.T) {
	notify.SetBackoff(t, 10*time.Millisecond, 50*time.Millisecond)
	db := sqltest.Open(t)
	pool := db.Primary()
	l := notify.NewListener(db.ListenConfig(), slog.New(slog.DiscardHandler))
	sub, err := l.Subscribe(notify.Invalidate)
	if err != nil {
		t.Fatal(err)
	}
	other, err := l.Subscribe(notify.Jobs)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); l.Run(ctx) }()
	defer func() { cancel(); <-done }()

	expect(t, sub, notify.Message{Resync: true})
	expect(t, other, notify.Message{Resync: true})
	if err := notify.Notify(t.Context(), pool, notify.Invalidate, "a"); err != nil {
		t.Fatal(err)
	}
	expect(t, sub, notify.Message{Payload: "a"})
	quiet(t, other)

	var terminated bool
	if err := pool.QueryRow(t.Context(),
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name = $1",
		notify.ApplicationName).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminate the listener: %v %v", terminated, err)
	}
	expect(t, sub, notify.Message{Resync: true})
	expect(t, other, notify.Message{Resync: true})
	if err := notify.Notify(t.Context(), pool, notify.Invalidate, "b"); err != nil {
		t.Fatal(err)
	}
	expect(t, sub, notify.Message{Payload: "b"})

	// Inside a transaction a notification waits for the commit, and a
	// rollback discards it.
	for _, commit := range []bool{false, true} {
		txn, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if err := notify.Notify(
			t.Context(),
			txn,
			notify.Invalidate,
			strconv.FormatBool(commit),
		); err != nil {
			t.Fatal(err)
		}
		quiet(t, sub)
		if commit {
			err = txn.Commit(t.Context())
		} else {
			err = txn.Rollback(t.Context())
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	expect(t, sub, notify.Message{Payload: "true"})

	other.Close()
	if err := notify.Notify(t.Context(), pool, notify.Jobs, "q"); err != nil {
		t.Fatal(err)
	}
	if err := notify.Notify(t.Context(), pool, notify.Invalidate, "c"); err != nil {
		t.Fatal(err)
	}
	expect(t, sub, notify.Message{Payload: "c"})
	quiet(t, other)
}

// A subscriber that falls more than a buffer behind loses payloads but
// gets a Resync before the next one it can take.
func TestAccNotifySlowSubscriber(t *testing.T) {
	db := sqltest.Open(t)
	pool := db.Primary()
	l := start(t, db.ListenConfig(), slog.New(slog.DiscardHandler))
	sub, err := l.Subscribe(notify.Runs)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, sub, notify.Message{Resync: true})
	const sent = 100
	for i := range sent {
		if err := notify.Notify(t.Context(), pool, notify.Runs, strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	// A sentinel on another channel arrives after every Runs payload, since
	// one connection delivers in order.
	probe, err := l.Subscribe(notify.Logs)
	if err != nil {
		t.Fatal(err)
	}
	if err := notify.Notify(t.Context(), pool, notify.Logs, "after"); err != nil {
		t.Fatal(err)
	}
	expect(t, probe, notify.Message{Payload: "after"})
	got := 0
	for {
		select {
		case m := <-sub.C:
			if m.Resync {
				t.Fatalf("Resync before the queue drained, after %d", got)
			}
			got++
			continue
		default:
		}
		break
	}
	if got >= sent {
		t.Fatalf("received %d of %d: the buffer never filled", got, sent)
	}
	if err := notify.Notify(t.Context(), pool, notify.Runs, "late"); err != nil {
		t.Fatal(err)
	}
	expect(t, sub, notify.Message{Resync: true})
	expect(t, sub, notify.Message{Payload: "late"})
}

func TestUnitNotifyRetriesAConnection(t *testing.T) {
	notify.SetBackoff(t, time.Millisecond, 2*time.Millisecond)
	cfg, err := pgx.ParseConfig("postgres://u:p@127.0.0.1:1/x?sslmode=disable&connect_timeout=2")
	if err != nil {
		t.Fatal(err)
	}
	h := &records{msgs: make(chan string, 8)}
	l := notify.NewListener(cfg, slog.New(h))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); l.Run(ctx) }()
	for range 2 {
		select {
		case <-h.msgs:
		case <-time.After(within):
			t.Fatal("no reconnect warning")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(within):
		t.Fatal("Run did not stop")
	}
}

type execerFail struct{}

func (execerFail) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("down")
}

func TestUnitNotifyChannels(t *testing.T) {
	l := notify.NewListener(&pgx.ConnConfig{}, slog.New(slog.DiscardHandler))
	if _, err := l.Subscribe("infraweave_other"); !errors.Is(err, notify.ErrChannel) {
		t.Errorf("Subscribe to an unknown channel: %v", err)
	}
	if err := notify.Notify(
		t.Context(),
		nil,
		"infraweave_other",
		"x",
	); !errors.Is(
		err,
		notify.ErrChannel,
	) {
		t.Errorf("Notify on an unknown channel: %v", err)
	}
	if err := notify.Notify(t.Context(), execerFail{}, notify.Jobs, "x"); err == nil ||
		err.Error() != fmt.Sprintf("notify %s: down", notify.Jobs) {
		t.Errorf("Notify with a failing connection: %v", err)
	}
}
