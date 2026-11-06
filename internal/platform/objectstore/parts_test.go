package objectstore_test

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
)

func TestUnitReadPart(t *testing.T) {
	buf := make([]byte, 8)
	got, err := objectstore.ReadPart(strings.NewReader("abcdef"), buf, 4)
	if err != nil || string(got) != "abcd" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := objectstore.ReadPart(
		strings.NewReader("ab"),
		buf,
		4,
	); !errors.Is(
		err,
		objectstore.ErrSize,
	) {
		t.Errorf("a short source: %v", err)
	}
	broken := errors.New("broken")
	if _, err := objectstore.ReadPart(iotest.ErrReader(broken), buf, 4); !errors.Is(err, broken) ||
		errors.Is(err, objectstore.ErrSize) {
		t.Errorf("a failing source: %v", err)
	}
}

func TestUnitAtEnd(t *testing.T) {
	if err := objectstore.AtEnd(strings.NewReader("")); err != nil {
		t.Errorf("an empty source: %v", err)
	}
	if err := objectstore.AtEnd(iotest.DataErrReader(strings.NewReader(""))); err != nil {
		t.Errorf("a source that ends with EOF: %v", err)
	}
	if err := objectstore.AtEnd(strings.NewReader("x")); !errors.Is(err, objectstore.ErrSize) {
		t.Errorf("a longer source: %v", err)
	}
	if err := objectstore.AtEnd(
		iotest.ErrReader(io.ErrClosedPipe),
	); !errors.Is(
		err,
		io.ErrClosedPipe,
	) {
		t.Errorf("a failing source: %v", err)
	}
}
