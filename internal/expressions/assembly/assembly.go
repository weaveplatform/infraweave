package assembly

import (
	"time"

	"github.com/weaveplatform/infraweave/internal/expressions/internal/http"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
)

// Domain is the expressions domain, constructed once per process.
type Domain struct {
	registry *expr.Registry
}

// New constructs the domain over registry, which holds every domain's
// surfaces.
func New(registry *expr.Registry) *Domain { return &Domain{registry: registry} }

// ExpressionsAPI is the domain's partial of the API's strict server, which
// internal/app embeds under this alias.
type ExpressionsAPI = http.Server

// API is the domain's partial of the API's strict server, evaluating at
// the wall clock's time; a nil domain's is the zero partial, for tests
// that construct none.
func (d *Domain) API() ExpressionsAPI {
	if d == nil {
		return ExpressionsAPI{}
	}
	return http.New(d.registry, time.Now)
}
