package launcher

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/agenttest"
	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/config"
)

func TestUnitIdentityKeys(t *testing.T) {
	pool, err := agenttest.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(pool.Key)
	sec1, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(sec1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newIdentity(
		pool.Token,
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}),
	); err != nil {
		t.Fatalf("a PKCS #8 key: %v", err)
	}

	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p384DER, err := x509.MarshalECPrivateKey(p384)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaDER, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		t.Fatal(err)
	}
	for name, key := range map[string][]byte{
		"not PEM":      []byte("key"),
		"a P-384 key":  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: p384DER}),
		"an RSA key":   pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rsaDER}),
		"a public key": pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: p384DER}),
		"garbage":      pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: []byte("x")}),
	} {
		if _, err := newIdentity(pool.Token, key); !errors.Is(err, ErrIdentity) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestUnitIdentityTokens(t *testing.T) {
	pool, err := agenttest.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(pool.Token)
	if err != nil {
		t.Fatal(err)
	}
	var tok poolToken
	if err := json.Unmarshal(raw, &tok); err != nil {
		t.Fatal(err)
	}
	encode := func(edit func(*poolToken)) string {
		t := tok
		edit(&t)
		b, _ := json.Marshal(t)
		return base64.StdEncoding.EncodeToString(b)
	}
	for name, token := range map[string]string{
		"not base64":      "%%",
		"not JSON":        base64.StdEncoding.EncodeToString([]byte("[")),
		"another version": encode(func(t *poolToken) { t.Version = 2 }),
		"no endpoint":     encode(func(t *poolToken) { t.Endpoint = "" }),
		"no certificate":  encode(func(t *poolToken) { t.Certificate = "" }),
		"a bad certificate": encode(func(t *poolToken) {
			t.Certificate = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("x")}))
		}),
	} {
		if _, err := newIdentity(token, pool.Key); !errors.Is(err, ErrIdentity) {
			t.Errorf("%s: %v", name, err)
		}
	}
	id, err := newIdentity(" "+pool.Token+"\n", pool.Key)
	if err != nil || id.pool != pool.ID || id.endpoint != agenttest.Endpoint {
		t.Fatalf("a token with surrounding space: %+v, %v", id, err)
	}
}

func TestUnitState(t *testing.T) {
	dir := t.TempDir()
	if s := load(dir); s != (state{}) {
		t.Fatalf("no state: %+v", s)
	}
	if err := forget(dir); err != nil {
		t.Fatalf("forget no state: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, stateFile+".tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := save(dir, state{Pool: "p"}); err == nil {
		t.Fatal("save over a directory")
	}

	dir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, stateFile, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := save(dir, state{Pool: "p"}); err == nil {
		t.Fatal("save renamed over a directory")
	}
	if err := forget(dir); err == nil {
		t.Fatal("forget removed a directory")
	}

	api := client.New("http://launcher.invalid", down{})
	var log bytes.Buffer
	l := &Launcher{api: api, d: Deps{
		Config: config.Launcher{CacheDir: dir}, Log: slog.New(slog.NewTextHandler(&log, nil)),
	}}
	l.saveOrLog(t.Context())
	l.deregister(t.Context(), "stopped")
	if got := log.String(); !strings.Contains(got, `msg="save the worker"`) ||
		!strings.Contains(got, `msg="forget the worker"`) {
		t.Fatalf("log: %s", got)
	}
}

func TestUnitTruncate(t *testing.T) {
	for _, c := range []struct {
		s    string
		n    int
		want string
	}{
		{"abc", 3, "abc"},
		{"abc", 2, "ab"},
		{"aé", 2, "a"},
		{"é", 1, ""},
	} {
		if got := truncate(c.s, c.n); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
}

func TestUnitLinesSplitsALongLine(t *testing.T) {
	var log bytes.Buffer
	w := &lines{log: slog.New(slog.NewJSONHandler(&log, nil))}
	w.named("run-1")
	if _, err := w.Write(bytes.Repeat([]byte("x"), maxLine+1)); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(log.String(), `"msg":"runner output"`); n != 1 {
		t.Fatalf("%d lines logged of a line longer than maxLine", n)
	}
	if len(w.buf) != 0 {
		t.Fatalf("%d bytes held", len(w.buf))
	}
}

// down is a worker API that cannot be reached.
type down struct{}

func (down) Do(*http.Request) (*http.Response, error) { return nil, errors.New("unreachable") }

// TestUnitWorkspaceDir checks that a run ID that is a path, or that
// names the workspaces directory itself, names no workspace, so no
// workspace is made or removed outside the cache's workspaces, and that
// the refused removal is logged.
func TestUnitWorkspaceDir(t *testing.T) {
	var log bytes.Buffer
	cache := t.TempDir()
	l := &Launcher{d: Deps{
		Config: config.Launcher{CacheDir: cache}, Log: slog.New(slog.NewTextHandler(&log, nil)),
	}}
	if dir, err := l.workspaceDir("01JRUN"); err != nil ||
		dir != filepath.Join(cache, "workspaces", "01JRUN") {
		t.Fatalf("a run ID: %s, %v", dir, err)
	}
	for _, run := range []string{"", ".", "..", "../x", "a/b", `a\b`, "/abs"} {
		if _, err := l.workspaceDir(run); !errors.Is(err, errRun) {
			t.Errorf("%q: %v", run, err)
		}
	}
	if err := os.WriteFile(filepath.Join(cache, "keep"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	l.dropWorkspace(t.Context(), "..")
	if _, err := os.Stat(filepath.Join(cache, "keep")); err != nil ||
		!strings.Contains(log.String(), "remove the run's workspace") {
		t.Fatalf("a run ID of ..: %v, %s", err, log.String())
	}
}

// TestUnitNewClient checks that the launcher's client refuses a server
// whose certificate the system's roots do not issue, and reaches it once
// the roots hold its CA.
func TestUnitNewClient(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(srv.Close)
	get := func(c *http.Client) error {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := c.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	if err := get(newClient(nil)); err == nil {
		t.Fatal("the system's roots trust the test server")
	}
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	if err := get(newClient(roots)); err != nil {
		t.Fatal(err)
	}
}
