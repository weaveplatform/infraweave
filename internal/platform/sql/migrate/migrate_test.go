package migrate_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

func TestMain(m *testing.M) { os.Exit(sqltest.Main(m)) }

func stream(name string, files ...string) migrate.Stream {
	m := fstest.MapFS{}
	for i := 0; i < len(files); i += 2 {
		m[files[i]] = &fstest.MapFile{Data: []byte(files[i+1])}
	}
	return migrate.Stream{Name: name, Files: m}
}

// fresh returns the settings of a new database with nothing applied.
func fresh(t *testing.T) *pgx.ConnConfig {
	t.Helper()
	cfg, err := pgx.ParseConfig(sqltest.Empty(t))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func ran(statuses []migrate.Status) []string {
	var out []string
	for _, s := range statuses {
		for _, f := range s.Ran {
			out = append(out, s.Stream+"/"+f.Name)
		}
	}
	return out
}

func query[T any](t *testing.T, cfg *pgx.ConnConfig, sql string, args ...any) T {
	t.Helper()
	conn, err := pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	var v T
	if err := conn.QueryRow(t.Context(), sql, args...).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAccMigrate(t *testing.T) {
	cfg := fresh(t)
	accounts := stream("accounts",
		"00001_create_accounts.sql", "CREATE TABLE accounts (id uuid PRIMARY KEY);\n"+
			"CREATE TABLE members (account_id uuid NOT NULL REFERENCES accounts);",
	)
	// stacks reuses version 00001 and references accounts, which exists
	// because accounts is registered first.
	stacks := stream(
		"stacks",
		"00001_create_stacks.sql",
		"CREATE TABLE stacks (id uuid PRIMARY KEY, account_id uuid REFERENCES accounts);",
		"00002_index_stacks.sql",
		"-- infraweave:no-transaction\n"+
			"CREATE INDEX CONCURRENTLY IF NOT EXISTS stacks_account ON stacks (account_id);\n"+
			"CREATE INDEX CONCURRENTLY IF NOT EXISTS stacks_id ON stacks (id);\n",
	)
	streams := []migrate.Stream{accounts, stacks}

	if err := migrate.Check(t.Context(), cfg, streams); !errors.Is(err, migrate.ErrPending) ||
		!strings.Contains(
			err.Error(),
			"accounts 00001_create_accounts, stacks 00001_create_stacks",
		) {
		t.Fatalf("Check on a fresh database: %v", err)
	}
	got, err := migrate.Up(t.Context(), cfg, "v1.0.0", streams)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(
		ran(got),
		" ",
	) != "accounts/create_accounts stacks/create_stacks stacks/index_stacks" {
		t.Errorf("ran %v", ran(got))
	}
	// CREATE INDEX CONCURRENTLY fails inside a transaction, so the index
	// shows the no-transaction file ran outside one.
	if n := query[int](
		t,
		cfg,
		"SELECT count(*) FROM pg_indexes WHERE indexname IN ('stacks_account', 'stacks_id')",
	); n != 2 {
		t.Errorf("indexes = %d", n)
	}
	if r := query[string](
		t,
		cfg,
		"SELECT release FROM schema_state WHERE stream = 'stacks'",
	); r != "v1.0.0" {
		t.Errorf("release = %q", r)
	}

	again, err := migrate.Up(t.Context(), cfg, "v1.0.1", streams)
	if err != nil || len(ran(again)) != 0 {
		t.Fatalf("second run: %v %v", ran(again), err)
	}
	if err := migrate.Check(t.Context(), cfg, streams); err != nil {
		t.Errorf("Check when up to date: %v", err)
	}

	stacks = stream("stacks", append(
		stacksFiles(stacks),
		"00003_add_name.sql",
		"-- infraweave:compatible-from 00003\nALTER TABLE stacks ADD COLUMN name text;",
	)...)
	streams = []migrate.Stream{accounts, stacks}
	if err := migrate.Check(t.Context(), cfg, streams); !errors.Is(err, migrate.ErrPending) ||
		!strings.Contains(err.Error(), "stacks 00003_add_name") {
		t.Fatalf("Check with a new file: %v", err)
	}
	statuses, err := migrate.Inspect(t.Context(), cfg, streams)
	if err != nil || len(statuses[1].Applied) != 2 || len(statuses[1].Pending) != 1 ||
		statuses[1].Release != "v1.0.0" {
		t.Fatalf("Inspect = %+v, %v", statuses, err)
	}
	got, err = migrate.Up(t.Context(), cfg, "v1.1.0", streams)
	if err != nil || strings.Join(ran(got), " ") != "stacks/add_name" ||
		got[1].CompatibleFrom != 3 {
		t.Fatalf("Up with a new file: %+v %v", got, err)
	}
}

// stacksFiles returns a stream's files as name, body pairs.
func stacksFiles(s migrate.Stream) []string {
	out := make([]string, 0, 4)
	for _, name := range []string{"00001_create_stacks.sql", "00002_index_stacks.sql"} {
		out = append(out, name, string(s.Files.(fstest.MapFS)[name].Data))
	}
	return out
}

// A file that fails rolls back with its record, and Up names it.
func TestAccMigrateFailingFile(t *testing.T) {
	cfg := fresh(t)
	bad := stream(
		"s",
		"00001_a.sql",
		"CREATE TABLE a (id int);",
		"00002_b.sql",
		"CREATE TABLE b (id nonsense);",
	)
	if _, err := migrate.Up(t.Context(), cfg, "v1", []migrate.Stream{bad}); err == nil ||
		!strings.Contains(err.Error(), "s 00002_b") {
		t.Fatalf("Up = %v", err)
	}
	statuses, err := migrate.Inspect(t.Context(), cfg, []migrate.Stream{bad})
	if err != nil || len(statuses[0].Applied) != 1 || len(statuses[0].Pending) != 1 {
		t.Errorf("after the failure: %+v %v", statuses, err)
	}
	noTx := stream(
		"n",
		"00001_a.sql",
		"-- infraweave:no-transaction\nCREATE INDEX CONCURRENTLY x ON nowhere (id);",
	)
	if _, err := migrate.Up(t.Context(), cfg, "v1", []migrate.Stream{noTx}); err == nil ||
		!strings.Contains(err.Error(), "n 00001_a") {
		t.Errorf("no-transaction failure: %v", err)
	}
}

// Two runners started together apply each file once: a second application
// of CREATE TABLE would fail.
func TestAccMigrateConcurrent(t *testing.T) {
	cfg := fresh(t)
	s := []migrate.Stream{stream("s",
		"00001_slow.sql", "SELECT pg_sleep(0.3); CREATE TABLE once (id int);",
		"00002_more.sql", "CREATE TABLE twice (id int);",
	)}
	var wg sync.WaitGroup
	results := make([][]string, 2)
	for i := range results {
		wg.Go(func() {
			got, err := migrate.Up(t.Context(), cfg, "v1", s)
			if err != nil {
				t.Errorf("runner %d: %v", i, err)
			}
			results[i] = ran(got)
		})
	}
	wg.Wait()
	if n := len(results[0]) + len(results[1]); n != 2 {
		t.Errorf("applied %v and %v: want 2 files in all", results[0], results[1])
	}
	if n := query[int](t, cfg, "SELECT count(*) FROM schema_migrations"); n != 2 {
		t.Errorf("schema_migrations has %d rows", n)
	}
}

func TestAccMigrateRefusals(t *testing.T) {
	v1 := stream("s", "00001_a.sql", "CREATE TABLE a (id int);")
	v2 := stream(
		"s",
		"00001_a.sql",
		"CREATE TABLE a (id int);",
		"00002_b.sql",
		"CREATE TABLE b (id int);",
	)
	v2strict := stream("s", "00001_a.sql", "CREATE TABLE a (id int);",
		"00002_b.sql", "-- infraweave:compatible-from 00002\nCREATE TABLE b (id int);")
	for _, tc := range []struct {
		name    string
		before  []migrate.Stream
		setup   string
		binary  []migrate.Stream
		refuses string
	}{
		{
			name:    "changed checksum",
			before:  []migrate.Stream{v1},
			binary:  []migrate.Stream{stream("s", "00001_a.sql", "CREATE TABLE a (id bigint);")},
			refuses: "s: 00001_a changed after it was applied",
		},
		{
			name:    "another file at an applied version",
			before:  []migrate.Stream{v1},
			binary:  []migrate.Stream{stream("s", "00001_other.sql", "SELECT 1;")},
			refuses: "stream s has diverged: version 00001 is applied as a, but this binary has other",
		},
		{
			name:    "a hole below an applied version",
			before:  []migrate.Stream{v2},
			setup:   "DELETE FROM schema_migrations WHERE version = 1",
			binary:  []migrate.Stream{v2},
			refuses: "stream s has diverged: 00001_a is not applied, but version 00002 is",
		},
		{
			name:    "too old for compatible_from",
			before:  []migrate.Stream{v2strict},
			binary:  []migrate.Stream{v1},
			refuses: "stream s needs a binary that knows its version 00002; this binary's newest is 00001: run release v-before or newer",
		},
		{
			name:    "an unknown stream that demands a newer binary",
			before:  []migrate.Stream{v1, stream("z", "00001_z.sql", "-- infraweave:compatible-from 00001\nSELECT 1;")},
			binary:  []migrate.Stream{v1},
			refuses: "stream z needs a binary that knows its version 00001; this binary does not know the stream (last migrated by v-before)",
		},
		{
			name:   "skipped major",
			before: []migrate.Stream{v1},
			binary: []migrate.Stream{{
				Name:  "s",
				Files: v2.Files,
				Floor: migrate.Floor{Version: 2, Release: "v1.9.0"},
			}},
			refuses: "stream s is at version 00001, below this major's upgrade floor 00002: upgrade to v1.9.0",
		},
		{
			name:   "a newer release's file that keeps older binaries working",
			before: []migrate.Stream{v2},
			binary: []migrate.Stream{v1},
		},
		{
			name:   "a newer release's stream that keeps older binaries working",
			before: []migrate.Stream{v1, stream("z", "00001_z.sql", "SELECT 1;")},
			binary: []migrate.Stream{v1},
		},
		{
			name:   "a fresh database under an upgrade floor",
			binary: []migrate.Stream{{Name: "s", Files: v2.Files, Floor: migrate.Floor{Version: 2, Release: "v1.9.0"}}},
		},
		{
			name:   "an unknown stream whose compatible_from is unset",
			setup:  "INSERT INTO schema_state (stream) VALUES ('ghost')",
			before: []migrate.Stream{v1},
			binary: []migrate.Stream{v1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := fresh(t)
			if _, err := migrate.Up(t.Context(), cfg, "v-before", tc.before); err != nil {
				t.Fatal(err)
			}
			if tc.setup != "" {
				conn, err := pgx.ConnectConfig(t.Context(), cfg)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := conn.Exec(t.Context(), tc.setup); err != nil {
					t.Fatal(err)
				}
				_ = conn.Close(t.Context())
			}
			before := query[int](t, cfg, "SELECT count(*) FROM schema_migrations")
			_, upErr := migrate.Up(t.Context(), cfg, "v-after", tc.binary)
			checkErr := migrate.Check(t.Context(), cfg, tc.binary)
			if tc.refuses == "" {
				if upErr != nil || (checkErr != nil && !errors.Is(checkErr, migrate.ErrPending)) {
					t.Fatalf("Up = %v, Check = %v; want no refusal", upErr, checkErr)
				}
				return
			}
			for name, err := range map[string]error{"Up": upErr, "Check": checkErr} {
				if !errors.Is(err, migrate.ErrRefused) ||
					!strings.Contains(err.Error(), tc.refuses) {
					t.Errorf("%s = %v\nwant ErrRefused naming %q", name, err, tc.refuses)
				}
			}
			if after := query[int](
				t,
				cfg,
				"SELECT count(*) FROM schema_migrations",
			); after != before {
				t.Errorf("a refused Up applied %d files", after-before)
			}
		})
	}
}

func TestUnitMigrateUnreachable(t *testing.T) {
	cfg, err := pgx.ParseConfig("postgres://u:p@127.0.0.1:1/x?sslmode=disable&connect_timeout=2")
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams = nil
	s := []migrate.Stream{stream("s")}
	if _, err := migrate.Up(
		t.Context(),
		cfg,
		"v1",
		s,
	); err == nil ||
		!strings.Contains(err.Error(), "migrate: connect") {
		t.Errorf("Up: %v", err)
	}
	if err := migrate.Check(
		t.Context(),
		cfg,
		s,
	); err == nil ||
		!strings.Contains(err.Error(), "migrate: connect") {
		t.Errorf("Check: %v", err)
	}
	bad := []migrate.Stream{stream("S")}
	if _, err := migrate.Up(t.Context(), cfg, "v1", bad); !errors.Is(err, migrate.ErrStream) {
		t.Errorf("Up with a bad stream: %v", err)
	}
	if _, err := migrate.Inspect(t.Context(), cfg, bad); !errors.Is(err, migrate.ErrStream) {
		t.Errorf("Inspect with a bad stream: %v", err)
	}
}
