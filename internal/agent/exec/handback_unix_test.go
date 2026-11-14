//go:build unix

package exec

import (
	"os"
	"path/filepath"
	"testing"
)

// TestUnitHandBack checks that a workspace the runner owns is left as it
// is, and that openUp sets every directory below the workspace that the
// UID owns to 0777, leaving the workspace itself, its files and a
// directory a link leads to outside it.
func TestUnitHandBack(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	for _, d := range []string{".terraform/providers", "modules"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(dir, ".terraform", "terraform.tfstate")
	if err := os.WriteFile(file, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	perm := func(p string) os.FileMode {
		t.Helper()
		fi, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Mode().Perm()
	}
	top := perm(dir)

	if err := handBack(dir); err != nil {
		t.Fatal(err)
	}
	if got := perm(filepath.Join(dir, "modules")); got != 0o755 {
		t.Errorf("handBack changed the runner's own workspace: modules is %v", got)
	}

	if err := openUp(dir, os.Getuid()); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{
		dir: top, filepath.Join(dir, ".terraform"): 0o777,
		filepath.Join(dir, ".terraform", "providers"): 0o777, filepath.Join(dir, "modules"): 0o777,
		file: 0o644, outside: 0o700,
	} {
		if got := perm(p); got != want {
			t.Errorf("%s is %v, want %v", p, got, want)
		}
	}
	if err := openUp(dir, os.Getuid()+1); err != nil {
		t.Errorf("openUp for another UID: %v", err)
	}
	if err := openUp(filepath.Join(dir, "gone"), os.Getuid()); err == nil {
		t.Error("openUp of no directory answered no error")
	}
}
