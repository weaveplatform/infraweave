package state

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"
)

func TestUnitContaining(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for name, text := range map[string]string{"clean": "encrypted", "secret": "contains needle"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	keys, n, err := containing(root, []byte("needle"))
	if err != nil || n != 2 || !slices.Equal(keys, []string{filepath.Join(root, "secret")}) {
		t.Fatalf("containing = %v, %d, %v; want secret, 2, nil", keys, n, err)
	}
}

func TestUnitContainingRemovedRoot(t *testing.T) {
	t.Parallel()
	keys, n, err := containing(filepath.Join(t.TempDir(), "removed"), []byte("needle"))
	if err != nil || n != 0 || len(keys) != 0 {
		t.Fatalf("containing = %v, %d, %v; want empty, 0, nil", keys, n, err)
	}
}

// interruptedObjects models a file removed or made unreadable after WalkDir
// has listed its parent directory.
type interruptedObjects struct {
	fs.FS
	err error
}

func (f interruptedObjects) ReadFile(name string) ([]byte, error) {
	if name == "removed" {
		return nil, &fs.PathError{Op: "read", Path: name, Err: f.err}
	}
	return fs.ReadFile(f.FS, name) //nolint:wrapcheck // forwards the filesystem error
}

func TestUnitContainingInterruptedRead(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		err     error
		wantErr bool
	}{
		{name: "deleted after listing", err: fs.ErrNotExist},
		{name: "permission denied", err: fs.ErrPermission, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			objects := interruptedObjects{FS: fstest.MapFS{
				"clean":   &fstest.MapFile{Data: []byte("encrypted")},
				"removed": &fstest.MapFile{Data: []byte("needle")},
				"secret":  &fstest.MapFile{Data: []byte("contains needle")},
			}, err: tc.err}
			keys, n, err := containingFiles(objects, []byte("needle"))
			if tc.wantErr {
				if !errors.Is(err, tc.err) {
					t.Fatalf("containingFiles error = %v, want %v", err, tc.err)
				}
			} else if err != nil || n != 2 || !slices.Equal(keys, []string{"secret"}) {
				t.Fatalf("containingFiles = %v, %d, %v; want [secret], 2, nil", keys, n, err)
			}
		})
	}
}
