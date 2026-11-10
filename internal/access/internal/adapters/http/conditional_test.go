package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/group"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/member"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	managed "github.com/weaveplatform/infraweave/internal/access/internal/core/user"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// people is an in-memory user.Store and group.Store of one account,
// failing whenever st is down. Each change raises the row's version
// unless the expect it carries refuses it.
type people struct {
	st     *store
	acct   kernel.AccountID
	users  map[kernel.MemberID]managed.User
	groups map[kernel.ULID]group.Group
	trail  []kernel.AuditEntry
}

func newPeople(st *store, acct kernel.AccountID) *people {
	return &people{
		st: st, acct: acct,
		users:  map[kernel.MemberID]managed.User{},
		groups: map[kernel.ULID]group.Group{},
	}
}

func (p *people) user(acct kernel.AccountID, id kernel.MemberID) (managed.User, error) {
	if err := p.st.err(); err != nil {
		return managed.User{}, err
	}
	u, ok := p.users[id]
	if !ok || acct != p.acct {
		return managed.User{}, port.ErrNotFound
	}
	return u, nil
}

func (p *people) group(acct kernel.AccountID, id kernel.ULID) (group.Group, error) {
	if err := p.st.err(); err != nil {
		return group.Group{}, err
	}
	g, ok := p.groups[id]
	if !ok || acct != p.acct {
		return group.Group{}, port.ErrNotFound
	}
	return g, nil
}

// sortedUsers are the users in member ID order from the first after
// after, at most limit.
func (p *people) sortedUsers(after kernel.MemberID, limit int) []managed.User {
	var out []managed.User
	for _, u := range p.users {
		if after.IsZero() || u.Member.ID.String() > after.String() {
			out = append(out, u)
		}
	}
	slices.SortFunc(out, func(a, b managed.User) int {
		return strings.Compare(a.Member.ID.String(), b.Member.ID.String())
	})
	return out[:min(len(out), limit)]
}

func (p *people) Users(
	_ context.Context,
	_ kernel.AccountID,
	after kernel.MemberID,
	limit int,
) ([]managed.User, error) {
	return p.sortedUsers(after, limit), p.st.err()
}

func (p *people) User(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.MemberID,
) (managed.User, error) {
	return p.user(acct, id)
}

func (p *people) Invite(
	_ context.Context,
	_ kernel.OrganizationID,
	acct kernel.AccountID,
	in managed.Invite,
	inv managed.Invitation,
	e kernel.AuditEntry,
) (managed.User, error) {
	u := managed.User{
		AccountID: acct, SlackMemberID: in.SlackMemberID, Invitation: &inv, Version: 1,
		Member: member.Member{ID: kernel.NewMemberID(), Login: in.Login, Status: member.Pending},
	}
	p.users[u.Member.ID] = u
	p.trail = append(p.trail, e)
	return u, p.st.err()
}

func (p *people) SetSlackMemberID(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.MemberID,
	expect kernel.Expect,
	slack string,
	e kernel.AuditEntry,
) (managed.User, error) {
	u, err := p.user(acct, id)
	if err != nil {
		return u, err
	}
	if err := expect.Check(u.Version); err != nil {
		return managed.User{}, err
	}
	u.SlackMemberID, u.Version = slack, u.Version+1
	p.users[id] = u
	p.trail = append(p.trail, e)
	return u, nil
}

func (p *people) Resend(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.MemberID,
	at time.Time,
	e kernel.AuditEntry,
) (managed.User, error) {
	u, err := p.user(acct, id)
	if err != nil {
		return u, err
	}
	inv := managed.Invitation{CreatedAt: at, SentAt: at}
	u.Invitation, u.Version = &inv, u.Version+1
	p.users[id] = u
	p.trail = append(p.trail, e)
	return u, nil
}

