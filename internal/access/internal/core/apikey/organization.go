package apikey

import (
	"crypto/subtle"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// OrganizationPrefix starts every organization key secret, so a scanner
// tells one from an account's key (decision 0006).
const OrganizationPrefix = "iwo_"

// OrganizationKey is an organization key: held by the organization's
// admins, exchanged for a token that only the Organization tag accepts,
// reaching no account's data and taking no seat.
type OrganizationKey struct {
	OrganizationID kernel.OrganizationID
	ID             kernel.ULID
	Name           string
	// SecretHash is the SHA-256 of the secret.
	SecretHash []byte
	// CreatorMemberID is the member that created the key; zero when a
	// break-glass admin or another organization key did.
	CreatorMemberID kernel.MemberID
	ExpiresAt       time.Time
	DisabledAt      time.Time
	LastUsedAt      time.Time
	CreatedAt       time.Time
}

// NewOrganizationSecret returns a new organization key secret:
// OrganizationPrefix and 32 random bytes in base64url.
func NewOrganizationSecret() string { return newSecret(OrganizationPrefix) }

// HashOrganization is the stored form of an organization key's secret, its
// SHA-256, and false when secret is not OrganizationPrefix and 43
// base64url characters.
func HashOrganization(secret string) ([]byte, bool) { return hash(OrganizationPrefix, secret) }

// Usable reports whether the key may exchange at now: neither disabled
// nor expired.
func (k OrganizationKey) Usable(now time.Time) bool {
	return k.DisabledAt.IsZero() && (k.ExpiresAt.IsZero() || now.Before(k.ExpiresAt))
}

// Matches reports whether secret is the key's, comparing SHA-256 digests
// in constant time.
func (k OrganizationKey) Matches(secret string) bool {
	h, ok := HashOrganization(secret)
	return ok && subtle.ConstantTimeCompare(h, k.SecretHash) == 1
}
