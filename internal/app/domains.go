package app

import (
	accessassembly "github.com/weaveplatform/infraweave/internal/access/assembly"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
)

// Domains are the business domains, constructed in the declared graph's
// order (repo-layout.md, "Declared graph"), so each is built after every
// domain whose api/ it imports. A back edge, a call to a domain built
// later, is bound here after construction.
type Domains struct {
	Access *accessassembly.Domain
}

// newDomains constructs every domain on db.
func newDomains(db *sql.DB) Domains {
	return Domains{
		Access: accessassembly.New(accessassembly.Deps{DB: db.Primary()}),
	}
}
