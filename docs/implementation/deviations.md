# Deviations from Spacelift

infraweave reproduces Spacelift's feature set behind its own REST API
([0001](../research/decisions/0001-rest-first-spacelift-surface.md)). It is built
with Spacelift's design in view, and where Spacelift's interface does not fit a
self-hosted REST product, or where the research shows a limit worth avoiding,
infraweave deliberately does something else. This register lists every such
departure in one place, so a reader comparing infraweave with Spacelift does not
have to collect them from twenty decision records.

Every row is a design commitment of an Accepted or Proposed decision record.
Availability is the [build status](plan.md#build-status) of the phase that
delivers it; phases in [plan.md](plan.md) cite the `DV-NNN` rows their tasks
implement. A Proposed record can still change, and with it the rows it names
(see [pending deviations](#pending-deviations)).

## Scope

- **A deviation** is an infraweave behaviour or interface that differs from
  Spacelift on purpose: a different wire shape, a different default, a stricter
  rule, an added operation or header, or a different deployment artefact.
- **Operation-level divergences** are also recorded in the served spec: each
  operation that differs from the reference lists its differences in
  `x-infraweave-divergence`, and `specref parity` fails on a difference without
  one ([0016](../research/decisions/0016-testing-and-acceptance.md#parity-checks-against-the-specification-reference)).
  This register holds the rule; the spec holds each operation's instance of it.
- **Not deviations:** interfaces infraweave does not serve because a newer
  Spacelift interface supersedes them. They follow
  [0021](../research/decisions/0021-capability-scope-and-supersession.md)'s
  supersession test and live in the
  [supersession register](capability-roadmap.md#supersession-register). Missing
  capabilities and 501 stubs are tracked by their owning phase in the
  [capability roadmap](capability-roadmap.md), not here.
- **Choices where Spacelift is silent** are deviations only when a record names
  them as such; the record's [I] or *(unverified)* basis and its capture are in
  [report 30](../research/reference/30-open-questions.md#captures-needed).
- **Client impact** is stated against Spacelift's own clients and integrations:
  spacectl, `terraform-provider-spacelift`, direct GraphQL callers, webhook and
  audit consumers, runner images, launchers and agents. Decision 0001 already
  rules out re-pointing Spacelift's API clients unchanged, so for those rows the
  impact says what a port must change.

## Adding an entry

Add a row in the pull request that introduces the divergence, under the record
that decides it, with the next free number. The record lists the departure in
its Rationale and the row links back to it.

| Field | Content |
|---|---|
| ID | `DV-NNN`, numbered across the whole file and never reused |
| Spacelift | What Spacelift does, as a report records it. A behaviour the reports mark *(unverified)* says so |
| infraweave | What infraweave does instead, with exact names of settings, routes and fields |
| Why | The limit avoided or the property gained |
| Client impact | `None`, with the reason, or what a Spacelift client, an operator or a script must change |
| Source | The decision record, linked to the section that names the departure |

## Register

### API surface: decisions 0001 and 0003

| ID | Spacelift | infraweave | Why | Client impact | Source |
|---|---|---|---|---|---|
| DV-001 | One GraphQL endpoint with 285 queries and 380 mutations ([report 02](../research/reference/02-graphql-to-rest-mapping.md)) | REST over JSON described by OpenAPI 3 (`api/deploy.openapi.yaml`); no GraphQL endpoint and no GraphQL facade. The reference spec projects every root field to a route, and the served spec is scaffolded from it family by family | Standard HTTP status codes, caching, per-route authorization and rate limits; the chi and oapi-codegen toolchain; Spacelift's clients use fixed selections, so GraphQL's client-shaped queries buy nothing | spacectl, `terraform-provider-spacelift`, the Bruno collection and direct GraphQL callers cannot talk to infraweave; a port changes transport, paths and bodies. Migration from Spacelift is a data import | [0001 Decision](../research/decisions/0001-rest-first-spacelift-surface.md#decision) |
| DV-002 | spacectl and `terraform-provider-spacelift` are the clients | infraweave's own clients: a REST-native Terraform provider and `infraweavectl`, both generated over `pkg/client/deploy` and checked against Spacelift's provider schema and spacectl's command list ([0016](../research/decisions/0016-testing-and-acceptance.md#the-terraform-provider-parity-oracle)) | Follows from DV-001; a client that speaks GraphQL cannot reach a REST server | Terraform configurations change provider source and any renamed attributes recorded in `api/reference/provider-parity.yaml`; scripts move from `spacectl` to `infraweavectl` | [0001 Decision](../research/decisions/0001-rest-first-spacelift-surface.md#decision) |
| DV-003 | No retry-safety mechanism for mutations; a create that times out cannot be retried without risking a duplicate | `POST` and `PATCH` accept an optional `Idempotency-Key` header (1 to 255 characters), scoped to the caller and kept 24 hours by default. A finished request with the same fingerprint replays with `Idempotency-Replayed: true`; a different fingerprint answers `422` `idempotency-key-reuse`; one in flight answers `409` `idempotency-key-in-flight` | Terraform providers and CI scripts retry on network errors; only the client knows whether two requests are one intent | None for a client that sends no key; a retrying client opts in by sending one | [0003 Constraints](../research/decisions/0003-api-conventions.md#constraints) |
| DV-004 | No published load contract per caller | One token bucket per caller per cost class, `standard` or `expensive`, named per operation in `x-infraweave-cost`. Every response carries `X-RateLimit-Limit`, `X-RateLimit-Remaining` and `X-RateLimit-Reset`; an empty bucket answers `429` `rate-limited` with `Retry-After` | Searches, batches and run triggers cost far more than reads; a visible budget lets clients slow down before they are throttled | Clients honour `429` and `Retry-After`; fan-out callers see the smaller `expensive` budget | [0003 Constraints](../research/decisions/0003-api-conventions.md#constraints) |
| DV-005 | No per-caller API switch for the operator | The operator can disable the API for one caller, who receives `503` `api-disabled` with `Retry-After`; other callers are unaffected | Shedding one misbehaving caller without shedding everyone | None unless the operator disables the caller | [0003 Constraints](../research/decisions/0003-api-conventions.md#constraints) |
| DV-006 | 95 list fields return bare arrays, such as `GET /stacks` in the reference | Every list `GET` takes `limit` (default 20, maximum 100, clamped) and an opaque `cursor`, and returns a `<Type>Page` with `items` and `nextCursor`, present only when another page exists; no total count. Cursors are keyset positions that never expire; a bad one answers `400` `invalid-cursor`. `POST .../search` keeps relay-style `first`, `after` and `pageInfo` | Unbounded lists load everything at once; offset paging is O(n) and unstable under writes | A port pages until `nextCursor` is absent instead of reading one array | [0003 Constraints](../research/decisions/0003-api-conventions.md#constraints) |
| DV-007 | Error identity is message text in GraphQL `errors` | Each problem `type` is a URN, `urn:infraweave:problem:<slug>`, from one registry of slugs (0003's five, extended by DV-084) | RFC 9457 lets clients match on `type` without dereferencing it, and infraweave has no documentation host to resolve a URL to | Clients branch on the `type` slug; see DV-084 for the text kept for ported clients | [0003 Decision](../research/decisions/0003-api-conventions.md#decision) |

### Architecture and persistence: decisions 0004 and 0005

| ID | Spacelift | infraweave | Why | Client impact | Source |
|---|---|---|---|---|---|
| DV-008 | Ships the launcher as a separate binary and image that embeds the worker binary ([report 10](../research/reference/10-workers-and-execution.md)) | The launcher is the `worker` role of the one `infraweave` binary, and the phase executor inside the run container is the hidden `worker exec` role of the same static binary, which the launcher mounts | Launcher, executor and server share a version by construction; the worker's package graph stays free of server code, so a separate worker build remains a one-file change | Worker hosts run `infraweave worker` in place of Spacelift's launcher image; custom runner images need no embedded worker binary | [0004 Rationale](../research/decisions/0004-repository-architecture.md#rationale) |
| DV-009 | Self-hosted supports PostgreSQL 14 to 18 ([report 24](../research/reference/24-self-hosted-reference.md)) | PostgreSQL 15 is the floor | 14 leaves community support within days of the research baseline; 15 brings `UNIQUE NULLS NOT DISTINCT` and `MERGE` | An operator on PostgreSQL 14 upgrades before installing | [0005 Rationale](../research/decisions/0005-persistence-postgresql.md#rationale) |
| DV-010 | Self-hosted provisions one bucket per purpose | One bucket with a prefix per store by default; a per-store override keeps separate buckets where policy requires them | Eleven buckets are eleven pieces of infrastructure to provision before a first run | Operators may provision one bucket; existing per-purpose buckets map through the override | [0005 Rationale](../research/decisions/0005-persistence-postgresql.md#rationale) |
| DV-011 | Relies on bucket lifecycle rules for expiry and asks for bucket versioning on the states bucket | infraweave writes every object under an immutable key, records it in `blob_objects`, and expires and versions objects itself; state history needs no bucket versioning. A lifecycle rule is at most an operator backstop set longer than the store's retention | Retention and state history become database facts the API can list and test; the local-disk driver and minimal S3-compatible stores need neither feature | Operators drop versioning and lifecycle requirements from the bucket; tooling that read old state versions from bucket versions reads them through the API | [0005 Rationale](../research/decisions/0005-persistence-postgresql.md#rationale), [0010 Rationale](../research/decisions/0010-state-backend.md#rationale), [0020 Constraints](../research/decisions/0020-reference-deployments-and-packaging.md#constraints) |

### Identity and access: decisions 0006 and 0007

| ID | Spacelift | infraweave | Why | Client impact | Source |
|---|---|---|---|---|---|
| DV-012 | The in-run API token's issuer is Spacelift's | `iss` defaults to `spacelift` and is configurable; the other claim formats match Spacelift's by default, with configurable templates | Keeps code such as `spacelift_current_stack` working while letting an operator brand the issuer | None at the default; trust policies change only if the operator changes the issuer | [0006 Rationale](../research/decisions/0006-authentication-and-tokens.md#rationale) |
| DV-013 | `ApiKeyInput.admin`, a required `Boolean!`, makes an admin key; the changelog deprecates it and the schema does not ([report 14](../research/reference/14-spaces-rbac-and-identity.md#81-types-and-fields)) | The field is not accepted: a body that sets it answers `400` `invalid-request` like any unknown field. A key becomes an administrator through a `space-admin` role binding on `root`, and the key's `admin` reads whether that binding exists | One source of truth for access; decision 0021 treats the changelog-deprecated field as superseded | A port drops the field, which Spacelift's provider always sends as `false`, and binds the key to `space-admin` on `root` instead | [0006 Rationale](../research/decisions/0006-authentication-and-tokens.md#rationale) |
| DV-014 | Fetches OIDC API key issuers as configured | Issuers are fetched over HTTPS only, optionally restricted to `auth.oidc_api_keys.allowed_issuers` | A self-hosted server fetching user-supplied URLs is an SSRF path | OIDC API keys whose issuer is plain HTTP, or outside a configured allowlist, are refused | [0006 Rationale](../research/decisions/0006-authentication-and-tokens.md#rationale) |
| DV-015 | Self-hosted documents no rotation of its JWT signing key ([report 24](../research/reference/24-self-hosted-reference.md#35-encryption-and-kms)) | Signing keys rotate automatically every 90 days | Rotation limits key exposure | Relying parties read the JWKS document rather than pinning one key | [0006 Rationale](../research/decisions/0006-authentication-and-tokens.md#rationale) |
| DV-016 | Does not document reissue of the federation token during a run ([report 12](../research/reference/12-cloud-integrations-and-oidc.md)) | The federation token file is refreshed during long runs | A run past one hour otherwise loses cloud access | None; runs longer than the token lifetime keep their cloud credentials | [0006 Rationale](../research/decisions/0006-authentication-and-tokens.md#rationale) |
| DV-017 | Browser session lifetime is undocumented | Session lifetimes are stated, configurable defaults | A self-hosted operator needs a known value | None | [0006 Rationale](../research/decisions/0006-authentication-and-tokens.md#rationale) |
| DV-018 | Seat limits by plan | No seat accounting for API keys; tier and seat answers follow DV-079 | infraweave has no commercial plans | None | [0006 Rationale](../research/decisions/0006-authentication-and-tokens.md#rationale) |
| DV-019 | The `legacy` space carries access-policy behaviour | `legacy` is an ordinary space with no special semantics | Access policies, its only special behaviour, are superseded | None for clients; imported access policies follow the supersession register | [0007 Rationale](../research/decisions/0007-authorization-spaces-and-roles.md#rationale) |
| DV-020 | System role contents are not published ([report 14](../research/reference/14-spaces-rbac-and-identity.md)) | System role contents are derived from the legacy fallback column of the action catalogue; parity is checked against `Role.actions` once captured | A role either grants an action or the fallback level decides, as one set-membership test | A system role's action list may differ from Spacelift's until the capture lands | [0007 Rationale](../research/decisions/0007-authorization-spaces-and-roles.md#rationale) |
| DV-021 | The worker pool controller and drift detection system roles' slugs are unrecorded | Their slugs are `worker-pool-controller` and `drift-detection-manager` | A slug is needed to bind them | Configurations that name Spacelift's slugs for these roles may need the name changed | [0007 Rationale](../research/decisions/0007-authorization-spaces-and-roles.md#rationale) |
| DV-022 | The schema marks some actions "do not use" | Disabled actions cannot be assigned to roles; operations they would govern use their fallback | A role must not depend on an action that grants nothing | A role create or update naming a disabled action is rejected | [0007 Rationale](../research/decisions/0007-authorization-spaces-and-roles.md#rationale) |
| DV-023 | Two statements conflict on whether an `ACTIVE` managed user with no binding can log in | Such a user logs in with empty grants | The newer changelog statement wins | None | [0007 Rationale](../research/decisions/0007-authorization-spaces-and-roles.md#rationale) |

### Runs, workers and policies: decisions 0008 and 0009

| ID | Spacelift | infraweave | Why | Client impact | Source |
|---|---|---|---|---|---|
| DV-024 | Workers talk MQTT or HTTP long-poll; self-hosted runs a built-in broker or bridges to IoT Core, and keeps an `iot` queue for worker messages | HTTP long-poll only, under `/worker/v1`; no MQTT transport, broker listener or IoT Core bridge, and no `iot` queue | Spacelift retires MQTT for workers on 2027-03-30; a broker adds a port, a component and a second delivery path; Spacelift's launchers cannot reach infraweave anyway (DV-001) | Worker networks need outbound HTTPS to `serve` only; load balancer idle timeouts must exceed the poll hold by 15 seconds | [0008 Rationale](../research/decisions/0008-run-execution-and-worker-protocol.md#rationale), [0011 Rationale](../research/decisions/0011-persistent-work-queue.md#rationale), [0020 Rationale](../research/decisions/0020-reference-deployments-and-packaging.md#rationale) |
| DV-025 | Leaves a run alone while a worker executes it, for up to 30 days | A worker that stops polling for three minutes is declared lost and its run fails, releasing the stack | The minute poll tells the server when a worker is gone; a stack should not stay locked behind a dead host | A run on a crashed worker fails and can be retried at once instead of holding the stack | [0008 Rationale](../research/decisions/0008-run-execution-and-worker-protocol.md#rationale) |
| DV-026 | Defaults to Spacelift's own runner images | Defaults to infraweave's own runner images; the shared image contract (UID 1983, the same configuration precedence) keeps Spacelift's images usable | An installation does not depend on another vendor's registry | None for stacks that name an image; stacks on the default get infraweave's | [0008 Rationale](../research/decisions/0008-run-execution-and-worker-protocol.md#rationale) |
| DV-027 | Launcher settings are `SPACELIFT_*` environment variables | Launcher settings are named `INFRAWEAVE_LAUNCHER_*` | The launcher is infraweave's client, not Spacelift's | Worker deployment configuration renames its variables | [0008 Rationale](../research/decisions/0008-run-execution-and-worker-protocol.md#rationale) |
| DV-028 | `REGO_V0` is the provider's default engine; the API's default is unverified | `REGO_V1` is the default when a create omits the engine | Rego v1 is OPA's current language | A policy written for v0 sets the engine explicitly | [0009 Rationale](../research/decisions/0009-policy-engine-opa.md#rationale) |
| DV-029 | Removes `http.send`, `opa.runtime`, `rego.parse_module`, `time.now_ns` and `trace` from policies | Also removes `net.lookup_ip_addr` | DNS lookups from the server reach the network the other removals keep policies away from | A policy using `net.lookup_ip_addr` fails to compile at save | [0009 Rationale](../research/decisions/0009-policy-engine-opa.md#rationale) |
| DV-030 | Documents no outcome for a policy evaluation error | Each policy type has a stated fail-closed outcome; an `APPROVAL` error leaves the run waiting | A policy that cannot be evaluated must not let a plan apply or a user log in | None for policies that evaluate; a broken policy blocks rather than passes | [0009 Rationale](../research/decisions/0009-policy-engine-opa.md#rationale) |
| DV-031 | The `sanitized` helper's truncation of SHA-256 is unverified ([report 20](../research/reference/20-vendors.md#13-resource-sanitization)) | `sanitized` uses infraweave's own truncation | A rule is needed until a capture shows Spacelift's | A policy comparing sanitized values with strings produced by Spacelift may not match | [0009 Rationale](../research/decisions/0009-policy-engine-opa.md#rationale) |

### State and queue: decisions 0010 and 0011

| ID | Spacelift | infraweave | Why | Client impact | Source |
|---|---|---|---|---|---|
| DV-032 | External state access uses the Terraform `remote` backend | External access uses the Terraform `http` backend with a token | The `remote` API is a second surface with its own JSON:API shapes and login flow; `http` already serves `terraform_remote_state` and `terraform state pull` | Configurations that read state from outside change to `backend "http"` | [0010 Rationale](../research/decisions/0010-state-backend.md#rationale) |
| DV-033 | State upload and download URLs point at the object store | They point at infraweave, which proxies and keeps state sealed at rest | The server holds the encryption; states are small enough to proxy | Worker networks need no route to the bucket for state | [0010 Rationale](../research/decisions/0010-state-backend.md#rationale) |
| DV-034 | The lock kind taken while planning is undocumented | Planning takes a shared lock | Concurrent read-only plans need not serialise | None | [0010 Rationale](../research/decisions/0010-state-backend.md#rationale) |
| DV-035 | A stopped run can leave a state lock behind | A run's locks are released when it ends | No stuck lock needs a manual unlock | None; manual unlock is rarely needed | [0010 Rationale](../research/decisions/0010-state-backend.md#rationale) |
| DV-036 | SaaS keeps non-current state versions for 30 days | Non-current versions are kept forever by default; retention is configurable | State history is the rollback path, and storage is the operator's | Storage grows until the operator sets a retention | [0010 Rationale](../research/decisions/0010-state-backend.md#rationale) |
| DV-037 | Self-hosted offers SQS or a PostgreSQL queue | The PostgreSQL queue only | Enqueue joins the change's transaction, which SQS cannot | Operators provision no SQS queues | [0011 Rationale](../research/decisions/0011-persistent-work-queue.md#rationale), [0005 Rationale](../research/decisions/0005-persistence-postgresql.md#rationale) |
| DV-038 | A fixed receive count of 3 before dead-lettering | A retry limit per job kind, default 5, with exponential backoff and full jitter | Different kinds tolerate different numbers of failures | None | [0011 Rationale](../research/decisions/0011-persistent-work-queue.md#rationale) |
| DV-039 | Separate dead-letter queues | A `dead` job state kept 30 days, listed and acted on through three operations outside the Spacelift surface: `GET /admin/queue/jobs`, `POST /admin/queue/jobs/{id}/retry`, `DELETE /admin/queue/jobs/{id}` | Dead work is inspectable and retryable in place | New operator API; no Spacelift client uses it | [0011 Rationale](../research/decisions/0011-persistent-work-queue.md#rationale), [0011 Decision](../research/decisions/0011-persistent-work-queue.md#decision) |
| DV-040 | A separate FIFO queue | Ordering keys on jobs | Ordering applies per key, not per queue | None | [0011 Rationale](../research/decisions/0011-persistent-work-queue.md#rationale) |

### VCS: decision 0012

| ID | Spacelift | infraweave | Why | Client impact | Source |
|---|---|---|---|---|---|
| DV-041 | SaaS installs Spacelift's GitHub Marketplace app | The operator registers a GitHub App, as self-hosted Spacelift requires | infraweave has no global app of its own | Operators create and register an app; repositories install that app | [0012 Rationale](../research/decisions/0012-vcs-integration-model.md#rationale) |
| DV-042 | Spacelift's own webhook endpoints | Webhook URLs under `/webhooks/vcs/` | One route family for every provider | VCS webhook configurations point at the new path | [0012 Rationale](../research/decisions/0012-vcs-integration-model.md#rationale) |
| DV-043 | One webhook secret at a time | The previous secret is accepted for a window after rotation | Rotation without dropped deliveries | None | [0012 Rationale](../research/decisions/0012-vcs-integration-model.md#rationale) |
| DV-044 | No delivery listing or replay | Routes to list received deliveries and replay one | Debugging a missed trigger without the provider's console | New operations; no Spacelift client uses them | [0012 Rationale](../research/decisions/0012-vcs-integration-model.md#rationale) |
| DV-045 | VCS agents speak gRPC to a separate gateway service with its own load balancer | `infraweave vcs-agent` long-polls `serve` over HTTPS with its own token layout; no gateway process and no gRPC listener | Reuses the worker protocol's shape and any HTTPS load balancer | `spacelift-vcs-agent` does not connect; agent pools run `infraweave vcs-agent` | [0012 Rationale](../research/decisions/0012-vcs-integration-model.md#rationale), [0020 Rationale](../research/decisions/0020-reference-deployments-and-packaging.md#rationale) |
| DV-046 | The docs promise a strict request allowlist; the released agent at the research pin validates nothing | The allowlist is on by default, derived from the client's own route table, and the agent refuses unmatched Azure DevOps requests | Without validation the agent is a general proxy into a private network | Integrations that relied on unvalidated proxying fail until their route is listed | [0012 Rationale](../research/decisions/0012-vcs-integration-model.md#rationale) |
| DV-047 | The individual commit check name is undocumented | `spacelift/<stack slug>` | A stable name is needed for branch protection | Branch protection rules copied from Spacelift may need the check name changed | [0012 Rationale](../research/decisions/0012-vcs-integration-model.md#rationale) |

### Secrets: decision 0013

| ID | Spacelift | infraweave | Why | Client impact | Source |
|---|---|---|---|---|---|
| DV-048 | The choice between KMS and an RSA key is permanent; only KMS keys rotate, yearly | `infraweave secrets rewrap` moves the keyring between a local key, KMS providers or a new key in a few statements | Operators change custody without a reinstall | None | [0013 Rationale](../research/decisions/0013-secrets-at-rest.md#rationale) |
| DV-049 | Kubernetes deployments can use only RSA | GCP KMS, Azure Key Vault and Vault are offered beside AWS KMS | Every reference deployment gets a managed key option | None | [0013 Rationale](../research/decisions/0013-secrets-at-rest.md#rationale) |
| DV-050 | In the local-key mode one RSA key both encrypts and signs | The JWT signing key is a separate sealed row, apart from the encryption root | Rotating one never touches the other | None | [0013 Rationale](../research/decisions/0013-secrets-at-rest.md#rationale) |
| DV-051 | Documents no minimum length for masked values | Values shorter than 4 bytes are not masked in run logs | Masking `1` or `on` destroys the log and protects nothing | A secret shorter than 4 bytes appears in logs; such secrets should be lengthened | [0013 Rationale](../research/decisions/0013-secrets-at-rest.md#rationale) |

### Registry: decision 0014

| ID | Spacelift | infraweave | Why | Client impact | Source |
|---|---|---|---|---|---|
| DV-052 | Splits registry discovery (`spacelift.io`) from the protocol host (`app.spacelift.io`) | Discovery and the protocol share the installation's host | One host to deploy, certify and allow | Module and provider source addresses use the installation host | [0014 Rationale](../research/decisions/0014-module-and-provider-registry.md#rationale) |
| DV-053 | A failed provider verification blocks neither publishing nor use | Publishing a provider version checks that every uploaded file exists and matches its declared checksum | A bad upload fails at publish, not at `terraform init` | Publishing pipelines see an error for a bad upload | [0014 Rationale](../research/decisions/0014-module-and-provider-registry.md#rationale) |
| DV-054 | Labels registry protocol 404 bodies `text/plain` | Serves them as `application/json` | The body is JSON | None for Terraform and OpenTofu | [0014 Rationale](../research/decisions/0014-module-and-provider-registry.md#rationale) |
| DV-055 | Behaviour for yanked versions is uncaptured | Yanked versions are neither listed nor downloadable, even by an exact pin | A yanked version is withdrawn | An exact pin to a yanked version fails to install | [0014 Rationale](../research/decisions/0014-module-and-provider-registry.md#rationale) |

### Events: decision 0015

| ID | Spacelift | infraweave | Why | Client impact | Source |
|---|---|---|---|---|---|
| DV-056 | Its own delivery headers | Adds `X-Infraweave-Delivery` and `X-Infraweave-Event` to every HTTP delivery | Receivers can deduplicate and route without parsing the body | None; receivers may use them | [0015 Rationale](../research/decisions/0015-events-notifications-webhooks-audit.md#rationale) |
| DV-057 | `retryOnFailure` retries `5xx` | Also retries network errors, timeouts and `429` | A receiver that is down or throttling is a transient failure | Receivers may see more retries and deduplicate by `X-Infraweave-Delivery` | [0015 Rationale](../research/decisions/0015-events-notifications-webhooks-audit.md#rationale) |
| DV-058 | Fixed audit, notification and delivery retention | Each retention is an operator setting | Storage and compliance needs are the operator's | None | [0015 Rationale](../research/decisions/0015-events-notifications-webhooks-audit.md#rationale) |
| DV-059 | Raises worker usage events for negotiated contracts | Requires `events.worker_usage.limit` and `events.worker_usage.percentile`; without a limit the event is never raised | infraweave has no contracts | Consumers of the usage event configure a limit first | [0015 Rationale](../research/decisions/0015-events-notifications-webhooks-audit.md#rationale) |

### Observability: decision 0017

| ID | Spacelift | infraweave | Why | Client impact | Source |
|---|---|---|---|---|---|
| DV-060 | Selects a `Datadog` or `AWS` observability vendor | OTLP is the only export; Datadog and X-Ray are reached through OTLP-capable agents | Each vendor SDK adds dependencies and code paths to test | Operators point an OTLP collector or agent at infraweave | [0017 Rationale](../research/decisions/0017-observability-and-health.md#rationale) |
| DV-061 | A separate Prometheus exporter process queries the API with an API key | infraweave serves the exporter's metrics itself at `/metrics/spacelift` | No extra process, credential or per-replica duplication | Scrape configurations target infraweave instead of the exporter | [0017 Rationale](../research/decisions/0017-observability-and-health.md#rationale) |
| DV-062 | Probes are `/readiness` and `/startup` on `:8080` | Probes are `/livez`, `/readyz` and `/startupz` on `:9090`; `:8080` is the API listener | Probes stay off the public listener | Orchestrator probe configuration changes | [0017 Rationale](../research/decisions/0017-observability-and-health.md#rationale) |
| DV-063 | Workers upload traces, CPU profiles and logs to a run observability bucket | Workers export traces over OTLP to the operator's collector | One telemetry path, owned by the operator | Operators run a collector the workers can reach | [0017 Rationale](../research/decisions/0017-observability-and-health.md#rationale) |
| DV-064 | Logs queries per request | No per-request query logging at `info`; the access log records the route pattern, never the body | Bodies carry secrets and personal data | Log pipelines that parsed query logs lose them | [0017 Rationale](../research/decisions/0017-observability-and-health.md#rationale) |

### Flows: decision 0018

| ID | Spacelift | infraweave | Why | Client impact | Source |
|---|---|---|---|---|---|
| DV-065 | Flows is a separately linked organization with its own login | One Flows organization per infraweave account, sharing its identity; the Deploy link is implicit | One identity and access model across the product | No linking step; Flows users are the account's users | [0018 Rationale](../research/decisions/0018-flows-runtime.md#rationale) |
| DV-066 | HTTP Endpoint and MCP surfaces use subdomains (`mcp.`, `oauth.`) | Endpoint addresses fall back to paths when no wildcard domain is configured; MCP is served at `/flows/mcp` | A self-hosted install may have no wildcard DNS | Clients configured with Spacelift's subdomains use the paths | [0018 Rationale](../research/decisions/0018-flows-runtime.md#rationale) |
| DV-067 | A GraphQL Request Deploy block | An API Request block over the REST API | infraweave serves no GraphQL (DV-001) | Flows using the GraphQL block are rewritten to the API Request block | [0018 Rationale](../research/decisions/0018-flows-runtime.md#rationale) |
| DV-068 | Self-hosted Flows runs on EKS only | Flows runs wherever infraweave runs; agents run in Kubernetes or Docker | Every reference deployment carries Flows | None | [0018 Rationale](../research/decisions/0018-flows-runtime.md#rationale) |
| DV-069 | `flowctl` and `terraform-provider-flows` call RPC-style `/cli/...` and `/provider/...` routes | The Flows API is REST under `/flows/v1/`; infraweave's CLI and provider replace both clients | Decision 0001's REST rule applies to every family | `flowctl` and `terraform-provider-flows` do not work; their operations remain the parity list | [0018 Rationale](../research/decisions/0018-flows-runtime.md#rationale) |

### Intelligence: decision 0019

| ID | Spacelift | infraweave | Why | Client impact | Source |
|---|---|---|---|---|---|
| DV-070 | Intent's `discover`, `query` and `mutate` tools work over GraphQL | They work over infraweave's OpenAPI operations | infraweave serves no GraphQL (DV-001) | MCP clients see OpenAPI operation names in tool results | [0019 Rationale](../research/decisions/0019-ai-provider-boundary.md#rationale) |
| DV-071 | The provided AI integration is Spacelift's, with a budget on the Free tier | The provided integration is the operator's; `isSpaceliftProvided` means provided by the installation, and its budget applies to every account | The installation, not Spacelift, pays for the model | None for clients; operators configure the default model and budget | [0019 Rationale](../research/decisions/0019-ai-provider-boundary.md#rationale) |
| DV-072 | The chat transport is unpublished | An infraweave-defined SSE endpoint | A transport is needed and none is documented | Chat clients use infraweave's endpoint | [0019 Rationale](../research/decisions/0019-ai-provider-boundary.md#rationale) |
| DV-073 | Spacelift's terms and documentation search | The terms are the operator's, and `search_docs` searches infraweave's documentation | infraweave cannot accept terms or search docs on Spacelift's behalf | None | [0019 Rationale](../research/decisions/0019-ai-provider-boundary.md#rationale) |
| DV-074 | `spacelift-intent` does not verify provider checksums or signatures | Intent verifies provider checksums and signatures | Providers run with cloud credentials on workers | An unsigned or mismatched provider is refused | [0019 Rationale](../research/decisions/0019-ai-provider-boundary.md#rationale) |

### Packaging: decision 0020

| ID | Spacelift | infraweave | Why | Client impact | Source |
|---|---|---|---|---|---|
| DV-075 | Ships ECS and CloudFormation reference deployments | No ECS or CloudFormation reference deployment | The supported set stays testable | ECS operators adapt the container image themselves | [0020 Rationale](../research/decisions/0020-reference-deployments-and-packaging.md#rationale) |
| DV-076 | Infrastructure modules and the Helm chart version separately from the application | Modules and chart share the application's version | One version answers which artefacts fit together | Operators pin one version | [0020 Rationale](../research/decisions/0020-reference-deployments-and-packaging.md#rationale) |
| DV-077 | Launchers download from Spacelift and are verified with GPG keys from a public key server | Launchers download from the installation itself and are verified with Sigstore | Air-gapped installs reach nothing else; no second trust root | Worker bootstrap scripts change their download and verification steps | [0020 Rationale](../research/decisions/0020-reference-deployments-and-packaging.md#rationale) |
| DV-078 | Self-hosted needs a licence token and lacks the public pool and the Azure and GCP integrations | No licence token; those limits are not inherited, and the shared pool is a chart option (`sharedPool.enabled`) | infraweave mirrors SaaS, not the self-hosted subset | None | [0020 Rationale](../research/decisions/0020-reference-deployments-and-packaging.md#rationale), [0021 Decision](../research/decisions/0021-capability-scope-and-supersession.md#self-hosted-limits-are-not-inherited) |

### Scope, tiers and clients: decision 0021

| ID | Spacelift | infraweave | Why | Client impact | Source |
|---|---|---|---|---|---|
| DV-079 | `tier` and `tierFeatures` reflect a commercial plan | `tier` is the constant `V5_ENTERPRISE_PLUS`; `tierFeatures` is every non-superseded `BillingTierFeature` not listed in the operator's `features.disabled`. A gated operation of a disabled feature answers `403` `feature-disabled`, named per operation in `x-infraweave-feature` | infraweave has no editions; operators still need a switch for features they must keep off | Clients that branch on tier see every feature on unless the operator disables it | [0021 Rationale](../research/decisions/0021-capability-scope-and-supersession.md#rationale) |
| DV-080 | Billing mutations change a Stripe subscription | Billing mutations answer `403` `installation-unsupported`; reads answer with local meaning (`billedExternally: true`, `usage` over a trailing 30 days, empty add-on lists) | A fake success looks like progress in a test and fails in production | Billing automation receives an explicit refusal | [0021 Rationale](../research/decisions/0021-capability-scope-and-supersession.md#rationale) |
| DV-081 | The Azure integration, Origin and global plugin templates rely on Spacelift's own apps and catalogue | They rely on operator-registered apps and catalogues: `cloud.azure`, `vcs.origin` and `plugins.catalog_paths`; without one, the operation answers `installation-unsupported` | infraweave operates no global apps | Operators register the apps before these features work | [0021 Rationale](../research/decisions/0021-capability-scope-and-supersession.md#rationale) |
| DV-082 | Accepts some superseded enum values in a disabled state | A superseded enum value is rejected outright | A request that would do nothing should fail visibly | Clients sending a superseded value receive `400` `invalid-request` | [0021 Rationale](../research/decisions/0021-capability-scope-and-supersession.md#rationale) |
| DV-083 | spacectl picks one credential when several are set, in an order its docs and source disagree on, and stores profiles in `config.json` or a file per alias | `infraweavectl` errors when two methods are set without `INFRAWEAVE_API_PREFERRED_METHOD`, and stores profiles in one strict YAML file, `$XDG_CONFIG_HOME/infraweave/config.yaml`, written `0600` | Never pick silently between conflicting inputs; one strict file | Scripts set one credential method or the preference variable | [0021 Rationale](../research/decisions/0021-capability-scope-and-supersession.md#rationale) |

### Errors, updates and input: decision 0022

| ID | Spacelift | infraweave | Why | Client impact | Source |
|---|---|---|---|---|---|
| DV-084 | GraphQL `errors` with message text; no documented status codes or stable codes ([report 06](../research/reference/06-spacectl-and-clients.md#24-error-behaviour)) | An HTTP status per failure class and `application/problem+json` from one slug registry; fixed titles keep `not found` and `unauthorized` as the provider and spacectl match them; hidden and missing resources both answer `404`; field errors carry `errors` with `pointer` and `reason`; protocol endpoints answer what their own clients expect | Any HTTP client, proxy and retry library can act on the status; ported string matches keep working | A port changes transport only; its message-text checks still match | [0022 Rationale](../research/decisions/0022-api-errors-and-update-semantics.md#rationale) |
| DV-085 | Updates are whole-input mutations (`stackUpdate`, `stackConfigAdd`) | Every update route keeps `PUT`, which replaces, and gains `PATCH` with `application/merge-patch+json`; configuration elements get an item `PUT` that answers `201` or `200` | Providers want full replacement, people want to change one field | Providers use `PUT`; CLIs and scripts may use `PATCH` | [0022 Rationale](../research/decisions/0022-api-errors-and-update-semantics.md#rationale) |
| DV-086 | Last write wins | Item `GET`, `PUT` and `PATCH` return a strong `ETag`; `PUT`, `PATCH` and `DELETE` accept `If-Match`, and a stale tag answers `412` `precondition-failed`. Without `If-Match`, last write wins | Optional protection against lost updates | None unless the client sends `If-Match` | [0022 Rationale](../research/decisions/0022-api-errors-and-update-semantics.md#rationale) |
| DV-087 | An unset relation reads as `null` | A sub-read of an unset optional relation answers `204 No Content` | `404` on such a route always means the parent is missing or hidden | Clients treat `204` as "none" | [0022 Rationale](../research/decisions/0022-api-errors-and-update-semantics.md#rationale) |
| DV-088 | Unknown input fields are not documented as rejected | Request bodies are strict: an unknown field answers `400` `invalid-request` with reason `unknown-field`; `StackInput.administrative` stays, deprecated and ignored | A misspelt body field silently ignored writes the wrong state | Clients that send extra fields remove them | [0022 Rationale](../research/decisions/0022-api-errors-and-update-semantics.md#rationale) |
| DV-089 | Label limits are undocumented | Labels are 1 to 255 characters of UTF-8 with no control characters or edge whitespace, at most 100 per entity, compared byte for byte; a duplicate answers `422` `validation-failed` | Bounded, unambiguous labels | Imports with longer, more numerous or duplicate labels fail with the field named | [0022 Rationale](../research/decisions/0022-api-errors-and-update-semantics.md#rationale) |
| DV-090 | Cron and timezone acceptance is unverified | Five-field cron with `*`, `n`, `a-b` and `/step`, Vixie day matching; names, `?`, `L`, `W`, `#` and `@` macros are rejected. Timezones are IANA names (embedded `time/tzdata`), absent means `UTC`; abbreviations and numeric offsets are rejected | Schedules must fire when the user expects, or fail loudly | Imports with an expression or zone outside these rules fail with a pointer to the field | [0022 Rationale](../research/decisions/0022-api-errors-and-update-semantics.md#rationale) |
| DV-091 | Unknown keys in `.spacelift/config.yml` are unverified | Known keys are typed strictly and fail the run in `INITIALIZING`; unknown keys warn in the run log and `Run.runtimeConfig`, or fail with `runs.runtime_config.unknown_keys: reject` | Failing every stack because a key is newer than infraweave turns schema lag into an outage | Repositories with a mistyped known key see the run fail with the key's path | [0022 Constraints](../research/decisions/0022-api-errors-and-update-semantics.md#constraints) |

## Pending deviations

These rows rest on a proposed answer to one of
[report 30's product decisions](../research/reference/30-open-questions.md#product-decisions),
which the maintainer accepts by accepting the record, or on a fact a capture
may overturn. Changing the answer changes or withdraws the rows.

| Open item | Proposed answer | Rows | Record |
|---|---|---|---|
| P1, error mapping | A status per class with Spacelift's words kept (option b) | DV-007, DV-084 | [0022](../research/decisions/0022-api-errors-and-update-semantics.md) |
| P4, docs against clients | Docs for server-enforced and security behaviour; strict choices for client-local behaviour | DV-046, DV-083 | [0021](../research/decisions/0021-capability-scope-and-supersession.md) |
| P5, features and tiers | No editions; operator switches in `features.disabled` | DV-018, DV-079 | [0021](../research/decisions/0021-capability-scope-and-supersession.md) |
| P6, worker transport | HTTP long-poll only (option a) | DV-024 | [0008](../research/decisions/0008-run-execution-and-worker-protocol.md) |
| P7, run credential formats | Identical by default, with a configurable issuer and templates (option b) | DV-012 | [0006](../research/decisions/0006-authentication-and-tokens.md) |
| P8, update semantics | Both `PUT` and `PATCH` (option c) | DV-085, DV-086 | [0022](../research/decisions/0022-api-errors-and-update-semantics.md) |
| P9, input strictness | A rule per input | DV-088, DV-089, DV-090, DV-091 | [0022](../research/decisions/0022-api-errors-and-update-semantics.md) |
| P11, default AI integration | Bring your own model, plus an optional installation default with a budget, off by default (option b) | DV-071 | [0019](../research/decisions/0019-ai-provider-boundary.md) |
| Capture C78 | Whether the released VCS agent validates requests decides whether DV-046 departs from the agent as well as from the docs | DV-046 | [0012](../research/decisions/0012-vcs-integration-model.md) |
| Capture C132 | What Spacelift does with yanked versions | DV-055 | [0014](../research/decisions/0014-module-and-provider-registry.md) |
| Captures C17, C81, C83, C91 | The in-run token `sub`, external ID suffixes, `RoleSessionName` and the federation algorithm | DV-012 | [0006](../research/decisions/0006-authentication-and-tokens.md) |
| Captures C2, C3, C5, C24, C26, C30, C97, C155, C156 | Whether Spacelift has stable error codes or is looser than infraweave's input rules | DV-084 to DV-091 | [0022](../research/decisions/0022-api-errors-and-update-semantics.md) |

## Not deviations

Interfaces infraweave does not serve because Spacelift supersedes them (such as
`ACCESS` policies, `policyCreate` and `policyUpdate`, the versioned-blueprint
family, the legacy per-stack AWS role and GCP service-account integration,
`/intent/mcp`, the standalone `spacelift-intent` stdio server and
`pulumi-spacelift`) are recorded with their evidence in the
[supersession register](capability-roadmap.md#supersession-register), under
[0021](../research/decisions/0021-capability-scope-and-supersession.md)'s
supersession test. Decision
[0016](../research/decisions/0016-testing-and-acceptance.md) governs how
infraweave proves its behaviour and adds no behavioural departure.
