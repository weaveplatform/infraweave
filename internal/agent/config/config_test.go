package config_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"

	"github.com/weaveplatform/infraweave/internal/agent/config"
	"github.com/weaveplatform/infraweave/internal/platform/observe"
)

const keyPEM = "-----BEGIN EC PRIVATE KEY-----\nMHcC\n-----END EC PRIVATE KEY-----\n"

// load parses args as the launcher's flags and loads the configuration
// over the required settings, which the environment holds.
func load(t *testing.T, args ...string) (*config.Config, error) {
	t.Helper()
	t.Setenv("INFRAWEAVE_LAUNCHER_TOKEN", "token")
	t.Setenv("INFRAWEAVE_LAUNCHER_PRIVATE_KEY", keyPEM)
	fs := pflag.NewFlagSet("launcher", pflag.ContinueOnError)
	config.Flags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	return config.Load(fs)
}

func file(t *testing.T, yaml string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "launcher.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUnitLoadDefaults(t *testing.T) {
	c, err := load(t)
	if err != nil {
		t.Fatal(err)
	}
	l, d := c.Launcher, config.Defaults().Launcher
	if l.Token != "token" || l.PrivateKey.Reveal() != keyPEM || l.PollTimeout != time.Minute ||
		l.AdminListen != ":9091" || l.DefaultRunnerImage != d.DefaultRunnerImage || l.CacheDir != d.CacheDir ||
		l.Log.Level != "info" || l.Metadata != nil || l.StateLockTimeout != 5*time.Minute ||
		l.ToolMirror != "" || l.CAFile != "" {
		t.Fatalf("defaults: %+v", l)
	}
}

func TestUnitLoadRejectsUnknownKeys(t *testing.T) {
	for _, yaml := range []string{
		"launcher:\n  bogus: 1\n",
		"launcher:\n  docker:\n    privileged: true\n",
		"server:\n  listen: :8080\n",
	} {
		_, err := load(t, "--config", file(t, yaml))
		if err == nil || !strings.Contains(err.Error(), "invalid keys") {
			t.Errorf("%q: %v", yaml, err)
		}
	}
	if _, err := load(t, "--config", filepath.Join(t.TempDir(), "none.yaml")); err == nil {
		t.Fatal("a configuration file that does not exist")
	}
}

func TestUnitLoadPrecedence(t *testing.T) {
	f := file(t, `launcher:
  admin_listen: ":7000"
  poll_timeout: 30s
  state_lock_timeout: 90s
  mask_envs: [AWS_SECRET_ACCESS_KEY, DB_PASSWORD]
  tool_mirror: https://mirror.example.com/releases
  ca_file: /etc/infraweave/ca.pem
  metadata:
    zone: a
  docker:
    network: runs
    runner_binary: /opt/linux/infraweave
`)
	c, err := load(t, "--config", f)
	if err != nil {
		t.Fatal(err)
	}
	if c.Launcher.AdminListen != ":7000" || c.Launcher.PollTimeout != 30*time.Second ||
		c.Launcher.Docker.Network != "runs" || c.Launcher.Metadata["zone"] != "a" ||
		c.Launcher.Docker.RunnerBinary != "/opt/linux/infraweave" ||
		c.Launcher.StateLockTimeout != 90*time.Second ||
		!slices.Equal(c.Launcher.MaskEnvs, []string{"AWS_SECRET_ACCESS_KEY", "DB_PASSWORD"}) ||
		c.Launcher.ToolMirror != "https://mirror.example.com/releases" ||
		c.Launcher.CAFile != "/etc/infraweave/ca.pem" {
		t.Fatalf("file: %+v", c.Launcher)
	}

	t.Setenv("INFRAWEAVE_LAUNCHER_ADMIN_LISTEN", ":7001")
	t.Setenv("INFRAWEAVE_LAUNCHER_DOCKER_RUNTIME", "runsc")
	t.Setenv(config.MetadataEnv+"Rack", "7")
	t.Setenv("INFRAWEAVE_LAUNCHER_MASK_ENVS", "TOKEN,_KEY2")
	c, err = load(t, "--config", f)
	if err != nil {
		t.Fatal(err)
	}
	if c.Launcher.AdminListen != ":7001" || c.Launcher.Docker.Runtime != "runsc" ||
		!slices.Equal(c.Launcher.MaskEnvs, []string{"TOKEN", "_KEY2"}) ||
		c.Launcher.Metadata["zone"] != "a" || c.Launcher.Metadata["Rack"] != "7" {
		t.Fatalf("environment over file: %+v", c.Launcher)
	}

	c, err = load(t, "--config", f, "--admin-listen", ":7002", "--log-level", "debug")
	if err != nil {
		t.Fatal(err)
	}
	if c.Launcher.AdminListen != ":7002" || c.Launcher.Log.Level != "debug" {
		t.Fatalf("flag over environment: %+v", c.Launcher)
	}
}

