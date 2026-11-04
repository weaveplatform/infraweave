package docscheck

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/weaveplatform/infraweave/internal/devtools/specref"
)

// TestUnitDocs is the check make docs-check runs over this repository.
func TestUnitDocs(t *testing.T) {
	found, err := Check(os.DirFS("../../.."))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		t.Error(f)
	}
}

func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

const goodReport = "# 01: A report\n\nChecked against: v1\n\nResearch baseline: **2026-10-28**.\n\n## Sources\n\n- <https://example.com>\n"

// referenceAPI is the Deploy reference spec, where the fixtures put spec.
const referenceAPI = "api/reference/spacelift/spacelift.openapi.yaml"

// spec is a reference spec with two tags; the capabilities register must
// hold both.
const spec = "openapi: 3.0.3\ntags:\n  - name: Stacks\n    description: Stack management.\n  - name: Runs\n"

// anySchema accepts any register; the phase and tag checks read records
// whatever their schema says.
const anySchema = `{"x-id-pattern": ".+"}`

// features, capabilities and supersessions are registers that name only
// phases the plan has. Report 26's wording in a capability's spacelift
// member and a phase in a code span are not assignments.
const (
	features = `{"$schema": "schema/features.schema.json", "records": [
  {"id": "stacks", "phase": "P0", "notes": "` + "`P42`" + ` is a name"}]}`
	capabilities = `{"$schema": "schema/capabilities.schema.json", "records": [
  {"id": "tag:Stacks", "product": "deploy", "spacelift": {"notes": "P99 in report 26"}, "infraweave": {"phase": "P0"}},
  {"id": "tag:Runs", "product": "deploy", "infraweave": {"phase": "P1a", "also": "P0"}},
  {"id": "operation:x", "infraweave": {"phase": "P1a"}}]}`
	supersessions = `{"$schema": "schema/supersessions.schema.json", "records": [
  {"id": "SUP-01", "replacementPhase": "P0, P1a"}]}`
)

// option is an architecture option's outline. H3s outside the nested
// sections, and those before the tree, may differ between options.
const option = "# Option\n\n## Summary\n\n### Rules\n\n## Full package tree\n\n### Status\n\n### The tree\n\n" +
	"## Deep dives\n\n### Runs\n\n## Walkthroughs\n\n### A push\n"

// plan is an implementation plan with two phases; the registers name both.
const plan = "# Plan\n\n## Build status\n\n| P0 | done |\n\n## P0: Reference\n\nDecisions: [0001](../research/decisions/0001-one.md)\n\n" +
	"**Depends on:** nothing.\n\n**Tasks**\n\n1. Generate.\n\n**Exit criteria**\n\n- `make gate` passes.\n\n" +
	"## P1a: Skeleton\n\nDecisions: none.\n\n**Depends on:** P0.\n\n**Tasks**\n\n1. Serve.\n\n```\n## P9: Not a phase\n```\n\n**Exit criteria**\n\n- Served.\n"

// glossary is a root glossary whose second term's definition runs over two
// lines.
const glossary = "# Repo\n\nWhat it is.\n\n## Language\n\n**Stack**:\nA unit of infrastructure.\n_Avoid_: project\n\n" +
	"**Run**:\nOne execution\nof a stack.\n_Avoid_: job, build\n\n```\n**Not**:\n```\n"

// pkgDoc is a doc.go for package name that passes the package check.
func pkgDoc(name string) string {
	return "// Package " + name + " is a fixture.\n//\n// # Input and output\n//\n// None.\n//\n" +
		"// # Dependencies\n//\n// None.\npackage " + name + "\n"
}

