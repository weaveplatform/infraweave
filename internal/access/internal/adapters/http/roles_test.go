package http

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
)

// roleBook is an in-memory role.Store of one account's roles, failing
// whenever st is down. bindings are each role's, by slug.
type roleBook struct {
	st       *store
	roles    map[string]role.Role
	bindings map[string][]binding.Listed
}

func newRoleBook(st *store, acct kernel.AccountID) *roleBook {
	b := &roleBook{st: st, roles: map[string]role.Role{}, bindings: map[string][]binding.Listed{}}
	for _, r := range role.Seed(acct) {
		r.Version = 1
		b.roles[r.Slug] = r
	}
	return b
}

func (b *roleBook) Roles(
	_ context.Context,
	_ kernel.AccountID,
	after string,
	limit int,
) ([]role.Role, error) {
	var out []role.Role
	for _, r := range b.roles {
		if r.Slug > after {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(x, y role.Role) int { return strings.Compare(x.Slug, y.Slug) })
	return out[:min(limit, len(out))], b.st.err()
}

func (b *roleBook) Role(_ context.Context, _ kernel.AccountID, slug string) (role.Role, error) {
	if err := b.st.err(); err != nil {
		return role.Role{}, err
	}
	r, ok := b.roles[slug]
	if !ok {
		return role.Role{}, port.ErrNotFound
	}
	r.Bindings = len(b.bindings[slug])
	return r, nil
}

func (b *roleBook) CreateRole(_ context.Context, r role.Role) error {
	if err := b.st.err(); err != nil {
		return err
	}
	if _, ok := b.roles[r.Slug]; ok {
		return port.ErrSlugTaken
	}
	b.roles[r.Slug] = r
	return nil
}

func (b *roleBook) UpdateRole(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
	edit func(role.Role) (role.Role, error),
) (role.Role, error) {
	r, err := b.Role(ctx, acct, slug)
	if err != nil {
		return role.Role{}, err
	}
	if r, err = edit(r); err != nil {
		return role.Role{}, err
	}
	r.Version++
	b.roles[slug] = r
	return r, nil
}

func (b *roleBook) DeleteRole(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
	check func(role.Role) error,
) (role.Role, error) {
	r, err := b.Role(ctx, acct, slug)
	if err != nil {
		return role.Role{}, err
	}
	if err := check(r); err != nil {
		return role.Role{}, err
	}
	if r.Bindings > 0 {
		return role.Role{}, role.ErrInUse
	}
	delete(b.roles, slug)
	return r, nil
}

func (b *roleBook) RoleBindings(
	_ context.Context,
	_ kernel.AccountID,
	id kernel.ULID,
) ([]binding.Listed, error) {
	for slug, r := range b.roles {
		if r.ID == id {
			return b.bindings[slug], b.st.err()
		}
	}
	return nil, b.st.err()
}

// TestUnitRoles lists the system roles beside a custom one a page at a
// time, creates, renames and re-actions the custom role through PUT and
// PATCH, renders its bindings, and refuses a taken slug, an action no
// role may hold, a system role's change and a bound role's delete.
func TestUnitRoles(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt

	rec := w.do(t, http.MethodPost, "/roles", me,
		`{"name":"Deploy Bots","actions":["STACK_MANAGE","SPACE_READ"]}`)
	bots := decode[api.Role](t, rec)
	if rec.Code != http.StatusCreated || bots.Id != "deploy-bots" || bots.Slug != bots.Id ||
		bots.IsSystem || bots.Description != "" ||
		!slices.Equal(bots.Actions, []api.Action{api.SPACEREAD, api.STACKMANAGE}) ||
		bots.RoleBindings == nil {
		t.Fatalf("create = %d %+v", rec.Code, bots)
	}
	wantProblem(t, w.do(t, http.MethodPost, "/roles", me, `{"name":"deploy  bots","actions":[]}`),
		http.StatusConflict, "already-exists")
	if p := wantProblem(
		t,
		w.do(t, http.MethodPost, "/roles", me,
			`{"name":"Bad","actions":["SPACE_READ","STACK_ROLE_BINDING_CREATE"]}`),
		http.StatusUnprocessableEntity,
		"validation-failed",
	); len(p.Errors) != 1 || p.Errors[0].Pointer != "/actions/1" ||
		!strings.Contains(p.Errors[0].Message, "STACK_ROLE_BINDING_CREATE") {
		t.Errorf("a disabled action = %+v", p)
	}

	var seen []string
	cursor := ""
	for range 5 {
		target := "/roles?limit=2"
		if cursor != "" {
			target += "&cursor=" + url.QueryEscape(cursor)
		}
		pg := decode[api.RolePage](t, w.do(t, http.MethodGet, target, me, ""))
		for _, r := range pg.Items {
			seen = append(seen, r.Id)
			if want, _ := role.SystemActions(r.Id); r.IsSystem && len(r.Actions) != len(want) {
				t.Errorf("%s lists %d actions, want %d", r.Id, len(r.Actions), len(want))
			}
		}
		if pg.NextCursor == nil {
			break
		}
		cursor = *pg.NextCursor
	}
	want := []string{
		"deploy-bots", role.DriftDetectionManager, role.SpaceAdmin, role.SpaceReader,
		role.SpaceWriter, role.WorkerPoolController,
	}
	if !slices.Equal(seen, want) {
		t.Errorf("pages = %v, want %v", seen, want)
	}
	rec = w.do(t, http.MethodGet, "/roles?cursor=garbage", me, "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a bad cursor = %d %s", rec.Code, rec.Body)
	}

	wantTag(t, "read", w.do(t, http.MethodGet, "/roles/deploy-bots", me, ""), "1")
	wantProblem(t, w.do(t, http.MethodGet, "/roles/nobody", me, ""), http.StatusNotFound,
		"not-found")

	rec = w.conditional(t, http.MethodPut, "/roles/deploy-bots", me, `"1"`,
		`{"name":"Deployers","Actions":["SPACE_WRITE"]}`)
	wantTag(t, "a PUT", rec, "2")
	if got := decode[api.Role](t, rec); got.Id != "deploy-bots" || got.Name != "Deployers" ||
		!slices.Equal(got.Actions, []api.Action{api.SPACEWRITE}) {
		t.Errorf("a PUT = %+v", got)
	}
	wantStale(t, "a stale PUT", w.conditional(t, http.MethodPut, "/roles/deploy-bots", me, `"1"`,
		`{"name":"x"}`))
	rec = w.conditional(t, http.MethodPatch, "/roles/deploy-bots", me, "",
		`{"description":"Bots that deploy"}`)
	wantTag(t, "a PATCH", rec, "3")
	if got := decode[api.Role](t, rec); got.Name != "Deployers" ||
		got.Description != "Bots that deploy" ||
		!slices.Equal(got.Actions, []api.Action{api.SPACEWRITE}) {
		t.Errorf("a PATCH = %+v", got)
	}
	wantProblem(t, w.conditional(t, http.MethodPatch, "/roles/nobody", me, "", `{"name":"x"}`),
		http.StatusNotFound, "not-found")
	for _, c := range []struct{ method, body string }{
		{http.MethodPut, `{"name":"Boss"}`},
		{http.MethodPatch, `{"name":"Boss"}`},
		{http.MethodDelete, ""},
	} {
		wantProblem(t, w.conditional(t, c.method, "/roles/space-admin", me, "", c.body),
			http.StatusUnprocessableEntity, "validation-failed")
	}

	r := w.roles.roles["deploy-bots"]
	at := binding.Binding{ID: kernel.NewULID(), RoleID: r.ID, ActorID: kernel.NewULID()}
	for _, kind := range []binding.ActorKind{binding.ActorUser, binding.ActorGroup, binding.ActorAPIKey} {
		at.ActorKind = kind
		w.roles.bindings["deploy-bots"] = append(w.roles.bindings["deploy-bots"],
			binding.Listed{Binding: at, Role: r.Slug, Space: "acme"})
	}
	got := decode[api.Role](t, w.do(t, http.MethodGet, "/roles/deploy-bots", me, ""))
	if got.RoleBindingsCount != 3 || len(got.RoleBindings) != 3 {
		t.Fatalf("bindings = %d %+v", got.RoleBindingsCount, got.RoleBindings)
	}
	user, _ := got.RoleBindings[0].AsUserRoleBinding()
	group, _ := got.RoleBindings[1].AsUserGroupRoleBinding()
	key, _ := got.RoleBindings[2].AsApiKeyRoleBinding()
	if user.UserID != at.ActorID.String() || user.Space.Id != "acme" || user.Role.Id != r.Slug ||
		group.UserGroup.Id != at.ActorID.String() || group.RoleID != r.Slug ||
		key.ApiKeyID != at.ActorID.String() || key.Kind != api.ApiKeyRoleBindingKindApiKeyRoleBinding {
		t.Errorf("bindings = %+v %+v %+v", user, group, key)
	}
	wantProblem(t, w.do(t, http.MethodDelete, "/roles/deploy-bots", me, ""), http.StatusConflict,
		"invalid-state")
	delete(w.roles.bindings, "deploy-bots")
	wantStale(t, "a stale DELETE",
		w.conditional(t, http.MethodDelete, "/roles/deploy-bots", me, `"1"`, ""))
	rec = w.conditional(t, http.MethodDelete, "/roles/deploy-bots", me, `"3"`, "")
	if got := decode[api.Role](t, rec); rec.Code != http.StatusOK || got.Id != "deploy-bots" {
		t.Errorf("delete = %d %+v", rec.Code, got)
	}
	wantProblem(t, w.do(t, http.MethodGet, "/roles/deploy-bots", me, ""), http.StatusNotFound,
		"not-found")
}

