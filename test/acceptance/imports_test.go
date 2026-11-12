package acceptance

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// generatedClient is the import path prefix of the generated clients,
// which pkg/sdk wraps.
const generatedClient = "github.com/weaveplatform/infraweave/pkg/client"

// clientImports lists the Go files under dir that import a generated
// client, by their path from dir. It walks every package below dir, the
// harness and each area's steps, but no testdata directory below it, where
// the guard's own fixture lives.
func clientImports(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && p != dir && d.Name() == "testdata":
			return filepath.SkipDir
		case !d.IsDir() && strings.HasSuffix(p, ".go"):
			files = append(files, p)
		}
		return nil
	})
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files under %s: %v", dir, err)
	}
	var out []string
	for _, f := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(dir, f)
		for _, imp := range parsed.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if path == generatedClient || strings.HasPrefix(path, generatedClient+"/") {
				out = append(out, filepath.ToSlash(rel)+": "+path)
			}
		}
	}
	return out
}

// TestUnitStepsUseTheSDK holds every step to one rule: a step that drives
// the API does it through pkg/sdk, so each scenario exercises the SDK too,
// never through the generated client beneath it.
//
// ref: decision 0016
func TestUnitStepsUseTheSDK(t *testing.T) {
	if got := clientImports(t, "."); len(got) > 0 {
		t.Errorf("acceptance files import a generated client; use pkg/sdk: %q", got)
	}
	got := clientImports(t, filepath.Join("testdata", "imports"))
	want := []string{"internal/steps/area/steps.go: " + generatedClient + "/api"}
	if !slices.Equal(got, want) {
		t.Errorf("the guard misses the fixture's import: %q", got)
	}
}
