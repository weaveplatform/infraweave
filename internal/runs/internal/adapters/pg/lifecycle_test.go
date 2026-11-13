package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// projection is one SetRunState call.
type projection struct {
	state   kernel.StackState
	blocked bool
}

// fakeStacks is the stacks domain over settings by stack ID. LockForRun
// takes a transaction-scoped advisory lock on the stack, as the real one
// locks its row, and SetRunState records each projection; a non-nil err
// fails the method it names.
type fakeStacks struct {
	stacksapi.Stacks
	db       *pgxpool.Pool
	mu       sync.Mutex
	settings map[kernel.ULID]stacksapi.Settings
	written  map[kernel.ULID][]projection
	err      map[string]error
}

func (f *fakeStacks) Get(
	_ context.Context,
	_ kernel.AccountID,
	id kernel.ULID,
) (stacksapi.Stack, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err["Get"]; err != nil {
		return stacksapi.Stack{}, err
	}
	return f.settings[id].Stack, nil
}

func (f *fakeStacks) BySlug(
	_ context.Context,
	_ kernel.AccountID,
	slug string,
) (stacksapi.Stack, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, st := range f.settings {
		if st.Slug == slug {
			return st.Stack, nil
		}
	}
	return stacksapi.Stack{}, stacksapi.ErrNotFound
}

func (f *fakeStacks) Settings(
	_ context.Context,
	_ kernel.AccountID,
	id kernel.ULID,
) (stacksapi.Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err["Settings"]; err != nil {
		return stacksapi.Settings{}, err
	}
	return f.settings[id], nil
}

func (f *fakeStacks) LockForRun(
	ctx context.Context,
	_ kernel.AccountID,
	id kernel.ULID,
) (stacksapi.Settings, error) {
	f.mu.Lock()
	err := f.err["LockForRun"]
	st, ok := f.settings[id]
	f.mu.Unlock()
	if err != nil {
		return stacksapi.Settings{}, err
	}
	if !ok {
		return stacksapi.Settings{}, stacksapi.ErrNotFound
	}
	if _, err := tx.Q(ctx, f.db).Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		id.String()); err != nil {
		return stacksapi.Settings{}, err
	}
	return st, nil
}

func (f *fakeStacks) SetRunState(
	_ context.Context,
	_ kernel.AccountID,
	id kernel.ULID,
	state kernel.StackState,
	blocked bool,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err["SetRunState"]; err != nil {
		return err
	}
	f.written[id] = append(f.written[id], projection{state, blocked})
	return nil
}

// last is the last projection written of stack.
func (f *fakeStacks) last(stack kernel.ULID) (projection, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w := f.written[stack]
	if len(w) == 0 {
		return projection{}, false
	}
	return w[len(w)-1], true
}

// fakeSources resolves the commits by SHA, answers every run's source with
// none, and records the runs whose credentials are released; err fails a
// release.
type fakeSources struct {
	mu       sync.Mutex
	commits  map[string]stacksapi.Commit
	released []kernel.ULID
	err      error
}

func (f *fakeSources) Resolve(
	_ context.Context,
	_ kernel.AccountID,
	_ kernel.ULID,
	ref string,
) (stacksapi.Commit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.commits[ref]; ok {
		return c, nil
	}
	return stacksapi.Commit{}, stacksapi.ErrRefNotFound
}

func (*fakeSources) Source(
	context.Context,
	kernel.AccountID,
	kernel.ULID,
	kernel.ULID,
	string,
) (runsapi.Source, error) {
	return runsapi.Source{}, nil
}

func (f *fakeSources) Release(_ context.Context, _ kernel.AccountID, run kernel.ULID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.released = append(f.released, run)
	return nil
}

// failing is a lifecycle port that fails the call fail names, and passes
// the rest to the store, the outbox or the queue.
type failing struct {
	lifecycle.Store
	events lifecycle.Events
	jobs   lifecycle.Jobs
	fail   string
}

var errInjected = errors.New("injected")

func (f failing) Lock(
	ctx context.Context,
	a kernel.AccountID,
	id kernel.ULID,
) (lifecycle.Run, error) {
	if f.fail == "Lock" {
		return lifecycle.Run{}, errInjected
	}
	return f.Store.Lock(ctx, a, id)
}

func (f failing) Move(ctx context.Context, run lifecycle.Run, step lifecycle.Step) error {
	if f.fail == "Move" {
		return errInjected
	}
	return f.Store.Move(ctx, run, step)
}

func (f failing) Insert(ctx context.Context, run lifecycle.Run, step lifecycle.Step) error {
	if f.fail == "Insert" {
		return errInjected
	}
	return f.Store.Insert(ctx, run, step)
}

