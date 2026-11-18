package pg_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func TestAccVCSRepositoryStacks(t *testing.T) {
	f := newFixture(t)
	bound, other := kernel.NewULID(), kernel.NewULID()
	for index, row := range []struct {
		slug, provider, namespace, repository, branch string
		integration                                   kernel.ULID
		detached, foreign                             bool
	}{
		{"main", "GITHUB_ENTERPRISE", "acme", "infra", "main", bound, false, false},
		{"feature", "GITHUB_ENTERPRISE", "acme", "infra", "feature", bound, false, false},
		{"alias-named", "GITHUB", "acme", "infra", "main", bound, false, false},
		{"alias-default", "GITHUB", "acme", "infra", "main", kernel.ULID{}, false, false},
		{"canonical-default", "GITHUB_ENTERPRISE", "acme", "infra", "other", kernel.ULID{}, false, false},
		{"other-binding", "GITHUB_ENTERPRISE", "acme", "infra", "main", other, false, false},
		{"other-provider", "GITLAB", "acme", "infra", "main", kernel.ULID{}, false, false},
		{"other-owner", "GITHUB_ENTERPRISE", "elsewhere", "infra", "main", bound, false, false},
		{"other-repository", "GITHUB_ENTERPRISE", "acme", "web", "main", bound, false, false},
		{"detached", "GITHUB_ENTERPRISE", "acme", "infra", "main", bound, true, false},
		{"foreign", "GITHUB_ENTERPRISE", "acme", "infra", "main", bound, false, true},
	} {
		account := f.acct
		if row.foreign {
			account = kernel.NewAccountID()
		}
		if _, err := f.db.Exec(t.Context(), `INSERT INTO stacks
			(account_id,id,slug,space_id,name,provider,namespace,repository,branch,vcs_integration_id,vcs_detached,vendor)
			VALUES($1,$2,$3,$4,$3,$5,$6,$7,$8,$9,$10,'OPEN_TOFU')`,
			account, kernel.NewULID(), row.slug, f.hidden, row.provider, row.namespace, row.repository, row.branch, row.integration, row.detached); err != nil {
			t.Fatalf("seed %d: %v", index, err)
		}
	}
	for _, tc := range []struct {
		isDefault bool
		want      []string
	}{
		{false, []string{"alias-named", "feature", "main"}},
		{true, []string{"alias-default", "alias-named", "canonical-default", "feature", "main"}},
	} {
		use := stacksapi.Use{Integration: bound, Provider: "GITHUB_ENTERPRISE", Aliases: []string{"GITHUB"}, Default: tc.isDefault}
		rows, err := f.svc.ByVCSRepository(t.Context(), f.acct, use, "acme", "infra")
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(rows))
		for i, row := range rows {
			if row.Account != f.acct {
				t.Fatal("repository lookup crossed account boundary")
			}
			got[i] = row.Slug
		}
		if !slices.Equal(got, tc.want) {
			t.Fatalf("default=%t got=%v want=%v", tc.isDefault, got, tc.want)
		}
	}
	var index string
	if err := f.db.QueryRow(t.Context(), `SELECT indexdef FROM pg_indexes WHERE tablename='stacks' AND indexname='stacks_repository'`).Scan(&index); err != nil ||
		!strings.Contains(index, "(account_id, vcs_integration_id, namespace, repository, branch)") {
		t.Fatal("repository event routing lacks its ordered index", index, err)
	}
	f.db.Close()
	_, cause := f.db.Exec(t.Context(), "SELECT 1")
	if cause == nil {
		t.Fatal("closed pool accepted a query")
	}
	if _, err := f.svc.ByVCSRepository(t.Context(), f.acct, stacksapi.Use{Integration: bound}, "acme", "infra"); !errors.Is(err, cause) {
		t.Fatal(fmt.Errorf("repository stack read lost cause: %w", err))
	}
}
