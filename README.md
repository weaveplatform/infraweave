# infraweave

Self-hosted infrastructure-as-code orchestration with the feature set of
Spacelift (stacks, runs, policies, contexts, drift detection, worker pools)
behind a REST API described by OpenAPI 3 instead of GraphQL.

Everything starts from a machine-checked reference of the upstream surface:
`api/reference/spacelift/spacelift.openapi.yaml`. It holds a REST route for
every one of Spacelift's 665 GraphQL root fields, with the official Terraform
provider's attribute descriptions attached. See
[decision 0001](docs/research/decisions/0001-rest-first-spacelift-surface.md)
for why infraweave is REST-first.

## The Spacelift specification reference

| Piece | What |
|---|---|
| `internal/devtools/specref` | Builds one REST reference per Spacelift product. Deploy: parses the GraphQL introspection snapshot, the spacelift-api-bruno collection and the Terraform provider schema and maps root fields to REST routes. Flows: reads the RPC calls of `terraform-provider-flows` and `flowctl` and maps RPC paths to REST routes. Builds and validates each OpenAPI 3.0.3 document |
| `tools/specref` | `fetch` (network; terraform for Deploy); `generate`, `check`, `scaffold`, `inventory` and `parity` (offline). `--product` picks the Spacelift product, `deploy` by default or `flows`, and with it the default `--dir`, parity `--baseline` and inventory outputs |
| `api/reference/spacelift/` | Pinned snapshots, `sources.lock.yaml`, the hand-curated `rest-mapping.yaml`, and the generated spec and `coverage.md` |
| `docs/research/` | Research reports in `reference/` (landscape, mapping rules, provider), decision records in `decisions/`; see its [README](docs/research/README.md) |

Generation fails when any of these happens:

- a root field is neither routed nor excluded with a reason;
- two routes collide;
- a mutation gets no resource shape (`rpcBudget` is 0);
- `rest-mapping.yaml` names something upstream no longer has.

The weekly **OpenAPI specification update** workflow (`openapi-spec-update.yml`)
compares the pinned upstream commits with the newest ones. When either is
newer, it regenerates on a runner and opens a PR.

## The served API

infraweave's own API is authored as fragments in `api/src/api/`:
- `info.yaml` holds the document head;
- `components-*.yaml` and `paths-<family>.yaml` hold only `paths` and `components`.

`make bundle` (`tools/openapi-bundle` over `internal/devtools/openapibundle`)
merges the fragments into `api/api.openapi.yaml`, served under `/api/v1`. The
operator API's fragments in `api/src/operator/` bundle the same way into
`api/operator.openapi.yaml`, served under `/operator/v1`; it is
infraweave's own, outside the reference and parity. Each bundle is
committed, and a unit test fails when it is stale.

`make parity` decides, for each product's reference operation, decision
0016's outcome: served, served with divergence, superseded or missing. A
served operation that differs from the reference in method, route or
parameters must record each difference in `x-infraweave-divergence`, and a
superseded one must not be served. `api/parity-baseline.<product>.json` is
the computed missing list, by tag. `TestUnitAPIParity` (Deploy) and
`TestUnitFlowsParity` fail on a broken rule, on a missing operation the
baseline does not list and on an entry the bundle no longer needs, so
coverage of the reference only goes up. `make verify-parity` runs the same
check and also fails when `docs/data/capabilities.json` claims a state the
acceptance results do not show.

