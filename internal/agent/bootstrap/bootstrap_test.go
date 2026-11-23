package bootstrap

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

const (
	run    = "01KRUN00000000000000000001"
	worker = "01KWORKER0000000000000001"
)

var want = Expect{Run: run, StateVersion: 3, Worker: worker}

// executable is a minimal 64-bit little-endian ELF header of machine.
func executable(machine uint16) []byte {
	b := make([]byte, 64)
	copy(b, "\x7fELF")
	b[4], b[5], b[6] = 2, 1, 1
	binary.LittleEndian.PutUint16(b[16:], 2)
	binary.LittleEndian.PutUint16(b[18:], machine)
	binary.LittleEndian.PutUint32(b[20:], 1)
	binary.LittleEndian.PutUint16(b[52:], 64)
	binary.LittleEndian.PutUint16(b[54:], 56)
	binary.LittleEndian.PutUint16(b[58:], 64)
	return b
}

const (
	machineAMD64 = 62
	machineARM64 = 183
)

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// fixture is a workspace directory, its packed payload and the paths a
// receiver writes, with the receiving executable it copies.
type fixture struct {
	src   string
	paths Paths
	self  string
	ws    []byte
	desc  Workspace
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{src: t.TempDir()}
	must(t, os.MkdirAll(filepath.Join(f.src, "source", "modules"), 0o750))
	must(t, os.WriteFile(filepath.Join(f.src, "source", "main.tf"), []byte("resource \"terraform_data\" \"this\" {}\n"), 0o600))
	must(t, os.WriteFile(filepath.Join(f.src, "source", "hook.sh"), []byte("#!/bin/sh\n"), 0o700))
	must(t, os.WriteFile(filepath.Join(f.src, "source", "modules", "empty"), nil, 0o600))
	out := t.TempDir()
	f.paths = Paths{Workspace: filepath.Join(out, "workspace"), Tools: filepath.Join(out, "tools"), Binary: filepath.Join(out, "bin", "infraweave")}
	for _, d := range []string{f.paths.Workspace, f.paths.Tools, filepath.Dir(f.paths.Binary)} {
		must(t, os.MkdirAll(d, 0o750))
	}
	f.self = filepath.Join(out, "self")
	must(t, os.WriteFile(f.self, []byte("runner binary"), 0o600))
	f.pack(t)
	return f
}

func (f *fixture) pack(t *testing.T) {
	t.Helper()
	var buf bytes.Buffer
	desc, err := Pack(&buf, f.src)
	must(t, err)
	f.ws, f.desc = buf.Bytes(), desc
}

func (f *fixture) manifest(tools ...Tool) Manifest {
	return Manifest{Version: Version, Run: run, StateVersion: 3, Worker: worker, Workspace: f.desc, Tools: tools}
}

func tool(name string, body []byte) Tool {
	return Tool{Name: name, Version: "1.17.2", Size: int64(len(body)), SHA256: sum(body)}
}

