package bootstrap

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
)

// Version is the stream format Send writes and Receive reads.
const Version = 1

// Codec is the workspace payload's encoding: a tar compressed with zstd.
const Codec = "tar+zstd"

// The stream's bounds. MaxWorkspace bounds the compressed workspace
// payload and MaxDecoded everything it decodes to, tar headers and
// padding included; the saved workspace's own bounds are the same 2 GiB
// sealed and 8 GiB extracted. MaxTool is the tool fetcher's bound on one
// verified executable.
//
// ref: docs/implementation/worker-pool-contracts.md § "Transfer protocol and existing bounds"
const (
	MaxManifest  = 64 << 10
	MaxWorkspace = 2 << 30
	MaxDecoded   = 8 << 30
	MaxTool      = 512 << 20
	MaxTools     = 2
)

// workspaceLimit and decodedLimit are MaxWorkspace and MaxDecoded, which
// tests lower.
var (
	workspaceLimit int64 = MaxWorkspace
	decodedLimit   int64 = MaxDecoded
)

// maxWindow bounds the zstd window the receiver decodes with, which the
// sender's default level stays well within.
const maxWindow = 64 << 20

// Errors of a stream.
var (
	// ErrStream marks a stream that is malformed, truncated, longer than
	// its manifest says, or whose content does not match its manifest.
	ErrStream = errors.New("bootstrap: the preparation stream")
	// ErrTooLarge marks a workspace over the stream's bounds.
	ErrTooLarge = errors.New("bootstrap: the workspace is over the transfer bounds")
	// ErrArchitecture marks a tool executable built for another
	// architecture than the receiver's.
	ErrArchitecture = errors.New("bootstrap: the tool is built for another architecture")
)

// Workspace describes the workspace payload: its codec, its exact
// compressed length and its SHA-256 in lowercase hex.
type Workspace struct {
	Codec  string `json:"codec"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Tool describes one verified tool executable: its fixed name, its
// release, its exact length and its SHA-256.
type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
}

// Manifest opens a stream: the run and assignment the stream prepares,
// the worker that holds it, and the payloads that follow it in order,
// the workspace and then each tool.
type Manifest struct {
	Version      int       `json:"version"`
	Run          string    `json:"run"`
	StateVersion int64     `json:"stateVersion"`
	Worker       string    `json:"worker"`
	Workspace    Workspace `json:"workspace"`
	Tools        []Tool    `json:"tools"`
}

// The tool names a stream may carry: the executables the runner images
// hold at /bin/<name>, which a downloaded release replaces.
var toolNames = map[string]bool{"terraform": true, "tofu": true, "terragrunt": true}

var (
	sha256Hex   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	toolVersion = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]{0,63}$`)
	identifier  = regexp.MustCompile(`^[0-9A-Za-z]{1,64}$`)
)

// Validate checks m's version, identity, payload descriptors and tools.
func (m Manifest) Validate() error {
	switch {
	case m.Version != Version:
		return fmt.Errorf("%w: version %d, want %d", ErrStream, m.Version, Version)
	case !identifier.MatchString(m.Run) || !identifier.MatchString(m.Worker) || m.StateVersion < 1:
		return fmt.Errorf("%w: the manifest names no run, state version and worker", ErrStream)
	case m.Workspace.Codec != Codec:
		return fmt.Errorf("%w: workspace codec %q", ErrStream, m.Workspace.Codec)
	case m.Workspace.Size < 0 || m.Workspace.Size > workspaceLimit || !sha256Hex.MatchString(m.Workspace.SHA256):
		return fmt.Errorf("%w: the workspace's length or hash", ErrStream)
	case len(m.Tools) > MaxTools:
		return fmt.Errorf("%w: %d tools, at most %d", ErrStream, len(m.Tools), MaxTools)
	}
	seen := map[string]bool{}
	for _, t := range m.Tools {
		switch {
		case !toolNames[t.Name] || seen[t.Name]:
			return fmt.Errorf("%w: tool %q is unknown or repeated", ErrStream, t.Name)
		case !toolVersion.MatchString(t.Version):
			return fmt.Errorf("%w: tool %s's version", ErrStream, t.Name)
		case t.Size < 1 || t.Size > MaxTool || !sha256Hex.MatchString(t.SHA256):
			return fmt.Errorf("%w: tool %s's length or hash", ErrStream, t.Name)
		}
		seen[t.Name] = true
	}
	return nil
}

