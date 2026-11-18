package assembly

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/cron"
	"github.com/weaveplatform/infraweave/internal/platform/observe"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	adapter "github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// DeliverySettings controls the retention of verified delivery rows; inbox body
// expiry remains the platform object store's separate two-day setting.
type DeliverySettings struct {
	Retention time.Duration `mapstructure:"retention"`
}

// Objects is the platform blob lifecycle used by durable delivery bodies.
type Objects = intake.Objects

// DefaultSettings are the VCS retention and secret rotation defaults.
func DefaultSettings() Settings {
	return Settings{Deliveries: DeliverySettings{Retention: 7 * 24 * time.Hour}, WebhookSecretOverlap: 24 * time.Hour, EventRunsCeiling: 500}
}

// Validate refuses non-positive delivery retention or secret overlap.
func (s Settings) Validate() error {
	if s.EventRunsCeiling < 1 {
		return fmt.Errorf("%w: vcs.event_runs_ceiling must be at least 1", ErrConfig)
	}
	if s.Deliveries.Retention <= 0 {
		return fmt.Errorf("%w: vcs.deliveries.retention must be positive", ErrConfig)
	}
	if s.WebhookSecretOverlap <= 0 {
		return fmt.Errorf("%w: vcs.webhook_secret_overlap must be positive", ErrConfig)
	}
	return nil
}

func newIntake(d Deps, dispatcher intake.Dispatcher, providers map[string]any) (*intake.Service, error) {
	hooks := map[string]port.Webhooks{}
	for name, p := range providers {
		if h, ok := p.(port.Webhooks); ok {
			hooks[name] = h
		}
	}
	rejected, err := observe.WebhookRejected(otel.Meter("infraweave/vcs"))
	if err != nil {
		return nil, fmt.Errorf("vcs: webhook rejection metric: %w", err)
	}
	return intake.New(intake.Deps{
		DB: d.DB, Store: pg.NewDeliveries(d.DB, d.Keys), Objects: d.Objects, Jobs: queue.New(d.DB),
		Providers: hooks, Now: d.Now, Timeout: d.PushEventTimeout, Retention: d.Settings.Deliveries.Retention,
		Rejected: rejected, Log: d.Log, Dispatcher: dispatcher,
	}), nil
}

// WebhookPattern is the route table's protocol entry.
const WebhookPattern = adapter.WebhookPattern

// Routes mounts provider intake; nil domains retain a safe unavailable handler
// for router fixtures that construct no VCS domain.
func (d *Domain) Routes(r chi.Router) {
	var handler http.Handler
	if d == nil {
		handler = adapter.Webhook(nil)
	} else {
		handler = adapter.Webhook(d.intake)
	}
	r.Method(http.MethodPost, WebhookPattern, handler)
}

// Cron is the hourly delivery retention task, distinct from inbox object expiry.
func (d *Domain) Cron() []cron.Task {
	if d == nil {
		return nil
	}
	return []cron.Task{
		{Kind: intake.Prune, Every: time.Hour, Handler: d.pruned},
		{Kind: intake.Reconcile, Every: time.Minute, Handler: d.intake.Reconciled},
	}
}

func (d *Domain) pruned(ctx context.Context, j queue.Job) error {
	if err := d.intake.Pruned(ctx, j); err != nil {
		return err
	}
	return d.requests.Pruned(ctx)
}

// ProcessDelivery is the durable inbox job kind, gated when claimed, never at
// intake, so a tier change cannot lose an otherwise valid delivery.
func (d *Domain) deliveryJob() queue.Registration {
	return queue.Registration{
		Kind: intake.Process, Handler: d.intake.Processed,
		Options: queue.HandlerOptions{Feature: "vcs-integrations"},
	}
}

// purgeDeliveries follows integration purge, whose row locks fence new intake,
// and schedules every retained large body for deletion.
func (d *Domain) purgeDeliveries(ctx context.Context, acct kernel.AccountID) error {
	return d.intake.Purge(ctx, acct) //nolint:wrapcheck // the durable store names the failure
}
