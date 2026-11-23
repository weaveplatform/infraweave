package launcher_test

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"

	"github.com/weaveplatform/infraweave/internal/agent/agenttest"
	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/config"
	"github.com/weaveplatform/infraweave/internal/agent/executor"
	"github.com/weaveplatform/infraweave/internal/agent/handoff"
	"github.com/weaveplatform/infraweave/internal/agent/initcommand"
	"github.com/weaveplatform/infraweave/internal/agent/initpolicy"
	"github.com/weaveplatform/infraweave/internal/agent/launcher"
	"github.com/weaveplatform/infraweave/internal/agent/source"
	"github.com/weaveplatform/infraweave/internal/agent/tools"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/agent/workspace"
	"github.com/weaveplatform/infraweave/internal/platform/observe"
)

// logs is the launcher's log, which its goroutines write together.
type logs struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// world is a pool's fake worker API and a fake executor, which launchers
// of the pool share across restarts through one cache directory.
type world struct {
	pool    agenttest.Pool
	srv     *agenttest.Server
	exec    *agenttest.Executor
	sources *fakeSources
	tools   *fakeTools
	dir     string
	log     *logs
	image   string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	pool, err := agenttest.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	return &world{
		pool: pool, srv: agenttest.NewServer(pool), exec: agenttest.NewExecutor(),
		sources: &fakeSources{}, tools: &fakeTools{}, dir: t.TempDir(), log: &logs{},
	}
}

func (w *world) config() config.Launcher {
	c := config.Defaults().Launcher
	c.Token, c.PrivateKey, c.CacheDir = w.pool.Token, observe.Secret(w.pool.Key), w.dir
	c.DefaultRunnerImage = w.image
	c.Metadata = map[string]string{"zone": "a"}
	c.MaskEnvs = []string{"DB_PASSWORD"}
	c.SensitiveOutputUpload = true
	c.MaxRequestsPerSecond, c.MaxRequestsBurst = 17, 170
	return c
}

func (w *world) launcher(t *testing.T) *launcher.Launcher {
	t.Helper()
	l, err := launcher.New(launcher.Deps{
		Config: w.config(), Doer: w.srv.Client(), Executor: w.exec, Sources: w.sources,
		Tools:   w.tools,
		Log:     slog.New(slog.NewTextHandler(w.log, nil)),
		Version: "1.2.3", OS: "linux", Arch: "arm64",
	})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// fakeSources writes main.tf into each workspace, fetched or restored,
// or answers err.
type fakeSources struct {
	mu       sync.Mutex
	dirs     []string
	creds    []*wire.RunSpecSourceCredential
	restored []string
	// errs fail the first fetches and restores, in order, and err every
	// later one.
	errs []error
	err  error
}

func (f *fakeSources) next() error {
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		return err
	}
	return f.err
}

func (f *fakeSources) Fetch(_ context.Context, dir string, _ wire.RunSpec, cred *wire.RunSpecSourceCredential) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dirs, f.creds = append(f.dirs, dir), append(f.creds, cred)
	if err := os.MkdirAll(filepath.Join(dir, source.Checkout), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, source.Checkout, "main.tf"), nil, 0o600); err != nil {
		return err
	}
	return f.next()
}

func (f *fakeSources) Restore(_ context.Context, dir, src string, pool *ecdh.PrivateKey, _ string, _ wire.RunSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pool == nil {
		return errNoPool
	}
	f.restored = append(f.restored, dir+" from "+src)
	if err := os.MkdirAll(filepath.Join(dir, source.Checkout), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, source.Checkout, "main.tf"), nil, 0o600); err != nil {
		return err
	}
	return f.next()
}

var errNoPool = errors.New("no pool key")

func (f *fakeSources) fetched() ([]string, []*wire.RunSpecSourceCredential) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.dirs), slices.Clone(f.creds)
}

// fakeTools answers each release's binary under /cache, or err.
type fakeTools struct {
	mu  sync.Mutex
	got []tools.Release
	err error
}

func (f *fakeTools) Binary(_ context.Context, r tools.Release) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, r)
	if f.err != nil {
		return "", f.err
	}
	return "/cache/" + string(r.Tool) + "/" + r.Version + "/" + r.OS + "_" + r.Arch, nil
}

func (f *fakeTools) fetched() []tools.Release {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.got)
}

// running is a launcher's Run, until the test stops it.
type running struct {
	l      *launcher.Launcher
	cancel context.CancelFunc
	done   chan error
}

// start runs a launcher and waits until it settles, holding a poll.
func (w *world) start(t *testing.T) *running {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	r := &running{l: w.launcher(t), cancel: cancel, done: make(chan error, 1)}
	go func() { r.done <- r.l.Run(ctx) }()
	synctest.Wait()
	return r
}

// stop ends the launcher's context and waits for Run.
func (r *running) stop(t *testing.T) {
	t.Helper()
	r.cancel()
	if err := <-r.done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// runFor runs a launcher for d on the bubble's clock and answers Run's
// error.
func (w *world) runFor(t *testing.T, d time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), d)
	defer cancel()
	return w.launcher(t).Run(ctx)
}

// assign queues an assignment of run in state, and waits until the
// launcher acted on it.
func (w *world) assign(t *testing.T, run string, state wire.AssignmentState, image string) {
	t.Helper()
	w.queue(t, newRun(run, state, image))
}

// assignTool assigns as assign does, of a run whose specification names
// tool.
func (w *world) assignTool(t *testing.T, run string, state wire.AssignmentState, image string, tool *wire.RunSpecTool) {
	t.Helper()
	r := newRun(run, state, image)
	r.Spec.Tool = tool
	w.queue(t, r)
}

// newRun is an assignment of run in state, with image when it is set.
func newRun(run string, state wire.AssignmentState, image string) agenttest.Run {
	r := agenttest.Run{
		Assignment: wire.Assignment{
			Run:          run,
			RunToken:     "token-" + run,
			State:        state,
			StateVersion: 4,
		},
		Spec: wire.RunSpec{
			Run:   wire.RunSpecRun{},
			Hooks: map[string][]string{"before_init": {"echo hi"}},
		},
		Secrets: wire.RunSpecSecrets{
			ApiToken:    "api-" + run,
			Environment: map[string]string{"TF_VAR_x": "1"},
		},
	}
	if image != "" {
		r.Spec.RunnerImage = &image
	}
	return r
}

// queue queues r's assignment, and waits until the launcher acted on it.
func (w *world) queue(t *testing.T, r agenttest.Run) {
	t.Helper()
	if err := w.srv.Assign(r); err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
}

// answer queues a poll result and waits until the launcher acted on it.
func (w *world) answer(r wire.PollResult) {
	w.srv.Answer(r)
	synctest.Wait()
}

// lastPoll is the poll the server holds.
func (w *world) lastPoll(t *testing.T) agenttest.Poll {
	t.Helper()
	ps := w.srv.Polls()
	if len(ps) == 0 {
		t.Fatal("no poll")
	}
	return ps[len(ps)-1]
}

