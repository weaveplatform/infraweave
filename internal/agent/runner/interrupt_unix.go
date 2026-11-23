//go:build unix

package runner

import (
	"fmt"
	"syscall"
)

// Interrupt sends process pid SIGINT, which a runner takes as a stop of
// its run.
func Interrupt(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGINT); err != nil {
		return fmt.Errorf("runner: interrupt process %d: %w", pid, err)
	}
	return nil
}
