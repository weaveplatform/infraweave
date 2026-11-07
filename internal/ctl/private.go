//go:build !windows

package ctl

import (
	"fmt"
	"os"
)

// private makes the file at path readable and writable by its owner
// alone.
func private(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("restrict %s: %w", path, err)
	}
	return nil
}
