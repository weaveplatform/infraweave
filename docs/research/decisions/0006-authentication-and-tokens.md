# 0006: Authentication and tokens

Status: Accepted

## Context

Every infraweave caller arrives with a credential that has to become a
principal before authorization ([0007](0007-authorization-spaces-and-roles.md))
can run. Spacelift has several credentials and several tokens, and clients and
customer infrastructure depend on their exact shapes:

- **API access.** A secret API key or an OIDC API key is exchanged for a
  short-lived JWT; the JWT's single `aud` claim is the API endpoint, which
  spacectl and the Terraform provider read without verifying the token
  ([01](../reference/01-spacelift-landscape.md#7-authentication),
  [05](../reference/05-terraform-provider.md#authentication-flow),
  [06](../reference/06-spacectl-and-clients.md#23-credentials-and-sessions)).
- **Sessions.** Browser, CLI and `terraform login` sessions are separate per
  channel, carry a precomputed access snapshot, and are listed and revoked
  through the API ([14](../reference/14-spaces-rbac-and-identity.md#10-sessions)).
- **The in-run API token** (`SPACELIFT_API_TOKEN`) lets code inside a run call
  the API with the stack's narrowed rights; the provider reads the stack ID
  from its `sub` ([13](../reference/13-configuration.md#62-other-injected-values),
  [05](../reference/05-terraform-provider.md#authentication-flow)).
- **The OIDC federation token** lets a run assume cloud roles and log in to
  Vault. Its issuer, audience, claims and subject template are written into
  customer trust policies
  ([12](../reference/12-cloud-integrations-and-oidc.md#6-the-oidc-federation-token)).
- **AWS role assumption** uses a deterministic external ID and session name
  that customers also write into trust policies and CloudTrail filters
  ([12](../reference/12-cloud-integrations-and-oidc.md#22-external-id-and-session-name)).
- **MCP clients** reach Spacelift's `/mcp` and Flows' MCP servers through
  OAuth with dynamic client registration and a consent page
  ([22 section 7.3](../reference/22-intelligence.md#73-authentication),
  [23 section 10.4](../reference/23-flows.md#104-live-oauth-authorization-server-metadata)).

On Spacelift, identity providers, MFA and users belong to one account.
infraweave's tenancy is root, organization, account and spaces
([0007](0007-authorization-spaces-and-roles.md#tenancy)): an organization is
one agreement covering several accounts, and its members sign in once
through its identity providers. Open question PD-07 in
[report 30](../reference/30-open-questions.md#product-decisions) asks whether
the run credentials keep Spacelift's formats. Decision 0003 keys idempotency
and rate limits by the authenticated principal once one exists; this record
defines that principal.

## Decision

### Authentication at the organization, authorization per account

- **The organization authenticates.** Identity providers, MFA enforcement,
  the session lifetime and the members belong to the organization. A person
  signs in once, through one of the organization's identity providers, and
  becomes a member session of that organization.
- **The account authorizes.** Entering an account runs that account's
  admission (0007): its `LOGIN` policies on its top space, or its bindings
  under `MANAGED_USERS`. Admission decides whether the member may work in the
  account and with which grants. A member that no account admits holds a
  session with no account and sees an empty account switcher.
- **One account at a time.** A session, and every token derived from it,
  works in one account. Switching accounts issues a new session for the
  other account; nothing a member holds in one account grants anything in
  another.

### Token kinds

Every token infraweave issues is a JWT signed by an installation key (see
[Signing keys](#signing-keys)), except the browser session and the OAuth
refresh token, which are opaque. One library, `github.com/go-jose/go-jose/v4`,
signs and verifies all of them with ES256, RS256 or PS256, which Go's FIPS
140-3 module approves ([0013](0013-secrets-at-rest.md#fips-140-3)). go-jose
is used for signatures only; nothing infraweave seals uses JWE.

| Kind | Obtained by | Lifetime | `aud` | `sub` | Session row |
|---|---|---|---|---|---|
| API token | `POST /auth/token` with a secret or OIDC API key | 10 hours, never past the key's `expiresAt` | the API base URL | `api::<key ULID>`, or `api::oidc::<client ID>::<key ULID>` | yes, channel `UNKNOWN` |
| Organization token | `POST /auth/token` with an organization key | 10 hours, never past the key's `expiresAt` | the API base URL | `org::<key ULID>` | yes, channel `UNKNOWN` |
| Browser session | sign-in through one of the organization's identity providers, or its break-glass login | the organization's session lifetime, by default idle 12 hours and absolute 7 days | none: an opaque cookie, not a JWT | n/a | yes, channel `WEB` |
| CLI token | loopback login from `infraweavectl` | expires with the browser session it came from | the API base URL | member login | yes, channel `INFRAWEAVECTL` |
| Terraform CLI token | `terraform login` against the registry's `login.v1` service | 30 days | the API base URL | member login | yes, channel `TERRAFORM_CLI` |
| MCP access token | the token endpoint of the [authorization server](#the-oauth-authorization-server-for-mcp) | 1 hour | the MCP resource URL the grant names | member login | yes, the grant's, channel `UNKNOWN` |
| MCP refresh token | the same token endpoint | 30 days without use, rotated on every use | none: opaque | n/a | the grant's |
| In-run API token | injected into runs as `INFRAWEAVE_API_TOKEN` | 3 hours, and only while the run is `PLANNING`, `APPLYING` or `PERFORMING` | the API base URL | `<stack or module slug>/<run ULID>` | yes, actor kind `run` |
| OIDC federation token | injected into runs as `INFRAWEAVE_OIDC_TOKEN` and `/mnt/workspace/infraweave.oidc` | 1 hour | the federation audience | the rendered subject template | no |
| Worker session token | launcher registration ([0008](0008-run-execution-and-worker-protocol.md)) | 1 hour, renewed in poll responses | `infraweave-worker` | worker ULID | no |
| Run token | handover of a run to a worker ([0008](0008-run-execution-and-worker-protocol.md)) | until the run ends, plus 1 hour, and at most 24 hours | `infraweave-runner` | run ULID | no |

The lifetimes above are defaults. Each is configuration
(`auth.tokens.<kind>.ttl`; the browser session's two limits are
`auth.session.idle_timeout` and `auth.session.max_age`); a value that is not
positive fails the start. An organization may shorten its browser session
limits below the installation's, never lengthen them. Every API-facing JWT
carries:

| Claim | Value |
|---|---|
| `iss` | `auth.api_issuer`, default the public URL; the in-run token uses `auth.run_api_token.issuer`, default the public URL |
| `aud` | exactly one string: the API base URL (`server.public_url` plus the API prefix), because clients take the endpoint from it; for an MCP access token, the MCP resource URL |
| `sub` | as in the table |
| `jti` | the session ULID; revoking the session revokes the token |
| `iat`, `nbf`, `exp` | Unix seconds; `nbf` equals `iat` |
| `iw_kind` | `user`, `api_key`, `organization_key`, `oauth` or `run`; the worker protocol's tokens carry `worker` and `runner` |

Nothing about access lives in the token: the session row, which names the
organization and the account, and 0007's evaluator decide every request, so
a binding change takes effect without a new token.

### Members and identity providers

- **Tables.**
  - `identity_providers(organization_id, id, kind, name, settings jsonb,
    secret_sealed, is_default, created_at)`, `kind` one of `OIDC`, `SAML`
    and `GITHUB_OAUTH`, the login providers of report 14
    ([6.1](../reference/14-spaces-rbac-and-identity.md#61-login-providers)).
    An organization holds any number; one per kind is its default.
  - `members(organization_id, id, login, name, email, status, created_at,
    last_seen_at)`: a person in the organization.
  - `member_identities(organization_id, member_id, identity_provider_id,
    subject, created_at)`, unique `(identity_provider_id, subject)`: the
    identities that sign in as a member.
  - The account's users of report 28 are the members it has invited or
    admitted, one row per `(account_id, member_id)`; bindings, personal keys
    and sessions name them.
- **Sign-in.** A sign-in starts at `/auth/login/{organization}`, and
  `/auth/login` goes straight there on an installation with one
  organization. The front end verifies the identity with one of the
  organization's identity providers, and `member_identities` resolves the
  provider's stable subject to one member. An unknown subject creates a new
  member, unless SCIM manages the provider, in which case only provisioned
  members sign in.
- **The provider's admin login.** A provider's `adminLogin` setting, as
  Spacelift's SAML and OIDC settings carry it, and a GitHub organization
  admin under `GITHUB_OAUTH`, sign in holding the `organization-admin` role
  (0007), so a misconfigured provider cannot lock the organization out.
  They hold no account role from it.
- **One member, one seat.** A member counts once against the organization's
  `seats` limit, however many accounts admit it
  ([0021](0021-capability-scope-and-supersession.md)).
- **Identities are linked only on purpose.** Two identities from two
  identity providers are two members, and two seats, until an organization
  admin links them:
  1. the admin opens a link naming both members, through the organization
     API;
  2. the person signs in through each of the two providers within one hour,
     at `/auth/link/{link ID}`;
  3. the second sign-in merges the two members into the first member's ID:
     identities, account users, bindings and personal keys move to it, and
     the second member's sessions are revoked.

  Nothing is linked automatically by email, because a provider that lets a
  person set their own email address would otherwise let them take over
  another member.
- **MFA and session lifetime** are organization settings. Spacelift's
  `accountToggleEnforcingMFA` and `enforceMFA` act on the organization of
  the caller's account. Enforcing MFA revokes every session of the
  organization but the caller's.
- **The reference's identity operations.** `samlCreate`, `samlUpdate`,
  `samlDelete`, `samlSettings`, `oidcCreate`, `oidcUpdate`, `oidcDelete`,
  `oidcSettings`, `hasSSO`, the SCIM client operations and the MFA
  operations, served under the `SSO` tag, act on the organization's
  identity providers, not on the caller's account. Each needs the
  organization admin role (0007), reads included, because a provider's
  settings describe the sign-in of every account in the organization; an
  account admin without that role gets `403`, problem `forbidden`. The
  single-provider operations address the organization's default provider
  of that kind, and the organization API lists them all.

### Sessions

- **Table** `sessions(organization_id, account_id, id, channel, actor_kind,
  actor_id, member_id, login, name, email, admin, teams text[],
  identity_provider_id, idp_subject, oauth_client_id, scopes text[],
  ip_address, user_agent, secret_hash, parent_session_id, run_id,
  created_at, last_seen_at, idle_expires_at, expires_at, revoked_at)`. `id`
  is the `Session.id` the API lists and the JWT's `jti`. `account_id` is
  null for a member session no account admits, for organization tokens and
  for break-glass sessions; `member_id` is null for the last two.
- **Browser sessions** use an opaque 32-byte secret in the cookie
  `__Host-infraweave_session` (`HttpOnly`, `Secure`, `SameSite=Lax`,
  `Path=/`). Only its SHA-256 is stored. A cookie-authenticated `POST`,
  `PUT`, `PATCH` or `DELETE` must carry an `Origin` header equal to the
  public URL; without it the cookie is not a credential, and the request
  answers `401`, problem `unauthenticated`.
- **Switching accounts.** `GET /auth/accounts` lists the accounts that
  admit the member, evaluated when it is read. `POST /auth/session/account`
  runs the target account's admission and, when it admits the member,
  replaces the session with a new one for that account in the same channel.
- **Channels.** A new login replaces the same member's earlier live session
  in the same channel. A CLI session records its browser session as
  `parent_session_id`, keeps the account it was issued for and takes its
  parent's absolute expiry, but survives that session's logout and account
  switches. Each OAuth grant is a session of its own and is never replaced
  by another.
- **Live checks.** The middleware verifies the JWT, then reads the session
  through a per-replica cache keyed by `jti`. The cache holds an entry for at
  most 60 seconds and drops it at once on a `session:<id>` message on
  `infraweave_invalidate` ([0005](0005-persistence-postgresql.md)), so a
  revocation reaches every replica within milliseconds, and within 60 seconds
  if a notification is lost.
- **Forced revocation.** Switching an account's authorization scheme,
  changing any of its login policies, enforcing MFA, unlinking SSO, merging
  members, and SCIM deactivation or deletion revoke the sessions report 14
  names, except the caller's where it says so.
- **Expiry.** An hourly periodic queue task (`authn.sessions.expire`,
  [0011](0011-persistent-work-queue.md)) deletes sessions 30 days after they
  end, so the audit trail can still name them meanwhile. The same task
  deletes authorization requests a day after they end, which keeps a
  replayed code able to name the grant to revoke, and OAuth clients unused
  for 30 days.

### API keys and organization keys

- **API keys belong to an account.** Table `api_keys(account_id, id, name,
  description, type, secret_hash, oidc_issuer, oidc_client_id,
  oidc_subject_expression, oidc_claim_mapping jsonb, teams text[],
  is_personal, owner_user_id, is_machine, creator, expires_at, disabled_at,
  disabled_reason, last_used_at, created_at, deleted_at)`, the shape of
  report 28 ([12.2](../reference/28-data-model.md#122-account-and-spaces)).
  A key's token works in its account only. Keys count against the
  organization's `seats` limit by Spacelift's rule
  ([14 section 4.4](../reference/14-spaces-rbac-and-identity.md#44-api-keys)).
- **Secret format.** `iwk_` followed by 43 base64url characters encoding 32
  random bytes from `crypto/rand`. The prefix lets secret scanners and log
  masking recognize a leaked key. The secret is returned once, on create or
  reset, and only its SHA-256 is stored; comparison is constant-time.
- **Exchange.** `POST /auth/token` takes `{"id", "secret"}` and answers
  `{"jwt", "validUntil"}`, the fields `apiKeyUser` returns. It is the only
  API route that takes no bearer token, besides login callbacks, the OAuth
  endpoints, discovery and health. Protocol routes that authenticate by
  other means (the state backend and its upload URLs, inbound VCS webhooks,
  the VCS agent and worker registration) are decisions 0008, 0010 and
  0012's. A wrong ID, wrong secret, expired, disabled or deleted key answers
  `401` with one problem type, `unauthenticated`
  ([0022](0022-api-errors-and-update-semantics.md)), so the answer does not
  reveal which part failed. `last_used_at` is written at most once a minute
  per key.
- **OIDC API keys.** The `secret` is a JWT from the caller's identity provider.
  The server:
  1. fetches `<issuer>/.well-known/openid-configuration` and its JWKS, over
     HTTPS only, with a 5-second timeout and a 1 MiB body limit, and caches
     the JWKS for one hour, refetching at most once a minute when a `kid` is
     unknown;
  2. accepts only asymmetric algorithms (`RS*`, `PS*`, `ES*`, `EdDSA`);
  3. requires `iss` to equal the key's issuer exactly, `aud` to contain the
     client ID, and `exp` and `nbf` to hold within 60 seconds of skew;
  4. matches `sub` against the subject expression compiled with Go's RE2 and
     anchored as `^(?:<expression>)$`;
  5. applies the claim mapping, which renames a claim (`name`) to `value`; the
     values of the claim mapped to `groups` join the key's own teams as the
     session's teams.

  The session's subject is `api::oidc::<client ID>::<key ID>`.
  `auth.oidc_api_keys.allowed_issuers` optionally lists the issuers a key may
  name, so a key creator cannot point the server at arbitrary URLs.
  `auth.oidc_api_keys.ca_file` names a PEM file of further roots to trust
  for issuers, beside the system's, for an issuer behind a private CA.
- **Reset** (`apiKeyReset`) replaces the secret in place and revokes every
  session of the key. **Disable** and **delete** do the same.
- **`ApiKeyInput.admin`.** Not accepted, as decision 0021 rules for this
  changelog-deprecated field: a body that sets it answers `400
  invalid-request` like any unknown field (decision 0022). A key is made an
  account admin by a `space-admin` binding on its account's top space
  (decision 0007), and the key's read model reports `admin` as whether that
  binding exists. There is no separate admin column.
- **Personal keys** carry `owner_user_id`; their principal is the owner's in
  the key's account, so they never exceed the owner's access, and they stop
  working when the owner is deactivated (`disabled_reason = DEPROVISIONED`).
- **Organization keys** belong to the organization and are held by its
  admins: `organization_keys(organization_id, id, name, secret_hash,
  creator_member_id, expires_at, disabled_at, last_used_at, created_at)`.
  The secret is `iwo_` followed by 43 base64url characters, so a scanner
  tells it from an account key. It is exchanged at `POST /auth/token` like an
  API key, for an organization token that only the routes of the
  `Organization` tag accept; every account route treats it as no
  credential. Organization keys reach no account's data and take no seat.

### Human login

Human login establishes an identity (`login`, `name`, `email`, `teams`,
`idp_subject`, `member`) at the organization and hands it to the admission of
the account the member enters, which admits or refuses it and computes its
grants (decision 0007). The front ends are:

| Front end | Library |
|---|---|
| OIDC SSO (authorization code with PKCE) | `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2` |
| SAML 2.0 SSO (SP-initiated; IdP-initiated requests get `303` to the SP flow) | `github.com/crewjam/saml` |
| GitHub OAuth app, the managed-provider analog | `golang.org/x/oauth2` |
| Break-glass admin of an organization, at `/admin-login/{organization}`: a login and a PBKDF2-HMAC-SHA256 hash (below) that the operator holds, sets and replaces through the operator API ([0021](0021-capability-scope-and-supersession.md)); none disables it | `crypto/pbkdf2` |
| FIDO2 security keys for MFA | `github.com/go-webauthn/webauthn` |

The SCIM server, the SAML claim mapping and the WebAuthn ceremonies are outside
this record; each produces or narrows the identity above. Before any identity
provider is configured, the organization reports its provider as `EMPTY`, as
self-hosted Spacelift reports for its account, and only the break-glass login
works.

**The break-glass admin.**

- **Per organization, held by the operator.** Each organization has at most
  one break-glass credential. The operator sets it when it creates the
  organization and replaces or removes it through the operator API;
  organization admins can neither read nor change it. Table
  `organization_break_glass(organization_id, login, password_hash,
  updated_at)`, one row or none per organization.
- **An organization admin, nothing more.** A break-glass sign-in is a
  browser session with actor kind `break_glass`, the organization admin role
  and no account. It reaches the `Organization` and `SSO` tags and the
  console's organization settings, so it can repair identity providers, MFA
  and members, and no account admits it, so it reads no account's data and
  holds no account role. It is not a member and takes no seat.
- **Audited.** Every break-glass sign-in and every change it makes is an
  entry of the organization's audit trail
  ([0015](0015-events-notifications-webhooks-audit.md#audit-trail)), with
  the actor kind `break_glass`.

**The break-glass password hash.**

- **Algorithm.** PBKDF2 with HMAC-SHA256 from Go's standard `crypto/pbkdf2`,
  600,000 iterations, a 16-byte random salt and a 32-byte output.
- **Format.** `$pbkdf2-sha256$i=<iterations>$<salt>$<hash>`, salt and hash in
  unpadded standard base64. Verification compares in constant time.
- **Producing it.** `infraweave auth hash-password` reads the password on
  standard input and prints the hash, so the password never appears in a
  process list or shell history.
- **Iterations.** A hash below 600,000 iterations, with an unknown prefix or
  with a short salt is refused with `422`, problem `validation-failed`, naming
  the field. A hash with more iterations is accepted, so the cost can rise
  without a release.

### The principal

`authn.Middleware` (chi) puts an `authn.Principal` in the request context:

| Field | Meaning |
|---|---|
| `Kind` | `user`, `api_key`, `run`, `organization`, `break_glass`, `operator`, `worker`, `anonymous` |
| `ActorID` | user ULID, API key ULID, organization key ULID, organization ULID for `break_glass`, run ULID, or worker ULID |
| `SessionID` | the session ULID |
| `OrganizationID`, `AccountID`, `MemberID` | from the session; `AccountID` is empty for an organization or break-glass principal and for a member session no account admits; `MemberID` is empty for both of the first two |
| `Login`, `Teams`, `Admin` | from the session |
| `Channel`, `Scopes` | the session channel, and an OAuth grant's scopes |
| `RunID`, `StackID` | set for `run` |

- **Operator principals.** The operator API of
  [0021](0021-capability-scope-and-supersession.md) accepts only `operator`
  principals. An operator key is an ID and the SHA-256 of its secret in the
  installation's configuration, `auth.operator.keys`, so it exists before
  any organization does; it is exchanged at `POST /auth/token` for an API
  token whose audience is the operator API, and every other route treats that
  token as no credential. An operator principal holds no role in any
  organization or account ([0007](0007-authorization-spaces-and-roles.md)).
- **Worker principals.** The worker API of
  [0008](0008-run-execution-and-worker-protocol.md) accepts a worker session
  token on its launcher's routes, as a `worker` principal whose actor is the
  worker, with no session, organization or account; the token's worker and
  expiry ride on the request beside it, for the poll's renewal. A worker
  principal holds no role, and every other route treats its token as no
  credential.
- **Organization principals.** The routes of the `Organization` tag accept
  an organization token, a break-glass session, and a member session or CLI
  token whose member holds the organization admin role, whichever account
  it is in. The `SSO` tag's routes accept the last two only, and every
  account route treats an organization token or a break-glass session as no
  credential.
- The caller key for decision 0003's idempotency keys and rate-limit buckets
  is `<kind>:<actor ID>`, so all sessions of one API key share one budget.
- A request with no credential on a route that needs one answers `401`,
  problem `unauthenticated`, with `WWW-Authenticate: Bearer`. A token with
  the wrong `aud` (a worker or run token on the public API, an MCP access
  token anywhere but its resource) is treated as no credential, and so is a
  member session with no account on an account route. The one cross-audience
  acceptance is `/mcp`, which also takes an API token, unnarrowed, as
  Spacelift's takes spacectl tokens ([0019](0019-ai-provider-boundary.md)),
  because a token already able to call every operation gains nothing
  there.
- Worker tokens are accepted only on `/worker/v1`, which has its own
  middleware owned by [0008](0008-run-execution-and-worker-protocol.md). Run
  tokens are accepted there and on the state backend, `/state/v1`
  ([0010](0010-state-backend.md)), where Terraform sends them as the HTTP Basic
  password with the username `run`.
- `/state/v1` also accepts a user API token, a session token or an in-run API
  token as the Basic password, for read-only external state access; which of
  them may read which stack is [0010](0010-state-backend.md)'s.

### The OAuth authorization server for MCP

infraweave serves its own minimal OAuth 2.1 authorization server for the
MCP surfaces `/mcp` ([0019](0019-ai-provider-boundary.md)) and `/flows/mcp`
with its per-flow servers ([0018](0018-flows-runtime.md)). It is part of the
`access` domain ([0004](0004-repository-architecture.md)), beside sessions,
because every grant is a session row; the MCP surfaces only verify its
access tokens, and `/mcp` also API tokens (below). Each MCP surface
publishes protected-resource metadata ([RFC 9728](https://www.rfc-editor.org/rfc/rfc9728))
at `/.well-known/oauth-protected-resource<resource path>` naming this
server.

| Endpoint | Contract |
|---|---|
| `GET /.well-known/oauth-authorization-server` | [RFC 8414](https://www.rfc-editor.org/rfc/rfc8414) metadata: `issuer` the public URL; the three endpoints below; `response_types_supported: ["code"]`; `grant_types_supported: ["authorization_code", "refresh_token"]`; `code_challenge_methods_supported: ["S256"]`; `token_endpoint_auth_methods_supported: ["none"]`; `scopes_supported: ["mcp:read", "mcp:write"]` |
| `POST /oauth/register` | [RFC 7591](https://www.rfc-editor.org/rfc/rfc7591) dynamic client registration of public clients, without an initial access token. Every redirect URI must be a loopback URI ([RFC 8252](https://www.rfc-editor.org/rfc/rfc8252), `http` on `127.0.0.1`, `[::1]` or `localhost`, any port) or an `https` URI; anything else answers `invalid_redirect_uri` naming it. Registration is rate-limited by client address (decision 0003) |
| `GET /oauth/authorize` | Authorization code with PKCE `S256` only: a request without `code_challenge`, or with `plain`, answers `invalid_request`. `resource` ([RFC 8707](https://www.rfc-editor.org/rfc/rfc8707)) is required and names one MCP surface. The server stores the request for 10 minutes and answers `302` to the console's consent page, `/consent/{id}`, which signs the member in first when there is no browser session. Until the client and redirect URI are known good, a refusal answers `400` with OAuth's JSON error (`401` for `invalid_client`); after, it redirects to the client with `error`, `error_description` and `state` |
| `POST /oauth/token` | `authorization_code` with `code_verifier`, and `refresh_token`. A code is single-use and lives 60 seconds; redeeming it twice revokes the grant it made. Every refresh returns a new refresh token and spends the old one; presenting a spent refresh token revokes the whole grant. A refresh's `scope` narrows that access token alone, never the grant. Answers carry `Cache-Control: no-store` |

- **Consent in the console.** The consent page is a console route
  ([0023](0023-console.md)) on the console's browser session. It shows the
  client's name, its redirect host, the scopes and the account switcher,
  which it reads through `GET /auth/oauth/requests/{id}`, and approves or
  denies through `POST /auth/oauth/requests/{id}/approve` (with the
  account) and `/deny`, cookie-authenticated with the `Origin` check above.
  Approving and denying answer `redirectTo`, the client's redirect URI with
  the code or `access_denied` and the state, where the page sends the
  browser. Only a member's browser session reaches the three operations;
  any other credential answers `403`. The grant is for the account chosen
  there: the member must be admitted to it, and its redirect URI must be
  loopback or match that account's `oauthRedirectAllowedHosts`, or the
  approval answers `403`; a request already decided, or expired, answers
  `404`.
- **Redirect allowlist.** `oauthRedirectAllowedHosts` is an account setting,
  a list that account admins maintain, of entries a bare host or one
  leading `*.`, for `https` only, as Spacelift's OAuth redirect allowlist is
  ([22 section 7.3](../reference/22-intelligence.md#73-authentication)).
  There is no built-in list of client hosts.
- **Scopes.** `mcp:read` and `mcp:write`, which implies `mcp:read`. A scope
  only narrows: the token acts as the member in the grant's account, with
  that member's grants (0007), and `mcp:read` keeps it to the read tools.
  0019 and 0018 say which tools and operations each scope reaches.
- **Tokens.** Access tokens are JWTs signed under the `api-token` purpose,
  with `aud` the resource URL, `iw_kind: oauth` and `scope`. Refresh tokens
  are opaque 32-byte secrets stored as SHA-256 in the grant's session row.
  Revoking the session, deactivating the member or removing the member from
  the account ends the grant.

### In-run API token

- Minted at each handover to `PLANNING`, `APPLYING` or `PERFORMING` and
  delivered sealed with the run's other secret material
  ([0013](0013-secrets-at-rest.md)).
- Valid only while its run is in one of those three states: the middleware
  checks the run's state on every request, from the same cache as sessions.
- Its rights are the stack's grants narrowed by 0007's in-run restriction: no
  state, run, task or log access, and no write-only values; a stack with no
  role binding gets read-only access.
- `sub` is `<stack or module slug>/<run ULID>`, so the directory part is the
  caller ID, as the Spacelift provider's `spacelift_current_stack` reads it.

### OIDC federation token

- **Issuer.** `auth.federation.issuer`, default the public URL, so a relying
  party registers one identity provider per installation.
  `auth.federation.jwks_uri` overrides the discovery document's `jwks_uri`,
  as self-hosted Spacelift's `FEDERATION_CUSTOM_ISSUER` and
  `FEDERATION_CUSTOM_JWKS_URI` do.
- **Audience.** `auth.federation.audience`, default the issuer's host
  without scheme, followed by `/` and the run's account slug. Spacelift's
  default audience is its account's host, so a trust policy that checks the
  audience accepts only one account's tokens; the account slug keeps that
  property on an installation that hosts many accounts.
- **Discovery.** `GET /.well-known/openid-configuration` returns `issuer`,
  `jwks_uri`, `response_types_supported: ["id_token"]`,
  `subject_types_supported: ["public"]`,
  `id_token_signing_alg_values_supported` and `claims_supported`.
  `GET /.well-known/jwks` returns the federation keys only, each with `use:
  sig`, `alg` and `kid`, with `Cache-Control: public, max-age=3600`.
- **Claims.** Exactly report 12's set
  ([6.3](../reference/12-cloud-integrations-and-oidc.md#63-claims)): `iss`,
  `sub`, `aud`, `exp`, `iat`, `jti`, `nbf`, `spaceId`, `spacePath`,
  `callerType`, `callerId`, `runType`, `runId`, `scope`, with `scope` by
  report 12's table ([6.4](../reference/12-cloud-integrations-and-oidc.md#64-scope)).
  A testing run gets `write`. Where Spacelift's claims say `root`, the
  account's top space is named by the account's slug (0007).
- **Subject template.** The account setting `oidc_subject_template`, empty
  meaning the default
  `space:{spaceId}:{callerType}:{callerId}:run_type:{runType}:scope:{scope}`.
  Validation is report 12's
  ([6.5](../reference/12-cloud-integrations-and-oidc.md#65-subject-and-subject-templates)):
  at most 1000 characters, known placeholders only, the restricted character
  set, and a rendered subject of at most 2048 characters. A failure answers
  `422`, problem `validation-failed`, whose `errors` entry names the
  offending placeholder or character.
- **Refresh.** The runner asks for a new token 15 minutes before expiry and
  rewrites `/mnt/workspace/infraweave.oidc`; the environment variable keeps the
  token of the phase start. A tracked run without autodeploy gets a `read`
  token for planning and a new `write` token at the `PREPARING_APPLY`
  handover.

### AWS external ID and session name

These are fixed formats, not configuration, because a change breaks every
customer role:

| Item | Format |
|---|---|
| External ID, stack | `<account slug>@<integration ULID>@<stack slug>@<read or write>` |
| External ID, module | `<account slug>@<integration ULID>@<module slug>@<read or write>` |
| Suffix | `read` for planning phases and proposed runs; `write` for applying, tasks, destroy runs and module test runs |
| Assume role on worker | the integration's custom `externalId` when set |
| `RoleSessionName` | `<run ULID>@<stack or module slug>@<account slug>`, cut to 64 characters |

`installationAwsAccountId` answers the AWS account of the identity the
server assumes roles with, read once through `sts:GetCallerIdentity`, so a
customer can write it into a trust policy. Without AWS credentials it answers
`403`, problem `installation-unsupported`.

### Signing keys

- **Table** `signing_keys(id, purpose, algorithm, public_jwk jsonb,
  private_key_sealed, kms_key_ref, state, created_at, activated_at,
  retire_after, retired_at)`. `id` is the `kid`. `private_key_sealed` uses
  0013's column format; `kms_key_ref` replaces it when the key lives in a KMS.
- **Purposes and algorithms.**

  | Purpose | Algorithm | Published |
  |---|---|---|
  | `api-token` (API, organization, CLI, Terraform CLI, MCP access, in-run, worker and run tokens) | ES256 (P-256) | no |
  | `federation` | RS256 (RSA 3072) by default; PS256 and ES256 selectable | yes, `/.well-known/jwks` |

- **Rotation.** States are `next`, `active`, `retiring` and `retired`. A
  daily periodic task, `authn.keys.rotate`, creates a `next` key 7 days before the
  active key turns 90 days old; the `next` key is published in the JWKS for at
  least 48 hours before it signs anything; the old key then becomes
  `retiring` and stays published until every token it signed has expired plus
  24 hours, then `retired`. `auth.signing.rotation: manual` disables the task
  for relying parties that hold an uploaded copy of the JWKS.
- **Backends.** `auth.signing.backend: local` (default) generates keys in
  process and seals them; `kms` takes operator-supplied key references
  (`awskms`, `gcpkms`, `azurekv`, `vault` transit), signs through the KMS,
  and rotates only when the operator adds a reference.
  - **References** in `auth.signing.kms_keys`, a list of `{provider, ref}`
    oldest first, each name one fixed key pair, because the row's
    `public_jwk` is read once: an AWS KMS key ID or ARN (not an alias), a
    Cloud KMS CryptoKeyVersion, a Key Vault key ID with its version, and a
    Transit key's `<mount>/<name>/<version>`. `kms_key_ref` stores
    `<provider>:<ref>`.
  - **Rotation.** The newest reference no key holds becomes the `next` key
    and signs once published for 48 hours, as a local key does, whatever
    the active key's age; with no key it is active at once. The daily task
    makes the move, so `rotation: manual` is refused with `kms`.
  - **Start.** Each replica reads every reference's public key at start,
    and a key that is not ECDSA P-256 for signing stops it, naming the
    reference. Credentials and clients are the KEK providers'
    ([0013](0013-secrets-at-rest.md)); `vault` keys use
    `secrets.kek.vault`'s address and auth.
- **Verification** caches public keys per replica; an unknown `kid` reloads
  the table at most once a second.

### Configuration

Strict YAML under `auth`: `api_issuer`, `run_api_token.issuer`,
`tokens.<kind>.ttl` (including `oauth_access`, `oauth_refresh` and
`oauth_code`), `session.idle_timeout`, `session.max_age`,
`oidc_api_keys.allowed_issuers`, `oidc_api_keys.ca_file`, `federation.issuer`, `federation.jwks_uri`,
`federation.audience`, `federation.algorithm`, `signing.backend`,
`signing.rotation`, `signing.kms_keys`, `operator.keys`. Identity providers,
MFA enforcement, session limits and break-glass credentials are
organization data, not configuration; the operator API writes the
break-glass credential.

## Rationale

- **PD-07: identical formats by default, with a configurable issuer and
  templates (option b).** Trust policies, Vault roles, CloudTrail filters and
  Azure federated credentials are infrastructure customers already wrote for
  Spacelift. Matching the claim names, the default subject template, the
  external ID and the session name lets a migrated stack assume the same
  roles once the customer adds infraweave's issuer to the trust, which they
  must do anyway because the signing key differs. Option (a) would forbid an
  installation's own issuer URL, which a self-hosted product cannot avoid.
  Option (c) would make every migration a trust-policy rewrite for no gain.
- **Authentication at the organization, authorization per account.** An
  organization is one company signing in through one set of identity
  providers, so one sign-in, one MFA rule and one seat per person are what
  the agreement covers. Its accounts are separate business entities, so each
  keeps its own login policies and bindings, and a role in one grants
  nothing in another. A token for one account at a time keeps every
  request inside one data boundary without naming it.
- **Explicit linking.** Merging identities by matching email is the classic
  account-takeover path; requiring a sign-in through each provider proves
  the person controls both.
- **Short-lived JWTs from long-lived keys.** The exchange keeps the key off
  every request and limits a leaked token to hours. 10 hours matches
  Spacelift and outlives a Terraform apply, which matters because the
  provider exchanges once and never refreshes.
- **A session row behind every JWT.** Spacelift lists and revokes sessions by
  JWT ID, which a purely stateless token cannot honour. One cached lookup per
  request buys immediate revocation; the signature check still rejects forged
  tokens before any database access.
- **`aud` as a single URL string.** Both Spacelift clients find the endpoint in
  it, and infraweave's own clients do the same, so a token alone configures a
  client. MCP access tokens name their resource instead, as RFC 8707 and the
  MCP authorization specification require, so a token minted for `/mcp`
  cannot be replayed on the REST API.
- **API-token keys never published.** Only federation keys appear in the
  JWKS, so a cloud trust policy can never accept an API or run token, even
  if a customer writes a loose audience condition.
- **ES256 inside, RS256 outside.** ES256 keys and signatures are small and
  fast for tokens infraweave alone verifies. RS256 is the algorithm every
  relying party in report 12 accepts; Spacelift's own may be PS512 (capture
  C91), which is selectable if needed.
- **SHA-256 for key secrets, PBKDF2 for the break-glass password.** A
  32-byte random secret cannot be brute-forced, so a slow hash adds cost
  without security. A human password can, so it gets a password hash.
  - **PBKDF2-HMAC-SHA256** is the password hash that FIPS 140-3 approves
    (NIST [SP 800-132](https://csrc.nist.gov/pubs/sp/800/132/final)), and
    Go's `crypto/pbkdf2` runs under `GODEBUG=fips140=only` with a SHA-2 hash,
    a salt of at least 128 bits and a key of at least 112 bits
    ([0013](0013-secrets-at-rest.md#fips-140-3)).
  - **600,000 iterations** is OWASP's figure for PBKDF2-HMAC-SHA256 where
    FIPS 140 compliance is required
    ([Password Storage Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html#pbkdf2)).
    NIST [SP 800-63B-4 section 3.1.1.2](https://pages.nist.gov/800-63-4/sp800-63b.html)
    sets no count; it asks for a cost "as high as practical" and a salt of
    at least 32 bits, which the 16-byte salt exceeds. The login is used only
    when SSO is down, so a slow verification costs nothing that matters.
  - **Rejected: argon2id.** It is memory-hard and the stronger choice
    against GPU cracking, but it is not a FIPS-approved algorithm, so it
    could not run in the FIPS 140-3 mode infraweave supports from the start.
  - **Rejected: bcrypt.** Also not FIPS-approved, and it truncates passwords
    at 72 bytes.
- **A break-glass login per organization, held by the operator.** The
  login exists for the moment an organization's identity providers fail
  and its admins cannot sign in, so it must be held by someone the failure
  does not lock out: the operator, whom the organization calls in that
  moment. One credential per organization means using it for one customer
  reaches no other. Giving it the organization admin role and no account
  role keeps the operator's rule (0007): the operator can repair sign-in
  but reads no account's data.
- **Reference SSO operations need the organization role.** They read and
  change the identity providers every account of the organization signs in
  through, so an account admin of one account must not see or change
  them.
- **A built-in authorization server, minimal.** MCP clients expect metadata
  discovery and dynamic registration, and the console already holds the
  session a consent page needs. PKCE `S256` only, public clients only,
  short access tokens and rotating refresh tokens are the OAuth 2.1 profile
  the MCP specification asks for, and nothing beyond it is served.
- **Redirects: loopback and the account's allowlist.** Loopback covers
  local MCP clients; a hosted client's host is a decision for the account
  that grants it access, so no host is trusted by default.
- **Rejected: an external authorization server** (Dex, Keycloak). It adds a
  stateful dependency beside PostgreSQL for flows a library and a few
  handlers cover.
- **Rejected: HMAC-signed tokens.** They cannot be published for relying
  parties, and every verifier would hold the signing secret.
- **Rejected: PASETO.** Every client and relying party in the research
  expects JWTs.

Departures from Spacelift, for the plan's deviations register:

| Departure | Why |
|---|---|
| The in-run token's `iss` defaults to the public URL and is configurable | The in-run token's issuer is infraweave's public URL like every other infraweave token, and an operator can change it |
| `ApiKeyInput.admin` is not accepted; a `space-admin` binding on the account's top space grants admin | One source of truth for access; the changelog deprecates the flag and decision 0021 excludes it |
| OIDC API key issuers are fetched over HTTPS only, optionally from an allowlist | A self-hosted server fetching user-supplied URLs is an SSRF path |
| Signing keys rotate automatically every 90 days | Self-hosted Spacelift documents no rotation of its JWT signing key, while its AWS module rotates only the master and in-app encryption keys yearly ([24 section 3.5](../reference/24-self-hosted-reference.md#35-encryption-and-kms)); rotation limits key exposure |
| The federation token file is refreshed during long runs | Spacelift does not document reissue (report 12 section 6.1); a run past one hour otherwise loses cloud access |
| Session lifetimes are stated defaults | Spacelift's browser session lifetime is undocumented |
| Identity providers, MFA, session lifetime and members belong to the organization, and a member enters one account at a time | Spacelift has no organization level; one agreement covering several business entities needs one sign-in and separate authorization |
| Identities from two providers are linked only by an admin and a sign-in through each | Spacelift links external accounts to one user per provider (report 14 section 6.5); infraweave never merges by email |
| The federation audience defaults to the host plus the account slug, and claims name the account's top space by its slug | One installation hosts many accounts under one issuer; Spacelift's account host plays both roles on SaaS |
| The authorization server trusts loopback and the account's allowlist only, with no built-in client hosts | No client host is trusted without the account's decision |
| The break-glass login belongs to an organization, the operator holds it, and it is an organization admin with no account role | An installation-wide admin would reach every customer, and an organization-held credential would be lost with the admins it exists to replace |
| The SSO operations need the organization admin role, reads included | The identity providers are the organization's, shared by all its accounts |

## Constraints

- **Unverified formats.** The in-run `sub`'s last segment (capture C17), the
  external ID suffix for tasks, destroy runs and test runs (C81), the exact
  `RoleSessionName` (C83), the federation algorithm (C91) and whether
  `legacy`'s space ID has a ULID suffix (C94) are proposals here until those
  captures land; each is one constant to change.
- **Issuer reachability.** AWS, GCP and Vault fetch the JWKS from the issuer,
  so the public URL must be reachable from them, or the operator uploads the
  JWKS and sets rotation to manual.
- **Trust policies that name `root`.** A trust policy written for a
  Spacelift subject or `spacePath` that names `root` must name the account's
  slug instead; nothing else in the default subject changes.
- **Identity providers carry trust into accounts.** An account admits the
  identities its organization's providers assert, so whoever configures a
  provider, a break-glass session included, can shape the teams a login
  policy sees. Accounts that need to refuse that trust bind users rather
  than teams; the organization's audit trail records every provider
  change.
- **Open client registration.** Anyone can register an OAuth client, as the
  MCP specification expects; a client gains nothing until a member consents
  for an account, and unused clients are deleted after 30 days.
- **Clock skew.** Verification allows 30 seconds of skew on infraweave's own
  tokens and 60 seconds on identity-provider tokens.
- **Spacelift clients still cannot connect** (decision 0001): matching token
  formats serves code inside runs and customer trust policies, not spacectl.

## Verification

- `internal/authn` unit tests: key secret formats and hashing, for account
  and organization keys; exchange failures all answering the same
  `unauthenticated` problem; an organization token and a break-glass
  session refused on an account route; an account admin without the
  organization role refused on the `SSO` tag, reads included; OIDC API key verification with a test issuer served by chi (wrong
  `iss`, `aud`, expired, `alg: none`, `HS256`, unanchored regex attempts,
  unknown `kid` refetch limit); session channel replacement; account
  switching; revocation through `infraweave_invalidate`.
- `TestUnitOAuthServer`: the metadata document; registration refusing a
  non-loopback `http` redirect; `plain` and missing PKCE refused; a code
  used twice; a spent refresh token revoking the grant; `aud` bound to the
  requested resource; a redirect outside the chosen account's allowlist
  refused at consent.
- `TestUnitMemberLinking`: two identities stay two members until linked;
  a link needs a sign-in through each provider within the hour; the merge
  moves identities, users, bindings and personal keys and revokes the
  second member's sessions; two identities with one email are never merged.
- Golden tests for the federation claims, the scope table, the default
  subject template and audience, every placeholder and each validation
  rule, and for the external ID and session name formats.
- Key rotation tests with a fake clock: `next` published 48 hours before use,
  `retiring` kept until the last token expires.
- Break-glass tests: only the operator API sets or replaces the credential;
  an organization admin's attempt answers `403`; a break-glass session
  reaches the `Organization` and `SSO` tags, no account, and takes no seat.
- Break-glass password tests: a known password verifies against a hash made
  by `infraweave auth hash-password`; a wrong one fails; a hash below 600,000
  iterations, with an unknown prefix or with a short salt is refused; all of
  it also under `GODEBUG=fips140=only`.
- Configuration tests: each `auth.tokens.<kind>.ttl` overrides its default,
  a value that is not positive fails the start, and an organization's
  session limit above the installation's is refused.
- Gherkin acceptance against PostgreSQL in testcontainers:
  `test/acceptance/features/auth_api_keys.feature` (create, exchange, reset,
  disable, expiry, organization keys), `auth_sessions.feature` (list,
  delete, forced revocation, account switching, an empty switcher for a
  member no account admits), `auth_oauth_server.feature` (registration,
  consent in a browser session, redeeming, refreshing, a replayed code or
  refresh token revoking the grant, the redirect allowlist at consent, and
  removal from the account ending the grant), `auth_mcp_oauth.feature` (a Go MCP client
  registers, consents in a browser session, calls `/mcp`, refreshes, and is
  refused with a token for another resource), `oidc_federation.feature` (a
  real run's token verified against the served discovery document and JWKS,
  and accepted by a Vault container's `jwt` auth method with
  `bound_claims` on `spacePath`).
- Parity checks against captures C17, C81, C83, C91 and C94 once recorded.

## References

- [01 section 7](../reference/01-spacelift-landscape.md#7-authentication): credentials and token facts clients depend on.
- [05](../reference/05-terraform-provider.md#authentication-flow): how the provider reads `aud` and `sub`.
- [06 section 2.3](../reference/06-spacectl-and-clients.md#23-credentials-and-sessions): spacectl's credentials and refresh.
- [12 section 6](../reference/12-cloud-integrations-and-oidc.md#6-the-oidc-federation-token) and [section 7](../reference/12-cloud-integrations-and-oidc.md#7-per-cloud-oidc-setup): the federation token and its relying parties.
- [14 sections 6 to 8 and 10](../reference/14-spaces-rbac-and-identity.md#6-single-sign-on): single sign-on, SCIM and MFA, API keys and sessions.
- [22 section 7.3](../reference/22-intelligence.md#73-authentication) and [23 section 10.4](../reference/23-flows.md#104-live-oauth-authorization-server-metadata): Spacelift's MCP OAuth.
- [24 section 3.5](../reference/24-self-hosted-reference.md#35-encryption-and-kms): self-hosted signing keys.
- [30](../reference/30-open-questions.md#product-decisions): PD-07 and captures C17, C81, C83, C91, C94.
- [GLOSSARY](../../../GLOSSARY.md): organization, account, member.
- [RFC 7519](https://www.rfc-editor.org/rfc/rfc7519) (JWT), [RFC 7517](https://www.rfc-editor.org/rfc/rfc7517) (JWK), [RFC 8252](https://www.rfc-editor.org/rfc/rfc8252) (loopback login for native clients), [RFC 8414](https://www.rfc-editor.org/rfc/rfc8414) (authorization server metadata), [RFC 7591](https://www.rfc-editor.org/rfc/rfc7591) (dynamic client registration), [RFC 8707](https://www.rfc-editor.org/rfc/rfc8707) (resource indicators), [RFC 9728](https://www.rfc-editor.org/rfc/rfc9728) (protected resource metadata).
- MCP authorization: <https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization>.
