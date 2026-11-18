package pg_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/control"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/schedule"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/trigger"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

type creationSpaces struct {
	accessapi.Spaces
	account kernel.AccountID
	err     error
}

func (s *creationSpaces) Hold(ctx context.Context, account kernel.AccountID, _ kernel.ULID) error {
	if !tx.In(ctx) {
		return runsapi.ErrNoTransaction
	}
	if s.err != nil {
		return s.err
	}
	if account != s.account {
		return accessapi.ErrNoAccount
	}
	return nil
}

// heldCreationStacks models the stack lifetime hold with the same advisory
// lock the existing lifecycle fixture uses for creation.
type heldCreationStacks struct{ *fakeStacks }

func (s heldCreationStacks) Hold(ctx context.Context, account kernel.AccountID, stack kernel.ULID) error {
	_, err := s.LockForRun(ctx, account, stack)
	return err
}

func (w *world) internalCreator() (*trigger.Service, *creationSpaces) {
	spaces := &creationSpaces{account: w.acct}
	return trigger.New(trigger.Deps{
		DB: w.db, Stacks: w.stacks, Spaces: spaces, Lifecycle: w.lc,
	}), spaces
}

func (w *world) internalSupersede() *control.Service {
	return control.New(control.Deps{
		DB: w.db, Store: w.store, Stacks: heldCreationStacks{w.stacks},
		Spaces: &creationSpaces{account: w.acct}, Lifecycle: w.lc,
		Now: func() time.Time { return w.now },
	})
}

func creationRequest(w *world, hash string) runsapi.CreateRequest {
	return runsapi.CreateRequest{
		Account: w.acct, Stack: w.stack, Type: kernel.RunTypeProposed,
		Commit:      runsapi.Commit{Hash: hash, Branch: "feature", Message: "Change infrastructure", Timestamp: w.now},
		TriggeredBy: "vcs/commit", Mode: runsapi.OncePerCommit,
	}
}

