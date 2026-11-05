package api_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"strings"
	"testing"
	"testing/fstest"
	"unicode"
	"unicode/utf8"
)

// testKinds are decision 0016's test kinds. make test selects -run
// '^TestUnit' and make accept -run '^TestAcc', so a test named otherwise
// runs in neither.
var testKinds = []string{"TestUnit", "TestAcc"}

// testNameProblems parses every _test.go file of the module rooted at fsys
// and names each top-level test whose name is not a test kind followed by an
// upper-case letter, a digit or nothing. That boundary matters because the
// make targets select by prefix: TestAccount would run as acceptance.
// Hidden directories, testdata and nested modules (a go.mod below the root,
// such as test/depguard) are not part of the module and are skipped.
func testNameProblems(fsys fs.FS) ([]string, error) {
	var out []string
	fset := token.NewFileSet()
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == "." {
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata" {
				return fs.SkipDir
			}
			if _, err := fs.Stat(fsys, path.Join(p, "go.mod")); err == nil {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		src, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, p, src, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !isTest(fn.Name.Name) || hasKind(fn.Name.Name) {
				continue
			}
			out = append(
				out,
				fset.Position(fn.Pos()).
					String()+
					": "+fn.Name.Name+" is neither TestUnit… nor TestAcc…",
			)
		}
		return nil
	})
	return out, err
}

// isTest reports whether go test runs a top-level function of this name:
// Test followed by nothing or by anything but a lower-case letter. TestMain
// is the package's entry point, not a test.
func isTest(name string) bool {
	rest, ok := strings.CutPrefix(name, "Test")
	if !ok || name == "TestMain" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return rest == "" || !unicode.IsLower(r)
}

func hasKind(name string) bool {
	for _, k := range testKinds {
		rest, ok := strings.CutPrefix(name, k)
		if !ok {
			continue
		}
		r, _ := utf8.DecodeRuneInString(rest)
		if rest == "" || unicode.IsUpper(r) || unicode.IsDigit(r) || r == '_' {
			return true
		}
	}
	return false
}

func TestUnitTestNames(t *testing.T) {
	got, err := testNameProblems(os.DirFS(".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got {
		t.Error(p)
	}

	tree := fstest.MapFS{
		"go.mod": {},
		"a/a_test.go": {Data: []byte(`package a

import "testing"

func TestMain(m *testing.M) {}
func TestUnitParse(t *testing.T) {}
func TestAccFeatures(t *testing.T) {}
func TestUnit2(t *testing.T) {}
func TestFoo(t *testing.T) {}
func TestAccount(t *testing.T) {}
func Testify(t *testing.T) {}
func helperTest(t *testing.T) {}

type s struct{}

func (s) TestNotTopLevel(t *testing.T) {}
`)},
		"a/a.go":                {Data: []byte("package a\n\nfunc TestNotATestFile() {}\n")},
		"a/testdata/x_test.go":  {Data: []byte("package x\n\nfunc TestIgnored(t *testing.T) {}\n")},
		".claude/w/x_test.go":   {Data: []byte("package x\n\nfunc TestIgnored(t *testing.T) {}\n")},
		"nested/go.mod":         {},
		"nested/x/x_test.go":    {Data: []byte("package x\n\nfunc TestIgnored(t *testing.T) {}\n")},
		"b/b_test.go":           {Data: []byte("package b\n\nfunc Test(t *testing.T) {}\n")},
		"broken/broken_test.go": {Data: []byte("package broken\n\nfunc {")},
	}
	broken := tree["broken/broken_test.go"]
	delete(tree, "broken/broken_test.go")
	got, err = testNameProblems(tree)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"a/a_test.go:9:1: TestFoo is neither TestUnit… nor TestAcc…",
		"a/a_test.go:10:1: TestAccount is neither TestUnit… nor TestAcc…",
		"b/b_test.go:3:1: Test is neither TestUnit… nor TestAcc…",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	tree["broken/broken_test.go"] = broken
	if _, err := testNameProblems(tree); err == nil {
		t.Error("a test file that does not parse must fail")
	}
}
