package pg_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
)

// ledger keeps role bindings as the access domain does, refusing a role
// it does not know, a space the fake spaces lack and one the caller does
// not administer. Its grants are each stack's read of its own space and
// its bindings on the space asked, all RESIDENT. fail fails every call.
type ledger struct {
	mu     sync.Mutex
	spaces *spaces
	authz  authz
	rows   map[kernel.ULID]accessapi.RoleBinding
	order  []kernel.ULID
	fail   error
}

var _ interface {
	accessapi.Bindings
	accessapi.Grants
} = (*ledger)(nil)

// refusals are r's refusals as p, at pointer.
func (l *ledger) refusals(
	ctx context.Context,
	p kernel.Principal,
	pointer string,
	r accessapi.BindingRequest,
) ([]kernel.FieldError, kernel.ULID) {
	var errs []kernel.FieldError
	if !slices.Contains([]string{"space-reader", "space-writer", "space-admin"}, r.Role) {
		errs = append(
			errs,
			kernel.FieldError{Pointer: pointer + "/roleID", Reason: kernel.ReasonNotFound},
		)
	}
	id, err := l.spaces.Find(ctx, p.AccountID, r.Space)
	if err != nil {
		return append(errs, kernel.FieldError{
			Pointer: pointer + "/spaceID", Reason: kernel.ReasonNotFound,
		}), id
	}
	if ok, _ := l.authz.Authorize(ctx, p, accessapi.ActionSpaceAdmin, id); !ok {
		errs = append(
			errs,
			kernel.FieldError{Pointer: pointer + "/spaceID", Reason: kernel.ReasonRule},
		)
	}
	return errs, id
}

func (l *ledger) held(actor kernel.ULID, role string, space kernel.ULID, but kernel.ULID) bool {
	for id, b := range l.rows {
		if id != but && b.ActorID == actor && b.Role == role && b.SpaceID == space {
			return true
		}
	}
	return false
}

func (l *ledger) Bind(
	ctx context.Context,
	p kernel.Principal,
	actor string,
	pointer string,
	reqs []accessapi.BindingRequest,
) ([]accessapi.RoleBinding, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail != nil {
		return nil, l.fail
	}
	var errs []kernel.FieldError
	out := make([]accessapi.RoleBinding, len(reqs))
	for i, r := range reqs {
		at := pointer
		if pointer != "" {
			at = fmt.Sprintf("%s/%d", pointer, i)
		}
		refused, space := l.refusals(ctx, p, at, r)
		errs = append(errs, refused...)
		out[i] = accessapi.RoleBinding{
			ID: kernel.NewULID(), Actor: actor, ActorID: r.Actor, Role: r.Role,
			SpaceID: space, Space: r.Space, CreatedBy: p.Caller(), Version: 1,
		}
	}
	if len(errs) > 0 {
		return nil, &kernel.Invalid{Detail: "The bindings are refused.", Errors: errs}
	}
	for _, b := range out {
		if l.held(b.ActorID, b.Role, b.SpaceID, kernel.ULID{}) {
			return nil, accessapi.ErrBindingHeld
		}
	}
	for _, b := range out {
		l.rows[b.ID] = b
		l.order = append(l.order, b.ID)
	}
	return out, nil
}

func (l *ledger) one(actor string, id kernel.ULID) (accessapi.RoleBinding, error) {
	if l.fail != nil {
		return accessapi.RoleBinding{}, l.fail
	}
	b, ok := l.rows[id]
	if !ok || b.Actor != actor {
		return accessapi.RoleBinding{}, accessapi.ErrNoBinding
	}
	return b, nil
}

func (l *ledger) Binding(
	_ context.Context,
	_ kernel.AccountID,
	actor string,
	id kernel.ULID,
) (accessapi.RoleBinding, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.one(actor, id)
}