func (p *people) Remove(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.MemberID,
	expect kernel.Expect,
	e kernel.AuditEntry,
) ([]kernel.ULID, error) {
	u, err := p.user(acct, id)
	if err != nil {
		return nil, err
	}
	if err := expect.Check(u.Version); err != nil {
		return nil, err
	}
	delete(p.users, id)
	p.trail = append(p.trail, e)
	return nil, nil
}

func (p *people) PersonalAPIKeys(
	_ context.Context,
	_ kernel.AccountID,
	_ kernel.MemberID,
	_ kernel.ULID,
	_ int,
) ([]apikey.Key, error) {
	return []apikey.Key{{ID: kernel.NewULID(), Name: "laptop", Type: apikey.TypeSecret}},
		p.st.err()
}

func (p *people) RecordRequest(_ context.Context, e kernel.AuditEntry) error {
	p.trail = append(p.trail, e)
	return p.st.err()
}

func (p *people) Groups(
	_ context.Context,
	_ kernel.AccountID,
	after kernel.ULID,
	limit int,
) ([]group.Group, error) {
	var out []group.Group
	for _, g := range p.groups {
		if g.ID.String() > after.String() || after.IsZero() {
			out = append(out, g)
		}
	}
	slices.SortFunc(out, func(a, b group.Group) int {
		return strings.Compare(a.ID.String(), b.ID.String())
	})
	return out[:min(len(out), limit)], p.st.err()
}

