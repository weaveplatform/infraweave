// Package assembly constructs the access domain: decision 0007's tenancy
// (organizations, accounts, members and the members each account admits)
// and decision 0006's identity (API keys, sessions, tokens, signing keys
// and the break-glass credential). New builds the domain on its Deps; API
// is its partial of the API's strict server, aliased AccessAPI; Stream is
// its migration stream and SealedColumns its sealed columns, which
// internal/app registers in the declared graph's order. Settings are its
// auth.* settings: the issuer, auth.tokens.api.ttl, and the signing
// backend and rotation, which Validate checks. Authenticator is the
// middleware internal/app puts in front of the API; WatchSessions keeps the
// replica's session cache in step with infraweave_invalidate. Cron is its
// periodic tasks: ExpireSessions, authn.sessions.expire, hourly, and
// RotateKeys, authn.keys.rotate, daily unless rotation is manual.
//
// # Input and output
//
// None of its own; the domain reaches PostgreSQL and the keyring through
// Deps.
//
// # Dependencies
//
// It imports accessapi, the access domain's adapters and its authn,
// session and token packages, platform/cron and platform/queue for its
// tasks, platform/crypt, platform/sql/migrate and platform/sql/notify. Only internal/app and cmd/ import it, as the
// constructors rule enforces.
package assembly