// wantPoll checks the held poll's busy flag, run and exit.
func (w *world) wantPoll(t *testing.T, busy bool, run string, exit *wire.ContainerExit) {
	t.Helper()
	p := w.lastPoll(t).Request
	var got string
	if p.Run != nil {
		got = *p.Run
	}
	if p.Busy != busy || got != run || !equalExit(p.Exit, exit) {
		t.Fatalf(
			"poll: busy %v, run %q, exit %+v; want %v, %q, %+v",
			p.Busy,
			got,
			p.Exit,
			busy,
			run,
			exit,
		)
	}
}

// maxBackoff is the launcher's longest wait between failed calls.
const maxBackoff = 30 * time.Second

func equalExit(a, b *wire.ContainerExit) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func signals(s ...wire.Signal) wire.PollResult { return wire.PollResult{Signals: &s} }

func ended(code int, reason wire.ContainerExitReason) executor.Exit {
	return executor.Exit{Code: code, Reason: reason}
}

func TestUnitLauncherRegistersPollsAndDeregisters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		if err := w.launcher(t).Ready(t.Context()); err == nil {
			t.Fatal("a launcher that never registered is ready")
		}
		r := w.start(t)
		if err := r.l.Ready(t.Context()); err != nil {
			t.Fatalf("Ready: %v", err)
		}
		regs := w.srv.Registrations()
		if len(regs) != 1 {
			t.Fatalf("registrations: %+v", regs)
		}
		c := regs[0].Claims
		capabilities, ok := c["capabilities"].([]any)
		if !ok || len(capabilities) != 2 || capabilities[0] != "plan-sanitization-v1" || capabilities[1] != "public-pool-v1" {
			t.Fatalf("signed capabilities: %v", c["capabilities"])
		}
		meta, _ := c["metadata"].(map[string]any)
		if c["pool"] != w.pool.ID || c["version"] != "1.2.3" || c["os"] != "linux" ||
			c["arch"] != "arm64" || meta["zone"] != "a" || c["jti"] == "" {
			t.Fatalf("claims: %v", c)
		}
		if _, ok := c["worker"]; ok {
			t.Fatalf("a first registration names a worker: %v", c)
		}
		p := w.lastPoll(t)
		if p.Session != "session-1" || *p.Request.Drained || *p.Request.Version != "1.2.3" ||
			*p.Request.Wait != 60 {
			t.Fatalf("poll: %+v", p)
		}
		w.wantPoll(t, false, "", nil)

		w.answer(wire.PollResult{})
		if n := len(w.srv.Polls()); n != 2 {
			t.Fatalf("an answered poll polls again: %d polls", n)
		}
		r.stop(t)
		if got := w.srv.Deregistered(); !slices.Equal(got, []string{"worker-1"}) {
			t.Fatalf("deregistered: %v", got)
		}
		if _, err := os.Stat(filepath.Join(w.dir, "worker.json")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("worker.json after deregistering: %v", err)
		}
		if err := r.l.Ready(t.Context()); err == nil {
			t.Fatal("a deregistered launcher is ready")
		}
	})
}

func TestUnitLauncherExecutesARun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		r := w.start(t)
		w.assign(t, "run-1", wire.AssignmentStatePREPARING, "")
		jobs := w.exec.Jobs()
		if len(jobs) != 1 {
			t.Fatalf("jobs: %+v", jobs)
		}
		j := jobs[0]
		if j.Worker != "worker-1" || j.Run != "run-1" ||
			j.Image != config.VendorRunnerImage("") {
			t.Fatalf("job: %+v", j)
		}
		h, err := handoff.Read(bytes.NewReader(j.Handoff))
		if err != nil {
			t.Fatal(err)
		}
		if h.Endpoint != client.Base(agenttest.Endpoint) || h.Run != "run-1" ||
			h.RunToken != "token-run-1" || h.Assigned != wire.AssignmentStatePREPARING ||
			h.StateVersion != 4 || h.Secrets.ApiToken != "api-run-1" ||
			h.Secrets.Environment["TF_VAR_x"] != "1" || h.Spec.Sealed != nil ||
			h.Spec.Hooks["before_init"][0] != "echo hi" ||
			!slices.Equal(h.MaskEnvs, []string{"DB_PASSWORD"}) || !h.SensitiveOutputUpload ||
			h.MaxRequestsPerSecond != 17 || h.MaxRequestsBurst != 170 ||
			!bytes.Equal(h.PoolKey, poolKey(t, w.pool).Bytes()) {
			t.Fatalf("handoff: %+v", h)
		}
		w.wantPoll(t, true, "run-1", nil)
		state, err := os.ReadFile(filepath.Join(w.dir, "worker.json"))
		if err != nil || !strings.Contains(string(state), `"run":"run-1"`) {
			t.Fatalf("worker.json: %s, %v", state, err)
		}

		if _, err := j.Output.Write([]byte("init\nplan")); err != nil {
			t.Fatal(err)
		}
		if got := w.log.String(); !strings.Contains(
			got,
			`msg="runner output" run=run-1 line=init`,
		) ||
			strings.Contains(got, "line=plan") {
			t.Fatalf("a whole line is logged, and a part waits: %s", got)
		}

		c := w.exec.Container("worker-1")
		c.End(ended(3, wire.Exited))
		synctest.Wait()
		w.wantPoll(t, false, "run-1", &wire.ContainerExit{Code: 3, Reason: wire.Exited})
		if !c.Removed() {
			t.Fatal("an ended container is not removed")
		}
		w.answer(wire.PollResult{})
		w.wantPoll(t, false, "", nil)
		state, err = os.ReadFile(filepath.Join(w.dir, "worker.json"))
		if err != nil || strings.Contains(string(state), `"run":`) {
			t.Fatalf("worker.json once the exit is reported: %s, %v", state, err)
		}

		w.srv.SaveWorkspace("run-2", 3, []byte("sealed"))
		w.assign(t, "run-2", wire.AssignmentStatePREPARINGAPPLY, "example.com/runner:1")
		if j := w.exec.Jobs()[1]; j.Image != "example.com/runner:1" {
			t.Fatalf("the specification's image: %q", j.Image)
		}
		w.wantPoll(t, true, "run-2", nil)
		w.exec.Container("worker-1").Fail(errors.New("no such container"))
		synctest.Wait()
		w.wantPoll(
			t,
			false,
			"run-2",
			&wire.ContainerExit{Code: executor.MissingCode, Reason: wire.Missing},
		)
		w.answer(wire.PollResult{})
		r.stop(t)
		if got := w.srv.Deregistered(); len(got) != 1 {
			t.Fatalf("an idle launcher deregisters: %v", got)
		}
	})
}

// TestUnitRunnerImage checks the vendor fallback and both overriding
// sources on the image the executor actually receives.
func TestUnitRunnerImage(t *testing.T) {
	for _, vendor := range []string{"terraform", "opentofu", "terragrunt"} {
		for _, source := range []string{"vendor", "launcher", "specification"} {
			t.Run(vendor+"/"+source, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					w := newWorld(t)
					want := config.VendorRunnerImage(vendor)
					image := ""
					if source != "vendor" {
						w.image, want = "example.com/launcher:1", "example.com/launcher:1"
					}
					if source == "specification" {
						image, want = "example.com/specification:1", "example.com/specification:1"
					}
					r := w.start(t)
					run := newRun("run-1", wire.AssignmentStatePREPARING, image)
					run.Spec.Stack.Vendor = vendor
					if vendor == "terragrunt" {
						run.Spec.Tool = &wire.RunSpecTool{Name: wire.Tofu, Version: "1.13.9", Download: true}
						run.Spec.Terragrunt = &wire.RunSpecTerragrunt{Version: "1.0.0", Download: true}
					}
					w.queue(t, run)
					if jobs := w.exec.Jobs(); len(jobs) != 1 || jobs[0].Image != want {
						t.Fatalf("jobs = %+v, want image %q", jobs, want)
					}
					r.stop(t)
				})
			})
		}
	}
}

