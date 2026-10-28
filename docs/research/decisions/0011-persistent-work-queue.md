# 0011: Persistent work queue and scheduler

Status: Proposed

## Context

Most of what infraweave does happens after the request that asked for it has
been answered:
- VCS webhooks become runs ([0012](0012-vcs-integration-model.md));
- run state changes fan out to notifications, webhooks, audit entries and PR
  checks (decision 0015);
- schedules fire tracked runs, tasks, deletes and drift detection
  ([18 section 2](../reference/18-scheduling-drift-dependencies.md#2-scheduled-runs-tasks-and-deletes));
- timers settle runs that wait too long
  ([08 timers](../reference/08-run-lifecycle.md#timers));
- maintenance expires objects and keys ([0005](0005-persistence-postgresql.md)).

Self-hosted Spacelift runs this through eight logical queues, on SQS or on
PostgreSQL, with:
- visibility timeouts;
- a maximum receive count of 3 before a dead-letter queue;
- a FIFO variant;
- per-queue drain concurrency;
- a processing timeout of 900 seconds;
- cron fired from the drain.

The details are in
[24 section 3.3](../reference/24-self-hosted-reference.md#33-message-queues).
Report 27 weighs three ways to meet those semantics, along with report 08's
waits of hours or days: a queue of infraweave's own, River, or Temporal
([27 section 10](../reference/27-reference-materials.md#10-work-queue-and-workflow-engines)).
Two lessons from that report shape the design:
- Atlantis and Digger lose events by working in goroutines that outlive the
  request
  ([27 section 12](../reference/27-reference-materials.md#12-cross-tool-comparison)).
- A queue row is a delivery mechanism, not the run record.

## Decision

### An infraweave queue in PostgreSQL

- **Where it lives.** Background work runs from a queue of infraweave's own in
  `internal/queue`. Its jobs live in the `jobs` table of the main database
  ([0005](0005-persistence-postgresql.md)), so a job and the change that asks
  for it commit together.
- **Runs are not jobs.** A run is a row in `runs` with its own state machine,
  and a worker's poll claims runs directly from that table
  ([0008](0008-run-execution-and-worker-protocol.md)). The queue carries the
  work around runs: intake, fan-out, timers, schedules and maintenance.

**The `jobs` table.**

| Column | Type | Meaning |
|---|---|---|
| `id` | `bigint` identity | Claim order tie-breaker |
| `queue` | `text` | Logical queue (table below) |
| `kind` | `text` | Code constant `<area>.<verb>`, such as `vcs.process_delivery` |
| `payload` | `jsonb` | At most 64 KiB. Carries IDs, never secrets. Anything larger goes to the `inbox` store and the payload carries its key |
| `unique_key` | `text`, nullable | Deduplication key within the kind |
| `ordering_key` | `text`, nullable | FIFO group |
| `priority` | `smallint` | 0 (first) to 9, default 5 |
| `run_at` | `timestamptz` | Not claimable before this time |
| `state` | `text` | `pending`, `running`, `succeeded`, `dead`, `cancelled` |
| `attempt`, `max_attempts` | `int` | Attempts made, and the limit |
| `generation` | `bigint` | Incremented by every claim. It fences stale workers |
| `lease_until` | `timestamptz` | When a running job's lease lapses |
| `lease_owner` | `text` | Replica and worker slot, for diagnosis only |
| `rerun` | `boolean` | Set when a duplicate arrives while the job runs |
| `last_error` | `text` | Truncated to 4 KiB |
| `created_at`, `finished_at` | `timestamptz` | |

Indexes:
- `(queue, priority, run_at, id) WHERE state = 'pending'` for claims;
- unique `(kind, unique_key) WHERE state IN ('pending','running') AND unique_key IS NOT NULL`;
- `(ordering_key, id) WHERE ordering_key IS NOT NULL AND state IN ('pending','running')`;
- `(lease_until) WHERE state = 'running'` for lease recovery.

The migration sets aggressive autovacuum storage parameters on `jobs`
(`autovacuum_vacuum_scale_factor = 0.01`), because it is the most
update-heavy table.

**Logical queues.** Queue workers and the scheduler ticker run in replicas
with the `scheduler` role (decision 0020). Each queue has its own worker count per
replica, so a
backlog in one never starves another.

| Queue | Carries | Spacelift counterpart | Workers per replica |
|---|---|---|---|
| `default` | Background work: run follow-ups, stack and module housekeeping, external effects | `async-jobs`, `async-jobs.fifo` | 8 |
| `events` | Fan-out of domain events to notifications, webhooks, audit and checks (decision 0015) | `events-inbox` | 8 |
| `vcs-inbox` | Stored VCS and integration webhooks ([0012](0012-vcs-integration-model.md)) | `webhooks` | 4 |
| `deliveries` | Outbound HTTP deliveries (decision 0015) | part of `async-jobs` | 8 |
| `scheduler` | Fired schedules and run timers | `cronjobs` | 2 |
| `maintenance` | Expiry, pruning, backfills and resealing | none | 1 |

Spacelift's `dlq` and `dlq.fifo` become the `dead` state. Its `iot` queue
has no counterpart, because workers report over HTTP straight to the API
([0008](0008-run-execution-and-worker-protocol.md)).

### Enqueueing

- **Calls.** `queue.EnqueueTx(ctx, tx, job)` enqueues inside the caller's
  transaction. `queue.Enqueue(ctx, job)` is for callers without one. A job
  names its kind, payload and, optionally, `unique_key`, `ordering_key`,
  `priority`, `run_at` and `max_attempts`.
- **Wake-up.** The enqueue issues `pg_notify('infraweave_jobs', queue)` in the
  same transaction. PostgreSQL delivers it only at commit, so idle workers wake
  exactly when the job becomes visible, and never for a rolled-back job.
- **Duplicates.** A duplicate `(kind, unique_key)` resolves by the kind's
  registered policy:

  | Policy | Pending duplicate | Running duplicate | Used for |
  |---|---|---|---|
  | `Coalesce` | Keeps the earlier `run_at`, takes the newer payload | Sets `rerun`; the job returns to `pending` once the current attempt ends | Recalculations, such as refreshing a PR check from current state |
  | `Ignore` | Dropped | Dropped | Cron fires, periodic tasks, event deduplication on commit and run type ([08 section 4](../reference/08-run-lifecycle.md#4-queueing-the-stack-lock-and-scheduling)) |

### Claiming, leases and fencing

- **Claim.** A worker slot claims with one statement. It selects `pending`
  jobs of its queue whose `run_at` has passed, ordered by
  `priority, run_at, id`, with `FOR UPDATE SKIP LOCKED` and `LIMIT` set to its
  free slots. It sets `state = 'running'`, increments `attempt` and
  `generation`, and sets `lease_until` to now plus the lease. Replicas never
  wait on each other's claims.
- **FIFO.** A job with an `ordering_key` is claimable only when no other job
  with that key is running and no older pending job with that key exists. Jobs
  sharing a key therefore run one at a time, in insertion order. Jobs with
  different keys run in parallel. This takes the place of Spacelift's FIFO
  queue and its message groups.
- **Lease.** The lease is 5 minutes (`queue.lease`), matching Spacelift's
  300-second visibility timeout. A running handler's lease is extended every
  third of the lease.
- **Fencing.** Every completion, failure or extension is conditional on
  `(id, generation)`. A handler whose lease lapsed and was claimed again gets
  `queue.ErrLeaseLost`, and its result is discarded.
- **Recovery.** A `running` job whose `lease_until` has passed becomes
  `pending` again, and the lapsed attempt counts. Every replica's poll loop
  does this check, so a crashed replica's work is taken over within one lease.

### Handlers, timeouts and outcomes

- **Registration.** A kind is registered with
  `queue.Register(kind, handler, opts)`. The options give the queue, duplicate
  policy, `max_attempts` (default 5), timeout (default 5 minutes, at most 15
  minutes, Spacelift's 900-second processing bound) and retry delay.
- **Unregistered kinds.** A job of a kind no handler is registered for stays
  `pending`. This happens during a rolling upgrade, when an older replica sees
  a newer kind. Claims filter on the kinds the replica knows.

| Handler result | Outcome |
|---|---|
| `nil` | `succeeded`, or back to `pending` when `rerun` is set |
| `queue.Snooze(d)` | `pending` at now plus `d`; the attempt is not counted |
| `queue.Permanent(err)` | `dead` at once |
| any other error, a panic, or the timeout | Retried after a delay, or `dead` once `attempt` reaches `max_attempts` |

- **Retry delay.** Exponential backoff with full jitter: a random delay in
  [1 s, min(15 min, 1 s * 2^(attempt-1))].
- **Dead letters.** Dead jobs are kept 30 days (`queue.dead_retention`) with
  their last error. Three infraweave-specific admin operations, outside the
  Spacelift surface and recorded as divergences, list, retry and discard them:
  `GET /admin/queue/jobs`, `POST /admin/queue/jobs/{id}/retry` and
  `DELETE /admin/queue/jobs/{id}`. A retry resets `attempt` to zero. They need
  the account administrator permission of
  [0007](0007-authorization-spaces-and-roles.md).
- **Pruning.** Succeeded and cancelled jobs are deleted after one hour
  (`queue.completed_retention`) by the `queue.prune` periodic task.

### Exactly-once effects

Delivery is at least once. A handler can run again after doing its work, for
example when a replica dies before recording success. Effects are made exactly
once by three rules.

1. **Enqueue in the change's transaction.** Work is asked for if, and only if,
   the change commits. A webhook is stored and its job enqueued in one
   transaction before the provider gets its answer
   ([0012](0012-vcs-integration-model.md)). A run's state change and its
   event job commit together
   ([0008](0008-run-execution-and-worker-protocol.md)).
2. **Database effects commit with the completion.**
   - **`CompleteTx`.** A handler whose effect is database writes calls
     `queue.CompleteTx(ctx, tx, job)`. That marks the job `succeeded`, fenced
     by `generation`, in the same transaction as its writes. A lapsed lease
     makes the transaction fail and roll back, so the writes happen exactly
     once.
   - **The fallback.** A handler that cannot use `CompleteTx` makes its
     writes idempotent with a natural key, for example a run created from a
     delivery carries the delivery ID under a unique constraint.
3. **External effects carry a stable identity.**
   - **The identity.** An HTTP call to another system carries an identity
     derived from the job, not the attempt: `<kind>:<job id>` or the domain
     ID, such as a webhook delivery ID.
   - **Where it goes.** Receivers that support idempotency get it as
     `Idempotency-Key`, or as the delivery header the receiving protocol
     defines.
   - **Naturally idempotent effects** need nothing more: setting a commit
     status, or upserting a PR comment found by a marker.
   - **The rest.** Each kind's record says whether its external effect is
     exactly once or at least once.

### Scheduling and cron

- **What schedules.** Report 28 places `next_fire_at` on the rows that
  schedule: `scheduled_jobs` (runs, tasks, deletes) and `drift_detection`
  ([28 section 12.3](../reference/28-data-model.md#123-stacks-and-runs)).
  Infraweave's own recurring work is in `periodic_tasks(name primary key, interval, next_run_at)`.
- **The ticker.** Each `scheduler` replica runs one scheduler loop, every 5 seconds by
  default (`queue.scheduler_tick`). In one transaction it:
  1. selects up to 100 due rows (`next_fire_at <= now()`) with
     `FOR UPDATE SKIP LOCKED`;
  2. enqueues a fire job for each on the `scheduler` queue, with
     `unique_key = <row id>@<fire time>` and policy `Ignore`;
  3. computes and stores the next `next_fire_at`.

  Two replicas never fire one schedule twice, and no leader election is needed.
- **Missed firings.** After downtime, a schedule whose next computed time is
  still in the past fires once, not once per missed time, and moves to the
  first future time. A one-off timestamp schedule fires once, and its
  `next_fire_at` becomes null.
- **Grammar** (`internal/cron`).
  - **Fields.** Exactly five: minute, hour, day of month, month and day of
    week, where 0 and 7 both mean Sunday.
  - **Items.** Each field is a comma list of items. An item is `*`, `n` or
    `a-b`, each optionally followed by `/step`. `n/step` runs from `n` to the
    field's maximum.
  - **Compared with Spacelift.** This is a superset of the template schema's
    pattern ([18 section 2.6](../reference/18-scheduling-drift-dependencies.md#26-cron-and-timezone)).
    It adds ranges with steps and lists of ranges.
  - **Rejected forms**, with a validation error: names, `?`, `L`, `W`, `#`,
    `@` macros, a seconds or year field, values out of range, reversed
    ranges and a zero step.
  - **Day of month with day of week.** When both are restricted, a time
    matches either one, as Vixie cron does.
  - **How strict input validation is** comes from
    [0022](0022-api-errors-and-update-semantics.md)'s answer to P9.
    `internal/cron` is the one parser.
- **Time zones.**
  - **Names.** IANA names, read with `time.LoadLocation`. The binary embeds
    `time/tzdata`, so results don't depend on the host. The default is `UTC`.
  - **Daylight saving.** A local time that falls in a spring-forward gap fires
    once, at the first instant after the gap. A local time that repeats in the
    autumn fires once, at its first occurrence.
  - **Several expressions.** A schedule with several expressions fires once at
    each instant that any of them matches.
- **Firing.** A fire job's handler applies the feature's own rules, for example
  firing only for stacks on private pools, or skipping drift detection over the
  pool's limit
  ([18 section 2.5](../reference/18-scheduling-drift-dependencies.md#25-behaviour)).
  Then it creates the run or deletion in its own transaction with
  `CompleteTx`.
- **Periodic tasks** use the same ticker, with `unique_key = name` and
  `Ignore`, so a slow task never overlaps itself. Each record registers its
  own; the tasks of decisions 0005 to 0013 are:

  | Task | Interval | Owner |
  |---|---|---|
  | `queue.prune` | 10 minutes | this record |
  | `blob.expire` | 10 minutes | [0005](0005-persistence-postgresql.md) |
  | `idempotency.purge` | 1 hour | [0005](0005-persistence-postgresql.md) |
  | `authn.sessions.expire` | 1 hour | [0006](0006-authentication-and-tokens.md) |
  | `authn.keys.rotate` | 1 day | [0006](0006-authentication-and-tokens.md) |
  | `run.timers` | 1 minute | [0008](0008-run-execution-and-worker-protocol.md) |
  | `worker.reap` | 30 seconds | [0008](0008-run-execution-and-worker-protocol.md) |
  | `vcs.deliveries.prune` | 1 hour | [0012](0012-vcs-integration-model.md) |
  | `secrets.rotate` | 1 day | [0013](0013-secrets-at-rest.md) |

### Shutdown

On `SIGTERM` a replica:
1. stops claiming;
2. lets running handlers finish for up to `queue.shutdown_grace` (default 30
   seconds);
3. cancels the rest and returns their jobs to `pending` without counting the
   attempt.

This happens after the HTTP server has drained, so another replica takes the
work at once.

### Configuration and metrics

- **Settings.** `queue.lease`, `queue.poll_interval` (fallback poll when no
  notification arrives; default 1 s), `queue.scheduler_tick`,
  `queue.shutdown_grace`, `queue.completed_retention`,
  `queue.dead_retention`, and `queue.queues.<name>.workers`. They follow the
  usual flag, then environment, then file precedence.
- **Metrics.** Instrument names follow decision 0017. The queue reports:
  - jobs by queue, kind and state;
  - the age of the oldest pending job per queue;
  - attempts by kind and outcome (`succeeded`, `retried`, `dead`, `snoozed`,
    `lease_lost`);
  - claim latency.

  Kinds are code constants, so labels stay bounded.

## Rationale

- **An own queue over River.**
  - **What River gets right.** It shows the right mechanics: `SKIP LOCKED`
    claims ordered by priority and time, transactional insert, unique jobs,
    and maintenance on several replicas
    ([27 section 10.2](../reference/27-reference-materials.md#102-river)).
  - **What it lacks.** Spacelift's semantics need strict ordering and
    concurrency limits. In River those are Pro features under a proprietary
    licence, as is a separate dead-letter queue. Its open-source `discarded`
    state, kept 7 days by default and retryable with `JobRetry`, already covers
    the dead letters this record needs.
  - **Its defaults** are a one-minute job timeout and a one-hour rescue, and
    its maintenance runs on one leader-elected replica.
  - **Its schema** comes with its own migration tool, beside infraweave's.
  - **Its API** is pre-1.0 (v0.50.0).
  - **What it would save** is about a thousand lines, which the queue's own
    contract tests cover. Licence is not the obstacle, because MPL-2.0 permits
    linking an unmodified library. The missing features are.
- **Not Temporal.**
  - **A second cluster.** It would add four services and a database schema
    beside infraweave's PostgreSQL. Self-hosted Spacelift needs only
    PostgreSQL and object storage.
  - **Run state out of reach.** It would keep run state in an event history
    that the API can't query
    ([27 section 10.1](../reference/27-reference-materials.md#101-temporal)).
  - **Fitting the run model.** Runs wait days for people and policies, and
    their states are API data. A state machine over rows, with a queue for
    side work, models that directly.
- **Generations, not lease owners.** A fence on owner names fails when the
  same replica claims a job back after its own lease lapsed. A counter
  doesn't.
- **`rerun` rather than a second pending row.** It stops a burst of requests
  from piling up copies while still guaranteeing that a request arriving
  mid-run is honoured.
- **Cron on table rows, not a cron library's in-process timer.** Fire times
  survive restarts, are visible as `nextSchedule` in the API, and are shared
  across replicas without election.
- **An own cron parser.** The grammar is small and fixed by 0022's answer to P9. Owning it keeps
  validation errors, daylight-saving rules and the `nextSchedule` computation
  in one tested package.
  - **Rejected: `robfig/cron`.** It has had no release since 2020, and its
    grammar is wider than infraweave accepts.
- **Departures from Spacelift.**

  | Spacelift | infraweave | Why |
  |---|---|---|
  | SQS or a Postgres queue | The PostgreSQL queue only | Transactional enqueue, which SQS can't join |
  | A fixed receive count of 3 | Retry limit per kind, default 5 | Different kinds tolerate different numbers of failures |
  | Separate dead-letter queues | A `dead` state | Inspectable, and retryable in place |
  | A separate FIFO queue | Ordering keys | Ordering applies per key, not per queue |
  | An `iot` queue for worker messages | Nothing | Workers report over HTTP |

## Constraints

- **Throughput.** Queue throughput is bounded by PostgreSQL's write rate. Each
  job costs an insert and two to three updates, which suits infraweave's
  volume of events per run. It is not a general event bus.
- **Handlers.** Every handler must tolerate running twice. A kind without
  `CompleteTx` or a natural key is a review finding.
- **Clock.** Schedules fire up to one ticker interval late, plus queue latency.
  Firing is minute-accurate, not second-accurate.
- **Upgrades.** A kind that is renamed or removed needs a migration that moves
  or drops its pending jobs. Otherwise they wait for a handler that never
  comes.

## Verification

- **Queue contract.** The `TestAcc` suite in `internal/queue/queuetest` runs
  against PostgreSQL in testcontainers. It covers:
  - concurrent claims, where N slots claim M jobs and each runs once;
  - priority order;
  - FIFO order under concurrency;
  - the `Coalesce` and `Ignore` policies and `rerun`;
  - fencing (`ErrLeaseLost`), and recovery after a killed handler;
  - retry delays within bounds, and `dead` at `max_attempts`;
  - `Permanent`, `Snooze`, panics and timeouts;
  - an enqueue that rolls back with its transaction, and `CompleteTx`
    atomicity;
  - shutdown that hands jobs back without counting the attempt;
  - an unregistered kind left pending;
  - a wake-up by notification, measured under the poll interval.
- **Cron parser.** `internal/cron` has table tests of the grammar and its
  rejections. It tests next-fire computation across daylight-saving changes
  in `Europe/London`, `America/New_York` and `Australia/Lord_Howe`, whose
  shift is 30 minutes. `FuzzParse` checks that parsing never panics and that
  accepted expressions round-trip.
- **Scheduler.** Acceptance features run two server instances on one
  database:
  - `scheduler_scheduled_runs.feature`: each fire time produces one run;
  - `scheduler_missed_firings.feature`: downtime coalesces missed fire times;
  - `queue_periodic_tasks.feature`: tasks never overlap.
- **Dead letters.** `queue_dead_letters.feature` lists, retries and discards a dead
  job through the admin routes.

## References

- [24 section 3.3](../reference/24-self-hosted-reference.md#33-message-queues):
  Spacelift's eight queues and their semantics.
- [27 section 10](../reference/27-reference-materials.md#10-work-queue-and-workflow-engines):
  Temporal and River.
- [08 timers](../reference/08-run-lifecycle.md#timers) and
  [08 priority](../reference/08-run-lifecycle.md#priority).
- [18 section 2](../reference/18-scheduling-drift-dependencies.md#2-scheduled-runs-tasks-and-deletes):
  schedules and the cron pattern.
- <https://github.com/riverqueue/river> at `fc6c1d57cc72`: claim and uniqueness
  patterns (MPL-2.0; patterns only, no code copied).
