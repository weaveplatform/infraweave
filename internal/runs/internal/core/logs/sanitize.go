package logs

import "strings"

// The control characters that start and end escape sequences.
const (
	esc = 0x1b
	bel = 0x07
)

// Sanitize strips line's ANSI escape sequences: a control sequence, ESC [
// with its parameter, intermediate and final bytes; a command string,
// ESC ] (an operating system command), ESC P, ESC X, ESC ^ or ESC _, up to
// the BEL or ESC \ that ends it, or the line's end; and any other escape,
// ESC with its intermediate bytes and final byte. The rest of the line
// stays as it was written.
func Sanitize(line string) string {
	if strings.IndexByte(line, esc) < 0 {
		return line
	}
	var b strings.Builder
	b.Grow(len(line))
	for i := 0; i < len(line); {
		if line[i] != esc {
			b.WriteByte(line[i])
			i++
			continue
		}
		i = escapeEnd(line, i+1)
	}
	return b.String()
}

// escapeEnd is the index after the escape sequence whose ESC precedes
// s[i].
func escapeEnd(s string, i int) int {
	if i >= len(s) {
		return i
	}
	switch s[i] {
	case '[':
		i++
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x3f {
			i++
		}
		if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7e {
			i++
		}
		return i
	case ']', 'P', 'X', '^', '_':
		for i++; i < len(s); i++ {
			switch {
			case s[i] == bel:
				return i + 1
			case s[i] == esc && i+1 < len(s) && s[i+1] == '\\':
				return i + 2
			}
		}
		return i
	}
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
		i++
	}
	return i
}
