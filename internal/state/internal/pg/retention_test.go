package pg_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/state/internal/core"
	"github.com/weaveplatform/infraweave/internal/state/internal/pg"
)

// ends answers the runs it holds as not ended.
type ends []kernel.ULID

func (e ends) NotEnded(_ context.Context, _ kernel.AccountID, runs []kernel.ULID) ([]kernel.ULID, error) {
	var out []kernel.ULID
	for _, r := range runs {
		if slices.Contains(e, r) {
			out = append(out, r)
		}
	}
	return out, nil
}

// versions writes versions into one database: each a row of a stack's
// workspace, written a while before now, the database's clock when the
// test began, by a run or none, with its object live in blob_objects
// under a key of its own.
type versions struct {
	t     *testing.T
	db    *pgxpool.Pool
	stack kernel.ULID
	now   time.Time
}

func newVersions(t *testing.T, db *pgxpool.Pool) versions {
	t.Helper()
	v := versions{t: t, db: db, stack: kernel.NewULID()}
	if err := db.QueryRow(t.Context(), `SELECT now()`).Scan(&v.now); err != nil {
		t.Fatal(err)
	}
	return v
}

// add writes the version id of acct's workspace, written ago, by run when
// it is set, making it the workspace's current version when current is
// true, and answers id.
func (v versions) add(id kernel.ULID, acct kernel.AccountID, workspace string, ago time.Duration, run *kernel.ULID, current bool) kernel.ULID {
	v.t.Helper()
	ctx := v.t.Context()
	if err := pg.New(v.db).OpenWorkspace(ctx, acct, v.stack, workspace); err != nil {
		v.t.Fatal(err)
	}
	key := acct.String() + "/" + id.String()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{
			`INSERT INTO managed_state_versions (account_id, id, stack_id, workspace, object_key,
			size, sha256, serial, lineage, run_id, created_at)
			VALUES ($1, $2, $3, $4, $5, 1, decode(repeat('00', 32), 'hex'), 1, 'l', $6, $7)`,
			[]any{acct, id, v.stack, workspace, key, run, v.now.Add(-ago)},
		},
		{`INSERT INTO blob_objects (store, key, size, sha256, state)
			VALUES ('states', $1, 1, decode(repeat('00', 32), 'hex'), 'live')`, []any{key}},
		{
			`UPDATE state_workspaces SET current_version_id = $4
			WHERE account_id = $1 AND stack_id = $2 AND name = $3 AND $5`,
			[]any{acct, v.stack, workspace, id, current},
		},
	} {
		if _, err := v.db.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			v.t.Fatal(err)
		}
	}
	return id
}

