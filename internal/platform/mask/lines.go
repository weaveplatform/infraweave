package mask

// Lines masks text one line at a time, carrying from line to line what
// spans lines: a private key block a BEGIN line opened. Add grows the
// Masker it masks with, as ::add-mask does. It is not safe for concurrent
// use.
type Lines struct {
	m   *Masker
	key bool
}

// Lines returns a Lines that masks with m, with no block open.
func (m *Masker) Lines() *Lines { return &Lines{m: m} }

// Mask answers line, which holds no newline, masked: line itself when
// nothing is masked.
func (l *Lines) Mask(line []byte) []byte {
	out, key := l.m.mask(line, l.key)
	l.key = key
	return out
}

// Add masks words too, by New's rules, from the next line on.
func (l *Lines) Add(words ...[]byte) { l.m = l.m.With(words...) }
