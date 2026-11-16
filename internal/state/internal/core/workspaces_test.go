package core

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func (m *mem) Get(_ context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Stack, error) {
	return m.stack(func(st stacksapi.Stack) bool { return st.Account == acct && st.ID == id }, "Get")
}

func (m *mem) BySlug(_ context.Context, acct kernel.AccountID, slug string) (stacksapi.Stack, error) {
	return m.stack(func(st stacksapi.Stack) bool { return st.Account == acct && st.Slug == slug }, "BySlug")
}

func (m *mem) stack(match func(stacksapi.Stack) bool, method string) (stacksapi.Stack, error) {
	if err := m.failing(method); err != nil {
		return stacksapi.Stack{}, err
	}
	if i := slices.IndexFunc(m.stacks, match); i >= 0 {
		return m.stacks[i], nil
	}
	return stacksapi.Stack{}, stacksapi.ErrNotFound
}

func (m *mem) InSpaces(_ context.Context, acct kernel.AccountID, spaces []kernel.ULID) ([]stacksapi.Stack, error) {
	var out []stacksapi.Stack
	for _, st := range m.stacks {
		if st.Account == acct && slices.Contains(spaces, st.Space) {
			out = append(out, st)
		}
	}
	return out, m.failing("InSpaces")
}

// view is the workspace id names as the store answers it, with the lock
// that holds it.
func (m *mem) view(id kernel.ULID, t Target) Workspace {
	w := Workspace{ID: id, Stack: t.Stack, Name: t.Workspace}
	if held := m.locks[t]; len(held) > 0 {
		w.Locked, w.TriggeredBy = true, holder(held).TriggeredBy
	}
	return w
}

func (m *mem) WorkspaceByID(_ context.Context, acct kernel.AccountID, id kernel.ULID) (Workspace, bool, error) {
	t, ok := m.named[id]
	if !ok || t.Account != acct {
		return Workspace{}, false, m.failing("WorkspaceByID")
	}
	return m.view(id, t), true, m.failing("WorkspaceByID")
}

func (m *mem) LockStackWorkspaces(_ context.Context, acct kernel.AccountID, stack kernel.ULID) ([]Workspace, error) {
	var out []Workspace
	for id, t := range m.named {
		if t.Account == acct && t.Stack == stack {
			out = append(out, Workspace{ID: id, Stack: stack, Name: t.Workspace})
		}
	}
	slices.SortFunc(out, func(a, b Workspace) int { return strings.Compare(a.Name, b.Name) })
	return out, m.failing("LockStackWorkspaces")
}

func (m *mem) SearchWorkspaces(_ context.Context, acct kernel.AccountID, in search.Input, scope WorkspaceScope) (search.Result[Workspace], error) {
	m.searched.in, m.searched.scope, m.searched.account = in, scope, acct
	var out search.Result[Workspace]
	for id, t := range m.named {
		if slices.ContainsFunc(scope.Stacks, func(st stacksapi.Stack) bool { return st.ID == t.Stack }) {
			out.Edges = append(out.Edges, search.Edge[Workspace]{Cursor: id.String(), Node: m.view(id, t)})
		}
	}
	return out, m.failing("SearchWorkspaces")
}

func (m *mem) SuggestWorkspaces(_ context.Context, acct kernel.AccountID, in search.SuggestionsInput, scope WorkspaceScope) (search.Suggestions, error) {
	m.searched.in, m.searched.scope, m.searched.account = in, scope, acct
	return search.Suggestions{Count: len(scope.Stacks)}, m.failing("SuggestWorkspaces")
}

// reach is an Authorizer that answers spaces, or err, recording the
// action it was asked.
type reach struct {
	spaces []kernel.ULID
	err    error
	asked  accessapi.Action
}

func (r *reach) Reachable(_ context.Context, _ kernel.Principal, action accessapi.Action) ([]kernel.ULID, error) {
	r.asked = action
	return r.spaces, r.err
}

// fixture is a service over a store with stack st in space, whose
// default workspace def is held by two planning runs that alice and bob
// triggered, whose workspace blue is free and whose workspace green is
// held by an applying run carol triggered.
type fixture struct {
	m              *mem
	s              *Service
	r              *reach
	st             stacksapi.Stack
	def, blue, grn kernel.ULID
}

