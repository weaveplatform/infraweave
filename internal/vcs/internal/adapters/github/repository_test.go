package github_test

import (
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
)

func TestUnitRepositoryIdentity(t *testing.T) {
	l := newLab(t)
	first, err := l.gh.ResolveRepositoryRef(t.Context(), l.repo.Repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	second, err := l.gh.ResolveRepositoryRef(t.Context(), l.repo.Repo, l.repo.Head)
	if err != nil {
		t.Fatal(err)
	}
	if first.Repository.ID == "" || first.Repository.Namespace != l.repo.Repo.Namespace || first.Repository.Name != l.repo.Repo.Name || first.Commit.Hash != l.repo.Head ||
		second.Repository != first.Repository {
		t.Fatalf("stable identity: first=%+v second=%+v", first, second)
	}
}

func TestUnitRepositoryIdentitySurvivesRenames(t *testing.T) {
	l := newLab(t)
	original, err := l.gh.ResolveRepositoryRef(t.Context(), l.repo.Repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.fake.RenameRepository("acme", "infra", "renamed"); err != nil {
		t.Fatal(err)
	}
	if err := l.fake.RenameOwner("acme", "platform"); err != nil {
		t.Fatal(err)
	}
	next := l.repo.Repo
	next.ExpectedID = original.Repository.ID
	next.Namespace, next.Name = "platform", "renamed"
	resolved, err := l.gh.ResolveRepositoryRef(t.Context(), next, "main")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Repository.ID != original.Repository.ID || resolved.Repository.OwnerID != original.Repository.OwnerID || resolved.Repository.Namespace != "platform" ||
		resolved.Repository.Name != "renamed" ||
		resolved.Commit.Hash != original.Commit.Hash {
		t.Fatal(original, resolved)
	}
}

func TestUnitBoundRepositoryIdentity(t *testing.T) {
	l := newLab(t)
	sourcetest.BoundRepository(t, l.gh, l.repo)
}

func TestUnitRepositoryPathReuseRefusesOldIdentityBeforeSourceRead(t *testing.T) {
	l := newLab(t)
	original, err := l.gh.ResolveRepositoryRef(t.Context(), l.repo.Repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.fake.RenameRepository("acme", "infra", "retired"); err != nil {
		t.Fatal(err)
	}
	l.fake.Repo("acme", "infra").Commit("main", "different repository", map[string]string{"main.tf": "new"})
	l.fake.Install(l.app.ID, "acme", "Organization", "infra")
	expected := l.repo.Repo
	expected.ExpectedID = original.Repository.ID
	before := l.fake.SourceReads()
	_, err = l.gh.ResolveRepositoryRef(t.Context(), expected, l.repo.Head)
	if !errors.Is(err, port.ErrRepositoryIdentity) || l.fake.SourceReads() != before {
		t.Fatal("reused path reached old repository source", err)
	}
}
