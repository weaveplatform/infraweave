//go:build unix

package workspace_test

import (
	"archive/tar"
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/workspace"
)

// tree writes files, by slash path, under a new directory and answers it.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// contents are dir's entries by slash path: a file's content, a link's
// target behind "-> ", and "/" for a directory.
func contents(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			out[rel] = "-> " + target
			return err
		case d.IsDir():
			out[rel] = "/"
			return nil
		}
		b, err := os.ReadFile(p)
		out[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestUnitWorkspaceRoundTrip saves a workspace and restores it with the
// pool's key: files, their modes and the links that stay in it come back,
// a .terraform directory's providers are left out silently, and a link
// that leaves the workspace and a pipe are left out and named.
func TestUnitWorkspaceRoundTrip(t *testing.T) {
	dir := tree(t, map[string]string{
		"main.tf":                             "terraform {}",
		"infraweave.plan":                     "binary-plan",
		"infra/scripts/run.sh":                "#!/bin/sh",
		".terraform/modules/vpc/main.tf":      "module",
		".terraform/terraform.tfstate":        "{}",
		".terraform/providers/registry/x/bin": "provider",
		"nested/.terraform/providers/y":       "provider",
	})
	if err := os.Chmod(filepath.Join(dir, "infra/scripts/run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../main.tf", filepath.Join(dir, "infra", "main.tf")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../etc/passwd", filepath.Join(dir, "out")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	pool := poolKey(t)
	var b bytes.Buffer
	left, err := workspace.Write(&b, dir, workspace.Options{
		Pool: pool.PublicKey(), Run: run, StateVersion: 5,
		Forbidden: [][]byte{[]byte("run-token-value"), nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(left, []string{"out", "pipe"}) {
		t.Errorf("left out %q", left)
	}
	if bytes.Contains(b.Bytes(), []byte("binary-plan")) ||
		bytes.Contains(b.Bytes(), []byte("terraform {}")) {
		t.Error("the saved workspace holds plaintext")
	}
	out := filepath.Join(t.TempDir(), "restored")
	if err := workspace.Read(bytes.NewReader(b.Bytes()), out, pool, run, 1<<20); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"main.tf": "terraform {}", "infraweave.plan": "binary-plan",
		"infra": "/", "infra/scripts": "/", "infra/scripts/run.sh": "#!/bin/sh",
		"infra/main.tf": "-> ../main.tf",
		".terraform":    "/", ".terraform/modules": "/", ".terraform/modules/vpc": "/",
		".terraform/modules/vpc/main.tf": "module", ".terraform/terraform.tfstate": "{}",
		"nested": "/", "nested/.terraform": "/",
	}
	got := contents(t, out)
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("restored %s, which was left out", k)
		}
	}
	if info, err := os.Stat(filepath.Join(out, "infra/scripts/run.sh")); err != nil ||
		info.Mode().Perm()&0o100 == 0 {
		t.Errorf("run.sh lost its mode: %v, %v", info, err)
	}
}

// TestUnitWorkspaceRefused checks that a workspace opens only with its
// pool's key for its run, and that one altered, truncated or extended
// anywhere, or that is no saved workspace, is refused.
func TestUnitWorkspaceRefused(t *testing.T) {
	dir := tree(t, map[string]string{"main.tf": strings.Repeat("terraform {}\n", 20000)})
	pool := poolKey(t)
	var b bytes.Buffer
	if _, err := workspace.Write(&b, dir, workspace.Options{
		Pool: pool.PublicKey(), Run: run, StateVersion: 5,
	}); err != nil {
		t.Fatal(err)
	}
	saved := b.Bytes()
	flip := func(i int) []byte {
		c := bytes.Clone(saved)
		c[i] ^= 1
		return c
	}
	for name, c := range map[string]struct {
		body []byte
		pool *ecdh.PrivateKey
		run  string
	}{
		"another pool's key":        {saved, poolKey(t), run},
		"another run":               {saved, pool, "01HZX4Y9Q2W8E5R7T3V6K1M0PB"},
		"no saved workspace":        {[]byte("PK\x03\x04 a zip"), pool, run},
		"another version":           {append([]byte("IWWS\x02"), saved[5:]...), pool, run},
		"a short header":            {saved[:6], pool, run},
		"another state version":     {flip(12), pool, run},
		"an envelope over bound":    {append(append(bytes.Clone(saved[:13]), 0xff, 0xff), saved[15:]...), pool, run},
		"a short envelope":          {saved[:40], pool, run},
		"an altered envelope":       {flip(60), pool, run},
		"an altered stream":         {flip(len(saved) - 100), pool, run},
		"a truncated stream":        {saved[:len(saved)-1], pool, run},
		"a stream cut at a segment": {saved[:len(saved)/2], pool, run},
		"an extended stream":        {append(bytes.Clone(saved), 0), pool, run},
	} {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "restored")
			err := workspace.Read(bytes.NewReader(c.body), out, c.pool, c.run, 1<<30)
			if !errors.Is(err, workspace.ErrRefused) {
				t.Errorf("Read: %v", err)
			}
		})
	}
	if err := workspace.Read(bytes.NewReader(saved), dir, pool, run, 1<<30); !errors.Is(err,
		workspace.ErrRefused) || !errors.Is(err, fs.ErrExist) {
		t.Errorf("a directory that exists: %v", err)
	}
}

