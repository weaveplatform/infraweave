package assembly

import (
	"context"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/features/internal/features"
	"github.com/weaveplatform/infraweave/internal/features/internal/pg"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// Settings are the features domain's settings, under features (decision
// 0021).
type Settings struct {
	// Disabled are the features switched off for every organization of
	// the installation.
	Disabled []string `mapstructure:"disabled"`
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
	entitlements *features.Service
}

// New constructs the domain on d.
func New(d Deps) *Domain {
	inTx := func(ctx context.Context, fn func(context.Context) error) error {
		//nolint:wrapcheck // fn's error, as it returned it
		return tx.Do(ctx, d.DB, tx.Options{}, fn)
	}
	return &Domain{entitlements: features.New(pg.New(d.DB), inTx, d.Settings.Disabled)}
}

// Entitlements is what other domains call of this one.
func (d *Domain) Entitlements() featuresapi.Entitlements { return d.entitlements }
