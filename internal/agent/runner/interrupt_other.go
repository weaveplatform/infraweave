//go:build !unix

package runner

import "errors"

// Interrupt fails: a runner runs only in a Linux container, and no other
// system has SIGINT to send it.
func Interrupt(int) error { return errors.ErrUnsupported }
