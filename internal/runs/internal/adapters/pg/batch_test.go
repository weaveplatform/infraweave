package pg_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/trigger"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// tracked is a SetTracked call the fake stacks recorded.
type tracked struct {
	stack  kernel.ULID
	commit stacksapi.Commit
	setBy  string
}

func (f *fakeStacks) SetTracked(_ context.Context, _ kernel.AccountID, id kernel.ULID, head, commit *stacksapi.Commit, setBy string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err["SetTracked"]; err != nil {
		return err
	}
	if head != nil || commit == nil {
		return errors.New("a batch sets the tracked commit alone")
	}
	f.tracked = append(f.tracked, tracked{id, *commit, setBy})
	st := f.settings[id]
	st.TrackedCommit = commit
	f.settings[id] = st
	return nil
}

// batchWorld is w with more stacks, by slug: storage, beside network in
// its space; hidden, in a space alice cannot read; readonly, in one she
// may not trigger runs in; off, disabled; going, deleting; held, locked by
// bob; and bare, with no commit. Each but bare tracks a commit; alice holds RUN_TRIGGER.
func batchWorld(t *testing.T) (*world, *trigger.Service, map[string]kernel.ULID) {
	t.Helper()
	w := newWorld(t)
	network := w.stacks.settings[w.stack]
	network.Branch, network.TrackedCommit = "main", &stacksapi.Commit{Hash: "tracked"}
	w.stacks.settings[w.stack] = network
	ids := map[string]kernel.ULID{"network": w.stack}
	hidden, readonly := kernel.NewULID(), kernel.NewULID()
	for _, slug := range []string{"storage", "hidden", "readonly", "off", "going", "held", "bare"} {
		st := stacksapi.Settings{
			Stack: stacksapi.Stack{
				ID: kernel.NewULID(), Account: w.acct, Slug: slug, Space: network.Space,
			},
			Branch: "main", TrackedCommit: &stacksapi.Commit{Hash: slug + "-tracked"},
		}
		switch slug {
		case "hidden":
			st.Space = hidden
		case "readonly":
			st.Space = readonly
		case "off":
			st.Disabled = true
		case "going":
			st.Deleting = true
		case "held":
			st.Lock = &stacksapi.Lock{Holder: "user:bob", By: "bob"}
		case "bare":
			st.TrackedCommit = nil
		}
		w.stacks.settings[st.ID] = st
		ids[slug] = st.ID
	}
	w.sources.commits = map[string]stacksapi.Commit{"named": {Hash: "named", Message: "Named"}}
	a := w.access()
	a.held = append(slices.Clone(writer), accessapi.ActionRunTrigger)
	a.withheld = map[kernel.ULID][]accessapi.Action{
		hidden:   {accessapi.ActionSpaceRead},
		readonly: {accessapi.ActionRunTrigger},
	}
	s := trigger.New(trigger.Deps{
		DB: w.db, Stacks: w.stacks, Authz: a, Lifecycle: w.lc, Views: w.reader(a),
	})
	s.BindSources(w.sources)
	return w, s, ids
}

// TestAccTriggerBatch creates a tracked run on each stack of a batch, in
// its order, with the force-apply mode, a named commit becoming its
// stack's tracked commit set by the caller, and answers a stack's run of
// the same commit again; and refuses a whole batch with every entry's
// refusal at its pointer, creating no run.
func TestAccTriggerBatch(t *testing.T) {
	w, s, ids := batchWorld(t)
	views, err := s.Batch(t.Context(), w.alice(), []trigger.BatchStack{
		{Stack: "storage", CommitSHA: "named"}, {Stack: "network"},
	}, "SINGLE")
	if err != nil || len(views) != 2 {
		t.Fatalf("a batch = %d views, %v", len(views), err)
	}
	for i, want := range []struct {
		stack kernel.ULID
		hash  string
	}{{ids["storage"], "named"}, {ids["network"], "tracked"}} {
		r := views[i].Run
		if r.Stack != want.stack || r.Type != kernel.RunTypeTracked || r.Commit.Hash != want.hash ||
			r.Commit.Branch != "main" || r.ForceApply != "SINGLE" || r.TriggeredBy != "alice" {
			t.Errorf("run %d = %+v, want a tracked run of %s on %s", i, r, want.stack, want.hash)
		}
	}
	if got := w.stacks.tracked; len(got) != 1 || got[0].stack != ids["storage"] ||
		got[0].commit.Hash != "named" || got[0].commit.Branch != "" || got[0].setBy != "alice" {
		t.Errorf("tracked commits set = %+v", got)
	}
	again, err := s.Batch(t.Context(), w.alice(), []trigger.BatchStack{{Stack: "network"}}, "")
	if err != nil || len(again) != 1 || again[0].Run.ID != views[1].Run.ID {
		t.Errorf("the batch again = %v, %v; want run %s", again, err, views[1].Run.ID)
	}
	runs := count(t, w.db, "runs", w.acct)

	for name, c := range map[string]struct {
		stacks []trigger.BatchStack
		want   []kernel.FieldError
	}{
		"stacks that cannot be read, triggered or named twice": {
			[]trigger.BatchStack{
				{Stack: "network"},
				{Stack: "gone"},
				{Stack: "hidden"},
				{Stack: "readonly"},
				{Stack: "network"},
			},
			[]kernel.FieldError{
				{Pointer: "/stacks/1/stack", Reason: kernel.ReasonNotFound},
				{Pointer: "/stacks/2/stack", Reason: kernel.ReasonNotFound},
				{Pointer: "/stacks/3/stack", Reason: kernel.ReasonRule},
				{Pointer: "/stacks/4/stack", Reason: kernel.ReasonDuplicate},
			},
		},
		"stacks that cannot run, and an unknown commit": {
			[]trigger.BatchStack{
				{Stack: "network"},
				{Stack: "off"},
				{Stack: "going"},
				{Stack: "held"},
				{Stack: "bare"},
				{Stack: "storage", CommitSHA: "gone"},
			},
			[]kernel.FieldError{
				{Pointer: "/stacks/1/stack", Reason: kernel.ReasonRule},
				{Pointer: "/stacks/2/stack", Reason: kernel.ReasonRule},
				{Pointer: "/stacks/3/stack", Reason: kernel.ReasonRule},
				{Pointer: "/stacks/4/stack", Reason: kernel.ReasonRule},
				{Pointer: "/stacks/5/commitSha", Reason: kernel.ReasonNotFound},
			},
		},
		"no stacks": {nil, []kernel.FieldError{{Pointer: "/stacks", Reason: kernel.ReasonMinLength}}},
		"too many stacks": {
			make([]trigger.BatchStack, trigger.MaxBatch+1),
			[]kernel.FieldError{{Pointer: "/stacks", Reason: kernel.ReasonMaxLength}},
		},
	} {
		_, err := s.Batch(t.Context(), w.alice(), c.stacks, "")
		inv, ok := errors.AsType[*kernel.Invalid](err)
		if !ok || len(inv.Errors) != len(c.want) {
			t.Errorf("%s = %v, want %d refusals", name, err, len(c.want))
			continue
		}
		for i, e := range inv.Errors {
			if e.Pointer != c.want[i].Pointer || e.Reason != c.want[i].Reason || e.Message == "" {
				t.Errorf("%s: refusal %d = %+v, want %+v", name, i, e, c.want[i])
			}
		}
	}
	if n := count(t, w.db, "runs", w.acct); n != runs {
		t.Errorf("%d runs after refused batches, want %d", n, runs)
	}
}

