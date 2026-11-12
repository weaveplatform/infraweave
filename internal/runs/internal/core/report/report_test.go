package report_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/report"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

var errDown = errors.New("down")

// store holds one run, the states it entered by state version, its
// indexed chunks and recorded objects; the named method in fail fails.
type store struct {
	run      lifecycle.Run
	states   map[int64]kernel.RunState
	chunks   map[int64]report.Chunk
	recorded map[string]report.Artifact
	purge    [][]report.Object
	fail     string
}

func (s *store) err(m string) error {
	if s.fail == m {
		return errDown
	}
	return nil
}

func (s *store) Handed(_ context.Context, id kernel.ULID) (lifecycle.Run, error) {
	if err := s.err("Handed"); err != nil {
		return lifecycle.Run{}, err
	}
	if id != s.run.ID {
		return lifecycle.Run{}, lifecycle.ErrNotFound
	}
	return s.run, nil
}

func (s *store) StateAt(_ context.Context, _ kernel.AccountID, _ kernel.ULID,
	v int64,
) (kernel.RunState, error) {
	return s.states[v], s.err("StateAt")
}

func (s *store) ArtifactKey(context.Context, lifecycle.Run) (crypt.Value, error) {
	return crypt.NewValue(bytes.Repeat([]byte{5}, 32)), s.err("ArtifactKey")
}

func (s *store) Indexed(_ context.Context, _ lifecycle.Run, _, seq int64) (bool, error) {
	_, ok := s.chunks[seq]
	return ok, s.err("Indexed")
}

func (s *store) IndexChunk(_ context.Context, _ lifecycle.Run, c report.Chunk) error {
	if err := s.err("IndexChunk"); err != nil {
		return err
	}
	s.chunks[c.Seq] = c
	return nil
}

func (s *store) Recorded(_ context.Context, _ lifecycle.Run, _ int64, kind workersapi.UploadKind,
	name string,
) (bool, error) {
	_, ok := s.recorded[string(kind)+"/"+name]
	return ok, s.err("Recorded")
}

func (s *store) Record(_ context.Context, _ lifecycle.Run, a report.Artifact) error {
	if err := s.err("Record"); err != nil {
		return err
	}
	s.recorded[string(a.Kind)+"/"+a.Name] = a
	return nil
}

func (s *store) Workspace(context.Context, lifecycle.Run) (string, bool, error) {
	a, ok := s.recorded["workspace/ws"]
	return a.Key, ok, s.err("Workspace")
}

func (s *store) PurgeObjects(context.Context, kernel.AccountID, int) ([]report.Object, error) {
	if err := s.err("PurgeObjects"); err != nil || len(s.purge) == 0 {
		return nil, err
	}
	next := s.purge[0]
	s.purge = s.purge[1:]
	return next, nil
}

// objects records what it was asked; uploaded holds the size and
// SHA-256 of each key's upload, and the named method in fail fails with
// failErr, errDown when nil.
type objects struct {
	begun    map[string][]byte
	uploaded map[string]objectstore.Object
	deleted  []string
	fail     string
	failErr  error
}

func (o *objects) err(m string) error {
	if o.fail != m {
		return nil
	}
	if o.failErr != nil {
		return o.failErr
	}
	return errDown
}

func (o *objects) Begin(_ context.Context, s objectstore.Name, k string, _ int64, _ []byte) error {
	if err := o.err("Begin"); err != nil {
		return err
	}
	o.begun[string(s)+"/"+k] = nil
	return nil
}

func (o *objects) BeginSealed(_ context.Context, s objectstore.Name, k string, _ int64,
	_, content []byte,
) error {
	if err := o.err("BeginSealed"); err != nil {
		return err
	}
	o.begun[string(s)+"/"+k] = content
	return nil
}

func (o *objects) SignPut(_ context.Context, s objectstore.Name, k string, _ int64,
	_ []byte,
) (objectstore.Signed, error) {
	h := http.Header{}
	h.Set("X-Amz-Checksum-Sha256", "sum")
	return objectstore.Signed{URL: "https://store.test/" + string(s) + "/" + k, Header: h},
		o.err("SignPut")
}

