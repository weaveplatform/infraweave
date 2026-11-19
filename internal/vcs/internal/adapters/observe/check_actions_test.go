package observe

import (
	"context"
	"maps"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestUnitCheckActionRefusalCounts(t *testing.T) {
	t.Parallel()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	reject, err := CheckActionRefused(provider.Meter("infraweave/vcs"))
	if err != nil {
		t.Fatal(err)
	}
	reject(t.Context(), "disabled")
	reject(t.Context(), "disabled")
	reject(t.Context(), "insufficient_permission")
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &metrics); err != nil {
		t.Fatal(err)
	}
	if len(metrics.ScopeMetrics) != 1 || len(metrics.ScopeMetrics[0].Metrics) != 1 {
		t.Fatal("unexpected instruments", metrics)
	}
	instrument := metrics.ScopeMetrics[0].Metrics[0]
	sum, ok := instrument.Data.(metricdata.Sum[int64])
	if !ok || !sum.IsMonotonic || instrument.Name != "infraweave.vcs.check_action.refused" || instrument.Unit != "{action}" {
		t.Fatal("refusal counter contract", instrument)
	}
	got := map[string]int64{}
	for _, point := range sum.DataPoints {
		if point.Attributes.Len() != 1 {
			t.Fatal("counter has data-dependent labels", point.Attributes)
		}
		reason, _ := point.Attributes.Value(attribute.Key("reason"))
		got[reason.AsString()] = point.Value
	}
	if !maps.Equal(got, map[string]int64{"disabled": 2, "insufficient_permission": 1}) {
		t.Fatal("committed refusal counts", got)
	}
}
