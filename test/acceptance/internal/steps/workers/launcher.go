package workers

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/cucumber/godog"
	"gopkg.in/yaml.v3"

	"github.com/weaveplatform/infraweave/internal/agent/executor/docker"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// launcherSteps run real launchers of a @docker scenario's pools: each an
// infraweave launcher process with coverage, which reaches the server
// through the scenario's relay and runs its runs in containers of
// RunnerImage on the suite's Docker daemon, mounting the Linux build of
// infraweave as their runner. The relay records each call and holds the
// runners' reports on demand, so a run stays on its worker while the
// scenario kills the launcher.
//
// ref: decision 0008 § "The launcher and the runner"
// ref: decision 0016
type launcherSteps struct {
	w *harness.World
	// label names the scenario's forwarder and run containers, which its
	// end removes.
	label string
	// keys are the pool keys by the name of their request, which the
	// worker steps make.
	keys      map[string]crypto.Signer
	relay     *harness.Relay
	network   string
	launchers map[string]*launcher
}

// launcher is one launcher of the scenario: its process, the directory
// that holds its configuration and cache, and the configuration a restart
// starts it with again.
type launcher struct {
	srv    *harness.Server
	dir    string
	config []byte
}

func (l *launcherSteps) register(sc *godog.ScenarioContext) {
	l.launchers = map[string]*launcher{}
	sc.After(l.end)
	sc.Step(`^two infraweave servers, A and B, on one database, with the settings '([^']*)'$`,
		l.servers)
	sc.Step(
		`^token "(\w+)" starts the launcher "(\w+)" of the pool "(\w+)" with the key of "(\w+)" through ([AB])$`,
		l.start,
	)
	sc.Step(`^the launcher "(\w+)" is killed$`, l.kill)
	sc.Step(`^the launcher "(\w+)" starts again and resumes its worker$`, l.restart)
	sc.Step(`^the relay holds the runners' reports$`, func() { l.relay.Hold() })
	sc.Step(`^the relay releases the runners' reports$`, func() { l.relay.Release() })
	sc.Step(`^the relay holds a report of the run "(\w+)"$`, l.holds)
	sc.Step(`^the relay passed "([A-Z]+) ([^"]+)" answered (\d+)$`, l.passed)
	sc.Step(
		`^token "(\w+)" eventually reads the worker "(\w+)" of the pool "(\w+)" as "([A-Z]+)" through ([AB])$`,
		l.worker,
	)
	sc.Step(`^the answer's history is the states "([^"]*)"$`, l.history)
	sc.Step(`^the answer's last state transition's note is "([^"]*)"$`, l.note)
}

// servers starts the scenario's two servers with the top-level sections
// raw names, a JSON object of sections by their key.
func (l *launcherSteps) servers(ctx context.Context, raw string) error {
	var sections map[string]map[string]any
	if err := json.Unmarshal([]byte(raw), &sections); err != nil {
		return fmt.Errorf("%w: the settings %s: %w", harness.ErrStep, raw, err)
	}
	maps.Copy(l.w.Settings, sections)
	return l.w.TwoReplicas(ctx)
}

// start starts a launcher of the pool kept as pool, with the private key
// of the request kept as csr, through the relay to replica, which it
// starts on first use. The pool token token reads names the relay as its
// endpoint, as a pool token names the server a launcher reaches. The
// launcher's worker is kept as name once it registers.
func (l *launcherSteps) start(ctx context.Context, token, name, pool, csr, replica string) error {
	w := l.w
	host, err := w.Suite.Launchers(ctx)
	if err != nil {
		return err
	}
	if l.relay == nil {
		srv := w.Servers[slices.Index([]string{"A", "B"}, replica)]
		if l.relay, err = harness.StartRelay(ctx, srv.API); err != nil {
			return err
		}
		l.label = kernel.NewULID().String()
		if l.network, err = host.Network(ctx, l.label, l.relay.Port); err != nil {
			return err
		}
	}
	poolToken, err := l.poolToken(ctx, token, pool, replica)
	if err != nil {
		return err
	}
	dir := filepath.Join(w.Dir, "launcher-"+name)
	if err := os.MkdirAll(filepath.Join(dir, "cache"), 0o750); err != nil {
		return fmt.Errorf("launcher dir: %w", err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(l.keys[csr])
	if err != nil {
		return fmt.Errorf("%w: the key of %q: %w", harness.ErrStep, csr, err)
	}
	keyFile := filepath.Join(dir, "pool.key")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{
		Type: "PRIVATE KEY", Bytes: key,
	}), 0o600); err != nil {
		return fmt.Errorf("launcher key: %w", err)
	}
	config, err := yaml.Marshal(map[string]any{"launcher": map[string]any{
		"token":                poolToken,
		"private_key":          keyFile,
		"poll_timeout":         "5s",
		"cache_dir":            filepath.Join(dir, "cache"),
		"admin_listen":         "127.0.0.1:0",
		"default_runner_image": harness.RunnerImage,
		"docker": map[string]any{
			"network": l.network, "runner_binary": host.Runner,
		},
	}})
	if err != nil {
		return fmt.Errorf("launcher config: %w", err)
	}
	l.launchers[name] = &launcher{dir: dir, config: config}
	return l.run(ctx, name)
}

