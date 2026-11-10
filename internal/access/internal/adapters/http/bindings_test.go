package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// bindBook is binding.Store over the world's roles, spaces, people and
// keys.
type bindBook struct {
	w   *world
	all []binding.Listed
	// down fails every read of an actor's bindings, with the rest of the
	// world up.
	down bool
}

var _ binding.Store = (*bindBook)(nil)

func (b *bindBook) RoleID(_ context.Context, _ kernel.AccountID, slug string) (kernel.ULID, error) {
	if err := b.w.st.err(); err != nil {
		return kernel.ULID{}, err
	}
	r, ok := b.w.roles.roles[slug]
	if !ok {
		return kernel.ULID{}, port.ErrNotFound
	}
	return r.ID, nil
}

func (b *bindBook) SpaceID(
	_ context.Context,
	_ kernel.AccountID,
	slug string,
) (kernel.ULID, error) {
	if err := b.w.st.err(); err != nil {
		return kernel.ULID{}, err
	}
	sp, ok := b.w.tree.spaces[slug]
	if !ok {
		return kernel.ULID{}, port.ErrNotFound
	}
	return sp.ID, nil
}

func (b *bindBook) HasActor(
	_ context.Context,
	_ kernel.AccountID,
	kind binding.ActorKind,
	id kernel.ULID,
) (bool, error) {
	if err := b.w.st.err(); err != nil {
		return false, err
	}
	switch kind {
	case binding.ActorUser:
		_, ok := b.w.people.users[kernel.MemberID{ULID: id}]
		return ok, nil
	case binding.ActorGroup:
		_, ok := b.w.people.groups[id]
		return ok, nil
	case binding.ActorAPIKey:
		k, ok := b.w.st.keys[id]
		return ok && k.DeletedAt.IsZero(), nil
	}
	return false, nil
}

// find is the index of the binding id of kind, or -1.
func (b *bindBook) find(kind binding.ActorKind, id kernel.ULID) int {
	return slices.IndexFunc(b.all, func(l binding.Listed) bool {
		return l.ID == id && l.ActorKind == kind
	})
}

func (b *bindBook) Binding(
	_ context.Context,
	_ kernel.AccountID,
	kind binding.ActorKind,
	id kernel.ULID,
) (binding.Listed, error) {
	if err := b.w.st.err(); err != nil {
		return binding.Listed{}, err
	}
	i := b.find(kind, id)
	if i < 0 {
		return binding.Listed{}, port.ErrNotFound
	}
	return b.all[i], nil
}

func (b *bindBook) where(keep func(binding.Listed) bool) ([]binding.Listed, error) {
	if err := b.w.st.err(); err != nil {
		return nil, err
	}
	out := []binding.Listed{}
	for _, l := range b.all {
		if keep(l) {
			out = append(out, l)
		}
	}
	return out, nil
}

func (b *bindBook) ActorBindings(
	_ context.Context,
	_ kernel.AccountID,
	kind binding.ActorKind,
	id kernel.ULID,
) ([]binding.Listed, error) {
	if b.down {
		return nil, errDown
	}
	return b.where(func(l binding.Listed) bool { return l.ActorKind == kind && l.ActorID == id })
}

func (b *bindBook) SpaceBindings(
	_ context.Context,
	_ kernel.AccountID,
	id kernel.ULID,
) ([]binding.Listed, error) {
	return b.where(func(l binding.Listed) bool { return l.SpaceID == id })
}

// listed is bb with its role's and space's slugs.
func (b *bindBook) listed(bb binding.Binding) binding.Listed {
	l := binding.Listed{Binding: bb}
	for slug, r := range b.w.roles.roles {
		if r.ID == bb.RoleID {
			l.Role = slug
		}
	}
	for slug, sp := range b.w.tree.spaces {
		if sp.ID == bb.SpaceID {
			l.Space = slug
		}
	}
	return l
}

// held reports whether a binding other than skip grants bb's role to its
// actor in its space.
func (b *bindBook) held(bb binding.Binding, skip kernel.ULID) bool {
	return slices.ContainsFunc(b.all, func(l binding.Listed) bool {
		return l.ID != skip && l.ActorKind == bb.ActorKind && l.ActorID == bb.ActorID &&
			l.RoleID == bb.RoleID && l.SpaceID == bb.SpaceID
	})
}

func (b *bindBook) CreateBindings(
	_ context.Context,
	_ kernel.AccountID,
	bs []binding.Binding,
) ([]binding.Listed, error) {
	if err := b.w.st.err(); err != nil {
		return nil, err
	}
	was := len(b.all)
	out := make([]binding.Listed, 0, len(bs))
	for i, bb := range bs {
		if b.held(bb, kernel.ULID{}) {
			b.all = b.all[:was]
			return nil, &binding.Duplicate{Index: i}
		}
		l := b.listed(bb)
		b.all = append(b.all, l)
		out = append(out, l)
	}
	return out, nil
}

