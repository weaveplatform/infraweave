package acceptance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// The credentials of the suite's own containers, which listen on loopback
// only and live as long as one test binary.
const (
	pgUser, pgPassword, pgDatabase = "infraweave", "infraweave", "infraweave"
	storeAccessKey, storeSecretKey = "infraweave", "infraweave-secret" //nolint:gosec // see above
	vaultToken                     = "infraweave-root"                 //nolint:gosec // see above
)

// pgTemplate is the database every scenario's database is copied from.
// Copying it costs one CREATE DATABASE, where migrating each scenario's
// database would cost the whole schema.
//
// ref: decision 0016
const pgTemplate = "iw_template"

// DockerReachable reports whether a Docker daemon answers, through the same
// discovery testcontainers uses (DOCKER_HOST, the Docker context, the
// default socket).
func DockerReachable(ctx context.Context) error {
	p, err := testcontainers.NewDockerProvider()
	if err != nil {
		return fmt.Errorf("docker: %w", err)
	}
	defer p.Close()
	if err := p.Health(ctx); err != nil {
		return fmt.Errorf("docker: %w", err)
	}
	return nil
}

// Database is one database in the shared @postgres container.
type Database struct {
	c    *postgres.PostgresContainer
	name string
	// DSN reaches the database from the host.
	DSN string
}

// Name is the database's name in the container.
func (d *Database) Name() string { return d.name }

// Query runs sql through psql inside the container and returns its
// unaligned output, so a scenario checks the database without a driver.
func (d *Database) Query(ctx context.Context, sql string) (string, error) {
	return d.exec(ctx, "psql", "-U", pgUser, "-d", d.name, "-At", "-c", sql)
}

// Dump is pg_dump of the database, a failing scenario's evidence.
func (d *Database) Dump(ctx context.Context) (string, error) {
	return d.exec(ctx, "pg_dump", "-U", pgUser, d.name)
}

// Drop removes the database, ending any session still connected to it.
func (d *Database) Drop(ctx context.Context) error {
	_, err := d.exec(ctx, "psql", "-U", pgUser, "-d", pgDatabase, "-At", "-c",
		"DROP DATABASE "+d.name+" WITH (FORCE)")
	return err
}

// ErrExec is returned when a command in a container exits non-zero.
var ErrExec = errors.New("container command failed")

func (d *Database) exec(ctx context.Context, cmd ...string) (string, error) {
	code, out, err := d.c.Exec(ctx, cmd, tcexec.Multiplexed())
	if err != nil {
		return "", fmt.Errorf("%s: %w", cmd[0], err)
	}
	var b bytes.Buffer
	if _, err := io.Copy(&b, out); err != nil {
		return "", fmt.Errorf("%s: %w", cmd[0], err)
	}
	text := b.String()
	if code != 0 {
		return text, fmt.Errorf("%w: %s exited %d: %s", ErrExec, cmd[0], code, text)
	}
	return text, nil
}

// ObjectStore is the @objectstore dependency.
type ObjectStore struct {
	c testcontainers.Container
	// Endpoint is the S3 endpoint from the host.
	Endpoint             string
	AccessKey, SecretKey string
}

