package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/serve"
	"github.com/weaveplatform/infraweave/internal/platform/observe"
)

// Start starts the serve process mode: the API on the API listener and the
// probes on the admin listener. ctx bounds only the binding of the
// listeners. Every setting is checked before either listener binds.
//
// serve's errors already name the listener or setting that failed, so they
// are returned as they are.
func Start(ctx context.Context, c config.Config, log *slog.Logger) (*serve.Running, error) {
	if err := c.Server.Validate(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	api, probes := API(log, c.Server), observe.Probes(log)
	return serve.Start(ctx, c.Server.Config, log, api, probes) //nolint:wrapcheck // see above
}

// Serve runs the serve process mode until ctx is done, then drains in-flight
// requests (serve.Run). With database.url set it opens PostgreSQL and brings
// the schema to this binary's (openDatabase) before either listener binds,
// so a server below the version floor or a schema this binary must not run
// on never answers, and closes the pools only after the drain, when no
// request can use them.
// Without it the mode serves without a database: no operation reads one yet.
func Serve(ctx context.Context, c config.Config, log *slog.Logger) error {
	if err := errors.Join(c.Server.Validate(), c.Database.Validate()); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if c.Database.URL != "" {
		db, err := openDatabase(ctx, c.Database, log, Streams())
		if err != nil {
			return err
		}
		defer db.Close()
	}
	api, probes := API(log, c.Server), observe.Probes(log)
	return serve.Run(ctx, c.Server.Config, log, api, probes) //nolint:wrapcheck // as Start
}
