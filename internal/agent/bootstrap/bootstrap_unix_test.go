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

func TestUnitReceiveCreatesTheRunsScopedDirectories(t *testing.T) {
	f := newFixture(t)
	stream := f.stream(t, f.manifest())
	f.paths.Scoped = filepath.Join(t.TempDir(), "scoped")
	// No scoped-input volume: the directory is absent.
	if _, err := Receive(bytes.NewReader(stream), f.paths, want, "arm64", f.self); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"0", "1"} {
		must(t, os.MkdirAll(filepath.Join(f.paths.Scoped, d), 0o777))
	}
	must(t, os.WriteFile(filepath.Join(f.paths.Scoped, "stray"), nil, 0o600))
	must(t, os.Mkdir(filepath.Join(f.paths.Scoped, "1", ScopedFiles), 0o700))
	if _, err := Receive(bytes.NewReader(stream), f.paths, want, "arm64", f.self); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"0", "1"} {
		fi, err := os.Stat(filepath.Join(f.paths.Scoped, d, ScopedFiles))
		if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
			t.Fatalf("volume %s: %v, %v", d, fi, err)
		}
	}
	must(t, os.Remove(filepath.Join(f.paths.Scoped, "0", ScopedFiles)))
	must(t, os.Chmod(filepath.Join(f.paths.Scoped, "0"), 0o500))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(f.paths.Scoped, "0"), 0o700) })
	if _, err := Receive(bytes.NewReader(stream), f.paths, want, "arm64", f.self); err == nil {
		t.Fatal("an unwritable volume was accepted")
	}
	broken := f.paths
	broken.Scoped = filepath.Join(f.paths.Scoped, "stray")
	if _, err := Receive(bytes.NewReader(stream), broken, want, "arm64", f.self); err == nil {
		t.Fatal("a scoped root that is a file was accepted")
	}
}
