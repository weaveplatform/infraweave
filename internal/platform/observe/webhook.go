package observe

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// WebhookRejected records refused VCS signatures through meter. Callers
// supply a registered provider and a fixed reason, never request contents,
// so an unauthenticated sender cannot create unbounded metric series.
//
// ref: decisions 0012 and 0017
func WebhookRejected(meter metric.Meter) (func(context.Context, string, string), error) {
	counter, err := meter.Int64Counter("infraweave.vcs.webhook.rejected",
		metric.WithDescription("VCS webhook deliveries refused during verification"),
		metric.WithUnit("{delivery}"))
	if err != nil {
		return nil, fmt.Errorf("observe: VCS webhook rejection counter: %w", err)
	}
	return func(ctx context.Context, provider, reason string) {
		counter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("provider", provider), attribute.String("reason", reason)))
	}, nil
}
