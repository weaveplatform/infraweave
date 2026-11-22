# 0008: Run execution and the worker protocol

Status: Accepted

## Context

A run is the unit of work in infraweave: a plan, an apply, a task, a test case or
a destroy, executed in a container on a worker that the customer runs. Three
facts shape the design.

- **The run state machine is API data.** Report 08 records 22 `RunState` values,
  20 of them reachable, and 43 transitions, with per-type paths, a replan loop, a
  second handover after confirmation and timers at 2, 7 and 30 days
  ([08 section 3](../reference/08-run-lifecycle.md#3-transitions)). Clients read
  `state`, `history` and `stateVersion`, and policies, notifications and VCS
  checks react to each change.
- **Execution happens where infraweave cannot reach.** Workers sit behind NAT in
  the customer's network and need no inbound connectivity. Spacelift moves work
  to them over MQTT or over a worker-initiated HTTP long-poll, and retires MQTT
  on 2027-03-30
  ([10 section 4.2](../reference/10-workers-and-execution.md#42-transports)).
  The poll paths and payloads are not published, and infraweave's clients are its
  own ([0001](0001-rest-first-spacelift-surface.md)), so the protocol is
  infraweave's to design.
- **The pieces around a run are already decided elsewhere.** Persistence and
  object storage are [0005](0005-persistence-postgresql.md), background work is
  [0011](0011-persistent-work-queue.md), sealing is
  [0013](0013-secrets-at-rest.md), policies are
  [0009](0009-policy-engine-opa.md), tokens are
  [0006](0006-authentication-and-tokens.md), the state backend is
  [0010](0010-state-backend.md) and source access is
  [0012](0012-vcs-integration-model.md). This record fixes how a run moves through
  its states and how a worker takes part. It answers PD-06
  ([30, product decisions](../reference/30-open-questions.md#product-decisions)).

## Decision

### The state machine is the authority

- **One transition table.** `internal/runs/internal/core/machine` holds the edges of the
  [run-transitions register](../../data/run-transitions.json) as data: report
  08's T1 to T43
  ([08 transition table](../reference/08-run-lifecycle.md#transition-table))
  and T44, T45 and the C52 assignment refusal T46, each with the register's
  source state, target state and run
  types, and the driver allowed to take it. A pair not in the table is
  refused. `UNKNOWN` is served in the enum and never assigned; `PENDING` is
  absent from the served enum (decision 0021).
- **One entry point.** Every state change goes through
  `lifecycle.Transition(ctx, tx, req)` in `internal/runs/internal/core/lifecycle`,
  with the run ID, the expected `state_version`, the
  target state, the driver, the actor and a note. In the caller's transaction it:
  1. locks the run row (`SELECT ... FOR UPDATE`, in 0005's lock order) and checks
     that `runs.state_version` equals the expected value and that the edge exists
     for the run's type and the driver;
  2. sets the new state, increments `state_version` and sets `state_changed_at`;
  3. appends a `run_state_transitions` row (`run`, `sequence` = the new
     `state_version`, `state`, `timestamp`, `until`, `username`, `note`,
     `terminal`, `has_logs`);
  4. writes the stack's `state` and `blocked` into the `stacks` domain's table
     through `stacksapi` when the run is the stack's tracked run and the state
     exists in `StackState`
     ([08 StackState](../reference/08-run-lifecycle.md#stackstate-and-runstate)).
     They are a projection written inside the transition's transaction, so stack
     lists filter, sort and count by state from their own table; the blocker and
     the run lock are read through `stacksapi.StackStatus`, which `runs` implements
     ([0004](0004-repository-architecture.md#constraints));
  5. emits a `run_updated` event with `outbox.Append` in the same transaction;
     decision 0015's fan-out, through the queue of
     [0011](0011-persistent-work-queue.md), reaches notifications, webhooks,
     the audit trail and the commit checks of
     [0012](0012-vcs-integration-model.md). When a blocking run ends, the
     transaction also enqueues decision 0009's `policy.trigger` job;
  6. when the new state is terminal, releases the stack lock the run holds in
     `stack_run_locks`. A transition writes only the `runs` domain's tables, so
     the `state` domain releases the run's rows in `state_locks` when it consumes
     the run's terminal `run_updated`, and `runsapi.StateAccess` refuses a lock to
     a run that is no longer active; a stopped, killed or failed run never keeps a
     state lock ([0010](0010-state-backend.md));
  7. enqueues `run.schedule` for the stack when the run leaves a state that holds
     or waits on the stack lock, and issues `NOTIFY infraweave_runs` with the pool
     ID when the new state is claimable (`READY`, `CONFIRMED`, `REPLAN_REQUESTED`).
- **`state_version` is a per-run counter**, one per transition. It is the
  optimistic concurrency token, it lets a state recur (the replan loop), and with
  the state it addresses a phase's logs, as `RunStateTransition.stateVersion` does
  ([08 section 9](../reference/08-run-lifecycle.md#9-the-run-entity)). A
  transition whose expected version is stale fails with `run.ErrConflict`, which
  the API answers as `409`, problem `invalid-state` with the current `state` in the
  body ([0022](0022-api-errors-and-update-semantics.md)).
- **Gaps in report 08 are closed by explicit choices** [I]:

  | Situation | infraweave's target | Edge |
  |---|---|---|
  | Handover fails in `PREPARING_REPLAN` | `FAILED` | added as T44, the analogue of T11 and T36 |
  | Worker lost, or container ended, while a test case is `APPLYING` | `FAILED`, by liveness alone | added as T45: T39 moves even a failed apply on to its destroy, which needs a worker |
  | `runKill` ends the process | `FAILED`, note `killed` | T15, T18, T38, T41 or T43 by the state killed in |
  | Run timeout or no-log timeout on the worker | `FAILED`, note naming the timeout | as for a kill |
  | Approval policy `reject` | `FAILED`, note `Run rejected by approval policies`, as Spacelift's console fails a run rejected in `QUEUED` ([32 section 16.2](../reference/32-spacelift-console-survey.md#162-policies-on-runs)); `PENDING_REVIEW` and `UNCONFIRMED` follow the same rule (C291) | T5, T24, T29 |
  | Push policy `cancel` naming a run in `UNCONFIRMED` | `DISCARDED`, note `canceled by push policy in favor of run <id>`, as Spacelift's console shows (C37) | T27 |
  | Upstream run in a dependency chain fails or is canceled | dependants `SKIPPED`, note naming the upstream run | T6 |
  | Drift run on a pool with drift runs disabled | created `QUEUED` and failed in the same transaction (T1 then T5), note explaining the limit | T1, T5 |
  | Task with `skipInitialization` | passes through `INITIALIZING`, which runs neither the vendor's init nor the `before_init` and `after_init` hooks and logs that initialization is skipped, as Spacelift's console shows (C42) | T13 |
  | `runRetry` | a new run of the retried run's type on its commit, with the runtime configuration the retried run recorded at its first handover (its hooks and settings as they were, not the stack's current ones) and its overrides; variables and files are read afresh, for a `PROPOSED` or `TRACKED` run that has ended other than in `FINISHED`, whose source still exists and, for a tracked run, whose commit is still the stack's tracked commit, as Spacelift's console retries ([32 section 17.3](../reference/32-spacelift-console-survey.md#173-run-states-discard-retry-and-targeted-replan)); a run without a record resolves afresh; `canRetry` and `retryBlocker` say whether and why not; a retry never triggers dependants, and a tracked run needs `RUN_RETRY_BLOCKING` as well | T1 |
  | `DESTROY` run | no plan and no confirmation: `INITIALIZING` straight to `DESTROYING` | T14 |
  | The `state` argument of `runStop` and `runKill` | the state the caller saw; a run that has left it answers `409` `invalid-state` with its current state | none |
  | `PARSE` run type | served in the enum, never created | none |

### Who drives each transition

| Driver | Transitions | Mechanism |
|---|---|---|
| API action | T3, T8 (discard), T4 and T9 (`runCancel`), T16 and T19 (`runStop` once the worker confirms), T26 (`runConfirm`), T27 (`runDiscard`), T28 (`runTargetedReplan`) | Handler calls `run.Transition` with the caller's identity; [0007](0007-authorization-spaces-and-roles.md) checks the run action first |
| Creation | T1 | Every route in [08 section 1](../reference/08-run-lifecycle.md#1-run-types) inserts the run in `QUEUED` in the transaction that creates it |
| Scheduler | T2, T5 and T6 for dependencies; for a push policy `cancel`, T4 on a queued run, T9 on a ready one and T27 on one awaiting confirmation | The `run.schedule` job, below |
| Approval policy | T5, T23, T24, T29, and the gates on T2 and T26 | Evaluated on entering `QUEUED`, `UNCONFIRMED` and `PENDING_REVIEW` and on each review ([09 section 8](../reference/09-policies.md#8-where-each-type-is-evaluated)) |
| Server INITIALIZATION policy | T46 | The worker claim evaluates the READY run before assignment; C52 fixes the refusal boundary |
| Worker claim | T7, T30, T33 | The poll claims the run (below) |
| Worker report | T10, T11, T12 to T16, T18 for a tool error, T19, T32, T35 to T43, T44 | Runner or launcher reports through `/worker/v1` |
| Server decision after planning | T17, T18 for a plan policy `deny`, T20, T21, T22 | The server evaluates plan policies on the reported plan ([0009](0009-policy-engine-opa.md)) and picks the edge |
| Timers | T4, T9, T25, T27, T31, T34 and the 30-day rule | Periodic task `run.timers` (every minute, [0011](0011-persistent-work-queue.md)) |
| Liveness | the `FAILED` edge out of whichever worker state the run is in | Periodic task `worker.reap` (below), for a lost run and an overdue preparation, and a poll that reports the run's container ended |

The worker never chooses a run's next state. It reports what happened, and the
server answers with the state the runner is to execute next, or with `pause` or
`exit`.

### From `QUEUED` to `READY`

`run.schedule` is a `default`-queue job with unique key `stack:<id>`, so bursts
coalesce into one evaluation ([0011](0011-persistent-work-queue.md)). It is
enqueued when a run is created, reaches a terminal state or `UNCONFIRMED`, is
reviewed, when an upstream run or external dependency settles, and on
`stackUnlock` and `stackEnable`. The handler locks the stack's `stack_run_locks`
row and walks its `QUEUED` runs oldest first
([08 section 4](../reference/08-run-lifecycle.md#4-queueing-the-stack-lock-and-scheduling)):

- **The stack lock.** `stack_run_locks`, keyed by stack, names the run holding the
  exclusive lock. A blocking run (`TRACKED`, `TASK`, `DESTROY`) moves to `READY`
  only when no run holds it, and takes it in the same transaction; it releases it on
  reaching a terminal state. Proposed and test runs never take it. A blocked run
  reports the holder as its blocker.
- **Gates.** The run's approval policies have approved; every upstream run it
  depends on has finished
  ([18 section 4.4](../reference/18-scheduling-drift-dependencies.md#44-run-ordering-and-states));
  every external dependency is `FINISHED` or `SKIPPED`
  ([18 section 6](../reference/18-scheduling-drift-dependencies.md#6-external-dependencies));
  the stack is not disabled. A failed gate takes T5 or T6 as listed above.
- **The human lock** is checked when a run is created, not here: only the lock
  owner may create runs on a locked stack, and autodeploy is off while it holds.

### Claiming runs

Runs are not jobs in the work queue. The run row is the record, and a worker's
poll claims it directly, so a run waiting days for a person never occupies a
queue lease ([27 section 10](../reference/27-reference-materials.md#10-work-queue-and-workflow-engines)).

- **Order.** `runs.priority_tier` (1 to 7) is computed from the priority preset,
  the run type and the drift flag, using report 08's seven tiers, and recomputed by
  `runPrioritySet` ([08 priority](../reference/08-run-lifecycle.md#priority)). A
  partial index on `(worker_pool_id, priority_tier, created_at, id)` where the
  state is claimable serves the claim. `runs.worker_id` and
  `runs.worker_pool_id` are columns of the run row holding the `workers`
  domain's IDs, so the claim writes no other table of `runs`.
- **The claim** runs in one transaction. It first locks the worker and takes
  any finite drift-limit fence, then probes candidates in priority, creation
  and ID order without locking a run ahead of its owners:

  ```text
  probe the next eligible account/pool entry
  savepoint: take nonblocking space → stack fences, then lock its exact run version
  on a miss: release every candidate lock before considering another target
  on a contended owner: exclude that stack for the rest of this queue walk
  on success: evaluate server INITIALIZATION for READY, before assignment
  ```

  A contended stack is excluded as a unit: its fence freeing partway through
  the walk must not let a lower-priority sibling overtake its earlier run.
  Exact revalidation includes account, pool, state, version and priority tier.
  An allowed claim takes `run.Transition` to `PREPARING`, `PREPARING_APPLY` or `PREPARING_REPLAN`,
  sets `runs.worker_id`, marks the worker busy and issues the run token
  ([0006](0006-authentication-and-tokens.md)). Two replicas never hand one run to
  two workers, and no replica is special. The poll locks the worker's row
  first, and a worker whose last claimed run is still on a worker claims
  none, even when its launcher, having lost the answer, reports idle; the
  preparing timeout ends such a run.
- **Limits applied in the claim.** Drift runs (tiers 6 and 7) are skipped when the
  pool's executing drift runs reach its drift detection run limit
  ([10 section 2.4](../reference/10-workers-and-execution.md#24-pool-settings-with-behaviour)).
  A claim of a drift run counts under a per-pool advisory lock, so two
  claims at once cannot pass the limit together. A high drift run is in
  tier 1, which no limit bounds.
  On the public worker pool, an account's runs are skipped while its
  organization's accounts together have as many runs executing as the
  organization's `public-worker-slots` limit, or the account alone has as
  many as its account limit when one is set
  ([0021](0021-capability-scope-and-supersession.md#limits)).
- **Wake-ups.** A replica holding idle polls `LISTEN`s on `infraweave_runs`. A run
  that enters a claimable state notifies `<pool>/<run>`, the run's ID keeping two
  such notifications in one transaction from folding into one, and each replica
  wakes one held poll for that pool, the one from the most recently registered
  idle worker, which approximates Spacelift's newest-first
  assignment ([10 section 5.3](../reference/10-workers-and-execution.md#53-scheduling-inside-a-pool)).
  A queue walk that encounters contention but finds no assignable run retries
  after a randomized 50–100ms wait, without a transaction or pooled connection.
  Jitter keeps workers sharing a stack from retrying in lockstep. A normal
  metadata transaction can release an owner without emitting a run event, so
  this retry does not depend on another assignment. Existing pool wakes can
  end the wait earlier. The original hold deadline, cancellation, superseding
  poll, shutdown and token-renewal behavior still bound it; idle pools with no
  candidates keep their ordinary notification hold. A poll always tries one
  claim before it starts to wait, so a missed notification costs
  at most one poll interval.

### The worker API

- **Contract.** The worker API is served under `/worker/v1` by chi in the same
  `serve` process as the public API, and is described by its own OpenAPI document,
  `api/worker.openapi.yaml`. It is not part of the Spacelift-mirrored surface.
  Errors are problem details ([0022](0022-api-errors-and-update-semantics.md)).
  It stands behind the controls every API surface shares, with the
  deviations [0003](0003-api-conventions.md) lists: no account action, no
  feature gate and no idempotency keys. A registration is bounded: the JWS
  at 32 KiB, the version, OS and architecture at 64 characters each, and at
  most 32 metadata entries with keys of 64 characters and values of 255.
- **Credentials** (formats are [0006](0006-authentication-and-tokens.md)'s):
  - **Pool token.** `workerPoolCreate` takes a base64 CSR, as in Spacelift
    ([10 section 2.3](../reference/10-workers-and-execution.md#23-credentials)).
    The server signs it with the pool CA, one ECDSA P-256 key per installation
    kept in `worker_pool_ca` and sealed by [0013](0013-secrets-at-rest.md),
    and returns the pool token: base64 JSON
    `{version, endpoint, pool, certificate}`. The pool key must be an EC P-256
    key, because it both signs registrations and opens the sealed specification
    through ECDH ([0013](0013-secrets-at-rest.md#delivery-to-runs)); a CSR with
    any other key answers `422`, problem `validation-failed`, naming P-256. The
    private key never leaves the customer.
  - **Reset.** `workerPoolReset` signs a new certificate and records its serial on
    the pool. Registrations and sessions bound to an older serial are refused at
    their next call. A held poll rechecks its generation before every successful
    return, including timeout and supersession, so old workers stop within one
    poll even if a replacement registration has already resumed the same ID.
  - **Registration.** `POST /worker/v1/register` carries a compact JWS signed with
    the pool private key, with the certificate in `x5c` and the payload
    `{pool, iat, jti, version, os, arch, metadata}`. The server checks the chain
    to the pool CA, the current serial, `iat` within five minutes, and that `jti`
    is unseen (table `worker_nonces`, kept 10 minutes). A new worker beyond the
    organization's `private-workers` limit, pooled across its accounts, answers
    `403`, problem `limit-reached`
    ([0021](0021-capability-scope-and-supersession.md#limits)). It answers with
    the worker ID and a worker session token (`aud` `infraweave-worker`, one
    hour).
  - **Resuming.** A registration that names an existing worker ID of the same
    pool resumes that worker instead of creating one, while the worker row
    still exists. A launcher that was offline and whose session token expired
    meanwhile resumes this way and keeps its run. A resumed worker is not
    counted against `private-workers` again.
    A private worker resumed with a replacement certificate keeps that ID but
    records the new serial: old session tokens remain revoked. An ordinary
    same-certificate restart retains the ID and generation.
  - **Session generation.** The stateless worker JWT carries the positive,
    worker-only signed `iw_worker_serial` claim
    ([0006](0006-authentication-and-tokens.md#token-kinds)). The bearer serial,
    worker registration serial and current pool serial must all match before
    liveness (`last_seen_at`, `busy`, `offline`, `available_at`), exited-run
    handling, run claims, signal delivery, renewal or deregistration can change
    state. A missing or stale generation answers `401` without those effects.
    Renewal retains the presented generation; it does not adopt one from a
    resumed worker row. Losing a renewal response leaves the old token usable
    until its own expiry while that generation remains current.
  - **Generation fence.** Registration takes the pool row `FOR SHARE` before
    touching a worker, and consumes the registration nonce, creates or resumes
    the worker, and signs its token in one transaction. A token issuance failure
    rolls those writes back, allowing the same registration proof to retry.
    Session mutations take the pool `FOR SHARE` before the worker `FOR UPDATE`,
    recheck all three serials, and commit their effects together. Reset's pool
    write lock serializes with those short transactions: an operation admitted
    first may commit before reset, while one admitted after reset must use the
    new generation. No lock or transaction spans a held poll's wait.
  - **Run token.** Issued at claim (`aud` `infraweave-runner`), scoped to one run,
    accepted only while the run is in a worker state and its `state_version`
    matches the run's current execution, and valid at most until the run ends
    plus one hour. It names the run by its ULID alone, which `runs` keeps
    unique across accounts. Its bearer is a `runner` principal
    ([0006](0006-authentication-and-tokens.md)), admitted only on the
    operations whose security names `runToken`, as a worker session token is
    admitted only on the others. A run token for another run's path answers
    `401`; a run no longer in a worker state answers `409` `invalid-state`, as
    a report for an execution that moved on does.
- **Operations.**

  | Operation | Caller and token | Purpose |
  |---|---|---|
  | `POST /worker/v1/register` | launcher, pool key | Register; returns worker ID and session token |
  | `POST /worker/v1/poll` | launcher, session | Heartbeat; returns an assignment, signals, the drain flag and a renewed session token |
  | `POST /worker/v1/deregister` | launcher, session | Clean exit; the worker row is removed |
  | `GET /worker/v1/runs/{run}/spec` | launcher, run token | The run specification |
  | `POST /worker/v1/runs/{run}/reports` | runner or launcher, run token | Phase started, succeeded, failed or stopped, for the run's current state version; answers the next step |
  | `POST /worker/v1/runs/{run}/upload-urls` | runner, run token | Pre-signed `PUT` URLs for log chunks, sealed run artifacts, the workspace and observability artifacts, each for a declared kind, name, size and SHA-256 under the run's current state and state version |
  | `POST /worker/v1/runs/{run}/log-chunks` | runner, run token | Commits uploaded chunks and indexes them in `run_log_chunks` |
  | `POST /worker/v1/runs/{run}/artifacts` | runner, run token | Commits uploaded run artifacts, workspaces and observability artifacts and records them in `run_artifacts` |
  | `GET /worker/v1/runs/{run}/workspace` | launcher, run token | `307` to a pre-signed `GET` of the run's latest saved workspace; `404` when it has none |

- **The poll.** The request carries `{busy, drained, run, version}`, where
  `drained` is the drain flag the launcher last learned. The server updates
  `workers.last_seen_at`, then answers at once when it has anything to say,
  including a drain flag other than the one reported, or holds the request until
  a wake-up or `poll_timeout`. The launcher asks for a hold
  between 5 and 120 seconds (default 60, Spacelift's default); the server caps it
  at `worker.poll_timeout_max`. A held poll holds no transaction or pooled
  connection ([0005](0005-persistence-postgresql.md)), and a worker's next
  poll answers its held one at once, so a worker holds one poll per replica.
  An authenticated, current generation's answer is `200`
  with a JSON body, empty when nothing happened, so a session renewal can ride on
  it. It may carry:

  | Field | Meaning |
  |---|---|
  | `assignment` | A claimed run: ID, state (`PREPARING`, `PREPARING_APPLY` or `PREPARING_REPLAN`), its state version after the claim, and the run token with its expiry. Only for an idle, undrained worker |
  | `signals` | `stop` or `kill` for the worker's current run; `cycle` |
  | `drained` | The worker's drain flag |
  | `sessionToken` | A renewed session token, sent when less than 15 minutes remain |

  A busy launcher keeps polling, so stop and kill reach it, and its poll is the
  run's heartbeat.
- **Versions.** The runner is the launcher's own executable, so launcher and
  runner always match. The server accepts launchers from its own minor version and
  the one before; an older one is refused at registration with `422`, problem
  `validation-failed`, whose `detail` names the oldest accepted version
  ([0022](0022-api-errors-and-update-semantics.md)).

### The run specification and its secrets

`GET .../spec` returns what the run needs, in one document: run, stack and commit
metadata, where the run names its account, the start of each of its objects'
keys; the source, as a git URL with a short-lived credential or as a
pre-signed archive URL ([0012](0012-vcs-integration-model.md) says which per
provider); the runtime configuration and hooks; tool versions and the runner image;
timeouts; environment variables and mounted files; the managed state backend's
address and credentials ([0010](0010-state-backend.md)); and the in-run API token
and OIDC token settings ([0006](0006-authentication-and-tokens.md)).

- **The resolved configuration.** The first successful `GET .../spec`
  fixes the merged hooks, project root, a named runtime/stack runner image,
  tool releases and the vendor's settings and execution flags. It records
  them in `runs.resolved_config`, under the run row's lock, in the same
  transaction as the specification's in-run token, artifact key and pool
  envelope sealing. A failed handover or sealed answer rolls them back,
  leaving no record. The worker's claim is an earlier transaction.
  Every later handover, including apply, replan and resumed preparation,
  uses this record even when the stack, its contexts or the catalogue
  change. Variables and mounted files are resolved and rendered at each
  handover, so a rotated secret reaches the apply; neither their values
  nor source credentials or authorization are snapshotted.

  The column is an internal envelope: `configuration` is the served
  `RuntimeConfig` shape, with all eleven hook lists, the resolved releases,
  selected vendor block and public YAML; `execution` keeps workspace,
  download, profiling, masking, output-upload and state-management settings
  that the public shape cannot express. Shared resolved releases and
  Terragrunt's mode have one authoritative place in `configuration`.
  Reads expose that configuration alone, absent before the first successful
  handover. Its environment lists the runtime configuration's variable keys with empty
  values, and its sparse paths are those the checkout uses. Its YAML contains
  supported runtime-file keys, with environment values empty, or the repository
  file's selected source projection described below; other
  vendor flags remain in JSON and the private execution settings. An
  unspecified runtime/stack runner image is recorded as an
  empty string and replayed as absent: account and launcher defaults are
  still resolved by those layers, rather than snapshotted here.

- **A run's runtime configuration.** `runTrigger` takes the eleven hook lists,
  environment variables, project root, runner image and sparse paths as fields,
  or `yaml` containing one stack's settings in the runtime file's keys
  ([07 section 7.3](../reference/07-stack-model.md#73-key-reference)).
  Its presence, including an empty object, needs
  `RUN_TRIGGER_WITH_CUSTOM_RUNTIME_CONFIG` on the stack's space beside
  `RUN_TRIGGER` ([0007](0007-authorization-spaces-and-roles.md)), and creates a new run
  each time. One strict parser checks both forms before creation: wrong types,
  unknown keys, reserved variables, an unclean relative project root and a
  vendor block with no effect answer `422 validation-failed`.
  A YAML environment value's origin may be shared with other environment
  values, but reusing it in an execution setting is refused at
  `/runtimeConfig/yaml`, naming the setting without the value. This includes
  environment candidates shadowed by YAML merges. Independent identical
  literals remain independent; no value comparison classifies them as aliases.
  Mapping keys sharing such an origin are refused before a diagnostic can
  name them. This privacy check also covers malformed object-valued
  environments; ordinary scalar/list type refusals remain unchanged.

  At the first handover, a supplied hook list replaces the stack's own list
  for that phase; an empty list clears it, and an omitted phase keeps it.
  Context hooks still wrap those lists in the environment's order (C29).
  Variables win over every stored source, unrendered, through the sealed
  overrides below (C102). Project root, runner image, tool constraints and
  workflow tool replace the stack's settings. A native `opentofu` or
  `terragrunt` block replaces that whole vendor block, with omitted switches
  defaulting false. Native OpenTofu versions belong to `opentofu.version`;
  top-level versions alone fail preparation. An unresolved custom release
  fails preparation naming `/runtimeConfig/yaml`. Terragrunt's
  `use_smart_sanitization` is retained; `use_state_management` cannot change
  the stack's backend ([0010](0010-state-backend.md)). Nonempty custom sparse
  paths win, otherwise the stack's paths apply. The resolved record fixes
  this merge, and the specification's own fields carry it to the worker;
  `runtimeConfig` there is only the redacted trigger reference.

  Run reads answer the supplied configuration as `customRuntimeConfig`, absent
  for a plain trigger, and the first handover's effective configuration as
  `runtimeConfig`. Both list environment keys with empty values and render
  YAML without their values. `runs.runtime_config` stores the typed input,
  never the original YAML or variable values. C311 captures what Spacelift
  answers; infraweave never answers a trigger's variable values.

  The first handover also reads `.infraweave/config.yml` at the run's commit,
  from the repository root, bounded at 1 MiB. The label
  `feature:ignore_runtime_config` skips it; a local preview reads no file and
  reports that its source is an archive. Provider errors fail the handover.
  Module keys are ignored for stack runs. The file's `version` is the string
  `"1"` (the default) or `"2"`. Strongest first: custom trigger input, the
  matching `stacks.<slug>` entry, then defaults and stack settings in version
  2, or stack settings and defaults in version 1. Each supplied entry key
  replaces its default value, including whole maps and lists; an absent key
  retains the default. Version 1 uses observable persisted stack presence:
  an empty own hook phase has no retained explicit-presence flag. File
  variables use this selected-map precedence, stay unrendered, preserve a
  stored name's write-only status, and are sealed with custom/proposal
  overrides in the first successful handover transaction. Later handovers
  and retries use that record and sealed values without rereading the file.

  Known file keys are typed strictly. A refusal names the key's path and is
  carried in the specification, alongside notices; at the start of
  `INITIALIZING` the runner logs both with `infraweave: .infraweave/config.yml:`.
  A refusal fails that phase before setup, hooks, commands or skip handling,
  using the stack's settings alone until then. Unknown keys warn by default;
  `runs.runtime_config.unknown_keys: reject` makes them fatal. The record
  retains notices and refusal for replay. Its YAML is the selected entry,
  else defaults, normalized without comments or external aliases and with
  environment origins redacted, including losing or unrelated environment
  anchors. Unknown diagnostic fields are retained: selected entry, then
  defaults, then root, keeping one value per key and every original-path
  notice. They never become execution settings. Sensitive environment
  origins cannot be reused in known execution settings; environment-only
  aliases and independent identical literals remain valid. Cycles or
  excessive alias expansion are typed refusals.

- **The clear part and the sealed part.** The specification answers the run
  token's own run alone. Its clear part carries everything but secrets. A
  write-only variable or file appears by name alone, with `writeOnly: true`,
  and so does a variable whose `${{ … }}` template reads a write-only one
  ([0024](0024-expression-language.md)). `sealed` holds
  `{environment, files, apiToken, apiTokenExpiresAt, artifactKey,
  sourceCredential}`: those values by name and path, the in-run API token,
  the artifact key and the source's credential. The
  server renders templates in the request's transaction, against the
  variables the stack resolves to, the stack and the run, with `now` fixed at
  the run's last transition. A template that does not render answers `422`
  `validation-failed` naming the variable, and leaves out the evaluator's
  message when the template reads a secret, so a failure cannot echo one.
- **The environment's sources.** The stack's own elements rank first;
  then directly attached contexts by priority, lowest number first, then
  by slug; then auto-attached contexts by name from Z to A, then by slug
  from Z to A. The first source of a name wins for variables and mounted
  files, and a name the stack drops lets the next source through. A stack
  cannot create an element whose name an attached context sets; a name
  the stack already has keeps its place when a context with that name
  attaches. Replacing that stack element remains allowed. Before a
  phase, the contexts' hooks run in rank order and then the stack's;
  after it, the reverse. A stack's `runtimeConfig` answers its stored
  environment in the same order, one winner per variable name or file
  path, carrying the winning context's attachment. The expression editor's
  environment samples and a saved stack template's secrecy use these same
  winners: a winning write-only variable is masked in samples and makes
  a template reading it write-only; a losing secret does neither.
- **Overrides.** A proposed run, from `proposeWithOverrides` or
  `runProposeLocalWorkspace`, may carry environment variable overrides for
  that run alone. A trigger's runtime configuration variables use the same
  mechanism on both tracked and proposed runs. They are kept in `runs.env_overrides_sealed`
  ([0013](0013-secrets-at-rest.md)), and only the specification answers
  them. The handover applies them over the variables the stack resolves
  to:
  - an override replaces the variable of the same name, unrendered, and
    stays write-only, in the sealed part, when that variable is;
  - a template reads the overridden value;
  - a name the stack does not set is set in the clear.

  No override names a variable infraweave sets for the run,
  `TF_VAR_infraweave_*`, `INFRAWEAVE_*` or `TF_APPEND_USER_AGENT`, and names
  follow a context's rules. A proposal with overrides creates a run each
  time, since no other run shares them. A plain trigger reuses only a run
  without overrides. A custom trigger always creates a new run, including
  when its configuration is empty; a retry retains its own reuse rule.
- **The runner image** is the run's runtime configuration's, else the
  stack's, else the account's default for private pools, else the
  launcher's default for the stack's vendor (see "Runner images").
- **The source.** The clear part's `source` names `kind`, `url` and
  `narrowed`, and the launcher fetches it before the container starts:
  - **`git`:** an HTTPS Git URL of the stack's repository, from which the
    launcher fetches the run's commit. The handover asks `vcs` for it
    through the `runsapi.RunSources` back edge, with the credential the
    sealed part carries as `sourceCredential` (`username`, `token`),
    absent for raw Git. `narrowed` says whether the credential reads that
    repository alone, and the run's log says which
    ([0012](0012-vcs-integration-model.md#source-access-for-runs)).
  - **`archive`:** a pre-signed `GET` of a local preview's `tar.gz` in the
    `uploads` store, with no credential. `uploadLocalWorkspace` takes the
    archive's size, at most 2 GiB, and SHA-256, and signs a `PUT` bound to
    them (DV-118), on a stack with `localPreviewEnabled`.
    `runProposeLocalWorkspace` commits the upload in the run's transaction
    and creates a `PROPOSED` run that records it, with the stack's tracked
    commit as its commit's metadata; proposing on the same upload again
    answers that run while it has not ended. The upload expires with the
    store's retention, a day after it started, and a run handed over after
    that fails with a note saying so.

  The launcher writes the source to `<cache_dir>/workspaces/<run>` and removes
  it when the run ends. Every HTTPS call the launcher makes, to the server, a
  Git provider, the archive store or a tool mirror, trusts the system's roots
  and those of `launcher.ca_file`, as the server's `vcs.ca_file` does for its
  providers. It fetches Git in process with go-git, so the host needs no
  `git`, the credential travels only in the request's `Authorization` header
  and is written to no file, and its SHA-1 is not the `crypto/sha1` that
  `fips140=only` refuses. It asks for the commit alone, without history; a
  server that does not serve a commit by its hash gets a fetch of every branch
  instead. An archive downloads up to 2 GiB, the bound of a saved workspace,
  and extracts to at most 8 GiB through an `os.Root`: an entry or a link that
  leaves the workspace, and a device or pipe, fail the run. No error names the
  archive's URL, whose signature reads it, and the credential is replaced in
  any error that echoes it. The project root must then be a directory of the
  workspace. The workspace is owned by UID and GID 1983 when the launcher may
  change owners; otherwise every user may read and write it, and the
  runner, before it exits, makes every directory the run made writable by
  every user too, so a launcher that is not root removes the workspace once
  the run pauses or ends, and restores a confirmed run's saved workspace
  into a clean one. A container that ends before its runner does so, as a
  killed one, leaves directories the launcher cannot remove; the launcher
  then runs its binary as `runner hand-back` in a container of the ended
  one's image, mounts and user, with no network, which does the same, and
  removes the workspace before the container. Only the launcher's user and
  the run's container reach it, through the launcher's cache directory.
- **The tool.** `tool` names `name` (`terraform`, `tofu` or `kubectl`),
  `version` and `download`; a Pulumi, CloudFormation or Ansible run names
  none (see "Vendors' runs"). The version is the Version Catalog's effective version
  for the stack's constraint, resolved at the first successful handover, so every phase of the
  run uses one release. A Terragrunt stack's run also names `terragrunt`
  (`version`, `download` and `runAll`), which drives the tool its `tool` setting
  chooses (`OPEN_TOFU` is `tofu`, `TERRAFORM_FOSS` is `terraform`;
  `MANUALLY_PROVISIONED` names no `tool`, so the image's own binary runs,
  found through `TG_TF_PATH` when the stack or image sets it, and a
  `terraformVersion` on such a stack answers `422` `validation-failed`);
  `runAll` comes from the run's runtime YAML terragrunt block's `use_run_all`
  when that block is present, false when the key is omitted, otherwise the
  stack's `useRunAll`. Malformed YAML or a non-boolean switch is refused at
  `/runtimeConfig/yaml` before a Terragrunt run is created; other vendors
  leave it unread. Its version is the newest Terragrunt release matching the stack's
  `terragruntVersion`, from Terragrunt's release index without alpha or
  release-candidate tags, and the stack reads both back as
  `effectiveVersions`, the tool's null for `MANUALLY_PROVISIONED`;
  `terragruntEffectiveVersion` resolves a pair of constraints the same
  way, each against its own index. The constraint is the stack's `terraformVersion`,
  else its vendor settings' `version`; a stack with neither runs the
  newest listed release. `download` is false when the stack sets
  `skipVendorBinaryDownload`, and the image's own binary runs. A
  constraint that resolves to no release answers `422`
  `validation-failed` naming the setting it comes from, `/terraformVersion`
  or `/vendorConfig/<vendor>/version`, as does a catalogue that lists no
  release, naming `/terraformVersion`; the launcher fails the run in
  preparation with that detail.
- **Masking.** `stack.enableWellKnownSecretMasking` is the stack's flag,
  which turns on the well-known patterns ([0013](0013-secrets-at-rest.md#masking)).
- **Sensitive outputs.** `stack.enableSensitiveOutputUpload` is the
  stack's flag, which lets the run keep its sensitive outputs' values
  where its launcher's `launcher.sensitive_output_upload` allows it too
  (see Outputs under "Logs and artifacts").
- **The run's work.** A `TASK` run's clear part carries
  `task: {command, skipInitialization}`, and a run claimed from
  `REPLAN_REQUESTED` carries `targets`, the resource addresses
  `runTargetedReplan` keeps in `runs.replan_targets`. The domain's
  constants bound them in both documents and the domain
  ([0003](0003-api-conventions.md)): 1 to 100 addresses of up to 1024
  characters. The domain also refuses, as `422` `validation-failed` at
  `/targets/<i>`, an address that is not a resource or module address
  as `-target` takes one, such as `aws_instance.web`, `data.aws_ami.base`
  or `module.app["a"].aws_instance.web[0]`; the runner still quotes each
  as one shell word, since a quoted key may hold any character. The
  runner fills the workflow template's `targets` from them, so the
  replan's plan command passes one `-target` for each. The tool plans each
  target with what it depends on, and Terraform's plan reads
  `complete: false`; a target that leaves out a resource a pending `moved`
  block moves fails the plan with the tool's error, which names the
  `-target` to add
  ([33 section 6.3](../reference/33-vendor-tool-documents.md#63-drift-refresh-only-targeted-and-destroy-plans)). An OpenTofu stack's
  `enableCPUProfiling` is in the clear part beside the tool.
- **The state backend.** For a stack that manages state, the clear part
  names `stateBackend.path`, `/state/v1/stacks/<stack ULID>`, and a stack
  without managed state has no `stateBackend`. The runner resolves the path
  against the server URL its launcher reaches, so the backend answers behind
  the same relay or private address as the protocol, and its credential is
  the run token the runner already holds ([0010](0010-state-backend.md)).
- **infraweave's own variables.** The variables infraweave sets in a run are
  named `INFRAWEAVE_*`, and the Terraform metadata variables
  `TF_VAR_infraweave_*`, where Spacelift's are `SPACELIFT_*` and
  `TF_VAR_spacelift_*` (0001's rename table), with the same suffixes and
  values except where the rename register records another. A stack's
  `runtimeConfig` lists the 17 metadata variables and
  `INFRAWEAVE_API_TOKEN` as computed elements, `runtime` true with the
  category value `INFRAWEAVE_ENVIRONMENT` and no value; its `config` lists only
  the elements set on it. A stored source of a computed name replaces
  that placeholder in this configuration-source view. The real API token
  and phase state are injected and protected separately at execution.
  The four launcher settings below are not computed elements of this
  view. The server puts the `TF_VAR_infraweave_*`
  variables it knows at handover in the clear part, unless the stack sets a
  variable of the same name: `account_name`, `commit_branch`, `commit_sha`,
  `local_preview`, `project_root`, `repository`, `run_id`, `run_trigger`,
  `run_type`, `space_id`, `stack_branch`, `stack_id`, `stack_labels`,
  `stack_labels_list` (the labels as an HCL list) and `workspace_root`
  (`/mnt/workspace`). The runner sets `run_state`, which changes with each
  phase, and `TF_VAR_infraweave_api_endpoint`, the REST API's base URL,
  derived from the worker endpoint its launcher reaches, keeping any relay
  prefix or private address. The variable names the API a run's provider
  calls, so RN-45 maps Spacelift's endpoint name to `api_endpoint`.
  The launcher's settings reach every phase through the handoff:
  `INFRAWEAVE_MAX_REQUESTS_PER_SECOND` and `INFRAWEAVE_MAX_REQUESTS_BURST`
  are positive integers, defaulting to 10 and 100, the server's standard
  rate class; C308 captures Spacelift's values. The runner sets
  `INFRAWEAVE_SENSITIVE_OUTPUT_UPLOAD_ENABLED` to the launcher's
  `sensitive_output_upload`, and `INFRAWEAVE_VENDOR_BINARY_CACHED` to true
  when the launcher mounts the tool from its cache (`tool.download`), false
  when the image's binary runs; C309 captures the cache flag's meaning.
  The runner also sets `TF_APPEND_USER_AGENT` to
  `infraweave/<runner version>`; C310 captures Spacelift's value. A stack
  variable of any of these names replaces the default. The launcher hands
  `INFRAWEAVE_API_TOKEN` over from the sealed part, and the runner sets it
  after the stack's variables so none replaces it. These match the observed
  names in [32 section 17.2](../reference/32-spacelift-console-survey.md#172-the-run-environment).
  P15 adds `INFRAWEAVE_OIDC_TOKEN`; P17 adds `TG_TF_REGISTRY_TOKEN` with
  the module registry's in-run credential, which
  [0014](0014-module-and-provider-registry.md) has the runner write into
  `~/.terraformrc`; P18b adds the trace context.

- **Secrets are sealed to the pool.** The server opens the sealed values
  ([0013](0013-secrets-at-rest.md)) and seals the secret part of the
  specification to the P-256 public key of the pool's current certificate in
  0013's pool envelope (ECDH P-256, HKDF-SHA256, AES-256-GCM), bound to the
  run's ID and the state version the claim answered. Only a holder of the
  pool private key can read it, and only for that run in that state.
- **The run's artifact key.** The sealed part also carries a fresh 256-bit
  artifact key generated for the run on its first fetch and the same key on
  every later one, since the fetch locks the run's row. The server keeps it as
  `runs.artifact_key_sealed`, sealed by the keyring
  ([0013](0013-secrets-at-rest.md)), so it can open what the runner seals with
  it.
- **The launcher decrypts.** The launcher opens the envelope and hands values to the
  container as environment and files. The pool private key never enters the run
  container or the server.
- **Workspaces are end to end.** When a tracked run with changes finishes
  planning, the runner archives the workspace (`tar` with `zstd`) and encrypts it
  in 0013's segmented AES-256-GCM format under a fresh data key. The data
  key is sealed in an envelope to the pool's public key, which the handoff
  carries, bound to the run and the state version that saved it, instead of
  the keyring:

  ```text
  "IWWS" | 0x01 | state version (uint64) | envelope length (uint16) | envelope | object stream
  ```

  The archive leaves out each `.terraform/providers`, which `init`
  installs again from the lock file, those in a Terragrunt unit's
  `.terragrunt-cache` too, which it otherwise keeps, and leaves out and
  logs a link that
  leaves the workspace, a device, a pipe or a socket. A file, link or name
  that holds the run token or the source credential fails the planning,
  naming the path but never the value, as does a sealed workspace over
  2 GiB. The runner uploads it to `workspaces` before it reports the plan,
  since the plan's answer is not known yet. For `PREPARING_APPLY` and
  `PREPARING_REPLAN` the launcher asks `GET .../workspace` with the run
  token and does not follow the redirect, so the token never reaches the
  store; it downloads the pre-signed URL, opens the data key with the
  pool's private key and extracts it as it extracts an archive source,
  reading the stream to its end so a truncated or altered workspace fails.
  A run with none saved, or one that does not open, fails its preparation.
  The runner in the new container runs the `init` hooks and command
  before the phase, since the providers were left out. The server stores
  but cannot read it, which is Spacelift's guarantee for private pools
  ([10 section 1.1](../reference/10-workers-and-execution.md#11-public-and-private-pools-compared)).

### Phases, reports and the plan decision

- **Phase order and hooks** follow report 08
  ([08 section 5](../reference/08-run-lifecycle.md#5-phases-and-hooks)): the
  runner executes the hooks and the vendor commands of each state in one shell
  session, and `after_run` runs whenever the runner processed the run.
- **Reports.** A report is `{stateVersion, state, event, details}` with `event`
  `started`, `succeeded`, `failed` or `stopped`, and names the run's current
  state version. A `succeeded`, `failed` or `stopped` report also carries
  `logChunks`, how many chunks the runner cut the state's log into, which
  the state's transition keeps (see "Logs and artifacts").
  - **`started`** of the state a preparing run moves to ends the handover
    (T10, T32, T35). `started` of the state the run is already in moves
    nothing and answers that state.
  - **`succeeded`, `failed` and `stopped`** name the run's current state and
    take its worker-report edge: to the next phase, to `FAILED` or to
    `STOPPED`. A report the state has no such edge for answers `409`
    `invalid-state`.
  - **The answer** is the next step: a state to execute, `pause` (the run
    waits in `UNCONFIRMED` or `PENDING_REVIEW`, and the container exits) or
    `exit` for a terminal state, with the state version the next report
    names.
  - **A repeated report.** A report whose answer was lost names the state
    version before the current one. When that report takes the run to the
    state it is in, the server answers the same step again and moves
    nothing, so a runner retries a report safely.
  - **A run that moved on.** Any other report for an earlier state version,
    or for a run no longer on a worker, answers `409` `invalid-state`, for
    example when a timer ended the run, and the runner exits without further
    reports.
- **A Terragrunt run** writes each unit's backend file in `INITIALIZING`
  ([0010](0010-state-backend.md)), failing the phase with a note that names
  the first unit the backend cannot hold, and runs each phase's tool
  commands as `terragrunt run --all -- <command>` under run-all. Without it,
  only the project root's unit runs, through `terragrunt run -- <command>`,
  and a root without `terragrunt.hcl` fails `INITIALIZING` naming it.
  Both modes set `TG_TF_PATH` naming
  the mounted tool, or the image's own when the specification does not
  download it, and `TG_NON_INTERACTIVE` set. Its plan command runs with
  `TG_JSON_OUT_DIR`, the variable of `--json-out-dir`, naming a directory
  of the runner's temporary directory for that command alone, and saves
  each unit's plan as `tfplan.tfplan` (`planFileName`), the name
  Terragrunt reads to write the plan's JSON, which it writes as
  `<unit path>/tfplan.json` under that directory, the unit's `show -json`
  ([33 section 7.2](../reference/33-vendor-tool-documents.md#72-plans-outputs-and-render)). After planning the runner
  reads each unit's plan from that directory and reports their summed
  `delta`, failing on a plan that does not parse, naming its unit, or on
  none under run-all. A single-unit plan that writes no JSON there falls
  back to `terragrunt run -- show -json tfplan.tfplan`, reading the root's
  saved plan. Terragrunt 1.2.2 writes the JSON directory under `--all` only.
  Run-all planning never invokes `show -json`: it re-evaluates a unit's
  dependencies and can fail before they have outputs when mocks allow
  `plan` but not `show`. The built-in `showState`, `showPlan` and
  `getOutputs` templates always use `terragrunt run --` in one unit's
  directory, in either mode. Only `init`, `workspaceSelect`,
  `workspaceNew`, `plan`, `apply` and `destroy` take `--all` under run-all.
  Terragrunt approves `apply` and `destroy` under run-all; single-unit
  commands carry `-auto-approve`. Without
  `skipReplan`, `apply` plans again rather than reading the saved plan, so
  mocked outputs never reach an apply; with `skipReplan`, or with the
  deprecated `skipReplanWhenRunAll` under run-all, it applies each unit's
  saved `tfplan.tfplan` (C188). A tracked run whose plan changes something saves its
  workspace for the confirmation, Terragrunt's cache of each unit with it.
  P13 adds unit plan collection and the minimal sanitized `plan.json` upload
  needed for policies, with both root-unit and run-all selection. P20 owns full
  aggregate changes and unit-named outputs; until then no output projection is
  advertised. Saved binary plans/workspaces remain separate from policy JSON.
  It uploads `plan.json` as `{"units": [{"path", "plan"}]}`, each unit's
  path from the project root, empty for the root unit, with its plan's
  JSON, and `changes.json` with each change's unit in a field of its own,
  so no reader splits an address; the plan policy input's `terragrunt`
  block has one entry per unit. Its outputs are each unit's `output -json`,
  named `<unit>/<output>`, read with `terragrunt run -- output -json` in
  each unit's directory, since `run --all` writes the units' documents one
  after another with no unit's name
  ([33 section 7.2](../reference/33-vendor-tool-documents.md#72-plans-outputs-and-render)).
- **After planning** the runner uploads two sealed artifacts: `plan.json`,
  the tool's `show -json` of the plan, and `changes.json`, the changes
  `internal/iac` parses from it, which the server reads for the run's
  changes. It then reports `succeeded` with the plan's `delta`: the counts
  of resources to add, change, delete and import, whether any output
  changes, and optionally the counts it moves and forgets and the
  state's resource count before the plan (`move`, `forget` and
  `resources`, zero when absent), so the run's delta answers every
  Terraform count of report 08's `Delta`. A `succeeded` report of `PLANNING`
  without a `delta`, or any other report with one, answers `400`
  `invalid-request`. In that request's transaction the server evaluates the
  plan policies on the uploaded plan ([0009](0009-policy-engine-opa.md)),
  records the receipts, and takes T17, T18, T20, T21 or T22 by the rules of
  [09 section 8](../reference/09-policies.md#8-where-each-type-is-evaluated).
  The run stays `PLANNING` until then, so a worker cannot skip a policy. With
  no plan policy attached, the decision reads the delta alone:

  | Run | Delta | Stack | Edge | Answer |
  |---|---|---|---|---|
  | proposed | any | any | T17 to `FINISHED` | `exit` |
  | tracked | no resource or output change | any | T17 to `FINISHED` | `exit` |
  | tracked | changes | autodeploy on and unlocked | T21 to `APPLYING` | `execute` |
  | tracked | changes | autodeploy off, or locked | T20 to `UNCONFIRMED` | `pause` |
  | test case | any | any | T21 to `APPLYING` | `execute` |

  The run keeps its delta for its changes view. A `failed` or `stopped`
  report of `PLANNING` is taken as in any state.
- **The Terraform family's plan** is `show -json` of the saved plan, format
  version 1.x, whose members and change kinds
  [33 section 6.2](../reference/33-vendor-tool-documents.md#62-the-plan-document) records for Terraform and
  OpenTofu alike; OpenTofu writes no `applyable` or `complete`. A drift run
  plans as any proposed run does, never `-refresh-only`, whose plan has no
  `resource_changes`: its plan holds `resource_drift`, each resource changed
  outside the tool as the refresh found it, beside the `resource_changes`
  that repair it
  ([33 section 6.3](../reference/33-vendor-tool-documents.md#63-drift-refresh-only-targeted-and-destroy-plans)),
  and `internal/iac` reads both.

### Vendors' runs

Pulumi, CloudFormation, Kubernetes and Ansible runs follow the same phases,
hooks, reports and plan decision as the Terraform family's. What differs is
each phase's commands, the plan document, the outputs, the image and the
tool. Report [33](../reference/33-vendor-tool-documents.md) records what each
tool writes, and `test/fixtures/vendor-documents` keeps those documents.

- **What every vendor uploads.** After planning the runner uploads
  `plan.json`, the vendor's plan document below, sanitized
  ([0009](0009-policy-engine-opa.md#vendor-plan-documents)), and
  `changes.json`, the changes `internal/iac/<vendor>` parses from it, and
  reports the delta. A plan document that does not parse, or that exceeds
  the plan bound, fails planning, naming the vendor. A replacement counts
  once in add and once in delete, as a Terraform replacement does. No
  vendor in this section manages state through
  [0010](0010-state-backend.md), so their runs carry no `stateBackend`.
  `changesV2`, `changesV3` and `searchEntityChanges` read every vendor's
  `changes.json` alike.
- **Extra arguments.** `INFRAWEAVE_PULUMI_CLI_ARGS_preview`, `_up` and
  `_destroy`, and `INFRAWEAVE_ANSIBLE_CLI_ARGS`, `_plan` and `_apply`, are
  split into words as `sh` splits them, without expansion, and appended to
  the phase's command, each word quoted.

**Pulumi** runs on the image's CLI; the specification names no tool.

| Phase | Commands |
|---|---|
| `INITIALIZING` | `before_init` hooks; `pulumi login --non-interactive <loginURL>`; `pulumi stack select --create <stackName> --non-interactive`; `after_init` hooks |
| `PLANNING` | `pulumi preview --refresh --diff --show-replacement-steps --non-interactive --event-log <runner temp>/events.jsonl`, with `PULUMI_DEBUG_COMMANDS=true` in that command's environment alone |
| `APPLYING` | `pulumi up --refresh --diff --show-replacement-steps --yes --non-interactive`, then `pulumi stack export` for the outputs |
| `DESTROYING` | `pulumi destroy --refresh --yes --non-interactive` |

- **Hooks first.** `before_init` hooks run before the login, as Spacelift's
  do, so a hook can export `PULUMI_ACCESS_TOKEN` or cloud credentials the
  login needs.
- **The event log.** The CLI accepts `--event-log` only under
  `PULUMI_DEBUG_COMMANDS=true`, which the Automation API sets too. The run
  log keeps the preview's human diff, and the steps come from the log
  file. The nightly job runs the Pulumi scenarios on the newest Pulumi
  release ([0016](0016-testing-and-acceptance.md#ci)), where a change to
  the flag fails first.
- **The plan document** is `{"steps": [...]}`: one step per
  `resOutputsEvent` of the preview, in order, each
  `{op, urn, type, provider, old, new}` from the event's `metadata`, the
  shape of Spacelift's `pulumi.steps`. The stack resource's last step
  carries the stack's outputs, and each default provider has its own step.
  A value unknown at preview stays Pulumi's sentinel string
  `04da6b54-80e4-46f7-96ec-b56ff0331ba9`. A preview whose `summaryEvent`
  is missing or whose `result` is not `succeeded` fails planning.
- **Changes** are one per URN, from its steps other than `refresh`; the URN
  is the address, the type token the type, and the URN's last segment the
  name:

  | Pulumi step | Change type |
  |---|---|
  | `same` | `NOOP` |
  | `create` | `ADD` |
  | `update` | `CHANGE` |
  | `delete` | `DELETE` |
  | `replace`, with its `create-replacement` and `delete-replaced` | `REPLACE_CREATE_BEFORE_DESTROY` when `create-replacement` comes first, else `REPLACE_DESTROY_BEFORE_CREATE` |
  | `read` | `READ` |
  | `import` | `IMPORT` |
  | `discard` | `FORGET` |

  The delta's `resources` counts the custom resources with an `old` state,
  and its output flag is set when the stack resource's last step changes
  its outputs.
- **Applying previews again.** `pulumi up` without `--skip-preview` previews
  and then updates, as Spacelift's applies do. It applies the program as
  it stands, not a saved plan, which Pulumi offers only as a preview
  feature.
- **Outputs** are the root `pulumi:pulumi:Stack` resource's `outputs` in
  `pulumi stack export`: a secret signature object
  (`4dabf18193072939515e22adb298388d`) is a sensitive output. When the run
  keeps sensitive outputs' values (see Outputs under "Logs and artifacts"),
  the runner exports with `--show-secrets` and takes the value from the
  object's `plaintext`. `pulumi stack output --json` is not read: its
  `"[secret]"` cannot be told from a literal string.
- **Destroying previews first.** A resource the preview refuses, such as a
  protected one, fails the run before anything is deleted; with
  `--skip-preview` the same destroy deletes part of the stack and then
  fails.
- **The passphrase.** The runner sets none. A stack whose backend uses
  Pulumi's passphrase provider sets `PULUMI_CONFIG_PASSPHRASE` as a
  write-only variable or `PULUMI_CONFIG_PASSPHRASE_FILE` as a mounted file;
  without either, Pulumi's own error fails `INITIALIZING`. The salt is in
  the backend and in the `Pulumi.<stack>.yaml` that `stack select` writes,
  which the workspace archive carries from planning to applying. Plugins
  live in the container's home, not the workspace, so applying downloads
  them again, as `init` installs Terraform's providers again.

**CloudFormation** runs `infraweave runner cloudformation <step>`, the
runner binary's driver over aws-sdk-go-v2, with the AWS SDK's credential
chain and `AWS_ENDPOINT_URL*`. The phase's shell runs it, so variables a
hook exports reach it, and no image needs the AWS CLI; the specification
names no tool. `CF_METADATA_REGION`, `CF_METADATA_STACK_NAME`,
`CF_METADATA_ENTRY_TEMPLATE_FILE` and `CF_METADATA_TEMPLATE_BUCKET` are set
for every phase's hooks.

| Phase | Steps |
|---|---|
| `INITIALIZING` | hooks; `DescribeStacks` decides the change set's type: `CREATE` when the stack is missing or in `REVIEW_IN_PROGRESS`, else `UPDATE` |
| `PLANNING` | upload the entry template, and each nested `AWS::CloudFormation::Stack` whose `TemplateURL` is a local path rewritten to its uploaded `https://` URL, to `s3://<templateBucket>/<run ULID>/`; `CreateChangeSet` named `infraweave-<run ULID>` with `TemplateURL`, the parameters of `CF_PARAM_<name>`, the capabilities of `CF_CAPABILITY_<name>=1` and `IncludeNestedStacks`, without property values; `DescribeChangeSet` until it settles, every page and every nested change set |
| `APPLYING` | `ExecuteChangeSet` of that change set with the run's ULID as `ClientRequestToken`; `DescribeStackEvents` of the stack and its nested stacks into the log until the stack settles; `DescribeStacks` for the outputs |
| `DESTROYING` | `DeleteStack`, then `DescribeStacks` until it is gone |

- **The plan document** is `{"parameters": [...], "changes": [...]}`: the
  change set's `Parameters`, and every `Change` of the root change set
  and of each nested one, as the API answers them, each with `stack`, the
  logical IDs of its nested stacks from the root joined by `/`, empty at
  the root.
- **An empty plan.** A change set that fails because it holds no change is
  a plan with no changes, matched by its `StatusReason` (C303); any other
  failure fails planning with that reason.
- **Changes.** The address is `stack` and the `LogicalResourceId` joined
  by `/`, the type the `ResourceType`. `Add` is `ADD`, `Remove` `DELETE`,
  `Import` `IMPORT`, `Modify` with `Replacement: True` a replacement that
  creates first, and any other `Modify` or `Dynamic` `CHANGE`.
- **The confirmed change set.** `APPLYING` executes the change set named
  for the run, so it applies exactly what was confirmed. Executing a
  change set deletes the stack's others, so a run whose change set another
  run's apply removed fails `APPLYING` with a note saying so.
- **A stack that fails to settle**, ending in a `*_FAILED` or
  `*_ROLLBACK_COMPLETE` status, fails the phase with the first failed
  event's reason.
- **Outputs** are `DescribeStacks`' `Outputs`, every one plain: the API
  marks none sensitive, and `NoEcho` does not mask a template's `Outputs`.
- **CDK, SAM and Serverless** are hook recipes on a custom image, as report
  [20 section 8](../reference/20-vendors.md#8-cloudformation) gives them.

**Kubernetes** runs kubectl, which the launcher downloads at the run's
`tool` (`name` `kubectl`): the newest release matching the stack's
`kubectlVersion`, the newest release when it is null, resolved at the
handover. `KUBECONFIG` is `/mnt/workspace/.kube/config` in every phase, and
a mounted file or a `before_init` hook writes it. Each command is a key of
the Kubernetes workflow ([0024](0024-expression-language.md#workflow-command-templates)).

| Phase | Commands |
|---|---|
| `INITIALIZING` | hooks; `createFromStdin` with a `SelfSubjectAccessReview`; the kustomization (below) |
| `PLANNING` | `apiResources`, the kinds to read and prune; `get` of the stack's objects; `apply` with `dryRunStrategy` `server` when `get` found any and `client` when it found none, and `outputFormat` `json` |
| `APPLYING` | `apply`; `rolloutStatus` for each Deployment, StatefulSet and DaemonSet applied, with `getPods` writing their pods to the log meanwhile |
| `DESTROYING` | `apiResources`; `delete` of every listed kind by the stack's label |

- **The kustomization.** Without a `kustomization.yaml` at the project
  root, the runner writes one that lists every `.yaml` and `.yml` file
  under it; with one, it adds a `labels` transformer. Either way the
  transformer sets `infraweave-stack: <stack ULID>` and
  `app.kubernetes.io/managed-by: infraweave` on each object's `metadata`
  and leaves selectors alone. A project root with no manifest fails
  planning, since pruning would remove everything.
- **Why a client dry run first.** A server dry run into a namespace that
  does not exist yet, or that the same apply creates, fails; a client dry
  run does not. A custom resource definition and a resource of its kind
  fail in one apply in every mode, so a stack applies definitions in an
  earlier run or another stack.
- **Pruning** passes `--prune` with the stack's label and one allowlist
  entry per kind `apiResources` lists, through the template's
  `pruneAllowList`; `pruneFlag` is `--prune-allowlist`, or
  `--prune-whitelist` when the resolved kubectl is below 1.26, which has
  only that flag.
- **The plan document** is `{"version": 1, "resources": [...]}`, one entry
  per object, `{address, kind, namespace, name, before, after}`. `before`
  is the object `get` read and `after` the object of the dry run's `List`;
  the dry run writes each object it would prune as a further JSON value
  after the `List`, which is an entry with `after` null. The address is
  `[<group>/]<kind>/[<namespace>/]<name>`, such as
  `apps/Deployment/example/example-app`.
- **Changes.** `before` alone is `DELETE`, `after` alone `ADD`, and both
  are `CHANGE` when they differ with `metadata.uid`, `creationTimestamp`,
  `resourceVersion`, `generation`, `managedFields`, the
  `kubectl.kubernetes.io/last-applied-configuration` and
  `deployment.kubernetes.io/revision` annotations, and `status` left out,
  else `NOOP`. `resources` counts the objects `get` read. Kubernetes has no
  outputs.
- **Rollouts** wait `KUBECTL_ROLLOUT_TIMEOUT` seconds, 600 by default.
- **No `kubectl diff`.** The runner never runs it: its output prints a
  Secret's last-applied annotation, and a pruned Secret whole, into the
  log.
- **Helm** is a hook recipe: a `before_plan` hook renders `helm template`
  into a file the committed `kustomization.yaml` lists (C298).

**Ansible** runs on the `infraweave-runner-ansible` image's ansible-core,
with infraweave's aggregate callback `infraweave`; the specification names
no tool. Before every phase the runner exports `ANSIBLE_VERBOSITY=1` and
`ANSIBLE_SSH_CONTROL_PATH_DIR=/tmp/.ansible/cp` unless the stack sets
them, and adds `infraweave` to the stack's `ANSIBLE_CALLBACKS_ENABLED`.

| Phase | Commands |
|---|---|
| `INITIALIZING` | hooks; `ansible-galaxy install -r <file>` for the first `requirements.yml` or `requirements.yaml` at the project root, in `roles/` or in `collections/`, or the file `INFRAWEAVE_ANSIBLE_REQUIREMENTS_FILE` names |
| `PLANNING` | `ansible-playbook <playbook> --check`, skipped when `INFRAWEAVE_SKIP_PLANNING` is `true` |
| `APPLYING` | `ansible-playbook <playbook>` |

- **No `--diff` and no `-i`.** A diff prints a template's rendered secrets
  into the log, and the inventory comes from the project's `ansible.cfg`,
  `ANSIBLE_INVENTORY` or the extra arguments, as in Spacelift's runs. Any
  exit code other than 0 fails the phase: 2 for a failed task, 4 for an
  unreachable host.
- **The callback** writes JSON lines to the file `INFRAWEAVE_ANSIBLE_RECORD`
  names: one record per task result, `{host, playbook, role, task, action,
  status, checkMode}`, with `status` one of `ok`, `changed`, `failed`,
  `skipped`, `unreachable`, `rescued` and `ignored`, and last
  `{"stats": {processed, ok, changed, dark, failures, skipped, rescued,
  ignored}}`, ansible-core's play recap. It writes no module result and no
  diff, since results carry file checksums, command output and rendered
  content, and it keeps a `no_log` task's names and status alone. It needs
  neither `ansible.posix` nor ansible-runner.
- **The plan document** is `{"playbook", "stats", "tasks"}` from those
  records.
- **Changes** are one per task result that is `ok` or `changed`, with its
  host, playbook, role, task and action, and no address. The delta's
  `change` counts `changed` results, and `skipped` ones too unless
  `INFRAWEAVE_IGNORE_SKIPPED_TASK_CHANGES` is `true`
  ([20 section 10](../reference/20-vendors.md#10-ansible)). Ansible has no
  outputs.
- **The configuration management view** reads its hosts, tasks and
  executions from each run's records.

### Logs and artifacts

- **Masking before anything leaves the container.** The runner applies
  [0013](0013-secrets-at-rest.md)'s masking rules line by line, as Spacelift does
  ([13 section 4.4](../reference/13-configuration.md#44-log-masking)): the values
  of every secret configuration element and of every variable in the launcher's
  mask list, words announced with `::add-mask`, and well-known patterns when the
  stack enables them, each replaced by five asterisks. Values shorter than 4 bytes
  are not masked, which is 0013's rule, not Spacelift's. Log chunks are therefore
  stored as masked plaintext.
- **Uploads.** `upload-urls` takes up to 64 declarations of `{kind, name,
  size, sha256}` for the run's current state version and answers a pre-signed
  `PUT` for each, under `<account>/<run>/<state>-<stateVersion>/<name>` in the
  kind's store. A name is 1 to 128 letters, digits, `.`, `_` and `-`, starting
  with a letter or digit, so no upload leaves its run's prefix.

  | Kind | Store | Largest object |
  |---|---|---|
  | `log-chunk` | `run-logs` | 1 MiB |
  | `artifact` | `run-artifacts`, sealed | 512 MiB |
  | `workspace` | `workspaces` | 2 GiB |
  | `observability` | `run-observability` | 64 MiB |

  Each URL holds a `pending` row of `blob_objects` for the declared size and
  SHA-256 ([0005](0005-persistence-postgresql.md)). A declaration repeated
  with the same size and SHA-256 signs the same object again, so a runner
  retries one safely; one that differs, or names an object already recorded,
  answers `409` `already-exists`.
- **Chunks.** The runner writes masked lines to stdout, which the launcher reads,
  and to chunks of at most 1 MiB or two seconds. A chunk's name is its
  sequence number in decimal, and `upload-urls` refuses any other name for
  a `log-chunk`. The runner commits a state's last chunk before it reports
  the state's end. The session's output reaches the runner through a pipe,
  so before that commit the runner writes a barrier line of its own to the
  pipe and waits to read it back, which proves every earlier line read. The
  `after_run` hooks run once the run has left its last worker state, so
  their output goes to stdout alone. Each chunk is uploaded by pre-signed
  `PUT`, then `log-chunks` commits it and indexes it in
  `run_log_chunks(run, state_version, seq, state, object key, bytes, first_at, last_at, lost)`;
  a chunk already indexed is skipped, so a repeated `seq` is not duplicated.
  Readers tail the index; logs therefore exist per state and state version.
- **A signature the store refuses.** Every transfer to or from a
  pre-signed URL, the runner's and the launcher's alike, takes a `400` or
  `403` from the store as a refusal of the URL's signature. That is how a
  store answers a signature that expired while the transfer waited, or one
  dated ahead of its clock, as a store whose clock differs from the
  server's sees it. The transfer asks the worker API for a URL signed
  afresh and tries again within its budget, as it does when the worker API
  or the store cannot take it for now:
  - a log chunk, from a new declaration, within the log's budget below;
  - a plan's artifacts, its saved workspace, the outputs and a CPU
    profile, from a new declaration, within the ten minutes a save takes.
    A save still refused fails the phase with the store's answer, but a
    profile's, which is noted in the log;
  - a local preview's archive and a saved workspace the launcher fetches,
    from the specification or the saved workspace's location asked for
    again, after a backoff that doubles from 1 to 30 seconds, for ten
    minutes. One still refused fails the run's preparation with the store's
    answer.

  A URL the API answers a client, such as a log's download, is the
  client's to ask for again.
- **A chunk that cannot be stored.** A chunk the worker API or the store
  cannot take for now is sent again, as a report is, and so is one whose
  signature the store refuses, as above. The ten-minute budget is
  the log's, not each chunk's. It runs from the first failure, a chunk
  taken renews it, and once a chunk has spent it each later chunk is sent
  once until one is taken, so an outage holds the run's output for ten
  minutes in all rather than ten minutes a chunk. A chunk that cannot be
  taken is committed as lost, `{seq, firstAt, lastAt, lost: true}` with no
  upload, and the runner warns. The state's report names `logChunks`,
  which `run_state_transitions.log_chunks` keeps, so a chunk whose lost
  commit failed too, even the state's last, is still known to be missing.
- **Reading.** `runLogs` answers a page of whole chunks of one state and
  state version, in sequence order, at most 1 MiB and 64 chunks. Each
  line's timestamp is its chunk's `first_at`, and `sanitize` strips ANSI
  escape sequences only (DV-117). While the run is in the state a page ends
  before a chunk not yet indexed. Once it has left, the state's chunks run
  to its report's `logChunks`, or one past the highest sequence number
  indexed when that is more or no report came. Each chunk that holds no
  lines, lost or never committed, reads as one line,
  `infraweave: part of this log could not be stored.`, stamped with a lost
  chunk's `first_at`, otherwise with the last line before it or when the
  run entered the state. A lost chunk reads so while the run is in the
  state too. The server writes the line, so it carries no value of the
  run's ([0013](0013-secrets-at-rest.md)), and a page's shape is unchanged.
  A chunk whose object retention removed is left out and the page is
  `expired`.
- **Compaction.** The transaction of a transition out of a worker state
  enqueues `run.logs.compact` for that state and state version, keyed so
  it runs once. The job writes the state's chunks in sequence order to one
  object, `<account>/<run>/<state>-<stateVersion>.log` in `run-logs`, with
  the reading's line in place of each chunk that holds none, and running
  it again changes nothing. `runLogsDownloadURL` answers that object's
  pre-signed `GET`, with `ongoing` while the run is in the state or the
  compaction is pending, `expired` when no chunk was written or retention
  removed them, and `deleted` after `runLogsDelete`. A state whose every
  chunk was lost has no chunk object to tell a pending compaction from an
  expired one, so it is pending while the run entered the state within
  the store's retention.
- **Artifacts.** Plan JSON, changes, outputs and anything else the server or a
  policy reads are sealed by the runner with the run's artifact key, in 0013's
  streaming format (64 KiB AES-256-GCM segments, the same code in the same
  binary), uploaded to `run-artifacts` by pre-signed `PUT` and recorded
  through `POST .../artifacts`. The server opens them with
  `runs.artifact_key_sealed`. The binary plan file travels only inside the
  encrypted workspace, which the server cannot read.
- **Records.** `POST .../artifacts` commits run artifacts, the workspace and
  observability artifacts and records each in
  `run_artifacts(run, state_version, kind, name, object key, size, sha256)`;
  one already recorded is skipped. An upload missing from the store or
  unlike its declaration answers `422` `validation-failed`, pointing at the
  entry, and stays `pending` until `blob.expire` removes it.
- **The workspace.** `GET .../workspace` answers `307` to a pre-signed `GET`
  of the run's most recently recorded workspace, which the launcher
  downloads for `PREPARING_APPLY` and `PREPARING_REPLAN`, or `404`
  `not-found` when the run recorded none.
- **Outputs.** `APPLYING` runs `apply`, then `getOutputs`, whose stdout is
  the tool's `output -json`. The runner refuses a document that is not
  one, runs the `after_apply` hooks, then seals it as `outputs.json` and
  records it before the phase's report. A sensitive output keeps its value
  there only when both the stack's `enableSensitiveOutputUpload` and the
  launcher's `launcher.sensitive_output_upload` allow it, as Spacelift's
  stack setting and `SPACELIFT_SENSITIVE_OUTPUT_UPLOAD_ENABLED` do;
  otherwise the runner writes it with its name, type and `sensitive` and
  no value, so no object holds the value, sealed or not. Both are off by
  default. The server opens the document in that report's transaction,
  before the run moves on, and drops any sensitive value the stack's
  setting does not allow, whether a runner kept it against the
  specification or the setting was turned off after the run's handover.
  Each output's description comes from the configuration in the
  `plan.json` the run recorded last while planning, since an apply in a
  later container may not hold the plan's JSON; there are none once
  retention has removed it. The server then replaces the stack's
  `stack_outputs`, each value sealed in `value_sealed` to its row
  ([0013](0013-secrets-at-rest.md)), beside `sensitive` and `description`.
  A sensitive output with no value has a `NULL` `value_sealed`, which a
  `CHECK` allows for sensitive rows alone. `NULL` rather than a sealed
  JSON `null`: `null` is a value an output can have, and a row with no
  ciphertext has nothing a later reader, such as an output reference,
  could open. A tracked run's succeeded `APPLYING` report whose
  `outputs.json` is not recorded, or cannot be read, is refused with `400`
  `invalid-request`. A failed apply leaves the previous outputs. A
  `DESTROY` run's `FINISHED` transition deletes the stack, and its outputs
  with it. The stack's read answers them through `stacksapi.StackStatus`,
  which `runs` implements. A value is the JSON of the tool's output, as a
  string, and a sensitive output's value is never opened or answered.
- **CPU profiles.** On a stack whose OpenTofu settings enable CPU
  profiling, with OpenTofu 1.11 or later, the runner points
  `TOFU_CPU_PROFILE` at a fresh file for each tool command, merges a
  state's files into one pprof whose samples carry the label
  `infraweave.command`, the workflow command's key or `task` for a task's
  command, and uploads it as the `observability` artifact
  `cpu-<STATE>.pprof` before the state's report. A profile that fails is
  logged and never fails the run. `runCpuProfileDownloadURL` answers the
  pre-signed `GET` of the profile of the state's latest execution as
  `runLogsDownloadURL` answers a log, with `ongoing`, `expired` and
  `deleted`.
- **Deleting logs.** `runLogsDelete`, decided as `SPACE_ADMIN` on the
  account's root space, deletes a finished run's chunks, compacted logs
  and CPU profiles with their records and sets `runs.logs_deleted_at`,
  after which both log reads answer `deleted`. `runLogRetentionDays`
  answers `objectstore.retention.run-logs` in whole days.
- **Deletion.** An account's purge deletes its runs' chunk and artifact
  records with their objects, and their compacted logs. A run deleted with
  its stack leaves its objects to their stores' retention, which bounds
  them.

### Liveness: offline workers and lost runs

Two settings separate "this worker should get no new work" from "this run
has failed":

| Setting | Default | After a worker's last poll | Effect |
|---|---|---|---|
| `worker.offline_after` | 3 minutes, three default polls | the worker is an offline worker | It is listed as `OFFLINE` and claims no run. A run it holds keeps its state and its stack lock |
| `run.lost_after` | 60 minutes | the worker's run is a lost run | The run fails with note `worker lost`, and the worker row is deleted |

- **The task.** The periodic task `worker.reap` (every 30 seconds) marks
  workers unseen for `worker.offline_after` as offline. For workers unseen
  for `run.lost_after`, it deletes the worker row and, in the same
  transaction, fails each run the worker held that is in `PREPARING`,
  `PREPARING_APPLY`, `PREPARING_REPLAN`, `INITIALIZING`, `PLANNING`,
  `APPLYING`, `PERFORMING` or `DESTROYING`. An idle offline worker's row is
  deleted at the same point. Then it fails each run a claim moved to a
  preparing state that no `started` report moved on within its pool's
  preparing timeout, with the note `preparing timeout`. Each run fails
  through its state's liveness edge, which every state on a worker has. A
  pass deletes at most 100 lost workers and fails at most 100 overdue
  preparations, and the next goes on; its reads skip rows another
  transaction holds, so no run fails twice.
- **A worker that returns in time.** A poll from an offline worker, or a
  registration that resumes it (above), clears the offline mark, and its run
  continues where it was: the runner's reports, uploads and the state backend
  keep working, because the run token outlives the run by an hour and is
  checked against the run's state, not the worker's.
- **A worker that returns late.** Its run has already failed. Its next poll
  answers `401`, the launcher registers again, and, answered with a new
  worker, removes the old one's container.
- `run.lost_after` must be greater than `worker.offline_after`, and
  `worker.offline_after` greater than `worker.poll_timeout_max`, which would
  otherwise mark a worker offline while its poll is held; any other pair
  fails the start. All are server settings, so one installation applies
  one tolerance to every pool.
- A launcher whose container ends (exit, crash, out of memory, timeout,
  kill) or disappears reports through its next poll (`busy: false` with the
  run ID and `exit`, its code and reason, as in
  [The launcher and the runner](#the-launcher-and-the-runner)), and the server
  fails the run if no runner report ended it, with a note of how:
  `container exited with code N`, `container killed`, `container out of
  memory` or `container missing`.

### Worker status, drain, cycle and reset

| Action | Behaviour |
|---|---|
| Status | `OFFLINE` when offline, else `DRAINED` when drained, else `BUSY` when busy, else `IDLE`; `busy` and `drained` stay separate fields ([10 section 3.1](../reference/10-workers-and-execution.md#31-worker)). `OFFLINE` is a fourth `WorkerStatus` value that Spacelift's schema does not have, recorded as a divergence (decision 0016) |
| Drain, undrain | `workerDrainSet` sets the flag and returns the worker with `busy`. A drained worker gets no assignment and finishes its current run |
| Cycle | `workerPoolCycle` sends `cycle` to every worker of the pool on its next poll. An idle launcher exits at once; a busy one exits after its run ends |
| Reset | A new certificate serial; old workers are refused at their next call and exit |
| `availableAt` | Set when a worker becomes idle, for autoscalers ([10 section 3.3](../reference/10-workers-and-execution.md#33-drain-cycle-and-recycle)) |

### Pools

- **Private pools** are `worker_pools` rows in a space, with the preparing
  timeout (default 15 minutes) and drift detection run limit
  ([28 section 12.5](../reference/28-data-model.md#125-workers)).
- **The public worker pool** is one pool the installation's operator runs for
  every organization's accounts, on the same protocol. Its launchers register with an
  installation-level pool token that the operator issues through the
  operator API ([0021](0021-capability-scope-and-supersession.md)). The restrictions Spacelift places on its public pool
  hold here too
  ([10 section 1.1](../reference/10-workers-and-execution.md#11-public-and-private-pools-compared)):
  - no run priorities: `runPrioritySet` on a run in the public pool answers
    `422`, problem `validation-failed`;
  - runner images only from the configured registry allowlist
    (`worker.public_pool.allowed_registries`, default Spacelift's list),
    because the shared Docker daemon would otherwise cache one account's
    private image for every other;
  - no scheduled or drift detection runs;
  - an account's runs are skipped by the claim while its organization has
    its `public-worker-slots` runs in flight, or the account has its account
    limit's.

  The number of slots is a limit of the feature catalogue, pooled across an
  organization's accounts, so a tier or a grant sets it per organization and
  an organization admin may give an account a lower account limit
  ([0021](0021-capability-scope-and-supersession.md#limits));
  `PublicWorkerPool.parallelism` reports the account's effective share as a
  required nullable int64: the minimum bounded account/organization ceiling,
  null when unbounded (also billing off), zero when no run may start. Physical
  installation capacity is separate; account ceilings are not reservations. An installation without
  public workers simply has none registered.

Private hosting on EKS/AKS/GKE and concurrent pools/clusters uses the existing
catalogue: worker-pools Free, private-worker-pools Business, private-workers
0/0/3 and public slots 1/2/2. Limits aggregate across the organization, with
lower account ceilings; an explicit lab grant may permit four. A run retains
its stored pool through apply/replan; cloud has no separate entitlement.
The [public REST/operator mappings](../../implementation/worker-pool-contracts.md#rest-and-operator-mappings)
add the missing default-image/egress reads and explicit literal public queue/user
paths. Installation public credentials never fabricate an account or consume
private-worker quota. Protocol bind/release enforces exclusive controller
hosting even for an Existing pool. These are intended additions, not current
runtime or observed reference compatibility.

The [installation public pool implementation](../../implementation/public-worker-pool.md)
describes operator issuance, image-policy capability, tenant-owned assignment
and credential rotation. Public replacement-certificate registration uses a fresh
worker identity so old session tokens cannot revive; same-certificate restarts
retain their identity. Controller binding and the remaining public admission/read
surfaces retain their separate delivery owners.

### Pool samples

- **The job.** `worker.sample`, a periodic queue task of `scheduler`
  ([0011](0011-persistent-work-queue.md)), runs every minute and writes one
  row per pool to `worker_pool_samples(account_id, worker_pool_id,
  sampled_at, busy_workers, registered_workers, queued_runs)`, primary key
  `(account_id, worker_pool_id, sampled_at)`; `sampled_at` is the minute,
  so a second run in the same minute is a no-op, and two replicas writing
  the same minute at once write one row per pool between them. The busy
  and registered counts are the pool's `workers` rows, an offline worker
  included until the reap deletes it; the queued count is the pool's runs
  a claim may take (`READY`, `CONFIRMED` and `REPLAN_REQUESTED`), which
  the runs domain counts through `workersapi.Scheduler`, the back edge
  claims use. The public pool has one row per account that has runs on
  it, counting that account's busy workers and queued runs, and its
  registered workers; those rows begin with the public pool itself. A
  deleted pool's samples stay until they expire, so a usage window that
  covers its last days still counts them, and a deleted account's go with
  its purge.
- **Retention.** `worker.samples.retention`, default 35 days: a month of
  daily assessments plus a margin. The same task deletes older rows, at
  most 100,000 a pass, so a shortened retention's backlog never holds up
  the next minute's sample.
- **Readers.** Other domains read samples only through the workers
  domain's `api/` package, `SamplesBetween(account, pool, from, to)`,
  oldest first over `[from, to)`; the account scopes the read as every
  other read of a pool is scoped; the worker
  usage assessment of [0015](0015-events-notifications-webhooks-audit.md)
  and the usage read models are its readers. No other domain queries the
  table.

### The launcher and the runner

- **One binary, two subcommands.** `infraweave launcher` registers,
  polls, prepares and supervises; `infraweave runner` (hidden) executes the
  phases inside the run container.
  0020 packages them and 0004 places the code.
- **The workspace.** The checkout, or a local preview's archive, is at
  `/mnt/workspace/source`. Hooks and the tool run in the project root
  under it, or in `source` when none is named. Mounted files are at
  `/mnt/workspace/<path>`; one under `source/` replaces the checkout's
  file. Infraweave's own files live at the workspace's top level, beside
  `source/`. The whole workspace, checkout and mounted files alike, is
  what a plan saves for its confirmation. `TF_VAR_infraweave_workspace_root`
  stays `/mnt/workspace`.
- **Preparation**, inside the preparing timeout: fetch the specification and
  decrypt its secrets; evaluate the worker-side initialization policy; fetch the
  source into the workspace's `source/` or restore the workspace; download
  and verify tool binaries; pull the runner image; start the container.
  A Git source with sparse paths checks out only those directories; a local
  preview's archive is extracted whole. A failure reports T11, T36 or T44.
- **The container.** Docker through `github.com/moby/moby/client`. The launcher
  bind-mounts its own executable read-only at `/opt/infraweave/bin/infraweave`
  and runs it as the container's root process with the argument `runner`,
  replacing the image's `ENTRYPOINT` and `CMD`. The runner is statically linked,
  so it runs on any image's libc. `launcher.docker.runner_binary` names another
  build to mount instead: a launcher whose own build cannot run in a Linux
  container, such as one on macOS with Docker Desktop, names a statically
  linked Linux build of the same version. The container runs as UID and GID
  1983, with the workspace at `/mnt/workspace`, the tool binaries mounted
  read-only, and the launcher's network, runtime and extra mount settings.
  Each container is labelled `io.infraweave.worker` and `io.infraweave.run`
  with the worker's and the run's IDs.
- **The handoff.** The launcher writes the run's endpoint, run token,
  assigned state and state version, the specification without its sealed
  part, the opened secrets, the pool's public key from its certificate,
  the names `launcher.mask_envs` lists and `launcher.state_lock_timeout` as
  one JSON document to the runner's standard input, then closes it. The
  runner takes no other configuration. Docker keeps a container's
  environment and command in its configuration on the host's disk, so the
  opened secrets never go there. The runner reads at most 64 MiB, rejects unknown fields, and reports
  `started` for the state the assigned one leads to (`INITIALIZING`,
  `PLANNING` or `APPLYING`), which ends the handover.
- **Resuming.** `worker.json` in `launcher.cache_dir` keeps the pool, the
  worker, the run it executes and the exit of a container whose end the
  server has not acknowledged. A restarted launcher registers naming that
  worker, finds the run's container by its labels and follows what it writes
  from then on, or
  reports the stored exit, or reports the run `missing` when its container is
  gone. An answer naming another worker means the server lost the old one
  and failed its run, so the launcher removes that container. A launcher
  stopped while it executes a run keeps its worker and its container for the
  restart; an idle one deregisters.
- **A container that ends** cuts the launcher's held poll short. Its next
  poll carries `busy: false`, the run and `exit` with the code and the reason
  (`exited`, `killed`, `out-of-memory`, or `missing` with code `-1`), and the
  launcher repeats it until the server answers.
- **The admin listener** (`launcher.admin_listen`, `:9091`, one above the
  server's) answers decision 0017's probes for the launcher.
- **Kubernetes.** `infraweave worker-controller` is an agent-only launcher of
  strict namespaced WorkerPool/Worker resources, deterministic logical slots and
  one Pod per assignment version. The [worker pool contract](../../implementation/worker-pool-contracts.md)
  specifies its Managed/Existing/Public modes, durable hosting/management owner,
  Lease/replica fencing, finalizers, credential references, Pod-local streamed
  workspace/verified-tool preparation, same-version static bootstrap, private
  stdin handoff, signals and recovery. Docker's local bind mounts remain its
  own adapter contract; Kubernetes uses no controller-host paths or privileged
  daemon. Shared server-issued preparingDeadline covers scheduling/preparation;
  run timeout and an absolute watchdog survive controller restarts. No runtime
  replays an ambiguous handoff or silently selects another pool/cloud.

### Runner images

- **Contract.** An image works if it has a user with UID 1983, a `ps` command, and
  the architecture of the worker, and does not rely on `ENTRYPOINT` or `CMD`
  ([10 section 6.3](../reference/10-workers-and-execution.md#63-requirements-on-a-custom-image)).
  That is Spacelift's contract, so Spacelift's public runner images and customers'
  derived images run unchanged.
- **Selection** follows Spacelift's precedence: runtime configuration, stack, the
  account default, `launcher.default_runner_image` when set, then the vendor's
  image
  ([10 section 6.1](../reference/10-workers-and-execution.md#61-defaults-and-selection)).
  The vendor's image is: `-opentofu` for
  OpenTofu, `-pulumi` for Pulumi, `launcher.default_ansible_runner_image`
  (by default `-ansible`) for Ansible, and `-terraform` for every other
  vendor. `launcher.default_runner_image`, unset by default, replaces the
  default of every vendor but Ansible, as Spacelift's launcher variables
  do. For the shipped vendors, OpenTofu uses
  `ghcr.io/weaveplatform/infraweave-runner-opentofu`; every other vendor,
  Terragrunt included and whatever tool it drives, uses
  `ghcr.io/weaveplatform/infraweave-runner-terraform`. Pulumi and Ansible's
  environments are published alongside them; P20/P20b retain those vendors'
  backend work and Ansible's separate default setting.
- **Defaults.** infraweave publishes its own multi-arch images
  (`ghcr.io/weaveplatform/infraweave-runner-terraform`, `-opentofu`,
  `-pulumi`, `-ansible`) that meet the contract, defined under
  `deploy/images/` on one Alpine base. They hold the tools a phase's shell
  needs (`bash`, `ca-certificates`, `curl`, `git`, `jq`, `openssh-client`,
  `ps` and `tzdata`). Terraform and OpenTofu images also include their pinned
  official stable CLI at `/bin/terraform` or `/bin/tofu`. Each architecture's
  release archive is checked against the SHA-256 pinned in `versions.json` at
  build time. A network-disabled plan/apply/destroy contract proves that the
  image works without launcher downloads or tool mounts. This follows the
  maintainer's 2026-11-21 image decision; retained revision 1/2 images are the
  earlier shell-only environments.
  Built-in references are immutable digest pins shared by the application and
  acceptance harness. Runner environment releases have their own cadence
  (decision 0020); version tags identify the installed CLI. With
  `skipVendorBinaryDownload`, the image's CLI runs. Otherwise the run's requested
  tool version remains authoritative and its read-only mount overrides the
  installed executable. This preserves explicit image overrides and saved-run
  specifications. Experimental fixture producers are never baked into the
  released images.
  - **`-pulumi`** adds the Pulumi CLI and its language hosts from Pulumi's
    release archive, checked against the SHA-256 its Dockerfile pins, with
    Node.js and npm, and Python and pip, from Alpine; Go and .NET programs
    build on an image derived from it. The CLI and its language hosts are
    statically linked
    ([33 section 2.1](../reference/33-vendor-tool-documents.md#21-commands-per-phase));
    that they and a provider plugin run on musl is C299.
  - **`-ansible`** adds ansible-core from PyPI and infraweave's callback
    plugin, enabled by the runner (see "Vendors' runs"), and neither
    `ansible.posix` nor ansible-runner.
  - Kubernetes and CloudFormation stacks run on `-terraform`: kubectl is
    mounted, and CloudFormation's driver is the runner itself.
- **Tool binaries.** The launcher downloads the requested version of Terraform,
  OpenTofu, Terragrunt or kubectl, verifies it and mounts it read-only at
  `/bin/<tool>`, such as `/bin/terraform` or `/bin/tofu`; a Terragrunt
  stack's run mounts both Terragrunt, at `/bin/terragrunt`, and the tool it
  drives
  ([20 section 3.5](../reference/20-vendors.md#35-version-management)):

  | Tool | Verification |
  |---|---|
  | Terraform | `SHA256SUMS` signed by HashiCorp's PGP key, which is embedded in the binary, then the archive checksum |
  | OpenTofu | `SHA256SUMS` signed by the OpenTofu signing key, embedded, then the archive checksum |
  | Terragrunt | `SHA256SUMS` signed by Gruntwork's Terragrunt signing key, embedded rather than taken from the release, then the binary's checksum |
  | kubectl | The SHA-256 that `dl.k8s.io` publishes beside the binary, fetched over TLS |

  Binaries are cached at `<cache_dir>/binaries/<tool>/<version>/<os>_<arch>` and
  reused. Each is written beside its path and renamed into place, so
  launchers that share a cache never see a partial one. Archives are
  bounded at 256 MiB, and only the tool's binary is extracted; Terragrunt
  publishes each build as a bare binary, `terragrunt_<os>_<arch>`, bounded
  at 512 MiB and listed in one `SHA256SUMS` per release.
  `launcher.tool_mirror` points downloads at a mirror for air-gapped
  installations, which serves each release's files under
  `<tool_mirror>/<tool>/<version>/` with the vendor's file names, the layout
  of `releases.hashicorp.com`. `skipVendorBinaryDownload` skips the download.
  A refused release fails the run in preparation, and its note names the
  release and the step that refused it. kubectl's releases come from the
  `kubernetes/kubernetes` GitHub releases, alpha, beta and release
  candidates left out, through the reader Terragrunt's index uses, and
  `kubectlVersions` lists them newest first; `launcher.tool_mirror` serves
  kubectl under `kubectl/<version>/`.

### Timeouts and timers

| Limit | Default | Enforced by | Outcome |
|---|---|---|---|
| Run timeout | 70 minutes (`launcher.run_timeout`) | launcher, from container start | `FAILED`, note `run timeout` |
| No-log timeout | 7 minutes (`launcher.log_timeout`) | launcher, from the last line on the container's output | `FAILED`, note `no log output` |
| Preparing timeout | 15 minutes, per pool | `worker.reap`: no `started` report in time | `FAILED` (T11, T36, T44), note `preparing timeout` |
| Offline worker | 3 minutes (`worker.offline_after`) | `worker.reap` | no new work; the run continues |
| Lost run | 60 minutes (`run.lost_after`) | `worker.reap` | `FAILED`, note `worker lost` |
| Not picked up: proposed and test runs | 2 days (`run.timers.pickup_proposed`) | `run.timers` | `CANCELED` |
| Not picked up: tracked runs, tasks and destroy runs | 7 days (`run.timers.pickup_blocking`) | `run.timers` | `CANCELED` |
| `UNCONFIRMED` unchanged | 7 days (`run.timers.unconfirmed`) | `run.timers` | `DISCARDED` |
| `CONFIRMED` or `REPLAN_REQUESTED` unchanged | 7 days (`run.timers.confirmed`) | `run.timers` | `FAILED` |
| Not terminal after creation | 30 days (`run.timers.max_age`) | `run.timers` | `CANCELED`, `DISCARDED` or `FAILED` by state |

The timer rules and their notification rules are report 08's
([08 timers](../reference/08-run-lifecycle.md#timers)), and their defaults
are Spacelift's. `run.timers` runs every minute on the scheduler queue and
reads `runs (state, state_changed_at, created_at)` in batches of 100, ten
transactions a pass at most, oldest state change first, skipping rows
another transaction locks, so two replicas never end a run twice. It
reads only the states a run waits in with no worker: `QUEUED`, `READY`,
`PENDING_REVIEW`, `UNCONFIRMED`, `CONFIRMED` and `REPLAN_REQUESTED`. A run
on a worker is liveness's, offline worker or not, so the 30-day rule's
"unless a worker is executing it" is every state on a worker. Each rule
ends a run through the timers' edge out of its state (T4, T9, T25, T27,
T31, T34). A rule fires once its time has passed in full, and a run that
two rules time out takes the 30-day rule. Each timer's transition carries
a note naming its rule, such as `not picked up within 2 days`, and its
`run_updated` event names the rule in `timer`, by its key under
`run.timers`, so notifications apply report 08's rules: the shorter rules
notify, and the 30-day rule (`max_age`) never does
([0015](0015-events-notifications-webhooks-audit.md)). Each timer is at
least a minute.

A launcher that kills a container on either of its timeouts reports the
container's exit with the reason `run-timeout` or `log-timeout`, beside
`exited`, `killed`, `out-of-memory` and `missing`, and liveness fails the
run through the edge out of its state with the note `run timeout` or
`no log output`. A container killed on a `kill` signal fails its run with
the note `killed`.

### Stop and kill

- `runStop` is valid in `INITIALIZING` for every run type (T16) and in
  `PLANNING` for proposed and tracked runs (T19)
  ([08 section 6](../reference/08-run-lifecycle.md#6-run-actions)), and
  needs `RUN_STOP_BLOCKING` as well for the run that holds its stack's lock.
  Its transaction records `runs.stop_requested_at`, the caller's login and
  the request's note. The launcher sends `SIGINT` to the runner, which
  passes it to the running command's process group and reports `stopped`
  when the command ends. `APPLYING`, `DESTROYING` and `PERFORMING` ignore
  it.
- `runKill` is valid once a stop was requested, while the run is on a
  worker, and needs `RUN_KILL_BLOCKING` as well for the run that holds its
  stack's lock. Its transaction records `runs.kill_requested_at`, the
  login and the note. The launcher kills the container with `SIGKILL`, and
  liveness fails the run through the edge out of its state (T15, T18, T38,
  T41, T43, T45) with the note `killed`.
- **The transition records the request.** `STOPPED`, and `FAILED` after a
  kill, record the caller's login and the request's note; a request with
  no note leaves the runner's details for a stop and `killed` for a kill.
- **The `state` argument** names the state the caller saw. A run that has
  left it answers `409 invalid-state`, as does a run whose state takes
  neither action, and each refusal's detail names the run's state (C36).
  A repeated stop or kill of a run that still takes it is answered as it
  is, with no second request.
- **`allowedActions`** lists stop, or its blocking variant, until a stop is
  requested, and then kill while a worker holds the run, until a kill is
  requested.
- **Signals reach the worker through its poll.** The request notifies the
  run's pool with the pool's ID alone, which wakes every held poll of the
  pool, since the poll to wake is the busy worker's. A busy worker's poll
  asks `workersapi.Signals`, which `runs` implements, for the pending
  signal of the run the worker holds, in a transaction that locks the
  run's row and records the delivery, so each signal is answered once: a
  second `SIGINT` makes Terraform and OpenTofu exit at once,
  mid-operation. A kill goes in place of a stop not yet delivered, and a
  stop is delivered only while the run's state takes one.
- **The runner's session** leads a process group of its own, which every
  step's commands join, and traps `SIGINT` with a handler rather than
  ignoring it, since commands inherit an ignored signal. The runner gives
  `SIGINT` up from the process's shutdown handler and takes each one as a
  stop, once: it signals the session's group, runs no further step of the
  phase, and reports `stopped` whatever the interrupted step answered. A
  stop that arrives between phases ends the next phase before its first
  step when that phase is initialization or planning, and is dropped
  otherwise. The `after_run` hooks then run with
  `TF_VAR_infraweave_final_run_state=STOPPED`.

### Destroy on delete

- `stackDelete` with `destroyResources: true` answers the stack with
  `deleting: true` and creates a `DESTROY` run on its tracked commit through
  a `stacksapi` back edge, which `runs` implements, so `stacks` never
  imports `runsapi` ([0004](0004-repository-architecture.md#constraints)).
- The stack is deleted in the transaction of that run's `FINISHED`
  transition. A destroy run that ends in any other state clears `deleting`,
  and the stack stays with that run as its last.
- While `deleting`, the stack creates no other run, answering `409`
  `invalid-state`. `protectFromDeletion` refuses both forms of deletion.
- A stack that cannot run the destroy run refuses the delete, and nothing
  changes: one locked by another caller with `409` `resource-locked`, as
  the stack's other writes answer that lock, and one that is disabled or
  tracks no commit with `409` `invalid-state`.
- While `deleting`, a delete without `destroyResources` answers `409`
  `invalid-state`, since the destroy run is still to end. Another delete
  with it answers the stack as it is, and creates no second run.
- `deleting` is the runs domain's state of the stack, as its projected run
  state is, so setting and clearing it changes neither the stack's version
  nor its `ETag`.
- A CloudFormation stack's destroy run deletes the CloudFormation stack
  with `DeleteStack` (see "Vendors' runs"), where Spacelift's docs give a
  task to run before deleting the stack.
- An Ansible stack has nothing to destroy: `destroyResources: true` answers
  `422` `validation-failed` at `/destroyResources`, as Spacelift's console
  offers no destroy for one.

### Signed runs

The launcher evaluates the worker-side initialization policy before it starts the
container, with the embedded OPA of [0009](0009-policy-engine-opa.md)
([10 section 10](../reference/10-workers-and-execution.md#10-signed-runs-and-worker-side-policy)).
The complete version-1 input in the [worker pool contract](../../implementation/worker-pool-contracts.md#worker-initialization-and-signed-runs)
carries run metadata/commit/server JWT and immutable static environment. It is
infraweave's chosen schema; C68 remains unobserved. The current bare runner JWT
signs run subject, kind, audience and times, not pool/account/space claims.
Trusted assignment/envelope supplies pool binding; optional user metadata
signatures are verified independently. An invalid policy
stops the launcher at start. A `deny` fails the run in preparation, with the
reasons in the log of the preparing state. The `exec` built-in that Spacelift
offers this policy is available to it alone; 0009 lists built-ins.

### Configuration

- **Server:** `worker.poll_timeout_max` (120s), `worker.offline_after` (3m),
  `worker.samples.retention` (35 days, at least a minute), `run.lost_after` (60m),
  `run.preparing_timeout_default` (15m), `run.timers.pickup_proposed` (48h),
  `run.timers.pickup_blocking` (168h), `run.timers.unconfirmed` (168h),
  `run.timers.confirmed` (168h), `run.timers.max_age` (720h),
  `runs.runtime_config.unknown_keys` (`warn`, or `reject`),
  `worker.public_pool.allowed_registries`.
- **Launcher:** `launcher.token`, `launcher.private_key`, `launcher.poll_timeout`
  (60s), `launcher.run_timeout` (70m), `launcher.log_timeout` (7m),
  `launcher.cache_dir`, `launcher.metadata`, `launcher.admin_listen` (`:9091`),
  `launcher.state_lock_timeout` (5m),
  `launcher.max_requests_per_second` (10), `launcher.max_requests_burst` (100),
  `launcher.log.*`, `launcher.default_runner_image` (unset),
  `launcher.default_ansible_runner_image`, `launcher.initialization_policy`
  (optional local file), `launcher.initialization_policy_engine` (`REGO_V1`,
  also accepts `REGO_V0`), `launcher.initialization_timeout` (60s, positive
  and at most 60s),
  `launcher.mask_envs`, `launcher.whitelist_envs`,
  `launcher.sensitive_output_upload` (off), `launcher.tool_mirror`,
  `launcher.ca_file`, `launcher.docker.config_dir`, `.network`, `.runtime`, `.runner_binary`,
  `.extra_mounts`, `.ro_extra_mounts` and `.wo_extra_mounts`. Environment variables are
  `INFRAWEAVE_LAUNCHER_*`. Every Spacelift launcher variable of
  [10 section 5.1](../reference/10-workers-and-execution.md#51-launcher-environment-variables)
  has an equivalent here except the transport selectors and three this
  record leaves open: `SPACELIFT_RUN_LOGS_ON_STANDARD_OUTPUT_ENABLED`,
  `SPACELIFT_DEBUG` and
  `SPACELIFT_LAUNCHER_WORKSPACE_INIT_TIMEOUT`, whose deadline the pool's
  preparing timeout bounds.


### Policy artifact and transition ownership

Runs owns run_policy_receipts and run_flags in its migration stream; state,
history, flags, receipts, outbox and fenced queue completion commit together.
Every targeted replan reevaluates its newly sanitized JSON. Worker JSON is
transformed before sealing/upload under DEFAULT/SMART; NONE is the explicit
Terraform-family opt-out and still seals artifacts. Pool-sealed binary plans
and workspaces remain usable for apply and are not rewritten by JSON sanitization.
The 512 MiB artifact upload bound and 0009's 64 MiB policy input bound remain
separate. A sanitization failure never falls back to uploading raw plan JSON.
Server INITIALIZATION is P13b at READY before worker assignment (C52, T46); launcher initialization and
its exec capability are P14.

## Rationale

- **PD-06: HTTP long-poll only (option a).** Spacelift retires MQTT for workers on
  2027-03-30 and long-poll already covers every deployment path. A broker would
  add a port, a component and a second delivery path to test, and buy nothing:
  Spacelift's launchers cannot talk to infraweave anyway
  ([0001](0001-rest-first-spacelift-surface.md)), so no existing launcher needs
  MQTT. **Departure:** infraweave offers no MQTT transport, built-in broker or
  IoT Core bridge
  ([24 section 3.4](../reference/24-self-hosted-reference.md#34-mqtt-broker-and-worker-transport)).
  Report 26 section 7.8 keeps MQTT in scope provisionally
  ([26 section 7.8](../reference/26-scope-and-tiers.md#78-other-dated-supersessions));
  decision 0021 records the exclusion.
- **Runs are claimed, not queued.** A queue lease suits seconds of work. A run
  waits days for review and resumes in a new container, so the run row, with its
  states and history, is the only record, and the worker claims from it in the
  scheduler's order. This takes OTF's two-level shape (per-stack lock, pool-wide
  order) without its single in-memory allocator
  ([27 section 3.4](../reference/27-reference-materials.md#34-do-not-copy)).
- **Rejected: server push (gRPC streams, WebSockets, server-sent events).** Each
  needs connections that load balancers and proxies treat specially. A poll is
  plain HTTPS, served by chi, and fails over to any replica.
- **Rejected: Temporal for run orchestration.** Run states must be queryable API
  data; an engine's event history would need a projection and a second cluster
  ([27 section 10.1](../reference/27-reference-materials.md#101-temporal)).
- **The server decides, the worker reports.** Plan policies, approval and the
  stack lock stay on the server, so a modified worker cannot skip them; the worker
  only states facts about its process.
- **The launcher holds the key.** Opening secrets in the launcher keeps the pool
  private key out of both the run container and the server, and keeps workspaces
  unreadable by the server.
- **Departure: lost runs fail after an hour.** Spacelift's 30-day rule leaves a
  run alone while a worker executes it. With a poll every minute, infraweave
  knows within three minutes that a worker has stopped polling, and stops
  sending it work. Failing the run at that point would kill applies through
  a network blip or a launcher restart, and a half-applied run is worse than a
  late one; leaving it for 30 days keeps the stack locked for a month. An
  hour covers restarts and short outages, and `run.lost_after` lets an
  operator choose another tolerance.
- **Departure: P-256 pool keys only.** Spacelift's documented setup generates
  an RSA 4096 key ([10 section 2.3](../reference/10-workers-and-execution.md#23-credentials)).
  The pool envelope of 0013 is an ECDH construction, chosen so that it runs
  under Go's FIPS 140-3 mode, and one key per pool keeps registration and
  sealing on the same key.
- **Departure: an `OFFLINE` worker status.** Spacelift's worker list has no
  such state; an operator needs to see a worker that holds a run but has
  stopped polling, since that run is waiting on it.
- **Departure: infraweave's own default runner images**, so an installation does
  not depend on another vendor's registry; the shared contract keeps Spacelift's
  images usable.
- **Departure: launcher settings are named `INFRAWEAVE_LAUNCHER_*`.** The
  launcher is infraweave's client, not Spacelift's.
- **Departure: `MANUALLY_PROVISIONED` still downloads Terragrunt.**
  Spacelift downloads nothing for such a stack and relies on the image's
  `terragrunt`; infraweave's images carry no Terragrunt, so the launcher
  downloads and verifies it, and only the tool comes from the image.
- **Departure: Terragrunt and its tool resolve on their own.** Spacelift
  picks a compatible pair from Gruntwork's compatibility table, which is a
  documentation page rather than data; current Terragrunt drives every
  current OpenTofu and Terraform release, so each constraint resolves
  against its own index.
- **Departure: a default image for Pulumi.** Spacelift's Pulumi stacks
  need an image named per language; `-pulumi` runs a Pulumi YAML, Node.js
  or Python program with none named, and a stack that names one is
  unaffected.
- **Departure: CloudFormation stacks destroy on delete.** Spacelift
  documents no destroy for a CloudFormation stack; every other vendor's
  destroy run deletes what it created, and a destroy run that did nothing
  would be a silent failure.
- **Departure: changes for every vendor.** Spacelift's `changesV3` answers
  an error for vendors outside the Terraform family; every vendor records
  `changes.json`, so one read serves them all and no client breaks.
- **Departure: no passphrase of infraweave's own for Pulumi.** Spacelift's
  Pulumi runs need no `PULUMI_CONFIG_PASSPHRASE` from the stack, so its
  runner sets one (C300); a passphrase the runner chose would protect
  nothing, so the stack sets its own.
- **kubectl is checked by its published SHA-256 alone**, from the origin
  that serves the binary, where Terraform, OpenTofu and Terragrunt are
  checked against embedded signing keys. Kubernetes signs its releases
  keylessly through Sigstore, whose verification needs a trust root the
  launcher would have to keep current, and publishes no long-lived key.
- **Plans from the tools' own documents.** Each vendor's plan document is
  what its tool writes for a machine (Pulumi's engine events, kubectl's
  dry run, ansible-core's callback, CloudFormation's change set), never a
  log parsed for meaning
  ([33](../reference/33-vendor-tool-documents.md)).
- **UID 1983 and Spacelift's precedence** cost nothing and let existing images,
  hooks and runtime configuration move over unchanged.

## Constraints

- **Load balancer idle timeouts** between workers and the server must exceed the
  poll hold by at least 15 seconds (75 seconds at the defaults); the reference
  deployments set this (decision 0020).
- **The run token is visible to user code**, since hooks run in the same
  container as the runner. Its scope (one run, worker states only, the matching
  execution) bounds what a hook can do with it.
- **The Docker socket is root on the host.** A launcher host is a trust boundary
  for every pool that uses it; gVisor via `launcher.docker.runtime` narrows it.
- **One run per worker.** A host runs several launchers for more concurrency.
- **Report 08's unverified targets** are resolved above as infraweave choices
  marked [I]; a capture that shows Spacelift does otherwise amends this record.
- **Registration needs clocks within five minutes** of the server.
- **A run held by an offline worker holds its stack lock** until the worker
  returns or `run.lost_after` passes. Stop and kill reach the run only
  through the worker's next poll, so until then the stack's users see the
  worker as `OFFLINE` and wait.
- **Existing RSA pool keys** cannot be brought over; a pool moving from
  Spacelift creates a new P-256 CSR.

## Verification

- `TestUnitTransitionTable` (`internal/runs/internal/core/machine`): every edge of
  the table is allowed for its run types and driver, and every other pair is
  refused.
- `TestUnitTransitionRegister`: reads the
  [run-transitions register](../../data/run-transitions.json), which holds report
  08's transition table and T44, and fails when an edge there is missing from the
  machine or the machine has one beyond it.
- `TestAccRunClaim` (`internal/runs/internal/core/schedule`, PostgreSQL through
  testcontainers): seven-tier
  order with oldest first, one claim per run under 50 concurrent pollers, drift
  limit, and the `public-worker-slots` limit pooled per organization with an
  account limit below it.
- `TestUnitWorkerPoolSample` (`internal/workers/internal/core/sample`, in
  virtual time): one row per pool per minute, a repeated minute writes
  nothing, rows older than `worker.samples.retention` are deleted, and
  `SamplesBetween` returns the window; `TestAccWorkerPoolSamples`: two
  replicas sampling the same minute at once write one row per pool.
- `internal/workers` protocol adapter tests: registration refused for a foreign chain, a reset
  serial, a replayed `jti` and a skewed `iat`; poll hold, wake-up and timeout;
  signals; session renewal; `409` on a stale report.
- `TestUnitReport` and `TestUnitReportEdges` (`internal/runs/internal/core/report`):
  every worker-report edge by event, a repeated report answered again, a
  stale one refused, and `PLANNING`'s `succeeded` taking each row of the
  decision with no plan policy attached, refused `400` without its `delta`.
  `TestUnitReportCountsChunks`: a phase's end records its `logChunks`, and
  a started report, one of another state and a repeat record none.
- `TestUnitLogSignedRefusal`, `TestUnitLogRetries` and `TestUnitLogGivesUp`
  (`internal/agent/runlog`, in virtual time): a chunk whose `PUT` the store
  refuses with `403` stored from a second declaration, one refused for ten
  minutes committed as lost, a chunk after the budget is spent sent once,
  the budget renewed once a chunk is taken, and the state's chunk count.
- `TestUnitClientSignedRefusal` (`internal/agent/client`) and
  `TestUnitFetchSignedRefusal` (`internal/agent/source`): a store's `400`
  or `403` to a pre-signed upload, archive or saved workspace is marked a
  refused signature with the store's answer as its message, and a `404`,
  a `429`, a `503` and the worker API's own refusals are not.
- `TestUnitSaveSignedRefusal` (`internal/agent/exec`) and
  `TestUnitLauncherFetchesAgainARefusedSignature`
  (`internal/agent/launcher`), in virtual time: a refused upload declared
  again and stored after 1 second, the worker API's own `403` failing the
  save at once, and one refused for ten minutes failing it with the store's
  answer; a refused archive fetched again after 1 and 2 seconds from the
  specification asked for again, a refused saved workspace restored from
  its location asked for again, and one refused for ten minutes failing
  the preparation.
- `TestUnitLogsPage`, `TestUnitLogsCompact` and `TestUnitLogsDownload`
  (`internal/runs/internal/core/logs`): a lost chunk, a middle gap and a
  trailing gap below the reported count each read and compacted as the
  missing line once the run has left the state, a page in the state ending
  at a gap, and a state whose every chunk was lost pending within its
  retention and expired after. `features/runs/logs.feature`: a worker's
  chunks 0 and 2, chunk 1 committed as lost and a report of 4 chunks read
  and downloaded with the line in place of chunks 1 and 3.
- `TestAccRunReports` (`internal/app`, PostgreSQL and the `fs` driver): the
  handover ended once, uploads under the run's prefix, a chunk indexed once,
  an artifact the runner sealed opened by the server, the workspace's `307`,
  another run's token `401`, and the account's purge of the records and
  their objects.
- `internal/agent/launcher` tests over `agenttest`'s fake worker API and
  executor, in virtual time: registration and deregistration, an assignment
  prepared and started with its handoff, `stop`, `kill`, `cycle` and
  `drained`, a resumed run, a stored or missing exit after a restart, a
  refused session that keeps or loses the worker, backoff, and a failed
  preparation reported. `TestAccDockerExecutor` (Docker): the runner runs
  as PID 1 with UID and GID 1983 in `/mnt/workspace` and reads its standard
  input, and a container is found again, stopped, killed and missing.
- `internal/workers` reap tests on a fake clock: offline at 3 minutes with the
  run untouched; a poll at 59 minutes clears the mark and the run finishes;
  `FAILED` with `worker lost` at 60 minutes; a late poll answers `401`; a
  resumed registration after the session token expired keeps the run.
- `TestUnitRunTimers` (`internal/runs/internal/core/timers`, on a fake
  clock): each rule ends its runs at its boundary and not a minute before,
  with its note and its rule; the 30-day rule goes before a shorter one; a
  tracked run queued behind the lock and a run on a worker stay; a pass ends
  at most 1,000 runs. `TestAccRunTimers` (PostgreSQL): the boundary at
  microsecond precision, the released stack run lock and the event's
  `timer`, and two concurrent passes ending each run once.
- Gherkin acceptance with PostgreSQL, RustFS and Docker:
  `test/acceptance/features/workers/protocol.feature`,
  `runs/lifecycle.feature` (proposed with its retry, tracked with confirm,
  autodeploy, a task and one that skips initialization, destroy on delete,
  targeted replan and one whose handover stalls in `PREPARING_REPLAN`, stop,
  kill, no-log timeout, offline worker, lost run, and the timers, each run's
  history and `run_updated` events read; time passes for the timers and the
  reap on the run or worker alone, whose timestamps move back),
  `runs/runner_image.feature` (a Spacelift public runner image runs unchanged) and
  `runs/signed.feature`. `runs/terraform.feature` and `runs/tofu.feature`
  run a tracked run from a Gitea commit through `runConfirm` to `FINISHED`
  on a restored workspace, and a local preview, with the real tools
  ([0016](0016-testing-and-acceptance.md)); `runs/logs.feature` pages a
  state's log and downloads it once compacted.
- `TestUnitToolVerification` (`internal/agent/tools`): a release verified
  and cached, and a tampered `SHA256SUMS`, a tampered archive, a signature
  by another key, a missing signature and an oversized archive each
  refused.
- Each vendor's `iactest` plan cases parse the recorded documents of
  `test/fixtures/vendor-documents`: Pulumi's event logs of a create, no
  change, a change and a failed preview, and kubectl's `get` and dry runs
  of a create, no change, a change and a prune. The Ansible callback's
  tests run ansible-core over the recorded project, a check, an apply and
  a failure, and compare its records' statuses and recap with the recorded
  ones. `vendors/pulumi.feature`,
  `vendors/cloudformation.feature`, `vendors/kubernetes.feature` and
  `vendors/ansible.feature` run each vendor's tracked run, proposed run,
  task and, but for Ansible, destroy run
  ([0016](0016-testing-and-acceptance.md)).
- `api/worker.openapi.yaml` validates as OpenAPI 3, and a test checks that the
  served `/worker/v1` routes match it.

## References

- [08: The run lifecycle](../reference/08-run-lifecycle.md): states, transitions,
  queueing, phases, actions and timers.
- [10: Workers and execution](../reference/10-workers-and-execution.md): pools,
  workers, transports, launcher variables, runner images, caching and signed runs.
- [24 section 2.1](../reference/24-self-hosted-reference.md#21-process-inventory),
  [24 section 3.3](../reference/24-self-hosted-reference.md#33-message-queues) and
  [24 section 4](../reference/24-self-hosted-reference.md#4-networking-and-ports):
  Spacelift's self-hosted processes, queues and ports.
- [27 section 3.3](../reference/27-reference-materials.md#33-borrow-patterns-only-mpl-20):
  OTF's worker protocol, allocation and job tokens.
- [28 section 12.3](../reference/28-data-model.md#123-stacks-and-runs) and
  [28 section 12.5](../reference/28-data-model.md#125-workers): the run and worker
  tables.
- [16 section 1.1](../reference/16-state-management.md#11-how-a-run-reaches-it):
  per-run state credentials.
- [20 section 11](../reference/20-vendors.md#11-runner-images): runner images per
  vendor.