func (p *people) Group(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (group.Group, error) {
	return p.group(acct, id)
}

func (p *people) CreateGroup(
	_ context.Context,
	g group.Group,
	e kernel.AuditEntry,
) (group.Group, error) {
	g.Version = 1
	p.groups[g.ID] = g
	p.trail = append(p.trail, e)
	return g, p.st.err()
}

func (p *people) SetDescription(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	expect kernel.Expect,
	description string,
	e kernel.AuditEntry,
) (group.Group, error) {
	g, err := p.group(acct, id)
	if err != nil {
		return g, err
	}
	if err := expect.Check(g.Version); err != nil {
		return group.Group{}, err
	}
	g.Description, g.Version = description, g.Version+1
	p.groups[id] = g
	p.trail = append(p.trail, e)
	return g, nil
}

func (p *people) DeleteGroup(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	expect kernel.Expect,
	e kernel.AuditEntry,
) error {
	g, err := p.group(acct, id)
	if err != nil {
		return err
	}
	if err := expect.Check(g.Version); err != nil {
		return err
	}
	delete(p.groups, id)
	p.trail = append(p.trail, e)
	return nil
}

func (p *people) GroupUsers(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	after kernel.MemberID,
	limit int,
) ([]managed.User, error) {
	if _, err := p.group(acct, id); err != nil {
		return nil, err
	}
	return p.sortedUsers(after, limit), nil
}

func (p *people) AvailableGroups(
	_ context.Context,
	_ kernel.AccountID,
	after string,
	_ int,
) ([]string, error) {
	var out []string
	for _, n := range []string{"dev", "ops"} {
		if n > after {
			out = append(out, n)
		}
	}
	return out, p.st.err()
}

// conditional sends a request as bearer with the If-Match ifMatch, when
// it is not empty, and a merge patch's content type on a PATCH.
func (w *world) conditional(
	t *testing.T,
	method, target, bearer, ifMatch, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	return w.send(t, method, target, body, func(req *http.Request) {
		req.Header.Set("Authorization", bearer)
		if ifMatch != "" {
			req.Header.Set("If-Match", ifMatch)
		}
		if method == http.MethodPatch {
			req.Header.Set("Content-Type", "application/merge-patch+json")
		}
	})
}

// wantTag checks that rec answered 200 with the entity tag of version.
func wantTag(t *testing.T, what string, rec *httptest.ResponseRecorder, version string) {
	t.Helper()
	if rec.Code != http.StatusOK || rec.Header().Get("ETag") != `"`+version+`"` {
		t.Errorf("%s = %d, ETag %q, want %q: %s", what, rec.Code, rec.Header().Get("ETag"),
			`"`+version+`"`, rec.Body)
	}
}

// wantStale checks that rec answered 412 precondition-failed.
func wantStale(t *testing.T, what string, rec *httptest.ResponseRecorder) {
	t.Helper()
	if p := decode[problem.Problem](t, rec); rec.Code != http.StatusPreconditionFailed ||
		p.Type != problem.PreconditionFailed.New("").Type {
		t.Errorf("%s = %d %+v, want 412", what, rec.Code, p)
	}
}

// TestUnitSpaceConditional reads a space's ETag, refuses a PUT, PATCH or
// DELETE whose If-Match names another version without changing the
// space, accepts one naming the version or *, and merges a PATCH into
// the space's input: a member it leaves out is kept, an array it names
// replaces the space's whole, and a null resets the member.
func TestUnitSpaceConditional(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt
	rec := w.do(t, http.MethodPost, "/spaces", me,
		`{"name":"Dev","description":"d","inheritEntities":true,"labels":["b","a"],`+
			`"parentSpace":"legacy"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	dev := "/spaces/" + decode[api.Space](t, rec).Id
	wantTag(t, "read", w.do(t, http.MethodGet, dev, me, ""), "1")

	put := `{"name":"Changed","description":"","inheritEntities":true,"parentSpace":"legacy"}`
	wantStale(t, "a stale PUT", w.conditional(t, http.MethodPut, dev, me, `"7"`, put))
	wantStale(t, "a weak PUT", w.conditional(t, http.MethodPut, dev, me, `W/"1"`, put))
	wantStale(t, "a stale PATCH", w.conditional(t, http.MethodPatch, dev, me, `"7"`,
		`{"name":"Changed"}`))
	wantStale(t, "a stale DELETE", w.conditional(t, http.MethodDelete, dev, me, `"7"`, ""))
	rec = w.do(t, http.MethodGet, dev, me, "")
	if got := decode[api.Space](t, rec); got.Name != "Dev" ||
		rec.Header().Get("ETag") != `"1"` {
		t.Errorf("after the stale writes = %+v, ETag %s", got, rec.Header().Get("ETag"))
	}

	rec = w.conditional(t, http.MethodPut, dev, me, `"0", "1"`,
		`{"name":"Dev","description":"d","inheritEntities":true,"labels":["b","a"],`+
			`"parentSpace":"legacy"}`)
	wantTag(t, "a PUT naming the version", rec, "2")
	wantTag(t, "a PUT of any version", w.conditional(t, http.MethodPut, dev, me, "*",
		`{"name":"Dev","description":"d","inheritEntities":true,"labels":["b","a"],`+
			`"parentSpace":"legacy"}`), "3")

	rec = w.conditional(t, http.MethodPatch, dev, me, "", `{"name":"Development"}`)
	wantTag(t, "a PATCH of the name", rec, "4")
	if got := decode[api.Space](t, rec); got.Name != "Development" || got.Description != "d" ||
		!got.InheritEntities || !slices.Equal(got.Labels, []string{"b", "a"}) ||
		got.ParentSpace == nil || *got.ParentSpace != "legacy" {
		t.Errorf("a PATCH kept %+v", got)
	}
	rec = w.conditional(t, http.MethodPatch, dev, me, `"4"`, `{"labels":["c"]}`)
	wantTag(t, "a PATCH of the labels", rec, "5")
	if got := decode[api.Space](t, rec); !slices.Equal(got.Labels, []string{"c"}) {
		t.Errorf("a PATCH's labels = %v", got.Labels)
	}
	rec = w.conditional(t, http.MethodPatch, dev, me, "", `{"labels":null,"description":null}`)
	wantTag(t, "a PATCH of nulls", rec, "6")
	if got := decode[api.Space](t, rec); len(got.Labels) != 0 || got.Description != "" ||
		got.Name != "Development" {
		t.Errorf("a PATCH of nulls = %+v", got)
	}
	rec = w.conditional(t, http.MethodPatch, "/spaces/acme", me, "", `{"description":"top"}`)
	if got := decode[api.Space](t, rec); rec.Code != http.StatusOK || got.ParentSpace != nil ||
		got.Description != "top" {
		t.Errorf("a PATCH of the top space = %d %+v", rec.Code, got)
	}

	for _, c := range []struct {
		target, body string
		code         int
	}{
		{"/spaces/nope", `{"name":"x"}`, http.StatusNotFound},
		{dev, `{"name":null}`, http.StatusUnprocessableEntity},
		{dev, `{"parentSpace":"nope"}`, http.StatusNotFound},
	} {
		if rec := w.conditional(t, http.MethodPatch, c.target, me, "", c.body); rec.Code != c.code {
			t.Errorf("PATCH %s %s = %d %s, want %d", c.target, c.body, rec.Code, rec.Body, c.code)
		}
	}

	rec = w.conditional(t, http.MethodDelete, dev, me, `"6"`, "")
	if rec.Code != http.StatusOK {
		t.Errorf("a DELETE naming the version = %d %s", rec.Code, rec.Body)
	}
}

// TestUnitSpaceLabels refuses a create and an update whose labels break
// the label rule, naming each label at its pointer.
func TestUnitSpaceLabels(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt
	for _, c := range []struct {
		method, target string
	}{
		{http.MethodPost, "/spaces"},
		{http.MethodPut, "/spaces/legacy"},
	} {
		rec := w.do(t, c.method, c.target, me, `{"name":"x","description":"",`+
			`"inheritEntities":false,"labels":["a"," b","a"],"parentSpace":"acme"}`)
		p := decode[problem.Problem](t, rec)
		if rec.Code != http.StatusUnprocessableEntity || len(p.Errors) != 2 ||
			p.Errors[0].Pointer != "/labels/1" || p.Errors[0].Reason != kernel.ReasonPattern ||
			p.Errors[1].Pointer != "/labels/2" || p.Errors[1].Reason != kernel.ReasonDuplicate {
			t.Errorf("%s %s with bad labels = %d %+v", c.method, c.target, rec.Code, p)
		}
	}
	if got := w.tree.spaces["legacy"]; len(got.Labels) != 0 || len(w.tree.spaces) != 2 {
		t.Errorf("a refused write changed the tree: %+v", got)
	}
}

// TestUnitAPIKeyConditional reads a key's ETag, refuses a stale PUT,
// PATCH or DELETE, and merges a PATCH into the key's input, keeping a
// member the patch sets to null as the PUT keeps one it leaves out.
func TestUnitAPIKeyConditional(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt
	rec := w.do(t, http.MethodPost, "/api-keys", me,
		`{"name":"deploy","description":"CI","teams":["ops","dev"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	key := "/api-keys/" + decode[api.ApiKey](t, rec).Id
	wantTag(t, "read", w.do(t, http.MethodGet, key, me, ""), "1")

	wantStale(t, "a stale PUT", w.conditional(t, http.MethodPut, key, me, `"7"`,
		`{"name":"x"}`))
	wantStale(t, "a stale PATCH", w.conditional(t, http.MethodPatch, key, me, `"7"`,
		`{"name":"x"}`))
	wantStale(t, "a stale DELETE", w.conditional(t, http.MethodDelete, key, me, `"7"`, ""))

	wantTag(t, "a PUT naming the version",
		w.conditional(t, http.MethodPut, key, me, `"1"`, `{"description":"ci"}`), "2")
	rec = w.conditional(t, http.MethodPatch, key, me, "*", `{"teams":["sre"],"name":null}`)
	wantTag(t, "a PATCH", rec, "3")
	if got := decode[api.ApiKey](t, rec); got.Name != "deploy" || got.Description != "ci" ||
		!slices.Equal(got.Teams, []string{"sre"}) {
		t.Errorf("a PATCH = %+v", got)
	}
	if rec := w.conditional(t, http.MethodPatch, "/api-keys/nope", me, "", `{}`); rec.Code !=
		http.StatusNotFound {
		t.Errorf("a PATCH of no key = %d %s", rec.Code, rec.Body)
	}
	if rec := w.conditional(t, http.MethodDelete, key, me, `"3"`, ""); rec.Code != http.StatusOK {
		t.Errorf("a DELETE naming the version = %d %s", rec.Code, rec.Body)
	}
}

// TestUnitKeyInput projects a key onto its update input: a claim mapping
// for an OIDC key alone.
func TestUnitKeyInput(t *testing.T) {
	oidc := keyInput(apikey.Key{Name: "gha", Type: apikey.TypeOIDC, OIDC: apikey.OIDC{
		ClaimMapping: []apikey.Mapping{{Name: "roles", Value: apikey.TeamsClaim}},
	}})
	if *oidc.Name != "gha" || oidc.Teams == nil || len(*oidc.Teams) != 0 ||
		oidc.OidcClaimMappings == nil || !slices.Equal(oidc.OidcClaimMappings.Entries,
		[]api.ClaimMappingEntryInput{{Name: "roles", Value: apikey.TeamsClaim}}) {
		t.Errorf("an OIDC key's input = %+v", oidc)
	}
	if in := keyInput(apikey.Key{Type: apikey.TypeSecret}); in.OidcClaimMappings != nil {
		t.Errorf("a secret key's input = %+v", in)
	}
}

// TestUnitManagedUsers runs the Managed Users operations through the
// router: users are invited, listed, read with their ETag, changed by PUT
// and PATCH, conditionally or not, and removed, and the requests and
// personal keys are served.
func TestUnitManagedUsers(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt
	rec := w.do(t, http.MethodPost, "/managed-users", me,
		`{"username":"bob","invitationEmail":"bob@example.com","roleBindings":[]}`)
	bob := decode[api.ManagedUser](t, rec)
	if rec.Code != http.StatusCreated || bob.Username != "bob" || bob.InvitationEmail == nil {
		t.Fatalf("invite = %d %+v", rec.Code, bob)
	}
	if rec := w.do(t, http.MethodPost, "/managed-users", me,
		`{"username":"carol","roleBindings":[]}`); rec.Code != http.StatusCreated {
		t.Fatalf("invite carol = %d %s", rec.Code, rec.Body)
	}
	first := decode[api.ManagedUserPage](t, w.do(t, http.MethodGet, "/managed-users?limit=1",
		me, ""))
	if len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatalf("first page = %+v", first)
	}
	if next := decode[api.ManagedUserPage](t, w.do(t, http.MethodGet,
		"/managed-users?limit=1&cursor="+*first.NextCursor, me, "")); len(next.Items) != 1 ||
		next.NextCursor != nil || next.Items[0].Id == first.Items[0].Id {
		t.Errorf("second page = %+v", next)
	}

	user := "/managed-users/" + bob.Id
	wantTag(t, "read", w.do(t, http.MethodGet, user, me, ""), "1")
	wantStale(t, "a stale PUT", w.conditional(t, http.MethodPut, user, me, `"7"`,
		`{"accessRules":[],"slackMemberID":"U1"}`))
	wantStale(t, "a stale PATCH", w.conditional(t, http.MethodPatch, user, me, `"7"`,
		`{"slackMemberID":"U1"}`))
	wantStale(t, "a stale DELETE", w.conditional(t, http.MethodDelete, user, me, `"7"`, ""))

	rec = w.conditional(t, http.MethodPut, user, me, `"1"`,
		`{"accessRules":[],"slackMemberID":"U1"}`)
	wantTag(t, "a PUT naming the version", rec, "2")
	rec = w.conditional(t, http.MethodPatch, user, me, "", `{}`)
	wantTag(t, "an empty PATCH", rec, "3")
	if got := decode[api.ManagedUser](t, rec); got.SlackMemberID == nil ||
		*got.SlackMemberID != "U1" {
		t.Errorf("an empty PATCH dropped the Slack member: %+v", got)
	}
	rec = w.conditional(t, http.MethodPatch, user, me, `"3"`, `{"slackMemberID":null}`)
	wantTag(t, "a PATCH of null", rec, "4")
	if got := decode[api.ManagedUser](t, rec); got.SlackMemberID != nil {
		t.Errorf("a PATCH of null kept the Slack member: %+v", got)
	}
	for _, c := range []struct {
		method, target, body string
		code                 int
	}{
		{http.MethodPatch, "/managed-users/nope", `{}`, http.StatusNotFound},
		{
			http.MethodPatch, user, `{"accessRules":[{"space":"legacy","spaceAccessLevel":"READ"}]}`,
			http.StatusUnprocessableEntity,
		},
		{http.MethodPut, "/managed-users/nope", `{"accessRules":[]}`, http.StatusNotFound},
		{http.MethodGet, "/managed-users/nope", "", http.StatusNotFound},
		{http.MethodDelete, "/managed-users/nope", "", http.StatusNotFound},
		{http.MethodPost, user + "/resend-invite", "", http.StatusOK},
		{http.MethodPost, "/managed-users/nope/resend-invite", "", http.StatusNotFound},
		{http.MethodGet, user + "/personal-api-keys", "", http.StatusOK},
		{http.MethodGet, "/managed-users/nope/personal-api-keys", "", http.StatusNotFound},
		{
			http.MethodPost, "/managed-user-invite-requests",
			`{"email":"dan@example.com","userName":"dan"}`, http.StatusCreated,
		},
		{
			http.MethodPost, "/managed-user-invite-requests", `{"email":"","userName":"dan"}`,
			http.StatusUnprocessableEntity,
		},
		{
			http.MethodPost, "/spaces/legacy/role-change-requests",
			`{"requestedAccessLevel":"READ"}`, http.StatusForbidden,
		},
	} {
		if rec := w.conditional(t, c.method, c.target, me, "", c.body); rec.Code != c.code {
			t.Errorf("%s %s = %d %s, want %d", c.method, c.target, rec.Code, rec.Body, c.code)
		}
	}
	if rec := w.conditional(t, http.MethodDelete, user, me, "*", ""); rec.Code != http.StatusOK ||
		len(w.people.users) != 1 {
		t.Errorf("a DELETE of any version = %d %s", rec.Code, rec.Body)
	}

	w.st.down = true
	for _, target := range []string{"/managed-users", "/managed-users/" + first.Items[0].Id} {
		if rec := w.do(t, http.MethodGet, target, me, ""); rec.Code !=
			http.StatusServiceUnavailable {
			t.Errorf("GET %s with the store down = %d", target, rec.Code)
		}
	}
}

// TestUnitManagedUserGroups runs the managed user group operations
// through the router: groups are created, listed, read with their ETag,
// changed by PUT and PATCH, conditionally or not, and deleted.
func TestUnitManagedUserGroups(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt
	bob := kernel.NewMemberID()
	w.people.users[bob] = managed.User{
		AccountID: w.acct, Member: member.Member{ID: bob, Login: "bob"},
	}
	rec := w.do(t, http.MethodPost, "/managed-user-groups", me,
		`{"groupName":"ops","description":"d","roleBindings":[]}`)
	ops := decode[api.ManagedUserGroup](t, rec)
	if rec.Code != http.StatusCreated || ops.GroupName != "ops" {
		t.Fatalf("create = %d %+v", rec.Code, ops)
	}
	if list := decode[api.ManagedUserGroupPage](t, w.do(t, http.MethodGet,
		"/managed-user-groups", me, "")); len(list.Items) != 1 || list.Items[0].Id != ops.Id {
		t.Errorf("groups = %+v", list)
	}
	grp := "/managed-user-groups/" + ops.Id
	if us := decode[api.ManagedUserPage](t, w.do(t, http.MethodGet, grp+"/managed-users", me,
		"")); len(us.Items) != 1 {
		t.Errorf("members = %+v", us)
	}
	if names := decode[api.AvailableUserGroupsPage](t, w.do(t, http.MethodGet,
		"/available-user-groups?limit=1", me, "")); !slices.Equal(names.Items, []string{"dev"}) ||
		names.NextCursor == nil {
		t.Errorf("available = %+v", names)
	}

	wantTag(t, "read", w.do(t, http.MethodGet, grp, me, ""), "1")
	wantStale(t, "a stale PUT", w.conditional(t, http.MethodPut, grp, me, `"7"`,
		`{"description":"x"}`))
	wantStale(t, "a stale PATCH", w.conditional(t, http.MethodPatch, grp, me, `"7"`,
		`{"description":"x"}`))
	wantStale(t, "a stale DELETE", w.conditional(t, http.MethodDelete, grp, me, `"7"`, ""))

	wantTag(t, "a PUT naming the version", w.conditional(t, http.MethodPut, grp, me, `"1"`,
		`{"description":"people"}`), "2")
	rec = w.conditional(t, http.MethodPatch, grp, me, "", `{}`)
	wantTag(t, "an empty PATCH", rec, "3")
	if got := decode[api.ManagedUserGroup](t, rec); got.Description != "people" {
		t.Errorf("an empty PATCH = %+v", got)
	}
	rec = w.conditional(t, http.MethodPatch, grp, me, `"3"`, `{"description":null}`)
	wantTag(t, "a PATCH of null", rec, "4")
	if got := decode[api.ManagedUserGroup](t, rec); got.Description != "" {
		t.Errorf("a PATCH of null = %+v", got)
	}
	for _, c := range []struct {
		method, target, body string
		code                 int
	}{
		{http.MethodPatch, "/managed-user-groups/nope", `{}`, http.StatusNotFound},
		{http.MethodPut, "/managed-user-groups/nope", `{}`, http.StatusNotFound},
		{http.MethodGet, "/managed-user-groups/nope", "", http.StatusNotFound},
		{http.MethodDelete, "/managed-user-groups/nope", "", http.StatusNotFound},
		{http.MethodGet, "/managed-user-groups/nope/managed-users", "", http.StatusNotFound},
		{
			http.MethodPost, "/managed-user-groups", `{"groupName":"","roleBindings":[]}`,
			http.StatusUnprocessableEntity,
		},
	} {
		if rec := w.conditional(t, c.method, c.target, me, "", c.body); rec.Code != c.code {
			t.Errorf("%s %s = %d %s, want %d", c.method, c.target, rec.Code, rec.Body, c.code)
		}
	}
	if rec := w.conditional(t, http.MethodDelete, grp, me, `"4"`, ""); rec.Code != http.StatusOK ||
		len(w.people.groups) != 0 {
		t.Errorf("a DELETE naming the version = %d %s", rec.Code, rec.Body)
	}

	w.st.down = true
	for _, target := range []string{"/managed-user-groups", "/available-user-groups"} {
		if rec := w.do(t, http.MethodGet, target, me, ""); rec.Code !=
			http.StatusServiceUnavailable {
			t.Errorf("GET %s with the store down = %d", target, rec.Code)
		}
	}
}

// TestUnitPatchNoPrincipal checks that each PATCH refuses a call that
// reaches it with no principal.
func TestUnitPatchNoPrincipal(t *testing.T) {
	s := Server{}
	ctx := t.Context()
	for name, call := range map[string]func() error{
		"space": func() error { _, err := s.SpacePatch(ctx, api.SpacePatchRequestObject{}); return err },
		"key": func() error {
			_, err := s.ApiKeyPatch(ctx, api.ApiKeyPatchRequestObject{})
			return err
		},
		"user": func() error {
			_, err := s.ManagedUserPatch(ctx, api.ManagedUserPatchRequestObject{})
			return err
		},
		"group": func() error {
			_, err := s.ManagedUserGroupPatch(ctx, api.ManagedUserGroupPatchRequestObject{})
			return err
		},
	} {
		if err := call(); err == nil {
			t.Errorf("a %s PATCH with no principal succeeded", name)
		}
	}
}
