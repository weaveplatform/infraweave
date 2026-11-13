package harness

import (
	"context"
	"crypto"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/deps"
)

// ErrStep is the failure of a step whose check does not hold, which each
// step wraps with what it found.
var ErrStep = errors.New("step failed")

// World is one scenario's state, shared by its step groups.
type World struct {
	Suite *Suite
	Name  string
	Tags  []string
	Dir   string
	// Servers are the scenario's infraweave processes, each a replica on
	// the scenario's database, in the order they started.
	Servers []*Server
	db      *deps.Database
	// OwnPG is the scenario's own PostgreSQL container, when it has one.
	OwnPG *deps.OwnPostgres
	// API and Admin call the first server's two listeners through pkg/sdk;
	// APIs call each server's API listener, in the order of Servers.
	API, Admin *sdk.Client
	APIs       []*sdk.Client
	// APITokens are the API tokens APIs hold, for a request no client sends.
	APITokens []string
	// Sent holds every answer of the scenario, the last one last.
	Sent []Response
	// Tokens are the scenario's named API tokens, and IDs the names a
	// request's {name} resolves to: IDs, clone URLs and kept commits.
	Tokens, IDs map[string]string
	// Keys are the private keys of the scenario's certificate signing
	// requests, by the name each request is kept as: the pool keys its
	// workers register and open run specifications with.
	Keys map[string]crypto.Signer
	// Answers are the bodies of the answers to SendAs, by the request's
	// method and path.
	Answers map[string][][]byte
	// Jar is the scenario's browser.
	Jar *Jar
	// Vault is the Vault KEK the data keys are wrapped by after a rewrap;
	// nil while they are under the local KEK.
	Vault *kek.VaultConfig
	// Settings are the top-level sections every server of the scenario
	// starts with over ServerConfig's, by their key, such as secrets for
	// the KEK, auth, features, vendors, queue, worker and run.
	Settings map[string]map[string]any
	// Fakes are the stand-ins for the services the scenario's servers
	// reach, which the scenario's tags start, in the order their
	// certificates join vcs.ca_file.
	Fakes []Fake
	// Bucket is an @objectstore scenario's bucket in the shared RustFS,
	// which every server of the scenario keeps its objects in.
	Bucket string
	// storePort is an @objectstore scenario's port of the object store on
	// the host's loopback, which the URLs its servers sign name.
	storePort string
	// env is what every process mode of the scenario runs with beyond
	// the test binary's environment: the object store's keys.
	env []string
}