// Healthy reports whether the store's readiness endpoint answers 200.
func (o *ObjectStore) Healthy(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.Endpoint+ObjectStoreReady, nil)
	if err != nil {
		return false, fmt.Errorf("object store: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("object store: %w", err)
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

// Dependencies starts each dependency the first time a scenario asks for
// it and shares it with every later scenario of the test binary, so a
// dependency no selected scenario needs never starts.
type Dependencies struct {
	// Migrate brings the database at a DSN to infraweave's schema; the
	// template is migrated with it once, so every scenario's database
	// starts migrated. Nil leaves the template empty.
	Migrate func(ctx context.Context, dsn string) error

	pgOnce, storeOnce, oidcOnce, vaultOnce, giteaOnce sync.Once

	pg       *Database
	pgErr    error
	pgCount  atomic.Int64
	store    *ObjectStore
	storeErr error
	issuer   *Issuer
	oidcErr  error
	vault    *Vault
	vaultErr error
	gitea    *Gitea
	giteaErr error
	repos    atomic.Int64
	keys     atomic.Int64

	mu      sync.Mutex
	cleanup []func(context.Context) error
}

// Postgres returns a new database for one scenario, copied from the
// template, so scenarios running in parallel never share rows. The
// container starts on first use.
func (d *Dependencies) Postgres(ctx context.Context) (*Database, error) {
	d.pgOnce.Do(func() { d.pg, d.pgErr = d.startPostgres(ctx) })
	if d.pgErr != nil {
		return nil, d.pgErr
	}
	name := fmt.Sprintf("iw_scenario_%d", d.pgCount.Add(1))
	if _, err := d.pg.Query(ctx, "CREATE DATABASE "+name+" TEMPLATE "+pgTemplate); err != nil {
		return nil, fmt.Errorf("@postgres: %w", err)
	}
	return &Database{
		c:    d.pg.c,
		name: name,
		DSN:  strings.Replace(d.pg.DSN, "/"+pgDatabase+"?", "/"+name+"?", 1),
	}, nil
}

// startPostgres starts the container and creates and migrates the template.
func (d *Dependencies) startPostgres(ctx context.Context) (*Database, error) {
	c, dsn, err := runPostgres(ctx)
	if c != nil {
		d.onClose(func(ctx context.Context) error { return c.Terminate(ctx) })
	}
	if err != nil {
		return nil, err
	}
	db := &Database{c: c, name: pgDatabase, DSN: dsn}
	if _, err := db.Query(ctx, "CREATE DATABASE "+pgTemplate); err != nil {
		return nil, fmt.Errorf("@postgres: %w", err)
	}
	if d.Migrate != nil {
		template := strings.Replace(dsn, "/"+pgDatabase+"?", "/"+pgTemplate+"?", 1)
		if err := d.Migrate(ctx, template); err != nil {
			return nil, fmt.Errorf("@postgres: migrate %s: %w", pgTemplate, err)
		}
	}
	return db, nil
}

// runPostgres starts a PostgreSQL container of the run's image and returns
// the DSN of its default database. The container is returned whenever one
// was created, even with an error, for the caller to terminate.
func runPostgres(
	ctx context.Context,
	opts ...testcontainers.ContainerCustomizer,
) (*postgres.PostgresContainer, string, error) {
	img, err := Postgres()
	if err != nil {
		return nil, "", fmt.Errorf("@postgres: %w", err)
	}
	return runImage(ctx, img, opts...)
}

// runImage is runPostgres with the image img.
func runImage(
	ctx context.Context,
	img string,
	opts ...testcontainers.ContainerCustomizer,
) (*postgres.PostgresContainer, string, error) {
	c, err := postgres.Run(ctx, img, append([]testcontainers.ContainerCustomizer{
		postgres.WithDatabase(pgDatabase),
		postgres.WithUsername(pgUser),
		postgres.WithPassword(pgPassword),
		postgres.BasicWaitStrategies(),
	}, opts...)...)
	if err != nil {
		return c, "", fmt.Errorf("@postgres: %w", err)
	}
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return c, "", fmt.Errorf("@postgres: %w", err)
	}
	return c, dsn, nil
}

// OwnPostgres is a PostgreSQL container of one scenario's own, for a
// scenario that takes PostgreSQL away from a running server; pausing the
// shared container would stall every other scenario.
type OwnPostgres struct {
	c *postgres.PostgresContainer
	// DSN reaches its migrated default database from the host.
	DSN string
}

// OwnPostgres starts a container of the scenario's own and migrates its
// default database. The scenario terminates it. Its host port is fixed
// rather than Docker's choice, so a Stop and Start keep the DSN a running
// server holds.
func (d *Dependencies) OwnPostgres(ctx context.Context) (*OwnPostgres, error) {
	port, err := freePort(ctx)
	if err != nil {
		return nil, fmt.Errorf("@postgres: %w", err)
	}
	c, dsn, err := runPostgres(
		ctx,
		testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			hc.PortBindings = network.PortMap{
				network.MustParsePort("5432/tcp"): {
					{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: port},
				},
			}
		}),
	)
	if err == nil && d.Migrate != nil {
		if err = d.Migrate(ctx, dsn); err != nil {
			err = fmt.Errorf("@postgres: migrate: %w", err)
		}
	}
	if err != nil {
		if c != nil {
			err = errors.Join(err, c.Terminate(ctx))
		}
		return nil, err
	}
	return &OwnPostgres{c: c, DSN: dsn}, nil
}

