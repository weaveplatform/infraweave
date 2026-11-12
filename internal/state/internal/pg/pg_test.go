package pg_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/state/internal/pg"
)

// TestMain migrates the platform stream and the state stream alone: no
// state table references another domain's.
func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream(), pg.Stream())) }

func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(t.Context(), sqltest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// count counts table's rows of acct.
func count(t *testing.T, db tx.Querier, table string, acct kernel.AccountID) int {
	t.Helper()
	var n int
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM `+table+` WHERE account_id = $1`,
		acct).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestAccStoreWorkspace opens a workspace once however often it is
// opened, in a transaction or not, and refuses a name over 512 bytes.
func TestAccStoreWorkspace(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	acct, stack := kernel.NewAccountID(), kernel.NewULID()
	if err := s.OpenWorkspace(ctx, acct, stack, ""); err != nil {
		t.Fatal(err)
	}
	if err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		if err := s.OpenWorkspace(ctx, acct, stack, ""); err != nil {
			return err //nolint:wrapcheck // the test reads it as it is
		}
		return s.OpenWorkspace(ctx, acct, stack, "staging") //nolint:wrapcheck // as above
	}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, "state_workspaces", acct); n != 2 {
		t.Errorf("%d workspaces, want the default and staging", n)
	}
	if err := s.OpenWorkspace(ctx, acct, stack, strings.Repeat("a", 512)); err != nil {
		t.Errorf("a 512-byte name = %v", err)
	}
	if err := s.OpenWorkspace(ctx, acct, stack, strings.Repeat("a", 513)); err == nil {
		t.Error("a 513-byte name was opened")
	}
}

// TestAccSchema checks the locks' and versions' constraints: a lock's
// mode and LockInfo size, a version's digest and its serial and lineage
// unless client-encrypted, and that both go with their workspace.
func TestAccSchema(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	acct, stack := kernel.NewAccountID(), kernel.NewULID()
	if err := pg.New(db).OpenWorkspace(ctx, acct, stack, ""); err != nil {
		t.Fatal(err)
	}
	lock := func(id, mode, info string) error {
		_, err := db.Exec(ctx, `INSERT INTO state_locks
			(account_id, stack_id, workspace, lock_id, mode, run_id, info)
			VALUES ($1, $2, '', $3, $4, $5, $6)`,
			acct, stack, id, mode, kernel.NewULID(), info)
		return err //nolint:wrapcheck // the test reads it as it is
	}
	version := func(sha int, serial *int64, lineage *string, encrypted bool) error {
		_, err := db.Exec(ctx, `INSERT INTO managed_state_versions
			(account_id, id, stack_id, workspace, object_key, size, sha256, serial, lineage,
			client_encrypted, run_id)
			VALUES ($1, $2, $3, '', 'key', 10, $4, $5, $6, $7, $8)`,
			acct, kernel.NewULID(), stack, make([]byte, sha), serial, lineage, encrypted,
			kernel.NewULID())
		return err //nolint:wrapcheck // as above
	}
	serial, lineage := new(int64(1)), new("f3c1")
	for name, err := range map[string]error{
		"a shared lock":    lock("a", "shared", `{"ID": "a"}`),
		"an exclusive one": lock("b", "exclusive", `{"ID": "b"}`),
		"16 KiB of LockInfo": lock("c", "shared",
			`{"Info": "`+strings.Repeat("x", 16384-len(`{"Info": ""}`))+`"}`),
		"a plain version":            version(32, serial, lineage, false),
		"a client-encrypted version": version(32, nil, nil, true),
	} {
		if err != nil {
			t.Errorf("%s = %v", name, err)
		}
	}
	for name, err := range map[string]error{
		"another mode":            lock("d", "read", `{}`),
		"over 16 KiB of LockInfo": lock("e", "shared", `"`+strings.Repeat("x", 16384)+`"`),
		"a lock of no workspace": func() error {
			_, err := db.Exec(ctx, `INSERT INTO state_locks
				(account_id, stack_id, workspace, lock_id, mode, run_id, info)
				VALUES ($1, $2, 'none', 'f', 'shared', $3, '{}')`, acct, stack, kernel.NewULID())
			return err //nolint:wrapcheck // as above
		}(),
		"a short digest":             version(31, serial, lineage, false),
		"a plain version no serial":  version(32, nil, lineage, false),
		"a plain version no lineage": version(32, serial, nil, false),
	} {
		if err == nil {
			t.Errorf("%s was written", name)
		}
	}

	if _, err := db.Exec(ctx, `DELETE FROM state_workspaces WHERE account_id = $1`,
		acct); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"state_locks", "managed_state_versions"} {
		if n := count(t, db, table, acct); n != 0 {
			t.Errorf("%d rows of %s outlive their workspace", n, table)
		}
	}
}

// TestAccStoreUnavailable fails each method on a closed pool.
func TestAccStoreUnavailable(t *testing.T) {
	db := pool(t)
	s := pg.New(db)
	db.Close()
	if err := s.OpenWorkspace(context.Background(), kernel.NewAccountID(), kernel.NewULID(),
		""); err == nil {
		t.Error("OpenWorkspace on a closed pool succeeded")
	}
}

// TestUnitHeld declares the workspaces' account, a held stack column for
// each table, and the run of each lock and version.
func TestUnitHeld(t *testing.T) {
	want := []string{
		"account state_workspaces(account_id)",
		"stack state_workspaces(account_id, stack_id)",
		"stack state_locks(account_id, stack_id)",
		"stack managed_state_versions(account_id, stack_id)",
		"run state_locks(account_id, run_id)",
		"run managed_state_versions(account_id, run_id)",
	}
	held := pg.Held()
	got := make([]string, len(held))
	for i, h := range held {
		got[i] = h.Entity + " " + h.String()
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("Held:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if pg.Stream().Name != pg.StreamName {
		t.Errorf("the stream is named %q", pg.Stream().Name)
	}
}