func (b *bindBook) UpdateBinding(
	ctx context.Context,
	acct kernel.AccountID,
	kind binding.ActorKind,
	id kernel.ULID,
	edit func(binding.Listed) (binding.Binding, error),
) (binding.Listed, error) {
	cur, err := b.Binding(ctx, acct, kind, id)
	if err != nil {
		return binding.Listed{}, err
	}
	next, err := edit(cur)
	if err != nil {
		return binding.Listed{}, err
	}
	if b.held(next, id) {
		return binding.Listed{}, &binding.Duplicate{}
	}
	next.Version++
	l := b.listed(next)
	b.all[b.find(kind, id)] = l
	return l, nil
}

func (b *bindBook) DeleteBinding(
	ctx context.Context,
	acct kernel.AccountID,
	kind binding.ActorKind,
	id kernel.ULID,
	check func(binding.Listed) error,
) (binding.Listed, error) {
	cur, err := b.Binding(ctx, acct, kind, id)
	if err != nil {
		return binding.Listed{}, err
	}
	if err := check(cur); err != nil {
		return binding.Listed{}, err
	}
	b.all = slices.Delete(b.all, b.find(kind, id), b.find(kind, id)+1)
	return cur, nil
}

func (b *bindBook) ReplaceAccessRules(
	ctx context.Context,
	acct kernel.AccountID,
	kind binding.ActorKind,
	id kernel.ULID,
	bs []binding.Binding,
) error {
	if err := b.w.st.err(); err != nil {
		return err
	}
	b.all = slices.DeleteFunc(b.all, func(l binding.Listed) bool {
		return l.ActorKind == kind && l.ActorID == id && l.Origin == binding.OriginAccessRule
	})
	_, err := b.CreateBindings(ctx, acct, bs)
	return err
}

func (b *bindBook) DeleteActorBindings(
	_ context.Context,
	_ kernel.AccountID,
	kind binding.ActorKind,
	id kernel.ULID,
) error {
	if err := b.w.st.err(); err != nil {
		return err
	}
	b.all = slices.DeleteFunc(b.all, func(l binding.Listed) bool {
		return l.ActorKind == kind && l.ActorID == id
	})
	return nil
}

// invite is a managed user the world's key invites, with roleBindings,
// a JSON array.
func (w *world) invite(t *testing.T, bearer, login, roleBindings string) api.ManagedUser {
	t.Helper()
	rec := w.do(t, http.MethodPost, "/managed-users", bearer,
		fmt.Sprintf(`{"username":%q,"roleBindings":%s}`, login, roleBindings))
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite %s = %d %s", login, rec.Code, rec.Body)
	}
	return decode[api.ManagedUser](t, rec)
}

// wantField checks that p names exactly the pointers, in order.
func wantField(t *testing.T, p problem.Problem, pointers ...string) {
	t.Helper()
	got := make([]string, 0, len(p.Errors))
	for _, e := range p.Errors {
		got = append(got, e.Pointer)
	}
	if !slices.Equal(got, pointers) {
		t.Errorf("pointers = %v, want %v: %+v", got, pointers, p)
	}
}