func (l *ledger) Rebind(
	ctx context.Context,
	p kernel.Principal,
	actor string,
	id kernel.ULID,
	expect kernel.Expect,
	req accessapi.BindingRequest,
	check func(accessapi.RoleBinding) error,
) (accessapi.RoleBinding, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.one(actor, id)
	if err != nil {
		return b, err
	}
	refused, space := l.refusals(ctx, p, "", req)
	if len(refused) > 0 {
		return accessapi.RoleBinding{}, &kernel.Invalid{Errors: refused}
	}
	if err := expect.Check(b.Version); err != nil {
		return accessapi.RoleBinding{}, err
	}
	if err := check(b); err != nil {
		return accessapi.RoleBinding{}, err
	}
	if l.held(b.ActorID, req.Role, space, b.ID) {
		return accessapi.RoleBinding{}, accessapi.ErrBindingHeld
	}
	b.Role, b.SpaceID, b.Space = req.Role, space, req.Space
	b.Version++
	l.rows[id] = b
	return b, nil
}

func (l *ledger) Unbind(
	_ context.Context,
	_ kernel.AccountID,
	actor string,
	id kernel.ULID,
	expect kernel.Expect,
	check func(accessapi.RoleBinding) error,
) (accessapi.RoleBinding, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.one(actor, id)
	if err != nil {
		return b, err
	}
	if err := expect.Check(b.Version); err != nil {
		return accessapi.RoleBinding{}, err
	}
	if err := check(b); err != nil {
		return accessapi.RoleBinding{}, err
	}
	delete(l.rows, id)
	return b, nil
}

func (l *ledger) UnbindActor(
	_ context.Context,
	_ kernel.AccountID,
	_ string,
	id kernel.ULID,
) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail != nil {
		return l.fail
	}
	for k, b := range l.rows {
		if b.ActorID == id {
			delete(l.rows, k)
		}
	}
	return nil
}

// list is the rows keep keeps, oldest first.
func (l *ledger) list(keep func(accessapi.RoleBinding) bool) ([]accessapi.RoleBinding, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail != nil {
		return nil, l.fail
	}
	out := []accessapi.RoleBinding{}
	for _, id := range l.order {
		if b, ok := l.rows[id]; ok && keep(b) {
			out = append(out, b)
		}
	}
	return out, nil
}

func (l *ledger) ByActor(
	_ context.Context,
	_ kernel.AccountID,
	actor string,
	id kernel.ULID,
) ([]accessapi.RoleBinding, error) {
	return l.list(func(b accessapi.RoleBinding) bool { return b.Actor == actor && b.ActorID == id })
}

func (l *ledger) BySpace(
	_ context.Context,
	_ kernel.AccountID,
	space kernel.ULID,
) ([]accessapi.RoleBinding, error) {
	return l.list(func(b accessapi.RoleBinding) bool { return b.SpaceID == space })
}

func (l *ledger) Stacks(
	_ context.Context,
	_ kernel.AccountID,
	space kernel.ULID,
	stacks []accessapi.StackHome,
) ([][]accessapi.StackGrant, error) {
	bound, err := l.list(func(b accessapi.RoleBinding) bool { return b.SpaceID == space })
	if err != nil {
		return nil, err
	}
	out := make([][]accessapi.StackGrant, len(stacks))
	for i, h := range stacks {
		if h.Space == space {
			out[i] = append(out[i], accessapi.StackGrant{
				Source: "RESIDENT", Role: "space-reader", Space: space,
			})
		}
		for _, b := range bound {
			if b.ActorID == h.Stack {
				out[i] = append(out[i], accessapi.StackGrant{
					Source: "RESIDENT", Binding: b.ID, Role: b.Role, Space: space,
				})
			}
		}
	}
	return out, nil
}

