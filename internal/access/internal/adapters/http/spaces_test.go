package http

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/space"
	"github.com/weaveplatform/infraweave/internal/kernel"
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
		f.spaces[slug] = out
	}
	return out, err
}

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
	for _, r := range routes[:5] {
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