// TestUnitUserRoleBindings binds a managed user, reads, lists, moves,
// patches and deletes the binding, and refuses a duplicate, an unknown
// role, space or actor, a space the caller cannot read or does not
// administer, a changed actor, a stale If-Match and a batch with one bad
// element, which writes none of it.
func TestUnitUserRoleBindings(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt
	bob := w.invite(t, me, "bob", "[]")
	body := func(user, role, space string) string {
		return fmt.Sprintf(`{"userID":%q,"roleID":%q,"spaceID":%q}`, user, role, space)
	}

	rec := w.do(t, http.MethodPost, "/user-role-bindings", me, body(bob.Id, "space-reader", "acme"))
	b := decode[api.UserRoleBinding](t, rec)
	if rec.Code != http.StatusCreated || b.UserID != bob.Id || b.User.Id != bob.Id ||
		b.RoleID != "space-reader" || b.Role.Id != "space-reader" || b.Space.Id != "acme" ||
		b.Kind != api.UserRoleBindingKindUserRoleBinding {
		t.Fatalf("create = %d %+v", rec.Code, b)
	}
	wantTag(t, "read", w.do(t, http.MethodGet, "/user-role-bindings/"+b.Id, me, ""), "1")
	wantProblem(t, w.do(t, http.MethodPost, "/user-role-bindings", me,
		body(bob.Id, "space-reader", "acme")), http.StatusConflict, "already-exists")

	p := wantProblem(t, w.do(t, http.MethodPost, "/user-role-bindings", me,
		body("nobody", "no-role", "nowhere")), http.StatusUnprocessableEntity, "validation-failed")
	wantField(t, p, "/userID", "/roleID", "/spaceID")

	// legacy is a space of the account the caller holds nothing on, then
	// one it reads and does not administer.
	p = wantProblem(t, w.do(
		t,
		http.MethodPost,
		"/user-role-bindings",
		me,
		body(
			bob.Id,
			"space-reader",
			"legacy",
		),
	), http.StatusUnprocessableEntity, "validation-failed")
	if len(p.Errors) != 1 || p.Errors[0].Reason != kernel.ReasonNotFound {
		t.Errorf("unreadable = %+v", p)
	}
	legacy := w.tree.spaces["legacy"].ID
	w.authz.on[legacy] = []accessapi.Action{accessapi.ActionSpaceRead}
	p = wantProblem(t, w.do(
		t,
		http.MethodPost,
		"/user-role-bindings",
		me,
		body(
			bob.Id,
			"space-reader",
			"legacy",
		),
	), http.StatusUnprocessableEntity, "validation-failed")
	if len(p.Errors) != 1 || p.Errors[0].Reason != kernel.ReasonRule ||
		p.Errors[0].Pointer != "/spaceID" {
		t.Errorf("not administered = %+v", p)
	}

	p = wantProblem(t, w.do(
		t,
		http.MethodPost,
		"/user-role-binding-batches",
		me,
		`{"bindings":[`+body(
			bob.Id,
			"space-writer",
			"acme",
		)+`,`+body(
			bob.Id,
			"no-role",
			"acme",
		)+`]}`,
	),
		http.StatusUnprocessableEntity, "validation-failed")
	wantField(t, p, "/bindings/1/roleID")
	p = wantProblem(t, w.do(
		t,
		http.MethodPost,
		"/user-role-binding-batches",
		me,
		`{"bindings":[`+body(
			bob.Id,
			"space-writer",
			"acme",
		)+`,`+body(
			bob.Id,
			"space-reader",
			"acme",
		)+`]}`,
	),
		http.StatusConflict, "already-exists")
	if !strings.Contains(p.Detail, "/bindings/1") {
		t.Errorf("duplicate in a batch = %+v", p)
	}
	list := decode[[]api.UserRoleBinding](t, w.do(t, http.MethodGet,
		"/managed-users/"+bob.Id+"/user-role-bindings", me, ""))
	if len(list) != 1 || list[0].Id != b.Id {
		t.Fatalf("after refused batches = %+v", list)
	}
	rec = w.do(
		t,
		http.MethodPost,
		"/user-role-binding-batches",
		me,
		`{"bindings":[`+body(
			bob.Id,
			"space-writer",
			"acme",
		)+`,`+body(
			bob.Id,
			"space-admin",
			"acme",
		)+`]}`,
	)
	if got := decode[[]api.UserRoleBinding](
		t,
		rec,
	); rec.Code != http.StatusCreated || len(got) != 2 ||
		got[0].RoleID != "space-writer" ||
		got[1].RoleID != "space-admin" {
		t.Fatalf("batch = %d %+v", rec.Code, got)
	}

	at := "/user-role-bindings/" + b.Id
	wantStale(t, "a stale PUT", w.conditional(t, http.MethodPut, at, me, `"9"`,
		body(bob.Id, "space-writer", "acme")))
	wantProblem(
		t,
		w.conditional(t, http.MethodPut, at, me, "", body(bob.Id, "space-writer", "acme")),
		http.StatusConflict,
		"already-exists",
	)
	p = wantProblem(t, w.conditional(
		t,
		http.MethodPut,
		at,
		me,
		"",
		body(
			w.key.String(),
			"space-reader",
			"acme",
		),
	), http.StatusUnprocessableEntity, "validation-failed")
	wantField(t, p, "/userID")
	p = wantProblem(
		t,
		w.conditional(t, http.MethodPut, at, me, "", body(bob.Id, "no-role", "acme")),
		http.StatusUnprocessableEntity,
		"validation-failed",
	)
	wantField(t, p, "/roleID")

	readOnly := role.Role{ID: kernel.NewULID(), Slug: "read-only", Version: 1}
	w.roles.roles[readOnly.Slug] = readOnly
	rec = w.conditional(t, http.MethodPut, at, me, `"1"`, body(bob.Id, "read-only", "acme"))
	wantTag(t, "put", rec, "2")
	if got := decode[api.UserRoleBinding](t, rec); got.RoleID != "read-only" {
		t.Errorf("put = %+v", got)
	}
	rec = w.conditional(t, http.MethodPatch, at, me, `"2"`, `{"roleID":"space-reader"}`)
	wantTag(t, "patch", rec, "3")
	if got := decode[api.UserRoleBinding](
		t,
		rec,
	); got.RoleID != "space-reader" ||
		got.UserID != bob.Id {
		t.Errorf("patch = %+v", got)
	}
	wantStale(t, "a stale PATCH", w.conditional(t, http.MethodPatch, at, me, `"1"`, `{}`))
	wantProblem(t, w.conditional(t, http.MethodPatch, "/user-role-bindings/nope", me, "", `{}`),
		http.StatusNotFound, "not-found")

	wantStale(t, "a stale DELETE", w.conditional(t, http.MethodDelete, at, me, `"1"`, ""))
	rec = w.conditional(t, http.MethodDelete, at, me, `"3"`, "")
	if got := decode[api.UserRoleBinding](t, rec); rec.Code != http.StatusOK || got.Id != b.Id {
		t.Errorf("delete = %d %+v", rec.Code, got)
	}
	for _, target := range []string{at, "/user-role-bindings/nope"} {
		wantProblem(t, w.do(t, http.MethodGet, target, me, ""), http.StatusNotFound, "not-found")
		wantProblem(t, w.do(t, http.MethodDelete, target, me, ""), http.StatusNotFound, "not-found")
		wantProblem(t, w.do(t, http.MethodPut, target, me, body(bob.Id, "space-reader", "acme")),
			http.StatusNotFound, "not-found")
	}
	wantProblem(t, w.do(t, http.MethodGet, "/managed-users/nope/user-role-bindings", me, ""),
		http.StatusNotFound, "not-found")
}

