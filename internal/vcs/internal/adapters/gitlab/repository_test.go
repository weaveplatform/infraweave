package gitlab_test

import (
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
)

func TestUnitRepositoryIdentity(t *testing.T) {
	l := newLab(t)
	first, err := l.gl.ResolveRepositoryRef(t.Context(), l.repo.Repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	second, err := l.gl.ResolveRepositoryRef(t.Context(), l.repo.Repo, l.repo.Head)
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
	original, err := l.gl.ResolveRepositoryRef(t.Context(), l.repo.Repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.fake.RenameProject("acme/platform", "infra", "company/team", "renamed"); err != nil {
		t.Fatal(err)
	}
	next := l.repo.Repo
	next.ExpectedID = original.Repository.ID
	next.Namespace, next.Name = "company/team", "renamed"
	resolved, err := l.gl.ResolveRepositoryRef(t.Context(), next, "main")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Repository.ID != original.Repository.ID || resolved.Repository.Namespace != "company/team" || resolved.Repository.Name != "renamed" ||
		resolved.Commit.Hash != original.Commit.Hash {
		t.Fatal(original, resolved)
	}
}

func TestUnitBoundRepositoryIdentity(t *testing.T) {
	l := newLab(t)
	sourcetest.BoundRepository(t, l.gl, l.repo)
}

func TestUnitRepositoryPathReuseRefusesOldIdentityBeforeSourceRead(t *testing.T) {
	l := newLab(t)
	original, err := l.gl.ResolveRepositoryRef(t.Context(), l.repo.Repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.fake.RenameProject("acme/platform", "infra", "acme/platform", "retired"); err != nil {
		t.Fatal(err)
	}
	l.fake.AddMember("acme/platform", "infra", "infraweave", gitlabfake.Maintainer)
	l.fake.Repo("acme/platform", "infra").Commit("main", "different repository", map[string]string{"main.tf": "new"})
	expected := l.repo.Repo
	expected.ExpectedID = original.Repository.ID
	before := l.fake.SourceReads()
	_, err = l.gl.ResolveRepositoryRef(t.Context(), expected, l.repo.Head)
	if !errors.Is(err, port.ErrRepositoryIdentity) || l.fake.SourceReads() != before {
		t.Fatal("reused path reached old repository source", err)
	}
}
