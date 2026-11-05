package seal

import (
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// envelopeInfo opens the HKDF info of the pool envelope's key, which goes
// on to bind both public keys.
const envelopeInfo = "infraweave/pool-envelope/v1"

// p256Public is an uncompressed P-256 public key's length.
const p256Public = 65

// ErrPoolKey reports a pool key that is not a P-256 key.
var ErrPoolKey = errors.New("seal: pool key must be a P-256 key")

// envelopeAAD is the version byte, the run ID behind its length
// (uint16, big-endian) and the state version (uint64, big-endian), so a
// spec opens only for the run and the execution it was issued for.
func envelopeAAD(runID string, stateVersion int64) ([]byte, error) {
	if len(runID) > math.MaxUint16 {
		return nil, fmt.Errorf("%w: run ID longer than 65535 bytes", ErrFormat)
	}
	aad := make([]byte, 0, 1+2+len(runID)+8)
	aad = append(aad, Version)
	aad = binary.BigEndian.AppendUint16(aad, uint16(len(runID))) //nolint:gosec // checked above
	aad = append(aad, runID...)
	// Two's complement: every int64 has its own uint64.
	return binary.BigEndian.AppendUint64(aad, uint64(stateVersion)), nil //nolint:gosec // see above
}

// envelopeKey is HKDF-SHA256 of the shared secret, with an empty salt and
// info infraweave/pool-envelope/v1 | ephemeral public key | pool public key.
func envelopeKey(shared, ephemeral, pool []byte) ([]byte, error) {
	key, err := hkdf.Key(
		sha256.New,
		shared,
		nil,
		envelopeInfo+string(ephemeral)+string(pool),
		KeySize,
	)
	if err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	return key, nil
}

// SealEnvelope seals plaintext to a worker pool's P-256 public key, for
// run runID at stateVersion, in the pool envelope
// 0x01 | ephemeral public key (65) | nonce | ciphertext and tag.
func SealEnvelope(
	pool *ecdh.PublicKey,
	runID string,
	stateVersion int64,
	plaintext []byte,
) ([]byte, error) {
	if pool.Curve() != ecdh.P256() {
		return nil, ErrPoolKey
	}
	aad, err := envelopeAAD(runID, stateVersion)
	if err != nil {
		return nil, err
	}
	eph, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	shared, err := eph.ECDH(pool)
	if err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	ephPub := eph.PublicKey().Bytes()
	key, err := envelopeKey(shared, ephPub, pool.Bytes())
	if err != nil {
		return nil, err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, 1+p256Public+len(plaintext)+overhead)
	out = append(out, Version)
	out = append(out, ephPub...)
	//nolint:gosec // G407: NewGCMWithRandomNonce draws the nonce; Seal takes none
	return aead.Seal(
		out,
		nil,
		plaintext,
		aad,
	), nil
}

// OpenEnvelope opens an envelope SealEnvelope sealed to pool's public key
// for the same run and state version.
func OpenEnvelope(
	pool *ecdh.PrivateKey,
	runID string,
	stateVersion int64,
	sealed []byte,
) ([]byte, error) {
	if pool.Curve() != ecdh.P256() {
		return nil, ErrPoolKey
	}
	if len(sealed) == 0 {
		return nil, ErrFormat
	}
	if sealed[0] != Version {
		return nil, fmt.Errorf("%w: 0x%02x", ErrVersion, sealed[0])
	}
	if len(sealed) < 1+p256Public+overhead {
		return nil, ErrFormat
	}
	aad, err := envelopeAAD(runID, stateVersion)
	if err != nil {
		return nil, err
	}
	ephPub := sealed[1 : 1+p256Public]
	eph, err := ecdh.P256().NewPublicKey(ephPub)
	if err != nil {
		return nil, ErrFormat
	}
	shared, err := pool.ECDH(eph)
	if err != nil {
		return nil, ErrAuth
	}
	key, err := envelopeKey(shared, ephPub, pool.PublicKey().Bytes())
	if err != nil {
		return nil, err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, nil, sealed[1+p256Public:], aad)
	if err != nil {
		return nil, ErrAuth
	}
	return plaintext, nil
}
