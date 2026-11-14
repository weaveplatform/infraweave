//go:build unix

package exec

import (
	osexec "os/exec"
	"syscall"
)

// ownGroup starts cmd in a process group of its own, which the commands of
// every step it runs join, so a stop's SIGINT reaches them and not the
// runner.
func ownGroup(cmd *osexec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// interruptGroup sends SIGINT to the process group pid leads.
func interruptGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGINT) //nolint:wrapcheck // the caller names the signal
}