func newFixture() fixture {
	m, r := newMem(), &reach{}
	f := fixture{
		m: m, r: r, s: New(Deps{DB: db{}, Store: m, Stacks: m, Authz: r}),
		st: stacksapi.Stack{
			ID: kernel.NewULID(), Account: kernel.NewAccountID(), Slug: "app", Space: kernel.NewULID(),
		},
		def: kernel.NewULID(), blue: kernel.NewULID(), grn: kernel.NewULID(),
	}
	r.spaces = []kernel.ULID{f.st.Space}
	m.stacks = []stacksapi.Stack{f.st, {
		ID: kernel.NewULID(), Account: f.st.Account, Slug: "hidden", Space: kernel.NewULID(),
	}}
	for id, name := range map[kernel.ULID]string{f.def: "", f.blue: "blue", f.grn: "green"} {
		t := f.target(name)
		m.named[id], m.workspaces[t] = t, true
	}
	planA, planB := lockOf("a", Shared, kernel.NewULID()), lockOf("b", Shared, kernel.NewULID())
	planA.TriggeredBy, planB.TriggeredBy = "alice", "bob"
	apply := lockOf("x", Exclusive, kernel.NewULID())
	apply.TriggeredBy = "carol"
	m.locks[f.target("")] = []Lock{planA, planB}
	m.locks[f.target("green")] = []Lock{apply}
	return f
}

func (f fixture) target(name string) Target {
	return Target{Account: f.st.Account, Stack: f.st.ID, Workspace: name}
}

// user is a member of the fixture's account who acts as login.
func (f fixture) user(login string) kernel.Principal {
	return kernel.Principal{
		Kind: kernel.PrincipalUser, AccountID: f.st.Account, ActorID: kernel.NewULID(), Login: login,
	}
}

// TestUnitWorkspaceView checks the derived fields a workspace answers:
// the default workspace's name, and lockedBy, absent while the workspace
// is free or its holding run names no one.
//
// ref: decision 0010 § "Storage"
func TestUnitWorkspaceView(t *testing.T) {
	for _, tc := range []struct {
		w           Workspace
		name, by    string
		hasLockedBy bool
	}{
		{w: Workspace{}, name: DefaultName},
		{
			w: Workspace{Name: "blue", Locked: true, TriggeredBy: "alice"}, name: "blue", by: "alice",
			hasLockedBy: true,
		},
		{w: Workspace{Name: "blue", TriggeredBy: "alice"}, name: "blue"},
		{w: Workspace{Locked: true}, name: DefaultName},
	} {
		got := tc.w.LockedBy()
		if tc.w.DisplayName() != tc.name || (got != nil) != tc.hasLockedBy ||
			(got != nil && *got != tc.by) {
			t.Errorf("%+v answers %q locked by %v", tc.w, tc.w.DisplayName(), got)
		}
	}
}

// TestUnitWorkspaceLockedBy checks that a workspace answers the run that
// holds it as a conflict names it: the exclusive lock's, else the oldest
// shared lock's.
func TestUnitWorkspaceLockedBy(t *testing.T) {
	f := newFixture()
	for id, want := range map[kernel.ULID]string{f.def: "alice", f.grn: "carol", f.blue: ""} {
		w, ok, err := f.m.WorkspaceByID(t.Context(), f.st.Account, id)
		if got := w.LockedBy(); err != nil || !ok || (got == nil) != (want == "") ||
			(got != nil && *got != want) {
			t.Errorf("%s locked by %v, want %q", w.Name, got, want)
		}
	}
}

