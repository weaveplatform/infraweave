package assembly

import (
	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
)

// AccessAPI is the access domain's partial of the API's strict server,
// named after the domain because an embedded field takes its type's name.
type AccessAPI = http.Server

// Stream is the access domain's migration stream.
func Stream() migrate.Stream { return pg.Stream() }

// SealedColumns are the access domain's sealed columns.
func SealedColumns() []crypt.Column { return pg.SealedColumns() }

// Deps are what the access domain takes from the platform.
type Deps struct {
	DB pg.DB
}

// Domain is the access domain, constructed once per process.
type Domain struct {
	tenancy *pg.Store
}

// New constructs the domain on d.
func New(d Deps) *Domain { return &Domain{tenancy: pg.New(d.DB)} }

// API is the domain's partial of the API's strict server.
func (*Domain) API() AccessAPI { return AccessAPI{} }