func TestUnitPublicRunnerImageAdmission(t *testing.T) {
	for _, tt := range []struct {
		name       string
		launcher   string
		spec       string
		registries []string
		allowed    bool
	}{
		{"vendor fallback", "", "", []string{"ghcr.io"}, true},
		{"launcher allowed", "ghcr.io/weaveplatform/runner:1", "", []string{"ghcr.io"}, true},
		{"launcher denied", "untrusted.example/runner:1", "", []string{"ghcr.io"}, false},
		{"specification allowed", "untrusted.example/runner:1", "ghcr.io/weaveplatform/runner:1", []string{"ghcr.io"}, true},
		{"specification denied", "ghcr.io/weaveplatform/runner:1", "untrusted.example/runner:1", []string{"ghcr.io"}, false},
		{"empty policy", "", "", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t)
				w.image = tt.launcher
				r := w.start(t)
				run := newRun("run-1", wire.AssignmentStatePREPARING, tt.spec)
				run.Spec.PublicPool = &wire.PublicPoolPolicy{AllowedRegistries: tt.registries}
				w.queue(t, run)
				if jobs := w.exec.Jobs(); (len(jobs) == 1) != tt.allowed {
					t.Fatalf("public image admission allowed=%t: %+v", tt.allowed, jobs)
				}
				if !tt.allowed {
					reports := w.srv.Reports()
					if len(reports) != 1 || reports[0].Report.Event != wire.Failed || reports[0].Report.Details == nil ||
						*reports[0].Report.Details != "runner image is not admitted by the installation public pool" {
						t.Fatalf("public image refusal: %+v", reports)
					}
					if len(w.tools.fetched()) != 0 {
						t.Fatal("downloaded tools for a refused image")
					}
				}
				r.stop(t)
			})
		})
	}
}

var errRefused = errors.New(
	"terraform 1.17.2 for linux/arm64: verify terraform_1.17.2_SHA256SUMS: " +
		"the signature does not verify",
)

// TestUnitLauncherMountsTheRunsTool checks that a run whose specification
// names a tool to download gets its binary for Linux at the worker's
// architecture, mounted read-only at /bin/<tool>, and a Terragrunt
// stack's run the Terragrunt that drives it at /bin/terragrunt beside it;
// that a binary whose download is false is not mounted; and that a
// refused release, the tool's or Terragrunt's, fails the run in its
// preparing state with the refusal as its note, starting no container.
func TestUnitLauncherMountsTheRunsTool(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		r := w.start(t)
		end := func() {
			w.exec.Container("worker-1").End(ended(0, wire.Exited))
			synctest.Wait()
			w.answer(wire.PollResult{})
		}
		w.assignTool(t, "run-1", wire.AssignmentStatePREPARING, "",
			&wire.RunSpecTool{Name: wire.Tofu, Version: "1.13.9", Download: true})
		want := []executor.Mount{
			{Source: "/cache/tofu/1.13.9/linux_arm64", Target: "/bin/tofu", ReadOnly: true},
		}
		if got := w.exec.Jobs()[0].Mounts; !slices.Equal(got, want) {
			t.Fatalf("mounts: %+v", got)
		}
		if got := w.tools.fetched(); !slices.Equal(got, []tools.Release{
			{Tool: tools.Tofu, Version: "1.13.9", OS: "linux", Arch: "arm64"},
		}) {
			t.Fatalf("fetched: %+v", got)
		}
		end()

		w.assignTool(t, "run-2", wire.AssignmentStatePREPARING, "",
			&wire.RunSpecTool{Name: wire.Terraform, Version: "1.17.2"})
		if got := w.exec.Jobs()[1].Mounts; got != nil || len(w.tools.fetched()) != 1 {
			t.Fatalf("a tool not to download: mounts %+v, fetched %+v", got, w.tools.fetched())
		}
		end()

		w.assignTerragrunt(t, "run-3",
			&wire.RunSpecTool{Name: wire.Terraform, Version: "1.17.2", Download: true},
			&wire.RunSpecTerragrunt{Version: "1.2.2", Download: true})
		want = []executor.Mount{
			{
				Source: "/cache/terraform/1.17.2/linux_arm64", Target: "/bin/terraform",
				ReadOnly: true,
			},
			{
				Source: "/cache/terragrunt/1.2.2/linux_arm64", Target: "/bin/terragrunt",
				ReadOnly: true,
			},
		}
		if got := w.exec.Jobs()[2].Mounts; !slices.Equal(got, want) {
			t.Fatalf("a Terragrunt run's mounts: %+v", got)
		}
		if got := w.tools.fetched()[1:]; !slices.Equal(got, []tools.Release{
			{Tool: tools.Terraform, Version: "1.17.2", OS: "linux", Arch: "arm64"},
			{Tool: tools.Terragrunt, Version: "1.2.2", OS: "linux", Arch: "arm64"},
		}) {
			t.Fatalf("a Terragrunt run fetched: %+v", got)
		}
		end()

		w.assignTerragrunt(t, "run-4", &wire.RunSpecTool{Name: wire.Tofu, Version: "1.13.9"},
			&wire.RunSpecTerragrunt{Version: "1.2.2"})
		if got := w.exec.Jobs()[3].Mounts; got != nil || len(w.tools.fetched()) != 3 {
			t.Fatalf("a Terragrunt run that downloads nothing: mounts %+v, fetched %+v", got,
				w.tools.fetched())
		}
		end()

		for _, c := range []struct {
			run  string
			spec func(*agenttest.Run)
			err  error
		}{
			{"run-5", func(r *agenttest.Run) {
				r.Spec.Tool = &wire.RunSpecTool{
					Name: wire.Terraform, Version: "1.17.2", Download: true,
				}
			}, errRefused},
			{"run-6", func(r *agenttest.Run) {
				r.Spec.Tool = &wire.RunSpecTool{Name: wire.Terraform, Version: "1.17.2"}
				r.Spec.Terragrunt = &wire.RunSpecTerragrunt{Version: "1.2.2", Download: true}
			}, errTampered},
		} {
			w.tools.err = c.err
			run := newRun(c.run, wire.AssignmentStatePREPARING, "")
			c.spec(&run)
			w.queue(t, run)
			reps := w.srv.Reports()
			if rep := reps[len(reps)-1]; rep.Run != c.run || rep.Report.Event != wire.Failed ||
				rep.Report.State != wire.WorkerStatePREPARING ||
				*rep.Report.Details != c.err.Error() || len(w.exec.Jobs()) != 4 {
				t.Fatalf("a refused release: %+v, %d jobs", rep, len(w.exec.Jobs()))
			}
			w.wantPoll(t, false, "", nil)
		}
		r.stop(t)
	})
}

