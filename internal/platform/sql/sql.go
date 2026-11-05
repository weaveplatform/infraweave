package sql

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config holds the database settings, decision 0005's database.* keys.
type Config struct {
	// URL is the primary's connection string, database.url.
	URL string `mapstructure:"url"`
	// ReadURL is an optional replica for reads that tolerate lag,
	// database.read_url.
	ReadURL string `mapstructure:"read_url"`
	// MaxConns is the pool size per replica, database.max_conns.
	MaxConns int32 `mapstructure:"max_conns"`
	// StatementTimeout is set on each connection, database.statement_timeout.
	StatementTimeout time.Duration `mapstructure:"statement_timeout"`
	// IdleInTransactionTimeout is each connection's
	// idle_in_transaction_session_timeout,
	// database.idle_in_transaction_timeout.
	IdleInTransactionTimeout time.Duration `mapstructure:"idle_in_transaction_timeout"`
	// Migrate is what a process mode does with pending migrations at start,
	// database.migrate: MigrateAuto or MigrateCheck.
	Migrate string `mapstructure:"migrate"`
}

// The database.migrate values: auto applies pending migrations at start;
// check refuses to start while any are pending, for operators who run
// infraweave migrate up as a separate job.
const (
	MigrateAuto  = "auto"
	MigrateCheck = "check"
)

// Defaults are the settings an empty configuration means.
func Defaults() Config {
	return Config{
		MaxConns:                 20,
		StatementTimeout:         30 * time.Second,
		IdleInTransactionTimeout: 60 * time.Second,
		Migrate:                  MigrateAuto,
	}
}

// ErrConfig marks an invalid database setting.
var ErrConfig = errors.New("invalid database configuration")

// Validate rejects a setting the pool could not open with. An empty URL is
// valid: the process modes that need a database say so themselves.
func (c Config) Validate() error {
	var errs []error
	for _, u := range []struct{ key, url string }{
		{"database.url", c.URL},
		{"database.read_url", c.ReadURL},
	} {
		if u.url == "" {
			continue
		}
		// pgconn redacts the password from its parse errors.
		if _, err := pgxpool.ParseConfig(u.url); err != nil {
			errs = append(errs, fmt.Errorf("%w: %s: %w", ErrConfig, u.key, err))
		}
	}
	if c.ReadURL != "" && c.URL == "" {
		errs = append(errs, fmt.Errorf("%w: database.read_url needs database.url", ErrConfig))
	}
	if c.MaxConns < 1 {
		errs = append(errs, fmt.Errorf("%w: database.max_conns must be at least 1, got %d",
			ErrConfig, c.MaxConns))
	}
	for _, d := range []struct {
		key string
		v   time.Duration
	}{
		{"database.statement_timeout", c.StatementTimeout},
		{"database.idle_in_transaction_timeout", c.IdleInTransactionTimeout},
	} {
		// PostgreSQL reads 0 as "no limit", and whole milliseconds only.
		if d.v < time.Millisecond {
			errs = append(errs, fmt.Errorf("%w: %s must be at least 1ms, got %s",
				ErrConfig, d.key, d.v))
		}
	}
	if c.Migrate != MigrateAuto && c.Migrate != MigrateCheck {
		errs = append(errs, fmt.Errorf("%w: database.migrate must be %s or %s, got %q",
			ErrConfig, MigrateAuto, MigrateCheck, c.Migrate))
	}
	return errors.Join(errs...)
}

// FloorVersion is the oldest server_version_num infraweave runs on:
// PostgreSQL 18, the newest major every managed service of decision 0005
// offers generally available. A floor raise changes it and the message of
// ErrBelowFloor together.
const FloorVersion = 180000

// ErrBelowFloor reports a server older than FloorVersion.
var ErrBelowFloor = errors.New("infraweave needs PostgreSQL 18 or newer")

// DB is the process's PostgreSQL: the primary pool and, when
// database.read_url is set, a pool on the replica.
type DB struct {
	primary, read *pgxpool.Pool
}

// Open opens the primary pool and, when configured, the read pool, and
// refuses a server below FloorVersion. It connects once to each before it
// returns, so an unreachable or too old database fails the start.
func Open(ctx context.Context, c Config) (*DB, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if c.URL == "" {
		return nil, fmt.Errorf("%w: database.url is required", ErrConfig)
	}
	primary, err := open(ctx, "database.url", c.URL, c)
	if err != nil {
		return nil, err
	}
	db := &DB{primary: primary, read: primary}
	if c.ReadURL != "" {
		if db.read, err = open(ctx, "database.read_url", c.ReadURL, c); err != nil {
			primary.Close()
			return nil, err
		}
	}
	return db, nil
}

func open(ctx context.Context, key, url string, c Config) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrConfig, key, err)
	}
	pc.MaxConns = c.MaxConns
	setSessionParams(pc.ConnConfig, c)
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	if err := checkFloor(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return pool, nil
}

// setSessionParams sets the per-connection timeouts in the startup
// message, so a connection carries them from its first statement;
// ConnConfig's copy carries them too.
func setSessionParams(cc *pgx.ConnConfig, c Config) {
	cc.RuntimeParams["statement_timeout"] = strconv.FormatInt(c.StatementTimeout.Milliseconds(), 10)
	cc.RuntimeParams["idle_in_transaction_session_timeout"] = strconv.FormatInt(
		c.IdleInTransactionTimeout.Milliseconds(), 10)
}

type versionQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func checkFloor(ctx context.Context, q versionQuerier) error {
	var num int
	var version string
	err := q.QueryRow(ctx,
		"SELECT current_setting('server_version_num')::int, current_setting('server_version')").
		Scan(&num, &version)
	if err != nil {
		return fmt.Errorf("read the server version: %w", err)
	}
	if num < FloorVersion {
		return fmt.Errorf("%w; the server runs PostgreSQL %s", ErrBelowFloor, version)
	}
	return nil
}

// Primary is the pool on the primary, for writes and for every read that
// must see them.
func (d *DB) Primary() *pgxpool.Pool { return d.primary }

// Read is the pool for reads that tolerate replica lag: lists, searches and
// audit queries. It is never used inside a write, an idempotent request or
// a worker poll. Without database.read_url it is the primary.
func (d *DB) Read() *pgxpool.Pool { return d.read }

// ConnConfig is a copy of the primary's connection settings for a
// dedicated connection outside the pools: package notify's listener and the
// migration runner's session, which holds its advisory lock.
func (d *DB) ConnConfig() *pgx.ConnConfig { return d.primary.Config().ConnConfig.Copy() }

// Close closes both pools.
func (d *DB) Close() {
	if d.read != d.primary {
		d.read.Close()
	}
	d.primary.Close()
}