// TestUnitWorkspaceEntries checks that a saved workspace whose archive
// holds a traversing entry, a link out or a device, or extracts past its
// bound, is refused, naming the entry.
func TestUnitWorkspaceEntries(t *testing.T) {
	pool := poolKey(t)
	o := workspace.Options{Pool: pool.PublicKey(), Run: run, StateVersion: 5}
	for name, c := range map[string]struct {
		archive []byte
		limit   int64
		want    string
	}{
		"a traversing entry": {
			entries(t, ent{tar.Header{Name: "../evil", Typeflag: tar.TypeReg, Mode: 0o644}, "x"}), 1 << 20,
			"the entry ../evil leaves the workspace",
		},
		"a link out": {
			entries(t, ent{tar.Header{Name: "l", Typeflag: tar.TypeSymlink, Linkname: "/etc"}, ""}), 1 << 20,
			"the link l leaves the workspace",
		},
		"a device": {
			entries(t, ent{tar.Header{Name: "tty", Typeflag: tar.TypeChar}, ""}), 1 << 20,
			"the entry tty is a device or a pipe",
		},
		"past its bound": {
			entries(t, ent{tar.Header{Name: "big", Typeflag: tar.TypeReg, Mode: 0o644}, "123456789"}), 8,
			"it extracts to over 8 bytes",
		},
		"no tar": {[]byte("not a tar archive"), 1 << 20, "EOF"},
	} {
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			if err := workspace.SealRaw(&b, o, c.archive); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "restored")
			err := workspace.Read(&b, out, pool, run, c.limit)
			if !errors.Is(err, workspace.ErrRefused) || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Read: %v", err)
			}
		})
	}
}

// TestUnitWorkspaceForbidden checks that a file, a link or a name that
// holds a forbidden value fails the save naming its path and not the
// value, including a value split across two of a file's reads.
func TestUnitWorkspaceForbidden(t *testing.T) {
	token := "run-" + strings.Repeat("t", 40)
	pool := poolKey(t)
	split := strings.Repeat("x", 32<<10-10) + token
	for name, c := range map[string]struct {
		files map[string]string
		link  [2]string
		want  string
	}{
		"a file":        {files: map[string]string{"a/leak.txt": "token=" + token}, want: "the file a/leak.txt"},
		"a split value": {files: map[string]string{"big": split}, want: "the file big"},
		"a name": {
			files: map[string]string{"a/x" + token: "x"},
			want:  "the name of an entry of the directory a",
		},
		"a link": {link: [2]string{token, "l"}, want: "the link l"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := tree(t, c.files)
			if c.link[1] != "" {
				if err := os.Symlink(c.link[0], filepath.Join(dir, c.link[1])); err != nil {
					t.Fatal(err)
				}
			}
			var b bytes.Buffer
			_, err := workspace.Write(&b, dir, workspace.Options{
				Pool: pool.PublicKey(), Run: run, StateVersion: 5,
				Forbidden: [][]byte{[]byte("unused-value"), []byte(token)},
			})
			if !errors.Is(err, workspace.ErrSecret) || !strings.Contains(err.Error(), c.want) ||
				strings.Contains(err.Error(), "tttttttttt") {
				t.Errorf("Write: %v", err)
			}
		})
	}
}

// TestUnitWorkspaceBounds checks that a workspace sealed past its bound,
// one that cannot be read, and a key that is not P-256 fail the save.
func TestUnitWorkspaceBounds(t *testing.T) {
	pool := poolKey(t)
	o := workspace.Options{Pool: pool.PublicKey(), Run: run, StateVersion: 5}
	dir := tree(t, map[string]string{"main.tf": "terraform {}"})

	restore := workspace.SetMaxSize(64)
	_, err := workspace.Write(&bytes.Buffer{}, dir, o)
	restore()
	if !errors.Is(err, workspace.ErrTooLarge) {
		t.Errorf("past the bound: %v", err)
	}
	noise := make([]byte, 4096)
	if _, err := rand.Read(noise); err != nil {
		t.Fatal(err)
	}
	restore = workspace.SetMaxSize(300)
	_, err = workspace.Write(&bytes.Buffer{}, tree(t, map[string]string{"big": string(noise)}), o)
	restore()
	if !errors.Is(err, workspace.ErrTooLarge) {
		t.Errorf("a stream past the bound: %v", err)
	}
	if _, err := workspace.Write(&bytes.Buffer{}, filepath.Join(dir, "gone"), o); err == nil {
		t.Error("a workspace that does not exist was saved")
	}
	x, err := ecdh.P384().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Write(&bytes.Buffer{}, dir, workspace.Options{
		Pool: x.PublicKey(), Run: run, StateVersion: 5,
	}); err == nil {
		t.Error("a workspace was sealed to a P-384 key")
	}
	if _, err := workspace.Write(failing{}, dir, o); err == nil {
		t.Error("a failing writer took the workspace")
	}
	if os.Getuid() == 0 {
		return
	}
	for _, name := range []string{"secret", "closed"} {
		locked := tree(t, map[string]string{"secret": "x", "closed/f": "y"})
		if err := os.Chmod(filepath.Join(locked, name), 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(locked, name), 0o755) })
		if _, err := workspace.Write(&bytes.Buffer{}, locked, o); err == nil {
			t.Errorf("an unreadable %s was saved", name)
		}
	}
}

// failing refuses every write.
type failing struct{}

func (failing) Write(
	[]byte,
) (int, error) {
	return 0, errors.New("disk full")
} //nolint:err113 // a test double
