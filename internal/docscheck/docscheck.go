// Package docscheck checks the structure of the documentation tree, so that a
// moved or renamed document cannot leave dangling references behind and a
// research report or decision record cannot merge without the parts readers
// rely on. make docs-check runs it over the repository, and the gate blocks
// on any finding.
//
// The checks:
//   - every relative markdown link under docs/ resolves, including its
//     #anchor when the target is markdown;
//   - every numbered report in docs/research/reference/ has its
//     "Checked against:" and "Research baseline:" lines and a "## Sources"
//     section (docs/research/README.md, Conventions);
//   - every decision record has a Status of Proposed, Accepted or Superseded,
//     and docs/research/decisions/README.md lists it with that status;
//   - every docs/….md path named in the repository's source, configuration
//     and documentation exists;
//   - every tag of the Spacelift reference spec appears in report 26, so the
//     scope decision accounts for every area of the API;
//   - every report with an *(unverified)* claim is linked from report 30,
//     the register of open questions;
//   - every numbered architecture option has the same outline as the first,
//     so the comparison in docs/research/architecture-options/README.md
//     compares like with like.
package docscheck

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

const (
	docsDir      = "docs"
	reportsDir   = "docs/research/reference"
	decisionsDir = "docs/research/decisions"
	decisionsIdx = decisionsDir + "/README.md"
	optionsDir   = "docs/research/architecture-options"
	scopeReport  = reportsDir + "/26-scope-and-tiers.md"
	openReport   = reportsDir + "/30-open-questions.md"
	referenceAPI = "api/reference/spacelift/spacelift.openapi.yaml"
	unverified   = "*(unverified)*"
)

// Finding is one problem at a file and line of the checked tree. Line is 1
// for a problem with the file as a whole.
type Finding struct {
	Path    string
	Line    int
	Message string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s", f.Path, f.Line, f.Message)
}

// Check runs every check over repo, a repository root, and returns the
// findings sorted by path and line. An error means the tree couldn't be read,
// not that it failed a check.
func Check(repo fs.FS) ([]Finding, error) {
	c := &checker{repo: repo, docs: map[string]*doc{}}
	err := fs.WalkDir(repo, docsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".md" {
			return err
		}
		_, err = c.load(p)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("docscheck: %w", err)
	}
	for _, d := range c.sorted() {
		c.links(d)
		c.report(d)
		c.decision(d)
	}
	c.unverified()
	c.options()
	if err := c.tags(); err != nil {
		return nil, fmt.Errorf("docscheck: %w", err)
	}
	if err := c.mentions(); err != nil {
		return nil, fmt.Errorf("docscheck: %w", err)
	}
	slices.SortFunc(c.found, func(a, b Finding) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Line, b.Line),
			cmp.Compare(a.Message, b.Message))
	})
	return c.found, nil
}

type checker struct {
	repo  fs.FS
	docs  map[string]*doc
	found []Finding
}

func (c *checker) add(p string, line int, format string, args ...any) {
	c.found = append(c.found, Finding{Path: p, Line: line, Message: fmt.Sprintf(format, args...)})
}

func (c *checker) sorted() []*doc {
	out := make([]*doc, 0, len(c.docs))
	for _, d := range c.docs {
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b *doc) int { return cmp.Compare(a.path, b.path) })
	return out
}

// load parses a markdown file once. Links may point at markdown outside
// docs/, such as the repository README, whose anchors are checked the same
// way.
func (c *checker) load(p string) (*doc, error) {
	if d, ok := c.docs[p]; ok {
		return d, nil
	}
	b, err := fs.ReadFile(c.repo, p)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	d := parse(p, string(b))
	c.docs[p] = d
	return d, nil
}

// doc is a markdown file split into lines, with the lines inside fenced code
// blocks marked so that examples aren't read as links or headings.
type doc struct {
	path    string
	lines   []string
	code    []bool
	anchors map[string]bool
}

var heading = regexp.MustCompile(`^#{1,6}\s+(.*?)\s*#*\s*$`)

func parse(p, text string) *doc {
	lines := strings.Split(text, "\n")
	d := &doc{path: p, lines: lines, code: make([]bool, len(lines)), anchors: map[string]bool{}}
	fence := ""
	seen := map[string]int{}
	for i, l := range lines {
		// A Windows checkout may carry CRLF; the \r would end up in slugs.
		l = strings.TrimSuffix(l, "\r")
		lines[i] = l
		t := strings.TrimLeft(l, " ")
		switch {
		case fence != "":
			d.code[i] = true
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		case strings.HasPrefix(t, "```"), strings.HasPrefix(t, "~~~"):
			d.code[i] = true
			fence = t[:3]
			continue
		}
		if m := heading.FindStringSubmatch(l); m != nil {
			// GitHub suffixes repeated headings -1, -2, … in document order.
			s := slug(m[1])
			n := seen[s]
			seen[s]++
			if n > 0 {
				s = fmt.Sprintf("%s-%d", s, n)
			}
			d.anchors[s] = true
		}
	}
	return d
}

