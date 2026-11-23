//go:build !unix

package runner

import (
	"errors"
	"testing"
)

func TestUnitInterruptIsUnsupported(t *testing.T) {
	if err := Interrupt(1); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatal(err)
	}
}
