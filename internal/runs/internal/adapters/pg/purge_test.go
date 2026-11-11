package pg_test

import (
	"context"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
)

// TestAccPurge purges a deleted account's transitions, outputs, tracked
// runs, run locks and runs a batch at a time, one table after another; a
// purge stopped part way and run again finishes, and another account's
// rows stay.
func TestAccPurge(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	acct, other := kernel.NewAccountID(), kernel.NewAccountID()
	first, second := kernel.NewULID(), kernel.NewULID()
	insertRun(t, db, acct, first, true)
	insertRun(t, db, acct, first, false)
	insertRun(t, db, acct, second, true)
	insertRun(t, db, other, first, true)
	rows := func(acct kernel.AccountID) int {
		n := 0
		for _, table := range tables {
			n += count(t, db, table, acct)
		}
		return n
	}
	// Three runs and their transitions, two of them their stack's tracked
	// run, run lock holder and output writer.
	if n := rows(acct); n != 12 {
		t.Fatalf("before the purge: %d rows, want 12", n)
	}

	// Two of the three transitions, then the last with nothing else.
	for _, left := range []int{10, 9} {
		if done, err := s.Purge(ctx, acct, 2); err != nil || done || rows(acct) != left {
			t.Fatalf("a batch: done %t, %v, %d rows left, want %d", done, err, rows(acct), left)
		}
	}
	// Run again: the outputs, tracked runs, run locks, two runs and the
	// last, then nothing.
	for _, want := range []struct {
		done bool
		left int
	}{{false, 7}, {false, 5}, {false, 3}, {false, 1}, {false, 0}, {true, 0}, {true, 0}} {
		if done, err := s.Purge(ctx, acct, 2); err != nil || done != want.done ||
			rows(acct) != want.left {
			t.Fatalf("a resumed batch: done %t, %v, %d rows left, want %+v", done, err,
				rows(acct), want)
		}
	}
	if n := rows(other); n != 5 {
		t.Errorf("another account keeps %d rows, want 5", n)
	}

	db.Close()
	if _, err := s.Purge(context.Background(), acct, 2); err == nil {
		t.Error("a purge on a closed pool passed")
	}
}