func (f failing) Active(
	ctx context.Context,
	a kernel.AccountID,
	stack kernel.ULID,
	typ kernel.RunType,
	hash, workspace string,
) (lifecycle.Run, bool, error) {
	if f.fail == "Active" {
		return lifecycle.Run{}, false, errInjected
	}
	return f.Store.Active(ctx, a, stack, typ, hash, workspace)
}

func (f failing) Track(ctx context.Context, run lifecycle.Run) error {
	if f.fail == "Track" {
		return errInjected
	}
	return f.Store.Track(ctx, run)
}

func (f failing) Unlock(ctx context.Context, a kernel.AccountID, run kernel.ULID) (bool, error) {
	if f.fail == "Unlock" {
		return false, errInjected
	}
	return f.Store.Unlock(ctx, a, run)
}

func (f failing) Projection(
	ctx context.Context,
	a kernel.AccountID,
	stack kernel.ULID,
	states []kernel.StackState,
) (kernel.StackState, bool, error) {
	if f.fail == "Projection" {
		return "", false, errInjected
	}
	return f.Store.Projection(ctx, a, stack, states)
}

func (f failing) Announce(ctx context.Context, run lifecycle.Run) error {
	if f.fail == "Announce" {
		return errInjected
	}
	return f.Store.Announce(ctx, run)
}

func (f failing) Append(ctx context.Context, ev outbox.Event) (outbox.Event, error) {
	if f.fail == "Append" {
		return outbox.Event{}, errInjected
	}
	return f.events.Append(ctx, ev)
}

func (f failing) EnqueueTx(
	ctx context.Context,
	k queue.Kind,
	payload any,
	opts ...queue.Option,
) (int64, error) {
	if f.fail == "EnqueueTx" || f.fail == "EnqueueTx "+k.Name {
		return 0, errInjected
	}
	return f.jobs.EnqueueTx(ctx, k, payload, opts...)
}

// world is an account with one stack, its fakes, and a lifecycle over the
// runs store, the outbox and the queue, with a fixed clock.
type world struct {
	db      *pgxpool.Pool
	acct    kernel.AccountID
	stack   kernel.ULID
	stacks  *fakeStacks
	sources *fakeSources
	store   *pg.Store
	events  *outbox.Outbox
	jobs    *queue.Queue
	now     time.Time
	lc      *lifecycle.Lifecycle
}

func newWorld(t *testing.T) *world {
	t.Helper()
	db := pool(t)
	w := &world{
		db: db, acct: kernel.NewAccountID(), stack: kernel.NewULID(),
		sources: &fakeSources{}, store: pg.New(db, keyring(t, db)), jobs: queue.New(db),
		now: time.Date(2026, 11, 11, 9, 30, 0, 123456789, time.UTC),
	}
	kinds := outbox.NewKinds()
	if err := kinds.Register(runsapi.UpdatedKind, runsapi.UpdatedVersion,
		runsapi.UpdatedSchema); err != nil {
		t.Fatal(err)
	}
	w.events = outbox.New(db, w.jobs, kinds)
	w.stacks = &fakeStacks{
		db: db, err: map[string]error{}, written: map[kernel.ULID][]projection{},
		settings: map[kernel.ULID]stacksapi.Settings{w.stack: {Stack: stacksapi.Stack{
			ID: w.stack, Account: w.acct, Slug: "network", Name: "Network",
			Space: kernel.NewULID(),
		}}},
	}
	w.lc = w.lifecycle(w.store, w.events, w.jobs)
	return w
}

// lifecycle is a lifecycle of w's over store, events and jobs.
func (w *world) lifecycle(
	store lifecycle.Store,
	events lifecycle.Events,
	jobs lifecycle.Jobs,
) *lifecycle.Lifecycle {
	lc := lifecycle.New(lifecycle.Deps{
		Store: store, Stacks: w.stacks, Events: events, Jobs: jobs,
		Now: func() time.Time { return w.now },
	})
	lc.BindSources(w.sources)
	return lc
}

// in runs f in a transaction of its own.
func (w *world) in(t *testing.T, f func(ctx context.Context) error) error {
	t.Helper()
	return tx.Do(t.Context(), w.db, tx.Options{}, f)
}

