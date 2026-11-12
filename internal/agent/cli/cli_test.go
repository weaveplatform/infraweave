package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/agenttest"
	"github.com/weaveplatform/infraweave/internal/agent/cli"
	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/config"
	"github.com/weaveplatform/infraweave/internal/agent/handoff"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// engine is a fake executor that Docker always answers for.
type engine struct{ *agenttest.Executor }

func (engine) Ping(context.Context) error { return nil }
func (engine) Close() error               { return nil }

// pool sets the environment of a launcher of a fresh pool, and answers the
// pool's fake worker API.
func pool(t *testing.T) *agenttest.Server {
	t.Helper()
	p, err := agenttest.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("INFRAWEAVE_LAUNCHER_TOKEN", p.Token)
	t.Setenv("INFRAWEAVE_LAUNCHER_PRIVATE_KEY", string(p.Key))
	t.Setenv("INFRAWEAVE_LAUNCHER_CACHE_DIR", t.TempDir())
	return agenttest.NewServer(p)
}

func fake(config.Docker) (cli.Engine, error) { return engine{agenttest.NewExecutor()}, nil }

// logged is the launcher's log, a JSON object a line, as it is written.
func logged(r io.Reader) <-chan map[string]any {
	out := make(chan map[string]any, 1024)
	go func() {
		defer close(out)
		s := bufio.NewScanner(r)
		for s.Scan() {
			var m map[string]any
			if json.Unmarshal(s.Bytes(), &m) == nil {
				select {
				case out <- m:
				default:
				}
			}
		}
	}()
	return out
}

// await reads the log until msg, and answers that record.
func await(t *testing.T, log <-chan map[string]any, msg string) map[string]any {
	t.Helper()
	for m := range log {
		if m["msg"] == msg {
			return m
		}
	}
	t.Fatalf("the log ended before %q", msg)
	return nil
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(b)
}

func TestUnitLauncherServesItsProbes(t *testing.T) {
	srv := pool(t)
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
	admin, _ := await(t, log, "listening")["admin"].(string)
	await(t, log, "registered")
	if code, body := get(t, "http://"+admin+"/livez"); code != http.StatusOK || body != "ok\n" {
		t.Fatalf("/livez: %d %q", code, body)
	}
	for _, probe := range []string{"/startupz", "/readyz"} {
		if code, body := get(t, "http://"+admin+probe+"?verbose"); code != http.StatusOK {
			t.Fatalf("%s: %d %s", probe, code, body)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("launcher: %v", err)
	}
	if got := srv.Deregistered(); !slices.Equal(got, []string{"worker-1"}) {
		t.Fatalf("deregistered: %v", got)
	}
}

func TestUnitLauncherFailsToStart(t *testing.T) {
	busy, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = busy.Close() }()
	unknown := filepath.Join(t.TempDir(), "launcher.yaml")
	if err := os.WriteFile(unknown, []byte("launcher:\n  bogus: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		args   []string
		env    map[string]string
		engine func(config.Docker) (cli.Engine, error)
		want   string
	}{
		"an unknown key": {args: []string{"--config", unknown}, want: "bogus"},
		"a bad log format": {
			env: map[string]string{"INFRAWEAVE_LAUNCHER_LOG_FORMAT": "xml"}, want: "log",
		},
		"no Docker": {
			engine: func(config.Docker) (cli.Engine, error) { return nil, errors.New("no socket") },
			want:   "no socket",
		},
		"a bad pool token":  {env: map[string]string{"INFRAWEAVE_LAUNCHER_TOKEN": "%%"}, want: "pool token"},
		"a busy admin port": {args: []string{"--admin-listen", busy.Addr().String()}, want: "admin_listen"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := pool(t)
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			if c.engine == nil {
				c.engine = fake
			}
			cmd := cli.LauncherCmd(srv.Client(), c.engine)
			cmd.SetArgs(c.args)
			cmd.SetOut(io.Discard)
			err := cmd.ExecuteContext(t.Context())
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("start: %v, want %q", err, c.want)
			}
			if len(srv.Registrations()) != 0 {
				t.Fatal("a launcher that failed to start registered")
			}
		})
	}
}

func TestUnitRunnerCommand(t *testing.T) {
	p, err := agenttest.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	srv := agenttest.NewServer(p)
	a := wire.Assignment{
		Run:          "run-1",
		RunToken:     "token",
		State:        wire.AssignmentStatePREPARING,
		StateVersion: 1,
	}
	if err := srv.Assign(agenttest.Run{Assignment: a}); err != nil {
		t.Fatal(err)
	}
	var in, out bytes.Buffer
	if err := handoff.Write(&in, handoff.Handoff{
		Endpoint: client.Base(agenttest.Endpoint), Run: a.Run, RunToken: a.RunToken,
		Assigned: a.State, StateVersion: a.StateVersion,
	}); err != nil {
		t.Fatal(err)
	}
	cmd := cli.RunnerCmd(srv.Client())
	cmd.SetIn(&in)
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := len(srv.Reports()); n != 2 || !strings.Contains(out.String(), `"msg":"run ended"`) {
		t.Fatalf("%d reports, output %s", n, out.String())
	}
	if !cli.Runner().Hidden || cli.Launcher().Use != "launcher" {
		t.Fatal("the runner is hidden, and the launcher is not")
	}
}
