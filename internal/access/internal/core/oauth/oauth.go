package oauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// The scopes of decision 0006. ScopeWrite implies ScopeRead.
const (
	ScopeRead  = "mcp:read"
	ScopeWrite = "mcp:write"
)

// MethodS256 is the one PKCE method the server accepts.
const MethodS256 = "S256"

// The paths of the server's endpoints and of the console's consent page,
// under the public URL.
const (
	MetadataPath  = accessapi.OAuthMetadataPattern
	RegisterPath  = accessapi.OAuthRegisterPattern
	AuthorizePath = accessapi.OAuthAuthorizePattern
	TokenPath     = accessapi.OAuthTokenPattern
	// ConsentPath prefixes the console's consent page, /consent/{request
	// ID} (decision 0023).
	ConsentPath = "/consent/"
)

// The MCP surfaces a grant may name as its resource (decisions 0019 and
// 0018), under the public URL: /mcp, /flows/mcp and each flow's server
// below it.
const (
	mcpPath      = "/mcp"
	flowsMCPPath = "/flows/mcp"
)

// The OAuth error codes the server answers (RFC 6749, RFC 7591).
const (
	CodeInvalidRequest          = "invalid_request"
	CodeInvalidClient           = "invalid_client"
	CodeInvalidGrant            = "invalid_grant"
	CodeInvalidScope            = "invalid_scope"
	CodeUnauthorizedClient      = "unauthorized_client"
	CodeUnsupportedGrantType    = "unsupported_grant_type"
	CodeUnsupportedResponseType = "unsupported_response_type"
	CodeAccessDenied            = "access_denied"
	CodeInvalidRedirectURI      = "invalid_redirect_uri"
	CodeInvalidClientMetadata   = "invalid_client_metadata"
)

// Error is an answer of the OAuth protocol: an error code, a description
// for the developer, and for the authorization endpoint whether it may be
// sent to the client's redirect URI.
type Error struct {
	Code        string
	Description string
}

func (e *Error) Error() string { return e.Code + ": " + e.Description }

func fail(code, format string, a ...any) *Error {
	return &Error{Code: code, Description: fmt.Sprintf(format, a...)}
}

// Client is a registered public client (RFC 7591).
type Client struct {
	ID           kernel.ULID
	Name         string
	RedirectURIs []string
	CreatedAt    time.Time
}

// Request is an authorization request the server keeps for the consent
// page, and once approved, the code that redeems it.
type Request struct {
	ID          kernel.ULID
	Client      Client
	RedirectURI string
	Resource    string
	Scopes      []string
	State       string
	Challenge   string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	// Decided is set once the member approved or denied the request,
	// which may happen once.
	Decided bool
	// The approval: the member, in which account, and the SHA-256 of the
	// code it issued with the code's end.
	OrganizationID kernel.OrganizationID
	AccountID      kernel.AccountID
	MemberID       kernel.MemberID
	CodeHash       []byte
	CodeExpiresAt  time.Time
}

// Owner is the member a grant acts as, read from their admission to the
// grant's account: their login, name and the groups their latest sign-in
// reported.
type Owner struct {
	Login string
	Name  string
	Teams []string
}

// Scopes reads a scope parameter: space-separated scopes, each one the
// server knows, where mcp:write brings mcp:read with it. An empty
// parameter is mcp:read. The answer is sorted.
func Scopes(param string) ([]string, error) {
	out := []string{ScopeRead}
	for s := range strings.FieldsSeq(param) {
		switch s {
		case ScopeRead:
		case ScopeWrite:
			if !slices.Contains(out, ScopeWrite) {
				out = append(out, ScopeWrite)
			}
		default:
			return nil, fail(CodeInvalidScope, "the scope %q is not mcp:read or mcp:write", s)
		}
	}
	slices.Sort(out)
	return out, nil
}

// Within reports whether every scope of want is among have.
func Within(want, have []string) bool {
	for _, s := range want {
		if !slices.Contains(have, s) {
			return false
		}
	}
	return true
}

// loopbackHosts are RFC 8252's loopback redirect hosts.
var loopbackHosts = []string{"127.0.0.1", "::1", "localhost"}

// Loopback reports whether u is a loopback redirect URI: http on
// 127.0.0.1, [::1] or localhost, on any port.
func Loopback(u *url.URL) bool {
	return u.Scheme == "http" && slices.Contains(loopbackHosts, strings.ToLower(u.Hostname()))
}

