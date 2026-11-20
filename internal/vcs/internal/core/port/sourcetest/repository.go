package sourcetest

import (
	"errors"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// BoundRepository proves that native identity validation precedes source
// objects: an intentionally missing ref must not hide a mismatched identity.
func BoundRepository(t *testing.T, source port.RepositorySource, repo Repository) {
	t.Helper()
	first, err := source.ResolveRepositoryRef(t.Context(), repo.Repo, repo.Head)
	if err != nil {
		t.Fatal(err)
	}
	bound := repo.Repo
	bound.ExpectedID = first.Repository.ID
	current, err := source.ResolveRepositoryRef(t.Context(), bound, repo.Head)
	if err != nil || current.Repository != first.Repository || current.Commit.Hash != repo.Head {
		t.Fatal("validated identity lost its current coordinates", current, err)
	}
	bound.ExpectedID = "another-native-repository"
	if _, err := source.ResolveRepositoryRef(t.Context(), bound, strings.Repeat("f", 40)); !errors.Is(err, port.ErrRepositoryIdentity) {
		t.Fatal("mismatched native identity reached the missing source ref", err)
	}
}
