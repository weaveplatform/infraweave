package docscheck

import (
	"cmp"
	"encoding/json"
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
	glossaryDoc  = "GLOSSARY.md"
	reportsDir   = "docs/research/reference"
	decisionsDir = "docs/research/decisions"
	decisionsIdx = decisionsDir + "/README.md"
	optionsDir   = "docs/research/architecture-options"
	planDoc      = "docs/implementation/plan.md"
	featuresReg  = "docs/data/features.json"
	capsReg      = "docs/data/capabilities.json"
	supersReg    = "docs/data/supersessions.json"
	tagPrefix    = "tag:"
	openReport   = reportsDir + "/30-open-questions.md"
	openRegister = "docs/data/open-questions.json"
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
	if err := c.glossary(); err != nil {
		return nil, fmt.Errorf("docscheck: %w", err)
	}
	for _, d := range c.sorted() {
		c.links(d)
		c.report(d)
		c.decision(d)
	}
	c.unverified()
	c.options()
	// The plan and tag checks read the registers, so they load first.
	if err := c.registers(); err != nil {
		return nil, fmt.Errorf("docscheck: %w", err)
	}
	c.plan()
	if err := c.tags(); err != nil {
		return nil, fmt.Errorf("docscheck: %w", err)
	}
	if err := c.mentions(); err != nil {
		return nil, fmt.Errorf("docscheck: %w", err)
	}
	if err := c.packageDocs(); err != nil {
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
	regs  []*register
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

// reg is the loaded register at p, or nil when it is absent or failed its
// schema, which registers() has already reported.
func (c *checker) reg(p string) *register {
	for _, r := range c.regs {
		if r.path == p {
			return r
		}
	}
	return nil
}

// tags checks that the capabilities register holds a tag: record for every
// tag of the reference spec and none for a tag the spec lacks, so a tag the
// weekly reference update adds or renames cannot go unplaced. Without the
// register there is no scope to check, and without the spec no tags.
func (c *checker) tags() error {
	reg := c.reg(capsReg)
	if reg == nil {
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
	inSpec := map[string]bool{}
	for _, t := range spec.Tags {
		inSpec[t.Name] = true
		if !reg.ids[tagPrefix+t.Name] {
			c.add(
				reg.path,
				1,
				"has no record %s%s for tag %q of %s",
				tagPrefix,
				t.Name,
				t.Name,
				referenceAPI,
			)
		}
	}
	for _, rec := range reg.records {
		id, _ := rec["id"].(string)
		if name, ok := strings.CutPrefix(id, tagPrefix); ok && !inSpec[name] {
			c.add(
				reg.path,
				reg.line(id),
				"record %s names a tag %s does not have",
				id,
				referenceAPI,
			)
		}
	}
	return nil
}

// unverified checks that an open-questions record lists every report that
// marks a claim *(unverified)*, so no open question is recorded only where it
// was found. Report 30 is exempt: it defines the marker rather than using it.
// Without the register there is nothing to check against, and a register
// that does not decode is a finding of registers().
func (c *checker) unverified() {
	b, err := fs.ReadFile(c.repo, openRegister)
	if err != nil {
		return
	}
	var reg struct {
		Records []struct {
			Reports []string `json:"reports"`
		} `json:"records"`
	}
	if json.Unmarshal(b, &reg) != nil {
		return
	}
	listed := map[string]bool{}
	for _, r := range reg.Records {
		for _, p := range r.Reports {
			listed[p] = true
		}
	}
	for _, d := range c.sorted() {
		if d.path == openReport || !reportName.MatchString(d.path) || listed[d.path] {
			continue
		}
		for i, l := range d.lines {
			// A marker in a code span is a mention of the convention, not a claim.
			if !d.code[i] && strings.Contains(codeSpan.ReplaceAllString(l, ""), unverified) {
				c.add(
					d.path,
					i+1,
					"report marks a claim %s but no record of %s lists it",
					unverified,
					openRegister,
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
// walkthrough, so each option must cover the same ones. A section that names
// a heading contributes only that H3: every option draws its whole tree under
// "The tree", so the trees can be read side by side, and may put tables of
// its own before it. Other sections may add H3s that only one option needs.
var nested = map[string]string{
	"Deep dives":        "",
	"Walkthroughs":      "",
	"Full package tree": "### The tree",
}

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
		case strings.HasPrefix(l, "### "):
			if only, ok := nested[section]; ok && (only == "" || l == only) {
				out = append(out, outlineHeading{i + 1, l})
			}
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

var (
	phaseHeading = regexp.MustCompile(`^## (P\d+[a-z]?): `)
	phaseRef     = regexp.MustCompile(`\bP\d+[a-z]?\b`)
)

// phaseParts are the lines every phase of the plan carries, in the plan's
// own wording, so a phase cannot be cut into tickets without its decisions,
// its blockers or a way to tell that it is finished.
var phaseParts = []string{"Decisions:", "**Depends on:**", "**Tasks**", "**Exit criteria**"}

// phaseMembers says where each register assigns phases: "" for the whole
// record, or the one member that holds infraweave's plan. A capability's
// spacelift member keeps report 26's wording, which names the research's
// provisional phases rather than the plan's, so it is not read.
var phaseMembers = []struct{ reg, member string }{
	{featuresReg, ""}, {capsReg, "infraweave"}, {supersReg, ""},
}

// plan checks each phase of the implementation plan for its parts, and
// that the registers assign features, reference rows and replacements only
// to phases the plan has.
func (c *checker) plan() {
	d, ok := c.docs[planDoc]
	if !ok {
		return
	}
	phases := map[string]bool{}
	var id string
	var start int
	var body []string
	check := func() {
		for _, want := range phaseParts {
			if !slices.ContainsFunc(
				body,
				func(l string) bool { return strings.HasPrefix(l, want) },
			) {
				c.add(d.path, start, "phase %s has no %q", id, want)
			}
		}
	}
	for i, l := range d.lines {
		if d.code[i] {
			continue
		}
		if strings.HasPrefix(l, "## ") {
			if id != "" {
				check()
			}
			id, start, body = "", 0, nil
			if m := phaseHeading.FindStringSubmatch(l); m != nil {
				id, start = m[1], i+1
				phases[id] = true
			}
			continue
		}
		if id != "" {
			body = append(body, l)
		}
	}
	if id != "" {
		check()
	}
	for _, pm := range phaseMembers {
		r := c.reg(pm.reg)
		if r == nil {
			continue
		}
		for _, rec := range r.records {
			id, _ := rec["id"].(string)
			var v any = r.text(rec)
			if pm.member != "" {
				v = rec[pm.member]
			}
			var texts []string
			stringsOf(v, &texts)
			for _, s := range texts {
				// A phase in a code span is a name, such as a file's, not an assignment.
				for _, ph := range phaseRef.FindAllString(codeSpan.ReplaceAllString(s, ""), -1) {
					if !phases[ph] {
						c.add(
							r.path,
							r.line(id),
							"%s names phase %s, which %s does not have",
							id,
							ph,
							path.Base(planDoc),
						)
					}
				}
			}
		}
	}
}

var glossaryTerm = regexp.MustCompile(`^\*\*(.+)\*\*:$`)

// glossary checks the root glossary's shape: a title, a "## Language"
// section, and terms that each open a block of a definition and an "_Avoid_:"
// line, so a term can't be left without the synonyms it replaces. Terms are
// unique regardless of case, because two entries for one word would let
// documents disagree on its meaning.
func (c *checker) glossary() error {
	if _, err := fs.Stat(c.repo, glossaryDoc); errors.Is(err, fs.ErrNotExist) {
		c.add(glossaryDoc, 1, "the glossary does not exist")
		return nil
	}
	d, err := c.load(glossaryDoc)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(d.lines[0], "# ") {
		c.add(d.path, 1, "the glossary has no title")
	}
	if !slices.Contains(d.lines, "## Language") {
		c.add(d.path, 1, `the glossary has no "## Language" section`)
	}
	first := map[string]int{}
	for i, l := range d.lines {
		m := glossaryTerm.FindStringSubmatch(l)
		if d.code[i] || m == nil {
			continue
		}
		term, line := m[1], i+1
		if at, ok := first[strings.ToLower(term)]; ok {
			c.add(d.path, line, "term %q is defined again (first at line %d)", term, at)
		} else {
			first[strings.ToLower(term)] = line
		}
		var block []string
		for _, b := range d.lines[i+1:] {
			if strings.TrimSpace(b) == "" {
				break
			}
			block = append(block, b)
		}
		avoid := slices.IndexFunc(
			block,
			func(b string) bool { return strings.HasPrefix(b, "_Avoid_:") },
		)
		switch avoid {
		case -1:
			c.add(d.path, line, `term %q has no "_Avoid_:" line`, term)
		case 0:
			c.add(d.path, line, "term %q has no definition", term)
		}
	}
	return nil
}
