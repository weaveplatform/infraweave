package pg_test

import (
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// TestAccPurge purges a deleted account's attachments, config elements and
// contexts a batch at a time, one table after another; a purge stopped
// part way and run again finishes, and another account's rows stay.
func TestAccPurge(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	f.create(t, "A", "team", variable("X", "1"), variable("Y", "2"))
	f.create(t, "B", "team", variable("Z", "3"))
	for _, c := range []string{"a", "b"} {
		if _, _, err := f.svc.Attach(ctx, f.admin, c, "web", 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := f.svc.SetStackElement(ctx, f.admin, "web", variable("S", "4"), "",
		"/configId"); err != nil {
		t.Fatal(err)
	}
	kept := kernel.NewAccountID()
	if _, err := f.db.Exec(ctx, `INSERT INTO contexts (account_id, id, slug, space_id, name)
		VALUES ($1, $2, 'kept', $2, 'Kept')`, kept, kernel.NewULID()); err != nil {
		t.Fatal(err)
	}
	rows := func(acct kernel.AccountID) int {
		var n int
		if err := f.db.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM context_attachments WHERE account_id = $1)
			+ (SELECT count(*) FROM config_elements WHERE account_id = $1)
			+ (SELECT count(*) FROM contexts WHERE account_id = $1)`, acct).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := rows(f.acct); n != 8 {
		t.Fatalf("before the purge: %d rows, want 8", n)
	}

	// Two attachments, then three of the four elements, stopping there.
	for _, left := range []int{6, 3} {
		if done, err := f.store.Purge(ctx, f.acct, 3); err != nil || done || rows(f.acct) != left {
			t.Fatalf("a batch: done %t, %v, %d rows left, want %d", done, err, rows(f.acct), left)
		}
	}
	// Run again: the last element, the contexts, then nothing.
	for _, want := range []struct {
		done bool
		left int
	}{{false, 2}, {false, 0}, {true, 0}, {true, 0}} {
		if done, err := f.store.Purge(ctx, f.acct, 3); err != nil || done != want.done ||
			rows(f.acct) != want.left {
			t.Fatalf("a resumed batch: done %t, %v, %d rows left, want %+v", done, err,
				rows(f.acct), want)
		}
	}
	if n := rows(kept); n != 1 {
		t.Errorf("another account keeps %d rows, want 1", n)
	}
}
