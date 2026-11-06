package assembly

import (
	"context"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/platform/cron"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
)

// AccessAPI is the access domain's partial of the API's strict server,
// named after the domain because an embedded field takes its type's name.
type AccessAPI = http.Server

// RotateKeys is authn.keys.rotate, decision 0006's daily signing key
// rotation. It runs on the maintenance queue with policy Ignore, so a slow
// run never overlaps the next.
var RotateKeys = queue.Kind{
	Name:   "authn.keys.rotate",
	Queue:  queue.Maintenance,
	Policy: queue.Ignore,
}

// Stream is the access domain's migration stream.
func Stream() migrate.Stream { return pg.Stream() }

// SealedColumns are the access domain's sealed columns.
func SealedColumns() []crypt.Column { return pg.SealedColumns() }

// Deps are what the access domain takes from the platform.
type Deps struct {
	DB pg.DB
	// Keys seals the signing keys' private halves; crypt.Keyring is one.
	Keys     pg.Sealer
	Settings Settings
	// PublicURL is server.public_url, the issuer when auth.api_issuer is
	// empty.
	PublicURL string
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Domain is the access domain, constructed once per process.
type Domain struct {
	tenancy *pg.Store
	tokens  *token.Service
	manual  bool
}

// New constructs the domain on d.
func New(d Deps) *Domain {
	issuer := d.Settings.APIIssuer
	if issuer == "" {
		issuer = d.PublicURL
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	manual := d.Settings.Signing.Rotation == RotationManual
	return &Domain{
		tenancy: pg.New(d.DB),
		tokens: token.New(pg.NewSigningKeys(d.DB, d.Keys), issuer, token.Policy{
			Manual:    manual,
			TokenLife: d.Settings.Tokens.API.TTL,
		}, now),
		manual: manual,
	}
}

// API is the domain's partial of the API's strict server.
func (*Domain) API() AccessAPI { return AccessAPI{} }

// Cron is the domain's periodic tasks: authn.keys.rotate, daily, unless
// auth.signing.rotation is manual.
func (d *Domain) Cron() []cron.Task {
	if d.manual {
		return nil
	}
	return []cron.Task{{
		Kind:  RotateKeys,
		Every: token.RotateEvery,
		Handler: func(ctx context.Context, _ queue.Job) error {
			return d.tokens.Rotate(ctx) //nolint:wrapcheck // names itself
		},
	}}
}
