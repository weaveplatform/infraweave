package objectstore

import (
	"errors"
	"fmt"
	"io"
)

// ReadPart reads exactly n bytes of r into buf, whose capacity holds n,
// for a driver that sends a write in parts. A source that ends early
// fails with ErrSize.
func ReadPart(r io.Reader, buf []byte, n int64) ([]byte, error) {
	buf = buf[:n]
	if _, err := io.ReadFull(r, buf); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, fmt.Errorf("%w: the source ended early", ErrSize)
		}
		return nil, fmt.Errorf("objectstore: read: %w", err)
	}
	return buf, nil
}

// AtEnd fails with ErrSize when r holds more than the declared size,
// which a driver checks before it makes the object.
func AtEnd(r io.Reader) error {
	var one [1]byte
	n, err := r.Read(one[:])
	switch {
	case n > 0:
		return fmt.Errorf("%w: the source holds more", ErrSize)
	case err == nil || errors.Is(err, io.EOF):
		return nil
	default:
		return fmt.Errorf("objectstore: read: %w", err)
	}
}
