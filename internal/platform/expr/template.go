package expr

import (
	"strings"
	"unicode/utf8"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

const (
	open    = "${{"
	escaped = "$${{"
	closing = "}}"
)

// segment is a piece of a template: literal text, or the source of one
// embedded expression and where it starts in the whole template.
type segment struct {
	text   string
	source string
	at     kernel.Position
	isExpr bool
}

// scanError is a template whose ${{ is never closed.
type scanError struct {
	at kernel.Position
}

// scan splits a template into literal text and embedded expressions. An
// expression runs from ${{ to the first }} outside its string literals and
// its own braces, so a map literal or a "}}" string inside it does not end
// it. $${{ is a literal ${{.
//
// ref: decision 0024 "Two forms"
func scan(tmpl string) ([]segment, *scanError) {
	var segs []segment
	var lit strings.Builder
	pos := kernel.Position{Line: 1, Column: 1}
	flush := func() {
		if lit.Len() > 0 {
			segs = append(segs, segment{text: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(tmpl); {
		switch {
		case strings.HasPrefix(tmpl[i:], escaped):
			lit.WriteString(open)
			pos = advance(pos, escaped)
			i += len(escaped)
		case strings.HasPrefix(tmpl[i:], open):
			flush()
			start := advance(pos, open)
			end := closeOf(tmpl, i+len(open))
			if end < 0 {
				return nil, &scanError{at: pos}
			}
			src := tmpl[i+len(open) : end]
			segs = append(segs, segment{source: src, at: start, isExpr: true})
			pos = advance(advance(start, src), closing)
			i = end + len(closing)
		default:
			r, n := utf8.DecodeRuneInString(tmpl[i:])
			lit.WriteRune(r)
			pos = advance(pos, tmpl[i:i+n])
			i += n
		}
	}
	flush()
	return segs, nil
}

// closeOf is the index of the }} that closes the expression starting at
// from, or -1 when there is none.
func closeOf(tmpl string, from int) int {
	depth := 0
	for i := from; i < len(tmpl); i++ {
		switch c := tmpl[i]; c {
		case '"', '\'':
			i = stringEnd(tmpl, i)
			if i < 0 {
				return -1
			}
		case '{':
			depth++
		case '}':
			if depth == 0 && strings.HasPrefix(tmpl[i:], closing) {
				return i
			}
			if depth > 0 {
				depth--
			}
		}
	}
	return -1
}

// stringEnd is the index of the last byte of the CEL string literal whose
// opening quote is at i, or -1 when it is not closed. A triple quote opens
// a literal that only a triple quote closes; a literal with an r or R
// prefix is raw, so a backslash escapes nothing.
func stringEnd(s string, i int) int {
	q := s[i : i+1]
	if strings.HasPrefix(s[i:], strings.Repeat(q, 3)) {
		q = strings.Repeat(q, 3)
	}
	raw := false
	for j := i - 1; j >= 0 && j >= i-2 && strings.IndexByte("rRbB", s[j]) >= 0; j-- {
		raw = raw || s[j] == 'r' || s[j] == 'R'
	}
	for j := i + len(q); j < len(s); j++ {
		if s[j] == '\\' && !raw {
			j++
			continue
		}
		if strings.HasPrefix(s[j:], q) {
			return j + len(q) - 1
		}
		if len(q) == 1 && s[j] == '\n' {
			return -1
		}
	}
	return -1
}

// advance moves p past s.
func advance(p kernel.Position, s string) kernel.Position {
	for _, r := range s {
		if r == '\n' {
			p.Line++
			p.Column = 1
			continue
		}
		p.Column++
	}
	return p
}

// whole reports whether a template is exactly one ${{ … }}, which is an
// expression and keeps its type.
func whole(segs []segment) bool {
	return len(segs) == 1 && segs[0].isExpr
}
