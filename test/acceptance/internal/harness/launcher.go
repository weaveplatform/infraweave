package harness

import (
	"archive/tar"
	"bytes"
	"context"
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
)

// RunnerImage is the image of a @docker scenario's run containers: a
// filesystem with the user runner, UID and GID 1983, and the workspace it
// owns, and nothing else, so no scenario pulls an image. The launcher
// mounts the runner itself.
//
// ref: decision 0008 § "The launcher and the runner"
const RunnerImage = "infraweave-runner:acceptance"

// LabelScenario labels the containers a scenario starts beside its
// launchers', which its end removes.
const LabelScenario = "io.infraweave.acceptance"

// ErrDockerArch is returned for a Docker daemon of an architecture Go
// builds for under another name than the suite knows.
var ErrDockerArch = errors.New("no GOARCH for the Docker daemon's architecture")

// Launchers are what a @docker scenario's launchers need of Docker, made
// once per test binary: the daemon, RunnerImage, and the Linux builds of
// infraweave and of testdata/forward for the daemon's architecture.
type Launchers struct {
	// Host is the daemon's address, which each launcher is given as
	// DOCKER_HOST, so it reaches the daemon the suite found.
	Host string
	// Runner is a statically linked Linux build of infraweave, which each
	// launcher mounts into its run containers as launcher.docker.runner_binary.
	Runner string
	// Forward is testdata/forward's Linux build.
	Forward string
	cli     *testcontainers.DockerClient
}

// prepare builds what the launchers need into dir, from the module at
// root, and imports RunnerImage.
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
	if l.Runner, err = buildLinux(ctx, root, dir, arch, "./cmd/infraweave"); err != nil {
		return err
	}
	if l.Forward, err = buildLinux(ctx, root, dir, arch,
		"./test/acceptance/testdata/forward"); err != nil {
		return err
	}
	return importRunnerImage(ctx, cli)
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

// importRunnerImage imports RunnerImage from a filesystem made here,
// which needs no registry and no builder.
func importRunnerImage(ctx context.Context, cli *testcontainers.DockerClient) error {
	var root bytes.Buffer
	tw := tar.NewWriter(&root)
	for _, h := range []tar.Header{
		{Name: "etc/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "mnt/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "mnt/workspace/", Typeflag: tar.TypeDir, Mode: 0o755, Uid: 1983, Gid: 1983},
		{Name: "opt/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "tmp/", Typeflag: tar.TypeDir, Mode: 0o1777},
	} {
		if err := tw.WriteHeader(&h); err != nil {
			return fmt.Errorf("runner image: %w", err)
		}
	}
	for name, body := range map[string]string{
		"etc/passwd": "root:x:0:0:root:/:/sbin/nologin\n" +
			"runner:x:1983:1983:runner:/mnt/workspace:/sbin/nologin\n",
		"etc/group": "root:x:0:\nrunner:x:1983:\n",
	} {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body)),
		}); err != nil {
			return fmt.Errorf("runner image: %w", err)
		}
		if _, err := io.WriteString(tw, body); err != nil {
			return fmt.Errorf("runner image: %w", err)
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("runner image: %w", err)
	}
	res, err := cli.ImageImport(ctx, client.ImageImportSource{Source: &root, SourceName: "-"},
		RunnerImage, client.ImageImportOptions{
			Changes: []string{"USER runner", "WORKDIR /mnt/workspace"},
		})
	if err != nil {
		return fmt.Errorf("runner image: %w", err)
	}
	defer res.Close()
	if _, err := io.Copy(io.Discard, res); err != nil {
		return fmt.Errorf("runner image: %w", err)
	}
	return nil
}

