package assembly

import (
	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/http"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/pg"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/stacks"
)

// Stream is the stacks domain's migration stream.
func Stream() migrate.Stream { return pg.Stream() }

// Deps are what the stacks domain takes from the platform.
type Deps struct {
	DB tx.Querier
}

// Domain is the stacks domain, constructed once per process.
type Domain struct {
	stacks *stacks.Service
}

// New constructs the domain on d.
func New(d Deps) *Domain {
	return &Domain{stacks: stacks.New(pg.New(d.DB))}
}

// Stacks is what other domains call of this one.
func (d *Domain) Stacks() stacksapi.Stacks { return d.stacks }

// SpaceGuard is the domain's say in the access domain's space deletes and
// moves, which internal/app registers there.
func (d *Domain) SpaceGuard() accessapi.SpaceGuard { return d.stacks }

// StacksAPI is the domain's partial of the API's strict server, which
// internal/app embeds under this alias.
type StacksAPI = http.Server

// API is the domain's partial of the API's strict server.
func (*Domain) API() StacksAPI { return StacksAPI{} }
