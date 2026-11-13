package pg_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/report"
)

// TestAccLogsStore checks the latest state version at which a run entered
// a state, a state version's chunks read in sequence order from a
// sequence number with a limit, the transitions that wrote logs marked as
// having them, and LockChunks holding a purge's delete of the chunks
// until its transaction ends.
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

	conn, err := db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `SET lock_timeout = '200ms'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		seqs, err := s.LockChunks(ctx, acct, id, 4)
		if err != nil || !slices.Equal(seqs, []int64{0, 1, 3}) {
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
	} {
		if err == nil {
			t.Errorf("%s on a closed pool succeeded", name)
		}
	}
}
