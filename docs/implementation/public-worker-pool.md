# Installation public worker pool

The installation operator can issue a single pool identity through
`/operator/v1/public-worker-pool`. Account credentials do not administer it.
Public workers use the ordinary `/worker/v1` registration, poll, sealed run
specification and reporting protocol. They have no fabricated account,
organization or space identity and consume no private-worker registration quota.
Tenant runs retain their own immutable account/organization ownership.

## Enablement and configuration

Public capacity is disabled by default. Enabling it neither creates hosting nor
registers workers. Configure every server replica consistently:

```yaml
worker:
  public_pool:
    enabled: true
    preparing_timeout: 15m
    allowed_registries: [ghcr.io]
    outgoing_ip_addresses: []
```

The default registry selectors are `azurecr.io`,
`dkr.ecr.<region>.amazonaws.com`, `docker.io`, `docker.pkg.dev`, `gcr.io`,
`ghcr.io`, `public.ecr.aws`, `quay.io`, `registry.gitlab.com` and
`registry.hub.docker.com`. Empty selectors admit no images. DNS suffixes match
label boundaries; IP/localhost selectors match exactly. A selector without a
port accepts only the default port or explicit 443; any other port is an explicit
opt-in. ECR's region selector requires a twelve-digit account and its actual
registry host shape. `docker.pkg.dev` also accepts Google's regional
`LOCATION-docker.pkg.dev` authorities. Complete image references are validated
and Docker shorthand is normalized before comparison. Lookalike host/path
prefixes do not match.

The shipped runner images are published on `ghcr.io`, which the default
selectors admit. A launcher without its own default runs the stack vendor's
shipped image, so an allowlist that omits `ghcr.io` needs every public launcher
configured with an admitted default image, or such runs fail in preparation.

The launcher applies the policy to its final image after the run/account image,
launcher default and vendor fallback have been resolved, before tool acquisition
or container creation. Public registrations must advertise signed
`public-pool-v1` support as well as `plan-sanitization-v1`, preventing older
launchers from silently ignoring this policy. Private execution carries no public
policy and retains its existing image behavior.

The preparation timeout must be positive whole seconds. Outgoing addresses are
an operator assertion of canonical IPv4/IPv6 literals, with no duplicates or
IPv4-mapped/zone-qualified forms; configuration does not provision or verify NAT.
`GET /outgoing-ip-addresses` answers these addresses while public execution is
enabled and none while it is disabled.

## Credentials and rotation

The host generates and retains a P-256 private key and submits only its CSR.
An operator bearer can create/read credentials or reset the certificate. Creation
uses the standard idempotency control: identical key/body replays its result;
a different creation after the singleton exists is a conflict. Reads return the
same stable pool ULID, version, ETag and launcher configuration. The server
stores the signed certificate, not the pool's private key.

Reset requires the current `If-Match`: absent is 428, stale is 412. Successful
rotation preserves the pool ULID and advances its certificate serial/version.
A public launcher restarting under the same certificate resumes its worker ID.
After rotation, replacement-key registration creates a fresh worker identity;
it never revives an old bearer by rewriting that bearer's worker serial.
Obsolete public polls cannot refresh liveness, and the ordinary reap retires
obsolete registrations and fails abandoned assignments. Existing work is not
replayed or silently adopted by a new worker.

