package pg_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/control"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// fakeAccess holds held on every space and reaches reach, the account's
// tree being tree; err fails the read its key names.
type fakeAccess struct {
	accessapi.Authorizer
	accessapi.Spaces
	held  []accessapi.Action
	reach []kernel.ULID
	tree  []accessapi.SpaceNode
	err   map[string]error
}

func (f *fakeAccess) Actions(
	context.Context,
	kernel.Principal,
	kernel.ULID,
) ([]accessapi.Action, error) {
	return f.held, f.err["Actions"]
}

func (f *fakeAccess) Authorize(
	_ context.Context,
	_ kernel.Principal,
	a accessapi.Action,
	_ kernel.ULID,
) (bool, error) {
	return slices.Contains(f.held, a), f.err["Authorize"]
}

func (f *fakeAccess) Reachable(
	context.Context,
	kernel.Principal,
	accessapi.Action,
) ([]kernel.ULID, error) {
	return f.reach, f.err["Reachable"]
}

func (f *fakeAccess) Tree(context.Context, kernel.AccountID) ([]accessapi.SpaceNode, error) {
	return f.tree, f.err["Tree"]
}

func (f *fakeStacks) InSpaces(
	_ context.Context,
	_ kernel.AccountID,
	spaces []kernel.ULID,
) ([]stacksapi.Stack, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err["InSpaces"]; err != nil {
		return nil, err
	}
	var out []stacksapi.Stack
	for _, st := range f.settings {
		if slices.Contains(spaces, st.Space) {
			out = append(out, st.Stack)
		}
	}
	return out, nil
}

// writer is what a space writer holds, which cancels a blocking run.
var writer = []accessapi.Action{
	accessapi.ActionSpaceRead, accessapi.ActionRunCancel, accessapi.ActionRunCancelBlocking,
	accessapi.ActionRunDiscard, accessapi.ActionRunPrioritizeSet,
}

// access is w's access domain: a writer on the stack's space, its only
// space.
func (w *world) access() *fakeAccess {
	space := w.stacks.settings[w.stack].Space
	return &fakeAccess{
		held: writer, reach: []kernel.ULID{space},
		tree: []accessapi.SpaceNode{{ID: space, Slug: "legacy", Name: "Legacy"}},
		err:  map[string]error{},
	}
}

// reader is a read service of w's over a.
func (w *world) reader(a *fakeAccess) *read.Service {
	return read.New(read.Deps{Store: w.store, Stacks: w.stacks, Spaces: a, Authz: a})
}

// controller is a control service of w's over a.
func (w *world) controller(a *fakeAccess) *control.Service {
	return control.New(control.Deps{
		DB: w.db, Store: w.store, Stacks: w.stacks, Authz: a, Lifecycle: w.lc,
		Views: w.reader(a), Now: func() time.Time { return w.now },
	})
}

func ids(vs []read.View) []kernel.ULID {
	out := make([]kernel.ULID, len(vs))
	for i, v := range vs {
		out[i] = v.Run.ID
	}
	return out
}

