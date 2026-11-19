package vcslab

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/private"
)

// TestUnitTunnelProcessFixture is a child-process fixture, never a network tunnel.
func TestUnitTunnelProcessFixture(t *testing.T) {
	mode := os.Getenv("INFRAWEAVE_UNIT_TUNNEL_PROCESS")
	if mode == "" {
		return
	}
	if mode == "invalid" {
		fmt.Fprintln(os.Stderr, "https://lookalike.trycloudflare.com.evil.example private-native-error")
		return
	}
	fmt.Fprintln(os.Stderr, "discarded private-native-error")
	fmt.Fprintln(os.Stderr, "| https://unit-proof.trycloudflare.com |")
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(time.Hour)
	defer deadline.Stop()
	for {
		select {
		case <-deadline.C:
			return
		case <-tick.C:
			if path := os.Getenv("INFRAWEAVE_UNIT_TUNNEL_EXIT"); path != "" {
				if _, err := os.Stat(path); err == nil {
					return
				}
			}
		}
	}
}

func tunnelFixture(ctx context.Context, mode, exit string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUnitTunnelProcessFixture$")
	cmd.Env = append(os.Environ(), "INFRAWEAVE_UNIT_TUNNEL_PROCESS="+mode, "INFRAWEAVE_UNIT_TUNNEL_EXIT="+exit)
	return cmd
}

func waitTunnel(t *testing.T, dir string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if value, err := TunnelURL(dir); err == nil {
				if value != "https://unit-proof.trycloudflare.com" {
					t.Fatal("unexpected tunnel URL")
				}
				return
			}
		case <-deadline.C:
			t.Fatal("tunnel fixture never became ready")
		}
	}
}

func TestUnitQuickTunnelOwnedStopJoinsAndAcknowledges(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	dir := t.TempDir()
	cmd := tunnelFixture(ctx, "ready", "")
	done := make(chan error, 1)
	go func() { done <- RunTunnelSession(ctx, cmd, dir) }()
	waitTunnel(t, dir)
	if err := StopTunnel(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if cmd.ProcessState == nil {
		t.Fatal("owned child was not joined")
	}
	var result tunnelResult
	if err := readPrivateJSON(filepath.Join(dir, "result.json"), &result); err != nil || !result.Ready || !result.Requested || !result.Success {
		t.Fatal("requested stop has no successful joined receipt")
	}
	for _, name := range []string{"session.json", "ready.json", "stop.json", "result.json"} {
		if err := private.Check(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := StopTunnel(t.Context(), dir); err != nil {
		t.Fatal("repeated owned cleanup was not idempotent")
	}
}

func TestUnitQuickTunnelPostReadyExitIsAConfiguredFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	dir := t.TempDir()
	trigger := filepath.Join(t.TempDir(), "exit")
	cmd := tunnelFixture(ctx, "ready", trigger)
	done := make(chan error, 1)
	go func() { done <- RunTunnelSession(ctx, cmd, dir) }()
	waitTunnel(t, dir)
	if err := os.WriteFile(trigger, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrTunnel) {
		t.Fatal("unexpected established tunnel exit was accepted")
	}
	if err := StopTunnel(t.Context(), dir); !errors.Is(err, ErrTunnel) {
		t.Fatal("cleanup hid established tunnel failure")
	}
	if _, err := TunnelURL(dir); !errors.Is(err, ErrTunnel) {
		t.Fatal("dead tunnel retained usable readiness")
	}
	if cmd.ProcessState == nil {
		t.Fatal("failed child was not joined")
	}
}

func TestUnitQuickTunnelCancellationAtPublishedReadinessCannotSucceed(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	dir := t.TempDir()
	cmd := tunnelFixture(ctx, "ready", "")
	done := make(chan error, 1)
	go func() { done <- RunTunnelSession(ctx, cmd, dir) }()
	waitTunnel(t, dir)
	cancel()
	if err := <-done; !errors.Is(err, ErrTunnel) {
		t.Fatal("cancellation without an owned stop was accepted")
	}
	var result tunnelResult
	if err := readPrivateJSON(filepath.Join(dir, "result.json"), &result); err != nil || !result.Ready || result.Success {
		t.Fatal("published readiness cancellation lost its terminal failure")
	}
}

func TestUnitQuickTunnelStartupFailureIsJoinedAndSkippable(t *testing.T) {
	dir := t.TempDir()
	cmd := tunnelFixture(t.Context(), "invalid", "")
	err := RunTunnelSession(t.Context(), cmd, dir)
	if !errors.Is(err, ErrTunnel) || strings.Contains(err.Error(), "private-native-error") {
		t.Fatal("startup failure leaked native output or passed")
	}
	if err := StopTunnel(t.Context(), dir); err != nil {
		t.Fatal("joined pre-readiness failure did not remain skippable")
	}
	if _, err := os.Stat(filepath.Join(dir, "ready.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("lookalike URL became ready")
	}
	if cmd.ProcessState == nil {
		t.Fatal("startup child was not joined")
	}
}

func TestUnitQuickTunnelNeverAdoptsStaleSessionOrTerminalResult(t *testing.T) {
	dir := t.TempDir()
	session := tunnelSession{ID: strings.Repeat("a", 32)}
	if err := writePrivateJSON(filepath.Join(dir, "session.json"), session); err != nil {
		t.Fatal(err)
	}
	cmd := tunnelFixture(t.Context(), "ready", "")
	if err := RunTunnelSession(t.Context(), cmd, dir); !errors.Is(err, ErrTunnel) || cmd.Process != nil {
		t.Fatal("stale session was adopted or started another child")
	}
	if err := writePrivateJSON(filepath.Join(dir, "result.json"), tunnelResult{ID: strings.Repeat("b", 32), Ready: true, Requested: true, Success: true}); err != nil {
		t.Fatal(err)
	}
	if err := StopTunnel(t.Context(), dir); !errors.Is(err, ErrTunnel) {
		t.Fatal("another session's terminal receipt was adopted")
	}
	missing := t.TempDir()
	if err := writePrivateJSON(filepath.Join(missing, "session.json"), session); err != nil {
		t.Fatal(err)
	}
	if err := stopTunnel(t.Context(), missing, 50*time.Millisecond); !errors.Is(err, ErrTunnel) {
		t.Fatal("missing terminal acknowledgment was accepted")
	}
}

func TestUnitQuickTunnelDoesNotInheritProviderCredentialsOrAccountConfig(t *testing.T) {
	t.Setenv("LAB_GITHUB_APP_PRIVATE_KEY", "private-lab-key-must-not-reach-tunnel")
	t.Setenv("SEED_TOKEN", "private-seed-token-must-not-reach-tunnel")
	t.Setenv("TUNNEL_TOKEN", "preexisting-cloud-account-token-must-not-reach-tunnel")
	dir := t.TempDir()
	cmd, err := TunnelCommand(t.Context(), "pinned-cloudflared", "http://127.0.0.1:8765", dir)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cmd.Env, "\n")
	if strings.Contains(joined, "must-not-reach-tunnel") || !strings.Contains(joined, "HOME="+dir) {
		t.Fatal("tunnel inherited provider credentials or account configuration")
	}
	if _, err := TunnelCommand(t.Context(), "binary", "http://provider.example.com:80", dir); !errors.Is(err, ErrTunnel) {
		t.Fatal("nonlocal tunnel origin accepted")
	}
}