// TestUnitGroupAndKeyRoleBindings binds a group and an API key, reads,
// lists, moves, patches and deletes each binding, and refuses a binding
// of another kind's ID.
func TestUnitGroupAndKeyRoleBindings(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt
	rec := w.do(
		t,
		http.MethodPost,
		"/managed-user-groups",
		me,
		`{"groupName":"ops","roleBindings":[]}`,
	)
	ops := decode[api.ManagedUserGroup](t, rec)
	if rec.Code != http.StatusCreated {
		t.Fatalf("group = %d %s", rec.Code, rec.Body)
	}
	key := w.key.String()

	rec = w.do(t, http.MethodPost, "/user-group-role-bindings", me,
		fmt.Sprintf(`{"userGroupID":%q,"roleID":"space-writer","spaceID":"acme"}`, ops.Id))
	g := decode[api.UserGroupRoleBinding](t, rec)
	if rec.Code != http.StatusCreated || g.UserGroup.Id != ops.Id || g.RoleID != "space-writer" ||
		g.Space.Id != "acme" || g.Kind != api.UserGroupRoleBindingKindUserGroupRoleBinding {
		t.Fatalf("group binding = %d %+v", rec.Code, g)
	}
	rec = w.do(t, http.MethodPost, "/api-key-role-bindings", me,
		fmt.Sprintf(`{"apiKeyID":%q,"roleID":"space-admin","spaceID":"acme"}`, key))
	k := decode[api.ApiKeyRoleBinding](t, rec)
	if rec.Code != http.StatusCreated || k.ApiKeyID != key || k.ApiKey.Id != key ||
		k.Role.Id != "space-admin" || k.Kind != api.ApiKeyRoleBindingKindApiKeyRoleBinding {
		t.Fatalf("key binding = %d %+v", rec.Code, k)
	}
	if v := decode[api.ApiKey](t, w.do(t, http.MethodGet, "/api-keys/"+key, me, "")); !v.Admin ||
		v.SpaceCount != 1 || len(v.AccessRules) != 0 {
		t.Errorf("bound key = %+v", v)
	}

	rec = w.do(
		t,
		http.MethodPost,
		"/user-group-role-binding-batches",
		me,
		fmt.Sprintf(
			`{"bindings":[{"userGroupID":%q,"roleID":"space-reader","spaceID":"acme"}]}`,
			ops.Id,
		),
	)
	if got := decode[[]api.UserGroupRoleBinding](
		t,
		rec,
	); rec.Code != http.StatusCreated ||
		len(got) != 1 {
		t.Errorf("group batch = %d %+v", rec.Code, got)
	}
	rec = w.do(t, http.MethodPost, "/api-key-role-binding-batches", me,
		fmt.Sprintf(`{"bindings":[{"apiKeyID":%q,"roleID":"space-reader","spaceID":"acme"}]}`, key))
	if got := decode[[]api.ApiKeyRoleBinding](
		t,
		rec,
	); rec.Code != http.StatusCreated ||
		len(got) != 1 {
		t.Errorf("key batch = %d %+v", rec.Code, got)
	}
	if got := decode[[]api.UserGroupRoleBinding](t, w.do(t, http.MethodGet,
		"/managed-user-groups/"+ops.Id+"/user-role-bindings", me, "")); len(got) != 2 {
		t.Errorf("group's = %+v", got)
	}
	if got := decode[[]api.ApiKeyRoleBinding](t, w.do(t, http.MethodGet,
		"/api-keys/"+key+"/api-key-role-bindings", me, "")); len(got) != 2 {
		t.Errorf("key's = %+v", got)
	}

	for _, c := range []struct{ at, put, patch string }{
		{
			"/user-group-role-bindings/" + g.Id,
			fmt.Sprintf(`{"userGroupID":%q,"roleID":"space-admin","spaceID":"acme"}`, ops.Id),
			`{"roleID":"space-writer"}`,
		},
		{
			"/api-key-role-bindings/" + k.Id,
			fmt.Sprintf(`{"apiKeyID":%q,"roleID":"space-writer","spaceID":"acme"}`, key),
			`{"roleID":"space-admin"}`,
		},
	} {
		wantTag(t, c.at, w.do(t, http.MethodGet, c.at, me, ""), "1")
		wantTag(t, c.at, w.conditional(t, http.MethodPut, c.at, me, `"1"`, c.put), "2")
		wantTag(t, c.at, w.conditional(t, http.MethodPatch, c.at, me, `"2"`, c.patch), "3")
		if rec := w.do(t, http.MethodDelete, c.at, me, ""); rec.Code != http.StatusOK {
			t.Errorf("delete %s = %d %s", c.at, rec.Code, rec.Body)
		}
	}
	// A binding's ID names it only under its own kind.
	wantProblem(t, w.do(t, http.MethodGet, "/user-role-bindings/"+k.Id, me, ""),
		http.StatusNotFound, "not-found")
	wantProblem(t, w.do(t, http.MethodGet, "/managed-user-groups/nope/user-role-bindings", me, ""),
		http.StatusNotFound, "not-found")
	wantProblem(t, w.do(t, http.MethodGet, "/api-keys/nope/api-key-role-bindings", me, ""),
		http.StatusNotFound, "not-found")
}

