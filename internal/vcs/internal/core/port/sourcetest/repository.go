package sourcetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// BoundRepository proves that native identity validation precedes source
// objects: an intentionally missing ref must not hide a mismatched identity.
func BoundRepository(t *testing.T, source port.RepositorySource, repo Repository) {
	t.Helper()
	if err := checkBoundRepository(t.Context(), source, repo); err != nil {
		t.Fatal(err)
	}
}

func checkBoundRepository(ctx context.Context, source port.RepositorySource, repo Repository) error {
	first, err := source.ResolveRepositoryRef(ctx, repo.Repo, repo.Head)
	if err != nil || first.Repository.ID == "" {
		return fmt.Errorf("%w: initial native identity is unavailable: %w", ErrContract, err)
	}
	bound := repo.Repo
	bound.ExpectedID = first.Repository.ID
	current, err := source.ResolveRepositoryRef(ctx, bound, repo.Head)
	if err != nil || current.Repository != first.Repository || current.Commit.Hash != repo.Head {
		return fmt.Errorf("%w: validated identity lost its coordinates or immutable head: %w", ErrContract, err)
	}
	bound.ExpectedID = "another-native-repository"
	if _, err := source.ResolveRepositoryRef(ctx, bound, strings.Repeat("f", 40)); !errors.Is(err, port.ErrRepositoryIdentity) {
		return fmt.Errorf("%w: mismatched identity reached the missing ref: %w", ErrContract, err)
	}
	return nil
}
