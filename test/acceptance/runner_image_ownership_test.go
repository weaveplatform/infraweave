package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/cucumber/godog"
	"github.com/go-chi/chi/v5"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"

	"github.com/weaveplatform/infraweave/internal/agent/executor/docker"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

const (
	imageOwnerRole    = "INFRAWEAVE_TEST_IMAGE_OWNER"
	imageOwnerBarrier = "INFRAWEAVE_TEST_IMAGE_BARRIER"
)

// Separate test processes own separate dependencies, launcher caches and image
// clients. A deliberately fails only after both first plans complete; B's next
// scenario cannot start its forwarder until A has exited and cleaned its suite.
func TestAccRunnerImageOwnership(t *testing.T) {
	if role := os.Getenv(imageOwnerRole); role != "" {
		harness.Features(t, append(slices.Clone(areas), imageOwnerSteps))
		return
	}
	if err := deps.DockerReachable(t.Context()); err != nil {
		if os.Getenv(harness.EnvRequireDocker) == "1" {
			t.Fatal("required Docker daemon", err)
		}
		t.Skip(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	cli, err := testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	bin, err := harness.Binary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cover, given := harness.CoverDir(t)
	out := os.Getenv(harness.EnvOut)
	if out == "" {
		out = "out"
	}
	dir, err := filepath.Abs(filepath.Join(out, "runner-image-ownership"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if given {
		// Keep each fixture's report with CI's coverage/parity artifacts without
		// replacing the ordinary TestAccFeatures cucumber.json.
		for _, role := range []string{"a", "b"} {
			t.Cleanup(func() {
				body, err := os.ReadFile(filepath.Join(dir, role+"-cucumber.json"))
				if err == nil {
					err = os.WriteFile(filepath.Join(cover, "runner-owner-"+role+"-cucumber.json"), body, 0o600)
				}
				if err != nil {
					t.Error("preserve child Cucumber report", err)
				}
			})
		}
	}
	barrier := newImageOwnerBarriers()
	server := httptest.NewServer(barrier.routes())
	defer server.Close()
	// Cancel blocked barrier requests before closing the HTTP server, including
	// when a child fails before its expected checkpoint.
	defer cancel()
	a := startImageOwner(t, ctx, "a", bin, server.URL, dir, cover, given)
	defer func() { barrier.abort(); a.stop(t) }()
	waitImageOwner(t, ctx, barrier.aAllocated, a, "A allocation")
	b := startImageOwner(t, ctx, "b", bin, server.URL, dir, cover, given)
	defer func() { barrier.abort(); b.stop(t) }()
	waitImageOwner(t, ctx, a.done, a, "A failed-scenario cleanup")
	var exit *exec.ExitError
	if !errors.As(a.err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("owner A did not reach its intended failed-scenario cleanup: %v; evidence %s", a.err, dir)
	}
	checkImageOwnerReport(t, a.report, 1, true)
	aFacts, bFacts := barrier.facts("a"), barrier.facts("b")
	if !aFacts.First || !bFacts.First || aFacts.Image == "" || bFacts.Image == "" || aFacts.Image == bFacts.Image || aFacts.Process == bFacts.Process {
		t.Fatalf("owners did not overlap independent launchers and complete both real plans: A=%+v B=%+v", aFacts, bFacts)
	}
	checkImageOwnerGone(t, ctx, cli, aFacts)
	if _, err := cli.ImageInspect(ctx, bFacts.Image); err != nil {
		t.Fatal("A cleanup removed B's image before its next forwarder/worker", err)
	}
	close(barrier.aExited)
	waitImageOwner(t, ctx, b.done, b, "B's second plan and cleanup")
	if b.err != nil {
		t.Fatalf("B failed after peer cleanup: %v; evidence %s", b.err, dir)
	}
	checkImageOwnerReport(t, b.report, 2, false)
	bFacts = barrier.facts("b")
	if !bFacts.Second {
		t.Fatal("B did not complete its second real Terraform plan after A's exit")
	}
	checkImageOwnerGone(t, ctx, cli, bFacts)
	for _, fact := range []imageOwnerFacts{aFacts, bFacts} {
		if err := deps.Eventually(ctx, "both processes' dependency cleanup", func(ctx context.Context) (bool, error) {
			containers, err := cli.ContainerList(ctx, client.ContainerListOptions{
				All:     true,
				Filters: client.Filters{}.Add("label", "org.testcontainers.sessionId="+fact.Session),
			})
			return err == nil && len(containers.Items) == 0, err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if raw, err := json.MarshalIndent([]imageOwnerFacts{aFacts, bFacts}, "", "  "); err != nil {
		t.Fatal(err)
	} else if err := os.WriteFile(filepath.Join(dir, "owners.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("two independent processes completed three real Terraform plans; peer cleanup preserved %s; evidence %s", bFacts.Image, dir)
}

type imageOwnerProcess struct {
	cmd    *exec.Cmd
	done   chan struct{}
	err    error // published by closing done
	report string
}

func startImageOwner(t *testing.T, ctx context.Context, role, bin, barrier, dir, cover string, covered bool) *imageOwnerProcess {
	t.Helper()
	args := []string{"-test.run", "^TestAccRunnerImageOwnership$", "-test.v", "-test.timeout=4m"}
	if covered && testing.CoverMode() != "" {
		args = append(args, "-test.gocoverdir", cover)
	}
	childDir := filepath.Join(dir, role)
	if err := os.MkdirAll(childDir, 0o700); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(childDir, "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	child := &imageOwnerProcess{cmd: exec.CommandContext(ctx, os.Args[0], args...), done: make(chan struct{}), report: filepath.Join(dir, role+"-cucumber.json")}
	child.cmd.Env = append(os.Environ(), imageOwnerRole+"="+role, imageOwnerBarrier+"="+barrier,
		harness.EnvFeatures+"=testdata/harness/runner-images/"+role+".feature",
		harness.EnvOut+"="+childDir, harness.EnvCucumber+"="+child.report, harness.EnvBinary+"="+bin,
		harness.EnvPartition+"=", harness.EnvTags+"=", harness.EnvNoDocker+"=", harness.EnvRequireDocker+"=1",
		"INFRAWEAVE_ACCEPT_SEED=0", "INFRAWEAVE_ACCEPT_PROGRESS=1", "GOMAXPROCS=1")
	child.cmd.Stdout, child.cmd.Stderr = log, log
	if err := child.cmd.Start(); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	go func() {
		child.err = child.cmd.Wait()
		child.err = errors.Join(child.err, log.Close())
		close(child.done)
	}()
	return child
}

func (p *imageOwnerProcess) stop(t *testing.T) {
	t.Helper()
	// The controller first returns errors to outstanding barriers so Godog can
	// run normal cleanup even when the parent fails its own assertion.
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	select {
	case <-p.done:
	case <-timer.C:
		_ = p.cmd.Process.Kill()
		<-p.done
		t.Error("child did not finish bounded cleanup after barriers were released", p.report)
	}
}

func waitImageOwner(t *testing.T, ctx context.Context, ready <-chan struct{}, child *imageOwnerProcess, what string) {
	t.Helper()
	select {
	case <-ready:
	case <-child.done:
		select {
		case <-ready:
			return
		default:
		}
		t.Fatalf("%s: child exited early: %v; report %s", what, child.err, child.report)
	case <-ctx.Done():
		t.Fatalf("%s: %v; report %s", what, ctx.Err(), child.report)
	}
}

type imageOwnerFacts struct {
	Role       string   `json:"role"`
	Phase      string   `json:"phase"`
	Session    string   `json:"session"`
	Process    int      `json:"process"`
	Image      string   `json:"image,omitempty"`
	Worker     string   `json:"worker,omitempty"`
	Containers []string `json:"containers,omitempty"`
	First      bool     `json:"first"`
	Second     bool     `json:"second"`
}

type imageOwnerBarriers struct {
	mu         sync.Mutex
	owners     map[string]imageOwnerFacts
	aAllocated chan struct{}
	both       chan struct{}
	bFirst     chan struct{}
	aExited    chan struct{}
	aborted    chan struct{}
	stop       sync.Once
}

func newImageOwnerBarriers() *imageOwnerBarriers {
	return &imageOwnerBarriers{
		owners:     map[string]imageOwnerFacts{},
		aAllocated: make(chan struct{}),
		both:       make(chan struct{}),
		bFirst:     make(chan struct{}),
		aExited:    make(chan struct{}),
		aborted:    make(chan struct{}),
	}
}

func (b *imageOwnerBarriers) abort() { b.stop.Do(func() { close(b.aborted) }) }

func (b *imageOwnerBarriers) facts(role string) imageOwnerFacts {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.owners[role]
}

func (b *imageOwnerBarriers) routes() http.Handler {
	router := chi.NewRouter()
	router.Post("/checkpoint", func(w http.ResponseWriter, r *http.Request) {
		var fact imageOwnerFacts
		if err := json.NewDecoder(r.Body).Decode(&fact); err != nil || fact.Session == "" || fact.Process <= 0 || !slices.Contains([]string{"a", "b"}, fact.Role) ||
			!slices.Contains([]string{"starting", "allocated", "first", "second"}, fact.Phase) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		old := b.owners[fact.Role]
		if old.Session != "" && (old.Session != fact.Session || old.Process != fact.Process) || old.Image != "" && fact.Image != "" && old.Image != fact.Image {
			w.WriteHeader(http.StatusConflict)
			return
		}
		fact.First, fact.Second = old.First || fact.Phase == "first", old.Second || fact.Phase == "second"
		if fact.Image == "" {
			fact.Image, fact.Worker = old.Image, old.Worker
		}
		fact.Containers = append(old.Containers, fact.Containers...)
		slices.Sort(fact.Containers)
		fact.Containers = slices.Compact(fact.Containers)
		b.owners[fact.Role] = fact
		switch {
		case fact.Phase == "allocated" && fact.Role == "a":
			close(b.aAllocated)
		case fact.Phase == "allocated" && fact.Role == "b":
			close(b.both)
		case fact.Phase == "first" && fact.Role == "b":
			close(b.bFirst)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	for name, barrier := range map[string]<-chan struct{}{"both-allocated": b.both, "b-ready": b.bFirst, "a-exited": b.aExited} {
		router.Get("/"+name, func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-barrier:
				w.WriteHeader(http.StatusNoContent)
			case <-r.Context().Done():
				w.WriteHeader(http.StatusRequestTimeout)
			case <-b.aborted:
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		})
	}
	return router
}

func imageOwnerSteps(sc *godog.ScenarioContext, world *harness.World) {
	baseline := map[string]bool{}
	sc.Step(`^the image owner records "(starting|allocated|first|second)"$`, func(ctx context.Context, phase string) error {
		fact := imageOwnerFacts{Role: os.Getenv(imageOwnerRole), Phase: phase, Session: testcontainers.SessionID(), Process: os.Getpid()}
		if phase != "starting" {
			owner, err := world.Suite.Launchers(ctx)
			if err != nil {
				return err
			}
			fact.Image, fact.Worker = owner.Image, world.IDs["l"]
		}
		cli, err := testcontainers.NewDockerClientWithOpts(ctx)
		if err != nil {
			return fmt.Errorf("image owner Docker: %w", err)
		}
		defer cli.Close()
		containers, err := cli.ContainerList(ctx, client.ContainerListOptions{All: true})
		if err != nil {
			return fmt.Errorf("image owner containers: %w", err)
		}
		// Testcontainers shares a session across sibling processes. A is
		// allocated before B starts: B's baseline excludes A's dependencies,
		// and the shared reaper remains until both processes finish.
		for _, c := range containers.Items {
			if phase == "starting" {
				baseline[c.ID] = true
				continue
			}
			dependency := phase == "allocated" && c.Labels["org.testcontainers.sessionId"] == fact.Session && c.Labels["org.testcontainers.reaper"] != "true" && !baseline[c.ID]
			if dependency || c.Image == fact.Image || fact.Worker != "" && c.Labels[docker.LabelWorker] == fact.Worker {
				fact.Containers = append(fact.Containers, c.ID)
			}
		}
		body, err := json.Marshal(fact)
		if err != nil {
			return fmt.Errorf("image owner checkpoint: %w", err)
		}
		return imageOwnerRequest(ctx, http.MethodPost, "/checkpoint", body)
	})
	sc.Step(`^the image owner awaits "(both-allocated|b-ready|a-exited)"$`, func(ctx context.Context, name string) error {
		return imageOwnerRequest(ctx, http.MethodGet, "/"+name, nil)
	})
}

func imageOwnerRequest(ctx context.Context, method, path string, body []byte) error {
	request, err := http.NewRequestWithContext(ctx, method, os.Getenv(imageOwnerBarrier)+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("image owner request: %w", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("image owner barrier %s: %w", path, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("%w: image owner barrier %s returned %d", harness.ErrStep, path, response.StatusCode)
	}
	return nil
}

func checkImageOwnerGone(t *testing.T, ctx context.Context, cli *testcontainers.DockerClient, fact imageOwnerFacts) {
	t.Helper()
	if _, err := cli.ImageInspect(ctx, fact.Image); !cerrdefs.IsNotFound(err) {
		t.Fatalf("owner %s image remains after process cleanup: %v", fact.Role, err)
	}
	for _, id := range fact.Containers {
		if _, err := cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{}); !cerrdefs.IsNotFound(err) {
			t.Fatalf("owner %s container %s remains after cleanup: %v", fact.Role, id, err)
		}
	}
}

func checkImageOwnerReport(t *testing.T, path string, scenarios int, failed bool) {
	t.Helper()
	var report []struct {
		Elements []struct {
			Steps []struct {
				Name   string `json:"name"`
				Result struct {
					Status string `json:"status"`
					Error  string `json:"error_message"` //nolint:tagliatelle // Cucumber's fixed report schema.
				} `json:"result"`
			} `json:"steps"`
		} `json:"elements"`
	}
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &report) != nil || len(report) != 1 || len(report[0].Elements) != scenarios {
		t.Fatalf("incomplete child scenario report %s: %v", path, err)
	}
	failures := 0
	for _, scenario := range report[0].Elements {
		for i, step := range scenario.Steps {
			if failed && step.Name == "the scenario fails on purpose" && i == len(scenario.Steps)-1 && step.Result.Status == "failed" &&
				strings.Contains(step.Result.Error, "this fixture scenario fails on purpose") {
				failures++
				continue
			}
			if step.Result.Status != "passed" {
				t.Fatalf("child %s failed before the intended cleanup boundary: %s (%s)", path, step.Name, step.Result.Status)
			}
		}
	}
	if failed && failures != 1 {
		t.Fatal("A did not fail at the exact intended final step", failures)
	}
}
