package workspace_test

import (
	"archive/tar"
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/workspace"
)

const run = "01HZX4Y9Q2W8E5R7T3V6K1M0PA"

// poolKey is a fresh pool key.
func poolKey(t *testing.T) *ecdh.PrivateKey {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// ent is a tar entry with its body.
type ent struct {
	h    tar.Header
	body string
}

// entries are a tar of es.
func entries(t *testing.T, es ...ent) []byte {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for _, e := range es {
		e.h.Size = int64(len(e.body))
		if err := tw.WriteHeader(&e.h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestUnitExtract checks that Extract writes a hard link inside the
// workspace and refuses one out of it, and fails an entry it cannot write
// or a workspace that does not exist.
func TestUnitExtract(t *testing.T) {
	dir := t.TempDir()
	if err := workspace.Extract(bytes.NewReader(entries(
		t,
		ent{tar.Header{Name: "a/f", Typeflag: tar.TypeReg, Mode: 0o644}, "body"},
		ent{tar.Header{Name: "b/g", Typeflag: tar.TypeLink, Linkname: "a/f"}, ""},
		ent{
			tar.Header{Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"x": "y"}},
			"",
		},
	)), dir, 1<<20); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "b", "g")); err != nil || string(b) != "body" {
		t.Errorf("b/g = %q, %v", b, err)
	}
	var ee *workspace.EntryError
	err := workspace.Extract(bytes.NewReader(entries(t,
		ent{tar.Header{Name: "h", Typeflag: tar.TypeLink, Linkname: "../etc/passwd"}, ""},
	)), t.TempDir(), 1<<20)
	if !errors.As(err, &ee) || ee.Name != "h" {
		t.Errorf("a hard link out: %v", err)
	}
	for name, archive := range map[string][]byte{
		"an entry under a file": entries(t,
			ent{tar.Header{Name: "f", Typeflag: tar.TypeReg, Mode: 0o644}, "x"},
			ent{tar.Header{Name: "f/g", Typeflag: tar.TypeReg, Mode: 0o644}, "y"},
		),
		"a file over a directory": entries(t,
			ent{tar.Header{Name: "d/", Typeflag: tar.TypeDir, Mode: 0o755}, ""},
			ent{tar.Header{Name: "d", Typeflag: tar.TypeReg, Mode: 0o644}, "y"},
		),
		"a link under a file": entries(t,
			ent{tar.Header{Name: "f", Typeflag: tar.TypeReg, Mode: 0o644}, "x"},
			ent{tar.Header{Name: "f/l", Typeflag: tar.TypeSymlink, Linkname: "../f"}, ""},
		),
		"a hard link under a file": entries(t,
			ent{tar.Header{Name: "f", Typeflag: tar.TypeReg, Mode: 0o644}, "x"},
			ent{tar.Header{Name: "f/l", Typeflag: tar.TypeLink, Linkname: "f"}, ""},
		),
	} {
		t.Run(name, func(t *testing.T) {
			err := workspace.Extract(bytes.NewReader(archive), t.TempDir(), 1<<20)
			if err == nil || errors.As(err, &ee) {
				t.Errorf("Extract: %v", err)
			}
		})
	}
	if err := workspace.Extract(strings.NewReader(""), filepath.Join(dir, "gone"), 1); err == nil {
		t.Error("a workspace that does not exist took an archive")
	}
}

// TestUnitWorkspaceAfterArchive checks that a saved workspace whose
// stream fails after its archive ends is refused.
func TestUnitWorkspaceAfterArchive(t *testing.T) {
	pool := poolKey(t)
	o := workspace.Options{Pool: pool.PublicKey(), Run: run, StateVersion: 5}
	noise := make([]byte, 256<<10)
	if _, err := rand.Read(noise); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := workspace.SealRaw(&b, o, append(entries(t,
		ent{tar.Header{Name: "f", Typeflag: tar.TypeReg, Mode: 0o644}, "x"},
	), noise...)); err != nil {
		t.Fatal(err)
	}
	cut := b.Bytes()[:b.Len()-10]
	err := workspace.Read(bytes.NewReader(cut), filepath.Join(t.TempDir(), "w"), pool, run, 1<<20)
	if !errors.Is(err, workspace.ErrRefused) || !strings.Contains(err.Error(), "its content") {
		t.Errorf("Read: %v", err)
	}
}
