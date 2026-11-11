package assembly

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// The auth.signing settings' values.
const (
	BackendLocal       = "local"
	BackendKMS         = "kms"
	RotationAutomatic  = "automatic"
	RotationManual     = "manual"
	defaultAPITokenTTL = 10 * time.Hour
	// The authorization server's lifetimes: an access token, a refresh
	// token unused, and a code.
	// ref: decision 0006 § "Token kinds"
	defaultOAuthAccessTTL  = time.Hour
	defaultOAuthRefreshTTL = 30 * 24 * time.Hour
	defaultOAuthCodeTTL    = time.Minute
	// The worker protocol's lifetimes: a worker session token, renewed by
	// the poll; a run token, which the worker protocol accepts only while
	// its run is in a worker state, so this bounds a token whose run is
	// lost track of; and an in-run API token.
	// ref: decision 0006 § "Token kinds"
	defaultWorkerTTL = time.Hour
	defaultRunTTL    = 24 * time.Hour
	defaultRunAPITTL = 3 * time.Hour
	// A browser session's limits: idle and absolute.
	// ref: decision 0006 § "Token kinds"
	defaultIdleTimeout = 12 * time.Hour
	defaultMaxAge      = 7 * 24 * time.Hour
)

// ErrConfig reports an auth setting the domain could not run with.
var ErrConfig = errors.New("invalid auth configuration")

// Settings are the access domain's settings, under auth.
//
// ref: decision 0006 § "Configuration"
type Settings struct {
	// APIIssuer is the iss of every API-facing token; empty means
	// server.public_url.
	APIIssuer string `mapstructure:"api_issuer"`
	// RunAPIToken is auth.run_api_token: the in-run API token's issuer.
	RunAPIToken RunAPITokenSettings `mapstructure:"run_api_token"`
	Tokens      TokenSettings       `mapstructure:"tokens"`
	Session     SessionSettings     `mapstructure:"session"`
	Signing     SigningSettings     `mapstructure:"signing"`
	OIDCAPIKeys OIDCAPIKeySettings  `mapstructure:"oidc_api_keys"`
	Operator    OperatorSettings    `mapstructure:"operator"`
}

// OperatorSettings are auth.operator.
type OperatorSettings struct {
	// Keys are the operator keys, which exchange for operator tokens: they
	// live in configuration so the operator can act before any
	// organization exists.
	// ref: decision 0006 § "The principal"
	Keys []OperatorKey `mapstructure:"keys"`
}

// OperatorKey is one operator key: a ULID, and the lower-case hex SHA-256
// of its secret, so configuration never holds the secret itself.
type OperatorKey struct {
	ID           string `mapstructure:"id"`
	SecretSHA256 string `mapstructure:"secret_sha256"`
}

// OIDCAPIKeySettings are auth.oidc_api_keys.
type OIDCAPIKeySettings struct {
	// AllowedIssuers are the issuers an OIDC API key may name, checked at
	// creation and at every exchange; empty allows any HTTPS issuer. Only
	// HTTPS is fetched, since a server fetching user-supplied URLs is an
	// SSRF path.
	// ref: DV-014
	AllowedIssuers []string `mapstructure:"allowed_issuers"`
	// CAFile is a PEM file of certificate authorities trusted beside the
	// system's when fetching an issuer's documents, for an issuer under a
	// private CA; empty trusts the system's alone.
	CAFile string `mapstructure:"ca_file"`
}

// RunAPITokenSettings are auth.run_api_token.
type RunAPITokenSettings struct {
	// Issuer is the in-run API token's iss; empty means
	// server.public_url.
	Issuer string `mapstructure:"issuer"`
}

// TokenSettings are auth.tokens: each token kind's lifetime.
type TokenSettings struct {
	// API is the API token's, which an API key or an operator key
	// exchanges for.
	API Lifetime `mapstructure:"api"`
	// OAuthAccess is an MCP access token's; OAuthRefresh is how long a
	// grant's refresh token lives unused, each refresh starting it again;
	// OAuthCode is an authorization code's.
	OAuthAccess  Lifetime `mapstructure:"oauth_access"`
	OAuthRefresh Lifetime `mapstructure:"oauth_refresh"`
	OAuthCode    Lifetime `mapstructure:"oauth_code"`
	// Worker is a worker session token's, Run a run token's and RunAPI an
	// in-run API token's.
	Worker Lifetime `mapstructure:"worker"`
	Run    Lifetime `mapstructure:"run"`
	RunAPI Lifetime `mapstructure:"run_api"`
}

// longest is the longest lifetime of a token the api-token signing keys
// sign, so a retiring key stays published until every one has expired.
func (t TokenSettings) longest() time.Duration {
	return max(t.API.TTL, t.OAuthAccess.TTL, t.Worker.TTL, t.Run.TTL, t.RunAPI.TTL)
}

