package assembly

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

// The auth.signing settings' values.
const (
	BackendLocal       = "local"
	BackendKMS         = "kms"
	RotationAutomatic  = "automatic"
	RotationManual     = "manual"
	defaultAPITokenTTL = 10 * time.Hour
)

// ErrConfig reports an auth setting the domain could not run with.
var ErrConfig = errors.New("invalid auth configuration")

// Settings are the access domain's settings, under auth (decision 0006).
type Settings struct {
	// APIIssuer is the iss of every API-facing token; empty means
	// server.public_url.
	APIIssuer string          `mapstructure:"api_issuer"`
	Tokens    TokenSettings   `mapstructure:"tokens"`
	Signing   SigningSettings `mapstructure:"signing"`
}

// TokenSettings are auth.tokens: each token kind's lifetime.
type TokenSettings struct {
	// API is the API token's, which an API key or an operator key
	// exchanges for.
	API Lifetime `mapstructure:"api"`
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

// DefaultSettings are decision 0006's defaults.
func DefaultSettings() Settings {
	return Settings{
		Tokens:  TokenSettings{API: Lifetime{TTL: defaultAPITokenTTL}},
		Signing: SigningSettings{Backend: BackendLocal, Rotation: RotationAutomatic},
	}
}

// Validate rejects a lifetime that is not positive, an unknown backend or
// rotation, and an issuer that is not an absolute http or https URL.
// Errors name the key.
func (s Settings) Validate() error {
	var errs []error
	if s.Tokens.API.TTL <= 0 {
		errs = append(errs, fmt.Errorf("%w: auth.tokens.api.ttl must be positive, got %s",
			ErrConfig, s.Tokens.API.TTL))
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
	return errors.Join(errs...)
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
