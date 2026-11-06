// Package authn is decision 0006's authentication: Exchange trades a
// secret API key for an API token at POST /auth/token, creating the
// session the token's jti names, and Authenticate turns a bearer token
// into the principal a request acts as. Every refused credential is the
// one ErrUnauthenticated. Sessions, Revoke, RevokeOthers and Logout list
// and revoke an account's sessions; Expire is authn.sessions.expire.
//
// Authenticate reads sessions through the replica's session.Cache, which
// the assembly's watcher drops a session from on its revocation message.
//
// # Input and output
//
// None of its own: it reads and writes through the APIKeys and Sessions
// ports and the token service it is given. Nothing is logged.
//
// # Dependencies
//
// It imports the access domain's apikey, port, session and token
// packages, internal/kernel and the standard library. The access
// assembly constructs it.
package authn
