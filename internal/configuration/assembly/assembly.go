package assembly

import (
	"github.com/weaveplatform/infraweave/internal/configuration/internal/http"
	"github.com/weaveplatform/infraweave/internal/configuration/internal/pg"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
)

// Stream is the configuration domain's migration stream.
func Stream() migrate.Stream { return pg.Stream() }

// Domain is the configuration domain, constructed once per process.
type Domain struct{}

// New constructs the domain.
func New() *Domain { return &Domain{} }

// ConfigurationAPI is the domain's partial of the API's strict server,
// which internal/app embeds under this alias.
type ConfigurationAPI = http.Server

// API is the domain's partial of the API's strict server.
func (*Domain) API() ConfigurationAPI { return ConfigurationAPI{} }
