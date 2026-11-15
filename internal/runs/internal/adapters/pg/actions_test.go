package pg_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/control"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/report"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// holding is a writer's actions with more.
func holding(more ...accessapi.Action) []accessapi.Action {
	return append(slices.Clone(writer), more...)
}

// canceled is run canceled from QUEUED, which ends it.
func (w *world) canceled(t *testing.T, run lifecycle.Run) lifecycle.Run {
	t.Helper()
	return w.mustMove(t, run, kernel.RunStateCanceled, machine.DriverAPI)
}

// refusedFor checks that err is a *control.Refused that needs needs.
func refusedFor(t *testing.T, what string, err error, needs string) {
	t.Helper()
	var r *control.Refused
	if !errors.As(err, &r) || r.Needs != needs {
		t.Errorf("%s = %v, want a refusal that needs %q", what, err, needs)
	}
}

// note is the note of v's first transition, the one that created it.
func note(v read.View) string {
	if len(v.History) == 0 || v.History[0].Note == nil {
		return ""
	}
	return *v.History[0].Note
}

// TestAccRetry retries an ended run on its commit, with its environment
// variable overrides, as a new run whose first transition names the run
// retried, and answers the run that has not ended in its place; needs
// RUN_RETRY_BLOCKING for a tracked run; and refuses a run that has not
// ended, one whose commit the repository no longer has, and a retry with
// no source bound.
func TestAccRetry(t *testing.T) {
	w := newWorld(t)
	a := w.access()
	a.held = holding(accessapi.ActionRunRetry, accessapi.ActionRunRetryBlocking)
	c := w.controller(a)
	ctx, p := t.Context(), w.alice()
	ended := w.canceled(t, mustCreate(t, w, kernel.RunTypeProposed, "a"))
	if _, err := c.Retry(ctx, p, "network", ended.ID.String()); !errors.Is(err,
		kernel.ErrUnavailable) {
		t.Errorf("Retry with no source bound = %v", err)
	}
	c.BindSources(w.sources)
	w.sources.commits = map[string]stacksapi.Commit{"a": {Hash: "a"}}
	ovs := []lifecycle.Override{{Name: "TF_LOG", Value: "debug"}}
	if err := w.store.SetOverrides(ctx, ended, ovs); err != nil {
		t.Fatal(err)
	}

	v, err := c.Retry(ctx, p, "network", ended.ID.String())
	if err != nil || v.Run.ID == ended.ID || v.Run.Type != kernel.RunTypeProposed ||
		v.Run.State != kernel.RunStateQueued || v.Run.Commit.Hash != "a" ||
		note(v) != "retry of "+ended.ID.String() {
		t.Fatalf("Retry = %+v, %v", v, err)
	}
	if got, err := w.store.Overrides(ctx, v.Run); err != nil || !slices.Equal(got, ovs) {
		t.Errorf("the retry's overrides = %+v, %v", got, err)
	}
	if v.Guards.Retry != "a retry needs a run that has ended" {
		t.Errorf("the retry's guard = %q", v.Guards.Retry)
	}
	_, err = c.Retry(ctx, p, "network", v.Run.ID.String())
	refusedFor(t, "Retry of a queued run", err, "a retry needs a run that has ended")
	gone := w.canceled(t, mustCreate(t, w, kernel.RunTypeProposed, "gone"))
	_, err = c.Retry(ctx, p, "network", gone.ID.String())
	refusedFor(t, "Retry of a commit the repository lacks", err,
		"a retry needs the run's commit, which the stack's repository no longer has")

	tracked := w.canceled(t, mustCreate(t, w, kernel.RunTypeTracked, "a"))
	a.held = holding(accessapi.ActionRunRetry)
	var blocked *control.Blocked
	if _, err := c.Retry(ctx, p, "network", tracked.ID.String()); !errors.As(err, &blocked) ||
		blocked.Action != accessapi.ActionRunRetryBlocking {
		t.Errorf("Retry of a tracked run without RUN_RETRY_BLOCKING = %v", err)
	}
	a.held = holding(accessapi.ActionRunRetry, accessapi.ActionRunRetryBlocking)
	first, err := c.Retry(ctx, p, "network", tracked.ID.String())
	if err != nil || first.Run.Type != kernel.RunTypeTracked {
		t.Errorf("Retry of a tracked run = %+v, %v", first, err)
	}
	if again, err := c.Retry(ctx, p, "network", tracked.ID.String()); err != nil ||
		again.Run.ID != first.Run.ID {
		t.Errorf("a second Retry = %v, %v, want %v", again.Run.ID, err, first.Run.ID)
	}

	a.err["Authorize"] = errInjected
	if _, err := c.Retry(ctx, p, "network", tracked.ID.String()); !errors.Is(err, errInjected) {
		t.Errorf("Retry failing its authorization = %v", err)
	}
	delete(a.err, "Authorize")
	w.db.Close()
	if _, err := c.Retry(ctx, p, "network", ended.ID.String()); err == nil {
		t.Error("Retry on a closed pool succeeded")
	}
}

