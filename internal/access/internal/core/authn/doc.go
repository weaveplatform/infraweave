// Package authn is the access domain's authentication: Exchange trades an
// API key for an API token at POST /auth/token, creating the session the
// token's jti names. A secret key exchanges its secret; an OIDC key
// exchanges a JWT of its issuer, which the OIDC port verifies and which
// Deps.IssuerAllowed must allow, its sub matched by the key's subject
// expression, so the token's sub is OIDCKeySubject's. Without a verifier
// every OIDC key is refused. A personal key's session acts as its owner
// in the key's account, with the owner's member, login, name and teams
// from APIKeys.KeyOwner, and is refused once the account no longer admits
// the owner as an active member. An operator key of Deps.Operators exchanges
// its secret, whose SHA-256 it compares in constant time, for an operator
// token: sub OperatorSubject's, aud Deps.OperatorAudience, and a session
// of actor kind operator in no organization or account. An organization
// key, whose secret has apikey.OrganizationPrefix, exchanges through
// Deps.OrganizationKeys for an organization token: sub
// OrganizationKeySubject's, and a session of actor kind organization in
// its organization and no account; it takes no seat, and its last use
// moves at most once a minute. Authenticate turns a bearer token into the
// principal a request to the API acts as, by the route's Reach: on
// ReachAccount a session with no account is refused; on ReachOrganization
// any session of an organization but an organization key's is admitted;
// on ReachOrganizationAdmin, the Organization tag's, only a member's,
// the break-glass admin's or an organization key's session is, so API
// key and run tokens are refused there.
// AuthenticateOperator does so for the operator API, admitting operator
// sessions alone, so neither API accepts the other's tokens, and
// AuthenticateWorker for the worker API, admitting worker session tokens
// alone as a principal of kind worker.
// AuthenticateGrant does so for an MCP surface: it admits an OAuth access
// token only when its session is an OAuth grant for that resource whose
// scopes include the token's, and the principal holds the token's scopes.
// An account session's principal is an account admin as Deps.AccountAdmin
// decides at each authentication, from the account's bindings; a session
// of no account keeps the Admin it stores. Every
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
// The worker protocol's tokens: WorkerToken and RunToken issue a worker
// session token and a run token, with audiences
// accessapi.WorkerAudience and accessapi.RunnerAudience and no session
// row, which VerifyWorker and VerifyRun read back as the worker or run
// they name and when they expire. InRunToken issues an in-run API token: an API token whose
// session has actor kind run and whose sub is the stack's slug and the
// run. Its principal is completed at each authentication from the
// accessapi.Runs that BindRuns binds, acting as the run's stack from the
// space the stack lives in, and is refused while the run is not live,
// before Runs is bound, and on ReachOrganization's routes.
//
// Authenticate and AuthenticateBrowser read sessions through the
// replica's session.Cache, which the assembly's watcher drops a session
// from on its revocation message.
//
// ref: decision 0006
//
// # Input and output
//
// None of its own: it reads and writes through the APIKeys, Sessions,
// OrganizationKeys, BreakGlass and Tenancy ports and session.Admission,
// reads runs through accessapi.Runs, verifies through the OIDC port, and
// signs through the token service it is given. Nothing is logged.
//
// # Dependencies
//
// It imports the access domain's api, apikey, breakglass, loopback,
// organization, port, session and token packages, internal/kernel and the
// standard library. The access assembly constructs it.
package authn