// TestUnitLegacyBindings stores an invitation's and a group's role
// bindings, an API key's access rules and role bindings, and a managed
// user's access rules, which read back as access rules and survive a
// PATCH that leaves them out; a level of NONE grants nothing, and a
// refused element names its pointer and writes nothing.
func TestUnitLegacyBindings(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt

	bob := w.invite(t, me, "bob", `[{"roleID":"space-writer","spaceID":"acme"}]`)
	if len(bob.AccessRules) != 0 {
		t.Errorf("invited = %+v", bob.AccessRules)
	}
	if got := decode[[]api.UserRoleBinding](t, w.do(t, http.MethodGet,
		"/managed-users/"+bob.Id+"/user-role-bindings", me, "")); len(got) != 1 ||
		got[0].RoleID != "space-writer" {
		t.Errorf("invited bindings = %+v", got)
	}
	p := wantProblem(t, w.do(t, http.MethodPost, "/managed-users", me,
		`{"username":"eve","roleBindings":[{"roleID":"no-role","spaceID":"acme"}]}`),
		http.StatusUnprocessableEntity, "validation-failed")
	wantField(t, p, "/roleBindings/0/roleID")
	if page := decode[api.ManagedUserPage](
		t,
		w.do(t, http.MethodGet, "/managed-users", me, ""),
	); len(
		page.Items,
	) != 1 {
		t.Errorf("a refused invitation wrote %+v", page.Items)
	}

	at := "/managed-users/" + bob.Id
	rec := w.do(
		t,
		http.MethodPut,
		at,
		me,
		`{"accessRules":[{"space":"acme","spaceAccessLevel":"READ"},{"space":"acme","spaceAccessLevel":"NONE"}]}`,
	)
	u := decode[api.ManagedUser](t, rec)
	if rec.Code != http.StatusOK || len(u.AccessRules) != 1 || u.AccessRules[0].Space != "acme" ||
		u.AccessRules[0].SpaceAccessLevel != api.SpaceAccessLevelREAD {
		t.Fatalf("update = %d %+v", rec.Code, u.AccessRules)
	}
	rec = w.conditional(t, http.MethodPatch, at, me, "", `{"slackMemberID":"U1"}`)
	if u := decode[api.ManagedUser](t, rec); rec.Code != http.StatusOK || len(u.AccessRules) != 1 {
		t.Errorf("patch = %d %+v", rec.Code, u)
	}
	if got := decode[[]api.UserRoleBinding](
		t,
		w.do(t, http.MethodGet, at+"/user-role-bindings", me, ""),
	); len(
		got,
	) != 2 {
		t.Errorf("every binding = %+v", got)
	}
	p = wantProblem(t, w.do(t, http.MethodPut, at, me,
		`{"accessRules":[{"space":"nowhere","spaceAccessLevel":"ADMIN"}]}`),
		http.StatusUnprocessableEntity, "validation-failed")
	wantField(t, p, "/accessRules/0/space")
	page := decode[api.ManagedUserPage](t, w.do(t, http.MethodGet, "/managed-users", me, ""))
	if len(page.Items) != 1 || len(page.Items[0].AccessRules) != 1 {
		t.Errorf("list = %+v", page.Items)
	}
	// A rule in a space the caller does not administer stays.
	legacy := w.tree.spaces["legacy"].ID
	w.authz.on[legacy] = []accessapi.Action{accessapi.ActionSpaceRead, accessapi.ActionSpaceAdmin}
	if rec := w.do(
		t,
		http.MethodPut,
		at,
		me,
		`{"accessRules":[{"space":"legacy","spaceAccessLevel":"WRITE"}]}`,
	); rec.Code != http.StatusOK {
		t.Fatalf("legacy rule = %d %s", rec.Code, rec.Body)
	}
	delete(w.authz.on, legacy)
	p = wantProblem(t, w.do(t, http.MethodPut, at, me, `{"accessRules":[]}`),
		http.StatusUnprocessableEntity, "validation-failed")
	wantField(t, p, "/accessRules")
	w.authz.on[legacy] = []accessapi.Action{accessapi.ActionSpaceRead, accessapi.ActionSpaceAdmin}
	if u := decode[api.ManagedUser](
		t,
		w.do(t, http.MethodPut, at, me, `{"accessRules":[]}`),
	); len(
		u.AccessRules,
	) != 0 {
		t.Errorf("cleared = %+v", u.AccessRules)
	}

	rec = w.do(t, http.MethodPost, "/managed-user-groups", me,
		`{"groupName":"ops","roleBindings":[{"roleID":"space-reader","spaceID":"acme"}]}`)
	if g := decode[api.ManagedUserGroup](
		t,
		rec,
	); rec.Code != http.StatusCreated ||
		len(g.AccessRules) != 0 {
		t.Errorf("group = %d %+v", rec.Code, g)
	}
	p = wantProblem(t, w.do(t, http.MethodPost, "/managed-user-groups", me,
		`{"groupName":"devs","roleBindings":[{"roleID":"space-reader","spaceID":"nowhere"}]}`),
		http.StatusUnprocessableEntity, "validation-failed")
	wantField(t, p, "/roleBindings/0/spaceID")
	wantProblem(t, w.do(t, http.MethodPost, "/managed-user-groups", me,
		`{"groupName":"dup","roleBindings":[{"roleID":"space-reader","spaceID":"acme"},`+
			`{"roleID":"space-reader","spaceID":"acme"}]}`), http.StatusConflict, "already-exists")

	rec = w.do(t, http.MethodPost, "/api-keys", me, `{"name":"deploy","teams":[],`+
		`"accessRules":[{"space":"acme","spaceAccessLevel":"ADMIN"}],`+
		`"roleBindings":[{"roleID":"space-reader","spaceID":"legacy"}]}`)
	k := decode[api.ApiKey](t, rec)
	if rec.Code != http.StatusCreated || !k.Admin || k.SpaceCount != 2 || len(k.AccessRules) != 1 ||
		k.AccessRules[0].SpaceAccessLevel != api.SpaceAccessLevelADMIN {
		t.Fatalf("key = %d %+v", rec.Code, k)
	}
	p = wantProblem(t, w.do(t, http.MethodPost, "/api-keys", me, `{"name":"x","teams":[],`+
		`"accessRules":[{"space":"nowhere","spaceAccessLevel":"READ"}],`+
		`"roleBindings":[{"roleID":"nope","spaceID":"acme"}]}`),
		http.StatusUnprocessableEntity, "validation-failed")
	wantField(t, p, "/accessRules/0/space", "/roleBindings/0/roleID")
	wantProblem(t, w.do(t, http.MethodPost, "/api-keys", me, `{"name":"","teams":[]}`),
		http.StatusUnprocessableEntity, "validation-failed")
	wantProblem(t, w.do(t, http.MethodPost, "/managed-users", me,
		`{"username":"dup","roleBindings":[{"roleID":"space-reader","spaceID":"acme"},`+
			`{"roleID":"space-reader","spaceID":"acme"}]}`), http.StatusConflict, "already-exists")
	wantProblem(
		t,
		w.do(t, http.MethodPost, "/managed-users", me, `{"username":"","roleBindings":[]}`),
		http.StatusUnprocessableEntity,
		"validation-failed",
	)
}

