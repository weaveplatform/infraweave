//go:build unix

package source

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// signature is the pre-signed URL's signature, which no error may name.
const signature = "c2lnbmF0dXJl"

// file is one entry of a test archive.
type file struct {
	h    tar.Header
	body string
}

func reg(name, body string, mode int64) file {
	return file{
		tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: mode, Size: int64(len(body))},
		body,
	}
}

func dir(name string) file {
	return file{h: tar.Header{Typeflag: tar.TypeDir, Name: name, Mode: 0o755}}
}

func link(typ byte, name, target string) file {
	return file{h: tar.Header{Typeflag: typ, Name: name, Linkname: target}}
}

func targz(t *testing.T, files ...file) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	tw := tar.NewWriter(zw)
	for _, f := range files {
		h := f.h
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// uploads serves one archive at /upload, as the uploads store's pre-signed
// GET does, or status when it is set.
type uploads struct {
	srv    *httptest.Server
	mu     sync.Mutex
	body   []byte
	status int
}

func newUploads(t *testing.T) *uploads {
	t.Helper()
	u := &uploads{}
	r := chi.NewRouter()
	r.Get("/upload", func(w http.ResponseWriter, _ *http.Request) {
		u.mu.Lock()
		defer u.mu.Unlock()
		if u.status != 0 {
			w.WriteHeader(u.status)
			return
		}
		_, _ = w.Write(u.body)
	})
	u.srv = httptest.NewServer(r)
	t.Cleanup(u.srv.Close)
	return u
}

func (u *uploads) serve(body []byte, status int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.body, u.status = body, status
}

func (u *uploads) url() string { return u.srv.URL + "/upload?X-Amz-Signature=" + signature }

func archiveSpec(url string, root *string) wire.RunSpec {
	var spec wire.RunSpec
	spec.Source = wire.RunSpecSource{Kind: wire.Archive, Url: url}
	spec.Stack.ProjectRoot = root
	return spec
}

// mode is path's permissions, not following a link.
func mode(t *testing.T, path string) fs.FileMode {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode()
}

// TestUnitFetchArchive checks that a local preview's archive is extracted
// with its directories, files, modes and the links that stay inside it;
// and that an entry or a link that leaves the workspace, a device, a
// pipe, an escape through links, an archive that is not one, a refused or
// failed download, and archives past their bounds each fail with the
// step named and never the URL's signature.
func TestUnitFetchArchive(t *testing.T) {
	ctx := t.Context()
	u := newUploads(t)
	u.serve(targz(
		t,
		file{
			h: tar.Header{
				Typeflag: tar.TypeXGlobalHeader,
				Name:     "pax",
				PAXRecords: map[string]string{
					"comment": "local preview",
				},
			},
		},
		dir("./"),
		dir("./infra/"),
		reg("./main.tf", "one\n", 0o644),
		reg("./infra/run.sh", "#!/bin/sh\n", 0o4755),
		link(tar.TypeSymlink, "./infra/main.tf", "../main.tf"),
		link(tar.TypeLink, "./copy.tf", "main.tf"),
		reg("deep/er/file.tf", "deep\n", 0o600),
	), 0)
	ws := filepath.Join(t.TempDir(), "ws")
	if err := fetcher().Fetch(ctx, ws, archiveSpec(u.url(), ptr("infra")), nil); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"main.tf": "one\n", "infra/main.tf": "one\n", "copy.tf": "one\n",
		"deep/er/file.tf": "deep\n", "infra/run.sh": "#!/bin/sh\n",
	} {
		if b, err := os.ReadFile(filepath.Join(ws, name)); err != nil || string(b) != want {
			t.Errorf("%s: %q, %v", name, b, err)
		}
	}
	if target, err := os.Readlink(filepath.Join(ws, "infra", "main.tf")); err != nil ||
		target != "../main.tf" {
		t.Errorf("the link: %q, %v", target, err)
	}
	if os.Geteuid() == 0 {
		var st syscall.Stat_t
		if err := syscall.Lstat(filepath.Join(ws, "main.tf"), &st); err != nil ||
			st.Uid != UID || st.Gid != GID {
			t.Errorf("owner %d:%d, %v", st.Uid, st.Gid, err)
		}
	} else {
		for name, want := range map[string]fs.FileMode{
			"": fs.ModeDir | 0o777, "infra": fs.ModeDir | 0o777, "main.tf": 0o666,
			"infra/run.sh": 0o777, "deep/er/file.tf": 0o666,
		} {
			if got := mode(t, filepath.Join(ws, name)); got != want {
				t.Errorf("%q: %v, want %v", name, got, want)
			}
		}
	}

	host := strings.TrimPrefix(u.srv.URL, "http://")
	for _, c := range []struct {
		name   string
		body   []byte
		status int
		edit   func(f *Fetcher)
		err    error
		note   string
	}{
		{
			"a traversing entry", targz(t, reg("../evil", "x", 0o644)), 0, nil, ErrArchive,
			"the entry ../evil leaves the workspace",
		},
		{
			"an absolute entry", targz(t, reg("/etc/evil", "x", 0o644)), 0, nil, ErrArchive,
			"the entry /etc/evil leaves the workspace",
		},
		{
			"a link out", targz(t, link(tar.TypeSymlink, "infra/l", "../../etc")), 0, nil,
			ErrArchive, "the link infra/l leaves the workspace",
		},
		{
			"an absolute link", targz(t, link(tar.TypeSymlink, "l", "/etc/passwd")), 0, nil,
			ErrArchive, "the link l leaves the workspace",
		},
		{
			"a hard link out", targz(t, link(tar.TypeLink, "h", "../x")), 0, nil, ErrArchive,
			"the link h leaves the workspace",
		},
		{
			"a device", targz(t, file{h: tar.Header{Typeflag: tar.TypeChar, Name: "tty"}}), 0, nil,
			ErrArchive, "the entry tty is a device or a pipe",
		},
		{
			"a pipe", targz(t, file{h: tar.Header{Typeflag: tar.TypeFifo, Name: "p"}}), 0, nil,
			ErrArchive, "the entry p is a device or a pipe",
		},
		{"an escape through links", targz(t,
			link(tar.TypeSymlink, "sub", "."), link(tar.TypeSymlink, "up", "sub/.."),
			reg("up/escape", "x", 0o644)), 0, nil, ErrArchive, "escapes"},
		{
			"no gzip", []byte("a workspace that is no tar.gz"), 0, nil, ErrArchive,
			"the archive is refused: gzip: invalid header",
		},
		{
			"a cut archive", targz(t, reg("main.tf", strings.Repeat("x", 4096), 0o644))[:64], 0,
			nil, ErrArchive, "unexpected EOF",
		},
		{
			"a refused download", nil, http.StatusForbidden, nil, ErrDownload,
			"the download failed: answered 403 Forbidden",
		},
		{
			"an oversized archive", targz(t, reg("main.tf", strings.Repeat("x", 4096), 0o644)), 0,
			func(f *Fetcher) { f.maxArchive = 16 }, ErrDownload, "the download failed: over 16 bytes",
		},
		{
			"an oversized extraction", targz(t, reg("a", "12345", 0o644), reg("b", "67890", 0o644)),
			0, func(f *Fetcher) { f.maxExtracted = 8 }, ErrArchive, "it extracts to over 8 bytes",
		},
		{
			"no project root", targz(t, reg("main.tf", "x", 0o644)), 0, nil, ErrProjectRoot,
			"the project root is not a directory of the workspace: infra",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			u.serve(c.body, c.status)
			f := fetcher()
			if c.edit != nil {
				c.edit(f)
			}
			parent := t.TempDir()
			err := f.Fetch(
				ctx,
				filepath.Join(parent, "ws"),
				archiveSpec(u.url(), ptr("infra")),
				nil,
			)
			if !errors.Is(err, c.err) || !strings.Contains(err.Error(), c.note) ||
				strings.Contains(err.Error(), signature) {
				t.Fatalf("got %v, want %v with %q", err, c.err, c.note)
			}
			if !errors.Is(c.err, ErrProjectRoot) && !strings.HasPrefix(err.Error(),
				"fetch the archive from "+host+": ") {
				t.Fatalf("the step: %v", err)
			}
			if entries, _ := os.ReadDir(parent); len(entries) != 1 {
				t.Fatalf("the archive wrote beside the workspace: %v", entries)
			}
		})
	}

	u.srv.Close()
	err := fetcher().Fetch(ctx, filepath.Join(t.TempDir(), "ws"), archiveSpec(u.url(), nil), nil)
	if !errors.Is(err, ErrDownload) || strings.Contains(err.Error(), signature) {
		t.Fatalf("an unreachable store: %v", err)
	}
	err = fetcher().Fetch(ctx, filepath.Join(t.TempDir(), "ws"),
		archiveSpec("http://example.com/\x7f", nil), nil)
	if !errors.Is(err, ErrURL) {
		t.Fatalf("a URL that does not parse: %v", err)
	}
	ws = filepath.Join(t.TempDir(), "ws")
	if err := os.Mkdir(ws, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := fetcher().Fetch(ctx, ws, archiveSpec(u.url(), nil), nil); err == nil ||
		!strings.Contains(err.Error(), "the workspace: ") {
		t.Fatalf("a workspace that is there: %v", err)
	}
}

// TestUnitHandOver checks that a launcher that may not change owners
// opens the workspace to every user, keeping executables executable and
// changing nothing a link points at.
func TestUnitHandOver(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root changes owners instead")
	}
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	ws := filepath.Join(base, "ws")
	for path, m := range map[string]fs.FileMode{outside: 0o600} {
		if err := os.WriteFile(path, nil, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(ws, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "sub", "x"), nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, "out")); err != nil {
		t.Fatal(err)
	}
	if err := handOver(ws); err != nil {
		t.Fatal(err)
	}
	if mode(t, outside) != 0o600 || mode(t, filepath.Join(ws, "sub")) != fs.ModeDir|0o777 ||
		mode(t, filepath.Join(ws, "sub", "x")) != 0o777 {
		t.Fatalf("modes: outside %v, sub %v, x %v", mode(t, outside),
			mode(t, filepath.Join(ws, "sub")), mode(t, filepath.Join(ws, "sub", "x")))
	}
	if err := handOver(filepath.Join(base, "none")); err == nil {
		t.Fatal("a workspace that is not there")
	}
}
