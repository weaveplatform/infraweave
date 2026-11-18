package metrics_test

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/weaveplatform/infraweave/internal/events/internal/adapters/metrics"
)

func TestUnitDeliveryMetricSDKCounts(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	m, err := metrics.New(provider.Meter("events-test"))
	if err != nil {
		t.Fatal(err)
	}
	m.Add(t.Context(), "delivered", 2)
	m.Add(t.Context(), "delivered", 3)
	m.Add(t.Context(), "skipped", 1)
	var data metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &data); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{}
	for _, scope := range data.ScopeMetrics {
		for _, instrument := range scope.Metrics {
			if instrument.Name != "infraweave.events.deliveries" {
				t.Fatalf("unexpected instrument %s", instrument.Name)
			}
			sum, ok := instrument.Data.(metricdata.Sum[int64])
			if !ok || !sum.IsMonotonic {
				t.Fatalf("not a counter: %+v", instrument.Data)
			}
			for _, point := range sum.DataPoints {
				sink, _ := point.Attributes.Value(attribute.Key("sink"))
				outcome, _ := point.Attributes.Value(attribute.Key("outcome"))
				if sink.AsString() != "commit_checks" || point.Attributes.Len() != 2 {
					t.Fatalf("attributes %+v", point.Attributes)
				}
				counts[outcome.AsString()] += point.Value
			}
		}
	}
	if counts["delivered"] != 5 || counts["skipped"] != 1 || len(counts) != 2 {
		t.Fatalf("counts %v", counts)
	}
}

type brokenMeter struct{ metric.Meter }

var errCounter = errors.New("counter unavailable")

func (brokenMeter) Int64Counter(string, ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	return nil, errCounter
}

func TestUnitDeliveryMetricConstructionFailure(t *testing.T) {
	if _, err := metrics.New(brokenMeter{}); !errors.Is(err, errCounter) {
		t.Fatalf("New = %v", err)
	}
}