func (o *objects) SignGet(_ context.Context, s objectstore.Name,
	k string,
) (objectstore.Signed, error) {
	return objectstore.Signed{URL: "https://store.test/" + string(s) + "/" + k}, o.err("SignGet")
}

func (o *objects) CommitUpload(_ context.Context, s objectstore.Name,
	k string,
) (objectstore.Object, error) {
	if err := o.err("CommitUpload"); err != nil {
		return objectstore.Object{}, err
	}
	obj, ok := o.uploaded[string(s)+"/"+k]
	if !ok {
		return obj, blobs.ErrNotUploaded
	}
	return obj, nil
}

func (o *objects) Delete(_ context.Context, s objectstore.Name, k string) error {
	o.deleted = append(o.deleted, string(s)+"/"+k)
	return o.err("Delete")
}

// mover moves the store's run as the lifecycle would, recording each
// request; fail fails it.
type mover struct {
	s    *store
	reqs []lifecycle.Request
	fail bool
}

func (m *mover) Transition(_ context.Context, req lifecycle.Request) (lifecycle.Run, error) {
	m.reqs = append(m.reqs, req)
	if m.fail {
		return lifecycle.Run{}, errDown
	}
	m.s.run.State, m.s.run.Version = req.To, req.Version+1
	m.s.states[m.s.run.Version] = req.To
	return m.s.run, nil
}

type (
	db     struct{}
	fakeTx struct{ pgx.Tx }
)

func (db) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return fakeTx{}, nil }
func (fakeTx) Commit(context.Context) error                       { return nil }
func (fakeTx) Rollback(context.Context) error                     { return nil }

type world struct {
	s   *store
	o   *objects
	m   *mover
	svc *report.Service
}

// newWorld holds a run of type typ, on a private pool, at version 2 in
// state, which it entered from READY.
func newWorld(typ kernel.RunType, state kernel.RunState) *world {
	pool := kernel.NewULID()
	s := &store{
		run: lifecycle.Run{
			Account: kernel.NewAccountID(), ID: kernel.NewULID(), Type: typ, State: state,
			Version: 2, WorkerPool: &pool,
		},
		states:   map[int64]kernel.RunState{1: kernel.RunStateReady, 2: state},
		chunks:   map[int64]report.Chunk{},
		recorded: map[string]report.Artifact{},
	}
	o := &objects{begun: map[string][]byte{}, uploaded: map[string]objectstore.Object{}}
	m := &mover{s: s}
	return &world{s: s, o: o, m: m, svc: report.New(report.Deps{
		DB: db{}, Store: s, Objects: o, Lifecycle: m,
	})}
}

// in runs fn in a transaction.
func in(t *testing.T, fn func(ctx context.Context) error) error {
	t.Helper()
	return tx.Do(t.Context(), db{}, tx.Options{}, fn)
}

func (w *world) report(t *testing.T, v int64, state kernel.RunState,
	ev workersapi.ReportEvent,
) (workersapi.Step, error) {
	t.Helper()
	var out workersapi.Step
	err := in(t, func(ctx context.Context) error {
		var err error
		out, err = w.svc.Report(ctx, w.s.run.ID, workersapi.Report{
			StateVersion: v, State: state, Event: ev, Details: "exit code 0",
		})
		return err
	})
	return out, err
}

func want(t *testing.T, got workersapi.Step, err error, a workersapi.Action,
	s kernel.RunState, v int64,
) {
	t.Helper()
	if err != nil || got != (workersapi.Step{Action: a, State: s, StateVersion: v}) {
		t.Errorf("step %+v, %v, want %s %s at %d", got, err, a, s, v)
	}
}

