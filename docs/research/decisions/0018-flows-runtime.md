# 0018: Flows engine, apps and agents

Status: Accepted

## Context

Spacelift Flows is a visual automation product beside Spacelift Deploy:
organizations hold projects, projects hold flows, and a flow is a graph of
blocks that emit and receive events
([23 section 2](../reference/23-flows.md#2-resource-hierarchy),
[section 3](../reference/23-flows.md#3-flows-and-blocks)). Most behaviour
comes from apps: Node.js bundles written against the types-only
`@slflows/sdk/v1` contract, published to HTTPS registries and run by agents
in isolated Node.js runtimes, which also evaluate every flow expression
([section 6](../reference/23-flows.md#6-apps-installations-and-registries),
[section 7](../reference/23-flows.md#7-the-app-sdk-contract),
[section 8](../reference/23-flows.md#8-agents-and-agent-pools)). Spacelift
supports self-hosted Flows on AWS EKS only
([26 section 2.3](../reference/26-scope-and-tiers.md#23-self-hosted)).

infraweave offers Flows' capabilities, not its implementation. Flows is
built in Go, on the same events as Deploy
([0015](0015-events-notifications-webhooks-audit.md)), with the user-facing
and infrastructure-facing languages kept apart
([0024](0024-expression-language.md)). Five constraints shape it:

- **Untrusted code.** App code is written by users or third parties. It must
  never run inside an `infraweave` server process.
- **Event semantics.** Events record primary and secondary parents,
  invocations derive from lineage, and an invocation can wait for days on
  a Sleep, a Show Form or a pending event
  ([23 section 4](../reference/23-flows.md#4-events-invocations-and-limits)).
  Limits, retries, re-emit, echo and 60-day retention are part of the
  capability.
- **No upstream API specification.** The Flows API is a private RPC
  surface; `flowctl` and `terraform-provider-flows`, both MIT, are the only
  evidence of its shape
  ([section 10](../reference/23-flows.md#10-the-flows-api)).
- **One binary.** infraweave ships one `infraweave` binary with process
  modes ([0004](0004-repository-architecture.md),
  [0020](0020-reference-deployments-and-packaging.md)); Spacelift's three
  Flows backend processes (server, worker, gateway) map onto them
  ([23 section 14](../reference/23-flows.md#14-self-hosted-flows)).
- **Apps outlive releases.** An app built against one version of the
  contract has to keep working while infraweave evolves, and apps need the
  libraries and network access of ordinary programs.

## Decision

### Flows is a set of features

Flows' parts are capabilities of the catalogue that
[0021](0021-capability-scope-and-supersession.md) defines:

| Feature | Gates |
|---|---|
| `flows-engine` | Projects, flows, blocks, events, invocations, secrets, named expressions, core blocks and the Deploy triggers of [0015](0015-events-notifications-webhooks-audit.md) |
| `flows-apps` | App installations, custom apps, app versions and registry subscriptions |
| `flows-agents` | Agent pools, their tokens and the agent gateway |
| `flows-data-tables` | Data tables, their rows and the Data Table blocks |
| `flows-forms` | Forms, Form Opened and Show Form blocks |
| `flows-mcp-tools` | MCP Tool blocks and the flow and platform Flows MCP servers |

Every Flows feature requires `flows-engine`. Each carries a tier, can be
granted, and is switched off with `features.disabled` like any other
feature; there is no Flows switch of its own. A caller whose entitlement
lacks one sees `403 feature-disabled` or `403 feature-not-in-tier`, and
`403 limit-reached` where a catalogue limit applies, as decision
[0022](0022-api-errors-and-update-semantics.md) defines. Its background
work (schedules, timers, lifecycle syncs, deliveries to its blocks)
pauses, as 0021 describes for any feature, and resumes when the feature is
available again; nothing is deleted.

### Model and storage

infraweave implements the Flows domain of report 23 with the tables of
[28 section 12.11](../reference/28-data-model.md#1211-flows), in the
PostgreSQL database of decision 0005, with UUIDv7 identifiers. The tables
live in their own PostgreSQL schema, `flows`, with their own migration
stream (decision 0005).

| Concept | infraweave contract |
|---|---|
| Organization | Spacelift Flows' organization is the infraweave account: Flows belongs to the account, with its own projects, apps and agents, and has no tenant of its own. Flows and Deploy share the account's users, teams and service accounts (decision 0006); Flows roles live in `flows_memberships`. Flows' organization admin is the account admin, a holder of `space-admin` on the account's top space ([0007](0007-authorization-spaces-and-roles.md#spaces)). An infraweave organization ([0007](0007-authorization-spaces-and-roles.md#tenancy)) has no Flows role and sees no account's flows |
| Project | As in Spacelift, including one personal sandbox per user, created on the user's first Flows request |
| Flow | `flows` row with `active`, `event_limit` and the stored definition; the YAML of [section 13](../reference/23-flows.md#13-the-flow-definition-format), with CEL expressions ([0024](0024-expression-language.md)), is the import and export form, parsed strictly per decision 0022 |
| Block | `flow_blocks`, unique by `(flow, name)`; renames follow `alternativeLookupStrategy` |
| Lifecycle | The seven lifecycle states of [section 3.5](../reference/23-flows.md#35-lifecycle-blocks-and-signals) plus `drifted`. infraweave never sets `drifted` until capture C240 shows when Spacelift does; it is accepted as a filter value |
| Secret | `flows_secrets`, sealed per decision 0013, write-only through the API |
| Named expression | `flows_named_expressions`, per project ([0024](0024-expression-language.md)) |
| Data table | `flows_data_tables` plus `flows_data_table_rows` (JSONB row, typed columns validated on write) |
| KV | `flows_kv` keyed `(installation or block, key)`, with `value` JSONB, `expires_at`, `lock_id`, `lock_expires_at` |
| Event | `flow_events`: the event envelope of [0015](0015-events-notifications-webhooks-audit.md) plus flow, block, output, primary parent, secondary parents (array), invocation, echo flag, pending status, and the `now` and seed of its evaluation context |
| Invocation | `flow_invocations`: flow, root event, status (`in_progress`, `failed_in_progress`, `processed`, `failed`), terminal time |
| Timer | `flows_timers`: owner (installation or block), due time, payload, linked pending event or prompt |

Event bodies up to 256 KiB are stored inline as JSONB. A larger body goes
to decision 0005's `flows-events` store under
`<account>/<flow>/<event>` and
the row keeps the key and a SHA-256. The 256 KiB threshold is
infraweave's; Spacelift publishes no event size limit.

### The engine

The Flows engine is a Go domain, `internal/flows`, on the persistent work
queue of decision 0011. Its jobs run in the `scheduler` process mode on a
logical queue of their own, `flows`, so a busy flow never delays Deploy's
fan-out. There is no Temporal cluster and no separate engine.

- **One event model.** A Flows event and a Deploy event share 0015's
  envelope (`id`, `kind`, `account`, subject, `occurred_at`, `document`)
  and its emit path. Deploy events reach flows natively: the fan-out consumer inside `flows` ([0015](0015-events-notifications-webhooks-audit.md))
  treats every enabled Deploy trigger block whose filter matches as a
  target, and in the fan-out's transaction inserts the root `flow_events`
  row, linked to the Deploy event, and its deliveries. A Flows event also
  feeds 0015's server-sent-event streams and audit.
- **Delivery.** Emitting an event inserts the `flow_events` row and, in the
  same transaction, one queue job of kind `flows.deliver` per linked input,
  keyed `(event, block, input)`. The handler evaluates the input's
  configuration ([0024](0024-expression-language.md)) and runs the block:
  a core block in Go in the same job, an app block as a task for an agent
  (below).
- **Effects commit with the step.** Events, pending events, timers and
  internal messages that a handler produces are buffered and returned with
  the handler's result; the server commits them in one transaction with
  the delivery's completion. A retried delivery therefore never emits
  twice. KV operations are the exception: they return values and take
  locks, so they run live, as Spacelift's KV locks on PostgreSQL rows
  require ([section 7.4](../reference/23-flows.md#74-services)).
- **Retries.** A failed delivery is recorded as a failed event on the
  block's input and routed to the block's `errorHandler`
  ([section 4.4](../reference/23-flows.md#44-errors)); an expression's
  `fail(message)` is such a failure. Automatic retry applies only to
  infrastructure failures (agent lost, timeout before the handler started);
  app errors wait for a manual retry, as in Spacelift.
- **Waiting.** Sleep, Show Form, pending events and SDK timers are rows
  with a due time, not running code. The scheduler of decision 0011 turns a
  due timer into a `flows.timer` job. A wait of days costs one row.
- **Schedules.** Schedule blocks and app schedules register cron or
  interval entries with the scheduler of decision 0011, evaluated in the
  entry's time zone; a disabled flow or block fires nothing.
- **Lifecycle.** App and block syncs and drains run as `flows.lifecycle`
  jobs keyed by entity, so at most one runs per entity at a time
  ([section 7.4](../reference/23-flows.md#74-services)); a result with a
  next schedule delay re-enqueues the job with that delay.
- **Invocation status.** Every delivery state change enqueues
  `flows.invocation.status` keyed by invocation; the handler recomputes the
  four statuses of [section 4.2](../reference/23-flows.md#42-invocations)
  from the invocation's deliveries.
- **Re-emit and manual retry** create a new root event linked to the
  original by `history_of`, or reset the failed delivery, as
  [section 4.1](../reference/23-flows.md#41-events-and-lineage) describes.
  A manual retry reuses the stored `now` and seed.

### Limits and retention

| Limit | Default | Behaviour |
|---|---|---|
| Events per flow per calendar month | 50,000, `flows.limits.monthly_events` | `flow_event_counters` row per `(flow, month)`, incremented in the emit transaction; at the limit the delivery fails with `flow monthly event limit exceeded` and is not retried |
| Account ceiling for a flow's monthly limit | 1,000,000, `flows.limits.monthly_ceiling` | Raised by the operator, not by support |
| Events per flow per hour | 50,000, `flows.limits.hourly_events` | At the limit the emitting delivery is re-enqueued for the start of the next hour and logs `flow event rate limit exceeded`; debug events are refused |
| App call duration | 60 s, `flows.limits.handler_timeout` | The call is cancelled through its gRPC deadline and the delivery fails with a timeout error |
| App container memory | 512 MiB, `flows.limits.app_memory` | Enforced by the executor |
| Retention after an invocation ends | 60 days, `flows.retention` | A periodic queue kind deletes invocations, events and stored bodies in batches of 1,000; daily counts in `flow_daily_stats` survive deletion |

The month and hour boundaries are UTC. The values in [section
4.3](../reference/23-flows.md#43-limits) are the defaults; the call timeout
and memory limit are infraweave's, since Spacelift publishes neither.

### Apps: the `App` gRPC contract

A Flows app is a program that serves the `App` gRPC service, defined in
versioned protobuf files under `api/proto/flows/app/v1/`, published with
every release.

- **`App` service (implemented by the app).**

  | RPC | Does |
  |---|---|
  | `Describe` | Returns the app's manifest: name, version, the contract versions it supports, installation configuration fields as JSON Schema, blocks with their configuration, input, output and signal schemas, schedules, HTTP routes and widgets |
  | `SyncApp`, `DrainApp` | Installation lifecycle; return a lifecycle state, signals and an optional next schedule delay |
  | `SyncBlock`, `DrainBlock` | Block lifecycle, the same shape |
  | `HandleEvent` | One event on one block input: the evaluated input configuration, the block's evaluated static configuration, the installation configuration, event and invocation metadata, and the upstream outputs the block declared it reads; returns buffered effects |
  | `HandleHTTP` | A request to the installation's or block's URL; returns the response and buffered effects |
  | `HandleTimer`, `HandleMessage`, `HandleSchedule` | A due timer, an internal message, a fired schedule |

- **`Host` service (implemented by the agent for the app).** The calls that
  must happen live: KV get, set, delete, list and locks; `ListBlocks`;
  `SetWidget`; invocation metadata. The agent relays each to the server
  over its connection (below); everything else is a buffered effect.
- **Inputs are evaluated.** An app receives evaluated configuration and
  event data, never an expression, and only the secrets its configuration
  resolved to ([0024](0024-expression-language.md)).
- **Versioning.** The protobuf package carries the major version
  (`infraweave.flows.app.v1`). Within a major version changes are additive
  and follow protobuf's field rules; CI runs `buf breaking` against the
  last release and blocks on any break. `Describe` lists the major versions
  an app serves; the agent picks the highest it shares with the server, and
  a new major version is served beside the old one for at least one
  further major release of infraweave.
- **Any language.** The contract is the `.proto` files; any language with a
  gRPC implementation can serve `App`.

### The Go app SDK and first-party apps

- **SDK.** `pkg/flows/appsdk` is a public Go package: the generated stubs,
  `appsdk.Serve(app)`, typed block and configuration definitions that
  produce the `Describe` schemas from Go types, helpers for KV, locks,
  timers, messages and widgets, and `appsdk/apptest`, which runs an app's
  blocks in a test without an agent or a server.
- **First-party apps** are Go programs on the SDK, built, signed and
  published like any custom app. Which integrations infraweave provides
  first-party is a catalogue question (0021), not part of this record.
- **No JavaScript.** There is no JavaScript runtime and no compatibility
  layer for Spacelift's JavaScript apps. A Spacelift bundle (`app.js`,
  `ui.js`, `bundle.tar.gz`) uploaded as an app version is refused with
  `422 validation-failed` naming the expected image form.

### Packaging and registries

- **An app version is an OCI image** (multi-architecture, `linux/amd64` and
  `linux/arm64`) whose entrypoint serves `App`, plus a manifest: the image
  digest, the contract versions and the `Describe` result captured at
  publish. infraweave stores the manifest, never the image; agents pull
  the image by digest from its registry.
- **Registries.** A registry is an HTTPS document listing apps and their
  versions with image references and digests. Subscriptions fetch it
  hourly through a periodic queue kind, and an agent refuses an image whose
  digest differs from the manifest's. `flows.registries.default` lists the
  subscriptions a new account gets: infraweave's first-party registry
  by default, an empty list for air-gapped installs, which mirror images
  into their own OCI registry.
- **Custom apps.** Drafts, publish, and project and account scope, the
  scopes Spacelift calls project and organization, as in
  [section 6.3](../reference/23-flows.md#63-custom-apps), with an image
  reference in place of a bundle: `infraweavectl flows app version publish
  --image <reference>`.

### Agents and isolation

- **Agent.** `infraweave flows-agent` is a Go process mode. It reads
  `flows_agent.endpoint`, `flows_agent.pool_id`, `flows_agent.pool_token`,
  `flows_agent.executor` and `flows_agent.pool_size` (default 3), dials the
  server and runs app containers.
- **The default agent pool** (`flows.default_agent_pool`) is one pool the
  operator runs for every organization's accounts, as decision
  [0008](0008-run-execution-and-worker-protocol.md)'s public worker pool is
  for runs. Account isolation holds on it in three ways:
  - an app container serves one app installation, which belongs to one
    account, so no container is shared between accounts;
  - every `Host` call is authorized against the account of the task that
    made it, so KV, secrets, timers and messages never cross accounts;
  - the claim takes tasks round-robin across accounts, so one account's
    backlog cannot delay another's.

  An account that needs its own network reach or isolation runs a pool of
  its own.
- **Gateway in the `serve` process mode.** The agent connects to
  `wss://<host>/flows/agent/v1/connect` on any `serve` replica, with the pool
  ID and token in the `Authorization` header. Pool tokens are 64 random
  characters, stored as SHA-256, shown once and rotated by
  `POST /flows/v1/agent-pools/{id}/token`.
- **Work routing through the queue.** App tasks are queue jobs of kind
  `flows.agent_task` partitioned by pool. The replica holding an agent's
  socket claims jobs for that pool up to the agent's free capacity and
  pushes them as frames; the agent's result completes the job. A replica
  that dies loses only sockets; the claims expire and another replica's
  agents take the work.
- **Frames.** JSON over WebSocket: `hello` (agent version, capacity,
  executor, contract versions), `task` (task ID, RPC, installation, app
  version digest, request), `call` and `reply` (relayed `Host` calls),
  `result` (response, buffered effects, logs), `ping` every 20 s. A task's
  request and response are the protobuf messages in their JSON encoding,
  so the agent forwards them to the app without a second schema. A socket
  silent for 60 s is closed and its claims released.
- **gRPC stays agent-local.** The agent speaks gRPC only to the app
  containers it started, never across the network to the server. The
  worker long-poll of [0008](0008-run-execution-and-worker-protocol.md) is
  unaffected.
- **Each app its own process in a container**, serving one installation and
  app version digest:

  | Executor | Container and channel |
  |---|---|
  | `docker` | Sibling container from the app image, with `runsc` (gVisor) when `flows_agent.docker_runtime` names it; all capabilities dropped, `no-new-privileges`, read-only root, `tmpfs` `/tmp`, non-root UID, memory and CPU limits, no published ports. gRPC over a Unix socket on a volume shared only with the agent |
  | `kubernetes` | One pod per app in `flows_agent.kubernetes.namespace`, with the same security context and owner references to the agent pod. gRPC over the pod network with mutual TLS from a per-pod certificate the agent issues, and a `NetworkPolicy` that admits ingress from the agent pod only |
  | `process` | A local child process for development, gRPC over a Unix socket; refused unless `flows_agent.allow_unsandboxed` is true |

- **Reuse.** A container is reused for the same installation and app
  version digest and recycled after 15 minutes idle; it is never handed to
  another installation.
- **Network.** App containers have egress, since apps call external APIs;
  proxy settings and extra CAs come from `flows_agent.http_proxy` and
  `flows_agent.additional_root_cas`. They have no route to the agent's
  credentials or to the server.
- **Secrets.** A task carries only the secret values its evaluated
  configuration contains. Secret values are masked in logs, errors and
  event views by the masker of decision 0013. Sensitive paths are RFC 9535
  JSONPath, applied when an event is read through the API, never to the
  stored body ([section 3.6](../reference/23-flows.md#36-secrets-and-sensitive-values)).

### Widgets

Apps contribute declarative widgets only. `Describe` declares each widget's
type and placement; `SetWidget` sets its data, validated against the type's
schema. The types are text, Markdown, key and value list, table, status,
link and chart (line and bar), rendered by the console (decision 0023).
No app ships browser code: there is no `ui.js`
and no `uiCode`.

### Core blocks and Deploy blocks

- Every `coreBlock` of [section 3.3](../reference/23-flows.md#33-core-block-catalogue)
  is implemented in Go. Data Table, Form, Group and Concurrency Group have
  no behaviour page (capture C241); infraweave defines their behaviour from
  the definition reference and pins it in acceptance tests, marked as
  infraweave's own until a capture confirms it.
- Transform takes one expression or the steps form of
  [0024](0024-expression-language.md); there is no Script block.
- `coreBlock: deploy` blocks act on infraweave's Deploy side in the same
  installation. Trigger blocks subscribe to 0015's event kinds (run state
  changes, drift, policy outcomes, notifications, module versions); Trigger
  Run and Trigger Task create runs (decision 0008), Confirm Run and Review
  Run call the run actions, Deploy Template and Deploy Blueprint call the
  template and blueprint operations. A block runs with the Deploy role
  bindings of its Flows project
  ([section 15](../reference/23-flows.md#15-spacelift-deploy-integration)).
- The GraphQL Request kind is replaced by an API Request kind that calls an
  infraweave REST operation by `operationId`.

### Triggers and endpoints

| Trigger | Address |
|---|---|
| HTTP Endpoint block | `https://<words>.<flows.endpoints_domain>/<path>` when the operator sets a wildcard domain; otherwise `https://<host>/flows/e/<words>/<path>` |
| Flow MCP server | `/flows/mcp/<flow>`, Streamable HTTP, tools from the flow's MCP Tool blocks |
| Platform Flows MCP server | `/flows/mcp`, tools mirroring the Flows API operations plus `usage_instructions` |
| Forms | `/flows/forms/<block>` for the Consumer role |

`audience` (`world`, `account`, `project`) is enforced before an event
exists, as [section 5](../reference/23-flows.md#5-triggers-and-endpoints)
describes for Spacelift's `world`, `organization` and `project`; `account`
admits any member the account admits, and the importer maps
`organization` to `account` (0001's rename table). Both MCP surfaces use the MCP implementation and OAuth server
that [0019](0019-ai-provider-boundary.md) and decision 0006 define.

### API and clients

- **REST.** Flows has its own OpenAPI document, `api/flows.openapi.yaml`,
  authored by infraweave under `/flows/v1/`, following decision 0003's
  pagination, idempotency and rate limits. It covers every capability that
  `flowctl` and the Flows provider reach
  ([sections 10 to 12](../reference/23-flows.md#10-the-flows-api)), the
  expression endpoints of 0024, and the event stream of 0015.
- **Clients are infraweave's own** ([0001](0001-rest-first-spacelift-surface.md)).
  `infraweavectl` and the provider cover Flows through the REST document
  above. `api/reference/flows-clients.yaml` maps each RPC path of
  `flowctl` and `terraform-provider-flows` to its REST operation, and serves
  as their parity list. The server does not accept the RPC paths
  `/cli/...` and `/provider/...`.
- **Deploy-side operations.** The six operations of the reference `Flows`
  tag (`flowsInstances`, `flowsIntegration`, `flowsProjects` and the role
  binding operations) are served in the deploy API. `flowsInstances`
  returns this installation; `flowsIntegration` returns the built-in link,
  whose `accountID` and `accountName` name the caller's account where
  Spacelift's `organizationID` and `organizationName` name the Flows
  organization (0001's rename table).
- **Access control.** The four project roles of
  [section 9.1](../reference/23-flows.md#91-roles), and the account admin
  in place of its organization admin, are enforced in the API
  handlers. Authorizations are tokens of decision 0006 narrowed by the
  capabilities, project access and MCP flow access of
  [section 9.3](../reference/23-flows.md#93-authorizations-and-capabilities);
  `:edit` requires its `:view` partner. The scope names `apps:viewer` and
  `apps:editor` are accepted as aliases of `apps:view` and `apps:edit`
  until capture C232 settles them.
- **Audit.** Flows audit entries are audit events of
  [0015](0015-events-notifications-webhooks-audit.md) with a `flows.`
  resource-type prefix; the OTLP Logs export of
  [section 9.4](../reference/23-flows.md#94-audit-logs), per Flows
  organization on Spacelift, is per account here and a sink of the same
  event model.

### Configuration

| Key | Default | Meaning |
|---|---|---|
| `flows.endpoints_domain` | empty | Wildcard domain for HTTP Endpoint subdomains |
| `flows.limits.*` | as in the limits table | Event and app limits |
| `flows.retention` | `1440h` | Retention after an invocation ends |
| `flows.registries.default` | infraweave's first-party registry | Subscriptions of a new account |
| `flows.default_agent_pool.id`, `.token` | generated at first start | The default pool |
| `flows_agent.*` | see above | `infraweave flows-agent` settings |

## Rationale

- **Flows in Go, on Deploy's events.** Building the engine as a Go domain on
  the same queue and event model as Deploy means a run state change, a
  drift result or a policy outcome is a Flows trigger without a bridge, and
  one toolchain, gate and operational story covers both.
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
- **Expressions on the server, apps on agents.** Expressions are CEL,
  bounded and deterministic, so they evaluate in `scheduler` without a
  sandbox and agents never see them ([0024](0024-expression-language.md)).
  App code is infrastructure code: arbitrary, networked and untrusted, so
  it runs in containers on agents.
- **gRPC over WebAssembly.** A protobuf contract evolves safely: field
  rules, `buf breaking` and negotiated major versions let old apps keep
  working, as Terraform's provider protocol does with its negotiated
  protocol versions over gRPC. Apps also need unrestricted networking,
  TLS, cloud SDKs and database drivers. A WebAssembly host (wazero, WASI
  preview 1) is single-threaded and needs a host function for every
  capability, sockets included, so each library an app wants becomes
  infraweave's work. A WebAssembly host can be added later as a second
  host of the same `App` contract.
- **gRPC over Node.js.** Implementing Spacelift's SDK in Node would mean a
  JavaScript component outside the Go gate, written from a licence-less
  type declaration file, and a runtime image to patch; it would also keep
  expressions on agents. A gRPC contract lets app authors use any
  language, Go first.
- **Containers per app.** One process per installation and digest isolates
  apps from each other and from the agent's credentials, and an image
  carries whatever the app needs without the agent knowing.
- **Declarative widgets.** Apps cannot run code in the browser, so the
  console's content security policy and its authorization model stay
  whole; no public Spacelift app ships `ui.js`
  ([23 section 6.4](../reference/23-flows.md#64-bundle-format)).
- **Gateway as a route, queue as the router.** Agents holding sockets to
  arbitrary replicas would need cross-replica routing; claiming per pool
  from the queue makes any replica able to serve any agent.
- **Rejected: an RPC adapter for `flowctl` and the Flows provider.**
  Accepting `/cli/...` and `/provider/...` with Spacelift's `{"data",
  "error"}` envelope would be a non-REST surface, which decision 0001 rules
  out; the mapping file keeps the clients' operations as the parity list
  without serving their shape.
- **Departure from Spacelift:** apps are OCI images serving a gRPC contract,
  not JavaScript bundles; Spacelift's apps and registries do not work
  with infraweave.
- **Departure from Spacelift:** apps contribute declarative widgets only,
  with no `ui.js`.
- **Departure from Spacelift:** expressions are CEL evaluated on the server,
  not JavaScript on agents ([0024](0024-expression-language.md)).
- **Departure from Spacelift:** Flows belongs to the infraweave account and
  shares its identity, rather than a separately linked Flows organization
  with its own login; the Deploy link is implicit, the organization admin
  is the account admin, and `audience: organization` is `account`.
- **Departure from Spacelift:** HTTP Endpoint addresses fall back to paths
  when no wildcard domain is configured, and MCP surfaces are paths
  (`/flows/mcp`) rather than the `mcp.` and `oauth.` subdomains.
- **Departure from Spacelift:** Deploy blocks are `coreBlock: deploy`, and
  the GraphQL Request block becomes an API Request block, because
  infraweave serves no GraphQL (decision 0001).
- **Departure from Spacelift:** self-hosted Flows runs anywhere infraweave
  runs, not only on EKS, and agents may run in Kubernetes or Docker.
- **Departure from Spacelift:** the Flows API is REST under `/flows/v1/`,
  so `flowctl` and `terraform-provider-flows` do not work against
  infraweave; its own CLI and provider replace them.

## Constraints

- **A container runtime on agent hosts.** An agent needs Docker or a
  Kubernetes namespace it can create pods in; `process` is for development
  only.
- **The `flows` queue** is a logical queue of decision 0011's queue table,
  with its own worker count per `scheduler` replica.
- **Flow definitions from Spacelift** import only after their expressions
  are rewritten in CEL and their app blocks point at infraweave apps.
- **Unverified behaviour.** Core blocks without behaviour pages (C241),
  the `drifted` state (C240), Deploy block `kind` values (C239), the
  Flows MCP server's tool list (C231) and scope names (C232) follow
  infraweave's own definitions until captures exist
  ([30, Flows](../reference/30-open-questions.md#captures-needed)).
- Code placement (`internal/flows`, `internal/agent/flowsagent`,
  `pkg/flows/appsdk`, `api/proto/flows/app/v1/`) is indicative; decision
  0004 owns the layout.

## Verification

- `TestUnitFlowsDelivery`, `TestUnitFlowsInvocationStatus`,
  `TestUnitFlowsLimits`, `TestUnitFlowsDefinitionParse`: delivery effects,
  the four statuses, counter boundaries in UTC, and strict YAML import and
  export round-trips with CEL expressions.
- `TestUnitAppContractCompatible` runs `buf breaking` against the last
  release's `.proto` files.
- `pkg/flows/appsdk` tests run a fixture app through `apptest`: `Describe`
  schemas from Go types, lifecycle, KV and widgets.
- `test/acceptance/features/flows_engine.feature` (`@postgres`,
  `@flows-agent`): fan-out and merge, echo, pending events, Sleep across
  a server restart, manual retry and re-emit, monthly and hourly limits,
  retention deletion.
- `flows_apps.feature` (`@postgres`, `@flows-agent`): a fixture registry
  served by a chi test server, digest refusal, a custom app published from
  an image built from the Go SDK, lifecycle sync serialization, KV locks,
  timers, block HTTP, widgets, a JavaScript bundle refused.
- `flows_agents.feature` (`@postgres`, `@flows-agent`): two `serve`
  replicas, an agent killed mid-task and its work taken over, pool token
  rotation, an installation's container never reused for another.
- `flows_clients.feature` (`@postgres`, `@terraform`, `@flows-agent`):
  `infraweavectl` publishes an app version and exports a flow;
  infraweave's provider applies, plans with no drift and imports a flow.
- `TestUnitFlowsClientParity` fails when an RPC path in
  `api/reference/flows-clients.yaml` names no operation of
  `api/flows.openapi.yaml`.
- `flows_deploy.feature` (`@postgres`, `@tofu`, `@flows-agent`): a Deploy
  trigger block reacts to a real run's state change and to a drift result,
  and Trigger Run starts one.

## References

- [23: Spacelift Flows](../reference/23-flows.md), in particular sections
  [3.4](../reference/23-flows.md#34-expressions-and-event-data),
  [4](../reference/23-flows.md#4-events-invocations-and-limits),
  [7](../reference/23-flows.md#7-the-app-sdk-contract),
  [8](../reference/23-flows.md#8-agents-and-agent-pools) and
  [10](../reference/23-flows.md#10-the-flows-api).
- [27 section 10](../reference/27-reference-materials.md#10-work-queue-and-workflow-engines):
  Temporal and River.
- [28 section 11](../reference/28-data-model.md#11-flows) and
  [section 12.11](../reference/28-data-model.md#1211-flows): the Flows data
  model.
- Terraform, [plugin protocol](https://developer.hashicorp.com/terraform/plugin/terraform-plugin-protocol):
  a gRPC contract with negotiated protocol versions.
- [Protocol Buffers, updating a message type](https://protobuf.dev/programming-guides/proto3/#updating)
  and [buf breaking](https://buf.build/docs/breaking/): the compatibility
  rules the contract follows.
- [wazero](https://github.com/tetratelabs/wazero): the WebAssembly runtime
  weighed as a second host.
- [RFC 9535](https://www.rfc-editor.org/rfc/rfc9535): JSONPath, for
  sensitive paths.
- Related records: [0015](0015-events-notifications-webhooks-audit.md),
  [0019](0019-ai-provider-boundary.md),
  [0020](0020-reference-deployments-and-packaging.md),
  [0021](0021-capability-scope-and-supersession.md),
  [0024](0024-expression-language.md); decisions 0005, 0006, 0008, 0011,
  0013 and 0022.
