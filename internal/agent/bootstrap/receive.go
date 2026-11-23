package bootstrap

import (
	"bytes"
	"debug/elf"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/zstd"

	"github.com/weaveplatform/infraweave/internal/agent/workspace"
)

// Paths are where Receive writes in its Pod: the workspace directory, the
// directory that holds each tool under its name, and the path the
// receiving executable copies itself to, which the run container starts
// as its runner. Scoped, when set, is the directory under which each of
// the Pod's scoped-input volumes is mounted.
type Paths struct {
	Workspace string
	Tools     string
	Binary    string
	Scoped    string
}

// Expect is the assignment the receiving Pod was created for; a stream of
// any other run, state version or worker is refused before it is read.
type Expect struct {
	Run          string
	StateVersion int64
	Worker       string
}

// Receive reads a stream Send wrote from r into p, answering its
// manifest. The manifest must be strict version-1 JSON naming want; the
// workspace is extracted under the workspace package's safe rules, with
// its decoded tar bounded by MaxDecoded, and each tool is written
// read-only and executable once its length and SHA-256 match, as an ELF
// executable of arch, GOARCH's name. self, the receiving executable, is
// copied to p.Binary. Each volume under p.Scoped gets a ScopedFiles
// directory the run's user owns, which a volume's own root is not, so the
// runner can leave the directory it mounts at unwritable once it writes a
// phase's files. Trailing bytes fail the stream. Every failure
// leaves the Pod's volumes to be discarded with it: nothing it wrote is
// started.
func Receive(r io.Reader, p Paths, want Expect, arch, self string) (Manifest, error) {
	m, err := manifest(r)
	if err != nil {
		return Manifest{}, err
	}
	if m.Run != want.Run || m.StateVersion != want.StateVersion || m.Worker != want.Worker {
		return Manifest{}, fmt.Errorf("%w: the manifest names another assignment than this Pod's", ErrStream)
	}
	if err := install(self, p.Binary); err != nil {
		return Manifest{}, err
	}
	if err := scopedDirs(p.Scoped); err != nil {
		return Manifest{}, err
	}
	if err := receiveWorkspace(r, m.Workspace, p.Workspace); err != nil {
		return Manifest{}, err
	}
	for _, t := range m.Tools {
		if err := receiveTool(r, t, p.Tools, arch); err != nil {
			return Manifest{}, err
		}
	}
	var extra [1]byte
	if n, _ := r.Read(extra[:]); n != 0 {
		return Manifest{}, fmt.Errorf("%w: trailing bytes", ErrStream)
	}
	return m, nil
}

// manifest reads and validates the stream's first frame.
// scopedDirs creates ScopedFiles, for the run's user alone, in each
// volume mounted under root; a Pod with no scoped inputs has no root.
func scopedDirs(root string) error {
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("the scoped-input volumes: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if err := os.Mkdir(filepath.Join(root, e.Name(), ScopedFiles), 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("the scoped-input volume %s: %w", e.Name(), err)
		}
	}
	return nil
}

func manifest(r io.Reader) (Manifest, error) {
	f, err := readFrame(r, -1, MaxManifest)
	if err != nil {
		return Manifest{}, err
	}
	raw, err := io.ReadAll(f)
	if err != nil || f.N != 0 {
		return Manifest{}, fmt.Errorf("%w: the manifest is truncated", ErrStream)
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("%w: the manifest: %w", ErrStream, err)
	}
	if dec.More() {
		return Manifest{}, fmt.Errorf("%w: the manifest has trailing data", ErrStream)
	}
	return m, m.Validate()
}

// install copies the executable self to dst, read-only and executable.
func install(self, dst string) error {
	in, err := os.Open(self) //nolint:gosec // the receiver's own executable
	if err != nil {
		return fmt.Errorf("bootstrap: the runner binary: %w", err)
	}
	defer in.Close()
	return writeExecutable(dst, in, -1)
}

