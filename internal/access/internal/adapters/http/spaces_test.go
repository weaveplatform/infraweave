package http

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/space"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// spaceTree is an in-memory space.Store of one account's tree, failing
// whenever st is down.
type spaceTree struct {
	st      *store
	acct    kernel.AccountID
	spaces  map[string]space.Space
	setting bool
}

func newSpaceTree(st *store, a account.Account) *spaceTree {
	top, legacy := space.Seed(a)
	return &spaceTree{st: st, acct: a.ID, spaces: map[string]space.Space{
		top.Slug: top, legacy.Slug: legacy,
	}}
}

func (f *spaceTree) Spaces(context.Context, kernel.AccountID) ([]space.Space, error) {
	out := make([]space.Space, 0, len(f.spaces))
	for _, s := range f.spaces {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b space.Space) int { return strings.Compare(a.Slug, b.Slug) })
	return out, f.st.err()
}

func (f *spaceTree) Space(
	_ context.Context,
	acct kernel.AccountID,
	slug string,
) (space.Space, error) {
	if err := f.st.err(); err != nil {
		return space.Space{}, err
	}
	s, ok := f.spaces[slug]
	if !ok || acct != f.acct {
		return space.Space{}, port.ErrNotFound
	}
	return s, nil
}

func (f *spaceTree) CreateSpace(_ context.Context, s space.Space) error {
	// A new row's version is the column's default.
	s.Version = 1
	f.spaces[s.Slug] = s
	return f.st.err()
}

func (f *spaceTree) UpdateSpace(
	ctx context.Context,
	acct kernel.AccountID,
	slug, parentSlug string,
	edit func(context.Context, space.Space, space.Space, *accessapi.SpaceMove) (space.Space, error),
) (space.Space, error) {
	cur, err := f.Space(ctx, acct, slug)
	if err != nil {
		return space.Space{}, err
	}
	var parent space.Space
	if parentSlug != "" {
		if parent, err = f.Space(ctx, acct, parentSlug); err != nil {
			return space.Space{}, err
		}
	}
	var m *accessapi.SpaceMove
	if parentSlug != "" && parent.ID != cur.ParentID {
		m = &accessapi.SpaceMove{Account: acct, Space: cur.ID}
	}
	out, err := edit(ctx, cur, parent, m)
	if err == nil {
		out.Version = cur.Version + 1
		f.spaces[slug] = out
	}
	return out, err
}

func (*spaceTree) HoldSpace(context.Context, kernel.AccountID, kernel.ULID) error { return nil }

func (f *spaceTree) DeleteSpace(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
	check func(context.Context, space.Space) error,
) error {
	s, err := f.Space(ctx, acct, slug)
	if err != nil {
		return err
	}
	if err := check(ctx, s); err != nil {
		return err
	}
	delete(f.spaces, slug)
	return nil
}

func (f *spaceTree) NonAdminCreation(context.Context, kernel.AccountID) (bool, error) {
	return f.setting, f.st.err()
}

func (f *spaceTree) ToggleNonAdminCreation(context.Context, kernel.AccountID) (bool, error) {
	f.setting = !f.setting
	return f.setting, f.st.err()
}

// Search pages through the reachable spaces by slug, each cursor its
// slug; it checks the input against space.Fields and applies no
// predicate.
func (f *spaceTree) Search(
	ctx context.Context,
	acct kernel.AccountID,
	in search.Input,
	reachable []kernel.ULID,
) (search.Result[space.Space], error) {
	var out search.Result[space.Space]
	if err := search.Check(space.Fields, in); err != nil {
		return out, err
	}
	all, err := f.Spaces(ctx, acct)
	n := int(kernel.PageLimit(in.First))
	for _, sp := range all {
		if slices.Contains(reachable, sp.ID) && sp.Slug > in.After {
			if len(out.Edges) == n {
				out.HasNext = true
				break
			}
			out.Edges = append(out.Edges, search.Edge[space.Space]{Cursor: sp.Slug, Node: sp})
		}
	}
	if len(out.Edges) > 0 {
		out.Start, out.End = out.Edges[0].Cursor, out.Edges[len(out.Edges)-1].Cursor
	}
	out.HasPrevious = in.After != ""
	return out, err
}

