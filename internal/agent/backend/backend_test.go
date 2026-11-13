package backend_test

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/backend"
	"github.com/weaveplatform/infraweave/internal/agent/handoff"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

var update = flag.Bool("update", false, "rewrite golden files")

// stack is the ULID the backend's path names.
const stack = "01JD6Z3Q4W8N5V2K7M9P0R1S3T"

// token is the run token, which no file may hold.
const token = "run.01JD6Z3Q4W8N5V2K7M9P0R1S3T.secret-part"

// managed is a handoff of a run on a stack with managed state, whose
// launcher reaches the server through a relay at relay.
func managed(relay string) handoff.Handoff {
	return handoff.Handoff{
		Endpoint: relay + "/worker/v1", Run: "run-1", RunToken: token,
		Assigned: wire.AssignmentStatePREPARING,
		Spec: wire.RunSpec{
			StateBackend: &wire.RunSpecStateBackend{Path: "/state/v1/stacks/" + stack},
		},
	}
}

// TestUnitBackendFile checks that Configure writes the backend file the
// golden file holds, with no credential in it, and answers the run token
// in the environment alone; that it replaces a symbolic link the
// repository commits under the file's name rather than writing through
// it; and that a run on a stack without managed state writes nothing.
func TestUnitBackendFile(t *testing.T) {
	root := t.TempDir()
	env, err := backend.Configure(root, managed("https://relay.internal:8443"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, backend.File))
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", backend.File)
	if *update {
		if err := os.WriteFile(golden, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run go test -update)", err)
	}
	if string(got) != string(want) {
		t.Errorf("the backend file differs from %s; run go test ./internal/agent/backend "+
			"-update and review the diff:\n%s", golden, got)
	}
	if strings.Contains(string(got), "secret-part") || strings.Contains(string(got), "password") {
		t.Errorf("the backend file holds a credential:\n%s", got)
	}
	if !slices.Equal(env, []string{"TF_HTTP_USERNAME=run", "TF_HTTP_PASSWORD=" + token}) {
		t.Errorf("environment = %q", env)
	}
	if err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if b, err := os.ReadFile(p); err != nil || strings.Contains(string(b), "secret-part") {
			t.Errorf("%s holds the run token (%v)", p, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	elsewhere := filepath.Join(t.TempDir(), "main.tf")
	if err := os.WriteFile(elsewhere, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(linked, backend.File)); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Configure(linked, managed("https://relay.internal:8443")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(elsewhere); string(b) != "kept" {
		t.Errorf("the link's target was overwritten: %q", b)
	}
	if fi, err := os.Lstat(filepath.Join(linked, backend.File)); err != nil ||
		!fi.Mode().IsRegular() {
		t.Errorf("the link was not replaced by a file: %v, %v", fi, err)
	}

	bare := t.TempDir()
	h := managed("https://relay.internal:8443")
	h.Spec.StateBackend = nil
	if env, err := backend.Configure(bare, h); env != nil || err != nil {
		t.Errorf("a stack without managed state = %q, %v", env, err)
	}
	if entries, _ := os.ReadDir(bare); len(entries) != 0 {
		t.Errorf("a stack without managed state wrote %v", entries)
	}
}

// TestUnitBackendFileRefused checks that Configure answers ErrAddress for
// an endpoint it cannot join, and an error when the project root cannot
// hold the file.
func TestUnitBackendFileRefused(t *testing.T) {
	if _, err := backend.Configure(t.TempDir(), managed("ftp://relay")); !errors.Is(err,
		backend.ErrAddress) {
		t.Errorf("an FTP endpoint = %v", err)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := backend.Configure(missing, managed("https://relay")); err == nil {
		t.Error("a missing project root wrote the backend file")
	}
	full := t.TempDir()
	if err := os.MkdirAll(filepath.Join(full, backend.File, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Configure(full, managed("https://relay")); err == nil {
		t.Error("a directory under the file's name was replaced")
	}
}

// TestUnitAddress checks that a backend path joins a relay's, a private
// address's and a prefixed server URL's endpoint keeping its scheme, host,
// port and prefix, and that an endpoint or a path Address cannot join is
// refused.
func TestUnitAddress(t *testing.T) {
	path := "/state/v1/stacks/" + stack
	for endpoint, want := range map[string]string{
		"https://relay.internal:8443/worker/v1": "https://relay.internal:8443" + path,
		"http://10.0.0.7:8080/worker/v1":        "http://10.0.0.7:8080" + path,
		"https://[fd00::7]:8443/worker/v1":      "https://[fd00::7]:8443" + path,
		"https://iw.example.com/infra/worker/v1": "https://iw.example.com/infra" +
			path,
	} {
		if got, err := backend.Address(endpoint, path); got != want || err != nil {
			t.Errorf("Address(%s) = %s, %v; want %s", endpoint, got, err, want)
		}
	}
	for name, tc := range map[string]struct{ endpoint, path string }{
		"no URL":         {"https://relay:port/worker/v1", path},
		"another scheme": {"ftp://relay/worker/v1", path},
		"no host":        {"https:///worker/v1", path},
		"user info":      {"https://user:pass@relay/worker/v1", path},
		"a query":        {"https://relay/worker/v1?x=1", path},
		"an empty query": {"https://relay/worker/v1?", path},
		"a fragment":     {"https://relay/worker/v1#x", path},
		"another API":    {"https://relay/api/v1", path},
		"a relative":     {"https://relay/worker/v1", "state/v1/stacks/" + stack},
		"a climb":        {"https://relay/worker/v1", "/state/v1/../../etc"},
	} {
		if got, err := backend.Address(tc.endpoint, tc.path); !errors.Is(err,
			backend.ErrAddress) {
			t.Errorf("%s = %q, %v", name, got, err)
		}
	}
}
