package assembly

import (
	"reflect"
	"testing"

	"github.com/weaveplatform/infraweave/internal/state/internal/pg"
)

// TestUnitDomain checks that the domain constructs its core and declares
// its stream and held columns.
func TestUnitDomain(t *testing.T) {
	if d := New(Deps{}); d == nil || d.state == nil {
		t.Error("the domain has no core")
	}
	if Stream().Name != pg.StreamName || !reflect.DeepEqual(Held(), pg.Held()) {
		t.Errorf("stream %q, held %v", Stream().Name, Held())
	}
}
