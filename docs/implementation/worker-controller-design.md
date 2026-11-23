# Kubernetes worker controller implementation notes

Status: implemented for #745 (pool and worker resources, ownership and
registration), #746 (run execution in Pods) and #747 (recovery across restarts
and lifecycle failures). The accepted
[worker contract](worker-pool-contracts.md#controller-and-resources) governs the
public interface; this document records the concrete ownership and recovery
choices behind it. The chart (#748) is a separate delivery.

## Process and API boundaries

`infraweave worker-controller` is agent code. It uses the worker protocol for
pool binding and registration, and the existing account API for scoped Managed
pool creation/read/update/delete. It imports no server domain or database code.
The agent import rules allow kernel value types and its own wire clients, but
not `pkg/client`; generate the small management client from the same account
OpenAPI source, retaining one schema and normal server authorization.

Use official Kubernetes client/API modules at v0.37.2, matching the actual k3s
v1.37.2 lab. Its version-specific vulnerability lookup returned no results on
2026-11-22. Prefer the dynamic client for custom resources and typed core/Lease
clients; do not link an API-server implementation to operate CRDs.

## Durable ownership

A shared `kernel.WorkerOwner` is the immutable tuple of cluster ID, namespace
and opaque Kubernetes WorkerPool UID. Names are not identities. It appears in
Managed create/update inputs and in certificate-signed protocol proofs.

Use two distinct records within workers persistence:

- A private management registry reserves each Managed resource tuple and its
  pool ID beyond HTTP idempotency retention. It retains a tombstone when the
  pool is deleted or explicitly released for recovery. A stale resource cannot
  manufacture another pool when its status or a create response is lost.
  Account purge removes that account's retained registry records.
- A hosting binding selects the sole live owner of a private or public pool.
  It is acquired by the current pool certificate, not by an account management
  key alone. It survives controller restart and is released only after workers
  and active assignments are absent. Existing/Public finalizers retain the
  remote pool. Explicit reset recovery needs a replacement private key.

Managed create reserves management and hosting ownership atomically with the
pool. A normal update cannot add or replace management identity; owned settings
updates require the exact identity and current version. Normal authorized
recovery deletion/reset keeps its existing action checks. Managed lookup is
account/space scoped, returns no private key, and never adopts a name match.

Hosting owner acquisition is serialized with the pool and owner tuple. Same
owner plus fresh proof is idempotent; a competing owner, legacy unowned workers,
retained management tombstone or active work is a conflict. Identical signed
proof replay remains invalid: retries sign a fresh timestamp/nonce. Register
requires the exact bound owner, and a Docker registration cannot omit ownership
to join a bound pool. All certificate/current-generation checks and effects
remain in their short transaction; no network wait holds a database connection.

## Kubernetes reconciliation and execution readiness

Persist finalizers before remote effects. WorkerPool status records the stable
remote ID and owner; Worker objects have deterministic slot names and immutable
parent UID/pool/slot. A restart recovers that identity and re-registers the same
worker, rather than creating another due to empty process memory. Use versioned
status updates and bounded work per reconciliation. A quota denial stops further
slot creation for that attempt and remains a visible condition.

Registration also carries the signed logical slot. The server reserves at most
one live worker per bound pool/slot, and a fresh signed retry recovers that row
when the registration response or Kubernetes status write is lost. Persisting
only Worker status would leave a duplicate-registration window before the
returned ID reaches Kubernetes. Owned registration serializes its short creation
transaction before slot lookup/quota admission; ordinary public claim
transactions retain shared pool locking and independent organization fencing.
No registration response is evidence of execution readiness without an executor.

Lease leadership uses the agreed 15/10/2-second bounds, context cancellation and
explicit watch namespaces. Server ownership and Kubernetes resource-version
fences remain necessary during leader transitions. Never perform pool takeover,
credential reset or namespace-wide sweeping as automatic recovery.

The controller has a separate execution port: each slot's launcher executes
through the Kubernetes executor, `internal/agent/executor/kubernetes`. A
controller without Kubernetes stream configuration has no executor; its
registration maintenance neither polls for nor claims runs, and the missing
executor is a visible unavailable condition, not successful capacity.

## Recovery

Each assignment carries the server's issued `preparingDeadline`: the assigned
state's change time plus the pool's preparing timeout at the claim, rounded down
to the second. The runs domain stores it on the run and its reap fails a run
still preparing past it, so a changed pool setting never moves an issued
deadline. The launcher bounds its whole preparation by it, Pod scheduling,
image pulls, the bootstrap stream and the handoff included, and a restart
keeps it rather than starting another clock. The run Pod's
`activeDeadlineSeconds` is the time left to that deadline at creation plus the
run timeout and `cleanup_timeout`: the hard deadline as Kubernetes measures it,
from a Pod start no earlier than creation, so it never cuts a run short of it.

A slot is its launcher's state store. The launcher saves its state before each
effect a restart must know of: the assignment before preparation, the kill
intent before deleting the Pod, the runner's start once handed off, and the
exit until the server takes it. The slot writes it to the Worker's
`status.assignment` as a merge patch naming every field, absent ones as null,
with the Worker's UID, so a replacement of the same name is never written; the
reconciliation writes only `workerID`, `observedGeneration` and conditions
beside it. When leadership moves, the next leader starts the slot's launcher
from that record: it re-registers the same slot's worker, finds the run Pod the
Worker controls and was handed off, follows it to its exit and reports that
once; a Pod never handed off is deleted and the run reported missing, and a
recorded kill is applied again, its Pod's absence reporting killed. A Worker
retired while it records an assignment gets a launcher on the new leader so its
run ends before the worker deregisters. Server fencing makes a repeated exit
report harmless: the run fails only while its worker still holds it.

Adoption and cleanup consider only Pods whose controller owner reference is the
slot's Worker UID; Pods of another Worker, pool or namespace, or bearing the
worker label without that owner, are neither adopted nor deleted.

Loss is distinguishable. A Pod the cluster evicted or preempted, shown by the
`Evicted` reason or a true `DisruptionTarget` condition, reports `evicted`
("container evicted"); a Pod otherwise gone reports `missing` ("container
missing"); a worker whose installation is unreachable keeps its run, shows
`Connected=False` with `ServerUnreachable`, and is failed by the server as
"worker lost" only when it stays away for the lost-worker bound. None of these
reroutes the run to another pool.

Drain is the server's: a drained worker finishes its current run and claims no
other. The Worker's `Drained` condition shows it, and the pool counts a drained
slot as busy while its run executes and never as ready.

## Structural schema compatibility

Kubernetes explicitly rejects `additionalProperties: false` in CRD schemas,
and it cannot accompany named `properties`. The literal schema spelling in the
planning contract therefore cannot be installed. Use structural schemas with
every supported field named, typed maps for deliberate extension points,
pruning enabled, CEL invariants/immutability, and `fieldValidation=Strict` for
controller and acceptance writes. Configuration decoding remains strict.

A client that explicitly requests non-strict native API validation can receive
pruning rather than unknown-field rejection; unknown PodSpec fields are never
preserved or forwarded. Record that Kubernetes behavior honestly and exercise
both strict rejection and safe pruning against the real lab. Do not add
preserved unknown objects, arbitrary sidecars/commands or a raw PodSpec escape.
This is a schema-form correction, not evidence of runtime compatibility.

Primary source: [Kubernetes CRD validation restrictions](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#validation).

## Required real acceptance

Use `infraweave-lab`'s Terraform-owned k3s extension and normal application
replicas/SDK. Before completing this ticket, exercise:

1. Actual CRD installation, strict valid/invalid resources, immutable fields,
   missing/mismatched Secrets and scoped Kubernetes/account permissions.
2. Concurrent controllers/reconciles, status/create-response loss, restart and
   idempotency retention boundaries: one remote pool and stable worker IDs.
3. Multiple independent pool resources, wrong account/space, unowned pool
   refusal, competing cluster ownership, and the aggregate registration quota.
4. Pool/key reset, remote deletion, zero-capacity retirement and finalizers while
   resources remain; failures stay visible and retryable without force deletion.
5. Cleanup plus readback preserving another owner and borrowed resources.

Keep vendor C21 traffic explicitly unobserved. Rendered YAML, fake Kubernetes
clients and local helper tests establish no real-controller acceptance.

## Delivered behaviour

`infraweave worker-controller` (`internal/agent/controller`) leads a Lease and
reconciles every WorkerPool of its watch namespaces each five seconds:

- **Credentials.** A Managed pool exchanges the configured management key for
  an API token and requires `/api/v1/id` to equal `spec.account`. Existing and
  Public pools read `token` and `privateKey`; the token must name
  `spec.poolID` and certify the private key. The controller reaches the
  installation at `spec.endpoint` over HTTPS, trusting the system roots and
  `ca_file`; the token's own endpoint is not consulted.
- **Managed pools.** `GET /worker-pools/managed` finds the pool by owner. On
  404 it creates one only when status records no pool, with a certificate
  signing request of the Secret's key and `Idempotency-Key:
  worker-pool-<resource UID>`, then patches only the Secret's `token` key.
  Settings that differ from the resource are written with `If-Match`.
  A managed pool deleted elsewhere is reported as `RemotePoolMissing` and
  never recreated.
- **Binding.** Every mode binds with a fresh signed proof each pass. A pool
  bound to or managed for another owner answers 409, reported as
  `OwnershipConflict`.
- **Slots.** Worker resources `SlotName(uid, slot)` carry the pool label,
  owner reference and finalizer. Each slot registers with owner and slot and
  registers again every minute, well within the server's three-minute
  offline bound, which resumes its worker. A slot the private-workers limit
  refuses stays unregistered (`Registered=False`, `LimitReached`), and the
  pass creates no later slot. Each registered slot runs a launcher with the
  pool's token and key, `spec.endpoint` and its own cache directory, which
  polls, claims and executes one run at a time
  (`ExecutorAvailable=True`, `LauncherRunning`); a slot of changed
  credentials or endpoint starts again once idle. The Worker's status
  carries the claimed run, its Pod, handoff phase, deadlines and exit.
- **Execution.** A run Pod, named for its run and state version and owned
  by its Worker, runs the `bootstrap` init container of `bootstrap_image`,
  which receives the staged workspace, at most two verified tools and its
  own executable through attached stdin, then the `runner` container of the
  run's image, which receives the handoff through its own. Run Pods are
  Linux; the slot fetches tools for the controller's architecture and
  selects nodes of it. Stop executes the fixed signal helper; kill deletes
  the Pod with a one-second grace. A preparation failure deletes the Pod.
  The handoff carries `ca_file`'s bundle, so the runner and the run's tools
  trust a privately certified `spec.endpoint`.
- **Retirement and deletion.** A slot beyond capacity, or of a deleted pool,
  is deleted, its worker resumed and deregistered, and only then its
  finalizer released. A deleted Managed pool is then removed through the
  ordinary guarded delete; an Existing or Public pool's binding is released.
  Each cleanup attempt is bounded by `cleanup_timeout`; a refusal is
  recorded as `Terminating=False` with its reason and retried next pass.
- **Writes.** Finalizers and status are written with patches under strict
  field validation, so the controller never rewrites the user's spec.
- **Readiness.** `/readyz` checks that the API server answered the
  replica's Lease within the lease duration, that the leader's last pass
  read every watch namespace, and that it reached every pool's
  installation: a transport failure, a 5xx or an unanswered token exchange
  makes the leader unready, while a refusal (4xx, including 429) does not.
  `/livez` is the admin listener alone, so an outage never restarts it.
  The controller logs its commit and FIPS 140-3 mode and module as it
  starts.
- **Uninstall.** `infraweave worker-controller uninstall --selector`
  deletes the WorkerPools of the watch namespaces the selector matches, each
  with a UID precondition, and waits while the running controller finalizes
  them. It refuses an empty selector, or a watch namespace holding a
  WorkerPool the selector does not match, and on timeout names each
  remaining pool with its `Terminating` reason. The chart runs it as its
  pre-delete hook.

Real acceptance: `features/workers/hosting.feature` exercises binding,
release, slots, managed lookup and the aggregate limit against PostgreSQL;
`features/workers/kubernetes_controller.feature` runs replicated controller
processes against the Terraform-owned k3s lab and two servers;
`features/workers/kubernetes_execution.feature` executes OpenTofu and
Terraform runs, a local preview and failed preparations in the lab's Pods.
`features/workers/kubernetes_chart.feature` installs two releases of the
`infraweave-workers` chart through Helm's SDK with the imported image as the
controller's, runs a local preview in a release's Pods, and checks RBAC
denial, readiness through an outage, upgrade, restart, the draining
uninstall and the values the chart refuses.