// tree is a repository that passes every check; each case changes one file.
func tree() fstest.MapFS {
	return fstest.MapFS{
		"README.md":   file("# Repo\n\n## Make targets\n"),
		"GLOSSARY.md": file(glossary),
		"Makefile":    file("# see docs/research/README.md\n"),
		"docs/research/README.md": file("# Research\n\n## How to read\n\n## How to read\n\n" +
			"[01](reference/01-a.md#01-a-report) [index](decisions/README.md) [self](#how-to-read-1)\n" +
			"[repo](../../README.md#make-targets) [web](https://example.com/x) [dir](reference/)\n" +
			"`[not](a-link.md)`\n\n```\n[not](a-link.md)\n# Not a heading\n```\n"),
		"docs/research/reference/01-a.md":     file(goodReport),
		"docs/research/reference/glossary.md": file("# Glossary\n"),
		"docs/research/decisions/README.md": file(
			"# Decisions\n\n| Decision | Design | Status |\n|---|---|---|\n" +
				"| 0001 | [One](0001-one.md) | Accepted |\n",
		),
		"docs/research/decisions/0001-one.md": file("# 0001: One\r\n\r\nStatus: Accepted\r\n"),
		"docs/research/decisions/TEMPLATE.md": file(
			"# NNNN: <design decision>\n\nStatus: Proposed | Accepted | Superseded\n",
		),
		"internal/x/x.go": file(
			"// See docs/research/decisions/0001-one.md.\npackage x\n",
		),
		"internal/x/x_test.go":                      file("// docs/missing.md is a fixture.\n"),
		"internal/x/doc.go":                         file(pkgDoc("x")),
		"internal/x/only/x_test.go":                 file("package only\n"),
		"internal/x/testdata/gen/y.go":              file("package y\n"),
		"internal/x/_skip/y.go":                     file("package y\n"),
		"internal/x/testdata/fixture.md":            file("docs/missing.md\n"),
		"docs/research/architecture-options/1-a.md": file(option),
		"docs/research/architecture-options/2-b.md": file(
			strings.Replace(
				option,
				"### Status",
				"### Domain status",
				1,
			) + "\n```\n## Not a heading\n```\n",
		),
		"docs/implementation/plan.md":                file(plan),
		"docs/data/schema/common.schema.json":        file(commonSchema),
		"docs/data/schema/features.schema.json":      file(anySchema),
		"docs/data/schema/capabilities.schema.json":  file(anySchema),
		"docs/data/schema/supersessions.schema.json": file(anySchema),
		"docs/data/features.json":                    file(features),
		"docs/data/capabilities.json":                file(capabilities),
		"docs/data/supersessions.json":               file(supersessions),
		".git/description.md":                        file("docs/missing.md\n"),
		".github/workflows/w.yml":                    file("# docs/research/README.md\n"),
		"api/spec.yaml": file(
			"description: see docs/research/reference/01-a.md and ../docs/x.md\n",
		),
	}
}

func TestUnitCheckPasses(t *testing.T) {
	found, err := Check(tree())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		t.Error(f)
	}
}

