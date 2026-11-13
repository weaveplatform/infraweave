package harness

import (
	"context"
	"crypto"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/deps"
)

// The environment the suite reads. make accept sets PARTITION; CI sets
// REQUIRE_DOCKER; the rest let the harness feature run the suite nested
// over fixture features without touching the real outputs.
const (
	EnvRequireDocker = "INFRAWEAVE_ACCEPT_REQUIRE_DOCKER"
	EnvPartition     = "INFRAWEAVE_ACCEPT_PARTITION"
	envSeed          = "INFRAWEAVE_ACCEPT_SEED"
	EnvFeatures      = "INFRAWEAVE_ACCEPT_FEATURES"
	// EnvTags narrows the partition's scenarios to those matching a
	// further tag expression, such as one provider's @lab: tag.
	EnvTags     = "INFRAWEAVE_ACCEPT_TAGS"
	EnvOut      = "INFRAWEAVE_ACCEPT_OUT"
	EnvCucumber = "INFRAWEAVE_ACCEPT_CUCUMBER"
	EnvBinary   = "INFRAWEAVE_ACCEPT_BINARY"
	// EnvNoDocker makes the suite treat the daemon as unreachable. The
	// harness feature's nested runs need that on a host whose daemon
	// answers, and no Docker variable can hide it: testcontainers falls
	// back to the default socket when DOCKER_HOST does not answer.
	EnvNoDocker = "INFRAWEAVE_ACCEPT_NO_DOCKER"
)

// errNoDocker is the reachability error EnvNoDocker reports.
var errNoDocker = errors.New(EnvNoDocker + "=1")

// RepoRoot is the module root, relative to test/acceptance, where the
// suite's test binary runs every step.
var RepoRoot = filepath.Join("..", "..")

// Suite is what every scenario of one test binary shares: the settings, the
// lazily started dependencies, the built binary and the skip record.
type Suite struct {
	features, Out, cucumber, CoverDir string
	requireDocker                     bool
	dockerErr                         error
	Deps                              *deps.Dependencies

	launchOnce sync.Once
	launchers  Launchers
	launchErr  error

	// areas register each area's steps on every scenario.
	areas []func(*godog.ScenarioContext, *World)

	mu      sync.Mutex
	skipped map[string][]string // scenario name → the tags that needed Docker
}

