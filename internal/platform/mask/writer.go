package mask

import (
	"bytes"
	"errors"
	"io"
)

// MaxLine is the longest line the Writer keeps. Bytes past it, up to the
// next newline, are dropped, so one unbroken stream cannot grow the buffer
// without bound.
const MaxLine = 64 << 10

// ErrClosed is returned by a write to a closed Writer.
var ErrClosed = errors.New("mask: write to a closed writer")

// Writer masks what is written to it one line at a time and writes each
// line on to the io.Writer it wraps once the line's newline arrives, so a
// secret split across writes is still masked. It is not safe for
// concurrent use.
type Writer struct {
	m      *Masker
	w      io.Writer
	line   []byte
	closed bool
	// err is the wrapped writer's first error, which every later write
	// returns: the line it failed on is lost, so the output is no longer
	// whole.
	err error
}

// Writer returns a Writer that masks text on its way to w.
func (m *Masker) Writer(w io.Writer) *Writer {
	return &Writer{m: m, w: w}
}

// Write takes p into the pending line and writes out each line that p
// completes. It returns len(p) unless the wrapped writer fails.
func (w *Writer) Write(p []byte) (int, error) {
	if w.closed {
		return 0, ErrClosed
	}
	if w.err != nil {
		return 0, w.err
	}
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		chunk := p
		if i >= 0 {
			chunk = p[:i]
		}
		w.line = append(w.line, chunk[:min(len(chunk), MaxLine-len(w.line))]...)
		if i < 0 {
			break
		}
		p = p[i+1:]
		if err := w.flush(true); err != nil {
			return n - len(p), err
		}
	}
	return n, nil
}

// Close writes the pending line, without a newline, and refuses later
// writes. It does not close the wrapped writer.
func (w *Writer) Close() error {
	if w.closed {
		return w.err
	}
	w.closed = true
	if w.err == nil && len(w.line) > 0 {
		return w.flush(false)
	}
	return w.err
}

func (w *Writer) flush(newline bool) error {
	out := w.m.Bytes(w.line)
	if newline {
		out = append(out, '\n')
	}
	_, w.err = w.w.Write(out)
	w.line = w.line[:0]
	return w.err
}
