package source

import (
	"context"
	"crypto/ecdh"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/agent/workspace"
)

// The run's user in its container, which owns the workspace.
//
// ref: decision 0008 § "The launcher and the runner"
const (
	UID = 1983
	GID = 1983
)

// fetchTimeout bounds one source's fetch, under the pool's default
// preparing timeout of 15 minutes, so a stalled fetch fails the run before
// the server gives up on it.
const fetchTimeout = 10 * time.Minute

// Errors of a source that cannot be fetched, each wrapped with the step
// that refused it and the source's host.
var (
	ErrKind        = errors.New("no such source kind")
	ErrURL         = errors.New("the source is not an http or https URL")
	ErrCommit      = errors.New("not a commit hash")
	ErrDownload    = errors.New("the download failed")
	ErrArchive     = errors.New("the archive is refused")
	ErrProjectRoot = errors.New("the project root is not a directory of the workspace")
)

// Fetcher fetches runs' sources into their workspaces.
type Fetcher struct {
	doer wire.HttpRequestDoer
	ca   []byte
	// depth is how much history a Git fetch asks for: the commit alone.
	depth                    int
	maxArchive, maxExtracted int64
}

// New is a Fetcher that downloads archives through doer, and fetches Git
// over the system's roots and the PEM certificates of ca, which doer is
// expected to trust as well.
func New(doer wire.HttpRequestDoer, ca []byte) *Fetcher {
	return &Fetcher{
		doer: doer, ca: ca, depth: 1, maxArchive: maxArchive, maxExtracted: maxExtracted,
	}
}

// Fetch writes spec's source into dir, which must not exist: the commit
// checked out from its Git URL, presenting cred as HTTP Basic auth when
// there is one, or the archive extracted. It then checks that the
// specification's project root is a directory of it, and hands it to the
// run's user. cred stays in memory: it reaches the provider's host alone,
// and no file, URL, log or error holds it.
//
// ref: decision 0008 § "The run specification and its secrets"
// ref: decision 0012 § "Source access for runs"
func (f *Fetcher) Fetch(
	ctx context.Context,
	dir string,
	spec wire.RunSpec,
	cred *wire.RunSpecSourceCredential,
) error {
	err := f.fetch(ctx, dir, spec, cred)
	if err != nil && cred != nil && cred.Token != "" {
		err = redacted{err: err, secret: cred.Token}
	}
	return err
}

func (f *Fetcher) fetch(
	ctx context.Context,
	dir string,
	spec wire.RunSpec,
	cred *wire.RunSpecSourceCredential,
) error {
	s := spec.Source
	u, err := url.Parse(s.Url)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" ||
		u.User != nil {
		return ErrURL
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	switch s.Kind {
	case wire.Git:
		if err := f.git(ctx, dir, s.Url, spec.Commit.Hash, cred); err != nil {
			return fmt.Errorf("fetch commit %s from %s: %w", spec.Commit.Hash, u.Host, err)
		}
	case wire.Archive:
		if err := f.archive(ctx, dir, s.Url); err != nil {
			return fmt.Errorf("fetch the archive from %s: %w", u.Host, err)
		}
	default:
		return fmt.Errorf("%w %q", ErrKind, s.Kind)
	}
	return ready(dir, spec)
}

// Restore writes into dir, which must not exist, the run's saved
// workspace that src, its pre-signed URL, answers: at most 2 GiB, opened
// with pool's private key for run and extracted, at most 8 GiB, as
// workspace.Read does. It then checks spec's project root and hands dir to
// the run's user, as Fetch does. No error names src.
//
// ref: decision 0008 § "The run specification and its secrets"
func (f *Fetcher) Restore(
	ctx context.Context,
	dir, src string,
	pool *ecdh.PrivateKey,
	run string,
	spec wire.RunSpec,
) error {
	u, err := url.Parse(src)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" {
		return ErrURL
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	if err := f.download(ctx, src, func(body io.Reader) error {
		return workspace.Read(body, dir, pool, run, f.maxExtracted)
	}); err != nil {
		return fmt.Errorf("restore the saved workspace from %s: %w", u.Host, err)
	}
	return ready(dir, spec)
}

// ready checks that spec's project root is a directory of dir and hands
// dir to the run's user.
func ready(dir string, spec wire.RunSpec) error {
	if root := spec.Stack.ProjectRoot; root != nil && *root != "" && !isDir(dir, *root) {
		return fmt.Errorf("%w: %s", ErrProjectRoot, *root)
	}
	if err := handOver(dir); err != nil {
		return fmt.Errorf("hand the workspace to the run's user: %w", err)
	}
	return nil
}

// isDir reports whether name, slash-separated, is a directory of dir,
// reached through no link that leaves it.
func isDir(dir, name string) bool {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return false
	}
	defer r.Close()
	fi, err := r.Stat(filepath.FromSlash(name))
	return err == nil && fi.IsDir()
}

// redacted is err with each occurrence of secret replaced, so a provider
// that echoes the credential in an error cannot put it in a note or a
// log.
type redacted struct {
	err    error
	secret string
}

func (r redacted) Error() string {
	return strings.ReplaceAll(r.err.Error(), r.secret, "[credential]")
}

func (r redacted) Unwrap() error { return r.err }

// handOver gives dir to the run's user: owned by UID and GID 1983 where
// the launcher may change owners, as root may. Elsewhere, such as a
// launcher that is not root or one on macOS or Windows, whose Docker maps
// owners itself, every user may read and write it; only the run's
// container and the launcher's own user reach it, through the cache
// directory.
func handOver(dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err //nolint:wrapcheck // names the path
	}
	defer root.Close()
	chown := root.Lchown(".", UID, GID) == nil
	err = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		name := filepath.FromSlash(path)
		switch {
		case err != nil:
			return err
		case chown:
			return root.Lchown(name, UID, GID)
		case d.Type()&fs.ModeSymlink != 0:
			return nil
		case d.IsDir():
			return root.Chmod(name, 0o777)
		}
		fi, err := d.Info()
		if err != nil {
			return err //nolint:wrapcheck // names the path
		}
		mode := fs.FileMode(0o666)
		if fi.Mode()&0o111 != 0 {
			mode = 0o777
		}
		return root.Chmod(name, mode)
	})
	return err //nolint:wrapcheck // WalkDir names the path
}