`releaseBinding=true` additionally requires a different key, no registered
workers and no active public assignments, including assignments whose worker
row is gone. Missing/failing assignment storage refuses release. The eventual
controller binding lifecycle is a separate delivery. Private-worker reset's
pre-existing session-resume defect is tracked separately in
[#766](https://github.com/weaveplatform/infraweave/issues/766).

## Assignment and limits

An enabled public worker can claim eligible normal-priority, non-drift runs
without a private pool. Selection is FIFO by creation/ID across tenant accounts.
The ordinary transactional organization/account `public-worker-slots` check,
owner fences, candidate revalidation and state transition govern assignment;
full organizations/accounts are skipped so another eligible owner can proceed.
Billing disabled retains the existing unbounded Enterprise behavior. An account
ceiling cannot exceed its organization's pool. Usage is the count of public runs
in a worker-held state, so a terminal report, cancellation, stop or worker loss
releases its slot through the run's committed transition, exactly once across
replicas; a rolled-back assignment consumes none.

First assignment fixes the installation pool ID on the run; subsequent
apply/replan, handover, reporting, signalling and preparation reap use that
binding. It remains distinct from the selected private pool. Disabling public
capacity refuses new public registration/claims and does not redirect work to a
private pool. Samples and Kubernetes hosting retain their own implementation
owners in the [delivery register](worker-pool-contracts.json).

## Execution restrictions

A run keeps the pool selected when it was created, whatever later happens to
its stack. Creating a run on a stack that selects public execution refuses a
non-normal priority or drift detection with `422 validation-failed` before any
write. Push and trigger policy priorities apply only to private-pool targets, so
a public target is created at normal priority. `runPrioritySet` on a public run
answers `422 validation-failed` after authorization, and its allowed actions
never advertise `RUN_PRIORITIZE_SET`. The public claim independently skips any
stored candidate that is prioritized or drift detection. Private execution
keeps its priority tiers, drift limits and pool selection.

### Schedule producer dependency

The accepted contract also requires timed schedules to select a private pool.
The declared `stackScheduledRun*`, `stackScheduledTask*` and
`stackScheduledDelete*` routes still use generated unimplemented handlers, and
there is no timed run producer in this revision. Drift configuration is likewise
a declared schema without an executing producer. A uniform `501` is not proof
of a public-specific restriction. Current lifecycle drift refusal and the claim
exclusion prove those boundaries, not a working drift workflow.

The proposed integration for the future schedule owner uses the existing
`runsapi.Creator.Create` transaction and `CreateRequest.BeforeCreate` callback.
Under the owner/stack creation fence, admission must check that the locked stack
still selects a private pool before inserting the timed run. Schedule
configuration writes need the same private-only check under their owner fence.
Timed fires must use `Mode=None` with their job/fire deduplication committed in
the same transaction: `OncePerCommit` intentionally skips admission callbacks
for reused runs. Real public refusal and private timed firing acceptance remain
required before the schedule criterion is complete; no unused scheduled
provenance or producer execution is claimed here.

## Read projections

`PublicWorkerPool.parallelism` is a required nullable int64: the account's
effective public-worker-slots quota, the lower of the organization's pool and
the account's ceiling, null while billing is off or neither bounds it. The
literal `/worker-pools/public/search-schedulable-runs` and
`/worker-pools/public/search-users` paths, with their suggestions, read the
account's queue and users over the caller's readable spaces. The default public
runner image answers the account's own default, else the built-in Terraform
runner image; it is a display default and does not change a run's image
precedence.

## Verification

`features/workers/public_execution.feature` executes provider-free
`terraform_data` configurations through real launchers, PostgreSQL, object
storage, Gitea and Docker. It checks the exact pinned runner image with its baked
Terraform CLI, successful state persistence, an unlisted-image refusal and a
billing-enabled Free slot ceiling that queues work until the active run finishes.

`public_pool.feature` and `public_registration.feature` cover normal operator
routes, missing/stale preconditions, invalid CSR, wrong audiences, disabled
registration, signed capabilities, replay, same-certificate resume and rotation
revocation. PostgreSQL component tests exercise concurrent singleton creation,
certificate write failure/rollback, NULL tenant scope, active assignment/usage
readback, FIFO exclusions, obsolete-worker liveness and unavailable dependencies.
These are application evidence, not new vendor captures.

`public_execution_admission.feature` runs registry lookalikes and unadmitted
ports through a real launcher, which refuses them before creating a container,
and proves that stopping a public plan or losing its launcher admits waiting
work. `public_admission.feature` and `organization/public_capacity.feature`
cover priority refusal, authorization order and account ceilings through the
normal API. Real PostgreSQL tests claim with fifty concurrent public workers
across two replicas, two accounts of one organization and a second organization,
with billing on and off, and release capacity through failed assignments,
terminal reports, duplicate reports and worker loss. Cloud/controller gates
remain open.

`pool_projections.feature` and `pool_queues.feature` read the projections
through two replicas: users, queues with their positions and busy public
workers across readable spaces and another account, billing-off and Business
quotas, egress, the public runner image and usage cycles, tier refusals, stale
cursors, ordering-only filters and the reserved `public` selector.
`TestAccCaptures` replays C292's public observations as a REST subset.
