package kek

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Provider wraps the keyring's data keys under a key encryption key that
// never enters the database. Wrap and Unwrap bind the data key's ID into
// the wrapping, so a wrapped key copied to another ID fails to unwrap.
type Provider interface {
	Wrap(ctx context.Context, id uint32, key []byte) ([]byte, error)
	Unwrap(ctx context.Context, id uint32, wrapped []byte) ([]byte, error)
	// Ref names the KEK, stored beside each key it wraps as kek_ref.
	Ref() string
}

// The providers secrets.kek.provider names. Remote providers join this
// list with their own settings.
const Local = "local"

// Env is the variable that holds a local KEK, base64 of 32 bytes, in
// place of secrets.kek.local.key_file.
const Env = "INFRAWEAVE_SECRETS_KEK"

// Config holds the KEK settings, keyed below secrets.kek.
type Config struct {
	Provider string      `mapstructure:"provider"`
	Local    LocalConfig `mapstructure:"local"`
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

// Validate rejects an unknown provider and a local provider with no key,
// or with both a key file and Env. Errors name the key.
func (c Config) Validate() error {
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
		return nil
	default:
		return fmt.Errorf(
			"%w: secrets.kek.provider must be %s, got %q",
			ErrConfig,
			Local,
			c.Provider,
		)
	}
}

// New returns the provider c names, reading a local KEK from its file or
// Env. It returns ErrNoProvider when c names none.
func New(c Config) (Provider, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if c.Provider == "" {
		return nil, ErrNoProvider
	}
	text := c.Local.Key
	if c.Local.KeyFile != "" {
		b, err := os.ReadFile(c.Local.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("%w: secrets.kek.local.key_file: %w", ErrConfig, err)
		}
		text = string(b)
	}
	k, err := ParseKey(text)
	if err != nil {
		return nil, err
	}
	return NewLocal(k)
}

// ParseKey decodes a local KEK: base64 of 32 bytes, around which spaces
// and newlines are ignored.
func ParseKey(s string) ([]byte, error) {
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("%w: the local KEK is not base64: %w", ErrConfig, err)
	}
	if len(k) != 32 {
		return nil, fmt.Errorf("%w: the local KEK is %d bytes, want 32", ErrConfig, len(k))
	}
	return k, nil
}

// version opens a key the local provider wrapped: 0x01 | nonce |
// ciphertext and tag.
const version byte = 0x01

// localProvider wraps with AES-256-GCM under the KEK, through
// cipher.NewGCMWithRandomNonce, with the associated data
// infraweave/data-key/<id>.
type localProvider struct{ aead cipher.AEAD }

// NewLocal returns the local provider for kek, 32 bytes.
func NewLocal(kek []byte) (Provider, error) {
	if len(kek) != 32 {
		return nil, fmt.Errorf("%w: the local KEK is %d bytes, want 32", ErrConfig, len(kek))
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, fmt.Errorf("local KEK: %w", err)
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, fmt.Errorf("local KEK: %w", err)
	}
	return localProvider{aead}, nil
}

func dataKeyAAD(id uint32) []byte {
	return strconv.AppendUint([]byte("infraweave/data-key/"), uint64(id), 10)
}

func (l localProvider) Wrap(_ context.Context, id uint32, key []byte) ([]byte, error) {
	//nolint:gosec // G407: NewGCMWithRandomNonce draws the nonce; Seal takes none
	return l.aead.Seal(
		[]byte{version},
		nil,
		key,
		dataKeyAAD(id),
	), nil
}

func (l localProvider) Unwrap(_ context.Context, id uint32, wrapped []byte) ([]byte, error) {
	if len(wrapped) == 0 || wrapped[0] != version {
		return nil, ErrUnwrap
	}
	key, err := l.aead.Open(nil, nil, wrapped[1:], dataKeyAAD(id))
	if err != nil {
		return nil, ErrUnwrap
	}
	return key, nil
}

func (localProvider) Ref() string { return Local }