// poolToken reads the pool kept as pool through replica with token and
// answers its pool token with the relay as its endpoint.
func (l *launcherSteps) poolToken(
	ctx context.Context,
	token, pool, replica string,
) (string, error) {
	if err := l.w.SendAs(ctx, `token "`+token+`"`, http.MethodGet,
		"/api/v1/worker-pools/"+l.w.IDs[pool], "", replica); err != nil {
		return "", err
	}
	r := l.w.Last()
	var p struct {
		Config string `json:"config"`
	}
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &p) != nil {
		return "", fmt.Errorf("%w: the pool answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	raw, err := base64.StdEncoding.DecodeString(p.Config)
	if err != nil {
		return "", fmt.Errorf("%w: the pool token: %w", harness.ErrStep, err)
	}
	var t map[string]any
	if err := json.Unmarshal(raw, &t); err != nil {
		return "", fmt.Errorf("%w: the pool token: %w", harness.ErrStep, err)
	}
	t["endpoint"] = l.relay.URL
	raw, err = json.Marshal(t)
	if err != nil {
		return "", fmt.Errorf("the pool token: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// run starts the launcher name's process and waits until its worker
// registers, which keeps the worker its cache names as name.
func (l *launcherSteps) run(ctx context.Context, name string) error {
	w, ln := l.w, l.launchers[name]
	host, err := w.Suite.Launchers(ctx)
	if err != nil {
		return err
	}
	bin, err := harness.Binary(ctx)
	if err != nil {
		return err
	}
	if ln.srv, err = harness.StartLauncher(ctx, bin, ln.dir, w.Suite.CoverDir, ln.config,
		host.Host); err != nil {
		return err
	}
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(
		ctx,
		"the launcher "+name+" to register",
		func(ctx context.Context) (bool, error) {
			if ok, err := probe(ctx, "http://"+ln.srv.Admin+"/startupz"); !ok {
				return false, err
			}
			worker, err := l.cached(name)
			if err != nil {
				return false, err
			}
			l.w.IDs[name] = worker
			return true, nil
		},
	)
}

// cached is the worker the launcher name's cache names.
func (l *launcherSteps) cached(name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(l.launchers[name].dir, "cache", "worker.json"))
	if err != nil {
		return "", fmt.Errorf("the launcher's cache: %w", err)
	}
	var st struct {
		Worker string `json:"worker"`
	}
	if err := json.Unmarshal(b, &st); err != nil || st.Worker == "" {
		return "", fmt.Errorf("%w: the launcher's cache holds %s", harness.ErrStep, b)
	}
	return st.Worker, nil
}

// probe reports whether a GET of url answers 200.
func probe(ctx context.Context, url string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Errorf("probe: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("probe: %w", err)
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

// kill ends the launcher name with SIGKILL, so it neither deregisters nor
// stops its run's container.
func (l *launcherSteps) kill(ctx context.Context, name string) error {
	srv := l.launchers[name].srv
	if err := srv.Signal(syscall.SIGKILL); err != nil {
		return err
	}
	_, err := srv.ExitCode(ctx)
	return err
}

// restart starts the launcher name again on its configuration and cache,
// and checks that it resumed the worker it had.
func (l *launcherSteps) restart(ctx context.Context, name string) error {
	before := l.w.IDs[name]
	if err := l.run(ctx, name); err != nil {
		return err
	}
	if got := l.w.IDs[name]; got != before {
		return fmt.Errorf("%w: the launcher registered the worker %s, not %s", harness.ErrStep, got,
			before)
	}
	return nil
}

func (l *launcherSteps) holds(ctx context.Context, run string) error {
	id := l.w.IDs[run]
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(
		ctx,
		"the relay to hold a report of "+run,
		func(context.Context) (bool, error) {
			return l.relay.Held(id) > 0, nil
		},
	)
}

// passed checks that the relay passed a call of method to path, with the
// scenario's keys resolved, that answered status.
func (l *launcherSteps) passed(method, path string, status int) error {
	path = l.w.Resolve(path)
	calls := l.relay.Calls()
	if slices.Contains(calls, harness.Call{Method: method, Path: path, Status: status}) {
		return nil
	}
	return fmt.Errorf(
		"%w: the relay passed no %s %s answered %d: %+v",
		harness.ErrStep,
		method,
		path,
		status,
		calls,
	)
}

// worker reads the workers of the pool kept as pool through replica with
// token until the worker kept as name has status, or, for GONE, until the
// pool has no such worker.
func (l *launcherSteps) worker(
	ctx context.Context,
	token, name, pool, status, replica string,
) error {
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(
		ctx,
		"the worker "+name+" to be "+status,
		func(ctx context.Context) (bool, error) {
			if err := l.w.SendAs(ctx, `token "`+token+`"`, http.MethodGet,
				"/api/v1/worker-pools/"+l.w.IDs[pool]+"/search-workers?input=%7B%7D", "",
				replica); err != nil {
				return false, err
			}
			r := l.w.Last()
			var page struct {
				Edges []struct {
					Node struct {
						ID     string `json:"id"`
						Status string `json:"status"`
					} `json:"node"`
				} `json:"edges"`
			}
			if r.Status != http.StatusOK || json.Unmarshal(r.Body, &page) != nil {
				return false, fmt.Errorf("%w: answered %d %s", harness.ErrStep, r.Status, r.Body)
			}
			for _, e := range page.Edges {
				if e.Node.ID == l.w.IDs[name] {
					return e.Node.Status == status, nil
				}
			}
			return status == "GONE", nil
		},
	)
}

// transitions are the last answer's, a run's, history, oldest first.
func (l *launcherSteps) transitions() ([]struct {
	State string  `json:"state"`
	Note  *string `json:"note"`
}, error,
) {
	var run struct {
		History []struct {
			State string  `json:"state"`
			Note  *string `json:"note"`
		} `json:"history"`
	}
	r := l.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &run) != nil || len(run.History) == 0 {
		return nil, fmt.Errorf("%w: no run's history in %d %s", harness.ErrStep, r.Status, r.Body)
	}
	return run.History, nil
}

// history checks the states of the last answer's history, a run's, oldest
// first, against want, comma-separated.
func (l *launcherSteps) history(want string) error {
	h, err := l.transitions()
	if err != nil {
		return err
	}
	states := make([]string, len(h))
	for i, t := range h {
		states[i] = t.State
	}
	if got := strings.Join(states, ", "); got != want {
		return fmt.Errorf("%w: the history is %s, want %s", harness.ErrStep, got, want)
	}
	return nil
}

func (l *launcherSteps) note(want string) error {
	h, err := l.transitions()
	if err != nil {
		return err
	}
	if last := h[len(h)-1]; last.Note == nil || *last.Note != want {
		return fmt.Errorf("%w: the last transition is %+v", harness.ErrStep, last)
	}
	return nil
}

// end stops the scenario's launchers, leaving the evidence of a failed
// scenario beside the servers', removes the containers its launchers and
// forwarder started, and closes the relay.
func (l *launcherSteps) end(ctx context.Context, _ *godog.Scenario, failed error) (context.Context,
	error,
) {
	ctx = context.WithoutCancel(ctx)
	if l.relay == nil {
		return ctx, nil
	}
	w := l.w
	host, err := w.Suite.Launchers(ctx)
	for name, ln := range l.launchers {
		if ln.srv == nil {
			continue
		}
		if stopErr := ln.srv.Stop(ctx); stopErr != nil && !errors.Is(stopErr, harness.ErrExited) {
			err = errors.Join(err, stopErr)
		}
		if failed != nil {
			dir := filepath.Join(w.Suite.Out, harness.Slug(w.Name), "launcher-"+name)
			err = errors.Join(err, os.MkdirAll(dir, 0o750), ln.srv.Evidence(dir))
		}
		if worker := l.w.IDs[name]; worker != "" && err == nil {
			err = host.Remove(ctx, docker.LabelWorker, worker)
		}
	}
	if failed != nil {
		calls, _ := json.MarshalIndent(l.relay.Calls(), "", "  ")
		dir := filepath.Join(w.Suite.Out, harness.Slug(w.Name))
		err = errors.Join(err, os.MkdirAll(dir, 0o750),
			os.WriteFile(filepath.Join(dir, "relay.json"), calls, 0o600))
	}
	if err == nil {
		err = host.Remove(ctx, harness.LabelScenario, l.label)
	}
	return ctx, errors.Join(err, l.relay.Close(ctx))
}
