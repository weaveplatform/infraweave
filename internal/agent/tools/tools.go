package tools

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/fips140"
	"crypto/sha256"
	_ "embed" // the vendors' keys
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	pgperrors "github.com/ProtonMail/go-crypto/openpgp/errors"
	"github.com/ProtonMail/go-crypto/openpgp/packet"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// Release is one build of a tool: the binary a run's specification names,
// its version, and the platform of the container that runs it.
type Release struct {
	Tool    wire.RunSpecToolName
	Version string
	OS      string
	Arch    string
}

func (r Release) String() string {
	return fmt.Sprintf("%s %s for %s/%s", r.Tool, r.Version, r.OS, r.Arch)
}

// Config is where a Fetcher keeps binaries and what it fetches through.
type Config struct {
	// Dir holds the cache, each binary at
	// binaries/<tool>/<version>/<os>_<arch> under it.
	Dir string
	// Mirror is the base URL of a mirror that serves every release at
	// <Mirror>/<tool>/<version>/<file>, the layout of
	// releases.hashicorp.com, with each vendor's own file names. Empty
	// fetches from the vendors.
	Mirror string
	Doer   wire.HttpRequestDoer
}

// The bounds of a release's files. A tool's archive is tens of MiB and
// its binary a few hundred; SHA256SUMS lists a few dozen archives, and a
// detached signature is under a KiB.
const (
	maxArchive   = 256 << 20
	maxBinary    = 512 << 20
	maxSums      = 1 << 20
	maxSignature = 64 << 10
)

// fetchTimeout bounds one release's download and verification, under the
// pool's default preparing timeout of 15 minutes, so a stalled download
// fails the run before the server gives up on it.
const fetchTimeout = 10 * time.Minute

// vendor is where a tool's releases come from and who signs them.
type vendor struct {
	// name is the signer, as a note names it.
	name string
	// base is the URL of a release's files, with %s for the version.
	base string
	// signature is the suffix of SHA256SUMS's detached signature.
	signature string
	// key is the signer's armored public key.
	key []byte
}

// The vendors' public keys: HashiCorp's PGP key, which signs each
// Terraform release's SHA256SUMS, and the OpenTofu signing key, which
// signs each of OpenTofu's. They are compiled in, so a release is trusted
// only when a key the launcher's own binary carries signed it.
var (
	//go:embed keys/hashicorp.asc
	hashicorpKey []byte
	//go:embed keys/opentofu.asc
	opentofuKey []byte
)

// vendors are the tools a run mounts, by the binary its specification
// names.
//
// ref: decision 0008 § "Runner images"
var vendors = map[wire.RunSpecToolName]vendor{
	wire.Terraform: {
		name:      "HashiCorp",
		base:      "https://releases.hashicorp.com/terraform/%s/",
		signature: ".sig",
		key:       hashicorpKey,
	},
	wire.Tofu: {
		name:      "OpenTofu",
		base:      "https://github.com/opentofu/opentofu/releases/download/v%s/",
		signature: ".gpgsig",
		key:       opentofuKey,
	},
}

// Errors of a release that cannot be used, each wrapped with the release
// and the step that refused it.
var (
	ErrTool      = errors.New("no such tool")
	ErrRelease   = errors.New("not a release")
	ErrDownload  = errors.New("the download failed")
	ErrSigner    = errors.New("the signature is by an unknown key")
	ErrSignature = errors.New("the signature does not verify")
	ErrUnlisted  = errors.New("the signed sums list no such archive")
	ErrChecksum  = errors.New("the SHA-256 is not the signed one")
	ErrArchive   = errors.New("the archive holds no binary of the tool")
)

