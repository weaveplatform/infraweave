package app

import (
	"strings"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	accessassembly "github.com/weaveplatform/infraweave/internal/access/assembly"
	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/cron"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
)

// Domains are the business domains, constructed in the declared graph's
// order (repo-layout.md, "Declared graph"), so each is built after every
// domain whose api/ it imports. A back edge, a call to a domain built
// later, is bound here after construction.
type Domains struct {
	Access *accessassembly.Domain
	// Auth authenticates the API's requests; nil refuses every credential.
	Auth accessapi.Authenticator
}

// newDomains constructs every domain on db, sealing under keys, with c's
// settings.
func newDomains(c config.Config, db *sql.DB, keys *crypt.Keyring) Domains {
	access := accessassembly.New(accessassembly.Deps{
		DB:        db.Primary(),
		Keys:      keys,
		Settings:  c.Auth,
		PublicURL: c.Server.PublicURL,
		Audience:  strings.TrimSuffix(c.Server.PublicURL, "/") + BaseURL,
	})
	return Domains{Access: access, Auth: access.Authenticator()}
}

// cron is every domain's periodic tasks, which the scheduler role runs.
func (d Domains) cron() []cron.Task { return d.Access.Cron() }
