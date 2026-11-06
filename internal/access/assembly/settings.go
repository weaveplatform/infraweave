package assembly

import (
	"errors"
	"fmt"
	"net/url"
	"time"
)

// The auth.signing settings' values.
const (
	BackendLocal       = "local"
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
	// sealed under the keyring.
	Backend string `mapstructure:"backend"`
	// Rotation is automatic, under which authn.keys.rotate replaces each
	// key every 90 days, or manual, under which it does not run.
	Rotation string `mapstructure:"rotation"`
}

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
	if s.Signing.Backend != BackendLocal {
		errs = append(errs, fmt.Errorf("%w: auth.signing.backend is %q, want %s",
			ErrConfig, s.Signing.Backend, BackendLocal))
	}
	if s.Signing.Rotation != RotationAutomatic && s.Signing.Rotation != RotationManual {
		errs = append(errs, fmt.Errorf("%w: auth.signing.rotation is %q, want %s or %s",
			ErrConfig, s.Signing.Rotation, RotationAutomatic, RotationManual))
	}
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

// absoluteURL accepts an absolute http or https URL with a host, and no
// query, fragment or credentials, as a token's iss must be.
func absoluteURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" &&
		u.User == nil && u.RawQuery == "" && u.Fragment == ""
}
