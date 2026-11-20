package github_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/source"
)

// Native request responses follow the provider's released GET request contract;
// webhook actors and delivery state do not supply this independent snapshot.
func TestUnitNativeRequestSnapshotAndAvailability(t *testing.T) {
	l := newLab(t)
	l.fake.Repo("contributor", "fork").Commit("feature", "fork", map[string]string{"fork.tf": "fork"})
	target, err := l.fake.RepositoryPayload("acme", "infra")
	if err != nil {
		t.Fatal(err)
	}
	source, err := l.fake.RepositoryPayload("contributor", "fork")
	if err != nil {
		t.Fatal(err)
	}

	body, err := githubfake.Payload("pull_request", "opened")
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	r := envelope["pull_request"].(map[string]any)
	r["head"] = map[string]any{"ref": "feature", "sha": l.repo.Head, "repo": source}
	r["base"] = map[string]any{"ref": "main", "sha": l.repo.Base, "repo": target}

	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.fake.SetPullRequest("acme", "infra", 12, encoded); err != nil {
		t.Fatal(err)
	}
	snapshot, err := l.gh.ReadRequest(t.Context(), l.repo.Repo, 12)
	if err != nil || snapshot.Head != l.repo.Head || snapshot.Base != l.repo.Base || snapshot.Request.Number != 12 || snapshot.Request.AuthorLogin != "author" ||
		snapshot.Request.HeadBranch != "feature" || snapshot.Request.BaseBranch != "main" || !snapshot.Request.Fork || snapshot.Request.Title != "Infrastructure change" ||
		snapshot.HeadRepository.Name != "fork" || snapshot.BaseRepository.Name != "infra" || snapshot.HeadRepository.ID == snapshot.BaseRepository.ID {
		t.Fatal("native peers or immutable request metadata changed", snapshot, err)
	}
	for _, number := range []int{0, 13} {
		if _, err := l.gh.ReadRequest(t.Context(), l.repo.Repo, number); !errors.Is(err, port.ErrNotFound) {
			t.Fatal("missing request accepted", number, err)
		}
	}
	r["number"] = 99
	encoded, err = json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.fake.SetPullRequest("acme", "infra", 12, encoded); err != nil {
		t.Fatal(err)
	}
	if _, err := l.gh.ReadRequest(t.Context(), l.repo.Repo, 12); err == nil {
		t.Fatal("different native request accepted")
	}
	l.fake.Fail(githubfake.ServerError, time.Minute)
	if _, err := l.gh.ReadRequest(t.Context(), l.repo.Repo, 12); err == nil {
		t.Fatal("backend failure hidden")
	}
}

type requestIntegration struct {
	account kernel.AccountID
	in      port.Integration
}

func (b requestIntegration) Use(_ context.Context, account kernel.AccountID, provider string, id *kernel.ULID) (port.Integration, bool, error) {
	if account != b.account || id == nil || *id != b.in.ID || provider != b.in.Provider {
		return port.Integration{}, false, nil
	}
	return b.in, true, nil
}

func TestUnitPublicForkUsesOriginalTargetInstallation(t *testing.T) {
	l := newLab(t)
	l.in.ID = kernel.NewULID()
	// Shared fork history is real Git data. The target's App has no
	// installation on contributor/fork, while its public source is readable.
	fork := seed(l.fake.Repo("contributor", "fork"))
	if fork.Head != l.repo.Head {
		t.Fatal("fixture fork histories differ")
	}
	l.fake.SetPublicRepository("contributor", "fork", true)
	target, err := l.fake.RepositoryPayload("acme", "infra")
	if err != nil {
		t.Fatal(err)
	}
	peer, err := l.fake.RepositoryPayload("contributor", "fork")
	if err != nil {
		t.Fatal(err)
	}
	body, err := githubfake.Payload("pull_request", "opened")
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	r := envelope["pull_request"].(map[string]any)
	r["head"] = map[string]any{"ref": "feature", "sha": fork.Head, "repo": peer}
	r["base"] = map[string]any{"ref": "main", "sha": l.repo.Base, "repo": target}
	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.fake.SetPullRequest("acme", "infra", 12, encoded); err != nil {
		t.Fatal(err)
	}
	account := kernel.NewAccountID()
	service := source.New(source.Deps{Integrations: requestIntegration{account: account, in: l.in}, Providers: map[string]port.Source{l.in.Provider: l.gh}})
	event := vcsapi.Event{
		Type:        "pull_request",
		Head:        fork.Head,
		Repository:  vcsapi.Repository{Namespace: "acme", Name: "infra"},
		PullRequest: &vcsapi.PullRequest{Number: 12},
	}
	facts, err := service.PrepareRequest(t.Context(), account, l.in.ID, l.in.Provider, event)
	if err != nil || facts.Head.Hash != fork.Head || !facts.Snapshot.Request.Fork || facts.Snapshot.HeadRepository.Namespace != "contributor" || len(facts.Files) != 3 {
		t.Fatal("public fork required a separate installation or lost its own source identity", facts, err)
	}
	l.fake.SetPublicRepository("contributor", "fork", false)
	if _, err := service.PrepareRequest(t.Context(), account, l.in.ID, l.in.Provider, event); !errors.Is(err, port.ErrNotFound) {
		t.Fatal("target token reached a private uninstalled fork", err)
	}
}

func TestUnitRequestRevisionRetainsFilesBeyondNativeComparisonLimit(t *testing.T) {
	l := newLab(t)
	files := map[string]string{}
	for index := range 350 {
		files[fmt.Sprintf("extra/%03d.tf", index)] = "resource terraform_data x {}"
	}
	head := l.fake.Repo("acme", "infra").Commit("main", "large native request", files)
	resolved, err := l.gh.ResolveRepositoryRef(t.Context(), l.repo.Repo, head)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := l.gh.AffectedFiles(t.Context(), l.repo.Repo, l.repo.Head, head)
	if err != nil || len(legacy) != 300 {
		t.Fatal("fixture did not reproduce native comparison truncation", len(legacy), err)
	}
	l.fake.TruncateRecursiveTrees(2)
	revision, err := l.gh.ResolveRequest(
		t.Context(),
		l.repo.Repo,
		port.RequestSnapshot{Base: l.repo.Head, Head: head, BaseRepository: resolved.Repository, HeadRepository: resolved.Repository},
	)
	if err != nil || revision.Base.Hash != l.repo.Head || revision.Head.Hash != head || len(revision.Files) != 350 || revision.Files[0] != "extra/000.tf" ||
		revision.Files[349] != "extra/349.tf" {
		t.Fatal("native request dropped paths beyond the comparison/recursive-tree response limit", len(revision.Files), err)
	}
	bad := resolved.Repository
	bad.ID = "999999999"
	if _, err := l.gh.ResolveRequest(
		t.Context(),
		l.repo.Repo,
		port.RequestSnapshot{Base: l.repo.Head, Head: head, BaseRepository: resolved.Repository, HeadRepository: bad},
	); err == nil {
		t.Fatal("changed native source repository identity was accepted")
	}
}
