package observe

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// CheckActionRefused records committed button refusals, by fixed core reason.
// No delivery data, target identity, sender or credential becomes a label.
func CheckActionRefused(meter metric.Meter) (func(context.Context, string), error) {
	counter, err := meter.Int64Counter(
		"infraweave.vcs.check_action.refused",
		metric.WithDescription("GitHub check actions refused by current VCS rules"),
		metric.WithUnit("{action}"),
	)
	if err != nil {
		return nil, fmt.Errorf("vcs: check action counter: %w", err)
	}
	return func(ctx context.Context, reason string) {
		counter.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", reason)))
	}, nil
}
