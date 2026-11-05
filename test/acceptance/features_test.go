package acceptance

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/pkg/sdk"
)

// The environment the suite reads. make accept sets PARTITION; CI sets
// REQUIRE_DOCKER; the rest let the harness feature run the suite nested
// over fixture features without touching the real outputs.
const (
	envRequireDocker = "INFRAWEAVE_ACCEPT_REQUIRE_DOCKER"
	envPartition     = "INFRAWEAVE_ACCEPT_PARTITION"
	envSeed          = "INFRAWEAVE_ACCEPT_SEED"
	envFeatures      = "INFRAWEAVE_ACCEPT_FEATURES"
	envOut           = "INFRAWEAVE_ACCEPT_OUT"
	envCucumber      = "INFRAWEAVE_ACCEPT_CUCUMBER"
	envBinary        = "INFRAWEAVE_ACCEPT_BINARY"
	// envNoDocker makes the suite treat the daemon as unreachable. The
	// harness feature's nested runs need that on a host whose daemon
	// answers, and no Docker variable can hide it: testcontainers falls
	// back to the default socket when DOCKER_HOST does not answer.
	envNoDocker = "INFRAWEAVE_ACCEPT_NO_DOCKER"
)

// errNoDocker is the reachability error envNoDocker reports.
var errNoDocker = errors.New(envNoDocker + "=1")

// repoRoot is the module root, relative to this package.
var repoRoot = filepath.Join("..", "..")

var errStep = errors.New("step failed")

// suite is what every scenario of one test binary shares: the settings, the
// lazily started dependencies, the built binary and the skip record.
type suite struct {
	features, out, cucumber, coverDir string
	requireDocker                     bool
	dockerErr                         error
	deps                              *Dependencies

	binOnce sync.Once
	bin     string
	binErr  error
	binDir  string

	mu      sync.Mutex
	skipped map[string][]string // scenario name → the tags that needed Docker
}

