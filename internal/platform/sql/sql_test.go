package sql_test

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/test/acceptance"
)

func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, acceptance.ImagePostgres)) }

func TestUnitConfigValidate(t *testing.T) {
	ok := sql.Defaults()
	ok.URL = "postgres://u:p@db.example:5432/infraweave?sslmode=verify-full"
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	if err := sql.Defaults().Validate(); err != nil {
		t.Errorf("defaults without a URL: %v", err)
	}
	for _, tc := range []struct {
		name string
		edit func(*sql.Config)
		want string
	}{
		{"bad url", func(c *sql.Config) { c.URL = "postgres://u:secret@db:notaport/x" }, "database.url"},
		{"bad read url", func(c *sql.Config) { c.ReadURL = "postgres://db:x/y" }, "database.read_url"},
		{"read url alone", func(c *sql.Config) { c.URL, c.ReadURL = "", ok.URL }, "needs database.url"},
		{"no connections", func(c *sql.Config) { c.MaxConns = 0 }, "database.max_conns"},
		{"unknown migrate mode", func(c *sql.Config) { c.Migrate = "never" }, "database.migrate"},
		{"no statement timeout", func(c *sql.Config) { c.StatementTimeout = 0 }, "database.statement_timeout"},
		{
			"sub-millisecond idle timeout",
			func(c *sql.Config) { c.IdleInTransactionTimeout = time.Microsecond },
			"database.idle_in_transaction_timeout",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := ok
			tc.edit(&c)
			err := c.Validate()
			if !errors.Is(err, sql.ErrConfig) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want ErrConfig naming %s", err, tc.want)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Errorf("the error shows the password: %v", err)
			}
		})
	}
}

func TestUnitOpenRejects(t *testing.T) {
	if _, err := sql.Open(t.Context(), sql.Defaults()); !errors.Is(err, sql.ErrConfig) ||
		!strings.Contains(err.Error(), "database.url is required") {
		t.Errorf("no URL: %v", err)
	}
	c := sql.Defaults()
	c.URL, c.MaxConns = "postgres://localhost/x", 0
	if _, err := sql.Open(t.Context(), c); !errors.Is(err, sql.ErrConfig) {
		t.Errorf("invalid config: %v", err)
	}
	// Port 1 refuses at once, so this needs no server.
	c = sql.Defaults()
	c.URL = "postgres://u:p@127.0.0.1:1/x?sslmode=disable&connect_timeout=2"
	if _, err := sql.Open(
		t.Context(),
		c,
	); err == nil ||
		!strings.Contains(err.Error(), "database.url") {
		t.Errorf("unreachable server: %v", err)
	}
}

func TestAccOpen(t *testing.T) {
	db := sqltest.Open(t)
	for setting, want := range map[string]string{
		"statement_timeout":                   "30s",
		"idle_in_transaction_session_timeout": "1min",
	} {
		var got string
		if err := db.Primary().QueryRow(t.Context(), "SHOW "+setting).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s = %s, want %s", setting, got, want)
		}
	}
	if db.Read() != db.Primary() {
		t.Error("without database.read_url, Read is not the primary")
	}
	if cc, err := db.ConnConfig(
		t.Context(),
	); err != nil ||
		cc.RuntimeParams["statement_timeout"] != "30000" {
		t.Errorf("ConnConfig = %v, %v", cc, err)
	}

	c := sql.Defaults()
	c.URL = sqltest.DSN(t)
	c.ReadURL = c.URL
	c.MaxConns = 3
	withRead, err := sql.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if withRead.Read() == withRead.Primary() {
		t.Error("with database.read_url, Read is the primary")
	}
	if got := withRead.Primary().Config().MaxConns; got != 3 {
		t.Errorf("MaxConns = %d, want 3", got)
	}
	withRead.Close()
	if err := withRead.Read().Ping(t.Context()); err == nil {
		t.Error("Close left the read pool open")
	}
}

func TestAccVersionFloor(t *testing.T) {
	_, old := sqltest.Server(t, acceptance.ImagePostgresBelowFloor)
	c := sql.Defaults()
	c.URL = old
	_, err := sql.Open(t.Context(), c)
	if !errors.Is(err, sql.ErrBelowFloor) || !strings.Contains(err.Error(), "PostgreSQL 17") {
		t.Fatalf("PostgreSQL 17 as the primary: %v", err)
	}

	c.URL, c.ReadURL = sqltest.DSN(t), old
	_, err = sql.Open(t.Context(), c)
	if !errors.Is(err, sql.ErrBelowFloor) || !strings.Contains(err.Error(), "database.read_url") {
		t.Fatalf("PostgreSQL 17 as the replica: %v", err)
	}
}

// TestAccULIDColumn checks decision 0005's encoding: a ULID is stored in a
// uuid column, sorts there in generation order and reads back unchanged.
func TestAccULIDColumn(t *testing.T) {
	pool := sqltest.Open(t).Primary()
	if _, err := pool.Exec(
		t.Context(),
		"CREATE TABLE ids (id uuid PRIMARY KEY, n int)",
	); err != nil {
		t.Fatal(err)
	}
	want := make([]kernel.ULID, 100)
	for i := range want {
		want[i] = kernel.NewULID()
		if _, err := pool.Exec(
			t.Context(),
			"INSERT INTO ids VALUES ($1, $2)",
			want[i],
			i,
		); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := pool.Query(t.Context(), "SELECT id FROM ids ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[kernel.ULID])
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("read %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf(
				"row %d = %s, want %s: the column does not sort in generation order",
				i,
				got[i],
				want[i],
			)
		}
	}
	var null kernel.ULID
	if err := pool.QueryRow(t.Context(), "SELECT NULL::uuid").
		Scan(&null); err != nil ||
		!null.IsZero() {
		t.Errorf("NULL = %s, %v", null, err)
	}
}
