package queue

import (
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitHoldNeedsTransaction(t *testing.T) {
	if err := New(nil).HoldTx(t.Context(), Job{}); !errors.Is(err, ErrNoTransaction) {
		t.Fatal(err)
	}
	if _, err := New(nil).DeadByKeyTx(t.Context(), Kind{}, "", kernel.ULID{}); !errors.Is(err, ErrNoTransaction) {
		t.Fatal(err)
	}
}
