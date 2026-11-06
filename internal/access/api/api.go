package api

import "net/http"

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
