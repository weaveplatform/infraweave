package assembly

import (
	"errors"
	"slices"
	"testing"

	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

var errInstrumentUnavailable = errors.New("instrument backend unavailable")

type unavailableInstrument struct {
	metric.Meter
	name  string
	names []string
}

func (m *unavailableInstrument) Int64Counter(name string, options ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	m.names = append(m.names, name)
	if name == m.name {
		return nil, errInstrumentUnavailable
	}
	return m.Meter.Int64Counter(name, options...)
}

// Instrument availability is part of constructing the whole VCS domain. No
// caller receives a partial service when either required refusal counter fails.
func TestUnitDomainRefusesUnavailableInstrumentation(t *testing.T) {
	t.Parallel()
	names := []string{"infraweave.vcs.check_action.refused", "infraweave.vcs.webhook.rejected"}
	for index, name := range names {
		t.Run(name, func(t *testing.T) {
			provider := sdkmetric.NewMeterProvider()
			t.Cleanup(func() {
				if err := provider.Shutdown(t.Context()); err != nil {
					t.Error(err)
				}
			})
			meter := &unavailableInstrument{Meter: provider.Meter("infraweave/vcs"), name: name}
			domain, err := New(Deps{Meter: meter})
			if domain != nil || !errors.Is(err, errInstrumentUnavailable) || !slices.Equal(meter.names, names[:index+1]) {
				t.Fatal("VCS returned a usable service or lost instrument failure", domain, err, meter.names)
			}
		})
	}
}
