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
		"internal/x/x_test.go":           file("// docs/missing.md is a fixture.\n"),
		"internal/x/testdata/fixture.md": file("docs/missing.md\n"),
		".git/description.md":            file("docs/missing.md\n"),
		".github/workflows/w.yml":        file("# docs/research/README.md\n"),
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
	for _, name := range []string{"docs/research/README.md", "internal/x/x.go", "docs", "internal"} {
		t.Run(name, func(t *testing.T) {
			if _, err := Check(failFS{m: tree(), name: name}); !errors.Is(err, errRead) {
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
