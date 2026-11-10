package assembly

import (
	"reflect"
	"testing"

	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
)

// TestUnitDomain checks that the domain answers the stack view's status
// and the stacks guard from one service, declares its stream and held
// columns, and that a nil domain's partial is the zero one.
func TestUnitDomain(t *testing.T) {
	d := New(Deps{})
	if d.StackStatus() == nil || d.StackGuard() == nil ||
		any(d.StackStatus()) != any(d.StackGuard()) {
		t.Error("the status and the guard are not the domain's service")
	}
	if Stream().Name != pg.StreamName || !reflect.DeepEqual(Held(), pg.Held()) {
		t.Errorf("stream %q, held %v", Stream().Name, Held())
	}
	var none *Domain
	if !reflect.ValueOf(none.API()).IsZero() {
		t.Error("a nil domain's partial is not the zero one")
	}
}
