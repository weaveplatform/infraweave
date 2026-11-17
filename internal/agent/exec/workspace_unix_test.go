//go:build unix

package exec_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// TestUnitExecWorkspaceLayout keeps execution in the checkout while
// mounted files can live beside it or replace its committed content.
func TestUnitExecWorkspaceLayout(t *testing.T) {
	for name, project := range map[string]*string{
		"unset": nil, "empty": new(""), "dot": new("."), "subdirectory": new("infra"),
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.h.Spec.Stack.ProjectRoot = project
			working := w.checkout
			if project != nil && *project != "" {
				working = filepath.Join(working, *project)
			}
			if err := os.MkdirAll(working, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(w.checkout, "main.tf"), []byte("committed"), 0o644); err != nil {
				t.Fatal(err)
			}
			outer, replacement := []byte("mounted"), []byte("replacement")
			w.h.Spec.Files = []wire.RunSpecFile{
				{Path: "a.txt", Content: &outer}, {Path: "source/main.tf", Content: &replacement},
			}
			w.h.Spec.Hooks["before_plan"] = []string{`printf 'cwd %s\n' "$PWD" >> "$TRACE"`}
			if err := w.run(); err != nil {
				t.Fatal(err)
			}
			physical, err := filepath.EvalSymlinks(working)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(w.traced(), "cwd "+physical) {
				t.Errorf("hook working directory: %v", w.traced())
			}
			for path, want := range map[string]string{
				filepath.Join(w.workspace, "a.txt"): "mounted", filepath.Join(w.checkout, "main.tf"): "replacement",
			} {
				if got, err := os.ReadFile(path); err != nil || string(got) != want {
					t.Errorf("%s = %q, %v; want %q", path, got, err, want)
				}
			}
		})
	}
}
