package pg_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func TestAccServiceGitHubDefault(t *testing.T) {
	f := newFixture(t)
	in := input("Implicit")
	in.Provider = nil
	if _, err := f.svc.Create(t.Context(), f.admin, "", false, "", in); !slices.Equal(pointers(t, err), []string{"/provider"}) {
		t.Fatalf("a missing default was not refused at provider: %v", err)
	}
	var count int
	if err := f.db.QueryRow(t.Context(), `SELECT count(*) FROM stacks WHERE account_id = $1`, f.acct).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed create stored %d stacks: %v", count, err)
	}
	f.svc.BindIntegrations(integrations{defaultAvailable: true})
	st, err := f.svc.Create(t.Context(), f.admin, "", false, "", in)
	if err != nil || st.Provider != "GITHUB" || st.VCSIntegration != nil {
		t.Fatalf("implicit alias was not retained: %+v %v", st, err)
	}
	f.svc.BindIntegrations(integrations{})
	in.Name = "Changed"
	if _, err := f.svc.Update(t.Context(), f.admin, "implicit", kernel.Expect{}, in); !slices.Equal(pointers(t, err), []string{"/provider"}) {
		t.Fatalf("an update with no default was not refused: %v", err)
	}
	read, err := f.svc.Settings(t.Context(), f.acct, st.ID)
	if err != nil || read.Name != st.Name || read.Version != st.Version || read.VCSIntegration != nil {
		t.Fatalf("failed update changed the stack: %+v %v", read, err)
	}
	f.svc.BindIntegrations(integrations{err: kernel.ErrUnavailable})
	if _, err := f.svc.Update(t.Context(), f.admin, "implicit", kernel.Expect{}, in); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("default lookup failure was lost: %v", err)
	}
}

func TestAccServiceGitHubAliasUsers(t *testing.T) {
	f := newFixture(t)
	bound, other := kernel.NewULID(), kernel.NewULID()
	for _, tc := range []struct {
		name, provider string
		id             *kernel.ULID
	}{
		{"Implicit", "GITHUB", nil},
		{"Canonical", "GITHUB_ENTERPRISE", nil},
		{"Named", "GITHUB", &bound},
		{"Elsewhere", "GITHUB", &other},
		{"GitLab", "GITLAB", nil},
	} {
		in := input(tc.name)
		in.Provider = new(tc.provider)
		binding := integrations{provider: "GITHUB_ENTERPRISE", defaultAvailable: true}
		if tc.id != nil {
			binding.bound = *tc.id
			in.VCSIntegration = new(tc.id.String())
		}
		f.svc.BindIntegrations(binding)
		if _, err := f.svc.Create(t.Context(), f.admin, "", false, "", in); err != nil {
			t.Fatalf("create %s: %v", tc.name, err)
		}
	}
	for _, tc := range []struct {
		isDefault bool
		want      []string
	}{{true, []string{"canonical", "implicit", "named"}}, {false, []string{"named"}}} {
		use := stacksapi.Use{Integration: bound, Provider: "GITHUB_ENTERPRISE", Aliases: []string{"GITHUB"}, Default: tc.isDefault}
		res, err := f.svc.SearchUsers(t.Context(), f.admin, use, search.Input{})
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(res.Edges))
		for i, edge := range res.Edges {
			got[i] = edge.Node.Slug
		}
		slices.Sort(got)
		if !slices.Equal(got, tc.want) {
			t.Fatalf("default=%t users %v, want %v", tc.isDefault, got, tc.want)
		}
	}
}
