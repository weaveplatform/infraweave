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
  and T44 and T45 below, each with the register's source state, target state and run
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
  A woken poll that loses the `SKIP LOCKED` race goes back to waiting. A poll
  always tries one claim before it starts to wait, so a missed notification costs
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
    their next call, so old workers stop within one poll.
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
  The answer is always `200`
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
- **The runner image** is the run's runtime configuration's, else the
  stack's, else the account's default for private pools.
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
  change owners; otherwise every user may read and write it, and a launcher
  that is not root may be refused removing what the run wrote, which it logs.
- **The tool.** `tool` names `name` (`terraform` or `tofu`), `version`
  and `download`. The version is the Version Catalog's effective version
  for the stack's constraint, resolved at handover, so every phase of the
  run uses one release. The constraint is the stack's `terraformVersion`,
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
  values. Configuration reads list them under the `ConfigElementCategory`
  value `INFRAWEAVE_ENVIRONMENT`. The server puts the `TF_VAR_infraweave_*`
  variables it knows at handover in the clear part, unless the stack sets a
  variable of the same name: `account_name`, `commit_branch`, `commit_sha`,
  `local_preview`, `project_root`, `repository`, `run_id`, `run_trigger`,
  `run_type`, `space_id`, `stack_branch`, `stack_id`, `stack_labels`,
  `stack_labels_list` (the labels as an HCL list) and `workspace_root`
  (`/mnt/workspace`). The runner sets `run_state`, which changes with each
  phase, and the launcher sets `INFRAWEAVE_API_TOKEN` from the sealed part.

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
  installs again from the lock file, and leaves out and logs a link that
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
  state version.
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
  pipe and waits to read it back, which proves every earlier line read. A
  chunk the worker API or the store cannot take for now is sent again for up
  to ten minutes, as a report is; one still refused is dropped with a
  warning. The `after_run` hooks run once the run has left its last worker
  state, so their output goes to stdout alone. Each chunk is uploaded by pre-signed `PUT`, then
  `log-chunks` commits it and indexes it in
  `run_log_chunks(run, state_version, seq, state, object key, bytes, first_at, last_at)`;
  a chunk already indexed is skipped, so a repeated `seq` is not duplicated.
  Readers tail the index; logs therefore exist per state and state version.
- **Reading.** `runLogs` answers a page of whole chunks of one state and
  state version, in sequence order, at most 1 MiB and 64 chunks. While the
  run is in the state a page ends before a chunk not yet indexed; once it
  has left, a missing sequence number is skipped. Each line's timestamp is
  its chunk's `first_at`, and `sanitize` strips ANSI escape sequences only
  (DV-117). A chunk whose object retention removed is left out and the page
  is `expired`.
