// Package assembly constructs the access domain: decision 0007's tenancy
// (organizations, accounts, members and the members each account admits)
// and decision 0006's identity (API keys, sessions, tokens, signing keys
// and the break-glass credential). New builds the domain on its Deps; API
// is its partial of the API's strict server, aliased AccessAPI; Stream is
// its migration stream and SealedColumns its sealed columns, which
// internal/app registers in the declared graph's order. Settings are its
// auth.* settings: the issuer, auth.tokens.api.ttl, and the signing
// backend and rotation, which Validate checks. Cron is its periodic tasks:
// RotateKeys, authn.keys.rotate, daily unless rotation is manual.
//
// # Input and output
//
// None of its own; the domain reaches PostgreSQL and the keyring through
// Deps.
//
// # Dependencies
//
// It imports the access domain's adapters and token package,
// platform/cron and platform/queue for its task, platform/crypt and
// platform/sql/migrate. Only internal/app and cmd/ import it, as the
// constructors rule enforces.
package assembly
