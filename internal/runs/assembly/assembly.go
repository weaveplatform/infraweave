package assembly

import (
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Stream is the runs domain's migration stream.
func Stream() migrate.Stream { return pg.Stream() }

// Held are the runs columns that hold other domains' rows, for the
// nightly orphan count.
func Held() []integrity.Held { return pg.Held() }

// Deps are what the runs domain takes from the platform: the database.
type Deps struct {
	DB tx.Querier
}

// Domain is the runs domain, constructed once per process.
type Domain struct {
	runs *core.Service
}

// New constructs the domain on d.
func New(d Deps) *Domain { return &Domain{runs: core.New(pg.New(d.DB))} }

// StackStatus is the domain's answer to the stack view's read of the run
// blocking each stack, a back edge internal/app binds in the stacks
// domain.
func (d *Domain) StackStatus() stacksapi.StackStatus { return d.runs }

// StackGuard is the domain's say in the stacks domain's moves and
// deletes, which internal/app registers there, so a deleted stack's runs
// go in its delete's transaction.
func (d *Domain) StackGuard() stacksapi.StackGuard { return d.runs }

// RunsAPI is the domain's partial of the API's strict server, which
// internal/app embeds under this alias.
type RunsAPI = http.Server

// API is the domain's partial of the API's strict server; a nil domain's
// is the zero partial, for tests that construct none.
func (d *Domain) API() RunsAPI { return RunsAPI{} }
