package core

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

var errDown = errors.New("down")

// mem is an in-memory Store and Stacks: the workspaces that exist, each
// one's locks oldest first and current version, and the audit entries
// recorded. fail names the one method that fails with errDown.
type mem struct {
	workspaces map[Target]bool
	locks      map[Target][]Lock
	current    map[Target]Version
	audits     []kernel.AuditEntry
	fail       string
}

func newMem() *mem {
	return &mem{
		workspaces: map[Target]bool{},
		locks:      map[Target][]Lock{},
		current:    map[Target]Version{},
	}
}

func (m *mem) failing(method string) error {
	if m.fail == method {
		return errDown
	}
	return nil
}

func (m *mem) Hold(context.Context, kernel.AccountID, kernel.ULID) error {
	return m.failing("Hold")
}

func (m *mem) OpenWorkspace(
	_ context.Context,
	acct kernel.AccountID,
	stack kernel.ULID,
	name string,
) error {
	m.workspaces[Target{Account: acct, Stack: stack, Workspace: name}] = true
	return m.failing("OpenWorkspace")
}

func (m *mem) LockWorkspace(_ context.Context, t Target) (bool, error) {
	return m.workspaces[t], m.failing("LockWorkspace")
}

func (m *mem) Locks(_ context.Context, t Target) ([]Lock, error) {
	return slices.Clone(m.locks[t]), m.failing("Locks")
}

func (m *mem) AddLock(_ context.Context, t Target, l Lock) error {
	if err := m.failing("AddLock"); err != nil {
		return err
	}
	m.locks[t] = append(m.locks[t], l)
	return nil
}

func (m *mem) RemoveLocks(_ context.Context, t Target, ids []string) error {
	if err := m.failing("RemoveLocks"); err != nil {
		return err
	}
	m.locks[t] = slices.DeleteFunc(
		m.locks[t],
		func(l Lock) bool { return slices.Contains(ids, l.ID) },
	)
	return nil
}

func (m *mem) ReleaseRun(_ context.Context, acct kernel.AccountID, run kernel.ULID) error {
	if err := m.failing("ReleaseRun"); err != nil {
		return err
	}
	for t := range m.locks {
		if t.Account == acct {
			m.locks[t] = slices.DeleteFunc(m.locks[t], func(l Lock) bool { return l.Run == run })
		}
	}
	return nil
}

func (m *mem) Audit(_ context.Context, e kernel.AuditEntry) error {
	if err := m.failing("Audit"); err != nil {
		return err
	}
	m.audits = append(m.audits, e)
	return nil
}

func (m *mem) Current(_ context.Context, t Target) (Version, bool, error) {
	v, ok := m.current[t]
	return v, ok, m.failing("Current")
}

func (m *mem) AddVersion(_ context.Context, acct kernel.AccountID, v Version) error {
	if err := m.failing("AddVersion"); err != nil {
		return err
	}
	m.current[Target{Account: acct, Stack: v.Stack, Workspace: v.Workspace}] = v
	return nil
}

func (m *mem) Purge(context.Context, kernel.AccountID, *kernel.ULID, int) ([]string, bool, error) {
	return nil, true, m.failing("Purge")
}

// lockOf is a lock of run with id in mode, whose LockInfo names id.
func lockOf(id string, mode Mode, run kernel.ULID) Lock {
	return Lock{ID: id, Mode: mode, Run: run, Info: json.RawMessage(`{"ID":"` + id + `"}`)}
}

// ids are the IDs of locks, in order.
func ids(locks []Lock) []string {
	out := make([]string, 0, len(locks))
	for _, l := range locks {
		out = append(out, l.ID)
	}
	return out
}

