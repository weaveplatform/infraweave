# Worker pool hosting and execution contracts

This is the detailed contract of [0008](../research/decisions/0008-run-execution-and-worker-protocol.md),
[0009](../research/decisions/0009-policy-engine-opa.md) and
[0020](../research/decisions/0020-reference-deployments-and-packaging.md) for
[P14](plan.md#p14-public-worker-pool-kubernetes-workers-and-signed-runs).
The [ownership matrix](worker-pool-contracts.json) assigns every requirement and
its proof, and the [delivery audit](worker-pool-delivery.md) records the executed
evidence that delivers each one. The [reference observations](worker-pool-reference-observations.md) are
separate: nine read-only exchanges establish an empty public-pool account view;
C21, C66, C67, C68, C69 and private-priority C292 remain open.

## Baseline and reconciliation

Source inspected at `41ab59b5b417ff3d0f9767642b2db0620706640e`:

| Existing behavior | Settled extension |
|---|---|
| [Catalogue](../../internal/features/catalogue.gen.go) seeds `worker-pools` Free, `private-worker-pools` Business; private workers 0/0/3 and public slots 1/2/2 for Free/Starter/Business. | Preserve these seeds. All clouds use the same entitlement. Business's three workers are aggregate across accounts/pools/clusters; a four-worker lab uses an explicit organization grant of four. Enterprise's absent limit and billing-off remain unbounded. Historical reference Starter+ prose does not change the catalogue. |
| [Run creation](../../internal/runs/internal/core/lifecycle/lifecycle.go) copies the stack pool; [claim SQL](../../internal/runs/internal/adapters/pg/claim.go) selects account and stored pool. | Keep immutable per-run assignment through plan/apply/replan. A stack change affects future runs. Capacity loss leaves work queued or fails its existing execution; it never selects another pool/cloud. |
| ADR 0008 and [public schema](../../api/src/api/paths-workers.yaml) describe account parallelism; P14 says organization ceiling. | Account projection reports its effective quota; organization and lower account ceilings both fence claim. Physical configured public workers are a separate installation capacity. |
| [WorkerPool schema](../../api/src/api/paths-stacks.yaml) declares `managedByK8sController`, but [pool persistence](../../internal/workers/internal/adapters/pg/migrations/00001_workers.sql) has no owner and [view](../../internal/workers/internal/adapters/http/http.go) leaves it false. | Persist an immutable management identity, enforce ownership and expose the existing boolean from it. |
| [Executor Job/Mount](../../internal/agent/executor/executor.go), [launcher preparation](../../internal/agent/launcher/launcher.go) and Docker HandBack use launcher-local directories/binds. | Retain Docker behavior; add portable streamed preparation assets and Pod-local volumes. Kubernetes never treats a controller path as a node path. |
| [Images](../../deploy/images/runner-terraform/Dockerfile) contain verified pinned CLI binaries; explicit versions still download/verify and override read-only. | Retain both behaviors, including `skipVendorBinaryDownload`. Correct 0020's tool-free prose; Terraform infrastructure provisioning does not build replacement tool environments. |
| `available_at` is already [stored](../../internal/workers/internal/adapters/pg/workers.go), set idle and cleared busy. | Reuse it and prove Kubernetes projections; no new availability clock or cross-cloud autoscaler. |
| Public default-image and egress reads have no served fields/routes; other P14 projections are declared. | Add the precise reads below. A declared stub is not an implemented or accepted operation. |

Workers owns pool identity, credentials, registration, worker state and samples.
Runs owns immutable run assignments, claims, transitions, artifacts and queued-run
reads. Features owns organization/account quotas. Access owns authentication and
authorization. The agent speaks the worker/client API; its controller links no
server persistence. [App composition](../../internal/app/domains.go) constructs
workers before runs and binds scheduler/reports/liveness/signals afterward.
Queue projections remain a runs partial or a composed API port; workers imports
no runs implementation. Initial schema additions follow the pre-v0.1 initial
migration convention, not a second competing store.

## Identity, public pool and quota

Four execution identities remain distinct: Terraform's cloud provisioner; the
scoped controller management API key; pool protocol certificate/private key; and
worker-session/run credentials with their separate audiences. A user's workload
cloud credentials are an additional concern that remains P15; hosting grants no
automatic cloud administration identity to a run. Kubernetes service accounts authorize controller Kubernetes
operations, not infraweave API calls. Cloud choice adds no feature/tier gate.

The optional public pool has one installation-owned stable ULID, certificate and
serial, persisted by workers separately from account-owned private pools. Public
workers have installation scope, no fabricated account/organization, and do not
consume `private-workers`. A public assignment still identifies its actual
account/run; its secrets are sealed to this pool key. Public workers use the
existing `/worker/v1` protocol and are admitted only with its installation pool
certificate. The worker session has no tenant browsing authority.

Worker sessions bind their signed certificate generation to registration and
current pool state; private resume cannot revive a pre-reset token. The
[session contract and verification](worker-session-verification.md) records
the mutation fences and legacy-token re-registration behavior.

The server configuration is strict `worker.public_pool`: `enabled` (false),
`allowed_registries` (the existing ADR allowlist), `outgoing_ip_addresses` (empty
array), and `preparing_timeout` (15 minutes). Chart `publicWorkerPool.enabled`
maps to that switch. Each egress entry is a canonical IPv4/IPv6 literal, unique;
invalid entries fail startup. These are operator assertions about egress, not
addresses guessed from nodes or published as cloud-provider guarantees. Enabling
the pool does not provision capacity; worker hosts configure their own capacity.
Disabling stops new public claims and lets already assigned executions finish.

The public claim retains space/stack owner fencing and state/version checks.
Before assigning it locks the organization's entitlement row, applies the lower
account ceiling and counts in-flight public assignments across all its accounts
in the same transaction. Concurrent public workers cannot both consume the last
slot. A run that exceeds quota waits; it does not return `limit-reached`. Another
organization remains eligible. Within public capacity, claim orders by creation
time then run ULID, respects stack locks, and skips quota-blocked accounts without
reserving their slots. Public priority changes answer `422 validation-failed`;
scheduled/drift admission is refused. Registry allowlisting applies after full
image precedence resolution, including runtime/stack/account/launcher overrides.

`PublicWorkerPool.parallelism` becomes **required nullable int64**: the minimum
of the organization's bounded quota and a stored bounded account ceiling, or
`null` when neither applies. Billing off answers null. Zero means no public
assignment can start; it never means unlimited. Values are quotas, not free
slots, reservations or physical replicas. JSON int64 is consumed losslessly by
SDKs; browser clients use the exact decimal representation for display and never
round it through arithmetic. The deliberate int32-to-nullable-int64 REST schema
change preserves catalogue semantics; no sentinel, saturation or replica-count
substitution is permitted. Existing counts retain their declared count types;
the new quota has no narrowing conversion.

### REST and operator mappings

These are intended source-schema changes; no generated API is changed by this
document. Routes are under `/api/v1`, except operator routes under
`/operator/v1`. Preserve chi, strict schemas, SDK calls, normal problems,
rate limits, conditional updates and account/space isolation.

| Route and operation | Schema/control contract |
|---|---|
| `GET /public-worker-pool` (`publicWorkerPool`) | Existing operation; feature `worker-pools`, `SPACE_READ`, list reach. Account counts only, over the stacks in the caller's readable spaces: busy is the account's runs a public worker holds; pending is its claimable public runs; schedulable equals pending while public execution is enabled and is zero while it is disabled; used-by counts stacks naming no private pool. `parallelism` as above. No installation-wide tenant/worker inventory. Intent projects are empty until their domain exists. |
| `GET /default-public-worker-pool-runner-image` (`defaultPublicWorkerPoolRunnerImage`) | New Account read, `account-details`, `AUTHENTICATED`, account subject, standard cost; string account public default, otherwise built-in Terraform image, mirroring existing private read. This display default does not override a run's vendor-image precedence. |
| `GET /outgoing-ip-addresses` (`outgoingIPAddresses`) | New Account read, `worker-pools`, `AUTHENTICATED`, account subject, standard cost; string array from operator egress configuration, empty when disabled/unconfigured. No cloud lookup. |
| `GET /worker-pools/public/search-schedulable-runs` and `...-suggestions` (`publicWorkerPoolSearchSchedulableRuns`, `publicWorkerPoolSearchSchedulableRunsSuggestions`) | New literal public paths, feature `worker-pools`, `SPACE_READ`, list subject, standard cost; run visibility and filters commit/drift/prioritized/stack/state/type match private search; `createdAt` and `position` order and never filter, and a predicate on them is 422. Public priority/drift filters return no prohibited runs. Cursor ordering uses current claim ordering (private: priority tier, creation, ULID; public: creation, ULID); position numbers the caller's visible queue at query time, not a slot reservation. The public queue is empty while public execution is disabled. The runs domain serves both queues; workers reads their counts through `workersapi.Queues`. |
| `GET /worker-pools/public/search-users` and `...-suggestions` (`publicWorkerPoolSearchUsers`, `publicWorkerPoolSearchUsersSuggestions`) | New literal public paths with the same public controls; users means visible stacks naming no private pool, not human principals; fields isModule/label/name/slug/space, by slug unless ordered. Modules and intent projects arrive with their domains. Count matches caller visibility. The stacks domain serves the private and public user searches; workers reads a pool's users through `workersapi.Users`. |
| Existing `/worker-pools/{workerPoolId}/search-*` | Private operation IDs/feature/action remain. The literal selector `public` is reserved to the four explicit public read paths; the pool's subject resolution refuses it, so every private read, mutation, reset/cycle and worker-inventory route answers `422 validation-failed` at `#/path/workerPoolId`. Public read does not pass through private ULID/space resolution or require Business. |
| Existing `GET /worker-usage` (`workerUsage`) | Workers-owned account read with the existing schema. Cycles are calendar months in UTC; `selectableCycles` stop at the fifth before the current one or the oldest cycle the sample retention covers, and any other `cyclesAgo` is `422 validation-failed`. `contractedLimit` is the private-workers quota as `parallelism` computes it, `multiAccountContract` whether the organization's pool is that limit; no percentile is billed, so percentile fields are null and `burst` absent. The schema carries no sample series, so no installation capacity is summed per account. |
| `POST /public-worker-pool` operator (`publicWorkerPoolCreate`) | New operator-only idempotent singleton creation. Strict body `{certificateSigningRequest}` using existing P-256 CSR encoding. Creates stable pool ULID once; same Idempotency-Key/body replays the original response, same key/different body answers 422 idempotency mismatch, a new key against an existing singleton answers `409 invalid-state`. Response `{id, config, version}` contains public certificate/token, never private key; 201 with ETag. |
| `GET /public-worker-pool` operator (`publicWorkerPoolCredentials`) | Operator-only 200 `{id, config, version}` + ETag; 404 before creation. Account/organization credentials are refused as the wrong audience. |
| `POST /public-worker-pool/reset` operator (`publicWorkerPoolReset`) | Operator-only idempotent strict `{certificateSigningRequest, releaseBinding?}` replacement requiring current If-Match; releaseBinding defaults false; 200 same ID/new token/version. Missing condition 428, stale 412, owner/state conflict 409, invalid CSR 422. It replaces certificate serial, never silently creates another pool. |

Operator operations use `operator` feature, standard cost and existing operator
authentication; they bypass account feature tiers and grant no account role.
Their credential-audience controls are protocol/operator-classified: wrong
credential is 401, not an invented role-403. Account reads need executed success,
403 from a billing-on entitlement/authorization denial and real failure; public
capability on seeded Free does not remove tests against a deliberately restricted
test tier. The existing operator switch-off convention remains 503.

Stack APIs keep `workerPool: null` as explicit public selection and a reachable
private ULID as private selection. Omission preserves each operation's existing
create/update semantics. Run creation stores this choice once, plus the stable
public pool identity where relevant for worker delivery; a later installation
reset or stack move never selects another cloud. Public disabled/no workers
leaves selected public work queued; it does not choose a private pool.

## Controller and resources

The added process is `infraweave worker-controller`, assembled from agent code.
Docker remains `infraweave launcher`. The controller watches namespaced
`workers.infraweave.io/v1alpha1` WorkerPool and Worker resources and manages
protocol loops for logical slots; idle slots need no Pod. There is one active
leader per configured watch scope using a Kubernetes Lease (15-second lease,
10-second renewal deadline, 2-second retry). Replicas use resourceVersion
compare-and-set, deterministic names and server fencing even during leader
handover. Every slot's preparation and busy heartbeat continue independently;
source/tool transfer never blocks protocol polling for other slots or its own
liveness. No second worker registers merely because a replica's local cache is
empty. Each registered slot runs a launcher that polls for, claims and executes
runs in Pods, and the WorkerPool reports `ExecutorAvailable=True`; a controller
without Kubernetes stream configuration claims nothing and reports
`ExecutorAvailable=False`, its slots registering again each minute, which
resumes their workers and keeps them within the server's offline bound.

Strict `worker_controller` YAML/configuration keys:

| Key | Type/default |
|---|---|
| `watch_namespaces` | Required nonempty unique namespace list; no wildcard default. |
| `cluster_id` | Required stable installation-assigned string, 1..63 DNS-label characters. |
| `management_credentials_ref` | Optional `{name, id_key, secret_key}` reference to distinct keys in a same-namespace Secret; required for Managed resources; resolved per watched namespace. |
| `leader_election` | Required enabled; Lease name and namespace explicitly configured, timings above. The chart's release creates its Lease, `<release>-worker-controller` in the release namespace. |
| `bootstrap_image` | Required immutable digest of same-version multi-architecture infraweave image; runner binary copied from it. |
| `cache_dir` | Slot-private ephemeral staging/cache root, default `/var/cache/infraweave`; no hostPath. |
| `ca_file` | Optional mounted PEM trust bundle for private application endpoints; no insecure TLS switch. |
| `log` | Existing strict format/level settings; JSON/info by default. |
| `admin_listen` | `:9091`, cluster-internal probes/metrics. |
| `initialization_policy`, `initialization_timeout` | Optional mounted policy path; timeout 60 seconds, positive and no longer than effective preparation; compile failure stops startup. |
| `cleanup_timeout` | 120 seconds per reconciliation attempt; pending failure remains visible/retryable. |

All secret inputs are references, not embedded values in ConfigMaps, arguments
or chart values. API key authentication uses the existing account API-key login
and audience. Pool credentials use an existing Secret with fixed keys `token`
and `privateKey`, read together and matched to the resource's pool: the token
must name that pool and certify that key. The controller calls the resource's
immutable HTTPS `endpoint`; the token's endpoint field, which names the server's
public URL, is not used to reach it. Only an
unbound Managed resource may initially omit token: its externally supplied
privateKey is required, and create/management lookup returns the public token
config for that exact owner so response loss can recover it before registration.
Once bound, missing/mismatched token or key fails closed; no replacement pool or
key is generated. Existing/Public require both from the first reconciliation.
The key is P-256 PEM as the launcher already accepts; controller configuration never
persists the opened contents. Referenced missing/mismatched credentials fail
closed without creating a replacement pool.

### WorkerPool schema

WorkerPool uses native Kubernetes structural schemas with named supported fields,
typed map entries and pruning enabled. Kubernetes rejects the literal
`additionalProperties: false` spelling for CRDs. Controller writes use
`fieldValidation=Strict`, so unknown fields are refused; clients requesting
non-strict validation may receive safely pruned resources. No unknown PodSpec is
preserved or forwarded. CEL enforces mode, resource bounds and lifecycle
invariants. The [controller design and native verification](worker-controller-design.md)
record this schema-form correction and its real API evidence.

The supported spec fields are:

- `endpoint`: required HTTPS origin, immutable; `account`/`space`: required
  account ID and space slug in Managed/Existing, immutable after binding. Public
  mode forbids account/space rather than inventing a tenant.
- `mode`: required `Managed`, `Existing` or `Public`, immutable. `credentialsRef`: required
  same-namespace Secret name with fixed token/privateKey keys. Existing requires
  immutable `poolID` ULID. Public also requires poolID, matching the separately
  operator-issued installation token. Managed omits poolID until status records
  creation. A credential Secret reference can be corrected without changing the
  pool identity; opened token/key matching still fails closed after binding.
  Public forbids management key/private-pool settings and consumes only
  installation credentials; it creates capacity for the singleton without
  calling account API or consuming private-worker quota.
- `capacity`: required integer 0..2147483647, desired registered logical slots;
  defaults are never inferred from queue length or another pool/cloud.
- Managed-only `name`, `description`, `labels`, `preparingTimeoutSeconds`,
  `driftDetectionRunLimit`: existing private-pool settings, same validations.
  Existing/Public forbid these remote-setting inputs.
- `pod`: required typed run resource requests/limits, storage size, node
  selectors/tolerations and same-namespace imagePullSecret references. Baseline
  uses ephemeral workspace/tools/binary volumes. The chart defaults run request
  1 CPU/2 GiB memory and limit 2 CPU/4 GiB memory, 12 GiB ephemeral storage;
  the run may fail its configured resource limit, not escape it. Policy helpers
  request 100m CPU/64 MiB and limit 1 CPU/256 MiB, 64 MiB ephemeral storage.
  Operators can raise typed resource values without relaxing fixed security.
- `environment`: explicit list of variable names, exposure `all`, `read` or
  `write` agreeing with the name's `ro_`/`wo_` prefix, and `from`, a typed
  `{kind: Secret|ConfigMap, name, key}` reference in the pool's namespace that
  is never the credential Secret; `mounts`: typed existing Secret/ConfigMap key
  or PVC references, target absolute path and exposure, with `readOnly`
  mandatory and a PVC of `all` exposure only. Targets cannot overlap infraweave
  binary/tools/handoff locations, the workspace, `/proc`, `/sys` or `/dev`.
  Node/controller host paths, custom sidecars, arbitrary command overrides and
  arbitrary PodSpec are refused; no controller environment reaches a run.

Managed flow reads the externally supplied key, builds its CSR, calls ordinary
pool create and writes returned token into the referenced Secret. No private key
is returned by the server. Existing mode consumes supplied token/key and never updates pool settings,
resets its certificate or deletes the remote pool. It claims/releases the
separate hosting binding through the signed pool protocol below. Public mode
uses that same binding protocol with installation credentials and never creates
or resets the operator singleton. Managed deletion uses ordinary pool
delete and its existing guards; stack use or active work produces a terminating
condition rather than force-deletion. Every mode preserves the stable pool ULID.

Managed pool create/read schema gains `management`:
`{clusterID, namespace, resourceUID}` (immutable strings, resourceUID is an opaque Kubernetes UID, 1..128 characters;
cluster/namespace length at most 63). The pool scope and ID scope a unique durable owner row;
the remote endpoint scopes the installation. `resourceUID` is the WorkerPool
UID, not a display name. Ordinary create requires Idempotency-Key derived from
that UID; ownership uniqueness persists after HTTP idempotency expiration.
`GET /worker-pools/managed?clusterID=...&namespace=...&resourceUID=...`
(`workerPoolManaged`) is a private-worker-pools/SPACE_READ/list standard-cost
lookup, returning one owned pool including its public token `config` and ETag,
or 404. SPACE_READ authorizes that public certificate/token config as existing
private reads already do; no private key is returned. Wrong ownership conflicts with 409;
controller never adopts a similarly named pool. Requests carrying management
identity still require ordinary create/update/delete permissions in the target
space; the field grants no authority itself. Updates of managed settings require
the exact management identity and conditional version. Unrelated clients'
updates answer 409; authorized deletion stays possible for recovery.

The system role stays exactly SPACE_READ and WORKER_POOL_CREATE/UPDATE/DELETE.
It has no RESET/CYCLE/account-admin/operator permission. Certificate reset is
an explicit separate account principal with WORKER_POOL_RESET, using the ordinary
reset route; this authorized reset is the documented exception to managed-update
ownership. The controller never resets because a secret changed or a registration
failed. Reissuing a certificate for the same key preserves saved-workspace access;
replacing the private key makes existing old-key workspaces unreadable unless
that key is retained externally. Such restore fails and requires explicit replan;
there is no silent key/pool/cloud fallback or server-side decryption service.

WorkerPool status: observedGeneration, immutable poolID/management identity,
conditions, desired/registered/busy/retiring counts. Worker spec: immutable
WorkerPool UID, poolID and integer slot; name is parent UID plus slot hash.
Worker status: server worker ULID, assignment run/state/version/deadlines,
Pod name/UID, handoff phase, original start, exit code/reason, acknowledgement.
The acknowledgement is written in the same update as the original start and run
deadline it begins; until then the handoff phase is `Staged`. No token, private key, opened handoff, source credential or signed URL is in
status. Capacity reconciliation creates slots 0..N-1, registrations enforce live
organization limits, and excess slots retire without new claims, finish current
work, report/deregister, then disappear. A retiring slot's polls say `retiring`,
so the server claims no run for them, and its exit poll answers at once. A refused fourth Business registration
stays a visible limit denial; it does not change tier seeds.

One WorkerPool owns its Worker resources; each Worker owns run/helper Pods in
the same namespace. A pool has exactly one cluster/controller owner. Concurrent
distinct pools across arbitrary clusters/clouds remain supported. A remote
pool ID referenced by two owner resources conflicts instead of sharing replicas.
This applies to Managed, Existing and Public, enforced by workers persistence,
not merely a namespace Lease. Before any worker registration, the controller
calls `POST /worker/v1/pools/bind` with a current pool-certificate ES256 JWS
containing `{pool, owner:{clusterID,namespace,resourceUID}, iat, jti}`. Verification
reuses current serial/chain/timestamp/replay rules. Binding succeeds idempotently
for that owner, conflicts 409 for a different owner or pre-existing unowned
registered workers, and persists after controller restarts. Registration JWS
adds this owner and rejects a mismatch. Ordinary Docker registration is allowed
only when the pool has no controller binding; it never joins a bound pool by
omitting owner. Managed create reserves the identical binding transactionally.
`POST /worker/v1/pools/release` uses the same signed owner proof and succeeds
only when no worker/active on-worker run remains; the controller calls it after
verified local cleanup. It removes hosting ownership, not pool identity/settings
or credentials. Existing/Public finalizers release ownership and retain remote
pool; Managed releases as part of guarded delete. An unreachable abandoned
controller requires a separately authorized pool delete/reset recovery and
verified old-worker removal, never automatic takeover. Reset to a replacement
key clears an abandoned binding only through an explicit `releaseBinding: true`
reset input under existing WORKER_POOL_RESET/operator authorization, requiring
a replacement key different from the current certificate and refused while
workers or on-worker runs remain. Private reset body gains the same optional
boolean, default false; public operator reset uses it too. This does not grant reset to the
controller role. Registration of a bound pool names its owner and a logical
`slot`; each pool holds at most one worker per slot, so registering a slot
again resumes its worker, and a slot without an owner is invalid.
Qualified finalizers are installed before remote side effects. Worker finalizer
waits for exit acknowledgement/deregistration and verified Pod removal; pool
finalizer waits for children plus Managed remote delete. Existing retains remote
pool. Failure/auth outage leaves retryable terminating conditions; timeouts do
not falsely certify deletion. Emergency removal is an explicit operator recovery
that may leave resources and must be followed by verified cleanup.

Controller RBAC is namespaced: CRD get/list/watch/status/finalizer updates,
owned Pod create/get/list/watch/delete/log/attach/exec, referenced credential
Secret reads by name (never a list), Managed token-key patch on that existing
Secret, and get/update of its release's Lease, which the release creates. The controller patches only token and preserves externally managed
privateKey; it creates no credential Secret implicitly. Install-time CRD/ClusterRole authority is separate.
Run/helper Pods have `automountServiceAccountToken: false`, no API permissions,
no shared process namespace and no management credentials. Workload admission
may additionally require a sandbox runtime; it cannot require privileged DinD,
Docker sockets, controller-host mounts or host namespaces.

## Portable preparation, handoff and recovery

`executor.Job`'s local paths remain Docker adapter inputs. An agent preparation
port adds bounded stream assets: workspace and at most two verified tool
executables (tool plus Terragrunt), each with purpose/name, exact length and
SHA-256. They are separate from the existing handoff JSON. The controller stages
privately or streams them; Kubernetes never reads an absolute controller path.
The selected image keeps its baked tools; download=false creates no override.
Download=true retains existing official signature/checksum/version/architecture
verification and stages read-only per-run override bytes. Only implemented tools
are advertised: Terraform/OpenTofu/Terragrunt today; kubectl remains its vendor
phase's implementation, not proof supplied by runner-image availability.

### Transfer protocol and existing bounds

The init container uses same-version infraweave bootstrap, UID/GID 1983, to copy
the static runner into a Pod-local binary volume. The init container is named `bootstrap` with `stdin=true`, `stdinOnce=true`,
`tty=false`; attach targets that exact container and Pod UID. Its non-TTY
authenticated TLS stdin stream sends a 64-KiB maximum strict version-1 manifest followed
by **separate length-framed** workspace and tool payloads. Manifest keys are
`version`, `run`, `stateVersion`, `worker`, `workspace` (codec/size/hash), `tools`
(name/version/size/hash; max two). Hashes are SHA-256 hex, lengths nonnegative
int64, tools fixed names only; duplicate names, unknown fields/versions, trailing
bytes, truncation or mismatch fail preparation before main runner starts.

| Payload | Authoritative baseline and chosen transfer behavior |
|---|---|
| Local tar.gz source | Server [trigger](../../internal/runs/internal/core/trigger/local.go) declares 1..2 GiB with SHA-256; agent [download/extract](../../internal/agent/source/archive.go) bounds download at 2 GiB and extracted file content at 8 GiB. Preserve these source checks. |
| Saved workspace | [Workspace format](../../internal/agent/workspace/workspace.go): 2 GiB total sealed object, <=1-KiB envelope, <=64-MiB zstd window; [restore](../../internal/agent/source/source.go) authenticates and extracts <=8 GiB. Pool private key remains controller-side. |
| Prepared workspace transport | New admission: zstd tar <=2 GiB compressed, <=8 GiB total decoded tar including headers. This explicitly bounds stream overhead as well as files; it is not an existing Git limit or observed reference behavior. Repack source/restored content into it using safe extraction rules. A near-bound existing archive whose headers exceed this transport ceiling fails explicitly. Git checkout uses the same new transport limit; no unbounded Git-byte guarantee is claimed. |
| Tool acquisition | [Fetcher](../../internal/agent/tools/tools.go): signed sums <=1 MiB, signature <=64 KiB, ZIP <=256 MiB, executable <=512 MiB. Transfer only the verified extracted executable; max two => <=1 GiB aggregate, plus manifest. Keep signature verification in the acquiring agent and receiver hash verification. |
| Main handoff | Existing [handoff](../../internal/agent/handoff/handoff.go) strict JSON <=64 MiB; no binary/source bytes in JSON. Send separately to main stdin after init success, never a bootstrap file. |
| Output objects | [Report limits](../../internal/runs/internal/core/report/report.go) remain log chunk 1 MiB, artifact 512 MiB, sealed workspace 2 GiB, observability 64 MiB **as uploaded**. Input bootstrap frames do not replace uploads or change their bounds. |

Streams use bounded buffers, not whole-payload RAM allocation. Receiver rejects
escaping path/link targets, device/FIFO/socket entries and invalid project roots,
using existing safe workspace extraction. The new decoded transport bound stops
metadata consuming unbounded decode work without inventing a separate entry
count cap. Valid relative links remain supported. The init can write workspace
and tools; main mounts tools/binary read-only and workspace at `/mnt/workspace`
read-write, all UID/GID 1983. Use Pod-local emptyDir; no controller-to-run shared
PVC, host chmod fallback or Docker HandBack is needed. Requests/limits cover
actual staging and transient upload files; 12 GiB is a default storage budget,
not a proof that arbitrary workload-generated files will fit. Exhaustion fails
that execution. Controller staging budget scales with configured preparation
concurrency (one per slot, bounded by configured capacity); it cannot grow an
unbounded queue of staged work.

After `bootstrap` exits zero, main container named `runner` starts the injected binary as `runner` with its image
entrypoint replaced, `stdin=true`, `stdinOnce=true`, `tty=false`. A second attach targets only `runner`; the launcher
checks exact Pod UID/run/version and sends existing in-memory handoff, closes
stdin and clears buffers. It includes opened workload secrets/run token, pool
**public** key and the controller's `ca_file` trust bundle, which the runner and
the run's tools trust beside the image's roots, only. It never sends pool private key, management API key, operator
key or worker session token. Secrets never enter PodSpec env/args, annotations,
Secret/ConfigMap objects, status, termination messages, logs or retained evidence.
Expected workload-mounted files retain their existing secret semantics.
Cluster/node administrators and principals allowed attach/exec can inspect the
execution; TLS and ephemeral volumes do not protect secrets from those trusted
operators. Restrict and audit those privileges accordingly.

A partial/ambiguous handoff is not replayed into a replacement Pod. The runner's
fenced `started` report decides whether execution begins. Failover adopts the
exact owned Pod and reports its terminal state; a missing Pod reports missing.
A pre-handoff incomplete init/main is terminated and the assignment fails; a
user retry creates new work. Keep durable non-secret Worker status before
side effects and retain terminal exit until server acknowledgement. Same run
apply/replan uses a **new assignment-version Pod**, restoring the sealed saved
workspace on its same pool. `restartPolicy: Never` and no Job retries/container
restart rules prevent hidden re-execution. A different worker returned during
registration makes old owned work obsolete; terminate it, never adopt a newer
unrelated Pod selected by label alone.

Stop uses the injected fixed signal helper over pods/exec to deliver SIGINT to
runner PID 1, which forwards to the command process group. Kill uses SIGKILL and
persists kill intent before signalling. No secret argument is required. Pod
ordinary deletion/SIGTERM is not a successful stop. Missing runtime permission
is a failure; OOM/eviction/missing/timeout are explicit terminal mappings.
Cleanup retries in bounded reconciliation attempts and never certifies removal
from a timeout alone. API fencing does not prove an unreachable old workload
stopped external side effects, so neither automatic Pod replacement nor
cross-cloud retry is permitted after handoff.

### Shared deadlines

Assignment gains `preparingDeadline`, required Unix seconds int64, computed by
the server from the assigned state's change time plus its effective pool timeout;
round toward the earlier whole second. It is not run creation time or token
expiry. New launchers honor it; older clients keep their existing server reap
fence during the supported rollout window. A pool timeout change affects future
assignments; the issued deadline is fixed for the attempt and persisted with it.
Runs' overdue enforcement uses that same issued deadline, avoiding disagreement
with a live changed pool setting.

Policy, source/restore, verified tool acquisition, scheduling/image pulls, init
transfer and main handoff all share its remaining time. Their existing individual
10-minute fetch/retry budgets become `min(individual, remaining preparation)`.
The controller keeps polling while preparing and cancels on deadline/revocation.
No step starts a fresh 15-minute preparation clock after restart.

Existing run timeout starts when the main handoff succeeds (default 70 minutes),
no-log default remains 7 minutes. Persist original execution start for recovery.
Controller also maintains absolute `hardDeadline = preparingDeadline +
runTimeout + cleanup_timeout` (cleanup_timeout default 120 seconds). It enforces
preparation and hard deadline even while Pod is Pending. Pod
`activeDeadlineSeconds = ceil(remaining preparation at creation + runTimeout +
cleanup_timeout)` is a secondary cap, at most 900+4200+120=**5220 seconds** with
the defaults; it ends no earlier than `hardDeadline`, since StartTime follows creation.
Kubernetes measures this from Pod StartTime, so it cannot replace the controller's
absolute scheduling/preparation watchdog. Neither cap resets on failover. The
120-second margin is an explicit infraweave operational choice, not an observed
reference default, nor a promise of extra after-run execution or the runner's
existing 10-minute upload/report retry opportunity. Run-timeout kill semantics
remain as in Docker. C67 stays open.

## Worker initialization and signed runs

This is launcher-local admission, distinct from the already implemented server
INITIALIZATION policy at READY. The local configuration is optional
`launcher.initialization_policy` (regular Rego file),
`launcher.initialization_policy_engine` (`REGO_V1` default, or `REGO_V0`) and
`launcher.initialization_timeout` (60 seconds default, positive and at most
60 seconds). The file is compiled once before registration; missing/invalid or
oversized source fails startup, and changing it requires restart. A configured
policy requires the worker specification's additive `stack.ulid` and
`commit.branch` fields; the existing `stack.id` remains its slug. Missing fields
from an older server fail that admission closed. Current run metadata has no
producer and is represented as an empty array, without inventing server-side
metadata ingestion or changing its unknown-metadata policy semantics.
The local adapter emits a typed projection, checks required facts, the supported
run-type enum and assignment run/state/version consistency. Identity strings
come from the authenticated server/pool-token producers; the adapter does not
independently parse every ULID. The launcher verifies the run JWT before the
adapter runs, as below.

The local implementation's combined deadline honors caller cancellation and
any earlier context deadline. Issued/restart-safe preparation deadlines retain
their own delivery owner; a local timeout does not prove them. Worker programs compile `exec` against the worker engine's own
capability set; server policy/simulation still cannot compile it, also after a
worker compilation has cached the same body, and worker queries use a separate
cache identity. The
[worker initialization verification](worker-initialization-verification.json)
records the bounded local implementation's candidate evidence; other worker
and cloud requirements remain unproven. Compile at worker startup with bounded embedded
OPA and a separate worker-only capability set. Default evaluation deadline is
60 seconds or remaining preparation, whichever is less; server initialization
stays 5 seconds. Common server evaluation, simulation and policies cannot compile
or invoke `exec`. Cache/capability keys distinguish the worker mode; cached
queries contain policy/modules only, never credentials, input or an executor
closure for a particular pool. Invoke context is evaluation-local and cancelled
with its assignment. Keep existing 2048 cache entries, eight evaluations per
process, 256-KiB policy, 64-MiB input, 64-KiB print and bounded decision messages
(100 x 4 KiB). A worker engine cannot extend a global server capability registry.

Initialization input is strict version 1, and contains exactly:

```json
{
  "version": 1,
  "run": {
    "id": "ULID", "type": "TRACKED", "user_provided_metadata": [],
    "commit": {"hash": "commit", "branch": "branch", "message": "message"},
    "token": "server-run-JWT",
    "token_trust": {
      "issuer": "server-issuer", "audience": "infraweave-runner",
      "jwks": "{\"keys\":[RFC 7517 public ES256 keys]}"
    }
  },
  "static_run_environment": {
    "stack_id": "ULID", "stack_slug": "slug", "space": "space-slug",
    "pool_id": "ULID", "account_id": "ULID", "runner_image": "image-reference"
  },
  "tool": {"name": "terraform", "version": "1.17.2", "download": true},
  "terragrunt": null
}
```

Run type uses worker RunSpec enum; commit string keys always exist and absent
metadata becomes an empty array. `tool`/`terragrunt` are null when absent;
Terragrunt otherwise has version/download. Stack slug/name and immutable IDs
come from the trusted spec, not a mutable controller display name. The additional
stack slug and metadata needed from server specification are explicit worker
schema additions. The input contains no complete opened run secrets, environment
or files. C68's vendor input remains unobserved; this is infraweave's own schema.

The server JWT is the current bare [runner credential](../../internal/access/internal/core/authn/worker.go):
issuer from server, `aud=infraweave-runner`, `iw_kind=runner`, subject run ULID,
issued/not-before/expiry times and jti. It contains **no signed pool/account/space
claim**. The launcher verifies it natively before the specification is fetched:
signature against the public keys `GET /worker/v1/run-token-keys` answers the
worker session, issuer, audience, kind, subject (the assigned run), jti and
times within 30 seconds; pool binding is from the authenticated assignment and
run/state-bound pool envelope, not a fictional JWT claim. A refused token
fails the run in PREPARING with `signed run admission refused:` and one fixed
reason, and keys that cannot be read or are invalid with `signed run admission
failed:`; neither names the token, a claim or a key. `run.token_trust` carries
the issuer, audience and keys (a JWK Set string, sorted by `kid`) that admitted
the token, so `io.jwt.decode_verify(input.run.token, {"cert":
input.run.token_trust.jwks, "iss": ..., "aud": ...})` verifies it again in
either dialect. Policy
may require an additional user-provided signature and verify its stack/commit
claims, independently of trusting server delivery. Exposing a token does not
make that extra admission automatic. An exec verifier is only as trustworthy as
its image: admission policies must pin/constrain the resolved immutable image
digest and trusted verifier, or verify natively against pinned keys. A stack
author-controlled image can fake exit zero; successful exec alone is no signature
proof. Key fetch uses the configured server and CA with the worker session,
a bounded 1-MiB response of at most 16 keys, a 30-second call under the total
deadline, a cache of at most five minutes, and one refresh for an unknown kid.
TLS or key failure fails closed. No token header, claim, user metadata or key
URL chooses another issuer or trust endpoint.

Evaluate policy **before source fetch/restore and override-tool acquisition**.
Image resolution/pull and a policy helper may occur first because verifier code
lives in the chosen runner image. No workspace, override tools, pool key,
management credential, worker session or automatic opened-secret environment
is mounted/exposed to that helper. A baked CLI can be called, but input's explicit
requested version does not falsely describe that baked CLI as the eventual
override. Deny/error/timeout fails in PREPARING with sanitized messages before
any user run hook. Apply/replan assignments repeat worker admission in their
preparing state, failing from PREPARING_APPLY/PREPARING_REPLAN respectively.

Each `exec(argv)` runs in a fresh isolated Docker container or Kubernetes helper
Pod of the selected run image with injected agent helper, fixed entrypoint,
UID/GID 1983, no service-account token, separate process namespace, fixed pod
security and bounded resources. The policy can explicitly pass verifier/signature
arguments from input; these travel in a strict <=64-KiB stdin/attach request
frame, never PodSpec command/args or persistent objects. Request fields are
`version`, `call`, `argv`, `timeoutMillis`; max 128 arguments, each <=4 KiB,
combined decoded argv <=32 KiB. Input token/signature values are added to the
masker before any diagnostic/output reaches logs. No shell interpolation is
implicit; a policy explicitly invoking a shell still uses this private frame.

Per evaluation: max eight exec calls, one in flight, <=60 seconds per call and
remaining total deadline, <=1 MiB stdout and <=1 MiB stderr per call,
**4 MiB aggregate combined stdout/stderr across all calls**. Output streams in
<=64-KiB length-framed chunks, then fixed `{call,status,exitCode,reason}` result;
no unbounded accumulation before checks. Budget exhaustion cancels helper and
fails admission; partial output is not success. Return to policy `{status,
stdout,stderr}`; policy print/deny logs remain subject to existing print/message
bounds and masking. Scope these new exec budgets to worker initialization;
wall-clock limits alone are not a memory bound. Helper ownership, termination,
restart cleanup and hard deadlines use the same controller mechanisms as run
Pods. Helper Pods do not register protocol workers or expand private-worker quota.

The policy outcomes of a call are explicit. A command that ends by itself,
including a nonzero exit, returns its status and output to the policy; a
command that cannot start returns status 127 with the reason on stderr. A
command the helper kills, or never starts, because the helper itself is
stopping, as its container's runtime or its Pod's active deadline stops it,
did not end by itself: its status never reaches the policy, so a policy that
denies only on a zero status cannot admit it. Every
other outcome halts the evaluation, so no deny rule is left undefined, and fails
the assignment's preparing state with `worker initialization policy evaluation
failed:` and one fixed reason: more than eight calls, arguments beyond their
bounds, the call's deadline, cancellation, output beyond its bounds, an image
that cannot be pulled, or a helper that fails. No reason names an argument,
output or image. Docker helpers run with every capability dropped, no new
privileges, 1 GiB memory and 512 processes; Kubernetes helper Pods have the run
Pod's fixed security, the pool's resources and placement, and an active
deadline ten seconds beyond the call's remaining time, so the call's own
deadline ends the call first and the Pod's ends only a helper its caller left. Helpers carry labels no run container
or Pod carries, and a restarted launcher's or controller's recovery removes any
helper of its worker before it looks for its run.

## Phase-scoped inputs and vendor signals

Configured passthrough env names use `ro_` for read exposure, `wo_` for write
exposure; remove that prefix for the child environment. Unprefixed inputs have
all exposure. Explicit exposure and a prefix must agree; collisions after prefix
removal or with managed runner variables are validation failures. Stack/context
variables retain their existing precedence; passthrough cannot override them.
All sensitive passthrough values enter the masker. Runtime-provided env/file
secrets retain existing sealing; no unfiltered controller environment is inherited.

| Execution | Exposures admitted |
|---|---|
| INITIALIZING/PLANNING (proposed, tracked, test, replan) | all + read |
| APPLYING (tracked/destroy) | all + write |
| TASK, including initialization for that task | all + write; a task is arbitrary potentially mutating work |
| after_run | Same set as the immediately preceding execution phase; no switch to the opposite credential set |
| Worker policy helper | No automatic passthrough/run secrets/mounts; explicit argv only |

RO/WO is exposure by phase, independently of filesystem `readOnly` semantics,
and every input is read-only to the run. An `all` file is a read-only mount
(Docker `:ro` bind; Kubernetes read-only Secret/ConfigMap projection or PVC),
which refuses a write with `EROFS`. A read or write file is never mounted,
because a plan and its autodeployed apply share one container: the launcher
or controller reads its content for the assignment, the handoff carries it,
and the runner writes it mode 0400 in a Pod-local or container-local
in-memory directory the run's user owns, leaving the directory mode 0500, so
a write is refused with `EACCES`. Docker gives that directory as a tmpfs;
Kubernetes as a directory the bootstrap creates in an in-memory emptyDir.
No writable shared Secret/ConfigMap or host volume exists. A phase transition
unsets the other side's variables in the runner's session, removes its files,
verifies their absence, failing the phase otherwise, and only then writes the
next permitted set, before user hooks; a confirmed apply's container starts
from the write set. Targets cannot overlap the workspace, so no saved
workspace holds a scoped file. Scoped files are at most 1 MiB each and 16 MiB
per run. Modes stop a stray write, not the run's own user, who may change
them. This guarantee covers managed injection paths, not arbitrary user
copies: commands already allowed a secret can copy or disclose it. Saved-workspace credential detection for run/source
tokens remains as implemented; it is not proof of complete secret scrubbing. These are chosen infraweave rules, not C66 parity proof.

Worker traces/metrics name the actual vendor and runtime, with bounded labels.
Expose signals only when the implemented vendor backend produced them. Each run
container (Docker labels `io.infraweave.vendor`/`io.infraweave.tool`) and run
Pod (`workers.infraweave.io/vendor`/`workers.infraweave.io/tool`) names its
stack's vendor, `terraform`, `opentofu` or `terragrunt`, else `unknown`, and
the tool its runner calls, `terraform` or `tofu`, else `unknown`; the launcher
or controller logs both at container start with the runtime (`docker` or
`kubernetes`), the verified release's version and whether the tool is that
release or the image's own. Acceptance executes Terraform and OpenTofu on both
runtimes; Terragrunt's labels name its driven tool as unit-proven. Shipped
Terraform/OpenTofu/Terragrunt runner execution gets transport/lifecycle resource
metrics; vendor-specific traces are eligible only when that driver's actual
collector is implemented and exercised. Unsupported Pulumi/Ansible/Kubernetes
backends remain unavailable until their phases and are labelled `unknown`;
their published image is not a backend proof. The public observability scope
stays unavailable. No claim of captured Spacelift vendor parity is made for
C69.

## Hosting, chart and evidence

One `deploy/helm/infraweave-workers` chart installs CRDs, controller Deployment,
Lease/RBAC/probes/NetworkPolicy and typed WorkerPool resources. Its strict
`values.schema.json` rejects unknown keys; its `workerPoolSpec` is generated
from the WorkerPool CRD (`make generate`). Values cover controller image digest,
bootstrap image digest, replicas (2), watch namespaces, cluster ID, existing
management/pool/policy/CA secret references, explicit pool IDs/mode/capacity,
resource/storage budgets and imagePullSecret refs. All chart images use immutable
pins, same-version static infraweave bootstrap and the existing GHCR runner
images. It does not put secret values in rendered ConfigMaps or accept arbitrary
PodSpecs that evade security. Run namespace egress must reach the configured
API/store/source/tool mirror and declared verifier hosts; DNS/network policy
allowances are explicit and may be restricted further by operators.

Each release names its own objects after itself, so releases share a namespace;
the controller's Role in each watch namespace has a fixed name, so Helm refuses a
second release that watches a namespace another release watches. An install
fails before creating anything on a value the schema refuses, a pool outside the
watch namespaces, a Managed pool without a management key reference, or, when
Helm reaches the cluster, a referenced Secret, ConfigMap or key that does not
exist. A replica is ready once the API server answers its Lease and, on the
leader, once a pass has read every watch namespace and reached every pool's
installation; liveness is the admin listener alone. Upgrades roll one replica at
a time with a replacement ready first, and the successor resumes each slot from
its Worker status. Uninstalling runs a pre-delete Job,
`infraweave worker-controller uninstall`, that deletes the release's WorkerPools
and waits while the still-running controller finalizes them, so runs end and
workers deregister before Helm deletes the controller; it refuses while a watch
namespace holds a WorkerPool the release does not own, and a timeout fails the
uninstall naming each remaining pool. Helm never deletes CRDs; installations that
share a cluster install them once and the others pass `--skip-crds`.

### Worker artifacts

A release publishes, through `worker-artifacts.yml` on `main`:

| Artifact | Reference | Provenance |
|---|---|---|
| Worker image | `ghcr.io/weaveplatform/infraweave-worker:<version>@sha256:…`, linux/amd64 and linux/arm64; the controller's image and the run Pods' bootstrap image | `GOFIPS140=certified` static build stamped with version and commit, SLSA provenance (mode=max) and SPDX SBOM attestations, labels `org.opencontainers.image.revision` and `io.infraweave.recipe` |
| Chart | `oci://ghcr.io/weaveplatform/charts/infraweave-workers:<version>@sha256:…`, `version` = `appVersion` | Default values pin the worker image's digest |
| Binaries | `SHA256SUMS` of the image's `infraweave-linux-{amd64,arm64}` | Release asset |

The image and chart are signed keylessly; verification pins the certificate
identity `https://github.com/weaveplatform/infraweave/.github/workflows/worker-artifacts.yml@refs/heads/main`
and the issuer `https://token.actions.githubusercontent.com`. The release's
`worker-artifacts.json` asset is the contract cloud modules consume:

```json
{"schema": 1, "kind": "worker-artifacts", "version": "…", "revision": "<commit>",
 "controller_image": "ghcr.io/weaveplatform/infraweave-worker@sha256:…",
 "bootstrap_image": "ghcr.io/weaveplatform/infraweave-worker@sha256:…",
 "platforms": {"linux/amd64": "sha256:…", "linux/arm64": "sha256:…"},
 "binaries": {"linux/amd64": "sha256:…", "linux/arm64": "sha256:…"},
 "fips": {"build": "GOFIPS140=certified", "default_mode": "on"},
 "chart": {"source": "oci://ghcr.io/weaveplatform/charts/infraweave-workers", "version": "…",
           "digest": "sha256:…", "sha256": "sha256:…"},
 "runner_images": {"<kind>": "ghcr.io/weaveplatform/infraweave-runner-<kind>@sha256:…"},
 "signature": {"identity": "…/worker-artifacts.yml@refs/heads/main",
               "issuer": "https://token.actions.githubusercontent.com"}}
```

A module installs `chart.source` at `chart.version`, checks the pulled package
against `chart.digest`, and passes `controller_image` as `controller.image`;
`runner_images` are the digests the release's server pins. Each pull request
builds the image without pushing, runs each platform's build, which must report
its commit and the certified module in FIPS 140-3 mode, and packages and lints
the chart pinned to a digest. The worker artifacts publish independently of the
server image; `deploy/infraweave-image/build.py` builds the certified binaries,
image and checks any infraweave image reuses.

The detailed [managed Kubernetes hosting contract](worker-hosting-contracts.md)
defines common typed semantic inputs/outputs, explicit three-cloud identifiers,
secret/state boundaries and ordered apply/run/destroy responsibilities.
Each cloud delivery includes its acceptance root in the same PR; module-only
completion is refused. Separate cluster and deployment states keep the cluster
available through finalizer and protocol cleanup. Provider/version pins belong
to the implementing cloud owner. The 12 GiB default resource/storage budget
requires explicit managed-profile admission and node-capacity evidence;
emptyDir size, resource requests/limits and available storage are distinct.

Worker-hosting Terraform modules live at `deploy/terraform/modules/workers/{aws,azure,google}`;
acceptance roots at `test/terraform/platform/workers/{aws,azure,google}` integrate
`test/terraform/lab`. Modules take existing server HTTPS endpoint, cluster/create
network inputs, cluster ID, namespace, chart/image pins, desired pool capacity
and existing credential references. They output cluster/namespace/pool-binding
identifiers and ownership for lifecycle checks, never private keys. The cloud
provisioner creates EKS Auto Mode, AKS or GKE Standard hosting and minimal
Kubernetes access/RBAC/worker-chart installation, or targets an explicitly named
existing managed cluster. This pulls only worker hosting forward; full server,
database, object-store/key-root deployment and workload cloud credentials remain
in their owning later phases. Terraform owns cloud resources; the controller
never provisions clusters or uses provider credentials to schedule across clouds.

[GKE Autopilot](https://docs.cloud.google.com/kubernetes-engine/docs/concepts/autopilot-security)
blocks ordinary privileged workloads and most hostPath use.
[EKS Auto Mode](https://docs.aws.amazon.com/eks/latest/userguide/auto-troubleshoot.html)
SELinux makes cross-Pod volume sharing assumptions unsafe; same-Pod local
staging avoids that requirement. [AKS](https://learn.microsoft.com/en-us/azure/aks/concepts-security)
separates cloud workload identity from Kubernetes API authorization. These
primary platform constraints support the portable design, not a claim that
infraweave cloud execution has already passed.

Acceptance keeps unique lab owner/state/namespace, exact revision/image/chart
pins, fail-on-empty/skipped scenario evidence, cleanup even on interruption,
destroy plus cloud API absence readback and a second-owner-survives negative
control. Concurrent proof runs distinct pools in all three clouds; sequential
single-cloud success cannot establish it. Keep Terraform state/plans, keys,
kubeconfigs/tokens and plaintext handoffs private. Native/OS/configured-lint/
certified-FIPS checks and >=95% total/>=90% per gated package remain required.
Every changed authenticated API gets real normal API/SDK success, 403 denial and
failure; operator/protocol cases use their stated credential classification.
Missing cloud credentials, simulated captures and docs checks are not real-cloud
proof. Remaining capture owners and delivered-versus-unproven classifications
are maintained in the linked observations and ownership matrix.