func TestUnitCheckFindings(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(fstest.MapFS)
		want  string
		where string
	}{
		{
			name:  "broken link",
			edit:  func(m fstest.MapFS) { m["docs/a.md"] = file("[x](gone.md)\n") },
			want:  `broken link "gone.md": docs/gone.md does not exist`,
			where: "docs/a.md:1",
		},
		{
			name:  "missing anchor",
			edit:  func(m fstest.MapFS) { m["docs/a.md"] = file("# A\n\n[x](#b)\n") },
			want:  "docs/a.md has no heading #b",
			where: "docs/a.md:3",
		},
		{
			name:  "missing anchor in another file",
			edit:  func(m fstest.MapFS) { m["docs/a.md"] = file("[x](../README.md#nope)\n") },
			want:  "README.md has no heading #nope",
			where: "docs/a.md:1",
		},
		{
			name:  "link out of the repository",
			edit:  func(m fstest.MapFS) { m["docs/a.md"] = file("[x](../../elsewhere.md)\n") },
			want:  "leaves the repository",
			where: "docs/a.md:1",
		},
		{
			name:  "malformed link",
			edit:  func(m fstest.MapFS) { m["docs/a.md"] = file("[x](%zz)\n") },
			want:  `malformed link "%zz"`,
			where: "docs/a.md:1",
		},
		{
			name:  "unreadable anchor target",
			edit:  func(m fstest.MapFS) { m["docs/a.md"] = file("[x](dir.md#a)\n"); m["docs/dir.md/f"] = file("") },
			want:  `link "dir.md#a"`,
			where: "docs/a.md:1",
		},
		{
			name: "report without header",
			edit: func(m fstest.MapFS) {
				m["docs/research/reference/02-b.md"] = file(
					"# 02\n\nResearch baseline: x\n\n## Sources\n",
				)
			},
			want:  `report has no "Checked against:"`,
			where: "docs/research/reference/02-b.md:1",
		},
		{
			name: "report without baseline",
			edit: func(m fstest.MapFS) {
				m["docs/research/reference/02-b.md"] = file(
					"# 02\n\nChecked against: v1\n\n## Sources\n",
				)
			},
			want:  `report has no "Research baseline:"`,
			where: "docs/research/reference/02-b.md:1",
		},
		{
			name: "report without sources",
			edit: func(m fstest.MapFS) {
				m["docs/research/reference/02-b.md"] = file(
					"# 02\n\nChecked against: v1\n\nResearch baseline: x\n\n```\n## Sources\n```\n")
			},
			want:  `report has no "## Sources"`,
			where: "docs/research/reference/02-b.md:1",
		},
		{
			name: "decision without status",
			edit: func(m fstest.MapFS) {
				m["docs/research/decisions/0001-one.md"] = file("# 0001\n\nStatus: Maybe\n")
			},
			want:  "decision has no Status: line",
			where: "docs/research/decisions/0001-one.md:1",
		},
		{
			name: "decision not indexed",
			edit: func(m fstest.MapFS) {
				m["docs/research/decisions/0002-two.md"] = file("# 0002\n\nStatus: Proposed\n")
			},
			want:  "decision 0002 (0002-two.md) is not indexed",
			where: "docs/research/decisions/README.md:1",
		},
		{
			name: "index links another file",
			edit: func(m fstest.MapFS) {
				m["docs/research/decisions/0001-copy.md"] = m["docs/research/decisions/0001-one.md"]
			},
			want:  "decision 0001 links 0001-one.md, not 0001-copy.md",
			where: "docs/research/decisions/README.md:5",
		},
		{
			name: "index status disagrees",
			edit: func(m fstest.MapFS) {
				m["docs/research/decisions/0001-one.md"] = file(
					"# 0001\n\nStatus: Superseded by 0002\n",
				)
			},
			want:  "decision 0001 is Accepted here but Superseded in its record",
			where: "docs/research/decisions/README.md:5",
		},
		{
			name: "no index",
			edit: func(m fstest.MapFS) {
				delete(m, "docs/research/decisions/README.md")
				m["docs/research/README.md"] = file("# R\n")
			},
			want:  "decision is not indexed: docs/research/decisions/README.md does not exist",
			where: "docs/research/decisions/0001-one.md:1",
		},
		{
			name: "tag missing from the capabilities register",
			edit: func(m fstest.MapFS) {
				m["api/reference/spacelift/spacelift.openapi.yaml"] = file(
					spec + "  - name: Spaces\n",
				)
			},
			want:  `has no deploy record tag:Spaces for tag "Spaces" of api/reference/spacelift/spacelift.openapi.yaml`,
			where: "docs/data/capabilities.json:1",
		},
		{
			name: "tag record the spec lacks",
			edit: func(m fstest.MapFS) {
				m["api/reference/spacelift/spacelift.openapi.yaml"] = file(
					strings.Replace(spec, "  - name: Runs\n", "", 1),
				)
			},
			want:  "deploy record tag:Runs names a tag api/reference/spacelift/spacelift.openapi.yaml does not have",
			where: "docs/data/capabilities.json:3",
		},
		{
			name: "unreadable spec tags",
			edit: func(m fstest.MapFS) {
				m["api/reference/spacelift/spacelift.openapi.yaml"] = file("tags: {")
			},
			want:  "cannot read its tags",
			where: "api/reference/spacelift/spacelift.openapi.yaml:1",
		},
		{
			name: "unverified report not listed by an open question",
			edit: func(m fstest.MapFS) {
				openQuestions(m, "docs/research/reference/01-a.md")
				m["docs/research/reference/30-open-questions.md"] = file(goodReport +
					"\nOpen claims stay *(unverified)*.\n")
				m["docs/research/reference/01-a.md"] = file(
					goodReport + "\nIt is *(unverified)* (C1).\n",
				)
				m["docs/research/reference/02-b.md"] = file(goodReport +
					"\nThe `*(unverified)*` marker.\n\n```\n*(unverified)*\n```\n\nIt is *(unverified)*.\n\nSo is this *(unverified)*.\n")
			},
			want:  "report marks a claim *(unverified)* but no record of docs/data/open-questions.json lists it",
			where: "docs/research/reference/02-b.md:17",
		},
		{
			name: "open questions not JSON",
			edit: func(m fstest.MapFS) {
				openQuestions(m)
				// Without a register that uses it, the schema would be a
				// second finding.
				delete(m, "docs/data/schema/open-questions.schema.json")
				m[openRegister] = file("{")
				m["docs/research/reference/02-b.md"] = file(
					goodReport + "\nIt is *(unverified)*.\n",
				)
			},
			want:  "not JSON",
			where: "docs/data/open-questions.json:1",
		},
		{
			name: "option heading differs",
			edit: func(m fstest.MapFS) {
				m["docs/research/architecture-options/2-b.md"] = file(
					strings.Replace(option, "## Walkthroughs", "## Walk-throughs", 1),
				)
			},
			want:  `outline departs from 1-a.md: has "## Walk-throughs" where it has "## Walkthroughs"`,
			where: "docs/research/architecture-options/2-b.md:17",
		},
		{
			name: "option without its tree heading",
			edit: func(m fstest.MapFS) {
				m["docs/research/architecture-options/2-b.md"] = file(
					strings.Replace(option, "### The tree\n\n", "", 1),
				)
			},
			want:  `has "## Deep dives" where it has "### The tree"`,
			where: "docs/research/architecture-options/2-b.md:11",
		},
		{
			name: "option missing a nested heading",
			edit: func(m fstest.MapFS) {
				m["docs/research/architecture-options/2-b.md"] = file(
					strings.Replace(option, "### A push\n", "", 1),
				)
			},
			want:  `has "(end)" where it has "### A push"`,
			where: "docs/research/architecture-options/2-b.md:1",
		},
		{
			name: "option with an extra heading",
			edit: func(m fstest.MapFS) {
				m["docs/research/architecture-options/2-b.md"] = file(option + "\n## Numbers\n")
			},
			want:  `has "## Numbers" where it has "(end)"`,
			where: "docs/research/architecture-options/2-b.md:21",
		},
		{
			name: "phase without exit criteria",
			edit: func(m fstest.MapFS) {
				m["docs/implementation/plan.md"] = file(
					strings.Replace(plan, "**Exit criteria**\n\n- Served.\n", "", 1),
				)
			},
			want:  `phase P1a has no "**Exit criteria**"`,
			where: "docs/implementation/plan.md:21",
		},
		{
			name: "phase without decisions",
			edit: func(m fstest.MapFS) {
				m["docs/implementation/plan.md"] = file(
					strings.Replace(
						plan,
						"Decisions: [0001](../research/decisions/0001-one.md)\n\n",
						"",
						1,
					),
				)
			},
			want:  `phase P0 has no "Decisions:"`,
			where: "docs/implementation/plan.md:7",
		},
		{
			name: "feature names a missing phase",
			edit: func(m fstest.MapFS) {
				m["docs/data/features.json"] = file(strings.Replace(features, `"P0"`, `"P2"`, 1))
			},
			want:  "stacks names phase P2, which plan.md does not have",
			where: "docs/data/features.json:2",
		},
		{
			name: "capability names a missing phase",
			edit: func(m fstest.MapFS) {
				m["docs/data/capabilities.json"] = file(
					strings.Replace(capabilities, `"also": "P0"`, `"also": "P0, P3"`, 1),
				)
			},
			want:  "tag:Runs names phase P3, which plan.md does not have",
			where: "docs/data/capabilities.json:3",
		},
		{
			name: "replacement in a missing phase",
			edit: func(m fstest.MapFS) {
				m["docs/data/supersessions.json"] = file(
					strings.Replace(supersessions, "P1a", "P1b", 1),
				)
			},
			want:  "SUP-01 names phase P1b, which plan.md does not have",
			where: "docs/data/supersessions.json:2",
		},
		{
			name:  "no glossary",
			edit:  func(m fstest.MapFS) { delete(m, "GLOSSARY.md") },
			want:  "the glossary does not exist",
			where: "GLOSSARY.md:1",
		},
		{
			name:  "glossary without a title",
			edit:  func(m fstest.MapFS) { m["GLOSSARY.md"] = file(strings.TrimPrefix(glossary, "# ")) },
			want:  "the glossary has no title",
			where: "GLOSSARY.md:1",
		},
		{
			name: "glossary without a language section",
			edit: func(m fstest.MapFS) {
				m["GLOSSARY.md"] = file(strings.Replace(glossary, "## Language", "## Terms", 1))
			},
			want:  `the glossary has no "## Language" section`,
			where: "GLOSSARY.md:1",
		},
		{
			name: "glossary term defined twice",
			edit: func(m fstest.MapFS) {
				m["GLOSSARY.md"] = file(glossary + "\n**stack**:\nAgain.\n_Avoid_: pile\n")
			},
			want:  `term "stack" is defined again (first at line 7)`,
			where: "GLOSSARY.md:20",
		},
		{
			name: "glossary term without avoid",
			edit: func(m fstest.MapFS) {
				m["GLOSSARY.md"] = file(strings.Replace(glossary, "_Avoid_: project\n", "", 1))
			},
			want:  `term "Stack" has no "_Avoid_:" line`,
			where: "GLOSSARY.md:7",
		},
		{
			name: "glossary term without a definition",
			edit: func(m fstest.MapFS) {
				m["GLOSSARY.md"] = file(
					strings.Replace(glossary, "A unit of infrastructure.\n", "", 1),
				)
			},
			want:  `term "Stack" has no definition`,
			where: "GLOSSARY.md:7",
		},
		{
			name:  "broken link in the glossary",
			edit:  func(m fstest.MapFS) { m["GLOSSARY.md"] = file(glossary + "\n[x](gone.md)\n") },
			want:  `broken link "gone.md"`,
			where: "GLOSSARY.md:20",
		},
		{
			name:  "stale mention",
			edit:  func(m fstest.MapFS) { m["internal/x/x.go"] = file("package x\n\n// docs/research/02-old.md\n") },
			want:  "names docs/research/02-old.md, which does not exist",
			where: "internal/x/x.go:3",
		},
		{
			name:  "stale mention in a Makefile",
			edit:  func(m fstest.MapFS) { m["Makefile"] = file("x: # docs/old.md\n") },
			want:  "names docs/old.md",
			where: "Makefile:1",
		},
		{
			name:  "package without doc.go",
			edit:  func(m fstest.MapFS) { m["pkg/y/y.go"] = file("package y\n") },
			want:  "Go package has no doc.go",
			where: "pkg/y:1",
		},
		{
			name: "doc.go without a package comment",
			edit: func(m fstest.MapFS) {
				m["internal/x/doc.go"] = file("// Package other is misnamed.\npackage x\n")
			},
			want:  `doc.go has no package comment starting "Package x"`,
			where: "internal/x/doc.go:1",
		},
		{
			name: "doc.go without a section",
			edit: func(m fstest.MapFS) {
				m["internal/x/doc.go"] = file(
					strings.Replace(pkgDoc("x"), "// # Dependencies\n", "", 1),
				)
			},
			want:  `package comment has no "# Dependencies" section`,
			where: "internal/x/doc.go:1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tree()
			tc.edit(m)
			found, err := Check(m)
			if err != nil {
				t.Fatal(err)
			}
			if len(found) != 1 {
				t.Fatalf("want one finding, got %d: %v", len(found), found)
			}
			if got := found[0].String(); !strings.HasPrefix(got, tc.where+": ") ||
				!strings.Contains(got, tc.want) {
				t.Errorf("got %q, want %s: …%s…", got, tc.where, tc.want)
			}
		})
	}
}