// writeExecutable writes r to dst through a temporary file it renames
// into place, mode 0555, failing past limit bytes when limit is not
// negative.
func writeExecutable(dst string, r io.Reader, limit int64) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".receive-*")
	if err != nil {
		return fmt.Errorf("bootstrap: write %s: %w", filepath.Base(dst), err)
	}
	if limit >= 0 {
		r = io.LimitReader(r, limit)
	}
	_, err = io.Copy(tmp, r)
	err = errors.Join(err, tmp.Chmod(0o555), tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("bootstrap: write %s: %w", filepath.Base(dst), err)
	}
	return nil
}

// receiveWorkspace extracts the workspace frame into dir, then reads the
// rest of the frame so its whole length and hash are checked.
func receiveWorkspace(r io.Reader, ws Workspace, dir string) error {
	f, err := readFrame(r, ws.Size, 0)
	if err != nil {
		return err
	}
	d := newDigest()
	zr, err := zstd.NewReader(io.TeeReader(f, d), zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(maxWindow))
	if err != nil {
		return fmt.Errorf("%w: the workspace: %w", ErrStream, err)
	}
	defer zr.Close()
	decoded := &bounded{r: zr, left: decodedLimit}
	if err := workspace.Extract(decoded, dir, decodedLimit); err != nil {
		if errors.Is(err, errDecoded) || errors.Is(err, workspace.ErrTooLarge) {
			return fmt.Errorf("%w: %w", ErrTooLarge, err)
		}
		return fmt.Errorf("%w: extract the workspace: %w", ErrStream, err)
	}
	if _, err := io.Copy(io.Discard, decoded); err != nil {
		return fmt.Errorf("%w: the workspace: %w", ErrStream, err)
	}
	if _, err := io.Copy(d, f); err != nil {
		return fmt.Errorf("%w: the workspace: %w", ErrStream, err)
	}
	if f.N != 0 {
		return fmt.Errorf("%w: the workspace is truncated", ErrStream)
	}
	if d.hex() != ws.SHA256 {
		return fmt.Errorf("%w: the workspace's SHA-256 does not match its manifest", ErrStream)
	}
	return nil
}

// receiveTool writes the tool frame t to dir/<name> once its hash matches
// and it is an executable of arch.
func receiveTool(r io.Reader, t Tool, dir, arch string) error {
	f, err := readFrame(r, t.Size, 0)
	if err != nil {
		return err
	}
	dst := filepath.Join(dir, t.Name)
	d := newDigest()
	if err := writeExecutable(dst, io.TeeReader(f, d), -1); err != nil {
		return err
	}
	if f.N != 0 {
		return fmt.Errorf("%w: %s is truncated", ErrStream, t.Name)
	}
	if d.hex() != t.SHA256 {
		return fmt.Errorf("%w: %s's SHA-256 does not match its manifest", ErrStream, t.Name)
	}
	return built(dst, t.Name, arch)
}

// machines are the ELF machines of the architectures a run Pod may have.
var machines = map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}

// built checks that path is a 64-bit little-endian Linux ELF executable
// for arch.
func built(path, name, arch string) error {
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("%w: %s is not an ELF executable", ErrArchitecture, name)
	}
	defer f.Close()
	want, ok := machines[arch]
	if !ok || f.Machine != want || f.Class != elf.ELFCLASS64 || f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN {
		return fmt.Errorf("%w: %s is a %s executable, this Pod is %s", ErrArchitecture, name, f.Machine, arch)
	}
	return nil
}

// errDecoded marks a decoded workspace past MaxDecoded.
var errDecoded = errors.New("it decodes to more than the transfer bound")

// bounded reads r until left bytes have passed, then fails.
type bounded struct {
	r    io.Reader
	left int64
}

func (b *bounded) Read(p []byte) (int, error) {
	if b.left <= 0 {
		return 0, errDecoded
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.r.Read(p)
	b.left -= int64(n)
	return n, err //nolint:wrapcheck // the decoder's own error
}
