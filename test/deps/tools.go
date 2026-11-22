package deps

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Tool is a pinned release of a command a scenario runs in a container:
// its Linux archives, each checked against the SHA-256 its release
// publishes before its binary is used. deps-refresh.yml moves each pin to
// its tool's newest release.
//
// ref: decision 0016
type Tool struct {
	// Name is the binary's name, and its archives' prefix.
	Name    string
	Version string
	// Download is the URL of the release's directory of archives, with %s
	// for the version.
	Download string
	// SHA256 is the hex SHA-256 of each Linux archive, by its file name.
	SHA256 map[string]string
	// Signature is the suffix of the release's detached signature of its
	// SHA256SUMS, which a launcher verifies against the vendor's key.
	Signature string
	// Build and SumsFile name the release's Linux build for an
	// architecture and its SHA256SUMS, with {version} and {arch} for
	// theirs; empty for <name>_<version>_linux_<arch>.zip and
	// <name>_<version>_SHA256SUMS.
	Build, SumsFile string
	// Bare is whether the build is the binary itself, not a zip that
	// holds it.
	Bare bool
}

// Terraform serves @terraform, from HashiCorp's release site.
var Terraform = Tool{
	Name:      "terraform",
	Version:   "1.17.2",
	Download:  "https://releases.hashicorp.com/terraform/%s/",
	Signature: ".sig",
	SHA256: map[string]string{
		"terraform_1.17.2_linux_amd64.zip": "e6f16b2f0b137ee5729caa8c4e79a3bae4ee261acd198a1fcfa8584a6296605b",
		"terraform_1.17.2_linux_arm64.zip": "858e08c181a76b6b223e8c767a8e983922eebe2d062db949a2de66ce040c368c",
	},
}

// OpenTofu serves @tofu, from its GitHub releases.
var OpenTofu = Tool{
	Name:      "tofu",
	Version:   "1.13.9",
	Download:  "https://github.com/opentofu/opentofu/releases/download/v%s/",
	Signature: ".gpgsig",
	SHA256: map[string]string{
		"tofu_1.13.9_linux_amd64.zip": "bc44eb6b06446ba83bd4072114891987f590f66cca712737a7f1147c50c539ce",
		"tofu_1.13.9_linux_arm64.zip": "04f7c93828b859d420269ed30f923ffebf09157b5d7fb95bf8d88a4d208b7fa3",
	},
}

// Cloudflared opens the hosted VCS lab's account-free, short-lived tunnel.
// Checksums are published in the official release; deps-refresh keeps these
// bare Linux builds current alongside the infrastructure tools.
var Cloudflared = Tool{
	Name:     "cloudflared",
	Version:  "2026.11.1",
	Download: "https://github.com/cloudflare/cloudflared/releases/download/%s/",
	Build:    "cloudflared-linux-{arch}",
	Bare:     true,
	SHA256: map[string]string{
		"cloudflared-linux-amd64": "97bb3b02d23a5980fc9c15a1a270e692da1923f103f920d5b16d4931d5ab55e7",
		"cloudflared-linux-arm64": "b155cec3e48e489cfb8e5c602769fb8c771083018d8881d68b4d18a791567c5a",
	},
}

// TerraformPrevious is the recorded catalogue's preceding stable release,
// used when a runtime configuration selects a different actual binary.
var TerraformPrevious = Tool{
	Name:      "terraform",
	Version:   "1.6.6",
	Download:  "https://releases.hashicorp.com/terraform/%s/",
	Signature: ".sig",
	SHA256: map[string]string{
		"terraform_1.6.6_linux_amd64.zip": "d117883fd98b960c5d0f012b0d4b21801e1aea985e26949c2d1ebb39af074f00",
		"terraform_1.6.6_linux_arm64.zip": "4066567f4ba031036d9b14c1edb85399aac1cfd6bbec89cdd8c26199adb2793b",
	},
}

// OpenTofuPrevious is the recorded catalogue's preceding stable OpenTofu
// release. deps-refresh moves both previous pins with the catalogue.
var OpenTofuPrevious = Tool{
	Name:      "tofu",
	Version:   "1.6.3",
	Download:  "https://github.com/opentofu/opentofu/releases/download/v%s/",
	Signature: ".gpgsig",
	SHA256: map[string]string{
		"tofu_1.6.3_linux_amd64.zip": "7069cf8b59b91d09bfe6fceb1a0a4f3e01473ea634dc83da56ac7f4d210b89ce",
		"tofu_1.6.3_linux_arm64.zip": "ac71ea31163a7c667b0e1cde2a5276b90aa25f57b03274e3162d7cab615559b4",
	},
}

