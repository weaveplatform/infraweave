package docscheck

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

// TestUnitDocs is the check make docs-check runs over this repository.
func TestUnitDocs(t *testing.T) {
	found, err := Check(os.DirFS("../.."))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		t.Error(f)
	}
}

func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

const goodReport = "# 01: A report\n\nChecked against: v1\n\nResearch baseline: **2026-10-28**.\n\n## Sources\n\n- <https://example.com>\n"

// spec is a reference spec with two tags; report 26 must name both.
const spec = "openapi: 3.0.3\ntags:\n  - name: Stacks\n    description: Stack management.\n  - name: Runs\n"

// option is an architecture option's outline. H3s outside the nested
// sections may differ between options.
const option = "# Option\n\n## Summary\n\n### Rules\n\n## Deep dives\n\n### Runs\n\n## Walkthroughs\n\n### A push\n"

// plan is an implementation plan with two phases; the roadmap names both.
const plan = "# Plan\n\n## Build status\n\n| P0 | done |\n\n## P0: Reference\n\nDecisions: [0001](../research/decisions/0001-one.md)\n\n" +
	"**Depends on:** nothing.\n\n**Tasks**\n\n1. Generate.\n\n**Exit criteria**\n\n- `make gate` passes.\n\n" +
	"## P1a: Skeleton\n\nDecisions: none.\n\n**Depends on:** P0.\n\n**Tasks**\n\n1. Serve.\n\n```\n## P9: Not a phase\n```\n\n**Exit criteria**\n\n- Served.\n"

// tree is a repository that passes every check; each case changes one file.
func tree() fstest.MapFS {
	return fstest.MapFS{
		"README.md": file("# Repo\n\n## Make targets\n"),
		"Makefile":  file("# see docs/research/README.md\n"),
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
		"internal/x/testdata/fixture.md":            file("docs/missing.md\n"),
		"docs/research/architecture-options/1-a.md": file(option),
		"docs/research/architecture-options/2-b.md": file(
			option + "\n```\n## Not a heading\n```\n",
		),
		"docs/implementation/plan.md": file(plan),
		"docs/implementation/capability-roadmap.md": file(
			"# Roadmap\n\nP99 in prose is not an assignment.\n\n| Stacks | P0 | `P42` |\n| Runs | P1a |\n",
		),
		".git/description.md":     file("docs/missing.md\n"),
		".github/workflows/w.yml": file("# docs/research/README.md\n"),
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
			name: "tag missing from report 26",
			edit: func(m fstest.MapFS) {
				m["docs/research/reference/26-scope-and-tiers.md"] = file(
					goodReport + "\nStacks are in scope.\n",
				)
				m["api/reference/spacelift/spacelift.openapi.yaml"] = file(spec)
			},
			want:  `tag "Runs" of api/reference/spacelift/spacelift.openapi.yaml does not appear`,
			where: "docs/research/reference/26-scope-and-tiers.md:1",
		},
		{
			name: "unreadable spec tags",
			edit: func(m fstest.MapFS) {
				m["docs/research/reference/26-scope-and-tiers.md"] = file(goodReport)
				m["api/reference/spacelift/spacelift.openapi.yaml"] = file("tags: {")
			},
			want:  "cannot read its tags",
			where: "api/reference/spacelift/spacelift.openapi.yaml:1",
		},
		{
			name: "unverified report not linked from report 30",
			edit: func(m fstest.MapFS) {
				m["docs/research/reference/30-open-questions.md"] = file(goodReport +
					"\n[01](01-a.md) [web](https://example.com/02-b.md) [self](#sources) `[02](02-b.md)`\n" +
					"\n```\n[02](02-b.md)\n```\n\nOpen claims stay *(unverified)*.\n")
				m["docs/research/reference/01-a.md"] = file(
					goodReport + "\nIt is *(unverified)*.\n",
				)
				m["docs/research/reference/02-b.md"] = file(goodReport +
					"\nThe `*(unverified)*` marker.\n\n```\n*(unverified)*\n```\n\nIt is *(unverified)*.\n\nSo is this *(unverified)*.\n")
			},
			want:  "report marks a claim *(unverified)* but 30-open-questions.md does not link it",
			where: "docs/research/reference/02-b.md:17",
		},
		{
			name: "option heading differs",
			edit: func(m fstest.MapFS) {
				m["docs/research/architecture-options/2-b.md"] = file(
					strings.Replace(option, "## Walkthroughs", "## Walk-throughs", 1),
				)
			},
			want:  `outline departs from 1-a.md: has "## Walk-throughs" where it has "## Walkthroughs"`,
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
			where: "docs/research/architecture-options/2-b.md:15",
		},
		{
			name: "phase without exit criteria",
			edit: func(m fstest.MapFS) {
				m["docs/implementation/plan.md"] = file(strings.Replace(plan, "**Exit criteria**\n\n- Served.\n", "", 1))
			},
			want:  `phase P1a has no "**Exit criteria**"`,
			where: "docs/implementation/plan.md:21",
		},
		{
			name: "phase without decisions",
			edit: func(m fstest.MapFS) {
				m["docs/implementation/plan.md"] = file(
					strings.Replace(plan, "Decisions: [0001](../research/decisions/0001-one.md)\n\n", "", 1),
				)
			},
			want:  `phase P0 has no "Decisions:"`,
			where: "docs/implementation/plan.md:7",
		},
		{
			name: "roadmap names a missing phase",
			edit: func(m fstest.MapFS) {
				m["docs/implementation/capability-roadmap.md"] = file("# Roadmap\n\n| Stacks | P0, P2 |\n")
			},
			want:  "names phase P2, which plan.md does not have",
			where: "docs/implementation/capability-roadmap.md:3",
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

// TestUnitCheckScopeAndOpenQuestions covers reports 26 and 30 when they
// satisfy their checks, and report 26 without a spec to check it against.
func TestUnitCheckScopeAndOpenQuestions(t *testing.T) {
	m := tree()
	m["docs/research/reference/26-scope-and-tiers.md"] = file(goodReport + "\n| Stacks | Runs |\n")
	m["docs/research/reference/30-open-questions.md"] = file(
		goodReport + "\n[01](./01-a.md#sources)\n",
	)
	m["docs/research/reference/01-a.md"] = file(goodReport + "\nIt is *(unverified)*.\n")
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
// plan, with and without its capability roadmap.
func TestUnitCheckWithoutPlan(t *testing.T) {
	for name, gone := range map[string][]string{
		"no plan":    {"docs/implementation/plan.md", "docs/implementation/capability-roadmap.md"},
		"no roadmap": {"docs/implementation/capability-roadmap.md"},
	} {
		t.Run(name, func(t *testing.T) {
			m := tree()
			for _, p := range gone {
				delete(m, p)
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

func TestUnitCheckSortsFindings(t *testing.T) {
	m := tree()
	m["docs/b.md"] = file("[x](gone.md)\n[y](gone.md)\n")
	m["docs/a.md"] = file("[x](gone.md)\n")
	found, err := Check(m)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
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
	for _, name := range []string{"docs/research/README.md", "internal/x/x.go", "docs", "internal", referenceAPI} {
		t.Run(name, func(t *testing.T) {
			m := tree()
			m[scopeReport] = file(goodReport)
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
