# 0015: Events: one model for Deploy and Flows, streams, notifications, webhooks and audit

Status: Accepted

## Context

Spacelift sends what happens inside it out through several surfaces
([19 section 1](../reference/19-events-notifications-audit.md#1-the-outbound-surfaces)):
- the notification inbox;
- the notification policy, whose rules route to the inbox, Slack, named
  webhooks and pull request comments;
- per-stack and per-module webhooks;
- the audit trail and its account webhook.

Teams and Datadog are packaged as a named webhook plus a policy, so they
need nothing beyond those surfaces
([19 section 5](../reference/19-events-notifications-audit.md#5-microsoft-teams),
[section 9](../reference/19-events-notifications-audit.md#9-datadog)).

Spacelift Flows reacts to Deploy through blocks that subscribe to run
events ([23 section 15](../reference/23-flows.md#15-spacelift-deploy-integration)),
a link between two products with separate event systems. infraweave builds
Flows on the same events as Deploy ([0018](0018-flows-runtime.md)), so the
event model has to serve both.

Spacelift's clients learn about change by polling: `spacectl` pages run
logs per state and polls the run history with a back-off, and there is no
push ([06 section 5.1](../reference/06-spacectl-and-clients.md#51-log-streaming)).
infraweave's console, `infraweavectl` and integrators need live run logs,
run state, worker pool queues and Flows activity without each building
its own polling loop.

The research leaves the delivery contract largely undocumented: the retry
back-off, timeouts, retryable conditions, failure counting, header
precedence, default payloads for module and worker-usage events, and the
audit stream's retries (captures C170 to C184,
[30](../reference/30-open-questions.md#captures-needed)).
infraweave has to fix each one, and say where it is choosing rather than
copying.

The sources share one shape. Run state changes, module version changes,
internal errors and worker usage feed a policy-driven router with four
sinks, beside the per-stack webhooks and the audit stream
([19, "What this means for infraweave"](../reference/19-events-notifications-audit.md#what-this-means-for-infraweave)).

## Decision

### One event model

Every outbound effect, every Flows trigger on Deploy and every stream
starts as an **event**, written by the service that makes the change,
never by an HTTP handler, so every entry point produces the same stream.
Deploy and Flows share the envelope, the emit path and the kind registry
in `internal/platform/outbox`, which sits below every domain that emits.
The `events` domain holds only the sinks: the inbox, HTTP deliveries,
Slack, Teams, pull request comments and the streams. Flows triggers are a
fan-out consumer inside `flows`, so `events` never imports `flows`. Each
domain writes audit entries through `internal/platform/audit` in the
change's own transaction, and `events` holds the trail's search and its
shipper. Decision 0004 owns their placement.

| Kind | Raised when | Subject | Notification policy |
|---|---|---|---|
| `run_updated` | A run changes state (decision 0008's state machine), drift detection runs included | run | yes |
| `drift_detected` | A drift detection run finishes with changes ([18 section 3](../reference/18-scheduling-drift-dependencies.md#3-drift-detection)) | stack | no |
| `policy_evaluated` | A policy evaluation records its receipt (decision [0009](0009-policy-engine-opa.md)) | the evaluated run, stack or module | no |
| `notification_created` | An inbox entry opens, or a repeat increments it (below) | notification | no |
| `module_version` | A module version changes state ([0014](0014-module-and-provider-registry.md)) | version | yes |
| `internal_error` | infraweave fails while talking to another system: VCS, cloud, Slack, a webhook | the component | yes |
| `worker_usage` | The daily worker usage assessment crosses 60% of the configured limit or burst budget | account | yes |
| `worker_updated` | A worker registers, goes offline, drains or leaves its pool (decision 0008) | worker | no |
| `flows.event` | A Flows block emits an event ([0018](0018-flows-runtime.md)) | flow | no |

- **Kinds are typed.** Each kind's document has a JSON Schema, served as a
  component of the OpenAPI document (`EventRunUpdated` and so on) and
  carried by the envelope's `schema_version`. Within a version a schema
  only gains optional fields. The same schemas type webhook default
  payloads, stream messages, Flows trigger outputs and the expressions
  that read them ([0024](0024-expression-language.md)).
- **Outbox.** `events.Emit(ctx, tx, ev)` inserts a row into `events` in the
  transaction of the change, and in the same transaction enqueues an
  `events.fanout` job on decision 0011's queue, keyed by the event's ULID,
  and issues `pg_notify('infraweave_events', '<account>:<id>')`, which
  PostgreSQL delivers at commit. An event therefore exists exactly when its
  change commits. Flows events use the same call and land in `flow_events`
  with their lineage ([0018](0018-flows-runtime.md)).
- **The event row** holds `account`, `id` (ULID), `kind`,
  `schema_version`, `space`, `subject_kind`, `subject_id`, `sequence` (the
  run's `stateVersion`, or a per-subject counter), `occurred_at`
  (nanoseconds) and `document` (JSONB). For the four kinds the
  notification policy reads, the document is the event's notification
  policy input object, snake_case with nanosecond timestamps, built once
  at emit time
  ([19 section 3](../reference/19-events-notifications-audit.md#3-the-notification-policy)).
  Where the docs page and the policy contract differ, the contract wins, as
  report 19 rules: `policy_receipts` sits at `run_updated`, and the run type
  and change action enums are the contract's. The other kinds' documents
  are infraweave's own schemas.
- **Fan-out.** The `events.fanout` job computes every delivery the event
  causes and enqueues one `events.deliver` job per target, then marks the
  event fanned out. Each delivery job carries the idempotency key
  `(event, sink, target, rule index)`, unique in `event_deliveries`, so a
  fan-out that runs twice creates no second delivery. One slow endpoint
  never holds up another.

| Sink | Targets for one event |
|---|---|
| Inbox | `internal_error` always; any `inbox` rule result |
| Notification policy | Each `NOTIFICATION` policy selected by the event's space, evaluated with decision 0009's engine, for the four kinds it reads; each `slack`, `webhook` and `pull_request` result becomes a target |
| Per-stack webhooks | `run_updated` on a stack or module: every enabled `WebhooksIntegration` on it |
| Per-stack Slack subscription | `run_updated`: every channel subscribed to the stack or module |
| Audit webhook | Audit entries, and `run_updated` when `includeRuns` is set |
| Commit checks | `run_updated` on a stack or module with a VCS integration: one `vcs.report_check` job per check ([0012](0012-vcs-integration-model.md)) |
| Flows triggers | Every enabled Deploy trigger block whose kind and filter match: the fan-out inserts the root Flows event and its deliveries in its own transaction ([0018](0018-flows-runtime.md)) |

- **No loops.** A `notification_created` event never targets the inbox, and
  an event a Flows block emits reaches Deploy sinks only through Deploy
  operations the block calls.
- **Policy selection** follows report 19 section 3. A run uses its space
  and the spaces it inherits from. An internal error uses the component's
  space, or the account's top space, checking stack, worker pool, AWS
  integration and policy in that order. A module version uses the module's
  space, and worker usage uses the account's top space, where Spacelift
  uses its root space. The last two are inferences (capture C171).

### Event streams

Server-sent-event streams are part of the public REST API, for the console,
`infraweavectl` and integrators. Every stream has a polling equivalent, and
no capability needs a stream.

| Stream | Path | Message types | Same data by polling |
|---|---|---|---|
| Run logs | `GET /runs/{runId}/logs/stream` | `log` (lines for one state and state version, with the continuation token), `state` (the run entered a state), `end` | `runLogs` with `state`, `stateVersion` and `token` |
| Run state | `GET /runs/{runId}/stream` and `GET /stacks/{stackId}/runs/stream` | `run_updated`, `drift_detected`, `policy_evaluated` | The run, its history and the stack's runs |
| Worker pool queue | `GET /worker-pools/{workerPoolId}/stream` and `GET /public-worker-pool/stream` | `run_updated` for runs scheduled on the pool, `worker_updated` | The pool's schedulable-run and worker searches |
| Flows events | `GET /api/v1/flows/flows/{flowId}/events/stream` | `flows.event`, `invocation_updated`, `counters` | The flow's event and invocation lists |

- **Format.** `Content-Type: text/event-stream`, `Cache-Control: no-store`
  and `X-Accel-Buffering: no`. Each message has `id:` (the event's ID, or
  for logs the continuation token), `event:` (the message type) and one
  `data:` line holding a JSON object of the kind's schema. The first
  message is preceded by `retry: 3000`, and a comment line every 15 s keeps
  idle connections open.
- **Wake-up.** Each `serve` replica holds one `LISTEN` connection for
  `infraweave_events` and `infraweave_logs`, and routes notifications to its
  local subscribers. Recording log chunks in `run_log_chunks` (decision
  0008) notifies `infraweave_logs` with the run, state and state version.
  Notifications carry identifiers only; a replica reads the row before it
  sends.
- **Resume.** A client reconnects with `Last-Event-ID`, and the stream
  replays from the row after it. When that ID is older than
  `events.retention`, the stream sends `reset` and the client refetches by
  polling before it continues.
- **Authorization.** A stream authorizes as its polling equivalent when it
  opens, and checks each message's subject against the caller's
  permissions when it sends, so a revoked permission stops messages. When
  the caller's token expires the stream sends `end` with reason
  `unauthenticated` and closes.
- **Limits.** `events.streams.max_per_identity` (default 20) and
  `events.streams.max_per_replica` (default 10,000) open streams; over
  either, the open answers `429 rate-limited` (decision 0003). A client that
  falls behind by more than `events.streams.buffer` (default 1 MiB) is sent
  `end` with reason `slow-consumer` and resumes from its last ID.
- **Shutdown.** A `serve` replica that is shutting down sends `end` with
  reason `shutdown` to every stream within `server.shutdown.timeout`
  ([0017](0017-observability-and-health.md)), and clients reconnect to
  another replica.
- **Described in the API.** Each stream is an operation of the served
  OpenAPI document with a `text/event-stream` response and
  `x-infraweave-stream-messages`, which lists its message types and their
  schemas, so the SDK offers typed stream iterators
  ([0001](0001-rest-first-spacelift-surface.md)).

### Delivery guarantees

- **At least once** per target. A delivery job that crashes after sending
  sends again. Every HTTP delivery carries `X-Infraweave-Delivery: <ULID>`,
  stable across retries of the same target, so receivers can deduplicate,
  and `X-Infraweave-Event: <kind>`.
- **No order across deliveries**, as on Spacelift. Run payloads carry
  `stateVersion` and `timestamp` so that a receiver can order them. Two
  sinks need order and enforce it themselves. The Slack sink keeps
  `last_state_version` per `(channel, run)` and drops an update older than
  the message it would edit. The audit stream is ordered (below).
- **Retention.** An `events` row is deleted `events.retention` (default
  72h) after it is fanned out. A row not fanned out is never deleted, and
  `infraweave.events.oldest_unfanned` reports its age
  ([0017](0017-observability-and-health.md)).

### HTTP delivery

This contract serves named webhooks, per-stack webhooks and the audit
webhook ([19 section 6](../reference/19-events-notifications-audit.md#6-named-webhooks)).

| Aspect | Behaviour |
|---|---|
| Method | `POST`, or a policy rule's `method` |
| Body | The event's default payload, or a rule's `payload`, with its strings rendered as text templates |
| Headers, in order of precedence | signature and `X-Infraweave-*` headers; then secret headers; then rule `headers`; then `Content-Type: application/json` and `User-Agent: infraweave-webhooks/<version>` |
| Signature | With a secret: `X-Signature-256: sha256=<hex HMAC-SHA256 of the exact body bytes>` and `X-Signature: sha1=<hex HMAC-SHA1>`. With `events.webhooks.fips: true`, or when the process runs with `GODEBUG=fips140=only`, which refuses SHA-1 ([0013](0013-secrets-at-rest.md#fips-140-3)), only the SHA-256 header, as on FedRAMP. Without a secret, neither |
| Timeout | `events.webhooks.timeout`, default 10s, for connect plus response |
| Redirects | Not followed; a 3xx is a failure |
| Success | Any 2xx |
| Retryable | 5xx, 429, timeouts and network errors |
| Retries | Only with `retryOnFailure`: up to 3, after 10s, 60s and 300s with ±20% jitter. A `Retry-After` on 429 or 503 is honoured up to 300s |
| Failure | A delivery whose last attempt fails |
| Auto-disable | 10 consecutive failed deliveries set `enabled = false` and raise an `internal_error` with severity `ERROR` |
| Egress guard | `events.webhooks.blocked_cidrs`, default empty, refuses endpoints that resolve into the listed ranges, checked at connect time |

- **What is infraweave's.** Spacelift documents the signature headers, up
  to 3 retries on a 5xx with `retryOnFailure`, and auto-disable after 10
  consecutive failures. The timeout, back-off, the other retryable
  conditions, what counts as a failure, `Content-Type`, `User-Agent` and
  the `sha1=` prefix of `X-Signature` are infraweave's while capture C178
  is open.
- **Header precedence** is infraweave's while capture C177 is open: a
  credential the operator set as a secret header cannot be overridden by
  policy code.
- **Message text.** Every string a notification policy returns (a Slack
  `message`, an inbox `title` or `body`, a webhook `payload` string, a pull
  request `body`) is a text template of
  [0024](0024-expression-language.md), rendered in the delivery job against
  the event document. Its `logs(phase)` function, for `initializing`,
  `preparing`, `planning` and `applying`, returns the last 65,536 bytes of
  that phase's log; a cut log starts with `[truncated]`. The cap is
  infraweave's while capture C172 is open. A body over
  `events.webhooks.max_body` (default 5 MiB) fails the delivery without
  sending.
- **Delivery records.** Every attempt is a `WebhookDelivery`: `status` (`0`
  on a transport failure), `timestamp`, and details with request and
  response headers and bodies, plus `responseError`. Secret header values
  are stored as `[redacted]`. Bodies up to 64 KiB each live in decision
  0005's `deliveries` store under `<account>/<webhook>/<delivery>/request`
  and `.../response`, and the rows in `webhook_deliveries`. Both are kept
  `events.deliveries.retention`, default 7 days. `deliveries(token)` pages with 0003's cursors.
- **Secrets.** A webhook's `secret` and every secret header value are
  sealed (decision 0013) and write-only. The deprecated `secret` output
  field answers an empty string. An update with a new `secret` rotates it in
  place, and an update without one keeps the stored value. That follows the
  schema and the provider, not the docs, while capture C176 is open.

### Default payloads

| Event | Default body |
|---|---|
| `run_updated` | Spacelift's documented run payload: `account`, `state`, `stateVersion`, `timestamp` (Unix seconds), `run`, `stack`, camelCase ([19 section 6](../reference/19-events-notifications-audit.md#default-payloads-per-event)) |
| `internal_error` | `title`, `body`, `error`, `severity`, `account` |
| `module_version` | infraweave's until capture C179: `account`, `timestamp`, `module` (`id`, `name`, `terraformProvider`, `space`), `version` (`id`, `number`, `state`, `yanked`) |
| `worker_usage` | infraweave's until capture C179: the `worker_usage` input object as is |

### Inbox

- **Rows.** `notifications` holds `type`, `target`, `severity`, `title`,
  `body`, `grouping_key`, `occurrences_count`, `last_occurrence_at` and
  `dismissed`. A unique partial index on `(account, grouping_key) WHERE NOT
  dismissed` makes a repeat increment the open row. A repeat after
  dismissal opens a new row. Opening or incrementing a row emits
  `notification_created` in the same transaction.
- **Grouping key.** For an internal error, a SHA-256 of kind, target and
  error code. For a policy `inbox` rule, a SHA-256 of policy, event subject
  and the rendered title.
- **Worker usage** entries carry type `ACCOUNT`, since `NotificationType`
  has no `WORKER_USAGE` value (capture C170).
- **Visibility.** Admins only, as decision 0007 enforces. Rows expire
  `events.notifications.retention` (default 336h, Spacelift's 14 days)
  after their last occurrence.

### Slack, Teams and pull requests

- **The Slack app is the installation's own**, as on self-hosted Spacelift
  ([19 section 4](../reference/19-events-notifications-audit.md#4-slack)).
  Its credentials come from `slack.client_id`, `slack.client_secret` and
  `slack.signing_secret` (and `slack.govslack.*`), which make
  `canUpdate` false, or from `slackAppConfigSet`. `slackAppManifest`
  renders the manifest for `server.public_url`, with `/webhooks/slack`
  for commands, interactivity and events and `/slack_oauth` for the
  redirect. Inbound requests are verified with Slack's `v0` signature over
  the timestamp and body, and refused when the timestamp is more than
  five minutes old. Bot tokens are sealed.
- **Messages.** Without `thread_key`, one message per `(channel, run)` is
  posted, its `ts` kept in `slack_messages`, and edited with `chat.update`
  on every later state. With `thread_key`, the first message starts a
  thread and later ones reply in it. A custom `message` without
  `thread_key` posts anew each time. Slack's 429 `Retry-After` is honoured.
- **Slack actions.** Confirm and discard from Slack are authorized by the
  login policy's Slack input or by `ManagedUserGroupIntegration` channel
  grants (decisions 0007 and 0009).
- **Per-stack Slack subscription.** In scope: it is not superseded
  ([0021](0021-capability-scope-and-supersession.md)). `/infraweave
  subscribe` and `unsubscribe` maintain `stack_slack_subscriptions`, and a
  subscribed channel receives the default run message, threaded as above.
  What Spacelift sends is unknown (capture C175).
- **Teams and Datadog** need no code: they are a named webhook plus a
  policy.
- **Pull request comments** go through decision 0012's provider
  abstraction. `deduplication_key` maps to a stored comment ID per pull
  request, so the comment is edited rather than duplicated. A body over the
  provider's limit is cut with a marker when `truncate_body` is true, and
  otherwise fails the delivery (capture C174).

### Audit trail

- **Recording.** Every mutating operation records an `AuditTrailEntry`
  through `audit.Record(ctx, entry)`, in the transaction that `tx.Do` put
  on `ctx`, so an entry exists exactly when the change does. Failed logins
  are recorded by the authentication path (decision 0006).
- **Actions.** Each mutating operation in the served spec names its action
  in `x-infraweave-audit-action` (`<resource>.<verb>`, snake case, for
  example `stack.create`), with its `AuditTrailResourceType` and event type.
  The full Spacelift vocabulary is unknown (capture C180), so infraweave's
  list is derived from the operations and kept as code constants.
- **Fields.** `actor` (`username`, and `run` when an administrative run
  acted, with username `infraweave`), `action`, `eventType`,
  `affectedResource` and `relatedResource` with typed metadata, `body`,
  `remoteIP`, `identityUlid`, `ownerLogin` and `createdAt`.
- **Sanitizing.** Every field that the spec marks `writeOnly` is replaced in
  `body` and in webhook `data` by `{"SecretSHA": "<hex HMAC-SHA256>"}`,
  keyed by `secrets.DeriveKey("audit-secret-sha")` (decision 0013).
- **The organization's trail.** Operations of the `Organization` and `SSO`
  tags, break-glass sign-ins ([0006](0006-authentication-and-tokens.md)) and
  the operator API's changes to an organization record entries with the
  organization's ID and no account, in `organization_audit_trail_entries`,
  keyed by organization, so `audit_trail_entries` stays account-scoped.
  Organization admins list them through
  the `Organization` tag; no account's listing or audit webhook shows them,
  because they are not that account's data.
- **Retention.** `audit.retention`, default 30 days, Spacelift's value.
  `auditTrailRetentionDays` answers it, and a daily job deletes older rows.
- **Audit webhook.** One per account. `auditTrailSetWebhook` on an enabled
  webhook first sends an `audit_trail_webhook.set` delivery and refuses the
  change with 422 unless the endpoint answers 2xx. The problem slug
  `webhook-verification-failed` is listed in decision 0022. Setting and
  deleting the webhook are themselves audited and sent.
- **Ordered shipping.** A single `audit.ship` job per account reads entries
  after its stored cursor in `(created_at, id)` order and sends them one at
  a time. It advances the cursor only after a 2xx. Failures are retried
  whatever `retryOnFailure` says, as the docs describe. The schedule is
  infraweave's while capture C181 is open: back-off from 10s doubling to
  1h, up to `audit.webhook.max_attempts` (default 20). Then the
  failed delivery is recorded, an `internal_error` is raised, and the
  shipper moves on.
- **Payload** as documented: `account`, `action`, `actor`
  (`<source>::<identity>`), `actor_roles`, `context` (`{"mutation":
  "<operationId>"}`, since operation IDs are the reference's root field
  names), `data`, `remoteIP` and `timestamp` (Unix milliseconds). With
  `includeRuns`, run state changes are sent with action `run.state_changed`
  and the run default payload as `data`. That shape is infraweave's until
  capture C182.

### Features

These features of the catalogue that
[0021](0021-capability-scope-and-supersession.md) defines gate this record's
operations and sinks; 0021's mapping table traces each to the reference
value it covers
([0021, mapping](0021-capability-scope-and-supersession.md#mapping-from-billingtierfeature)).
A caller whose entitlement lacks one sees `403 feature-disabled` or `403
feature-not-in-tier`, as decision
[0022](0022-api-errors-and-update-semantics.md) defines, and gated
background work pauses rather than failing (0021).

| Feature | Kind | Gates | While unavailable |
|---|---|---|---|
| `notification-policies` | option | The `NOTIFICATION` policy type: creating such policies, and their evaluation | Events skip policy evaluation; internal errors still reach the inbox |
| `notifications` | capability | The notification inbox operations | Inbox entries are still recorded |
| `webhooks` | capability | Named and per-stack webhook operations and deliveries | No HTTP deliveries are created |
| `audit-trail` | capability | The audit trail operations and the audit webhook | Entries are still recorded; the shipper pauses |

Each event stream belongs to the capability of its polling equivalent, and
the Flows triggers sink requires `flows-engine`, with nothing delivered to
flows while the account's organization lacks it ([0018](0018-flows-runtime.md)).

### Storage

Report 28's `webhooks` (kind `named`, `stack` or `audit`),
`webhook_secret_headers`, `webhook_deliveries`, `audit_trail_entries`
(account entries only), `notifications`, `slack_integrations` and
`stack_slack_subscriptions`
([28 section 12.9](../reference/28-data-model.md#129-events)), plus:

| Table | Key | Purpose |
|---|---|---|
| `events` | `(account, id)` | The outbox, and the replay source for streams |
| `organization_audit_trail_entries` | `(organization, id)` | The organization's trail: entries with an organization and no account |
| `event_deliveries` | `(event, sink, target, rule_index)` | Fan-out idempotency and delivery state |
| `webhook_failure_counts` | `webhook` | Consecutive failures for auto-disable |
| `slack_app_config` | `account` | The account's Slack workspace installation; the app's own credentials are configuration (`slack.*`, above) |
| `slack_messages` | `(channel, run or thread key)` | `ts` and `last_state_version` |
| `vcs_pr_comments` | `(integration, repository, pull request, deduplication key)` | Comment ID to edit; decision [0012](0012-vcs-integration-model.md) owns it |
| `audit_ship_cursors` | `account` | The audit shipper's position |

A per-stack webhook's endpoint is unique per owner. A duplicate answers 409
`already-exists` (decision 0022).

### Metrics

| Instrument | Attributes |
|---|---|
| `infraweave.events.emitted` | `kind` |
| `infraweave.events.oldest_unfanned` (seconds) | none |
| `infraweave.events.deliveries` | `sink`, `outcome` (`delivered`, `failed`, `skipped`) |
| `infraweave.events.delivery_duration` (seconds) | `sink` |
| `infraweave.events.streams` (open streams) | `stream` |
| `infraweave.events.streams_ended` | `stream`, `reason` |
| `infraweave.audit.entries` | `event_type` |
| `infraweave.audit.ship_lag` (seconds) | none |

## Rationale

- **One model for Deploy and Flows.** A run state change, a drift result, a
  policy outcome or a notification is a Flows trigger because it is an
  event like any other, fanned out in the same job. A bridge between two
  event systems would need its own delivery guarantees and its own schemas,
  and would drift from both.
- **The outbox in the change's transaction.** A delivery queued after commit
  is lost when the process dies in between. One queued before commit is
  sent for a change that rolls back. Writing the event and its fan-out job
  in the change's own transaction removes both windows. Atlantis and Digger
  show the failure this avoids
  ([27 sections 2.4](../reference/27-reference-materials.md#24-do-not-copy)
  and [5.4](../reference/27-reference-materials.md#54-do-not-copy)), and
  Terrakube the claim-and-fence intake it follows
  ([27 section 4.3](../reference/27-reference-materials.md#43-borrow)).
- **Policy input as the event document.** The notification policy contract
  is the richest description of each event it covers. Storing it once means
  policy evaluation, default payloads and Slack messages all read the same
  facts; typed schemas for the other kinds give Flows and expressions the
  same footing.
- **Server-sent events for observers.** A stream is one direction, which is
  all an observer needs; it is a plain `GET` that load balancers and
  proxies pass, browsers consume natively, and `Last-Event-ID` makes resume
  part of the protocol. A WebSocket adds an upgrade and a two-way protocol
  nobody uses here. Decision 0008 rejects server push for workers, whose
  control channel must fail over with plain polls; streams here are
  optional, and polling stays for every client that prefers it.
- **`LISTEN` and `NOTIFY` for wake-up.** Notifications arrive at commit,
  never for a rolled-back change, without a broker; the replay comes from
  the outbox the events already live in.
- **One delivery job per target.** A dead endpoint retrying for minutes
  delays only its own deliveries, and auto-disable is counted per webhook.
- **A cursor shipper for audit.** A SIEM needs entries in order and without
  gaps. A per-entry job would need per-key serial execution from the queue.
  A cursor over the table gives order and at-least-once delivery with one
  job per account.
- **Audit recorded even when `audit-trail` is unavailable.** Recording
  costs one insert, and an account whose organization gains the feature
  later gets the history.
- **Rejected: a message broker** (NATS, Kafka) for fan-out or streams. It
  adds a dependency that self-hosted Spacelift made optional in its v3
  reference architecture, where a built-in PostgreSQL queue replaces SQS
  ([24 section 9.1](../reference/24-self-hosted-reference.md#91-what-v3-changed)),
  and it cannot join the change's transaction.
- **Departure from Spacelift:** Deploy events are Flows triggers natively,
  and the event kinds include drift, policy outcomes, notifications and
  worker changes.
- **Departure from Spacelift:** run logs, run state, worker pool queues and
  Flows events are also served as server-sent-event streams.
- **Departure from Spacelift:** strings a notification policy returns are
  text templates, and `logs(phase)` replaces the `spacelift::logs::*`
  placeholders ([0024](0024-expression-language.md)).
- **Departure from Spacelift:** `X-Infraweave-Delivery` and
  `X-Infraweave-Event` are added to every HTTP delivery.
- **Departure from Spacelift:** with `retryOnFailure`, network errors,
  timeouts and 429 are retried as well as 5xx.
- **Departure from Spacelift:** audit retention, notification retention and
  delivery retention are operator settings, not fixed values.
- **Departure from Spacelift:** worker usage events require an
  operator-set limit, `events.worker_usage.limit` with
  `events.worker_usage.percentile`. Spacelift raises them only for
  negotiated contracts, and infraweave has none. Without a limit the event
  is never raised.

## Constraints

- **Transactional enqueue.** The outbox relies on decision 0011 enqueuing
  inside the caller's transaction, which its own queue does.
- **One `LISTEN` connection per replica.** Each `serve` replica holds one
  database connection outside its pool for notifications; on reconnect it
  replays from the last event ID it routed.
- **Stream resume is bounded** by `events.retention`; a client away longer
  refetches by polling.
- **Worker usage samples.** The worker usage assessment reads the
  per-minute `worker_pool_samples` that decision
  [0008](0008-run-execution-and-worker-protocol.md#pool-samples) records,
  through the workers domain's `api/` package, never the table.
- **Captures may move choices.** C170 to C184 may change the choices marked
  as infraweave's above. Each change is an amendment to this record and a
  fixture under `test/acceptance/`.
- **SaaS host names.** Spacelift's Teams and Datadog modules build run URLs
  from SaaS host names and its log placeholders, so they need their
  `policy.rego` edited before use with infraweave.
- **Out of scope.** The usage read models and CSV export
  ([19 section 11](../reference/19-events-notifications-audit.md#11-usage-data-and-usage-export))
  and the Prometheus exporter's metrics
  ([0017](0017-observability-and-health.md)) are not decided here.
  Sending usage data to Spacelift answers 403 `installation-unsupported`
  ([0021](0021-capability-scope-and-supersession.md)).

## Verification

- Unit tests in `internal/events`:
  - `TestUnitSignature` checks the HMAC-SHA256 header against GitHub's
    published vector: secret `It's a Secret to Everybody`, body
    `Hello, World!`, signature
    `sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17`.
    It also checks the FIPS mode.
  - `TestUnitHeaderPrecedence` and `TestUnitMessageTemplates` (`logs(phase)`
    truncation, escapes).
  - `TestUnitRetrySchedule` and `TestUnitAutoDisable`.
  - `TestUnitGroupingKey` and `TestUnitPolicySelection`, which covers the
    four kinds the notification policy reads.
  - `TestUnitEventSchemas` validates each kind's emitted document against
    its served schema and fails when a kind has no schema.
- `TestUnitNotificationInputConformsToContract` validates each event document
  the policy reads against the `NOTIFICATION` entry of the policy contract,
  pinned or held as a fixture as PD-14 decides.
- `TestUnitAuditActionsCoverMutations` fails when a mutating operation in
  `api/api.openapi.yaml` lacks `x-infraweave-audit-action`.
- `test/acceptance/features/events_webhooks.feature` (`@postgres`,
  `@objectstore`) runs against a chi receiver started by the test. It
  covers signed deliveries, secret headers, retries on 5xx, auto-disable
  after 10 failures with its inbox entry, the delivery log, a per-stack
  webhook on a run, and at-least-once delivery when a node is stopped
  mid-delivery.
- `test/acceptance/features/events_notifications.feature` (`@postgres`):
  inbox grouping, dismissal per item, group and all, retention, and a
  notification policy routing a run to the inbox and to a webhook with a
  message template.
- `test/acceptance/features/events_streams.feature` (`@postgres`,
  `@objectstore`): run logs and run state streamed from two `serve`
  replicas, resume with `Last-Event-ID` after a replica stops, `reset`
  past retention, a permission revoked mid-stream, the per-identity limit,
  and polling returning the same data.
- `test/acceptance/features/events_audit.feature` (`@postgres`): entries
  for mutations with sanitized secrets, search, retention, verification on
  `auditTrailSetWebhook`, and ordered shipping across a receiver that fails
  three times.
- `test/acceptance/features/events_slack.feature` (`@postgres`) runs against a
  chi server modelling Slack's Web API. It covers inbound signature checks,
  message editing per run, threading and stale-update suppression.
- `flows_deploy.feature` of [0018](0018-flows-runtime.md) covers Deploy
  events reaching Flows triggers.

## References

- [19](../reference/19-events-notifications-audit.md): sections
  [2](../reference/19-events-notifications-audit.md#2-the-notification-inbox),
  [3](../reference/19-events-notifications-audit.md#3-the-notification-policy),
  [4](../reference/19-events-notifications-audit.md#4-slack),
  [6](../reference/19-events-notifications-audit.md#6-named-webhooks),
  [7](../reference/19-events-notifications-audit.md#7-stack-and-module-webhooks),
  [8](../reference/19-events-notifications-audit.md#8-the-audit-trail)
  and [12](../reference/19-events-notifications-audit.md#12-tier-gates).
- [06 section 5.1](../reference/06-spacectl-and-clients.md#51-log-streaming):
  how Spacelift's client follows logs by polling.
- [23 section 15](../reference/23-flows.md#15-spacelift-deploy-integration):
  Spacelift's Deploy blocks in Flows.
- [26 section 3](../reference/26-scope-and-tiers.md#3-billingtierfeature): gating values.
- [27 section 4.3](../reference/27-reference-materials.md#43-borrow): Terrakube's durable intake.
- [28 section 12.9](../reference/28-data-model.md#129-events): tables.
- [30](../reference/30-open-questions.md#captures-needed): captures C169 to C184.
- WHATWG HTML, [server-sent events](https://html.spec.whatwg.org/multipage/server-sent-events.html):
  the stream format and `Last-Event-ID`.
- PostgreSQL, [`NOTIFY`](https://www.postgresql.org/docs/current/sql-notify.html):
  delivery at commit.
- GitHub, [validating webhook deliveries](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries): the signature mechanism Spacelift cites.
- Slack, [verifying requests from Slack](https://api.slack.com/authentication/verifying-requests-from-slack).
