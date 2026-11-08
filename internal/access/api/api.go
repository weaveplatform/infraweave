package api

import (
	"context"
	"net/http"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Access is what a route admits (decision 0006 § "The principal").
type Access int

const (
	// Account routes admit a principal that works in an account.
	Account Access = iota
	// Organization routes also admit a principal of an organization that
	// works in no account: the break-glass admin, or a member no account
	// admits.
	Organization
	// Public routes take no credential.
	Public
	// OrganizationAdmin routes, the Organization tag's, admit the
	// principals of an organization that may hold the organization role,
	// members and the break-glass admin, and organization tokens, which
	// every other route refuses. An API key's or a run's token works in an
	// account, and is refused.
	OrganizationAdmin
)

// Authenticator authenticates the API's requests (decision 0006).
type Authenticator interface {
	// Middleware authenticates every request access does not make public.
	// It puts the principal the request's bearer token or browser session
	// acts as in the request's context, where kernel.PrincipalFrom reads
	// it, or answers 401 unauthenticated with WWW-Authenticate: Bearer. A
	// public request passes with no principal, whatever it carries.
	Middleware(access func(*http.Request) Access) func(http.Handler) http.Handler
}

// LoginPattern is the route of an organization's break-glass login
// (decision 0006 § "The break-glass admin"): GET answers its form, and a
// POST of the form signs in. It is outside the API's base URL, a page
// for a browser rather than an operation of the API.
const LoginPattern = "/admin-login/{organization}"

// The routes of the authorization server for MCP clients (decision 0006 §
// "The OAuth authorization server for MCP"), outside the API's base URL
// as the OAuth protocol places them: RFC 8414 metadata, RFC 7591
// registration, and the authorization and token endpoints. None takes a
// credential.
const (
	OAuthMetadataPattern  = "/.well-known/oauth-authorization-server"
	OAuthRegisterPattern  = "/oauth/register"
	OAuthAuthorizePattern = "/oauth/authorize"
	OAuthTokenPattern     = "/oauth/token" //nolint:gosec // a route, not a credential
)

// SpaceGuard is a domain's say in changes to an account's space tree
// (decision 0007 § "Spaces"): a domain whose rows name spaces registers
// one, and the tree refuses a delete or a move it objects to. Both
// methods run inside the change's transaction, which the context
// carries, after the tree is locked, so what they read cannot change
// before the change commits.
type SpaceGuard interface {
	// SpaceInUse reports whether a row of the domain names space, which
	// refuses its deletion.
	SpaceInUse(ctx context.Context, acct kernel.AccountID, space kernel.ULID) (bool, error)
	// LostOnMove lists each stack that would no longer reach an entity
	// it reaches today if m were made, which refuses it.
	LostOnMove(ctx context.Context, m SpaceMove) ([]LostReach, error)
}

// SpaceMove is a proposed move of Space, with every space below it, to a
// new parent. From and To are the moved space's ancestors before and
// after the move, its parent first and the top space last.
type SpaceMove struct {
	Account  kernel.AccountID
	Space    kernel.ULID
	Subtree  []kernel.ULID
	From, To []kernel.ULID
}

// LostReach is a stack that a tree change would cut off from an entity:
// a worker pool, a cloud integration, a context or a policy, by its kind
// and name.
type LostReach struct {
	Stack  string
	Entity string
}