// TestUnitReport walks a tracked run from its handover: the first started
// report ends the handover, its repeat answers alike, a started report
// of the state the run is in changes nothing, a succeeded report enters
// the next phase, one of PLANNING needs the plan decision, and a failed
// report ends the run; a report one behind is answered as it was only
// when it took the run's last transition, and an older one is refused.
func TestUnitReport(t *testing.T) {
	w := newWorld(kernel.RunTypeTracked, kernel.RunStatePreparing)
	const (
		started   = workersapi.ReportStarted
		succeeded = workersapi.ReportSucceeded
		failed    = workersapi.ReportFailed
	)
	exec := workersapi.ActionExecute
	got, err := w.report(t, 2, kernel.RunStateInitializing, started)
	want(t, got, err, exec, kernel.RunStateInitializing, 3)
	if len(w.m.reqs) != 1 || w.m.reqs[0].Driver != machine.DriverWorkerReport ||
		w.m.reqs[0].Note != "exit code 0" || w.m.reqs[0].Version != 2 {
		t.Errorf("the handover moved %+v", w.m.reqs)
	}
	got, err = w.report(t, 2, kernel.RunStateInitializing, started)
	want(t, got, err, exec, kernel.RunStateInitializing, 3)
	got, err = w.report(t, 3, kernel.RunStateInitializing, started)
	want(t, got, err, exec, kernel.RunStateInitializing, 3)
	if len(w.m.reqs) != 1 {
		t.Errorf("a repeated started report moved the run: %+v", w.m.reqs)
	}

	got, err = w.report(t, 3, kernel.RunStateInitializing, succeeded)
	want(t, got, err, exec, kernel.RunStatePlanning, 4)
	got, err = w.report(t, 3, kernel.RunStateInitializing, succeeded)
	want(t, got, err, exec, kernel.RunStatePlanning, 4)
	for name, tc := range map[string]struct {
		v     int64
		state kernel.RunState
		ev    workersapi.ReportEvent
		err   error
	}{
		"plan decision":     {4, kernel.RunStatePlanning, succeeded, workersapi.ErrNoPlanDecision},
		"another state":     {4, kernel.RunStateApplying, failed, workersapi.ErrReportRefused},
		"started handover":  {4, kernel.RunStateApplying, started, workersapi.ErrReportRefused},
		"one behind, other": {3, kernel.RunStateInitializing, failed, workersapi.ErrMovedOn},
		"two behind":        {2, kernel.RunStateInitializing, started, workersapi.ErrMovedOn},
		"ahead":             {5, kernel.RunStatePlanning, failed, workersapi.ErrMovedOn},
	} {
		if _, err := w.report(t, tc.v, tc.state, tc.ev); !errors.Is(err, tc.err) {
			t.Errorf("%s: %v, want %v", name, err, tc.err)
		}
	}
	got, err = w.report(t, 4, kernel.RunStatePlanning, failed)
	want(t, got, err, workersapi.ActionExit, kernel.RunStateFailed, 5)
	if _, err := w.report(t, 4, kernel.RunStatePlanning, failed); !errors.Is(err,
		workersapi.ErrNotOnWorker) {
		t.Errorf("a report on an ended run: %v", err)
	}
	if len(w.m.reqs) != 3 {
		t.Errorf("moves %+v", w.m.reqs)
	}
}

