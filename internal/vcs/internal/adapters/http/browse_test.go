package http_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// browser lists three repositories and three branches, unsorted, failing
// with fail. Its repository "missing" is not found, and "revoked" refuses
// the credential.
type browser struct{ fail error }

func (b browser) Repositories(context.Context, port.Integration) ([]port.Listed, error) {
	return []port.Listed{
		{Namespace: "ops", Name: "Infra-live"},
		{Namespace: "acme", Name: "web"},
		{Namespace: "acme", Name: "infra"},
	}, b.fail
}

func (b browser) Branches(_ context.Context, repo port.Repository) ([]string, error) {
	switch repo.Name {
	case "missing":
		return nil, port.ErrNotFound
	case "revoked":
		return nil, port.ErrCredential
	}
	return []string{"main", "dev", "feature/a"}, b.fail
}

// users answers one stack in the top space as an integration's user,
// keeping the Use it was asked for, and fails with fail.
type users struct {
	stacksapi.Stacks

	top  kernel.ULID
	got  stacksapi.Use
	fail error
}

func (u *users) SearchUsers(
	_ context.Context,
	_ kernel.Principal,
	use stacksapi.Use,
	_ search.Input,
) (search.Result[stacksapi.Stack], error) {
	u.got = use
	return search.Result[stacksapi.Stack]{Edges: []search.Edge[stacksapi.Stack]{{
		Cursor: "c", Node: stacksapi.Stack{Slug: "infra", Name: "Infra", Space: u.top},
	}}}, u.fail
}

func (u *users) SuggestUsers(
	_ context.Context,
	_ kernel.Principal,
	use stacksapi.Use,
	_ search.SuggestionsInput,
) (search.Suggestions, error) {
	u.got = use
	return search.Suggestions{}, u.fail
}

func ptr[T any](v T) *T { return &v }

func (f fixture) repositories(
	ctx context.Context,
	params api.RepositoriesParams,
) (api.RepositoryPage, error) {
	res, err := f.srv.Repositories(ctx, api.RepositoriesRequestObject{Params: params})
	if err != nil {
		return api.RepositoryPage{}, err
	}
	return res.(api.Repositories200JSONResponse).Body, nil
}

func (f fixture) branches(
	ctx context.Context,
	params api.BranchesParams,
) (api.BranchesPage, error) {
	res, err := f.srv.Branches(ctx, api.BranchesRequestObject{Params: params})
	if err != nil {
		return api.BranchesPage{}, err
	}
	return res.(api.Branches200JSONResponse).Body, nil
}

// refused reports whether err is a validation failure at pointer.
func refused(err error, pointer string) bool {
	inv, ok := errors.AsType[*kernel.Invalid](err)
	return ok && len(inv.Errors) == 1 && inv.Errors[0].Pointer == pointer
}