func (f *fixture) stream(t *testing.T, m Manifest, bodies ...[]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	readers := make([]io.Reader, len(bodies))
	for i, b := range bodies {
		readers[i] = bytes.NewReader(b)
	}
	must(t, Send(&out, m, bytes.NewReader(f.ws), readers))
	return out.Bytes()
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestUnitStreamDeliversWorkspaceToolsAndRunner(t *testing.T) {
	f := newFixture(t)
	terraform, terragrunt := executable(machineARM64), executable(machineARM64)
	terragrunt = append(terragrunt, 'x')
	m := f.manifest(tool("terraform", terraform), tool("terragrunt", terragrunt))
	got, err := Receive(bytes.NewReader(f.stream(t, m, terraform, terragrunt)), f.paths, want, "arm64", f.self)
	must(t, err)
	if got.Run != run || len(got.Tools) != 2 || got.Workspace.SHA256 != f.desc.SHA256 {
		t.Fatalf("manifest %+v", got)
	}
	main, err := os.ReadFile(filepath.Join(f.paths.Workspace, "source", "main.tf"))
	must(t, err)
	if !strings.Contains(string(main), "terraform_data") {
		t.Fatalf("main.tf %q", main)
	}
	if _, err := os.Stat(filepath.Join(f.paths.Workspace, "source", "modules", "empty")); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{"terraform": terraform, "terragrunt": terragrunt} {
		b, err := os.ReadFile(filepath.Join(f.paths.Tools, name))
		must(t, err)
		if !bytes.Equal(b, body) {
			t.Fatalf("%s differs", name)
		}
	}
	bin, err := os.ReadFile(f.paths.Binary)
	must(t, err)
	if string(bin) != "runner binary" {
		t.Fatalf("binary %q", bin)
	}
	if runtime.GOOS != "windows" {
		hook, err := os.Stat(filepath.Join(f.paths.Workspace, "source", "hook.sh"))
		must(t, err)
		if hook.Mode().Perm()&0o100 == 0 {
			t.Fatalf("hook mode %v", hook.Mode())
		}
		fi, err := os.Stat(filepath.Join(f.paths.Tools, "terraform"))
		must(t, err)
		if fi.Mode().Perm() != 0o555 {
			t.Fatalf("tool mode %v", fi.Mode())
		}
	}
}

func TestUnitStreamWithoutToolsKeepsTheImagesOwn(t *testing.T) {
	f := newFixture(t)
	_, err := Receive(bytes.NewReader(f.stream(t, f.manifest())), f.paths, want, "amd64", f.self)
	must(t, err)
	entries, err := os.ReadDir(f.paths.Tools)
	must(t, err)
	if len(entries) != 0 {
		t.Fatalf("tools %v", entries)
	}
}

func TestUnitManifestValidation(t *testing.T) {
	f := newFixture(t)
	good := tool("tofu", executable(machineAMD64))
	for name, change := range map[string]func(*Manifest){
		"version":       func(m *Manifest) { m.Version = 2 },
		"run":           func(m *Manifest) { m.Run = "../escape" },
		"worker":        func(m *Manifest) { m.Worker = "" },
		"state version": func(m *Manifest) { m.StateVersion = 0 },
		"codec":         func(m *Manifest) { m.Workspace.Codec = "tar" },
		"size":          func(m *Manifest) { m.Workspace.Size = -1 },
		"oversized":     func(m *Manifest) { m.Workspace.Size = MaxWorkspace + 1 },
		"hash":          func(m *Manifest) { m.Workspace.SHA256 = strings.ToUpper(m.Workspace.SHA256) },
		"tool count":    func(m *Manifest) { m.Tools = []Tool{good, tool("terraform", []byte{1}), tool("terragrunt", []byte{1})} },
		"tool name":     func(m *Manifest) { m.Tools = []Tool{tool("kubectl", []byte{1})} },
		"duplicate":     func(m *Manifest) { m.Tools = []Tool{good, good} },
		"tool version":  func(m *Manifest) { m.Tools = []Tool{{Name: "tofu", Version: "../1", Size: 1, SHA256: good.SHA256}} },
		"tool size":     func(m *Manifest) { m.Tools = []Tool{{Name: "tofu", Version: "1", Size: 0, SHA256: good.SHA256}} },
		"tool hash":     func(m *Manifest) { m.Tools = []Tool{{Name: "tofu", Version: "1", Size: 1, SHA256: "x"}} },
	} {
		m := f.manifest()
		change(&m)
		if err := m.Validate(); !errors.Is(err, ErrStream) {
			t.Errorf("%s: %v", name, err)
		}
		if err := Send(io.Discard, m, bytes.NewReader(f.ws), nil); !errors.Is(err, ErrStream) {
			t.Errorf("send %s: %v", name, err)
		}
	}
}

// framed is a stream of raw frames.
func framed(parts ...[]byte) []byte {
	var out bytes.Buffer
	for _, p := range parts {
		var head [frame]byte
		binary.BigEndian.PutUint64(head[:], uint64(len(p)))
		out.Write(head[:])
		out.Write(p)
	}
	return out.Bytes()
}

func TestUnitReceiveRefusesMalformedStreams(t *testing.T) {
	f := newFixture(t)
	body := executable(machineARM64)
	m := f.manifest(tool("terraform", body))
	head, err := json.Marshal(m)
	must(t, err)
	good := f.stream(t, m, body)
	other := m
	other.StateVersion = 4
	otherHead, err := json.Marshal(other)
	must(t, err)
	wrongHash := m
	wrongHash.Workspace.SHA256 = sum([]byte("other"))
	wrongHashHead, err := json.Marshal(wrongHash)
	must(t, err)
	toolHash := m
	toolHash.Tools = []Tool{{Name: "terraform", Version: "1", Size: int64(len(body)), SHA256: sum([]byte("other"))}}
	toolHashHead, err := json.Marshal(toolHash)
	must(t, err)
	unknown := append(bytes.TrimSuffix(head, []byte("}")), []byte(`,"extra":1}`)...)
	for name, stream := range map[string][]byte{
		"empty":            nil,
		"unknown field":    framed(unknown),
		"trailing json":    framed(append(slicesClone(head), []byte(" {}")...)),
		"not json":         framed([]byte("{")),
		"oversized header": framed(make([]byte, MaxManifest+1)),
		"other assignment": append(framed(otherHead), good[frame+len(head):]...),
		"short manifest":   good[:frame+len(head)-1],
		"workspace length": append(framed(head), framed(f.ws[:len(f.ws)-1])...),
		"workspace hash":   append(framed(wrongHashHead), good[frame+len(head):]...),
		"truncated":        good[:len(good)-1],
		"trailing":         append(slicesClone(good), 0),
		"tool hash":        append(framed(toolHashHead), good[frame+len(head):]...),
		"corrupt payload":  append(framed(head), framed(bytes.Repeat([]byte{1}, len(f.ws)))...),
	} {
		dir := newFixture(t)
		if _, err := Receive(bytes.NewReader(stream), dir.paths, want, "arm64", dir.self); !errors.Is(err, ErrStream) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func slicesClone(b []byte) []byte { return append([]byte(nil), b...) }

func TestUnitReceiveRefusesAnotherArchitecture(t *testing.T) {
	f := newFixture(t)
	for _, body := range [][]byte{executable(machineAMD64), []byte("#!/bin/sh\n")} {
		g := newFixture(t)
		m := f.manifest(tool("tofu", body))
		_, err := Receive(bytes.NewReader(f.stream(t, m, body)), g.paths, want, "arm64", g.self)
		if !errors.Is(err, ErrArchitecture) {
			t.Fatalf("%v", err)
		}
	}
	m := f.manifest(tool("tofu", executable(machineARM64)))
	_, err := Receive(bytes.NewReader(f.stream(t, m, executable(machineARM64))), f.paths, want, "riscv64", f.self)
	if !errors.Is(err, ErrArchitecture) {
		t.Fatal(err)
	}
}

func TestUnitReceiveFailsWithoutItsBinaryOrDirectories(t *testing.T) {
	f := newFixture(t)
	stream := f.stream(t, f.manifest())
	if _, err := Receive(bytes.NewReader(stream), f.paths, want, "arm64", filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing executable was copied")
	}
	broken := f.paths
	broken.Binary = filepath.Join(t.TempDir(), "absent", "infraweave")
	if _, err := Receive(bytes.NewReader(stream), broken, want, "arm64", f.self); err == nil {
		t.Fatal("the binary was written outside its volume")
	}
	broken = f.paths
	broken.Workspace = filepath.Join(t.TempDir(), "absent")
	if _, err := Receive(bytes.NewReader(stream), broken, want, "arm64", f.self); !errors.Is(err, ErrStream) {
		t.Fatalf("%v", err)
	}
	body := executable(machineARM64)
	broken = f.paths
	broken.Tools = filepath.Join(t.TempDir(), "absent")
	if _, err := Receive(bytes.NewReader(f.stream(t, f.manifest(tool("tofu", body)), body)), broken, want, "arm64", f.self); err == nil {
		t.Fatal("a tool was written outside its volume")
	}
}

func TestUnitSendFailsPayloadsOfAnotherLength(t *testing.T) {
	f := newFixture(t)
	body := executable(machineARM64)
	m := f.manifest(tool("tofu", body))
	if err := Send(io.Discard, m, bytes.NewReader(f.ws), nil); !errors.Is(err, ErrStream) {
		t.Fatal(err)
	}
	if err := Send(io.Discard, m, bytes.NewReader(f.ws[:1]), []io.Reader{bytes.NewReader(body)}); !errors.Is(err, ErrStream) {
		t.Fatal(err)
	}
	if err := Send(io.Discard, m, bytes.NewReader(append(slicesClone(f.ws), 1)), []io.Reader{bytes.NewReader(body)}); !errors.Is(err, ErrStream) {
		t.Fatal(err)
	}
	if err := Send(io.Discard, m, bytes.NewReader(f.ws), []io.Reader{bytes.NewReader(body[:3])}); !errors.Is(err, ErrStream) {
		t.Fatal(err)
	}
	for _, at := range []int{0, frame + 1, 2*frame + 200} {
		if err := Send(&failing{left: at}, m, bytes.NewReader(f.ws), []io.Reader{bytes.NewReader(body)}); !errors.Is(err, errWrite) {
			t.Fatalf("at %d: %v", at, err)
		}
	}
}

var errWrite = errors.New("the stream broke")

// failing accepts left bytes and then fails.
type failing struct{ left int }

func (f *failing) Write(p []byte) (int, error) {
	if len(p) > f.left {
		return 0, errWrite
	}
	f.left -= len(p)
	return len(p), nil
}

func TestUnitTransferBoundsFailOversizedWorkspaces(t *testing.T) {
	f := newFixture(t)
	must(t, os.WriteFile(filepath.Join(f.src, "large"), bytes.Repeat([]byte("infraweave "), 4096), 0o600))
	defer func(w, d int64) { workspaceLimit, decodedLimit = w, d }(workspaceLimit, decodedLimit)
	decodedLimit = 4096
	if _, err := Pack(io.Discard, f.src); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("decoded: %v", err)
	}
	decodedLimit, workspaceLimit = MaxDecoded, 64
	if _, err := Pack(io.Discard, f.src); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("compressed: %v", err)
	}
	workspaceLimit = MaxWorkspace
	f.pack(t)
	decodedLimit = 4096
	if _, err := Receive(bytes.NewReader(f.stream(t, f.manifest())), f.paths, want, "arm64", f.self); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("receive: %v", err)
	}
}

func TestUnitReceiveDrainsTrailingCompressedData(t *testing.T) {
	f := newFixture(t)
	var extra bytes.Buffer
	zw, err := zstd.NewWriter(&extra)
	must(t, err)
	_, err = zw.Write(make([]byte, 1024))
	must(t, err)
	must(t, zw.Close())
	f.ws = append(slicesClone(f.ws), extra.Bytes()...)
	f.desc.Size, f.desc.SHA256 = int64(len(f.ws)), sum(f.ws)
	if _, err := Receive(bytes.NewReader(f.stream(t, f.manifest())), f.paths, want, "arm64", f.self); err != nil {
		t.Fatal(err)
	}
}

func TestUnitPackFailsAnUnreadableDirectory(t *testing.T) {
	if _, err := Pack(io.Discard, filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("packed a missing directory")
	}
}

func TestUnitBoundedReaderStopsAtItsLimit(t *testing.T) {
	b := &bounded{r: strings.NewReader("abcdef"), left: 4}
	got, err := io.ReadAll(b)
	if !errors.Is(err, errDecoded) || string(got) != "abcd" {
		t.Fatalf("%q %v", got, err)
	}
}