// Ports are the ports on the host's loopback that the scenario's
// containers reach, which Launchers.Network forwards: the relay's, and an
// @objectstore scenario's object store's, which a runner uploads its log
// to through the URLs a server signs.
func (w *World) Ports(relay string) []string {
	if w.storePort == "" {
		return []string{relay}
	}
	return []string{relay, w.storePort}
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// Slug names a scenario's evidence directory.
func Slug(name string) string {
	return strings.Trim(nonWord.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

// evidence writes what a failed scenario leaves for its reader to
// out/<scenario>/: each server's output and configuration, the first's at
// the top and each later one's in server-<n>/, and, for a scenario that
// took a database, a dump of it.
//
// ref: decision 0016
func (w *World) evidence(ctx context.Context) error {
	dir := filepath.Join(w.Suite.Out, Slug(w.Name))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("evidence: %w", err)
	}
	var err error
	for i, srv := range w.Servers {
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
	fmt.Printf("acceptance: evidence of %q is in %s\n", w.Name, dir)
	return err
}

// Database returns the scenario's own database, creating it on first use.
func (w *World) Database(ctx context.Context) (*deps.Database, error) {
	if w.db != nil {
		return w.db, nil
	}
	db, err := w.Suite.Deps.Postgres(ctx)
	if err != nil {
		return nil, err
	}
	w.db = db
	return db, nil
}

// Querier is the scenario's database: its own PostgreSQL's when it has
// one, else its database in the shared container.
func (w *World) Querier(ctx context.Context) (Querier, error) {
	if w.OwnPG != nil {
		return w.OwnPG, nil
	}
	return w.Database(ctx)
}

// Objects is the scenario's object storage root, which all its servers
// share.
func (w *World) Objects() string { return filepath.Join(w.Dir, "objects") }

// errNeedsPostgres is returned when a scenario without @postgres starts a
// server, which needs the scenario's database.
var errNeedsPostgres = errors.New("a scenario that starts infraweave needs @postgres")

// StartServer runs one more infraweave all on the scenario's database, with
// settings over ServerConfig's server settings.
func (w *World) StartServer(ctx context.Context, settings map[string]any) (*Server, error) {
	if !slices.Contains(w.Tags, "@postgres") {
		return nil, errNeedsPostgres
	}
	db, err := w.Database(ctx)
	if err != nil {
		return nil, err
	}
	config, err := ServerConfig(db.DSN, w.Objects(), settings)
	if err != nil {
		return nil, err
	}
	for _, key := range slices.Sorted(maps.Keys(w.Settings)) {
		if config, err = withSection(config, key, w.Settings[key]); err != nil {
			return nil, err
		}
	}
	if config, err = w.vcs(ctx, config); err != nil {
		return nil, err
	}
	if config, err = w.objectStore(ctx, config); err != nil {
		return nil, err
	}
	return w.Launch(ctx, "all", config)
}

// objectStore is config with, for an @objectstore scenario, the s3 driver
// on the scenario's Bucket, which it creates on first use, and the
// store's keys in the environment its servers start with, where the
// driver reads them. Every other scenario keeps the fs driver under
// Objects.
//
// ref: decision 0016
func (w *World) objectStore(ctx context.Context, config []byte) ([]byte, error) {
	if !slices.Contains(w.Tags, "@objectstore") {
		return config, nil
	}
	store, err := w.Suite.Deps.ObjectStore(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // names @objectstore
	}
	u, err := url.Parse(store.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("the object store's endpoint: %w", err)
	}
	w.storePort = u.Port()
	if w.Bucket == "" {
		name := "scenario-" + strconv.FormatInt(w.Suite.Deps.Serial(), 10)
		if err := store.Bucket(ctx, name); err != nil {
			return nil, err //nolint:wrapcheck // names @objectstore
		}
		w.Bucket = name
	}
	w.env = []string{
		"AWS_ACCESS_KEY_ID=" + store.AccessKey,
		"AWS_SECRET_ACCESS_KEY=" + store.SecretKey,
		"AWS_EC2_METADATA_DISABLED=true",
	}
	return withSection(config, "objectstore", map[string]any{
		"driver": "s3",
		"s3": map[string]any{
			"bucket": w.Bucket, "endpoint": store.Endpoint, "region": deps.ObjectStoreRegion,
			"path_style": true,
		},
	})
}

// Fake is a stand-in for a service the scenario's servers reach, which
// the scenario's tags start. VCS starts it on first use when the tags
// name it and returns the vcs settings and the certificates every server
// of the scenario needs, none when it is not running; Stop stops it.
type Fake interface {
	VCS(ctx context.Context, w *World) (map[string]any, []byte, error)
	Stop()
}

// vcs is config with the vcs settings the scenario's fakes give, under one
// vcs.ca_file holding every certificate they trust.
func (w *World) vcs(ctx context.Context, config []byte) ([]byte, error) {
	vcs := map[string]any{}
	var cas []byte
	for _, f := range w.Fakes {
		settings, ca, err := f.VCS(ctx, w)
		if err != nil {
			return nil, err
		}
		maps.Copy(vcs, settings)
		cas = append(cas, ca...)
	}
	if len(vcs) == 0 && len(cas) == 0 {
		return config, nil
	}
	if len(cas) > 0 {
		file := filepath.Join(w.Dir, caFile)
		if err := os.WriteFile(file, cas, 0o600); err != nil {
			return nil, fmt.Errorf("vcs CA: %w", err)
		}
		vcs["ca_file"] = file
	}
	return withSection(config, "vcs", vcs)
}

// caFile holds every certificate the scenario's fakes give, in w.Dir.
const caFile = "vcs-ca.pem"

// CAFile is the file vcs.ca_file names, holding every certificate the
// scenario's fakes give, for a launcher to trust as well; empty when they
// give none or no server has started.
func (w *World) CAFile() string {
	f := filepath.Join(w.Dir, caFile)
	if _, err := os.Stat(f); err != nil {
		return ""
	}
	return f
}

// KEK is the secrets.kek settings the scenario's servers and commands
// run with; nil is the local KEK.
func (w *World) KEK() map[string]any {
	k, _ := w.Settings["secrets"]["kek"].(map[string]any)
	return k
}

// SetKEK makes kek the secrets.kek settings of the servers the scenario
// starts from now on and of its commands.
func (w *World) SetKEK(kek map[string]any) { w.Settings["secrets"] = SecretsWith(kek) }

// Launch runs one more infraweave process mode with config and gives the
// scenario SDK clients for its listeners.
func (w *World) Launch(ctx context.Context, mode string, config []byte) (*Server, error) {
	bin, err := Binary(ctx)
	if err != nil {
		return nil, err
	}
	dir := w.Dir
	if len(w.Servers) > 0 {
		dir = filepath.Join(w.Dir, fmt.Sprintf("server-%d", len(w.Servers)+1))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("server dir: %w", err)
		}
	}
	srv, err := start(ctx, bin, dir, w.Suite.CoverDir, mode, config, w.env)
	if err != nil {
		return nil, err
	}
	w.Servers = append(w.Servers, srv)
	if len(w.Servers) == 1 {
		if w.Admin, err = sdk.New("http://"+srv.Admin, sdk.WithRetry(sdk.NoRetry)); err != nil {
			return nil, fmt.Errorf("sdk: %w", err)
		}
	}
	if srv.API == "" {
		return srv, nil
	}
	// Every route but a few needs a credential, so the scenario's clients
	// hold a token of the home tenant's key.
	// ref: decision 0006
	db, err := w.Querier(ctx)
	if err != nil {
		return nil, err
	}
	api, tok, err := Authenticated(ctx, db, "http://"+srv.API, Home)
	if err != nil {
		return nil, fmt.Errorf("API client: %w", err)
	}
	w.APIs, w.APITokens = append(w.APIs, api), append(w.APITokens, tok)
	if len(w.Servers) == 1 {
		w.API = api
	}
	return srv, nil
}
