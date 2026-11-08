package authz_test

import (
	"context"
	"errors"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// store serves one account's rows, counting loads; during, when set, runs
// inside a load, as a concurrent drop would.
type store struct {
	x       *account
	loads   int
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

func (s *store) AuthzVersions(
	_ context.Context,
	accts []kernel.AccountID,
) (map[kernel.AccountID]int64, error) {
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
	// A binding the store holds is decided only after the snapshot is
	// dropped or found stale.
	x.bind(role.ActorUser, m.ULID, role.SpaceAdmin, "top")
	f.version = 1
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
	if got, err := svc.Actions(ctx, p, x.spaces["a"]); err != nil || len(got) != 145 {
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
