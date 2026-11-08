package docscheck

import (
	"fmt"
	"go/scanner"
	"go/token"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
)

// recordCitation matches a pointer into the project's records: a decision, a
// report, or the ID of a register record. A comment that leans on one
// says nothing to a reader without the record open, so the comment states
// its reason itself and the citation sits on a ref: line of its own.
// "reports" counts only before a list of numbers, since "reports 2 visible"
// is the verb, and a bare possessive only on a decision's number, so "RFC
// 7591's" is no citation.
var recordCitation = regexp.MustCompile(`\b[Dd]ecisions? \d{4}\b|\b0\d{3}'s\b|` +
	`\b[Rr]eport \d+\b|\b[Rr]eports \d+(?:, \d+)*,? and \d+\b|` +
	`\b(?:ARC|DEP|DES|DV|IMP|OQ|PD|RES|RN|RV|SUP)-\d`)

// refMarkers are the comment markers a ref: line starts with in each kind
// of file Check scans for citations. A .feature description is prose
// rather than a comment, so a bare ref: line serves there too. Only the #
// comments of YAML and the Makefile are scanned: a spec's values, such as
// x-infraweave-divergence's IDs, are data that tools read, and the served
// prose has its own check in api's TestUnitServedProse.
var refMarkers = map[string][]string{
	".sql":     {"-- ref:"},
	".yaml":    {"# ref:"},
	".yml":     {"# ref:"},
	"Makefile": {"# ref:"},
	".feature": {"# ref:", "ref:"},
}

// refLines checks that a citation in the repository's Go comments,
// migrations, specs, configuration and scenarios sits on a ref: line.
// Documentation and the registers cite records by design, and the
// Spacelift reference under api/reference is generated from upstream, so
// neither is scanned.
func (c *checker) refLines() error {
	err := fs.WalkDir(c.repo, ".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir():
			name := d.Name()
			if p != "." && (strings.HasPrefix(name, ".") && name != ".github" ||
				slices.Contains(skipDirs, name) || p == docsDir || p == "api/reference") {
				return fs.SkipDir
			}
			return nil
		}
		kind := path.Ext(p)
		if d.Name() == "Makefile" {
			kind = d.Name()
		}
		if _, ok := refMarkers[kind]; !ok && kind != ".go" {
			return nil
		}
		b, err := fs.ReadFile(c.repo, p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		if kind == ".go" {
			c.goRefLines(p, b)
			return nil
		}
		for i, l := range strings.Split(string(b), "\n") {
			if _, hashed := hashComments[kind]; hashed {
				l = hashComment(l)
			}
			if recordCitation.MatchString(l) && !hasPrefix(strings.TrimSpace(l), refMarkers[kind]) {
				c.add(p, i+1, "cites %q outside a ref: line", recordCitation.FindString(l))
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk repository: %w", err)
	}
	return nil
}

// goRefLines checks the comments of one Go file. Only comments count: a
// string literal that names a decision is a message or a fixture, not a
// note to the reader of the code.
func (c *checker) goRefLines(p string, src []byte) {
	fset := token.NewFileSet()
	file := fset.AddFile(p, -1, len(src))
	var s scanner.Scanner
	s.Init(file, src, nil, scanner.ScanComments)
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			return
		}
		if tok != token.COMMENT {
			continue
		}
		line := fset.Position(pos).Line
		for i, l := range strings.Split(lit, "\n") {
			l = strings.TrimSpace(l)
			l = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(l, "//"), "/*"))
			if recordCitation.MatchString(l) && !strings.HasPrefix(l, "ref:") {
				c.add(p, line+i, "cites %q outside a ref: line", recordCitation.FindString(l))
			}
		}
	}
}

// hashComments are the kinds of file whose # comments alone are scanned.
var hashComments = map[string]bool{".yaml": true, ".yml": true, "Makefile": true}

// hashComment is l's comment: from a # that starts the line or follows
// whitespace, or "" when l has none. A # inside a word, as in a URL's
// fragment, starts no comment.
func hashComment(l string) string {
	for i, r := range l {
		if r == '#' && (i == 0 || l[i-1] == ' ' || l[i-1] == '\t') {
			return l[i:]
		}
	}
	return ""
}

func hasPrefix(s string, prefixes []string) bool {
	return slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(s, p) })
}
