package assembly

import (
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	"github.com/weaveplatform/infraweave/internal/configuration/internal/core"
	"github.com/weaveplatform/infraweave/internal/configuration/internal/http"
	"github.com/weaveplatform/infraweave/internal/configuration/internal/pg"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Stream is the configuration domain's migration stream.
func Stream() migrate.Stream { return pg.Stream() }

// Held are the configuration columns that hold other domains' rows, for
// the nightly orphan count.
func Held() []integrity.Held { return pg.Held() }

// SealedColumns are the configuration domain's sealed columns.
func SealedColumns() []crypt.Column { return pg.SealedColumns() }

// The kinds of entity the access domain's middleware finds a space for,
// as the API's x-infraweave-subject names them: a context, in its own
// space, and an attachment, in its stack's.
const (
	Entity           = "context"
	AttachmentEntity = "context_attachment"
)

// Deps are what the configuration domain takes from the platform and the
// domains below it: the database, the keys every element's value is
// sealed under, the spaces a context lives in and holds, the stacks it
// attaches to and holds, the authorizer that decides the checks beyond
// each route's, and the outbox its writes append configapi.Changed to.
type Deps struct {
	DB     pg.DB
	Keys   pg.Sealer
	Spaces accessapi.Spaces
	Stacks stacksapi.Stacks
	Authz  accessapi.Authorizer
	Events *outbox.Outbox
	Now    func() time.Time
}

// Domain is the configuration domain, constructed once per process.
type Domain struct {
	svc *core.Service
	api http.Server
}

// New constructs the domain on d.
func New(d Deps) *Domain {
	svc := core.New(core.Deps{
		Store: pg.New(d.DB, d.Keys), DB: d.DB, Spaces: d.Spaces, Stacks: d.Stacks, Authz: d.Authz,
		Events: d.Events, Now: d.Now,
	})
	return &Domain{svc: svc, api: http.New(svc, d.Authz, d.Spaces)}
}

// RegisterEvents adds the domain's event kinds to kinds, before the
// scheduler starts.
func RegisterEvents(kinds *outbox.Kinds) error {
	//nolint:wrapcheck // names the kind
	return kinds.Register(configapi.ChangedKind, configapi.ChangedVersion, configapi.ChangedSchema)
}

// SpaceGuard is the domain's say in the access domain's space deletes and
// moves, which internal/app registers there.
func (d *Domain) SpaceGuard() accessapi.SpaceGuard { return d.svc.SpaceGuard() }

// StackGuard is the domain's say in the stacks domain's stack deletes and
// moves, which internal/app registers there.
func (d *Domain) StackGuard() stacksapi.StackGuard { return d.svc.StackGuard() }

// Configuration is the stack view's reader of contexts and config
// elements, the back edge internal/app binds in the stacks domain.
func (d *Domain) Configuration() stacksapi.StackConfiguration { return d.svc }

// SpaceOf finds a context's space by its slug, which internal/app
// registers with the access domain for Entity.
func (d *Domain) SpaceOf() accessapi.SpaceOf { return d.svc.SpaceOf }

// AttachmentSpaceOf finds the space of an attachment's stack by the
// attachment's ID, which internal/app registers for AttachmentEntity.
func (d *Domain) AttachmentSpaceOf() accessapi.SpaceOf { return d.svc.AttachmentSpaceOf }

// ConfigurationAPI is the domain's partial of the API's strict server,
// which internal/app embeds under this alias.
type ConfigurationAPI = http.Server

// API is the domain's partial of the API's strict server; a nil domain's
// is the zero partial, for tests that construct none.
func (d *Domain) API() ConfigurationAPI {
	if d == nil {
		return ConfigurationAPI{}
	}
	return d.api
}
