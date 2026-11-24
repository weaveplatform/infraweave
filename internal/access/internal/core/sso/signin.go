package sso

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/member"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// The limits and names of an OIDC sign-in.
//
// ref: decision 0006 § "Human login"
const (
	// RequestTTL is how long a started sign-in waits for its callback.
	RequestTTL = 10 * time.Minute
	// Skew is the clock skew an ID token's auth_time is allowed.
	Skew = 60 * time.Second
	// RequestCookie names the browser that started a sign-in: its value's
	// SHA-256 is the request's browser hash. The __Host- prefix obliges
	// Secure, Path=/ and no Domain.
	RequestCookie = "__Host-infraweave_sso"
	// CallbackPath is the one redirect URI path of every OIDC provider; the
	// state names the request.
	CallbackPath = accessapi.OIDCCallbackPattern
)

// Scopes are the scopes every authorization request asks for. A provider
// that knows no groups scope ignores it.
var Scopes = []string{"openid", "email", "profile", "groups"}

// Purpose is what a sign-in is started for.
type Purpose string

// The purposes of a sign-in, as sso_requests.purpose holds them.
const (
	// PurposeSignIn signs a person in to the organization.
	PurposeSignIn Purpose = "sign-in"
	// PurposeEnable is an organization admin's sign-in through a provider
	// that enables it and records the login as its admin login.
	PurposeEnable Purpose = "enable"
	// PurposeLink is one of the two sign-ins that link two members.
	PurposeLink Purpose = "link"
	// PurposeReauthenticate is a signed-in member's fresh sign-in through a
	// provider, which must resolve to the same member.
	PurposeReauthenticate Purpose = "reauthenticate"
)

// ParsePurpose reads a start's purpose; empty is PurposeSignIn.
func ParsePurpose(s string) (Purpose, error) {
	switch p := Purpose(s); p {
	case "":
		return PurposeSignIn, nil
	case PurposeSignIn, PurposeEnable, PurposeLink, PurposeReauthenticate:
		return p, nil
	}
	return "", fmt.Errorf("%w: no purpose %q", ErrNotOffered, s)
}

// Fresh reports whether the purpose asks the provider for a fresh
// authentication, with max_age=0 and prompt=login: linking and
// re-authentication prove the person is at the browser now.
func (p Purpose) Fresh() bool { return p == PurposeLink || p == PurposeReauthenticate }

