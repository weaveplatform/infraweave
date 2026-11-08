package assembly

import (
	"testing"

	"github.com/weaveplatform/infraweave/internal/stacks/internal/pg"
)

// TestUnitDomain checks that the domain's stacksapi.Stacks and its space
// guard are one service, and that its stream is the stacks stream.
func TestUnitDomain(t *testing.T) {
	d := New(Deps{})
	if d.Stacks() == nil || d.SpaceGuard() == nil || any(d.Stacks()) != any(d.SpaceGuard()) {
		t.Errorf("Stacks = %v, SpaceGuard = %v", d.Stacks(), d.SpaceGuard())
	}
	if got := Stream().Name; got != pg.StreamName {
		t.Errorf("Stream().Name = %q, want %q", got, pg.StreamName)
	}
	if d.API() != (StacksAPI{}) {
		t.Error("API serves an operation")
	}
}
