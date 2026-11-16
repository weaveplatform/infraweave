package authz_test

import (
	"context"
	"errors"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// store serves one account's rows, counting loads and version reads;
// during, when set, runs inside a load, as a concurrent drop would.
type store struct {
	x       *account
	loads   int
	reads   int
	err     error
	verErr  error
	gone    bool
	during  func()
	version int64
}

func (s *store) AuthzData(_ context.Context, acct kernel.AccountID) (authz.Data, error) {
	s.loads++
	if s.err != nil {
		return authz.Data{}, s.err
	}
	if s.during != nil {
		s.during()
	}
	d := s.x.data
	d.Version = s.version
	if acct != s.x.id {
		return authz.Data{}, errors.New("no such account")
	}
	return d, nil
}

func (s *store) AuthzVersions(_ context.Context, accts []kernel.AccountID) (map[kernel.AccountID]int64, error) {
	s.reads++
	if s.verErr != nil {
		return nil, s.verErr
	}
	out := map[kernel.AccountID]int64{}
	for _, a := range accts {
		if a == s.x.id && !s.gone {
			out[a] = s.version
		}
	}
	return out, nil
}

func TestUnitService(t *testing.T) {
	ctx := t.Context()
	x := newAccount()
	m := kernel.NewMemberID()
	f := &store{x: x}
	svc := authz.NewService(f)
	p := kernel.Principal{Kind: kernel.PrincipalUser, AccountID: x.id, ActorID: m.ULID, MemberID: m}

	if ok, err := svc.Authorize(
		ctx,
		p,
		accessapi.ActionSpaceRead,
		x.spaces["a"],
	); err != nil ||
		ok {
		t.Fatalf("an unbound member reads = %t, %v", ok, err)
	}
	// A binding the store holds at the snapshot's version is decided only
	// after the snapshot is dropped or found stale.
	x.bind(binding.ActorUser, m.ULID, role.SpaceAdmin, "top")
	if ok, _ := svc.Admin(ctx, p); ok || f.loads != 1 {
		t.Fatalf("a held snapshot reloaded: admin %t after %d loads", ok, f.loads)
	}
	svc.Drop(x.id)
	if ok, err := svc.Admin(ctx, p); err != nil || !ok || f.loads != 2 {
		t.Fatalf("after a drop: admin %t, %v after %d loads", ok, err, f.loads)
	}
	if got, err := svc.Reachable(ctx, p, accessapi.ActionStackCreate); err != nil || len(got) != 6 {
		t.Errorf("Reachable = %v, %v", got, err)
	}
	if got, err := svc.Explain(ctx, p, x.spaces["a"]); err != nil || len(got) != 1 {
		t.Errorf("Explain = %+v, %v", got, err)
	}
	if ok, err := svc.Holds(ctx, p, accessapi.ActionApiKeyDelete); err != nil || !ok {
		t.Errorf("Holds = %t, %v", ok, err)
	}
	if got, err := svc.Actions(ctx, p, x.spaces["a"]); err != nil || len(got) != assignable() {
		t.Errorf("Actions = %d, %v", len(got), err)
	}
	if err := svc.Refresh(ctx); err != nil || f.loads != 2 {
		t.Fatalf("Refresh at the same version = %v, %d loads", err, f.loads)
	}
	f.version = 2
	if err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _ = svc.Admin(ctx, p); f.loads != 3 {
		t.Errorf("a moved version kept the snapshot: %d loads", f.loads)
	}
	f.gone = true
	if err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	f.gone = false
	if _, _ = svc.Admin(ctx, p); f.loads != 4 {
		t.Errorf("a gone account kept the snapshot: %d loads", f.loads)
	}
	svc.Flush()
	if _, _ = svc.Admin(ctx, p); f.loads != 5 {
		t.Errorf("Flush kept the snapshot: %d loads", f.loads)
	}
	f.verErr = errors.New("database down")
	if err := svc.Refresh(ctx); !errors.Is(err, f.verErr) {
		t.Errorf("Refresh on a failing store = %v", err)
	}
	f.verErr = nil

	// A load a drop overtakes is used once but not kept.
	svc.Flush()
	f.during = func() { svc.Drop(x.id) }
	if ok, _ := svc.Admin(ctx, p); !ok {
		t.Error("the overtaken load was not used")
	}
	f.during = nil
	if _, _ = svc.Admin(ctx, p); f.loads != 7 {
		t.Errorf("an overtaken load was kept: %d loads", f.loads)
	}

	// A space the snapshot lacks rebuilds it once the account's version
	// has moved, as when the space was created since, and not before.
	unknown := kernel.NewULID()
	before := f.loads
	if ok, err := svc.Authorize(ctx, p, accessapi.ActionSpaceRead, unknown); ok || err != nil ||
		f.loads != before {
		t.Errorf(
			"an unknown space at the same version = %t, %v after %d loads",
			ok,
			err,
			f.loads-before,
		)
	}
	f.version++
	if got, err := svc.Actions(
		ctx,
		p,
		unknown,
	); len(got) != 0 || err != nil ||
		f.loads != before+1 {
		t.Errorf(
			"an unknown space at a moved version = %v, %v after %d loads",
			got,
			err,
			f.loads-before,
		)
	}
	f.verErr = errors.New("database down")
	if _, err := svc.Explain(ctx, p, unknown); !errors.Is(err, f.verErr) {
		t.Errorf("an unknown space with the version unread = %v", err)
	}
	f.verErr = nil
	f.err = errors.New("database down")
	f.version++
	if _, err := svc.Authorize(ctx, p, accessapi.ActionSpaceRead, unknown); !errors.Is(err, f.err) {
		t.Errorf("an unknown space whose rebuild fails = %v", err)
	}
	f.err = nil

	// A principal with no account decides nothing and reads nothing.
	none := kernel.Principal{Kind: kernel.PrincipalOrganization}
	if ok, err := svc.Authorize(
		ctx,
		none,
		accessapi.ActionSpaceRead,
		x.spaces["a"],
	); ok ||
		err != nil {
		t.Errorf("a principal with no account = %t, %v", ok, err)
	}
	if got, err := svc.Reachable(ctx, none, accessapi.ActionSpaceRead); got != nil || err != nil {
		t.Errorf("a principal with no account reaches %v, %v", got, err)
	}
	if got, err := svc.Explain(ctx, none, x.spaces["a"]); got != nil || err != nil {
		t.Errorf("a principal with no account explains %v, %v", got, err)
	}
	if ok, err := svc.Admin(ctx, none); ok || err != nil {
		t.Errorf("a principal with no account is admin %t, %v", ok, err)
	}
	if err := authz.NewService(f).Refresh(ctx); err != nil {
		t.Errorf("Refresh holding nothing = %v", err)
	}

	// A failing store fails every check, naming the account.
	svc.Flush()
	f.err = errors.New("database down")
	if _, err := svc.Authorize(
		ctx,
		p,
		accessapi.ActionSpaceRead,
		x.spaces["a"],
	); !errors.Is(
		err,
		f.err,
	) {
		t.Errorf("Authorize on a failing store = %v", err)
	}
}

// TestUnitServiceRefusal checks that a refusal is decided again on a
// rebuilt snapshot once the account's version has moved, as when a grant
// was committed on another replica whose message has not arrived: a
// space the member could not read, an action it could not perform there,
// a held action and account admin; that a refusal at the snapshot's
// version, however often, rebuilds nothing; that an allowed decision
// reads no version, so a revocation waits for its message or Refresh;
// and that a version that cannot be read fails the refusal.
func TestUnitServiceRefusal(t *testing.T) {
	ctx := t.Context()
	x := newAccount()
	m := kernel.NewMemberID()
	f := &store{x: x}
	svc := authz.NewService(f)
	p := kernel.Principal{Kind: kernel.PrincipalUser, AccountID: x.id, ActorID: m.ULID, MemberID: m}
	authorize := func(action accessapi.Action) bool {
		t.Helper()
		ok, err := svc.Authorize(ctx, p, action, x.spaces["a"])
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}

	if authorize(accessapi.ActionSpaceRead) || f.loads != 1 || f.reads != 1 {
		t.Fatalf("an unbound member: %d loads, %d reads", f.loads, f.reads)
	}
	x.bind(binding.ActorUser, m.ULID, role.SpaceReader, "a")
	for i := range 3 {
		if authorize(accessapi.ActionSpaceRead) || f.loads != 1 || f.reads != i+2 {
			t.Fatalf("a refusal at the snapshot's version: %d loads, %d reads", f.loads, f.reads)
		}
	}
	f.version = 1
	if !authorize(accessapi.ActionSpaceRead) || f.loads != 2 {
		t.Fatalf("a space granted since the snapshot: %d loads", f.loads)
	}

	x.bind(binding.ActorUser, m.ULID, role.SpaceWriter, "a")
	if authorize(accessapi.ActionStackLock) || f.loads != 2 {
		t.Fatalf("an action at the snapshot's version: %d loads", f.loads)
	}
	f.version = 2
	if !authorize(accessapi.ActionStackLock) || f.loads != 3 {
		t.Fatalf("an action granted since the snapshot: %d loads", f.loads)
	}

	x.bind(binding.ActorUser, m.ULID, role.SpaceAdmin, "top")
	f.version = 3
	if ok, err := svc.Holds(ctx, p, accessapi.ActionApiKeyDelete); err != nil || !ok ||
		f.loads != 4 {
		t.Fatalf("a held action granted since the snapshot = %t, %v after %d loads", ok, err,
			f.loads)
	}
	x.data.Bindings = nil
	f.version = 4
	reads := f.reads
	if ok, err := svc.Admin(ctx, p); err != nil || !ok || f.loads != 4 || f.reads != reads {
		t.Fatalf("an allowed decision compared the version: admin %t, %v, %d loads, %d reads",
			ok, err, f.loads, f.reads-reads)
	}
	if err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, err := svc.Admin(ctx, p); err != nil || ok || f.loads != 5 {
		t.Fatalf("a revocation after Refresh: admin %t, %v after %d loads", ok, err, f.loads)
	}

	x.bind(binding.ActorUser, m.ULID, role.SpaceAdmin, "top")
	f.version = 5
	if ok, err := svc.Admin(ctx, p); err != nil || !ok || f.loads != 6 {
		t.Fatalf("admin granted since the snapshot = %t, %v after %d loads", ok, err, f.loads)
	}

	f.verErr = errors.New("database down")
	if _, err := svc.Authorize(ctx, p, accessapi.ActionSpaceRead, kernel.NewULID()); !errors.Is(
		err, f.verErr) {
		t.Errorf("a refusal with the version unread = %v", err)
	}
}

// TestUnitServiceStateRead checks the service's state read: the missing
// action at the snapshot's version, the read granted since it once the
// version moves, nothing for a principal of no account, and a version
// that cannot be read failing the refusal.
func TestUnitServiceStateRead(t *testing.T) {
	ctx := t.Context()
	x := newAccount()
	m := kernel.NewMemberID()
	f := &store{x: x}
	svc := authz.NewService(f)
	p := kernel.Principal{Kind: kernel.PrincipalUser, AccountID: x.id, ActorID: m.ULID, MemberID: m}
	stack := kernel.NewULID()
	if missing, err := svc.StateRead(ctx, p, stack, x.spaces["a"]); err != nil ||
		missing != accessapi.ActionSpaceRead {
		t.Errorf("an unbound member = %q, %v", missing, err)
	}
	x.bind(binding.ActorUser, m.ULID, role.SpaceWriter, "a")
	f.version = 1
	if missing, err := svc.StateRead(ctx, p, stack, x.spaces["a"]); err != nil || missing != "" {
		t.Errorf("a writer bound since the snapshot = %q, %v", missing, err)
	}
	if missing, err := svc.StateRead(ctx, kernel.Principal{}, stack, x.spaces["a"]); err != nil ||
		missing != accessapi.ActionSpaceRead {
		t.Errorf("a principal of no account = %q, %v", missing, err)
	}
	f.verErr = errors.New("database down")
	if _, err := svc.StateRead(ctx, p, stack, x.spaces["legacy"]); !errors.Is(err, f.verErr) {
		t.Errorf("a refusal with the version unread = %v", err)
	}
}

func TestUnitKey(t *testing.T) {
	acct := kernel.NewAccountID()
	if got, ok := authz.ParseKey(authz.Key(acct)); !ok || got != acct {
		t.Errorf("ParseKey(Key) = %v, %t", got, ok)
	}
	for _, p := range []string{"session:" + acct.String(), "authz:nope", ""} {
		if _, ok := authz.ParseKey(p); ok {
			t.Errorf("ParseKey(%q) read an account", p)
		}
	}
}

func TestUnitServiceHolders(t *testing.T) {
	ctx := t.Context()
	x := newAccount()
	m, key := kernel.NewMemberID(), kernel.NewULID()
	team := x.group("team")
	x.bind(binding.ActorGroup, team, role.SpaceWriter, "a")
	x.bind(binding.ActorAPIKey, key, role.SpaceReader, "top")
	svc := authz.NewService(&store{x: x})

	held, err := svc.Holders(ctx, x.id, x.spaces["a1"], binding.ActorGroup, binding.ActorAPIKey)
	if err != nil || len(held) != 2 || held[0].ID != team || held[0].Name != "team" ||
		held[0].Grants[0].Source != authz.Propagated || held[1].ID != key {
		t.Fatalf("Holders = %+v, %v", held, err)
	}
	if held := authz.Build(x.data).Holders(kernel.NewULID(), binding.ActorGroup); held != nil {
		t.Errorf("Holders of a space the snapshot lacks = %+v", held)
	}
	got, err := svc.ExplainEach(ctx, x.id, x.spaces["a"], []authz.Who{
		{Kind: kernel.PrincipalUser, Actor: m.ULID, Member: m, Teams: []string{"team"}},
		{Kind: kernel.PrincipalUser, Actor: m.ULID, Member: m},
	})
	if err != nil || len(got) != 2 || len(got[0]) != 1 || got[0][0].Source != authz.Resident ||
		len(got[1]) != 0 {
		t.Fatalf("ExplainEach = %+v, %v", got, err)
	}
}

func TestUnitServiceRun(t *testing.T) {
	ctx := t.Context()
	x := newAccount()
	stack := kernel.NewULID()
	x.bind(binding.ActorStack, stack, role.SpaceAdmin, "a1")
	svc := authz.NewService(&store{x: x})
	p := kernel.Principal{
		Kind: kernel.PrincipalRun, AccountID: x.id, ActorID: kernel.NewULID(),
		StackID: stack, StackSpaceID: x.spaces["legacy"],
	}
	// The run acts as its stack: its binding, the read a1's inheritance
	// gives of a, and a read of the space the stack lives in.
	for space, want := range map[string]bool{"a1": true, "a": true, "legacy": true, "top": false} {
		if ok, err := svc.Authorize(
			ctx,
			p,
			accessapi.ActionSpaceRead,
			x.spaces[space],
		); err != nil ||
			ok != want {
			t.Errorf("read %s = %t, %v; want %t", space, ok, err, want)
		}
	}
	// Narrowed: no run actions, whatever the stack holds.
	if ok, err := svc.Authorize(
		ctx,
		p,
		accessapi.ActionRunTrigger,
		x.spaces["a1"],
	); err != nil ||
		ok {
		t.Errorf("RUN_TRIGGER = %t, %v", ok, err)
	}
	// A run principal with no stack holds nothing.
	p.StackID, p.StackSpaceID = kernel.ULID{}, kernel.ULID{}
	if ok, err := svc.Authorize(
		ctx,
		p,
		accessapi.ActionSpaceRead,
		x.spaces["a1"],
	); err != nil ||
		ok {
		t.Errorf("no stack = %t, %v", ok, err)
	}
}
