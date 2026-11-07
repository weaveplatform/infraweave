package assembly

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/oidc"
	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/group"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/oauth"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/operator"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/settings"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/user"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/cron"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
)

// AccessAPI is the access domain's partial of the API's strict server,
// named after the domain because an embedded field takes its type's name.
type AccessAPI = http.Server

// AccessOperatorAPI is the access domain's partial of the operator API's
// strict server, named as AccessAPI is.
type AccessOperatorAPI = http.OperatorServer

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
	// Features is the features domain's entitlements, whose rows of an
	// organization go with the organization.
	Features featuresapi.Entitlements
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
	// OperatorAudience is the operator API's base URL: every operator
	// token's aud.
	OperatorAudience string
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Domain is the access domain, constructed once per process.
type Domain struct {
	store    *pg.Store
	tokens   *token.Service
	authn    *authn.Service
	keys     *apikey.Service
	orgs     *operator.Service
	orgAdmin *orgadmin.Service
	settings *settings.Service
	users    *user.Service
	groups   *group.Service
	oauth    *oauth.Service
	manual   bool
	// publicURL is server.public_url, whose origin a browser session's
	// write must carry.
	publicURL string
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
	store := pg.New(d.DB).WithFeatures(d.Features)
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
		Keys:             store,
		OrganizationKeys: store,
		Sessions:         store,
		Tokens:           tokens,
		OIDC:             verifier,
		IssuerAllowed: func(issuer string) bool {
			return apikey.IssuerAllowed(d.Settings.OIDCAPIKeys.AllowedIssuers, issuer)
		},
		Audience:         d.Audience,
		Operators:        d.Settings.Operator.operators(),
		OperatorAudience: d.OperatorAudience,
		TTL:              d.Settings.Tokens.API.TTL,
		BreakGlass:       store,
		Admission:        store,
		Idle:             d.Settings.Session.IdleTimeout,
		MaxAge:           d.Settings.Session.MaxAge,
		Now:              now,
	})
	keys := apikey.NewService(apikey.Deps{
		Store:          store,
		Drop:           auth.Cache().Drop,
		AllowedIssuers: d.Settings.OIDCAPIKeys.AllowedIssuers,
		Now:            now,
	})
	orgs := operator.NewService(operator.Deps{Store: store, Drop: auth.Cache().Drop, Now: now})
	grants := oauth.NewService(oauth.Deps{
		Store:      store,
		Tokens:     tokens,
		Admission:  store,
		Drop:       auth.Cache().Drop,
		PublicURL:  d.PublicURL,
		AccessTTL:  d.Settings.Tokens.OAuthAccess.TTL,
		RefreshTTL: d.Settings.Tokens.OAuthRefresh.TTL,
		CodeTTL:    d.Settings.Tokens.OAuthCode.TTL,
		Now:        now,
	})
	return &Domain{
		store:  store,
		tokens: tokens,
		authn:  auth,
		keys:   keys,
		orgs:   orgs,
		orgAdmin: orgadmin.NewService(orgadmin.Deps{
			Store: store, Features: d.Features, Drop: auth.Cache().Drop, Now: now,
		}),
		settings: settings.NewService(
			settings.Deps{Store: store, Drop: auth.Cache().Drop, Now: now},
		),
		users:     user.NewService(user.Deps{Store: store, Drop: auth.Cache().Drop, Now: now}),
		groups:    group.NewService(group.Deps{Store: store, Now: now}),
		oauth:     grants,
		manual:    manual,
		publicURL: d.PublicURL,
	}, nil
}

// API is the domain's partial of the API's strict server; a nil domain's
// serves nothing it can answer, for tests that construct none.
func (d *Domain) API() AccessAPI {
	if d == nil {
		return AccessAPI{}
	}
	return http.NewServer(http.Services{
		Auth: d.authn, Keys: d.keys, Accounts: d.store, Settings: d.settings, Orgs: d.orgAdmin,
		Users: d.users, Groups: d.groups, OAuth: d.oauth,
	})
}

