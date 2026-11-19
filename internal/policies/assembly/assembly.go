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
