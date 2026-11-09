package assembly

import (
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/http"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/pg"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/stacks"
)

// Stream is the stacks domain's migration stream.
func Stream() migrate.Stream { return pg.Stream() }

// Entity is the kind of entity the access domain's middleware finds a
// stack's space for, as the API's x-infraweave-subject names it.
const Entity = "stack"

// Deps are what the stacks domain takes from the platform and the access
// domain: the database, the spaces a stack lives in and holds, the
// authorizer that decides the checks beyond each route's and a stack's
// allowedActions, and the outbox its writes append StackChanged to.
type Deps struct {
	DB     pg.DB
	Spaces accessapi.Spaces
	Authz  accessapi.Authorizer
	Events *outbox.Outbox
	Now    func() time.Time
}

// Domain is the stacks domain, constructed once per process.
type Domain struct {
	stacks *stacks.Service
	api    http.Server
}

// New constructs the domain on d.
func New(d Deps) *Domain {
	svc := stacks.New(stacks.Deps{
		Store: pg.New(d.DB), DB: d.DB, Spaces: d.Spaces, Authz: d.Authz, Events: d.Events,
		Now: d.Now,
	})
	return &Domain{stacks: svc, api: http.New(svc, d.Authz, d.Spaces)}
}

// RegisterEvents adds the domain's event kinds to kinds, before the
// scheduler starts.
func RegisterEvents(kinds *outbox.Kinds) error {
	//nolint:wrapcheck // names the kind
	return kinds.Register(stacksapi.ChangedKind, stacksapi.ChangedVersion, stacksapi.ChangedSchema)
}

// Stacks is what other domains call of this one.
func (d *Domain) Stacks() stacksapi.Stacks { return d.stacks }

// SpaceGuard is the domain's say in the access domain's space deletes and
// moves, which internal/app registers there.
func (d *Domain) SpaceGuard() accessapi.SpaceGuard { return d.stacks }

// SpaceOf finds a stack's space by its slug, which internal/app registers
// with the access domain for Entity.
func (d *Domain) SpaceOf() accessapi.SpaceOf { return d.stacks.SpaceOf }

// StacksAPI is the domain's partial of the API's strict server, which
// internal/app embeds under this alias.
type StacksAPI = http.Server

// API is the domain's partial of the API's strict server; a nil domain's
// is the zero partial, for tests that construct none.
func (d *Domain) API() StacksAPI {
	if d == nil {
		return StacksAPI{}
	}
	return d.api
}