// TestUnitReportEdges checks the edge each run type's reports take out of
// each state a worker holds it in.
func TestUnitReportEdges(t *testing.T) {
	for _, tc := range []struct {
		typ      kernel.RunType
		from     kernel.RunState
		state    kernel.RunState
		ev       workersapi.ReportEvent
		to       kernel.RunState
		action   workersapi.Action
		refusals error
	}{
		{
			kernel.RunTypeTracked, kernel.RunStatePreparingApply, kernel.RunStateApplying,
			workersapi.ReportStarted, kernel.RunStateApplying, workersapi.ActionExecute, nil,
		},
		{
			kernel.RunTypeTracked, kernel.RunStatePreparingReplan, kernel.RunStatePlanning,
			workersapi.ReportStarted, kernel.RunStatePlanning, workersapi.ActionExecute, nil,
		},
		{
			kernel.RunTypeTracked, kernel.RunStatePreparing, kernel.RunStatePreparing,
			workersapi.ReportFailed, kernel.RunStateFailed, workersapi.ActionExit, nil,
		},
		{
			kernel.RunTypeTask, kernel.RunStateInitializing, kernel.RunStateInitializing,
			workersapi.ReportSucceeded, kernel.RunStatePerforming, workersapi.ActionExecute, nil,
		},
		{
			kernel.RunTypeDestroy, kernel.RunStateInitializing, kernel.RunStateInitializing,
			workersapi.ReportSucceeded, kernel.RunStateDestroying, workersapi.ActionExecute, nil,
		},
		{
			kernel.RunTypeTesting, kernel.RunStateApplying, kernel.RunStateApplying,
			workersapi.ReportSucceeded, kernel.RunStateDestroying, workersapi.ActionExecute, nil,
		},
		{
			kernel.RunTypeTracked, kernel.RunStateApplying, kernel.RunStateApplying,
			workersapi.ReportSucceeded, kernel.RunStateFinished, workersapi.ActionExit, nil,
		},
		{
			kernel.RunTypeTask, kernel.RunStatePerforming, kernel.RunStatePerforming,
			workersapi.ReportSucceeded, kernel.RunStateFinished, workersapi.ActionExit, nil,
		},
		{
			kernel.RunTypeProposed, kernel.RunStateInitializing, kernel.RunStateInitializing,
			workersapi.ReportStopped, kernel.RunStateStopped, workersapi.ActionExit, nil,
		},
		{
			kernel.RunTypeTracked, kernel.RunStateApplying, kernel.RunStateApplying,
			workersapi.ReportStopped, "", "", workersapi.ErrReportRefused,
		},
		{
			kernel.RunTypeTracked, kernel.RunStatePreparing, kernel.RunStatePreparing,
			workersapi.ReportSucceeded, "", "", workersapi.ErrReportRefused,
		},
		{
			kernel.RunTypeTracked, kernel.RunStatePreparing, kernel.RunStatePlanning,
			workersapi.ReportStarted, "", "", workersapi.ErrReportRefused,
		},
	} {
		w := newWorld(tc.typ, tc.from)
		got, err := w.report(t, 2, tc.state, tc.ev)
		if tc.refusals != nil {
			if !errors.Is(err, tc.refusals) {
				t.Errorf("%s %s %s from %s: %v", tc.typ, tc.state, tc.ev, tc.from, err)
			}
			continue
		}
		want(t, got, err, tc.action, tc.to, 3)
	}
	if s := report.StepOf(lifecycle.Run{State: kernel.RunStateUnconfirmed, Version: 7}); s !=
		(workersapi.Step{
			Action: workersapi.ActionPause, State: kernel.RunStateUnconfirmed,
			StateVersion: 7,
		}) {
		t.Errorf("a run awaiting confirmation: %+v", s)
	}
}

// TestUnitReportRefused checks the refusals before a report reads the
// run's state: no transaction, no such run or one not on a private
// pool's worker, and the store's and the lifecycle's failures.
func TestUnitReportRefused(t *testing.T) {
	w := newWorld(kernel.RunTypeTracked, kernel.RunStatePreparing)
	if _, err := w.svc.Report(t.Context(), w.s.run.ID, workersapi.Report{}); !errors.Is(err,
		lifecycle.ErrNoTransaction) {
		t.Errorf("outside a transaction: %v", err)
	}
	other := in(t, func(ctx context.Context) error {
		_, err := w.svc.Report(ctx, kernel.NewULID(), workersapi.Report{})
		return err
	})
	if !errors.Is(other, workersapi.ErrNotOnWorker) {
		t.Errorf("no such run: %v", other)
	}
	w.s.run.WorkerPool = nil
	if _, err := w.report(t, 2, kernel.RunStateInitializing,
		workersapi.ReportStarted); !errors.Is(err, workersapi.ErrNotOnWorker) {
		t.Errorf("a public pool's run: %v", err)
	}
	for _, fail := range []string{"Handed", "StateAt", "lifecycle"} {
		w := newWorld(kernel.RunTypeTracked, kernel.RunStatePreparing)
		w.s.fail, w.m.fail = fail, fail == "lifecycle"
		v := int64(2)
		if fail == "StateAt" {
			v = 1
		}
		if _, err := w.report(t, v, kernel.RunStateInitializing,
			workersapi.ReportStarted); !errors.Is(err, errDown) {
			t.Errorf("%s fails: %v", fail, err)
		}
	}
}

func sum(b byte) []byte { return bytes.Repeat([]byte{b}, sha256.Size) }

// uploads signs ups at version v.
func (w *world) uploads(t *testing.T, v int64,
	ups ...workersapi.Upload,
) ([]workersapi.SignedUpload, error) {
	t.Helper()
	var out []workersapi.SignedUpload
	err := in(t, func(ctx context.Context) error {
		var err error
		out, err = w.svc.Uploads(ctx, w.s.run.ID, v, ups)
		return err
	})
	return out, err
}

