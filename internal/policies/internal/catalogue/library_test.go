package catalogue

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/tester"
)

//go:embed upstream
var testCorpus embed.FS

func TestUnitPolicyLibrary(t *testing.T) {
	var manifest struct {
		Revision string            `json:"revision"`
		Files    map[string]string `json:"files"`
	}
	raw, err := testCorpus.ReadFile("upstream/MANIFEST.json")
	if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Revision != "8e8894fbd4cefffa3c261746bcdd70ef9fbb53a8" {
		t.Fatal("missing pinned source inventory", err)
	}
	for name, digest := range manifest.Files {
		body, err := testCorpus.ReadFile("upstream/" + name)
		sum := sha256.Sum256(body)
		if err != nil || hex.EncodeToString(sum[:]) != digest {
			t.Fatalf("pinned source changed: %s: %v", name, err)
		}
	}
	count, cases := 0, 0
	err = fs.WalkDir(testCorpus, "upstream/examples", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(name, "_test.rego") {
			return err
		}
		count++
		t.Run(strings.TrimPrefix(name, "upstream/examples/"), func(t *testing.T) {
			modules := map[string]*ast.Module{}
			for _, file := range []string{strings.TrimSuffix(name, "_test.rego") + ".rego", name} {
				raw, err := testCorpus.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				body, changes, err := Rewrite(file, string(raw))
				if err != nil || changes == 0 {
					t.Fatalf("rewrite %s: %d %v", file, changes, err)
				}
				t.Logf("%s: %d namespace rewrites", file, changes)
				module, err := ast.ParseModuleWithOpts(path.Base(file), body, ast.ParserOptions{RegoVersion: ast.RegoV0})
				if err != nil {
					t.Fatal(err)
				}
				if file == name {
					normalizeUnsafeWebhookAssertions(t, file, module)
				}
				modules[file] = module
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			results, err := tester.NewRunner().SetDefaultRegoVersion(ast.RegoV0).SetTimeout(2*time.Second).SetModules(modules).RunTests(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			local := 0
			for result := range results {
				local++
				cases++
				if !result.Pass() {
					t.Errorf("upstream %s: %s", name, result)
				}
			}
			if local == 0 {
				t.Fatal("upstream file discovered no tests")
			}
		})
		return nil
	})
	if err != nil || count != 62 {
		t.Fatalf("complete upstream inventory: files=%d: %v", count, err)
	}
	t.Logf("Executed all %d upstream test files: %d Rego tests", count, cases)
}

// Three pinned upstream assertions use a negated wildcard, which OPA rejects as
// unsafe even before namespace adaptation. Count-zero states the same intended
// empty-set assertion without introducing an unbound variable. Only the two
// inventoried test files are adapted; original upstream bytes remain unchanged.
func normalizeUnsafeWebhookAssertions(t *testing.T, name string, module *ast.Module) {
	t.Helper()
	want := 0
	switch path.Base(name) {
	case "discord-drift-detection-with-changes_test.rego":
		want = 1
	case "ms-teams-drift-detection-with-changes_test.rego":
		want = 2
	default:
		return
	}
	changed := 0
	ast.WalkExprs(module, func(expr *ast.Expr) bool {
		term, ok := expr.Terms.(*ast.Term)
		if !ok || !expr.Negated {
			return false
		}
		ref, ok := term.Value.(ast.Ref)
		if !ok || len(ref) != 2 || ref[0].Value != ast.Var("webhook") {
			return false
		}
		variable, ok := ref[1].Value.(ast.Var)
		if !ok || !variable.IsWildcard() {
			return false
		}
		replacement := ast.MustParseExpr("count(webhook) == 0")
		expr.Terms, expr.Negated = replacement.Terms, false
		changed++
		return false
	})
	if changed != want {
		t.Fatalf("pinned unsafe assertion inventory changed in %s: %d != %d", name, changed, want)
	}
}

func TestUnitRewritePreservesArbitraryStringsAndImportAliases(t *testing.T) {
	body := `package spacelift_test
import rego.v1
import data.spacelift
literal := "package spacelift input.spacelift data.spacelift spacelift"
test_namespace if { spacelift.allow with input.spacelift as {"x":"data.spacelift"} }
`
	out, changes, err := Rewrite("strings.rego", body)
	if err != nil || changes != 3 || !strings.Contains(out, `"package spacelift input.spacelift data.spacelift spacelift"`) || !strings.Contains(out, `"data.spacelift"`) ||
		!strings.Contains(out, "import data.infraweave as spacelift") ||
		!strings.Contains(out, "input.infraweave") {
		t.Fatalf("namespace/string rewrite changed semantics: changes=%d %v\n%s", changes, err, out)
	}
	if _, _, err := Rewrite("bad.rego", "invalid"); err == nil {
		t.Fatal("invalid source accepted")
	}
}
