package assembly

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/weaveplatform/infraweave/internal/events/internal/adapters/metrics"
	"github.com/weaveplatform/infraweave/internal/events/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/events/internal/core/delivery"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/cron"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

// Settings are keyed below events.deliveries.
type Settings struct {
	Retention time.Duration `mapstructure:"retention"`
}

// Defaults retains delivery idempotency longer than the outbox's events.
func Defaults() Settings { return Settings{Retention: 7 * 24 * time.Hour} }

// ErrConfig reports invalid delivery retention.
var ErrConfig = errors.New("invalid event deliveries configuration")

// Validate checks the domain's retention; app also checks it against the
// separately owned outbox retention.
func (s Settings) Validate() error {
	if s.Retention <= 0 {
		return fmt.Errorf("%w: events.deliveries.retention must be positive", ErrConfig)
	}
	return nil
}

// Prune is the hourly delivery-record retention task.
var Prune = queue.Kind{Name: "events.deliveries.prune", Queue: queue.Maintenance, Policy: queue.Ignore}

// Deps binds the platform and VCS feedback to the sinks domain.
type Deps struct {
	DB       queue.DB
	Feedback vcsapi.Feedback
	Settings Settings
	Meter    metric.Meter
	Now      func() time.Time
}

// Domain is the events sinks hexagon, with no public HTTP operation.
type Domain struct {
	sinks     *delivery.Service
	feedback  vcsapi.Feedback
	retention time.Duration
}

// New constructs the sinks consumer and its metric on d.
func New(d Deps) (*Domain, error) {
	if err := d.Settings.Validate(); err != nil {
		return nil, err
	}
	meter := d.Meter
	if meter == nil {
		meter = otel.Meter("github.com/weaveplatform/infraweave/internal/events")
	}
	observed, err := metrics.New(meter)
	if err != nil {
		return nil, err //nolint:wrapcheck // names the instrument
	}
	q := queue.New(d.DB)
	service := delivery.New(delivery.Deps{
		Store: pg.New(d.DB), Feedback: d.Feedback, Complete: q.CompleteTx, Metrics: observed, Now: d.Now,
		Atomic: func(ctx context.Context, run func(context.Context) error) error {
			return tx.Do(ctx, d.DB, tx.Options{}, run) //nolint:wrapcheck // each step names itself
		},
	})
	return &Domain{sinks: service, feedback: d.Feedback, retention: d.Settings.Retention}, nil
}

// Stream is the events domain's migration stream.
func Stream() migrate.Stream { return pg.Stream() }

// Held is the account reference removed by the account purge.
func Held() []integrity.Held { return pg.Held() }

// Subscribe binds the sinks consumer to VCS feedback's event kinds.
func (d *Domain) Subscribe(events *outbox.Outbox) error {
	for _, kind := range d.feedback.Kinds() {
		if err := events.Subscribe(kind, delivery.Consumer, d.Consume); err != nil {
			return err //nolint:wrapcheck // names the subscription
		}
	}
	return nil
}

// Consume handles one claimed sinks event; Subscribe binds this handler
// to the kinds VCS feedback names.
func (d *Domain) Consume(ctx context.Context, event outbox.Event, job queue.Job) error {
	return d.sinks.Consume(ctx, event, job) //nolint:wrapcheck // the consumer names each failure
}

// Cron is the delivery record's hourly retention task.
func (d *Domain) Cron() []cron.Task {
	return []cron.Task{{
		Kind: Prune, Every: time.Hour,
		Handler: func(ctx context.Context, _ queue.Job) error { return d.sinks.Prune(ctx, d.retention) },
	}}
}

// Purge deletes a removed account's retained deliveries.
func (d *Domain) Purge(ctx context.Context, account kernel.AccountID) error {
	return d.sinks.Purge(ctx, account.ULID) //nolint:wrapcheck // the store names the purge
}
