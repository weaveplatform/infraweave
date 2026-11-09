package core_test

import (
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
)

// TestUnitErrors checks that each refusal names what refused it.
func TestUnitErrors(t *testing.T) {
	for err, want := range map[error]string{
		&core.SlugTaken{Slug: "infra"}:                         `stacks: slug "infra" is taken`,
		&core.Denied{Action: accessapi.ActionStackUnlockForce}: "you may not perform STACK_UNLOCK_FORCE here",
		&core.Locked{By: "alice"}:                              "the stack is locked by alice",
		&core.SpaceError{Slug: "team"}:                         `stacks: no space "team"`,
	} {
		if got := err.Error(); got != want {
			t.Errorf("Error() = %q, want %q", got, want)
		}
	}
}
