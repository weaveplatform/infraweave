//go:build unix

package bootstrap

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestUnitStreamKeepsLinksInsideTheWorkspace(t *testing.T) {
	f := newFixture(t)
	must(t, os.Symlink("main.tf", filepath.Join(f.src, "source", "current.tf")))
	f.pack(t)
	_, err := Receive(bytes.NewReader(f.stream(t, f.manifest())), f.paths, want, "arm64", f.self)
	must(t, err)
	link, err := os.Readlink(filepath.Join(f.paths.Workspace, "source", "current.tf"))
	must(t, err)
	if link != "main.tf" {
		t.Fatalf("link %q", link)
	}
}

func TestUnitPackRefusesEntriesTheReceiverRefuses(t *testing.T) {
	for name, make := range map[string]func(dir string) error{
		"absolute link": func(dir string) error { return os.Symlink("/etc/passwd", filepath.Join(dir, "passwd")) },
		"escaping link": func(dir string) error { return os.Symlink("../../outside", filepath.Join(dir, "source", "out")) },
		"pipe":          func(dir string) error { return syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600) },
	} {
		f := newFixture(t)
		must(t, make(f.src))
		if _, err := Pack(io.Discard, f.src); !errors.Is(err, ErrStream) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestUnitPackFailsAnUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every file")
	}
	f := newFixture(t)
	must(t, os.Chmod(filepath.Join(f.src, "source", "main.tf"), 0))
	if _, err := Pack(io.Discard, f.src); err == nil {
		t.Fatal("packed an unreadable file")
	}
}
