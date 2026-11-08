package docscheck

import (
	"errors"
	"slices"
	"testing"
	"testing/fstest"
)

// TestUnitRefLines checks which citations refLines reports: one outside a
// ref: line in each kind of file it scans, and none on a ref: line, in a Go
// string, or in the trees it skips.
func TestUnitRefLines(t *testing.T) {
	repo := fstest.MapFS{
		"internal/a/a.go": file(
			"package a\n\n// A follows decision 0007.\n//\n// ref: decision 0007\n" +
				"var A = \"decision 0003\"\n\n/* as 0021's 409\n   ref: DV-085 */\nvar B = 1\n\n" +
				"// RFC 7591's names.\nvar C = 2\n",
		),
		"internal/a/a_test.go": file("package a\n\nfunc f() {\n\t// see DES-10.12\n}\n"),
		"internal/a/broken.go": file("package a\n\n// report 14 \"unterminated\n"),
		"m/00001_a.sql":        file("-- Decision 0005's table.\n-- ref: decision 0005\n"),
		"api/src/p.yaml": file("# decisions 0006 and 0007\n# ref: decision 0021\n" +
			"x-infraweave-divergence:\n  - DV-080 # see DV-081\nurl: a#DV-082\n"),
		".github/w.yml": file("  # ref: decision 0016\n  run: x # report 29\n"),
		"Makefile": file("## t: regenerate report 29\n# ref: report 29\n" +
			"# reports 14 and 17\n# stats reports 2 visible\n"),
		"f.feature": file("Feature: f\n  A credential works in one account (decision 0007).\n" +
			"  ref: decision 0007\n  # ref: PD-3\n"),
		"docs/research/decisions/0001-x.md": file("decision 0001\n"),
		"api/reference/spacelift/a.yaml":    file("# report 2\n"),
		"node_modules/x/x.yml":              file("# decision 0001\n"),
		".hidden/x.yml":                     file("# decision 0001\n"),
		"README.txt":                        file("decision 0001\n"),
	}
	c := &checker{repo: repo}
	if err := c.refLines(); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(c.found))
	for _, f := range c.found {
		got = append(got, f.String())
	}
	want := []string{
		`.github/w.yml:2: cites "report 29" outside a ref: line`,
		`Makefile:1: cites "report 29" outside a ref: line`,
		`Makefile:3: cites "reports 14 and 17" outside a ref: line`,
		`api/src/p.yaml:1: cites "decisions 0006" outside a ref: line`,
		`api/src/p.yaml:4: cites "DV-0" outside a ref: line`,
		`f.feature:2: cites "decision 0007" outside a ref: line`,
		`internal/a/a.go:3: cites "decision 0007" outside a ref: line`,
		`internal/a/a.go:8: cites "0021's" outside a ref: line`,
		`internal/a/a_test.go:4: cites "DES-1" outside a ref: line`,
		`internal/a/broken.go:3: cites "report 14" outside a ref: line`,
		`m/00001_a.sql:1: cites "Decision 0005" outside a ref: line`,
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("findings:\n%q\nwant:\n%q", got, want)
	}
}

// TestUnitRefLinesReadErrors checks that a file or directory refLines
// cannot read fails the walk rather than passing unchecked.
func TestUnitRefLinesReadErrors(t *testing.T) {
	for _, name := range []string{"a.go", "a.sql", "."} {
		c := &checker{repo: failFS{m: fstest.MapFS{
			"a.go":  file("package a\n"),
			"a.sql": file("SELECT 1;\n"),
		}, name: name}}
		if err := c.refLines(); !errors.Is(err, errRead) {
			t.Errorf("refLines with %s unreadable = %v, want %v", name, err, errRead)
		}
	}
}