func newSuite(t *testing.T) *Suite {
	t.Helper()
	s := &Suite{
		features:      envOr(EnvFeatures, "features"),
		Out:           envOr(EnvOut, "out"),
		requireDocker: os.Getenv(EnvRequireDocker) == "1",
		Deps:          &deps.Dependencies{},
		skipped:       map[string][]string{},
	}
	var given bool
	s.CoverDir, given = CoverDir(t)
	// godog's report lands beside the coverage for specref parity --states.
	if given {
		s.cucumber = filepath.Join(s.CoverDir, "cucumber.json")
	}
	if v, ok := os.LookupEnv(EnvCucumber); ok {
		s.cucumber = v
	}
	s.Deps.Migrate = func(ctx context.Context, dsn string) error {
		bin, err := Binary(ctx)
		if err != nil {
			return err
		}
		return Migrate(ctx, bin, s.CoverDir, dsn)
	}
	return s
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// CoverDir is where the subprocesses write coverage: make accept's
// -test.gocoverdir, so their counters merge with the test binary's, and
// otherwise a directory that is thrown away. given reports which.
func CoverDir(t *testing.T) (dir string, given bool) {
	t.Helper()
	if f := flag.Lookup("test.gocoverdir"); f != nil && f.Value.String() != "" {
		return f.Value.String(), true
	}
	return t.TempDir(), false
}

// The infraweave binary every test of this test binary runs.
var built struct {
	once sync.Once
	bin  string
	err  error
}

// Binary builds infraweave once per test binary, or uses the one a nesting
// run passed down. The binary outlives any one test, so it goes to a
// directory of its own that TestMain removes.
func Binary(ctx context.Context) (string, error) {
	built.once.Do(func() {
		if built.bin = os.Getenv(EnvBinary); built.bin != "" {
			return
		}
		root, err := filepath.Abs(RepoRoot)
		if err != nil {
			built.err = fmt.Errorf("repository root: %w", err)
			return
		}
		built.bin, built.err = BuildBinary(ctx, root, buildDir, "infraweave")
	})
	return built.bin, built.err
}

// The infraweavectl binary every test of this test binary runs.
var builtCtl struct {
	once sync.Once
	bin  string
	err  error
}

// CtlBinary builds infraweavectl once per test binary, beside infraweave.
// The harness feature's nested runs run no scenario that needs it.
func CtlBinary(ctx context.Context) (string, error) {
	builtCtl.once.Do(func() {
		root, err := filepath.Abs(RepoRoot)
		if err != nil {
			builtCtl.err = fmt.Errorf("repository root: %w", err)
			return
		}
		builtCtl.bin, builtCtl.err = BuildBinary(ctx, root, buildDir, "infraweavectl")
	})
	return builtCtl.bin, builtCtl.err
}

// DockerTags are the scenario's tags that need a Docker daemon: every
// dependency tag except @oidc, @github, @gitlab, @bitbucket-cloud,
// @bitbucket-dc and @azure-devops, which run in process, and @ai, whose provider is a fake on loopback.
func DockerTags(tags []string) []string {
	var out []string
	for _, t := range tags {
		if !Exempt(t) && t != "@oidc" && t != "@ai" && t != "@github" && t != "@gitlab" &&
			t != "@bitbucket-cloud" && t != "@bitbucket-dc" &&
			t != "@azure-devops" {
			out = append(out, t)
		}
	}
	return out
}

// Launchers returns what a @docker scenario's launchers need, building it
// into buildDir from the module on first use; the dependencies' Close
// removes its image.
func (s *Suite) Launchers(ctx context.Context) (*Launchers, error) {
	s.launchOnce.Do(func() {
		root, err := filepath.Abs(RepoRoot)
		if err != nil {
			s.launchErr = fmt.Errorf("repository root: %w", err)
			return
		}
		s.launchErr = s.launchers.prepare(ctx, root, buildDir)
		s.Deps.OnClose(s.launchers.Close)
	})
	return &s.launchers, s.launchErr
}

func (s *Suite) skip(name string, tags []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skipped[name] = tags
}

// buildDir holds the built binary for the life of the test binary.
var buildDir string

// MainWithBuild runs m's tests with a directory for the built binaries,
// which it removes after them, and returns their exit code.
func MainWithBuild(m *testing.M) int {
	var err error
	if buildDir, err = os.MkdirTemp("", "infraweave-build-"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(buildDir)
	return m.Run()
}

// Features runs the features of the suite's settings with godog, each
// scenario with the steps areas register.
func Features(t *testing.T, areas []func(*godog.ScenarioContext, *World)) {
	t.Helper()
	s := newSuite(t)
	s.areas = areas
	s.dockerErr = errNoDocker
	if os.Getenv(EnvNoDocker) != "1" {
		s.dockerErr = deps.DockerReachable(t.Context())
	}
	if s.requireDocker && s.dockerErr != nil {
		t.Fatalf("%s=1, and no Docker daemon answers: %v", EnvRequireDocker, s.dockerErr)
	}
	t.Cleanup(func() {
		if err := s.Deps.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	tags, err := TagExpression(os.Getenv(EnvPartition))
	if err != nil {
		t.Fatal(err)
	}
	tags = And(tags, os.Getenv(EnvTags))
	seed := time.Now().UnixNano()
	if v := os.Getenv(envSeed); v != "" {
		if seed, err = strconv.ParseInt(v, 10, 64); err != nil {
			t.Fatalf("%s: %v", envSeed, err)
		}
	}
	fmt.Printf("acceptance: scenario order seed %d (%s=%d repeats it); partition %q\n",
		seed, envSeed, seed, os.Getenv(EnvPartition))
	format := "pretty"
	if s.cucumber != "" {
		format += ",cucumber:" + s.cucumber
	}
	status := godog.TestSuite{
		ScenarioInitializer: s.initScenario,
		Options: &godog.Options{
			Format:      format,
			Paths:       []string{s.features},
			Tags:        tags,
			Strict:      true,
			Randomize:   seed,
			Concurrency: runtime.GOMAXPROCS(0),
		},
	}.Run()
	if len(s.skipped) > 0 {
		names := slices.Sorted(func(yield func(string) bool) {
			for n := range s.skipped {
				if !yield(n) {
					return
				}
			}
		})
		fmt.Printf(
			"acceptance: no Docker daemon (%v), so %d scenarios were skipped:\n",
			s.dockerErr,
			len(names),
		)
		for _, n := range names {
			fmt.Printf("  %s (%s)\n", n, strings.Join(s.skipped[n], " "))
		}
		if s.requireDocker {
			t.Errorf("%s=1, and %d scenarios were skipped", EnvRequireDocker, len(names))
		}
	}
	if status != 0 {
		t.Fatal("acceptance features failed")
	}
}

func (s *Suite) initScenario(sc *godog.ScenarioContext) {
	w := &World{
		Suite: s, Tokens: map[string]string{}, IDs: map[string]string{},
		Keys:    map[string]crypto.Signer{},
		Answers: map[string][][]byte{}, Jar: NewJar(), Settings: map[string]map[string]any{},
	}
	sc.Before(func(ctx context.Context, p *godog.Scenario) (context.Context, error) {
		w.Name = p.Name
		for _, t := range p.Tags {
			w.Tags = append(w.Tags, t.Name)
		}
		if need := DockerTags(w.Tags); len(need) > 0 && s.dockerErr != nil {
			s.skip(p.Name, need)
			return ctx, godog.ErrSkip
		}
		dir, err := os.MkdirTemp("", "infraweave-accept-")
		if err != nil {
			return ctx, fmt.Errorf("scenario dir: %w", err)
		}
		w.Dir = dir
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		ctx = context.WithoutCancel(ctx)
		if err != nil && w.Dir != "" {
			err = errors.Join(err, w.evidence(ctx))
		}
		for _, srv := range w.Servers {
			err = errors.Join(err, srv.Stop(ctx))
		}
		if w.db != nil {
			err = errors.Join(err, w.db.Drop(ctx))
		}
		if w.OwnPG != nil {
			err = errors.Join(err, w.OwnPG.Terminate(ctx))
		}
		for _, f := range w.Fakes {
			f.Stop()
		}
		if w.Dir != "" {
			err = errors.Join(err, os.RemoveAll(w.Dir))
		}
		return ctx, err
	})
	for _, register := range s.areas {
		register(sc, w)
	}
}