// TestUnitBindingsDown checks that each binding operation answers 503 with
// the store down.
func TestUnitBindingsDown(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt
	bob := w.invite(t, me, "bob", "[]")
	b := decode[api.UserRoleBinding](t, w.do(t, http.MethodPost, "/user-role-bindings", me,
		fmt.Sprintf(`{"userID":%q,"roleID":"space-reader","spaceID":"acme"}`, bob.Id)))
	w.st.down = true
	for _, r := range [][3]string{
		{http.MethodGet, "/user-role-bindings/" + b.Id, ""},
		{http.MethodPost, "/user-role-bindings", fmt.Sprintf(`{"userID":%q,"roleID":"r","spaceID":"s"}`, bob.Id)},
		{http.MethodPut, "/user-role-bindings/" + b.Id, `{"userID":"x","roleID":"r","spaceID":"s"}`},
		{http.MethodDelete, "/user-role-bindings/" + b.Id, ""},
		{http.MethodGet, "/managed-users/" + bob.Id + "/user-role-bindings", ""},
		{http.MethodGet, "/managed-users", ""},
		{http.MethodPost, "/managed-users", `{"username":"eve","roleBindings":[{"roleID":"r","spaceID":"s"}]}`},
	} {
		if rec := w.do(t, r[0], r[1], me, r[2]); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d %s", r[0], r[1], rec.Code, rec.Body)
		}
	}
}

