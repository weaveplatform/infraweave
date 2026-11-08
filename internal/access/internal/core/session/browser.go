package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// CookieName is the browser session's cookie. The __Host- prefix makes a
// browser refuse it unless it is Secure, has Path=/ and no Domain, so no
// subdomain can set or read it.
//
// ref: decision 0006 § "Sessions"
const CookieName = "__Host-infraweave_session"

// secretLen is the cookie secret's length in bytes before encoding.
const secretLen = 32

// NewSecret is a browser session's cookie value, 32 random bytes in
// unpadded base64url, and the SHA-256 the session row stores.
func NewSecret() (string, []byte) {
	b := make([]byte, secretLen)
	_, _ = rand.Read(b) // crypto/rand never fails
	s := base64.RawURLEncoding.EncodeToString(b)
	return s, HashSecret(s)
}

// HashSecret is the SHA-256 a cookie value is stored and looked up by.
func HashSecret(secret string) []byte {
	h := sha256.Sum256([]byte(secret))
	return h[:]
}

// Admitted is an account that admits a member.
type Admitted struct {
	ID   kernel.AccountID
	Slug kernel.Slug
	Name string
}

// Admission decides which accounts admit a member: rows of account_users
// today, which P4b's bindings and P13b's LOGIN policies extend. Both are
// evaluated when read, so a change admits or refuses at the member's next
// switch.
//
// ref: decision 0007
type Admission interface {
	// Accounts are the accounts of org that admit member, in slug order.
	Accounts(
		ctx context.Context,
		org kernel.OrganizationID,
		member kernel.MemberID,
	) ([]Admitted, error)
	// Admits reports whether acct, an account of org, admits member; an
	// account of another organization admits no one.
	Admits(
		ctx context.Context,
		org kernel.OrganizationID,
		member kernel.MemberID,
		acct kernel.AccountID,
	) (Admitted, bool, error)
}
