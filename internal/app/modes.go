package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/idempotency"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/serve"
	"github.com/weaveplatform/infraweave/internal/platform/observe"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
)

// Start starts the serve process mode's listeners with g as the controls'
// state: the API on the API listener and the probes on the admin listener.
// ctx bounds only the binding of the listeners. Every setting is checked
// before either listener binds.
//
// serve's errors already name the listener or setting that failed, so they
// are returned as they are.
func Start(
	ctx context.Context,
	c config.Config,
	log *slog.Logger,
	g Guards,
) (*serve.Running, error) {
	if err := c.Server.Validate(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	api, probes := API(log, c.Server, g), observe.Probes(log)
	return serve.Start(ctx, c.Server.Config, log, api, probes) //nolint:wrapcheck // see above
}

// Serve runs the serve process mode until ctx is done, then drains in-flight
// requests (serve.Run). It opens PostgreSQL and brings the schema to this
// binary's (openDatabase) before either listener binds, so a server below
// the version floor or a schema this binary must not run on never answers.
// The controls keep their state in the database (postgresGuards); the
// listening connection and the pools close only after the drain, when no
// request can use them.
func Serve(ctx context.Context, c config.Config, log *slog.Logger) error {
	if err := errors.Join(c.Server.Validate(), c.Database.Validate()); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	db, err := openDatabase(ctx, c.Database, log, Streams())
	if err != nil {
		return err
	}
	defer db.Close()
	// ctx ends when the drain begins; the switch keeps following
	// invalidations until the drain is over.
	background, stop := context.WithCancel(context.WithoutCancel(ctx))
	var wg sync.WaitGroup
	defer func() {
		stop()
		wg.Wait()
	}()
	g := postgresGuards(background, &wg, c.Server, db, log)
	api, probes := API(log, c.Server, g), observe.Probes(log)
	return serve.Run(ctx, c.Server.Config, log, api, probes) //nolint:wrapcheck // as Start
}

// postgresGuards keeps the controls' state in db, shared by every replica
// (decision 0005). The disabled callers are both lists, the configuration's
// and api_disabled_callers; the table's copy follows infraweave_invalidate
// through a listener that runs, with wg, until ctx ends.
func postgresGuards(
	ctx context.Context,
	wg *sync.WaitGroup,
	c config.Server,
	db *sql.DB,
	log *slog.Logger,
) Guards {
	l := notify.NewListener(db.ConnConfig, log)
	sub, _ := l.Subscribe(notify.Invalidate) // one of notify.Channels, so never an error
	table := ratelimit.NewTable(db.Primary(), time.Now)
	wg.Go(func() { l.Run(ctx) })
	wg.Go(func() { table.Watch(ctx, sub) })
	return Guards{
		Switch:  ratelimit.Switches{ratelimit.NewCallers(c.RateLimit.DisabledCallers), table},
		Buckets: ratelimit.NewPostgres(db.Primary()),
		Keys:    idempotency.NewPostgres(db.Primary(), c.Idempotency),
	}
}
