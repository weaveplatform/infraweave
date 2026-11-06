package assembly

import (
	"context"
	"fmt"
	"os"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/oidc"
	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/platform/cron"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
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

// ExpireSessions is authn.sessions.expire, decision 0006's hourly deletion
// of the sessions that ended authn.Retention ago.
var ExpireSessions = queue.Kind{
	Name:   "authn.sessions.expire",
	Queue:  queue.Maintenance,
	Policy: queue.Ignore,
}

// expireEvery is how often authn.sessions.expire runs.
const expireEvery = time.Hour

// Stream is the access domain's migration stream.
func Stream() migrate.Stream { return pg.Stream() }

// SealedColumns are the access domain's sealed columns.
func SealedColumns() []crypt.Column { return pg.SealedColumns() }

// Deps are what the access domain takes from the platform.
type Deps struct {
	DB pg.DB
	// Keys seals the signing keys' private halves; crypt.Keyring is one.
	Keys pg.Sealer
	// KMS are the opened keys of auth.signing.kms_keys, in its order,
	// under the kms backend.
	KMS      []signer.KMS
	Settings Settings
	// PublicURL is server.public_url, the issuer when auth.api_issuer is
	// empty.
	PublicURL string
	// Audience is the API's base URL, server.public_url and the API's
	// prefix: every API token's aud.
	Audience string
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Domain is the access domain, constructed once per process.
type Domain struct {
	store  *pg.Store
	tokens *token.Service
	authn  *authn.Service
	keys   *apikey.Service
	manual bool
}

// New constructs the domain on d. It fails only when
// auth.oidc_api_keys.ca_file cannot be read or holds no certificate.
func New(d Deps) (*Domain, error) {
	issuer := d.Settings.APIIssuer
	if issuer == "" {
		issuer = d.PublicURL
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	manual := d.Settings.Signing.Rotation == RotationManual
	store := pg.New(d.DB)
	refs := make([]string, 0, len(d.KMS))
	for _, k := range d.KMS {
		refs = append(refs, k.Ref())
	}
	tokens := token.New(pg.NewSigningKeys(d.DB, d.Keys, d.KMS), issuer, token.Policy{
		Manual:    manual,
		TokenLife: d.Settings.Tokens.API.TTL,
		Refs:      refs,
	}, now)
	var roots []byte
	if f := d.Settings.OIDCAPIKeys.CAFile; f != "" {
		var err error
		if roots, err = os.ReadFile(f); err != nil {
			return nil, fmt.Errorf("%w: auth.oidc_api_keys.ca_file: %w", ErrConfig, err)
		}
	}
	verifier, err := oidc.New(oidc.Options{RootCAs: roots, Now: now})
	if err != nil {
		return nil, fmt.Errorf("%w: auth.oidc_api_keys.ca_file %s: %w",
			ErrConfig, d.Settings.OIDCAPIKeys.CAFile, err)
	}
	auth := authn.New(authn.Deps{
		Keys:     store,
		Sessions: store,
		Tokens:   tokens,
		OIDC:     verifier,
		IssuerAllowed: func(issuer string) bool {
			return apikey.IssuerAllowed(d.Settings.OIDCAPIKeys.AllowedIssuers, issuer)
		},
		Audience: d.Audience,
		TTL:      d.Settings.Tokens.API.TTL,
		Now:      now,
	})
	keys := apikey.NewService(apikey.Deps{
		Store:          store,
		Drop:           auth.Cache().Drop,
		AllowedIssuers: d.Settings.OIDCAPIKeys.AllowedIssuers,
		Now:            now,
	})
	return &Domain{
		store:  store,
		tokens: tokens,
		authn:  auth,
		keys:   keys,
		manual: manual,
	}, nil
}

// API is the domain's partial of the API's strict server; a nil domain's
// serves nothing it can answer, for tests that construct none.
func (d *Domain) API() AccessAPI {
	if d == nil {
		return AccessAPI{}
	}
	return http.NewServer(d.authn, d.keys)
}

// Authenticator is the middleware that authenticates the API's requests.
func (d *Domain) Authenticator() accessapi.Authenticator { return http.NewAuthenticator(d.authn) }

// WatchSessions keeps the replica's session cache in step with
// revocations until ctx ends: a session:<id> message on sub, a
// subscription to infraweave_invalidate, drops that session, and a Resync,
// after which messages may have been lost, drops them all.
func (d *Domain) WatchSessions(ctx context.Context, sub *notify.Subscription) {
	cache := d.authn.Cache()
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-sub.C:
			if m.Resync {
				cache.Flush()
			} else if id, ok := session.ParseKey(m.Payload); ok {
				cache.Drop(id)
			}
		}
	}
}

// Cron is the domain's periodic tasks: authn.sessions.expire, hourly, and
// authn.keys.rotate, daily, unless auth.signing.rotation is manual.
func (d *Domain) Cron() []cron.Task {
	tasks := make([]cron.Task, 0, 2)
	tasks = append(tasks, cron.Task{
		Kind:  ExpireSessions,
		Every: expireEvery,
		Handler: func(ctx context.Context, _ queue.Job) error {
			return d.authn.Expire(ctx) //nolint:wrapcheck // names itself
		},
	})
	if d.manual {
		return tasks
	}
	return append(tasks, cron.Task{
		Kind:  RotateKeys,
		Every: token.RotateEvery,
		Handler: func(ctx context.Context, _ queue.Job) error {
			return d.tokens.Rotate(ctx) //nolint:wrapcheck // names itself
		},
	})
}