// TestUnitRolesDown checks that each Roles operation answers 503 with the
// store down.
func TestUnitRolesDown(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt
	w.do(t, http.MethodGet, "/viewer", me, "")
	w.st.down = true
	for _, r := range [][3]string{
		{http.MethodGet, "/roles", ""},
		{http.MethodGet, "/roles/space-admin", ""},
		{http.MethodPost, "/roles", `{"name":"x","actions":[]}`},
		{http.MethodPut, "/roles/space-admin", `{"name":"x"}`},
		{http.MethodPatch, "/roles/space-admin", `{"name":"x"}`},
		{http.MethodDelete, "/roles/space-admin", ""},
	} {
		if rec := w.conditional(
			t,
			r[0],
			r[1],
			me,
			"",
			r[2],
		); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s with the store down: %d %s", r[0], r[1], rec.Code, rec.Body)
		}
	}
	// The read of a role's bindings fails after the role is read.
	w.st.down = false
	w.roles.st = &store{down: true}
	if rec := w.do(t, http.MethodGet, "/roles", me, ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a list whose bindings fail = %d %s", rec.Code, rec.Body)
	}
}

// TestUnitRolesNoPrincipal checks that each Roles operation refuses a
// call that reaches it with no principal.
func TestUnitRolesNoPrincipal(t *testing.T) {
	s := Server{}
	ctx := t.Context()
	for name, call := range map[string]func() error{
		"list": func() error { _, err := s.Roles(ctx, api.RolesRequestObject{}); return err },
		"read": func() error { _, err := s.Role(ctx, api.RoleRequestObject{}); return err },
		"create": func() error {
			_, err := s.RoleCreate(ctx, api.RoleCreateRequestObject{Body: &api.RoleInput{}})
			return err
		},
		"update": func() error {
			_, err := s.RoleUpdate(ctx, api.RoleUpdateRequestObject{Body: &api.RoleUpdateInput{}})
			return err
		},
		"patch":  func() error { _, err := s.RolePatch(ctx, api.RolePatchRequestObject{}); return err },
		"delete": func() error { _, err := s.RoleDelete(ctx, api.RoleDeleteRequestObject{}); return err },
	} {
		if err := call(); err == nil {
			t.Errorf("%s without a principal answered", name)
		}
	}
}
