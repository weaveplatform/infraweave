package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// RequestTTL is how long an authorization request waits for the member's
// consent.
//
// ref: decision 0006 § "The OAuth authorization server for MCP"
const RequestTTL = 10 * time.Minute

// ClientRetention is how long a client goes unused before expiry deletes
// it.
const ClientRetention = 30 * 24 * time.Hour

// requestRetention is how long a request outlives its end before expiry
// deletes it: a code replayed meanwhile still finds the grant it issued
// and revokes it.
const requestRetention = 24 * time.Hour

// Registration's bounds, which keep a client that anyone may register
// small.
const (
	maxRedirectURIs = 10
	maxURILen       = 2000
	maxNameLen      = 200
)

// The grant and response types the server supports.
const (
	GrantAuthorizationCode = "authorization_code"
	GrantRefreshToken      = "refresh_token"
	ResponseCode           = "code"
	// codeInvalidTarget is the refusal of a resource in RFC 8707.
	codeInvalidTarget = "invalid_target"
)

var (
	// ErrReused reports a spent code or refresh token presented again,
	// whose grant the store revoked.
	ErrReused = errors.New("reused")
	// ErrNotMember reports a consent by a caller who is not a member.
	ErrNotMember = errors.New("not a member")
	// ErrNotAdmitted reports a consent for an account that does not
	// admit the member.
	ErrNotAdmitted = errors.New("not admitted")
	// ErrRedirectHost reports a consent for an account whose
	// oauthRedirectAllowedHosts do not name the request's redirect host.
	ErrRedirectHost = errors.New("redirect host not allowed")
)

// Store keeps clients, requests and grants.
type Store interface {
	CreateClient(ctx context.Context, c Client) error
	// Client reads the client id; port.ErrNotFound when there is none.
	Client(ctx context.Context, id kernel.ULID) (Client, error)
	// CreateRequest records r and marks its client used.
	CreateRequest(ctx context.Context, r Request) error
	// Request reads the request id with its client.
	Request(ctx context.Context, id kernel.ULID) (Request, error)
	// Decide records the decision on the request d.ID, its approval
	// fields and code hash, or none for a denial, unless it is decided
	// already or ended before now, which is port.ErrNotFound.
	Decide(ctx context.Context, d Request, now time.Time) error
	// RedirectHosts are the account's oauthRedirectAllowedHosts.
	RedirectHosts(ctx context.Context, acct kernel.AccountID) ([]string, error)
	// Redeem spends the code whose SHA-256 is hash and records the grant
	// session grant builds from its request and the owner its account
	// admits, in one transaction that holds the member's admission
	// against removal and deactivation. A code with no request, or
	// whose member the account no longer admits, is port.ErrNotFound;
	// a spent code revokes the grant it issued and is ErrReused. An
	// error from grant leaves the code unspent.
	Redeem(
		ctx context.Context,
		hash []byte,
		grant func(Request, Owner) (session.Session, error),
	) (session.Session, error)
	// Refresh rotates the grant whose current refresh token hashes to
	// hash: rotate answers the grant's next state, which the store
	// writes, keeping hash as spent, in one transaction that holds the
	// member's admission as Redeem does. A hash of no grant is
	// port.ErrNotFound; a spent one revokes its grant, which it answers,
	// and is ErrReused; a grant whose member the account no longer
	// admits is revoked and port.ErrNotFound. An error from rotate
	// changes nothing.
	Refresh(
		ctx context.Context,
		hash []byte,
		rotate func(session.Session, Owner) (session.Session, error),
	) (session.Session, error)
	// ExpireOAuth deletes the requests that ended before requests and
	// the clients last used before clients.
	ExpireOAuth(ctx context.Context, requests, clients time.Time) error
}

// Issuer issues access tokens; *token.Service is one.
type Issuer interface {
	Issue(ctx context.Context, r token.Request) (string, token.Claims, error)
}

// Deps are what the service needs.
type Deps struct {
	Store     Store
	Tokens    Issuer
	Admission session.Admission
	// Drop forgets a revoked grant's session in this replica's cache.
	Drop func(kernel.ULID)
	// PublicURL is server.public_url: the issuer, the endpoints' base and
	// the MCP resources'.
	PublicURL string
	// AccessTTL, RefreshTTL and CodeTTL are auth.tokens' oauth_access,
	// oauth_refresh and oauth_code lifetimes. A refresh token lives
	// RefreshTTL from its grant's last use.
	AccessTTL, RefreshTTL, CodeTTL time.Duration
	Now                            func() time.Time
}

