package deps

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// TestUnitToolsPinned checks that each tool pins the SHA-256 of its Linux
// archive for both architectures a Docker daemon runs, for its version.
func TestUnitToolsPinned(t *testing.T) {
	sum := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, tool := range []Tool{Terraform, OpenTofu} {
		if tool.Signature == "" {
			t.Errorf("%s names no signature of its SHA256SUMS", tool.Name)
		}
		if len(tool.SHA256) != 2 {
			t.Errorf("%s pins %d archives, want amd64 and arm64", tool.Name, len(tool.SHA256))
		}
		for _, arch := range []string{"amd64", "arm64"} {
			if !sum.MatchString(tool.SHA256[tool.Archive(arch)]) {
				t.Errorf("%s pins no SHA-256 for %s", tool.Name, tool.Archive(arch))
			}
		}
	}
}

// archiveOf is a zip holding files.
func archiveOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func hexSHA256(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// release serves archives by path, counting the downloads.
func release(t *testing.T, archives map[string][]byte) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := archives[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		n.Add(1)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestUnitTool(t *testing.T) {
	good := archiveOf(t, map[string]string{"LICENSE": "text", "tool": "binary"})
	srv, n := release(t, map[string][]byte{
		"/1.0.0/tool_1.0.0_linux_amd64.zip": good,
		"/1.0.0/tool_1.0.0_linux_arm64.zip": archiveOf(t, map[string]string{"other": "x"}),
	})
	tool := Tool{
		Name: "tool", Version: "1.0.0", Download: srv.URL + "/%s/",
		SHA256: map[string]string{
			"tool_1.0.0_linux_amd64.zip": hexSHA256(good),
			"tool_1.0.0_linux_arm64.zip": hexSHA256(archiveOf(t, map[string]string{"other": "x"})),
		},
	}
	cache := t.TempDir()
	use := func(arch string) (string, error) {
		d := &Dependencies{ToolCache: cache}
		t.Cleanup(func() {
			if err := d.Close(context.Background()); err != nil {
				t.Error(err)
			}
		})
		bin, err := d.Tool(t.Context(), tool, arch)
		again, againErr := d.Tool(t.Context(), tool, arch)
		if again != bin || !errors.Is(againErr, err) {
			t.Errorf("a second use answered %q, %v, want %q, %v", again, againErr, bin, err)
		}
		return bin, err
	}

	bin, err := use("amd64")
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(bin); err != nil || string(b) != "binary" {
		t.Errorf("the binary holds %q, %v", b, err)
	}
	// Windows keeps no execute bit, so only a Unix host's mode says the
	// binary a container mounts is executable.
	if fi, err := os.Stat(bin); err != nil ||
		runtime.GOOS != "windows" && fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("the binary is not executable: %v, %v", fi, err)
	}
	if _, err := use("amd64"); err != nil || n.Load() != 1 {
		t.Errorf("a cached archive was downloaded again: %d downloads, %v", n.Load(), err)
	}

	// A cached archive that differs from its pin is fetched again.
	if err := os.WriteFile(filepath.Join(cache, "tool_1.0.0_linux_amd64.zip"), []byte("cut"),
		0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := use("amd64"); err != nil || n.Load() != 2 {
		t.Errorf("a changed cache was not fetched again: %d downloads, %v", n.Load(), err)
	}

	for _, tc := range []struct {
		name string
		tool Tool
		arch string
		want error
	}{
		{"no pin", tool, "riscv64", ErrToolPin},
		{"no binary", tool, "arm64", ErrToolArchive},
		{"not found", Tool{
			Name: "tool", Version: "2.0.0", Download: srv.URL + "/%s/",
			SHA256: map[string]string{"tool_2.0.0_linux_amd64.zip": hexSHA256(good)},
		}, "amd64", ErrToolDownload},
		{"changed", Tool{
			Name: "tool", Version: "1.0.0", Download: srv.URL + "/%s/",
			SHA256: map[string]string{"tool_1.0.0_linux_amd64.zip": hexSHA256([]byte("other"))},
		}, "amd64", ErrToolChecksum},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &Dependencies{ToolCache: t.TempDir()}
			t.Cleanup(func() { _ = d.Close(context.Background()) })
			if _, err := d.Tool(t.Context(), tc.tool, tc.arch); !errors.Is(err, tc.want) {
				t.Errorf("Tool = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestUnitToolRelease checks that a release's files are cached once with
// their names, its SHA256SUMS fetched again, with its signature, when the
// cached one lists another SHA-256, a signature the cache lacks fetched
// alone, and that a SHA256SUMS that never lists the pin, a missing
// signature and an archive with no pin each fail.
func TestUnitToolRelease(t *testing.T) {
	good := archiveOf(t, map[string]string{"tool": "binary"})
	sums := hexSHA256(good) + "  tool_1.0.0_linux_amd64.zip\n"
	files := map[string][]byte{
		"/1.0.0/tool_1.0.0_linux_amd64.zip": good,
		"/1.0.0/tool_1.0.0_SHA256SUMS":      []byte(sums),
		"/1.0.0/tool_1.0.0_SHA256SUMS.sig":  []byte("signature"),
		"/2.0.0/tool_2.0.0_linux_amd64.zip": good,
		"/2.0.0/tool_2.0.0_SHA256SUMS": []byte(
			hexSHA256([]byte("x")) + "  tool_2.0.0_linux_amd64.zip\n",
		),
		"/2.0.0/tool_2.0.0_SHA256SUMS.sig":  []byte("signature"),
		"/3.0.0/tool_3.0.0_linux_amd64.zip": good,
		"/3.0.0/tool_3.0.0_SHA256SUMS": []byte(
			hexSHA256(good) + "  tool_3.0.0_linux_amd64.zip\n",
		),
	}
	srv, n := release(t, files)
	tool := func(v string) Tool {
		return Tool{
			Name: "tool", Version: v, Download: srv.URL + "/%s/", Signature: ".sig",
			SHA256: map[string]string{"tool_" + v + "_linux_amd64.zip": hexSHA256(good)},
		}
	}
	cache := t.TempDir()
	d := &Dependencies{ToolCache: cache}
	t.Cleanup(func() { _ = d.Close(context.Background()) })
	got, err := d.Release(t.Context(), tool("1.0.0"), "amd64")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"tool_1.0.0_linux_amd64.zip": string(good), "tool_1.0.0_SHA256SUMS": sums,
		"tool_1.0.0_SHA256SUMS.sig": "signature",
	} {
		if b, err := os.ReadFile(got[name]); err != nil || string(b) != want {
			t.Errorf("%s holds %q, %v", name, b, err)
		}
	}
	if len(got) != 3 || n.Load() != 3 {
		t.Errorf("%d files after %d downloads: %v", len(got), n.Load(), got)
	}
	if _, err := d.Release(t.Context(), tool("1.0.0"), "amd64"); err != nil || n.Load() != 3 {
		t.Errorf("a cached release was downloaded again: %d downloads, %v", n.Load(), err)
	}
	if err := os.WriteFile(got["tool_1.0.0_SHA256SUMS"], []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Release(t.Context(), tool("1.0.0"), "amd64"); err != nil || n.Load() != 5 {
		t.Errorf("a changed SHA256SUMS was not fetched again: %d downloads, %v", n.Load(), err)
	}
	if err := os.Remove(got["tool_1.0.0_SHA256SUMS.sig"]); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Release(t.Context(), tool("1.0.0"), "amd64"); err != nil || n.Load() != 6 {
		t.Errorf("a missing signature was not fetched alone: %d downloads, %v", n.Load(), err)
	}

	for _, tc := range []struct {
		name string
		tool Tool
		arch string
		want error
	}{
		{"another SHA-256 listed", tool("2.0.0"), "amd64", ErrToolChecksum},
		{"no signature", tool("3.0.0"), "amd64", ErrToolDownload},
		{"no pin", tool("1.0.0"), "arm64", ErrToolPin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := d.Release(t.Context(), tc.tool, tc.arch)
			if !errors.Is(err, tc.want) || !strings.HasPrefix(err.Error(), "tool "+tc.tool.Version+
				" for linux/"+tc.arch+": ") {
				t.Errorf("Release = %v, want %v", err, tc.want)
			}
		})
	}
}