// openSchema is a minimal open-questions schema: records are cited as C<n>
// and name the reports that must cite them.
const openSchema = `{"x-id-pattern": "C[0-9]+", "x-cite-pattern": "\\bC[0-9]+\\b", "x-cited-in": "reports"}`

// openQuestions adds an open-questions register whose one record, C1, lists
// reports.
func openQuestions(m fstest.MapFS, reports ...string) {
	m["docs/data/schema/common.schema.json"] = file(commonSchema)
	m["docs/data/schema/open-questions.schema.json"] = file(openSchema)
	m[openRegister] = file(
		`{"$schema": "schema/open-questions.schema.json", "records": [{"id": "C1", "reports": ["` +
			strings.Join(reports, `", "`) + `"]}]}`,
	)
}

// TestUnitCheckScopeAndOpenQuestions covers the capabilities register,
// report 30 and the open-questions register when they satisfy their checks,
// and the register without a spec to check it against. Report 30 defines the
// marker, so no record lists it.
func TestUnitCheckScopeAndOpenQuestions(t *testing.T) {
	m := tree()
	m["docs/research/reference/30-open-questions.md"] = file(
		goodReport + "\n| *(unverified)* | A claim no primary source confirms |\n",
	)
	openQuestions(m, "docs/research/reference/01-a.md")
	m["docs/research/reference/01-a.md"] = file(goodReport + "\nIt is *(unverified)* (C1).\n")
	for name, withSpec := range map[string]bool{"with spec": true, "without spec": false} {
		t.Run(name, func(t *testing.T) {
			if withSpec {
				m["api/reference/spacelift/spacelift.openapi.yaml"] = file(spec)
			} else {
				delete(m, "api/reference/spacelift/spacelift.openapi.yaml")
			}
			found, err := Check(m)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range found {
				t.Error(f)
			}
		})
	}
}