- **Compaction.** The transaction of a transition out of a worker state
  enqueues `run.logs.compact` for that state and state version, keyed so
  it runs once. The job writes the indexed chunks in sequence order to one
  object, `<account>/<run>/<state>-<stateVersion>.log` in `run-logs`, and
  running it again changes nothing. `runLogsDownloadURL` answers that
  object's pre-signed `GET`, with `ongoing` while the run is in the state
  or the compaction is pending, `expired` when no chunk was written or
  retention removed them, and `deleted` after `runLogsDelete`.
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
  `PublicWorkerPool.parallelism` reports the account's share: its account
  limit when set, otherwise the organization's. An installation without
  public workers simply has none registered.

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
- **Preparation**, inside the preparing timeout: fetch the specification and
  decrypt its secrets; evaluate the worker-side initialization policy; fetch the
  source or restore the workspace; download and verify tool binaries; pull the
  runner image; start the container. A failure reports T11, T36 or T44.
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
  worker, finds the run's container by its labels and follows its output, or
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
  that meet the contract, defined under `deploy/images/`. They hold the
  tools a phase's shell needs (`bash`, `ca-certificates`, `curl`, `git`,
  `jq`, `openssh-client`, `ps` and `tzdata`) and no Terraform or OpenTofu
  binary, which the launcher mounts, as Spacelift's images do
  ([10 section 6.1](../reference/10-workers-and-execution.md#61-defaults-and-selection)).
- **Tool binaries.** The launcher downloads the requested version of Terraform,
  OpenTofu, Terragrunt or kubectl, verifies it and mounts it read-only at
  `/bin/<tool>`, such as `/bin/terraform` or `/bin/tofu`
  ([20 section 3.5](../reference/20-vendors.md#35-version-management)):

  | Tool | Verification |
  |---|---|
  | Terraform | `SHA256SUMS` signed by HashiCorp's PGP key, which is embedded in the binary, then the archive checksum |
  | OpenTofu | `SHA256SUMS` signed by the OpenTofu signing key, embedded, then the archive checksum |
  | Terragrunt, kubectl | The published SHA-256 checksum |

  Binaries are cached at `<cache_dir>/binaries/<tool>/<version>/<os>_<arch>` and
  reused. Each is written beside its path and renamed into place, so
  launchers that share a cache never see a partial one. Archives are
  bounded at 256 MiB, and only the tool's binary is extracted.
  `launcher.tool_mirror` points downloads at a mirror for air-gapped
  installations, which serves each release's files under
  `<tool_mirror>/<tool>/<version>/` with the vendor's file names, the layout
  of `releases.hashicorp.com`. `skipVendorBinaryDownload` skips the download.
  A refused release fails the run in preparation, and its note names the
  release and the step that refused it.

### Timeouts and timers

| Limit | Default | Enforced by | Outcome |
|---|---|---|---|
| Run timeout | 70 minutes | launcher, from container start | `FAILED`, note `run timeout` |
| No-log timeout | 7 minutes | launcher, from the last line on the container's output | `FAILED`, note `no log output` |
| Preparing timeout | 15 minutes, per pool | `worker.reap`: no `started` report in time | `FAILED` (T11, T36, T44), note `preparing timeout` |
| Offline worker | 3 minutes (`worker.offline_after`) | `worker.reap` | no new work; the run continues |
| Lost run | 60 minutes (`run.lost_after`) | `worker.reap` | `FAILED`, note `worker lost` |
| Not picked up: 2 days (proposed, test) or 7 days (tracked, task, destroy) | as Spacelift | `run.timers` | `CANCELED` |
| `UNCONFIRMED` 7 days unchanged | as Spacelift | `run.timers` | `DISCARDED` |
| `CONFIRMED` or `REPLAN_REQUESTED` 7 days unchanged | as Spacelift | `run.timers` | `FAILED` |
| 30 days after creation | as Spacelift | `run.timers` | `CANCELED`, `DISCARDED` or `FAILED` by state |

The timer rules and their notification rules are report 08's
([08 timers](../reference/08-run-lifecycle.md#timers)). `run.timers` reads
`runs (state, state_changed_at)` and skips runs held by a worker that still
has a row, offline or not. The day
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

- **Server:** `worker.poll_timeout_max` (120s), `worker.offline_after` (3m),
  `worker.samples.retention` (35 days, at least a minute), `run.lost_after` (60m),
  `run.preparing_timeout_default` (15m), `run.timers.*`,
  `worker.public_pool.allowed_registries`.
- **Launcher:** `launcher.token`, `launcher.private_key`, `launcher.poll_timeout`
  (60s), `launcher.run_timeout` (70m), `launcher.log_timeout` (7m),
  `launcher.cache_dir`, `launcher.metadata`, `launcher.admin_listen` (`:9091`),
  `launcher.state_lock_timeout` (5m),
  `launcher.log.*`, `launcher.default_runner_image`,
  `launcher.default_ansible_runner_image`, `launcher.initialization_policy`,
  `launcher.mask_envs`, `launcher.whitelist_envs`, `launcher.tool_mirror`,
  `launcher.ca_file`, `launcher.docker.config_dir`, `.network`, `.runtime`, `.runner_binary`,
  `.extra_mounts`, `.ro_extra_mounts` and `.wo_extra_mounts`. Environment variables are
  `INFRAWEAVE_LAUNCHER_*`. Every Spacelift launcher variable of
  [10 section 5.1](../reference/10-workers-and-execution.md#51-launcher-environment-variables)
  has an equivalent here except the transport selectors and four this record
  leaves open: `SPACELIFT_SENSITIVE_OUTPUT_UPLOAD_ENABLED`,
  `SPACELIFT_RUN_LOGS_ON_STANDARD_OUTPUT_ENABLED`, `SPACELIFT_DEBUG` and
  `SPACELIFT_LAUNCHER_WORKSPACE_INIT_TIMEOUT`, whose deadline the pool's
  preparing timeout bounds.

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
- Gherkin acceptance with PostgreSQL, RustFS and Docker:
  `test/acceptance/features/workers/protocol.feature`,
  `runs/lifecycle.feature` (proposed, tracked with confirm, autodeploy, task,
  targeted replan, stop, kill, offline worker, lost run, timers on a fake clock),
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