// left answers which of ids still have a row, and checks that every
// version left has a live object and that every object no version names
// is deleting, so no row ever outlives its object.
func (v versions) left(ids ...kernel.ULID) []kernel.ULID {
	v.t.Helper()
	ctx := v.t.Context()
	var orphaned, lost int
	if err := v.db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM managed_state_versions m LEFT JOIN blob_objects b
			ON b.store = 'states' AND b.key = m.object_key
			WHERE b.state IS DISTINCT FROM 'live'),
		(SELECT count(*) FROM blob_objects b WHERE b.store = 'states' AND b.state <> 'deleting'
			AND NOT EXISTS (SELECT 1 FROM managed_state_versions m WHERE m.object_key = b.key))`).
		Scan(&orphaned, &lost); err != nil {
		v.t.Fatal(err)
	}
	if orphaned != 0 || lost != 0 {
		v.t.Errorf("%d versions without a live object, %d live objects without a version",
			orphaned, lost)
	}
	rows, err := v.db.Query(ctx, `SELECT id FROM managed_state_versions WHERE id = ANY($1)`, ids)
	if err != nil {
		v.t.Fatal(err)
	}
	defer rows.Close()
	var out []kernel.ULID
	for rows.Next() {
		var id kernel.ULID
		if err := rows.Scan(&id); err != nil {
			v.t.Fatal(err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		v.t.Fatal(err)
	}
	slices.SortFunc(out, compare)
	return out
}

func compare(a, b kernel.ULID) int { return slices.Compare(a[:], b[:]) }

// sorted are ids in the order of their bytes, as PostgreSQL orders uuids.
func sorted(ids ...kernel.ULID) []kernel.ULID {
	out := slices.Clone(ids)
	slices.SortFunc(out, compare)
	return out
}

// TestAccStoreRetention reads the versions an hour's retention may
// delete: those a newer version of their workspace replaced over an hour
// ago, with their runs, a newer version within one instant being the one
// with the greater ID, in the order of their accounts and IDs from where
// the last read stopped; never a workspace's current version, even one a
// later version followed, or a version replaced within the hour, and none
// under a retention longer than the oldest version's age. Through
// the core, a pass deletes each of them but those of a run under way,
// handing each deleted version's object on in the transaction that
// deletes its row; a retention of 0 deletes nothing, and a delete leaves
// a current version whatever it is handed.
//
// ref: decision 0010 § "Rollback, searches and retention"
func TestAccStoreRetention(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	store := pg.New(db)
	v := newVersions(t, db)
	acct, other := kernel.NewAccountID(), kernel.NewAccountID()
	ended, going := kernel.NewULID(), kernel.NewULID()

	id := kernel.NewULID
	d1 := v.add(id(), acct, "", 5*time.Hour, nil, false)
	d2 := v.add(id(), acct, "", 4*time.Hour, &ended, false)
	d3 := v.add(id(), acct, "", 3*time.Hour, &going, false)
	d4 := v.add(id(), acct, "", 2*time.Hour, nil, false)
	d5 := v.add(id(), acct, "", 30*time.Minute, nil, true)
	late := v.add(id(), acct, "late", 5*time.Hour, nil, true)
	later := v.add(id(), acct, "late", 3*time.Hour, nil, false)
	o1 := v.add(id(), other, "", 5*time.Hour, nil, false)
	o2 := v.add(id(), other, "", 4*time.Hour, nil, true)
	tie := sorted(id(), id())
	t1 := v.add(tie[0], acct, "tie", 6*time.Hour, nil, false)
	t2 := v.add(tie[1], acct, "tie", 6*time.Hour, nil, true)

	want := []core.Expired{
		{Account: acct, ID: d1},
		{Account: acct, ID: d2, Run: &ended},
		{Account: acct, ID: d3, Run: &going},
		{Account: acct, ID: t1},
		{Account: other, ID: o1},
	}
	slices.SortFunc(want, func(a, b core.Expired) int {
		if c := compare(a.Account.ULID, b.Account.ULID); c != 0 {
			return c
		}
		return compare(a.ID, b.ID)
	})
	got, err := store.Expired(ctx, core.Expired{}, time.Hour, 100)
	if err != nil || !equal(got, want) {
		t.Fatalf("Expired = %+v, %v; want %+v", got, err, want)
	}
	for i := range want[1:] {
		page, err := store.Expired(ctx, want[i], time.Hour, 1)
		if err != nil || !equal(page, want[i+1:i+2]) {
			t.Errorf("the page after %d = %+v, %v; want %+v", i, page, err, want[i+1])
		}
	}
	if got, err := store.Expired(ctx, core.Expired{}, 7*time.Hour, 100); err != nil ||
		!equal(got, nil) {
		t.Errorf("Expired over seven hours = %+v, %v; want none", got, err)
	}
	if keys, err := store.DeleteVersions(ctx, acct, []kernel.ULID{d5, late}); err != nil ||
		len(keys) != 0 {
		t.Errorf("a delete of current versions = %v, %v", keys, err)
	}

	all := []kernel.ULID{d1, d2, d3, d4, d5, late, later, o1, o2, t1, t2}
	objects := blobs.New(db, queue.New(db), nil, nil, objectstore.Config{})
	off := core.New(core.Deps{DB: db, Store: store, Objects: objects, Ends: ends{going}})
	if n, err := off.Retain(ctx); n != 0 || err != nil ||
		!slices.Equal(v.left(all...), sorted(all...)) {
		t.Errorf("a retention of 0 = %d, %v", n, err)
	}
	s := core.New(core.Deps{
		DB: db, Store: store, Objects: objects, Ends: ends{going}, Retention: time.Hour,
	})
	if n, err := s.Retain(ctx); n != 4 || err != nil {
		t.Errorf("Retain = %d, %v; want 4 deleted", n, err)
	}
	if got, want := v.left(all...), sorted(d3, d4, d5, late, later, o2, t2); !slices.Equal(got,
		want) {
		t.Errorf("left %v, want %v", got, want)
	}
	var deleting int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM blob_objects b JOIN jobs j
		ON j.kind = 'blob.delete' AND j.unique_key = 'states/' || b.key
		WHERE b.state = 'deleting'`).Scan(&deleting); err != nil || deleting != 4 {
		t.Errorf("%d objects deleting with their blob.delete, %v; want 4", deleting, err)
	}
}

