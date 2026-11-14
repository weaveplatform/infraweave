package workspace

import (
	"archive/tar"
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/klauspost/compress/zstd"

	"github.com/weaveplatform/infraweave/internal/platform/seal"
)

// The saved workspace's header: magic, version, the state version the
// content key's envelope is bound to, and the envelope's length.
const (
	magic   = "IWWS"
	version = 0x01
	header  = len(magic) + 1 + 8 + 2
)

// MaxSize is the largest saved workspace, the worker API's bound on a
// workspace upload.
//
// ref: decision 0008 § "Logs and artifacts"
const MaxSize = 2 << 30

// maxSize is the bound Write holds a workspace to, MaxSize but in tests.
var maxSize int64 = MaxSize

// maxEnvelope bounds the envelope Read takes: a sealed content key is
// 126 bytes.
const maxEnvelope = 1 << 10

// maxWindow bounds the zstd window Read decodes with, which Write's
// default level stays well under, so a crafted frame cannot ask for
// gigabytes of memory.
const maxWindow = 64 << 20

// Errors of a workspace that cannot be saved or restored.
var (
	ErrSecret   = errors.New("a file of the workspace holds one of the run's credentials")
	ErrTooLarge = errors.New("the workspace is over its bound")
	ErrRefused  = errors.New("the saved workspace is refused")
)

// Options are what Write seals a workspace to: the pool's public key,
// the run and the state version the content key's envelope is bound to,
// and the values no file, link or name of the workspace may hold.
type Options struct {
	Pool         *ecdh.PublicKey
	Run          string
	StateVersion int64
	Forbidden    [][]byte
}

// Write archives dir, the run's workspace, and writes it to w sealed:
//
//	"IWWS" | 0x01 | state version (uint64) | envelope length (uint16) | envelope | object stream
//
// The object stream, seal's segmented AES-256-GCM, holds the tar of dir
// compressed with zstd, under a fresh content key that the envelope seals
// to o.Pool for o.Run at o.StateVersion, so only the pool's private key
// opens it. Directories, regular files and links that stay in dir are
// archived; the providers of each .terraform directory are left out,
// since init installs them again from the lock file, as is every other
// entry, which Write answers by path: a link that leaves dir, a device,
// a pipe or a socket. A file, link or name that holds one of o.Forbidden
// fails the write with ErrSecret naming its path, and a sealed workspace
// past MaxSize with ErrTooLarge.
//
// ref: decision 0008 § "The run specification and its secrets"
// ref: decision 0013 § "Delivery to runs"
func Write(w io.Writer, dir string, o Options) ([]string, error) {
	var left []string
	err := sealed(w, o, func(zw io.Writer) error {
		var err error
		left, err = archive(tar.NewWriter(zw), dir, forbidden(o.Forbidden))
		return err
	})
	if err != nil {
		return nil, err
	}
	return left, nil
}

// sealed writes to w the header and the object stream of what body
// writes, compressed, under a fresh content key sealed to o.Pool.
func sealed(w io.Writer, o Options, body func(io.Writer) error) error {
	key := make([]byte, seal.KeySize)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("make the content key: %w", err)
	}
	env, err := seal.SealEnvelope(o.Pool, o.Run, o.StateVersion, key)
	if err != nil {
		return fmt.Errorf("seal the content key: %w", err)
	}
	bw := &bounded{w: w, n: maxSize}
	head := make([]byte, 0, header+len(env))
	head = append(head, magic...)
	head = append(head, version)
	sv := uint64(o.StateVersion) //nolint:gosec // a state version is positive
	n := uint16(len(env))        //nolint:gosec // an envelope is 126 bytes
	head = binary.BigEndian.AppendUint64(head, sv)
	head = binary.BigEndian.AppendUint16(head, n)
	head = append(head, env...)
	if _, err := bw.Write(head); err != nil {
		return err
	}
	sw, err := seal.NewWriter(bw, key)
	if err != nil {
		return fmt.Errorf("seal the workspace: %w", err)
	}
	zw, err := zstd.NewWriter(sw, zstd.WithEncoderConcurrency(1))
	if err != nil {
		return fmt.Errorf("compress the workspace: %w", err)
	}
	if err := body(zw); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("compress the workspace: %w", err)
	}
	if err := sw.Close(); err != nil {
		return fmt.Errorf("seal the workspace: %w", err)
	}
	return nil
}

// forbidden is values without the empty one, which every file holds.
func forbidden(values [][]byte) [][]byte {
	out := make([][]byte, 0, len(values))
	for _, v := range values {
		if len(v) > 0 {
			out = append(out, v)
		}
	}
	return out
}

// archive writes dir's entries to tw and closes it, answering the paths
// it left out.
func archive(tw *tar.Writer, dir string, banned [][]byte) ([]string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open the workspace: %w", err)
	}
	defer root.Close()
	var left []string
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case p == ".":
			return nil
		case holds([]byte(p), banned):
			// The name itself holds the value, so the error names only its
			// directory, which the walk has already checked.
			return fmt.Errorf("%w: the name of an entry of the directory %s", ErrSecret,
				path.Dir(p))
		case d.IsDir() && path.Base(p) == "providers" && path.Base(path.Dir(p)) == ".terraform":
			return fs.SkipDir
		}
		name := filepath.FromSlash(p)
		info, err := d.Info()
		if err != nil {
			return err //nolint:wrapcheck // names the path
		}
		var link string
		switch {
		case d.IsDir(), info.Mode().IsRegular():
		case d.Type()&fs.ModeSymlink != 0:
			if link, err = root.Readlink(name); err != nil {
				return err //nolint:wrapcheck // names the path
			}
			if filepath.IsAbs(link) || !filepath.IsLocal(filepath.Join(filepath.Dir(name), link)) {
				left = append(left, p)
				return nil
			}
			if holds([]byte(link), banned) {
				return fmt.Errorf("%w: the link %s", ErrSecret, p)
			}
		default:
			left = append(left, p)
			return nil
		}
		return entry(tw, root, p, info, link, banned)
	})
	if err != nil {
		return nil, fmt.Errorf("archive the workspace: %w", err)
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("archive the workspace: %w", err)
	}
	return left, nil
}

