package assembly

import "github.com/weaveplatform/infraweave/internal/expressions/internal/http"

// Domain is the expressions domain, constructed once per process.
type Domain struct{}

// New constructs the domain.
func New() *Domain { return &Domain{} }

// ExpressionsAPI is the domain's partial of the API's strict server, which
// internal/app embeds under this alias.
type ExpressionsAPI = http.Server

// API is the domain's partial of the API's strict server.
func (*Domain) API() ExpressionsAPI { return ExpressionsAPI{} }
