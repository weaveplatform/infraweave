package kek

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// OpenLocal returns the local provider, reading its KEK from
// secrets.kek.local.key_file or Env.
func OpenLocal(c LocalConfig) (Provider, error) {
	if err := (Config{Provider: Local, Local: c}).Validate(); err != nil {
		return nil, err
	}
	text := c.Key
	if c.KeyFile != "" {
		b, err := os.ReadFile(c.KeyFile)
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
// cipher.NewGCMWithRandomNonce, with the associated data AAD(id).
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

func (l localProvider) Wrap(_ context.Context, id uint32, key []byte) ([]byte, error) {
	//nolint:gosec // G407: NewGCMWithRandomNonce draws the nonce; Seal takes none
	return l.aead.Seal(
		[]byte{version},
		nil,
		key,
		AAD(id),
	), nil
}

func (l localProvider) Unwrap(_ context.Context, id uint32, wrapped []byte) ([]byte, error) {
	if len(wrapped) == 0 || wrapped[0] != version {
		return nil, ErrUnwrap
	}
	key, err := l.aead.Open(nil, nil, wrapped[1:], AAD(id))
	if err != nil {
		return nil, ErrUnwrap
	}
	return key, nil
}

func (localProvider) Ref() string { return Local }
