package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/state/internal/core"
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

// anyStack holds every stack.
type anyStack struct{}

func (anyStack) Hold(context.Context, kernel.AccountID, kernel.ULID) error { return nil }

// info is a LockInfo of id as Terraform sends it, with a field the server
// keeps without reading.
func info(id string) json.RawMessage {
	return json.RawMessage(`{"ID": "` + id + `", "Operation": "OperationTypeApply"}`)
}

// TestAccStoreLocks takes and releases locks through the core over the
// store: of concurrent exclusive locks on a new workspace exactly one is
// granted; shared locks are held together, refuse an exclusive one with
// the oldest's LockInfo as it was sent, and are granted again to their
// run; another run's lock is released only by a breaking caller, which
// the trail records; and a run's end releases its locks.
func TestAccStoreLocks(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := core.New(core.Deps{DB: db, Store: pg.New(db), Stacks: anyStack{}})
	at := core.Target{Account: kernel.NewAccountID(), Stack: kernel.NewULID(), Workspace: "blue"}

	const racers = 8
	errs := make([]error, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Go(func() {
			id := fmt.Sprintf("x%d", i)
			errs[i] = s.Lock(ctx, at, core.Lock{
				ID: id, Mode: core.Exclusive, Run: kernel.NewULID(), Info: info(id),
			})
		})
	}
	wg.Wait()
	granted, winner := 0, ""
	for i, err := range errs {
		switch c, ok := errors.AsType[*core.Conflict](err); {
		case err == nil:
			granted++
			winner = fmt.Sprintf("x%d", i)
		case !ok:
			t.Fatalf("racer %d = %v", i, err)
		case c.Holder.Mode != core.Exclusive:
			t.Errorf("racer %d was refused for %+v", i, c.Holder)
		}
	}
	if granted != 1 {
		t.Fatalf("%d of %d exclusive locks were granted", granted, racers)
	}
	breaker := kernel.NewULID()
	if err := s.Unlock(ctx, core.Unlock{
		Target: at, Run: breaker, Break: true,
		RemoteIP: "192.0.2.7",
	}); err != nil {
		t.Fatalf("a force-unlock = %v", err)
	}
	var (
		meta, ip string
		by       kernel.ULID
	)
	if err := db.QueryRow(ctx, `SELECT affected_metadata::text, remote_ip, actor_run
		FROM audit_trail_entries WHERE account_id = $1 AND action = $2`,
		at.Account, core.ActionBreakLock).Scan(&meta, &ip, &by); err != nil {
		t.Fatalf("the break was not recorded: %v", err)
	}
	if !strings.Contains(meta, `"ID": "`+winner+`"`) || !strings.Contains(meta, `"blue"`) ||
		ip != "192.0.2.7" || by != breaker {
		t.Errorf("the break recorded %s from %q by %s", meta, ip, by)
	}

	planA, planB := kernel.NewULID(), kernel.NewULID()
	for _, l := range []core.Lock{
		{ID: "a", Mode: core.Shared, Run: planA, Info: info("a")},
		{ID: "b", Mode: core.Shared, Run: planB, Info: info("b")},
		{ID: "a", Mode: core.Shared, Run: planA, Info: info("a")},
	} {
		if err := s.Lock(ctx, at, l); err != nil {
			t.Fatalf("shared lock %s = %v", l.ID, err)
		}
	}
	err := s.Lock(ctx, at, core.Lock{
		ID: "c", Mode: core.Exclusive, Run: kernel.NewULID(),
		Info: info("c"),
	})
	if c, ok := errors.AsType[*core.Conflict](err); !ok || c.Holder.ID != "a" ||
		string(c.Holder.Info) != string(info("a")) || c.Holder.Run != planA ||
		c.Holder.CreatedAt.IsZero() {
		t.Errorf("an exclusive lock beside shared ones = %v", err)
	}
	if err := s.Unlock(ctx, core.Unlock{Target: at, Run: planA, ID: "b"}); !errors.Is(err,
		core.ErrBreak) {
		t.Errorf("a release of another run's lock = %v", err)
	}
	if err := s.Unlock(ctx, core.Unlock{Target: at, Run: planA, ID: "a"}); err != nil {
		t.Errorf("a release of the run's own lock = %v", err)
	}
	if err := s.RunEnded(ctx, at.Account, planB, kernel.RunStateFinished); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, "state_locks", at.Account); n != 0 {
		t.Errorf("%d locks outlive their runs", n)
	}
	if n := count(t, db, "audit_trail_entries", at.Account); n != 1 {
		t.Errorf("%d entries, want the one break", n)
	}

	other := at
	other.Workspace = "never"
	if err := s.Unlock(ctx, core.Unlock{Target: other, Run: planA, ID: "a"}); !errors.Is(err,
		core.ErrNoWorkspace) {
		t.Errorf("an unlock of a workspace never locked = %v", err)
	}
}

// recorder records the objects a purge hands on.
type recorder struct{ keys []string }

func (r *recorder) Delete(_ context.Context, store objectstore.Name, key string) error {
	r.keys = append(r.keys, string(store)+"/"+key)
	return nil
}

