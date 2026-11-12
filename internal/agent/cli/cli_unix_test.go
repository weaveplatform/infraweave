//go:build unix

package cli_test

import (
	"context"
	"io"
	"syscall"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/cli"
)

func TestUnitLauncherReloadsItsLogLevelOnSIGHUP(t *testing.T) {
	srv := pool(t)
	t.Setenv("INFRAWEAVE_LAUNCHER_LOG_LEVEL", "debug")
	pr, pw := io.Pipe()
	cmd := cli.LauncherCmd(srv.Client(), fake)
	cmd.SetArgs([]string{"--admin-listen", "127.0.0.1:0"})
	cmd.SetOut(pw)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- cmd.ExecuteContext(ctx)
		_ = pw.Close()
	}()
	log := logged(pr)
	// The launcher handles SIGHUP once it listens; before, SIGHUP would end
	// the test binary.
	await(t, log, "listening")
	await(t, log, "registered")
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	if m := await(t, log, "log.level reloaded"); m["log_level"] != "debug" {
		t.Fatalf("reloaded: %v", m)
	}
	t.Setenv("INFRAWEAVE_LAUNCHER_LOG_LEVEL", "loud")
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	await(t, log, "log.level not reloaded")
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("launcher: %v", err)
	}
}
