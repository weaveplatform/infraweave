package sourcetest

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// History seeds actual divergent Git commits independently of a provider's
// comparison implementation.
type History interface {
	Commit(string, string, map[string]string) string
	Branch(string, string)
	SetDefaultBranch(string)
}

// Push checks two-tree pushes, including forced pushes and a branch whose
// default branch has advanced since its common ancestor.
func Push(t *testing.T, source port.Source, repo port.Repository, history History) {
	t.Helper()
	ancestor := history.Commit("trunk", "common ancestor", map[string]string{
		"root/main.tf": "base", "outside.txt": "base",
	})
	history.SetDefaultBranch("trunk")
	before := history.Commit("trunk", "default branch advances", map[string]string{"root/main.tf": "old"})
	history.Branch("topic", ancestor)
	head := history.Commit("topic", "topic changes only outside root", map[string]string{"outside.txt": "changed"})
	history.Branch("same-tree", ancestor)
	same := history.Commit("same-tree", "a different commit with the old tree", map[string]string{"root/main.tf": "old"})
	for _, check := range []struct {
		name, before, head string
		want               []string
	}{
		{"force push compares full trees", before, head, []string{"outside.txt", "root/main.tf"}},
		{"new branch uses default merge base", strings.Repeat("0", 40), head, []string{"outside.txt"}},
		{"different commits with identical trees", before, same, nil},
		{"deleted branch needs no source", head, strings.Repeat("0", 40), nil},
	} {
		t.Run(check.name, func(t *testing.T) {
			got, err := source.AffectedFiles(t.Context(), repo, check.before, check.head, port.PushDiff)
			if err != nil || !slices.Equal(got, check.want) {
				t.Errorf("push files = %q, %v; want %q", got, err, check.want)
			}
		})
	}
	if _, err := source.AffectedFiles(t.Context(), repo, before, head, port.DiffMode(99)); !errors.Is(err, port.ErrDiffMode) {
		t.Errorf("unknown comparison = %v, want ErrDiffMode", err)
	}
	if got, err := source.AffectedFiles(t.Context(), port.Repository{}, before, strings.Repeat("0", 40), port.PushDiff); err != nil || len(got) != 0 {
		t.Errorf("deleted branch opened a source = %q, %v", got, err)
	}
}
