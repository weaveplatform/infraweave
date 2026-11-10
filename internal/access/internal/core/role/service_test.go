package role_test

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

var errDown = errors.New("down")

// store keeps one account's roles in memory, by slug.
type store struct {
	roles map[string]role.Role
	err   error
}

func newStore(acct kernel.AccountID) *store {
	s := &store{roles: map[string]role.Role{}}
	for _, r := range role.Seed(acct) {
		r.Version = 1
		s.roles[r.Slug] = r
	}
	return s
}

func (s *store) Roles(
	_ context.Context,
	_ kernel.AccountID,
	after string,
	limit int,
) ([]role.Role, error) {
	var out []role.Role
	for _, r := range s.roles {
		if r.Slug > after {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b role.Role) int { return cmp.Compare(a.Slug, b.Slug) })
	return out[:min(limit, len(out))], s.err
}

func (s *store) Role(_ context.Context, _ kernel.AccountID, slug string) (role.Role, error) {
	r, ok := s.roles[slug]
	if !ok {
		return role.Role{}, port.ErrNotFound
	}
	return r, s.err
}

func (s *store) CreateRole(_ context.Context, r role.Role) error {
	if _, ok := s.roles[r.Slug]; ok {
		return port.ErrSlugTaken
	}
	s.roles[r.Slug] = r
	return s.err
}

func (s *store) UpdateRole(
	_ context.Context,
	_ kernel.AccountID,
	slug string,
	edit func(role.Role) (role.Role, error),
) (role.Role, error) {
	r, ok := s.roles[slug]
	if !ok {
		return role.Role{}, port.ErrNotFound
	}
	r, err := edit(r)
	if err != nil {
		return role.Role{}, err
	}
	r.Version++
	s.roles[slug] = r
	return r, s.err
}

func (s *store) DeleteRole(
	_ context.Context,
	_ kernel.AccountID,
	slug string,
	check func(role.Role) error,
) (role.Role, error) {
	r, ok := s.roles[slug]
	if !ok {
		return role.Role{}, port.ErrNotFound
	}
	if err := check(r); err != nil {
		return role.Role{}, err
	}
	if r.Bindings > 0 {
		return role.Role{}, role.ErrInUse
	}
	delete(s.roles, slug)
	return r, s.err
}

func (s *store) RoleBindings(
	context.Context,
	kernel.AccountID,
	kernel.ULID,
) ([]binding.Listed, error) {
	return []binding.Listed{{Space: "top"}}, s.err
}

func service(t *testing.T) (*role.Service, *store, kernel.AccountID, *int) {
	t.Helper()
	acct := kernel.NewAccountID()
	st := newStore(acct)
	drops := new(int)
	svc := role.NewService(role.Deps{Store: st, Drop: func(a kernel.AccountID) {
		if a != acct {
			t.Errorf("dropped %s, want %s", a, acct)
		}
		*drops++
	}})
	return svc, st, acct, drops
}

// pointers is each field error of err's *kernel.Invalid as pointer=reason.
func pointers(t *testing.T, err error) []string {
	t.Helper()
	inv, ok := errors.AsType[*kernel.Invalid](err)
	if !ok {
		t.Fatalf("err = %v, want a *kernel.Invalid", err)
	}
	out := make([]string, 0, len(inv.Errors))
	for _, e := range inv.Errors {
		out = append(out, e.Pointer+"="+e.Message)
	}
	return out
}

