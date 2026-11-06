package objectstoretest

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
)

// cancelAfter gives n bytes of r, then cancels and gives the rest.
type cancelAfter struct {
	r      io.Reader
	n      int64
	cancel context.CancelFunc
}

func (c *cancelAfter) Read(p []byte) (int, error) {
	if c.n <= 0 {
		c.cancel()
	} else {
		p = p[:min(int64(len(p)), c.n)]
	}
	n, err := c.r.Read(p)
	c.n -= int64(n)
	return n, err //nolint:wrapcheck // the test's own reader
}

// Canceled checks that a bucket over the network fails every call once
// its context has ended, with the context's error rather than
// ErrNotFound, and that a write canceled between its parts leaves
// nothing. The fs driver, whose calls never wait on a service, is not
// held to it.
func Canceled(t *testing.T, b objectstore.Bucket, partSize int64) {
	t.Helper()
	var run [8]byte
	copy(run[:], random(8))
	prefix := "canceled/" + hex.EncodeToString(run[:]) + "/"

	ended, cancel := context.WithCancel(t.Context())
	cancel()
	//nolint:wrapcheck // each call's own error, which the loop compares
	for name, call := range map[string]func() error{
		"Put": func() error { _, err := b.Put(ended, prefix+"a", bytes.NewReader(random(10)), 10); return err },
		"Put in parts": func() error {
			_, err := b.Put(ended, prefix+"a", bytes.NewReader(random(partSize+1)), partSize+1)
			return err
		},
		"Get":    func() error { _, err := b.Get(ended, prefix+"a"); return err },
		"Head":   func() error { _, err := b.Head(ended, prefix+"a"); return err },
		"Delete": func() error { return b.Delete(ended, prefix+"a") },
	} {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s under an ended context: %v", name, err)
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	size := 2*partSize + 1
	r := &cancelAfter{r: bytes.NewReader(random(size)), n: partSize + 1, cancel: cancel}
	if _, err := b.Put(ctx, prefix+"b", r, size); err == nil {
		t.Fatal("a write canceled between its parts succeeded")
	}
	if _, err := b.Head(t.Context(), prefix+"b"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Errorf("a write canceled between its parts left %v", err)
	}
}
