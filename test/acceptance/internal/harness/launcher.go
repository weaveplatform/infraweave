package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"

	"github.com/weaveplatform/infraweave/internal/agent/executor/docker"
	"github.com/weaveplatform/infraweave/internal/platform/runnerimages"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
)

// RunnerImage is the shared, immutable shell-free fixture. The current
// application and forwarder executables are mounted by each suite.
const RunnerImage = runnerimages.TestSupport

// LabelScenario labels the containers a scenario starts beside its
// launchers', which its end removes.
const LabelScenario = "io.infraweave.acceptance"

// ErrDockerArch is returned for a Docker daemon of an architecture Go
// builds for under another name than the suite knows.
var ErrDockerArch = errors.New("no GOARCH for the Docker daemon's architecture")

// Launchers are what a @docker scenario's launchers need of Docker, made
// once per test binary: the daemon, RunnerImage, the Linux builds of
// infraweave and of testdata/forward for the daemon's architecture, and
// the runner images, each pulled on first use and retained for other suites.
type Launchers struct {
	// Host is the daemon's address, which each launcher is given as
	// DOCKER_HOST, so it reaches the daemon the suite found.
	Host string
	// Runner is a statically linked Linux build of infraweave, which each
	// launcher mounts into its run containers as launcher.docker.runner_binary.
	Runner string
	// Forward is testdata/forward's Linux build.
	Forward string
	// Arch is the daemon's architecture as GOARCH names it, which a
	// binary its containers run is built or fetched for.
	Arch string
	cli  *testcontainers.DockerClient
	// ready holds successfully inspected or pulled immutable image references.
	mu    sync.Mutex
	ready map[string]bool
	// mirror is the tool mirror, once Mirror starts it. Its lock is its
	// own, so a build never holds up a download.
	mirrorMu sync.Mutex
	mirror   *mirror
	// stopCaps stops capRuns's watch, which capped waits for; capErrs is
	// what it could not cap.
	stopCaps context.CancelFunc
	capped   sync.WaitGroup
	capErrs  error
	bootstrapState
}

// prepare builds what the launchers need into dir, from the module at
// root, and pulls RunnerImage when absent.
func (l *Launchers) prepare(ctx context.Context, root, dir string) error {
	cli, err := testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		return fmt.Errorf("docker: %w", err)
	}
	l.cli, l.Host = cli, cli.DaemonHost()
	info, err := cli.Info(ctx, client.InfoOptions{})
	if err != nil {
		return fmt.Errorf("docker: %w", err)
	}
	arch := map[string]string{"x86_64": "amd64", "aarch64": "arm64"}[info.Info.Architecture]
	if arch == "" {
		return fmt.Errorf("%w: %q", ErrDockerArch, info.Info.Architecture)
	}
	l.Arch = arch
	if l.Runner, err = buildLinux(ctx, root, dir, arch, "./cmd/infraweave"); err != nil {
		return err
	}
	if l.Forward, err = buildLinux(ctx, root, dir, arch,
		"./test/acceptance/testdata/forward"); err != nil {
		return err
	}
	return l.EnsureRunnerImage(ctx, RunnerImage)
}

// buildLinux builds pkg statically for linux/arch into dir. The runner
// writes no coverage: a run container has no GOCOVERDIR.
func buildLinux(ctx context.Context, root, dir, arch, pkg string) (string, error) {
	bin := filepath.Join(dir, "linux-"+arch, filepath.Base(pkg))
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, pkg)
	build.Dir = root
	build.Env = append(os.Environ(), "GOWORK=off", "GOOS=linux", "GOARCH="+arch,
		"CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build %s for linux/%s: %w: %s", pkg, arch, err, out)
	}
	return bin, nil
}

// Network is the launcher.docker.network that puts a run container where
// 127.0.0.1:port reaches what listens there on the host, for each of
// ports, such as the relay. On Linux the daemon is the host's, and the
// host network is that place. Elsewhere the daemon runs in a virtual
// machine, so a container of the scenario, labelled scenario, runs
// testdata/forward from 127.0.0.1:port to the port as
// host.docker.internal names it, and each run container joins its network
// namespace.
func (l *Launchers) Network(ctx context.Context, scenario string, ports ...string) (string, error) {
	if runtime.GOOS == "linux" {
		return "host", nil
	}
	pairs := make([]string, 0, 2*len(ports))
	for _, port := range ports {
		pairs = append(pairs, "127.0.0.1:"+port, "host.docker.internal:"+port)
	}
	c, err := l.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:      RunnerImage,
			Entrypoint: []string{"/opt/forward"},
			Cmd:        pairs,
			Labels:     map[string]string{LabelScenario: scenario},
		},
		HostConfig: &container.HostConfig{
			Binds: []string{l.Forward + ":/opt/forward:ro"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("the relay's forwarder: %w", err)
	}
	if _, err := l.cli.ContainerStart(ctx, c.ID, client.ContainerStartOptions{}); err != nil {
		return "", fmt.Errorf("the relay's forwarder: %w", err)
	}
	return "container:" + c.ID, nil
}

