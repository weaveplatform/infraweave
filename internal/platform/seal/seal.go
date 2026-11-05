package seal

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
)

// Version is the version byte every format here starts with.
const Version byte = 0x01

// KeySize is the size of every key the formats take: AES-256.
const KeySize = 32

// overhead is what cipher.NewGCMWithRandomNonce's Seal adds to a
// plaintext: the 12-byte nonce before it and the 16-byte tag after it.
const overhead = 12 + 16

var (
	// ErrKeySize reports a key that is not KeySize bytes.
	ErrKeySize = errors.New("seal: key must be 32 bytes")
	// ErrVersion reports a sealed value whose version byte is not Version.
	ErrVersion = errors.New("seal: unknown format version")
	// ErrFormat reports a sealed value too short or malformed to be one.
	ErrFormat = errors.New("seal: malformed sealed value")
	// ErrAuth reports a sealed value that fails to authenticate: the wrong
	// key, other associated data, or bytes changed, dropped or reordered.
	ErrAuth = errors.New("seal: message authentication failed")
)

// newAEAD is AES-256-GCM with random nonces, the one AEAD decision 0013
// allows: cipher.NewGCM takes a caller's nonce, which FIPS 140-only mode
// refuses.
func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, ErrKeySize
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	return aead, nil
}

// columnHeader is the version byte and the key ID.
const columnHeader = 1 + 4

// SealColumn seals plaintext under key, data key id, in the column format
// 0x01 | id (uint32, big-endian) | nonce | ciphertext and tag. aad is the
// column's associated data, <table>.<column>:<primary key>, so the value
// opens only in the row and column it was sealed for.
func SealColumn(key []byte, id uint32, aad, plaintext []byte) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, columnHeader, columnHeader+len(plaintext)+overhead)
	out[0] = Version
	binary.BigEndian.PutUint32(out[1:], id)
	//nolint:gosec // G407: NewGCMWithRandomNonce draws the nonce; Seal takes none
	return aead.Seal(
		out,
		nil,
		plaintext,
		aad,
	), nil
}

// ColumnKeyID returns the ID of the data key a column value was sealed
// under, so the keyring can pick the key to open it with.
func ColumnKeyID(sealed []byte) (uint32, error) {
	if len(sealed) == 0 {
		return 0, ErrFormat
	}
	if sealed[0] != Version {
		return 0, fmt.Errorf("%w: 0x%02x", ErrVersion, sealed[0])
	}
	if len(sealed) < columnHeader+overhead {
		return 0, ErrFormat
	}
	return binary.BigEndian.Uint32(sealed[1:]), nil
}

// OpenColumn opens a value SealColumn sealed under key with the same aad.
func OpenColumn(key, aad, sealed []byte) ([]byte, error) {
	if _, err := ColumnKeyID(sealed); err != nil {
		return nil, err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, nil, sealed[columnHeader:], aad)
	if err != nil {
		return nil, ErrAuth
	}
	return plaintext, nil
}