// Suggest counts the reachable spaces and lists their slugs as the id
// field's values.
func (f *spaceTree) Suggest(
	ctx context.Context,
	acct kernel.AccountID,
	in search.SuggestionsInput,
	reachable []kernel.ULID,
) (search.Suggestions, error) {
	if err := search.CheckSuggestions(space.Fields, in); err != nil {
		return search.Suggestions{}, err
	}
	all, err := f.Spaces(ctx, acct)
	id := search.Suggestion{Field: space.Fields[0], Listed: true}
	for _, sp := range all {
		if slices.Contains(reachable, sp.ID) {
			id.Values = append(id.Values, sp.Slug)
			id.Counts = append(id.Counts, 1)
		}
	}
	return search.Suggestions{Count: len(id.Values), Fields: []search.Suggestion{id}}, err
}

// TestUnitSpaces runs the Spaces operations through the router as an
// account admin: a space is created, read, listed page by page and by
// the actions it allows, renamed and moved, and deleted; then the
// refusals and failures.
func TestUnitSpaces(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt
	top := w.acct.ULID
	legacy := w.tree.spaces[space.LegacySlug]
	w.authz.on[legacy.ID] = []accessapi.Action{accessapi.ActionSpaceRead}

	rec := w.do(
		t,
		http.MethodPost,
		"/spaces",
		me,
		`{"name":"Dev","description":"d","inheritEntities":true,"labels":["x"],"parentSpace":"legacy"}`,
	)
	dev := decode[api.Space](t, rec)
	if rec.Code != http.StatusCreated || !strings.HasPrefix(dev.Id, "dev-") || dev.Name != "Dev" ||
		dev.ParentSpace == nil || *dev.ParentSpace != space.LegacySlug || !dev.InheritEntities ||
		!slices.Equal(dev.Labels, []string{"x"}) || dev.AccessLevel != api.SpaceAccessLevelNONE ||
		len(dev.AllowedActions) != 0 {
		t.Fatalf("create = %d %+v", rec.Code, dev)
	}
	devID := w.tree.spaces[dev.Id].ID
	w.authz.on[devID] = []accessapi.Action{accessapi.ActionSpaceRead, accessapi.ActionSpaceWrite}

	got := decode[api.Space](t, w.do(t, http.MethodGet, "/spaces/"+dev.Id, me, ""))
	if got.AccessLevel != api.SpaceAccessLevelWRITE ||
		!slices.Equal(got.AllowedActions, []api.Action{api.SPACEREAD, api.SPACEWRITE}) {
		t.Errorf("read = %+v", got)
	}
	acme := decode[api.Space](t, w.do(t, http.MethodGet, "/spaces/acme", me, ""))
	if acme.ParentSpace != nil || acme.AccessLevel != api.SpaceAccessLevelADMIN ||
		acme.Labels == nil {
		t.Errorf("top space = %+v", acme)
	}

	var seen []string
	cursor := ""
	for range 4 {
		target := "/spaces?limit=1"
		if cursor != "" {
			target += "&cursor=" + url.QueryEscape(cursor)
		}
		pg := decode[api.SpacePage](t, w.do(t, http.MethodGet, target, me, ""))
		for _, s := range pg.Items {
			seen = append(seen, s.Id)
		}
		if pg.NextCursor == nil {
			break
		}
		cursor = *pg.NextCursor
	}
	if want := []string{"acme", dev.Id, "legacy"}; !slices.Equal(seen, want) {
		t.Errorf("pages = %v, want %v", seen, want)
	}
	filter := url.QueryEscape(`{"actions":["SPACE_WRITE"]}`)
	pg := decode[api.SpacePage](t, w.do(t, http.MethodGet, "/spaces?input="+filter, me, ""))
	if len(pg.Items) != 2 || pg.Items[0].Id != "acme" || pg.Items[1].Id != dev.Id {
		t.Errorf("spaces allowing SPACE_WRITE = %+v", pg.Items)
	}

	rec = w.do(t, http.MethodPut, "/spaces/"+dev.Id, me,
		`{"name":"Development","description":"","inheritEntities":false,"parentSpace":"acme"}`)
	moved := decode[api.Space](t, rec)
	if rec.Code != http.StatusOK || moved.Name != "Development" || *moved.ParentSpace != "acme" ||
		moved.InheritEntities || len(moved.Labels) != 0 {
		t.Errorf("update = %d %+v", rec.Code, moved)
	}

	for _, c := range []struct {
		method, target, body string
		code                 int
		typ                  problem.Type
		detail               string
	}{
		{
			http.MethodDelete, "/spaces/acme", "", http.StatusUnprocessableEntity, problem.ValidationFailed,
			"The space change is refused: the top space acme cannot be deleted.",
		},
		{
			http.MethodPut, "/spaces/" + dev.Id, `{"name":"","description":"","inheritEntities":false,` +
				`"parentSpace":"acme"}`, http.StatusUnprocessableEntity, problem.ValidationFailed,
			"A space needs a name.",
		},
		{
			http.MethodPut, "/spaces/" + dev.Id, `{"name":"x","description":"","inheritEntities":false,` +
				`"parentSpace":"nope"}`, http.StatusNotFound, problem.NotFound,
			`No space "nope" in this account.`,
		},
		{
			http.MethodGet, "/spaces/nope", "", http.StatusNotFound, problem.NotFound,
			`No space "nope" in this account.`,
		},
		{
			http.MethodDelete, "/spaces/nope", "", http.StatusNotFound, problem.NotFound,
			`No space "nope" in this account.`,
		},
	} {
		rec := w.do(t, c.method, c.target, me, c.body)
		if p := decode[problem.Problem](t, rec); rec.Code != c.code ||
			p.Type != c.typ.New("").Type || p.Detail != c.detail {
			t.Errorf(
				"%s %s = %d %+v, want %d %q",
				c.method,
				c.target,
				rec.Code,
				p,
				c.code,
				c.detail,
			)
		}
	}

	rec = w.do(t, http.MethodDelete, "/spaces/"+dev.Id, me, "")
	if gone := decode[api.Space](t, rec); rec.Code != http.StatusOK || gone.Id != dev.Id ||
		gone.AccessLevel != api.SpaceAccessLevelWRITE {
		t.Errorf("delete = %d %+v", rec.Code, gone)
	}
	if _, ok := w.tree.spaces[dev.Id]; ok {
		t.Error("the deleted space remains")
	}

	// An admin of legacy alone, with the setting off, may rename it but not
	// move it, and the problem names SPACE_ADMIN.
	w.authz.on = map[kernel.ULID][]accessapi.Action{
		top:       {accessapi.ActionSpaceRead},
		legacy.ID: {accessapi.ActionSpaceRead, accessapi.ActionSpaceAdmin},
	}
	rec = w.do(t, http.MethodPut, "/spaces/legacy", me,
		`{"name":"Old","description":"","inheritEntities":true,"parentSpace":"acme"}`)
	if p := decode[problem.Problem](t, rec); rec.Code != http.StatusForbidden ||
		p.Action != string(accessapi.ActionSpaceAdmin) {
		t.Errorf("a space admin's inheritance change = %d %+v", rec.Code, p)
	}
	rec = w.do(t, http.MethodPut, "/spaces/legacy", me,
		`{"name":"Old","description":"","inheritEntities":false,"parentSpace":"acme"}`)
	if rec.Code != http.StatusOK || decode[api.Space](t, rec).Name != "Old" {
		t.Errorf("a space admin's rename = %d %s", rec.Code, rec.Body)
	}
	rec = w.do(t, http.MethodPost, "/spaces", me,
		`{"name":"Sub","description":"","inheritEntities":true,"parentSpace":"legacy"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("a space admin's create with the setting off = %d %s", rec.Code, rec.Body)
	}
}

// TestUnitSpaceSearch searches the readable spaces page by page, lists
// their suggestions, refuses an unknown field with its pointer, and
// lists the action catalogue.
func TestUnitSpaceSearch(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt
	legacy := w.tree.spaces[space.LegacySlug]
	w.authz.on = map[kernel.ULID][]accessapi.Action{
		w.acct.ULID: {accessapi.ActionSpaceRead},
		legacy.ID:   {accessapi.ActionSpaceRead},
	}

	rec := w.do(t, http.MethodPost, "/spaces/search", me, `{"first":1}`)
	first := decode[api.SearchSpacesOutput](t, rec)
	if rec.Code != http.StatusOK || len(first.Edges) != 1 || first.Edges[0].Node.Id != "acme" ||
		!first.PageInfo.HasNextPage || first.PageInfo.HasPreviousPage ||
		first.PageInfo.EndCursor != first.Edges[0].Cursor {
		t.Fatalf("first page = %d %+v", rec.Code, first)
	}
	next := decode[api.SearchSpacesOutput](t, w.do(t, http.MethodPost, "/spaces/search", me,
		`{"first":1,"after":"`+first.PageInfo.EndCursor+`","requestedPage":"NEXT",`+
			`"orderBy":[{"field":"id","direction":"DESC"}],"fullTextSearch":"x"}`))
	if len(next.Edges) != 1 || next.Edges[0].Node.Id != "legacy" || next.PageInfo.HasNextPage ||
		!next.PageInfo.HasPreviousPage {
		t.Errorf("next page = %+v", next)
	}

	rec = w.do(t, http.MethodPost, "/spaces/search/suggestions", me, `{"fields":["id"]}`)
	sg := decode[api.SearchSuggestionsOutput](t, rec)
	if rec.Code != http.StatusOK || sg.FilteredCount != 2 || len(sg.Fields) != 1 ||
		!slices.Equal(sg.Fields[0].PossibleValuesString.Values, []string{"acme", "legacy"}) {
		t.Errorf("suggestions = %d %+v", rec.Code, sg)
	}

	for target, body := range map[string]string{
		"/spaces/search": `{"predicates":[{"field":"owner","constraint":{"stringEquals":["x"]}}]}`,
		"/spaces/search/suggestions": `{"predicates":[{"field":"owner",` +
			`"constraint":{"stringEquals":["x"]}}]}`,
	} {
		rec := w.do(t, http.MethodPost, target, me, body)
		p := decode[problem.Problem](t, rec)
		if rec.Code != http.StatusUnprocessableEntity || len(p.Errors) != 1 ||
			p.Errors[0] != (problem.FieldError{
				Pointer: "/predicates/0/field", Reason: "unknown-field",
				Message: `The search has no field "owner".`,
			}) {
			t.Errorf("%s with an unknown field = %d %+v", target, rec.Code, p)
		}
	}

	filter := url.QueryEscape(`{"actions":["SPACE_READ","` + string(deprecatedAction(t)) + `"]}`)
	if pg := decode[api.SpacePage](
		t,
		w.do(t, http.MethodGet, "/spaces?input="+filter, me, ""),
	); len(
		pg.Items,
	) != 0 {
		t.Errorf("spaces allowing a deprecated action = %+v", pg.Items)
	}

	rec = w.do(t, http.MethodGet, "/actions", me, "")
	cat := decode[[]api.ActionCatalogueEntry](t, rec)
	if rec.Code != http.StatusOK || len(cat) != len(accessapi.Actions()) {
		t.Fatalf("catalogue = %d, %d actions", rec.Code, len(cat))
	}
	for i, a := range accessapi.Actions() {
		if cat[i] != (api.ActionCatalogueEntry{
			Action: api.Action(a.Action), Disabled: a.Disabled, Deprecated: a.Deprecated,
		}) {
			t.Errorf("catalogue[%d] = %+v, want %+v", i, cat[i], a)
		}
	}
	if rec := w.do(t, http.MethodGet, "/actions", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("catalogue without a credential = %d", rec.Code)
	}
}

func deprecatedAction(t *testing.T) accessapi.Action {
	t.Helper()
	for _, a := range accessapi.Actions() {
		if a.Deprecated {
			return a.Action
		}
	}
	t.Fatal("the catalogue deprecates no action")
	return ""
}

// TestUnitSuggestions converts a suggestion of each field type.
func TestUnitSuggestions(t *testing.T) {
	got := suggestions(search.Suggestions{Count: 3, Fields: []search.Suggestion{
		{
			Field: search.Field{Name: "b", Type: search.Boolean}, Listed: true,
			Values: []string{"true", "false"}, Counts: []int{2, 1},
		},
		{
			Field: search.Field{Name: "e", Type: search.Enum}, Listed: true,
			Values: []string{"UP"}, Counts: []int{3},
		},
		{
			Field:  search.Field{Name: "h", Type: search.Hierarchy},
			Listed: true,
			Nodes: []search.HierarchyNode{
				{ID: "a", Display: "A"},
				{ID: "b", Parent: "a", Display: "B"},
			},
			Counts: []int{3, 1},
			Direct: []int{2, 1},
		},
		{Field: search.Field{Name: "t", Type: search.Time, Orderable: true}, Listed: true},
		{Field: search.Field{Name: "s", Type: search.String}},
	}})
	f := got.Fields
	if got.FilteredCount != 3 || len(f) != 5 ||
		!slices.Equal(f[0].PossibleValuesBoolean.Values, []bool{true, false}) ||
		!slices.Equal(f[0].PossibleValuesBoolean.Counts, []int32{2, 1}) ||
		!slices.Equal(f[1].PossibleValuesEnum.Values, []string{"UP"}) ||
		f[2].PossibleValuesHierarchy.Values[0].ParentId != nil ||
		*f[2].PossibleValuesHierarchy.Values[1].ParentId != "a" ||
		!slices.Equal(*f[2].PossibleValuesHierarchy.DirectCounts, []int32{2, 1}) ||
		!*f[3].PossibleValuesTime || !f[3].Orderable || *f[3].Type != "TIME" ||
		f[4].PossibleValuesString != nil || !f[4].Filterable {
		t.Errorf("suggestions = %+v", got)
	}
	if count(math.MaxInt32+1) != math.MaxInt32 {
		t.Error("count does not clamp")
	}
}

// TestUnitSearchInput converts every member of the reference's input.
func TestUnitSearchInput(t *testing.T) {
	var in api.SearchInput
	if err := json.Unmarshal([]byte(`{"first":5,"after":"c","fullTextSearch":"x",
		"requestedPage":"PREVIOUS","orderBy":[{"field":"name"},{"field":"id","direction":"DESC"}],
		"predicates":[
			{"field":"a","exclude":true,"constraint":{"booleanEquals":[true]}},
			{"field":"b","constraint":{"stringEquals":[]}},
			{"field":"c","constraint":{"hierarchyNode":{"nodes":[{"equals":"x","includeDescendants":true}]}}},
			{"field":"d","constraint":{"timeInRange":{"start":1}}},
			{"field":"e","constraint":{"timeInLast":"LAST1D"}},
			{"field":"f","constraint":{"enumEquals":["UP"],"stringMatches":["m*"],
				"hierarchyNodeValueEquals":["y"]}}]}`), &in); err != nil {
		t.Fatal(err)
	}
	want := search.Input{
		First: new(int32(5)), After: "c", FullText: "x", Page: search.Previous,
		OrderBy: []search.Order{{Field: "name"}, {Field: "id", Desc: true}},
		Predicates: []search.Predicate{
			{Field: "a", Exclude: true, Constraint: search.Constraint{BooleanEquals: []bool{true}}},
			{Field: "b", Constraint: search.Constraint{StringEquals: []string{}}},
			{Field: "c", Constraint: search.Constraint{
				Hierarchy: []search.Node{{Equals: "x", Descendants: true}},
			}},
			{
				Field:      "d",
				Constraint: search.Constraint{TimeInRange: &search.TimeRange{Start: new(int32(1))}},
			},
			{Field: "e", Constraint: search.Constraint{TimeInLast: "LAST1D"}},
			{Field: "f", Constraint: search.Constraint{
				EnumEquals: []string{
					"UP",
				},
				StringMatches:   []string{"m*"},
				HierarchyValues: []string{"y"},
			}},
		},
	}
	if got := searchInput(in); !reflect.DeepEqual(got, want) {
		t.Errorf("searchInput =\n%+v\nwant\n%+v", got, want)
	}
	fields := []string{"id"}
	got := suggestionsInput(api.SearchSuggestionsInput{Fields: &fields})
	if !slices.Equal(got.Fields, fields) || got.Predicates != nil {
		t.Errorf("suggestionsInput = %+v", got)
	}
	var none *[]string
	if derefOr(none) != nil || derefOr(&[]string{}) == nil {
		t.Error("derefOr does not keep an empty list set")
	}
}

// TestUnitSpaceSetting reads and flips the non-admin creation setting.
func TestUnitSpaceSetting(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt
	for _, want := range []bool{true, false} {
		rec := w.do(t, http.MethodPost, "/account/toggle-allow-non-admin-space-creation", me, "")
		if got := decode[bool](t, rec); rec.Code != http.StatusOK || got != want {
			t.Errorf("toggle = %d %t, want %t", rec.Code, got, want)
		}
		if got := decode[bool](
			t,
			w.do(t, http.MethodGet, "/allow-non-admin-space-creation", me, ""),
		); got != want {
			t.Errorf("setting = %t, want %t", got, want)
		}
	}
}

// TestUnitSpacesDown checks that each Spaces operation answers 503 when
// the store or the authorizer cannot be read.
func TestUnitSpacesDown(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt
	// Authenticate once, so the session is cached and the store fails
	// only behind the middleware.
	w.do(t, http.MethodGet, "/viewer", me, "")
	body := `{"name":"x","description":"","inheritEntities":false,"parentSpace":"acme"}`
	routes := [][3]string{
		{http.MethodGet, "/spaces", ""},
		{http.MethodPost, "/spaces/search", "{}"},
		{http.MethodPost, "/spaces/search/suggestions", "{}"},
		{http.MethodGet, "/spaces/legacy", ""},
		{http.MethodPost, "/spaces", body},
		{http.MethodPut, "/spaces/legacy", body},
		{http.MethodDelete, "/spaces/legacy", ""},
		{http.MethodGet, "/allow-non-admin-space-creation", ""},
		{http.MethodPost, "/account/toggle-allow-non-admin-space-creation", ""},
	}
	w.st.down = true
	for _, r := range routes {
		if rec := w.do(t, r[0], r[1], me, r[2]); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s with the store down: %d %s", r[0], r[1], rec.Code, rec.Body)
		}
	}
	w.st.down = false
	w.authz.err = errDown
	for _, r := range routes[:7] {
		if rec := w.do(t, r[0], r[1], me, r[2]); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s with the authorizer down: %d %s", r[0], r[1], rec.Code, rec.Body)
		}
	}
}

// TestUnitSpacesNoPrincipal checks that each Spaces operation refuses a
// call that reaches it with no principal.
func TestUnitSpacesNoPrincipal(t *testing.T) {
	s := Server{}
	ctx := t.Context()
	in := &api.SpaceInput{Name: "x"}
	for name, call := range map[string]func() error{
		"list": func() error { _, err := s.Spaces(ctx, api.SpacesRequestObject{}); return err },
		"search": func() error {
			_, err := s.SearchSpaces(ctx, api.SearchSpacesRequestObject{Body: &api.SearchInput{}})
			return err
		},
		"suggest": func() error {
			_, err := s.SearchSpacesSuggestions(ctx, api.SearchSpacesSuggestionsRequestObject{
				Body: &api.SearchSuggestionsInput{},
			})
			return err
		},
		"read": func() error { _, err := s.Space(ctx, api.SpaceRequestObject{}); return err },
		"create": func() error {
			_, err := s.SpaceCreate(ctx, api.SpaceCreateRequestObject{Body: in})
			return err
		},
		"update": func() error {
			_, err := s.SpaceUpdate(ctx, api.SpaceUpdateRequestObject{Body: in})
			return err
		},
		"delete": func() error { _, err := s.SpaceDelete(ctx, api.SpaceDeleteRequestObject{}); return err },
		"setting": func() error {
			_, err := s.AllowNonAdminSpaceCreation(ctx, api.AllowNonAdminSpaceCreationRequestObject{})
			return err
		},
		"toggle": func() error {
			_, err := s.AccountToggleAllowNonAdminSpaceCreation(ctx,
				api.AccountToggleAllowNonAdminSpaceCreationRequestObject{})
			return err
		},
	} {
		if err := call(); err == nil {
			t.Errorf("%s with no principal succeeded", name)
		}
	}
}
