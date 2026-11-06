package seal

import (
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// SegmentSize is the plaintext of every segment of a sealed object but
// the last, which holds the rest: 0 bytes only when the object is empty.
const SegmentSize = 64 << 10

// magic opens a sealed object, before its version byte.
const magic = "IWSB"

// header is the magic and the version byte, which every segment's
// associated data repeats.
const header = len(magic) + 1

// segmentWire is a full segment as stored: nonce, ciphertext and tag.
const segmentWire = SegmentSize + overhead

// maxSegments is the 2^32 limit cipher.NewGCMWithRandomNonce sets for one
// key, which caps an object at 256 TiB. A variable so a test can reach it.
var maxSegments uint64 = 1 << 32

var (
	// ErrTooLarge reports an object that would need more than 2^32
	// segments.
	ErrTooLarge = errors.New("seal: object exceeds 2^32 segments")
	// ErrClosed reports a write to a Writer after Close.
	ErrClosed = errors.New("seal: write after close")
)

// segmentAAD is IWSB | 0x01 | index (uint64, big-endian) | last flag: the
// index and the flag make a reordered, truncated or extended object fail
// to authenticate without deriving any nonce from them.
func segmentAAD(index uint64, last bool) [header + 8 + 1]byte {
	var aad [header + 8 + 1]byte
	copy(aad[:], magic)
	aad[len(magic)] = Version
	binary.BigEndian.PutUint64(aad[header:], index)
	if last {
		aad[header+8] = 1
	}
	return aad
}

// ObjectSize is the size of a sealed object of n plaintext bytes: the
// header, then each segment's plaintext with its nonce and tag. An empty
// object is one empty segment, so a store can declare the size before it
// writes.
func ObjectSize(n int64) int64 {
	segments := max(1, (n+SegmentSize-1)/SegmentSize)
	return int64(header) + n + segments*overhead
}

// Writer seals an object to an io.Writer, one segment at a time. It holds
// one segment of plaintext and one of ciphertext, whatever the object's
// size. Close seals the last segment, so an object whose Writer is not
// closed does not open.
type Writer struct {
	w      io.Writer
	aead   cipher.AEAD
	plain  []byte
	sealed []byte
	index  uint64
	err    error
}

// NewWriter writes the object header to w and returns a Writer that seals
// what is written to it under key, the object's own content key.
func NewWriter(w io.Writer, key []byte) (*Writer, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(w, magic+string(Version)); err != nil {
		return nil, fmt.Errorf("write sealed object: %w", err)
	}
	return &Writer{
		w:      w,
		aead:   aead,
		plain:  make([]byte, 0, SegmentSize),
		sealed: make([]byte, 0, segmentWire),
	}, nil
}

// Write buffers p, sealing each full segment once more follows it: a full
// segment may be the last, which only Close knows.
func (w *Writer) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	n := 0
	for len(p) > 0 {
		if len(w.plain) == SegmentSize {
			if err := w.flush(false); err != nil {
				return n, err
			}
		}
		c := copy(w.plain[len(w.plain):SegmentSize], p)
		w.plain = w.plain[:len(w.plain)+c]
		p = p[c:]
		n += c
	}
	return n, nil
}

// Close seals the buffered plaintext as the last segment. It does not
// close the underlying writer.
func (w *Writer) Close() error {
	if errors.Is(w.err, ErrClosed) {
		return nil
	}
	if w.err != nil {
		return w.err
	}
	if err := w.flush(true); err != nil {
		return err
	}
	w.err = ErrClosed
	return nil
}

func (w *Writer) flush(last bool) error {
	if w.index >= maxSegments {
		w.err = ErrTooLarge
		return w.err
	}
	aad := segmentAAD(w.index, last)
	//nolint:gosec // G407: NewGCMWithRandomNonce draws the nonce; Seal takes none
	w.sealed = w.aead.Seal(
		w.sealed[:0],
		nil,
		w.plain,
		aad[:],
	)
	if _, err := w.w.Write(w.sealed); err != nil {
		w.err = fmt.Errorf("write sealed object: %w", err)
		return w.err
	}
	w.index++
	w.plain = w.plain[:0]
	return nil
}

// Reader opens an object a Writer sealed, one segment at a time. It holds
// one segment of ciphertext and one of plaintext, and returns no byte of a
// segment before the segment authenticates. A segment is the last when
// nothing follows it, so the Reader reads one byte ahead.
type Reader struct {
	r     io.Reader
	aead  cipher.AEAD
	in    []byte
	ahead bool
	plain []byte
	pos   int
	index uint64
	done  bool
	err   error
}

// NewReader reads the object header from r and returns a Reader that
// opens the object under key.
func NewReader(r io.Reader, key []byte) (*Reader, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	var h [header]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, ErrFormat
		}
		return nil, fmt.Errorf("read sealed object: %w", err)
	}
	if string(h[:len(magic)]) != magic {
		return nil, ErrFormat
	}
	if h[len(magic)] != Version {
		return nil, fmt.Errorf("%w: 0x%02x", ErrVersion, h[len(magic)])
	}
	return &Reader{
		r:     r,
		aead:  aead,
		in:    make([]byte, segmentWire),
		plain: make([]byte, 0, SegmentSize),
	}, nil
}

// Read returns the object's plaintext, failing with ErrAuth at the first
// segment that does not authenticate.
func (r *Reader) Read(p []byte) (int, error) {
	for r.pos == len(r.plain) {
		if r.err != nil {
			return 0, r.err
		}
		if r.done {
			return 0, io.EOF
		}
		if err := r.next(); err != nil {
			r.err = err
			return 0, err
		}
	}
	n := copy(p, r.plain[r.pos:])
	r.pos += n
	return n, nil
}

// next reads and opens one segment. A segment shorter than a full one is
// the last; a full one is the last when no byte follows it.
func (r *Reader) next() error {
	have := 0
	if r.ahead {
		have = 1
	}
	n, err := io.ReadFull(r.r, r.in[have:])
	n += have
	last := false
	var next [1]byte
	switch {
	case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
		last = true
	case err != nil:
		return fmt.Errorf("read sealed object: %w", err)
	default:
		switch _, err := io.ReadFull(r.r, next[:]); {
		case errors.Is(err, io.EOF):
			last = true
		case err != nil:
			return fmt.Errorf("read sealed object: %w", err)
		}
	}
	if n < overhead || r.index >= maxSegments {
		return ErrFormat
	}
	aad := segmentAAD(r.index, last)
	plain, err := r.aead.Open(r.plain[:0], nil, r.in[:n], aad[:])
	if err != nil {
		return ErrAuth
	}
	r.plain, r.pos = plain, 0
	r.index++
	r.done = last
	r.ahead = !last
	r.in[0] = next[0]
	return nil
}
