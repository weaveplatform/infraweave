package deps

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// Archive is the file name of t's Linux archive for arch, a GOARCH.
func (t Tool) Archive(arch string) string {
	return fmt.Sprintf("%s_%s_linux_%s.zip", t.Name, t.Version, arch)
}

// Sums is the file name of t's release's list of SHA-256s.
func (t Tool) Sums() string {
	return fmt.Sprintf("%s_%s_SHA256SUMS", t.Name, t.Version)
}

// The bounds of a download and of the binary it holds. Each tool's
// archive is tens of MiB, and its binary under a few hundred.
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

// tools are the binaries a test binary extracted, by archive, each once.
type tools struct {
	mu   sync.Mutex
	once map[string]*toolOnce
	dir  string
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
	o, ok := d.tools.once[archive]
	if !ok {
		o = &toolOnce{}
		d.tools.once[archive] = o
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

// extract extracts archive's binary into the test binary's tools
// directory.
func (d *Dependencies) extract(ctx context.Context, t Tool, archive string) (string, error) {
	path, err := d.cached(ctx, t, archive)
	if err != nil {
		return "", err
	}
	dir, err := d.toolsDir()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, archive+".d", t.Name)
	if err := unzip(path, t.Name, bin); err != nil {
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
	path := filepath.Join(cache, archive)
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
func (d *Dependencies) Release(
	ctx context.Context,
	t Tool,
	arch string,
) (map[string]string, error) {
	archive := t.Archive(arch)
	files, err := d.release(ctx, t, archive)
	if err != nil {
		return nil, fmt.Errorf("%s %s for linux/%s: %w", t.Name, t.Version, arch, err)
	}
	return files, nil
}

func (d *Dependencies) release(ctx context.Context, t Tool, archive string) (map[string]string,
	error,
) {
	path, err := d.cached(ctx, t, archive)
	if err != nil {
		return nil, err
	}
	cache, err := d.toolCache()
	if err != nil {
		return nil, err
	}
	sums := filepath.Join(cache, t.Sums())
	sig := sums + t.Signature
	base := fmt.Sprintf(t.Download, t.Version)
	if !lists(sums, archive, t.SHA256[archive]) {
		for _, f := range []string{sums, sig} {
			if err := download(ctx, base+filepath.Base(f), f); err != nil {
				return nil, err
			}
		}
		if !lists(sums, archive, t.SHA256[archive]) {
			return nil, fmt.Errorf("%w: %s does not list %s", ErrToolChecksum, t.Sums(),
				t.SHA256[archive])
		}
	}
	if _, err := os.Stat(sig); err != nil {
		if err := download(ctx, base+filepath.Base(sig), sig); err != nil {
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s answered %d", ErrToolDownload, url, resp.StatusCode)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, io.LimitReader(resp.Body, maxArchive+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return fmt.Errorf("download %s: %w", url, err)
	case n > maxArchive:
		return fmt.Errorf("%w: %s is over %d bytes", ErrToolDownload, url, maxArchive)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("download %s: %w", url, err)
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
	return fmt.Errorf("%w: %s holds no %s", ErrToolArchive, filepath.Base(path), name)
}
