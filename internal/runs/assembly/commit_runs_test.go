package assembly

import (
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitCommitRuns(t *testing.T) {
	t.Parallel()
	domain := New(Deps{})
	if domain.CommitRuns() != domain {
		t.Fatal("commit facts do not come from the runs domain")
	}
	got, err := domain.CommitRuns().OnCommits(t.Context(), kernel.AccountID{}, kernel.ULID{}, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty commit facts: %+v %v", got, err)
	}
}
