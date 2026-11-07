//go:build !windows

package private

import (
	"fmt"
	"os"
)

// Restrict makes the file at path readable and writable by its owner
// alone.
func Restrict(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("restrict %s: %w", path, err)
	}
	return nil
}

// Check fails unless the file at path is readable and writable by its
// owner alone.
func Check(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("check %s: %w", path, err)
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("%w: %s has mode %v", ErrShared, path, info.Mode().Perm())
	}
	return nil
}