// assignTerragrunt assigns a run preparing its first phase, of a
// Terragrunt stack whose specification names tool and terragrunt.
func (w *world) assignTerragrunt(t *testing.T, run string, tool *wire.RunSpecTool, terragrunt *wire.RunSpecTerragrunt) {
	t.Helper()
	r := newRun(run, wire.AssignmentStatePREPARING, "")
	r.Spec.Tool, r.Spec.Terragrunt = tool, terragrunt
	w.queue(t, r)
}

var errTampered = errors.New(
	"terragrunt 1.2.2 for linux/arm64: download terragrunt_linux_arm64: " +
		"the SHA-256 is not the signed one",
)

// TestUnitLauncherFetchesTheRunsSource checks that a run being prepared
// gets its source fetched with its credential into a fresh workspace
// under the cache directory, which its container mounts and which goes
// when the container ends; that a run that applies after confirmation
// gets the workspace its planning saved restored instead, and fails with
// none saved; and that a fetch that fails, or a container that does not
// start, fails the run in preparation and leaves no workspace.
func TestUnitLauncherFetchesTheRunsSource(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		r := w.start(t)
		ws := filepath.Join(w.dir, "workspaces", "run-1")
		run := newRun("run-1", wire.AssignmentStatePREPARING, "")
		cred := &wire.RunSpecSourceCredential{Username: "x", Token: "secret"}
		run.Secrets.SourceCredential = cred
		w.queue(t, run)
		dirs, creds := w.sources.fetched()
		if len(dirs) != 1 || dirs[0] != ws || *creds[0] != *cred {
			t.Fatalf("fetched into %v with %v", dirs, creds)
		}
		if j := w.exec.Jobs()[0]; j.Workspace != ws {
			t.Fatalf("the job's workspace: %q", j.Workspace)
		}
		if _, err := os.Stat(filepath.Join(ws, source.Checkout, "main.tf")); err != nil {
			t.Fatal(err)
		}
		w.exec.Container("worker-1").End(ended(0, wire.Exited))
		synctest.Wait()
		if _, err := os.Stat(ws); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("an ended run's workspace: %v", err)
		}
		w.answer(wire.PollResult{})

		w.assign(t, "run-2", wire.AssignmentStatePREPARINGAPPLY, "")
		reps := w.srv.Reports()
		if rep := reps[len(reps)-1]; rep.Run != "run-2" || *rep.Report.Details !=
			"the run saved no workspace to apply or replan from" {
			t.Fatalf("an apply with no saved workspace: %+v", rep)
		}
		w.answer(wire.PollResult{})

		w.srv.SaveWorkspace("run-2b", 5, []byte("sealed"))
		w.assign(t, "run-2b", wire.AssignmentStatePREPARINGREPLAN, "")
		applied := filepath.Join(w.dir, "workspaces", "run-2b")
		if dirs, _ := w.sources.fetched(); len(dirs) != 1 ||
			w.exec.Jobs()[1].Workspace != applied || !slices.Equal(w.sources.restored, []string{
			applied + " from " + agenttest.Endpoint + "/store/workspace/run-2b/5/workspace",
		}) {
			t.Fatalf("a replan fetched %v, restored %v, into %q", dirs, w.sources.restored,
				w.exec.Jobs()[1].Workspace)
		}
		w.exec.Container("worker-1").End(ended(0, wire.Exited))
		synctest.Wait()
		w.answer(wire.PollResult{})

		w.sources.err = errFetch
		w.assign(t, "run-3", wire.AssignmentStatePREPARING, "")
		reps = w.srv.Reports()
		if rep := reps[len(reps)-1]; rep.Run != "run-3" || *rep.Report.Details != errFetch.Error() {
			t.Fatalf("a failed fetch: %+v", rep)
		}
		w.sources.err = nil
		w.exec.FailStart(errors.New("no such image"))
		w.assign(t, "run-4", wire.AssignmentStatePREPARING, "")
		entries, err := os.ReadDir(filepath.Join(w.dir, "workspaces"))
		if err != nil || len(entries) != 0 || len(w.exec.Jobs()) != 2 {
			t.Fatalf("workspaces left: %v, %v", entries, err)
		}
		r.stop(t)
	})
}

// skewed is a store's refusal of a pre-signed URL's signature.
var skewed = client.Signed(errors.New(
	"fetch the archive from store.example.com: the download failed: answered 403 Forbidden"))

// TestUnitLauncherFetchesAgainARefusedSignature checks that a source or a
// saved workspace whose pre-signed URL the store refuses is fetched again
// into a fresh directory after 1 and then 2 seconds, the specification
// asked for again, which signs a source afresh; that a refusal lasting
// past ten minutes fails the run with the store's answer; and that the
// specification's refusal on the way fails it with that.
func TestUnitLauncherFetchesAgainARefusedSignature(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		r := w.start(t)
		ws := filepath.Join(w.dir, "workspaces", "run-1")
		w.sources.errs = []error{skewed, skewed}
		w.assign(t, "run-1", wire.AssignmentStatePREPARING, "")
		<-time.After(3 * time.Second)
		synctest.Wait()
		dirs, _ := w.sources.fetched()
		if !slices.Equal(dirs, []string{ws, ws, ws}) || len(w.exec.Jobs()) != 1 ||
			w.exec.Jobs()[0].Workspace != ws {
			t.Fatalf("fetched into %v, started %d jobs", dirs, len(w.exec.Jobs()))
		}
		for _, wait := range []string{"retry_in=1s", "retry_in=2s"} {
			if !strings.Contains(w.log.String(),
				`msg="the store refused the workspace's signed URL" run=run-1`) ||
				!strings.Contains(w.log.String(), wait) {
				t.Fatalf("no warning of a retry %s: %s", wait, w.log.String())
			}
		}
		w.exec.Container("worker-1").End(ended(0, wire.Exited))
		synctest.Wait()
		w.answer(wire.PollResult{})

		w.srv.SaveWorkspace("run-2", 3, []byte("sealed"))
		w.sources.errs = []error{skewed}
		w.assign(t, "run-2", wire.AssignmentStatePREPARINGAPPLY, "")
		<-time.After(time.Second)
		synctest.Wait()
		applied := filepath.Join(w.dir, "workspaces", "run-2")
		from := applied + " from " + agenttest.Endpoint + "/store/workspace/run-2/3/workspace"
		if !slices.Equal(w.sources.restored, []string{from, from}) || len(w.exec.Jobs()) != 2 {
			t.Fatalf("restored %v, started %d jobs", w.sources.restored, len(w.exec.Jobs()))
		}
		w.exec.Container("worker-1").End(ended(0, wire.Exited))
		synctest.Wait()
		w.answer(wire.PollResult{})

		w.sources.err = skewed
		w.assign(t, "run-3", wire.AssignmentStatePREPARING, "")
		<-time.After(10*time.Minute - 31*time.Second)
		synctest.Wait()
		if reps := w.srv.Reports(); len(reps) != 0 {
			t.Fatalf("gave up within ten minutes: %+v", reps)
		}
		<-time.After(32 * time.Second)
		synctest.Wait()
		reps := w.srv.Reports()
		if len(reps) != 1 || reps[0].Run != "run-3" || *reps[0].Report.Details != skewed.Error() {
			t.Fatalf("a signature refused for ten minutes: %+v", reps)
		}
		if _, err := os.Stat(filepath.Join(w.dir, "workspaces", "run-3")); !errors.Is(
			err, fs.ErrNotExist) {
			t.Fatalf("a failed run's workspace: %v", err)
		}
		w.answer(wire.PollResult{})

		w.sources.err = nil
		w.sources.errs = []error{skewed}
		w.assign(t, "run-4", wire.AssignmentStatePREPARING, "")
		w.srv.Refuse("spec", http.StatusConflict, 1)
		<-time.After(time.Second)
		synctest.Wait()
		reps = w.srv.Reports()
		if len(reps) != 2 || reps[1].Run != "run-4" || *reps[1].Report.Details !=
			"fetch the run's specification: 409 the run is not on a worker" {
			t.Fatalf("the specification refused on a retry: %+v", reps)
		}
		r.stop(t)
	})
}