// TestUnitCheckWithoutPlan covers trees from before the implementation
// plan, and trees without the registers that assign phases, where a spec's
// tags have no register to be checked against.
func TestUnitCheckWithoutPlan(t *testing.T) {
	registers := []string{
		"docs/data/features.json", "docs/data/capabilities.json", "docs/data/supersessions.json",
		"docs/data/schema/features.schema.json", "docs/data/schema/capabilities.schema.json",
		"docs/data/schema/supersessions.schema.json",
	}
	for name, gone := range map[string][]string{
		"no plan":      {"docs/implementation/plan.md"},
		"no registers": registers,
	} {
		t.Run(name, func(t *testing.T) {
			m := tree()
			for _, p := range gone {
				delete(m, p)
			}
			m[referenceAPI] = file(spec)
			found, err := Check(m)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range found {
				t.Error(f)
			}
		})
	}
}

func TestUnitCheckSortsFindings(t *testing.T) {
	m := tree()
	m["docs/b.md"] = file("[x](gone.md)\n[y](gone.md)\n")
	m["docs/a.md"] = file("[x](gone.md)\n")
	found, err := Check(m)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(found))
	for _, f := range found {
		got = append(got, fmt.Sprintf("%s:%d", f.Path, f.Line))
	}
	if strings.Join(got, " ") != "docs/a.md:1 docs/b.md:1 docs/b.md:2" {
		t.Errorf("order %v", got)
	}
}

