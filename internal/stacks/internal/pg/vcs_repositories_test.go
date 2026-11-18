package pg_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func TestAccVCSRepositoryProjection(t *testing.T) {
	f := newFixture(t)
	bound, other := kernel.NewULID(), kernel.NewULID()
	for i, row := range []struct {
		provider, repository string
		integration          kernel.ULID
		detached, foreign    bool
	}{
		{"GITHUB_ENTERPRISE", "infra", bound, false, false},
		{"GITHUB_ENTERPRISE", "infra", bound, false, false},
		{"GITHUB", "private", bound, false, false},
		{"GITHUB", "implicit", kernel.ULID{}, false, false},
		{"GITHUB_ENTERPRISE", "canonical", kernel.ULID{}, false, false},
		{"GITHUB_ENTERPRISE", "another-binding", other, false, false},
		{"GITLAB", "another-provider", kernel.ULID{}, false, false},
		{"GITHUB", "foreign-account", bound, false, true},
		{"GITHUB", "detached", bound, true, false},
	} {
		account := f.acct
		if row.foreign {
			account = kernel.NewAccountID()
		}
		// All rows live in a space the ordinary caller cannot read. The
		// integration-admin projection still needs their repository set.
		_, err := f.db.Exec(t.Context(), `INSERT INTO stacks
			(account_id,id,slug,space_id,name,repository,branch,provider,namespace,vcs_integration_id,vcs_detached,vendor)
			VALUES($1,$2,$3,$4,$3,$5,'main',$6,'acme',$7,$8,'OPEN_TOFU')`,
			account, kernel.NewULID(), fmt.Sprintf("projection-%d", i), f.hidden, row.repository, row.provider, row.integration, row.detached)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		defaultIntegration bool
		want               []string
	}{
		{false, []string{"infra", "private"}},
		{true, []string{"canonical", "implicit", "infra", "private"}},
	} {
		rows, err := f.svc.VCSRepositories(
			t.Context(),
			f.acct,
			stacksapi.Use{Integration: bound, Provider: "GITHUB_ENTERPRISE", Aliases: []string{"GITHUB"}, Default: tc.defaultIntegration},
		)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(rows))
		for i, row := range rows {
			if row.Namespace != "acme" {
				t.Fatal(row)
			}
			got[i] = row.Name
		}
		if !slices.Equal(got, tc.want) {
			t.Fatalf("default=%t: %v, want %v", tc.defaultIntegration, got, tc.want)
		}
	}
	f.db.Close()
	_, cause := f.db.Exec(t.Context(), "SELECT 1")
	if cause == nil {
		t.Fatal("closed pool unexpectedly accepted a query")
	}
	if _, err := f.svc.VCSRepositories(t.Context(), f.acct, stacksapi.Use{Integration: bound}); !errors.Is(err, cause) {
		t.Fatalf("repository projection failure lost: %v", err)
	}
}