// Service is the authorization server for MCP clients.
//
// ref: decision 0006 § "The OAuth authorization server for MCP"
type Service struct{ d Deps }

// NewService returns the service on d.
func NewService(d Deps) *Service { return &Service{d: d} }

// Metadata is the RFC 8414 document.
//
//nolint:tagliatelle // the names RFC 8414 sets
type Metadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	ScopesSupported                   []string `json:"scopes_supported"`
}

// Metadata is the server's RFC 8414 document: the issuer, its three
// endpoints, and only the response type, grant types, PKCE method, client
// authentication and scopes it supports.
//
// ref: decision 0006 § "The OAuth authorization server for MCP"
func (s *Service) Metadata() Metadata {
	base := strings.TrimSuffix(s.d.PublicURL, "/")
	return Metadata{
		Issuer:                            base,
		AuthorizationEndpoint:             base + AuthorizePath,
		TokenEndpoint:                     base + TokenPath,
		RegistrationEndpoint:              base + RegisterPath,
		ResponseTypesSupported:            []string{ResponseCode},
		GrantTypesSupported:               []string{GrantAuthorizationCode, GrantRefreshToken},
		CodeChallengeMethodsSupported:     []string{MethodS256},
		TokenEndpointAuthMethodsSupported: []string{"none"},
		ScopesSupported:                   []string{ScopeRead, ScopeWrite},
	}
}

// Registration is an RFC 7591 registration request's fields the server
// reads.
type Registration struct {
	RedirectURIs            []string
	ClientName              string
	TokenEndpointAuthMethod string
	GrantTypes              []string
	ResponseTypes           []string
}

// Register registers a public client. Each redirect URI must pass
// CheckRedirectURI; a client that asks for a secret, or for a grant or
// response type the server does not support, is invalid_client_metadata.
func (s *Service) Register(ctx context.Context, r Registration) (Client, error) {
	switch {
	case len(r.RedirectURIs) == 0:
		return Client{}, fail(CodeInvalidRedirectURI, "redirect_uris is required")
	case len(r.RedirectURIs) > maxRedirectURIs:
		return Client{}, fail(CodeInvalidRedirectURI,
			"redirect_uris names %d URIs, more than %d", len(r.RedirectURIs), maxRedirectURIs)
	case len(r.ClientName) > maxNameLen:
		return Client{}, fail(CodeInvalidClientMetadata,
			"client_name is longer than %d bytes", maxNameLen)
	case r.TokenEndpointAuthMethod != "" && r.TokenEndpointAuthMethod != "none":
		return Client{}, fail(CodeInvalidClientMetadata,
			"token_endpoint_auth_method must be none: clients are public")
	}
	for _, g := range r.GrantTypes {
		if g != GrantAuthorizationCode && g != GrantRefreshToken {
			return Client{}, fail(
				CodeInvalidClientMetadata,
				"the grant type %q is not supported",
				g,
			)
		}
	}
	for _, t := range r.ResponseTypes {
		if t != ResponseCode {
			return Client{}, fail(CodeInvalidClientMetadata,
				"the response type %q is not supported", t)
		}
	}
	for _, u := range r.RedirectURIs {
		if len(u) > maxURILen {
			return Client{}, fail(CodeInvalidRedirectURI,
				"a redirect URI is longer than %d bytes", maxURILen)
		}
		if _, err := CheckRedirectURI(u); err != nil {
			return Client{}, err
		}
	}
	c := Client{
		ID:           kernel.NewULID(),
		Name:         r.ClientName,
		RedirectURIs: r.RedirectURIs,
		CreatedAt:    s.d.Now().UTC().Truncate(time.Second),
	}
	if err := s.d.Store.CreateClient(ctx, c); err != nil {
		return Client{}, err //nolint:wrapcheck // the store names the write
	}
	return c, nil
}

// AuthorizeParams are the authorization endpoint's parameters.
type AuthorizeParams struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	Scope               string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	Resource            string
}

