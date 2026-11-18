package assembly_test

import (
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/weaveplatform/infraweave/internal/events/assembly"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

type feedback struct{ vcsapi.Feedback }

func (feedback) Kinds() []string { return []string{"run_updated"} }

func TestUnitDomainBindings(t *testing.T) {
	d, err := assembly.New(assembly.Deps{Feedback: feedback{}, Settings: assembly.Defaults()})
	if err != nil {
		t.Fatal(err)
	}
	box := outbox.New(nil, queue.New(nil), outbox.NewKinds())
	if err := d.Subscribe(box); err != nil {
		t.Fatal(err)
	}
	if err := d.Subscribe(box); !errors.Is(err, outbox.ErrConsumer) {
		t.Fatalf("duplicate subscription = %v", err)
	}
	tasks := d.Cron()
	if len(tasks) != 1 || tasks[0].Kind != assembly.Prune || tasks[0].Every != time.Hour {
		t.Fatalf("periodic tasks %+v", tasks)
	}
	if s := assembly.Stream(); s.Name != "events" {
		t.Fatalf("stream %s", s.Name)
	}
	held := assembly.Held()
	if len(held) != 1 || held[0].Entity != "account" || held[0].Table != "event_deliveries" {
		t.Fatalf("held %+v", held)
	}
}

var errMeter = errors.New("meter failed")

type brokenMeter struct{ metric.Meter }

func (brokenMeter) Int64Counter(string, ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	return nil, errMeter
}
func TestUnitDomainConstructionRefusals(t *testing.T) {
	if _, err := assembly.New(assembly.Deps{}); !errors.Is(err, assembly.ErrConfig) {
		t.Fatalf("missing retention = %v", err)
	}
	if _, err := assembly.New(assembly.Deps{Feedback: feedback{}, Settings: assembly.Defaults(), Meter: brokenMeter{}}); !errors.Is(err, errMeter) {
		t.Fatalf("metric refusal = %v", err)
	}
}

func TestUnitSettingsRetention(t *testing.T) {
	if got := assembly.Defaults().Retention; got != 7*24*time.Hour {
		t.Fatalf("retention %s", got)
	}
	for _, retention := range []time.Duration{0, -time.Hour} {
		if err := (assembly.Settings{Retention: retention}).Validate(); !errors.Is(err, assembly.ErrConfig) {
			t.Fatalf("retention %s: %v", retention, err)
		}
	}
}
