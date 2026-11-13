package source

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

// The bounds of a local preview's archive: what it downloads, the bound of
// a saved workspace, and what it extracts to, so a small archive cannot
// fill the host's disk.
//
// ref: decision 0008 § "Logs and artifacts"
const (
	maxArchive   = 2 << 30
	maxExtracted = 8 << 30
)

var errOversized = errors.New("the archive is over its bound")

// archive extracts the tar.gz url answers into dir. Every entry is written
// through an os.Root of dir, so none reaches outside it even through a
// link an earlier entry made; an entry whose name leaves dir, a link whose
// target leaves it from where the link is, and a device or pipe are
// refused outright. The request's error never names url, whose signature
// lets anyone holding it read the archive.
func (f *Fetcher) archive(ctx context.Context, dir, src string) error {
	if err := os.Mkdir(dir, 0o750); err != nil {
		return fmt.Errorf("the workspace: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDownload, unwrapURL(err))
	}
	resp, err := f.doer.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDownload, unwrapURL(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: answered %s", ErrDownload, resp.Status)
	}
	body := &bounded{r: resp.Body, n: f.maxArchive}
	err = f.extract(body, dir)
	if body.n < 0 {
		return fmt.Errorf("%w: over %d bytes", ErrDownload, f.maxArchive)
	}
	return err
}

// unwrapURL is err without the URL a *url.Error adds.
func unwrapURL(err error) error {
	if ue, ok := errors.AsType[*url.Error](err); ok {
		return ue.Err
	}
	return err
}

// extract writes the tar.gz r holds into dir.
func (f *Fetcher) extract(r io.Reader, dir string) error {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrArchive, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("the workspace: %w", err)
	}
	defer root.Close()
	tr := tar.NewReader(zr)
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %w", ErrArchive, err)
		}
		name := filepath.FromSlash(h.Name)
		if !filepath.IsLocal(name) {
			return fmt.Errorf("%w: the entry %s leaves the workspace", ErrArchive, h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = root.MkdirAll(name, 0o750)
		case tar.TypeReg:
			var n int64
			n, err = write(root, name, h.FileInfo().Mode().Perm(), tr, f.maxExtracted-total)
			total += n
		case tar.TypeSymlink:
			target := filepath.FromSlash(h.Linkname)
			if filepath.IsAbs(target) ||
				!filepath.IsLocal(filepath.Join(filepath.Dir(name), target)) {
				return fmt.Errorf("%w: the link %s leaves the workspace", ErrArchive, h.Name)
			}
			err = mkdirFor(root, name)
			if err == nil {
				err = root.Symlink(target, name)
			}
		case tar.TypeLink:
			target := filepath.FromSlash(h.Linkname)
			if !filepath.IsLocal(target) {
				return fmt.Errorf("%w: the link %s leaves the workspace", ErrArchive, h.Name)
			}
			err = mkdirFor(root, name)
			if err == nil {
				err = root.Link(target, name)
			}
		case tar.TypeXGlobalHeader:
		default:
			return fmt.Errorf("%w: the entry %s is a device or a pipe", ErrArchive, h.Name)
		}
		switch {
		case errors.Is(err, errOversized):
			return fmt.Errorf("%w: it extracts to over %d bytes", ErrArchive, f.maxExtracted)
		case err != nil:
			return fmt.Errorf("%w: %w", ErrArchive, err)
		}
	}
}

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

// bounded reads r, failing once it has given more than n bytes.
type bounded struct {
	r io.Reader
	n int64
}

func (b *bounded) Read(p []byte) (int, error) {
	if int64(len(p)) > b.n+1 {
		p = p[:max(b.n+1, 0)]
	}
	n, err := b.r.Read(p)
	b.n -= int64(n)
	if b.n < 0 {
		return n, errOversized
	}
	return n, err //nolint:wrapcheck // the reader's own
}
