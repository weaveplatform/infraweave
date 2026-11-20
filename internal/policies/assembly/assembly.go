package assembly

import (
	"time"

	"github.com/go-chi/chi/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
	"github.com/weaveplatform/infraweave/internal/policies/internal/http"
	"github.com/weaveplatform/infraweave/internal/policies/internal/pg"
)

// ContractPattern is the policy schema route on the API listener.
const ContractPattern = http.ContractPattern

// Domain wires policy persistence, management and contract publication on the
// shared API listener.
type Domain struct {
	server http.Server
	svc    *core.Service
}

// Deps supplies persistence, space ownership, authorization and the bounded runtime.
type Deps struct {
	DB      pg.DB
	Spaces  accessapi.Spaces
	Authz   accessapi.Authorizer
	Runtime *rego.Engine
	Now     func() time.Time
}

// Entity identifies policy IDs to the shared subject resolver.
const Entity = "policy"

// Stream is the policies domain's migration stream, before runs.
func Stream() migrate.Stream { return pg.Stream() }

// New constructs policy management over its owned store and domain ports.
func New(d Deps) *Domain {
	store := pg.New(d.DB)
	svc := core.New(core.Deps{Store: store, DB: d.DB, Spaces: d.Spaces, Authz: d.Authz, Runtime: d.Runtime, Now: d.Now})
	return &Domain{server: http.New(svc, d.Authz, d.Spaces), svc: svc}
}

// SpaceOf resolves a policy subject using both its account and immutable ID.
func (d *Domain) SpaceOf() accessapi.SpaceOf { return d.svc.SpaceOf }

// Routes registers the domain's routes. A nil domain serves none, for tests
// which construct only the domains their request reaches.
func (d *Domain) Routes(r chi.Router) {
	if d != nil {
		d.server.Routes(r)
	}
}

// PoliciesAPI is the domain's partial of the shared strict server.
type PoliciesAPI = http.Server

// API is the domain's shared API partial. The zero partial still reads linked
// build metadata for tests constructing only the API surface under inspection.
func (d *Domain) API() PoliciesAPI {
	if d == nil {
		return PoliciesAPI{}
	}
	return d.server
}
