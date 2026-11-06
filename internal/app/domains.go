package app

import (
	"strings"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	accessassembly "github.com/weaveplatform/infraweave/internal/access/assembly"
	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/cron"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
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
	// OperatorAuth authenticates the operator API's requests, admitting
	// operator principals alone; nil refuses every credential.
	OperatorAuth accessapi.Authenticator
}

// newDomains constructs every domain on db, sealing under keys and signing
// tokens through kms under the kms backend, with c's settings. It fails
// only on a setting a domain reads a file for.
func newDomains(
	c config.Config,
	db *sql.DB,
	keys *crypt.Keyring,
	kms []signer.KMS,
) (Domains, error) {
	access, err := accessassembly.New(accessassembly.Deps{
		DB:               db.Primary(),
		Keys:             keys,
		KMS:              kms,
		Settings:         c.Auth,
		PublicURL:        c.Server.PublicURL,
		Audience:         strings.TrimSuffix(c.Server.PublicURL, "/") + BaseURL,
		OperatorAudience: strings.TrimSuffix(c.Server.PublicURL, "/") + OperatorBaseURL,
	})
	if err != nil {
		return Domains{}, err //nolint:wrapcheck // names the setting
	}
	return Domains{
		Access:       access,
		Auth:         access.Authenticator(),
		OperatorAuth: access.OperatorAuthenticator(),
	}, nil
}

// cron is every domain's periodic tasks, which the scheduler role runs.
func (d Domains) cron() []cron.Task { return d.Access.Cron() }