// A terminal run from another trigger still suppresses the event, even when
// it used a local workspace or private overrides. Different run types remain
// independent, and None bypasses reuse while retaining the source branch.
func TestAccInternalCreationModes(t *testing.T) {
	w := newWorld(t)
	c, _ := w.internalCreator()
	old := mustCreate(t, w, kernel.RunTypeProposed, "shared")
	w.mustMove(t, old, kernel.RunStateCanceled, machine.DriverAPI)
	if _, err := w.db.Exec(t.Context(), `UPDATE runs SET local_workspace='uploads/private',
		env_overrides_sealed=decode('01','hex') WHERE account_id=$1 AND id=$2`, w.acct, old.ID); err != nil {
		t.Fatal(err)
	}
	before := count(t, w.db, "events", w.acct)
	var admitted int
	in := creationRequest(w, "shared")
	in.BeforeCreate = func(context.Context) error { admitted++; return nil }
	var duplicate runsapi.CreatedRun
	if err := w.in(t, func(ctx context.Context) error {
		var err error
		duplicate, err = c.Create(ctx, in)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if duplicate.Created || duplicate.ID != old.ID || admitted != 0 || count(t, w.db, "events", w.acct) != before {
		t.Fatalf("duplicate=%+v admission=%d; an existing run must have no creation effects", duplicate, admitted)
	}
	in.Type = kernel.RunTypeTracked
	var tracked runsapi.CreatedRun
	if err := w.in(t, func(ctx context.Context) error {
		var err error
		tracked, err = c.Create(ctx, in)
		return err
	}); err != nil || !tracked.Created || admitted != 1 {
		t.Fatalf("tracked=%+v admission=%d err=%v", tracked, admitted, err)
	}
	in.Type, in.Mode, in.TriggeredBy = kernel.RunTypeProposed, runsapi.None, "github/developer"
	var fresh [2]runsapi.CreatedRun
	for i := range fresh {
		if err := w.in(t, func(ctx context.Context) error {
			var err error
			fresh[i], err = c.Create(ctx, in)
			return err
		}); err != nil || !fresh[i].Created || fresh[i].ID == old.ID {
			t.Fatalf("fresh=%+v err=%v", fresh[i], err)
		}
	}
	if fresh[0].ID == fresh[1].ID || admitted != 3 {
		t.Fatalf("fresh runs=%+v admission=%d", fresh, admitted)
	}
	var branch, actor string
	if err := w.db.QueryRow(t.Context(), `SELECT commit->>'branch',triggered_by FROM runs
		WHERE account_id=$1 AND id=$2`, w.acct, fresh[1].ID).Scan(&branch, &actor); err != nil || branch != "feature" || actor != "github/developer" {
		t.Fatalf("source branch=%s trigger=%s err=%v", branch, actor, err)
	}
	if _, found, err := w.store.OnCommit(t.Context(), kernel.NewAccountID(), w.stack, in.Type, in.Commit.Hash); err != nil || found {
		t.Fatalf("another account found a run: %t %v", found, err)
	}
	if _, found, err := w.store.OnCommit(t.Context(), w.acct, kernel.NewULID(), in.Type, in.Commit.Hash); err != nil || found {
		t.Fatalf("another stack found a run: %t %v", found, err)
	}
}

// The caller can retain earlier admitted events when a later item reaches its
// cap. A duplicate is recognized before admission even after the cap is full.
func TestAccInternalCreationBatchAdmission(t *testing.T) {
	w := newWorld(t)
	c, _ := w.internalCreator()
	conn, err := w.db.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(t.Context(), `CREATE TEMP TABLE run_admission (used integer NOT NULL);
		INSERT INTO run_admission VALUES (0)`); err != nil {
		t.Fatal(err)
	}
	capped := errors.New("event cap reached")
	var calls int
	admit := func(ctx context.Context) error {
		calls++
		var used int
		err := tx.Q(ctx, w.db).QueryRow(ctx, `UPDATE run_admission SET used=used+1 WHERE used<1 RETURNING used`).Scan(&used)
		if errors.Is(err, pgx.ErrNoRows) {
			return capped
		}
		return err
	}
	first := creationRequest(w, "first")
	first.BeforeCreate = admit
	second := first
	second.Commit.Hash = "second"
	var initial, duplicate runsapi.CreatedRun
	err = tx.Do(t.Context(), conn, tx.Options{}, func(ctx context.Context) error {
		var err error
		if initial, err = c.Create(ctx, first); err != nil {
			return err
		}
		if _, err = c.Create(ctx, second); !errors.Is(err, capped) {
			t.Fatalf("second item=%v, want cap refusal", err)
		}
		duplicate, err = c.Create(ctx, first)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var used int
	if err := conn.QueryRow(t.Context(), `SELECT used FROM run_admission`).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if !initial.Created || duplicate.Created || duplicate.ID != initial.ID || calls != 2 || used != 1 ||
		count(t, w.db, "runs", w.acct) != 1 || count(t, w.db, "events", w.acct) != 1 {
		t.Fatalf("batch initial=%+v duplicate=%+v calls=%d used=%d", initial, duplicate, calls, used)
	}
}

// A lost queue fence rolls back admission, the run and its outbox/scheduling
// work together. The valid claim can then create exactly one run.
func TestAccInternalCreationQueueFence(t *testing.T) {
	w := newWorld(t)
	c, _ := w.internalCreator()
	kind := queue.Kind{Name: "test.internal_creation", Queue: queue.Default, Policy: queue.Ignore}
	if err := w.in(t, func(ctx context.Context) error {
		_, err := w.jobs.EnqueueTx(ctx, kind, struct{}{}, queue.Account(w.acct.ULID), queue.UniqueKey(w.stack.String()))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	jobs, err := w.jobs.Claim(t.Context(), queue.Default, []string{kind.Name}, 1, time.Minute, "creation-test")
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim=%+v err=%v", jobs, err)
	}
	job := jobs[0]
	in := creationRequest(w, "fenced")
	in.BeforeCreate = func(ctx context.Context) error {
		_, err := tx.Q(ctx, w.db).Exec(ctx, `UPDATE jobs SET last_error='admitted' WHERE id=$1`, job.ID)
		return err
	}
	stale := job
	stale.Generation++
	err = w.in(t, func(ctx context.Context) error {
		if _, err := c.Create(ctx, in); err != nil {
			return err
		}
		return w.jobs.CompleteTx(ctx, stale)
	})
	if !errors.Is(err, queue.ErrLeaseLost) || count(t, w.db, "runs", w.acct) != 0 ||
		count(t, w.db, "events", w.acct) != 0 || count(t, w.db, "jobs", w.acct) != 1 {
		t.Fatalf("lost fence=%v left creation effects", err)
	}
	var lastError *string
	if err := w.db.QueryRow(t.Context(), `SELECT last_error FROM jobs WHERE id=$1`, job.ID).Scan(&lastError); err != nil || lastError != nil {
		t.Fatalf("rolled back admission=%v err=%v", lastError, err)
	}
	if err := w.in(t, func(ctx context.Context) error {
		if _, err := c.Create(ctx, in); err != nil {
			return err
		}
		return w.jobs.CompleteTx(ctx, job)
	}); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := w.db.QueryRow(t.Context(), `SELECT state FROM jobs WHERE id=$1`, job.ID).Scan(&state); err != nil || state != "succeeded" ||
		count(t, w.db, "runs", w.acct) != 1 || count(t, w.db, "events", w.acct) != 1 {
		t.Fatalf("committed job=%s err=%v", state, err)
	}
}

func TestAccInternalCreationConcurrent(t *testing.T) {
	w := newWorld(t)
	c, _ := w.internalCreator()
	in := creationRequest(w, "simultaneous")
	var admissions atomic.Int32
	in.BeforeCreate = func(context.Context) error { admissions.Add(1); return nil }
	type result struct {
		run runsapi.CreatedRun
		err error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			<-start
			var r runsapi.CreatedRun
			err := tx.Do(t.Context(), w.db, tx.Options{}, func(ctx context.Context) error {
				var err error
				r, err = c.Create(ctx, in)
				return err
			})
			results <- result{r, err}
		})
	}
	close(start)
	workers.Wait()
	close(results)
	var id kernel.ULID
	created := 0
	for r := range results {
		if r.err != nil {
			t.Fatal(r.err)
		}
		if !id.IsZero() && id != r.run.ID {
			t.Fatalf("concurrent jobs returned different runs: %s and %s", id, r.run.ID)
		}
		id = r.run.ID
		if r.run.Created {
			created++
		}
	}
	if created != 1 || admissions.Load() != 1 || count(t, w.db, "runs", w.acct) != 1 {
		t.Fatalf("created=%d admission=%d", created, admissions.Load())
	}
}

type failedCommitRead struct{ lifecycle.Store }

func (failedCommitRead) OnCommit(context.Context, kernel.AccountID, kernel.ULID, kernel.RunType, string) (lifecycle.Run, bool, error) {
	return lifecycle.Run{}, false, errInjected
}

func TestAccInternalCreationRefusals(t *testing.T) {
	w := newWorld(t)
	c, spaces := w.internalCreator()
	in := creationRequest(w, "refused")
	var admitted int
	in.BeforeCreate = func(context.Context) error { admitted++; return nil }
	if _, err := c.Create(t.Context(), in); !errors.Is(err, runsapi.ErrNoTransaction) {
		t.Fatalf("outside transaction=%v", err)
	}
	for _, mode := range []runsapi.DedupMode{"", "unknown"} {
		bad := in
		bad.Mode = mode
		if err := w.in(t, func(ctx context.Context) error { _, err := c.Create(ctx, bad); return err }); !errors.Is(err, runsapi.ErrCreationInput) {
			t.Fatalf("invalid mode=%v", err)
		}
	}
	settings := w.stacks.settings[w.stack]
	for _, refused := range []stacksapi.Settings{
		func() stacksapi.Settings { s := settings; s.Disabled = true; return s }(),
		func() stacksapi.Settings { s := settings; s.Deleting = true; return s }(),
		func() stacksapi.Settings {
			s := settings
			s.Lock = &stacksapi.Lock{Holder: "user:bob", By: "bob"}
			return s
		}(),
	} {
		w.stacks.settings[w.stack] = refused
		if err := w.in(t, func(ctx context.Context) error { _, err := c.Create(ctx, in); return err }); !errors.Is(err, runsapi.ErrCreationRefused) {
			t.Fatalf("stack refusal=%v", err)
		}
	}
	w.stacks.settings[w.stack] = settings
	for _, operation := range []string{"Settings", "LockForRun"} {
		w.stacks.err[operation] = errInjected
		if err := w.in(t, func(ctx context.Context) error { _, err := c.Create(ctx, in); return err }); !errors.Is(err, errInjected) {
			t.Fatalf("%s failure=%v", operation, err)
		}
		delete(w.stacks.err, operation)
	}
	spaces.err = errInjected
	if err := w.in(t, func(ctx context.Context) error { _, err := c.Create(ctx, in); return err }); !errors.Is(err, errInjected) {
		t.Fatalf("owner hold failure=%v", err)
	}
	spaces.err = nil
	missingOwner := in
	missingOwner.Account = kernel.NewAccountID()
	if err := w.in(t, func(ctx context.Context) error { _, err := c.Create(ctx, missingOwner); return err }); !errors.Is(err, accessapi.ErrNoAccount) {
		t.Fatalf("foreign owner=%v", err)
	}
	withoutHolder := trigger.New(trigger.Deps{Stacks: w.stacks, Lifecycle: w.lc})
	if err := w.in(t, func(ctx context.Context) error { _, err := withoutHolder.Create(ctx, in); return err }); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("missing owner holder=%v", err)
	}
	failedRead := trigger.New(trigger.Deps{
		Stacks: w.stacks, Spaces: spaces,
		Lifecycle: w.lifecycle(failedCommitRead{w.store}, w.events, w.jobs),
	})
	if err := w.in(t, func(ctx context.Context) error { _, err := failedRead.Create(ctx, in); return err }); !errors.Is(err, errInjected) {
		t.Fatalf("prior-run read failure=%v", err)
	}
	if admitted != 0 || count(t, w.db, "runs", w.acct) != 0 || count(t, w.db, "events", w.acct) != 0 {
		t.Fatalf("refusals invoked admission or wrote a run: admission=%d", admitted)
	}
}

func proposalOnBranch(t *testing.T, w *world, typ kernel.RunType, state kernel.RunState, hash, branch string) (kernel.ULID, kernel.ULID) {
	t.Helper()
	worker, workerPool := kernel.NewULID(), kernel.NewULID()
	id := w.working(t, typ, state, &workerPool, worker, w.now)
	if _, err := w.db.Exec(t.Context(), `UPDATE runs SET commit=jsonb_build_object('hash',$3::text,'branch',$4::text)
		WHERE account_id=$1 AND id=$2`, w.acct, id, hash, branch); err != nil {
		t.Fatal(err)
	}
	return id, worker
}

// Superseding uses the existing worker stop signal, never a direct state
// transition. It scopes account/stack/branch/type/state and preserves a prior
// request so the runner receives exactly one SIGINT.
func TestAccInternalSupersede(t *testing.T) {
	w := newWorld(t)
	s := w.internalSupersede()
	old, worker := proposalOnBranch(t, w, kernel.RunTypeProposed, kernel.RunStatePlanning, "old", "feature")
	current, _ := proposalOnBranch(t, w, kernel.RunTypeProposed, kernel.RunStatePlanning, "latest", "feature")
	otherBranch, _ := proposalOnBranch(t, w, kernel.RunTypeProposed, kernel.RunStatePlanning, "old", "elsewhere")
	tracked, _ := proposalOnBranch(t, w, kernel.RunTypeTracked, kernel.RunStatePlanning, "old", "feature")
	queued, _ := proposalOnBranch(t, w, kernel.RunTypeProposed, kernel.RunStateQueued, "old", "feature")
	foreign, _ := proposalOnBranch(t, w, kernel.RunTypeProposed, kernel.RunStatePlanning, "old", "feature")
	if _, err := w.db.Exec(t.Context(), `UPDATE runs SET account_id=$2 WHERE id=$1`, foreign, kernel.NewAccountID()); err != nil {
		t.Fatal(err)
	}
	var stopped int
	if err := w.in(t, func(ctx context.Context) error {
		var err error
		stopped, err = s.Supersede(ctx, w.acct, w.stack, "feature", "latest")
		return err
	}); err != nil || stopped != 1 {
		t.Fatalf("new stops=%d err=%v", stopped, err)
	}
	var state, by, note string
	if err := w.db.QueryRow(t.Context(), `SELECT state,stop_requested_by,stop_note FROM runs WHERE id=$1`, old).Scan(&state, &by, &note); err != nil ||
		state != "PLANNING" || by != "vcs/commit" || note != "superseded by commit latest" {
		t.Fatalf("stop state=%s by=%s note=%s err=%v", state, by, note, err)
	}
	for _, id := range []kernel.ULID{current, otherBranch, tracked, queued, foreign} {
		var requested bool
		if err := w.db.QueryRow(t.Context(), `SELECT stop_requested_at IS NOT NULL FROM runs WHERE id=$1`, id).Scan(&requested); err != nil || requested {
			t.Fatalf("unrelated run %s received stop=%t err=%v", id, requested, err)
		}
	}
	signals := schedule.NewSignals(w.store, nil)
	for _, want := range []workersapi.Signal{workersapi.SignalStop, ""} {
		if got := w.deliver(t, signals, worker); got != want {
			t.Fatalf("stop delivery=%s want=%s", got, want)
		}
	}
	if err := w.in(t, func(ctx context.Context) error {
		var err error
		stopped, err = s.Supersede(ctx, w.acct, w.stack, "feature", "latest")
		return err
	}); err != nil || stopped != 0 || w.deliver(t, signals, worker) != "" {
		t.Fatalf("repeat stop=%d err=%v", stopped, err)
	}
	rollback, _ := proposalOnBranch(t, w, kernel.RunTypeProposed, kernel.RunStatePlanning, "another-old", "feature")
	err := w.in(t, func(ctx context.Context) error {
		if _, err := s.Supersede(ctx, w.acct, w.stack, "feature", "latest"); err != nil {
			return err
		}
		return errInjected
	})
	var requested bool
	if !errors.Is(err, errInjected) {
		t.Fatalf("rollback=%v", err)
	}
	if err := w.db.QueryRow(t.Context(), `SELECT stop_requested_at IS NOT NULL FROM runs WHERE id=$1`, rollback).Scan(&requested); err != nil || requested {
		t.Fatalf("rolled-back stop=%t err=%v", requested, err)
	}
}