// Query runs sql in the container's database through psql, as
// Database.Query does.
func (p *OwnPostgres) Query(ctx context.Context, sql string) (string, error) {
	return (&Database{c: p.c, name: pgDatabase}).Query(ctx, sql)
}

// Pause freezes every process of the container, so connections stay open
// and queries go unanswered, as a PostgreSQL that has stopped responding
// looks to a client. Unpause resumes them on the same port, which stopping
// and starting the container would not keep.
func (p *OwnPostgres) Pause(ctx context.Context) error {
	return p.docker(ctx, func(cli *testcontainers.DockerClient) error {
		_, err := cli.ContainerPause(ctx, p.c.GetContainerID(), client.ContainerPauseOptions{})
		return err //nolint:wrapcheck // docker wraps it below
	})
}

// OldPostgres starts a container of the scenario's own running
// ImagePostgresBelowFloor, a release infraweave refuses, whatever image the
// run's other scenarios use. Nothing is migrated: the migration runner
// refuses it too. The scenario terminates it.
func (d *Dependencies) OldPostgres(ctx context.Context) (*OwnPostgres, error) {
	c, dsn, err := runImage(ctx, ImagePostgresBelowFloor)
	if err != nil {
		if c != nil {
			err = errors.Join(err, c.Terminate(ctx))
		}
		return nil, err
	}
	return &OwnPostgres{c: c, DSN: dsn}, nil
}

// Stop stops PostgreSQL as an operator or a failover does: the image stops
// it with a fast shutdown, which ends every session with admin_shutdown,
// and the port then refuses connections.
func (p *OwnPostgres) Stop(ctx context.Context) error {
	timeout := 10 * time.Second
	return p.c.Stop(ctx, &timeout) //nolint:wrapcheck // testcontainers names the container
}

// Start starts a stopped container again on the same port.
func (p *OwnPostgres) Start(ctx context.Context) error {
	return p.c.Start(ctx) //nolint:wrapcheck // as Stop
}

// freePort is a loopback port nothing listens on now.
func freePort(ctx context.Context) (string, error) {
	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("free port: %w", err)
	}
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		return "", fmt.Errorf("free port: %w", err)
	}
	return port, nil
}

// Unpause resumes the container's processes.
func (p *OwnPostgres) Unpause(ctx context.Context) error {
	return p.docker(ctx, func(cli *testcontainers.DockerClient) error {
		_, err := cli.ContainerUnpause(ctx, p.c.GetContainerID(), client.ContainerUnpauseOptions{})
		return err //nolint:wrapcheck // as Pause
	})
}

// Terminate removes the container.
func (p *OwnPostgres) Terminate(ctx context.Context) error {
	return p.c.Terminate(ctx) //nolint:wrapcheck // testcontainers names the container
}

func (p *OwnPostgres) docker(
	ctx context.Context,
	f func(*testcontainers.DockerClient) error,
) error {
	cli, err := testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		return fmt.Errorf("docker: %w", err)
	}
	defer cli.Close()
	if err := f(cli); err != nil {
		return fmt.Errorf("docker: %w", err)
	}
	return nil
}

