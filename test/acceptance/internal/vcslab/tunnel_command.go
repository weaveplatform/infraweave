package vcslab

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"strconv"
)

// TunnelCommand gives the pinned tunnel child only OS/network settings, never
// the App key, seed credential or provider tokens in the parent job environment.
// Its home is the fresh session directory, so it cannot reuse an account tunnel.
func TunnelCommand(ctx context.Context, binary, origin, directory string) (*exec.Cmd, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return nil, ErrTunnel
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || binary == "" || directory == "" {
		return nil, ErrTunnel
	}
	command := exec.CommandContext(ctx, binary, "tunnel", "--url", origin, "--no-autoupdate", "--protocol", "http2")
	command.Env = []string{"HOME=" + directory, "USERPROFILE=" + directory}
	for _, name := range []string{"PATH", "SYSTEMROOT", "WINDIR", "TEMP", "TMP", "TMPDIR", "SSL_CERT_FILE", "SSL_CERT_DIR", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "GOMAXPROCS"} {
		if value, ok := os.LookupEnv(name); ok {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	return command, nil
}
