# 0015: Events: notifications, webhooks and audit

Status: Proposed

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

The research leaves the delivery contract largely undocumented: the retry
back-off, timeouts, retryable conditions, failure counting, header
precedence, default payloads for module and worker-usage events, and the
audit stream's retries (captures C170 to C184,
[30](../reference/30-open-questions.md#events-notifications-and-audit-report-19)).
infraweave has to fix each one, and say where it is choosing rather than
copying.

The sources share one shape. Run state changes, module version changes,
internal errors and worker usage feed a policy-driven router with four
sinks, beside the per-stack webhooks and the audit stream
([19, "What this means for infraweave"](../reference/19-events-notifications-audit.md#what-this-means-for-infraweave)).

## Decision

### One event model

Every outbound effect starts as an **event**, written by the service that
makes the change, never by an HTTP handler, so every entry point produces
the same stream. Code lives in `internal/events` and `internal/audit`;
decision 0004 owns their placement.

| Kind | Raised when | Subject |
|---|---|---|
| `run_updated` | A run changes state (decision 0008's state machine) | run |
| `module_version` | A module version changes state ([0014](0014-module-and-provider-registry.md)) | version |
| `internal_error` | infraweave fails while talking to another system: VCS, cloud, Slack, a webhook | the component |
| `worker_usage` | The daily worker usage assessment crosses 60% of the configured limit or burst budget | account |

- **Outbox.** `events.Emit(ctx, tx, ev)` inserts a row into `events` in the
  transaction of the change, and in the same transaction enqueues an
  `events.fanout` job on decision 0011's queue, keyed by the event's ULID.
  An event therefore exists exactly when its change commits.
- **The event row** holds `account`, `id` (ULID), `kind`, `space`,
  `subject_kind`, `subject_id`, `sequence` (the run's `stateVersion`, or a
  per-subject counter), `occurred_at` (nanoseconds) and `document`
  (JSONB). The document is the event's notification policy input object,
  snake_case with nanosecond timestamps, built once at emit time
  ([19 section 3](../reference/19-events-notifications-audit.md#3-the-notification-policy)).
  Where the docs page and the policy contract differ, the contract wins, as
  report 19 rules: `policy_receipts` sits at `run_updated`, and the run type
  and change action enums are the contract's.
- **Fan-out.** The `events.fanout` job computes every delivery the event
  causes and enqueues one `events.deliver` job per target, then marks the
  event fanned out. Each delivery job carries the idempotency key
  `(event, sink, target, rule index)`, unique in `event_deliveries`, so a
  fan-out that runs twice creates no second delivery. One slow endpoint
  never holds up another.

| Sink | Targets for one event |
|---|---|
| Inbox | `internal_error` always; any `inbox` rule result |
| Notification policy | Each `NOTIFICATION` policy selected by the event's space, evaluated with decision 0009's engine; each `slack`, `webhook` and `pull_request` result becomes a target |
| Per-stack webhooks | `run_updated` on a stack or module: every enabled `WebhooksIntegration` on it |
| Per-stack Slack subscription | `run_updated`: every channel subscribed to the stack or module |
| Audit webhook | Audit entries, and `run_updated` when `includeRuns` is set |
| Commit checks | `run_updated` on a stack or module with a VCS integration: one `vcs.report_check` job per check ([0012](0012-vcs-integration-model.md)) |

- **Policy selection** follows report 19 section 3. A run uses its space
  and the spaces it inherits from. An internal error uses the component's
  space, or the root, checking stack, worker pool, AWS integration and
  policy in that order. A module version uses the module's space, and
  worker usage uses the root space. The last two are inferences
  (capture C171).

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
| Body | The event's default payload, or a rule's `payload`, after log placeholder expansion |
| Headers, in order of precedence | signature and `X-Infraweave-*` headers; then secret headers; then rule `headers`; then `Content-Type: application/json` and `User-Agent: infraweave-webhooks/<version>` |
| Signature | With a secret: `X-Signature-256: sha256=<hex HMAC-SHA256 of the exact body bytes>` and `X-Signature: sha1=<hex HMAC-SHA1>`. With `events.webhooks.fips: true`, only the SHA-256 header, as on FedRAMP. Without a secret, neither |
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
- **Log placeholders.** `spacelift::logs::initializing`, `::preparing`,
  `::planning` and `::applying`, in JSON values only, are replaced with the
  last 65,536 bytes of that phase's log. A cut log starts with
  `[truncated]`. The cap is infraweave's while capture C172 is open.
  The same expansion applies to a pull request rule's `body`. A body over `events.webhooks.max_body` (default 5 MiB)
  fails the delivery without sending.
- **Delivery records.** Every attempt is a `WebhookDelivery`: `status` (`0`
  on a transport failure), `timestamp`, and details with request and
  response headers and bodies, plus `responseError`. Secret header values
  are stored as `[redacted]`. Bodies up to 64 KiB each live in object
  storage under `deliveries/<account>/<webhook>/<delivery>/`, and the rows
  in `webhook_deliveries`. Both are kept `events.deliveries.retention`,
  default 7 days. `deliveries(token)` pages with 0003's cursors.
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
  dismissal opens a new row.
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
  ([0021](0021-capability-scope-and-supersession.md)). `/spacelift
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
  through `audit.Record(ctx, tx, entry)`, in the transaction of the
  change, so an entry exists exactly when the change does. Failed logins
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
- **Sanitising.** Every field that the spec marks `writeOnly` is replaced in
  `body` and in webhook `data` by `{"SecretSHA": "<hex HMAC-SHA256>"}`,
  keyed by `secrets.DeriveKey("audit-secret-sha")` (decision 0013).
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

### Feature gates

| `BillingTierFeature` | Gates | When disabled |
|---|---|---|
| `NOTIFICATION_POLICIES` | Creating `NOTIFICATION` policies, and their evaluation | 403 `feature-disabled`; events skip policy evaluation; internal errors still reach the inbox |
| `RUN_STATE_CHANGE_WEBHOOKS` | Named and per-stack webhook operations and deliveries | 403 `feature-disabled`; no HTTP deliveries are created |
| `AUDITING` | The Audit Trail operations and the audit webhook | 403 `feature-disabled`; entries are still recorded |

`AUDIT_WEBHOOKS` is superseded by `AUDITING` and is never reported.

### Storage

Report 28's `webhooks` (kind `named`, `stack` or `audit`),
`webhook_secret_headers`, `webhook_deliveries`, `audit_trail_entries`,
`notifications`, `slack_integrations` and `stack_slack_subscriptions`
([28 section 12.9](../reference/28-data-model.md#129-events)), plus:

| Table | Key | Purpose |
|---|---|---|
| `events` | `(account, id)` | The outbox |
| `event_deliveries` | `(event, sink, target, rule_index)` | Fan-out idempotency and delivery state |
| `webhook_failure_counts` | `webhook` | Consecutive failures for auto-disable |
| `slack_app_config` | `account` | The Slack app credentials, sealed |
| `slack_messages` | `(channel, run or thread key)` | `ts` and `last_state_version` |
| `pull_request_comments` | `(integration, repository, pull request, deduplication key)` | Comment ID to edit |
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
| `infraweave.audit.entries` | `event_type` |
| `infraweave.audit.ship_lag` (seconds) | none |

## Rationale

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
  is the richest description of each event. Storing it once means policy
  evaluation, default payloads and Slack messages all read the same facts.
- **One delivery job per target.** A dead endpoint retrying for minutes
  delays only its own deliveries, and auto-disable is counted per webhook.
- **A cursor shipper for audit.** A SIEM needs entries in order and without
  gaps. A per-entry job would need per-key serial execution from the queue.
  A cursor over the table gives order and at-least-once delivery with one
  job per account.
- **Audit recorded even when `AUDITING` is off.** Recording costs one insert,
  and an operator who turns the API on later gets the history.
- **Rejected: a message broker** (NATS, Kafka) for fan-out. It adds a
  dependency that self-hosted Spacelift made optional in its v3 reference
  architecture, where a built-in PostgreSQL queue replaces SQS
  ([24 section 9.1](../reference/24-self-hosted-reference.md#91-what-v3-changed)),
  and it cannot join the change's transaction.
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
  inside the caller's transaction. If 0011 chooses an engine that cannot do
  that, `events.fanout` becomes a relay that polls `events` for rows not yet
  fanned out, and adds the relay's poll interval to delivery latency.
- **Worker usage samples.** The worker usage assessment reads the
  per-minute worker samples that decision 0008 records.
- **Captures may move choices.** C170 to C184 may change the choices marked
  as infraweave's above. Each change is an amendment to this record and a
  fixture under `test/acceptance/`.
- **SaaS host names.** Spacelift's Teams and Datadog modules build run URLs
  from SaaS host names, so they need their `policy.rego` edited before use
  with infraweave.
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
  - `TestUnitHeaderPrecedence` and `TestUnitLogPlaceholders` (keys left
    alone, truncation).
  - `TestUnitRetrySchedule` and `TestUnitAutoDisable`.
  - `TestUnitGroupingKey` and `TestUnitPolicySelection`, which covers the
    four event kinds.
- `TestUnitNotificationInputConformsToContract` validates each event document
  against the `NOTIFICATION` entry of the policy contract, pinned or held
  as a fixture as P14 decides.
- `TestUnitAuditActionsCoverMutations` fails when a mutating operation in
  `api/deploy.openapi.yaml` lacks `x-infraweave-audit-action`.
- `test/acceptance/features/events_webhooks.feature` (`@postgres`,
  `@objectstore`) runs against a chi receiver started by the test. It
  covers signed deliveries, secret headers, retries on 5xx, auto-disable
  after 10 failures with its inbox entry, the delivery log, a per-stack
  webhook on a run, and at-least-once delivery when a node is stopped
  mid-delivery.
- `test/acceptance/features/events_notifications.feature` (`@postgres`):
  inbox grouping, dismissal per item, group and all, retention, and a
  notification policy routing a run to the inbox and to a webhook.
- `test/acceptance/features/events_audit.feature` (`@postgres`): entries
  for mutations with sanitised secrets, search, retention, verification on
  `auditTrailSetWebhook`, and ordered shipping across a receiver that fails
  three times.
- `test/acceptance/features/events_slack.feature` (`@postgres`) runs against a
  chi server modelling Slack's Web API. It covers inbound signature checks,
  message editing per run, threading and stale-update suppression.

## References

- [19](../reference/19-events-notifications-audit.md): sections
  [2](../reference/19-events-notifications-audit.md#2-the-notification-inbox),
  [3](../reference/19-events-notifications-audit.md#3-the-notification-policy),
  [4](../reference/19-events-notifications-audit.md#4-slack),
  [6](../reference/19-events-notifications-audit.md#6-named-webhooks),
  [7](../reference/19-events-notifications-audit.md#7-stack-and-module-webhooks),
  [8](../reference/19-events-notifications-audit.md#8-the-audit-trail)
  and [12](../reference/19-events-notifications-audit.md#12-tier-gates).
- [26 section 3](../reference/26-scope-and-tiers.md#3-billingtierfeature): gating values.
- [27 section 4.3](../reference/27-reference-materials.md#43-borrow): Terrakube's durable intake.
- [28 section 12.9](../reference/28-data-model.md#129-events): tables.
- [30](../reference/30-open-questions.md#events-notifications-and-audit-report-19): captures C169 to C184.
- GitHub, [validating webhook deliveries](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries): the signature mechanism Spacelift cites.
- Slack, [verifying requests from Slack](https://api.slack.com/authentication/verifying-requests-from-slack).
