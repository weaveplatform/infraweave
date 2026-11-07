// Package assembly constructs the access domain: decision 0007's tenancy
// (organizations, accounts, members and the members each account admits),
// decision 0006's identity (API keys, sessions, tokens, signing keys and
// the break-glass credential), the account settings no other domain
// owns, and each account's managed users and groups. New builds the domain on its Deps; API
// is its partial of the API's strict server, aliased AccessAPI, and
// OperatorAPI its partial of the operator API's, aliased
// AccessOperatorAPI, the API's partial serving the Organization tag
// through the orgadmin service and the Managed Users tag through the user
// and group services; OrganizationID finds an organization's ID by its
// slug, for the features domain's operator partial, and Seats counts an
// organization's and an account's seats in use, for its seats read; Stream is
// its migration stream and SealedColumns its sealed columns, which
// internal/app registers in the declared graph's order. Settings are its
// auth.* settings: the issuer, auth.tokens.api.ttl, auth.session's
// idle_timeout and max_age (12 hours and 7 days), and the signing
// backend, rotation and kms_keys, and auth.oidc_api_keys' allowed_issuers
// and ca_file, and auth.operator.keys, each a ULID and the lower-case hex
// SHA-256 of its secret, which Validate checks; New reads ca_file and fails when it
// holds no certificate; Deps.KMS are the
// kms backend's keys, which internal/app opens. Authenticator is the
// middleware internal/app puts in front of the API and
// OperatorAuthenticator the one in front of the operator API; Routes
// mounts the break-glass login at accessapi.LoginPattern;
// HashPassword and VerifyPassword are decision 0006's break-glass
// password hash, which infraweave auth hash-password prints; WatchSessions keeps the
// replica's session cache in step with infraweave_invalidate. Cron is its
// periodic tasks: ExpireSessions, authn.sessions.expire, hourly, and
// RotateKeys, authn.keys.rotate, daily unless rotation is manual.
//
// # Input and output
//
// New reads auth.oidc_api_keys.ca_file. Otherwise none of its own: the
// domain reaches PostgreSQL and the keyring through Deps, and OIDC issuers
// over HTTPS through the oidc adapter.
//
// # Dependencies
//
// It imports accessapi, featuresapi, whose Entitlements Deps.Features
// carries for the organization and account deletes and the account limits, the access domain's adapters (oidc among them) and
// its apikey, authn, breakglass, group, operator, orgadmin, session,
// settings, token and user packages, platform/cron and platform/queue for its
// tasks, platform/crypt and platform/crypt/signer, platform/sql/migrate
// platform/sql/notify, platform/httpkit/problem and chi. Only
// internal/app and cmd/ import it, as the
// constructors rule enforces.
package assembly
