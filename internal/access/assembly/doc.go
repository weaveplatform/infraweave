// Package assembly constructs the access domain: tenancy (organizations,
// accounts, members and the members each account admits), identity (API
// keys, sessions, tokens, signing keys and the break-glass credential), the account settings no other domain
// owns, each account's managed users and groups, each account's space
// tree, and each account's roles. New builds the domain on its Deps; API
// is its partial of the API's strict server, aliased AccessAPI, and
// OperatorAPI its partial of the operator API's, aliased
// AccessOperatorAPI, the API's partial serving the Organization tag
// through the orgadmin service, the Managed Users tag through the user
// and group services, the Roles tag through the role service, the role
// binding operations through the binding service and the grant views
// through the holder service; Bindings is the
// accessapi.Bindings through which another domain binds the actors it
// keeps, such as stacks, and Grants the accessapi.Grants that explains
// what reaches a space for them; OrganizationID finds an organization's ID by its
// slug, for the features domain's operator partial, and Seats counts an
// organization's and an account's seats in use, for its seats read;
// GuardSpaces registers another domain's accessapi.SpaceGuard, which the
// space service consults before each delete and move, and Spaces is the
// accessapi.Spaces such a domain holds a space through and reads the
// tree from; Stream is its
// migration stream and SealedColumns its sealed columns, which
// internal/app registers in construction order. Deleting an account or an
// organization appends accessapi.Deleted to Deps.Events for each account,
// whose kind RegisterEvents registers; Keys are the accounts,
// organizations and spaces other domains hold by ID, for the nightly
// orphan count. Settings are its
// auth.* settings: the issuer, auth.tokens.api.ttl, the authorization
// server's auth.tokens.oauth_access.ttl, oauth_refresh.ttl and
// oauth_code.ttl (an hour, 30 days and a minute), the worker protocol's
// auth.tokens.worker.ttl, run.ttl and run_api.ttl (an hour, 24 hours and
// 3 hours), auth.run_api_token.issuer, auth.session's
// idle_timeout and max_age (12 hours and 7 days), and the signing
// backend, rotation and kms_keys, and auth.oidc_api_keys' allowed_issuers
// and ca_file, and auth.operator.keys, each a ULID and the lower-case hex
// SHA-256 of its secret, which Validate checks; New reads ca_file and fails when it
// holds no certificate; Deps.KMS are the
// kms backend's keys, which internal/app opens. Authenticator is the
// middleware internal/app puts in front of the API and
// OperatorAuthenticator the one in front of the operator API; Tokens
// mints the worker protocol's tokens, and BindRuns binds the runs an
// in-run API token's authentication reads; Routes
// mounts the break-glass login at accessapi.LoginPattern and the
// authorization server for MCP clients at the OAuth patterns;
// HashPassword and VerifyPassword are the break-glass password hash, which infraweave auth hash-password prints; WatchSessions keeps the
// replica's session cache in step with infraweave_invalidate.
// Authorizer is the authorization evaluator for every domain, from the
// replica's per-account snapshots; Requirer is the middleware deciding
// each API operation's action on its subject, finding a space by its slug,
// a role binding's by its ID under its actor's kind, and another domain's
// entity through the lookup ResolveSubjects
// registers; which also decide each account
// principal's Admin as it authenticates; WatchAuthorization keeps them in
// step with infraweave_invalidate's authz:<account> messages and compares
// their versions every 5 seconds for a lost one. Cron is its
// periodic tasks: ExpireSessions, authn.sessions.expire, hourly, which
// also deletes ended authorization requests and unused OAuth clients, and
// RotateKeys, authn.keys.rotate, daily unless rotation is manual.
//
// ref: decisions 0006 and 0007
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
// carries for the organization's entitlement and the account limits, the
// access domain's adapters (oidc among them) and
// its apikey, authn, authz, breakglass, group, operator, orgadmin, session,
// settings, space, token and user packages, platform/cron and platform/queue for its
// tasks, platform/outbox for its event, platform/crypt and
// platform/crypt/signer, platform/sql/integrity, platform/sql/migrate,
// platform/sql/notify, platform/httpkit/problem and chi. Only
// internal/app and cmd/ import it, as the
// constructors rule enforces.
package assembly