// Network is the launcher.docker.network that puts a run container where
// 127.0.0.1:port reaches the relay listening there on the host. On Linux
// the daemon is the host's, and the host network is that place. Elsewhere
// the daemon runs in a virtual machine, so a container of the scenario,
// labelled scenario, runs testdata/forward from 127.0.0.1:port to the
// relay as host.docker.internal names it, and each run container joins
// its network namespace.
func (l *Launchers) Network(ctx context.Context, scenario, port string) (string, error) {
	if runtime.GOOS == "linux" {
		return "host", nil
	}
	c, err := l.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:      RunnerImage,
			Entrypoint: []string{"/opt/forward"},
			Cmd:        []string{"127.0.0.1:" + port, "host.docker.internal:" + port},
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

// Remove removes every container labelled with the label key=value, run
// containers and forwarders alike, running or not.
func (l *Launchers) Remove(ctx context.Context, key, value string) error {
	list, err := l.cli.ContainerList(ctx, client.ContainerListOptions{
		All: true, Filters: client.Filters{}.Add("label", key+"="+value),
	})
	if err != nil {
		return fmt.Errorf("docker: %w", err)
	}
	var errs error
	for _, c := range list.Items {
		_, err := l.cli.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true})
		errs = errors.Join(errs, err)
	}
	if errs != nil {
		return fmt.Errorf("docker: %w", errs)
	}
	return nil
}

// Close removes RunnerImage and closes the client.
func (l *Launchers) Close(ctx context.Context) error {
	if l.cli == nil {
		return nil
	}
	_, err := l.cli.ImageRemove(ctx, RunnerImage, client.ImageRemoveOptions{Force: true})
	return errors.Join(err, l.cli.Close())
}

// Call is one request a relay passed on and the status it answered.
type Call struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Status int    `json:"status"`
}

// reportsPath is the worker API's route a runner reports on.
const reportsPath = "/worker/v1/runs/{run}/reports"

// Relay is the worker API as a scenario's launchers and their run
// containers reach it: a reverse proxy on loopback to a server's API
// listener, which records each call and, while it holds, keeps each
// runner's report waiting until it releases them or the report's caller
// gives up.
type Relay struct {
	// URL is the relay's base URL; Port is its port.
	URL, Port string
	srv       *http.Server
	mu        sync.Mutex
	calls     []Call
	holding   bool
	held      map[string]int
	release   chan struct{}
}

// StartRelay starts a relay to the API listener at target, a host:port.
func StartRelay(ctx context.Context, target string) (*Relay, error) {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("relay: %w", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	r := &Relay{
		URL: "http://" + ln.Addr().String(), Port: port, held: map[string]int{},
		release: make(chan struct{}),
	}
	to := &url.URL{Scheme: "http", Host: target}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) { pr.SetURL(to) },
		ModifyResponse: func(resp *http.Response) error {
			r.record(resp.Request.Method, resp.Request.URL.Path, resp.StatusCode)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, _ error) {
			r.record(req.Method, req.URL.Path, http.StatusBadGateway)
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	mux := chi.NewRouter()
	mux.Post(reportsPath, func(w http.ResponseWriter, req *http.Request) {
		if !r.wait(req.Context(), chi.URLParam(req, "run")) {
			return
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

// wait holds a report of run while the relay holds, and reports whether
// to pass it on: false when its caller gave up first.
func (r *Relay) wait(ctx context.Context, run string) bool {
	r.mu.Lock()
	if !r.holding {
		r.mu.Unlock()
		return true
	}
	r.held[run]++
	release := r.release
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.held[run]--
		r.mu.Unlock()
	}()
	select {
	case <-release:
		return true
	case <-ctx.Done():
		return false
	}
}

// Hold makes the relay hold the runners' reports from now on.
func (r *Relay) Hold() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.holding = true
}

// Release passes the held reports on, and later ones at once.
func (r *Relay) Release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.holding {
		r.holding = false
		close(r.release)
		r.release = make(chan struct{})
	}
}

// Held reports how many of run's reports the relay holds.
func (r *Relay) Held(run string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.held[run]
}

// Calls are the calls the relay passed on so far, in the order answered.
func (r *Relay) Calls() []Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Call(nil), r.calls...)
}

// Close releases what it holds and stops the relay.
func (r *Relay) Close(ctx context.Context) error {
	r.Release()
	if err := r.srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("relay: %w", err)
	}
	return nil
}