// TestUnitUploads checks that each upload is keyed under the run's
// prefix in its kind's store, that an artifact's row holds the content
// key derived from the run's artifact key and its key, and the
// declarations, names and failures uploads refuse.
func TestUnitUploads(t *testing.T) {
	w := newWorld(kernel.RunTypeTracked, kernel.RunStatePlanning)
	prefix := w.s.run.Account.String() + "/" + w.s.run.ID.String() + "/PLANNING-2/"
	got, err := w.uploads(t, 2,
		workersapi.Upload{Kind: workersapi.UploadLogChunk, Name: "0", Size: 10, SHA256: sum(1)},
		workersapi.Upload{
			Kind: workersapi.UploadArtifact, Name: "plan.json", Size: 512 << 20,
			SHA256: sum(2),
		},
		workersapi.Upload{
			Kind: workersapi.UploadWorkspace, Name: "ws", Size: 2 << 30,
			SHA256: sum(3),
		},
		workersapi.Upload{
			Kind: workersapi.UploadObservability, Name: "trace.json", Size: 1,
			SHA256: sum(4),
		},
	)
	if err != nil || len(got) != 4 {
		t.Fatalf("Uploads = %+v, %v", got, err)
	}
	for i, store := range []string{"run-logs", "run-artifacts", "workspaces", "run-observability"} {
		u := got[i]
		if !strings.HasPrefix(u.Key, prefix) || u.URL != "https://store.test/"+store+"/"+u.Key ||
			u.Header["X-Amz-Checksum-Sha256"] != "sum" {
			t.Errorf("upload %d: %+v", i, u)
		}
		if _, ok := w.o.begun[store+"/"+u.Key]; !ok {
			t.Errorf("upload %d is not pending in %s", i, store)
		}
	}
	content, _ := seal.ArtifactKey(bytes.Repeat([]byte{5}, 32), prefix+"plan.json")
	if !bytes.Equal(w.o.begun["run-artifacts/"+prefix+"plan.json"], content) {
		t.Error("the artifact's content key is not derived from its key")
	}

	_, err = w.uploads(t, 2,
		workersapi.Upload{Kind: "other", Name: "x", SHA256: sum(1)},
		workersapi.Upload{Kind: workersapi.UploadLogChunk, Name: "01", SHA256: sum(1)},
		workersapi.Upload{Kind: workersapi.UploadArtifact, Name: "..", SHA256: sum(1)},
		workersapi.Upload{
			Kind: workersapi.UploadLogChunk, Name: "1", Size: 1<<20 + 1,
			SHA256: sum(1),
		},
		workersapi.Upload{Kind: workersapi.UploadWorkspace, Name: "w", SHA256: sum(1)[:31]},
	)
	inv, ok := errors.AsType[*kernel.Invalid](err)
	if !ok || len(inv.Errors) != 5 || inv.Errors[3].Pointer != "/uploads/3/size" {
		t.Errorf("refused declarations: %v", err)
	}
	if _, err := w.uploads(t, 1); !errors.Is(err, workersapi.ErrMovedOn) {
		t.Errorf("an older state version: %v", err)
	}
	log := workersapi.Upload{Kind: workersapi.UploadLogChunk, Name: "7", SHA256: sum(1)}
	art := workersapi.Upload{Kind: workersapi.UploadArtifact, Name: "a", SHA256: sum(1)}
	for _, tc := range []struct {
		fail    string
		failErr error
		u       workersapi.Upload
		want    error
	}{
		{"Begin", blobs.ErrKeyInUse, log, workersapi.ErrUploadExists},
		{"Begin", nil, log, errDown},
		{"BeginSealed", nil, art, errDown},
		{"SignPut", nil, log, errDown},
		{"ArtifactKey", nil, art, errDown},
	} {
		w := newWorld(kernel.RunTypeTracked, kernel.RunStatePlanning)
		w.o.fail, w.o.failErr, w.s.fail = tc.fail, tc.failErr, tc.fail
		if _, err := w.uploads(t, 2, tc.u); !errors.Is(err, tc.want) {
			t.Errorf("%s fails: %v, want %v", tc.fail, err, tc.want)
		}
	}
}

