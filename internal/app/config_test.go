package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

// loadArgs parses args as the serve process mode does and decodes them.
func loadArgs(t *testing.T, args ...string) (*Config, error) {
	t.Helper()
	fs := pflag.NewFlagSet("serve", pflag.ContinueOnError)
	ServeFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	return Load(fs)
}

func writeFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "infraweave.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUnitLoadDefaults(t *testing.T) {
	c, err := loadArgs(t)
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.Listen != ":8080" || c.Server.AdminListen != ":9090" ||
		c.Server.Shutdown.Timeout != 30*time.Second {
		t.Errorf("defaults %+v", c.Server)
	}
}

func TestUnitLoadPrecedence(t *testing.T) {
	file := writeFile(t, `
server:
  listen: ":1001"
  admin_listen: ":1002"
  shutdown:
    timeout: 1s
`)
	t.Setenv("INFRAWEAVE_SERVER_ADMIN_LISTEN", ":2002")
	t.Setenv("INFRAWEAVE_SERVER_SHUTDOWN_TIMEOUT", "2s")
	c, err := loadArgs(t, "--config", file, "--shutdown-timeout", "3s")
	if err != nil {
		t.Fatal(err)
	}
	// listen: the file alone; admin_listen: the environment over the file;
	// timeout: the flag over both.
	if c.Server.Listen != ":1001" || c.Server.AdminListen != ":2002" ||
		c.Server.Shutdown.Timeout != 3*time.Second {
		t.Errorf("got %+v", c.Server)
	}
}

func TestUnitLoadRejects(t *testing.T) {
	cases := []struct {
		name string
		file string
		args []string
		want string
	}{
		{"unknown key", "server:\n  listn: \":1\"\n", nil, "listn"},
		{"unknown section", "storage:\n  bucket: x\n", nil, "storage"},
		{"invalid listener", "", []string{"--listen", "nowhere"}, "server.listen"},
		{
			"invalid admin listener",
			"server:\n  admin_listen: \":99999\"\n",
			nil,
			"server.admin_listen",
		},
		{"bad duration", "server:\n  shutdown:\n    timeout: soon\n", nil, "timeout"},
		{"not YAML", "server: [\n", nil, "read config"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := tc.args
			if tc.file != "" {
				args = append(args, "--config", writeFile(t, tc.file))
			}
			_, err := loadArgs(t, args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error naming %q", err, tc.want)
			}
		})
	}
}

func TestUnitLoadMissingFile(t *testing.T) {
	if _, err := loadArgs(t, "--config", filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("a missing --config file was accepted")
	}
}

// TestUnitLoadUnboundFlag covers a settingFlags entry with no flag behind
// it, which renaming a flag would leave.
func TestUnitLoadUnboundFlag(t *testing.T) {
	all := pflag.NewFlagSet("serve", pflag.ContinueOnError)
	ServeFlags(all)
	fs := pflag.NewFlagSet("serve", pflag.ContinueOnError)
	all.VisitAll(func(f *pflag.Flag) {
		if f.Name != "listen" {
			fs.AddFlag(f)
		}
	})
	if _, err := Load(fs); err == nil || !strings.Contains(err.Error(), "bind flags") {
		t.Errorf("got %v, want a bind error", err)
	}
}

func TestUnitVersion(t *testing.T) {
	if got := Version(); got != "infraweave devel (commit unknown, built unknown)" {
		t.Errorf("Version() = %q", got)
	}
	if got := orUnknown("abc"); got != "abc" {
		t.Errorf("orUnknown(abc) = %q", got)
	}
}
