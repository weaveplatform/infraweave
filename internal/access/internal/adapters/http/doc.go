// Package http is the access domain's HTTP adapter. Server is its partial
// of the API's strict server, which internal/app embeds under the
// AccessAPI alias: POST /auth/token (apiKeyUser) exchanges a secret API
// key for an API token; viewer answers the caller; sessions,
// sessionDelete, sessionDeleteAll and logout list and revoke the caller's
// account's sessions; apiKeys, apiKeyCreate, apiKey, apiKeyUpdate,
// apiKeyReset, apiKeyDelete and apiKeyEnableNonAdminManagers manage the
// account's API keys, where a key of another account answers 404 and
// input the service refuses 422 validation-failed; id, name, type and
// defaultPrivateWorkerPoolRunnerImage read the caller's account, name
// through Accounts. OperatorServer is its partial of the operator API's
// strict server, which internal/app embeds under the AccessOperatorAPI
// alias: organizations, organizationCreate, organization,
// organizationDelete, organizationBreakGlassSet and
// organizationBreakGlassDelete, naming an organization by slug, where a
// slug in use answers 409 already-exists, an organization that does not
// exist 404 and input the service refuses 422 validation-failed naming
// each field; no answer carries a password hash. Authenticator is
// accessapi.Authenticator: the middleware that turns a bearer token into
// the request's principal, or answers 401 unauthenticated with
// WWW-Authenticate: Bearer; NewAuthenticator's admits API tokens and
// NewOperatorAuthenticator's operator tokens.
//
// # Input and output
//
// HTTP requests and responses in the generated servers' types. Every
// refused credential answers one problem whose detail names no part.
// Nothing is logged.
//
// # Dependencies
//
// It imports the access domain's apikey, authn, breakglass, operator,
// organization, port and session packages and accessapi, internal/kernel,
// the generated servers of both APIs, and platform/httpkit's page,
// problem and ratelimit. Only the access domain's assembly imports
// it.
package http