One document holds Deploy and, from P18c, Flows under `/api/v1/flows/`
([decision 0018](docs/research/decisions/0018-flows-runtime.md#api-and-clients)).
`make generate` turns it into the strict server in
`internal/platform/gen/api` and the client in `pkg/client/api`, and the
operator document into `internal/platform/gen/operator` and
`pkg/client/operator`. Go programs
call the API through `pkg/sdk`, which wraps that client with the API's
conventions: a bearer token, an `Idempotency-Key` on every `POST` and
`PATCH`, retries on `429` and on `503` with `Retry-After`, iterators over
`nextCursor`, and a typed error per problem slug
([decision 0001](docs/research/decisions/0001-rest-first-spacelift-surface.md#sdk-pkgsdk)).
Every acceptance step drives the server through it.

Bundling fails when any of these happens:

- `info.yaml` is missing, or holds keys other than its own;
- a fragment holds anything besides `paths` and `components`;
- a path, `operationId` or component is defined twice (the error names both files);
- the result is not valid OpenAPI 3.0.3.

### Running it

`infraweave serve` serves the API on the API listener under
`/api/v1`, and the probes on the admin listener only
([decision 0017](docs/research/decisions/0017-observability-and-health.md#health)):
`GET /livez` while the process serves HTTP, `GET /startupz` once the schema
is at this binary's version and the keyring has loaded, and `GET /readyz` while PostgreSQL answers and
shutdown has not begun. Add `?verbose` for the checks as JSON.
`infraweave scheduler` serves only the admin listener and runs the work
queue: the workers of each logical queue and the scheduler ticker, which
fires periodic tasks such as `queue.prune` once across every instance
([decision 0011](docs/research/decisions/0011-persistent-work-queue.md)).
Its `GET /readyz` also fails while its listening connection to PostgreSQL
is down. `infraweave all` runs both in one process; on shutdown the HTTP
drain comes first, then the ticker stops, then the workers finish or hand
their jobs back within `queue.shutdown_grace`. Every process mode logs JSON lines to stdout with
`time`, `level`, `msg`, `service`, `mode`, `version` and, inside a request,
`request_id`, and logs one `request` line per API request. A valid
`X-Request-Id` (1 to 128 of `A-Z a-z 0-9 . _ -`) is kept, any other is
replaced by a generated one, and the response echoes it. Secrets, cookies
and authorization headers are logged as `[redacted]`.
Every operation answers a `501` problem until its family is implemented, and
every error, the router's 404 and 405 included, is an
`application/problem+json` body whose `instance` is
`urn:infraweave:request:` and the response's `X-Request-Id`
([decision 0022](docs/research/decisions/0022-api-errors-and-update-semantics.md)).
A request that needs PostgreSQL while it is unreachable answers
`503 service-unavailable` with `Retry-After: 2`, which `pkg/sdk` retries;
any other unexpected failure answers `500`.

Every route but `POST /auth/token` takes a bearer token or a browser
session's cookie
([decision 0006](docs/research/decisions/0006-authentication-and-tokens.md)).
An API key exchanges there: `{"id", "secret"}` answers the reference's
`User`, whose `jwt` is an API token for `auth.tokens.api.ttl`, never past
the key's expiry, and whose `validUntil` is its expiry. A secret key's
secret is `iwk_` and 43 base64url characters. An OIDC key's secret is a JWT
from the key's issuer, fetched over HTTPS only: it must be signed with an
asymmetric algorithm, name the issuer as `iss` and the key's client ID in
`aud`, hold `exp` and `nbf` within 60 seconds, and carry a `sub` the key's
subject expression matches whole; its `groups` claim, after the key's claim
mapping, adds teams. A wrong ID or secret, or an expired, disabled or
deleted key, answers one `401 unauthenticated`.

`GET /api-keys`, `POST /api-keys`, `GET`, `PUT` and `DELETE
/api-keys/{apiKeyId}` and `POST /api-keys/{apiKeyId}/reset` manage the
caller's account's keys; a key of another account answers `404`. Create and
reset answer the secret once; only its SHA-256 is stored. Reset, disable
and delete revoke every session of the key. `ApiKeyInput` has no `admin`:
a body that sets it, or any field the schema does not name, answers
`400 invalid-request`. A key's `admin` reads `false` until API key role
bindings are served. A request without a
valid token answers `401 unauthenticated` with `WWW-Authenticate: Bearer`.
Each token stands for a session row, which `GET /sessions`,
`DELETE /sessions/{sessionId}`, `DELETE /sessions` (all but the caller's)
and `POST /auth/logout` list and revoke within the caller's account; a
session of another account answers `404`. Each replica trusts a session it
read for 60 seconds, and a revocation reaches every replica at once through
`NOTIFY infraweave_invalidate, 'session:<id>'`. The hourly
`authn.sessions.expire` task deletes sessions 30 days after they end.

#### Browser sessions

A browser session is named by the cookie `__Host-infraweave_session`
(`HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`), an opaque 32-byte secret
of which only the SHA-256 is stored. It ends after `auth.session.idle_timeout`
without a request, or at `auth.session.max_age` whatever its use. A cookie
authenticates `GET`, `HEAD` and `OPTIONS`; a `POST`, `PUT`, `PATCH` or
`DELETE` carries it as a credential only with an `Origin` equal to
`server.public_url`'s, and otherwise answers `401 unauthenticated`.
`POST /auth/logout` with a cookie also clears it.

- **Break-glass sign-in.** `GET /admin-login/<organization>` answers a form,
  and a `POST` of its `login` and `password`, with the public URL's
  `Origin`, checks them against the credential the operator set. Success
  sets the cookie and answers `303` to `/`; a wrong login or password, or an
  organization with no credential, answers one `401 unauthenticated`. Each
  attempt draws from the client address's expensive budget, and each is on
  the organization's trail as `organization.break_glass_sign_in` with actor
  kind `break_glass`. The session is an organization admin in no account:
  `GET /viewer` and the organization's routes admit it, and every account
  route answers it `401`.
- **CLI tokens.** `POST /auth/cli-confirmation-tokens` with `{"pubKey"}`, an
  RSA public key of at least 2048 bits in PEM or base64url PEM, answers the
  CLI's token sealed to that key as `<key>.<token>`, spacectl's loopback
  envelope. Its session is channel `INFRAWEAVECTL`, in the browser session's
  account, and ends with the browser session's absolute expiry, but survives
  its logout and account switches. Only a member's browser session issues
  one: any other credential answers `403 forbidden`, and a key that cannot
  be sealed to answers `422 validation-failed` naming `pubKey`.
- **Switching accounts.** `GET /auth/accounts` lists the accounts whose
  `account_users` admit the caller's member, and `POST /auth/session/account`
  with `{"account"}` replaces the caller's session with one for that account
  in the same channel and with the same absolute expiry: a browser session
  gets a new cookie, a token session a new `jwt`. An account that does not
  admit the member answers `404`. A member no account admits holds a session
  of no account, which lists no account.

#### The OAuth server for MCP clients

An MCP client authorizes as a member through the built-in OAuth 2.1
server, which `GET /.well-known/oauth-authorization-server` describes.

- **Registration.** `POST /oauth/register` registers a public client
  (RFC 7591) by anyone, from the client address's expensive budget. Each
  redirect URI is `https`, or `http` on a loopback host; any other answers
  `invalid_redirect_uri`. A client unused for 30 days is deleted.
- **Authorization.** `GET /oauth/authorize` takes the code flow with an
  `S256` PKCE challenge, the scopes `mcp:read` (the default) and
  `mcp:write`, which includes it, and a `resource` of `<public URL>/mcp`,
  `/flows/mcp` or `/flows/mcp/<project>`. It answers `302` to the
  console's `/consent/<request>` page, which reads the request with
  `GET /api/v1/auth/oauth/requests/{requestId}` and approves it for one
  account or denies it, each answering the `redirectTo` that sends the
  browser back to the client. Only a member's browser session consents. A
  redirect URI matches a registered one exactly, or on a loopback host at
  any port; approving sends the code only to a loopback host or one on the
  account's `oauthRedirectAllowedHosts`, any other answering `403`.
- **Tokens.** `POST /oauth/token` redeems a code, once and within
  `auth.tokens.oauth_code.ttl`, for an access token whose `aud` is the
  resource, of `auth.tokens.oauth_access.ttl`, and a refresh token. A
  refresh answers a new pair and spends the old refresh token; its `scope`
  narrows that access token alone. Redeeming a code twice, or presenting a
  spent refresh token, revokes the grant. The grant is a session of the
  member's in the account, listed by `GET /sessions`, which removing or
  deactivating the member ends.

#### The operator API

The operator API under `/operator/v1` creates the installation's
organizations
([decision 0021](docs/research/decisions/0021-capability-scope-and-supersession.md#billing)).
Its callers are operator keys, listed in `auth.operator.keys` by ID and the
SHA-256 of their secret, so the operator can act before any organization
exists. An operator key exchanges at `POST /api/v1/auth/token` like an API
key, for an operator token whose `aud` is the operator API's base URL and
whose `iw_kind` is `operator`. Only `/operator/v1` accepts it, and
`/operator/v1` accepts nothing else: each answers the other's token
`401 unauthenticated`.

- `POST /organizations` creates an organization from its `slug` and
  `name`, its first account's `slug` and `name`, and optionally its
  `breakGlass` `login` and `passwordHash`, in one transaction. The account
  starts unclaimed. A slug in use answers `409 already-exists`.
- `GET /organizations` lists them, oldest first, and `GET` and `DELETE
  /organizations/{organization}` read and delete one by slug. A delete
  takes the organization's accounts and everything access holds for them,
  and revokes their sessions at once.
- `PUT` and `DELETE /organizations/{organization}/break-glass` set and
  remove the organization's break-glass credential.

A password hash is PBKDF2-HMAC-SHA256 with at least 600,000 iterations, a
salt of at least 16 bytes and a 32-byte key, written
`$pbkdf2-sha256$i=<n>$<salt>$<hash>` in unpadded standard base64. Fewer
iterations, another prefix or a short salt answers `422
validation-failed` naming the field. No answer carries the hash. Every
write is on the organization's trail with actor kind `operator`.

To bootstrap an installation:

1. Make an operator key: a ULID and a secret, such as `iwk_` and 43
   base64url characters. Put the ID and the lower-case hex SHA-256 of the
   secret in `auth.operator.keys`, and keep the secret in your secret
   store.
2. Hash the break-glass password with `infraweave auth hash-password`,
   which reads the password's first line from stdin and prints its hash:
   `infraweave auth hash-password < password.txt`.
3. Exchange the operator key at `POST /api/v1/auth/token`, then `POST
   /operator/v1/organizations` with the hash as `breakGlass.passwordHash`.

| Setting | Flag | Environment | Default |
|---|---|---|---|
| `server.listen` | `--listen` | `INFRAWEAVE_SERVER_LISTEN` | `:8080` |
| `server.admin_listen` | `--admin-listen` | `INFRAWEAVE_SERVER_ADMIN_LISTEN` | `:9090` |
| `server.shutdown.ready_delay` | | `INFRAWEAVE_SERVER_SHUTDOWN_READY_DELAY` | `5s` |
| `server.shutdown.timeout` | `--shutdown-timeout` | `INFRAWEAVE_SERVER_SHUTDOWN_TIMEOUT` | `30s` |
| `server.rate_limit.standard.rate` | | `INFRAWEAVE_SERVER_RATE_LIMIT_STANDARD_RATE` | `10` (per second) |
| `server.rate_limit.standard.burst` | | `INFRAWEAVE_SERVER_RATE_LIMIT_STANDARD_BURST` | `100` |
| `server.rate_limit.expensive.rate` | | `INFRAWEAVE_SERVER_RATE_LIMIT_EXPENSIVE_RATE` | `1` (per second) |
| `server.rate_limit.expensive.burst` | | `INFRAWEAVE_SERVER_RATE_LIMIT_EXPENSIVE_BURST` | `10` |
| `server.rate_limit.disabled_callers` | | `INFRAWEAVE_SERVER_RATE_LIMIT_DISABLED_CALLERS` (comma-separated) | none |
| `server.idempotency.ttl` | | `INFRAWEAVE_SERVER_IDEMPOTENCY_TTL` | `24h` |
| `server.idempotency.lock` | | `INFRAWEAVE_SERVER_IDEMPOTENCY_LOCK` | `5m` |
| `server.public_url` | | `INFRAWEAVE_SERVER_PUBLIC_URL` | required by `serve` and `all`: the http or https origin clients reach infraweave at, such as `https://infraweave.example.com` |
| `auth.api_issuer` | | `INFRAWEAVE_AUTH_API_ISSUER` | `server.public_url`: the `iss` of every API token |
| `auth.tokens.api.ttl` | | `INFRAWEAVE_AUTH_TOKENS_API_TTL` | `10h`: the lifetime of the token an API key or operator key exchanges for; must be positive |
| `auth.tokens.oauth_access.ttl` | | `INFRAWEAVE_AUTH_TOKENS_OAUTH_ACCESS_TTL` | `1h`: the most an MCP client's access token lasts, never past its grant; must be positive |
| `auth.tokens.oauth_refresh.ttl` | | `INFRAWEAVE_AUTH_TOKENS_OAUTH_REFRESH_TTL` | `720h`: how long an MCP client's grant lasts after its last refresh; must be positive |
| `auth.tokens.oauth_code.ttl` | | `INFRAWEAVE_AUTH_TOKENS_OAUTH_CODE_TTL` | `1m`: how long an authorization code waits to be redeemed; must be positive |
| `auth.tokens.worker.ttl` | | `INFRAWEAVE_AUTH_TOKENS_WORKER_TTL` | `1h`: a worker session token's lifetime, which each poll renews before it ends; must be positive |
| `auth.tokens.run.ttl` | | `INFRAWEAVE_AUTH_TOKENS_RUN_TTL` | `24h`: the most a run token lasts; the worker protocol accepts it only while its run is with a worker; must be positive |
| `auth.tokens.run_api.ttl` | | `INFRAWEAVE_AUTH_TOKENS_RUN_API_TTL` | `3h`: an in-run API token's lifetime; it authenticates only while its run is planning, applying or performing; must be positive |
| `auth.run_api_token.issuer` | | `INFRAWEAVE_AUTH_RUN_API_TOKEN_ISSUER` | `server.public_url`: the `iss` of in-run API tokens |
| `auth.session.idle_timeout` | | `INFRAWEAVE_AUTH_SESSION_IDLE_TIMEOUT` | `12h`: how long a browser session lasts without a request; must be positive |
| `auth.session.max_age` | | `INFRAWEAVE_AUTH_SESSION_MAX_AGE` | `168h`: a browser session's absolute lifetime, which its CLI tokens share; must be positive |
| `auth.signing.backend` | | `INFRAWEAVE_AUTH_SIGNING_BACKEND` | `local`: signing keys generated in process and sealed under the keyring; `kms` signs through the KMS keys of `auth.signing.kms_keys`, whose private halves never enter infraweave |
| `auth.signing.rotation` | | `INFRAWEAVE_AUTH_SIGNING_ROTATION` | `automatic`: the daily `authn.keys.rotate` task replaces each local signing key every 90 days, and moves to a KMS key added to `auth.signing.kms_keys`; `manual` never replaces one, and is refused with `kms` |
| `auth.oidc_api_keys.allowed_issuers` | | `INFRAWEAVE_AUTH_OIDC_API_KEYS_ALLOWED_ISSUERS` (comma-separated) | none: an OIDC API key may name any `https` issuer; when set, only these, at creation and at every exchange |
| `auth.oidc_api_keys.ca_file` | | `INFRAWEAVE_AUTH_OIDC_API_KEYS_CA_FILE` | none: a PEM file of certificate authorities trusted beside the system's when fetching an OIDC issuer's documents, for an issuer under a private CA; read at start |
| `auth.operator.keys` | | file only | none: a list of `{id, secret_sha256}`, each an operator key's ULID, named once, and the lower-case hex SHA-256 of its secret. Each exchanges at `POST /api/v1/auth/token` for an operator token, which only the operator API under `/operator/v1` accepts |
| `auth.signing.kms_keys` | | file only | none; required by `kms`: a list of `{provider, ref}`, oldest first, whose newest key signs once it has been published for 48 hours. `awskms`: a key ID or key ARN of an `ECC_NIST_P256` `SIGN_VERIFY` key, not an alias. `gcpkms`: an `EC_SIGN_P256_SHA256` CryptoKeyVersion's resource name. `azurekv`: an EC P-256 key's ID with its version, `https://<vault>/keys/<name>/<version>`. `vault`: `<mount>/<key name>/<version>` of an `ecdsa-p256` Transit key, on the Vault `secrets.kek.vault.address` and `.auth` reach. Credentials are each KEK provider's. Every key is read at start, and one that cannot sign ES256 stops it, naming the key |
| `database.url` | | `INFRAWEAVE_DATABASE_URL` | required |
| `features.disabled` | | `INFRAWEAVE_FEATURES_DISABLED` (comma-separated) | none: the capabilities and options of [the catalogue](docs/implementation/tiers.md) switched off for every organization of the installation, read at start; an operation of one answers `403 feature-disabled`, and an unknown name, a limit, `billing` or `operator` fails startup |
| `secrets.kek.provider` | | `INFRAWEAVE_SECRETS_KEK_PROVIDER` | required by `serve`, `scheduler` and `all`: `local`, `awskms`, `gcpkms`, `azurekv` or `vault` |
| `secrets.kek.local.key_file` | | `INFRAWEAVE_SECRETS_KEK_LOCAL_KEY_FILE` | a file holding the KEK, base64 of 32 bytes; or the KEK itself in `INFRAWEAVE_SECRETS_KEK` |
| `secrets.kek.awskms.key_id`, `.region` | | `INFRAWEAVE_SECRETS_KEK_AWSKMS_KEY_ID` and so on | a KMS key ID, ARN or alias; the SDK's region; credentials from the AWS SDK's default chain |
| `secrets.kek.gcpkms.key_name` | | `INFRAWEAVE_SECRETS_KEK_GCPKMS_KEY_NAME` | a CryptoKey resource name; Application Default Credentials |
| `secrets.kek.azurekv.vault_url`, `.key_name`, `.key_version` | | `INFRAWEAVE_SECRETS_KEK_AZUREKV_VAULT_URL` and so on | an RSA key; the key's current version; `azidentity`'s default chain |
| `secrets.kek.vault.address`, `.mount`, `.key_name` | | `INFRAWEAVE_SECRETS_KEK_VAULT_ADDRESS` and so on | mount `transit`; a Transit key of an AEAD type, such as the default `aes256-gcm96`; the `vault` signing keys use the address and auth whatever the KEK provider |
| `secrets.rotation_interval` | | `INFRAWEAVE_SECRETS_ROTATION_INTERVAL` | `8760h` (365 days): the age at which the daily `secrets.rotate` task rotates the data key; `0` never rotates |
| `secrets.kek.vault.auth.method`, `.mount`, `.token_file`, `.role`, `.jwt_file`, `.role_id`, `.secret_id_file` | | `INFRAWEAVE_SECRETS_KEK_VAULT_AUTH_METHOD` and so on | `token`, from `token_file` or `VAULT_TOKEN`; `kubernetes` with `role` and `jwt_file` (`/var/run/secrets/kubernetes.io/serviceaccount/token`); `approle` with `role_id` and `secret_id_file`; the auth mount defaults to the method's name |
| `objectstore.driver` | | `INFRAWEAVE_OBJECTSTORE_DRIVER` | required by `serve`, `scheduler` and `all`: `s3`, `gcs`, `azureblob` or `fs` |
| `objectstore.s3.bucket`, `.endpoint`, `.region`, `.path_style` | | `INFRAWEAVE_OBJECTSTORE_S3_BUCKET` and so on | region `us-east-1`; credentials from the AWS SDK's default chain |
| `objectstore.s3.encryption`, `.kms_key_id` | | `INFRAWEAVE_OBJECTSTORE_S3_ENCRYPTION`, `INFRAWEAVE_OBJECTSTORE_S3_KMS_KEY_ID` | `none` (`s3`, or `kms` with a key) |
| `objectstore.gcs.bucket` | | `INFRAWEAVE_OBJECTSTORE_GCS_BUCKET` | Application Default Credentials |
| `objectstore.azureblob.account_url`, `.container` | | `INFRAWEAVE_OBJECTSTORE_AZUREBLOB_ACCOUNT_URL`, `INFRAWEAVE_OBJECTSTORE_AZUREBLOB_CONTAINER` | `azidentity`'s default chain |
| `objectstore.fs.root`, `.url` | | `INFRAWEAVE_OBJECTSTORE_FS_ROOT`, `INFRAWEAVE_OBJECTSTORE_FS_URL` | a directory every replica shares, and the API listener's public URL for signed URLs |
| `objectstore.buckets.<store>` | | `INFRAWEAVE_OBJECTSTORE_BUCKETS_RUN_LOGS` and so on | the driver's bucket; not with `fs` |
| `objectstore.retention.<store>` | | `INFRAWEAVE_OBJECTSTORE_RETENTION_RUN_LOGS` and so on | `run-logs` and `run-observability` `1440h`, `run-artifacts` `720h`, `workspaces` and `policy-inputs` `168h`, `inbox` `48h`, `uploads` `24h`; `0` keeps objects |
| `objectstore.part_size` | | `INFRAWEAVE_OBJECTSTORE_PART_SIZE` | `16777216` (16 MiB; at least 5 MiB) |
| `objectstore.signed_url_ttl` | | `INFRAWEAVE_OBJECTSTORE_SIGNED_URL_TTL` | `15m` |
| `vcs.ca_file` | | `INFRAWEAVE_VCS_CA_FILE` | none: a PEM file of certificate authorities trusted beside the system's when reaching a repository's host, for a host under a private CA; read at start |
| `log.format` | | `INFRAWEAVE_LOG_FORMAT` | `json` (or `text`) |
| `log.level` | `--log-level` | `INFRAWEAVE_LOG_LEVEL` | `info` (`debug`, `warn`, `error`) |

A flag beats the environment, which beats the YAML file named by
`--config`. The file is strict: an unknown key fails startup. Values are
sealed under a keyring of data keys in PostgreSQL, each wrapped by the KEK
([decision 0013](docs/research/decisions/0013-secrets-at-rest.md)); the
first replica on an empty database creates the first key, and a replica
whose KEK does not unwrap the keys refuses to start, naming the key.
`infraweave secrets` runs the key operations and exits:

- `infraweave secrets generate-kek <file>` writes a new local KEK to a file
  that does not exist yet, readable only by its owner.
- `infraweave secrets rotate` makes a new active data key now. Every
  replica reloads its keyring and seals under the new key; a scheduler's
  `secrets.reseal` job reseals every value and object key under it and
  deletes the old key. A second rotation is refused until that is done.
- `infraweave secrets rewrap --to <file>` rewraps every data key from the
  configured KEK to the one a YAML file of `secrets.kek` settings names,
  such as `provider: vault` with a `vault:` mapping, touching no value.
  Restart the replicas with the new settings afterwards.
Objects live under one bucket with a prefix per store, or a store's own
bucket, and each has a row in `blob_objects`: `infraweave` expires them
itself and deletes them only from the `scheduler` role, so `serve`'s
identity needs no delete permission
([decision 0005](docs/research/decisions/0005-persistence-postgresql.md#object-storage)).
On `SIGINT`
or `SIGTERM`, `/readyz` answers 503 at once; the listeners keep accepting
for `server.shutdown.ready_delay`, so load balancers stop routing to the
process, then stop accepting and drain in-flight requests for up to
`server.shutdown.timeout`, and the process exits 0. A second signal exits 1
at once. `SIGHUP` re-reads `log.level`, and no other setting. `infraweave version` prints
the build's version, commit and date.

Every request on the API listener applies
[decision 0003](docs/research/decisions/0003-api-conventions.md)'s
controls, whether its operation is implemented or a stub:

- **Rate limits.** Each caller has a token bucket per cost class: the
  operation's `x-infraweave-cost`, `standard` or `expensive`. A bucket holds
  `burst` requests and refills at `rate` a second. Every response carries
  `X-RateLimit-Limit`, `X-RateLimit-Remaining` and `X-RateLimit-Reset`, and
  an empty bucket answers `429` with `Retry-After`.
- **Idempotency keys.** A `POST` or `PATCH` with an `Idempotency-Key` runs
  once. A repeat replays the first response with `Idempotency-Replayed:
  true`; a key reused for another request answers `422`, one still in
  flight `409`. A `5xx` is not kept, so a retry runs again. A key still in
  flight after `server.idempotency.lock`, as when its replica crashed, is
  taken over by the next request with it. Searches ignore the key.
- **The operator switch.** A caller in `server.rate_limit.disabled_callers`,
  or in the `api_disabled_callers` table until its `until`, gets `503`
  `api-disabled`, and other callers are unaffected. The list is read at
  startup; a change to the table takes effect on every replica once
  `NOTIFY infraweave_invalidate, 'api_disabled_callers'` announces it.

The caller is the principal, `<kind>:<actor ID>`, so every token of one
API key, `api_key:<key ID>`, shares one budget and one set of keys, and
the disabled-callers list names callers so. `POST /auth/token`, which no
token reaches, counts against the client's IP address, behind a proxy the
proxy's. The buckets, keys
and switch live in PostgreSQL
([decision 0005](docs/research/decisions/0005-persistence-postgresql.md)),
so every replica on one database enforces one budget and sees the same
keys.

## Layout

| Path | What |
|---|---|
| `api/` | OpenAPI documents, embedded for tests and tools by `api/doc.go` |
| `cmd/<binary>/` | Entry points: thin shims over `internal/`, such as `cmd/infraweave`, one cobra file per process mode over `internal/app`; excluded from the coverage gate |
| `internal/` | Project-only packages, placed as [the repo layout](docs/implementation/repo-layout.md) says: the composition root in `internal/app`, shared value types in `internal/kernel`, infrastructure in `internal/platform` (`internal/platform/buildinfo` carries the version stamped in by `make build`), and the libraries behind `tools/` in `internal/devtools` |
| `pkg/` | Public, importable packages: the generated client `pkg/client/api` and the SDK over it, `pkg/sdk` |
| `tools/` | Developer tooling (`tools/specref`, `tools/openapi-bundle`); excluded from the coverage gate |
| `docs/` | Research and decision records |
| `test/acceptance/` | Gherkin features run by godog (`TestAccFeatures`) against the committed reference and the built `infraweave` binary: `features/<area>/`, each area's steps in `internal/steps/<area>/` and the harness in `internal/harness/`; a failed scenario leaves its logs, configuration and database dump in `test/acceptance/out/<scenario>/` |
| `test/deps/` | The Docker dependencies of every `TestAcc` test, PostgreSQL, the object store, Vault, Gitea and the OIDC issuer, started by testcontainers from the digests in `images.go` |
| `test/fixtures/captures/` | Redacted exchanges with Spacelift's real API, each with the REST calls that stand for it; `TestAccCaptures` replays them and `TestUnitCaptureRedaction` refuses credentials ([layout](test/fixtures/captures/README.md)) |
| `test/depguard/` | A module of its own that breaks each depguard rule once, for `make lint-fixtures` |

## Make targets

| Target | What |
|---|---|
| `make gate` | Everything CI runs but its `FIPS=1` rerun: docs-check, vet, lint, lint-fixtures, test, accept, verify-parity, verify-reference, verify-generate, cover, vuln, build |
| `make docs-check` | Fails on a broken link under `docs/`, a research report without its header or sources, a decision without a valid status or index row, a stale `docs/…` path anywhere in the repository, a register under `docs/data/` that fails its schema or cites a missing ID, a reference spec tag with no record in `docs/data/capabilities.json`, a superseded `operations` entry that is not an operation of its product's reference, a superseded `enumValues` or `fields` entry that is not a value or field of its type in Deploy's GraphQL inventory, or a report with *(unverified)* claims that no record of `docs/data/open-questions.json` lists, or a Go package under `internal/` or `pkg/` without a `doc.go` that has its `# Input and output` and `# Dependencies` sections |
| `make test` | Every `TestUnit…` test in the module, with `-race -shuffle=on`, coverage to `cover/unit` |
| `make accept` | Every `TestAcc…` test: the Gherkin features (godog) and the suites beside the code, such as specref's fetcher tests; coverage, the subprocess's included, and `cucumber.json` to `cover/accept`. `PARTITION=core\|runs\|integrations` runs one CI partition, as the `accept-<partition>` jobs do: `core` runs every `TestAcc…` test and the other two only their scenarios, and each job reports how many scenarios it ran, or that its partition has none yet. Without Docker, scenarios that need it are skipped and listed; `INFRAWEAVE_ACCEPT_REQUIRE_DOCKER=1`, as CI sets, fails them instead. `INFRAWEAVE_POSTGRES=next` runs `@postgres` and the `sqltest` suites on the next PostgreSQL major instead of the floor, as the `postgres-next` job does with `PARTITION=core`; that job reports the version it ran and never blocks a merge. `INFRAWEAVE_ACCEPT_SEED` repeats the printed scenario order. `TestUnitTestNames` fails on a test named neither way ([decision 0016](docs/research/decisions/0016-testing-and-acceptance.md#test-kinds)) |
| `make test FIPS=1`, `make accept FIPS=1` | The same tests with every binary, the one under acceptance included, built with the certified FIPS 140-3 Go Cryptographic Module (`GOFIPS140=certified`) and every process run with `GODEBUG=fips140=only`, as the required `fips-only` job runs `make test` and every partition: a call to MD5, SHA-1 or another algorithm the mode refuses fails, in a test or a library as in product code. Its coverage is not merged. `infraweave version` prints the binary's FIPS 140-3 mode and module version |
| `make scaffold TAG=Stacks FAMILY=stacks` | Copies one reference tag (Deploy's, or Flows' with `PRODUCT=flows`) into `api/src/api/paths-<family>.yaml` with the decision 0003 conventions, then bundles |
| `make parity` | Prints, for Deploy and then Flows, each reference operation's outcome (served, served with divergence, superseded, missing) and the infraweave-only operations by tag, and how they differ from `api/parity-baseline.<product>.json` |
| `make verify-parity` | Run by the `parity` job over the merged `cucumber.json` of every partition. Fails, for either product, on an unrecorded divergence, a served superseded operation, a stale baseline, or a `docs/data/capabilities.json` state that `cover/accept/cucumber.json` does not show |
| `make parity-baseline` | Rewrites both `api/parity-baseline.<product>.json` to accept the current parity report, keeping each recorded reason; review the diff |
| `make bundle` | Merges `api/src/<service>/` fragments into `api/<service>.openapi.yaml` |
| `make generate` | Bundles, then generates each strict chi server (`internal/platform/gen/<service>`), Go client (`pkg/client/<service>`) and 501 stubs |
| `make verify-generate` | Fails if the committed bundles or generated code are stale |
| `make reference-fetch` | Refreshes each product's snapshots at its lock's pins (needs network, and `terraform` for Deploy); `PRODUCTS=flows` narrows it to one |
| `make reference` | Regenerates each product's spec and `coverage.md` from its snapshots |
| `make reference-inventory` | Regenerates the GraphQL inventory register (`docs/data/graphql-inventory.json`) and report 29, which summarises it (`docs/research/reference/29-data-model-inventory.md`), from the introspection snapshot, and the Flows SDK register (`docs/data/flows-sdk-inventory.json`) from the SDK's identifiers |
| `make verify-reference` | Fails if either committed reference, report 29 or either inventory register is not exactly what the snapshots generate |
| `make cover` | Merges every `cover/*` directory and enforces `.testcoverage.yml` (≥95% total, ≥90% per package) |
| `make lint` / `make fmt` | golangci-lint with `.golangci.yml` over the whole tree, including depguard's [import rules](docs/implementation/repo-layout.md#depguard-rules) and the `time.Sleep` ban in tests |
| `make lint-fixtures` | Lints the `test/depguard` module, whose packages break each import rule once, and fails unless the findings are exactly `test/depguard/expected.txt` |
| `make build` | Cross-compiles every `cmd/*` binary for six platforms, CGO disabled |

Three scheduled workflows report beside the gate and never block a
merge; each opens an issue on failure, or comments on the open one.
`nightly.yml` runs every acceptance partition on `main`.
`objectstore-cloud.yml` runs the object store contract (`objectstoretest`)
each night against real Amazon S3, Google Cloud Storage and Azure Blob
Storage, as `serve`'s writer identity, whose delete the cloud must refuse,
and as the `scheduler`'s deleter, through GitHub's OIDC token with no
stored key; the issue is per cloud.
[`test/terraform/cloud`](test/terraform/cloud/README.md) provisions the
buckets and the trust. `vcs-lab.yml` makes private repositories on
GitHub, GitLab, Bitbucket Cloud, Azure DevOps and a Bitbucket Data Center
container each night, runs every provider's source contract and a stack
through `infraweave` against them, and destroys them; the issue is per
provider. [`test/terraform/vcs`](test/terraform/vcs/README.md) holds its
Terraform. A cloud or provider without its environment secrets is
skipped, and the run's summary names what it lacks.

## Dependencies stay at latest

| Piece | What it moves | When |
|---|---|---|
| `deps-refresh.yml` | The `go` directive (latest stable Go), every module and every `go.mod` tool, and `.github/golangci-lint-version` | Daily and on demand |
| Dependabot | Go modules and GitHub Actions pins, one grouped PR per ecosystem | Daily |
| `auto-merge.yml` | Merges either kind of PR once the quality gate has passed on that exact commit | When the gate finishes |

## Licence notices

The committed Spacelift reference includes material from MIT-licensed
Spacelift repositories; see `api/reference/spacelift/NOTICE`.