// slug is GitHub's anchor for a heading: lower case, spaces become hyphens,
// and everything but letters, digits, hyphens and underscores is dropped.
func slug(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(h) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

var (
	codeSpan = regexp.MustCompile("`[^`]*`")
	mdLink   = regexp.MustCompile(`\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
)

func (c *checker) links(d *doc) {
	for i, l := range d.lines {
		if d.code[i] {
			continue
		}
		for _, m := range mdLink.FindAllStringSubmatch(codeSpan.ReplaceAllString(l, ""), -1) {
			c.link(d, i+1, m[1])
		}
	}
}

func (c *checker) link(d *doc, line int, target string) {
	u, err := url.Parse(target)
	if err != nil {
		c.add(d.path, line, "malformed link %q", target)
		return
	}
	if u.Scheme != "" {
		return
	}
	dest := d.path
	if u.Path != "" {
		dest = path.Join(path.Dir(d.path), u.Path)
		if dest == ".." || strings.HasPrefix(dest, "../") {
			c.add(d.path, line, "link %q leaves the repository", target)
			return
		}
		if _, err := fs.Stat(c.repo, dest); err != nil {
			c.add(d.path, line, "broken link %q: %s does not exist", target, dest)
			return
		}
	}
	if u.Fragment == "" || path.Ext(dest) != ".md" {
		return
	}
	t, err := c.load(dest)
	if err != nil {
		c.add(d.path, line, "link %q: %v", target, err)
		return
	}
	if !t.anchors[u.Fragment] {
		c.add(d.path, line, "link %q: %s has no heading #%s", target, dest, u.Fragment)
	}
}

var reportName = regexp.MustCompile(`^` + reportsDir + `/\d{2}-[^/]+\.md$`)

// report checks a numbered research report for the parts every report
// carries: the version it was checked against, its research date and its
// sources.
func (c *checker) report(d *doc) {
	if !reportName.MatchString(d.path) {
		return
	}
	for _, want := range []string{"Checked against:", "Research baseline:", "## Sources"} {
		if !slices.ContainsFunc(
			d.text(),
			func(l string) bool { return strings.HasPrefix(l, want) },
		) {
			c.add(d.path, 1, "report has no %q", want)
		}
	}
}

// text is the lines outside fenced code blocks.
func (d *doc) text() []string {
	out := make([]string, 0, len(d.lines))
	for i, l := range d.lines {
		if !d.code[i] {
			out = append(out, l)
		}
	}
	return out
}

var (
	decisionName = regexp.MustCompile(`^` + decisionsDir + `/(\d{4})-[^/]+\.md$`)
	statusLine   = regexp.MustCompile(`^Status: (Proposed|Accepted|Superseded)\b`)
	indexRow     = regexp.MustCompile(`^\|\s*(\d{4})\s*\|\s*\[[^\]]*\]\(([^)]+)\)\s*\|\s*(\w+)`)
)

type indexEntry struct {
	line   int
	file   string
	status string
}

// decision checks a decision record's status and its row in the index.
func (c *checker) decision(d *doc) {
	m := decisionName.FindStringSubmatch(d.path)
	if m == nil {
		return
	}
	status := ""
	for _, l := range d.text() {
		if s := statusLine.FindStringSubmatch(l); s != nil {
			status = s[1]
			break
		}
	}
	if status == "" {
		c.add(d.path, 1, "decision has no Status: line of Proposed, Accepted or Superseded")
	}
	idx, ok := c.docs[decisionsIdx]
	if !ok {
		c.add(d.path, 1, "decision is not indexed: %s does not exist", decisionsIdx)
		return
	}
	e, ok := index(idx)[m[1]]
	switch {
	case !ok:
		c.add(decisionsIdx, 1, "decision %s (%s) is not indexed", m[1], path.Base(d.path))
	case e.file != path.Base(d.path):
		c.add(decisionsIdx, e.line, "decision %s links %s, not %s", m[1], e.file, path.Base(d.path))
	case status != "" && e.status != status:
		c.add(
			decisionsIdx,
			e.line,
			"decision %s is %s here but %s in its record",
			m[1],
			e.status,
			status,
		)
	}
}

func index(d *doc) map[string]indexEntry {
	out := map[string]indexEntry{}
	for i, l := range d.lines {
		if m := indexRow.FindStringSubmatch(l); m != nil {
			out[m[1]] = indexEntry{line: i + 1, file: m[2], status: m[3]}
		}
	}
	return out
}

// tags checks that report 26 names every tag of the reference spec. Without
// report 26 there is no scope to check, and without the spec no tags.
func (c *checker) tags() error {
	scope, ok := c.docs[scopeReport]
	if !ok {
		return nil
	}
	b, err := fs.ReadFile(c.repo, referenceAPI)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", referenceAPI, err)
	}
	var spec struct {
		Tags []struct {
			Name string `yaml:"name"`
		} `yaml:"tags"`
	}
	if err := yaml.Unmarshal(b, &spec); err != nil {
		c.add(referenceAPI, 1, "cannot read its tags: %v", err)
		return nil
	}
	text := strings.Join(scope.lines, "\n")
	for _, t := range spec.Tags {
		if !strings.Contains(text, t.Name) {
			c.add(scope.path, 1, "tag %q of %s does not appear", t.Name, referenceAPI)
		}
	}
	return nil
}

// unverified checks that report 30 links every report that marks a claim
// *(unverified)*, so no open question is recorded only where it was found.
func (c *checker) unverified() {
	open, ok := c.docs[openReport]
	if !ok {
		return
	}
	linked := map[string]bool{}
	for i, l := range open.lines {
		if open.code[i] {
			continue
		}
		for _, m := range mdLink.FindAllStringSubmatch(codeSpan.ReplaceAllString(l, ""), -1) {
			if u, err := url.Parse(m[1]); err == nil && u.Scheme == "" && u.Path != "" {
				linked[path.Join(path.Dir(open.path), u.Path)] = true
			}
		}
	}
	for _, d := range c.sorted() {
		if d == open || !reportName.MatchString(d.path) || linked[d.path] {
			continue
		}
		for i, l := range d.lines {
			// A marker in a code span is a mention of the convention, not a claim.
			if !d.code[i] && strings.Contains(codeSpan.ReplaceAllString(l, ""), unverified) {
				c.add(
					d.path,
					i+1,
					"report marks a claim %s but %s does not link it",
					unverified,
					path.Base(openReport),
				)
				break
			}
		}
	}
}

var (
	mention = regexp.MustCompile(`(?:^|[^\w./-])(docs/[\w./-]+\.md)`)
	// Comments, messages and configuration name documents in these files.
	mentionExts = []string{".go", ".md", ".yaml", ".yml", ".feature"}
	// Fixtures, build output and dependencies aren't the repository's own
	// references. Of the dot directories only .github is the repository's.
	skipDirs = []string{"testdata", "cover", "bin", "node_modules", "vendor"}
)

// mentions checks every docs/….md path named in the repository. Paths are
// matched from the repository root, so a relative "../docs/…" is not one.
// Test files are skipped: a test that names a document reads it and fails
// by itself, and this package's tests name documents that don't exist.
func (c *checker) mentions() error {
	err := fs.WalkDir(c.repo, ".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir():
			name := d.Name()
			if p != "." &&
				(strings.HasPrefix(name, ".") && name != ".github" || slices.Contains(skipDirs, name)) {
				return fs.SkipDir
			}
			return nil
		case strings.HasSuffix(p, "_test.go"),
			!slices.Contains(mentionExts, path.Ext(p)) && d.Name() != "Makefile":
			return nil
		}
		b, err := fs.ReadFile(c.repo, p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		for i, l := range strings.Split(string(b), "\n") {
			for _, m := range mention.FindAllStringSubmatch(l, -1) {
				if _, err := fs.Stat(c.repo, m[1]); errors.Is(err, fs.ErrNotExist) {
					c.add(p, i+1, "names %s, which does not exist", m[1])
				}
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk repository: %w", err)
	}
	return nil
}

var optionName = regexp.MustCompile(`^` + optionsDir + `/\d+-[^/]+\.md$`)

// nested names the H2 sections whose H3s are part of the outline too: the
// comparison weighs the options domain by domain and walkthrough by
// walkthrough, so each option must cover the same ones. Other sections may
// add H3s that only one option needs.
var nested = map[string]bool{"Deep dives": true, "Walkthroughs": true}

type outlineHeading struct {
	line int
	text string
}

// outline is a document's H2 headings, with the H3s of the nested sections.
func outline(d *doc) []outlineHeading {
	var out []outlineHeading
	section := ""
	for i, l := range d.lines {
		if d.code[i] {
			continue
		}
		switch {
		case strings.HasPrefix(l, "## "):
			section = strings.TrimSpace(l[3:])
			out = append(out, outlineHeading{i + 1, l})
		case strings.HasPrefix(l, "### ") && nested[section]:
			out = append(out, outlineHeading{i + 1, l})
		}
	}
	return out
}

// options checks every numbered architecture option against the outline of
// the first, reporting the first heading where each one departs from it.
func (c *checker) options() {
	var first *doc
	var want []outlineHeading
	for _, d := range c.sorted() {
		if !optionName.MatchString(d.path) {
			continue
		}
		got := outline(d)
		if first == nil {
			first, want = d, got
			continue
		}
		for i := range max(len(got), len(want)) {
			var g, w outlineHeading
			if i < len(got) {
				g = got[i]
			}
			if i < len(want) {
				w = want[i]
			}
			if g.text == w.text {
				continue
			}
			// A missing heading has no line of its own: the file as a whole.
			c.add(d.path, max(g.line, 1), "outline departs from %s: has %q where it has %q",
				path.Base(first.path), cmp.Or(g.text, "(end)"), cmp.Or(w.text, "(end)"))
			break
		}
	}
}