// Authorize stores a valid authorization request for RequestTTL and
// answers the console's consent page for it. Until the client and its
// redirect URI are known good it answers an *Error, which the endpoint
// shows rather than redirects; after, a refusal is a redirect to the
// client carrying the error, as RFC 6749 section 4.1.2.1 asks. A
// redirect URI may be left out when the client registered exactly one.
func (s *Service) Authorize(ctx context.Context, a AuthorizeParams) (string, error) {
	c, err := s.client(ctx, a.ClientID)
	if err != nil {
		return "", err
	}
	redirect := a.RedirectURI
	switch {
	case redirect == "" && len(c.RedirectURIs) == 1:
		redirect = c.RedirectURIs[0]
	case !slices.ContainsFunc(c.RedirectURIs, func(r string) bool {
		return SameRedirect(r, redirect)
	}):
		return "", fail(CodeInvalidRequest, "redirect_uri %q is not one the client registered",
			redirect)
	}
	refuse := func(e error) (string, error) {
		return Redirect(redirect, a.State, e), nil
	}
	if a.ResponseType != ResponseCode {
		return refuse(fail(CodeUnsupportedResponseType, "response_type must be code"))
	}
	if err := CheckChallenge(a.CodeChallenge, a.CodeChallengeMethod); err != nil {
		return refuse(err)
	}
	scopes, err := Scopes(a.Scope)
	if err != nil {
		return refuse(err)
	}
	switch {
	case a.Resource == "":
		return refuse(fail(CodeInvalidRequest, "resource is required"))
	case !Resource(s.d.PublicURL, a.Resource):
		return refuse(fail(codeInvalidTarget, "the resource %q is not an MCP server of %s",
			a.Resource, s.d.PublicURL))
	}
	now := s.d.Now().UTC()
	r := Request{
		ID:          kernel.NewULID(),
		Client:      c,
		RedirectURI: redirect,
		Resource:    a.Resource,
		Scopes:      scopes,
		State:       a.State,
		Challenge:   a.CodeChallenge,
		CreatedAt:   now,
		ExpiresAt:   now.Add(RequestTTL),
	}
	if err := s.d.Store.CreateRequest(ctx, r); err != nil {
		return "", err //nolint:wrapcheck // the store names the write
	}
	return strings.TrimSuffix(s.d.PublicURL, "/") + ConsentPath + r.ID.String(), nil
}

// client reads the client a client_id names; one that names none is
// invalid_client.
func (s *Service) client(ctx context.Context, raw string) (Client, error) {
	id, err := kernel.ParseULID(raw)
	if err != nil {
		return Client{}, fail(CodeInvalidClient, "client_id %q names no client", raw)
	}
	c, err := s.d.Store.Client(ctx, id)
	if errors.Is(err, port.ErrNotFound) {
		return Client{}, fail(CodeInvalidClient, "client_id %q names no client", raw)
	}
	return c, err //nolint:wrapcheck // the store names the read
}

// Redirect is uri with the parameters of an authorization answer: e's
// error and description, or none when e is nil, and state when the
// request carried one.
func Redirect(uri, state string, e error, params ...string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return uri
	}
	q := u.Query()
	var oe *Error
	if errors.As(e, &oe) {
		q.Set("error", oe.Code)
		q.Set("error_description", oe.Description)
	}
	for i := 0; i+1 < len(params); i += 2 {
		q.Set(params[i], params[i+1])
	}
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// ConsentRequest is the request id as p's consent page shows it: one
// that is decided or past its end is port.ErrNotFound, and a caller who
// is not a member is ErrNotMember.
func (s *Service) ConsentRequest(
	ctx context.Context,
	p kernel.Principal,
	id kernel.ULID,
) (Request, error) {
	if p.Kind != kernel.PrincipalUser || p.MemberID.IsZero() {
		return Request{}, ErrNotMember
	}
	r, err := s.d.Store.Request(ctx, id)
	if err != nil {
		return Request{}, err //nolint:wrapcheck // the store names the read
	}
	if r.Decided || !s.d.Now().Before(r.ExpiresAt) {
		return Request{}, fmt.Errorf("%w: authorization request %s", port.ErrNotFound, id)
	}
	return r, nil
}