// Terragrunt serves @terragrunt, from its GitHub releases, whose builds
// are bare binaries listed in one SHA256SUMS.
var Terragrunt = Tool{
	Name:      "terragrunt",
	Version:   "1.2.2",
	Download:  "https://github.com/gruntwork-io/terragrunt/releases/download/v%s/",
	Signature: ".gpgsig",
	Build:     "terragrunt_linux_{arch}",
	SumsFile:  "SHA256SUMS",
	Bare:      true,
	SHA256: map[string]string{
		"terragrunt_linux_amd64": "1ba4ad11cf6ac277e8ba59a3fdceb8576778e22f6a24bedd542e91bad9f36491",
		"terragrunt_linux_arm64": "a730c7daf2fb7dd13e6e7b016b3a89fd4f546bc81a31f8ff19474866c566b755",
	},
}

// Archive is the file name of t's Linux build for arch, a GOARCH.
func (t Tool) Archive(arch string) string {
	if t.Build != "" {
		return t.name(t.Build, arch)
	}
	return fmt.Sprintf("%s_%s_linux_%s.zip", t.Name, t.Version, arch)
}

// Sums is the file name of t's release's list of SHA-256s.
func (t Tool) Sums() string {
	if t.SumsFile != "" {
		return t.name(t.SumsFile, "")
	}
	return fmt.Sprintf("%s_%s_SHA256SUMS", t.Name, t.Version)
}

// cacheName is the name archive, t's build, is cached and kept by: a
// bare build's name, which holds no version, with t's name and version
// before it.
func (t Tool) cacheName(archive string) string {
	if t.Bare {
		return t.Name + "_" + t.Version + "_" + archive
	}
	return archive
}

// name is pattern with t's version and arch in place.
func (t Tool) name(pattern, arch string) string {
	return strings.NewReplacer("{version}", t.Version, "{arch}", arch).Replace(pattern)
}

// The bounds of a download and of the binary it holds. Each tool's
// archive is tens of MiB, and its binary, which Terragrunt publishes
// bare, under a few hundred.
const (
	maxArchive = 256 << 20
	maxBinary  = 512 << 20
)

// Errors of a tool that cannot be used.
var (
	// ErrToolPin is returned for an architecture a tool pins no SHA-256
	// for.
	ErrToolPin = errors.New("no pinned SHA-256")
	// ErrToolChecksum is returned for a download whose SHA-256 is not the
	// pinned one.
	ErrToolChecksum = errors.New("the SHA-256 is not the pinned one")
	// ErrToolArchive is returned for an archive that holds no binary of
	// the tool's name, or one past maxBinary.
	ErrToolArchive = errors.New("the archive holds no binary of the tool")
	// ErrToolDownload is returned for a download that answers other than
	// 200 or runs past maxArchive.
	ErrToolDownload = errors.New("the download failed")
)

// tools are the binaries a test binary extracted, by the cache name of
// their build, each once.
type tools struct {
	mu       sync.Mutex
	once     map[string]*toolOnce
	releases map[string]chan struct{}
	dir      string
}

type toolOnce struct {
	once sync.Once
	bin  string
	err  error
}

// Tool returns the path of t's binary for linux/arch, extracted once per
// test binary from its archive. The archive is downloaded into ToolCache
// on first use, and checked against its pin each time it is used, so a
// download cut short or changed since is fetched again.
func (d *Dependencies) Tool(ctx context.Context, t Tool, arch string) (string, error) {
	archive := t.Archive(arch)
	d.tools.mu.Lock()
	if d.tools.once == nil {
		d.tools.once = map[string]*toolOnce{}
	}
	o, ok := d.tools.once[t.cacheName(archive)]
	if !ok {
		o = &toolOnce{}
		d.tools.once[t.cacheName(archive)] = o
	}
	d.tools.mu.Unlock()
	o.once.Do(func() {
		o.bin, o.err = d.extract(ctx, t, archive)
		if o.err != nil {
			o.err = fmt.Errorf("%s %s for linux/%s: %w", t.Name, t.Version, arch, o.err)
		}
	})
	return o.bin, o.err
}

