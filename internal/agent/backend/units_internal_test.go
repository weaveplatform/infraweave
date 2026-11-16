package backend

import (
	"errors"
	"testing"
)

// TestUnitUnitName checks that a unit's name that is no UTF-8 is no
// workspace name, which a file system that holds such names lets a
// repository commit.
func TestUnitUnitName(t *testing.T) {
	if err := checkName("a/\xff"); !errors.Is(err, ErrUnitName) ||
		err.Error() != `the unit "a/\xff" cannot name a workspace: its name is not UTF-8` {
		t.Errorf("a name that is no UTF-8: %v", err)
	}
	if err := checkName("a/é"); err != nil {
		t.Errorf("a UTF-8 name: %v", err)
	}
}