// TestUnitLockConflicts checks every row of the table of taking a lock:
// nothing held grants either mode, shared locks grant another shared
// lock and refuse an exclusive one naming the oldest, an exclusive lock
// refuses either naming itself, and a run's retry of its lock with the
// same ID and mode is granted again while the same ID in another mode or
// of another run is refused.
//
// ref: decision 0010 § "Locks"
func TestUnitLockConflicts(t *testing.T) {
	a, b, c := kernel.NewULID(), kernel.NewULID(), kernel.NewULID()
	for _, tc := range []struct {
		name   string
		held   []Lock
		want   Lock
		holder string
		after  []string
	}{
		{"nothing held, shared", nil, lockOf("1", Shared, a), "", []string{"1"}},
		{"nothing held, exclusive", nil, lockOf("1", Exclusive, a), "", []string{"1"}},
		{
			"shared held, shared",
			[]Lock{lockOf("1", Shared, a)},
			lockOf("2", Shared, b), "",
			[]string{"1", "2"},
		},
		{
			"shared held, exclusive",
			[]Lock{lockOf("1", Shared, a), lockOf("2", Shared, b)},
			lockOf("3", Exclusive, c), "1",
			[]string{"1", "2"},
		},
		{
			"exclusive held, shared",
			[]Lock{lockOf("1", Exclusive, a)},
			lockOf("2", Shared, b),
			"1",
			[]string{"1"},
		},
		{
			"exclusive held, exclusive",
			[]Lock{lockOf("1", Exclusive, a)},
			lockOf("2", Exclusive, b), "1",
			[]string{"1"},
		},
		{
			"a retry",
			[]Lock{lockOf("1", Shared, b), lockOf("2", Exclusive, a)},
			lockOf("2", Exclusive, a), "",
			[]string{"1", "2"},
		},
		{
			"the same ID in another mode",
			[]Lock{lockOf("1", Shared, a)},
			lockOf("1", Exclusive, a), "1",
			[]string{"1"},
		},
		{
			"the same ID of another run",
			[]Lock{lockOf("1", Shared, a)},
			lockOf("1", Shared, b), "1",
			[]string{"1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMem()
			s := New(Deps{DB: db{}, Store: m, Stacks: m})
			at := Target{Account: kernel.NewAccountID(), Stack: kernel.NewULID()}
			m.locks[at] = slices.Clone(tc.held)
			err := s.Lock(t.Context(), at, tc.want)
			conflict, ok := errors.AsType[*Conflict](err)
			switch {
			case tc.holder == "" && err != nil:
				t.Errorf("Lock = %v, want it granted", err)
			case tc.holder != "" && (!ok || conflict.Holder.ID != tc.holder ||
				string(conflict.Holder.Info) != `{"ID":"`+tc.holder+`"}`):
				t.Errorf("Lock = %v, want a conflict with %s", err, tc.holder)
			case ok && !strings.Contains(err.Error(), conflict.Holder.Run.String()):
				t.Errorf("the conflict %q does not name its holder's run", err)
			}
			if got := ids(m.locks[at]); !slices.Equal(got, tc.after) {
				t.Errorf("held %v, want %v", got, tc.after)
			}
			if !m.workspaces[at] {
				t.Error("the workspace was not made to exist")
			}
		})
	}
}

// TestUnitUnlock checks releasing a run's own lock by its ID, a
// mismatched ID answering the holder, an ID nothing holds, a workspace no
// lock was taken on, and releasing another run's lock or every lock:
// allowed with Break and audited naming the caller, the workspace and
// each LockInfo of another run, refused with ErrBreak without it.
//
// ref: decision 0010 § "Locks"
func TestUnitUnlock(t *testing.T) {
	me, other := kernel.NewULID(), kernel.NewULID()
	for _, tc := range []struct {
		name     string
		held     []Lock
		id       string
		brk      bool
		err      error
		holder   string
		after    []string
		released []string
	}{
		{
			name: "my lock", held: []Lock{lockOf("1", Shared, me), lockOf("2", Shared, other)},
			id: "1", after: []string{"2"},
		},
		{
			name: "a mismatched ID", held: []Lock{lockOf("1", Shared, other), lockOf("2",
				Exclusive, me)},
			id: "3", holder: "2", after: []string{"1", "2"},
		},
		{name: "an ID nothing holds", id: "3", after: []string{}},
		{
			name: "another run's lock, breaking", held: []Lock{lockOf("1", Exclusive, other)},
			id: "1", brk: true, after: []string{}, released: []string{"1"},
		},
		{
			name: "another run's lock", held: []Lock{lockOf("1", Exclusive, other)}, id: "1",
			err: ErrBreak, after: []string{"1"},
		},
		{
			name: "a force-unlock", brk: true,
			held:  []Lock{lockOf("1", Shared, me), lockOf("2", Shared, other)},
			after: []string{}, released: []string{"2"},
		},
		{
			name: "a force-unlock of my locks", brk: true, held: []Lock{lockOf("1", Shared, me)},
			after: []string{},
		},
		{
			name: "a force-unlock without breaking", held: []Lock{lockOf("1", Shared, me)},
			err: ErrBreak, after: []string{"1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMem()
			s := New(Deps{DB: db{}, Store: m, Stacks: m})
			at := Target{Account: kernel.NewAccountID(), Stack: kernel.NewULID(), Workspace: "w"}
			m.workspaces[at], m.locks[at] = true, slices.Clone(tc.held)
			err := s.Unlock(t.Context(), Unlock{
				Target: at, Run: me, ID: tc.id, Break: tc.brk, RemoteIP: "192.0.2.1",
			})
			conflict, ok := errors.AsType[*Conflict](err)
			switch {
			case tc.holder != "":
				if !ok || conflict.Holder.ID != tc.holder {
					t.Errorf("Unlock = %v, want a conflict with %s", err, tc.holder)
				}
			case !errors.Is(err, tc.err):
				t.Errorf("Unlock = %v, want %v", err, tc.err)
			}
			if got := ids(m.locks[at]); !slices.Equal(got, tc.after) {
				t.Errorf("held %v, want %v", got, tc.after)
			}
			if tc.released == nil {
				if len(m.audits) != 0 {
					t.Errorf("audited %+v", m.audits)
				}
				return
			}
			want := `{"workspace":"w","released":[`
			for i, id := range tc.released {
				if i > 0 {
					want += ","
				}
				want += `{"ID":"` + id + `"}`
			}
			want += "]}"
			if len(m.audits) != 1 {
				t.Fatalf("audited %+v", m.audits)
			}
			e := m.audits[0]
			if e.AccountID != at.Account.ULID || e.Actor != (kernel.AuditActor{
				Username: Actor, Run: me,
			}) || e.Action != ActionBreakLock || e.EventType != "DELETE" ||
				e.AffectedResource.Type != ResourceStack ||
				e.AffectedResource.ID != at.Stack.String() ||
				string(e.AffectedResource.Metadata) != want || e.RelatedResource == nil ||
				e.RelatedResource.Type != ResourceRun || e.RelatedResource.ID != me.String() ||
				e.RemoteIP != "192.0.2.1" {
				t.Errorf("audit entry %+v, metadata %s; want %s", e, e.AffectedResource.Metadata,
					want)
			}
		})
	}
	m := newMem()
	if err := New(
		Deps{DB: db{}, Store: m, Stacks: m},
	).Unlock(t.Context(), Unlock{Run: me, ID: "1"}); !errors.Is(
		err,
		ErrNoWorkspace,
	) {
		t.Errorf("a workspace no lock was taken on = %v", err)
	}
}