// TestAccPromote promotes a proposed run's commit, which becomes the
// stack's tracked commit, set by the caller, as a tracked run on the
// stack's branch; refuses a commit no newer than the tracked one, a
// tracked run and a stack that does not allow promotion; and keeps the
// tracked commit when the promotion fails.
func TestAccPromote(t *testing.T) {
	w := newWorld(t)
	st := w.stacks.settings[w.stack]
	st.GithubActionDeploy, st.Branch = true, "main"
	w.stacks.settings[w.stack] = st
	a := w.access()
	a.held = holding(accessapi.ActionRunPromote)
	c := w.controller(a)
	ctx, p := t.Context(), w.alice()

	proposed := mustCreate(t, w, kernel.RunTypeProposed, "a")
	v, err := c.Promote(ctx, p, "network", proposed.ID.String())
	if err != nil || v.Run.Type != kernel.RunTypeTracked || v.Run.Commit.Hash != "a" ||
		v.Run.Commit.Branch != "main" || note(v) != "promotion of "+proposed.ID.String() {
		t.Fatalf("Promote = %+v, %v", v, err)
	}
	if len(w.stacks.tracked) != 1 || w.stacks.tracked[0].commit.Hash != "a" ||
		w.stacks.tracked[0].commit.Branch != "" || w.stacks.tracked[0].setBy != "alice" {
		t.Errorf("the tracked commits set: %+v", w.stacks.tracked)
	}
	_, err = c.Promote(ctx, p, "network", proposed.ID.String())
	refusedFor(t, "a second Promote", err,
		"a promotion needs a commit newer than the stack's tracked commit")
	_, err = c.Promote(ctx, p, "network", v.Run.ID.String())
	refusedFor(t, "Promote of a tracked run", err, "a promotion needs a proposed run")

	w.now = w.now.Add(time.Minute)
	newer := mustCreate(t, w, kernel.RunTypeProposed, "b")
	w.stacks.err["SetTracked"] = errInjected
	if _, err := c.Promote(ctx, p, "network", newer.ID.String()); !errors.Is(err, errInjected) {
		t.Errorf("Promote failing its tracked commit = %v", err)
	}
	delete(w.stacks.err, "SetTracked")
	w.stacks.err["LockForRun"] = errInjected
	if _, err := c.Promote(ctx, p, "network", newer.ID.String()); !errors.Is(err, errInjected) {
		t.Errorf("Promote failing its stack's lock = %v", err)
	}
	delete(w.stacks.err, "LockForRun")
	if len(w.stacks.tracked) != 1 {
		t.Errorf("failed promotions set the tracked commit: %+v", w.stacks.tracked)
	}
	if v, err := c.Promote(ctx, p, "network", newer.ID.String()); err != nil ||
		v.Run.Commit.Hash != "b" || len(w.stacks.tracked) != 2 {
		t.Errorf("Promote of a newer commit = %+v, %v", v, err)
	}

	w.now = w.now.Add(time.Minute)
	st.GithubActionDeploy = false
	w.stacks.settings[w.stack] = st
	_, err = c.Promote(ctx, p, "network", mustCreate(t, w, kernel.RunTypeProposed, "c").ID.String())
	refusedFor(t, "Promote on a stack that does not allow it", err,
		"a promotion needs a stack that allows run promotion")
}

