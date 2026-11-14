package kernel_test

import (
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// TestUnitTaskCheck accepts a command up to MaxTaskCommand characters,
// counting a multi-byte character once, and refuses a blank command, one
// holding a NUL byte and one a character longer, each at the pointer.
func TestUnitTaskCheck(t *testing.T) {
	for _, ok := range []string{
		"terraform output", strings.Repeat("é", kernel.MaxTaskCommand),
	} {
		if errs := (kernel.Task{Command: ok}).Check("/command"); errs != nil {
			t.Errorf("%.20q refused: %v", ok, errs)
		}
	}
	for _, c := range []struct{ cmd, reason string }{
		{"", kernel.ReasonRequired},
		{" \t\n", kernel.ReasonRequired},
		{"ls\x00-l", kernel.ReasonRule},
		{strings.Repeat("x", kernel.MaxTaskCommand+1), kernel.ReasonMaxLength},
	} {
		errs := (kernel.Task{Command: c.cmd}).Check("/command")
		if len(errs) != 1 || errs[0].Reason != c.reason || errs[0].Pointer != "/command" {
			t.Errorf("%.20q: %v, want one %s at /command", c.cmd, errs, c.reason)
		}
	}
}