// RunImage reads the image used to create run's container. A scenario holds
// the runner's report until this read, so the launcher cannot remove it first.
func (l *Launchers) RunImage(ctx context.Context, run string) (string, error) {
	list, err := l.cli.ContainerList(ctx, client.ContainerListOptions{
		All: true, Filters: client.Filters{}.Add("label", docker.LabelRun+"="+run),
	})
	if err != nil {
		return "", fmt.Errorf("docker: find run %s: %w", run, err)
	}
	if len(list.Items) != 1 {
		return "", fmt.Errorf("%w: run %s has %d containers, want one", ErrStep, run, len(list.Items))
	}
	c, err := l.cli.ContainerInspect(ctx, list.Items[0].ID, client.ContainerInspectOptions{})
	if err != nil {
		return "", fmt.Errorf("docker: inspect run %s: %w", run, err)
	}
	return c.Container.Config.Image, nil
}

// RunContainerCount verifies run's actual Docker inventory, including stopped
// containers. A daemon failure cannot stand in for resource absence.
func (l *Launchers) RunContainerCount(ctx context.Context, run string) (int, error) {
	list, err := l.cli.ContainerList(ctx, client.ContainerListOptions{
		All: true, Filters: client.Filters{}.Add("label", docker.LabelRun+"="+run),
	})
	if err != nil {
		return 0, fmt.Errorf("docker: count run containers: %w", err)
	}
	return len(list.Items), nil
}

// Remove removes every container labelled with the label key=value, run
// containers and forwarders alike, running or not. Runs end and hand their
// workspace directories back before removal. A container already
// gone, or whose removal is in progress, such as one its launcher removes
// as it stops, counts as removed.
func (l *Launchers) Remove(ctx context.Context, key, value string) error {
	return removeContainers(ctx, l.cli, key, value)
}

// Close stops the tool mirror and run-container caps and closes this
// owner's client. Shared immutable images remain cached for other suites.
func (l *Launchers) Close(ctx context.Context) error {
	if l.mirror != nil {
		l.mirror.srv.Close()
	}
	if l.stopCaps != nil {
		l.stopCaps()
		l.capped.Wait()
	}
	if l.cli == nil {
		return l.capErrs
	}
	return errors.Join(l.capErrs, l.cli.Close())
}

// Call is one request a relay passed on and the status it answered.
type Call struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Status int    `json:"status"`
}

// Refusal is a call a relay passed on that answered 423, with the body it
// answered: the LockInfo of the lock that refused it, or {}.
type Refusal struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   string `json:"body"`
}

// The routes a relay holds calls of: a runner's reports, keyed by run,
// and the state backend's UNLOCK and POST, keyed by path.
const (
	reportsPath = "/worker/v1/runs/{run}/reports"
	statePath   = "/state/v1/*"
)

// init registers the state backend's lock methods with chi, which routes
// only the methods it knows. RegisterMethod writes a map every chi router
// reads, so it runs here, before any scenario's goroutine starts a relay
// or serves a fake.
func init() {
	chi.RegisterMethod(stateapi.MethodLock)
	chi.RegisterMethod(stateapi.MethodUnlock)
}

// maxRefusal bounds a 423's body the relay keeps, above the 16 KiB a
// LockInfo the state backend stores may take.
const maxRefusal = 64 << 10

// Relay is the worker API and the state backend as a scenario's launchers,
// its run containers and its tools reach them: a reverse proxy on loopback
// to a server's API listener, which records each call and the body of
// each 423. While it holds reports, each runner's report waits until it
// releases them or the report's caller gives up; while it holds unlocks,
// each UNLOCK of the state backend waits the same way, so the lock it
// would release stays held; while it holds writes, each POST of a state
// version waits the same way, so its tool holds its lock with the write
// still to come.
type Relay struct {
	// URL is the relay's base URL; Port is its port.
	URL, Port   string
	srv         *http.Server
	mu          sync.Mutex
	calls       []Call
	refusals    []Refusal
	refusePlans bool
	reports     *gate
	unlocks     *gate
	writes      *gate
}

// RefusePlanUploads injects a permanent declaration refusal for the actual runner.
func (r *Relay) RefusePlanUploads() { r.mu.Lock(); defer r.mu.Unlock(); r.refusePlans = true }

