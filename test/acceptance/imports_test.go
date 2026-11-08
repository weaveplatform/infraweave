package acceptance

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// generatedClient is the import path prefix of the generated clients,
// which pkg/sdk wraps.
const generatedClient = "github.com/weaveplatform/infraweave/pkg/client"

// clientImports lists the Go files in dir that import a generated client.
func clientImports(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files in %s: %v", dir, err)
	}
	var out []string
	for _, f := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if path == generatedClient || strings.HasPrefix(path, generatedClient+"/") {
				out = append(out, filepath.Base(f)+": "+path)
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
	if !slices.Equal(got, []string{"steps.go: " + generatedClient + "/api"}) {
		t.Errorf("the guard misses the fixture's import: %q", got)
	}
}
