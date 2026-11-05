package migrate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ApplicationName names the runner's session in pg_stat_activity.
const ApplicationName = "infraweave-migrate"

var (
	// ErrRefused marks a schema this binary must not run on: a changed
	// file, a diverged schema, a binary too old for it, or an upgrade that
	// skips a major. Its message names the stream, the versions and what
	// to do.
	ErrRefused = errors.New("the database schema does not fit this binary")
	// ErrPending marks pending migrations under database.migrate: check.
	ErrPending = errors.New("migrations are pending")
)

// lockKey is the session advisory lock every runner takes before it reads
// the bookkeeping: the FNV-1a hash of infraweave.migrate (decision 0005),
// the only advisory lock infraweave takes.
var lockKey = func() int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("infraweave.migrate"))
	return int64(h.Sum64()) //nolint:gosec // a bit pattern, not a quantity
}()

const bookkeeping = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	stream text NOT NULL,
	version int NOT NULL,
	name text NOT NULL,
	checksum bytea NOT NULL,
	applied_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (stream, version)
);
COMMENT ON TABLE schema_migrations IS 'global: the migration runner''s record of applied files';
CREATE TABLE IF NOT EXISTS schema_state (
	stream text PRIMARY KEY,
	compatible_from int NOT NULL DEFAULT 0,
	release text NOT NULL DEFAULT ''
);
COMMENT ON TABLE schema_state IS 'global: the oldest version of each stream a binary may know';
`

// Applied is a migration recorded in schema_migrations.
type Applied struct {
	Version   int
	Name      string
	Checksum  []byte
	AppliedAt time.Time
}

// Status is one stream's migrations: those applied, those this binary
// would apply, and the stream's schema_state.
type Status struct {
	Stream  string
	Applied []Applied
	Pending []File
	// Ran is what this call of Up applied.
	Ran            []File
	CompatibleFrom int
	// Release is the infraweave release that last applied a file of the
	// stream.
	Release string
}

// Up applies every pending file of streams, in order, on a connection of
// its own made from cfg, and returns each stream's status after it. It
// refuses, applying nothing, when Inspect would report ErrRefused. release
// is recorded in schema_state for each stream it changes.
func Up(
	ctx context.Context,
	cfg *pgx.ConnConfig,
	release string,
	streams []Stream,
) ([]Status, error) {
	loaded, err := loadAll(streams)
	if err != nil {
		return nil, err
	}
	conn, err := connect(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	// The lock is the session's, so closing the connection releases it
	// whatever happens below; the unlock only returns it sooner.
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		return nil, fmt.Errorf("migrate: lock: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", lockKey) }()
	if _, err := conn.Exec(ctx, bookkeeping); err != nil {
		return nil, fmt.Errorf("migrate: bookkeeping tables: %w", err)
	}
	statuses, err := inspect(ctx, conn, streams, loaded)
	if err != nil {
		return nil, err
	}
	for i := range statuses {
		s := &statuses[i]
		for _, f := range s.Pending {
			if err := apply(ctx, conn, s.Stream, release, f); err != nil {
				return nil, fmt.Errorf("migrate: %s %05d_%s: %w", s.Stream, f.Version, f.Name, err)
			}
			s.Applied = append(
				s.Applied,
				Applied{Version: f.Version, Name: f.Name, Checksum: f.Checksum},
			)
			s.Ran = append(s.Ran, f)
			s.CompatibleFrom = max(s.CompatibleFrom, f.CompatibleFrom)
			s.Release = release
		}
		s.Pending = nil
	}
	return statuses, nil
}

// Check returns ErrPending, naming the files, when any stream has pending
// migrations, and ErrRefused when Inspect would. It writes nothing, so it
// suits replicas run with database.migrate: check.
func Check(ctx context.Context, cfg *pgx.ConnConfig, streams []Stream) error {
	statuses, err := Inspect(ctx, cfg, streams)
	if err != nil {
		return err
	}
	var pending []string
	for _, s := range statuses {
		for _, f := range s.Pending {
			pending = append(pending, fmt.Sprintf("%s %05d_%s", s.Stream, f.Version, f.Name))
		}
	}
	if len(pending) > 0 {
		return fmt.Errorf(
			"%w: %s; run infraweave migrate up",
			ErrPending,
			strings.Join(pending, ", "),
		)
	}
	return nil
}

// Inspect reads each stream's status without taking the lock or writing.
// A schema this binary must not run on returns ErrRefused with every reason
// joined.
func Inspect(ctx context.Context, cfg *pgx.ConnConfig, streams []Stream) ([]Status, error) {
	loaded, err := loadAll(streams)
	if err != nil {
		return nil, err
	}
	conn, err := connect(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	return inspect(ctx, conn, streams, loaded)
}

func loadAll(streams []Stream) ([][]File, error) {
	loaded := make([][]File, len(streams))
	seen := map[string]bool{}
	for i, s := range streams {
		if seen[s.Name] {
			return nil, fmt.Errorf("%w: %s is registered twice", ErrStream, s.Name)
		}
		seen[s.Name] = true
		files, err := s.Load()
		if err != nil {
			return nil, err
		}
		loaded[i] = files
	}
	return loaded, nil
}

// connect opens the runner's session. Its statement timeout is lifted: the
// lock waits as long as another runner holds it, and a migration such as
// CREATE INDEX CONCURRENTLY runs as long as the table needs; ctx bounds
// both.
func connect(ctx context.Context, cfg *pgx.ConnConfig) (*pgx.Conn, error) {
	cfg = cfg.Copy()
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	cfg.RuntimeParams["statement_timeout"] = "0"
	cfg.RuntimeParams["application_name"] = ApplicationName
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("migrate: connect: %w", err)
	}
	return conn, nil
}

type record struct {
	applied        []Applied
	compatibleFrom int
	release        string
}

// read returns the bookkeeping by stream; a database without it has
// nothing applied.
func read(ctx context.Context, conn *pgx.Conn) (map[string]*record, error) {
	out := map[string]*record{}
	get := func(stream string) *record {
		if out[stream] == nil {
			out[stream] = &record{}
		}
		return out[stream]
	}
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('schema_state') IS NOT NULL").
		Scan(&exists); err != nil {
		return nil, fmt.Errorf("migrate: read: %w", err)
	}
	if !exists {
		return out, nil
	}
	rows, err := conn.Query(
		ctx,
		"SELECT stream, version, name, checksum, applied_at FROM schema_migrations ORDER BY stream, version",
	)
	if err != nil {
		return nil, fmt.Errorf("migrate: read: %w", err)
	}
	var stream string
	var a Applied
	if _, err := pgx.ForEachRow(
		rows,
		[]any{&stream, &a.Version, &a.Name, &a.Checksum, &a.AppliedAt},
		func() error {
			r := get(stream)
			r.applied = append(r.applied, a)
			return nil
		},
	); err != nil {
		return nil, fmt.Errorf("migrate: read: %w", err)
	}
	rows, err = conn.Query(ctx, "SELECT stream, compatible_from, release FROM schema_state")
	if err != nil {
		return nil, fmt.Errorf("migrate: read: %w", err)
	}
	var from int
	var release string
	if _, err := pgx.ForEachRow(rows, []any{&stream, &from, &release}, func() error {
		r := get(stream)
		r.compatibleFrom, r.release = from, release
		return nil
	}); err != nil {
		return nil, fmt.Errorf("migrate: read: %w", err)
	}
	return out, nil
}

func inspect(
	ctx context.Context,
	conn *pgx.Conn,
	streams []Stream,
	loaded [][]File,
) ([]Status, error) {
	records, err := read(ctx, conn)
	if err != nil {
		return nil, err
	}
	fresh := true
	for _, r := range records {
		fresh = fresh && len(r.applied) == 0
	}
	var refusals []string
	statuses := make([]Status, len(streams))
	for i, s := range streams {
		r := records[s.Name]
		delete(records, s.Name)
		if r == nil {
			r = &record{}
		}
		st, why := plan(s, loaded[i], r, fresh)
		statuses[i] = st
		refusals = append(refusals, why...)
	}
	// A stream this binary does not register belongs to a newer release;
	// like a newer file, it is refused only when it says older binaries
	// may not run.
	for _, name := range slices.Sorted(maps.Keys(records)) {
		if from := records[name].compatibleFrom; from > 0 {
			refusals = append(refusals, fmt.Sprintf(
				"stream %s needs a binary that knows its version %05d; this binary does not know the stream (last migrated by %s)",
				name,
				from,
				orUnknown(records[name].release),
			))
		}
	}
	if len(refusals) > 0 {
		return statuses, fmt.Errorf("%w:\n%s", ErrRefused, strings.Join(refusals, "\n"))
	}
	return statuses, nil
}

// plan compares one stream's embedded files with what is applied, and says
// why the binary must not run on it, if it must not.
func plan(s Stream, files []File, r *record, fresh bool) (Status, []string) {
	st := Status{
		Stream:         s.Name,
		Applied:        r.applied,
		CompatibleFrom: r.compatibleFrom,
		Release:        r.release,
	}
	newest := len(files)
	var why []string
	applied := map[int]bool{}
	top := 0
	for _, a := range r.applied {
		applied[a.Version] = true
		top = max(top, a.Version)
		if a.Version > newest {
			continue // a newer release's file; compatible_from decides
		}
		f := files[a.Version-1]
		switch {
		case a.Name != f.Name:
			why = append(why, fmt.Sprintf(
				"stream %s has diverged: version %05d is applied as %s, but this binary has %s",
				s.Name, a.Version, a.Name, f.Name))
		case !bytes.Equal(a.Checksum, f.Checksum):
			why = append(why, fmt.Sprintf(
				"stream %s: %05d_%s changed after it was applied; correct it with a new migration instead",
				s.Name,
				a.Version,
				a.Name,
			))
		}
	}
	for _, f := range files {
		if applied[f.Version] {
			continue
		}
		if f.Version < top {
			why = append(why, fmt.Sprintf(
				"stream %s has diverged: %05d_%s is not applied, but version %05d is",
				s.Name, f.Version, f.Name, top))
		}
		st.Pending = append(st.Pending, f)
	}
	if r.compatibleFrom > newest {
		why = append(why, fmt.Sprintf(
			"stream %s needs a binary that knows its version %05d; this binary's newest is %05d: "+
				"run release %s or newer", s.Name, r.compatibleFrom, newest, orUnknown(r.release)))
	}
	if !fresh && top < s.Floor.Version {
		why = append(why, fmt.Sprintf(
			"stream %s is at version %05d, below this major's upgrade floor %05d: "+
				"upgrade to %s and migrate with it first",
			s.Name, top, s.Floor.Version, s.Floor.Release))
	}
	return st, why
}

// apply runs one file and records it: in one transaction, or for a
// no-transaction file statement by statement and recorded after, so a file
// that fails part way runs again from the start and must be re-runnable.
func apply(ctx context.Context, conn *pgx.Conn, stream, release string, f File) error {
	if f.NoTransaction {
		for _, stmt := range f.statements() {
			if _, err := conn.Exec(ctx, stmt); err != nil {
				return err //nolint:wrapcheck // Up names the file
			}
		}
		return note(ctx, conn, stream, release, f)
	}
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error { //nolint:wrapcheck // Up names the file
		if _, err := tx.Exec(ctx, f.SQL); err != nil {
			return err //nolint:wrapcheck // Up names the file
		}
		return note(ctx, tx, stream, release, f)
	})
}

// execer is a connection or a transaction.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// note records f as applied and raises the stream's compatible_from to its
// header's version.
func note(ctx context.Context, q execer, stream, release string, f File) error {
	if _, err := q.Exec(ctx,
		"INSERT INTO schema_migrations (stream, version, name, checksum) VALUES ($1, $2, $3, $4)",
		stream, f.Version, f.Name, f.Checksum); err != nil {
		return fmt.Errorf("record: %w", err)
	}
	if _, err := q.Exec(ctx, `
INSERT INTO schema_state (stream, compatible_from, release) VALUES ($1, $2, $3)
ON CONFLICT (stream) DO UPDATE
SET compatible_from = greatest(schema_state.compatible_from, excluded.compatible_from),
    release = excluded.release`,
		stream, f.CompatibleFrom, release); err != nil {
		return fmt.Errorf("record: %w", err)
	}
	return nil
}

func orUnknown(release string) string {
	if release == "" {
		return "an unknown release"
	}
	return release
}
