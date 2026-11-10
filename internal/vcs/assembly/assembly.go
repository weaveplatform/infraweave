package assembly

import (
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/rawgit"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/source"
)

// Deps are what the vcs domain needs of the domains built before it.
type Deps struct {
	Stacks stacksapi.Stacks
}

// Domain is the vcs domain, constructed once per process.
type Domain struct {
	sources *source.Service
}

// New constructs the domain over d, reading raw Git repositories, served as
// the GIT provider, over https with the system's roots.
func New(d Deps) *Domain {
	return &Domain{sources: source.New(d.Stacks, map[string]port.Source{
		"GIT": rawgit.New(rawgit.Options{}),
	})}
}

// Sources resolves the commits of a stack's repository for other domains.
func (d *Domain) Sources() vcsapi.Sources { return d.sources }

// VCSAPI is the domain's partial of the API's strict server, which
// internal/app embeds under this alias.
type VCSAPI = http.Server

// API is the domain's partial of the API's strict server; a nil domain's
// is the zero partial, for tests that construct none.
func (d *Domain) API() VCSAPI { return VCSAPI{} }
