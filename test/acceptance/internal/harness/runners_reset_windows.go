//go:build windows

package harness

import (
	"errors"
	"syscall"
)

func nativeRunnerPullReset(err error) bool {
	// Socket reads use Winsock errors; ECONNRESET is a different application error.
	return errors.Is(err, syscall.WSAECONNRESET)
}
