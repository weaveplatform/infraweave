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
// turns a bearer token into the principal a request to the API acts as:
// on an account route a session with no account is refused, and on an
// organization route any session of an organization is admitted.
// AuthenticateOperator does so for the operator API, admitting operator
// sessions alone, so neither API accepts the other's tokens. Every
// refused credential is the one ErrUnauthenticated. Sessions, Revoke,
// RevokeOthers and Logout list and revoke sessions; Expire is
// authn.sessions.expire.
//
// Browser sessions: SignInBreakGlass checks an organization's break-glass
// login and password (a decoy hash stands in when there is no
// credential, so each refusal costs the same) and creates a WEB session
// of actor kind break_glass, an organization admin in no account, with
// Deps.Idle and Deps.MaxAge as its idle and absolute limits; each attempt
// on an organization that exists is on its trail as
// ActionBreakGlassSignIn. AuthenticateBrowser turns a cookie value into
// the principal, moving the idle end at most once a minute. CLIToken
// issues a member's CLI token from their browser session, sealed by
// loopback.Seal, whose INFRAWEAVECTL session names the browser session as
// its parent and ends with it. Accounts lists the accounts Deps.Admission
// admits a member to, and SwitchAccount replaces a member's session with
// one in another such account, in the same channel and with the same
// absolute end.
//
// Authenticate and AuthenticateBrowser read sessions through the
// replica's session.Cache, which the assembly's watcher drops a session
// from on its revocation message.
//
// # Input and output
//
// None of its own: it reads and writes through the APIKeys, Sessions and
// BreakGlass ports and session.Admission, verifies through the OIDC port,
// and signs through the token service it is given. Nothing is logged.
//
// # Dependencies
//
// It imports the access domain's apikey, breakglass, loopback,
// organization, port, session and token packages, internal/kernel and the
// standard library. The access assembly constructs it.
package authn
