package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Prefix starts every API key secret, so secret scanners and log masking
// recognize a leaked one.
//
// ref: decision 0006 § "API keys and organization keys"
const Prefix = "iwk_"

// secretBytes is the random part of a secret, 43 base64url characters.
const secretBytes = 32

// Type is how a key proves itself: a secret, or a JWT from an OIDC issuer.
type Type string

// The key types.
//
// ref: decision 0006 § "API keys and organization keys"
const (
	TypeSecret Type = "SECRET"
	TypeOIDC   Type = "OIDC"
)

// TeamsClaim is the claim whose values, after the claim mapping, are an
// OIDC key's session's teams, as Spacelift's groups claim is.
const TeamsClaim = "groups"

// Key is an API key.
type Key struct {
	OrganizationID kernel.OrganizationID
	AccountID      kernel.AccountID
	ID             kernel.ULID
	Name           string
	Description    string
	Type           Type
	// SecretHash is the SHA-256 of a secret key's secret.
	SecretHash []byte
	// OIDC is an OIDC key's issuer settings.
	OIDC    OIDC
	Teams   []string
	Machine bool
	// Owner is a personal key's member, whose principal in the key's
	// account its tokens act as; zero for a key of the account.
	Owner      kernel.MemberID
	OwnerLogin string
	// Creator is the login of the principal that created the key.
	Creator        string
	ExpiresAt      time.Time
	DisabledAt     time.Time
	DisabledReason string
	LastUsedAt     time.Time
	CreatedAt      time.Time
	DeletedAt      time.Time
	// Admin is whether the key holds space-admin on the account's top
	// space, which makes it an account admin: the binding is the one source
	// of truth, and no input sets the field.
	// ref: DV-013
	Admin bool
}

// OIDC is an OIDC key's settings: the issuer whose tokens it takes, the
// client ID their aud must contain, the expression their sub must match,
// and the mapping from the issuer's claim names to infraweave's.
type OIDC struct {
	Issuer            string
	ClientID          string
	SubjectExpression string
	ClaimMapping      []Mapping
}

// Mapping renames the issuer's claim Name to Value, such as
// cognito:groups to groups.
type Mapping struct {
	Name  string
	Value string
}

// Personal reports whether the key is a member's rather than the
// account's.
func (k Key) Personal() bool { return !k.Owner.IsZero() }

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
func NewSecret() string { return newSecret(Prefix) }

// Hash is the stored form of secret, its SHA-256, and false when secret is
// not Prefix and 43 base64url characters.
func Hash(secret string) ([]byte, bool) { return hash(Prefix, secret) }

func newSecret(prefix string) string {
	b := make([]byte, secretBytes)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

func hash(prefix, secret string) ([]byte, bool) {
	raw, ok := strings.CutPrefix(secret, prefix)
	if !ok {
		return nil, false
	}
	if b, err := base64.RawURLEncoding.DecodeString(raw); err != nil || len(b) != secretBytes {
		return nil, false
	}
	h := sha256.Sum256([]byte(secret))
	return h[:], true
}

// CompileSubject compiles a subject expression anchored at both ends, so
// repo:main matches only repo:main and never x-repo:main-evil.
func CompileSubject(expr string) (*regexp.Regexp, error) {
	return regexp.Compile(`^(?:` + expr + `)$`) //nolint:wrapcheck // the caller names the field
}

// Teams are the session teams of a token with claims: the key's own, then
// the values of TeamsClaim after o's mapping, a string or a list of them,
// each once.
func (o OIDC) Teams(own []string, claims map[string]any) []string {
	mapped := map[string]any{}
	for k, v := range claims {
		mapped[k] = v
	}
	for _, m := range o.ClaimMapping {
		if v, ok := claims[m.Name]; ok {
			mapped[m.Value] = v
		}
	}
	out := slices.Clone(own)
	add := func(v any) {
		if s, ok := v.(string); ok && s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	switch v := mapped[TeamsClaim].(type) {
	case []any:
		for _, e := range v {
			add(e)
		}
	default:
		add(v)
	}
	return out
}