// SessionSettings are auth.session: a browser session's limits. It ends
// IdleTimeout after its last use and MaxAge after its sign-in, whichever
// comes first, and a CLI token from it ends with it.
type SessionSettings struct {
	IdleTimeout time.Duration `mapstructure:"idle_timeout"`
	MaxAge      time.Duration `mapstructure:"max_age"`
}

// Lifetime is one token kind's.
type Lifetime struct {
	TTL time.Duration `mapstructure:"ttl"`
}

// SigningSettings are auth.signing.
type SigningSettings struct {
	// Backend is where the keys live: local, generated in process and
	// sealed under the keyring, or kms, the KMS keys KMSKeys names.
	Backend string `mapstructure:"backend"`
	// Rotation is automatic, under which authn.keys.rotate replaces each
	// local key every 90 days and moves to a kms key the operator adds, or
	// manual, under which it does not run.
	Rotation string `mapstructure:"rotation"`
	// KMSKeys are the kms backend's keys, oldest first: the newest signs
	// once it has been published for 48 hours.
	KMSKeys []KMSKey `mapstructure:"kms_keys"`
}

// KMSKey is one KMS signing key: a provider of secrets.kek.provider's but
// local, and the key's reference in that provider's form.
type KMSKey struct {
	Provider string `mapstructure:"provider"`
	Ref      string `mapstructure:"ref"`
}

// String is <provider>:<ref>, as signing_keys.kms_key_ref stores it.
func (k KMSKey) String() string { return k.Provider + ":" + k.Ref }

// kmsProviders are the providers a KMSKey may name.
var kmsProviders = []string{"awskms", "gcpkms", "azurekv", "vault"}

// DefaultSettings are the auth settings' defaults.
//
// ref: decision 0006
func DefaultSettings() Settings {
	return Settings{
		Tokens: TokenSettings{
			API:          Lifetime{TTL: defaultAPITokenTTL},
			OAuthAccess:  Lifetime{TTL: defaultOAuthAccessTTL},
			OAuthRefresh: Lifetime{TTL: defaultOAuthRefreshTTL},
			OAuthCode:    Lifetime{TTL: defaultOAuthCodeTTL},
			Worker:       Lifetime{TTL: defaultWorkerTTL},
			Run:          Lifetime{TTL: defaultRunTTL},
			RunAPI:       Lifetime{TTL: defaultRunAPITTL},
		},
		Session: SessionSettings{IdleTimeout: defaultIdleTimeout, MaxAge: defaultMaxAge},
		Signing: SigningSettings{Backend: BackendLocal, Rotation: RotationAutomatic},
	}
}

// Validate rejects a lifetime that is not positive, an unknown backend or
// rotation, an issuer that is not an absolute http or https URL, and an
// allowed OIDC issuer that is not an absolute https URL. Errors name the
// key.
func (s Settings) Validate() error {
	var errs []error
	if s.Tokens.API.TTL <= 0 {
		errs = append(errs, fmt.Errorf("%w: auth.tokens.api.ttl must be positive, got %s",
			ErrConfig, s.Tokens.API.TTL))
	}
	for _, l := range []struct {
		key string
		d   time.Duration
	}{
		{"auth.tokens.oauth_access.ttl", s.Tokens.OAuthAccess.TTL},
		{"auth.tokens.oauth_refresh.ttl", s.Tokens.OAuthRefresh.TTL},
		{"auth.tokens.oauth_code.ttl", s.Tokens.OAuthCode.TTL},
		{"auth.tokens.worker.ttl", s.Tokens.Worker.TTL},
		{"auth.tokens.run.ttl", s.Tokens.Run.TTL},
		{"auth.tokens.run_api.ttl", s.Tokens.RunAPI.TTL},
		{"auth.session.idle_timeout", s.Session.IdleTimeout},
		{"auth.session.max_age", s.Session.MaxAge},
	} {
		if l.d <= 0 {
			errs = append(
				errs,
				fmt.Errorf("%w: %s must be positive, got %s", ErrConfig, l.key, l.d),
			)
		}
	}
	if s.Signing.Rotation != RotationAutomatic && s.Signing.Rotation != RotationManual {
		errs = append(errs, fmt.Errorf("%w: auth.signing.rotation is %q, want %s or %s",
			ErrConfig, s.Signing.Rotation, RotationAutomatic, RotationManual))
	}
	errs = append(errs, s.Signing.validateBackend()...)
	if s.APIIssuer != "" {
		if !absoluteURL(s.APIIssuer) {
			errs = append(
				errs,
				fmt.Errorf("%w: auth.api_issuer %q is not an absolute http or https URL",
					ErrConfig, s.APIIssuer),
			)
		}
	}
	if iss := s.RunAPIToken.Issuer; iss != "" && !absoluteURL(iss) {
		errs = append(errs, fmt.Errorf(
			"%w: auth.run_api_token.issuer %q is not an absolute http or https URL",
			ErrConfig,
			iss,
		))
	}
	errs = append(errs, s.Operator.validate()...)
	for i, iss := range s.OIDCAPIKeys.AllowedIssuers {
		if u, err := url.Parse(iss); !absoluteURL(iss) || err != nil || u.Scheme != "https" {
			errs = append(errs, fmt.Errorf(
				"%w: auth.oidc_api_keys.allowed_issuers[%d] %q is not an absolute https URL",
				ErrConfig, i, iss))
		}
	}
	return errors.Join(errs...)
}