// Request is a started sign-in, an sso_requests row. Verifier is sealed at
// rest; the state and the browser's cookie are stored as their SHA-256.
type Request struct {
	ID             kernel.ULID
	OrganizationID kernel.OrganizationID
	ProviderID     kernel.ULID
	Purpose        Purpose
	LinkID         kernel.ULID
	StateHash      []byte
	Nonce          string
	Verifier       string
	BrowserHash    []byte
	ReturnTo       string
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

// Endpoints are what an OpenID Provider's discovery document names, each
// an https URL; EndSession is empty when the provider advertises none.
type Endpoints struct {
	Issuer        string
	Authorization string
	Token         string
	EndSession    string
}

// Client is the relying party at a provider: its client ID and secret and
// the redirect URI the authorization request named.
type Client struct {
	ID, Secret, RedirectURI string
}

// LogoutToken is a verified back-channel logout token: its jti, its end,
// and the subject or session, or both, it ends.
type LogoutToken struct {
	ID      string
	Subject string
	SID     string
	Expiry  time.Time
}

// RelyingParty speaks OpenID Connect with a provider over HTTPS; the
// access domain's OIDC adapter is one, on the key set OIDC API keys use.
type RelyingParty interface {
	// Discover reads the discovery document at host, the issuer or its
	// discovery URL, whose issuer must be the issuer exactly.
	Discover(ctx context.Context, host string) (Endpoints, error)
	// Exchange redeems code at the token endpoint with the PKCE verifier
	// and answers the ID token; the access and refresh tokens are
	// discarded.
	Exchange(ctx context.Context, ep Endpoints, c Client, code, verifier string) (string, error)
	// VerifyIDToken checks raw's signature against the issuer's key set,
	// its issuer, its audience holding clientID, its algorithm and its
	// times, and answers its claims.
	VerifyIDToken(ctx context.Context, issuer, clientID, raw string) (map[string]any, error)
	// VerifyLogout checks a back-channel logout token as VerifyIDToken
	// checks an ID token, and the logout token's own rules.
	VerifyLogout(ctx context.Context, issuer, clientID, raw string) (LogoutToken, error)
}

var (
	// ErrToken refuses an ID token that fails a check; the error names it.
	ErrToken = errors.New("ID token refused")
	// ErrRequest refuses a callback that names no live request of the
	// browser: unknown, used, expired, or started by another browser.
	ErrRequest = errors.New("no such sign-in request")
	// ErrNotOffered refuses a start the provider does not offer: a
	// disabled provider's sign-in, a kind or purpose not signed in through
	// here.
	ErrNotOffered = errors.New("sign-in not offered")
	// ErrProvider reports a provider that cannot be reached or answers
	// what OpenID Connect does not allow, or answers an error itself.
	ErrProvider = errors.New("identity provider unusable")
	// ErrForbidden refuses an enable start without an organization admin's
	// session, and a re-authentication without a member's.
	ErrForbidden = errors.New("not allowed to start this sign-in")
	// ErrDeactivated refuses a sign-in that resolves to a deactivated
	// member.
	ErrDeactivated = errors.New("member deactivated")
	// ErrOtherMember refuses a re-authentication whose identity is not the
	// signed-in member's.
	ErrOtherMember = errors.New("another member's identity")
)

// CheckIDToken applies the checks go-oidc leaves to its caller to an ID
// token's claims for req, the request it answers, and clientID: the
// nonce must be the request's, sub must not be empty, a token for more
// than one audience must name the client as azp, and when the request
// asked for a fresh authentication, auth_time must be present and within
// Skew of now.
//
// ref: report 36 section 2
func CheckIDToken(claims map[string]any, req Request, clientID string, now time.Time) error {
	nonce, _ := claims["nonce"].(string)
	if subtle.ConstantTimeCompare([]byte(nonce), []byte(req.Nonce)) != 1 || req.Nonce == "" {
		return fmt.Errorf("%w: the nonce is not the request's", ErrToken)
	}
	if sub, _ := claims["sub"].(string); sub == "" {
		return fmt.Errorf("%w: sub is empty", ErrToken)
	}
	if aud := audiences(claims["aud"]); len(aud) > 1 {
		if azp, _ := claims["azp"].(string); azp != clientID {
			return fmt.Errorf("%w: %d audiences without azp naming the client", ErrToken, len(aud))
		}
	}
	if !req.Purpose.Fresh() {
		return nil
	}
	at, ok := seconds(claims["auth_time"])
	if !ok {
		return fmt.Errorf("%w: no auth_time, which max_age=0 requires", ErrToken)
	}
	if now.Sub(at) > Skew {
		return fmt.Errorf("%w: auth_time %s is older than max_age=0 allows", ErrToken, at.UTC().Format(time.RFC3339))
	}
	return nil
}

// audiences are aud as strings, from a single string or an array, as
// encoding/json decodes them.
func audiences(v any) []string {
	switch a := v.(type) {
	case string:
		return []string{a}
	case []any:
		out := make([]string, 0, len(a))
		for _, x := range a {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// seconds reads a NumericDate claim as encoding/json decodes it.
func seconds(v any) (time.Time, bool) {
	n, ok := v.(float64)
	return time.Unix(int64(n), 0), ok
}

// Identity is what a verified ID token says of the person, through the
// provider's claim mapping.
type Identity struct {
	// Subject is the stable identifier member_identities keys on.
	Subject string
	Login   string
	Name    string
	// Email is the address the token names, only when it says
	// email_verified is true; it is shown, never matched.
	Email string
	// Groups are the provider's groups, empty, never nil, when the token
	// names none.
	Groups []string
	// SID is the provider's session, which a back-channel logout names.
	SID string
}

// Identity reads an OIDC ID token's claims through the claim mapping: the
// subject from the mapped claim, sub by default, which must be a non-empty
// string; the login, name and e-mail from the first of their claims that
// is a non-empty string, the login falling back to the subject; the groups
// as strings from their claim, none when it is absent or replaced by
// _claim_names; and sid.
//
// ref: decision 0006 § "Human login"
func (s Settings) Identity(claims map[string]any) (Identity, error) {
	m := s.Claims(OIDC)
	first := func(t Target) string {
		for _, c := range m[t] {
			if v, ok := claims[c].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	out := Identity{Subject: first(TargetSubject)}
	if out.Subject == "" {
		return Identity{}, fmt.Errorf("%w: no subject in the claim %s", ErrToken, strings.Join(m[TargetSubject], ", "))
	}
	out.Login = first(TargetLogin)
	if out.Login == "" {
		out.Login = out.Subject
	}
	out.Name = first(TargetName)
	if out.Name == "" {
		out.Name = out.Login
	}
	if verified(claims["email_verified"]) {
		out.Email = first(TargetEmail)
	}
	out.Groups = groups(claims, m[TargetGroups])
	out.SID, _ = claims["sid"].(string)
	return out, nil
}

// verified reads email_verified, a boolean, or the string "true" that some
// providers send.
func verified(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return b == "true"
	}
	return false
}

// groups are the strings of the first of names that the claims hold. A
// claim _claim_names points elsewhere, as Entra ID does past 200 groups, is
// no groups.
func groups(claims map[string]any, names []string) []string {
	distributed, _ := claims["_claim_names"].(map[string]any)
	out := []string{}
	for _, name := range names {
		if _, elsewhere := distributed[name]; elsewhere {
			return out
		}
		switch v := claims[name].(type) {
		case string:
			return append(out, v)
		case []any:
			for _, g := range v {
				if s, ok := g.(string); ok && !slices.Contains(out, s) {
					out = append(out, s)
				}
			}
			return out
		}
	}
	return out
}

// CheckReturnTo is where a sign-in sends the browser when it ends: a path
// on the public URL, "/" when raw is empty. A URL with a scheme or host,
// or a path a browser reads as one (//host, /\host), is not on the public
// URL.
func CheckReturnTo(raw string) (string, error) {
	if raw == "" {
		return "/", nil
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil, u.Scheme != "", u.Host != "", u.User != nil,
		!strings.HasPrefix(raw, "/"), strings.HasPrefix(raw, "//"), strings.HasPrefix(raw, "/\\"),
		strings.ContainsAny(raw, "\r\n\t"):
		return "", fmt.Errorf("%w: return_to %q is not a path on the public URL", ErrNotOffered, raw)
	}
	return raw, nil
}

// Candidate is a member a sign-in may resolve to, with its status.
type Candidate struct {
	ID     kernel.MemberID
	Status member.Status
}

// Resolution is what a sign-in does with the member it resolves to.
type Resolution int

// The resolutions.
const (
	// ResolveExisting signs in the member the identity names.
	ResolveExisting Resolution = iota
	// ResolveActivate makes a pending member active, with the identity.
	ResolveActivate
	// ResolveCreate creates an active member with the identity, after
	// the seats check.
	ResolveCreate
)

// ResolveMember decides a sign-in's member: bound, the member the
// provider's subject names in member_identities, when there is one;
// otherwise pending, the pending members invited under the login. A
// deactivated member is refused; a pending one becomes active; an
// identity no member holds makes the one pending member of its login
// active, or a new member when there is none. Several pending members of
// one login are refused rather than one picked. Nothing is matched by
// e-mail.
//
// ref: decision 0006 § "Members and identity providers"
func ResolveMember(bound *Candidate, pending []Candidate) (Resolution, kernel.MemberID, error) {
	if bound != nil {
		switch bound.Status {
		case member.Deactivated:
			return 0, kernel.MemberID{}, ErrDeactivated
		case member.Pending:
			return ResolveActivate, bound.ID, nil
		}
		return ResolveExisting, bound.ID, nil
	}
	switch len(pending) {
	case 0:
		return ResolveCreate, kernel.MemberID{}, nil
	case 1:
		return ResolveActivate, pending[0].ID, nil
	}
	return 0, kernel.MemberID{}, fmt.Errorf("%w: %d pending members share the login", ErrNotOffered, len(pending))
}

// peek reads a JWT's iss and aud without verifying it, to choose the
// providers whose key set verifies it.
func peek(raw string) (string, []string, bool) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return "", nil, false
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", nil, false
	}
	var c map[string]any
	if json.Unmarshal(b, &c) != nil {
		return "", nil, false
	}
	iss, _ := c["iss"].(string)
	aud := audiences(c["aud"])
	return iss, aud, iss != "" && len(aud) > 0
}