// racer is a store whose Discardable runs moved after its read, as a
// change another caller commits between DiscardAll's read and its
// discards does; fail fails its Lock.
type racer struct {
	*pg.Store
	moved func()
	fail  bool
}

func (r racer) Discardable(
	ctx context.Context,
	acct kernel.AccountID,
	stack kernel.ULID,
) ([]lifecycle.Run, error) {
	runs, err := r.Store.Discardable(ctx, acct, stack)
	r.moved()
	return runs, err
}

func (r racer) Lock(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (lifecycle.Run,
	error,
) {
	if r.fail {
		return lifecycle.Run{}, errInjected
	}
	return r.Store.Lock(ctx, acct, id)
}

// TestAccDiscardAll discards the stack's runs that wait, oldest first,
// with the note, which releases the run lock the unconfirmed run holds,
// and leaves a run that has ended; answers a run another change moved on
// first as failed, and discards the rest; and refuses a stack that does
// not exist.
func TestAccDiscardAll(t *testing.T) {
	w := newWorld(t)
	a := w.access()
	c := w.controller(a)
	ctx, p := t.Context(), w.alice()
	queued := mustCreate(t, w, kernel.RunTypeProposed, "a")
	w.now = w.now.Add(time.Second)
	unconfirmed := w.unconfirmed(t, "b")
	w.now = w.now.Add(time.Second)
	ready := w.mustMove(t, mustCreate(t, w, kernel.RunTypeProposed, "c"), kernel.RunStateReady,
		machine.DriverScheduler, machine.GuardStackLockFree, machine.GuardApproved,
		machine.GuardUpstreamFinished, machine.GuardExternalSettled, machine.GuardStackEnabled)
	ended := w.canceled(t, mustCreate(t, w, kernel.RunTypeProposed, "d"))

	got, err := c.DiscardAll(ctx, p, "network", "a clean slate")
	if want := []kernel.ULID{queued.ID, unconfirmed.ID, ready.ID}; err != nil ||
		!slices.Equal(got.Runs, want) || len(got.Failed) != 0 {
		t.Fatalf("DiscardAll = %+v, %v, want %v", got, err, want)
	}
	for _, id := range got.Runs {
		steps := w.steps(t, id)
		if last := steps[len(steps)-1]; last.State != string(kernel.RunStateDiscarded) ||
			last.Note == nil || *last.Note != "a clean slate" {
			t.Errorf("run %s's last transition = %+v", id, last)
		}
	}
	if steps := w.steps(t, ended.ID); steps[len(steps)-1].State != string(kernel.RunStateCanceled) {
		t.Errorf("the ended run moved: %+v", steps)
	}
	if _, held, err := w.store.Holder(ctx, w.acct, w.stack); err != nil || held {
		t.Errorf("the lock after the discards: held %v, %v", held, err)
	}
	if got, err := c.DiscardAll(ctx, p, "network", ""); err != nil || len(got.Runs) != 0 {
		t.Errorf("DiscardAll with nothing waiting = %+v, %v", got, err)
	}

	first := mustCreate(t, w, kernel.RunTypeProposed, "e")
	w.now = w.now.Add(time.Second)
	second := mustCreate(t, w, kernel.RunTypeProposed, "f")
	moving := racer{Store: w.store, moved: func() { w.canceled(t, first) }}
	c = control.New(control.Deps{
		DB: w.db, Store: moving, Stacks: w.stacks, Authz: a, Lifecycle: w.lc,
		Views: w.reader(a), Now: func() time.Time { return w.now },
	})
	if got, err := c.DiscardAll(ctx, p, "network", ""); err != nil ||
		!slices.Equal(got.Runs, []kernel.ULID{second.ID}) ||
		!slices.Equal(got.Failed, []kernel.ULID{first.ID}) {
		t.Errorf("DiscardAll racing a cancel = %+v, %v", got, err)
	}

	mustCreate(t, w, kernel.RunTypeProposed, "g")
	moving.moved, moving.fail = func() {}, true
	c = control.New(control.Deps{
		DB: w.db, Store: moving, Stacks: w.stacks, Authz: a,
		Lifecycle: w.lc, Views: w.reader(a),
	})
	if _, err := c.DiscardAll(ctx, p, "network", ""); !errors.Is(err, errInjected) {
		t.Errorf("DiscardAll failing a run's lock = %v", err)
	}
	if _, err := c.DiscardAll(ctx, p, "gone", ""); !errors.Is(err, stacksapi.ErrNotFound) {
		t.Errorf("DiscardAll on no stack = %v", err)
	}
	w.db.Close()
	if _, err := c.DiscardAll(ctx, p, "network", ""); err == nil {
		t.Error("DiscardAll on a closed pool succeeded")
	}
	if _, err := w.store.Discardable(ctx, w.acct, w.stack); err == nil {
		t.Error("the store's Discardable on a closed pool succeeded")
	}
}

// TestAccComment records comments on a run, which its read answers oldest
// first with their author; counts a body's characters, not its bytes,
// against MaxComment; refuses an empty body, a longer one and a run that
// does not exist; and an account's purge deletes them.
func TestAccComment(t *testing.T) {
	w := newWorld(t)
	a := w.access()
	c := w.controller(a)
	ctx, p := t.Context(), w.alice()
	run := mustCreate(t, w, kernel.RunTypeProposed, "a")
	bodies := []string{"Looks good.", strings.Repeat("é", control.MaxComment)}
	want := make([]read.Comment, 0, len(bodies))
	for _, body := range bodies {
		got, err := c.Comment(ctx, p, "network", run.ID.String(), body)
		if err != nil || got.Body != body || got.Username != "alice" ||
			!got.At.Equal(w.now.Truncate(time.Microsecond)) {
			t.Fatalf("Comment = %+v, %v", got, err)
		}
		want = append(want, got)
		w.now = w.now.Add(time.Second)
	}
	v, err := w.reader(a).Run(ctx, p, run.ID.String())
	if err != nil || !slices.Equal(v.Comments, want) {
		t.Errorf("the run's comments = %+v, %v, want %+v", v.Comments, err, want)
	}

	for body, reason := range map[string]string{
		"": kernel.ReasonMinLength, strings.Repeat("é", control.MaxComment+1): kernel.ReasonMaxLength,
	} {
		var invalid *kernel.Invalid
		if _, err := c.Comment(ctx, p, "network", run.ID.String(), body); !errors.As(
			err,
			&invalid,
		) || invalid.Errors[0].Pointer != "/body" || invalid.Errors[0].Reason != reason {
			t.Errorf("Comment of %d characters = %v", len([]rune(body)), err)
		}
	}
	if _, err := c.Comment(ctx, p, "network", kernel.NewULID().String(), "?"); !errors.Is(err,
		read.ErrNotFound) {
		t.Errorf("Comment on no run = %v", err)
	}

	for done := false; !done; {
		if done, err = w.store.Purge(ctx, w.acct, 100); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, w.db, "run_comments", w.acct); n != 0 {
		t.Errorf("%d comments are left after the purge", n)
	}
	w.db.Close()
	if _, err := c.Comment(ctx, p, "network", run.ID.String(), "?"); err == nil {
		t.Error("Comment on a closed pool succeeded")
	}
	if err := w.store.Comment(ctx, run, want[0]); err == nil {
		t.Error("the store's Comment on a closed pool succeeded")
	}
	if _, err := w.store.Comments(ctx, w.acct, []kernel.ULID{run.ID}); err == nil {
		t.Error("the store's Comments on a closed pool succeeded")
	}
}

// TestAccDeleteLogs deletes a run's log chunks and observability records,
// answers their objects with each state version's compacted log once, and
// marks the run's logs deleted; and leaves its other artifacts and
// another run's logs.
func TestAccDeleteLogs(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db, nil)
	acct := kernel.NewAccountID()
	id, other := insertRun(t, db, acct, kernel.NewULID(), false),
		insertRun(t, db, acct, kernel.NewULID(), false)
	run, err := s.Handed(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := s.Handed(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_800_000_000, 0).UTC()
	for _, c := range []struct {
		run   lifecycle.Run
		chunk report.Chunk
	}{
		{run, report.Chunk{Version: 2, State: kernel.RunStatePlanning, Seq: 0, Key: "plan-0"}},
		{run, report.Chunk{Version: 2, State: kernel.RunStatePlanning, Seq: 1, Key: "plan-1"}},
		{run, report.Chunk{Version: 3, State: kernel.RunStateApplying, Seq: 0, Key: "apply-0"}},
		{theirs, report.Chunk{Version: 2, State: kernel.RunStatePlanning, Seq: 0, Key: "theirs"}},
	} {
		c.chunk.FirstAt, c.chunk.LastAt = at, at
		if err := s.IndexChunk(ctx, c.run, c.chunk); err != nil {
			t.Fatal(err)
		}
	}
	for kind, key := range map[string]string{"observability": "trace", "artifact": "plan"} {
		if _, err := db.Exec(ctx, `INSERT INTO run_artifacts
			(account_id, run_id, state_version, kind, name, object_key, size, sha256)
			VALUES ($1, $2, 3, $3, $4, $4, 1, '\x00')`, acct, id, kind, key); err != nil {
			t.Fatal(err)
		}
	}

	objs, err := s.DeleteLogs(ctx, run, at)
	slices.SortFunc(objs, func(a, b report.Object) int { return strings.Compare(a.Key, b.Key) })
	want := []report.Object{
		{Store: objectstore.RunLogs, Key: report.LogKey(acct, id, kernel.RunStatePlanning, 2)},
		{Store: objectstore.RunLogs, Key: report.LogKey(acct, id, kernel.RunStateApplying, 3)},
		{Store: objectstore.RunLogs, Key: "apply-0"},
		{Store: objectstore.RunLogs, Key: "plan-0"},
		{Store: objectstore.RunLogs, Key: "plan-1"},
		{Store: objectstore.RunObservability, Key: "trace"},
	}
	slices.SortFunc(want, func(a, b report.Object) int { return strings.Compare(a.Key, b.Key) })
	if err != nil || !slices.Equal(objs, want) {
		t.Errorf("DeleteLogs = %+v, %v, want %+v", objs, err, want)
	}
	if got, err := s.Handed(ctx, id); err != nil || got.LogsDeletedAt == nil ||
		!got.LogsDeletedAt.Equal(at) || !got.UpdatedAt.Equal(at) {
		t.Errorf("the run after the delete = %+v, %v", got, err)
	}
	var chunks, artifacts int
	if err := db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM run_log_chunks WHERE account_id = $1 AND run_id = $2),
		(SELECT count(*) FROM run_artifacts WHERE account_id = $1 AND run_id = $3)`,
		acct, other, id).Scan(&chunks, &artifacts); err != nil || chunks != 1 || artifacts != 1 {
		t.Errorf("after the delete: %d of the other run's chunks and %d artifacts, %v", chunks,
			artifacts, err)
	}
	if got, err := s.Handed(ctx, other); err != nil || got.LogsDeletedAt != nil {
		t.Errorf("the other run = %+v, %v", got, err)
	}

	db.Close()
	if _, err := s.DeleteLogs(ctx, run, at); err == nil {
		t.Error("DeleteLogs on a closed pool succeeded")
	}
}
