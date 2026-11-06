package app

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

// lines is a log sink the test reads while the mode writes.
type lines struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func rows(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := db.Primary().QueryRow(t.Context(), query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// runMode runs m against observer's database until stop is called, and
// returns its log.
func runMode(t *testing.T, observer *sql.DB, m Mode) (out *lines, admin string, stop func()) {
	t.Helper()
	return runModeWith(t, observer, m, func(*config.Config) {})
}

// runModeWith is runMode with edit's changes to the configuration.
func runModeWith(
	t *testing.T,
	observer *sql.DB,
	m Mode,
	edit func(*config.Config),
) (out *lines, admin string, stop func()) {
	t.Helper()
	c := local(t)
	c.Database.URL = observer.Primary().Config().ConnString()
	c.Server.AdminListen = freeAddr(t)
	c.Queue.PollInterval = 50 * time.Millisecond
	c.Queue.SchedulerTick = 50 * time.Millisecond
	edit(&c)
	out = &lines{}
	log := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, c, log, m) }()
	admin = "http://" + c.Server.AdminListen
	until(t, m.String()+" to be ready", func() bool { return answers(t, admin+"/readyz") })
	return out, admin, func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Run(%s): %v", m, err)
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("Run(%s) did not stop", m)
		}
	}
}

// TestAccSchedulerModeStarts checks that the scheduler mode registers the
// platform's periodic tasks and events.prune, runs them through its workers, and stops the
// ticker before the workers; and that serve runs neither.
func TestAccSchedulerModeStarts(t *testing.T) {
	observer := sqltest.Open(t)
	out, _, stop := runMode(t, observer, ModeScheduler)
	if n := rows(t, observer, `SELECT count(*) FROM periodic_tasks
WHERE (name, interval) IN (('queue.prune', interval '10 minutes'), ('idempotency.purge', interval '1 hour'),
    ('events.prune', interval '10 minutes'))`); n != 3 {
		t.Errorf("%d of the platform's periodic tasks registered, want 3", n)
	}
	// Each is due once registered, so the first ticks run them.
	until(t, "the tasks to run", func() bool {
		return rows(t, observer, `SELECT count(*) FROM jobs
WHERE kind IN ('queue.prune', 'idempotency.purge', 'events.prune') AND queue = 'maintenance'
    AND state = 'succeeded'`) == 3
	})
	if n := rows(
		t,
		observer,
		`SELECT count(*) FROM periodic_tasks WHERE next_run_at <= now()`,
	); n != 0 {
		t.Errorf("%d tasks still due after they ran", n)
	}
	stop()
	got := out.String()
	for _, want := range []string{
		"queue workers started", "workers.maintenance=1", "scheduler ticker started",
		"finished jobs pruned", "expired idempotency keys purged", "fanned-out events pruned",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the log lacks %q:\n%s", want, got)
		}
	}
	ticker, workers := strings.Index(
		got,
		"scheduler ticker stopped",
	), strings.Index(
		got,
		"queue workers stopped",
	)
	if ticker < 0 || workers < ticker {
		t.Errorf("the ticker stops at %d and the workers at %d, want the ticker first:\n%s",
			ticker, workers, got)
	}
	until(t, "the pool to close", func() bool { return sessions(t, observer, "true") == 0 })

	served, _, stopServe := runMode(t, sqltest.Open(t), ModeServe)
	stopServe()
	if got := served.String(); strings.Contains(got, "queue workers") ||
		strings.Contains(got, "ticker") {
		t.Errorf("serve runs the scheduler:\n%s", got)
	}
}