func TestUnitValidate(t *testing.T) {
	valid := func() config.Launcher {
		l := config.Defaults().Launcher
		l.Token, l.PrivateKey = "token", keyPEM
		return l
	}
	if err := valid().Validate(); err != nil {
		t.Fatal(err)
	}
	many := map[string]string{}
	for i := range config.MaxMetadata + 1 {
		many[strings.Repeat("k", i+1)] = "v"
	}
	for key, edit := range map[string]func(*config.Launcher){
		"launcher.token":                 func(l *config.Launcher) { l.Token = " " },
		"launcher.private_key":           func(l *config.Launcher) { l.PrivateKey = "" },
		"launcher.poll_timeout":          func(l *config.Launcher) { l.PollTimeout = 7500 * time.Millisecond },
		"launcher.default_runner_image":  func(l *config.Launcher) { l.DefaultRunnerImage = "" },
		"launcher.cache_dir":             func(l *config.Launcher) { l.CacheDir = "" },
		"launcher.state_lock_timeout":    func(l *config.Launcher) { l.StateLockTimeout = -time.Second },
		"launcher.mask_envs \"A-B\"":     func(l *config.Launcher) { l.MaskEnvs = []string{"OK", "A-B"} },
		"launcher.mask_envs \"9X\"":      func(l *config.Launcher) { l.MaskEnvs = []string{"9X"} },
		"launcher.tool_mirror \"ftp:":    func(l *config.Launcher) { l.ToolMirror = "ftp://mirror" },
		"launcher.tool_mirror \"https:/": func(l *config.Launcher) { l.ToolMirror = "https:///x" },
		"launcher.tool_mirror \"::\"":    func(l *config.Launcher) { l.ToolMirror = "::" },
		"launcher.metadata holds":        func(l *config.Launcher) { l.Metadata = many },
		"launcher.metadata \"\"":         func(l *config.Launcher) { l.Metadata = map[string]string{"": "v"} },
		"launcher.admin_listen \"9091\"": func(l *config.Launcher) { l.AdminListen = "9091" },
		"port must be":                   func(l *config.Launcher) { l.AdminListen = ":http" },
		"launcher.log":                   func(l *config.Launcher) { l.Log.Level = "loud" },
	} {
		l := valid()
		edit(&l)
		err := l.Validate()
		if !errors.Is(err, config.ErrConfig) || !strings.Contains(err.Error(), key) {
			t.Errorf("%s: %v", key, err)
		}
	}
	for _, d := range []time.Duration{time.Second, 3 * time.Minute} {
		l := valid()
		l.PollTimeout = d
		if err := l.Validate(); err == nil {
			t.Errorf("poll_timeout %s", d)
		}
	}
}

func TestUnitKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, []byte(keyPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]string{
		"PEM":           "  " + keyPEM,
		"base64 of PEM": base64.StdEncoding.EncodeToString([]byte(keyPEM)),
		"a path":        path,
	} {
		b, err := config.Launcher{PrivateKey: observe.Secret(v)}.Key()
		if err != nil || strings.TrimSpace(string(b)) != strings.TrimSpace(keyPEM) {
			t.Errorf("%s: %q, %v", name, b, err)
		}
	}
	if _, err := (config.Launcher{PrivateKey: "/nonexistent/key.pem"}).Key(); err == nil {
		t.Fatal("a key file that does not exist")
	}
}

// TestUnitCA checks that launcher.ca_file adds its certificates to the
// system's roots, that an empty one adds none, and that a file that cannot
// be read or holds no certificate fails.
func TestUnitCA(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "private CA"},
		IsCA: true, BasicConstraintsValid: true,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	good, junk := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "junk.pem")
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(good, ca, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(junk, []byte("no certificate"), 0o600); err != nil {
		t.Fatal(err)
	}

	b, roots, err := config.Launcher{CAFile: good}.CA()
	if err != nil || string(b) != string(ca) {
		t.Fatalf("a CA file: %q, %v", b, err)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots}); err != nil {
		t.Fatalf("the roots do not trust the CA: %v", err)
	}
	if b, roots, err := (config.Launcher{}).CA(); b != nil || roots != nil || err != nil {
		t.Fatalf("no CA file: %q, %v, %v", b, roots, err)
	}
	if _, _, err := (config.Launcher{CAFile: filepath.Join(dir, "none.pem")}).CA(); err == nil ||
		!strings.Contains(err.Error(), "read launcher.ca_file") {
		t.Fatalf("a CA file that does not exist: %v", err)
	}
	if _, _, err := (config.Launcher{CAFile: junk}).CA(); !errors.Is(err, config.ErrConfig) {
		t.Fatalf("a CA file with no certificate: %v", err)
	}
}