// TestUnitUnlockWorkspace checks that releasing a workspace deletes every
// lock on it and records one entry naming the caller, the workspace and
// each LockInfo released; that a free workspace answers as it is and
// records nothing; that an in-run caller acts as its run; and that a
// workspace the account lacks, or no workspace ID, is ErrNotFound.
//
// ref: decision 0010 § "Locks"
func TestUnitUnlockWorkspace(t *testing.T) {
	f := newFixture()
	w, err := f.s.UnlockWorkspace(t.Context(), f.user("dave"), f.def.String())
	if err != nil || w.ID != f.def || w.Locked || w.LockedBy() != nil || w.DisplayName() != DefaultName {
		t.Fatalf("UnlockWorkspace = %+v, %v", w, err)
	}
	if held := f.m.locks[f.target("")]; len(held) != 0 {
		t.Errorf("%v outlive the release", ids(held))
	}
	if len(f.m.audits) != 1 {
		t.Fatalf("%d entries, want one", len(f.m.audits))
	}
	e := f.m.audits[0]
	meta := string(e.AffectedResource.Metadata)
	if e.AccountID != f.st.Account.ULID || e.Actor != (kernel.AuditActor{Username: "dave"}) ||
		e.Action != ActionBreakLock || e.EventType != "DELETE" ||
		e.AffectedResource.Type != ResourceStack || e.AffectedResource.ID != f.st.ID.String() ||
		!strings.Contains(meta, `"workspaceId":"`+f.def.String()+`"`) ||
		!strings.Contains(meta, `"workspace":""`) ||
		!strings.Contains(meta, `{"ID":"a"}`) || !strings.Contains(meta, `{"ID":"b"}`) {
		t.Errorf("the release recorded %+v with %s", e, meta)
	}

	again, err := f.s.UnlockWorkspace(t.Context(), f.user("dave"), f.def.String())
	if err != nil || again != w || len(f.m.audits) != 1 {
		t.Errorf("a free workspace = %+v, %v after %d entries", again, err, len(f.m.audits))
	}

	run := kernel.Principal{Kind: kernel.PrincipalRun, AccountID: f.st.Account, ActorID: kernel.NewULID()}
	if _, err := f.s.UnlockWorkspace(t.Context(), run, f.grn.String()); err != nil {
		t.Fatal(err)
	}
	if got := f.m.audits[len(f.m.audits)-1].Actor; got != (kernel.AuditActor{
		Username: Actor, Run: run.ActorID,
	}) {
		t.Errorf("an in-run release recorded %+v", got)
	}

	stranger := f.user("dave")
	stranger.AccountID = kernel.NewAccountID()
	for _, tc := range []struct {
		p   kernel.Principal
		raw string
	}{
		{f.user("dave"), kernel.NewULID().String()},
		{f.user("dave"), "not-an-id"},
		{stranger, f.blue.String()},
	} {
		if _, err := f.s.UnlockWorkspace(t.Context(), tc.p, tc.raw); !errors.Is(err, ErrNotFound) {
			t.Errorf("workspace %q of another account or none = %v", tc.raw, err)
		}
	}
	delete(f.m.workspaces, f.target("blue"))
	if _, err := f.s.UnlockWorkspace(t.Context(), f.user("dave"), f.blue.String()); !errors.Is(err,
		ErrNotFound) {
		t.Errorf("a workspace deleted after its read = %v", err)
	}
}

// TestUnitUnlockStack checks that releasing a stack's workspaces deletes
// every lock on each, records an entry for each one released, and answers
// those it unlocked by name, leaving the free one out; that a stack with
// none locked answers an empty list; and that a stack the account lacks
// is stacksapi.ErrNotFound.
//
// ref: decision 0010 § "Locks"
func TestUnitUnlockStack(t *testing.T) {
	f := newFixture()
	got, err := f.s.UnlockStack(t.Context(), f.user("dave"), "app")
	names := make([]string, len(got))
	for i, w := range got {
		names[i] = w.DisplayName()
		if w.Locked || w.LockedBy() != nil {
			t.Errorf("%s answers locked", names[i])
		}
	}
	if err != nil || !slices.Equal(names, []string{DefaultName, "green"}) {
		t.Fatalf("UnlockStack = %v, %v", names, err)
	}
	for _, name := range []string{"", "green"} {
		if held := f.m.locks[f.target(name)]; len(held) != 0 {
			t.Errorf("%v outlive the release of %q", ids(held), name)
		}
	}
	if len(f.m.audits) != 2 || !strings.Contains(string(f.m.audits[1].AffectedResource.Metadata),
		`"workspaceId":"`+f.grn.String()+`"`) {
		t.Errorf("the releases recorded %+v", f.m.audits)
	}

	again, err := f.s.UnlockStack(t.Context(), f.user("dave"), "app")
	if err != nil || again == nil || len(again) != 0 || len(f.m.audits) != 2 {
		t.Errorf("a stack with no lock = %v, %v", again, err)
	}
	if _, err := f.s.UnlockStack(t.Context(), f.user("dave"), "gone"); !errors.Is(err,
		stacksapi.ErrNotFound) {
		t.Errorf("a stack the account lacks = %v", err)
	}
}

