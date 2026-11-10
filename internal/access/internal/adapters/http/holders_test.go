package http

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/holder"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
)

// heldBook is holder.Store and holder.Evaluator over fixed holders: each
// list's rows with the grants each holds, whatever space is asked.
type heldBook struct {
	w      *world
	rows   map[holder.List][]holder.Row
	grants map[kernel.ULID][]authz.Grant
	groups []holder.Row
	at     time.Time
	// down fails every read of candidates and suggestions, with the rest
	// of the world up.
	down bool
}

var (
	_ holder.Store     = (*heldBook)(nil)
	_ holder.Evaluator = (*heldBook)(nil)
)

func (h *heldBook) Candidates(
	_ context.Context,
	_ kernel.AccountID,
	list holder.List,
) ([]holder.Candidate, error) {
	if h.down {
		return nil, errDown
	}
	var out []holder.Candidate
	for _, r := range h.rows[list] {
		out = append(out, holder.Candidate{ID: r.ID, Actor: r.ID, At: h.at})
	}
	return out, nil
}

func (h *heldBook) SearchHolders(
	_ context.Context,
	_ kernel.AccountID,
	list holder.List,
	_ search.Input,
	_ []kernel.ULID,
) (search.Result[holder.Row], error) {
	var out search.Result[holder.Row]
	for _, r := range h.rows[list] {
		out.Edges = append(out.Edges, search.Edge[holder.Row]{Cursor: r.ID.String(), Node: r})
	}
	return out, nil
}

func (h *heldBook) SuggestHolders(
	_ context.Context,
	_ kernel.AccountID,
	_ holder.List,
	_ search.SuggestionsInput,
	ids []kernel.ULID,
) (search.Suggestions, error) {
	if h.down {
		return search.Suggestions{}, errDown
	}
	return search.Suggestions{Count: len(ids)}, nil
}

func (h *heldBook) HolderGroups(
	context.Context,
	kernel.AccountID,
	[]kernel.ULID,
) ([]holder.Row, error) {
	return h.groups, nil
}

func (h *heldBook) Holders(
	context.Context,
	kernel.AccountID,
	kernel.ULID,
	...binding.ActorKind,
) ([]authz.Holder, error) {
	out := make([]authz.Holder, 0, len(h.rows[holder.Groups]))
	for _, r := range h.rows[holder.Groups] {
		out = append(out, authz.Holder{ID: r.ID, Grants: h.grants[r.ID]})
	}
	return out, nil
}

func (h *heldBook) ExplainEach(
	_ context.Context,
	_ kernel.AccountID,
	_ kernel.ULID,
	whos []authz.Who,
) ([][]authz.Grant, error) {
	out := make([][]authz.Grant, len(whos))
	for i, w := range whos {
		out[i] = h.grants[w.Actor]
	}
	return out, nil
}

// heldPath is the path of a grant view of space with an empty search.
func heldPath(path, space string) string {
	return "/" + path + "?" + url.Values{"space": {space}, "input": {"{}"}}.Encode()
}