// create creates a run of w's stack of typ on commit hash by alice.
func (w *world) create(
	t *testing.T,
	typ kernel.RunType,
	hash string,
) (lifecycle.Run, bool, error) {
	t.Helper()
	var (
		run     lifecycle.Run
		created bool
	)
	err := w.in(t, func(ctx context.Context) error {
		var err error
		run, created, err = w.lc.Create(ctx, lifecycle.NewRun{
			Account: w.acct, Stack: w.stack, Type: typ,
			Commit: stacksapi.Commit{
				Hash: hash, Message: "Add a subnet", AuthorName: "Alice",
				Timestamp: w.now.Add(-time.Hour),
			},
			Actor: lifecycle.Actor{Caller: "user:alice", Login: "alice"},
		})
		return err
	})
	return run, created, err
}

// move takes run from its version to the state to by driver, with the
// guards checked.
func (w *world) move(
	t *testing.T,
	run lifecycle.Run,
	to kernel.RunState,
	driver machine.Driver,
	checked ...machine.Guard,
) (lifecycle.Run, error) {
	t.Helper()
	var moved lifecycle.Run
	err := w.in(t, func(ctx context.Context) error {
		var err error
		moved, err = w.lc.Transition(ctx, lifecycle.Request{
			Account: w.acct, Run: run.ID, Version: run.Version, To: to, Driver: driver,
			Checked: checked,
		})
		return err
	})
	return moved, err
}

// mustMove is move that fails the test on an error.
func (w *world) mustMove(
	t *testing.T,
	run lifecycle.Run,
	to kernel.RunState,
	driver machine.Driver,
	checked ...machine.Guard,
) lifecycle.Run {
	t.Helper()
	moved, err := w.move(t, run, to, driver, checked...)
	if err != nil {
		t.Fatalf("%s → %s by %s: %v", run.State, to, driver, err)
	}
	return moved
}

// takeLock makes run hold its stack's run lock, as the scheduler does.
func (w *world) takeLock(t *testing.T, run lifecycle.Run) {
	t.Helper()
	if _, err := w.db.Exec(t.Context(), `INSERT INTO stack_run_locks (account_id, stack_id, run_id)
		VALUES ($1, $2, $3)`, w.acct, run.Stack, run.ID); err != nil {
		t.Fatal(err)
	}
}

// step is one transition row as stored.
type step struct {
	State    string
	Username *string
	Note     *string
	Until    *time.Time
	Terminal bool
}