// TestUnitRunEnded checks that a terminal state releases the run's locks
// in its account alone, and any other state none.
func TestUnitRunEnded(t *testing.T) {
	m := newMem()
	s := New(Deps{DB: db{}, Store: m, Stacks: m})
	acct, run, other := kernel.NewAccountID(), kernel.NewULID(), kernel.NewULID()
	mine := Target{Account: acct, Stack: kernel.NewULID()}
	theirs := Target{Account: kernel.NewAccountID(), Stack: kernel.NewULID()}
	m.locks[mine] = []Lock{lockOf("1", Shared, run), lockOf("2", Shared, other)}
	m.locks[theirs] = []Lock{lockOf("3", Shared, run)}
	if err := s.RunEnded(t.Context(), acct, run, kernel.RunStatePlanning); err != nil ||
		len(m.locks[mine]) != 2 {
		t.Errorf("a planning run = %v, held %v", err, ids(m.locks[mine]))
	}
	if err := s.RunEnded(t.Context(), acct, run, kernel.RunStateFailed); err != nil ||
		!slices.Equal(ids(m.locks[mine]), []string{"2"}) || len(m.locks[theirs]) != 1 {
		t.Errorf("a failed run = %v, held %v and %v", err, ids(m.locks[mine]),
			ids(m.locks[theirs]))
	}
}

// TestUnitLocksDown checks that each failing store method fails a lock or
// an unlock that reaches it, recording nothing after it.
func TestUnitLocksDown(t *testing.T) {
	me, other := kernel.NewULID(), kernel.NewULID()
	at := Target{Account: kernel.NewAccountID(), Stack: kernel.NewULID()}
	for _, method := range []string{"Hold", "OpenWorkspace", "LockWorkspace", "Locks", "AddLock"} {
		m := newMem()
		m.fail = method
		if err := New(
			Deps{DB: db{}, Store: m, Stacks: m},
		).Lock(t.Context(), at, lockOf("1", Shared, me)); !errors.Is(err,
			errDown) ||
			len(m.locks[at]) != 0 {
			t.Errorf("Lock with %s failing = %v, held %v", method, err, ids(m.locks[at]))
		}
	}
	for _, method := range []string{"LockWorkspace", "Locks", "RemoveLocks", "Audit"} {
		m := newMem()
		m.fail = method
		m.workspaces[at], m.locks[at] = true, []Lock{lockOf("1", Shared, other)}
		err := New(
			Deps{DB: db{}, Store: m, Stacks: m},
		).Unlock(t.Context(), Unlock{Target: at, Run: me, Break: true})
		if !errors.Is(err, errDown) || len(m.audits) != 0 {
			t.Errorf("Unlock with %s failing = %v, audited %v", method, err, m.audits)
		}
	}
	m := newMem()
	m.fail = "ReleaseRun"
	if err := New(
		Deps{DB: db{}, Store: m, Stacks: m},
	).RunEnded(t.Context(), at.Account, me, kernel.RunStateFinished); !errors.Is(
		err,
		errDown,
	) {
		t.Errorf("RunEnded with ReleaseRun failing = %v", err)
	}
	if err := New(Deps{DB: db{}, Store: m, Stacks: m}).Lock(t.Context(), Target{Workspace: "\x00"},
		lockOf("1", Shared, me)); !errors.Is(err, ErrWorkspaceName) {
		t.Errorf("a workspace name the table refuses = %v", err)
	}
}
