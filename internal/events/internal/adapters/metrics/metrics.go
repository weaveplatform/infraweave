package metrics

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/weaveplatform/infraweave/internal/events/internal/core/delivery"
	"github.com/weaveplatform/infraweave/internal/events/internal/core/port"
)

// Metrics records committed feedback fan-out through OpenTelemetry.
type Metrics struct{ deliveries metric.Int64Counter }

// New creates the deliveries counter. Export remains the process's
// responsibility; no provider is installed or replaced here.
func New(meter metric.Meter) (*Metrics, error) {
	counter, err := meter.Int64Counter("infraweave.events.deliveries",
		metric.WithDescription("Committed sink deliveries and events without a target"))
	if err != nil {
		return nil, fmt.Errorf("create event deliveries metric: %w", err)
	}
	return &Metrics{deliveries: counter}, nil
}

// Add records only a committed outcome, with a bounded sink and outcome.
func (m *Metrics) Add(ctx context.Context, outcome string, count int64) {
	m.deliveries.Add(ctx, count, metric.WithAttributes(
		attribute.String("sink", delivery.CommitChecks), attribute.String("outcome", outcome)))
}

var _ port.Metrics = (*Metrics)(nil)
