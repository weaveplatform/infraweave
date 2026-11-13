package pg_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/report"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// TestAccReportStore checks a run's state by state version, the plan's
// delta kept on it, its log chunks indexed once by state version and
// sequence number, its other
// uploads recorded once by kind and name with the last workspace read
// back, and a deleted account's chunks and records purged a batch at a
// time with their objects' stores and keys and each state's compacted
// log, another account's kept.
func TestAccReportStore(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db, nil)
	acct, other := kernel.NewAccountID(), kernel.NewAccountID()
	id := insertRun(t, db, acct, kernel.NewULID(), false)
	run, err := s.Handed(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if state, err := s.StateAt(ctx, acct, id, 1); err != nil || state != kernel.RunStateQueued {
		t.Errorf("StateAt(1) = %s, %v", state, err)
	}
	if _, err := s.StateAt(ctx, acct, id, 2); !errors.Is(err, lifecycle.ErrNotFound) {
		t.Errorf("StateAt(2) = %v", err)
	}
	if err := s.SetDelta(ctx, run, workersapi.Delta{
		Add: 1, Import: 4, Move: 2, Forget: 3, Resources: 9, Outputs: true,
	}); err != nil {
		t.Fatal(err)
	}
	var delta string
	if err := db.QueryRow(ctx, `SELECT delta::text FROM runs WHERE id = $1`, id).
		Scan(&delta); err != nil ||
		delta != `{"add": 1, "move": 2, "change": 0, "delete": 0, "forget": 3, "import": 4, `+
			`"outputs": true, "resources": 9}` {
		t.Errorf("the kept delta is %s, %v", delta, err)
	}

	at := time.Unix(1_800_000_000, 0).UTC()
	for _, seq := range []int64{0, 1} {
		if err := s.IndexChunk(ctx, run, report.Chunk{
			Version: 3, State: kernel.RunStatePlanning, Seq: seq, Key: "k/" + string(rune('0'+seq)),
			Bytes: 10, FirstAt: at, LastAt: at,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.IndexChunk(ctx, run, report.Chunk{
		Version: 3, Seq: 0, Key: "again",
		FirstAt: at, LastAt: at,
	}); err == nil {
		t.Error("a chunk indexed twice")
	}
	for v, want := range map[int64]bool{3: true, 4: false} {
		if ok, err := s.Indexed(ctx, run, v, 0); err != nil || ok != want {
			t.Errorf("Indexed(%d, 0) = %t, %v", v, ok, err)
		}
	}

	if _, ok, err := s.Workspace(ctx, run); ok || err != nil {
		t.Errorf("Workspace of none = %t, %v", ok, err)
	}
	for _, a := range []report.Artifact{
		{Version: 3, Kind: workersapi.UploadWorkspace, Name: "ws", Key: "ws-3"},
		{Version: 5, Kind: workersapi.UploadWorkspace, Name: "ws", Key: "ws-5"},
		{Version: 5, Kind: workersapi.UploadArtifact, Name: "plan.json", Key: "plan", Size: 9},
		{Version: 5, Kind: workersapi.UploadObservability, Name: "trace", Key: "trace"},
	} {
		if err := s.Record(ctx, run, a); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Record(ctx, run, report.Artifact{
		Version: 5, Kind: workersapi.UploadArtifact,
		Name: "plan.json", Key: "again",
	}); err == nil {
		t.Error("an artifact recorded twice")
	}
	if ok, err := s.Recorded(ctx, run, 5, workersapi.UploadArtifact, "plan.json"); !ok ||
		err != nil {
		t.Errorf("Recorded = %t, %v", ok, err)
	}
	if ok, err := s.Recorded(ctx, run, 5, workersapi.UploadWorkspace, "plan.json"); ok ||
		err != nil {
		t.Errorf("Recorded of another kind = %t, %v", ok, err)
	}
	if key, ok, err := s.Workspace(ctx, run); key != "ws-5" || !ok || err != nil {
		t.Errorf("Workspace = %q, %t, %v", key, ok, err)
	}

	otherRun, err := s.Handed(ctx, insertRun(t, db, other, kernel.NewULID(), false))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.IndexChunk(ctx, otherRun, report.Chunk{
		Version: 1, Key: "theirs", FirstAt: at,
		LastAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	var got []report.Object
	for {
		objs, err := s.PurgeObjects(ctx, acct, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(objs) == 0 {
			break
		}
		if n := len(objs) - strings.Count(fmt.Sprint(objs), ".log"); n > 3 {
			t.Errorf("a batch of %d rows", n)
		}
		got = append(got, objs...)
	}
	stores := map[string]objectstore.Name{}
	for _, o := range got {
		stores[o.Key] = o.Store
	}
	want := map[string]objectstore.Name{
		"k/0": objectstore.RunLogs, "k/1": objectstore.RunLogs,
		report.LogKey(acct, id, kernel.RunStatePlanning, 3): objectstore.RunLogs,
		"ws-3": objectstore.Workspaces,
		"ws-5": objectstore.Workspaces, "plan": objectstore.RunArtifacts,
		"trace": objectstore.RunObservability,
	}
	if len(got) != len(want) {
		t.Errorf("purged %+v", got)
	}
	for k, store := range want {
		if stores[k] != store {
			t.Errorf("purged %s from %q, want %q", k, stores[k], store)
		}
	}
	if n := count(t, db, "run_log_chunks", other); n != 1 {
		t.Errorf("another account keeps %d chunks", n)
	}

	db.Close()
	for name, err := range map[string]error{
		"StateAt":    func() error { _, err := s.StateAt(ctx, acct, id, 1); return err }(),
		"Indexed":    func() error { _, err := s.Indexed(ctx, run, 1, 0); return err }(),
		"IndexChunk": s.IndexChunk(ctx, run, report.Chunk{FirstAt: at, LastAt: at}),
		"Recorded": func() error {
			_, err := s.Recorded(ctx, run, 1, workersapi.UploadArtifact, "a")
			return err
		}(),
		"Record":       s.Record(ctx, run, report.Artifact{Kind: workersapi.UploadArtifact}),
		"Workspace":    func() error { _, _, err := s.Workspace(ctx, run); return err }(),
		"SetDelta":     s.SetDelta(ctx, run, workersapi.Delta{}),
		"PurgeObjects": func() error { _, err := s.PurgeObjects(context.Background(), acct, 1); return err }(),
	} {
		if err == nil {
			t.Errorf("%s on a closed pool succeeded", name)
		}
	}
}
