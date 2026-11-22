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
