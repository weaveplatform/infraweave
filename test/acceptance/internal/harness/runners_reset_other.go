//go:build !windows

package harness

import (
	"errors"
	"syscall"
)

func nativeRunnerPullReset(err error) bool {
	return errors.Is(err, syscall.ECONNRESET)
}
