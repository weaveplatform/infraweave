package stacks_test

import (
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/stacks"
)

// TestUnitErrors checks that each refusal names what refused it.
func TestUnitErrors(t *testing.T) {
	for err, want := range map[error]string{
		&stacks.SlugTaken{Slug: "infra"}:                         `stacks: slug "infra" is taken`,
		&stacks.Denied{Action: accessapi.ActionStackUnlockForce}: "you may not perform STACK_UNLOCK_FORCE here",
		&stacks.Locked{By: "alice"}:                              "the stack is locked by alice",
		&stacks.SpaceError{Slug: "team"}:                         `stacks: no space "team"`,
	} {
		if got := err.Error(); got != want {
			t.Errorf("Error() = %q, want %q", got, want)
		}
	}
}
