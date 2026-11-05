package acceptance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"gopkg.in/yaml.v3"
)

// BuildBinary builds cmd/infraweave from the module at root into dir with
// coverage over the whole module, so the statements a scenario reaches in
// the subprocess count toward the merged profile. atomic matches the mode
// make accept runs the test binary in; covdata refuses to merge profiles
// whose modes differ.
func BuildBinary(ctx context.Context, root, dir string) (string, error) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return "", fmt.Errorf("build infraweave: %w", err)
	}
	mod := exec.CommandContext(ctx, goBin, "list", "-m")
	mod.Dir = root
	mod.Env = append(os.Environ(), "GOWORK=off")
	module, err := mod.Output()
	if err != nil {
		return "", fmt.Errorf("build infraweave: go list -m: %w", err)
	}
	bin := filepath.Join(dir, "infraweave")
	build := exec.CommandContext(ctx, goBin, "build", "-cover", "-covermode=atomic",
		"-coverpkg="+strings.TrimSpace(string(module))+"/...", "-o", bin, "./cmd/infraweave")
	build.Dir = root
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build infraweave: %w: %s", err, out)
	}
	return bin, nil
}

// Migrate runs bin migrate up against the database at dsn, as an
// operator's migration job does, writing coverage to coverDir.
func Migrate(ctx context.Context, bin, coverDir, dsn string) error {
	dir, err := os.MkdirTemp("", "infraweave-migrate-")
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	defer os.RemoveAll(dir)
	config, err := yaml.Marshal(map[string]any{"database": map[string]any{"url": dsn}})
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, config, 0o600); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	cmd := exec.CommandContext(ctx, bin, "migrate", "up", "--config", path)
	cmd.Env = append(os.Environ(), "GOCOVERDIR="+coverDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("infraweave migrate up: %w: %s", err, out)
	}
	return nil
}

// Server is one infraweave process mode run as a subprocess. It binds its
// listeners on loopback port 0 and reports the ports it took in its
// "listening" log line, so concurrent scenarios never contend for a port.
type Server struct {
	cmd *exec.Cmd
	// API and Admin are the listeners' host:port; API is "" for a mode
	// that serves no API.
	API, Admin string
	// Config is the configuration file the process was given.
	Config         []byte
	stdout, stderr lockedBuffer
	done           chan struct{}
	waitErr        error
}

// lockedBuffer is a buffer the process writes while a scenario reads it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p) //nolint:wrapcheck // bytes.Buffer never fails
}

func (l *lockedBuffer) Bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return bytes.Clone(l.b.Bytes())
}

// KEK is the local KEK every server the harness starts runs with, in
// INFRAWEAVE_SECRETS_KEK: a test key, which seals only test databases.
const KEK = "hP6o+wuSeiPv+itTPyT9Ej7ptu/ap7Ll9sm5e6qab3k="

// ErrExited is returned when the server exits before it is ready.
var ErrExited = errors.New("infraweave exited")

// StartServer writes config to dir/config.yaml and runs bin's process mode
// mode, such as all, with it and KEK, writing coverage to coverDir. It returns once
// the admin listener answers /livez, which decision 0017 puts on the admin
// listener alone.
func StartServer(
	ctx context.Context,
	bin, dir, coverDir, mode string,
	config []byte,
) (*Server, error) {
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, config, 0o600); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}
	s := &Server{Config: config, done: make(chan struct{})}
	// The process outlives the step that starts it; Stop ends it.
	s.cmd = exec.Command(bin, mode, "--config", path) //nolint:noctx // see above
	s.cmd.Env = append(os.Environ(), "GOCOVERDIR="+coverDir, "INFRAWEAVE_SECRETS_KEK="+KEK)
	s.cmd.Stdout, s.cmd.Stderr = &s.stdout, &s.stderr
	if err := s.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start infraweave: %w", err)
	}
	go func() {
		s.waitErr = s.cmd.Wait()
		close(s.done)
	}()
	err := Eventually(ctx, "infraweave to log its listeners", func(context.Context) (bool, error) {
		if s.exited() {
			return false, fmt.Errorf(
				"%w %w: %w: %s",
				errPermanent,
				ErrExited,
				s.waitErr,
				s.stderr.Bytes(),
			)
		}
		return s.listening(), nil
	})
	if err == nil {
		err = Eventually(
			ctx,
			"GET /livez on the admin listener",
			func(ctx context.Context) (bool, error) {
				return s.live(ctx)
			},
		)
	}
	if err != nil {
		return nil, errors.Join(err, s.Stop(ctx))
	}
	return s, nil
}

func (s *Server) exited() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// listening reads the listeners from the JSON log line the process writes
// to stdout once they are bound.
func (s *Server) listening() bool {
	sc := bufio.NewScanner(bytes.NewReader(s.stdout.Bytes()))
	for sc.Scan() {
		var line struct {
			Msg   string `json:"msg"`
			API   string `json:"api"`
			Admin string `json:"admin"`
		}
		if json.Unmarshal(sc.Bytes(), &line) == nil && line.Msg == "listening" {
			s.API, s.Admin = line.API, line.Admin
			return true
		}
	}
	return false
}

func (s *Server) live(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+s.Admin+"/livez", nil)
	if err != nil {
		return false, fmt.Errorf("livez: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("livez: %w", err)
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

// Stop sends SIGTERM, which the server handles by draining, and waits for it
// to exit so its coverage counters are flushed; a server that outlives the
// wait is killed.
func (s *Server) Stop(ctx context.Context) error {
	if s.exited() {
		return nil
	}
	if err := s.cmd.Process.Signal(
		syscall.SIGTERM,
	); err != nil &&
		!errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop infraweave: %w", err)
	}
	err := Eventually(ctx, "infraweave to exit after SIGTERM", func(context.Context) (bool, error) {
		return s.exited(), nil
	})
	if err != nil {
		_ = s.cmd.Process.Kill()
		<-s.done
		return err
	}
	var exit *exec.ExitError
	if s.waitErr != nil && !errors.As(s.waitErr, &exit) {
		return fmt.Errorf("stop infraweave: %w", s.waitErr)
	}
	if exit != nil {
		return fmt.Errorf("%w: %w: %s", ErrExited, exit, s.stderr.Bytes())
	}
	return nil
}

// Stdout is what the process has logged so far, one JSON object per line.
func (s *Server) Stdout() []byte { return s.stdout.Bytes() }

// Pid is the process's ID, which it names itself by on the jobs it claims.
func (s *Server) Pid() int { return s.cmd.Process.Pid }

// Signal sends sig to the process.
func (s *Server) Signal(sig os.Signal) error {
	if err := s.cmd.Process.Signal(sig); err != nil {
		return fmt.Errorf("signal infraweave: %w", err)
	}
	return nil
}

// ExitCode waits for the process to exit and returns its exit code.
func (s *Server) ExitCode(ctx context.Context) (int, error) {
	err := Eventually(ctx, "infraweave to exit", func(context.Context) (bool, error) {
		return s.exited(), nil
	})
	if err != nil {
		return 0, err
	}
	return s.cmd.ProcessState.ExitCode(), nil
}

// Evidence writes the server's output and configuration into dir.
func (s *Server) Evidence(dir string) error {
	var err error
	for name, data := range map[string][]byte{
		"server.stdout": s.stdout.Bytes(),
		"server.stderr": s.stderr.Bytes(),
		"config.yaml":   s.Config,
	} {
		err = errors.Join(err, os.WriteFile(filepath.Join(dir, name), data, 0o600))
	}
	return err
}