// poolKey is p's public key.
func poolKey(t *testing.T, p agenttest.Pool) *ecdh.PublicKey {
	t.Helper()
	k, err := p.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

var errFetch = errors.New("fetch commit abc from git.example.com: authentication required")

// TestUnitNewDefaults checks that a launcher given no Sources or Tools
// fetches through a source.Fetcher and a tools.Fetcher on its cache
// directory and mirror: an archive source is read through the launcher's
// HTTP client; a saved workspace from the store the worker API redirects
// to, opened with the pool's key, which opens no other run's; and a
// tool's first file from the mirror.
func TestUnitNewDefaults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		c := w.config()
		c.ToolMirror = "http://mirror.invalid"
		l, err := launcher.New(launcher.Deps{
			Config: c, Doer: w.srv.Client(), Executor: w.exec,
			Log:     slog.New(slog.NewTextHandler(w.log, nil)),
			Version: "1.2.3", OS: "linux", Arch: "arm64",
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- l.Run(ctx) }()
		synctest.Wait()
		run := newRun("run-1", wire.AssignmentStatePREPARING, "")
		run.Spec.Source = wire.RunSpecSource{Kind: wire.Archive, Url: "http://store.invalid/upload"}
		w.queue(t, run)
		if d := *w.srv.Reports()[0].Report.Details; d !=
			"fetch the archive from store.invalid: the download failed: answered 404 Not Found" {
			t.Fatalf("details: %s", d)
		}
		src := t.TempDir()
		if err := os.MkdirAll(filepath.Join(src, source.Checkout), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, source.Checkout, "main.tf"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		var saved bytes.Buffer
		if _, err := workspace.Write(&saved, src, workspace.Options{
			Pool: poolKey(t, w.pool), Run: "run-2", StateVersion: 3,
		}); err != nil {
			t.Fatal(err)
		}
		w.srv.SaveWorkspace("run-2", 3, saved.Bytes())
		w.assignTool(t, "run-2", wire.AssignmentStatePREPARINGAPPLY, "",
			&wire.RunSpecTool{Name: wire.Terraform, Version: "1.17.2", Download: true})
		d := *w.srv.Reports()[1].Report.Details
		if !strings.HasPrefix(d, "terraform 1.17.2 for linux/arm64: download "+
			"terraform_1.17.2_SHA256SUMS: the download failed") ||
			!strings.Contains(d, "http://mirror.invalid/terraform/1.17.2/") {
			t.Fatalf("details: %s", d)
		}
		w.srv.SaveWorkspace("run-3", 3, saved.Bytes())
		w.assign(t, "run-3", wire.AssignmentStatePREPARINGAPPLY, "")
		if d := *w.srv.Reports()[2].Report.Details; !strings.HasPrefix(d, "restore the saved "+
			"workspace from agenttest.invalid: the saved workspace is refused: ") {
			t.Fatalf("details of another run's workspace: %s", d)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestUnitLauncherSignals(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		r := w.start(t)
		w.answer(signals(wire.Stop, wire.Kill))
		w.wantPoll(t, false, "", nil)

		w.answer(wire.PollResult{Drained: new(true)})
		if p := w.lastPoll(t).Request; !*p.Drained {
			t.Fatal("a drained launcher reports it")
		}
		w.answer(wire.PollResult{Drained: new(false)})

		w.assign(t, "run-1", wire.AssignmentStatePREPARING, "")
		c := w.exec.Container("worker-1")
		w.answer(signals(wire.Stop))
		if c.Stopped() != 1 || c.Killed() {
			t.Fatalf("stop: stopped %d, killed %v", c.Stopped(), c.Killed())
		}
		w.wantPoll(t, true, "run-1", nil)
		w.answer(signals(wire.Kill))
		if !c.Killed() {
			t.Fatal("kill does not kill the container")
		}
		w.wantPoll(t, false, "run-1", &wire.ContainerExit{Code: 137, Reason: wire.Killed})

		w.assign(t, "run-2", wire.AssignmentStatePREPARING, "")
		w.answer(signals(wire.Cycle))
		select {
		case err := <-r.done:
			t.Fatalf("a busy launcher cycled: %v", err)
		default:
		}
		w.exec.Container("worker-1").End(ended(0, wire.Exited))
		if err := <-r.done; err != nil {
			t.Fatalf("cycle: %v", err)
		}
		w.wantPoll(t, false, "run-2", &wire.ContainerExit{Code: 0, Reason: wire.Exited})
		if got := w.srv.Deregistered(); !slices.Equal(got, []string{"worker-1"}) {
			t.Fatalf("a cycled launcher deregisters: %v", got)
		}
		if !strings.Contains(w.log.String(), "why=cycled") {
			t.Fatalf("log: %s", w.log)
		}
	})
}

func TestUnitLauncherRenewsItsSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		r := w.start(t)
		renewed := w.srv.Renew("session-1")
		synctest.Wait()
		if p := w.lastPoll(t); p.Session != renewed {
			t.Fatalf("session %q, want %q", p.Session, renewed)
		}
		r.stop(t)
	})
}

func TestUnitLauncherResumesItsRunAfterARestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		r := w.start(t)
		w.assign(t, "run-1", wire.AssignmentStatePREPARING, "")
		r.stop(t)
		c := w.exec.Container("worker-1")
		if c == nil || c.Removed() || c.Stopped() != 0 || len(w.srv.Deregistered()) != 0 {
			t.Fatal("a busy launcher that stops leaves its worker and its container")
		}

		r = w.start(t)
		regs := w.srv.Registrations()
		if len(regs) != 2 || regs[1].Claims["worker"] != "worker-1" || !regs[1].Resumed {
			t.Fatalf("registrations: %+v", regs)
		}
		w.wantPoll(t, true, "run-1", nil)
		if !strings.Contains(w.log.String(), `msg="resumed run" run=run-1`) {
			t.Fatalf("log: %s", w.log)
		}
		c.End(ended(0, wire.Exited))
		synctest.Wait()
		w.wantPoll(t, false, "run-1", &wire.ContainerExit{Code: 0, Reason: wire.Exited})
		r.stop(t)
	})
}

