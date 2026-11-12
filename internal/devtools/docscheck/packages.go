package docscheck

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// packageRoots are the trees whose Go packages each need a doc.go: the
// product's, and the test code other packages import. test/depguard is
// left out; its packages are lint fixtures.
var packageRoots = []string{"internal", "pkg", "test/acceptance", "test/deps"}

// packageSections are the headings every package comment carries after its
// summary, so a reader learns from go doc alone what the package touches and
// what it depends on (repo layout, Code placement).
var packageSections = []string{"# Input and output", "# Dependencies"}

// packageDocs checks that every Go package under packageRoots has a doc.go
// that holds its package comment, starting "Package <name>" and carrying
// packageSections. Directories the go command ignores, testdata and names
// starting with . or _, are skipped, and so is a directory of tests only.
func (c *checker) packageDocs() error {
	for _, root := range packageRoots {
		if _, err := fs.Stat(c.repo, root); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		err := fs.WalkDir(c.repo, root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return err
			}
			if name := d.Name(); p != root && (name == "testdata" ||
				strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return fs.SkipDir
			}
			return c.packageDoc(p)
		})
		if err != nil {
			return fmt.Errorf("walk %s: %w", root, err)
		}
	}
	return nil
}

func (c *checker) packageDoc(dir string) error {
	entries, err := fs.ReadDir(c.repo, dir)
	if err != nil {
		return fmt.Errorf("read %s: %w", dir, err)
	}
	isPackage := false
	for _, e := range entries {
		n := e.Name()
		isPackage = isPackage ||
			!e.IsDir() && path.Ext(n) == ".go" && !strings.HasSuffix(n, "_test.go")
	}
	if !isPackage {
		return nil
	}
	p := path.Join(dir, "doc.go")
	b, err := fs.ReadFile(c.repo, p)
	if errors.Is(err, fs.ErrNotExist) {
		c.add(dir, 1, "Go package has no doc.go")
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", p, err)
	}
	var comment []string
	name := ""
	for _, l := range strings.Split(string(b), "\n") {
		if n, ok := strings.CutPrefix(l, "package "); ok {
			name = strings.TrimSpace(n)
			break
		}
		if strings.HasPrefix(l, "//") {
			comment = append(comment, strings.TrimSpace(strings.TrimPrefix(l, "//")))
		}
	}
	if len(comment) == 0 || !strings.HasPrefix(comment[0], "Package "+name+" ") {
		c.add(p, 1, "doc.go has no package comment starting %q", "Package "+name)
		return nil
	}
	for _, s := range packageSections {
		found := false
		for _, l := range comment {
			found = found || l == s
		}
		if !found {
			c.add(p, 1, "package comment has no %q section", s)
		}
	}
	return nil
}
