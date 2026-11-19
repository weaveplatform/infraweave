package vcslab

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
	"regexp"
	"time"
)

// ErrTunnel identifies a failed tunnel lifecycle without native log material.
// Only a completed pre-readiness startup failure is skippable by the workflow.
var ErrTunnel = errors.New("vcs lab: quick tunnel lifecycle failed")

var quickURL = regexp.MustCompile(`(https://[a-z0-9-]+\.trycloudflare\.com)(?:[[:space:]]|$)`)

type tunnelOutcome struct {
	Ready, Requested bool
	Err              error
}

// runTunnel alone owns the process handle and publishes readiness. It joins both
// Wait and the bounded stderr reader before returning any terminal outcome.
func runTunnel(ctx context.Context, command *exec.Cmd, readyFile, identity string, stop <-chan struct{}) tunnelOutcome {
	stderr, err := command.StderrPipe()
	if err != nil {
		return tunnelOutcome{Err: ErrTunnel}
	}
	defer func() { _ = stderr.Close() }()
	command.Stdout = io.Discard
	command.WaitDelay = 5 * time.Second
	if err := command.Start(); err != nil {
		return tunnelOutcome{Err: ErrTunnel}
	}
	urls := make(chan string, 1)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 4096), 64<<10)
		found := false
		for scanner.Scan() {
			if found {
				continue
			}
			match := quickURL.FindStringSubmatch(scanner.Text())
			if len(match) == 2 {
				found = true
				urls <- match[1]
			}
		}
	}()
	exited := make(chan struct{})
	go func() { _ = command.Wait(); close(exited) }()
	join := func() { <-exited; _ = stderr.Close(); <-readDone }
	end := func(out tunnelOutcome) tunnelOutcome { _ = command.Process.Kill(); join(); return out }
	timer := time.NewTimer(45 * time.Second)
	defer timer.Stop()
	out := tunnelOutcome{}
	select {
	case destination := <-urls:
		if ctx.Err() != nil {
			return end(tunnelOutcome{Err: ErrTunnel})
		}
		if err := writePrivateJSON(readyFile, tunnelReady{ID: identity, URL: destination}); err != nil {
			return end(tunnelOutcome{Err: ErrTunnel})
		}
		out.Ready = true
	case <-exited:
		join()
		return tunnelOutcome{Err: ErrTunnel}
	case <-readDone:
		return end(tunnelOutcome{Err: ErrTunnel})
	case <-timer.C:
		return end(tunnelOutcome{Err: ErrTunnel})
	case <-ctx.Done():
		return end(tunnelOutcome{Err: ErrTunnel})
	case <-stop:
		return end(tunnelOutcome{Requested: true})
	}
	select {
	case <-stop:
		// A child already observed dead is an unexpected exit, even when a
		// later cleanup request arrives. Our own kill happens only after the
		// requested-stop decision, so its Wait cannot race that classification.
		select {
		case <-exited:
			join()
			out.Err = ErrTunnel
			return out
		default:
		}
		out.Requested = true
		return end(out)
	case <-exited:
		join()
		out.Err = ErrTunnel
		return out
	case <-readDone:
		out.Err = ErrTunnel
		return end(out)
	case <-ctx.Done():
		out.Err = ErrTunnel
		return end(out)
	}
}
