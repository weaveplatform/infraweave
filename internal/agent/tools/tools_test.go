package tools

import (
	"archive/zip"
	"bytes"
	"crypto/fips140"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// signers are two test keys: the vendor's, which the tests compile in
// through Fetcher.vendors, and another.
var signers = sync.OnceValues(func() ([2]*openpgp.Entity, error) {
	var keys [2]*openpgp.Entity
	var err error
	// A version 4 key's fingerprint is its SHA-1, which fips140=only
	// refuses, as it refuses every SHA-1.
	fips140.WithoutEnforcement(func() {
		for i, name := range []string{"Vendor", "Other"} {
			keys[i], err = openpgp.NewEntity(name, "", "", &packet.Config{
				Algorithm: packet.PubKeyAlgoRSA, RSABits: 2048,
			})
			if err != nil {
				return
			}
		}
	})
	return keys, err //nolint:wrapcheck // a test key
})

func signer(t *testing.T, i int) *openpgp.Entity {
	t.Helper()
	keys, err := signers()
	if err != nil {
		t.Fatal(err)
	}
	return keys[i]
}

// armored is e's public key, armored as a vendor publishes it.
func armored(t *testing.T, e *openpgp.Entity) []byte {
	t.Helper()
	var b bytes.Buffer
	w, err := armor.Encode(&b, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Serialize(w); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func sign(t *testing.T, e *openpgp.Entity, b []byte) []byte {
	t.Helper()
	var sig bytes.Buffer
	if err := openpgp.DetachSign(&sig, e, bytes.NewReader(b), nil); err != nil {
		t.Fatal(err)
	}
	return sig.Bytes()
}

// zipped is a zip holding each of files.
func zipped(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, body := range files {
		w, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// binaryOf is the fake binary of tool's release version.
func binaryOf(tool, version string) string {
	return "#!/bin/sh\necho " + tool + " " + version + "\n"
}

// releases serves releases at /<tool>/<version>/<file>, as a mirror and
// releases.hashicorp.com do, and counts the requests it answers.
type releases struct {
	srv   *httptest.Server
	mu    sync.Mutex
	files map[string][]byte
	hits  atomic.Int64
	// short is a path whose body ends before its Content-Length.
	short string
}

func newReleases(t *testing.T) *releases {
	t.Helper()
	r := &releases{files: map[string][]byte{}}
	m := chi.NewRouter()
	m.Get("/{tool}/{version}/{file}", func(w http.ResponseWriter, req *http.Request) {
		r.hits.Add(1)
		r.mu.Lock()
		b, ok := r.files[req.URL.Path]
		short := r.short == req.URL.Path
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		if short {
			w.Header().Set("Content-Length", fmt.Sprint(len(b)+1))
		}
		_, _ = w.Write(b)
	})
	r.srv = httptest.NewServer(m)
	t.Cleanup(r.srv.Close)
	return r
}

func (r *releases) set(path string, b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b == nil {
		delete(r.files, path)
		return
	}
	r.files[path] = b
}

// publish serves tool's release version for linux/amd64, its SHA256SUMS
// signed with key and its signature at sig's suffix.
func (r *releases) publish(t *testing.T, tool, version, sig string, key *openpgp.Entity) {
	t.Helper()
	dir := "/" + tool + "/" + version + "/"
	archive := fmt.Sprintf("%s_%s_linux_amd64.zip", tool, version)
	zip := zipped(t, map[string]string{
		tool: binaryOf(tool, version), "LICENSE.txt": "licence",
	})
	sum := sha256.Sum256(zip)
	sums := []byte(fmt.Sprintf("%s  %s_%s_darwin_arm64.zip\n%s  %s\n",
		strings.Repeat("0", 64), tool, version, hex.EncodeToString(sum[:]), archive))
	sumsFile := fmt.Sprintf("%s_%s_SHA256SUMS", tool, version)
	r.set(dir+archive, zip)
	r.set(dir+sumsFile, sums)
	r.set(dir+sumsFile+sig, sign(t, key, sums))
}

// fetcher is a Fetcher on a fresh cache whose vendors serve from from,
// each signing with the test vendor key.
func fetcher(t *testing.T, from *releases, mirror string) *Fetcher {
	t.Helper()
	f := New(Config{Dir: t.TempDir(), Mirror: mirror, Doer: http.DefaultClient})
	key := armored(t, signer(t, 0))
	f.vendors = map[wire.RunSpecToolName]vendor{
		wire.Terraform: {
			name: "HashiCorp", base: from.srv.URL + "/terraform/%s/", signature: ".sig", key: key,
		},
		wire.Tofu: {
			name: "OpenTofu", base: from.srv.URL + "/tofu/%s/", signature: ".gpgsig", key: key,
		},
	}
	return f
}

func release(tool wire.RunSpecToolName, version string) Release {
	return Release{Tool: tool, Version: version, OS: "linux", Arch: "amd64"}
}

// wantBinary checks that path is the executable binary of tool's release
// version.
func wantBinary(t *testing.T, path, tool, version string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil || string(b) != binaryOf(tool, version) {
		t.Fatalf("binary %s: %q, %v", path, b, err)
	}
	// Windows keeps no Unix permissions, and its launcher mounts the
	// binary into a Linux container that reads them from the file.
	if fi, err := os.Stat(path); err != nil ||
		runtime.GOOS != "windows" && fi.Mode().Perm() != 0o755 {
		t.Fatalf("binary %s: %v, %v", path, fi.Mode(), err)
	}
}

// TestUnitToolVerification checks a good release of each tool, and that
// a tampered SHA256SUMS, a tampered archive, another key's signature, no
// signature and each other fault refuse the release with the step and
// the error that say which.
func TestUnitToolVerification(t *testing.T) {
	ctx := t.Context()
	for _, c := range []struct {
		tool       wire.RunSpecToolName
		version    string
		sig        string
		dir, sums  string
		archive    string
		binaryName string
	}{
		{
			wire.Terraform, "1.17.2", ".sig", "/terraform/1.17.2/", "terraform_1.17.2_SHA256SUMS",
			"terraform_1.17.2_linux_amd64.zip", "terraform",
		},
		{
			wire.Tofu, "1.13.9", ".gpgsig", "/tofu/1.13.9/", "tofu_1.13.9_SHA256SUMS",
			"tofu_1.13.9_linux_amd64.zip", "tofu",
		},
	} {
		t.Run(string(c.tool), func(t *testing.T) {
			r := newReleases(t)
			r.publish(t, string(c.tool), c.version, c.sig, signer(t, 0))
			f := fetcher(t, r, "")
			path, err := f.Binary(ctx, release(c.tool, c.version))
			if err != nil {
				t.Fatal(err)
			}
			wantBinary(t, path, c.binaryName, c.version)
			if want := filepath.Join(f.c.Dir, "binaries", string(c.tool), c.version,
				"linux_amd64"); path != want {
				t.Fatalf("path %s, want %s", path, want)
			}

			for _, bad := range []struct {
				name   string
				tamper func()
				err    error
				note   string
			}{
				{"tampered sums", func() {
					sums := r.files[c.dir+c.sums]
					r.set(c.dir+c.sums, append([]byte("x"), sums...))
				}, ErrSignature, "verify " + c.sums + ": the signature does not verify"},
				{"tampered archive", func() {
					zip := r.files[c.dir+c.archive]
					r.set(c.dir+c.archive, append(zip, 0))
				}, ErrChecksum, "download " + c.archive + ": the SHA-256 is not the signed one"},
				{"another key", func() {
					r.set(c.dir+c.sums+c.sig, sign(t, signer(t, 1), r.files[c.dir+c.sums]))
				}, ErrSigner, fmt.Sprintf("the signature is by an unknown key, %X, not ",
					signer(t, 1).PrimaryKey.Fingerprint)},
				{"no signature", func() {
					r.set(c.dir+c.sums+c.sig, nil)
				}, ErrDownload, "download " + c.sums + c.sig + ": the download failed: " +
					r.srv.URL + c.dir + c.sums + c.sig + " answered 404 Not Found"},
				{"an empty signature", func() {
					r.set(c.dir+c.sums+c.sig, []byte{})
				}, ErrSignature, "verify " + c.sums + ": the signature does not verify: " +
					"the file holds no signature"},
				{"no sums", func() { r.set(c.dir+c.sums, nil) }, ErrDownload, "download " + c.sums},
				{
					"no archive", func() { r.set(c.dir+c.archive, nil) }, ErrDownload,
					"download " + c.archive,
				},
				{"an unlisted archive", func() {
					sums := []byte(strings.Repeat("0", 64) + "  other.zip\n")
					r.set(c.dir+c.sums, sums)
					r.set(c.dir+c.sums+c.sig, sign(t, signer(t, 0), sums))
				}, ErrUnlisted, "check " + c.archive},
				{"no binary in the archive", func() {
					zip := zipped(t, map[string]string{"README": "no binary"})
					sum := sha256.Sum256(zip)
					sums := []byte(hex.EncodeToString(sum[:]) + "  " + c.archive + "\n")
					r.set(c.dir+c.archive, zip)
					r.set(c.dir+c.sums, sums)
					r.set(c.dir+c.sums+c.sig, sign(t, signer(t, 0), sums))
				}, ErrArchive, "extract " + c.binaryName + " from " + c.archive},
				{"an archive that is no zip", func() {
					sums := []byte(hex.EncodeToString(sha256.New().Sum(nil)) + "  " +
						c.archive + "\n")
					r.set(c.dir+c.archive, []byte{})
					r.set(c.dir+c.sums, sums)
					r.set(c.dir+c.sums+c.sig, sign(t, signer(t, 0), sums))
				}, ErrArchive, "extract " + c.binaryName + " from " + c.archive},
				{"a corrupt binary", func() {
					zip := zipped(t, map[string]string{c.binaryName: strings.Repeat("x", 4096)})
					zip[30+len(c.binaryName)+2] ^= 0xff
					sum := sha256.Sum256(zip)
					sums := []byte(hex.EncodeToString(sum[:]) + "  " + c.archive + "\n")
					r.set(c.dir+c.archive, zip)
					r.set(c.dir+c.sums, sums)
					r.set(c.dir+c.sums+c.sig, sign(t, signer(t, 0), sums))
				}, ErrArchive, "extract " + c.binaryName + " from " + c.archive},
				{"an oversized archive", func() { f.maxArchive = 16 }, ErrDownload, "over 16 bytes"},
				{
					"a body cut short", func() { r.short = c.dir + c.archive }, ErrDownload,
					"download " + c.archive + ": the download failed: unexpected EOF",
				},
			} {
				t.Run(bad.name, func(t *testing.T) {
					r.short = ""
					r.publish(t, string(c.tool), c.version, c.sig, signer(t, 0))
					f = fetcher(t, r, "")
					bad.tamper()
					_, err := f.Binary(ctx, release(c.tool, c.version))
					prefix := fmt.Sprintf("%s %s for linux/amd64: ", c.tool, c.version)
					if !errors.Is(err, bad.err) || !strings.HasPrefix(err.Error(), prefix) ||
						!strings.Contains(err.Error(), bad.note) {
						t.Fatalf("got %v, want %v with %q", err, bad.err, bad.note)
					}
					entries, _ := os.ReadDir(filepath.Join(f.c.Dir, "binaries", string(c.tool),
						c.version))
					if len(entries) != 0 {
						t.Fatalf("a refused release leaves %v in the cache", entries)
					}
				})
			}
		})
	}

	f := fetcher(t, newReleases(t), "")
	for _, r := range []struct {
		r   Release
		err error
	}{
		{Release{Tool: "terragrunt", Version: "1.0.0", OS: "linux", Arch: "amd64"}, ErrTool},
		{Release{Tool: wire.Terraform, Version: "../1.0.0", OS: "linux", Arch: "amd64"}, ErrRelease},
		{Release{Tool: wire.Terraform, Version: "1.0.0", OS: "linux/..", Arch: "amd64"}, ErrRelease},
		{Release{Tool: wire.Terraform, Version: "1.0.0", OS: "linux", Arch: ""}, ErrRelease},
	} {
		if _, err := f.Binary(ctx, r.r); !errors.Is(err, r.err) {
			t.Errorf("%s: %v, want %v", r.r, err, r.err)
		}
	}
	f.c.Doer = failing{}
	if _, err := f.Binary(ctx, release(wire.Terraform, "1.0.0")); !errors.Is(err, ErrDownload) {
		t.Errorf("a failed request: %v", err)
	}
	f.c.Mirror = "::"
	if _, err := f.Binary(ctx, release(wire.Terraform, "1.0.0")); !errors.Is(err, ErrDownload) {
		t.Errorf("a mirror that is no URL: %v", err)
	}

	r := newReleases(t)
	r.publish(t, "terraform", "1.17.2", ".sig", signer(t, 0))
	f = fetcher(t, r, "")
	f.c.Dir = filepath.Join(f.c.Dir, "file")
	if err := os.WriteFile(f.c.Dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Binary(ctx, release(wire.Terraform, "1.17.2")); err == nil ||
		!strings.Contains(err.Error(), "the cache: ") {
		t.Errorf("a cache directory that is a file: %v", err)
	}

	id := uint64(0xC820C6D5CD27AB87)
	for s, want := range map[*packet.Signature]string{
		{IssuerFingerprint: []byte{0xAB, 0x01}}: "AB01",
		{IssuerKeyId: &id}:                      "C820C6D5CD27AB87",
		{}:                                      "which it does not name",
	} {
		if got := issuer(s); got != want {
			t.Errorf("issuer: %s, want %s", got, want)
		}
	}
}

type failing struct{}

var errRefused = errors.New("connection refused")

func (failing) Do(*http.Request) (*http.Response, error) { return nil, errRefused }

// TestUnitToolCache checks that a cached binary is reused with no
// download, that preparations at once leave one complete binary, and
// that a mirror serves every file when one is set.
func TestUnitToolCache(t *testing.T) {
	ctx := t.Context()
	r := newReleases(t)
	r.publish(t, "terraform", "1.17.2", ".sig", signer(t, 0))
	f := fetcher(t, r, "")
	path, err := f.Binary(ctx, release(wire.Terraform, "1.17.2"))
	if err != nil {
		t.Fatal(err)
	}
	before := r.hits.Load()
	again, err := f.Binary(ctx, release(wire.Terraform, "1.17.2"))
	if err != nil || again != path || r.hits.Load() != before {
		t.Fatalf("a cached binary: %s, %v, %d requests", again, err, r.hits.Load()-before)
	}

	f = fetcher(t, r, "")
	var wg sync.WaitGroup
	paths := make([]string, 8)
	errs := make([]error, 8)
	for i := range paths {
		wg.Go(func() { paths[i], errs[i] = f.Binary(ctx, release(wire.Terraform, "1.17.2")) })
	}
	wg.Wait()
	for i := range paths {
		if errs[i] != nil || paths[i] != paths[0] {
			t.Fatalf("preparation %d: %s, %v", i, paths[i], errs[i])
		}
	}
	wantBinary(t, paths[0], "terraform", "1.17.2")
	entries, err := os.ReadDir(filepath.Dir(paths[0]))
	if err != nil || len(entries) != 1 {
		t.Fatalf("the cache holds %v, %v", entries, err)
	}

	mirror := newReleases(t)
	mirror.publish(t, "tofu", "1.13.9", ".gpgsig", signer(t, 0))
	upstream := newReleases(t)
	upstream.publish(t, "tofu", "1.13.9", ".gpgsig", signer(t, 0))
	f = fetcher(t, upstream, mirror.srv.URL+"/")
	path, err = f.Binary(ctx, release(wire.Tofu, "1.13.9"))
	if err != nil {
		t.Fatal(err)
	}
	wantBinary(t, path, "tofu", "1.13.9")
	if mirror.hits.Load() != 3 || upstream.hits.Load() != 0 {
		t.Fatalf("the mirror answered %d requests and the vendor %d",
			mirror.hits.Load(), upstream.hits.Load())
	}
}

// TestUnitVendorKeys checks that the compiled-in keys are the vendors'
// published keys, by fingerprint, and that each verifies the SHA256SUMS
// its vendor signed for a recorded release and refuses it changed.
func TestUnitVendorKeys(t *testing.T) {
	for _, c := range []struct {
		tool        wire.RunSpecToolName
		fingerprint string
		sums, sig   string
	}{
		{
			wire.Terraform, "C874011F0AB405110D02105534365D9472D7468F",
			"terraform_1.17.2_SHA256SUMS", "terraform_1.17.2_SHA256SUMS.sig",
		},
		{
			wire.Tofu, "E3E6E43D84CB852EADB0051D0C0AF313E5FD9F80",
			"tofu_1.13.9_SHA256SUMS", "tofu_1.13.9_SHA256SUMS.gpgsig",
		},
	} {
		v := vendors[c.tool]
		var ring openpgp.EntityList
		var err error
		fips140.WithoutEnforcement(func() {
			ring, err = openpgp.ReadArmoredKeyRing(bytes.NewReader(v.key))
		})
		if err != nil || len(ring) != 1 ||
			fmt.Sprintf("%X", ring[0].PrimaryKey.Fingerprint) != c.fingerprint ||
			ring[0].PrivateKey != nil {
			t.Fatalf("%s's key: %v, %d keys", v.name, err, len(ring))
		}
		sums, err := os.ReadFile(filepath.Join("testdata", c.sums))
		if err != nil {
			t.Fatal(err)
		}
		sig, err := os.ReadFile(filepath.Join("testdata", c.sig))
		if err != nil {
			t.Fatal(err)
		}
		if err := verify(v, sums, sig); err != nil {
			t.Errorf("%s: %v", c.sums, err)
		}
		broken := vendor{name: "Broken", key: []byte("no key")}
		if err := verify(broken, sums, sig); err == nil ||
			!strings.Contains(err.Error(), "Broken's key") {
			t.Errorf("a broken key: %v", err)
		}
		sums[0] ^= 1
		if err := verify(v, sums, sig); !errors.Is(err, ErrSignature) {
			t.Errorf("%s changed: %v", c.sums, err)
		}
	}
}