// TestAccStorePurge purges a stack's state through the core over the
// store after one batch of a purge stopped part way, handing on the
// versions' objects that batch left and leaving another stack's and
// another account's state; then the account's, leaving the other's.
func TestAccStorePurge(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	store := pg.New(db)
	objects := &recorder{}
	s := core.New(core.Deps{DB: db, Store: store, Stacks: anyStack{}, Objects: objects})
	acct, other := kernel.NewAccountID(), kernel.NewAccountID()
	gone, kept := kernel.NewULID(), kernel.NewULID()
	seed := func(acct kernel.AccountID, stack kernel.ULID) {
		t.Helper()
		for _, stmt := range []string{
			`INSERT INTO state_workspaces (account_id, stack_id, name)
				VALUES ($1, $2, ''), ($1, $2, 'blue')`,
			`INSERT INTO state_locks (account_id, stack_id, workspace, lock_id, mode, run_id, info)
				SELECT $1, $2, 'blue', 'l' || i, 'shared', gen_random_uuid(), '{}'
				FROM generate_series(1, 3) AS i`,
			`INSERT INTO managed_state_versions (account_id, id, stack_id, workspace, object_key,
				size, sha256, serial, lineage)
				SELECT $1, gen_random_uuid(), $2, '', $2::uuid::text || '/' || i, 1,
				decode(repeat('00', 32), 'hex'), i, 'l'
				FROM generate_series(1, 5) AS i`,
		} {
			if _, err := db.Exec(ctx, stmt, acct, stack); err != nil {
				t.Fatal(err)
			}
		}
	}
	seed(acct, gone)
	seed(acct, kept)
	seed(other, gone)
	rows := func(acct kernel.AccountID, stack kernel.ULID) int {
		t.Helper()
		var n int
		if err := db.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM state_workspaces WHERE account_id = $1 AND stack_id = $2)
			+ (SELECT count(*) FROM state_locks WHERE account_id = $1 AND stack_id = $2)
			+ (SELECT count(*) FROM managed_state_versions WHERE account_id = $1 AND stack_id = $2)`,
			acct, stack).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	first, done, err := store.Purge(ctx, acct, &gone, 2)
	if err != nil || done || len(first) != 2 || rows(acct, gone) != 8 {
		t.Fatalf("one batch = %v %v %v, %d rows left", first, done, err, rows(acct, gone))
	}
	if err := s.Purge(ctx, acct, &gone); err != nil {
		t.Fatal(err)
	}
	if rows(acct, gone) != 0 || rows(acct, kept) != 10 || rows(other, gone) != 10 ||
		len(objects.keys) != 3 {
		t.Errorf("after the stack's purge: %d, %d and %d rows, objects %v", rows(acct, gone),
			rows(acct, kept), rows(other, gone), objects.keys)
	}
	var prefix string
	if err := db.QueryRow(ctx, `SELECT $1::uuid::text || '/'`, gone).Scan(&prefix); err != nil {
		t.Fatal(err)
	}
	for _, k := range append(objects.keys, first...) {
		if !strings.HasPrefix(strings.TrimPrefix(k, "states/"), prefix) {
			t.Errorf("the purge handed on %s", k)
		}
	}
	if err := s.Purge(ctx, acct, nil); err != nil {
		t.Fatal(err)
	}
	if rows(acct, kept) != 0 || rows(other, gone) != 10 || len(objects.keys) != 8 {
		t.Errorf("after the account's purge: %d and %d rows, %d objects", rows(acct, kept),
			rows(other, gone), len(objects.keys))
	}
}

// TestAccStoreUnavailable fails each method on a closed pool.
func TestAccStoreUnavailable(t *testing.T) {
	db := pool(t)
	s := pg.New(db)
	db.Close()
	ctx := context.Background()
	at := core.Target{Account: kernel.NewAccountID(), Stack: kernel.NewULID()}
	if err := s.OpenWorkspace(ctx, at.Account, at.Stack, ""); err == nil {
		t.Error("OpenWorkspace on a closed pool succeeded")
	}
	if _, err := s.LockWorkspace(ctx, at); err == nil {
		t.Error("LockWorkspace on a closed pool succeeded")
	}
	if _, err := s.Locks(ctx, at); err == nil {
		t.Error("Locks on a closed pool succeeded")
	}
	if err := s.AddLock(
		ctx,
		at,
		core.Lock{ID: "a", Mode: core.Shared, Info: info("a")},
	); err == nil {
		t.Error("AddLock on a closed pool succeeded")
	}
	if err := s.RemoveLocks(ctx, at, []string{"a"}); err == nil {
		t.Error("RemoveLocks on a closed pool succeeded")
	}
	if err := s.ReleaseRun(ctx, at.Account, kernel.NewULID()); err == nil {
		t.Error("ReleaseRun on a closed pool succeeded")
	}
	if err := s.Audit(ctx, kernel.AuditEntry{}); err == nil {
		t.Error("Audit of no entry succeeded")
	}
	if _, _, err := s.Purge(ctx, at.Account, nil, 1); err == nil {
		t.Error("Purge on a closed pool succeeded")
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
