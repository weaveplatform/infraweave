//go:build !unix

package exec

import (
	"errors"
	osexec "os/exec"
)

// errNoGroups marks a system without process groups to signal.
var errNoGroups = errors.New("this system has no process groups to signal")

// ownGroup leaves cmd as it is: a runner runs in a Linux container, and no
// other system passes a stop to a session's commands.
func ownGroup(*osexec.Cmd) {}

// interruptGroup sends nothing, as ownGroup says.
func interruptGroup(int) error { return errNoGroups }
