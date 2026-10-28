# 0006: Authentication and tokens

Status: Proposed

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

Open question P7 in
[report 30](../reference/30-open-questions.md#product-decisions) asks whether
these run credentials keep Spacelift's formats. Decision 0003 keys idempotency
and rate limits by the authenticated principal once one exists; this record
defines that principal.

## Decision

### Token kinds

Every token infraweave issues is a JWT signed by an installation key (see
[Signing keys](#signing-keys)). One library, `github.com/go-jose/go-jose/v4`,
signs and verifies all of them, the same library
[0013](0013-secrets-at-rest.md) uses for JWE.

| Kind | Obtained by | Lifetime | `aud` | `sub` | Session row |
|---|---|---|---|---|---|
| API token | `POST /auth/token` with a secret or OIDC API key | 10 hours, never past the key's `expiresAt` | the API base URL | `api::<key ULID>`, or `api::oidc::<client ID>::<key ULID>` | yes, channel `UNKNOWN` |
| Browser session | SSO, GitHub OAuth or break-glass login | idle 12 hours, absolute 7 days | none: an opaque cookie, not a JWT | n/a | yes, channel `WEB` |
| CLI token | loopback login from `infraweavectl` | expires with the browser session it came from | the API base URL | user login | yes, channel `SPACECTL` |
| Terraform CLI token | `terraform login` against the registry's `login.v1` service | 30 days | the API base URL | user login | yes, channel `TERRAFORM_CLI` |
| In-run API token | injected into runs as `SPACELIFT_API_TOKEN` | 3 hours, and only while the run is `PLANNING`, `APPLYING` or `PERFORMING` | the API base URL | `<stack or module slug>/<run ULID>` | yes, actor kind `run` |
| OIDC federation token | injected into runs as `SPACELIFT_OIDC_TOKEN` and `/mnt/workspace/spacelift.oidc` | 1 hour | the federation audience | the rendered subject template | no |
| Worker session token | launcher registration ([0008](0008-run-execution-and-worker-protocol.md)) | 1 hour, renewed in poll responses | `infraweave-worker` | worker ULID | no |
| Run token | handover of a run to a worker ([0008](0008-run-execution-and-worker-protocol.md)) | until the run ends, plus 1 hour | `infraweave-runner` | run ULID | no |

Lifetimes are configuration (`auth.tokens.<kind>.ttl`), with the values above as
defaults. Every API-facing JWT carries:

| Claim | Value |
|---|---|
| `iss` | `auth.api_issuer`, default the public URL; the in-run token uses `auth.run_api_token.issuer`, default `spacelift` |
| `aud` | exactly one string, the API base URL (`server.public_url` plus the API prefix), because clients take the endpoint from it |
| `sub` | as in the table |
| `jti` | the session ULID; revoking the session revokes the token |
| `iat`, `nbf`, `exp` | Unix seconds; `nbf` equals `iat` |
| `iw_kind` | `user`, `api_key` or `run` |

Nothing about access lives in the token: the session row and 0007's evaluator
decide every request, so a binding change takes effect without a new token.

### API keys

- **Table** `api_keys(account_id, id, name, description, type, secret_hash,
  oidc_issuer, oidc_client_id, oidc_subject_expression, oidc_claim_mapping
  jsonb, teams text[], is_personal, owner_user_id, is_machine, creator,
  expires_at, disabled_at, disabled_reason, last_used_at, created_at,
  deleted_at)`, the shape of report 28
  ([12.2](../reference/28-data-model.md#122-account-and-spaces)).
- **Secret format.** `iwk_` followed by 43 base64url characters encoding 32
  random bytes from `crypto/rand`. The prefix lets secret scanners and log
  masking recognise a leaked key. The secret is returned once, on create or
  reset, and only its SHA-256 is stored; comparison is constant-time.
- **Exchange.** `POST /auth/token` takes `{"id", "secret"}` and answers
  `{"jwt", "validUntil"}`, the fields `apiKeyUser` returns. It is the only
  API route that takes no bearer token, besides login callbacks, discovery and
  health. Protocol routes that authenticate by other means (the state
  backend and its upload URLs, inbound VCS webhooks, the VCS agent and worker
  registration) are decisions 0008, 0010 and 0012's.
  A wrong ID, wrong secret, expired, disabled or deleted key answers `401`
  with one problem type, `unauthenticated`
  ([0022](0022-api-errors-and-update-semantics.md)), so the answer does not
  reveal which part failed. `last_used_at` is written at most once a minute per key.
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
  5. applies the claim mapping, so a mapped claim becomes the session's teams.

  `auth.oidc_api_keys.allowed_issuers` optionally lists the issuers a key may
  name, so a key creator cannot point the server at arbitrary URLs.
- **Reset** (`apiKeyReset`) replaces the secret in place and revokes every
  session of the key. **Disable** and **delete** do the same.
- **`ApiKeyInput.admin`.** `true` is stored as a `space-admin` binding on
  `root`, created with the key, and `admin` reads back whether that binding
  exists. There is no separate admin column.
- **Personal keys** carry `owner_user_id`; their principal is the owner's, so
  they never exceed the owner's access, and they stop working when the owner is
  deactivated (`disabled_reason = DEPROVISIONED`).

### Sessions

- **Table** `sessions(account_id, id, channel, actor_kind, actor_id, login,
  name, email, admin, teams text[], idp_subject, ip_address, user_agent,
  secret_hash, parent_session_id, run_id, created_at, last_seen_at,
  idle_expires_at, expires_at, revoked_at)`. `id` is the `Session.id` the API
  lists and the JWT's `jti`.
- **Browser sessions** use an opaque 32-byte secret in the cookie
  `__Host-infraweave_session` (`HttpOnly`, `Secure`, `SameSite=Lax`,
  `Path=/`). Only its SHA-256 is stored. A cookie-authenticated `POST`,
  `PUT`, `PATCH` or `DELETE` must carry an `Origin` header equal to the
  public URL; without it the cookie is not a credential, and the request
  answers `401`, problem `unauthenticated`.
- **Channels.** A new login replaces the same user's earlier live session in
  the same channel. A CLI session records its browser session as
  `parent_session_id` and takes its absolute expiry, but survives that
  session's logout.
- **Live checks.** The middleware verifies the JWT, then reads the session
  through a per-replica cache keyed by `jti`. The cache holds an entry for at
  most 60 seconds and drops it at once on a `session:<id>` message on
  `infraweave_invalidate` ([0005](0005-persistence-postgresql.md)), so a
  revocation reaches every replica within milliseconds, and within 60 seconds
  if a notification is lost.
- **Forced revocation.** Switching the authorization scheme, changing any login
  policy, enforcing MFA, unlinking SSO, and SCIM deactivation or deletion
  revoke the sessions report 14 names, except the caller's where it says so.
- **Expiry.** An hourly periodic queue task (`authn.sessions.expire`,
  [0011](0011-persistent-work-queue.md)) deletes sessions 30 days after they
  end, so the audit trail can still name them meanwhile.

### Human login

Human login establishes an identity (`login`, `name`, `email`, `teams`,
`idp_subject`, `member`) and hands it to 0007's authorization scheme, which
admits or refuses it and computes its grants. The front ends are:

| Front end | Library |
|---|---|
| OIDC SSO (authorization code with PKCE) | `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2` |
| SAML 2.0 SSO (SP-initiated; IdP-initiated requests get `303` to the SP flow) | `github.com/crewjam/saml` |
| GitHub OAuth app, the managed-provider analog | `golang.org/x/oauth2` |
| Break-glass admin: `auth.admin.username` and `auth.admin.password_hash` (argon2id) at `/admin-login`; empty disables it | `golang.org/x/crypto/argon2` |
| FIDO2 security keys for MFA | `github.com/go-webauthn/webauthn` |

The SCIM server, the SAML claim mapping and the WebAuthn ceremonies are outside
this record; each produces or narrows the identity above. Before any SSO is
configured the account's identity provider is `EMPTY`, as self-hosted Spacelift
reports, and only the break-glass login works.

### The principal

`authn.Middleware` (chi) puts an `authn.Principal` in the request context:

| Field | Meaning |
|---|---|
| `Kind` | `user`, `api_key`, `run`, `anonymous` |
| `ActorID` | user ULID, API key ULID or run ULID |
| `SessionID` | the session ULID |
| `Login`, `Teams`, `Admin` | from the session |
| `Channel` | the session channel |
| `RunID`, `StackID` | set for `run` |

- The caller key for decision 0003's idempotency keys and rate-limit buckets
  is `<kind>:<actor ID>`, so all sessions of one API key share one budget.
- A request with no credential on a route that needs one answers `401`,
  problem `unauthenticated`, with `WWW-Authenticate: Bearer`. A token with
  the wrong `aud` (a worker or run token on the public API) is treated as no
  credential.
- Worker tokens are accepted only on `/worker/v1`, which has its own
  middleware owned by [0008](0008-run-execution-and-worker-protocol.md). Run
  tokens are accepted there and on the state backend, `/state/v1`
  ([0010](0010-state-backend.md)), where Terraform sends them as the HTTP Basic
  password with the username `run`.
- `/state/v1` also accepts a user API token, a session token or an in-run API
  token as the Basic password, for read-only external state access; which of
  them may read which stack is [0010](0010-state-backend.md)'s.

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

- **Issuer.** `auth.federation.issuer`, default the account URL (the public
  URL when an installation hosts one account). `auth.federation.jwks_uri`
  overrides the discovery document's `jwks_uri`, as self-hosted Spacelift's
  `FEDERATION_CUSTOM_ISSUER` and `FEDERATION_CUSTOM_JWKS_URI` do.
- **Audience.** `auth.federation.audience`, default the issuer's host without
  scheme.
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
  A testing run gets `write`.
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
  rewrites `/mnt/workspace/spacelift.oidc`; the environment variable keeps the
  token of the phase start. A tracked run without autodeploy gets a `read`
  token for planning and a new `write` token at the `PREPARING_APPLY`
  handover.

### AWS external ID and session name

These are fixed formats, not configuration, because a change breaks every
customer role:

| Item | Format |
|---|---|
| External ID, stack | `<account name>@<integration ULID>@<stack slug>@<read or write>` |
| External ID, module | `<account name>@<integration ULID>@<module slug>@<read or write>` |
| Suffix | `read` for planning phases and proposed runs; `write` for applying, tasks, destroy runs and module test runs |
| Assume role on worker | the integration's custom `externalId` when set |
| `RoleSessionName` | `<run ULID>@<stack or module slug>@<account name>`, cut to 64 characters |

### Signing keys

- **Table** `signing_keys(id, purpose, algorithm, public_jwk jsonb,
  private_key_sealed, kms_key_ref, state, created_at, activated_at,
  retire_after, retired_at)`. `id` is the `kid`. `private_key_sealed` uses
  0013's column format; `kms_key_ref` replaces it when the key lives in a KMS.
- **Purposes and algorithms.**

  | Purpose | Algorithm | Published |
  |---|---|---|
  | `api-token` (API, CLI, Terraform CLI, in-run, worker and run tokens) | ES256 (P-256) | no |
  | `federation` | RS256 (RSA 3072) by default; PS256 and ES256 selectable | yes, `/.well-known/jwks` |
  | `worker-pool-ca` (0008's pool certificates) | ECDSA P-256 | no |

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
- **Verification** caches public keys per replica; an unknown `kid` reloads
  the table at most once a second.

### Configuration

Strict YAML under `auth`: `api_issuer`, `run_api_token.issuer`,
`tokens.<kind>.ttl`, `session.idle_timeout`, `session.max_age`,
`oidc_api_keys.allowed_issuers`, `federation.issuer`, `federation.jwks_uri`,
`federation.audience`, `federation.algorithm`, `signing.backend`,
`signing.rotation`, `signing.kms_keys`, `admin.username`,
`admin.password_hash`.

## Rationale

- **P7: identical formats by default, with a configurable issuer and
  templates (option b).** Trust policies, Vault roles, CloudTrail filters and
  Azure federated credentials are infrastructure customers already wrote for
  Spacelift. Matching the claim names, the default subject template, the
  external ID and the session name lets a migrated stack assume the same
  roles once the customer adds infraweave's issuer to the trust, which they
  must do anyway because the signing key differs. Option (a) would forbid an
  installation's own issuer URL, which a self-hosted product cannot avoid.
  Option (c) would make every migration a trust-policy rewrite for no gain.
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
  client.
- **API-token keys never published.** Only federation keys appear in the
  JWKS, so a cloud trust policy can never accept an API or run token, even
  if a customer writes a loose audience condition.
- **ES256 inside, RS256 outside.** ES256 keys and signatures are small and
  fast for tokens infraweave alone verifies. RS256 is the algorithm every
  relying party in report 12 accepts; Spacelift's own may be PS512 (capture
  C91), which is selectable if needed.
- **SHA-256 for key secrets, argon2id for the break-glass password.** A
  32-byte random secret cannot be brute-forced, so a slow hash adds cost
  without security. A human password can, so it gets argon2id.
- **Rejected: an external authorization server** (Dex, Keycloak). It adds a
  stateful dependency beside PostgreSQL for flows a library covers.
- **Rejected: HMAC-signed tokens.** They cannot be published for relying
  parties, and every verifier would hold the signing secret.
- **Rejected: PASETO.** Every client and relying party in the research
  expects JWTs.

Departures from Spacelift, for the plan's deviations register:

| Departure | Why |
|---|---|
| The in-run token's `iss` defaults to `spacelift` but is configurable | Keeps `spacelift_current_stack` working while letting an operator brand the issuer |
| `ApiKeyInput.admin` is stored as a root `space-admin` binding | One source of truth for access; the changelog already deprecates the flag |
| OIDC API key issuers are fetched over HTTPS only, optionally from an allowlist | A self-hosted server fetching user-supplied URLs is an SSRF path |
| Signing keys rotate automatically every 90 days | Self-hosted Spacelift documents no rotation of its JWT signing key, while its AWS module rotates only the master and in-app encryption keys yearly ([24 section 3.5](../reference/24-self-hosted-reference.md#35-encryption-and-kms)); rotation limits key exposure |
| The federation token file is refreshed during long runs | Spacelift does not document reissue (report 12 section 6.1); a run past one hour otherwise loses cloud access |
| Session lifetimes are stated defaults | Spacelift's browser session lifetime is undocumented |
| No seat accounting for API keys | Tier and seat gates are decision 0021's |

## Constraints

- **Unverified formats.** The in-run `sub`'s last segment (capture C17), the
  external ID suffix for tasks, destroy runs and test runs (C81), the exact
  `RoleSessionName` (C83), the federation algorithm (C91) and whether
  `legacy`'s space ID has a ULID suffix (C94) are proposals here until those
  captures land; each is one constant to change.
- **Issuer reachability.** AWS, GCP and Vault fetch the JWKS from the issuer,
  so the public URL must be reachable from them, or the operator uploads the
  JWKS and sets rotation to manual.
- **Clock skew.** Verification allows 30 seconds of skew on infraweave's own
  tokens and 60 seconds on identity-provider tokens.
- **Spacelift clients still cannot connect** (decision 0001): matching token
  formats serves code inside runs and customer trust policies, not spacectl.

## Verification

- `internal/authn` unit tests: key secret format and hashing; exchange
  failures all answering the same `unauthenticated` problem; OIDC API key verification
  with a test issuer served by chi (wrong `iss`, `aud`, expired, `alg: none`,
  `HS256`, unanchored regex attempts, unknown `kid` refetch limit); session
  channel replacement; revocation through `infraweave_invalidate`.
- Golden tests for the federation claims, the scope table, the default
  subject template, every placeholder and each validation rule, and for the
  external ID and session name formats.
- Key rotation tests with a fake clock: `next` published 48 hours before use,
  `retiring` kept until the last token expires.
- Gherkin acceptance against PostgreSQL in testcontainers:
  `test/acceptance/features/auth_api_keys.feature` (create, exchange, reset,
  disable, expiry), `auth_sessions.feature` (list, delete, forced
  revocation), `oidc_federation.feature` (a real run's token verified against
  the served discovery document and JWKS, and accepted by a Vault container's
  `jwt` auth method with `bound_claims` on `spacePath`).
- Parity checks against captures C17, C81, C83, C91 and C94 once recorded.

## References

- [01 section 7](../reference/01-spacelift-landscape.md#7-authentication): credentials and token facts clients depend on.
- [05](../reference/05-terraform-provider.md#authentication-flow): how the provider reads `aud` and `sub`.
- [06 section 2.3](../reference/06-spacectl-and-clients.md#23-credentials-and-sessions): spacectl's credentials and refresh.
- [12 section 6](../reference/12-cloud-integrations-and-oidc.md#6-the-oidc-federation-token) and [section 7](../reference/12-cloud-integrations-and-oidc.md#7-per-cloud-oidc-setup): the federation token and its relying parties.
- [14 sections 8 and 10](../reference/14-spaces-rbac-and-identity.md#8-api-keys): API keys and sessions.
- [24 section 3.5](../reference/24-self-hosted-reference.md#35-encryption-and-kms): self-hosted signing keys.
- [30](../reference/30-open-questions.md#product-decisions): P7 and captures C17, C81, C83, C91, C94.
- [RFC 7519](https://www.rfc-editor.org/rfc/rfc7519) (JWT), [RFC 7517](https://www.rfc-editor.org/rfc/rfc7517) (JWK), [RFC 8252](https://www.rfc-editor.org/rfc/rfc8252) (loopback login for native clients).
