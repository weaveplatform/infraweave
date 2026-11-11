package pg_test

import (
	"errors"
	"slices"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/pg"
)

// TestAccServiceSearchUsers searches and suggests over the stacks that use
// a GITLAB integration: bound and plain, which names none and so uses its
// provider's default, in team; elsewhere, bound to another integration;
// hub, on GITHUB with none; and secret, bound but in the hidden space,
// which only its owner reads.
func TestAccServiceSearchUsers(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	bound, other := kernel.NewULID(), kernel.NewULID()
	owner := f.writer
	owner.ActorID = kernel.NewULID()
	f.authz[owner.Caller()] = map[kernel.ULID][]accessapi.Action{
		f.hidden: f.authz[f.admin.Caller()][f.team],
	}
	for _, c := range []struct {
		p                 kernel.Principal
		space, name, prov string
		integration       kernel.ULID
	}{
		{f.admin, "team", "Bound", "GITLAB", bound},
		{f.admin, "team", "Plain", "GITLAB", kernel.ULID{}},
		{f.admin, "team", "Elsewhere", "GITLAB", other},
		{f.admin, "team", "Hub", "GITHUB", kernel.ULID{}},
		{owner, "hidden", "Secret", "GITLAB", bound},
	} {
		in := input(c.name)
		in.Space, in.Provider = c.space, new(c.prov)
		if c.integration != (kernel.ULID{}) {
			f.svc.BindIntegrations(integrations{bound: c.integration})
			in.VCSIntegration = new(c.integration.String())
		}
		if _, err := f.svc.Create(ctx, c.p, "", false, in); err != nil {
			t.Fatalf("create %s: %v", c.name, err)
		}
	}
	slugs := func(p kernel.Principal, u stacksapi.Use, in search.Input) []string {
		t.Helper()
		res, err := f.svc.SearchUsers(ctx, p, u, in)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(res.Edges))
		for i, e := range res.Edges {
			if e.Node.Account != f.admin.AccountID || e.Cursor == "" {
				t.Errorf("edge %+v", e)
			}
			out[i] = e.Node.Slug
		}
		return out
	}
	def := stacksapi.Use{Integration: bound, Provider: "GITLAB", Default: true}
	own := stacksapi.Use{Integration: bound, Provider: "GITLAB"}
	for name, c := range map[string]struct {
		p    kernel.Principal
		u    stacksapi.Use
		in   search.Input
		want []string
	}{
		"the default's users": {f.admin, def, search.Input{}, []string{"bound", "plain"}},
		"its own users":       {f.admin, own, search.Input{}, []string{"bound"}},
		"by name":             {f.admin, def, search.Input{FullText: "plai"}, []string{"plain"}},
		"the owner's":         {owner, own, search.Input{}, []string{"secret"}},
		"another provider's default": {
			f.admin,
			stacksapi.Use{Integration: kernel.NewULID(), Provider: "GITHUB", Default: true},
			search.Input{},
			[]string{"hub"},
		},
	} {
		if got := slugs(c.p, c.u, c.in); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
	page, err := f.svc.SearchUsers(ctx, f.admin, def, search.Input{First: new(int32(1))})
	if err != nil || len(page.Edges) != 1 || !page.HasNext || page.End == "" {
		t.Fatalf("first page %+v, %v", page, err)
	}

	got, err := f.svc.SuggestUsers(ctx, f.admin, def, search.SuggestionsInput{
		Fields: []string{"name"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var names search.Suggestion
	for _, sg := range got.Fields {
		if sg.Field.Name == "name" {
			names = sg
		}
	}
	slices.Sort(names.Values)
	if got.Count != 2 || !slices.Equal(names.Values, []string{"Bound", "Plain"}) {
		t.Errorf("suggestions %+v", got)
	}

	f.spaces.treeErr = errors.New("access down")
	if _, err := f.svc.SearchUsers(ctx, f.admin, def, search.Input{}); !errors.Is(
		err, f.spaces.treeErr) {
		t.Errorf("SearchUsers without the tree = %v", err)
	}
	if _, err := f.svc.SuggestUsers(ctx, f.admin, def, search.SuggestionsInput{}); !errors.Is(
		err, f.spaces.treeErr) {
		t.Errorf("SuggestUsers without the tree = %v", err)
	}
	f.spaces.treeErr = nil
	if _, err := f.svc.SearchUsers(ctx, f.admin, def, search.Input{
		Predicates: []search.Predicate{{Field: "owner"}},
	}); !errors.As(err, new(*search.Invalid)) {
		t.Errorf("a search under an unknown field = %v", err)
	}
}

// TestUnitSearchScopeUse holds a scope's integration filter to the
// arguments after the access view's stacks, so the two number apart.
func TestUnitSearchScopeUse(t *testing.T) {
	acct, id := kernel.NewAccountID(), kernel.NewULID()
	s := pg.SearchScope(acct, core.Scope{
		Stacks: []kernel.ULID{kernel.NewULID()},
		Use:    &stacksapi.Use{Integration: id, Provider: "GITLAB", Default: true},
	})
	want := "s.account_id = $1 AND s.id = ANY($4::uuid[]) AND (s.vcs_integration_id = $5 OR " +
		"($6 AND s.vcs_integration_id IS NULL AND s.provider = $7))"
	if s.Where != want || len(s.Args) != 7 || s.Args[4] != id || s.Args[5] != true ||
		s.Args[6] != "GITLAB" {
		t.Errorf("scope = %q, %v", s.Where, s.Args)
	}
}