// validate checks that each operator key has a ULID, named once, and a
// SHA-256 in 64 lower-case hex digits.
func (o OperatorSettings) validate() []error {
	var errs []error
	seen := map[kernel.ULID]bool{}
	for i, k := range o.Keys {
		id, err := kernel.ParseULID(k.ID)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("%w: auth.operator.keys[%d].id %q is not a ULID",
				ErrConfig, i, k.ID))
		case seen[id]:
			errs = append(errs, fmt.Errorf("%w: auth.operator.keys[%d] repeats the id %s",
				ErrConfig, i, k.ID))
		}
		seen[id] = true
		if _, ok := sha256Hex(k.SecretSHA256); !ok {
			errs = append(errs, fmt.Errorf(
				"%w: auth.operator.keys[%d].secret_sha256 is not a SHA-256 in 64 lower-case hex digits",
				ErrConfig,
				i,
			))
		}
	}
	return errs
}

// operators are the operator keys by ID, as authn takes them; Validate has
// checked each.
func (o OperatorSettings) operators() map[kernel.ULID][sha256.Size]byte {
	out := make(map[kernel.ULID][sha256.Size]byte, len(o.Keys))
	for _, k := range o.Keys {
		id, _ := kernel.ParseULID(k.ID)
		out[id], _ = sha256Hex(k.SecretSHA256)
	}
	return out
}

func sha256Hex(s string) ([sha256.Size]byte, bool) {
	var out [sha256.Size]byte
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != sha256.Size || strings.ToLower(s) != s {
		return out, false
	}
	copy(out[:], b)
	return out, true
}

// validateBackend checks that the kms backend has keys, each of a known
// provider and named once, and the local backend none. manual rotation
// would leave an added KMS key unused, since only authn.keys.rotate moves
// to it.
func (s SigningSettings) validateBackend() []error {
	switch s.Backend {
	case BackendLocal:
		if len(s.KMSKeys) > 0 {
			return []error{
				fmt.Errorf("%w: auth.signing.kms_keys is set, but auth.signing.backend is %s",
					ErrConfig, BackendLocal),
			}
		}
		return nil
	case BackendKMS:
	default:
		return []error{fmt.Errorf("%w: auth.signing.backend is %q, want %s or %s",
			ErrConfig, s.Backend, BackendLocal, BackendKMS)}
	}
	var errs []error
	if len(s.KMSKeys) == 0 {
		errs = append(
			errs,
			fmt.Errorf("%w: auth.signing.kms_keys is required by auth.signing.backend %s",
				ErrConfig, BackendKMS),
		)
	}
	if s.Rotation == RotationManual {
		errs = append(errs, fmt.Errorf(
			"%w: auth.signing.rotation %s does not apply to auth.signing.backend %s, which rotates when a key is added",
			ErrConfig,
			RotationManual,
			BackendKMS,
		))
	}
	seen := map[string]bool{}
	for i, k := range s.KMSKeys {
		switch {
		case !slices.Contains(kmsProviders, k.Provider):
			errs = append(
				errs,
				fmt.Errorf("%w: auth.signing.kms_keys[%d].provider is %q, want one of %s",
					ErrConfig, i, k.Provider, strings.Join(kmsProviders, ", ")),
			)
		case k.Ref == "":
			errs = append(
				errs,
				fmt.Errorf("%w: auth.signing.kms_keys[%d].ref is required", ErrConfig, i),
			)
		case seen[k.String()]:
			errs = append(
				errs,
				fmt.Errorf("%w: auth.signing.kms_keys[%d] repeats %s", ErrConfig, i, k),
			)
		}
		seen[k.String()] = true
	}
	return errs
}

// absoluteURL accepts an absolute http or https URL with a host, and no
// query, fragment or credentials, as a token's iss must be.
func absoluteURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" &&
		u.User == nil && u.RawQuery == "" && u.Fragment == ""
}
