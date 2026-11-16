package pg_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/logs"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/report"
)

// TestAccLogsStore checks the latest state version at which a run entered
// a state, a state version's chunks read in sequence order from a
// sequence number with a limit, a lost chunk read with no key, the
// transitions that wrote logs marked as having them, a state version's
// extent with and without a reported count, and LockChunks holding a
// purge's delete of the chunks until its transaction ends.
func TestAccLogsStore(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db, nil)
	acct, other := kernel.NewAccountID(), kernel.NewAccountID()
	id := insertRun(t, db, acct, kernel.NewULID(), false)
	theirs := insertRun(t, db, other, kernel.NewULID(), false)
	for seq, state := range map[int]kernel.RunState{
		2: kernel.RunStatePlanning, 3: kernel.RunStateApplying, 4: kernel.RunStatePlanning,
	} {
		if _, err := db.Exec(ctx, `INSERT INTO run_state_transitions
			(account_id, run_id, sequence, state, terminal) VALUES ($1, $2, $3, $4, false)`,
			acct, id, seq, state); err != nil {
			t.Fatal(err)
		}
	}
	for state, want := range map[kernel.RunState]int64{
		kernel.RunStatePlanning: 4, kernel.RunStateQueued: 1, kernel.RunStateFailed: 0,
	} {
		if v, ok, err := s.Entered(ctx, acct, id, state); v != want || ok != (want > 0) ||
			err != nil {
			t.Errorf("Entered(%s) = %d, %t, %v", state, v, ok, err)
		}
	}
	if _, ok, err := s.Entered(ctx, other, id, kernel.RunStateQueued); ok || err != nil {
		t.Errorf("Entered in another account = %t, %v", ok, err)
	}

	run, err := s.Handed(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_800_000_000, 0).UTC()
	for _, c := range []report.Chunk{
		{Version: 4, Seq: 3, Bytes: 30},
		{Version: 4, Seq: 0, Bytes: 10},
		{Version: 4, Seq: 1, Bytes: 20},
		{Version: 2, Seq: 0, Bytes: 5},
	} {
		c.State, c.Key = kernel.RunStatePlanning, "k"+string(
			rune('0'+c.Version),
		)+string(
			rune('0'+c.Seq),
		)
		c.FirstAt, c.LastAt = at.Add(time.Duration(c.Seq)*time.Second), at.Add(time.Minute)
		if err := s.IndexChunk(ctx, run, c); err != nil {
			t.Fatal(err)
		}
	}
	var marked []int64
	rows, err := db.Query(ctx, `SELECT sequence FROM run_state_transitions
		WHERE account_id = $1 AND run_id = $2 AND has_logs ORDER BY sequence`, acct, id)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		marked = append(marked, n)
	}
	if !slices.Equal(marked, []int64{2, 4}) {
		t.Errorf("the transitions with logs are %v", marked)
	}

	got, err := s.Chunks(ctx, acct, id, 4, 1, 10)
	for i := range got {
		got[i].FirstAt, got[i].LastAt = got[i].FirstAt.UTC(), got[i].LastAt.UTC()
	}
	if err != nil || len(got) != 2 || got[0] != (report.Chunk{
		Version: 4, State: kernel.RunStatePlanning, Seq: 1, Key: "k41", Bytes: 20,
		FirstAt: at.Add(time.Second), LastAt: at.Add(time.Minute),
	}) || got[1].Seq != 3 {
		t.Errorf("Chunks from 1 = %+v, %v", got, err)
	}
	if got, err := s.Chunks(ctx, acct, id, 4, 0, 2); err != nil || len(got) != 2 ||
		got[0].Seq != 0 || got[1].Seq != 1 {
		t.Errorf("Chunks limited to 2 = %+v, %v", got, err)
	}
	if got, err := s.Chunks(ctx, other, theirs, 4, 0, 10); err != nil || len(got) != 0 {
		t.Errorf("another account's chunks = %+v, %v", got, err)
	}
	if err := s.IndexChunk(ctx, run, report.Chunk{
		Version: 4, State: kernel.RunStatePlanning, Seq: 5, FirstAt: at, LastAt: at, Lost: true,
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Chunks(ctx, acct, id, 4, 5, 1); err != nil || len(got) != 1 ||
		!got[0].Lost || got[0].Key != "" || got[0].Bytes != 0 {
		t.Errorf("the lost chunk = %+v, %v", got, err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO run_log_chunks (account_id, run_id, state_version,
		seq, state, object_key, bytes, first_at, last_at, lost)
		VALUES ($1, $2, 4, 6, 'PLANNING', 'k46', 0, now(), now(), true)`, acct, id); err == nil {
		t.Error("a lost chunk with an object was indexed")
	}

	entered := func(v int64) time.Time {
		var ts time.Time
		if err := db.QueryRow(ctx, `SELECT timestamp FROM run_state_transitions
			WHERE account_id = $1 AND run_id = $2 AND sequence = $3`, acct, id, v).
			Scan(&ts); err != nil {
			t.Fatal(err)
		}
		return ts
	}
	extent := func(v, from int64, want logs.Extent) {
		t.Helper()
		got, err := s.Extent(ctx, acct, id, v, from)
		if err != nil || got.Chunks != want.Chunks || got.Stored != want.Stored ||
			!got.Before.Equal(want.Before) || !got.Entered.Equal(want.Entered) {
			t.Errorf("Extent(%d, %d) = %+v, %v, want %+v", v, from, got, err, want)
		}
	}
	extent(4, 3, logs.Extent{
		Chunks: 6, Stored: "k40", Before: at.Add(time.Minute), Entered: entered(4),
	})
	extent(4, 0, logs.Extent{Chunks: 6, Stored: "k40", Before: entered(4), Entered: entered(4)})
	extent(3, 0, logs.Extent{Before: entered(3), Entered: entered(3)})
	for n, want := range map[int64]int64{9: 9, 2: 6} {
		if err := s.CountChunks(ctx, run, 4, n); err != nil {
			t.Fatal(err)
		}
		extent(4, 3, logs.Extent{
			Chunks: want, Stored: "k40", Before: at.Add(time.Minute), Entered: entered(4),
		})
	}
	if _, err := s.Extent(ctx, acct, id, 99, 0); !errors.Is(err, lifecycle.ErrNotFound) {
		t.Errorf("Extent of no transition = %v", err)
	}

	conn, err := db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `SET lock_timeout = '200ms'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		seqs, err := s.LockChunks(ctx, acct, id, 4)
		if err != nil || !slices.Equal(seqs, []int64{0, 1, 3, 5}) {
			t.Errorf("LockChunks = %v, %v", seqs, err)
		}
		if _, err := conn.Exec(ctx, `DELETE FROM run_log_chunks WHERE account_id = $1`,
			acct); err == nil {
			t.Error("a purge deleted locked chunks")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	conn.Release()

	db.Close()
	for name, err := range map[string]error{
		"Entered": func() error {
			_, _, err := s.Entered(context.Background(), acct, id, kernel.RunStatePlanning)
			return err
		}(),
		"Chunks": func() error {
			_, err := s.Chunks(context.Background(), acct, id, 4, 0, 1)
			return err
		}(),
		"LockChunks": func() error {
			_, err := s.LockChunks(context.Background(), acct, id, 4)
			return err
		}(),
		"Extent": func() error {
			_, err := s.Extent(context.Background(), acct, id, 4, 0)
			return err
		}(),
		"CountChunks": s.CountChunks(context.Background(), run, 4, 1),
	} {
		if err == nil {
			t.Errorf("%s on a closed pool succeeded", name)
		}
	}
}
