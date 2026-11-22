# Policy sample and capture retention

Implementation ticket: [#661](https://github.com/weaveplatform/infraweave/issues/661),
under [P13b](https://github.com/weaveplatform/infraweave/issues/636).
The public evidence-reading operations belong to #662. This ticket supplies their
storage owner and updates the existing public run receipt status.

Every evaluated policy records safe metadata. A true `sample` decision reserves
one slot in the account's database-clock UTC hour: 200 NOTIFICATION, 200 GIT_PUSH,
or 500 shared by the remaining types. Metadata, quota, encrypted staging and an
ID-only upload job commit with the evaluation. A quota refusal records the
evaluation without a sample; it does not change the policy decision. LOGIN's
runtime default remains `sample := true`.

The sample preserves the actual evaluated input, source, engine, result, outcome,
error and captured print. Neither queue payloads nor metadata contain these
private bytes. Staging is encrypted to its account/input row identity. Objects
use the existing streaming seal, with the content key bound to store and object
key. The upload releases its transaction and pooled connection before external
I/O. Publication commits the blob reference, input state and queue completion
atomically. A completed upload can recover publication under a new job generation
without repeating the immutable object's write. An ambiguous failed upload is
discarded and cannot turn a denied evaluation into permission.

Samples expire seven days after evaluation; delayed publication cannot extend
that ceiling. Status and reads enforce elapsed deadlines before any sweep.
`sampled` means published and currently available. `sampleExpired` means formerly
published evidence is now unavailable. Failed, pending and quota-refused samples
have neither status. The blob reaper removes sealed object bytes, while the
feature-independent policy sweep erases staging for expired, dead, cancelled or
pruned upload deliveries. Policy deletion retires its samples without resetting
the account's quota or removing a shared capture. Account purge removes input
references, evaluation metadata, capture heads and quotas, and schedules blob
deletion. A late upload cannot recreate a purged input.

## Capture privacy contract

Captures keep the newest published input for an account, space snapshot and kind.
The database reserves at most one refresh per minute, independently of sampling
and its quotas. A pending or failed replacement leaves the previous capture
readable. Publication changes the exact pending head and retires the old input
in one transaction. A stale uploader cannot clear a newer pending/current head.
Captures have the same seven-day private-data ceiling; their historical space
identity does not prevent space deletion.

Capture privacy and cadence remain **maintainer-approved implementation
inferences**, not additional observed Spacelift behavior. The typed input contract
marks sensitive fields with `capture:"sensitive"`; the published JSON schema
exposes `x-infraweave-capture-sensitive`. The projection preserves JSON types,
keys, array lengths and absent/null distinctions. Sensitive string leaves become
`[redacted]`, numbers become zero, booleans become false and null stays null.
Structural IDs, resource addresses, actions, states and masks remain available.
The conservative classifications cover commands, hooks, environment values,
session/IdP identity, commit authors/messages, user metadata, provider values,
plan values, outputs, Pulumi inputs/outputs and opaque Kubernetes bodies. An
exact evaluation sample does not use this projection.

Supported capture kinds are APPROVAL, LOGIN, NOTIFICATION, PLAN, GIT_PUSH, TASK and
TRIGGER_RUN_FINISH. INITIALIZATION and INTENT have no capture kind; future module
version captures remain outside this phase. Evaluation still avoids materializing
input when no policy applies.

## Executable evidence

`test/acceptance/features/policies/sampling.feature` passes **three scenarios /
85 steps** against two actual servers, PostgreSQL 18.7, Gitea and S3-compatible
object storage, with certified FIPS enabled. It verifies a sampled TASK denial
creates no run, private input/print are absent from raw storage and server logs,
swapped store/key identities fail to decrypt, `sample := false` still produces a
sanitized capture, and public APPROVAL receipts report retained then expired
samples before and after the actual cleanup/deletion jobs run.

The composed evaluator and two-replica transition tests identify each evaluation
and run-update event's fanout separately from the independent capture upload.
They verify the exact account, target space, event, input and job identities;
unsampled evaluations create one capture without spending sample quota. Caller
rollback, lease loss and stale retries preserve run/receipt/flag identities,
evaluation metadata, capture reservation time, staging state and queue generations.
Adapter controls also preserve the opaque sample-owner link across partially
written malformed receipt batches, foreign run owners and mismatched state
versions. Terminating an owned PostgreSQL connection or closing the pool cannot
acknowledge a receipt or return an empty successful metadata view. The stored link does not imply sample
publication or availability, and metadata never returns the raw decision.

The policy PostgreSQL acceptance tests use real OPA V0/V1 and sealed filesystem
objects. They race quota and capture reservations across two pools, including a
different session timezone; prove rollback and account/hour/type isolation; use
a one-connection pool to detect a connection held over upload; force a real
filesystem permission failure; and check failed replacements preserve current
capture availability. Cleanup tests cover corrupt staging, dead/cancelled/pruned
deliveries, policy deletion, account purge and expired tombstones. Paused real
uploads exercise generation loss, account purge and a newer capture winning
before the stale upload resumes. These authored scenarios are implementation
proof, not vendor captures. Complete CI coverage, parity, generation and FIPS
checks remain required before merge.

After integrating the merged LOGIN implementation, the combined public sampling,
approval and LOGIN-failure batch passes **26 scenarios / 631 steps**. The complete
policy package suite passes with race detection, randomized test order and
certified FIPS. Its merged local counters cover **256/282 sampling statements
(90.8%)** and **315/341 policy-storage statements (92.4%)**. These package results
do not substitute for CI's complete repository-wide coverage gate.

The tracked-run vendor-image compatibility case uses Spacelift's own official
GHCR backup. An anonymous registry read on 2026-11-22 verified that
`ghcr.io/spacelift-io/runner-terraform:v2.5.1_20261116` serves the unchanged pinned
OCI index `sha256:b48969aba860b11f7fbfc12acdd3be539d8befeea64edeef544655a50f819a35`,
including Linux amd64 and arm64. Its raw SHA-256 and registry digest both match
the existing ECR pin. The upstream
[README](https://github.com/spacelift-io/runner-terraform/blob/6c75296f90e3fafd9f40bf166af59286768cb096/README.md#docker-repository)
and [publisher](https://github.com/spacelift-io/runner-terraform/blob/6c75296f90e3fafd9f40bf166af59286768cb096/.github/workflows/publish/action.yml)
establish this backup's provenance. The compatibility case still requires the
same tracked plan/apply/FINISHED lifecycle, UID 1983 and persisted Terraform
state; the weekly pin updater follows that same upstream GHCR repository.

Cold release preparation serializes callers of the same pinned archive and
rechecks its verified cache. Transient HTTP responses, connection timeouts and
interrupted bodies have at most three attempts, with 100/200 ms backoff inside
the caller's existing context. Permanent responses, checksum failures and bad
archives remain terminal. Real TCP HTTP controls reproduce service failures,
closed connections and partial bodies; verify the retry ceiling and temporary
file cleanup; and prove cancellation of either a waiter or the initial owner
cannot cancel an independent caller. Failed preparations are not cached. The
unchanged mixed Terraform/OpenTofu/Terragrunt policy-worker feature passes all
**six scenarios / 220 steps** with certified FIPS; its tool pins, signature
verification and scenario bounds remain unchanged.
