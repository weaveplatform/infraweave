package kek

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
)

// Provider wraps the keyring's data keys under a key encryption key that
// never enters the database. Wrap and Unwrap bind the data key's ID into
// the wrapping, so a wrapped key copied to another ID fails to unwrap.
type Provider interface {
	Wrap(ctx context.Context, id uint32, key []byte) ([]byte, error)
	Unwrap(ctx context.Context, id uint32, wrapped []byte) ([]byte, error)
	// Ref names the provider and the KEK, stored beside each key it wraps
	// as kek_ref.
	Ref() string
}

// The providers secrets.kek.provider names.
const (
	Local   = "local"
	AWSKMS  = "awskms"
	GCPKMS  = "gcpkms"
	AzureKV = "azurekv"
	Vault   = "vault"
)

// The methods secrets.kek.vault.auth.method names.
const (
	VaultToken      = "token"
	VaultKubernetes = "kubernetes"
	VaultAppRole    = "approle"
)

// Env is the variable that holds a local KEK, base64 of 32 bytes, in
// place of secrets.kek.local.key_file.
const Env = "INFRAWEAVE_SECRETS_KEK"

// Config holds the KEK settings, keyed below secrets.kek. Only the
// settings of the provider Provider names are read.
type Config struct {
	Provider string        `mapstructure:"provider"`
	Local    LocalConfig   `mapstructure:"local"`
	AWSKMS   AWSKMSConfig  `mapstructure:"awskms"`
	GCPKMS   GCPKMSConfig  `mapstructure:"gcpkms"`
	AzureKV  AzureKVConfig `mapstructure:"azurekv"`
	Vault    VaultConfig   `mapstructure:"vault"`
}

// LocalConfig holds the local provider's settings, keyed below
// secrets.kek.local.
type LocalConfig struct {
	// KeyFile holds the KEK as base64 of 32 bytes.
	KeyFile string `mapstructure:"key_file"`
	// Key is Env's value, which the configuration reads itself. Bound as
	// the key secrets.kek, the variable would hide every other
	// secrets.kek setting from viper, and no file may set it.
	Key string `mapstructure:"-"`
}

// AWSKMSConfig holds the awskms provider's settings. Credentials come from
// the AWS SDK's default chain.
type AWSKMSConfig struct {
	// KeyID is the KMS key's ID, ARN, alias name or alias ARN.
	KeyID string `mapstructure:"key_id"`
	// Region is the key's region; "" takes the SDK's.
	Region string `mapstructure:"region"`
}

// GCPKMSConfig holds the gcpkms provider's settings. Credentials come from
// Application Default Credentials.
type GCPKMSConfig struct {
	// KeyName is the CryptoKey's resource name,
	// projects/<p>/locations/<l>/keyRings/<r>/cryptoKeys/<k>.
	KeyName string `mapstructure:"key_name"`
}

// AzureKVConfig holds the azurekv provider's settings. Credentials come
// from azidentity's default chain.
type AzureKVConfig struct {
	// VaultURL is the vault's https URL.
	VaultURL string `mapstructure:"vault_url"`
	// KeyName names an RSA key.
	KeyName string `mapstructure:"key_name"`
	// KeyVersion is the version that wraps new data keys; "" is the
	// key's current version. Each wrapped key records the version that
	// wrapped it, which unwraps it.
	KeyVersion string `mapstructure:"key_version"`
}

// VaultConfig holds the vault provider's settings.
type VaultConfig struct {
	// Address is Vault's URL, such as https://vault.example.com:8200.
	Address string `mapstructure:"address"`
	// Mount is the Transit engine's mount path.
	Mount string `mapstructure:"mount"`
	// KeyName names a Transit key of an AEAD type, such as the default
	// aes256-gcm96, which binds the associated data.
	KeyName string    `mapstructure:"key_name"`
	Auth    VaultAuth `mapstructure:"auth"`
}

// VaultAuth is how the vault provider gets a Vault token.
type VaultAuth struct {
	// Method is token, kubernetes or approle.
	Method string `mapstructure:"method"`
	// Mount is the auth method's mount path; "" is the method's name.
	Mount string `mapstructure:"mount"`
	// TokenFile holds the token for the token method; "" reads
	// VAULT_TOKEN.
	TokenFile string `mapstructure:"token_file"`
	// Role is the kubernetes method's role.
	Role string `mapstructure:"role"`
	// JWTFile holds the service account token the kubernetes method logs
	// in with.
	JWTFile string `mapstructure:"jwt_file"`
	// RoleID and SecretIDFile are the approle method's role ID and the
	// file holding its secret ID.
	RoleID       string `mapstructure:"role_id"`
	SecretIDFile string `mapstructure:"secret_id_file"`
}

// Defaults are the settings an empty configuration means: no provider,
// which every process mode that seals refuses, and Vault's own defaults
// for the vault provider.
func Defaults() Config {
	return Config{Vault: VaultConfig{
		Mount: "transit",
		Auth: VaultAuth{
			Method:  VaultToken,
			JWTFile: "/var/run/secrets/kubernetes.io/serviceaccount/token",
		},
	}}
}