func TestUnitGrantViews(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt
	top := w.tree.spaces["acme"].ID
	alice, team, key := kernel.NewULID(), kernel.NewULID(), kernel.NewULID()
	direct := authz.Grant{
		Source: authz.Resident, Binding: kernel.NewULID(), Actor: binding.ActorUser, ActorID: alice,
		Space: top, Role: "space-writer", Origin: binding.OriginAccessRule,
	}
	via := authz.Grant{
		Source:  authz.Propagated,
		Binding: kernel.NewULID(),
		Actor:   binding.ActorGroup,
		ActorID: team,
		Space:   top,
		Role:    "space-reader",
	}
	keyed := authz.Grant{
		Source:  authz.Inherited,
		Binding: kernel.NewULID(),
		Actor:   binding.ActorAPIKey,
		ActorID: key,
		Space:   top,
		Role:    "space-reader",
	}
	w.held.at = time.Unix(1700000000, 0)
	w.held.groups = []holder.Row{{ID: team, Name: "ops", Description: "on call"}}
	w.held.rows = map[holder.List][]holder.Row{
		holder.Users: {{ID: alice, Login: "alice"}},
		holder.UserSessions: {
			{ID: alice, Login: "alice", Name: "Alice", Email: "alice@example.com"},
		},
		holder.Groups:      {{ID: team, Name: "ops"}},
		holder.Keys:        {{ID: key, Name: "deploy"}},
		holder.KeySessions: {{ID: key, Name: "deploy"}},
	}
	w.held.grants = map[kernel.ULID][]authz.Grant{
		alice: {direct, via}, team: {via}, key: {keyed},
	}

	users := decode[api.UsersWithAccessToSpaceOutput](t,
		w.do(t, http.MethodGet, heldPath("users-with-access-to-space", "acme"), me, ""))
	if len(users.Edges) != 1 || users.Edges[0].Node.User.Id != alice.String() ||
		len(users.Edges[0].Node.Roles) != 2 {
		t.Fatalf("users = %+v", users)
	}
	own, group := users.Edges[0].Node.Roles[0], users.Edges[0].Node.Roles[1]
	b, err := own.GrantedVia.AsUserRoleBinding()
	if err != nil || b.Id != direct.Binding.String() || own.Source != api.RESIDENT ||
		own.Propagated ||
		own.Space.Id != "acme" ||
		own.BindingID != direct.Binding.String() {
		t.Errorf("the direct grant = %+v, %+v, %v", own, b, err)
	}
	g, err := group.GrantedVia.AsSpaceAccessGroup()
	if err != nil || g.GroupName != "ops" || g.Description != "on call" || !group.Propagated ||
		group.Source != api.PROPAGATED {
		t.Errorf("the group's grant = %+v, %+v, %v", group, g, err)
	}

	groups := decode[api.UserGroupsWithAccessToSpaceOutput](t,
		w.do(t, http.MethodGet, heldPath("user-groups-with-access-to-space", "acme"), me, ""))
	if len(groups.Edges) != 1 || groups.Edges[0].Node.UserGroup.Id != team.String() ||
		groups.Edges[0].Node.Roles[0].Role.Id != "space-reader" {
		t.Errorf("groups = %+v", groups)
	}
	keys := decode[api.ApiKeysWithAccessToSpaceOutput](t,
		w.do(t, http.MethodGet, heldPath("api-keys-with-access-to-space", "acme"), me, ""))
	if len(keys.Edges) != 1 || keys.Edges[0].Node.Roles[0].Source != api.INHERITED {
		t.Errorf("keys = %+v", keys)
	}
	people := decode[api.UserSessionsWithAccessToSpaceOutput](t,
		w.do(t, http.MethodGet, heldPath("user-sessions-with-access-to-space", "acme"), me, ""))
	if len(people.Edges) != 1 || people.Edges[0].Node.User.SessionCount != 1 ||
		people.Edges[0].Node.User.LastLoginAt != 1700000000 ||
		people.Edges[0].Node.User.Email != "alice@example.com" ||
		len(
			people.Edges[0].Node.Roles,
		) != 2 || people.Edges[0].Node.Roles[0].Origin != api.ACCESSLEVEL ||
		people.Edges[0].Node.Roles[1].Origin != api.ASSIGNEDROLE {
		t.Errorf("user sessions = %+v", people)
	}
	sessions := decode[api.ApiKeySessionsWithAccessToSpaceOutput](t,
		w.do(t, http.MethodGet, heldPath("api-key-sessions-with-access-to-space", "acme"), me, ""))
	if len(sessions.Edges) != 1 || sessions.Edges[0].Node.SessionCount != 1 ||
		sessions.Edges[0].Node.ApiKey.Id != key.String() {
		t.Errorf("key sessions = %+v", sessions)
	}

	for path, n := range map[string]int32{
		"users-with-access-to-space-suggestions":            1,
		"user-groups-with-access-to-space-suggestions":      1,
		"api-keys-with-access-to-space-suggestions":         1,
		"user-sessions-with-access-to-space-suggestions":    1,
		"api-key-sessions-with-access-to-space-suggestions": 1,
	} {
		got := decode[api.SearchSuggestionsOutput](
			t,
			w.do(t, http.MethodGet, heldPath(path, "acme"), me, ""),
		)
		if got.FilteredCount != n {
			t.Errorf("%s = %+v", path, got)
		}
	}

	for _, path := range []string{
		"users-with-access-to-space", "user-groups-with-access-to-space",
		"api-keys-with-access-to-space", "user-sessions-with-access-to-space",
		"api-key-sessions-with-access-to-space",
		"users-with-access-to-space-suggestions", "user-groups-with-access-to-space-suggestions",
		"api-keys-with-access-to-space-suggestions", "user-sessions-with-access-to-space-suggestions",
		"api-key-sessions-with-access-to-space-suggestions",
	} {
		wantProblem(t, w.do(t, http.MethodGet, heldPath(path, "nowhere"), me, ""),
			http.StatusNotFound, "not-found")
	}
}

func TestUnitGrantViewsDown(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt
	w.held.down = true
	for _, path := range []string{"users-with-access-to-space", "users-with-access-to-space-suggestions"} {
		wantProblem(t, w.do(t, http.MethodGet, heldPath(path, "acme"), me, ""),
			http.StatusServiceUnavailable, "service-unavailable")
	}
	s := NewServer(Services{})
	if _, err := s.UsersWithAccessToSpace(
		t.Context(),
		api.UsersWithAccessToSpaceRequestObject{},
	); err == nil {
		t.Error("a view without a principal answers")
	}
	_, err := s.UsersWithAccessToSpaceSuggestions(
		t.Context(),
		api.UsersWithAccessToSpaceSuggestionsRequestObject{},
	)
	if err == nil {
		t.Error("suggestions without a principal answer")
	}
}