// OperatorAPI is the domain's partial of the operator API's strict
// server; a nil domain's serves nothing it can answer.
func (d *Domain) OperatorAPI() AccessOperatorAPI {
	if d == nil {
		return AccessOperatorAPI{}
	}
	return http.NewOperatorServer(d.orgs)
}

// OrganizationID resolves an organization's slug for another domain's
// operator operations, which name organizations as the operator does;
// ok is false when no organization has it.
func (d *Domain) OrganizationID(
	ctx context.Context,
	slug string,
) (id kernel.OrganizationID, ok bool, err error) {
	o, err := d.orgs.Get(ctx, slug)
	switch {
	case errors.Is(err, port.ErrNotFound):
		return id, false, nil
	case err != nil:
		return id, false, fmt.Errorf("access: %w", err)
	}
	return o.ID, true, nil
}

// Seats counts the seats in use in org and in acct for another domain's
// reads: each active member and each API key holding a seat, as the seats
// limit counts them.
func (d *Domain) Seats(
	ctx context.Context,
	org kernel.OrganizationID,
	acct kernel.AccountID,
) (featuresapi.Usage, error) {
	return d.store.Seats(ctx, org, acct) //nolint:wrapcheck // the store names itself
}

// Routes mounts the domain's pages and protocols beside the APIs on r:
// the break-glass login at accessapi.LoginPattern and the authorization
// server's endpoints at the OAuth patterns. A nil domain's answer 501, as
// its API partial's operations do, so the routes the controls know of
// exist.
func (d *Domain) Routes(r chi.Router) {
	if d == nil {
		for _, p := range []string{
			accessapi.LoginPattern, accessapi.OAuthMetadataPattern,
			accessapi.OAuthRegisterPattern, accessapi.OAuthAuthorizePattern,
			accessapi.OAuthTokenPattern,
		} {
			r.HandleFunc(p, func(w nethttp.ResponseWriter, _ *nethttp.Request) {
				problem.Write(w, problem.NotImplemented.New("No access domain serves this route."))
			})
		}
		return
	}
	http.NewLogin(d.authn, d.publicURL).Routes(r)
	http.NewOAuth(d.oauth).Routes(r)
}

// Authenticator is the middleware that authenticates the API's requests.
func (d *Domain) Authenticator() accessapi.Authenticator {
	return http.NewAuthenticator(d.authn, d.publicURL)
}

// OperatorAuthenticator is the middleware that authenticates the operator
// API's requests, accepting operator tokens alone.
func (d *Domain) OperatorAuthenticator() accessapi.Authenticator {
	return http.NewOperatorAuthenticator(d.authn)
}

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

// AccountOrganization is the organization that holds acct, for work
// outside a request that knows only its account, as a queued job does;
// the zero ID when no account acct exists.
func (d *Domain) AccountOrganization(
	ctx context.Context,
	acct kernel.AccountID,
) (kernel.OrganizationID, error) {
	a, err := d.store.Account(ctx, acct)
	switch {
	case errors.Is(err, port.ErrNotFound):
		return kernel.OrganizationID{}, nil
	case err != nil:
		return kernel.OrganizationID{}, err //nolint:wrapcheck // the store names the read
	}
	return a.OrganizationID, nil
}

// Cron is the domain's periodic tasks: authn.sessions.expire, hourly,
// which also deletes the authorization server's ended requests and unused
// clients, and authn.keys.rotate, daily, unless auth.signing.rotation is
// manual.
func (d *Domain) Cron() []cron.Task {
	tasks := make([]cron.Task, 0, 2)
	tasks = append(tasks, cron.Task{
		Kind:  ExpireSessions,
		Every: expireEvery,
		Handler: func(ctx context.Context, _ queue.Job) error {
			//nolint:wrapcheck // each names itself
			return errors.Join(d.authn.Expire(ctx), d.oauth.Expire(ctx))
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