func newSuite(t *testing.T) *suite {
	t.Helper()
	s := &suite{
		features:      envOr(envFeatures, "features"),
		out:           envOr(envOut, "out"),
		requireDocker: os.Getenv(envRequireDocker) == "1",
		deps:          &Dependencies{},
		binDir:        t.TempDir(),
		skipped:       map[string][]string{},
		bin:           os.Getenv(envBinary),
	}
	// make accept passes -test.gocoverdir; the subprocesses write there too,
	// and godog's report lands beside them for specref parity --states.
	if f := flag.Lookup("test.gocoverdir"); f != nil && f.Value.String() != "" {
		s.coverDir = f.Value.String()
		s.cucumber = filepath.Join(s.coverDir, "cucumber.json")
	} else {
		s.coverDir = t.TempDir()
	}
	if v, ok := os.LookupEnv(envCucumber); ok {
		s.cucumber = v
	}
	return s
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// binary builds infraweave once per test binary, or uses the one a nesting
// run passed down.
func (s *suite) binary(ctx context.Context) (string, error) {
	s.binOnce.Do(func() {
		if s.bin != "" {
			return
		}
		root, err := filepath.Abs(repoRoot)
		if err != nil {
			s.binErr = fmt.Errorf("repository root: %w", err)
			return
		}
		s.bin, s.binErr = BuildBinary(ctx, root, s.binDir)
	})
	return s.bin, s.binErr
}

// dockerTags are the scenario's tags that need a Docker daemon: every
// dependency tag except @oidc, which runs in process, and @ai, whose
// provider is a fake on loopback.
func dockerTags(tags []string) []string {
	var out []string
	for _, t := range tags {
		if !Exempt(t) && t != "@oidc" && t != "@ai" {
			out = append(out, t)
		}
	}
	return out
}

func (s *suite) skip(name string, tags []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skipped[name] = tags
}

func TestAccFeatures(t *testing.T) {
	s := newSuite(t)
	s.dockerErr = errNoDocker
	if os.Getenv(envNoDocker) != "1" {
		s.dockerErr = DockerReachable(t.Context())
	}
	if s.requireDocker && s.dockerErr != nil {
		t.Fatalf("%s=1, and no Docker daemon answers: %v", envRequireDocker, s.dockerErr)
	}
	t.Cleanup(func() {
		if err := s.deps.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	tags, err := TagExpression(os.Getenv(envPartition))
	if err != nil {
		t.Fatal(err)
	}
	seed := time.Now().UnixNano()
	if v := os.Getenv(envSeed); v != "" {
		if seed, err = strconv.ParseInt(v, 10, 64); err != nil {
			t.Fatalf("%s: %v", envSeed, err)
		}
	}
	fmt.Printf("acceptance: scenario order seed %d (%s=%d repeats it); partition %q\n",
		seed, envSeed, seed, os.Getenv(envPartition))
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
			t.Errorf("%s=1, and %d scenarios were skipped", envRequireDocker, len(names))
		}
	}
	if status != 0 {
		t.Fatal("acceptance features failed")
	}
}

// world is one scenario's state, shared by its step groups.
type world struct {
	s      *suite
	name   string
	tags   []string
	dir    string
	server *Server
	db     *Database
	// api and admin call the server's two listeners through pkg/sdk.
	api, admin *sdk.Client
}

func (s *suite) initScenario(sc *godog.ScenarioContext) {
	w := &world{s: s}
	sc.Before(func(ctx context.Context, p *godog.Scenario) (context.Context, error) {
		w.name = p.Name
		for _, t := range p.Tags {
			w.tags = append(w.tags, t.Name)
		}
		if need := dockerTags(w.tags); len(need) > 0 && s.dockerErr != nil {
			s.skip(p.Name, need)
			return ctx, godog.ErrSkip
		}
		dir, err := os.MkdirTemp("", "infraweave-accept-")
		if err != nil {
			return ctx, fmt.Errorf("scenario dir: %w", err)
		}
		w.dir = dir
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		ctx = context.WithoutCancel(ctx)
		if err != nil && w.dir != "" {
			err = errors.Join(err, w.evidence(ctx))
		}
		if w.server != nil {
			err = errors.Join(err, w.server.Stop(ctx))
		}
		if w.db != nil {
			err = errors.Join(err, w.db.Drop(ctx))
		}
		if w.dir != "" {
			err = errors.Join(err, os.RemoveAll(w.dir))
		}
		return ctx, err
	})
	(&referenceSteps{}).register(sc)
	(&deploySteps{}).register(sc)
	(&servedSteps{w: w}).register(sc)
	(&harnessSteps{w: w}).register(sc)
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// slug names a scenario's evidence directory.
func slug(name string) string {
	return strings.Trim(nonWord.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

// evidence writes what decision 0016 asks of a failed scenario to
// out/<scenario>/: the server's output and configuration and, for a
// scenario that took a database, a dump of it.
func (w *world) evidence(ctx context.Context) error {
	dir := filepath.Join(w.s.out, slug(w.name))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("evidence: %w", err)
	}
	var err error
	if w.server != nil {
		err = w.server.Evidence(dir)
	}
	if w.db != nil {
		dump, dumpErr := w.db.Dump(ctx)
		err = errors.Join(
			err,
			dumpErr,
			os.WriteFile(filepath.Join(dir, "pg_dump.sql"), []byte(dump), 0o600),
		)
	}
	fmt.Printf("acceptance: evidence of %q is in %s\n", w.name, dir)
	return err
}

// database returns the scenario's own database, creating it on first use.
func (w *world) database(ctx context.Context) (*Database, error) {
	if w.db != nil {
		return w.db, nil
	}
	db, err := w.s.deps.Postgres(ctx)
	if err != nil {
		return nil, err
	}
	w.db = db
	return db, nil
}

// startServer runs infraweave serve with config for the scenario.
func (w *world) startServer(ctx context.Context, config []byte) (*Server, error) {
	bin, err := w.s.binary(ctx)
	if err != nil {
		return nil, err
	}
	srv, err := StartServer(ctx, bin, w.dir, w.s.coverDir, config)
	if err != nil {
		return nil, err
	}
	w.server = srv
	if w.api, err = sdk.New("http://"+srv.API, sdk.WithRetry(sdk.NoRetry)); err != nil {
		return nil, fmt.Errorf("sdk: %w", err)
	}
	if w.admin, err = sdk.New("http://"+srv.Admin, sdk.WithRetry(sdk.NoRetry)); err != nil {
		return nil, fmt.Errorf("sdk: %w", err)
	}
	return srv, nil
}