func TestUnitLauncherReportsAContainerGoneDuringARestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		r := w.start(t)
		w.assign(t, "run-1", wire.AssignmentStatePREPARING, "")
		r.stop(t)
		if err := w.exec.Container("worker-1").Remove(t.Context()); err != nil {
			t.Fatal(err)
		}
		r = w.start(t)
		w.wantPoll(
			t,
			false,
			"run-1",
			&wire.ContainerExit{Code: executor.MissingCode, Reason: wire.Missing},
		)
		r.stop(t)
	})
}

func TestUnitLauncherReportsAnExitAfterARestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		r := w.start(t)
		w.assign(t, "run-1", wire.AssignmentStatePREPARING, "")
		w.srv.Refuse("poll", 503, 100)
		w.exec.Container("worker-1").End(ended(2, wire.OutOfMemory))
		synctest.Wait()
		r.stop(t)
		if len(w.srv.Deregistered()) != 0 {
			t.Fatal("a launcher with an unreported exit deregistered")
		}

		w.srv.Refuse("poll", 0, 0)
		r = w.start(t)
		w.wantPoll(t, false, "run-1", &wire.ContainerExit{Code: 2, Reason: wire.OutOfMemory})
		w.answer(wire.PollResult{})
		w.wantPoll(t, false, "", nil)
		r.stop(t)
	})
}

// TestUnitLauncherKillsASilentRun checks that a run whose container
// prints a line every minute runs on, that a line moves the no-log
// timeout's end within a held poll, and that a run silent for the no-log
// timeout is killed and reported as such. The lines fall between the
// polls, which renew each minute from the assignment, so the timeout
// ends inside a held poll.
func TestUnitLauncherKillsASilentRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		r := w.start(t)
		w.assign(t, "run-1", wire.AssignmentStatePREPARING, "")
		c := w.exec.Container("worker-1")
		<-time.After(30 * time.Second)
		for range 10 {
			c.Print("still planning")
			<-time.After(time.Minute)
		}
		<-time.After(5*time.Minute + 45*time.Second)
		synctest.Wait()
		if c.Killed() {
			t.Fatal("a run that printed a line every minute was killed")
		}
		c.Print("planned")
		<-time.After(7*time.Minute - time.Second)
		synctest.Wait()
		if c.Killed() {
			t.Fatal("a run silent for less than the no-log timeout was killed")
		}
		w.wantPoll(t, true, "run-1", nil)

		<-time.After(time.Second)
		synctest.Wait()
		if !c.Killed() {
			t.Fatal("a run silent for the no-log timeout runs on")
		}
		w.wantPoll(t, false, "run-1", &wire.ContainerExit{Code: 137, Reason: wire.LogTimeout})
		if !strings.Contains(w.log.String(),
			`msg="killed the run on its timeout" run=run-1 reason=log-timeout`) {
			t.Fatalf("log: %s", w.log)
		}
		r.stop(t)
	})
}

// TestUnitLauncherKillsALongRun checks that a run whose container prints
// a line every five minutes is killed at the run timeout.
func TestUnitLauncherKillsALongRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		r := w.start(t)
		w.assign(t, "run-1", wire.AssignmentStatePREPARING, "")
		c := w.exec.Container("worker-1")
		for range 13 {
			<-time.After(5 * time.Minute)
			c.Print("still applying")
		}
		<-time.After(5*time.Minute - time.Second)
		synctest.Wait()
		if c.Killed() {
			t.Fatal("a run younger than the run timeout was killed")
		}

		<-time.After(time.Second)
		synctest.Wait()
		w.wantPoll(t, false, "run-1", &wire.ContainerExit{Code: 137, Reason: wire.RunTimeout})
		r.stop(t)
	})
}

// TestUnitLauncherTimesAResumedRun checks that a restarted launcher
// times its resumed run from the container's start and from its own
// start for output.
func TestUnitLauncherTimesAResumedRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		r := w.start(t)
		w.assign(t, "run-1", wire.AssignmentStatePREPARING, "")
		c := w.exec.Container("worker-1")
		for range 6 {
			<-time.After(5 * time.Minute)
			c.Print("still applying")
		}
		r.stop(t)
		<-time.After(30 * time.Minute)

		r = w.start(t)
		if c.Killed() {
			t.Fatal("a resumed run's no-log timeout counts from before the restart")
		}
		<-time.After(5 * time.Minute)
		c.Print("still applying")
		<-time.After(5*time.Minute - time.Second)
		synctest.Wait()
		if c.Killed() {
			t.Fatal("a resumed run was killed before its run timeout")
		}
		<-time.After(time.Second)
		synctest.Wait()
		w.wantPoll(t, false, "run-1", &wire.ContainerExit{Code: 137, Reason: wire.RunTimeout})
		r.stop(t)
	})
}

// TestUnitLauncherRetriesAFailedTimeoutKill checks that a kill the host
// refuses is logged and tried again at the next poll.
func TestUnitLauncherRetriesAFailedTimeoutKill(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		r := w.start(t)
		w.assign(t, "run-1", wire.AssignmentStatePREPARING, "")
		c := w.exec.Container("worker-1")
		c.FailKill(errors.New("refused"))
		<-time.After(7 * time.Minute)
		synctest.Wait()
		if c.Killed() || !strings.Contains(w.log.String(),
			`msg="kill the run on its timeout" run=run-1 reason=log-timeout error=refused`) {
			t.Fatalf("killed %v; log: %s", c.Killed(), w.log)
		}

		c.FailKill(nil)
		<-time.After(time.Minute)
		synctest.Wait()
		w.wantPoll(t, false, "run-1", &wire.ContainerExit{Code: 137, Reason: wire.LogTimeout})
		r.stop(t)
	})
}

func TestUnitLauncherRegistersAgainWhenItsSessionIsRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		r := w.start(t)
		w.assign(t, "run-1", wire.AssignmentStatePREPARING, "")
		c := w.exec.Container("worker-1")

		w.srv.Refuse("poll", 401, 1)
		w.answer(wire.PollResult{})
		regs := w.srv.Registrations()
		if len(regs) != 2 || regs[1].Worker != "worker-1" || !regs[1].Resumed {
			t.Fatalf("registrations: %+v", regs)
		}
		if p := w.lastPoll(t); p.Session != "session-2" {
			t.Fatalf("session: %q", p.Session)
		}
		w.wantPoll(t, true, "run-1", nil)

		w.srv.Lose()
		w.answer(wire.PollResult{})
		regs = w.srv.Registrations()
		if len(regs) != 3 || regs[2].Claims["worker"] != "worker-1" ||
			regs[2].Worker != "worker-2" {
			t.Fatalf("registrations: %+v", regs)
		}
		if !c.Removed() {
			t.Fatal("the lost worker's container is not removed")
		}
		w.wantPoll(t, false, "", nil)
		r.stop(t)
		if got := w.srv.Deregistered(); !slices.Equal(got, []string{"worker-2"}) {
			t.Fatalf("deregistered: %v", got)
		}
	})
}

