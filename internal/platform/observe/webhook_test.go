package observe

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestUnitWebhookRejected(t *testing.T) {
	t.Parallel()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.WithoutCancel(t.Context())); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	reject, err := WebhookRejected(provider.Meter("infraweave.vcs"))
	if err != nil {
		t.Fatal(err)
	}

	var workers sync.WaitGroup
	for range 20 {
		workers.Go(func() { reject(t.Context(), "GITHUB_ENTERPRISE", "signature") })
	}
	workers.Wait()
	reject(t.Context(), "GITLAB", "signature")
	reject(t.Context(), "GITLAB", "missing_secret")

	var data metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.ScopeMetrics) != 1 || len(data.ScopeMetrics[0].Metrics) != 1 {
		t.Fatalf("want one scoped instrument, got %+v", data.ScopeMetrics)
	}
	instrument := data.ScopeMetrics[0].Metrics[0]
	if instrument.Name != "infraweave.vcs.webhook.rejected" || instrument.Unit != "{delivery}" {
		t.Errorf("instrument %q, unit %q", instrument.Name, instrument.Unit)
	}
	sum, ok := instrument.Data.(metricdata.Sum[int64])
	if !ok || !sum.IsMonotonic || sum.Temporality != metricdata.CumulativeTemporality {
		t.Fatalf("want a cumulative counter, got %+v", instrument.Data)
	}
	got := map[[2]string]int64{}
	for _, point := range sum.DataPoints {
		if point.Attributes.Len() != 2 {
			t.Errorf("unexpected attributes: %v", point.Attributes)
		}
		provider, _ := point.Attributes.Value(attribute.Key("provider"))
		reason, _ := point.Attributes.Value(attribute.Key("reason"))
		got[[2]string{provider.AsString(), reason.AsString()}] = point.Value
	}
	want := map[[2]string]int64{
		{"GITHUB_ENTERPRISE", "signature"}: 20,
		{"GITLAB", "signature"}:            1,
		{"GITLAB", "missing_secret"}:       1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("counter series = %v, want %v", got, want)
	}
}

type refusedMeter struct {
	metric.Meter
	err error
}

func (m refusedMeter) Int64Counter(string, ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	return nil, m.err
}

func TestUnitWebhookRejectedInstrumentFailure(t *testing.T) {
	t.Parallel()
	cause := errors.New("instrument refused")
	reject, err := WebhookRejected(refusedMeter{err: cause})
	if reject != nil || !errors.Is(err, cause) || !strings.Contains(err.Error(), "VCS webhook rejection counter") {
		t.Fatalf("has recorder = %v, error = %v", reject != nil, err)
	}
}
