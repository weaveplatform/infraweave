package mask

import (
	"bytes"
	"slices"
)

const (
	// Replacement stands in for each run of masked bytes.
	Replacement = "*****"
	// MinLen is the shortest secret line that is masked. Masking `1` or `on`
	// would destroy the text and protect nothing.
	MinLen = 4
)

// Masker replaces the secrets it was built with and, when asked, the
// well-known ones. It is immutable, so one Masker may serve any number of
// goroutines.
type Masker struct {
	// lines are the secret lines the automaton holds, which With builds a
	// larger one from.
	lines [][]byte
	// wellKnown is whether the well-known patterns apply too.
	wellKnown bool
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
	return build(secretLines(nil, secrets))
}

// With returns a Masker that masks what m masks and words too, by New's
// rules; m itself when no word adds a line to mask.
func (m *Masker) With(words ...[]byte) *Masker {
	lines := secretLines(slices.Clip(m.lines), words)
	if len(lines) == len(m.lines) {
		return m
	}
	n := build(lines)
	n.wellKnown = m.wellKnown
	return n
}

// WellKnown returns a Masker that masks what m masks and every secret of
// the well-known patterns too.
//
// ref: decision 0013 § "Masking"
func (m *Masker) WellKnown() *Masker {
	n := *m
	n.wellKnown = true
	return &n
}

// secretLines appends to lines a copy of every line of secrets that is
// masked: each split at its newlines, without a trailing carriage
// return, and at least MinLen bytes long.
func secretLines(lines, secrets [][]byte) [][]byte {
	for _, s := range secrets {
		for line := range bytes.SplitSeq(s, []byte("\n")) {
			line = bytes.TrimSuffix(line, []byte("\r"))
			if len(line) >= MinLen {
				lines = append(lines, bytes.Clone(line))
			}
		}
	}
	return lines
}

// build builds the automaton over lines.
func build(lines [][]byte) *Masker {
	m := &Masker{lines: lines, next: map[edge]int{}, fail: []int{0}, longest: []int{0}}
	depth := []int{0}
	children := [][]edge{nil}
	for _, line := range lines {
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
func (m *Masker) spans(text []byte) []span {
	var out []span
	n := 0
	for i := range len(text) {
		n = m.step(n, text[i])
		l := m.longest[n]
		if l == 0 {
			continue
		}
		out = join(out, span{i + 1 - l, i + 1})
	}
	return out
}

// join appends s to runs, which are sorted and apart, absorbing every run
// s overlaps or touches. Ends only grow, but s may start before runs
// already found.
func join(runs []span, s span) []span {
	for len(runs) > 0 && runs[len(runs)-1].end >= s.start {
		s.start = min(s.start, runs[len(runs)-1].start)
		s.end = max(s.end, runs[len(runs)-1].end)
		runs = runs[:len(runs)-1]
	}
	return append(runs, s)
}

// mask masks text, which may hold several lines, with a private key block
// open at its start when key is set, and answers whether one is open at
// its end. It answers text itself when nothing is masked.
func (m *Masker) mask(text []byte, key bool) ([]byte, bool) {
	found := m.spans(text)
	if m.wellKnown {
		var more []span
		at := 0
		for line := range bytes.SplitSeq(text, []byte("\n")) {
			var s []span
			s, key = patternSpans(line, at, key)
			more = append(more, s...)
			at += len(line) + 1
		}
		if len(more) > 0 {
			more = append(more, found...)
			slices.SortFunc(more, func(a, b span) int { return a.start - b.start })
			found = found[:0]
			for _, s := range more {
				found = join(found, s)
			}
		}
	}
	if len(found) == 0 {
		return text, key
	}
	out := make([]byte, 0, len(text))
	at := 0
	for _, sp := range found {
		out = append(out, text[at:sp.start]...)
		out = append(out, Replacement...)
		at = sp.end
	}
	return append(out, text[at:]...), key
}

// String returns s with every run of secret bytes replaced by Replacement.
func (m *Masker) String(s string) string {
	return string(m.Bytes([]byte(s)))
}

// Bytes returns b with every run of secret bytes replaced by Replacement.
// When nothing matches it returns b itself; otherwise a new slice. A
// private key block is masked within b alone.
func (m *Masker) Bytes(b []byte) []byte {
	out, _ := m.mask(b, false)
	return out
}
