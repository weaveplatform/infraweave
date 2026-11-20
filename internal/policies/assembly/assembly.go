package assembly

import (
	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/policies/internal/http"
)

// ContractPattern is the policy schema route on the API listener.
const ContractPattern = http.ContractPattern

// Domain is the policies domain's contract publisher. Later policy operations
// share this domain without introducing a separate listener or runtime here.
type Domain struct{ server http.Server }

// New constructs the publisher without a policy evaluator or store.
func New() *Domain { return &Domain{server: http.New()} }

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
