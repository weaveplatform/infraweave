package assembly

import (
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/state/internal/core"
	"github.com/weaveplatform/infraweave/internal/state/internal/pg"
)

// Stream is the domain's migration stream, which internal/app registers
// after runs'.
func Stream() migrate.Stream { return pg.Stream() }

// Held are the state columns that hold other domains' rows, for the
// nightly orphan count.
func Held() []integrity.Held { return pg.Held() }

// Deps are what the state domain takes from the platform and the stacks
// domain: DB, the database it stores workspaces, locks and versions in,
// and Stacks, which holds a stack while its workspace is written.
type Deps struct {
	DB     tx.Querier
	Stacks stacksapi.Stacks
}

// Domain is the state domain.
type Domain struct{ state *core.Service }

// New constructs the domain over d.
func New(d Deps) *Domain {
	return &Domain{state: core.New(pg.New(d.DB), d.Stacks)}
}