// TestUnitBindingError checks the problem each binding error answers.
func TestUnitBindingError(t *testing.T) {
	if pe, ok := errors.AsType[*problem.Error](bindingError(&binding.Duplicate{Index: 2}, "binding",
		"/bindings")); !ok || !strings.Contains(pe.Problem.Detail, "/bindings/2") {
		t.Errorf("a duplicate = %+v", pe)
	}
	if pe, ok := errors.AsType[*problem.Error](
		bindingError(port.ErrNotFound, "API key", ""),
	); !ok ||
		pe.Problem.Detail != "No such API key in this account." {
		t.Errorf("not found = %+v", pe)
	}
	down := errors.New("down")
	if err := bindingError(down, "binding", ""); !errors.Is(err, down) {
		t.Errorf("an unnamed error = %v", err)
	}
	if err := joinInvalid([]error{nil, down}); !errors.Is(err, down) {
		t.Errorf("joinInvalid = %v", err)
	}
}

// TestUnitTopAdmin checks that only space-admin on the top space makes
// an actor its account's admin.
func TestUnitTopAdmin(t *testing.T) {
	acct := kernel.NewAccountID()
	on := func(slug string, space kernel.ULID) []binding.Listed {
		return []binding.Listed{{Binding: binding.Binding{SpaceID: space}, Role: slug}}
	}
	if !topAdmin(on(role.SpaceAdmin, acct.ULID), acct) ||
		topAdmin(on(role.SpaceWriter, acct.ULID), acct) ||
		topAdmin(on(role.SpaceAdmin, kernel.NewULID()), acct) {
		t.Error("topAdmin")
	}
}