// TestUnitBrowse checks that repositories page by path through a
// provider's default, an integration and a built-in one, with a filter
// ignoring case, and that branches list sorted by URL and through an
// integration, including one in a space the caller's inherits from.
func TestUnitBrowse(t *testing.T) {
	f := newFixture(t)
	gitlab := api.VCSProviderGITLAB
	first, err := f.repositories(f.ctx, api.RepositoriesParams{
		Provider: &gitlab, Filter: ptr("INFRA"), Limit: ptr(api.Limit(1)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 ||
		first.Items[0] != (api.Repository{Namespace: "acme", Id: "infra"}) ||
		first.NextCursor == nil {
		t.Fatalf("first page = %+v", first)
	}
	second, err := f.repositories(f.ctx, api.RepositoriesParams{
		Provider: &gitlab, Filter: ptr("INFRA"), Limit: ptr(api.Limit(1)), Cursor: first.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].Id != "Infra-live" || second.NextCursor != nil {
		t.Errorf("second page = %+v", second)
	}
	byID, err := f.repositories(f.ctx, api.RepositoriesParams{VcsIntegrationID: &f.gitlab})
	if err != nil || len(byID.Items) != 3 {
		t.Errorf("by integration = %+v, %v", byID, err)
	}
	github := api.VCSProviderGITHUB
	if got, err := f.repositories(f.ctx, api.RepositoriesParams{Provider: &github}); err != nil ||
		len(got.Items) != 3 {
		t.Errorf("built in = %+v, %v", got, err)
	}

	git, err := f.branches(f.ctx, api.BranchesParams{
		Provider: api.VCSProviderGIT, RepositoryURL: ptr("https://git.example/infra.git"),
	})
	if err != nil || !slices.Equal(git.Items, []string{"dev", "feature/a", "main"}) {
		t.Errorf("raw Git branches = %+v, %v", git, err)
	}

	parent := kernel.NewULID()
	f.spaces.tree = []accessapi.SpaceNode{
		{ID: parent},
		{ID: f.acct.ULID, Parent: parent, Inherit: true},
	}
	inherited := kernel.NewULID()
	f.store.rows[inherited] = integration.Integration{
		Account: f.acct, ID: inherited, Provider: "GITLAB", Name: "Parent", Space: parent,
	}
	got, err := f.branches(f.ctx, api.BranchesParams{
		Provider: gitlab, Namespace: "acme", Repository: "infra",
		VcsIntegrationID: ptr(inherited.String()), Limit: ptr(api.Limit(2)),
	})
	if err != nil || !slices.Equal(got.Items, []string{"dev", "feature/a"}) ||
		got.NextCursor == nil {
		t.Errorf("inherited integration's branches = %+v, %v", got, err)
	}
}

// TestUnitBrowseRefusals checks each refusal: no principal, no provider
// to default, providers that list nothing, a raw Git repository with no
// URL, a missing or hidden integration, a missing repository, a refused
// credential, a bad cursor and failing reads.
func TestUnitBrowseRefusals(t *testing.T) {
	f := newFixture(t)
	gitlab, git := api.VCSProviderGITLAB, api.VCSProviderGIT
	if _, err := f.repositories(
		t.Context(),
		api.RepositoriesParams{},
	); !is(
		err,
		problem.Unauthenticated,
	) {
		t.Errorf("repositories with no principal = %v", err)
	}
	if _, err := f.branches(t.Context(), api.BranchesParams{}); !is(err, problem.Unauthenticated) {
		t.Errorf("branches with no principal = %v", err)
	}

	for name, params := range map[string]api.RepositoriesParams{
		"no provider":           {},
		"raw Git":               {Provider: &git},
		"a provider not listed": {VcsIntegrationID: &f.other},
	} {
		if _, err := f.repositories(f.ctx, params); !refused(err, "/provider") {
			t.Errorf("repositories of %s = %v", name, err)
		}
	}
	if _, err := f.repositories(f.ctx, api.RepositoriesParams{
		Provider: &gitlab, Cursor: ptr("x"),
	}); !is(err, problem.InvalidCursor) {
		t.Errorf("a bad cursor = %v", err)
	}
	cloud := api.VCSProviderBITBUCKETCLOUD
	if _, err := f.repositories(f.ctx, api.RepositoriesParams{
		Provider: &cloud,
	}); !errors.Is(err, errBoom) {
		t.Errorf("a failing provider's repositories = %v", err)
	}
	if _, err := f.branches(f.ctx, api.BranchesParams{
		Provider: gitlab, VcsIntegrationID: ptr(f.gitlab), Cursor: ptr("x"),
	}); !is(err, problem.InvalidCursor) {
		t.Errorf("a bad branches cursor = %v", err)
	}

	if _, err := f.branches(
		f.ctx,
		api.BranchesParams{Provider: git},
	); !refused(
		err,
		"/repositoryURL",
	) {
		t.Errorf("raw Git with no URL = %v", err)
	}
	if _, err := f.branches(f.ctx, api.BranchesParams{
		Provider: "OTHER", VcsIntegrationID: &f.other,
	}); !refused(err, "/provider") {
		t.Errorf("branches of a provider not listed = %v", err)
	}
	for repo, want := range map[string]problem.Type{
		"missing": problem.NotFound, "revoked": problem.UpstreamError,
	} {
		if _, err := f.branches(f.ctx, api.BranchesParams{
			Provider: gitlab, Namespace: "acme", Repository: repo,
		}); !is(err, want) {
			t.Errorf("branches of %s = %v", repo, err)
		}
	}

	hidden := kernel.NewULID()
	f.store.rows[hidden] = integration.Integration{
		Account: f.acct, ID: hidden, Provider: "GITLAB", Name: "Hidden", Space: kernel.NewULID(),
	}
	for name, id := range map[string]string{"missing": kernel.NewULID().String(), "hidden": hidden.String()} {
		if _, err := f.repositories(f.ctx, api.RepositoriesParams{
			VcsIntegrationID: &id,
		}); !is(err, problem.NotFound) {
			t.Errorf("repositories of a %s integration = %v", name, err)
		}
	}
	f.spaces.treeFail = errBoom
	if _, err := f.branches(f.ctx, api.BranchesParams{
		Provider: gitlab, VcsIntegrationID: ptr(hidden.String()),
	}); !errors.Is(err, errBoom) {
		t.Errorf("branches with a failing tree = %v", err)
	}
	f.store.fail = errBoom
	if _, err := f.branches(f.ctx, api.BranchesParams{Provider: gitlab}); !errors.Is(err, errBoom) {
		t.Errorf("branches on a failing store = %v", err)
	}
}

// TestUnitSearchUsers checks that an integration's users are searched
// as its use, answered by slug in their space, and that a missing
// integration, a failing search and failing slugs are refused.
func TestUnitSearchUsers(t *testing.T) {
	f := newFixture(t)
	f.users.top = f.acct.ULID
	search := func(ctx context.Context, id string) (api.SearchVCSIntegrationUsersOutput, error) {
		res, err := f.srv.VcsIntegrationSearchUsers(ctx, api.VcsIntegrationSearchUsersRequestObject{
			VcsIntegrationId: id,
		})
		if err != nil {
			return api.SearchVCSIntegrationUsersOutput{}, err
		}
		return res.(api.VcsIntegrationSearchUsers200JSONResponse).Body, nil
	}
	suggest := func(ctx context.Context, id string) error {
		_, err := f.srv.VcsIntegrationSearchUsersSuggestions(ctx,
			api.VcsIntegrationSearchUsersSuggestionsRequestObject{VcsIntegrationId: id})
		return err
	}

	got, err := search(f.ctx, f.gitlab)
	if err != nil {
		t.Fatal(err)
	}
	want := api.VCSIntegrationUser{
		StackId:      "infra",
		StackName:    "Infra",
		SpaceDetails: api.SpaceRef{Id: "acme"},
	}
	if len(got.Edges) != 1 || got.Edges[0].Node != want {
		t.Errorf("users = %+v", got)
	}
	if f.users.got.Provider != "GITLAB" || f.users.got.Integration.String() != f.gitlab {
		t.Errorf("searched as %+v", f.users.got)
	}
	if err := suggest(f.ctx, f.cloud); err != nil || f.users.got.Provider != "BITBUCKET_CLOUD" {
		t.Errorf("suggest = %v as %+v", err, f.users.got)
	}

	if _, err := search(t.Context(), f.gitlab); !is(err, problem.Unauthenticated) {
		t.Errorf("search with no principal = %v", err)
	}
	if err := suggest(t.Context(), f.gitlab); !is(err, problem.Unauthenticated) {
		t.Errorf("suggest with no principal = %v", err)
	}
	if _, err := search(f.ctx, "nope"); !is(err, problem.NotFound) {
		t.Errorf("search of a missing integration = %v", err)
	}
	if err := suggest(f.ctx, "nope"); !is(err, problem.NotFound) {
		t.Errorf("suggest of a missing integration = %v", err)
	}
	f.spaces.fail = errBoom
	if _, err := search(f.ctx, f.gitlab); !errors.Is(err, errBoom) {
		t.Errorf("search with failing slugs = %v", err)
	}
	f.users.fail = errBoom
	if _, err := search(f.ctx, f.gitlab); !errors.Is(err, errBoom) {
		t.Errorf("a failing search = %v", err)
	}
	if err := suggest(f.ctx, f.gitlab); !errors.Is(err, errBoom) {
		t.Errorf("a failing suggest = %v", err)
	}
}
