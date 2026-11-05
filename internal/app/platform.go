package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/buildinfo"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
)

// Streams are the migration streams in the declared graph's order: the
// platform's first, since every domain's tables may reference it, then each
// domain's after the streams of every domain it imports (decision 0004).
func Streams() []migrate.Stream {
	return []migrate.Stream{schema.Stream()}
}

// openDatabase opens PostgreSQL and brings its schema to this binary's:
// database.migrate auto applies pending migrations, and check refuses to
// start while any are pending. Either refuses a schema this binary must not
// run on (migrate.ErrRefused).
func openDatabase(
	ctx context.Context,
	c sql.Config,
	log *slog.Logger,
	streams []migrate.Stream,
) (*sql.DB, error) {
	db, cc, err := openWithSession(ctx, c)
	if err != nil {
		return nil, err
	}
	if c.Migrate == sql.MigrateCheck {
		err = migrate.Check(ctx, cc, streams)
	} else {
		var statuses []migrate.Status
		statuses, err = migrate.Up(ctx, cc, buildinfo.Version(), streams)
		for _, s := range statuses {
			for _, f := range s.Ran {
				log.Info("migrated", "stream", s.Stream, "version", f.Version, "name", f.Name)
			}
		}
	}
	if err != nil {
		db.Close()
		return nil, err //nolint:wrapcheck // migrate's errors name the stream and the file
	}
	return db, nil
}

// MigrateUp is infraweave migrate up: it applies every pending migration,
// writes what it applied to w and exits, for an operator who migrates in a
// separate job and runs the process modes with database.migrate: check.
func MigrateUp(ctx context.Context, c config.Config, w io.Writer) error {
	return migrateUp(ctx, c, w, Streams())
}

func migrateUp(ctx context.Context, c config.Config, w io.Writer, streams []migrate.Stream) error {
	db, cc, err := openWithSession(ctx, c.Database)
	if err != nil {
		return err
	}
	defer db.Close()
	statuses, err := migrate.Up(ctx, cc, buildinfo.Version(), streams)
	if err != nil {
		return err //nolint:wrapcheck // as openDatabase
	}
	ran := 0
	for _, s := range statuses {
		for _, f := range s.Ran {
			_, _ = fmt.Fprintf(w, "applied %s %05d_%s\n", s.Stream, f.Version, f.Name)
			ran++
		}
	}
	if ran == 0 {
		_, _ = fmt.Fprintln(w, "the schema is up to date")
	}
	return nil
}

// MigrateStatus is infraweave migrate status: it writes each stream's
// applied and pending migrations, its compatible_from and the release that
// last migrated it to w. A schema this binary must not run on is written
// too, then returned as the error.
func MigrateStatus(ctx context.Context, c config.Config, w io.Writer) error {
	return migrateStatus(ctx, c, w, Streams())
}

func migrateStatus(
	ctx context.Context,
	c config.Config,
	w io.Writer,
	streams []migrate.Stream,
) error {
	db, cc, err := openWithSession(ctx, c.Database)
	if err != nil {
		return err
	}
	defer db.Close()
	statuses, err := migrate.Inspect(ctx, cc, streams)
	if statuses == nil {
		return err //nolint:wrapcheck // as openDatabase
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, s := range statuses {
		release := s.Release
		if release == "" {
			release = "-"
		}
		_, _ = fmt.Fprintf(tw, "%s: %d applied, %d pending, compatible from %05d, release %s\n",
			s.Stream, len(s.Applied), len(s.Pending), s.CompatibleFrom, release)
		for _, a := range s.Applied {
			_, _ = fmt.Fprintf(tw, "  applied\t%05d_%s\t%s\n",
				a.Version, a.Name, a.AppliedAt.UTC().Format(time.RFC3339))
		}
		for _, f := range s.Pending {
			_, _ = fmt.Fprintf(tw, "  pending\t%05d_%s\t\n", f.Version, f.Name)
		}
	}
	return errors.Join(tw.Flush(), err)
}

// openWithSession opens the pools and returns the settings of the migration
// runner's own connection, which under database.iam_auth carries a token
// of its own.
func openWithSession(ctx context.Context, c sql.Config) (*sql.DB, *pgx.ConnConfig, error) {
	db, err := sql.Open(ctx, c)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // sql's errors name the setting
	}
	cc, err := db.ConnConfig(ctx)
	if err != nil {
		db.Close()
		return nil, nil, err //nolint:wrapcheck // as above
	}
	return db, cc, nil
}
