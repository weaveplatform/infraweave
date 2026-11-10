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
		skipped:       map[string][]string{},
	}
	var given bool
	s.coverDir, given = coverDir(t)
	// godog's report lands beside the coverage for specref parity --states.
	if given {
		s.cucumber = filepath.Join(s.coverDir, "cucumber.json")
	}
	if v, ok := os.LookupEnv(envCucumber); ok {
		s.cucumber = v
	}
	s.deps.Migrate = func(ctx context.Context, dsn string) error {
		bin, err := binary(ctx)
		if err != nil {
			return err
		}
		return Migrate(ctx, bin, s.coverDir, dsn)
	}
	return s
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// coverDir is where the subprocesses write coverage: make accept's
// -test.gocoverdir, so their counters merge with the test binary's, and
// otherwise a directory that is thrown away. given reports which.
func coverDir(t *testing.T) (dir string, given bool) {
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

// binary builds infraweave once per test binary, or uses the one a nesting
// run passed down. The binary outlives any one test, so it goes to a
// directory of its own that TestMain removes.
func binary(ctx context.Context) (string, error) {
	built.once.Do(func() {
		if built.bin = os.Getenv(envBinary); built.bin != "" {
			return
		}
		root, err := filepath.Abs(repoRoot)
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

// ctlBinary builds infraweavectl once per test binary, beside infraweave.
// The harness feature's nested runs run no scenario that needs it.
func ctlBinary(ctx context.Context) (string, error) {
	builtCtl.once.Do(func() {
		root, err := filepath.Abs(repoRoot)
		if err != nil {
			builtCtl.err = fmt.Errorf("repository root: %w", err)
			return
		}
		builtCtl.bin, builtCtl.err = BuildBinary(ctx, root, buildDir, "infraweavectl")
	})
	return builtCtl.bin, builtCtl.err
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

// buildDir holds the built binary for the life of the test binary.
var buildDir string

func TestMain(m *testing.M) {
	var err error
	if buildDir, err = os.MkdirTemp("", "infraweave-build-"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(buildDir)
	os.Exit(code)
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
	s    *suite
	name string
	tags []string
	dir  string
	// servers are the scenario's infraweave processes, each a replica on
	// the scenario's database, in the order they started.
	servers []*Server
	db      *Database
	// ownPG is the scenario's own PostgreSQL container, when it has one.
	ownPG *OwnPostgres
	// api and admin call the first server's two listeners through pkg/sdk;
	// apis call each server's API listener, in the order of servers.
	api, admin *sdk.Client
	apis       []*sdk.Client
	// tokens are the API tokens apis hold, for a request no client sends.
	tokens []string
	// kek is the secrets.kek settings the scenario's servers and commands
	// run with; nil is the local KEK.
	kek map[string]any
	// auth is the auth settings every server of the scenario starts with;
	// nil keeps the defaults.
	auth map[string]any
	// features is the features settings every server of the scenario
	// starts with; nil keeps the defaults.
	features map[string]any
	// vendors is the release indexes every server of the scenario reads;
	// nil reads offlineIndexes.
	vendors map[string]any
	// git seeds the scenario's @vcs repositories, whose CA every server of
	// a @vcs scenario trusts.
	git *giteaSteps
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
		for _, srv := range w.servers {
			err = errors.Join(err, srv.Stop(ctx))
		}
		if w.db != nil {
			err = errors.Join(err, w.db.Drop(ctx))
		}
		if w.ownPG != nil {
			err = errors.Join(err, w.ownPG.Terminate(ctx))
		}
		if w.dir != "" {
			err = errors.Join(err, os.RemoveAll(w.dir))
		}
		return ctx, err
	})
	(&referenceSteps{}).register(sc)
	(&deploySteps{}).register(sc)
	git := &giteaSteps{w: w}
	w.git = git
	(&servedSteps{w: w}).register(sc)
	(&observeSteps{w: w}).register(sc)
	(&startupSteps{w: w}).register(sc)
	(&harnessSteps{w: w}).register(sc)
	git.register(sc)
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// slug names a scenario's evidence directory.
func slug(name string) string {
	return strings.Trim(nonWord.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

// evidence writes what a failed scenario leaves for its reader to
// out/<scenario>/: each server's output and configuration, the first's at
// the top and each later one's in server-<n>/, and, for a scenario that
// took a database, a dump of it.
//
// ref: decision 0016
func (w *world) evidence(ctx context.Context) error {
	dir := filepath.Join(w.s.out, slug(w.name))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("evidence: %w", err)
	}
	var err error
	for i, srv := range w.servers {
		d := dir
		if i > 0 {
			d = filepath.Join(dir, fmt.Sprintf("server-%d", i+1))
			err = errors.Join(err, os.MkdirAll(d, 0o750))
		}
		err = errors.Join(err, srv.Evidence(d))
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

// querier is the scenario's database: its own PostgreSQL's when it has
// one, else its database in the shared container.
func (w *world) querier(ctx context.Context) (querier, error) {
	if w.ownPG != nil {
		return w.ownPG, nil
	}
	return w.database(ctx)
}

// objects is the scenario's object storage root, which all its servers
// share.
func (w *world) objects() string { return filepath.Join(w.dir, "objects") }

// errNeedsPostgres is returned when a scenario without @postgres starts a
// server, which needs the scenario's database.
var errNeedsPostgres = errors.New("a scenario that starts infraweave needs @postgres")

// startServer runs one more infraweave all on the scenario's database, with
// settings over serverConfig's server settings.
func (w *world) startServer(ctx context.Context, settings map[string]any) (*Server, error) {
	if !slices.Contains(w.tags, "@postgres") {
		return nil, errNeedsPostgres
	}
	db, err := w.database(ctx)
	if err != nil {
		return nil, err
	}
	config, err := serverConfigKEK(db.DSN, w.objects(), settings, w.kek)
	if err != nil {
		return nil, err
	}
	if w.auth != nil {
		if config, err = withSection(config, "auth", w.auth); err != nil {
			return nil, err
		}
	}
	if w.features != nil {
		if config, err = withSection(config, "features", w.features); err != nil {
			return nil, err
		}
	}
	if w.vendors != nil {
		if config, err = withSection(config, "vendors", w.vendors); err != nil {
			return nil, err
		}
	}
	if slices.Contains(w.tags, "@vcs") {
		if err := w.git.start(ctx); err != nil {
			return nil, err
		}
		vcs := map[string]any{"ca_file": w.git.gitea.CAFile}
		if config, err = withSection(config, "vcs", vcs); err != nil {
			return nil, err
		}
	}
	return w.launch(ctx, "all", config)
}

// launch runs one more infraweave process mode with config and gives the
// scenario SDK clients for its listeners.
func (w *world) launch(ctx context.Context, mode string, config []byte) (*Server, error) {
	bin, err := binary(ctx)
	if err != nil {
		return nil, err
	}
	dir := w.dir
	if len(w.servers) > 0 {
		dir = filepath.Join(w.dir, fmt.Sprintf("server-%d", len(w.servers)+1))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("server dir: %w", err)
		}
	}
	srv, err := StartServer(ctx, bin, dir, w.s.coverDir, mode, config)
	if err != nil {
		return nil, err
	}
	w.servers = append(w.servers, srv)
	if len(w.servers) == 1 {
		if w.admin, err = sdk.New("http://"+srv.Admin, sdk.WithRetry(sdk.NoRetry)); err != nil {
			return nil, fmt.Errorf("sdk: %w", err)
		}
	}
	if srv.API == "" {
		return srv, nil
	}
	// Every route but a few needs a credential, so the scenario's clients
	// hold a token of the home tenant's key.
	// ref: decision 0006
	db, err := w.querier(ctx)
	if err != nil {
		return nil, err
	}
	api, tok, err := authenticated(ctx, db, "http://"+srv.API, home)
	if err != nil {
		return nil, fmt.Errorf("API client: %w", err)
	}
	w.apis, w.tokens = append(w.apis, api), append(w.tokens, tok)
	if len(w.servers) == 1 {
		w.api = api
	}
	return srv, nil
}