// extract extracts archive's binary, or copies a bare one, into the test
// binary's tools directory.
func (d *Dependencies) extract(ctx context.Context, t Tool, archive string) (string, error) {
	path, err := d.cached(ctx, t, archive)
	if err != nil {
		return "", err
	}
	dir, err := d.toolsDir()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, t.cacheName(archive)+".d", t.Name)
	if t.Bare {
		err = copyBinary(path, bin)
	} else {
		err = unzip(path, t.Name, bin)
	}
	if err != nil {
		return "", err
	}
	return bin, nil
}

// cached is the path of archive in the tool cache, checked against its
// pin and downloaded when the cache lacks it or holds other bytes.
func (d *Dependencies) cached(ctx context.Context, t Tool, archive string) (string, error) {
	want, ok := t.SHA256[archive]
	if !ok {
		return "", fmt.Errorf("%w for %s", ErrToolPin, archive)
	}
	cache, err := d.toolCache()
	if err != nil {
		return "", err
	}
	path := filepath.Join(cache, t.cacheName(archive))
	if sum, err := fileSHA256(path); err != nil || sum != want {
		if err := download(ctx, fmt.Sprintf(t.Download, t.Version)+archive, path); err != nil {
			return "", err
		}
		if sum, err = fileSHA256(path); err != nil {
			return "", err
		}
		if sum != want {
			return "", fmt.Errorf("%w: %s is %s, pinned %s", ErrToolChecksum, archive, sum, want)
		}
	}
	return path, nil
}

// Release returns the files of t's release for linux/arch that a mirror
// serves, by name, as paths in the tool cache: the archive, SHA256SUMS
// and its signature. The archive is checked against its pin, and
// SHA256SUMS must list that SHA-256 for it, so the cache is fetched again
// when it lists another. The signature is left to the launcher, which
// verifies it as it would the vendor's.
func (d *Dependencies) Release(ctx context.Context, t Tool, arch string) (map[string]string, error) {
	archive := t.Archive(arch)
	unlock, err := d.releaseLock(ctx, t.cacheName(archive))
	if err != nil {
		return nil, fmt.Errorf("%s %s for linux/%s: %w", t.Name, t.Version, arch, err)
	}
	defer unlock()
	files, err := d.release(ctx, t, archive)
	if err != nil {
		return nil, fmt.Errorf("%s %s for linux/%s: %w", t.Name, t.Version, arch, err)
	}
	return files, nil
}

// A cold release has one preparation owner. Waiters retain their own contexts;
// a failed owner releases the slot without caching its error for later callers.
func (d *Dependencies) releaseLock(ctx context.Context, key string) (func(), error) {
	d.tools.mu.Lock()
	if d.tools.releases == nil {
		d.tools.releases = make(map[string]chan struct{})
	}
	gate := d.tools.releases[key]
	if gate == nil {
		gate = make(chan struct{}, 1)
		d.tools.releases[key] = gate
	}
	d.tools.mu.Unlock()
	select {
	case gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-gate
			return nil, fmt.Errorf("prepare release: %w", err)
		}
		return func() { <-gate }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("prepare release: %w", ctx.Err())
	}
}

func (d *Dependencies) release(ctx context.Context, t Tool, archive string) (map[string]string, error) {
	path, err := d.cached(ctx, t, archive)
	if err != nil {
		return nil, err
	}
	cache, err := d.toolCache()
	if err != nil {
		return nil, err
	}
	sums := filepath.Join(cache, t.Name+"_"+t.Version+"_SHA256SUMS")
	sig := sums + t.Signature
	base := fmt.Sprintf(t.Download, t.Version)
	if !lists(sums, archive, t.SHA256[archive]) {
		for _, f := range []struct{ name, path string }{
			{t.Sums(), sums}, {t.Sums() + t.Signature, sig},
		} {
			if err := download(ctx, base+f.name, f.path); err != nil {
				return nil, err
			}
		}
		if !lists(sums, archive, t.SHA256[archive]) {
			return nil, fmt.Errorf("%w: %s does not list %s", ErrToolChecksum, t.Sums(),
				t.SHA256[archive])
		}
	}
	if _, err := os.Stat(sig); err != nil {
		if err := download(ctx, base+t.Sums()+t.Signature, sig); err != nil {
			return nil, err
		}
	}
	return map[string]string{
		archive: path, t.Sums(): sums, t.Sums() + t.Signature: sig,
	}, nil
}

