package assembly

import (
	"context"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/http"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/pg"
)

// Stream is the stacks domain's migration stream.
func Stream() migrate.Stream { return pg.Stream() }

// Held are the stacks columns that hold other domains' rows, for the
// nightly orphan count.
func Held() []integrity.Held { return pg.Held() }

// Keys are the stacks rows other domains hold, for the nightly orphan
// count.
func Keys() []integrity.Key { return pg.Keys() }

// Entity is the kind of entity the access domain's middleware finds a
// stack's space for, as the API's x-infraweave-subject names it, and
// BindingEntity the kind it finds a stack binding's stack's space for.
const (
	Entity        = "stack"
	BindingEntity = "stack_role_binding"
)

// Deps are what the stacks domain takes from the platform and the access
// domain: the database, the spaces a stack lives in and holds, the
// authorizer that decides the checks beyond each route's and a stack's
// allowedActions, the stacks' role bindings and the grants that reach a
// space for them, and the outbox its writes append StackChanged to.
type Deps struct {
	DB       pg.DB
	Spaces   accessapi.Spaces
	Authz    accessapi.Authorizer
	Bindings accessapi.Bindings
	Grants   accessapi.Grants
	Events   *outbox.Outbox
	Now      func() time.Time
}

// Domain is the stacks domain, constructed once per process.
type Domain struct {
	stacks *core.Service
	store  *pg.Store
	api    http.Server
}

// New constructs the domain on d.
func New(d Deps) *Domain {
	store := pg.New(d.DB)
	svc := core.New(core.Deps{
		Store: store, DB: d.DB, Spaces: d.Spaces, Authz: d.Authz, Bindings: d.Bindings,
		Grants: d.Grants, Events: d.Events, Now: d.Now,
	})
	return &Domain{stacks: svc, store: store, api: http.New(svc, d.Authz, d.Spaces)}
}

// PurgeBatch is how many stacks one statement of Purge deletes, so no
// transaction holds an account's every stack and its rows at once.
const PurgeBatch = 500

// Purge deletes the deleted account acct's stacks, with their hooks,
// labels and stars, PurgeBatch at a time. internal/app runs it for each
// accessapi.Deleted; a purge stopped part way keeps what it deleted, and
// running it again finishes.
func (d *Domain) Purge(ctx context.Context, acct kernel.AccountID) error {
	for {
		done, err := d.store.Purge(ctx, acct, PurgeBatch)
		if err != nil || done {
			return err //nolint:wrapcheck // the store names the purge
		}
	}
}

// RegisterEvents adds the domain's event kinds to kinds, before the
// scheduler starts.
func RegisterEvents(kinds *outbox.Kinds) error {
	//nolint:wrapcheck // names the kind
	return kinds.Register(stacksapi.ChangedKind, stacksapi.ChangedVersion, stacksapi.ChangedSchema)
}

// Stacks is what other domains call of this one.
func (d *Domain) Stacks() stacksapi.Stacks { return d.stacks }

// GuardStacks registers another domain's stacksapi.StackGuard, which
// every later stack move and delete asks.
func (d *Domain) GuardStacks(g stacksapi.StackGuard) { d.stacks.Guard(g) }

// BindConfiguration binds the configuration domain's reader of a stack's
// contexts and config elements, a back edge internal/app binds after it
// constructs that domain; until then a stack has neither.
func (d *Domain) BindConfiguration(c stacksapi.StackConfiguration) {
	d.api.BindConfiguration(c)
}

// SetStackStatus binds the runs domain's reader of the run blocking each
// stack, a back edge internal/app binds after it constructs that domain;
// until then no run blocks a stack.
func (d *Domain) SetStackStatus(s stacksapi.StackStatus) { d.api.SetStackStatus(s) }

// SpaceGuard is the domain's say in the access domain's space deletes and
// moves, which internal/app registers there.
func (d *Domain) SpaceGuard() accessapi.SpaceGuard { return d.stacks }

// SpaceOf finds a stack's space by its slug, which internal/app registers
// with the access domain for Entity.
func (d *Domain) SpaceOf() accessapi.SpaceOf { return d.stacks.SpaceOf }

// BindingSpaceOf finds the space of a stack binding's stack by the
// binding's ID, which internal/app registers with the access domain for
// BindingEntity.
func (d *Domain) BindingSpaceOf() accessapi.SpaceOf { return d.stacks.BindingSpaceOf }

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