func TestUnitLauncherDiscardsALostWorkersRunAtStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		r := w.start(t)
		w.assign(t, "run-1", wire.AssignmentStatePREPARING, "")
		r.stop(t)
		c := w.exec.Container("worker-1")
		w.srv.Lose()

		r = w.start(t)
		if regs := w.srv.Registrations(); regs[1].Worker != "worker-2" {
			t.Fatalf("registrations: %+v", regs)
		}
		if !c.Removed() {
			t.Fatal("the lost worker's container is not removed")
		}
		w.wantPoll(t, false, "", nil)
		r.stop(t)
	})
}

func TestUnitLauncherStartsOverForAnotherPool(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		other := newWorld(t)
		other.dir = w.dir
		if err := os.WriteFile(
			filepath.Join(w.dir, "worker.json"),
			[]byte(
				`{"pool":"`+w.pool.ID+`","worker":"worker-1","run":"run-1"}`,
			),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		r := other.start(t)
		if _, ok := other.srv.Registrations()[0].Claims["worker"]; ok {
			t.Fatal("a launcher of another pool resumed this pool's worker")
		}
		other.wantPoll(t, false, "", nil)
		r.stop(t)

		if err := os.WriteFile(
			filepath.Join(w.dir, "worker.json"),
			[]byte("{"),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		r = w.start(t)
		if regs := w.srv.Registrations(); regs[len(regs)-1].Resumed {
			t.Fatal("an unreadable worker.json resumed a worker")
		}
		r.stop(t)
	})
}

func TestUnitLauncherReportsAFailedPreparation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		r := w.start(t)
		w.srv.Refuse("spec", 409, 1)
		w.assign(t, "run-1", wire.AssignmentStatePREPARINGAPPLY, "")
		reps := w.srv.Reports()
		if len(reps) != 1 {
			t.Fatalf("reports: %+v", reps)
		}
		rep := reps[0]
		if rep.Run != "run-1" || rep.Token != "token-run-1" || rep.Report.Event != wire.Failed ||
			rep.Report.State != "PREPARING_APPLY" || rep.Report.StateVersion != 4 ||
			!strings.Contains(*rep.Report.Details, "409") {
			t.Fatalf("report: %+v %s", rep, *rep.Report.Details)
		}
		w.wantPoll(t, false, "", nil)

		w.exec.FailStart(errors.New(strings.Repeat("é", 600)))
		w.assign(t, "run-2", wire.AssignmentStatePREPARING, "")
		d := *w.srv.Reports()[1].Report.Details
		if len(d) > 1024 || !utf8.ValidString(d) {
			t.Fatalf("details: %d bytes, valid %v", len(d), utf8.ValidString(d))
		}

		w.srv.Refuse("reports", 409, 1)
		w.assign(t, "run-3", wire.AssignmentStatePREPARING, "")
		if !strings.Contains(w.log.String(), `msg="report the failed preparation" run=run-3`) {
			t.Fatalf("log: %s", w.log)
		}
		w.wantPoll(t, false, "", nil)
		r.stop(t)
		if len(w.srv.Deregistered()) != 1 {
			t.Fatal("a launcher whose preparations failed is idle, and deregisters")
		}
	})
}

func TestUnitLauncherRetriesWithBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		start := time.Now()
		w.srv.Refuse("register", 503, 2)
		w.srv.Refuse("poll", 500, 3)
		if err := w.runFor(t, 30*time.Second); err != nil {
			t.Fatal(err)
		}
		// Registration retries after 1s and 2s, and polling after 1s, 2s and
		// 4s more.
		ps := w.srv.Polls()
		if got := ps[0].At.Sub(start); got != 10*time.Second {
			t.Fatalf("first answered poll after %s", got)
		}
		if n := strings.Count(w.log.String(), `msg="poll failed"`); n != 3 {
			t.Fatalf("%d failed polls logged", n)
		}
	})
}

func TestUnitLauncherFailsARefusedRegistration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.srv.Refuse("register", 403, 1)
		err := w.runFor(t, time.Minute)
		if client.Status(err) != 403 {
			t.Fatalf("Run: %v", err)
		}

		w.srv.Refuse("register", 503, 100)
		if err := w.runFor(t, 5*time.Second); err != nil {
			t.Fatalf("Run, ended while registering: %v", err)
		}
		w.srv.Refuse("register", 0, 0)

		r := w.start(t)
		w.srv.Lose()
		w.srv.Refuse("register", 403, 1)
		w.srv.Answer(wire.PollResult{})
		if err := <-r.done; client.Status(err) != 403 {
			t.Fatalf("Run, re-registration refused: %v", err)
		}
	})
}

func TestUnitLauncherReadiness(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		r := w.start(t)
		if err := r.l.Registered(t.Context()); err != nil {
			t.Fatalf("Registered: %v", err)
		}
		w.srv.Refuse("poll", 503, 1000)
		w.answer(wire.PollResult{})
		<-time.After(3 * time.Minute)
		if err := r.l.Ready(t.Context()); err != nil {
			t.Fatalf("a launcher answered 3 poll timeouts ago: %v", err)
		}
		<-time.After(time.Second)
		if err := r.l.Ready(t.Context()); err == nil {
			t.Fatal("a launcher answered more than 3 poll timeouts ago is ready")
		}
		if err := r.l.Registered(t.Context()); err != nil {
			t.Fatalf("a launcher whose polls fail keeps its session: %v", err)
		}
		w.srv.Refuse("poll", 0, 0)
		w.srv.Answer(wire.PollResult{})
		<-time.After(maxBackoff)
		synctest.Wait()
		if err := r.l.Ready(t.Context()); err != nil {
			t.Fatalf("a launcher answered again: %v", err)
		}
		r.stop(t)
	})
}

func TestUnitLauncherStopsDuringBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.srv.Refuse("poll", 503, 100)
		w.srv.Refuse("deregister", 503, 1)
		if err := w.runFor(t, 5*time.Second); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(w.log.String(), `msg=deregister`) {
			t.Fatalf("a failed deregistration is logged: %s", w.log)
		}
	})
}

func TestUnitLauncherStartFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.exec.FailFind(errors.New("docker is down"))
		if err := w.runFor(
			t,
			time.Minute,
		); err == nil ||
			!strings.Contains(err.Error(), "docker is down") {
			t.Fatalf("Run: %v", err)
		}
		w.exec.FailFind(nil)

		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		w.dir = filepath.Join(file, "cache")
		if err := w.runFor(
			t,
			time.Minute,
		); err == nil ||
			!strings.Contains(err.Error(), "cache directory") {
			t.Fatalf("Run: %v", err)
		}
	})
}

func TestUnitNewChecksTheIdentity(t *testing.T) {
	w := newWorld(t)
	other := newWorld(t)
	for name, c := range map[string]func(*config.Launcher){
		"key file missing": func(c *config.Launcher) { c.PrivateKey = "/nonexistent/key.pem" },
		"another pool's key": func(c *config.Launcher) {
			c.PrivateKey = observe.Secret(other.pool.Key)
		},
		"token not base64": func(c *config.Launcher) { c.Token = "%%" },
		"CA file missing":  func(c *config.Launcher) { c.CAFile = "/nonexistent/ca.pem" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := w.config()
			c(&cfg)
			if _, err := launcher.New(
				launcher.Deps{Config: cfg, Doer: w.srv.Client()},
			); err == nil {
				t.Fatal("New took it")
			}
		})
	}
	cfg := w.config()
	cfg.PrivateKey = observe.Secret(other.pool.Key)
	if _, err := launcher.New(launcher.Deps{Config: cfg}); !errors.Is(err, launcher.ErrIdentity) {
		t.Fatalf("New: %v", err)
	}
}