// lists reports whether the SHA256SUMS at path lists sum for file.
func lists(path, file, sum string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for line := range strings.Lines(string(b)) {
		if f := strings.Fields(line); len(f) == 2 && f[0] == sum && f[1] == file {
			return true
		}
	}
	return false
}

// toolCache is ToolCache, or infraweave-acceptance under the user's
// cache directory.
func (d *Dependencies) toolCache() (string, error) {
	dir := d.ToolCache
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("tool cache: %w", err)
		}
		dir = filepath.Join(base, "infraweave-acceptance")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("tool cache: %w", err)
	}
	return dir, nil
}

// toolsDir is the test binary's directory of extracted binaries, which
// Close removes. It is made under the system's temporary directory, which
// Docker Desktop shares with its virtual machine, so a container mounts a
// binary from it.
func (d *Dependencies) toolsDir() (string, error) {
	d.tools.mu.Lock()
	defer d.tools.mu.Unlock()
	if d.tools.dir != "" {
		return d.tools.dir, nil
	}
	dir, err := os.MkdirTemp("", "infraweave-tools-")
	if err != nil {
		return "", fmt.Errorf("tools: %w", err)
	}
	d.tools.dir = dir
	d.OnClose(func(context.Context) error { return os.RemoveAll(dir) })
	return dir, nil
}

// fileSHA256 is the hex SHA-256 of the file at path.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("tool cache: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("tool cache: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// download writes the body url answers to path, through a file beside it
// that is renamed into place once complete, so a test binary that stops
// mid-download leaves no partial archive under path.
func download(ctx context.Context, url, path string) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("download %s: %w", url, err)
		}
		retry, err := downloadAttempt(ctx, url, path)
		if stopped := ctx.Err(); stopped != nil {
			return fmt.Errorf("download %s: %w", url, stopped)
		}
		if err == nil || !retry || attempt == 2 {
			return err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("download %s: %w", url, ctx.Err())
		case <-timer.C:
		}
	}
}

// Only transport failures may retry. A completed body still has to match its
// immutable pin; retry never accepts changed bytes, archives or signatures.
func downloadAttempt(ctx context.Context, url, path string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Errorf("download %s: %w", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return transientDownload(err), fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return transientStatus(resp.StatusCode), fmt.Errorf("%w: %s answered %d", ErrToolDownload, url, resp.StatusCode)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return false, fmt.Errorf("download %s: %w", url, err)
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, io.LimitReader(resp.Body, maxArchive+1))
	if cerr := tmp.Close(); err == nil {
		if cerr != nil {
			return false, fmt.Errorf("download %s: %w", url, cerr)
		}
	}
	switch {
	case err != nil:
		return transientDownload(err), fmt.Errorf("download %s: %w", url, err)
	case n > maxArchive:
		return false, fmt.Errorf("%w: %s is over %d bytes", ErrToolDownload, url, maxArchive)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return false, fmt.Errorf("download %s: %w", url, err)
	}
	return false, nil
}

func transientDownload(err error) bool {
	var network net.Error
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &network) && network.Timeout()
}

func transientStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// copyBinary writes the binary at path to bin, executable.
func copyBinary(path, bin string) error {
	src, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("tools: %w", err)
	}
	defer src.Close()
	return write(src, bin)
}

// write writes src, at most maxBinary bytes of it, to the new file bin,
// executable.
func write(src io.Reader, bin string) error {
	if err := os.MkdirAll(filepath.Dir(bin), 0o750); err != nil {
		return fmt.Errorf("tools: %w", err)
	}
	out, err := os.OpenFile(bin, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return fmt.Errorf("tools: %w", err)
	}
	_, err = io.Copy(out, io.LimitReader(src, maxBinary))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("tools: %w", err)
	}
	return nil
}

// unzip writes the entry name of the archive at path to bin, executable.
// Only that entry is read, so no other path the archive names is written.
func unzip(path, name, bin string) error {
	r, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrToolArchive, err)
	}
	defer r.Close()
	for _, f := range r.File {
		if f.Name != name || f.UncompressedSize64 > maxBinary {
			continue
		}
		src, err := f.Open()
		if err != nil {
			return fmt.Errorf("%w: %w", ErrToolArchive, err)
		}
		defer src.Close()
		return write(src, bin)
	}
	return fmt.Errorf("%w: %s holds no %s", ErrToolArchive, filepath.Base(path), name)
}
