# 0018: Flows runtime

Status: Proposed

## Context

Spacelift Flows is a visual automation product beside Spacelift Deploy:
organizations hold projects, projects hold flows, and a flow is a graph of
blocks that emit and receive events
([23 section 2](../reference/23-flows.md#2-resource-hierarchy),
[section 3](../reference/23-flows.md#3-flows-and-blocks)). Most behaviour
comes from apps: Node.js bundles written against the types-only
`@slflows/sdk/v1` contract, published to HTTPS registries and run by agents
in isolated Node.js runtimes
([section 6](../reference/23-flows.md#6-apps-installations-and-registries),
[section 7](../reference/23-flows.md#7-the-app-sdk-contract),
[section 8](../reference/23-flows.md#8-agents-and-agent-pools)). Flows is
in scope ([0021](0021-capability-scope-and-supersession.md)), at every
tier and on every platform, although Spacelift supports self-hosted Flows
on AWS EKS only
([26 section 2.3](../reference/26-scope-and-tiers.md#23-self-hosted)).

Four constraints shape the runtime:

- **Untrusted code.** App bundles and flow expressions are arbitrary
  JavaScript written by users or third parties. They must never run inside
  the `infraweave` server process.
- **Event semantics.** Events record primary and secondary parents,
  invocations derive from lineage, and an invocation can wait for days on
  a Sleep, a Show Form or a pending event
  ([23 section 4](../reference/23-flows.md#4-events-invocations-and-limits)).
  Limits, retries, re-emit, echo and 60-day retention are part of the
  contract.
- **No upstream API specification.** The Flows API is a private RPC
  surface; `flowctl` and `terraform-provider-flows`, both MIT, are the only
  evidence of its shape
  ([section 10](../reference/23-flows.md#10-the-flows-api)).
- **One binary.** infraweave ships one `infraweave` binary with roles
  (decision 0020's topology,
  [0020](0020-reference-deployments-and-packaging.md)); Spacelift's three
  Flows backend processes (server, worker, gateway) must map onto it
  ([23 section 14](../reference/23-flows.md#14-self-hosted-flows)).

## Decision

### Model and storage

infraweave implements the Flows domain of report 23 with the tables of
[28 section 12.11](../reference/28-data-model.md#1211-flows), in the
PostgreSQL database of decision 0005, with UUIDv7 identifiers. The tables
live in their own PostgreSQL schema, `flows`, with their own migration
stream (decision 0005), so Flows can later move to a database of its own
without a data migration of Deploy tables.

| Concept | infraweave contract |
|---|---|
| Organization | One per infraweave account, created with it. Flows and Deploy share the account's users, teams and service accounts (decision 0006); Flows roles live in `flows_memberships` |
| Project | As in Spacelift, including one personal sandbox per user, created on the user's first Flows request |
| Flow | `flows` row with `active`, `event_limit` and the stored definition; the YAML of [section 13](../reference/23-flows.md#13-the-flow-definition-format) is the import and export form, parsed strictly per decision 0022 |
| Block | `flow_blocks`, unique by `(flow, name)`; renames follow `alternativeLookupStrategy` |
| Lifecycle | The SDK's seven `EntityLifecycleStatus` values plus `drifted` ([section 3.5](../reference/23-flows.md#35-lifecycle-blocks-and-signals)). infraweave never sets `drifted` until capture C240 shows when Spacelift does; it is accepted as a filter value |
| Secret | `flows_secrets`, sealed per decision 0013, write-only through the API |
| Data table | `flows_data_tables` plus `flows_data_table_rows` (JSONB row, typed columns validated on write) |
| KV | `flows_kv` keyed `(installation or block, key)`, with `value` JSONB, `expires_at`, `lock_id`, `lock_expires_at` |
| Event | `flow_events`: flow, block, output, primary parent, secondary parents (array), invocation, echo flag, pending status, body |
| Invocation | `flow_invocations`: flow, root event, status (`in_progress`, `failed_in_progress`, `processed`, `failed`), terminal time |
| Timer | `flows_timers`: owner (installation or block), due time, payload, linked pending event or prompt |

Event bodies up to 256 KiB are stored inline as JSONB. A larger body goes
to object storage (decision 0005) under `flows/events/<flow>/<event>` and
the row keeps the key and a SHA-256. The 256 KiB threshold is
infraweave's; Spacelift publishes no event size limit.

### Execution engine

Flows runs on the persistent work queue of decision 0011. There is no
Temporal cluster and no separate Flows engine.

- **Delivery.** Emitting an event inserts the `flow_events` row and, in the
  same transaction, one queue item of kind `flows.deliver` per linked input,
  keyed `(event, block, input)`. The item's handler evaluates the input's
  dynamic configuration and runs the block: core blocks in Go in the
  `scheduler` role, app blocks and every expression on an agent.
- **Effects commit with the step.** Events, pending events, timers and
  internal messages that a handler produces are buffered by the runtime
  and returned with the handler's result; the server commits them in one
  transaction with the delivery's completion. A retried delivery therefore
  never emits twice. KV operations are the exception: they return values
  and take locks, so they are executed live, as Spacelift's KV locks on
  PostgreSQL rows require
  ([section 7.4](../reference/23-flows.md#74-services)).
- **Retries.** A failed delivery is recorded as a failed event on the
  block's input and routed to the block's `errorHandler`
  ([section 4.4](../reference/23-flows.md#44-errors)). Automatic retry
  applies only to infrastructure failures (agent lost, timeout before the
  handler started); app errors wait for a manual retry, as in Spacelift.
- **Waiting.** Sleep, Show Form, pending events and SDK timers are rows
  with a due time, not running code. The scheduler of decision 0011 turns a
  due timer into a `flows.timer` item. A wait of days costs one row.
- **Schedules.** Schedule blocks and app `schedules` register cron or
  interval entries with the scheduler of decision 0011, evaluated in the
  entry's `location` time zone; a disabled flow or block fires nothing.
- **Lifecycle.** `onSync` and `onDrain` run as `flows.lifecycle` items
  keyed by entity, so at most one runs per entity at a time
  ([section 7.4](../reference/23-flows.md#74-services)); a result with
  `nextScheduleDelay` re-enqueues the item with that delay.
- **Invocation status.** Every delivery state change enqueues
  `flows.invocation.status` keyed by invocation; the handler recomputes the
  four statuses of
  [section 4.2](../reference/23-flows.md#42-invocations) from the
  invocation's deliveries.
- **Re-emit and manual retry** create a new root event linked to the
  original by `history_of`, or reset the failed delivery, as
  [section 4.1](../reference/23-flows.md#41-events-and-lineage) describes.

### Limits and retention

| Limit | Default | Behaviour |
|---|---|---|
| Events per flow per calendar month | 50,000, `flows.limits.monthly_events` | `flow_event_counters` row per `(flow, month)`, incremented in the emit transaction; at the limit the delivery fails with `flow monthly event limit exceeded` and is not retried |
| Organization ceiling for a flow's monthly limit | 1,000,000, `flows.limits.monthly_ceiling` | Raised by the operator, not by support |
| Events per flow per hour | 50,000, `flows.limits.hourly_events` | At the limit the emitting delivery is re-enqueued for the start of the next hour and logs `flow event rate limit exceeded`; debug events are refused |
| Handler duration | 60 s, `flows.limits.handler_timeout` | The runtime is killed and the delivery fails with a timeout error [I] |
| Runtime memory | 512 MiB, `flows.limits.runtime_memory` | Enforced by the executor |
| Retention after an invocation ends | 60 days, `flows.retention` | A periodic queue kind deletes invocations, events and stored bodies in batches of 1,000; daily counts in `flow_daily_stats` survive deletion |

The month and hour boundaries are UTC. The values in [section
4.3](../reference/23-flows.md#43-limits) are the defaults; the handler
timeout and memory limit are infraweave's, since Spacelift publishes
neither.

### Agents, pools and the gateway

- **Agent.** `infraweave flows-agent` replaces Spacelift's agent binary. It
  reads `flows_agent.endpoint`, `flows_agent.pool_id`,
  `flows_agent.pool_token`, `flows_agent.executor` and
  `flows_agent.pool_size` (default 3), dials the server and runs runtimes.
- **Gateway in the `serve` role.** The agent connects to
  `wss://<host>/flows/agent/v1/connect` on any `serve` replica, with the pool
  ID and token in the `Authorization` header. Pool tokens are 64 random
  characters, stored as SHA-256, shown once and rotated by
  `POST /flows/v1/agent-pools/{id}/token`.
- **Work routing through the queue.** Agent tasks are queue items of kind
  `flows.agent_task` partitioned by pool. The replica holding an agent's
  socket claims items for that pool up to the agent's free capacity and
  pushes them as frames; the agent's result completes the item. A replica
  that dies loses only sockets; the claims expire and another replica's
  agents take the work. No cross-replica routing exists.
- **Frames.** JSON over WebSocket: `hello` (agent version, capacity,
  executor), `task` (task ID, kind `expression`, `handler` or `lifecycle`,
  installation, app version digest, input), `call` and `reply` (live SDK
  calls such as `kv.set` during a task), `result` (outcome, buffered
  effects, logs), `ping` every 20 s. A socket silent for 60 s is closed and
  its claims released.
- **Pools.** A default pool exists in every organization, configured by
  `flows.default_agent_pool.id` and `.token`; the reference deployments run
  agents for it (decision 0020's topology). Private pools are
  organization-scoped and exposed to listed projects, as in
  [section 8](../reference/23-flows.md#8-agents-and-agent-pools).

### Isolation

Each runtime is one Node.js process in its own sandbox, serving one app
installation at a time; expressions run in runtimes that carry no app.

| Executor | Sandbox |
|---|---|
| `docker` | Sibling container from `flows_agent.runtime_image`, with `runsc` (gVisor) when `flows_agent.docker_runtime` names it; all capabilities dropped, `no-new-privileges`, read-only root, `tmpfs` `/tmp`, non-root UID, memory and CPU limits, no published ports |
| `kubernetes` | One pod per runtime in `flows_agent.kubernetes.namespace`, with the same security context, a `NetworkPolicy` that denies ingress, and owner references to the agent pod |
| `process` | A local `node` child process for development; refused unless `flows_agent.allow_unsandboxed` is true |

- **Runtime image.** `ghcr.io/weaveplatform/infraweave-flows-runtime`,
  built on `node:24-alpine`. It carries infraweave's implementation of the
  `@slflows/sdk/v1` module, resolved for the bundle's external import
  ([section 6.4](../reference/23-flows.md#64-bundle-format)).
- **Reuse.** A runtime is reused for the same installation and app version
  digest and recycled after 1,000 tasks or 15 minutes idle; it is never
  handed to another installation.
- **Network.** Runtimes have egress, since apps call external APIs; proxy
  settings and extra CAs come from `flows_agent.http_proxy` and
  `flows_agent.additional_root_cas`. They have no route to the agent's own
  credentials: the agent passes only task input over stdin and stdout.
- **Secrets.** Only the secrets an expression or installation
  configuration names are sent with a task. At save, the server extracts
  `secrets.<KEY>` and `secrets["KEY"]` references; a computed secret name
  is a validation error. Secret values are masked in logs, errors and event
  views by the masker of decision 0013. Sensitive paths are RFC 9535
  JSONPath, applied when an event is read through the API, never to the
  stored body ([section 3.6](../reference/23-flows.md#36-secrets-and-sensitive-values)).

### App SDK contract and registries

- **SDK surface.** The runtime implements every service and handler input
  of [section 7](../reference/23-flows.md#7-the-app-sdk-contract):
  `defineApp`, app and block lifecycle callbacks, `events` (including
  pending events and echo), `kv.app` and `kv.block` with locks,
  `messaging`, `blocks.list`, `http.respond`, `lifecycle`, `timers`, `ui`
  and `getInvocationMetadata`. The SDK package declares no licence, so the
  implementation is written from the identifiers and behaviour the report
  records, never from the declaration file's text.
- **Bundles.** A version is `app.js` plus optional `ui.js`, received as
  strings (`code`, `uiCode`) or as the `bundle.tar.gz` of `flowctl version
  bundle`. Both are stored in object storage under
  `flows/apps/<app>/<version>/` by SHA-256 digest.
- **Registries.** Subscriptions fetch the manifest and app metadata JSON of
  [section 6.2](../reference/23-flows.md#62-registries) hourly through a
  periodic queue kind and verify `artifactChecksum` before a bundle is
  stored. `flows.registries.default` lists the subscriptions a new
  organization gets: Spacelift's Core and Community registry URLs by
  default, an empty list for air-gapped installs.
- **Custom apps.** Drafts, publish, project and organization scope as in
  [section 6.3](../reference/23-flows.md#63-custom-apps).
- **HTTP.** Each installation and block gets a URL under
  `/flows/apps/<installation>/` that the runtime's `http.onRequest`
  answers through `http.respond`.

### Core blocks and Deploy blocks

- Every `coreBlock` of [section 3.3](../reference/23-flows.md#33-core-block-catalogue)
  is implemented. Data Table, Form, Group and Concurrency Group have no
  behaviour page (capture C241); infraweave defines their behaviour from the
  definition reference and pins it in acceptance tests, marked as
  infraweave's own until a capture confirms it.
- `coreBlock: spacelift` blocks act on infraweave's own Deploy side in the
  same installation: Run Events subscribes to the run state events of
  decision 0015, Trigger Run and Trigger Task create runs (decision 0008),
  Confirm Run and Review Run call the run actions, Deploy Template and
  Deploy Blueprint call the template and blueprint operations. The block
  runs with the Deploy role bindings of its Flows project
  ([section 15](../reference/23-flows.md#15-spacelift-deploy-integration)).
- The GraphQL Request kind is replaced by an API Request kind that calls an
  infraweave REST operation by `operationId`; a definition naming the
  GraphQL kind fails validation with a message naming the replacement.

### Triggers and endpoints

| Trigger | Address |
|---|---|
| HTTP Endpoint block | `https://<words>.<flows.endpoints_domain>/<path>` when the operator sets a wildcard domain; otherwise `https://<host>/flows/e/<words>/<path>` |
| Flow MCP server | `/flows/mcp/<flow>`, Streamable HTTP, tools from the flow's MCP Tool blocks |
| Platform Flows MCP server | `/flows/mcp`, tools mirroring the Flows API operations plus `usage_instructions` |
| Forms | `/flows/forms/<block>` for the Consumer role |

`audience` (`world`, `organization`, `project`) is enforced before an event
exists, as [section 5](../reference/23-flows.md#5-triggers-and-endpoints)
describes. Both MCP surfaces use the MCP implementation and OAuth server
that [0019](0019-ai-provider-boundary.md) and decision 0006 define.

### API and client compatibility

- **REST.** Flows has its own OpenAPI document, `api/flows.openapi.yaml`,
  authored by infraweave under `/flows/v1/`, following decision 0003's
  pagination, idempotency and rate limits. It covers every operation that
  `flowctl` and the provider call
  ([sections 10 to 12](../reference/23-flows.md#10-the-flows-api)) and
  what the UI needs.
- **Clients are infraweave's own** (decision 0001). `infraweavectl` and
  Terraform provider cover every operation that `flowctl` and
  `terraform-provider-flows` call, through the REST document above.
  `api/reference/flows-clients.yaml` maps each RPC path of those clients to
  its REST operation, and serves as the parity list for them. The server
  does not accept the RPC paths `/cli/...` and `/provider/...`.
- **Deploy-side operations.** The six operations of the reference `Flows`
  tag (`flowsInstances`, `flowsIntegration`, `flowsProjects` and the role
  binding operations) are served in the deploy API. `flowsInstances`
  returns this installation; `flowsIntegration` returns the built-in link.
- **Access control.** The four project roles and the organization admin of
  [section 9.1](../reference/23-flows.md#91-roles) are enforced in the API
  handlers. Authorizations are tokens of decision 0006 narrowed by the
  capabilities, project access and MCP flow access of
  [section 9.3](../reference/23-flows.md#93-authorizations-and-capabilities);
  `:edit` requires its `:view` partner. The scope names `apps:viewer` and
  `apps:editor` are accepted as aliases of `apps:view` and `apps:edit`
  until capture C232 settles them.
- **Audit.** Flows audit entries are audit events of
  [0015](0015-events-notifications-webhooks-audit.md) with a `flows.`
  resource-type prefix; the per-organization OTLP Logs export of
  [section 9.4](../reference/23-flows.md#94-audit-logs) is a sink of the
  same event model.

### Configuration

| Key | Default | Meaning |
|---|---|---|
| `flows.enabled` | `true` | Serve Flows at all |
| `flows.endpoints_domain` | empty | Wildcard domain for HTTP Endpoint subdomains |
| `flows.limits.*` | as in the limits table | Event and runtime limits |
| `flows.retention` | `1440h` | Retention after an invocation ends |
| `flows.registries.default` | Core and Community URLs | Subscriptions of a new organization |
| `flows.default_agent_pool.id`, `.token` | generated at first start | The default pool |
| `flows_agent.*` | see above | `infraweave flows-agent` settings |

Flows has no `BillingTierFeature` value, so `features.disabled` cannot turn
it off; `flows.enabled: false` answers every Flows API route with `403`
`feature-disabled` and no `feature` member (decision 0021), and answers the
Deploy-side `Flows` reads with an empty integration.

## Rationale

- **The queue, not Temporal.** Flows is event-driven dataflow: each step is
  one handler call whose inputs are already stored, and every wait is a
  timer. Durable replay of workflow code buys nothing when no code stays
  in flight, and Temporal would add four services and a second schema
  beside PostgreSQL
  ([27 section 10.1](../reference/27-reference-materials.md#101-temporal)).
  Spacelift's own self-hosted Flows runs on PostgreSQL with no queue or
  broker ([23 section 14.3](../reference/23-flows.md#143-external-dependencies)).
- **Rejected: a dedicated Flows engine.** A second scheduler, retry policy
  and lease model would duplicate decision 0011 and split the operational
  story.
- **Effects buffered per step.** Committing emitted events with the
  delivery's completion gives effectively-once emission on an at-least-once
  queue, which is what lineage and event counting need. Spacelift already
  buffers internal messages the same way
  ([section 7.4](../reference/23-flows.md#74-services)).
- **Expressions on agents, not in-process.** An in-process engine such as
  goja would put user JavaScript inside the server, and its language level
  differs from Node 22, so an expression could behave differently from the
  same code in an app. Running both in the Node runtime gives one
  semantics and one sandbox, as Spacelift's agents do
  ([section 8](../reference/23-flows.md#8-agents-and-agent-pools)).
- **Gateway as a route, queue as the router.** Agents holding sockets to
  arbitrary replicas would need cross-replica routing; claiming per pool
  from the queue makes any replica able to serve any agent.
- **Rejected: an RPC adapter for `flowctl` and the Flows provider.**
  Accepting `/cli/...` and `/provider/...` with Spacelift's `{"data",
  "error"}` envelope would let those two MIT clients run unchanged, at the
  cost of a mapping table. It would also be a non-REST surface, which
  decision 0001 rules out for every family; the mapping file keeps the
  clients' operations as the parity list without serving their shape.
- **Departure from Spacelift:** Flows is one organization per infraweave
  account with shared identity, rather than a separately linked Flows
  organization with its own login; the Deploy link is implicit.
- **Departure from Spacelift:** HTTP Endpoint addresses fall back to paths
  when no wildcard domain is configured, and MCP surfaces are paths
  (`/flows/mcp`) rather than the `mcp.` and `oauth.` subdomains.
- **Departure from Spacelift:** the GraphQL Request Deploy block becomes an
  API Request block, because infraweave serves no GraphQL (decision 0001).
- **Departure from Spacelift:** self-hosted Flows runs anywhere infraweave
  runs, not only on EKS, and agents may run in Kubernetes or Docker.
- **Departure from Spacelift:** the Flows API is REST under `/flows/v1/`,
  so `flowctl` and `terraform-provider-flows` do not work against
  infraweave; its own CLI and provider replace them.

## Constraints

- **A JavaScript component.** The SDK runtime inside the runtime image is
  JavaScript, outside the Go coverage gate and golangci-lint. It carries
  its own tests (`node --test`) run in CI, and the Go acceptance features
  exercise it end to end.
- **Unverified behaviour.** Core blocks without behaviour pages (C241),
  the `drifted` state (C240), Deploy block `kind` values (C239), the
  Flows MCP server's tool list (C231) and scope names (C232) follow
  infraweave's own definitions until captures exist
  ([30, Flows](../reference/30-open-questions.md#flows-report-23)).
- **Apps that call Spacelift.** Registry apps such as `flows-app-spacelift`
  call Spacelift's GraphQL API and do not work against infraweave; the
  native Deploy blocks cover their use.
- **Spacelift's registries are a network dependency** of the default
  subscriptions. An install that cannot reach them sets
  `flows.registries.default` to a mirror or to nothing.
- Code placement (`internal/flows`, `internal/flows/agent`,
  `runtime/flows/`) is indicative; decision 0004 owns the layout.

## Verification

- `TestUnitFlowsDelivery`, `TestUnitFlowsInvocationStatus`,
  `TestUnitFlowsLimits`, `TestUnitFlowsDefinitionParse`: delivery effects,
  the four statuses, counter boundaries in UTC, and strict YAML import and
  export round-trips.
- `test/acceptance/features/flows_engine.feature` (`@postgres`,
  `@flows-runtime`): fan-out and merge, echo, pending events, Sleep across
  a server restart, manual retry and re-emit, monthly and hourly limits,
  retention deletion.
- `flows_apps.feature` (`@postgres`, `@objectstore`, `@flows-runtime`):
  a fixture registry served by a chi test server, checksum refusal, a
  custom app published from a bundle, lifecycle `onSync` serialisation,
  KV locks, timers, block HTTP.
- `flows_agents.feature` (`@postgres`, `@flows-runtime`): two `serve`
  replicas, an agent killed mid-task and its work taken over, pool token
  rotation, an installation's runtime never reused for another.
- `flows_clients.feature` (`@postgres`, `@terraform`, `@flows-runtime`):
  `infraweavectl` creates and publishes a version from a `flowctl`-format
  bundle and exports a flow; infraweave's provider applies, plans with no
  drift and imports a flow.
- `TestUnitFlowsClientParity` fails when an RPC path in
  `api/reference/flows-clients.yaml` names no operation of
  `api/flows.openapi.yaml`.
- `flows_deploy.feature` (`@postgres`, `@tofu`, `@flows-runtime`): a Run
  Events block reacts to a real run, and Trigger Run starts one.

## References

- [23: Spacelift Flows](../reference/23-flows.md), in particular sections
  [4](../reference/23-flows.md#4-events-invocations-and-limits),
  [7](../reference/23-flows.md#7-the-app-sdk-contract),
  [8](../reference/23-flows.md#8-agents-and-agent-pools) and
  [10](../reference/23-flows.md#10-the-flows-api).
- [27 section 10](../reference/27-reference-materials.md#10-work-queue-and-workflow-engines):
  Temporal and River.
- [28 section 11](../reference/28-data-model.md#11-flows) and
  [section 12.11](../reference/28-data-model.md#1211-flows): the Flows data
  model.
- [RFC 9535](https://www.rfc-editor.org/rfc/rfc9535): JSONPath, for
  sensitive paths.
- Related records: [0015](0015-events-notifications-webhooks-audit.md),
  [0019](0019-ai-provider-boundary.md),
  [0020](0020-reference-deployments-and-packaging.md),
  [0021](0021-capability-scope-and-supersession.md); decisions 0005, 0006,
  0008, 0011, 0013 and 0022.
