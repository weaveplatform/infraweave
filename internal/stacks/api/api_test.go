package api

import "testing"

// TestUnitUndestroyable checks that a refused destroy run reads as its
// detail.
func TestUnitUndestroyable(t *testing.T) {
	var err error = &Undestroyable{Detail: "The stack is disabled.", LockedBy: "alice"}
	if got := err.Error(); got != "stacks: no destroy run: The stack is disabled." {
		t.Errorf("Error = %q", got)
	}
}
