package vcslab

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// TunnelCommand gives the pinned tunnel child only OS/network settings, never
// the App key, seed credential or provider tokens in the parent job environment.
// Existing OS home settings remain unchanged. An explicit private empty config
// prevents account/config discovery, and --url selects the account-free tunnel.
func TunnelCommand(ctx context.Context, binary, origin, directory string) (*exec.Cmd, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return nil, ErrTunnel
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || binary == "" || directory == "" {
		return nil, ErrTunnel
	}
	config := filepath.Join(directory, "cloudflared-config.yml")
	if err := writePrivateJSON(config, map[string]any{}); err != nil {
		return nil, err
	}
	// ref: cloudflare/cloudflared2026.11.1 tunnel/cmd.go: explicit config and
	// URL select RunQuickTunnel without a named tunnel or account credential.
	command := exec.CommandContext(ctx, binary, "tunnel", "--config", config, "--url", origin, "--no-autoupdate", "--protocol", "http2", "--metrics", "127.0.0.1:0")
	command.Env = []string{}
	for _, name := range []string{"HOME", "USERPROFILE", "PATH", "SYSTEMROOT", "WINDIR", "TEMP", "TMP", "TMPDIR", "SSL_CERT_FILE", "SSL_CERT_DIR", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "GOMAXPROCS"} {
		if value, ok := os.LookupEnv(name); ok {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	return command, nil
}