var (
	// ErrConfig reports a KEK setting infraweave could not start with.
	ErrConfig = errors.New("invalid secrets configuration")
	// ErrNoProvider reports a process mode that seals started without
	// secrets.kek.provider.
	ErrNoProvider = errors.New("secrets.kek.provider is required")
	// ErrUnwrap reports a wrapped data key that does not unwrap: the KEK
	// is not the one that wrapped it, or the row was changed.
	ErrUnwrap = errors.New("data key does not unwrap under the configured KEK")
)

// cryptoKeyName is a Cloud KMS CryptoKey's resource name.
var cryptoKeyName = regexp.MustCompile(
	`^projects/[^/]+/locations/[^/]+/keyRings/[^/]+/cryptoKeys/[^/]+$`,
)

// Validate rejects an unknown provider, and a provider without the
// settings it needs. Errors name the key.
func (c Config) Validate() error {
	var missing string
	switch c.Provider {
	case "":
		return nil
	case Local:
		switch {
		case c.Local.KeyFile == "" && c.Local.Key == "":
			return fmt.Errorf("%w: secrets.kek.local.key_file or %s is required", ErrConfig, Env)
		case c.Local.KeyFile != "" && c.Local.Key != "":
			return fmt.Errorf("%w: set secrets.kek.local.key_file or %s, not both", ErrConfig, Env)
		}
	case AWSKMS:
		missing = firstEmpty(map[string]string{"awskms.key_id": c.AWSKMS.KeyID})
	case GCPKMS:
		if c.GCPKMS.KeyName != "" && !cryptoKeyName.MatchString(c.GCPKMS.KeyName) {
			return fmt.Errorf(
				"%w: secrets.kek.gcpkms.key_name must be projects/<p>/locations/<l>/keyRings/<r>/cryptoKeys/<k>, got %q",
				ErrConfig,
				c.GCPKMS.KeyName,
			)
		}
		missing = firstEmpty(map[string]string{"gcpkms.key_name": c.GCPKMS.KeyName})
	case AzureKV:
		if err := httpURL("azurekv.vault_url", c.AzureKV.VaultURL, "https"); err != nil {
			return err
		}
		missing = firstEmpty(map[string]string{"azurekv.key_name": c.AzureKV.KeyName})
	case Vault:
		return c.Vault.validate()
	default:
		return fmt.Errorf(
			"%w: secrets.kek.provider must be %s, %s, %s, %s or %s, got %q",
			ErrConfig, Local, AWSKMS, GCPKMS, AzureKV, Vault, c.Provider)
	}
	if missing != "" {
		return fmt.Errorf("%w: secrets.kek.%s is required", ErrConfig, missing)
	}
	return nil
}

func (v VaultConfig) validate() error {
	if err := v.ValidateConnection(); err != nil {
		return err
	}
	if missing := firstEmpty(
		map[string]string{"vault.mount": v.Mount, "vault.key_name": v.KeyName},
	); missing != "" {
		return fmt.Errorf("%w: secrets.kek.%s is required", ErrConfig, missing)
	}
	return nil
}

// ValidateConnection checks the settings that reach Vault, its address and
// auth method, which the vault signing backend shares with the KEK
// provider; it needs no Transit key of the KEK's.
func (v VaultConfig) ValidateConnection() error {
	if err := httpURL("vault.address", v.Address, "http", "https"); err != nil {
		return err
	}
	want := map[string]string{}
	switch v.Auth.Method {
	case VaultToken:
	case VaultKubernetes:
		want["vault.auth.role"], want["vault.auth.jwt_file"] = v.Auth.Role, v.Auth.JWTFile
	case VaultAppRole:
		want["vault.auth.role_id"], want["vault.auth.secret_id_file"] = v.Auth.RoleID, v.Auth.SecretIDFile
	default:
		return fmt.Errorf(
			"%w: secrets.kek.vault.auth.method must be %s, %s or %s, got %q",
			ErrConfig, VaultToken, VaultKubernetes, VaultAppRole, v.Auth.Method)
	}
	if missing := firstEmpty(want); missing != "" {
		return fmt.Errorf("%w: secrets.kek.%s is required", ErrConfig, missing)
	}
	return nil
}

// firstEmpty returns the first key, in key order, whose setting is empty,
// so an error names the same key every time; "" when none is.
func firstEmpty(settings map[string]string) string {
	var first string
	for k, v := range settings {
		if v == "" && (first == "" || k < first) {
			first = k
		}
	}
	return first
}

// httpURL checks that the setting secrets.kek.<key> is an absolute URL
// with one of schemes.
func httpURL(key, s string, schemes ...string) error {
	if s == "" {
		return fmt.Errorf("%w: secrets.kek.%s is required", ErrConfig, key)
	}
	if u, err := url.Parse(s); err == nil && u.Host != "" && slices.Contains(schemes, u.Scheme) {
		return nil
	}
	return fmt.Errorf(
		"%w: secrets.kek.%s must be a %s URL, got %q",
		ErrConfig,
		key,
		schemes[len(schemes)-1],
		s,
	)
}

// AAD is the associated data a provider binds a data key's wrapping to:
// infraweave/data-key/<id>.
func AAD(id uint32) []byte {
	return strconv.AppendUint([]byte("infraweave/data-key/"), uint64(id), 10)
}
