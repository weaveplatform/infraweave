package sigcheck

import (
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Limit is the line golines keeps: it wraps a line longer than MaxLen
// characters, counting a tab as TabLen.
type Limit struct{ MaxLen, TabLen int }

// golines' defaults, kept where the configuration names no value.
const (
	defaultMaxLen = 100
	defaultTabLen = 4
)

// ErrConfig is a golangci-lint configuration the limit cannot be read
// from.
var ErrConfig = errors.New("sigcheck: the golangci-lint configuration")

// ReadLimit reads golines' max-len and tab-len from the golangci-lint
// configuration at path, golines' defaults where it names neither, so the
// check and the formatter cannot drift apart.
func ReadLimit(path string) (Limit, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Limit{}, fmt.Errorf("%w: %w", ErrConfig, err)
	}
	var c struct {
		Formatters struct {
			Settings struct {
				Golines struct {
					MaxLen int `yaml:"max-len"` //nolint:tagliatelle // golangci-lint's spelling
					TabLen int `yaml:"tab-len"` //nolint:tagliatelle // as above
				} `yaml:"golines"`
			} `yaml:"settings"`
		} `yaml:"formatters"`
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Limit{}, fmt.Errorf("%w: %s: %w", ErrConfig, path, err)
	}
	l := Limit{
		MaxLen: c.Formatters.Settings.Golines.MaxLen,
		TabLen: c.Formatters.Settings.Golines.TabLen,
	}
	if l.MaxLen == 0 {
		l.MaxLen = defaultMaxLen
	}
	if l.TabLen == 0 {
		l.TabLen = defaultTabLen
	}
	return l, nil
}

// Wrapped is a function signature over several lines that fits on one
// line of the limit: in File, from Line, joining into a line Length long.
type Wrapped struct {
	File   string
	Line   int
	Length int
}

func (w Wrapped) String() string {
	return fmt.Sprintf("%s:%d: the signature fits on one line of %d characters",
		w.File, w.Line, w.Length)
}

// span is a signature's bytes in its file, [start, end), from line.
type span struct{ start, end, line int }

// Join answers src, the Go file name, with every signature over several
// lines that fits on one line of l joined onto it, and the signatures it
// joined. A signature is a function's or a method's declaration up to its
// body's brace, a function literal's up to its brace, or an interface
// method's. One holding a comment, a string or a brace of its own, such
// as a struct type, is left as it is. A file that changes is gofmt'ed,
// since a joined line can move the column trailing comments align to; a
// generated one is left alone.
func Join(name string, src []byte, l Limit) ([]byte, []Wrapped, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		return nil, nil, fmt.Errorf("sigcheck: %w", err)
	}
	if ast.IsGenerated(f) {
		return src, nil, nil
	}
	var found []Wrapped
	var spans []span
	for _, s := range signatures(fset, f) {
		line, ok := fits(src, s, comments(fset, f), l)
		if !ok || len(spans) > 0 && s.start < spans[len(spans)-1].end {
			continue
		}
		spans = append(spans, s)
		found = append(found, Wrapped{File: name, Line: s.line, Length: lineLength(line, l.TabLen)})
	}
	if len(spans) == 0 {
		return src, nil, nil
	}
	out := slices.Clone(src)
	for _, s := range slices.Backward(spans) {
		out = slices.Concat(out[:s.start], []byte(joined(string(src[s.start:s.end]))), out[s.end:])
	}
	formatted, err := format.Source(out)
	if err != nil {
		return nil, nil, fmt.Errorf("sigcheck: %s: %w", name, err)
	}
	return formatted, found, nil
}

// signatures are f's signatures, sorted by where they start.
func signatures(fset *token.FileSet, f *ast.File) []span {
	var out []span
	add := func(from, to token.Pos) {
		p := fset.Position(from)
		out = append(out, span{p.Offset, fset.Position(to).Offset, p.Line})
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			if n.Body == nil {
				add(n.Type.Func, n.Type.End())
			} else {
				add(n.Type.Func, n.Body.Lbrace+1)
			}
		case *ast.FuncLit:
			add(n.Type.Func, n.Body.Lbrace+1)
		case *ast.InterfaceType:
			for _, m := range n.Methods.List {
				if _, ok := m.Type.(*ast.FuncType); ok && len(m.Names) > 0 {
					add(m.Names[0].Pos(), m.Type.End())
				}
			}
		}
		return true
	})
	slices.SortFunc(out, func(a, b span) int { return a.start - b.start })
	return out
}

// comments are the offsets in f where each of its comments starts.
func comments(fset *token.FileSet, f *ast.File) []int {
	var out []int
	for _, g := range f.Comments {
		for _, c := range g.List {
			out = append(out, fset.Position(c.Pos()).Offset)
		}
	}
	return out
}

// fits answers the line s joins into, and whether s spans lines, holds
// no comment, string or brace of its own, and joins into a line of at
// most l's length.
func fits(src []byte, s span, comments []int, l Limit) (string, bool) {
	text := string(src[s.start:s.end])
	if !strings.Contains(text, "\n") ||
		strings.ContainsAny(strings.TrimSuffix(text, "{"), "{\"`") ||
		slices.ContainsFunc(comments, func(c int) bool { return c >= s.start && c < s.end }) {
		return "", false
	}
	from := strings.LastIndexByte(string(src[:s.start]), '\n') + 1
	to := len(src)
	if i := strings.IndexByte(string(src[s.end:]), '\n'); i >= 0 {
		to = s.end + i
	}
	line := string(src[from:s.start]) + joined(text) + string(src[s.end:to])
	return line, lineLength(line, l.TabLen) <= l.MaxLen
}

// joined is text on one line: each of its lines trimmed and joined with a
// space, but none after an opening bracket, and a closing one taking the
// trailing comma before it away.
func joined(text string) string {
	var out string
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case out == "" || strings.HasSuffix(out, "(") || strings.HasSuffix(out, "["):
			out += line
		case strings.HasPrefix(line, ")") || strings.HasPrefix(line, "]"):
			out = strings.TrimSuffix(out, ",") + line
		default:
			out += " " + line
		}
	}
	return out
}

// lineLength is line's length as golines counts it: in characters, a tab
// counting tabLen.
func lineLength(line string, tabLen int) int {
	n := 0
	for _, r := range line {
		if r == '\t' {
			n += tabLen
		} else {
			n++
		}
	}
	return n
}

// skipped are the directories Walk does not enter: version control's and
// tools' state, other projects' code, and test fixtures, which may be
// shaped on purpose.
var skipped = []string{".git", ".claude", "node_modules", "testdata", "vendor"}

// Walk checks every Go file under root but *.gen.go and the skipped
// directories, joining the signatures that fit in place when write is
// set, and answers them, each File relative to root. It reads and writes
// through an os.Root, so a link cannot lead it out of the tree.
func Walk(root string, l Limit, write bool) ([]Wrapped, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("sigcheck: %w", err)
	}
	defer r.Close()
	var found []Wrapped
	err = fs.WalkDir(r.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && path != "." && slices.Contains(skipped, d.Name()):
			return fs.SkipDir
		case d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, ".gen.go"):
			return nil
		}
		src, err := r.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		out, w, err := Join(path, src, l)
		if err != nil {
			return err
		}
		found = append(found, w...)
		if !write || len(w) == 0 {
			return nil
		}
		info, err := d.Info()
		if err == nil {
			err = r.WriteFile(path, out, info.Mode().Perm())
		}
		if err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("sigcheck: %w", err)
	}
	return found, nil
}
