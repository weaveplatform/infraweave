# 0008: Run execution and the worker protocol

Status: Proposed

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
  its states and how a worker takes part. It answers P6
  ([30, product decisions](../reference/30-open-questions.md#product-decisions)).

## Decision

### The state machine is the authority

- **One transition table.** `internal/run` holds report 08's transition table as
  data: each edge T1 to T43 with its source state, target state, run types and the
  driver allowed to take it
  ([08 transition table](../reference/08-run-lifecycle.md#transition-table)). A
  pair not in the table is refused. `UNKNOWN` is served in the enum and never
  assigned; `PENDING` is absent from the served enum (decision 0021).
- **One entry point.** Every state change goes through
  `run.Transition(ctx, tx, req)` with the run ID, the expected `state_version`, the
  target state, the driver, the actor and a note. In the caller's transaction it:
  1. locks the run row (`SELECT ... FOR UPDATE`, in 0005's lock order) and checks
     that `runs.state_version` equals the expected value and that the edge exists
     for the run's type and the driver;
  2. sets the new state, increments `state_version` and sets `state_changed_at`;
  3. appends a `run_state_transitions` row (`run`, `sequence` = the new
     `state_version`, `state`, `timestamp`, `until`, `username`, `note`,
     `terminal`, `has_logs`);
  4. updates the stack's `state` and `state_set_by` when the run is the stack's
     tracked run and the state exists in `StackState`
     ([08 StackState](../reference/08-run-lifecycle.md#stackstate-and-runstate)).
     The columns live in a table the run transition owns, so the write stays
     inside the transition's transaction; decision 0004 places it;
  5. emits a `run_updated` event with `events.Emit` in the same transaction;
     decision 0015's fan-out, through the queue of
     [0011](0011-persistent-work-queue.md), reaches notifications, webhooks,
     the audit trail and the commit checks of
     [0012](0012-vcs-integration-model.md). When a blocking run ends, the
     transaction also enqueues decision 0009's `policy.trigger` job;
  6. when the new state is terminal, deletes the run's rows in `state_locks`, so a
     stopped, killed or failed run never leaves a state lock behind
     ([0010](0010-state-backend.md));
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
  | `runKill` ends the process | `FAILED`, note `killed` | T15, T18, T38, T41 or T43 by the state killed in |
  | Run timeout or no-log timeout on the worker | `FAILED`, note naming the timeout | as for a kill |
  | Approval policy `reject` | `FAILED` (the rules reading of T5, T24, T29) | as listed |
  | Upstream run in a dependency chain fails or is canceled | dependants `SKIPPED`, note naming the upstream run | T6 |
  | Drift run on a pool with drift runs disabled | created `QUEUED` and failed in the same transaction (T1 then T5), note explaining the limit | T1, T5 |
  | Task with `skipInitialization` | passes through `INITIALIZING` with no vendor init | T13 |
  | `PARSE` run type | served in the enum, never created | none |

### Who drives each transition

| Driver | Transitions | Mechanism |
|---|---|---|
| API action | T3, T8 (discard), T4 and T9 (`runCancel`), T16 and T19 (`runStop` once the worker confirms), T26 (`runConfirm`), T27 (`runDiscard`), T28 (`runTargetedReplan`) | Handler calls `run.Transition` with the caller's identity; [0007](0007-authorization-spaces-and-roles.md) checks the run action first |
| Creation | T1 | Every route in [08 section 1](../reference/08-run-lifecycle.md#1-run-types) inserts the run in `QUEUED` in the transaction that creates it |
| Scheduler | T2, T5 and T6 for dependencies, T4 for a push policy `cancel` | The `run.schedule` job, below |
| Approval policy | T5, T23, T24, T29, and the gates on T2 and T26 | Evaluated on entering `QUEUED`, `UNCONFIRMED` and `PENDING_REVIEW` and on each review ([09 section 8](../reference/09-policies.md#8-where-each-type-is-evaluated)) |
| Worker claim | T7, T30, T33 | The poll claims the run (below) |
| Worker report | T10, T11, T12 to T16, T18 for a tool error, T19, T32, T35 to T43, T44 | Runner or launcher reports through `/worker/v1` |
| Server decision after planning | T17, T18 for a plan policy `deny`, T20, T21, T22 | The server evaluates plan policies on the reported plan ([0009](0009-policy-engine-opa.md)) and picks the edge |
| Timers | T4, T9, T25, T27, T31, T34 and the 30-day rule | Periodic task `run.timers` (every minute, [0011](0011-persistent-work-queue.md)) |
| Liveness | the `FAILED` edge out of whichever worker state the run is in | Periodic task `worker.reap` (below) |

The worker never chooses a run's next state. It reports what happened, and the
server answers with the state the runner is to execute next, or with `pause` or
`exit`.

### From `QUEUED` to `READY`

`run.schedule` is a `default`-queue job with unique key `stack:<id>`, so bursts
coalesce into one evaluation ([0011](0011-persistent-work-queue.md)). It is
enqueued when a run is created, reaches a terminal state or `UNCONFIRMED`, is
reviewed, when an upstream run or external dependency settles, and on
`stackUnlock` and `stackEnable`. The handler locks the stack row and walks its
`QUEUED` runs oldest first
([08 section 4](../reference/08-run-lifecycle.md#4-queueing-the-stack-lock-and-scheduling)):

- **The stack lock.** `stacks.blocker_run_id` names the run holding the exclusive
  lock. A blocking run (`TRACKED`, `TASK`, `DESTROY`) moves to `READY` only when
  the column is empty, and takes it in the same transaction; it releases it on
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
  state is claimable serves the claim.
- **The claim** runs in one transaction:

  ```sql
  SELECT id FROM runs
  WHERE worker_pool_id = $1 AND state IN ('READY', 'CONFIRMED', 'REPLAN_REQUESTED')
  ORDER BY priority_tier, created_at, id
  LIMIT 1 FOR UPDATE SKIP LOCKED
  ```

  then `run.Transition` to `PREPARING`, `PREPARING_APPLY` or `PREPARING_REPLAN`,
  sets `runs.worker_id`, marks the worker busy and issues the run token
  ([0006](0006-authentication-and-tokens.md)). Two replicas never hand one run to
  two workers, and no replica is special.
- **Limits applied in the claim.** Drift runs (tiers 6 and 7) are skipped when the
  pool's executing drift runs reach its drift detection run limit
  ([10 section 2.4](../reference/10-workers-and-execution.md#24-pool-settings-with-behaviour)).
  On the public pool, an account's runs are skipped while it already has
  `parallelism` runs executing.
- **Wake-ups.** A replica holding idle polls `LISTEN`s on `infraweave_runs`. On a
  notification for a pool it wakes one held poll for that pool, the one from the
  most recently registered worker, which approximates Spacelift's newest-first
  assignment ([10 section 5.3](../reference/10-workers-and-execution.md#53-scheduling-inside-a-pool)).
  A woken poll that loses the `SKIP LOCKED` race goes back to waiting. A poll
  always tries one claim before it starts to wait, so a missed notification costs
  at most one poll interval.

### The worker API

- **Contract.** The worker API is served under `/worker/v1` by chi in the same
  `serve` process as the public API, and is described by its own OpenAPI document,
  `api/worker.openapi.yaml`. It is not part of the Spacelift-mirrored surface.
  Errors are problem details ([0022](0022-api-errors-and-update-semantics.md)).
- **Credentials** (formats are [0006](0006-authentication-and-tokens.md)'s):
  - **Pool token.** `workerPoolCreate` takes a base64 CSR, as in Spacelift
    ([10 section 2.3](../reference/10-workers-and-execution.md#23-credentials)).
    The server signs it with the pool CA, one ECDSA P-256 key per installation
    kept in `worker_pool_ca` and sealed by [0013](0013-secrets-at-rest.md),
    and returns the pool token: base64 JSON
    `{version, endpoint, pool, certificate}`. RSA keys of 2048 bits or more and EC
    P-256 keys are accepted. The private key never leaves the customer.
  - **Reset.** `workerPoolReset` signs a new certificate and records its serial on
    the pool. Registrations and sessions bound to an older serial are refused at
    their next call, so old workers stop within one poll.
  - **Registration.** `POST /worker/v1/register` carries a compact JWS signed with
    the pool private key, with the certificate in `x5c` and the payload
    `{pool, iat, jti, version, os, arch, metadata}`. The server checks the chain
    to the pool CA, the current serial, `iat` within five minutes, and that `jti`
    is unseen (table `worker_nonces`, kept 10 minutes). It answers with the worker
    ID and a worker session token (`aud` `infraweave-worker`, one hour).
  - **Run token.** Issued at claim (`aud` `infraweave-runner`), scoped to one run,
    accepted only while the run is in a worker state and its `state_version`
    matches the run's current execution, and valid at most until the run ends
    plus one hour.
- **Operations.**

  | Operation | Caller and token | Purpose |
  |---|---|---|
  | `POST /worker/v1/register` | launcher, pool key | Register; returns worker ID and session token |
  | `POST /worker/v1/poll` | launcher, session | Heartbeat; returns an assignment, signals, the drain flag and a renewed session token |
  | `POST /worker/v1/deregister` | launcher, session | Clean exit; the worker row is removed |
  | `GET /worker/v1/runs/{run}/spec` | launcher, run token | The run specification |
  | `POST /worker/v1/runs/{run}/reports` | runner or launcher, run token | Phase started, succeeded, failed or stopped; answers the next step |
  | `POST /worker/v1/runs/{run}/upload-urls` | runner, run token | Pre-signed `PUT` URLs for log chunks, sealed run artifacts, the workspace and observability artifacts, each for a declared size and SHA-256 |
  | `POST /worker/v1/runs/{run}/log-chunks` | runner, run token | Records uploaded chunks in `run_log_chunks` |
  | `POST /worker/v1/runs/{run}/artifacts` | runner, run token | Records uploaded run artifacts (kind, object key, size, SHA-256) |
  | `GET /worker/v1/runs/{run}/workspace` | launcher, run token | `307` to a pre-signed `GET` of the saved workspace |

- **The poll.** The request carries `{busy, run, version}`. The server updates
  `workers.last_seen_at`, then answers at once when it has anything to say, or
  holds the request until a wake-up or `poll_timeout`. The launcher asks for a hold
  between 5 and 120 seconds (default 60, Spacelift's default); the server caps it
  at `worker.poll_timeout_max`. A held poll holds no transaction or pooled
  connection ([0005](0005-persistence-postgresql.md)). The answer is always `200`
  with a JSON body, empty when nothing happened, so a session renewal can ride on
  it. It may carry:

  | Field | Meaning |
  |---|---|
  | `assignment` | A claimed run: ID, state (`PREPARING`, `PREPARING_APPLY` or `PREPARING_REPLAN`) and run token. Only for an idle, undrained worker |
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
metadata; the source, as a git URL with a short-lived credential or as a
pre-signed archive URL ([0012](0012-vcs-integration-model.md) says which per
provider); the runtime configuration and hooks; tool versions and the runner image;
timeouts; environment variables and mounted files; the managed state backend's
address and credentials ([0010](0010-state-backend.md)); and the in-run API token
and OIDC token settings ([0006](0006-authentication-and-tokens.md)).

- **Secrets are sealed to the pool.** The server opens the sealed values
  ([0013](0013-secrets-at-rest.md)) and seals the secret part of the
  specification to the pool's public key as a JWE through
  `github.com/go-jose/go-jose/v4`: `RSA-OAEP-256` with `A256GCM` for an RSA pool
  key, `ECDH-ES+A256KW` with `A256GCM` for a P-256 pool key. Only a holder of the
  pool private key can read it.
- **The run's artifact key.** The sealed part also carries a fresh 256-bit
  artifact key generated for the run. The server keeps it as
  `runs.artifact_key_sealed`, sealed by the keyring
  ([0013](0013-secrets-at-rest.md)), so it can open what the runner seals with
  it.
- **The launcher decrypts.** The launcher opens the JWE and hands values to the
  container as environment and files. The pool private key never enters the run
  container or the server.
- **Workspaces are end to end.** When a tracked run with changes finishes
  planning, the runner archives the workspace (`tar` with `zstd`) and encrypts it
  in 0013's segmented AES-256-GCM format, with a fresh data key wrapped by the
  pool's public key instead of the keyring. It uploads it to `workspaces` before
  it reports the plan. For `PREPARING_APPLY` and `PREPARING_REPLAN` the launcher
  downloads and decrypts it before starting the container. The server stores but
  cannot read it, which is Spacelift's guarantee for private pools
  ([10 section 1.1](../reference/10-workers-and-execution.md#11-public-and-private-pools-compared)).

### Phases, reports and the plan decision

- **Phase order and hooks** follow report 08
  ([08 section 5](../reference/08-run-lifecycle.md#5-phases-and-hooks)): the
  runner executes the hooks and the vendor commands of each state in one shell
  session, and `after_run` runs whenever the runner processed the run.
- **Reports.** A report is `{stateVersion, state, event, details}` with `event`
  `started`, `succeeded`, `failed` or `stopped`. The first `started` report ends
  the handover (T10, T32, T35). The server answers with the next step: a state to
  execute, `pause` (the run waits in `UNCONFIRMED` or `PENDING_REVIEW`, and the
  container exits) or `exit`. A `409` means the run moved on, for example a timer
  ended it, and the runner exits without further reports.
- **After planning** the runner uploads plan JSON and the delta, then reports
  `succeeded`. In that request's transaction the server evaluates the plan
  policies on the uploaded plan ([0009](0009-policy-engine-opa.md)), records the
  receipts, and takes T17, T18, T20, T21 or T22 by the rules of
  [09 section 8](../reference/09-policies.md#8-where-each-type-is-evaluated). The
  run stays `PLANNING` until then, so a worker cannot skip a policy.

### Logs and artifacts

- **Masking before anything leaves the container.** The runner applies
  [0013](0013-secrets-at-rest.md)'s masking rules line by line, as Spacelift does
  ([13 section 4.4](../reference/13-configuration.md#44-log-masking)): the values
  of every secret configuration element and of every variable in the launcher's
  mask list, words announced with `::add-mask`, and well-known patterns when the
  stack enables them, each replaced by five asterisks. Values shorter than 4 bytes
  are not masked, which is 0013's rule, not Spacelift's. Log chunks are therefore
  stored as masked plaintext.
- **Chunks.** The runner writes masked lines to stdout, which the launcher reads,
  and to chunks of at most 1 MiB or two seconds. Each chunk is uploaded by
  pre-signed `PUT` to `run-logs` under
  `<account>/<run>/<state>-<stateVersion>/<seq>`, then indexed in
  `run_log_chunks(run, state_version, seq, object key, bytes, first_at, last_at)`.
  Readers tail the index; logs therefore exist per state and state version.
- **Artifacts.** Plan JSON, changes, outputs and anything else the server or a
  policy reads are sealed by the runner with the run's artifact key, in 0013's
  streaming format (64 KiB AES-256-GCM segments, the same code in the same
  binary), uploaded to `run-artifacts` by pre-signed `PUT` (at most 512 MiB each)
  and recorded through `POST .../artifacts`. The server opens them with
  `runs.artifact_key_sealed`. The binary plan file travels only inside the
  encrypted workspace, which the server cannot read. Observability artifacts go to
  `run-observability` by pre-signed `PUT`.

### Liveness and lost workers

- A worker unseen for `worker.lost_after` (default 3 minutes, three default
  polls) is lost. The periodic task `worker.reap` (every 30 seconds) deletes lost
  worker rows and, in the same transaction, fails each run they held that is in
  `PREPARING`, `PREPARING_APPLY`, `PREPARING_REPLAN`, `INITIALIZING`, `PLANNING`,
  `APPLYING`, `PERFORMING` or `DESTROYING`, with note `worker lost`.
- A launcher that loses its container (crash, out of memory, timeout, kill)
  reports through its next poll (`busy: false` with the run ID, exit code and
  reason), and the server fails the run if no runner report ended it.

### Worker status, drain, cycle and reset

| Action | Behaviour |
|---|---|
| Status | `DRAINED` when drained, else `BUSY` when busy, else `IDLE`; `busy` and `drained` stay separate fields ([10 section 3.1](../reference/10-workers-and-execution.md#31-worker)) |
| Drain, undrain | `workerDrainSet` sets the flag and returns the worker with `busy`. A drained worker gets no assignment and finishes its current run |
| Cycle | `workerPoolCycle` sends `cycle` to every worker of the pool on its next poll. An idle launcher exits at once; a busy one exits after its run ends |
| Reset | A new certificate serial; old workers are refused at their next call and exit |
| `availableAt` | Set when a worker becomes idle, for autoscalers ([10 section 3.3](../reference/10-workers-and-execution.md#33-drain-cycle-and-recycle)) |

### Pools

- **Private pools** are `worker_pools` rows in a space, with the preparing
  timeout (default 15 minutes) and drift detection run limit
  ([28 section 12.5](../reference/28-data-model.md#125-workers)).
- **The public pool** is a pool the installation's operator runs for every
  account, on the same protocol. Its launchers register with an installation-level
  pool token that the operator issues through the administration API.
  `PublicWorkerPool.parallelism` caps each account's executing runs on
  it. The restrictions Spacelift places on its public pool hold here too: no run
  priorities, runner images only from the configured registry allowlist (default:
  Spacelift's list), no scheduled or drift runs. An installation without public
  workers simply has none registered.

### The launcher and the runner

- **One binary, two subcommands.** `infraweave worker` (the launcher) registers,
  polls, prepares and supervises; `infraweave worker exec` (hidden) executes the
  phases inside the run container.
  0020 packages them and 0004 places the code.
- **Preparation**, inside the preparing timeout: fetch the specification and
  decrypt its secrets; evaluate the worker-side initialization policy; fetch the
  source or restore the workspace; download and verify tool binaries; pull the
  runner image; start the container. A failure reports T11, T36 or T44.
- **The container.** Docker through `github.com/moby/moby/client`. The launcher
  bind-mounts its own executable read-only at `/opt/infraweave/bin/infraweave`
  and runs it as the container's root process with the argument `runner`,
  replacing the image's `ENTRYPOINT` and `CMD`. The runner is statically linked,
  so it runs on any image's libc. The container runs as UID and GID 1983, with the
  workspace at `/mnt/workspace`, the tool binaries mounted read-only, and the
  launcher's network, runtime and extra mount settings.
- **Kubernetes.** A controller that creates a pod per run acts as a launcher on
  this same protocol. Its resources and behaviour are outside this record.

### Runner images

- **Contract.** An image works if it has a user with UID 1983, a `ps` command, and
  the architecture of the worker, and does not rely on `ENTRYPOINT` or `CMD`
  ([10 section 6.3](../reference/10-workers-and-execution.md#63-requirements-on-a-custom-image)).
  That is Spacelift's contract, so Spacelift's public runner images and customers'
  derived images run unchanged.
- **Selection** follows Spacelift's precedence: runtime configuration, stack, the
  account default, the launcher default
  ([10 section 6.1](../reference/10-workers-and-execution.md#61-defaults-and-selection)).
- **Defaults.** infraweave publishes its own multi-arch images
  (`ghcr.io/weaveplatform/infraweave-runner-terraform`, `-opentofu`, `-ansible`)
  that meet the contract.
- **Tool binaries.** The launcher downloads the requested version of Terraform,
  OpenTofu, Terragrunt or kubectl, verifies it and mounts it read-only (for
  example at `/bin/terraform`)
  ([20 section 3.5](../reference/20-vendors.md#35-version-management)):

  | Tool | Verification |
  |---|---|
  | Terraform | `SHA256SUMS` signed by HashiCorp's PGP key, which is embedded in the binary, then the archive checksum |
  | OpenTofu | `SHA256SUMS` signed by the OpenTofu signing key, embedded, then the archive checksum |
  | Terragrunt, kubectl | The published SHA-256 checksum |

  Binaries are cached at `<cache_dir>/binaries/<tool>/<version>/<os>_<arch>` and
  reused. `launcher.tool_mirror` points downloads at a mirror for air-gapped
  installations, and `skipVendorBinaryDownload` skips the download.

### Timeouts and timers

| Limit | Default | Enforced by | Outcome |
|---|---|---|---|
| Run timeout | 70 minutes | launcher, from container start | `FAILED`, note `run timeout` |
| No-log timeout | 7 minutes | launcher, from the last line on the container's output | `FAILED`, note `no log output` |
| Preparing timeout | 15 minutes, per pool | server: no `started` report in time | `FAILED` (T11, T36, T44) |
| Lost worker | 3 minutes | `worker.reap` | `FAILED`, note `worker lost` |
| Not picked up: 2 days (proposed, test) or 7 days (tracked, task, destroy) | as Spacelift | `run.timers` | `CANCELED` |
| `UNCONFIRMED` 7 days unchanged | as Spacelift | `run.timers` | `DISCARDED` |
| `CONFIRMED` or `REPLAN_REQUESTED` 7 days unchanged | as Spacelift | `run.timers` | `FAILED` |
| 30 days after creation | as Spacelift | `run.timers` | `CANCELED`, `DISCARDED` or `FAILED` by state |

The timer rules and their notification rules are report 08's
([08 timers](../reference/08-run-lifecycle.md#timers)). `run.timers` reads
`runs (state, state_changed_at)` and skips runs held by a live worker. The day
counts are configuration with Spacelift's values as defaults.

### Stop and kill

- `runStop` is valid in `INITIALIZING` and `PLANNING` only
  ([08 section 6](../reference/08-run-lifecycle.md#6-run-actions)). It records
  `stop_requested_at`; the worker's next poll receives `stop`; the launcher sends
  `SIGINT` to the runner, which passes it to the running command and reports
  `stopped` when it ends (T16, T19).
- `runKill` is valid once a stop was requested. The worker receives `kill`, the
  launcher kills the container with `SIGKILL`, and the run fails with note
  `killed`.

### Signed runs

The launcher evaluates the worker-side initialization policy before it starts the
container, with the embedded OPA of [0009](0009-policy-engine-opa.md)
([10 section 10](../reference/10-workers-and-execution.md#10-signed-runs-and-worker-side-policy)).
The input carries at least `run` (with `user_provided_metadata` and
`commit.hash`) and `static_run_environment` (with `stack_slug`). An invalid policy
stops the launcher at start. A `deny` fails the run in preparation, with the
reasons in the log of the preparing state. The `exec` built-in that Spacelift
offers this policy is available to it alone; 0009 lists built-ins.

### Configuration

- **Server:** `worker.poll_timeout_max` (120s), `worker.lost_after` (3m),
  `run.preparing_timeout_default` (15m), `run.timers.*`,
  `worker.public_pool.allowed_registries`.
- **Launcher:** `launcher.token`, `launcher.private_key`, `launcher.poll_timeout`
  (60s), `launcher.run_timeout` (70m), `launcher.log_timeout` (7m),
  `launcher.cache_dir`, `launcher.metadata`, `launcher.default_runner_image`,
  `launcher.default_ansible_runner_image`, `launcher.initialization_policy`,
  `launcher.mask_envs`, `launcher.whitelist_envs`, `launcher.tool_mirror`,
  `launcher.docker.config_dir`, `.network`, `.runtime`, `.extra_mounts`,
  `.ro_extra_mounts` and `.wo_extra_mounts`. Environment variables are
  `INFRAWEAVE_LAUNCHER_*`. Every Spacelift launcher variable of
  [10 section 5.1](../reference/10-workers-and-execution.md#51-launcher-environment-variables)
  has an equivalent here except the transport selectors and four this record
  leaves open: `SPACELIFT_SENSITIVE_OUTPUT_UPLOAD_ENABLED`,
  `SPACELIFT_RUN_LOGS_ON_STANDARD_OUTPUT_ENABLED`, `SPACELIFT_DEBUG` and
  `SPACELIFT_LAUNCHER_WORKSPACE_INIT_TIMEOUT`, whose deadline the pool's
  preparing timeout bounds.

## Rationale

- **P6: HTTP long-poll only (option a).** Spacelift retires MQTT for workers on
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
- **Departure: lost workers fail their runs.** Spacelift's 30-day rule leaves a
  run alone while a worker executes it. With a poll every minute, infraweave
  knows when a worker is gone and fails its run after three minutes instead of
  leaving the stack locked.
- **Departure: infraweave's own default runner images**, so an installation does
  not depend on another vendor's registry; the shared contract keeps Spacelift's
  images usable.
- **Departure: launcher settings are named `INFRAWEAVE_LAUNCHER_*`.** The
  launcher is infraweave's client, not Spacelift's.
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

## Verification

- `internal/run/transitions_test.go`: every edge of the table is allowed for its
  run types and driver, and every other pair is refused.
- `internal/run/report08_test.go`: parses report 08's transition table and fails
  when an edge there is missing from the implementation.
- `internal/run/claim_test.go` (PostgreSQL through testcontainers): seven-tier
  order with oldest first, one claim per run under 50 concurrent pollers, drift
  limit and public-pool parallelism.
- `internal/workerapi` tests: registration refused for a foreign chain, a reset
  serial, a replayed `jti` and a skewed `iat`; poll hold, wake-up and timeout;
  signals; session renewal; `409` on a stale report.
- Gherkin acceptance with PostgreSQL, MinIO and Docker:
  `test/acceptance/features/worker_protocol.feature`,
  `runs_lifecycle.feature` (proposed, tracked with confirm, autodeploy, task,
  targeted replan, stop, kill, lost worker, timers on a fake clock),
  `runs_runner_image.feature` (a Spacelift public runner image runs unchanged) and
  `runs_signed.feature`.
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
