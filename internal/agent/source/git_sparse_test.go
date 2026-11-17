package source

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestUnitFetchSparseGit checks the actual worktree rather than the
// checkout options: selected directories exist, and other files do not.
func TestUnitFetchSparseGit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		paths  *[]string
		sparse bool
	}{
		{name: "unspecified"},
		{name: "empty", paths: new([]string{})},
		{name: "one directory", paths: new([]string{"infra"}), sparse: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepo(t)
			server := newGitServer(t, repo.s, false)
			spec := gitSpec(server.srv.URL+"/repo.git", repo.first, new("infra"))
			spec.Source.SparsePaths = tc.paths
			dir := filepath.Join(t.TempDir(), "workspace")
			if err := fetcher().Fetch(t.Context(), dir, spec, nil); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(filepath.Join(dir, Checkout, "infra", "net.tf")); err != nil || string(got) != "net\n" {
				t.Fatalf("selected directory: %q, %v", got, err)
			}
			got, err := os.ReadFile(filepath.Join(dir, Checkout, "main.tf"))
			if tc.sparse {
				if !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("unselected file: %q, %v", got, err)
				}
			} else if err != nil || string(got) != "one\n" {
				t.Fatalf("full checkout: %q, %v", got, err)
			}
		})
	}
}
