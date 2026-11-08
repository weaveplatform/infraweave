package assembly

import "github.com/weaveplatform/infraweave/internal/vendors/internal/http"

// Domain is the vendors domain, constructed once per process.
type Domain struct{}

// New constructs the domain.
func New() *Domain { return &Domain{} }

// VendorsAPI is the domain's partial of the API's strict server, which
// internal/app embeds under this alias.
type VendorsAPI = http.Server

// API is the domain's partial of the API's strict server.
func (*Domain) API() VendorsAPI { return VendorsAPI{} }
