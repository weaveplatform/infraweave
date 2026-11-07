// Package http is the access domain's HTTP adapter. Server is its partial
// of the API's strict server, which internal/app embeds under the
// AccessAPI alias: POST /auth/token (apiKeyUser) exchanges a secret API
// key for an API token; viewer answers the caller; sessions,
// sessionDelete, sessionDeleteAll and logout list and revoke the caller's
// account's sessions, logout clearing a browser session's cookie;
// cliConfirmationToken seals a CLI token from a member's browser session
// to the CLI's key, answering 403 to any other credential and 422 naming
// pubKey to a key it cannot seal to; authAccounts and authSessionAccount
// list the accounts that admit the caller's member and switch to one,
// any other answering 404; apiKeys, apiKeyCreate, apiKey, apiKeyUpdate,
// apiKeyReset, apiKeyDelete and apiKeyEnableNonAdminManagers manage the
// account's API keys, where a key of another account answers 404, input
// the service refuses 422 validation-failed, and an API key or run 403
// forbidden while the account does not let API keys manage API keys; the
// Account Settings operations read and set the settings no other domain
// owns, where only a member flips accountToggleAPIKeyManagementFromNonHumans,
// any other caller answering 403, a refused security email or redirect
// host answers 422 validation-failed, confirming the deletion of an
// unmarked account or its organization's last answers 409 invalid-state,
// and an account gone since its token was issued answers 404; id, name, type and
// defaultPrivateWorkerPoolRunnerImage read the caller's account, name
// through Accounts. OperatorServer is its partial of the operator API's
// strict server, which internal/app embeds under the AccessOperatorAPI
// alias: organizations, organizationCreate, organization,
// organizationDelete, organizationBreakGlassSet and
// organizationBreakGlassDelete, naming an organization by slug, where a
// slug in use answers 409 already-exists, an organization that does not
// exist 404 and input the service refuses 422 validation-failed naming
// each field; no answer carries a password hash. Authenticator is
// accessapi.Authenticator: the middleware that turns a bearer token, or
// a browser session's cookie, into the request's principal, or answers
// 401 unauthenticated with WWW-Authenticate: Bearer; a cookie
// authenticates a write only with an Origin of the public URL.
// NewAuthenticator's admits API tokens and cookies and
// NewOperatorAuthenticator's operator tokens alone. Login serves the
// break-glass login's form and sign-in at accessapi.LoginPattern, which
// sets the session's __Host- cookie and answers 303 to the console.
//
// # Input and output
//
// HTTP requests and responses in the generated servers' types. Every
// refused credential answers one problem whose detail names no part.
// Nothing is logged.
//
// # Dependencies
//
// It imports the access domain's apikey, authn, breakglass, loopback,
// operator, organization, port, session and settings packages, accessapi,
// featuresapi for limit-reached, internal/kernel, chi, the generated servers of both APIs, and
// platform/httpkit's page, problem and ratelimit. Only the access domain's assembly imports
// it.
package http