// failFS fails to open one path, as a permission error would. It wraps
// rather than embeds the MapFS, so every read goes through Open.
type failFS struct {
	m    fstest.MapFS
	name string
}

var errRead = errors.New("read failed")

func (f failFS) Open(name string) (fs.File, error) {
	if name == f.name {
		return nil, errRead
	}
	return f.m.Open(name)
}

func TestUnitCheckReadErrors(t *testing.T) {
	for _, name := range []string{"docs/research/README.md", "internal/x/x.go", "docs", "internal", referenceAPI, glossaryDoc} {
		t.Run(name, func(t *testing.T) {
			m := tree()
			m[referenceAPI] = file(spec)
			if _, err := Check(failFS{m: m, name: name}); !errors.Is(err, errRead) {
				t.Errorf("err = %v, want %v", err, errRead)
			}
		})
	}
}

func TestUnitSlug(t *testing.T) {
	for h, want := range map[string]string{
		"Target version and sources":       "target-version-and-sources",
		"01: The Spacelift API landscape":  "01-the-spacelift-api-landscape",
		"P19: Live `make` checks (part 2)": "p19-live-make-checks-part-2",
		"snake_case and hyphen-ated":       "snake_case-and-hyphen-ated",
	} {
		if got := slug(h); got != want {
			t.Errorf("slug(%q) = %q, want %q", h, got, want)
		}
	}
}

// flowsAPI is the Flows reference spec, which the capabilities fixture's
// flows records must match.
const flowsAPI = "api/reference/spacelift-flows/flows.openapi.yaml"

