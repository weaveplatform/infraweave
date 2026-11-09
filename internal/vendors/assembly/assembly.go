package assembly

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/cron"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vendorsapi "github.com/weaveplatform/infraweave/internal/vendors/api"
	"github.com/weaveplatform/infraweave/internal/vendors/internal/core"
	vhttp "github.com/weaveplatform/infraweave/internal/vendors/internal/http"
	"github.com/weaveplatform/infraweave/internal/vendors/internal/index"
	"github.com/weaveplatform/infraweave/internal/vendors/internal/pg"
)

// Stream is the vendors domain's migration stream.
func Stream() migrate.Stream { return pg.Stream() }

// Settings are the vendors settings: the release index each vendor's
// versions are read from, the vendors' public ones by default, so an
// installation without the internet can serve a mirror of its own.
type Settings struct {
	TerraformIndex string `mapstructure:"terraform_index"`
	OpenTofuIndex  string `mapstructure:"opentofu_index"`
}

// DefaultSettings read the vendors' public indexes.
func DefaultSettings() Settings {
	return Settings{
		TerraformIndex: index.DefaultTerraformURL,
		OpenTofuIndex:  index.DefaultOpenTofuURL,
	}
}

// ErrConfig reports a vendors setting the server could not start with.
var ErrConfig = errors.New("invalid vendors configuration")

// Validate rejects an index that is not an http or https URL, so a typo
// fails at startup rather than as a refresh that logs every hour.
func (s Settings) Validate() error {
	var errs []error
	for key, raw := range map[string]string{
		"vendors.terraform_index": s.TerraformIndex,
		"vendors.opentofu_index":  s.OpenTofuIndex,
	} {
		if u, err := url.Parse(raw); err != nil || (u.Scheme != "https" && u.Scheme != "http") ||
			u.Host == "" {
			errs = append(
				errs,
				fmt.Errorf("%w: %s %q is not an http or https URL", ErrConfig, key, raw),
			)
		}
	}
	return errors.Join(errs...)
}

// Deps are what the vendors domain takes from the platform: the database
// and its settings.
type Deps struct {
	DB       tx.Querier
	Settings Settings
}

// Domain is the vendors domain, the tool version catalogue, constructed
// once per process after access and ahead of stacks.
type Domain struct {
	catalog *core.Service
	api     vhttp.Server
}

// indexTimeout bounds one request to an index, so a stalled vendor ends
// the refresh's read of it well inside the job's lease.
const indexTimeout = time.Minute

// New constructs the domain on d.
func New(d Deps) *Domain {
	client := &http.Client{Timeout: indexTimeout}
	svc := core.New(pg.New(d.DB), map[vendorsapi.Vendor]index.Reader{
		vendorsapi.Terraform: index.HashiCorp{URL: d.Settings.TerraformIndex, Client: client},
		vendorsapi.OpenTofu:  index.OpenTofu{URL: d.Settings.OpenTofuIndex, Client: client},
	})
	return &Domain{catalog: svc, api: vhttp.New(svc)}
}

// Refresh is the periodic task that reads the vendors' release indexes
// into the catalogue. It runs on the maintenance queue with policy
// Ignore, so a slow index never overlaps the next run.
//
// ref: decision 0011
var Refresh = queue.Kind{Name: "vendors.refresh", Queue: queue.Maintenance, Policy: queue.Ignore}

// RefreshEvery is how often the catalogue is refreshed: the vendors
// publish a few releases a month, so an hour shows a release the day it
// ships while reading HashiCorp's paged index two dozen times an hour at
// most across every replica.
const RefreshEvery = time.Hour

// Cron is the domain's periodic tasks: Refresh every RefreshEvery,
// logging to log an index it cannot read.
func (d *Domain) Cron(log *slog.Logger) []cron.Task {
	return []cron.Task{{
		Kind:  Refresh,
		Every: RefreshEvery,
		Handler: func(ctx context.Context, _ queue.Job) error {
			return d.catalog.Refresh(ctx, log) //nolint:wrapcheck // the store names the write
		},
	}}
}

// Catalog is what other domains call of this one.
func (d *Domain) Catalog() vendorsapi.Catalog { return d.catalog }

// VendorsAPI is the domain's partial of the API's strict server, which
// internal/app embeds under this alias.
type VendorsAPI = vhttp.Server

// API is the domain's partial of the API's strict server; a nil domain's
// is the zero partial, for tests that construct none.
func (d *Domain) API() VendorsAPI {
	if d == nil {
		return VendorsAPI{}
	}
	return d.api
}
