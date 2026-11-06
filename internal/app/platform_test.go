package app

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

// widgets is a fixture stream with one file, for the paths the platform
// stream reaches only once it has files.
func widgets() migrate.Stream {
	return migrate.Stream{Name: "widgets", Files: fstest.MapFS{
		"00001_create_widgets.sql": {Data: []byte("CREATE TABLE widgets (id uuid PRIMARY KEY);")},
	}}
}

func exec(t *testing.T, url, stmt string) {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(t.Context(), stmt); err != nil {
		t.Fatal(err)
	}
}

func TestUnitStreams(t *testing.T) {
	s := Streams()
	if len(s) == 0 || s[0].Name != schema.Name {
		t.Fatalf("streams = %+v: the platform stream must come first", s)
	}
}

func TestAccOpenDatabase(t *testing.T) {
	c := sql.Defaults()
	c.URL = sqltest.Empty(t)
	streams := append(Streams(), widgets())

	c.Migrate = sql.MigrateCheck
	if _, err := openDatabase(
		t.Context(),
		c,
		discard(),
		streams,
	); !errors.Is(
		err,
		migrate.ErrPending,
	) ||
		!strings.Contains(err.Error(), "widgets 00001_create_widgets") {
		t.Fatalf("check with a pending file: %v", err)
	}

	c.Migrate = sql.MigrateAuto
	var logs bytes.Buffer
	db, err := openDatabase(t.Context(), c, slog.New(slog.NewTextHandler(&logs, nil)), streams)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if !strings.Contains(
		logs.String(),
		"msg=migrated stream=widgets migration=1 name=create_widgets",
	) {
		t.Errorf("logs: %s", logs.String())
	}

	c.Migrate = sql.MigrateCheck
	db, err = openDatabase(t.Context(), c, discard(), streams)
	if err != nil {
		t.Fatalf("check when up to date: %v", err)
	}
	db.Close()
}

// TestAccServeRefusesNewerSchema checks that serve does not start, in
// either mode, on a database a newer binary migrated and marked
// incompatible with this one.
func TestAccServeRefusesNewerSchema(t *testing.T) {
	url := sqltest.DSN(t)
	exec(
		t,
		url,
		"INSERT INTO schema_state (stream, compatible_from, release) VALUES ('platform', 99, 'v9.0.0') "+
			"ON CONFLICT (stream) DO UPDATE SET compatible_from = 99, release = 'v9.0.0'",
	)
	for _, mode := range []string{sql.MigrateAuto, sql.MigrateCheck} {
		c := local(t)
		c.Database.URL, c.Database.Migrate = url, mode
		err := Run(t.Context(), c, discard(), ModeServe)
		if !errors.Is(err, migrate.ErrRefused) ||
			!strings.Contains(err.Error(), "run release v9.0.0 or newer") {
			t.Errorf("%s: %v", mode, err)
		}
	}
}

func TestAccMigrateCommands(t *testing.T) {
	c := config.Defaults()
	c.Database.URL = sqltest.Empty(t)
	streams := append(Streams(), widgets())
	run := func(f func(context.Context, config.Config, *bytes.Buffer, []migrate.Stream) error) (string, error) {
		var out bytes.Buffer
		err := f(t.Context(), c, &out, streams)
		return out.String(), err
	}
	status := func(ctx context.Context, c config.Config, w *bytes.Buffer, s []migrate.Stream) error {
		return migrateStatus(ctx, c, w, s)
	}
	up := func(ctx context.Context, c config.Config, w *bytes.Buffer, s []migrate.Stream) error {
		return migrateUp(ctx, c, w, s)
	}

	out, err := run(status)
	if err != nil ||
		!strings.Contains(out, "widgets: 0 applied, 1 pending, compatible from 00000, release -\n"+
			"  pending  00001_create_widgets") {
		t.Fatalf("status before up: %v\n%s", err, out)
	}
	// The platform stream grows with each phase, so only its first file and
	// its place before the fixture stream are fixed.
	if out, err := run(up); err != nil ||
		!strings.HasPrefix(out, "applied platform 00001_api_controls\n") ||
		!strings.HasSuffix(out, "\napplied widgets 00001_create_widgets\n") ||
		strings.Count(out, "applied ") != strings.Count(out, "\n") {
		t.Fatalf("up: %v\n%s", err, out)
	}
	if out, err := run(up); err != nil || out != "the schema is up to date\n" {
		t.Fatalf("up again: %v\n%s", err, out)
	}
	out, err = run(status)
	if err != nil || !strings.Contains(out, "widgets: 1 applied, 0 pending") ||
		!strings.Contains(out, "  applied  00001_create_widgets  20") {
		t.Fatalf("status after up: %v\n%s", err, out)
	}

	exec(t, c.Database.URL, "UPDATE schema_state SET compatible_from = 5 WHERE stream = 'widgets'")
	out, err = run(status)
	if !errors.Is(err, migrate.ErrRefused) || !strings.Contains(out, "widgets: 1 applied") {
		t.Errorf("status of a refused schema: %v\n%s", err, out)
	}
	if _, err := run(up); !errors.Is(err, migrate.ErrRefused) {
		t.Errorf("up on a refused schema: %v", err)
	}
	streams = []migrate.Stream{{Name: "Bad", Files: fstest.MapFS{}}}
	if out, err := run(status); !errors.Is(err, migrate.ErrStream) || out != "" {
		t.Errorf("status with a bad stream: %v %q", err, out)
	}
}

func TestUnitMigrateCommandsNeedADatabase(t *testing.T) {
	var out bytes.Buffer
	for name, err := range map[string]error{
		"up":     MigrateUp(t.Context(), config.Defaults(), &out),
		"status": MigrateStatus(t.Context(), config.Defaults(), &out),
	} {
		if !errors.Is(err, sql.ErrConfig) ||
			!strings.Contains(err.Error(), "database.url is required") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// streamOf is the stream a migration file under internal/ belongs to: the
// platform's in platform/sql/schema, otherwise its domain's.
func streamOf(path string) string {
	if strings.HasPrefix(path, "platform/sql/schema/") {
		return schema.Name
	}
	domain, _, _ := strings.Cut(path, "/")
	return domain
}

// TestUnitMigrationNames checks every registered stream's naming and
// numbering, and that every .sql file under internal/ is a file of a
// registered stream, so a stream left out of Streams fails here rather than
// never running.
func TestUnitMigrationNames(t *testing.T) {
	registered := map[string]migrate.Stream{}
	for _, s := range Streams() {
		if _, err := s.Load(); err != nil {
			t.Error(err)
		}
		registered[s.Name] = s
	}
	err := fs.WalkDir(os.DirFS(".."), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".sql" ||
			strings.Contains(path, "/testdata/") {
			return err
		}
		s, ok := registered[streamOf(path)]
		if !ok {
			t.Errorf("%s: stream %s is not registered in Streams", path, streamOf(path))
			return nil
		}
		if _, err := fs.Stat(s.Files, filepath.Base(path)); err != nil {
			t.Errorf("%s is not embedded in stream %s", path, s.Name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := streamOf("stacks/internal/adapters/pg/migrations/00001_x.sql"); got != "stacks" {
		t.Errorf("streamOf a domain file = %s", got)
	}
	if got := streamOf("platform/sql/schema/00001_x.sql"); got != schema.Name {
		t.Errorf("streamOf a platform file = %s", got)
	}
}
