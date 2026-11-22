//go:build windows

package harness

import (
	"net"
	"os"
	"syscall"
	"testing"
)

func TestUnitRunnerPullUsesWinsockReset(t *testing.T) {
	err := &net.OpError{Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "wsarecv", Err: syscall.WSAECONNRESET}}
	if !nativeRunnerPullReset(err) {
		t.Fatal("native Winsock reset was not recognized", err)
	}
	if nativeRunnerPullReset(syscall.ECONNRESET) {
		t.Fatal("the compatibility application error was accepted as a socket reset")
	}
}