// TestUnitWorkspaceSpaceOf checks that a workspace resolves to its
// stack's space, and that one the account lacks, no workspace ID, or a
// workspace whose stack is gone is accessapi.ErrNoSubject.
func TestUnitWorkspaceSpaceOf(t *testing.T) {
	f := newFixture()
	if got, err := f.s.SpaceOf(t.Context(), f.st.Account, f.blue.String()); err != nil ||
		got != f.st.Space {
		t.Errorf("SpaceOf = %s, %v, want %s", got, err, f.st.Space)
	}
	orphan := kernel.NewULID()
	f.m.named[orphan] = Target{Account: f.st.Account, Stack: kernel.NewULID()}
	for _, tc := range []struct {
		acct kernel.AccountID
		raw  string
	}{
		{f.st.Account, kernel.NewULID().String()},
		{f.st.Account, "not-an-id"},
		{kernel.NewAccountID(), f.blue.String()},
		{f.st.Account, orphan.String()},
	} {
		if _, err := f.s.SpaceOf(t.Context(), tc.acct, tc.raw); !errors.Is(err,
			accessapi.ErrNoSubject) {
			t.Errorf("SpaceOf(%q) = %v", tc.raw, err)
		}
	}
	for _, method := range []string{"WorkspaceByID", "Get"} {
		f.m.fail = method
		if _, err := f.s.SpaceOf(t.Context(), f.st.Account, f.blue.String()); !errors.Is(err,
			errDown) {
			t.Errorf("SpaceOf with %s failing = %v", method, err)
		}
	}
}

// TestUnitSearchWorkspaces checks that a search and a suggestion read the
// workspaces of the caller's account's stacks in the spaces it may read,
// as SPACE_READ reaches them, and that a suggestion lists the values of
// name and stack alone of the fields it asks for.
//
// ref: decision 0010 § "Rollback, searches and retention"
func TestUnitSearchWorkspaces(t *testing.T) {
	f := newFixture()
	p := f.user("dave")
	res, err := f.s.SearchWorkspaces(t.Context(), p, search.Input{FullText: "g"})
	if err != nil || len(res.Edges) != 3 || f.r.asked != accessapi.ActionSpaceRead ||
		f.m.searched.account != f.st.Account ||
		!slices.Equal(f.m.searched.scope.Reachable, []kernel.ULID{f.st.Space}) ||
		len(f.m.searched.scope.Stacks) != 1 || f.m.searched.scope.Stacks[0] != f.st ||
		f.m.searched.in.(search.Input).FullText != "g" {
		t.Errorf("SearchWorkspaces = %d edges, %v, over %+v", len(res.Edges), err, f.m.searched)
	}

	for _, tc := range []struct {
		asked, want []string
	}{
		{nil, []string{"name", "stack"}},
		{[]string{"isLocked"}, []string{}},
		{[]string{"stack", "isLocked", "other"}, []string{"stack"}},
	} {
		got, err := f.s.SuggestWorkspaces(t.Context(), p, search.SuggestionsInput{Fields: tc.asked})
		in, _ := f.m.searched.in.(search.SuggestionsInput)
		if err != nil || got.Count != 1 || in.Fields == nil || !slices.Equal(in.Fields, tc.want) {
			t.Errorf("SuggestWorkspaces(%v) = %+v, %v, listing %v", tc.asked, got, err, in.Fields)
		}
	}

	f.r.err = errDown
	if _, err := f.s.SearchWorkspaces(t.Context(), p, search.Input{}); !errors.Is(err, errDown) {
		t.Errorf("a search the snapshot fails = %v", err)
	}
	if _, err := f.s.SuggestWorkspaces(t.Context(), p, search.SuggestionsInput{}); !errors.Is(err,
		errDown) {
		t.Errorf("a suggestion the snapshot fails = %v", err)
	}
	f.r.err, f.m.fail = nil, "InSpaces"
	if _, err := f.s.SearchWorkspaces(t.Context(), p, search.Input{}); !errors.Is(err, errDown) {
		t.Errorf("a search the stacks fail = %v", err)
	}
}

// TestUnitWorkspacesDown checks that each failing method fails a release
// that reaches it, answering nothing.
func TestUnitWorkspacesDown(t *testing.T) {
	for _, method := range []string{"WorkspaceByID", "LockWorkspace", "Locks", "RemoveLocks", "Audit"} {
		f := newFixture()
		f.m.fail = method
		if w, err := f.s.UnlockWorkspace(t.Context(), f.user("dave"), f.def.String()); !errors.Is(err,
			errDown) || w != (Workspace{}) {
			t.Errorf("UnlockWorkspace with %s failing = %+v, %v", method, w, err)
		}
	}
	for _, method := range []string{"BySlug", "LockStackWorkspaces", "Locks", "RemoveLocks", "Audit"} {
		f := newFixture()
		f.m.fail = method
		if _, err := f.s.UnlockStack(t.Context(), f.user("dave"), "app"); !errors.Is(err, errDown) {
			t.Errorf("UnlockStack with %s failing = %v", method, err)
		}
	}
}