// Approve grants the request id to p's member in the account acct, which
// must admit them, and answers the client's redirect URI with a code that
// lives CodeTTL. A redirect URI that is not loopback must match acct's
// oauthRedirectAllowedHosts, else ErrRedirectHost.
func (s *Service) Approve(
	ctx context.Context,
	p kernel.Principal,
	id kernel.ULID,
	acct kernel.AccountID,
) (string, error) {
	r, err := s.ConsentRequest(ctx, p, id)
	if err != nil {
		return "", err
	}
	_, ok, err := s.d.Admission.Admits(ctx, p.OrganizationID, p.MemberID, acct)
	if err != nil {
		return "", err //nolint:wrapcheck // the store names the read
	}
	if !ok {
		return "", ErrNotAdmitted
	}
	hosts, err := s.d.Store.RedirectHosts(ctx, acct)
	if err != nil {
		return "", err //nolint:wrapcheck // the store names the read
	}
	if !AllowedRedirect(r.RedirectURI, hosts) {
		return "", ErrRedirectHost
	}
	now := s.d.Now()
	code, hash := session.NewSecret()
	if err := s.d.Store.Decide(ctx, Request{
		ID:             id,
		OrganizationID: p.OrganizationID,
		AccountID:      acct,
		MemberID:       p.MemberID,
		CodeHash:       hash,
		CodeExpiresAt:  now.Add(s.d.CodeTTL),
	}, now); err != nil {
		return "", err //nolint:wrapcheck // the store names the write
	}
	return Redirect(r.RedirectURI, r.State, nil, "code", code), nil
}

// Deny refuses the request id for p and answers the client's redirect URI
// with access_denied.
func (s *Service) Deny(ctx context.Context, p kernel.Principal, id kernel.ULID) (string, error) {
	r, err := s.ConsentRequest(ctx, p, id)
	if err != nil {
		return "", err
	}
	if err := s.d.Store.Decide(ctx, Request{ID: id}, s.d.Now()); err != nil {
		return "", err //nolint:wrapcheck // the store names the write
	}
	return Redirect(r.RedirectURI, r.State,
		fail(CodeAccessDenied, "the member denied the request")), nil
}

// TokenParams are the token endpoint's parameters.
type TokenParams struct {
	GrantType    string
	ClientID     string
	Code         string
	RedirectURI  string
	CodeVerifier string
	RefreshToken string
	Scope        string
	Resource     string
}

// Tokens are a token endpoint's answer (RFC 6749 section 5.1).
//
//nolint:tagliatelle // the names RFC 6749 sets
type Tokens struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// Token is the token endpoint: it redeems a code or a refresh token for
// an access token and the grant's next refresh token, recording ip as a
// new grant's address. Every refusal is an *Error.
func (s *Service) Token(ctx context.Context, t TokenParams, ip string) (Tokens, error) {
	switch t.GrantType {
	case GrantAuthorizationCode:
		return s.redeem(ctx, t, ip)
	case GrantRefreshToken:
		return s.refresh(ctx, t)
	case "":
		return Tokens{}, fail(CodeInvalidRequest, "grant_type is required")
	}
	return Tokens{}, fail(CodeUnsupportedGrantType, "the grant type %q is not supported",
		t.GrantType)
}

// invalidGrant is every refused code or refresh token, which says no more
// than RFC 6749 asks.
var invalidGrant = fail(CodeInvalidGrant, "the grant is invalid, expired or revoked")

// redeem exchanges a code: of the client, for the request's redirect URI
// when the client names one, before its end, with the verifier of the
// request's challenge, and for the request's resource when the client
// names one. The new grant is a session of the request's member in its
// account, channel UNKNOWN, with the request's scopes and resource.
func (s *Service) redeem(ctx context.Context, t TokenParams, ip string) (Tokens, error) {
	c, err := s.client(ctx, t.ClientID)
	if err != nil {
		return Tokens{}, err
	}
	switch {
	case t.Code == "":
		return Tokens{}, fail(CodeInvalidRequest, "code is required")
	case t.CodeVerifier == "":
		return Tokens{}, fail(CodeInvalidRequest, "code_verifier is required")
	}
	var out Tokens
	ss, err := s.d.Store.Redeem(ctx, session.HashSecret(t.Code),
		func(r Request, o Owner) (session.Session, error) {
			now := s.d.Now()
			switch {
			case r.Client.ID != c.ID || !now.Before(r.CodeExpiresAt) ||
				t.RedirectURI != "" && t.RedirectURI != r.RedirectURI ||
				!VerifyPKCE(r.Challenge, t.CodeVerifier):
				return session.Session{}, invalidGrant
			case t.Resource != "" && t.Resource != r.Resource:
				return session.Session{}, fail(codeInvalidTarget,
					"resource is not the one the request named")
			}
			refresh, hash := session.NewSecret()
			ss := session.Session{
				ID:             kernel.NewULID(),
				OrganizationID: r.OrganizationID,
				AccountID:      r.AccountID,
				Channel:        session.ChannelUnknown,
				ActorKind:      kernel.PrincipalUser,
				ActorID:        r.MemberID.ULID,
				MemberID:       r.MemberID,
				Login:          o.Login,
				Name:           o.Name,
				Teams:          o.Teams,
				Scopes:         r.Scopes,
				IPAddress:      ip,
				CreatedAt:      now,
				ExpiresAt:      now.Add(s.d.RefreshTTL),
				SecretHash:     hash,
				ClientID:       c.ID.String(),
				Resource:       r.Resource,
			}
			var err error
			out, err = s.issue(ctx, ss, r.Scopes, refresh)
			return ss, err
		})
	if err != nil {
		return Tokens{}, s.refused(ss, err)
	}
	return out, nil
}

