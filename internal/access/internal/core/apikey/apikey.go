package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Prefix starts every API key secret, so secret scanners and log masking
// recognize a leaked one (decision 0006).
const Prefix = "iwk_"

// secretBytes is the random part of a secret, 43 base64url characters.
const secretBytes = 32

// Type is how a key proves itself: a secret, or a JWT from an OIDC issuer.
type Type string

// The key types of decision 0006.
const (
	TypeSecret Type = "SECRET"
	TypeOIDC   Type = "OIDC"
)

// Key is what the exchange needs of an API key.
type Key struct {
	OrganizationID kernel.OrganizationID
	AccountID      kernel.AccountID
	ID             kernel.ULID
	Name           string
	Type           Type
	// SecretHash is the SHA-256 of a secret key's secret.
	SecretHash []byte
	Teams      []string
	ExpiresAt  time.Time
	DisabledAt time.Time
	DeletedAt  time.Time
}

// Usable reports whether the key may exchange at now: neither disabled,
// deleted nor expired.
func (k Key) Usable(now time.Time) bool {
	return k.DisabledAt.IsZero() && k.DeletedAt.IsZero() &&
		(k.ExpiresAt.IsZero() || now.Before(k.ExpiresAt))
}

// Matches reports whether secret is the key's, comparing SHA-256 digests
// in constant time, so the time taken tells nothing of the stored hash.
func (k Key) Matches(secret string) bool {
	h, ok := Hash(secret)
	return ok && k.Type == TypeSecret && subtle.ConstantTimeCompare(h, k.SecretHash) == 1
}

// NewSecret returns a new secret: Prefix and 32 random bytes in base64url.
func NewSecret() string {
	b := make([]byte, secretBytes)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	return Prefix + base64.RawURLEncoding.EncodeToString(b)
}

// Hash is the stored form of secret, its SHA-256, and false when secret is
// not Prefix and 43 base64url characters.
func Hash(secret string) ([]byte, bool) {
	raw, ok := strings.CutPrefix(secret, Prefix)
	if !ok {
		return nil, false
	}
	if b, err := base64.RawURLEncoding.DecodeString(raw); err != nil || len(b) != secretBytes {
		return nil, false
	}
	h := sha256.Sum256([]byte(secret))
	return h[:], true
}
