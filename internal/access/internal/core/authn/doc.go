// Package authn is decision 0006's authentication: Exchange trades an
// API key for an API token at POST /auth/token, creating the session the
// token's jti names. A secret key exchanges its secret; an OIDC key
// exchanges a JWT of its issuer, which the OIDC port verifies and which
// Deps.IssuerAllowed must allow, its sub matched by the key's subject
// expression, so the token's sub is OIDCKeySubject's. Without a verifier
// every OIDC key is refused. An operator key of Deps.Operators exchanges
// its secret, whose SHA-256 it compares in constant time, for an operator
// token: sub OperatorSubject's, aud Deps.OperatorAudience, and a session
// of actor kind operator in no organization or account. Authenticate
// turns a bearer token into the principal a request to the API acts as,
// refusing a session with no account; AuthenticateOperator does so for
// the operator API, admitting operator sessions alone, so neither API
// accepts the other's tokens. Every refused credential is the one
// ErrUnauthenticated. Sessions, Revoke, RevokeOthers and Logout list
// and revoke an account's sessions; Expire is authn.sessions.expire.
//
// Authenticate reads sessions through the replica's session.Cache, which
// the assembly's watcher drops a session from on its revocation message.
//
// # Input and output
//
// None of its own: it reads and writes through the APIKeys and Sessions
// ports, verifies through the OIDC port, and signs through the token
// service it is given. Nothing is logged.
//
// # Dependencies
//
// It imports the access domain's apikey, port, session and token
// packages, internal/kernel and the standard library. The access
// assembly constructs it.
package authn
