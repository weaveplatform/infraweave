package bitbucketdc_test

import "testing"

func TestUnitRepositoryIdentity(t *testing.T) {
	l := newLab(t)
	first, err := l.bb.ResolveRepositoryRef(t.Context(), l.repo.Repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	second, err := l.bb.ResolveRepositoryRef(t.Context(), l.repo.Repo, l.repo.Head)
	if err != nil {
		t.Fatal(err)
	}
	if first.Repository.ID == "" || first.Repository.Namespace != l.repo.Repo.Namespace || first.Repository.Name != l.repo.Repo.Name || first.Commit.Hash != l.repo.Head || second.Repository != first.Repository {
		t.Fatalf("stable identity: first=%+v second=%+v", first, second)
	}
}