func TestUnitLauncherPolicyStartupRefused(t *testing.T) {
	w := newWorld(t)
	for _, body := range []string{"package wrong", "package infraweave\ndeny contains"} {
		cfg := w.config()
		cfg.InitializationPolicy = filepath.Join(t.TempDir(), "policy.rego")
		if err := os.WriteFile(cfg.InitializationPolicy, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := launcher.NewContext(t.Context(), launcher.Deps{Config: cfg, Executor: w.exec, Doer: w.srv.Client()}); !errors.Is(err, initpolicy.ErrPolicy) {
			t.Fatal(err)
		}
	}
	cfg := w.config()
	cfg.InitializationPolicy = filepath.Join(t.TempDir(), "missing")
	if _, err := launcher.New(launcher.Deps{Config: cfg, Executor: w.exec, Doer: w.srv.Client()}); !errors.Is(err, initpolicy.ErrPolicy) {
		t.Fatal(err)
	}
	if len(w.srv.Registrations()) != 0 || len(w.exec.Jobs()) != 0 {
		t.Fatal("invalid startup policy registered or started a container")
	}
}

func TestUnitLauncherPolicyDeniesBeforeSourceRestoreAndTools(t *testing.T) {
	for _, state := range []wire.AssignmentState{wire.AssignmentStatePREPARING, wire.AssignmentStatePREPARINGAPPLY, wire.AssignmentStatePREPARINGREPLAN} {
		t.Run(string(state), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t)
				cfg := w.config()
				cfg.InitializationPolicy = filepath.Join(w.dir, "initialization.rego")
				if err := os.WriteFile(cfg.InitializationPolicy, []byte("package infraweave\ndeny contains input.run.token if { print(input.run.token) }"), 0o600); err != nil {
					t.Fatal(err)
				}
				l, err := launcher.New(
					launcher.Deps{Config: cfg, Doer: w.srv.Client(), Executor: w.exec, Sources: w.sources, Tools: w.tools, Log: slog.New(slog.NewTextHandler(w.log, nil))},
				)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan error, 1)
				go func() { done <- l.Run(ctx) }()
				synctest.Wait()
				r := newRun("run-policy", state, "")
				stack, branch := "immutable-stack", "feature/source"
				r.Spec.Run = wire.RunSpecRun{
					Id:           r.Assignment.Run,
					Account:      "account",
					State:        string(state),
					StateVersion: r.Assignment.StateVersion,
					Type:         wire.RunSpecRunType("TRACKED"),
				}
				r.Spec.Stack = wire.RunSpecStack{Id: "slug", Ulid: &stack, Space: "legacy"}
				r.Spec.Commit.Branch = &branch
				r.Spec.Tool = &wire.RunSpecTool{Name: wire.Terraform, Version: "1.17.2", Download: true}
				w.queue(t, r)
				reports := w.srv.Reports()
				if len(reports) != 1 || reports[0].Report.State != wire.WorkerState(state) || reports[0].Report.Event != wire.Failed || reports[0].Report.Details == nil ||
					*reports[0].Report.Details != "worker initialization policy denied: *****" {
					t.Fatalf("preparation refusal: %+v", reports)
				}
				if len(w.exec.Jobs()) != 0 || len(w.sources.dirs) != 0 || len(w.sources.restored) != 0 || len(w.tools.fetched()) != 0 {
					t.Fatal("denied run reached preparation or execution")
				}
				if strings.Contains(w.log.String(), r.Assignment.RunToken) {
					t.Fatal("policy leaked run token")
				}
				cancel()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestUnitLauncherPolicyExecRunsInHelpersOfTheRunsImage(t *testing.T) {
	for name, c := range map[string]struct {
		helper  func(context.Context, executor.Command) error
		started bool
		details string
	}{
		"verified": {func(_ context.Context, cmd executor.Command) error {
			req, err := initcommand.ReadRequest(bytes.NewReader(cmd.Request))
			if err != nil {
				return err
			}
			if err := initcommand.WriteOutput(cmd.Output, false, []byte(strings.Join(req.Argv, " "))); err != nil {
				return err
			}
			return initcommand.WriteResult(cmd.Output, initcommand.Result{Call: req.Call, Status: initcommand.StatusExited})
		}, true, ""},
		"no helper": {nil, false, "worker initialization policy evaluation failed: the initialization command's helper failed"},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t)
				w.exec.OnCommand(c.helper)
				cfg := w.config()
				cfg.InitializationPolicy = filepath.Join(w.dir, "initialization.rego")
				body := "package infraweave\nimport future.keywords\nout := exec([\"verify\", input.run.token])\ndeny contains \"unverified\" if { out.stdout != concat(\" \", [\"verify\", input.run.token]) }"
				if err := os.WriteFile(cfg.InitializationPolicy, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				l, err := launcher.New(
					launcher.Deps{Config: cfg, Doer: w.srv.Client(), Executor: w.exec, Sources: w.sources, Tools: w.tools, Log: slog.New(slog.NewTextHandler(w.log, nil))},
				)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan error, 1)
				go func() { done <- l.Run(ctx) }()
				synctest.Wait()
				r := newRun("run-exec", wire.AssignmentStatePREPARING, "")
				stack, branch, image := "immutable-stack", "main", "ghcr.io/acme/runner@sha256:"+strings.Repeat("c", 64)
				r.Spec.Run = wire.RunSpecRun{
					Id:           r.Assignment.Run,
					Account:      "account",
					State:        "PREPARING",
					StateVersion: r.Assignment.StateVersion,
					Type:         wire.RunSpecRunType("TRACKED"),
				}
				r.Spec.Stack = wire.RunSpecStack{Id: "slug", Ulid: &stack, Space: "legacy"}
				r.Spec.Commit.Branch = &branch
				r.Spec.RunnerImage = &image
				w.queue(t, r)
				cmds := w.exec.Commands()
				if len(cmds) != 1 || cmds[0].Image != image || cmds[0].Run != r.Assignment.Run || cmds[0].StateVersion != r.Assignment.StateVersion ||
					cmds[0].Worker == "" || cmds[0].Call != 1 {
					t.Fatalf("helpers %+v", cmds)
				}
				if started := len(w.exec.Jobs()) == 1; started != c.started {
					t.Fatalf("started %v, reports %+v", started, w.srv.Reports())
				}
				if !c.started {
					reports := w.srv.Reports()
					if len(reports) != 1 || reports[0].Report.Details == nil || *reports[0].Report.Details != c.details || len(w.sources.dirs) != 0 {
						t.Fatalf("reports %+v", reports)
					}
				}
				if strings.Contains(w.log.String(), r.Assignment.RunToken) {
					t.Fatal("the helper's argument reached the log")
				}
				cancel()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}
