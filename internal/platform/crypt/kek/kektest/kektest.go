package kektest

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
)

// Key is the data key Contract wraps: the bytes 0 to 31, so a recorded
// request is the same on every run.
func Key() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}

// Contract checks that p wraps Key as data key 3 and unwraps it, that it
// refuses the wrapped key as data key 4 and refuses it with its last byte
// flipped, each with kek.ErrUnwrap, and that its Ref starts with prefix.
// Every request it makes is the same on every run but Wrap's answer, so
// recorded fixtures can serve it.
func Contract(t *testing.T, p kek.Provider, prefix string) {
	t.Helper()
	if ref := p.Ref(); !strings.HasPrefix(ref, prefix) {
		t.Errorf("Ref = %q, want %s…", ref, prefix)
	}
	wrapped, err := p.Wrap(t.Context(), 3, Key())
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	if bytes.Contains(wrapped, Key()) {
		t.Fatal("the wrapped key holds the key")
	}
	got, err := p.Unwrap(t.Context(), 3, wrapped)
	if err != nil || !bytes.Equal(got, Key()) {
		t.Fatalf("Unwrap = %x, %v", got, err)
	}
	if _, err := p.Unwrap(t.Context(), 4, wrapped); !errors.Is(err, kek.ErrUnwrap) {
		t.Errorf("unwrapped as another data key: %v", err)
	}
	flipped := bytes.Clone(wrapped)
	flipped[len(flipped)-1] ^= 1
	if _, err := p.Unwrap(t.Context(), 3, flipped); !errors.Is(err, kek.ErrUnwrap) {
		t.Errorf("unwrapped a changed key: %v", err)
	}
}