// frame is a payload's header: its length as an unsigned 64-bit
// big-endian integer.
const frame = 8

// Send writes m and then each payload to w, each framed by its length:
// workspace holds m.Workspace.Size bytes and tools[i] m.Tools[i].Size. A
// payload that ends early or runs longer fails the stream rather than
// shifting the next frame.
func Send(w io.Writer, m Manifest, workspace io.Reader, tools []io.Reader) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if len(tools) != len(m.Tools) {
		return fmt.Errorf("%w: %d tool payloads for %d tools", ErrStream, len(tools), len(m.Tools))
	}
	head, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrStream, err)
	}
	if err := writeFrame(w, int64(len(head)), bytes.NewReader(head)); err != nil {
		return fmt.Errorf("bootstrap: send the manifest: %w", err)
	}
	if err := writeFrame(w, m.Workspace.Size, workspace); err != nil {
		return fmt.Errorf("bootstrap: send the workspace: %w", err)
	}
	for i, r := range tools {
		if err := writeFrame(w, m.Tools[i].Size, r); err != nil {
			return fmt.Errorf("bootstrap: send %s: %w", m.Tools[i].Name, err)
		}
	}
	return nil
}

// writeFrame writes n's header and exactly n bytes of r.
func writeFrame(w io.Writer, n int64, r io.Reader) error {
	var head [frame]byte
	binary.BigEndian.PutUint64(head[:], uint64(n)) //nolint:gosec // Validate bounds every length
	if _, err := w.Write(head[:]); err != nil {
		return err //nolint:wrapcheck // the caller names the payload
	}
	copied, err := io.Copy(w, io.LimitReader(r, n))
	if err != nil {
		return err //nolint:wrapcheck // the caller names the payload
	}
	if copied != n {
		return fmt.Errorf("%w: %d of %d bytes", ErrStream, copied, n)
	}
	var extra [1]byte
	if k, _ := r.Read(extra[:]); k != 0 {
		return fmt.Errorf("%w: the payload is longer than %d bytes", ErrStream, n)
	}
	return nil
}

// readFrame reads a frame header and answers a reader of exactly its
// length, which must be want, or at most limit when want is negative.
func readFrame(r io.Reader, want, limit int64) (*io.LimitedReader, error) {
	var head [frame]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return nil, fmt.Errorf("%w: a frame header: %w", ErrStream, err)
	}
	n := binary.BigEndian.Uint64(head[:])
	if want >= 0 && n != uint64(want) || want < 0 && n > uint64(limit) { //nolint:gosec // want and limit are nonnegative here
		return nil, fmt.Errorf("%w: a frame of %d bytes", ErrStream, n)
	}
	return &io.LimitedReader{R: r, N: int64(n)}, nil //nolint:gosec // bounded above
}

// digest is a SHA-256 that answers its sum in lowercase hex.
type digest struct {
	io.Writer

	sum func([]byte) []byte
}

func newDigest() digest {
	h := sha256.New()
	return digest{Writer: h, sum: h.Sum}
}

func (d digest) hex() string { return hex.EncodeToString(d.sum(nil)) }

// The run Pod's volumes, where the bootstrap container writes and the
// runner container reads: the copied infraweave executable in BinaryDir,
// the verified tools in ToolsDir, and the workspace. BinaryDir is also
// where the Docker executor mounts the runner's executable. The bootstrap
// container mounts each scoped-input volume under ScopedDir, and the
// runner container mounts its ScopedFiles directory at the directory of
// the scoped files it holds.
const (
	BinaryDir    = "/opt/infraweave/bin"
	ToolsDir     = "/opt/infraweave/tools"
	ScopedDir    = "/opt/infraweave/scoped"
	ScopedFiles  = "files"
	WorkspaceDir = "/mnt/workspace"
)

// PodPaths are the run Pod's volumes as Receive writes them.
var PodPaths = Paths{
	Workspace: WorkspaceDir, Tools: ToolsDir, Binary: BinaryDir + "/infraweave", Scoped: ScopedDir,
}