// TestAccRead reads a run by its ID, alone and on its stack, with its
// stack, branch, transitions and allowed actions; pages each of the
// stack's lists newest first; and answers a run another stack's, or none,
// as not found.
func TestAccRead(t *testing.T) {
	w := newWorld(t)
	a := w.access()
	r := w.reader(a)
	ctx, p := t.Context(), w.alice()
	tracked := mustCreate(t, w, kernel.RunTypeTracked, "a")
	destroy := mustCreate(t, w, kernel.RunTypeDestroy, "b")
	proposed := mustCreate(t, w, kernel.RunTypeProposed, "c")
	task := mustCreate(t, w, kernel.RunTypeTask, "d")
	w.mustMove(t, tracked, kernel.RunStateCanceled, machine.DriverAPI)

	v, err := r.Run(ctx, p, tracked.ID.String())
	if err != nil || v.Stack.Slug != "network" || v.Run.State != kernel.RunStateCanceled ||
		len(v.History) != 2 || v.History[1].State != kernel.RunStateCanceled ||
		len(v.Actions) != 0 {
		t.Errorf("Run = %+v, %v", v, err)
	}
	if v, err = r.StackRun(ctx, p, "network", proposed.ID.String()); err != nil ||
		!slices.Equal(v.Actions, []accessapi.Action{
			accessapi.ActionRunDiscard, accessapi.ActionRunCancel, accessapi.ActionRunPrioritizeSet,
		}) {
		t.Errorf("StackRun = %+v, %v", v.Actions, err)
	}
	for _, raw := range []string{"nope", kernel.NewULID().String()} {
		if _, err := r.Run(ctx, p, raw); !errors.Is(err, read.ErrNotFound) {
			t.Errorf("Run %s = %v", raw, err)
		}
	}
	w.stacks.settings[kernel.NewULID()] = stacksapi.Settings{Stack: stacksapi.Stack{Slug: "other"}}
	if _, err := r.StackRun(ctx, p, "other", task.ID.String()); !errors.Is(err, read.ErrNotFound) {
		t.Errorf("StackRun of another stack's run = %v", err)
	}
	if _, err := r.StackRun(
		ctx,
		p,
		"gone",
		task.ID.String(),
	); !errors.Is(
		err,
		stacksapi.ErrNotFound,
	) {
		t.Errorf("StackRun on no stack = %v", err)
	}
	if _, err := r.StackRun(ctx, p, "network", "nope"); !errors.Is(err, read.ErrNotFound) {
		t.Errorf("StackRun of no run ID = %v", err)
	}

	for _, c := range []struct {
		list read.List
		want []kernel.ULID
	}{
		{read.ListRuns, []kernel.ULID{destroy.ID, tracked.ID}},
		{read.ListProposed, []kernel.ULID{proposed.ID}},
		{read.ListTasks, []kernel.ULID{task.ID}},
		{read.ListInRange, []kernel.ULID{task.ID, proposed.ID, destroy.ID, tracked.ID}},
	} {
		q := read.Query{Limit: 1}
		if c.list == read.ListInRange {
			q.From, q.To = w.now.Add(-time.Hour), w.now.Add(time.Hour)
		}
		var got []kernel.ULID
		for {
			pg, err := r.StackRuns(ctx, p, "network", c.list, q)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, ids(pg.Views)...)
			if !pg.More {
				break
			}
			q.After = &pg.Views[0].Run.ID
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s = %v, want %v", c.list, got, c.want)
		}
	}
	pg, err := r.StackRuns(ctx, p, "network", read.ListInRange, read.Query{
		Limit: 10, From: w.now.Add(time.Hour), To: w.now.Add(2 * time.Hour),
	})
	if err != nil || len(pg.Views) != 0 {
		t.Errorf("a range with no run = %v, %v", ids(pg.Views), err)
	}
	pg, err = r.StackRuns(
		ctx,
		p,
		"network",
		read.ListRuns,
		read.Query{Limit: 10, Before: &destroy.ID},
	)
	if err != nil || !slices.Equal(ids(pg.Views), []kernel.ULID{tracked.ID}) {
		t.Errorf("runs before the destroy run = %v, %v", ids(pg.Views), err)
	}
	if _, err := r.StackRuns(ctx, p, "gone", read.ListRuns, read.Query{Limit: 1}); !errors.Is(
		err, stacksapi.ErrNotFound) {
		t.Errorf("StackRuns on no stack = %v", err)
	}

	space, err := r.SpaceOf(ctx, w.acct, task.ID.String())
	if err != nil || space != w.stacks.settings[w.stack].Space {
		t.Errorf("SpaceOf = %v, %v", space, err)
	}
	if stack, err := r.StackOf(ctx, w.acct, task.ID); err != nil || stack != w.stack {
		t.Errorf("StackOf = %v, %v", stack, err)
	}
	for _, raw := range []string{"nope", kernel.NewULID().String()} {
		if _, err := r.SpaceOf(ctx, w.acct, raw); !errors.Is(err, accessapi.ErrNoSubject) {
			t.Errorf("SpaceOf %s = %v", raw, err)
		}
	}
	w.stacks.err["Get"] = stacksapi.ErrNotFound
	if _, err := r.SpaceOf(ctx, w.acct, task.ID.String()); !errors.Is(err, accessapi.ErrNoSubject) {
		t.Errorf("SpaceOf a run of a gone stack = %v", err)
	}
}