// StartRelay starts a relay to the API listener at target, a host:port.
func StartRelay(ctx context.Context, target string) (*Relay, error) {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("relay: %w", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	r := &Relay{
		URL: "http://" + ln.Addr().String(), Port: port, reports: newGate(), unlocks: newGate(),
		writes: newGate(),
	}
	to := &url.URL{Scheme: "http", Host: target}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) { pr.SetURL(to) },
		ModifyResponse: func(resp *http.Response) error {
			req := resp.Request
			r.record(req.Method, req.URL.Path, resp.StatusCode)
			if resp.StatusCode != http.StatusLocked {
				return nil
			}
			body, err := io.ReadAll(io.LimitReader(resp.Body, maxRefusal))
			resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(body))
			r.refused(Refusal{Method: req.Method, Path: req.URL.Path, Body: string(body)})
			return err
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, _ error) {
			r.record(req.Method, req.URL.Path, http.StatusBadGateway)
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	mux := chi.NewRouter()
	mux.Post(reportsPath, func(w http.ResponseWriter, req *http.Request) {
		if r.reports.wait(req.Context(), chi.URLParam(req, "run")) {
			proxy.ServeHTTP(w, req)
		}
	})
	mux.Method(stateapi.MethodUnlock, statePath, http.HandlerFunc(
		func(w http.ResponseWriter, req *http.Request) {
			if r.unlocks.wait(req.Context(), req.URL.Path) {
				proxy.ServeHTTP(w, req)
			}
		}))
	mux.Post(statePath, func(w http.ResponseWriter, req *http.Request) {
		if r.writes.wait(req.Context(), req.URL.Path) {
			proxy.ServeHTTP(w, req)
		}
	})
	mux.Post("/worker/v1/runs/{run}/upload-urls", func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		r.mu.Lock()
		refuse := r.refusePlans
		r.mu.Unlock()
		if refuse {
			var document struct {
				Uploads []struct {
					Name string `json:"name"`
				} `json:"uploads"`
			}
			if json.Unmarshal(body, &document) == nil {
				for _, upload := range document.Uploads {
					if upload.Name == "plan.json" {
						r.record(req.Method, req.URL.Path, http.StatusForbidden)
						w.WriteHeader(http.StatusForbidden)
						return
					}
				}
			}
		}
		proxy.ServeHTTP(w, req)
	})
	mux.Handle("/*", proxy)
	r.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = r.srv.Serve(ln) }()
	return r, nil
}

func (r *Relay) record(method, path string, status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, Call{Method: method, Path: path, Status: status})
}

func (r *Relay) refused(f Refusal) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refusals = append(r.refusals, f)
}

// Hold makes the relay hold the runners' reports from now on.
func (r *Relay) Hold() { r.reports.shut() }

// Release passes the held reports on, and later ones at once.
func (r *Relay) Release() { r.reports.open() }

// Held reports how many of run's reports the relay holds.
func (r *Relay) Held(run string) int { return r.reports.count(run) }

// HoldUnlocks makes the relay hold the state backend's UNLOCK calls from
// now on.
func (r *Relay) HoldUnlocks() { r.unlocks.shut() }

// ReleaseUnlocks passes the held UNLOCK calls on, and later ones at once.
func (r *Relay) ReleaseUnlocks() { r.unlocks.open() }

// HeldUnlocks reports how many UNLOCK calls of the backend at path the
// relay holds.
func (r *Relay) HeldUnlocks(path string) int { return r.unlocks.count(path) }

// HoldWrites makes the relay hold the state backend's POST calls from now
// on.
func (r *Relay) HoldWrites() { r.writes.shut() }

// ReleaseWrites passes the held POST calls on, and later ones at once.
func (r *Relay) ReleaseWrites() { r.writes.open() }

// HeldWrites reports how many POST calls of the backend at path the relay
// holds.
func (r *Relay) HeldWrites(path string) int { return r.writes.count(path) }

// Calls are the calls the relay passed on so far, in the order answered.
func (r *Relay) Calls() []Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Call(nil), r.calls...)
}

// Refusals are the calls the relay passed on that answered 423, in the
// order answered.
func (r *Relay) Refusals() []Refusal {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Refusal(nil), r.refusals...)
}

// Close releases what it holds and stops the relay.
func (r *Relay) Close(ctx context.Context) error {
	r.reports.open()
	r.unlocks.open()
	r.writes.open()
	if err := r.srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("relay: %w", err)
	}
	return nil
}

// gate holds calls while it is shut, each under a key, until it opens or
// a call's caller gives up.
type gate struct {
	mu      sync.Mutex
	holding bool
	held    map[string]int
	release chan struct{}
}

func newGate() *gate { return &gate{held: map[string]int{}, release: make(chan struct{})} }

// wait holds a call under key while the gate is shut, and reports whether
// to pass it on: false when its caller gave up first.
func (g *gate) wait(ctx context.Context, key string) bool {
	g.mu.Lock()
	if !g.holding {
		g.mu.Unlock()
		return true
	}
	g.held[key]++
	release := g.release
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		g.held[key]--
		g.mu.Unlock()
	}()
	select {
	case <-release:
		return true
	case <-ctx.Done():
		return false
	}
}

func (g *gate) shut() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.holding = true
}

func (g *gate) open() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.holding {
		g.holding = false
		close(g.release)
		g.release = make(chan struct{})
	}
}

func (g *gate) count(key string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.held[key]
}