// bindingFixture is a fixture whose admin manages stacks and administers
// the top and team spaces, with the stack infra in team and top in the
// top space. The reader manages and administers team too.
func bindingFixture(t *testing.T) fixture {
	t.Helper()
	f := newFixture(t)
	for _, space := range []kernel.ULID{f.acct.ULID, f.team} {
		f.authzGrant(f.admin, space, accessapi.ActionStackManage)
		f.authzGrant(f.admin, space, accessapi.ActionSpaceAdmin)
	}
	f.authzGrant(f.writer, f.team, accessapi.ActionStackManage)
	f.authzGrant(f.reader, f.team, accessapi.ActionStackManage)
	f.authzGrant(f.reader, f.team, accessapi.ActionSpaceAdmin)
	top := input("Top")
	top.Space = "acme"
	for _, in := range []core.Input{input("Infra"), top} {
		if _, err := f.svc.Create(t.Context(), f.admin, "", false, in); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// TestAccServiceBind binds stacks one at a time and in batches, refusing
// a space the caller cannot read or does not administer, a top-space
// binding of a stack below the top space, and a stack the caller may not
// manage, and reads the bindings back.
func TestAccServiceBind(t *testing.T) {
	ctx := t.Context()
	f := bindingFixture(t)
	b, err := f.svc.Bind(ctx, f.admin, core.BindRequest{
		Stack: "infra", Role: "space-writer", Space: "team",
	})
	if err != nil || b.Stack.Slug != "infra" || b.Space != "team" || b.Version != 1 ||
		b.ActorID != b.Stack.ID {
		t.Fatalf("Bind = %+v, %v", b, err)
	}
	if _, err := f.svc.Bind(ctx, f.admin, core.BindRequest{
		Stack: "top", Role: "space-admin", Space: "acme",
	}); err != nil {
		t.Errorf("a top-space binding of a top-space stack = %v", err)
	}
	for name, c := range map[string]struct {
		p    kernel.Principal
		r    core.BindRequest
		want func(error) bool
	}{
		"the top space for a stack below it": {
			f.admin,
			core.BindRequest{Stack: "infra", Role: "space-reader", Space: "acme"},
			func(err error) bool { return slices.Equal(pointers(t, err), []string{"/spaceID"}) },
		},
		"a hidden space": {
			f.admin,
			core.BindRequest{Stack: "infra", Role: "space-reader", Space: "hidden"},
			func(err error) bool { return isNoSpace(err, "hidden") },
		},
		"no space": {
			f.admin,
			core.BindRequest{Stack: "infra", Role: "space-reader", Space: "nowhere"},
			func(err error) bool { return isNoSpace(err, "nowhere") },
		},
		"a space the caller does not administer": {
			f.writer,
			core.BindRequest{Stack: "infra", Role: "space-reader", Space: "team"},
			func(err error) bool {
				d, ok := errors.AsType[*core.Denied](err)
				return ok && d.Action == accessapi.ActionSpaceAdmin
			},
		},
		"no stack": {
			f.admin,
			core.BindRequest{Stack: "nope", Role: "space-reader", Space: "team"},
			func(err error) bool { return errors.Is(err, stacksapi.ErrNotFound) },
		},
		"a held binding": {
			f.admin,
			core.BindRequest{Stack: "infra", Role: "space-writer", Space: "team"},
			func(err error) bool { return errors.Is(err, accessapi.ErrBindingHeld) },
		},
		"no role": {
			f.admin,
			core.BindRequest{Stack: "infra", Role: "nope", Space: "team"},
			func(err error) bool { return slices.Equal(pointers(t, err), []string{"/roleID"}) },
		},
	} {
		if _, err := f.svc.Bind(ctx, c.p, c.r); !c.want(err) {
			t.Errorf("%s: Bind = %v", name, err)
		}
	}

	f.authz[f.reader.Caller()][f.team] = []accessapi.Action{accessapi.ActionSpaceRead}
	for name, c := range map[string]struct {
		p    kernel.Principal
		rs   []core.BindRequest
		want []string
	}{
		"stacks the caller cannot read": {f.writer, []core.BindRequest{
			{Stack: "nope", Role: "space-reader", Space: "team"},
			{Stack: "top", Role: "space-reader", Space: "team"},
		}, []string{"/bindings/0/stackID", "/bindings/1/stackID"}},
		"a stack the caller may not manage": {f.reader, []core.BindRequest{
			{Stack: "infra", Role: "space-reader", Space: "team"},
			{Stack: "infra", Role: "space-reader", Space: "acme"},
		}, []string{"/bindings/0/stackID", "/bindings/1/stackID"}},
		"the top space for a stack below it": {f.admin, []core.BindRequest{
			{Stack: "top", Role: "space-reader", Space: "team"},
			{Stack: "infra", Role: "space-reader", Space: "acme"},
			{Stack: "infra", Role: "space-reader", Space: "nowhere"},
		}, []string{"/bindings/1/spaceID"}},
		"no role, as the access domain refuses it": {f.admin, []core.BindRequest{
			{Stack: "infra", Role: "nope", Space: "team"},
		}, []string{"/bindings/0/roleID"}},
	} {
		_, err := f.svc.BindAll(ctx, c.p, c.rs)
		if got := pointers(t, err); !slices.Equal(got, c.want) {
			t.Errorf("%s: BindAll refuses %v, want %v", name, got, c.want)
		}
	}
	all, err := f.svc.BindAll(ctx, f.admin, []core.BindRequest{
		{Stack: "infra", Role: "space-reader", Space: "team"},
		{Stack: "top", Role: "space-writer", Space: "team"},
	})
	if err != nil || len(all) != 2 || all[0].Stack.Slug != "infra" || all[1].Stack.Slug != "top" ||
		all[1].Role != "space-writer" {
		t.Fatalf("BindAll = %+v, %v", all, err)
	}

	got, err := f.svc.Binding(ctx, f.acct, b.ID.String())
	if err != nil || got.ID != b.ID || got.Stack.Slug != "infra" {
		t.Errorf("Binding = %+v, %v", got, err)
	}
	for _, raw := range []string{"nope", kernel.NewULID().String()} {
		if _, err := f.svc.Binding(ctx, f.acct, raw); !errors.Is(err, accessapi.ErrNoBinding) {
			t.Errorf("Binding(%q) = %v", raw, err)
		}
		if _, err := f.svc.BindingSpaceOf(ctx, f.acct, raw); !errors.Is(
			err, accessapi.ErrNoSubject) {
			t.Errorf("BindingSpaceOf(%q) = %v", raw, err)
		}
	}
	if space, err := f.svc.BindingSpaceOf(ctx, f.acct, b.ID.String()); err != nil ||
		space != f.team {
		t.Errorf("BindingSpaceOf = %v, %v", space, err)
	}
	held, err := f.svc.Bindings(ctx, f.acct, "infra")
	if err != nil || len(held) != 2 || held[0].ID != b.ID || held[1].Stack.Slug != "infra" {
		t.Errorf("Bindings = %+v, %v", held, err)
	}
	if _, err := f.svc.Bindings(ctx, f.acct, "nope"); !errors.Is(err, stacksapi.ErrNotFound) {
		t.Errorf("Bindings of no stack = %v", err)
	}

	f.ledger.fail = errDown
	if _, err := f.svc.Binding(ctx, f.acct, b.ID.String()); !errors.Is(err, errDown) {
		t.Errorf("Binding with access down = %v", err)
	}
	if _, err := f.svc.BindingSpaceOf(ctx, f.acct, b.ID.String()); !errors.Is(err, errDown) {
		t.Errorf("BindingSpaceOf with access down = %v", err)
	}
	if _, err := f.svc.Bindings(ctx, f.acct, "infra"); !errors.Is(err, errDown) {
		t.Errorf("Bindings with access down = %v", err)
	}
	if _, err := f.svc.Bind(ctx, f.admin, core.BindRequest{
		Stack: "infra", Role: "space-admin", Space: "team",
	}); !errors.Is(err, errDown) {
		t.Errorf("Bind with access down = %v", err)
	}
	if _, err := f.svc.BindAll(ctx, f.admin, []core.BindRequest{
		{Stack: "infra", Role: "space-admin", Space: "team"},
	}); !errors.Is(err, errDown) {
		t.Errorf("BindAll with access down = %v", err)
	}
}

// errDown is a dependency that is down.
var errDown = errors.New("down")

// TestAccServiceRebind moves and deletes stack bindings, refusing a
// binding in a space the caller does not administer under its lock, a
// stale version and the top space for a stack below it; a deleted
// stack's bindings go in its transaction, and stay when it fails.
func TestAccServiceRebind(t *testing.T) {
	ctx := t.Context()
	f := bindingFixture(t)
	bs, err := f.svc.BindAll(ctx, f.admin, []core.BindRequest{
		{Stack: "infra", Role: "space-reader", Space: "team"},
		{Stack: "top", Role: "space-reader", Space: "acme"},
	})
	if err != nil {
		t.Fatal(err)
	}
	infra, top := bs[0].ID.String(), bs[1].ID.String()
	moved, err := f.svc.Rebind(ctx, f.admin, infra, kernel.ExpectVersions(1), core.BindRequest{
		Role: "space-admin", Space: "team",
	})
	if err != nil || moved.Role != "space-admin" || moved.Version != 2 ||
		moved.Stack.Slug != "infra" {
		t.Errorf("Rebind = %+v, %v", moved, err)
	}
	denied := func(err error) bool {
		d, ok := errors.AsType[*core.Denied](err)
		return ok && d.Action == accessapi.ActionSpaceAdmin
	}
	for name, c := range map[string]struct {
		p    kernel.Principal
		id   string
		e    kernel.Expect
		r    core.BindRequest
		want func(error) bool
	}{
		"a stale version": {
			f.admin, infra, kernel.ExpectVersions(1),
			core.BindRequest{Role: "space-reader", Space: "team"},
			func(err error) bool { return errors.Is(err, kernel.ErrStale) },
		},
		"a binding in a space the caller does not administer": {
			f.reader, top,
			kernel.Expect{},
			core.BindRequest{Role: "space-reader", Space: "team"},
			denied,
		},
		"a space the caller does not administer": {
			f.writer, infra,
			kernel.Expect{},
			core.BindRequest{Role: "space-reader", Space: "team"},
			denied,
		},
		"the top space for a stack below it": {
			f.admin, infra,
			kernel.Expect{},
			core.BindRequest{Role: "space-reader", Space: "acme"},
			func(err error) bool { return slices.Equal(pointers(t, err), []string{"/spaceID"}) },
		},
		"no binding": {
			f.admin, kernel.NewULID().String(),
			kernel.Expect{},
			core.BindRequest{Role: "space-reader", Space: "team"},
			func(err error) bool { return errors.Is(err, accessapi.ErrNoBinding) },
		},
	} {
		if _, err := f.svc.Rebind(ctx, c.p, c.id, c.e, c.r); !c.want(err) {
			t.Errorf("%s: Rebind = %v", name, err)
		}
	}
	if _, err := f.svc.Unbind(ctx, f.reader, top, kernel.Expect{}); !denied(err) {
		t.Errorf("Unbind of a binding in a space the caller does not administer = %v", err)
	}
	if _, err := f.svc.Unbind(ctx, f.admin, top, kernel.ExpectVersions(9)); !errors.Is(
		err, kernel.ErrStale) {
		t.Errorf("a stale Unbind = %v", err)
	}
	gone, err := f.svc.Unbind(ctx, f.admin, top, kernel.Expect{})
	if err != nil || gone.Stack.Slug != "top" {
		t.Errorf("Unbind = %+v, %v", gone, err)
	}
	if _, err := f.svc.Unbind(ctx, f.admin, top, kernel.Expect{}); !errors.Is(
		err, accessapi.ErrNoBinding) {
		t.Errorf("a second Unbind = %v", err)
	}

	f.ledger.fail = errDown
	if _, err := f.svc.Delete(ctx, f.admin, "infra", kernel.Expect{}); !errors.Is(err, errDown) {
		t.Errorf("Delete with access down = %v", err)
	}
	f.ledger.fail = nil
	if _, err := f.svc.Stack(ctx, f.acct, "infra"); err != nil {
		t.Errorf("the stack after a failed Delete = %v", err)
	}
	if _, err := f.svc.Delete(ctx, f.admin, "infra", kernel.Expect{}); err != nil {
		t.Fatal(err)
	}
	if left, _ := f.ledger.list(func(accessapi.RoleBinding) bool { return true }); len(left) != 0 {
		t.Errorf("bindings after the delete = %+v", left)
	}
}

// TestAccServiceHolding lists the stacks a space's access view lists:
// residents alone, then, for its admin, every stack a grant reaches it
// for.
func TestAccServiceHolding(t *testing.T) {
	ctx := t.Context()
	f := bindingFixture(t)
	if _, err := f.svc.Bind(ctx, f.admin, core.BindRequest{
		Stack: "top", Role: "space-writer", Space: "team",
	}); err != nil {
		t.Fatal(err)
	}
	slugs := func(res search.Result[core.Holding]) []string {
		out := make([]string, len(res.Edges))
		for i, e := range res.Edges {
			out[i] = e.Node.Slug
		}
		return out
	}
	res, err := f.svc.Holding(ctx, f.writer, "team", core.Residing, search.Input{})
	if err != nil || !slices.Equal(slugs(res), []string{"infra"}) ||
		!reflect.DeepEqual(res.Edges[0].Node.Grants, []accessapi.StackGrant{
			{Source: "RESIDENT", Role: "space-reader", Space: f.team},
		}) {
		t.Errorf("residents = %+v, %v", res, err)
	}
	res, err = f.svc.Holding(ctx, f.admin, "team", core.AllWithAccess, search.Input{})
	if err != nil || !slices.Equal(slugs(res), []string{"infra", "top"}) ||
		res.Edges[1].Node.Grants[0].Role != "space-writer" {
		t.Errorf("all with access = %+v, %v", res, err)
	}
	res, err = f.svc.Holding(ctx, f.admin, "team", core.AllWithAccess, search.Input{
		Predicates: []search.Predicate{{
			Field: "name", Constraint: search.Constraint{StringMatches: []string{"T*"}},
		}},
	})
	if err != nil || !slices.Equal(slugs(res), []string{"top"}) {
		t.Errorf("searched = %+v, %v", res, err)
	}
	got, err := f.svc.SuggestHolding(ctx, f.admin, "team", core.AllWithAccess,
		search.SuggestionsInput{})
	if err != nil || got.Count != 2 {
		t.Errorf("suggestions = %+v, %v", got, err)
	}
	got, err = f.svc.SuggestHolding(ctx, f.admin, "team", core.Residing, search.SuggestionsInput{})
	if err != nil || got.Count != 1 {
		t.Errorf("residents' suggestions = %+v, %v", got, err)
	}
	if _, err := f.svc.Holding(ctx, f.writer, "team", core.AllWithAccess,
		search.Input{}); !errors.As(err, new(*core.Denied)) {
		t.Errorf("all with access for a writer = %v", err)
	}
	if _, err := f.svc.SuggestHolding(ctx, f.admin, "nowhere", core.Residing,
		search.SuggestionsInput{}); !isNoSpace(err, "nowhere") {
		t.Errorf("no space = %v", err)
	}
	if _, err := f.svc.Holding(ctx, f.admin, "team", core.Residing, search.Input{
		Predicates: []search.Predicate{{Field: "owner"}},
	}); !errors.As(err, new(*search.Invalid)) {
		t.Errorf("an unknown field = %v", err)
	}
	f.ledger.fail = errDown
	if _, err := f.svc.Holding(ctx, f.admin, "team", core.Residing, search.Input{}); !errors.Is(
		err, errDown) {
		t.Errorf("Holding with access down = %v", err)
	}
	f.ledger.fail = nil
	f.spaces.treeErr = errDown
	if _, err := f.svc.Holding(ctx, f.admin, "team", core.Residing, search.Input{}); !errors.Is(
		err, errDown) {
		t.Errorf("Holding without the tree = %v", err)
	}
}
