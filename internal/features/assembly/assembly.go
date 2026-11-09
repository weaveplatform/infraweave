package assembly

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"slices"

	"github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	entitlement "github.com/weaveplatform/infraweave/internal/features/internal/core"
	"github.com/weaveplatform/infraweave/internal/features/internal/http"
	"github.com/weaveplatform/infraweave/internal/features/internal/pg"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// Settings are the features domain's settings, under features (decision
// 0021).
type Settings struct {
	// Disabled are the features switched off for every organization of
	// the installation.
	Disabled []string `mapstructure:"disabled"`
}

// ErrConfig reports a features setting the server could not start with.
var ErrConfig = errors.New("invalid features configuration")

// Validate rejects a features.disabled name that is not a capability or
// option of the catalogue, so a typo, a Spacelift spelling such as SCANS,
// a limit, billing or operator cannot leave a feature silently on or lock
// the operator out.
func (s Settings) Validate() error {
	var errs []error
	for i, name := range s.Disabled {
		f, ok := features.Lookup(name)
		switch {
		case !ok:
			errs = append(
				errs,
				fmt.Errorf("%w: features.disabled names %q, which the catalogue lacks",
					ErrConfig, name),
			)
		case name == features.Operator || f.Kind == features.Switch:
			errs = append(
				errs,
				fmt.Errorf("%w: features.disabled names %q, which the installation holds",
					ErrConfig, name),
			)
		case f.Kind == features.Limit:
			errs = append(errs, fmt.Errorf("%w: features.disabled names %q, a limit, not a feature",
				ErrConfig, name))
		case slices.Contains(s.Disabled[:i], name):
			errs = append(errs, fmt.Errorf("%w: features.disabled names %q twice", ErrConfig, name))
		}
	}
	return errors.Join(errs...)
}

// Stream is the features domain's migration stream.
func Stream() migrate.Stream { return pg.Stream() }

// Deps are what the features domain takes from the platform.
type Deps struct {
	DB       pg.DB
	Settings Settings
}

// Domain is the features domain, constructed once per process.
type Domain struct {
	entitlements *entitlement.Service
}

// New constructs the domain on d.
func New(d Deps) *Domain {
	inTx := func(ctx context.Context, fn func(context.Context) error) error {
		//nolint:wrapcheck // fn's error, as it returned it
		return tx.Do(ctx, d.DB, tx.Options{}, fn)
	}
	return &Domain{entitlements: entitlement.New(pg.New(d.DB), inTx, d.Settings.Disabled)}
}

// FeaturesAPI is the domain's partial of the API's strict server, which
// internal/app embeds under this alias.
type FeaturesAPI = http.Server

// SeatCounter counts the seats in use in an organization and an account,
// which the access domain owns.
type SeatCounter = http.SeatCounter

// API is the domain's partial of the API's strict server, counting seats
// through seats; a nil domain's serves nothing it can answer.
func (d *Domain) API(seats SeatCounter) FeaturesAPI {
	if d == nil {
		return FeaturesAPI{}
	}
	return http.NewServer(d.entitlements, seats)
}

// FeaturesOperatorAPI is the domain's partial of the operator API's strict
// server, which internal/app embeds under this alias.
type FeaturesOperatorAPI = http.OperatorServer

// Organizations resolves an organization's slug, which the access domain
// owns; ok is false when no organization has it.
type Organizations = http.Organizations

// OperatorAPI is the domain's partial of the operator API's strict server,
// naming organizations through orgs; a nil domain's serves nothing it can
// answer.
func (d *Domain) OperatorAPI(orgs Organizations) FeaturesOperatorAPI {
	if d == nil {
		return FeaturesOperatorAPI{}
	}
	return http.NewOperatorServer(d.entitlements, orgs)
}

// Entitlements is what other domains call of this one.
func (d *Domain) Entitlements() featuresapi.Entitlements { return d.entitlements }

// Seed installs the seeded tiers the first time, and brings forward those
// the operator has not changed since; every mode that opens the database
// runs it after the migrations.
func (d *Domain) Seed(ctx context.Context) error {
	return d.entitlements.Seed(ctx) //nolint:wrapcheck // the store names the seed
}

// Middleware is the chi middleware that refuses a request for a feature
// the installation switched off or, with billing on, the caller's
// organization lacks; gate resolves each request's Gate.
func (d *Domain) Middleware(
	gate func(*nethttp.Request) (featuresapi.Gate, bool),
) func(nethttp.Handler) nethttp.Handler {
	return http.Middleware(d.entitlements, gate)
}

// Allowed reports whether org's work may use feature outside a request,
// as a queued job does at dequeue: false when the installation switched
// it off or, with billing on, org's entitlement lacks it.
func (d *Domain) Allowed(
	ctx context.Context,
	org kernel.OrganizationID,
	feature string,
) (bool, error) {
	err := d.entitlements.AllowOrganization(ctx, org, feature)
	if _, refused := errors.AsType[*entitlement.Refusal](err); refused {
		return false, nil
	}
	return err == nil, err //nolint:wrapcheck // the store names itself
}

// WatchEntitlements keeps the replica's entitlement snapshots in step with
// infraweave_invalidate until ctx ends: a payload of this domain drops what
// it concerns, and a resync drops them all.
func (d *Domain) WatchEntitlements(ctx context.Context, sub *notify.Subscription) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-sub.C:
			if m.Resync {
				d.entitlements.Flush()
			} else {
				d.entitlements.Invalidate(m.Payload)
			}
		}
	}
}
