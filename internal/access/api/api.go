package api

import "net/http"

// Authenticator authenticates the API's requests (decision 0006).
type Authenticator interface {
	// Middleware authenticates every request public does not exempt. It
	// puts the principal the request's bearer token acts as in the
	// request's context, where kernel.PrincipalFrom reads it, or answers
	// 401 unauthenticated with WWW-Authenticate: Bearer. An exempt
	// request passes with no principal, whatever it carries.
	Middleware(public func(*http.Request) bool) func(http.Handler) http.Handler
}
