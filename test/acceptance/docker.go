package acceptance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

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
)

// pgTemplate is the database every scenario's database is copied from
// (decision 0016). Copying it costs one CREATE DATABASE, where migrating
// each scenario's database would cost the whole schema.
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

// Healthy reports whether the store's health endpoint answers 200.
func (o *ObjectStore) Healthy(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.Endpoint+"/health", nil)
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

	pgOnce, storeOnce, oidcOnce sync.Once

	pg       *Database
	pgErr    error
	pgCount  atomic.Int64
	store    *ObjectStore
	storeErr error
	issuer   *Issuer
	oidcErr  error

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
	c, err := postgres.Run(ctx, ImagePostgres,
		postgres.WithDatabase(pgDatabase),
		postgres.WithUsername(pgUser),
		postgres.WithPassword(pgPassword),
		postgres.BasicWaitStrategies(),
	)
	if c != nil {
		d.onClose(func(ctx context.Context) error { return c.Terminate(ctx) })
	}
	if err != nil {
		return nil, fmt.Errorf("@postgres: %w", err)
	}
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, fmt.Errorf("@postgres: %w", err)
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

// ObjectStore returns the shared object store, starting it on first use.
func (d *Dependencies) ObjectStore(ctx context.Context) (*ObjectStore, error) {
	d.storeOnce.Do(func() {
		c, err := testcontainers.Run(ctx, ImageObjectStore,
			testcontainers.WithExposedPorts("9000/tcp"),
			testcontainers.WithEnv(map[string]string{
				"RUSTFS_ACCESS_KEY": storeAccessKey,
				"RUSTFS_SECRET_KEY": storeSecretKey,
			}),
			testcontainers.WithWaitStrategy(wait.ForHTTP("/health").WithPort("9000/tcp")),
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