// TestUnitCheckProductTags proves each reference's tags are held to its own
// product's records: a Flows tag needs a flows record, a Deploy tag a deploy
// one, and a flows record names a Flows tag.
func TestUnitCheckProductTags(t *testing.T) {
	flowsRecord := `{"id": "tag:Flows Projects", "product": "flows", "infraweave": {"phase": "P0"}}`
	withFlows := func(m fstest.MapFS, records ...string) {
		caps := capabilities
		for _, r := range records {
			caps = strings.Replace(caps, `"records": [`, `"records": [`+"\n  "+r+",", 1)
		}
		m[capsReg] = file(caps)
		m[referenceAPI] = file(spec)
		m[flowsAPI] = file("openapi: 3.0.3\ntags:\n  - name: Flows Projects\n")
	}
	for _, tc := range []struct {
		name string
		edit func(m fstest.MapFS)
		want []string
	}{
		{"both products placed", func(m fstest.MapFS) { withFlows(m, flowsRecord) }, nil},
		{"flows tag with no flows record", func(m fstest.MapFS) { withFlows(m) }, []string{
			`docs/data/capabilities.json:1: has no flows record tag:Flows Projects for tag "Flows Projects" of ` + flowsAPI,
		}},
		{"flows tag with a deploy record", func(m fstest.MapFS) {
			withFlows(m, strings.Replace(flowsRecord, `"flows"`, `"deploy"`, 1))
		}, []string{
			`docs/data/capabilities.json:1: has no flows record tag:Flows Projects for tag "Flows Projects" of ` + flowsAPI,
			"docs/data/capabilities.json:2: deploy record tag:Flows Projects names a tag " + referenceAPI + " does not have",
		}},
		{"deploy tag with only a flows record", func(m fstest.MapFS) {
			withFlows(m, flowsRecord)
			m[capsReg] = file(strings.Replace(string(m[capsReg].Data),
				`"tag:Runs", "product": "deploy"`, `"tag:Runs", "product": "flows"`, 1))
		}, []string{
			`docs/data/capabilities.json:1: has no deploy record tag:Runs for tag "Runs" of ` + referenceAPI,
			"docs/data/capabilities.json:4: flows record tag:Runs names a tag " + flowsAPI + " does not have",
		}},
		{"tag record without a product", func(m fstest.MapFS) {
			withFlows(m, flowsRecord)
			m[capsReg] = file(strings.Replace(string(m[capsReg].Data), `"tag:Runs", "product": "deploy", `, `"tag:Runs", `, 1))
		}, []string{
			`docs/data/capabilities.json:1: has no deploy record tag:Runs for tag "Runs" of ` + referenceAPI,
		}},
		{"unreadable flows spec", func(m fstest.MapFS) {
			withFlows(m, flowsRecord)
			m[flowsAPI] = file("tags: {")
		}, []string{flowsAPI + ":1: cannot read its tags"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tree()
			tc.edit(m)
			found, err := Check(m)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(found))
			for _, f := range found {
				got = append(got, f.String())
			}
			if len(got) != len(tc.want) {
				t.Fatalf("findings = %q, want %q", got, tc.want)
			}
			for i := range got {
				if !strings.HasPrefix(got[i], tc.want[i]) {
					t.Errorf("finding %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestUnitCapabilitiesSchemaProduct proves the committed capabilities schema
// requires product and accepts only the products specref knows.
func TestUnitCapabilitiesSchemaProduct(t *testing.T) {
	schema, err := os.ReadFile("../../../docs/data/schema/capabilities.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	common, err := os.ReadFile("../../../docs/data/schema/common.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	for name, product := range map[string]string{"no product": "", "unknown product": `"product": "space", `} {
		t.Run(name, func(t *testing.T) {
			m := tree()
			m["docs/data/schema/capabilities.schema.json"] = &fstest.MapFile{Data: schema}
			m["docs/data/schema/common.schema.json"] = &fstest.MapFile{Data: common}
			m[capsReg] = file(`{"$schema": "schema/capabilities.schema.json", "records": [
  {"id": "operation:x", "kind": "operation", ` + product + `"infraweave": {"feature": "x", "lowestTier": "Free", "phase": "P0"}}]}`)
			found, err := Check(m)
			if err != nil {
				t.Fatal(err)
			}
			if len(found) == 0 || !strings.Contains(found[0].String(), capsReg) {
				t.Errorf("findings = %v", found)
			}
		})
	}
	var s struct {
		Properties struct {
			Records struct {
				Items struct {
					Properties struct {
						Product struct {
							Enum []string `json:"enum"`
						} `json:"product"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"records"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema, &s); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(specref.Products()))
	for _, p := range specref.Products() {
		names = append(names, p.Name)
	}
	if got := s.Properties.Records.Items.Properties.Product.Enum; !slices.Equal(got, names) {
		t.Errorf("schema products %q, specref products %q", got, names)
	}
}
