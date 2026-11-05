package sqltest

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
)

// RequireDocker is the variable that turns a missing Docker daemon from a
// skip into a failure, as in the acceptance harness: CI sets it, so a test
// that needs PostgreSQL can never pass there by skipping (decision 0016).
const RequireDocker = "INFRAWEAVE_ACCEPT_REQUIRE_DOCKER"

// Template is the database each test's database is copied from, the
// acceptance harness's iw_template: one CREATE DATABASE per test instead
// of a schema per test.
const Template = "iw_template"

// The credentials of the test's own containers, which listen on loopback
// only and live as long as one test binary.
const user, password, database = "infraweave", "infraweave", "infraweave"

// errNoDocker marks a missing daemon, the one failure that may skip.
var errNoDocker = errors.New("no Docker daemon")

var (
	image   string
	streams []migrate.Stream
	started sync.Once
	server  *postgres.PostgresContainer
	baseDSN string
	failed  error
	count   atomic.Int64
)

// Main runs m's tests with img as the shared server's image, and stops the
// server once they finish. A test package that uses DSN or Open calls it
// from TestMain with test/acceptance's ImagePostgres, so every suite runs
// on the one pinned image, and with the migration streams its tests need,
// in registration order: Template is migrated with them once, so each
// test's database starts migrated. The server starts on the first DSN, so a
// run of TestUnit tests starts nothing.
func Main(m *testing.M, img string, s ...migrate.Stream) int {
	image, streams = img, s
	code := m.Run()
	if server != nil {
		if err := server.Terminate(context.Background()); err != nil && code == 0 {
			fmt.Fprintln(os.Stderr, "sqltest: stop PostgreSQL:", err)
			code = 1
		}
	}
	return code
}

// tb is the part of testing.TB the Docker rule uses.
type tb interface {
	Helper()
	Skipf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// unavailable skips t when Docker is missing and RequireDocker is unset,
// and fails it otherwise.
func unavailable(t tb, err error, require bool) {
	t.Helper()
	if errors.Is(err, errNoDocker) && !require {
		t.Skipf("PostgreSQL: %v (set %s=1 to fail instead)", err, RequireDocker)
		return
	}
	t.Fatalf("PostgreSQL: %v", err)
}

func required() bool { return os.Getenv(RequireDocker) == "1" }

// DSN returns the connection string of a new database for t, copied from
// Template and dropped when t ends.
func DSN(t testing.TB) string {
	t.Helper()
	return newDatabase(t, " TEMPLATE "+Template)
}

// Empty returns the connection string of a new database for t with no
// migrations applied, dropped when t ends, for a test that applies streams
// itself.
func Empty(t testing.TB) string {
	t.Helper()
	return newDatabase(t, "")
}

func newDatabase(t testing.TB, from string) string {
	t.Helper()
	if image == "" {
		t.Fatal("sqltest: call sqltest.Main from TestMain")
	}
	ctx := t.Context()
	started.Do(func() { server, baseDSN, failed = start(context.Background(), image, streams) })
	if failed != nil {
		unavailable(t, failed, required())
	}
	name := fmt.Sprintf("iw_test_%d", count.Add(1))
	if err := exec(ctx, baseDSN, "CREATE DATABASE "+name+from); err != nil {
		t.Fatalf("sqltest: %v", err)
	}
	t.Cleanup(func() {
		if err := exec(
			context.Background(),
			baseDSN,
			"DROP DATABASE "+name+" WITH (FORCE)",
		); err != nil {
			t.Errorf("sqltest: %v", err)
		}
	})
	dsn, err := withDatabase(baseDSN, name)
	if err != nil {
		t.Fatalf("sqltest: %v", err)
	}
	return dsn
}

// Open opens a new database for t through package sql with its defaults,
// and closes it when t ends.
func Open(t testing.TB) *sql.DB {
	t.Helper()
	c := sql.Defaults()
	c.URL = DSN(t)
	db, err := sql.Open(t.Context(), c)
	if err != nil {
		t.Fatalf("sqltest: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

// Server starts a PostgreSQL server of t's own from img and returns the
// connection string of its database; the server stops when t ends. It is
// for a test that stops the server or needs another major version, which
// the shared server's other users must not see.
func Server(t testing.TB, img string) (*postgres.PostgresContainer, string) {
	t.Helper()
	c, dsn, err := start(context.Background(), img, nil)
	if c != nil {
		t.Cleanup(func() {
			if err := c.Terminate(context.Background()); err != nil {
				t.Errorf("sqltest: stop PostgreSQL: %v", err)
			}
		})
	}
	if err != nil {
		unavailable(t, err, required())
	}
	return c, dsn
}

// start starts a server from img with Template migrated with streams.
func start(
	ctx context.Context,
	img string,
	streams []migrate.Stream,
) (*postgres.PostgresContainer, string, error) {
	if err := reachable(ctx); err != nil {
		return nil, "", err
	}
	c, err := postgres.Run(ctx, img,
		postgres.WithDatabase(database),
		postgres.WithUsername(user),
		postgres.WithPassword(password),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		return c, "", fmt.Errorf("start %s: %w", img, err)
	}
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return c, "", fmt.Errorf("start %s: %w", img, err)
	}
	if err := exec(ctx, dsn, "CREATE DATABASE "+Template); err != nil {
		return c, "", err
	}
	if len(streams) > 0 {
		tmpl, err := withDatabase(dsn, Template)
		if err != nil {
			return c, "", err
		}
		cfg, err := pgx.ParseConfig(tmpl)
		if err != nil {
			return c, "", fmt.Errorf("migrate %s: %w", Template, err)
		}
		if _, err := migrate.Up(ctx, cfg, "sqltest", streams); err != nil {
			return c, "", fmt.Errorf("migrate %s: %w", Template, err)
		}
	}
	return c, dsn, nil
}

// reachable asks the daemon through the discovery testcontainers uses
// (DOCKER_HOST, the Docker context, the default socket).
func reachable(ctx context.Context) error {
	p, err := testcontainers.NewDockerProvider()
	if err != nil {
		return fmt.Errorf("%w: %w", errNoDocker, err)
	}
	defer p.Close()
	if err := p.Health(ctx); err != nil {
		return fmt.Errorf("%w: %w", errNoDocker, err)
	}
	return nil
}

// exec runs one statement on its own connection: CREATE and DROP DATABASE
// cannot run inside a transaction or a pooled session.
func exec(ctx context.Context, dsn, stmt string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("%s: %w", stmt, err)
	}
	defer conn.Close(context.WithoutCancel(ctx))
	if _, err := conn.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("%s: %w", stmt, err)
	}
	return nil
}

func withDatabase(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse the server's connection string: %w", err)
	}
	u.Path = "/" + name
	return u.String(), nil
}