func TestUnitSlugOf(t *testing.T) {
	for name, want := range map[string]string{
		"Deploy Bots":          "deploy-bots",
		"  --Ops / On-Call!! ": "ops-on-call",
		"Team42":               "team42",
		"Ünïcode café":         "n-code-caf",
		"!!!":                  "",
		"":                     "",
	} {
		if got := role.SlugOf(name); got != want {
			t.Errorf("SlugOf(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestUnitCreateRole checks that a custom role takes its slug from its
// name and holds its actions once each in catalogue order, and that a
// nameless role or an action no role may hold is refused at its pointer,
// naming the action.
func TestUnitCreateRole(t *testing.T) {
	ctx := t.Context()
	svc, st, acct, drops := service(t)
	r, err := svc.Create(ctx, acct, role.Input{
		Name:    "Deploy Bots",
		Actions: []accessapi.Action{"STACK_MANAGE", "SPACE_READ", "STACK_MANAGE"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Slug != "deploy-bots" || r.System || r.Version != 1 || r.Description != "" ||
		!slices.Equal(r.Actions, []accessapi.Action{"SPACE_READ", "STACK_MANAGE"}) || *drops != 1 {
		t.Errorf("Create = %+v, %d drops", r, *drops)
	}
	if _, ok := st.roles["deploy-bots"]; !ok {
		t.Error("the role is not stored")
	}
	if _, err := svc.Create(ctx, acct, role.Input{
		Name: "deploy bots", Description: new("x"),
		Actions: []accessapi.Action{},
	}); !errors.Is(err, port.ErrSlugTaken) {
		t.Errorf("a taken slug = %v", err)
	}
	_, err = svc.Create(ctx, acct, role.Input{Name: "?!", Actions: []accessapi.Action{
		"SPACE_READ", "NO_SUCH_ACTION", "STACK_ROLE_BINDING_CREATE", "UPDATE_BLUEPRINT_DEPLOYMENT",
	}})
	want := []string{
		"/name=A role's name needs a letter or a digit, which its slug is made of.",
		"/actions/1=NO_SUCH_ACTION is not an action of the catalogue.",
		"/actions/2=STACK_ROLE_BINDING_CREATE is disabled, so a role cannot hold it.",
		"/actions/3=UPDATE_BLUEPRINT_DEPLOYMENT is deprecated, so a role cannot hold it.",
	}
	if got := pointers(t, err); !slices.Equal(got, want) {
		t.Errorf("refusals = %q, want %q", got, want)
	}
	if *drops != 1 {
		t.Errorf("a refused create dropped the snapshot: %d drops", *drops)
	}
}

// TestUnitUpdateRole checks that an update replaces only the fields it
// names, keeps the slug, and refuses a system role, a stale version, a
// nameless name and an action no role may hold.
func TestUnitUpdateRole(t *testing.T) {
	ctx := t.Context()
	svc, _, acct, drops := service(t)
	if _, err := svc.Create(ctx, acct, role.Input{
		Name: "Ops", Description: new("Operators"),
		Actions: []accessapi.Action{"SPACE_READ"},
	}); err != nil {
		t.Fatal(err)
	}
	r, err := svc.Update(
		ctx,
		acct,
		"ops",
		kernel.ExpectVersions(1),
		role.Change{Name: new("Operations")},
	)
	if err != nil || r.Slug != "ops" || r.Name != "Operations" || r.Description != "Operators" ||
		!slices.Equal(r.Actions, []accessapi.Action{"SPACE_READ"}) || r.Version != 2 {
		t.Errorf("rename = %+v, %v", r, err)
	}
	actions := []accessapi.Action{"STACK_MANAGE", "SPACE_WRITE"}
	r, err = svc.Update(ctx, acct, "ops", kernel.Expect{}, role.Change{
		Description: new(""),
		Actions:     &actions,
	})
	if err != nil || r.Name != "Operations" || r.Description != "" ||
		!slices.Equal(r.Actions, []accessapi.Action{"SPACE_WRITE", "STACK_MANAGE"}) {
		t.Errorf("new actions = %+v, %v", r, err)
	}
	if *drops != 3 {
		t.Errorf("%d drops, want 3", *drops)
	}
	if _, err := svc.Update(ctx, acct, "ops", kernel.ExpectVersions(1),
		role.Change{}); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("a stale update = %v", err)
	}
	if _, err := svc.Update(ctx, acct, role.SpaceAdmin, kernel.Expect{},
		role.Change{Name: new("Boss")}); !errors.Is(err, role.ErrSystem) {
		t.Errorf("a system role's update = %v", err)
	}
	bad := []accessapi.Action{"SPACE_READ", "STACK_ROLE_BINDING_DELETE"}
	_, err = svc.Update(
		ctx,
		acct,
		"ops",
		kernel.Expect{},
		role.Change{Name: new(" "), Actions: &bad},
	)
	want := []string{
		"/name=A role's name needs a letter or a digit, which its slug is made of.",
		"/Actions/1=STACK_ROLE_BINDING_DELETE is disabled, so a role cannot hold it.",
	}
	if got := pointers(t, err); !slices.Equal(got, want) {
		t.Errorf("refusals = %q, want %q", got, want)
	}
	if _, err := svc.Update(ctx, acct, "gone", kernel.Expect{},
		role.Change{}); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("a missing role's update = %v", err)
	}
	if *drops != 3 {
		t.Errorf("a refused update dropped the snapshot: %d drops", *drops)
	}
}

// TestUnitDeleteRole checks that a custom role without bindings is
// deleted, and that a system role, a stale version and a role with
// bindings are refused.
func TestUnitDeleteRole(t *testing.T) {
	ctx := t.Context()
	svc, st, acct, drops := service(t)
	for _, name := range []string{"Ops", "Bound"} {
		if _, err := svc.Create(ctx, acct, role.Input{
			Name:    name,
			Actions: []accessapi.Action{"SPACE_READ"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	bound := st.roles["bound"]
	bound.Bindings = 1
	st.roles["bound"] = bound
	for slug, want := range map[string]error{
		role.SpaceReader: role.ErrSystem,
		"bound":          role.ErrInUse,
	} {
		if _, err := svc.Delete(ctx, acct, slug, kernel.Expect{}); !errors.Is(err, want) {
			t.Errorf("delete %s = %v, want %v", slug, err, want)
		}
	}
	if _, err := svc.Delete(
		ctx,
		acct,
		"ops",
		kernel.ExpectVersions(9),
	); !errors.Is(
		err,
		kernel.ErrStale,
	) {
		t.Errorf("a stale delete = %v", err)
	}
	r, err := svc.Delete(ctx, acct, "ops", kernel.ExpectVersions(1))
	if err != nil || r.Slug != "ops" || !slices.Equal(r.Actions, []accessapi.Action{"SPACE_READ"}) {
		t.Errorf("delete = %+v, %v", r, err)
	}
	if _, ok := st.roles["ops"]; ok || *drops != 3 {
		t.Errorf("the role remains (%t) or %d drops, want 3", ok, *drops)
	}
}

// TestUnitReadRoles checks that reads give a system role its actions from
// the catalogue and pass the store's errors on.
func TestUnitReadRoles(t *testing.T) {
	ctx := t.Context()
	svc, st, acct, _ := service(t)
	rs, err := svc.Roles(ctx, acct, "", 10)
	if err != nil || len(rs) != 5 {
		t.Fatalf("Roles = %v, %v", rs, err)
	}
	for _, r := range rs {
		want, _ := role.SystemActions(r.Slug)
		if !slices.Equal(r.Actions, want) {
			t.Errorf("%s holds %v, want %v", r.Slug, r.Actions, want)
		}
	}
	r, err := svc.Role(ctx, acct, role.SpaceAdmin)
	if err != nil || len(r.Actions) == 0 {
		t.Errorf("Role = %+v, %v", r, err)
	}
	if b, err := svc.Bindings(ctx, acct, r); err != nil || len(b) != 1 {
		t.Errorf("Bindings = %v, %v", b, err)
	}
	st.err = errDown
	if _, err := svc.Roles(ctx, acct, "", 10); !errors.Is(err, errDown) {
		t.Errorf("Roles with the store down = %v", err)
	}
	if _, err := svc.Role(ctx, acct, role.SpaceAdmin); !errors.Is(err, errDown) {
		t.Errorf("Role with the store down = %v", err)
	}
	if _, err := svc.Create(ctx, acct, role.Input{Name: "x"}); !errors.Is(err, errDown) {
		t.Errorf("Create with the store down = %v", err)
	}
	if _, err := svc.Delete(ctx, acct, "x", kernel.Expect{}); !errors.Is(err, errDown) {
		t.Errorf("Delete with the store down = %v", err)
	}
}
