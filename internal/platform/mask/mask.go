package mask

import (
	"bytes"
	"strings"
)

const (
	// Replacement stands in for each run of masked bytes.
	Replacement = "*****"
	// MinLen is the shortest secret line that is masked. Masking `1` or `on`
	// would destroy the text and protect nothing.
	MinLen = 4
)

// Masker replaces the secrets it was built with. It is immutable, so one
// Masker may serve any number of goroutines.
type Masker struct {
	// root holds the root's transitions for every byte, so a byte that
	// starts no secret costs one array read. 0 is the root itself.
	root [256]int
	// next holds the trie edges of every other node. A map keeps the
	// automaton's size proportional to the secrets' total length, where a
	// full table per node would hold 256 entries for each byte of every
	// secret.
	next map[edge]int
	fail []int
	// longest is the length of the longest secret that ends at the node,
	// through its failure chain, or 0. Every other secret ending there is a
	// suffix of that one, so it alone decides what is masked.
	longest []int
}

// edge is a node and a byte: in next, the parent the byte leaves; while
// the automaton is built, the child the byte reaches.
type edge struct {
	node int
	b    byte
}

// New builds a Masker over secrets. Each secret is split into lines, with
// any trailing carriage return removed, and every line of MinLen bytes or
// more is masked on its own, so a mounted file's content is masked line by
// line and a secret never spans the lines the Writer splits text into.
func New(secrets ...[]byte) *Masker {
	m := &Masker{next: map[edge]int{}, fail: []int{0}, longest: []int{0}}
	depth := []int{0}
	children := [][]edge{nil}
	for _, s := range secrets {
		for line := range bytes.SplitSeq(s, []byte("\n")) {
			line = bytes.TrimSuffix(line, []byte("\r"))
			if len(line) < MinLen {
				continue
			}
			n := 0
			for _, b := range line {
				c := m.child(n, b)
				if c == 0 {
					c = len(depth)
					depth = append(depth, depth[n]+1)
					children = append(children, nil)
					children[n] = append(children[n], edge{c, b})
					m.fail = append(m.fail, 0)
					m.longest = append(m.longest, 0)
					if n == 0 {
						m.root[b] = c
					} else {
						m.next[edge{n, b}] = c
					}
				}
				n = c
			}
			m.longest[n] = depth[n]
		}
	}
	// Breadth first, a node's failure target is shallower and so already
	// final when the node is reached.
	queue := children[0]
	for len(queue) > 0 {
		n := queue[0].node
		queue = queue[1:]
		for _, c := range children[n] {
			if n != 0 {
				m.fail[c.node] = m.step(m.fail[n], c.b)
			}
			if m.longest[c.node] == 0 {
				m.longest[c.node] = m.longest[m.fail[c.node]]
			}
			queue = append(queue, c)
		}
	}
	return m
}

func (m *Masker) child(n int, b byte) int {
	if n == 0 {
		return m.root[b]
	}
	return m.next[edge{n, b}]
}

// step follows the automaton from node n on byte b.
func (m *Masker) step(n int, b byte) int {
	for n != 0 {
		if c, ok := m.next[edge{n, b}]; ok {
			return c
		}
		n = m.fail[n]
	}
	return m.root[b]
}

// span is the half-open byte range [start, end) of text to mask.
type span struct{ start, end int }

// spans finds the maximal runs of bytes that lie inside an occurrence of a
// secret. Overlapping and adjacent occurrences join one run, so the length
// and the count of what was masked do not show.
func spans[T string | []byte](m *Masker, text T) []span {
	var out []span
	n := 0
	for i := range len(text) {
		n = m.step(n, text[i])
		l := m.longest[n]
		if l == 0 {
			continue
		}
		s := span{i + 1 - l, i + 1}
		// Ends only grow, but a longer secret may start before runs
		// already found, so it absorbs every run it reaches.
		for len(out) > 0 && out[len(out)-1].end >= s.start {
			s.start = min(s.start, out[len(out)-1].start)
			out = out[:len(out)-1]
		}
		out = append(out, s)
	}
	return out
}

// String returns s with every run of secret bytes replaced by Replacement.
func (m *Masker) String(s string) string {
	found := spans(m, s)
	if len(found) == 0 {
		return s
	}
	var b strings.Builder
	at := 0
	for _, sp := range found {
		b.WriteString(s[at:sp.start])
		b.WriteString(Replacement)
		at = sp.end
	}
	b.WriteString(s[at:])
	return b.String()
}

// Bytes returns b with every run of secret bytes replaced by Replacement.
// When nothing matches it returns b itself; otherwise a new slice.
func (m *Masker) Bytes(b []byte) []byte {
	found := spans(m, b)
	if len(found) == 0 {
		return b
	}
	out := make([]byte, 0, len(b))
	at := 0
	for _, sp := range found {
		out = append(out, b[at:sp.start]...)
		out = append(out, Replacement...)
		at = sp.end
	}
	return append(out, b[at:]...)
}
