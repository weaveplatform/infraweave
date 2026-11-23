package bootstrap

import (
	"archive/tar"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/zstd"
)

// Pack writes the workspace directory dir to w as the stream's workspace
// payload and answers its descriptor. Directories, regular files and
// links that stay inside dir are archived; any other entry, and a link
// that leaves dir, fails the pack, since the receiver refuses them and a
// run must not miss a file it would see on Docker. Modes are normalized
// to 0755 for directories and executables and 0644 for other files, and
// owners are dropped: the run's user owns everything it receives. A
// workspace whose tar exceeds MaxDecoded or whose compressed payload
// exceeds MaxWorkspace fails with ErrTooLarge.
func Pack(w io.Writer, dir string) (Workspace, error) {
	d := newDigest()
	out := &capped{w: io.MultiWriter(w, d), left: workspaceLimit}
	zw, err := zstd.NewWriter(out, zstd.WithEncoderConcurrency(1))
	if err != nil {
		return Workspace{}, fmt.Errorf("bootstrap: pack the workspace: %w", err)
	}
	tw := tar.NewWriter(&capped{w: zw, left: decodedLimit})
	if err := archive(tw, dir); err != nil {
		zw.Close()
		return Workspace{}, err
	}
	if err := tw.Close(); err != nil {
		zw.Close()
		return Workspace{}, packed(err)
	}
	if err := zw.Close(); err != nil {
		return Workspace{}, packed(err)
	}
	return Workspace{Codec: Codec, Size: workspaceLimit - out.left, SHA256: d.hex()}, nil
}

func packed(err error) error {
	return fmt.Errorf("bootstrap: pack the workspace: %w", err)
}

// archive writes dir's entries to tw through an os.Root, so no link
// leads the walk outside dir.
func archive(tw *tar.Writer, dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return packed(err)
	}
	defer root.Close()
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == "." {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err //nolint:wrapcheck // names the path
		}
		h := &tar.Header{Name: p, ModTime: info.ModTime(), Format: tar.FormatPAX}
		switch {
		case d.IsDir():
			h.Typeflag, h.Name, h.Mode = tar.TypeDir, p+"/", 0o755
		case info.Mode().IsRegular():
			h.Typeflag, h.Size, h.Mode = tar.TypeReg, info.Size(), 0o644
			if info.Mode()&0o111 != 0 {
				h.Mode = 0o755
			}
		case d.Type()&fs.ModeSymlink != 0:
			link, err := root.Readlink(filepath.FromSlash(p))
			if err != nil {
				return err //nolint:wrapcheck // names the path
			}
			if filepath.IsAbs(link) || !filepath.IsLocal(filepath.Join(filepath.Dir(filepath.FromSlash(p)), link)) {
				return fmt.Errorf("%w: the link %s leaves the workspace", ErrStream, p)
			}
			h.Typeflag, h.Linkname, h.Mode = tar.TypeSymlink, filepath.ToSlash(link), 0o777
		default:
			return fmt.Errorf("%w: %s is a device, pipe or socket", ErrStream, p)
		}
		if err := tw.WriteHeader(h); err != nil {
			return err //nolint:wrapcheck // Pack names the step
		}
		if h.Typeflag != tar.TypeReg {
			return nil
		}
		f, err := root.Open(filepath.FromSlash(p))
		if err != nil {
			return err //nolint:wrapcheck // names the path
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err //nolint:wrapcheck // Pack names the step
	})
	if err != nil {
		return packed(err)
	}
	return nil
}

// capped passes writes to w until left bytes have passed, then fails
// with ErrTooLarge.
type capped struct {
	w    io.Writer
	left int64
}

func (c *capped) Write(p []byte) (int, error) {
	if int64(len(p)) > c.left {
		return 0, ErrTooLarge
	}
	n, err := c.w.Write(p)
	c.left -= int64(n)
	return n, err //nolint:wrapcheck // the writer's own error
}