// TestUnitLogChunks checks that a chunk is committed and indexed with its
// upload's size, once, and the chunks and failures it refuses.
func TestUnitLogChunks(t *testing.T) {
	w := newWorld(kernel.RunTypeTracked, kernel.RunStatePlanning)
	prefix := w.s.run.Account.String() + "/" + w.s.run.ID.String() + "/PLANNING-2/"
	w.o.uploaded["run-logs/"+prefix+"0"] = objectstore.Object{Size: 42}
	at := time.Unix(1_800_000_000, 0)
	chunks := func(v int64, c ...workersapi.LogChunk) error {
		return in(t, func(ctx context.Context) error {
			return w.svc.LogChunks(ctx, w.s.run.ID, v, c)
		})
	}
	zero := workersapi.LogChunk{Seq: 0, FirstAt: at, LastAt: at.Add(time.Second)}
	if err := chunks(2, zero, zero); err != nil {
		t.Fatal(err)
	}
	if c := w.s.chunks[0]; c.Bytes != 42 || c.Key != prefix+"0" || c.Version != 2 ||
		c.State != kernel.RunStatePlanning || !c.LastAt.Equal(at.Add(time.Second)) {
		t.Errorf("indexed %+v", c)
	}
	if err := chunks(2, zero); err != nil {
		t.Errorf("a chunk indexed again: %v", err)
	}
	for name, tc := range map[string]struct {
		c    workersapi.LogChunk
		fail string
		want error
	}{
		"not uploaded": {workersapi.LogChunk{Seq: 1, FirstAt: at, LastAt: at}, "", nil},
		"backwards": {
			workersapi.LogChunk{Seq: 2, FirstAt: at, LastAt: at.Add(-time.Second)},
			"", nil,
		},
		"commit":  {workersapi.LogChunk{Seq: 3}, "CommitUpload", errDown},
		"indexed": {workersapi.LogChunk{Seq: 4}, "Indexed", errDown},
		"index":   {zero, "IndexChunk", errDown},
	} {
		w.o.fail, w.s.fail = tc.fail, tc.fail
		if name == "index" {
			delete(w.s.chunks, 0)
		}
		err := chunks(2, tc.c)
		if tc.want == nil {
			if _, ok := errors.AsType[*kernel.Invalid](err); !ok {
				t.Errorf("%s: %v, want a refused chunk", name, err)
			}
			continue
		}
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := chunks(3, zero); !errors.Is(err, workersapi.ErrMovedOn) {
		t.Errorf("a newer state version: %v", err)
	}
}

// TestUnitArtifacts checks that an upload is committed and recorded with
// its size and SHA-256, once; that the latest workspace is signed for a
// GET; and the records, failures and workspaces it refuses.
func TestUnitArtifacts(t *testing.T) {
	w := newWorld(kernel.RunTypeTracked, kernel.RunStatePlanning)
	prefix := w.s.run.Account.String() + "/" + w.s.run.ID.String() + "/PLANNING-2/"
	w.o.uploaded["run-artifacts/"+prefix+"plan.json"] = objectstore.Object{
		Size:   9,
		SHA256: [32]byte{1},
	}
	w.o.uploaded["workspaces/"+prefix+"ws"] = objectstore.Object{Size: 3}
	record := func(r ...workersapi.Recorded) error {
		return in(t, func(ctx context.Context) error {
			return w.svc.Artifacts(ctx, w.s.run.ID, 2, r)
		})
	}
	workspace := func() (string, error) {
		var url string
		err := in(t, func(ctx context.Context) error {
			var err error
			url, err = w.svc.Workspace(ctx, w.s.run.ID)
			return err
		})
		return url, err
	}
	if _, err := workspace(); !errors.Is(err, workersapi.ErrNoWorkspace) {
		t.Errorf("no workspace: %v", err)
	}
	plan := workersapi.Recorded{Kind: workersapi.UploadArtifact, Name: "plan.json"}
	ws := workersapi.Recorded{Kind: workersapi.UploadWorkspace, Name: "ws"}
	if err := record(plan, ws, plan); err != nil {
		t.Fatal(err)
	}
	if a := w.s.recorded["artifact/plan.json"]; a.Size != 9 || a.SHA256 != [32]byte{1} ||
		a.Key != prefix+"plan.json" || a.Version != 2 {
		t.Errorf("recorded %+v", a)
	}
	if url, err := workspace(); err != nil || url != "https://store.test/workspaces/"+prefix+"ws" {
		t.Errorf("Workspace = %q, %v", url, err)
	}
	for name, tc := range map[string]struct {
		r    workersapi.Recorded
		fail string
		want error
	}{
		"log chunk":    {workersapi.Recorded{Kind: workersapi.UploadLogChunk, Name: "0"}, "", nil},
		"bad name":     {workersapi.Recorded{Kind: workersapi.UploadArtifact, Name: "a/b"}, "", nil},
		"not uploaded": {workersapi.Recorded{Kind: workersapi.UploadArtifact, Name: "x"}, "", nil},
		"recorded":     {plan, "Recorded", errDown},
		"commit":       {workersapi.Recorded{Kind: workersapi.UploadArtifact, Name: "y"}, "CommitUpload", errDown},
		"record":       {ws, "Record", errDown},
	} {
		w.o.fail, w.s.fail = tc.fail, tc.fail
		if name == "record" {
			delete(w.s.recorded, "workspace/ws")
		}
		err := record(tc.r)
		if tc.want == nil {
			if _, ok := errors.AsType[*kernel.Invalid](err); !ok {
				t.Errorf("%s: %v, want a refused record", name, err)
			}
			continue
		}
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	w.o.fail, w.s.fail = "", ""
	if err := record(ws); err != nil {
		t.Fatal(err)
	}
	for fail, want := range map[string]error{
		"SignGet": errDown, "Workspace": errDown, "expired": workersapi.ErrNoWorkspace,
	} {
		w.o.fail, w.s.fail, w.o.failErr = fail, fail, nil
		if fail == "expired" {
			w.o.fail, w.o.failErr = "SignGet", objectstore.ErrNotFound
		}
		if _, err := workspace(); !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", fail, err, want)
		}
	}
	w.s.run.State = kernel.RunStateFinished
	if _, err := workspace(); !errors.Is(err, workersapi.ErrNotOnWorker) {
		t.Errorf("an ended run's workspace: %v", err)
	}
	if err := record(ws); !errors.Is(err, workersapi.ErrNotOnWorker) {
		t.Errorf("an ended run's artifacts: %v", err)
	}
}

// TestUnitPurgeObjects checks that a purge deletes each object its
// batch's rows recorded and reports done once none is left, and that a
// failure of either stops it.
func TestUnitPurgeObjects(t *testing.T) {
	w := newWorld(kernel.RunTypeTracked, kernel.RunStatePlanning)
	w.s.purge = [][]report.Object{
		{
			{Store: objectstore.RunLogs, Key: "a/r/P-1/0"},
			{Store: objectstore.RunLogs, Key: "a/r/P-1/1"},
		},
		{{Store: objectstore.Workspaces, Key: "a/r/P-1/ws"}},
	}
	acct := w.s.run.Account
	for i, want := range []bool{false, false, true} {
		if done, err := w.svc.PurgeObjects(t.Context(), acct, 2); err != nil || done != want {
			t.Fatalf("batch %d: done %t, %v", i, done, err)
		}
	}
	if strings.Join(
		w.o.deleted,
		" ",
	) != "run-logs/a/r/P-1/0 run-logs/a/r/P-1/1 workspaces/a/r/P-1/ws" {
		t.Errorf("deleted %v", w.o.deleted)
	}
	w.s.purge = [][]report.Object{{{Store: objectstore.RunLogs, Key: "k"}}}
	w.o.fail = "Delete"
	if _, err := w.svc.PurgeObjects(t.Context(), acct, 2); !errors.Is(err, errDown) {
		t.Errorf("a failed delete: %v", err)
	}
	w.s.fail = "PurgeObjects"
	if _, err := w.svc.PurgeObjects(t.Context(), acct, 2); !errors.Is(err, errDown) {
		t.Errorf("a failed purge: %v", err)
	}
}