// entry writes p's header and, for a regular file, its content, which
// must hold none of banned.
func entry(
	tw *tar.Writer,
	root *os.Root,
	p string,
	info fs.FileInfo,
	link string,
	banned [][]byte,
) error {
	h, err := tar.FileInfoHeader(info, link)
	if err != nil {
		return err //nolint:wrapcheck // names the path
	}
	h.Name, h.Uid, h.Gid, h.Uname, h.Gname = p, 0, 0, "", ""
	if info.IsDir() {
		h.Name += "/"
	}
	if err := tw.WriteHeader(h); err != nil {
		return err //nolint:wrapcheck // the caller names the step
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	f, err := root.Open(filepath.FromSlash(p))
	if err != nil {
		return err //nolint:wrapcheck // names the path
	}
	defer f.Close()
	if _, err := io.Copy(&scanner{w: tw, banned: banned}, f); err != nil {
		if errors.Is(err, ErrSecret) {
			return fmt.Errorf("%w: the file %s", ErrSecret, p)
		}
		return err //nolint:wrapcheck // the caller names the step
	}
	return nil
}

// holds reports whether b holds one of banned.
func holds(b []byte, banned [][]byte) bool {
	for _, v := range banned {
		if bytes.Contains(b, v) {
			return true
		}
	}
	return false
}

// scanner writes to w what holds none of banned, keeping the end of what
// it wrote, so a value split across two writes is found.
type scanner struct {
	w      io.Writer
	banned [][]byte
	tail   []byte
}

func (s *scanner) Write(p []byte) (int, error) {
	seen := append(s.tail, p...) //nolint:gocritic // seen is a new slice on purpose
	if holds(seen, s.banned) {
		return 0, ErrSecret
	}
	keep := 0
	for _, v := range s.banned {
		keep = max(keep, len(v)-1)
	}
	s.tail = append(s.tail[:0], seen[max(len(seen)-keep, 0):]...)
	return s.w.Write(p) //nolint:wrapcheck // the caller names the step
}

// bounded writes to w, failing a write past n bytes in all.
type bounded struct {
	w io.Writer
	n int64
}

func (b *bounded) Write(p []byte) (int, error) {
	if int64(len(p)) > b.n {
		return 0, fmt.Errorf("%w: over %d bytes sealed", ErrTooLarge, maxSize)
	}
	b.n -= int64(len(p))
	return b.w.Write(p) //nolint:wrapcheck // the caller's writer
}

// Read restores into dir, which must not exist, the workspace Write
// sealed for run, opening its content key with pool's private key, and
// extracting at most limit bytes as Extract does. It reads the object
// stream to its end, so a workspace truncated, extended or altered
// anywhere fails, each failure as ErrRefused.
func Read(r io.Reader, dir string, pool *ecdh.PrivateKey, run string, limit int64) error {
	if err := read(r, dir, pool, run, limit); err != nil {
		return fmt.Errorf("%w: %w", ErrRefused, err)
	}
	return nil
}

func read(r io.Reader, dir string, pool *ecdh.PrivateKey, run string, limit int64) error {
	head := make([]byte, header)
	if _, err := io.ReadFull(r, head); err != nil {
		return fmt.Errorf("its header: %w", err)
	}
	if string(head[:len(magic)]) != magic || head[len(magic)] != version {
		return errFormat
	}
	at := len(magic) + 1
	sv := int64(binary.BigEndian.Uint64(head[at:])) //nolint:gosec // the envelope checks it
	n := int(binary.BigEndian.Uint16(head[at+8:]))
	if n > maxEnvelope {
		return errFormat
	}
	env := make([]byte, n)
	if _, err := io.ReadFull(r, env); err != nil {
		return fmt.Errorf("its content key: %w", err)
	}
	key, err := seal.OpenEnvelope(pool, run, sv, env)
	if err != nil {
		return fmt.Errorf("its content key: %w", err)
	}
	sr, err := seal.NewReader(r, key)
	if err != nil {
		return fmt.Errorf("its content: %w", err)
	}
	zr, err := zstd.NewReader(sr, zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxWindow(maxWindow))
	if err != nil {
		return fmt.Errorf("its content: %w", err)
	}
	defer zr.Close()
	if err := os.Mkdir(dir, 0o750); err != nil {
		return fmt.Errorf("the workspace: %w", err)
	}
	if err := Extract(zr, dir, limit); err != nil {
		return err
	}
	if _, err := io.Copy(io.Discard, zr); err != nil {
		return fmt.Errorf("its content: %w", err)
	}
	return nil
}

// errFormat marks a value that is not a saved workspace.
var errFormat = errors.New("not a saved workspace")
