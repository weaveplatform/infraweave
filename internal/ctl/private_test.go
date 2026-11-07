//go:build !windows

package ctl

import (
	"fmt"
	"os"
)

// isPrivate fails unless the file at path is readable and writable by its
// owner alone.
func isPrivate(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("%s has mode %v, want 0600", path, info.Mode().Perm())
	}
	return nil
}
