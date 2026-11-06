package fs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path"
	"runtime"
	"syscall"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
)

// Keys derives the key that signs URLs, as crypt.Keyring does.
type Keys interface {
	DeriveKey(purpose string) ([]byte, uint32, error)
	DerivedKey(purpose string, id uint32) ([]byte, error)
}

// Purpose is the derived key's purpose (decision 0013).
const Purpose = "objectstore-url"

// tmp holds writes until they are renamed into place. No key starts with
// a dot segment (objectstore.CheckKey), so it never meets an object.
const tmp = ".tmp"

// Bucket is a directory of objects, shared by every replica through a
// common volume.
type Bucket struct {
	root *os.Root
	url  string
	keys Keys
	now  func() time.Time
}

// New opens c.Root, creating it, with signed URLs under c.URL signed by
// keys.
func New(c objectstore.FSConfig, keys Keys) (*Bucket, error) {
	if err := os.MkdirAll(c.Root, 0o750); err != nil {
		return nil, fmt.Errorf("fs: %w", err)
	}
	root, err := os.OpenRoot(c.Root)
	if err != nil {
		return nil, fmt.Errorf("fs: %w", err)
	}
	if err := root.MkdirAll(tmp, 0o750); err != nil {
		return nil, errors.Join(fmt.Errorf("fs: %w", err), root.Close())
	}
	return &Bucket{root: root, url: c.URL, keys: keys, now: time.Now}, nil
}

// Close releases the directory.
func (b *Bucket) Close() error { return b.root.Close() } //nolint:wrapcheck // os names the path

// Put writes r to a temporary file, syncs it and renames it into place,
// so a reader sees the whole object or none.
func (b *Bucket) Put(
	ctx context.Context,
	key string,
	r io.Reader,
	size int64,
) (objectstore.Object, error) {
	return b.put(ctx, key, r, size, nil)
}

// put is Put that also refuses bytes whose SHA-256 is not want, before
// they become readable.
func (b *Bucket) put(
	ctx context.Context,
	key string,
	r io.Reader,
	size int64,
	want []byte,
) (objectstore.Object, error) {
	var name [16]byte
	_, _ = rand.Read(name[:])
	temp := tmp + "/" + hex.EncodeToString(name[:])
	f, err := b.root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return objectstore.Object{}, fmt.Errorf("fs: %w", err)
	}
	obj, err := write(ctx, f, r, size, want)
	if err == nil {
		err = b.place(temp, key)
	}
	if err != nil {
		_ = b.root.Remove(temp)
		return objectstore.Object{}, err
	}
	return obj, nil
}

// write copies size bytes of r to f, syncs and closes it.
func write(
	ctx context.Context,
	f *os.File,
	r io.Reader,
	size int64,
	want []byte,
) (objectstore.Object, error) {
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(contextReader{ctx, r}, size+1))
	if err == nil && n != size {
		err = fmt.Errorf("%w: got %d bytes or more, want %d", objectstore.ErrSize, n, size)
	}
	var obj objectstore.Object
	obj.Size = size
	h.Sum(obj.SHA256[:0])
	if err == nil && want != nil && string(want) != string(obj.SHA256[:]) {
		err = ErrChecksum
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return objectstore.Object{}, fmt.Errorf("fs: %w", err)
	}
	return obj, nil
}

// place renames temp to key and syncs key's directory, so the rename
// survives a crash.
func (b *Bucket) place(temp, key string) error {
	dir := path.Dir(key)
	if err := b.root.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("fs: %w", err)
	}
	if err := b.root.Rename(temp, key); err != nil {
		return fmt.Errorf("fs: %w", err)
	}
	// Windows cannot sync a directory, and orders a rename itself.
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := b.root.Open(dir)
	if err != nil {
		return fmt.Errorf("fs: %w", err)
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("fs: %w", err)
	}
	return nil
}

// ErrChecksum reports bytes whose SHA-256 is not the one a signed PUT
// bound.
var ErrChecksum = errors.New("object SHA-256 does not match")

// contextReader stops a copy once ctx ends.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c contextReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err //nolint:wrapcheck // the context's own error
	}
	return c.r.Read(p) //nolint:wrapcheck // the caller's reader
}

// notFound maps a missing file, or a key under an object, which names a
// file as a directory, to ErrNotFound.
func notFound(err error) error {
	if errors.Is(err, iofs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return objectstore.ErrNotFound
	}
	return fmt.Errorf("fs: %w", err)
}

// Get opens the object's file. A directory, the prefix of other keys, is
// no object.
func (b *Bucket) Get(_ context.Context, key string) (io.ReadCloser, error) {
	f, err := b.root.Open(key)
	if err != nil {
		return nil, notFound(err)
	}
	if fi, err := f.Stat(); err != nil || fi.IsDir() {
		_ = f.Close()
		return nil, objectstore.ErrNotFound
	}
	return f, nil
}

// Head returns the file's size. A directory, the prefix of other keys, is
// no object.
func (b *Bucket) Head(_ context.Context, key string) (int64, error) {
	fi, err := b.root.Stat(key)
	if err != nil {
		return 0, notFound(err)
	}
	if fi.IsDir() {
		return 0, objectstore.ErrNotFound
	}
	return fi.Size(), nil
}

// Delete removes the file. A directory's name is no object, so deleting
// it removes nothing.
func (b *Bucket) Delete(ctx context.Context, key string) error {
	if _, err := b.Head(ctx, key); err != nil {
		return nil //nolint:nilerr // a missing key, or a directory, is deleted already
	}
	if err := b.root.Remove(key); err != nil {
		return fmt.Errorf("fs: %w", err)
	}
	return nil
}
