//go:build unix

package source

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/workspace"
)

// TestUnitRestore checks that a saved workspace is restored with the
// pool's key into the workspace, handed to the run's user, and that one
// sealed to another key, a refused download, a URL that is no URL, a
// project root it lacks and a workspace past its bound each fail with
// the step named and never the URL's signature.
func TestUnitRestore(t *testing.T) {
	pool, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "infra"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "infra", "main.tf"), []byte("terraform {}"),
		0o644); err != nil {
		t.Fatal(err)
	}
	var saved bytes.Buffer
	if _, err := workspace.Write(&saved, src, workspace.Options{
		Pool: pool.PublicKey(), Run: "run-1", StateVersion: 5,
	}); err != nil {
		t.Fatal(err)
	}
	u := newUploads(t)
	u.serve(saved.Bytes(), 0)
	dir := filepath.Join(t.TempDir(), "ws")
	if err := New(http.DefaultClient, nil).Restore(t.Context(), dir, u.url(), pool, "run-1",
		archiveSpec("", ptr("infra"))); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "infra", "main.tf")); err != nil ||
		string(b) != "terraform {}" {
		t.Fatalf("main.tf = %q, %v", b, err)
	}
	if m := mode(t, dir); m.Perm()&0o070 != 0o070 {
		t.Errorf("the workspace is not handed to the run's user: %v", m)
	}

	other, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	host := strings.TrimPrefix(u.srv.URL, "http://")
	for name, c := range map[string]struct {
		pool   *ecdh.PrivateKey
		url    string
		status int
		root   string
		limit  int64
		want   error
		prefix string
	}{
		"another pool's key": {
			pool: other, want: workspace.ErrRefused,
			prefix: "restore the saved workspace from " + host + ": ",
		},
		"a refused download": {pool: pool, status: http.StatusForbidden, want: ErrDownload},
		"no URL":             {pool: pool, url: "file:///etc/passwd", want: ErrURL},
		"no project root":    {pool: pool, root: "missing", want: ErrProjectRoot},
		"past its bound":     {pool: pool, limit: 4, want: workspace.ErrTooLarge},
	} {
		t.Run(name, func(t *testing.T) {
			u.serve(saved.Bytes(), c.status)
			url := u.url()
			if c.url != "" {
				url = c.url
			}
			f := New(http.DefaultClient, nil)
			if c.limit != 0 {
				f.maxExtracted = c.limit
			}
			var root *string
			if c.root != "" {
				root = &c.root
			}
			err := f.Restore(t.Context(), filepath.Join(t.TempDir(), "ws"), url, c.pool, "run-1",
				archiveSpec("", root))
			if !errors.Is(err, c.want) || !strings.HasPrefix(err.Error(), c.prefix) ||
				strings.Contains(err.Error(), signature) {
				t.Errorf("Restore: %v", err)
			}
		})
	}
}