// CheckRedirectURI is registration's rule for one redirect URI: an
// absolute loopback or https URI, with no user, no fragment and, for
// https, a host. Anything else is invalid_redirect_uri naming it.
func CheckRedirectURI(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	bad := func(why string) error {
		return fail(CodeInvalidRedirectURI, "the redirect URI %q %s", raw, why)
	}
	switch {
	case err != nil || !u.IsAbs():
		return nil, bad("is not an absolute URI")
	case u.User != nil || u.Fragment != "" || strings.Contains(raw, "#"):
		return nil, bad("carries a user or a fragment")
	case Loopback(u):
		return u, nil
	case u.Scheme != "https" || u.Hostname() == "":
		return nil, bad("is neither loopback http nor https")
	}
	return u, nil
}

// SameRedirect reports whether given is registered, the redirect URI an
// authorization request names: equal to it, or for a loopback URI equal
// but for the port, which RFC 8252 lets the client choose per request.
func SameRedirect(registered, given string) bool {
	if registered == given {
		return true
	}
	r, err1 := url.Parse(registered)
	g, err2 := url.Parse(given)
	if err1 != nil || err2 != nil || !Loopback(r) || !Loopback(g) {
		return false
	}
	return strings.EqualFold(r.Hostname(), g.Hostname()) && r.Path == g.Path &&
		r.RawQuery == g.RawQuery && r.Fragment == "" && g.Fragment == ""
}

// AllowedRedirect is consent's rule for a redirect URI in an account
// whose oauthRedirectAllowedHosts are hosts: loopback, or https to a host
// an entry names, a bare host by equality and a *. entry by any subdomain
// of the host after it. Hosts compare without case.
func AllowedRedirect(raw string, hosts []string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if Loopback(u) {
		return true
	}
	if u.Scheme != "https" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	for _, e := range hosts {
		e = strings.ToLower(e)
		if sub, ok := strings.CutPrefix(e, "*."); ok {
			if strings.HasSuffix(h, "."+sub) {
				return true
			}
		} else if h == e {
			return true
		}
	}
	return false
}

// RedirectHost is the host the consent page names for a redirect URI,
// without its port.
func RedirectHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// Resource reports whether raw names an MCP surface of the server at
// public: public's /mcp, its /flows/mcp, or one flow's server one segment
// below it.
func Resource(public, raw string) bool {
	rest, ok := strings.CutPrefix(raw, strings.TrimSuffix(public, "/"))
	if !ok {
		return false
	}
	if rest == mcpPath || rest == flowsMCPPath {
		return true
	}
	flow, ok := strings.CutPrefix(rest, flowsMCPPath+"/")
	return ok && flow != "" && !strings.ContainsAny(flow, "/?#")
}

// challengeLen is the length of an S256 challenge: a SHA-256 in unpadded
// base64url.
const challengeLen = 43

// CheckChallenge is the authorization endpoint's PKCE rule (RFC 7636):
// a challenge, by S256 only. A missing method means plain, which is
// refused as plain is.
func CheckChallenge(challenge, method string) error {
	switch {
	case challenge == "":
		return fail(CodeInvalidRequest, "code_challenge is required")
	case method != MethodS256:
		return fail(CodeInvalidRequest, "code_challenge_method must be S256, not %q", method)
	case len(challenge) != challengeLen:
		return fail(CodeInvalidRequest, "code_challenge is not a base64url SHA-256")
	}
	if _, err := base64.RawURLEncoding.DecodeString(challenge); err != nil {
		return fail(CodeInvalidRequest, "code_challenge is not a base64url SHA-256")
	}
	return nil
}

// The verifier's length bounds (RFC 7636 section 4.1).
const (
	minVerifier = 43
	maxVerifier = 128
)

// VerifyPKCE reports whether verifier is the one challenge was made from
// by S256: 43 to 128 unreserved characters whose SHA-256, in unpadded
// base64url, is the challenge. It compares in constant time.
func VerifyPKCE(challenge, verifier string) bool {
	if len(verifier) < minVerifier || len(verifier) > maxVerifier {
		return false
	}
	for _, c := range verifier {
		if !unreserved(c) {
			return false
		}
	}
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(want), []byte(challenge)) == 1
}

// unreserved reports whether c is an RFC 3986 unreserved character.
func unreserved(c rune) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
		strings.ContainsRune("-._~", c)
}

// Challenge is the S256 challenge of verifier, as a client computes it.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
