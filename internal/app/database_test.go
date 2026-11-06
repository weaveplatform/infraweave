package app

import (
	"context"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, Streams()...)) }

// sessions counts the connections to the test's database, other than the
// observer's own, that match filter.
func sessions(t *testing.T, observer *sql.DB, filter string) int {
	t.Helper()
	const others = "SELECT count(*) FROM pg_stat_activity " +
		"WHERE datname = current_database() AND pid <> pg_backend_pid() AND "
	var n int
	if err := observer.Primary().QueryRow(t.Context(), others+filter).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// freeAddr is a loopback address with a port nothing listens on.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// answers reports whether url answers 200.
func answers(t *testing.T, url string) bool {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// until polls cond until it holds or the deadline passes.
func until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(10 * time.Second)
	for !cond() {
		select {
		case <-tick.C:
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// TestAccServeDatabase checks that a mode holds the pool while it runs,
// passes its startup and readiness checks against it, and closes it once it
// stops.
func TestAccServeDatabase(t *testing.T) {
	observer := sqltest.Open(t)
	c := local(t)
	c.Database.URL = observer.Primary().Config().ConnString()
	c.Server.AdminListen = freeAddr(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, c, discard(), ModeAll) }()
	// The listeners bind only after the database opens, so a probe that
	// answers means the pool is open and cancelling stops the server, not
	// the open.
	livez := "http://" + c.Server.AdminListen + "/livez"
	until(t, "serve to listen", func() bool { return answers(t, livez) })
	if sessions(t, observer, "true") == 0 {
		t.Fatal("serve listens without a database connection")
	}
	if !answers(t, "http://"+c.Server.AdminListen+"/startupz") {
		t.Error("/startupz fails against a migrated database")
	}
	// Readiness also waits for the scheduler's listening connection.
	until(
		t,
		"/readyz to pass",
		func() bool { return answers(t, "http://"+c.Server.AdminListen+"/readyz") },
	)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not stop")
	}
	until(t, "serve to close its pool", func() bool { return sessions(t, observer, "true") == 0 })
}