// ObjectStore returns the shared object store, starting it on first use.
func (d *Dependencies) ObjectStore(ctx context.Context) (*ObjectStore, error) {
	d.storeOnce.Do(func() {
		c, err := testcontainers.Run(ctx, ImageObjectStore,
			testcontainers.WithExposedPorts("9000/tcp"),
			testcontainers.WithEnv(map[string]string{
				"RUSTFS_ACCESS_KEY": storeAccessKey,
				"RUSTFS_SECRET_KEY": storeSecretKey,
			}),
			testcontainers.WithWaitStrategy(wait.ForHTTP(ObjectStoreReady).WithPort("9000/tcp")),
		)
		if c != nil {
			d.onClose(func(ctx context.Context) error { return c.Terminate(ctx) })
		}
		if err != nil {
			d.storeErr = fmt.Errorf("@objectstore: %w", err)
			return
		}
		endpoint, err := c.PortEndpoint(ctx, "9000/tcp", "http")
		if err != nil {
			d.storeErr = fmt.Errorf("@objectstore: %w", err)
			return
		}
		d.store = &ObjectStore{
			c: c, Endpoint: endpoint, AccessKey: storeAccessKey, SecretKey: storeSecretKey,
		}
	})
	return d.store, d.storeErr
}

// Vault is the @vault dependency: Vault in dev mode, with the Transit
// engine mounted at transit, which the vault KEK provider wraps with.
type Vault struct {
	// Addr is Vault's address from the host; Token is its root token.
	Addr, Token string
}

// Vault returns the shared Vault, starting it on first use.
func (d *Dependencies) Vault(ctx context.Context) (*Vault, error) {
	d.vaultOnce.Do(func() {
		c, err := testcontainers.Run(ctx, ImageVault,
			testcontainers.WithExposedPorts("8200/tcp"),
			testcontainers.WithEnv(map[string]string{
				"VAULT_DEV_ROOT_TOKEN_ID": vaultToken,
				// Dev mode keeps everything in memory, so it needs no
				// mlock, and the container no IPC_LOCK.
				"SKIP_SETCAP": "true",
			}),
			testcontainers.WithWaitStrategy(wait.ForHTTP("/v1/sys/health").WithPort("8200/tcp")),
		)
		if c != nil {
			d.onClose(func(ctx context.Context) error { return c.Terminate(ctx) })
		}
		if err != nil {
			d.vaultErr = fmt.Errorf("@vault: %w", err)
			return
		}
		addr, err := c.PortEndpoint(ctx, "8200/tcp", "http")
		if err != nil {
			d.vaultErr = fmt.Errorf("@vault: %w", err)
			return
		}
		v := &Vault{Addr: addr, Token: vaultToken}
		if err := v.post(ctx, "sys/mounts/transit", `{"type":"transit"}`); err != nil {
			d.vaultErr = fmt.Errorf("@vault: %w", err)
			return
		}
		d.vault = v
	})
	return d.vault, d.vaultErr
}

// VaultKey creates a Transit key of the scenario's own, so scenarios
// running in parallel never wrap with one another's key, and returns its
// name.
func (d *Dependencies) VaultKey(ctx context.Context) (string, error) {
	v, err := d.Vault(ctx)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("kek-%d", d.keys.Add(1))
	if err := v.post(ctx, "transit/keys/"+name, `{}`); err != nil {
		return "", fmt.Errorf("@vault: %w", err)
	}
	return name, nil
}

// ErrVault is returned when Vault refuses a call the harness makes.
var ErrVault = errors.New("vault refused the call")

func (v *Vault) post(ctx context.Context, path, body string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.Addr+"/v1/"+path,
		strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	req.Header.Set("X-Vault-Token", v.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusMultipleChoices {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%w: POST %s: %s: %s", ErrVault, path, resp.Status, msg)
	}
	return nil
}

// OIDC returns the shared issuer, starting it on first use.
func (d *Dependencies) OIDC() (*Issuer, error) {
	d.oidcOnce.Do(func() {
		d.issuer, d.oidcErr = StartIssuer()
		if d.issuer != nil {
			d.onClose(func(context.Context) error { d.issuer.Close(); return nil })
		}
	})
	return d.issuer, d.oidcErr
}

func (d *Dependencies) onClose(f func(context.Context) error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cleanup = append(d.cleanup, f)
}

// Close stops every dependency that started.
func (d *Dependencies) Close(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var err error
	for _, f := range d.cleanup {
		err = errors.Join(err, f(ctx))
	}
	d.cleanup = nil
	return err
}
