// Package http is the access domain's HTTP adapter. Server is its partial
// of the API's strict server, which internal/app embeds under the
// AccessAPI alias: POST /auth/token (apiKeyUser) exchanges a secret API
// key for an API token; viewer answers the caller; sessions,
// sessionDelete, sessionDeleteAll and logout list and revoke the caller's
// account's sessions; apiKeys, apiKeyCreate, apiKey, apiKeyUpdate,
// apiKeyReset, apiKeyDelete and apiKeyEnableNonAdminManagers manage the
// account's API keys, where a key of another account answers 404 and
// input the service refuses 422 validation-failed. Authenticator is accessapi.Authenticator: the
// middleware that turns a bearer token into the request's principal, or
// answers 401 unauthenticated with WWW-Authenticate: Bearer.
//
// # Input and output
//
// HTTP requests and responses in the generated server's types. Every
// refused credential answers one problem whose detail names no part.
// Nothing is logged.
//
// # Dependencies
//
// It imports the access domain's apikey, authn, port and session packages and
// accessapi, internal/kernel, the generated server, and platform/httpkit's
// page, problem and ratelimit. Only the access domain's assembly imports
// it.
package http
