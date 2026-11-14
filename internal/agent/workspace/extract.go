package workspace

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// EntryError is an entry Extract refuses, by name: one whose name leaves
// the workspace, a link whose target leaves it, or a device or a pipe.
type EntryError struct {
	Name string
	msg  string
}

func (e *EntryError) Error() string { return e.msg }

// Extract writes the tar r holds into dir, at most limit bytes of files.
// Every entry is written through an os.Root of dir, so none reaches
// outside it even through a link an earlier entry made; an entry whose
// name leaves dir, a link whose target leaves it from where the link is,
// and a device or pipe are refused outright as an *EntryError, and more
// than limit bytes with ErrTooLarge.
func Extract(r io.Reader, dir string, limit int64) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("the workspace: %w", err)
	}
	defer root.Close()
	tr := tar.NewReader(r)
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err //nolint:wrapcheck // the caller names the archive
		}
		name := filepath.FromSlash(h.Name)
		if !filepath.IsLocal(name) {
			return &EntryError{Name: h.Name, msg: "the entry " + h.Name + " leaves the workspace"}
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = root.MkdirAll(name, 0o750)
		case tar.TypeReg:
			var n int64
			n, err = write(root, name, h.FileInfo().Mode().Perm(), tr, limit-total)
			total += n
		case tar.TypeSymlink:
			target := filepath.FromSlash(h.Linkname)
			if filepath.IsAbs(target) ||
				!filepath.IsLocal(filepath.Join(filepath.Dir(name), target)) {
				return &EntryError{
					Name: h.Name,
					msg:  "the link " + h.Name + " leaves the workspace",
				}
			}
			err = mkdirFor(root, name)
			if err == nil {
				err = root.Symlink(target, name)
			}
		case tar.TypeLink:
			target := filepath.FromSlash(h.Linkname)
			if !filepath.IsLocal(target) {
				return &EntryError{
					Name: h.Name,
					msg:  "the link " + h.Name + " leaves the workspace",
				}
			}
			err = mkdirFor(root, name)
			if err == nil {
				err = root.Link(target, name)
			}
		case tar.TypeXGlobalHeader:
		default:
			return &EntryError{Name: h.Name, msg: "the entry " + h.Name + " is a device or a pipe"}
		}
		switch {
		case errors.Is(err, errOversized):
			return fmt.Errorf("%w: it extracts to over %d bytes", ErrTooLarge, limit)
		case err != nil:
			return err //nolint:wrapcheck // os.Root names the entry
		}
	}
}

// errOversized marks a file write past its limit.
var errOversized = errors.New("over the limit")

// mkdirFor makes the directory name is in.
func mkdirFor(root *os.Root, name string) error {
	return root.MkdirAll(filepath.Dir(name), 0o750) //nolint:wrapcheck // names the path
}

// write writes r to name with perm, at most limit bytes, and answers how
// many it wrote.
func write(root *os.Root, name string, perm fs.FileMode, r io.Reader, limit int64) (int64, error) {
	if err := mkdirFor(root, name); err != nil {
		return 0, err
	}
	out, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm|0o600)
	if err != nil {
		return 0, err //nolint:wrapcheck // names the path
	}
	n, err := io.Copy(out, io.LimitReader(r, limit+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > limit {
		err = errOversized
	}
	return n, err //nolint:wrapcheck // names the path
}
