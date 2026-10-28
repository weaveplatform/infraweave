package specref

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"strings"
)

// maxTarEntry bounds a single extracted file. The largest upstream file
// (resource_stack.go) is under 100 KiB; anything near this limit is not
// source and is refused rather than read into memory.
const maxTarEntry = 8 << 20

var ErrTarEntryTooLarge = errors.New("tarball entry exceeds size limit")

// walkTarball calls fn for every regular file in a GitHub codeload tarball,
// with the leading "<repo>-<ref>/" directory removed from the path. Files are
// read in memory; nothing is extracted to disk, so a hostile archive cannot
// write outside the caller's control.
func walkTarball(tgz []byte, fn func(path string, content []byte) error) error {
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return fmt.Errorf("open tarball: %w", err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tarball: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if hdr.Size > maxTarEntry {
			return fmt.Errorf("%w: %s (%d bytes)", ErrTarEntryTooLarge, hdr.Name, hdr.Size)
		}
		_, rel, ok := strings.Cut(hdr.Name, "/")
		if !ok {
			continue
		}
		content, err := io.ReadAll(io.LimitReader(tr, maxTarEntry))
		if err != nil {
			return fmt.Errorf("read %s: %w", hdr.Name, err)
		}
		if err := fn(rel, content); err != nil {
			return err
		}
	}
}