// steps reads run's transitions in sequence order.
func (w *world) steps(t *testing.T, run kernel.ULID) []step {
	t.Helper()
	rows, err := w.db.Query(t.Context(), `SELECT state, username, note, until, terminal
		FROM run_state_transitions WHERE account_id = $1 AND run_id = $2 ORDER BY sequence`,
		w.acct, run)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []step
	for rows.Next() {
		var s step
		if err := rows.Scan(&s.State, &s.Username, &s.Note, &s.Until, &s.Terminal); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// event is one run_updated event as stored.
type event struct {
	Sequence int64
	Space    string
	Doc      runsapi.Updated
}

// updates reads run's run_updated events in sequence order.
func (w *world) updates(t *testing.T, run kernel.ULID) []event {
	t.Helper()
	rows, err := w.db.Query(t.Context(), `SELECT sequence, space, document FROM events
		WHERE account_id = $1 AND kind = $2 AND subject_kind = 'run' AND subject_id = $3
		ORDER BY sequence`, w.acct, runsapi.UpdatedKind, run.String())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []event
	for rows.Next() {
		var (
			e   event
			doc []byte
		)
		if err := rows.Scan(&e.Sequence, &e.Space, &doc); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(doc, &e.Doc); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

// schedules counts w's pending run.schedule jobs for its stack.
func (w *world) schedules(t *testing.T) int {
	t.Helper()
	var n int
	if err := w.db.QueryRow(t.Context(), `SELECT count(*) FROM jobs
		WHERE kind = $1 AND unique_key = $2 AND state = 'pending' AND account_id = $3`,
		lifecycle.ScheduleKind.Name, "stack:"+w.stack.String(), w.acct).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestAccLifecycleCreate creates a run in QUEUED with its first transition,
// its run_updated event and its stack's run.schedule; creates it once per
// stack, type and commit among runs that have not ended, and reads back a
// local preview's flag and its commit's branch; and refuses a
// type no edge creates, a disabled stack, a stack another caller locked
// and a call outside a transaction.
func TestAccLifecycleCreate(t *testing.T) {
	w := newWorld(t)
	run, created, err := w.create(t, kernel.RunTypeTracked, "abc123")
	if err != nil || !created {
		t.Fatalf("Create = %v, %v", created, err)
	}
	if run.State != kernel.RunStateQueued || run.Version != 1 ||
		run.Priority != kernel.RunPriorityNormal || run.TriggeredBy != "alice" ||
		!run.CreatedAt.Equal(w.now.Truncate(time.Microsecond)) {
		t.Errorf("created %+v", run)
	}
	if got := w.steps(t, run.ID); len(got) != 1 || got[0].State != "QUEUED" ||
		got[0].Terminal || got[0].Username == nil || *got[0].Username != "alice" {
		t.Errorf("transitions %+v", got)
	}
	evs := w.updates(t, run.ID)
	if len(evs) != 1 || evs[0].Sequence != 1 ||
		evs[0].Space != w.stacks.settings[w.stack].Space.String() {
		t.Fatalf("events %+v", evs)
	}
	doc := evs[0].Doc
	if doc.State != kernel.RunStateQueued || doc.Run.ID != run.ID ||
		doc.Run.Commit.Hash != "abc123" || doc.Run.Commit.Author != "Alice" ||
		doc.Run.CreatedAt != run.CreatedAt.UnixNano() || doc.Stack.ID != "network" ||
		doc.Stack.ULID != w.stack || doc.Note != nil || *doc.Run.TriggeredBy != "alice" {
		t.Errorf("document %+v", doc)
	}
	if n := w.schedules(t); n != 1 {
		t.Errorf("%d run.schedule jobs, want 1", n)
	}
	if _, ok := w.stacks.last(w.stack); ok {
		t.Error("a queued run wrote the stack's projection")
	}

	again, created, err := w.create(t, kernel.RunTypeTracked, "abc123")
	if err != nil || created || again.ID != run.ID {
		t.Errorf("Create again = %s, %v, %v; want %s, false", again.ID, created, err, run.ID)
	}
	if n := count(t, w.db, "runs", w.acct); n != 1 {
		t.Errorf("%d runs after a second Create, want 1", n)
	}
	proposed, created, err := w.create(t, kernel.RunTypeProposed, "abc123")
	if err != nil || !created || proposed.ID == run.ID {
		t.Errorf("Create of another type = %v, %v", created, err)
	}
	preview := lifecycle.NewRun{
		Account: w.acct, Stack: w.stack, Type: kernel.RunTypeProposed,
		Commit:         stacksapi.Commit{Hash: "abc123", Branch: "main"},
		Actor:          lifecycle.Actor{Caller: "user:alice", Login: "alice"},
		LocalWorkspace: "acct/stack/upload-1",
	}
	var previews []kernel.ULID
	for _, workspace := range []string{
		"acct/stack/upload-1", "acct/stack/upload-1",
		"acct/stack/upload-2",
	} {
		preview.LocalWorkspace = workspace
		if err := w.in(t, func(ctx context.Context) error {
			r, _, err := w.lc.Create(ctx, preview)
			if err == nil && (!r.LocalPreview() || r.LocalWorkspace != workspace ||
				r.Commit.Branch != "main") {
				t.Errorf("a local preview reads back as %+v", r)
			}
			previews = append(previews, r.ID)
			return err
		}); err != nil {
			t.Errorf("Create of a local preview = %v", err)
		}
	}
	// A local preview on the commit of a proposed run is a run of its own,
	// and one of each upload.
	if len(previews) != 3 || previews[0] == proposed.ID || previews[1] != previews[0] ||
		previews[2] == previews[0] {
		t.Errorf("local previews %v beside the proposed run %s", previews, proposed.ID)
	}
	w.mustMove(t, run, kernel.RunStateDiscarded, machine.DriverAPI)
	if _, created, err := w.create(t, kernel.RunTypeTracked, "abc123"); err != nil || !created {
		t.Errorf("Create after the run ended = %v, %v", created, err)
	}

	if _, _, err := w.create(
		t,
		kernel.RunTypeParse,
		"abc123",
	); !errors.Is(
		err,
		lifecycle.ErrRefused,
	) {
		t.Errorf("Create of a PARSE run = %v, want ErrRefused", err)
	}
	st := w.stacks.settings[w.stack]
	st.Disabled = true
	w.stacks.settings[w.stack] = st
	if _, _, err := w.create(
		t,
		kernel.RunTypeTask,
		"abc123",
	); !errors.Is(
		err,
		lifecycle.ErrDisabled,
	) {
		t.Errorf("Create on a disabled stack = %v, want ErrDisabled", err)
	}
	st.Disabled, st.Lock = false, &stacksapi.Lock{Holder: "user:bob", By: "bob"}
	w.stacks.settings[w.stack] = st
	_, _, err = w.create(t, kernel.RunTypeTask, "abc123")
	if l, ok := errors.AsType[*lifecycle.Locked](err); !ok || l.By != "bob" ||
		!errors.Is(err, lifecycle.ErrLocked) || l.Error() == "" {
		t.Errorf("Create on a stack bob locked = %v, want Locked by bob", err)
	}
	st.Lock.Holder = "user:alice"
	w.stacks.settings[w.stack] = st
	if _, created, err := w.create(t, kernel.RunTypeTask, "abc123"); err != nil || !created {
		t.Errorf("Create by the lock's holder = %v, %v", created, err)
	}
	if _, _, err := w.lc.Create(t.Context(), lifecycle.NewRun{
		Account: w.acct, Stack: w.stack, Type: kernel.RunTypeTask,
	}); !errors.Is(err, lifecycle.ErrNoTransaction) {
		t.Errorf("Create outside a transaction = %v", err)
	}
}

// TestAccLifecycleTransition takes a tracked run from QUEUED to FINISHED:
// each move writes the run's state, version and transition, and appends
// its event; the run becomes the stack's tracked run on READY and writes
// the stack's projection from then on; a confirmation needs its guard;
// UNCONFIRMED and FINISHED schedule the stack; FINISHED releases the
// run's lock and credentials; and each move out of a state a worker holds
// enqueues that state's log compaction. A stale version, a move no edge
// allows, a missing run and a call outside a transaction are refused.
func TestAccLifecycleTransition(t *testing.T) {
	w := newWorld(t)
	run, _, err := w.create(t, kernel.RunTypeTracked, "abc123")
	if err != nil {
		t.Fatal(err)
	}

	_, err = w.move(t, run, kernel.RunStateReady, machine.DriverScheduler,
		machine.GuardStackLockFree, machine.GuardApproved)
	if r, ok := errors.AsType[*lifecycle.Refused](err); !ok ||
		r.Guard != machine.GuardUpstreamFinished || r.Error() == "" {
		t.Errorf("READY without its guards = %v, want Refused for upstream-finished", err)
	}
	_, err = w.move(t, run, kernel.RunStateApplying, machine.DriverAPI)
	if r, ok := errors.AsType[*lifecycle.Refused](err); !ok || r.Guard != "" || r.Error() == "" {
		t.Errorf("QUEUED → APPLYING = %v, want Refused", err)
	}

	w.now = w.now.Add(time.Second)
	run = w.mustMove(t, run, kernel.RunStateReady, machine.DriverScheduler,
		machine.GuardStackLockFree, machine.GuardApproved, machine.GuardUpstreamFinished,
		machine.GuardExternalSettled, machine.GuardStackEnabled)
	if run.Version != 2 || run.State != kernel.RunStateReady ||
		!run.StateChangedAt.Equal(w.now.Truncate(time.Microsecond)) {
		t.Errorf("READY: %+v", run)
	}
	if p, ok := w.stacks.last(w.stack); !ok || p != (projection{"READY", false}) {
		t.Errorf("projection on READY = %+v, %t", p, ok)
	}
	var tracked kernel.ULID
	if err := w.db.QueryRow(t.Context(), `SELECT tracked_run_id FROM stack_run_state
		WHERE account_id = $1 AND stack_id = $2`, w.acct, w.stack).Scan(&tracked); err != nil ||
		tracked != run.ID {
		t.Errorf("tracked run %s, %v; want %s", tracked, err, run.ID)
	}
	w.takeLock(t, run)

	stale := run
	stale.Version = 1
	_, err = w.move(t, stale, kernel.RunStatePreparing, machine.DriverWorkerClaim)
	if c, ok := errors.AsType[*lifecycle.Conflict](err); !ok ||
		c.State != kernel.RunStateReady || c.Version != 2 || !errors.Is(err, lifecycle.ErrConflict) ||
		c.Error() == "" {
		t.Errorf("a stale version = %v, want Conflict at READY 2", err)
	}

	for _, m := range []struct {
		to kernel.RunState
		by machine.Driver
	}{
		{kernel.RunStatePreparing, machine.DriverWorkerClaim},
		{kernel.RunStateInitializing, machine.DriverWorkerReport},
		{kernel.RunStatePlanning, machine.DriverWorkerReport},
		{kernel.RunStateUnconfirmed, machine.DriverDecision},
	} {
		run = w.mustMove(t, run, m.to, m.by)
	}
	if p, _ := w.stacks.last(w.stack); p != (projection{"UNCONFIRMED", true}) {
		t.Errorf("projection on UNCONFIRMED = %+v", p)
	}
	if n := w.schedules(t); n != 1 {
		t.Errorf("%d pending run.schedule jobs after UNCONFIRMED, want the coalesced 1", n)
	}
	if _, err := w.move(t, run, kernel.RunStateConfirmed, machine.DriverAPI); !errors.Is(err,
		lifecycle.ErrRefused) {
		t.Errorf("CONFIRMED unapproved = %v, want ErrRefused", err)
	}
	until := w.now.Add(time.Hour).Truncate(time.Microsecond)
	if err := w.in(t, func(ctx context.Context) error {
		var err error
		run, err = w.lc.Transition(ctx, lifecycle.Request{
			Account: w.acct, Run: run.ID, Version: run.Version, To: kernel.RunStateConfirmed,
			Driver: machine.DriverAPI, Checked: []machine.Guard{machine.GuardApproved},
			Actor: lifecycle.ActorOf(kernel.Principal{Kind: kernel.PrincipalUser, Login: "carol"}),
			Note:  "looks good", Until: &until,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct {
		to kernel.RunState
		by machine.Driver
	}{
		{kernel.RunStatePreparingApply, machine.DriverWorkerClaim},
		{kernel.RunStateApplying, machine.DriverWorkerReport},
		{kernel.RunStateFinished, machine.DriverWorkerReport},
	} {
		run = w.mustMove(t, run, m.to, m.by)
	}

	if run.Version != 10 {
		t.Errorf("version %d after nine transitions, want 10", run.Version)
	}
	steps := w.steps(t, run.ID)
	if len(steps) != 10 || steps[9].State != "FINISHED" || !steps[9].Terminal ||
		steps[9].Username != nil {
		t.Fatalf("transitions %+v", steps)
	}
	if c := steps[6]; c.State != "CONFIRMED" || *c.Username != "carol" || *c.Note != "looks good" ||
		!c.Until.Equal(until) {
		t.Errorf("CONFIRMED's transition %+v", c)
	}
	evs := w.updates(t, run.ID)
	if len(evs) != 10 || evs[9].Sequence != 10 || evs[9].Doc.State != kernel.RunStateFinished ||
		evs[6].Doc.Username == nil || *evs[6].Doc.Username != "carol" {
		t.Errorf("%d events, last %+v", len(evs), evs[len(evs)-1])
	}
	if n := count(t, w.db, "stack_run_locks", w.acct); n != 0 {
		t.Errorf("%d run locks after FINISHED, want 0", n)
	}
	if p, _ := w.stacks.last(w.stack); p != (projection{"FINISHED", false}) {
		t.Errorf("projection on FINISHED = %+v", p)
	}
	if len(w.sources.released) != 1 || w.sources.released[0] != run.ID {
		t.Errorf("released %v, want %s", w.sources.released, run.ID)
	}
	var compactions []string
	rows, err := w.db.Query(t.Context(), `SELECT payload::text FROM jobs
		WHERE kind = $1 AND account_id = $2 ORDER BY unique_key`,
		lifecycle.CompactKind.Name, w.acct)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		compactions = append(
			compactions,
			strings.TrimPrefix(payload, `{"run": "`+run.ID.String()+`", `),
		)
	}
	if want := []string{
		`"state": "PREPARING", "stateVersion": 3}`, `"state": "INITIALIZING", "stateVersion": 4}`,
		`"state": "PLANNING", "stateVersion": 5}`, `"state": "PREPARING_APPLY", "stateVersion": 8}`,
		`"state": "APPLYING", "stateVersion": 9}`,
	}; !slices.Equal(compactions, want) {
		t.Errorf("compactions %q, want %q", compactions, want)
	}

	if _, err := w.move(t, lifecycle.Run{ID: kernel.NewULID(), Version: 1},
		kernel.RunStateReady, machine.DriverScheduler); !errors.Is(err, lifecycle.ErrNotFound) {
		t.Errorf("a missing run = %v, want ErrNotFound", err)
	}
	if _, err := w.lc.Transition(t.Context(), lifecycle.Request{}); !errors.Is(err,
		lifecycle.ErrNoTransaction) {
		t.Errorf("Transition outside a transaction = %v", err)
	}
}

// TestAccLifecycleTaskReleases checks that a task leaves the stack's state
// to its tracked run, and that its end, releasing the lock, writes the
// tracked run's last state with the stack unblocked; a tracked run
// canceled before it ran leaves its last state the stack showed.
func TestAccLifecycleTaskReleases(t *testing.T) {
	w := newWorld(t)
	tracked, _, err := w.create(t, kernel.RunTypeTracked, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	all := []machine.Guard{
		machine.GuardStackLockFree, machine.GuardApproved, machine.GuardUpstreamFinished,
		machine.GuardExternalSettled, machine.GuardStackEnabled,
	}
	tracked = w.mustMove(t, tracked, kernel.RunStateReady, machine.DriverScheduler, all...)
	w.takeLock(t, tracked)
	w.mustMove(t, tracked, kernel.RunStateCanceled, machine.DriverTimers)
	if p, _ := w.stacks.last(w.stack); p != (projection{"READY", false}) {
		t.Errorf("projection after a canceled tracked run = %+v", p)
	}

	task, _, err := w.create(t, kernel.RunTypeTask, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	task = w.mustMove(t, task, kernel.RunStateReady, machine.DriverScheduler, all...)
	w.takeLock(t, task)
	before := len(w.stacks.written[w.stack])
	for _, m := range []struct {
		to kernel.RunState
		by machine.Driver
	}{
		{kernel.RunStatePreparing, machine.DriverWorkerClaim},
		{kernel.RunStateInitializing, machine.DriverWorkerReport},
		{kernel.RunStatePerforming, machine.DriverWorkerReport},
	} {
		task = w.mustMove(t, task, m.to, m.by)
	}
	if n := len(w.stacks.written[w.stack]); n != before {
		t.Errorf("a task wrote %d projections before it ended", n-before)
	}
	w.mustMove(t, task, kernel.RunStateFailed, machine.DriverLiveness)
	if p, _ := w.stacks.last(w.stack); p != (projection{"READY", false}) {
		t.Errorf("projection after the task = %+v, want the tracked run's READY, unblocked", p)
	}
}

// TestAccLifecycleRollback checks that a caller's transaction that rolls
// back after a run's end leaves no transition, no event, no job and its
// run lock in place.
func TestAccLifecycleRollback(t *testing.T) {
	w := newWorld(t)
	run, _, err := w.create(t, kernel.RunTypeTask, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	w.takeLock(t, run)
	jobs := func() int {
		var n int
		if err := w.db.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE account_id = $1`,
			w.acct).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := jobs()
	err = w.in(t, func(ctx context.Context) error {
		if _, err := w.lc.Transition(ctx, lifecycle.Request{
			Account: w.acct, Run: run.ID, Version: 1, To: kernel.RunStateCanceled,
			Driver: machine.DriverAPI,
		}); err != nil {
			return err
		}
		return errInjected
	})
	if !errors.Is(err, errInjected) {
		t.Fatalf("the caller's transaction = %v", err)
	}
	if n := len(w.steps(t, run.ID)); n != 1 {
		t.Errorf("%d transitions after the rollback, want 1", n)
	}
	if n := len(w.updates(t, run.ID)); n != 1 {
		t.Errorf("%d events after the rollback, want 1", n)
	}
	if n := count(t, w.db, "stack_run_locks", w.acct); n != 1 {
		t.Errorf("%d run locks after the rollback, want 1", n)
	}
	if n := jobs(); n != before {
		t.Errorf("%d jobs after the rollback, want %d", n, before)
	}
	var state string
	if err := w.db.QueryRow(t.Context(), `SELECT state FROM runs WHERE account_id = $1 AND id = $2`,
		w.acct, run.ID).Scan(&state); err != nil || state != "QUEUED" {
		t.Errorf("state %q, %v after the rollback", state, err)
	}
}

// TestAccLifecycleConcurrent races fifty transitions that read one version:
// one moves the run and the other 49 fail with ErrConflict.
func TestAccLifecycleConcurrent(t *testing.T) {
	w := newWorld(t)
	run, _, err := w.create(t, kernel.RunTypeTracked, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	const n = 50
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { _, errs[i] = w.move(t, run, kernel.RunStateCanceled, machine.DriverAPI) })
	}
	wg.Wait()
	moved, conflicts := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			moved++
		case errors.Is(err, lifecycle.ErrConflict):
			conflicts++
		default:
			t.Errorf("a racing transition failed: %v", err)
		}
	}
	if moved != 1 || conflicts != n-1 {
		t.Errorf("%d moved and %d conflicted, want 1 and %d", moved, conflicts, n-1)
	}
	if got := len(w.steps(t, run.ID)); got != 2 {
		t.Errorf("%d transitions, want 2", got)
	}
}

// TestAccLifecycleFailures fails each call a creation and a run's end
// make, and checks that the failure reaches the caller and its
// transaction leaves nothing.
func TestAccLifecycleFailures(t *testing.T) {
	for _, fail := range []string{
		"Lock", "Move", "Insert", "Active", "Track", "Unlock", "Projection", "Append",
		"EnqueueTx", "Get", "LockForRun", "SetRunState", "Release", "Announce",
	} {
		t.Run(fail, func(t *testing.T) {
			w := newWorld(t)
			st := w.stacks.settings[w.stack]
			st.WorkerPool = &w.stack
			w.stacks.settings[w.stack] = st
			run, _, err := w.create(t, kernel.RunTypeTracked, "abc123")
			if err != nil {
				t.Fatal(err)
			}
			f := failing{Store: w.store, events: w.events, jobs: w.jobs, fail: fail}
			w.lc = w.lifecycle(f, f, f)
			switch fail {
			case "Get", "LockForRun", "SetRunState":
				w.stacks.err[fail] = errInjected
			case "Release":
				w.sources.err = errInjected
			}
			_, _, createErr := w.create(t, kernel.RunTypeTracked, "def456")
			_, moveErr := w.move(t, run, kernel.RunStateReady, machine.DriverScheduler,
				machine.GuardStackLockFree, machine.GuardApproved, machine.GuardUpstreamFinished,
				machine.GuardExternalSettled, machine.GuardStackEnabled)
			if moveErr == nil {
				_, moveErr = w.move(t, lifecycle.Run{ID: run.ID, Version: 2},
					kernel.RunStateCanceled, machine.DriverAPI)
			}
			if !errors.Is(createErr, errInjected) && !errors.Is(moveErr, errInjected) {
				t.Fatalf(
					"Create = %v, Transition = %v; want the injected failure",
					createErr,
					moveErr,
				)
			}
			if n := count(t, w.db, "runs", w.acct); n != 1 && createErr != nil {
				t.Errorf("%d runs after a failed creation, want 1", n)
			}
		})
	}
}

// TestAccLifecycleCompactionFails checks that a move out of a state a
// worker holds fails, writing nothing, when its compaction cannot be
// enqueued.
func TestAccLifecycleCompactionFails(t *testing.T) {
	w := newWorld(t)
	run, _, err := w.create(t, kernel.RunTypeTracked, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	run = w.mustMove(t, run, kernel.RunStateReady, machine.DriverScheduler,
		machine.GuardStackLockFree, machine.GuardApproved, machine.GuardUpstreamFinished,
		machine.GuardExternalSettled, machine.GuardStackEnabled)
	run = w.mustMove(t, run, kernel.RunStatePreparing, machine.DriverWorkerClaim)
	f := failing{
		Store: w.store, events: w.events, jobs: w.jobs,
		fail: "EnqueueTx " + lifecycle.CompactKind.Name,
	}
	w.lc = w.lifecycle(f, f, f)
	if _, err := w.move(t, run, kernel.RunStateInitializing,
		machine.DriverWorkerReport); !errors.Is(err, errInjected) {
		t.Errorf("Transition = %v, want the injected failure", err)
	}
	if steps := w.steps(t, run.ID); len(steps) != 3 || steps[2].State != "PREPARING" {
		t.Errorf("transitions %+v after a failed move", steps)
	}
}

// TestAccLifecycleStoreUnavailable fails each lifecycle store method on a
// closed pool.
func TestAccLifecycleStoreUnavailable(t *testing.T) {
	db := pool(t)
	s := pg.New(db, nil)
	db.Close()
	ctx := context.Background()
	acct, id := kernel.NewAccountID(), kernel.NewULID()
	run := lifecycle.Run{Account: acct, ID: id, Stack: id}
	step := lifecycle.Step{Sequence: 2, State: kernel.RunStateReady}
	_, lockErr := s.Lock(ctx, acct, id)
	_, _, activeErr := s.Active(ctx, acct, id, kernel.RunTypeTracked, "abc", "")
	_, unlockErr := s.Unlock(ctx, acct, id)
	_, _, projErr := s.Projection(ctx, acct, id, kernel.StackStates())
	_, historyErr := s.Histories(ctx, acct, []kernel.ULID{id})
	_, getErr := s.Get(ctx, acct, id)
	_, _, stackErr := s.StackOf(ctx, acct, id)
	_, _, holderErr := s.Holder(ctx, acct, id)
	_, listErr := s.List(ctx, acct, id, kernel.RunTypes(), read.Query{Limit: 1})
	for name, err := range map[string]error{
		"Lock": lockErr, "Move": s.Move(ctx, run, step), "Insert": s.Insert(ctx, run, step),
		"Active": activeErr, "Track": s.Track(ctx, run), "Unlock": unlockErr,
		"Projection": projErr, "Histories": historyErr, "Get": getErr, "StackOf": stackErr,
		"Holder": holderErr, "List": listErr, "SetPriority": s.SetPriority(ctx, run, "high", time.Now()),
	} {
		if err == nil {
			t.Errorf("%s on a closed pool succeeded", name)
		}
	}
}
