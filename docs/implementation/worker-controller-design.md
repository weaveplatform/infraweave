# Kubernetes worker controller implementation notes

Status: implementation in progress for #745. The accepted
[worker contract](worker-pool-contracts.md#controller-and-resources) governs the
public interface; this document records the concrete ownership and recovery
choices needed to implement it. It does not claim controller or cloud delivery.

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

The controller has a separate execution port. Until its concrete Pod executor
is available, registration maintenance must not poll for/claim runs or report
execution readiness. A missing executor is a visible unavailable condition,
not successful capacity. Run/helper Pod execution, streamed preparation and
issued deadlines are owned by #746; lifecycle adoption/cleanup proof by #747.
This boundary lets #745 prove actual CRDs, ownership, registration and scoped
management without dropping assignments into a placeholder executor.

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

## Implementation checkpoint

The management API/storage slice is in progress: immutable owner validation,
account/space-scoped lookup, atomic management/hosting reservation, retained
deleted-resource tombstones, owner-and-version settings updates, explicit
ownership on merge patches, and an agent-only generated management client.
Unit contracts for kernel/workers, source OpenAPI contracts and HTTP mapping
pass under certified FIPS/race; application listener tests pass with their
required loopback permission. The full workers PostgreSQL acceptance suite now passes against the real pinned
service under certified FIPS/race, including management response recovery,
concurrent creation, account/space isolation, failed-write rollback, retained
tombstones and scoped purge. Docker recovery required a bounded Desktop restart
and termination of its unresponsive backend; no cache, image or volume pruning
was used. This storage slice is not controller completion evidence.
Agent-only Kubernetes core/Lease/dynamic client construction and durable resource
identity controls also pass certified-FIPS/race units and configured lint. These
controlled transport tests are not Kubernetes acceptance. Signed binding/slot
registration, live CRD validation, controller reconciliation, CLI and full k3s
controller acceptance remain outstanding. Canonical namespaced CRDs are drafted
under `deploy/helm/infraweave-workers/crds`; configuration now has strict scoped
namespace/leadership validation and Secret references. The configuration tests
pass certified FIPS/race. CRD installation is still unverified.