// equal reports whether the versions a and b name the same versions and
// runs in the same order.
func equal(a, b []core.Expired) bool {
	return slices.EqualFunc(a, b, func(x, y core.Expired) bool {
		return x.Account == y.Account && x.ID == y.ID &&
			(x.Run == nil) == (y.Run == nil) && (x.Run == nil || *x.Run == *y.Run)
	})
}

// brittle are objects whose deletion of one key fails.
type brittle struct {
	core.Objects
	key string
}

var errBrittle = errors.New("the object store is down")

func (b brittle) Delete(ctx context.Context, store objectstore.Name, key string) error {
	if key == b.key {
		return errBrittle
	}
	return b.Objects.Delete(ctx, store, key) //nolint:wrapcheck // the test reads it as it is
}

// TestAccStoreRetentionInterrupted stops a pass over 600 expired versions
// in its second batch: the first batch's rows are gone and their objects
// deleting, while the second batch's rows stay with their objects live,
// so no row is left without its object; the next pass deletes the rest.
func TestAccStoreRetentionInterrupted(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	store := pg.New(db)
	v := newVersions(t, db)
	acct := kernel.NewAccountID()
	if err := store.OpenWorkspace(ctx, acct, v.stack, ""); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO managed_state_versions (account_id, id, stack_id, workspace, object_key,
			size, sha256, serial, lineage, created_at)
			SELECT $1, gen_random_uuid(), $2, '', 'bulk/' || i, 1,
				decode(repeat('00', 32), 'hex'), i, 'l',
				now() - interval '1 day' + i * interval '1 second'
			FROM generate_series(1, 601) AS i`,
		`INSERT INTO blob_objects (store, key, size, sha256, state)
			SELECT 'states', object_key, 1, sha256, 'live' FROM managed_state_versions
			WHERE account_id = $1 AND stack_id = $2`,
		`UPDATE state_workspaces SET current_version_id = (SELECT id FROM managed_state_versions
			WHERE account_id = $1 AND stack_id = $2 AND serial = 601)
			WHERE account_id = $1 AND stack_id = $2`,
	} {
		if _, err := db.Exec(ctx, stmt, acct, v.stack); err != nil {
			t.Fatal(err)
		}
	}
	var second string
	if err := db.QueryRow(ctx, `SELECT object_key FROM managed_state_versions
		WHERE serial < 601 ORDER BY account_id, id OFFSET $1 LIMIT 1`,
		core.PurgeBatch+50).Scan(&second); err != nil {
		t.Fatal(err)
	}
	objects := blobs.New(db, queue.New(db), nil, nil, objectstore.Config{})
	s := core.New(core.Deps{
		DB: db, Store: store, Objects: brittle{objects, second}, Ends: ends{}, Retention: time.Hour,
	})
	if n, err := s.Retain(ctx); n != core.PurgeBatch || !errors.Is(err, errBrittle) {
		t.Fatalf("the interrupted pass = %d, %v; want the first batch's %d", n, err,
			core.PurgeBatch)
	}
	if n := count(t, db, "managed_state_versions", acct); n != 101 {
		t.Errorf("%d versions after the interrupted pass, want the second batch's 100 and the "+
			"current one", n)
	}
	v.left()
	s = core.New(core.Deps{DB: db, Store: store, Objects: objects, Ends: ends{}, Retention: time.Hour})
	if n, err := s.Retain(ctx); n != 100 || err != nil {
		t.Errorf("the next pass = %d, %v; want the 100 left", n, err)
	}
	if n := count(t, db, "managed_state_versions", acct); n != 1 {
		t.Errorf("%d versions after the next pass, want the current one", n)
	}
	v.left()
}
