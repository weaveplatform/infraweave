//go:build unix

package source

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestUnitReadyCheckout validates the default root too, and confines a
// named root to the checkout rather than the whole workspace.
func TestUnitReadyCheckout(t *testing.T) {
	for name, project := range map[string]*string{
		"unset": nil, "empty": new(""), "dot": new("."), "subdirectory": new("infra"),
	} {
		t.Run(name, func(t *testing.T) {
			ws := t.TempDir()
			if err := os.MkdirAll(filepath.Join(ws, Checkout, "infra"), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := ready(ws, archiveSpec("", project)); err != nil {
				t.Fatalf("valid checkout: %v", err)
			}
		})
	}
	ws := t.TempDir()
	if err := ready(ws, archiveSpec("", nil)); !errors.Is(err, ErrProjectRoot) {
		t.Fatalf("missing checkout: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(ws, Checkout), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(ws, "outside"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside", filepath.Join(ws, Checkout, "link")); err != nil {
		t.Fatal(err)
	}
	if err := ready(ws, archiveSpec("", new("link"))); !errors.Is(err, ErrProjectRoot) {
		t.Errorf("root leaves the checkout: %v", err)
	}
}