// refresh rotates a grant's refresh token: of the client, for the grant's
// resource when the client names one, and for scopes within the grant's
// when it names some, which narrow this access token alone. The grant
// lives RefreshTTL from now, and acts as its member as the account
// admits them now.
func (s *Service) refresh(ctx context.Context, t TokenParams) (Tokens, error) {
	if t.RefreshToken == "" {
		return Tokens{}, fail(CodeInvalidRequest, "refresh_token is required")
	}
	var out Tokens
	ss, err := s.d.Store.Refresh(ctx, session.HashSecret(t.RefreshToken),
		func(ss session.Session, o Owner) (session.Session, error) {
			now := s.d.Now()
			if ss.ClientID != t.ClientID || !ss.Live(now) {
				return session.Session{}, invalidGrant
			}
			if t.Resource != "" && t.Resource != ss.Resource {
				return session.Session{}, fail(codeInvalidTarget,
					"resource is not the one the grant names")
			}
			scopes := ss.Scopes
			if t.Scope != "" {
				var err error
				if scopes, err = Scopes(t.Scope); err != nil {
					return session.Session{}, err
				}
				if !Within(scopes, ss.Scopes) {
					return session.Session{}, fail(CodeInvalidScope,
						"the scope reaches beyond the grant's")
				}
			}
			var refresh string
			refresh, ss.SecretHash = session.NewSecret()
			ss.ExpiresAt = now.Add(s.d.RefreshTTL)
			ss.Login, ss.Name, ss.Teams = o.Login, o.Name, o.Teams
			var err error
			out, err = s.issue(ctx, ss, scopes, refresh)
			return ss, err
		})
	if err != nil {
		return Tokens{}, s.refused(ss, err)
	}
	return out, nil
}

// issue signs the grant ss's access token for scopes, for AccessTTL and
// never past the grant's end, and answers it with refresh.
func (s *Service) issue(
	ctx context.Context,
	ss session.Session,
	scopes []string,
	refresh string,
) (Tokens, error) {
	scope := strings.Join(scopes, " ")
	ttl := min(s.d.AccessTTL, ss.ExpiresAt.Sub(s.d.Now())).Truncate(time.Second)
	raw, _, err := s.d.Tokens.Issue(ctx, token.Request{
		Kind:     token.KindOAuth,
		Subject:  ss.Login,
		Session:  ss.ID,
		Audience: ss.Resource,
		TTL:      ttl,
		Scope:    scope,
	})
	if err != nil {
		return Tokens{}, fmt.Errorf("issue an access token: %w", err)
	}
	return Tokens{
		AccessToken:  raw,
		TokenType:    "Bearer",
		ExpiresIn:    int64(ttl / time.Second),
		RefreshToken: refresh,
		Scope:        scope,
	}, nil
}

// refused maps a redemption's or rotation's failure to its answer: a
// code or token the store does not know or revoked the grant of, or one
// spent, is invalid_grant, and a grant the store revoked leaves this
// replica's cache at once.
func (s *Service) refused(ss session.Session, err error) error {
	if !errors.Is(err, ErrReused) && !errors.Is(err, port.ErrNotFound) {
		return err
	}
	if !ss.ID.IsZero() {
		s.d.Drop(ss.ID)
	}
	return invalidGrant
}

// Expire is authn.sessions.expire's share of OAuth: it deletes the
// requests that ended requestRetention ago and the clients unused for
// ClientRetention.
func (s *Service) Expire(ctx context.Context) error {
	now := s.d.Now()
	//nolint:wrapcheck // the store names the write
	return s.d.Store.ExpireOAuth(ctx, now.Add(-requestRetention), now.Add(-ClientRetention))
}