// version and platform are the names a cache path and a URL are built
// from, which keep both inside their directories.
var (
	version  = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$`)
	platform = regexp.MustCompile(`^[a-z0-9]+$`)
)

// Fetcher downloads, verifies and caches tool binaries.
type Fetcher struct {
	c          Config
	vendors    map[wire.RunSpecToolName]vendor
	maxArchive int64
}

// New is a Fetcher of c.
func New(c Config) *Fetcher {
	return &Fetcher{c: c, vendors: vendors, maxArchive: maxArchive}
}

// Binary answers the path of r's binary, from the cache, or downloaded
// and verified into it: SHA256SUMS's detached signature is checked against
// the vendor's key, then the archive's SHA-256 against the one SHA256SUMS
// lists, before the binary is extracted. The binary is written beside its
// path and renamed into place, so launchers sharing the cache never see a
// partial one.
//
// ref: decision 0008 § "Runner images"
func (f *Fetcher) Binary(ctx context.Context, r Release) (string, error) {
	path, err := f.binary(ctx, r)
	if err != nil {
		return "", fmt.Errorf("%s: %w", r, err)
	}
	return path, nil
}

func (f *Fetcher) binary(ctx context.Context, r Release) (string, error) {
	v, ok := f.vendors[r.Tool]
	if !ok {
		return "", ErrTool
	}
	if !version.MatchString(r.Version) || !platform.MatchString(r.OS) ||
		!platform.MatchString(r.Arch) {
		return "", ErrRelease
	}
	dir, err := filepath.Abs(filepath.Join(f.c.Dir, "binaries", string(r.Tool), r.Version))
	if err != nil {
		return "", fmt.Errorf("the cache: %w", err)
	}
	path := filepath.Join(dir, r.OS+"_"+r.Arch)
	if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() {
		return path, nil
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	base := fmt.Sprintf(v.base, r.Version)
	if f.c.Mirror != "" {
		base = strings.TrimSuffix(f.c.Mirror, "/") + "/" + string(r.Tool) + "/" + r.Version + "/"
	}
	sumsFile := fmt.Sprintf("%s_%s_SHA256SUMS", r.Tool, r.Version)
	sums, err := f.get(ctx, base+sumsFile, maxSums)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", sumsFile, err)
	}
	sig, err := f.get(ctx, base+sumsFile+v.signature, maxSignature)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", sumsFile+v.signature, err)
	}
	if err := verify(v, sums, sig); err != nil {
		return "", fmt.Errorf("verify %s: %w", sumsFile, err)
	}
	archive := fmt.Sprintf("%s_%s_%s_%s.zip", r.Tool, r.Version, r.OS, r.Arch)
	want, ok := listed(sums, archive)
	if !ok {
		return "", fmt.Errorf("check %s: %w", archive, ErrUnlisted)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("the cache: %w", err)
	}
	zipped, err := f.download(ctx, base+archive, dir, want)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", archive, err)
	}
	defer os.Remove(zipped)
	if err := extract(zipped, string(r.Tool), path); err != nil {
		return "", fmt.Errorf("extract %s from %s: %w", r.Tool, archive, err)
	}
	return path, nil
}

// verify checks sig, a detached signature, of sums against v's key. The
// key is read with FIPS 140-only enforcement lifted: a version 4
// OpenPGP key's fingerprint is its SHA-1, which only names it, and the
// key is trusted because it is compiled in. The signature itself is
// checked with enforcement, so a release signed with an algorithm FIPS
// 140-3 refuses is refused.
func verify(v vendor, sums, sig []byte) error {
	p, err := packet.Read(bytes.NewReader(sig))
	s, ok := p.(*packet.Signature)
	if err != nil || !ok {
		return fmt.Errorf("%w: the file holds no signature", ErrSignature)
	}
	var ring openpgp.EntityList
	fips140.WithoutEnforcement(func() {
		ring, err = openpgp.ReadArmoredKeyRing(bytes.NewReader(v.key))
	})
	if err != nil {
		return fmt.Errorf("%s's key: %w", v.name, err)
	}
	_, err = openpgp.CheckDetachedSignature(ring, bytes.NewReader(sums), bytes.NewReader(sig), nil)
	switch {
	case errors.Is(err, pgperrors.ErrUnknownIssuer):
		return fmt.Errorf("%w, %s, not %s's", ErrSigner, issuer(s), v.name)
	case err != nil:
		return fmt.Errorf("%w: %w", ErrSignature, err)
	}
	return nil
}

// issuer names the key s says made it.
func issuer(s *packet.Signature) string {
	switch {
	case s.IssuerFingerprint != nil:
		return fmt.Sprintf("%X", s.IssuerFingerprint)
	case s.IssuerKeyId != nil:
		return fmt.Sprintf("%016X", *s.IssuerKeyId)
	}
	return "which it does not name"
}

// listed answers the hex SHA-256 sums lists for file, each of its lines
// a sum, two spaces and a file name.
func listed(sums []byte, file string) (string, bool) {
	s := bufio.NewScanner(bytes.NewReader(sums))
	for s.Scan() {
		sum, name, ok := strings.Cut(s.Text(), "  ")
		if ok && name == file {
			return sum, true
		}
	}
	return "", false
}

// get answers the body url answers, at most limit bytes.
func (f *Fetcher) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	var b bytes.Buffer
	if err := f.fetch(ctx, url, &b, limit); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// download writes the body url answers to a temporary file in dir and
// answers its path once its SHA-256 is want.
func (f *Fetcher) download(ctx context.Context, url, dir, want string) (string, error) {
	tmp, err := os.CreateTemp(dir, ".archive-*")
	if err != nil {
		return "", fmt.Errorf("the cache: %w", err)
	}
	h := sha256.New()
	err = f.fetch(ctx, url, io.MultiWriter(tmp, h), f.maxArchive)
	if cerr := tmp.Close(); err == nil && cerr != nil {
		err = fmt.Errorf("the cache: %w", cerr)
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != want {
		err = fmt.Errorf("%w: %s, signed %s", ErrChecksum, hex.EncodeToString(h.Sum(nil)), want)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

// fetch copies the body url answers to w, failing a status other than 200
// and a body over limit.
func (f *Fetcher) fetch(ctx context.Context, url string, w io.Writer, limit int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDownload, err)
	}
	resp, err := f.c.Doer.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDownload, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s answered %s", ErrDownload, url, resp.Status)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	switch {
	case err != nil:
		return fmt.Errorf("%w: %w", ErrDownload, err)
	case n > limit:
		return fmt.Errorf("%w: %s is over %d bytes", ErrDownload, url, limit)
	}
	return nil
}

// extract writes the entry name of the zip at archive to path,
// executable, through a temporary file beside it that is renamed into
// place. Only that entry is read, so no other path the archive names is
// written.
func extract(archive, name, path string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrArchive, err)
	}
	defer r.Close()
	i := -1
	for j, e := range r.File {
		if e.Name == name && e.Mode().IsRegular() && e.UncompressedSize64 <= maxBinary {
			i = j
		}
	}
	if i < 0 {
		return ErrArchive
	}
	src, err := r.File[i].Open()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrArchive, err)
	}
	defer src.Close()
	tmp, err := os.CreateTemp(filepath.Dir(path), ".binary-*")
	if err != nil {
		return fmt.Errorf("the cache: %w", err)
	}
	defer os.Remove(tmp.Name())
	_, err = io.Copy(tmp, io.LimitReader(src, maxBinary))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrArchive, err)
	}
	// The run's user, UID 1983, is not the launcher's, and executes the
	// binary through a read-only mount.
	//nolint:gosec // every user reads and executes a tool binary
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return fmt.Errorf("the cache: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		// Windows refuses to replace a file another launcher is
		// renaming into place; its binary is as good as this one.
		if fi, serr := os.Stat(path); serr == nil && fi.Mode().IsRegular() {
			return nil
		}
		return fmt.Errorf("the cache: %w", err)
	}
	return nil
}