func mustCreate(t *testing.T, w *world, typ kernel.RunType, hash string) lifecycle.Run {
	t.Helper()
	r, _, err := w.create(t, typ, hash)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestAccReadFailures fails each read the views and the search make in
// turn, and each failure reaches the caller.
func TestAccReadFailures(t *testing.T) {
	w := newWorld(t)
	run := mustCreate(t, w, kernel.RunTypeTracked, "a")
	ctx, p := t.Context(), w.alice()
	for _, key := range []string{"Settings", "Actions"} {
		a := w.access()
		a.err[key] = errInjected
		w.stacks.err[key] = errInjected
		if _, err := w.reader(a).Run(ctx, p, run.ID.String()); !errors.Is(err, errInjected) {
			t.Errorf("Run failing %s = %v", key, err)
		}
		delete(w.stacks.err, key)
	}
	for _, key := range []string{"Reachable", "Tree", "InSpaces"} {
		a := w.access()
		a.err[key] = errInjected
		w.stacks.err[key] = errInjected
		r := w.reader(a)
		if _, err := r.Search(ctx, p, search.Input{}); !errors.Is(err, errInjected) {
			t.Errorf("Search failing %s = %v", key, err)
		}
		if _, err := r.Suggest(ctx, p, search.SuggestionsInput{}); !errors.Is(err, errInjected) {
			t.Errorf("Suggest failing %s = %v", key, err)
		}
		delete(w.stacks.err, key)
	}
	r := w.reader(w.access())
	bad := search.Input{Predicates: []search.Predicate{{Field: "nope"}}}
	if _, err := r.Search(ctx, p, bad); !errors.As(err, new(*kernel.Invalid)) {
		t.Errorf("Search of an unknown field = %v", err)
	}
	w.db.Close()
	if _, err := r.Run(ctx, p, run.ID.String()); err == nil {
		t.Error("Run on a closed pool succeeded")
	}
	if _, err := r.StackRuns(ctx, p, "network", read.ListRuns, read.Query{Limit: 1}); err == nil {
		t.Error("StackRuns on a closed pool succeeded")
	}
	for name, err := range map[string]error{
		"Search":  second(r.Search(ctx, p, search.Input{})),
		"Count":   second(w.store.Count(ctx, w.acct, search.Input{}, read.Scope{})),
		"Suggest": second(r.Suggest(ctx, p, search.SuggestionsInput{})),
	} {
		if err == nil {
			t.Errorf("%s on a closed pool succeeded", name)
		}
	}
}

func second[T any](_ T, err error) error { return err }

// TestAccSearch searches the runs of the stacks in reachable spaces,
// newest first, by a filter and from an offset, with the total; leaves a
// stack in another space out; and suggests the values the runs hold.
func TestAccSearch(t *testing.T) {
	w := newWorld(t)
	a := w.access()
	r := w.reader(a)
	ctx, p := t.Context(), w.alice()
	// Each run is created a second after the one before, so newest first
	// is the reverse of their creation.
	later := func(typ kernel.RunType, hash string) lifecycle.Run {
		w.now = w.now.Add(time.Second)
		return mustCreate(t, w, typ, hash)
	}
	first := later(kernel.RunTypeTracked, "a")
	second := later(kernel.RunTypeProposed, "b")
	third := later(kernel.RunTypeProposed, "c")
	hidden := kernel.NewULID()
	w.stacks.settings[hidden] = stacksapi.Settings{Stack: stacksapi.Stack{
		ID: hidden, Account: w.acct, Slug: "hidden", Space: kernel.NewULID(),
	}}
	w.stack = hidden
	mustCreate(t, w, kernel.RunTypeTracked, "e")

	found, err := r.Search(ctx, p, search.Input{})
	if err != nil || found.Total != 3 || len(found.Result.Edges) != 3 ||
		found.Result.Edges[0].Node != (read.Hit{Run: third.ID, Stack: "network"}) ||
		found.Result.Edges[2].Node.Run != first.ID {
		t.Errorf("Search = %+v, %v", found, err)
	}
	found, err = r.Search(
		ctx,
		p,
		search.Input{First: new(int32(1)), Offset: 1, Predicates: []search.Predicate{{
			Field: "type", Constraint: search.Constraint{EnumEquals: []string{"PROPOSED"}},
		}}},
	)
	if err != nil || found.Total != 2 || len(found.Result.Edges) != 1 ||
		found.Result.Edges[0].Node.Run != second.ID || !found.Result.HasPrevious {
		t.Errorf("Search of proposed runs from 1 = %+v, %v", found, err)
	}
	found, err = r.Search(ctx, p, search.Input{FullText: "netw", OrderBy: []search.Order{
		{Field: "state"}, {Field: "stack"}, {Field: "triggeredBy"},
	}})
	if err != nil || found.Total != 3 {
		t.Errorf("Search of the stack's slug = %+v, %v", found, err)
	}
	got, err := r.Suggest(ctx, p, search.SuggestionsInput{Fields: []string{"type", "space"}})
	if err != nil || got.Count != 3 {
		t.Fatalf("Suggest = %+v, %v", got, err)
	}
	for _, f := range got.Fields {
		switch f.Field.Name {
		case "type":
			if !slices.Equal(f.Values, []string{"PROPOSED", "TRACKED"}) ||
				!slices.Equal(f.Counts, []int{2, 1}) {
				t.Errorf("type suggestions = %v %v", f.Values, f.Counts)
			}
		case "space":
			if len(f.Nodes) != 1 || f.Nodes[0].ID != "legacy" {
				t.Errorf("space suggestions = %+v", f.Nodes)
			}
		}
	}
	a.reach = nil
	if found, err = r.Search(ctx, p, search.Input{}); err != nil || found.Total != 0 ||
		len(found.Result.Edges) != 0 {
		t.Errorf("Search reaching no space = %+v, %v", found, err)
	}
}

// TestAccCancel cancels a queued run, and the ready run that holds the
// stack's lock, which releases the lock, only for a caller that may
// cancel a blocking run; refuses a run that has ended, one of another
// stack and one that does not exist; and sets a priority until the run
// ends.
func TestAccCancel(t *testing.T) {
	w := newWorld(t)
	a := w.access()
	c := w.controller(a)
	ctx, p := t.Context(), w.alice()
	queued := mustCreate(t, w, kernel.RunTypeProposed, "a")
	v, err := c.Cancel(ctx, p, "network", queued.ID.String(), "not now")
	if err != nil || v.Run.State != kernel.RunStateCanceled || len(v.Actions) != 0 ||
		*v.History[1].Note != "not now" {
		t.Errorf("Cancel of a queued run = %+v, %v", v, err)
	}
	if _, err := c.Cancel(
		ctx,
		p,
		"network",
		queued.ID.String(),
		"",
	); !errors.Is(
		err,
		control.ErrState,
	) {
		t.Errorf("Cancel of a canceled run = %v", err)
	}
	if _, err := c.SetPriority(
		ctx,
		p,
		"network",
		queued.ID.String(),
		kernel.RunPriorityHigh,
	); !errors.Is(
		err,
		control.ErrState,
	) {
		t.Errorf("SetPriority of a canceled run = %v", err)
	}

	blocking := mustCreate(t, w, kernel.RunTypeTracked, "b")
	w.takeLock(t, blocking)
	blocking = w.mustMove(t, blocking, kernel.RunStateReady, machine.DriverScheduler,
		machine.GuardStackLockFree, machine.GuardApproved, machine.GuardUpstreamFinished,
		machine.GuardExternalSettled, machine.GuardStackEnabled)
	v, err = c.SetPriority(ctx, p, "network", blocking.ID.String(), kernel.RunPriorityHigh)
	if err != nil || v.Run.Priority != kernel.RunPriorityHigh ||
		!slices.Contains(v.Actions, accessapi.ActionRunCancelBlocking) {
		t.Errorf("SetPriority = %+v, %v", v, err)
	}
	if got, err := w.reader(a).Run(ctx, p, blocking.ID.String()); err != nil ||
		got.Run.Priority != kernel.RunPriorityHigh {
		t.Errorf("the priority read back = %v, %v", got.Run.Priority, err)
	}
	a.held = []accessapi.Action{accessapi.ActionRunCancel}
	if _, err := c.Cancel(
		ctx,
		p,
		"network",
		blocking.ID.String(),
		"",
	); !errors.Is(
		err,
		control.ErrBlocking,
	) {
		t.Errorf("Cancel of the blocking run without RUN_CANCEL_BLOCKING = %v", err)
	}
	a.held = writer
	if v, err = c.Cancel(ctx, p, "network", blocking.ID.String(), ""); err != nil ||
		v.Run.State != kernel.RunStateCanceled {
		t.Errorf("Cancel of the blocking run = %+v, %v", v, err)
	}
	if _, held, err := w.store.Holder(ctx, w.acct, w.stack); err != nil || held {
		t.Errorf("the lock after the cancel: held %v, %v", held, err)
	}

	w.stacks.settings[kernel.NewULID()] = stacksapi.Settings{Stack: stacksapi.Stack{Slug: "other"}}
	for _, c2 := range []struct{ slug, raw string }{
		{"other", blocking.ID.String()}, {"network", kernel.NewULID().String()}, {"network", "nope"},
	} {
		if _, err := c.Cancel(ctx, p, c2.slug, c2.raw, ""); !errors.Is(err, read.ErrNotFound) {
			t.Errorf("Cancel of %s on %s = %v", c2.raw, c2.slug, err)
		}
	}
	if _, err := c.Cancel(
		ctx,
		p,
		"gone",
		blocking.ID.String(),
		"",
	); !errors.Is(
		err,
		stacksapi.ErrNotFound,
	) {
		t.Errorf("Cancel on no stack = %v", err)
	}
	locked := mustCreate(t, w, kernel.RunTypeTracked, "c")
	w.takeLock(t, locked)
	a.err["Authorize"] = errInjected
	if _, err := c.Cancel(ctx, p, "network", locked.ID.String(), ""); !errors.Is(err, errInjected) {
		t.Errorf("Cancel failing its authorization = %v", err)
	}
	w.db.Close()
	if _, err := c.Cancel(ctx, p, "network", locked.ID.String(), ""); err == nil {
		t.Error("Cancel on a closed pool succeeded")
	}
}