// TestUnitBindingsNoPrincipal checks that each binding operation refuses
// a request that reaches it with no principal.
func TestUnitBindingsNoPrincipal(t *testing.T) {
	s := Server{}
	ctx := t.Context()
	calls := map[string]func() error{}
	add := func(name string, call func() (any, error)) {
		calls[name] = func() error { _, err := call(); return err }
	}
	add(
		"user",
		func() (any, error) { return s.UserRoleBinding(ctx, api.UserRoleBindingRequestObject{}) },
	)
	add("user create", func() (any, error) {
		return s.UserRoleBindingCreate(
			ctx,
			api.UserRoleBindingCreateRequestObject{Body: &api.UserRoleBindingInput{}},
		)
	})
	add("user batch", func() (any, error) {
		return s.UserRoleBindingBatchCreate(ctx, api.UserRoleBindingBatchCreateRequestObject{
			Body: &api.UserRoleBindingBatchCreateJSONRequestBody{},
		})
	})
	add("user update", func() (any, error) {
		return s.UserRoleBindingUpdate(
			ctx,
			api.UserRoleBindingUpdateRequestObject{Body: &api.UserRoleBindingInput{}},
		)
	})
	add("user patch", func() (any, error) {
		return s.UserRoleBindingPatch(ctx, api.UserRoleBindingPatchRequestObject{})
	})
	add("user delete", func() (any, error) {
		return s.UserRoleBindingDelete(ctx, api.UserRoleBindingDeleteRequestObject{})
	})
	add("user's", func() (any, error) {
		return s.ManagedUserUserRoleBindings(ctx, api.ManagedUserUserRoleBindingsRequestObject{})
	})
	add("group", func() (any, error) {
		return s.UserGroupRoleBinding(ctx, api.UserGroupRoleBindingRequestObject{})
	})
	add("group create", func() (any, error) {
		return s.UserGroupRoleBindingCreate(ctx, api.UserGroupRoleBindingCreateRequestObject{
			Body: &api.UserGroupRoleBindingInput{},
		})
	})
	add("group batch", func() (any, error) {
		return s.UserGroupRoleBindingBatchCreate(
			ctx,
			api.UserGroupRoleBindingBatchCreateRequestObject{
				Body: &api.UserGroupRoleBindingBatchCreateJSONRequestBody{},
			},
		)
	})
	add("group update", func() (any, error) {
		return s.UserGroupRoleBindingUpdate(ctx, api.UserGroupRoleBindingUpdateRequestObject{
			Body: &api.UserGroupRoleBindingInput{},
		})
	})
	add("group patch", func() (any, error) {
		return s.UserGroupRoleBindingPatch(ctx, api.UserGroupRoleBindingPatchRequestObject{})
	})
	add("group delete", func() (any, error) {
		return s.UserGroupRoleBindingDelete(ctx, api.UserGroupRoleBindingDeleteRequestObject{})
	})
	add("group's", func() (any, error) {
		return s.ManagedUserGroupUserRoleBindings(
			ctx,
			api.ManagedUserGroupUserRoleBindingsRequestObject{},
		)
	})
	add(
		"key",
		func() (any, error) { return s.ApiKeyRoleBinding(ctx, api.ApiKeyRoleBindingRequestObject{}) },
	)
	add("key create", func() (any, error) {
		return s.ApiKeyRoleBindingCreate(ctx, api.ApiKeyRoleBindingCreateRequestObject{
			Body: &api.ApiKeyRoleBindingInput{},
		})
	})
	add("key batch", func() (any, error) {
		return s.ApiKeyRoleBindingBatchCreate(ctx, api.ApiKeyRoleBindingBatchCreateRequestObject{
			Body: &api.ApiKeyRoleBindingBatchCreateJSONRequestBody{},
		})
	})
	add("key update", func() (any, error) {
		return s.ApiKeyRoleBindingUpdate(ctx, api.ApiKeyRoleBindingUpdateRequestObject{
			Body: &api.ApiKeyRoleBindingInput{},
		})
	})
	add("key patch", func() (any, error) {
		return s.ApiKeyRoleBindingPatch(ctx, api.ApiKeyRoleBindingPatchRequestObject{})
	})
	add("key delete", func() (any, error) {
		return s.ApiKeyRoleBindingDelete(ctx, api.ApiKeyRoleBindingDeleteRequestObject{})
	})
	add("key's", func() (any, error) {
		return s.ApiKeyApiKeyRoleBindings(ctx, api.ApiKeyApiKeyRoleBindingsRequestObject{})
	})
	for name, call := range calls {
		if pe, ok := errors.AsType[*problem.Error](call()); !ok ||
			pe.Problem.Status != http.StatusUnauthorized {
			t.Errorf("%s = %+v", name, pe)
		}
	}
}

// TestUnitBindingViewsDown checks that a view reading an actor's bindings
// answers 503 with the binding store down and the rest up.
func TestUnitBindingViewsDown(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt
	bob := w.invite(t, me, "bob", "[]")
	rec := w.do(
		t,
		http.MethodPost,
		"/managed-user-groups",
		me,
		`{"groupName":"ops","roleBindings":[]}`,
	)
	ops := decode[api.ManagedUserGroup](t, rec)
	w.binds.down = true
	for _, target := range []string{
		"/managed-users", "/managed-users/" + bob.Id, "/managed-user-groups",
		"/managed-user-groups/" + ops.Id, "/api-keys", "/api-keys/" + w.key.String(),
	} {
		if rec := w.do(
			t,
			http.MethodGet,
			target,
			me,
			"",
		); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s = %d %s", target, rec.Code, rec.Body)
		}
	}
	if rec := w.conditional(
		t,
		http.MethodPatch,
		"/managed-users/"+bob.Id,
		me,
		"",
		`{}`,
	); rec.Code !=
		http.StatusServiceUnavailable {
		t.Errorf("PATCH = %d %s", rec.Code, rec.Body)
	}
}