// TestAccTriggerBatchBroken fails a batch on each read or write that
// fails, and on a named commit while no source is bound.
func TestAccTriggerBatchBroken(t *testing.T) {
	w, s, _ := batchWorld(t)
	for _, method := range []string{"LockForRun", "SetTracked"} {
		w.stacks.err[method] = errInjected
		if _, err := s.Batch(t.Context(), w.alice(), []trigger.BatchStack{
			{Stack: "network", CommitSHA: "named"},
		}, ""); !errors.Is(err, errInjected) {
			t.Errorf("a failed %s = %v", method, err)
		}
		delete(w.stacks.err, method)
	}
	a := w.access()
	a.held = append(a.held, accessapi.ActionRunTrigger)
	unbound := trigger.New(trigger.Deps{
		DB: w.db, Stacks: w.stacks, Authz: a, Lifecycle: w.lc, Views: w.reader(a),
	})
	if _, err := unbound.Batch(t.Context(), w.alice(), []trigger.BatchStack{
		{Stack: "network", CommitSHA: "named"},
	}, ""); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("a named commit with no source bound = %v", err)
	}
	a.err["Authorize"] = errInjected
	if _, err := unbound.Batch(t.Context(), w.alice(), []trigger.BatchStack{
		{Stack: "network"},
	}, ""); !errors.Is(err, errInjected) {
		t.Errorf("a failed decision = %v", err)
	}
}

// TestAccTriggerDestroy creates a stack's DESTROY run as the caller in
// the caller's transaction, on its tracked commit, else its branch head;
// it refuses a disabled stack, one another caller locked, naming the
// holder, and one with no commit with a *stacksapi.Undestroyable, creating
// no run, and fails on a
// read that fails.
func TestAccTriggerDestroy(t *testing.T) {
	w, s, ids := batchWorld(t)
	destroy := func(slug string) error {
		return w.in(
			t,
			func(ctx context.Context) error { return s.Destroy(ctx, w.alice(), ids[slug]) },
		)
	}
	created := func(slug, hash string) {
		t.Helper()
		if err := w.in(t, func(ctx context.Context) error {
			r, ok, err := w.store.Active(ctx, w.acct, ids[slug], kernel.RunTypeDestroy, hash, "")
			if err == nil && (!ok || r.TriggeredBy != "alice" || r.Commit.Branch != "main") {
				t.Errorf("the destroy run of %s on %s = %+v, %v", slug, hash, r, ok)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := destroy("storage"); err != nil {
		t.Fatal(err)
	}
	created("storage", "storage-tracked")

	runs := count(t, w.db, "runs", w.acct)
	for slug, by := range map[string]string{"off": "", "held": "bob", "bare": ""} {
		err := destroy(slug)
		if u, ok := errors.AsType[*stacksapi.Undestroyable](err); !ok || u.LockedBy != by {
			t.Errorf("the destroy of %s = %v, want Undestroyable locked by %q", slug, err, by)
		}
	}
	if n := count(t, w.db, "runs", w.acct); n != runs {
		t.Errorf("%d runs after refused destroys, want %d", n, runs)
	}
	if err := destroy("going"); !errors.Is(err, lifecycle.ErrDeleting) {
		t.Errorf("the destroy of a deleting stack = %v", err)
	}

	bare := w.stacks.settings[ids["bare"]]
	bare.TrackedBranchHead = &stacksapi.Commit{Hash: "head"}
	w.stacks.settings[ids["bare"]] = bare
	if err := destroy("bare"); err != nil {
		t.Fatal(err)
	}
	created("bare", "head")

	w.stacks.err["Settings"] = errInjected
	if err := destroy("network"); !errors.Is(err, errInjected) {
		t.Errorf("a failed read = %v", err)
	}
}
