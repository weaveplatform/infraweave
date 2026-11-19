package vcslab

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/private"
)

type (
	tunnelSession struct {
		ID string `json:"id"`
	}
	tunnelReady struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	tunnelResult struct {
		ID        string `json:"id"`
		Ready     bool   `json:"ready"`
		Requested bool   `json:"requested"`
		Success   bool   `json:"success"`
	}
)

func writePrivateJSON(path string, value any) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".receipt-*")
	if err != nil {
		return ErrTunnel
	}
	temporary := f.Name()
	defer func() { _ = os.Remove(temporary) }()
	if err := private.Restrict(temporary); err != nil {
		_ = f.Close()
		return ErrTunnel
	}
	writeErr := json.NewEncoder(f).Encode(value)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return ErrTunnel
	}
	// A hard link atomically publishes complete bytes without replacing an
	// existing file or following a symlink. ACLs remain those of the private file.
	if err := os.Link(temporary, path); err != nil {
		return ErrTunnel
	}
	return nil
}

func readPrivateJSON(path string, value any) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.ErrNotExist
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return ErrTunnel
	}
	if err := private.Check(path); err != nil {
		return ErrTunnel
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ErrTunnel
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return ErrTunnel
	}
	var tail any
	if decoder.Decode(&tail) != io.EOF {
		return ErrTunnel
	}
	return nil
}

// RunTunnelSession supervises one cloudflared process in a freshly created
// private directory. Cleanup requests address the random session, never a PID.
// The result is durable only after the actual child and stream reader are joined.
func RunTunnelSession(ctx context.Context, command *exec.Cmd, directory string) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ErrTunnel
	}
	session := tunnelSession{ID: hex.EncodeToString(nonce[:])}
	if err := writePrivateJSON(filepath.Join(directory, "session.json"), session); err != nil {
		return err
	}
	for _, name := range []string{"ready.json", "stop.json", "result.json"} {
		if _, err := os.Lstat(filepath.Join(directory, name)); !errors.Is(err, os.ErrNotExist) {
			return ErrTunnel
		}
	}
	monitorCtx, cancel := context.WithCancel(ctx)
	stop := make(chan struct{})
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		tick := time.NewTicker(25 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-monitorCtx.Done():
				return
			case <-tick.C:
				var request tunnelSession
				err := readPrivateJSON(filepath.Join(directory, "stop.json"), &request)
				if err == nil && request.ID == session.ID {
					close(stop)
					return
				}
			}
		}
	}()
	out := runTunnel(ctx, command, filepath.Join(directory, "ready.json"), session.ID, stop)
	cancel()
	<-monitorDone
	result := tunnelResult{ID: session.ID, Ready: out.Ready, Requested: out.Requested, Success: out.Requested && out.Err == nil}
	if err := writePrivateJSON(filepath.Join(directory, "result.json"), result); err != nil {
		return err
	}
	return out.Err
}

// TunnelURL returns only the current session's private, validated readiness.
// A terminal failure is never presented as an established usable tunnel.
func TunnelURL(directory string) (string, error) {
	var session tunnelSession
	if err := readPrivateJSON(filepath.Join(directory, "session.json"), &session); err != nil || len(session.ID) != 32 {
		return "", ErrTunnel
	}
	var result tunnelResult
	if err := readPrivateJSON(filepath.Join(directory, "result.json"), &result); !errors.Is(err, os.ErrNotExist) {
		return "", ErrTunnel
	}
	var ready tunnelReady
	if err := readPrivateJSON(filepath.Join(directory, "ready.json"), &ready); err != nil {
		return "", ErrTunnel
	}
	match := quickURL.FindStringSubmatch(ready.URL)
	if ready.ID != session.ID || len(match) != 2 || match[1] != ready.URL {
		return "", ErrTunnel
	}
	return ready.URL, nil
}

// StopTunnel requests an owned stop and waits for its terminal receipt. An
// unexpected established-tunnel exit or missing acknowledgment is a failure;
// an already completed startup failure remains the workflow's allowed skip.
func StopTunnel(ctx context.Context, directory string) error {
	return stopTunnel(ctx, directory, 15*time.Second)
}

func stopTunnel(ctx context.Context, directory string, limit time.Duration) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), limit)
	defer cancel()
	var session tunnelSession
	if err := readPrivateJSON(filepath.Join(directory, "session.json"), &session); err != nil || len(session.ID) != 32 {
		return ErrTunnel
	}
	finish := func() (bool, error) {
		var result tunnelResult
		err := readPrivateJSON(filepath.Join(directory, "result.json"), &result)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil || result.ID != session.ID {
			return true, ErrTunnel
		}
		if !result.Ready || result.Success && result.Requested {
			return true, nil
		}
		return true, ErrTunnel
	}
	if done, err := finish(); done {
		return err
	}
	path := filepath.Join(directory, "stop.json")
	if err := writePrivateJSON(path, session); err != nil {
		var existing tunnelSession
		if readPrivateJSON(path, &existing) != nil || existing.ID != session.ID {
			return ErrTunnel
		}
	}
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ErrTunnel
		case <-tick.C:
			if done, err := finish(); done {
				return err
			}
		}
	}
}
