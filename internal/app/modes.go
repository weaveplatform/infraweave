package app

import (
	"context"
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
// requests (serve.Run).
func Serve(ctx context.Context, c config.Config, log *slog.Logger) error {
	if err := c.Server.Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	api, probes := API(log, c.Server), observe.Probes(log)
	return serve.Run(ctx, c.Server.Config, log, api, probes) //nolint:wrapcheck // as Start
}
