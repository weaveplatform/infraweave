# 23: Spacelift Flows

Checked against: docs.spacelift.io `flows/` pages read 2026-10-28 (identical copies under `self-hosted/latest/flows/`); introspection `b3255fe38f5e`; `@slflows/sdk` 0.14.0 from npm; flowctl `3e7d42dabb01`; terraform-provider-flows v0.7.0 (`9d1a7ad4d937`); spacelift-helm-charts `4e02e1d22e73`; terraform-aws-eks-spacelift-flows-selfhosted `b7d32fa51c7e`; terraform-aws-spacelift-flows-agentpool-ec2 v1.3.0 (`6bb853a4d0a1`); the `flows-app-*` repositories at the commits in [section 6.5](#65-app-repositories); live OAuth metadata from `useflows.eu` read 2026-10-28.

Research baseline: **2026-10-28**.

This report records Spacelift Flows, the visual automation product that sits
beside Spacelift Deploy: its organisation, project and flow model, blocks and
the event and invocation semantics, apps and their registries, the app SDK
contract that app code is written against, agents and agent pools, access
control, the HTTP API that its first-party clients call, `flowctl`, the Flows
Terraform provider, the YAML flow definition, the self-hosted deployment, and
the link between Flows and Spacelift Deploy. It is the reference for whoever
designs infraweave's automation engine, its app runtime, its agent protocol or
its Flows API. The Deploy-side GraphQL operations are inventoried in
[04](04-platform-api-inventory.md); Deploy's worker pools are
[10](10-workers-and-execution.md); Deploy's RBAC is
[14](14-spaces-rbac-and-identity.md); the AI assistant and Intent are report
22; self-hosted Spacelift Deploy is report 24.

| Label | Means |
|---|---|
| **[D]** | A docs.spacelift.io page, cited by URL, read 2026-10-28 |
| **[G]** | The introspection snapshot `b3255fe38f5e` |
| **[S]** | Source code or a published package at the cited commit or version |
| **[C]** | A live capture, cited by URL with the date read |
| **[I]** | Inferred; the sentence says from what |
| *(unverified)* | No primary source confirms it; listed in 30-open-questions |

Licence notes for this report: the `@slflows/sdk` npm package declares no
licence, and neither do `terraform-aws-eks-spacelift-flows-selfhosted`,
`terraform-aws-spacelift-flows-agentpool-ec2` and `flows-app-utilities`. They
are cited by file and line and never quoted; only identifiers (type, field,
variable and key names) and values are recorded. flowctl, the Flows provider,
the Helm charts and the other `flows-app-*` repositories are MIT.

## 1. Product, instances and editions

| Fact | Value | Source |
|---|---|---|
| Product | Spacelift Flows: a canvas on which blocks are connected to build automations, integrations and MCP servers | [D] [intro] |
| Relation to Deploy | The product previously called Spacelift is renamed Spacelift Deploy; Flows is a separate module of the platform; self-hosted installs keep the Spacelift name | [D] [home]; [D] [intro] |
| SaaS instances | `useflows.eu` and `useflows.us`; each is a separate tenant domain | [D] [mcp]; [D] [oidc]; [S] [fc-auth-login] |
| Self-hosted | Supported on AWS EKS only, with a licence token | [D] [selfhost]; [D] [sh-eks] |
| Upgrades | Zero-downtime unless the changelog says otherwise: infrastructure first, then backend components | [D] [selfhost] |
| Docs location | `flows/` pages; the same pages are mirrored under `self-hosted/latest/flows/` with no differences | [I] from a recursive diff of the two mirror directories |
| Pricing and tier | Not stated on any Flows page or in the Deploy billing pages | *(unverified)* |
| Changelog | The Deploy changelog has no Flows entries | [I] from a search of `product/changelog.md` |

Use cases the docs name are DevOps automation (incident response,
deployment approvals, scaling), tool-to-tool workflow automation, and MCP
servers and AI agents built from flows [D] [intro].

## 2. Resource hierarchy

| Entity | Scope | What it holds | Source |
|---|---|---|---|
| Organization | Top level | Projects, users, teams, service accounts, agent pools, registry subscriptions, OIDC providers, audit log and its OTEL export | [D] [nav]; [D] [audit]; [D] [oidc]; [D] [agents] |
| Project | Organization | Flows, app installations, secrets, data tables, project-scoped custom apps, memberships | [D] [nav]; [D] [secrets]; [D] [custom-apps]; [D] [tfprov-d] |
| Personal Sandbox | One per user | A project every user gets for testing | [D] [quick] |
| Flow | Project | Blocks, links, notes, enabled flag, monthly event limit | [D] [limits]; [S] [prov-flow-schema] |
| Block | Flow | Name, type, config, input and output sockets, error handler, lifecycle state | [D] [blockmgmt]; [D] [defref] |
| App installation | Project | One configured instance of an app; an app can be installed several times per project | [D] [apps-ov]; [D] [nav] |
| Custom app | Project or organization | App code versions created with `flowctl` | [D] [custom-apps] |
| Agent pool | Organization, exposed per project | Agents that run JavaScript and app code | [D] [agents] |
| Secret | Project | Key and write-only value | [D] [secrets]; [S] [prov-secret] |
| Data table | Project | Typed columns and rows; attached to flows | [S] [prov-docs]; [D] [defref] |
| Team | Organization | Users, added to projects as a unit | [D] [nav] |
| Service account | Organization | A machine user with roles and API keys | [D] [sa] |
| Registry subscription | Organization | A registry URL the organization pulls apps from | [D] [registries] |

Identifiers are UUIDs: flow IDs match a UUID pattern in `flowctl`
[S] [fc-flow-export], and the flow definition types block, app installation,
data table and agent pool IDs as `String (UUID)` [D] [defref]. Example IDs in
the docs are UUIDv7 (time-ordered, such as `019a11e0-...`) [D] [err-block]
[I] from the version nibble of the examples.

## 3. Flows and blocks

### 3.1 The flow entity

| Field | Type | Meaning | Source |
|---|---|---|---|
| `id` | UUID | Flow ID | [S] [prov-docs] |
| `name` | string | Display name | [S] [prov-docs] |
| `active` (`enabled` in the provider) | bool | A disabled flow does not react to events; HTTP and schedule triggers on a disabled flow or block create no events | [S] [prov-flow-structs]; [D] [inv] |
| `eventLimit` | int or null | Monthly event limit override; null means the default | [S] [prov-flow-structs]; [D] [limits] |
| definition | YAML | Blocks, links, notes and referenced installations, data tables and agent pools (section 13) | [D] [defref] |
| `blocks` | map of name to `{id}` | Server-assigned block IDs, keyed by block name | [S] [prov-flow-structs] |

The event limit is stored with the flow and not in its definition, so it is
not exported, and a duplicated flow starts with the default [D] [limits]. The
provider can still set it, through the `eventLimit` field of the create and
update calls [S] [prov-flow-structs]; the docs page says the limit is not
managed through `flowctl` or the provider, which the provider source
contradicts, and the source wins [D] [limits].

### 3.2 Blocks, sockets and configuration

- A block performs one function and talks to other blocks through **input
  sockets** (top) and **output sockets** (bottom) [D] [blockmgmt].
- One output can feed many inputs (fan-out) and many outputs can feed one
  input (merge) [D] [blockmgmt].
- **Static configuration** belongs to the block, is evaluated once when the
  flow is saved, and may reference other blocks' signals; **dynamic
  configuration** belongs to an input socket, is evaluated per event and reads
  upstream data through `outputs` [D] [blockmgmt]; [D] [tfprov-d].
- An output can carry an **output customization** expression and a list of
  **sensitive paths** [D] [defref]; [D] [sensitive].
- A block can name a Handle Error block as its **error handler** (`errorHandler`) [D] [err-block]; [D] [defref].
- Blocks are identified by name inside a flow: renaming deletes and recreates
  the block unless `alternativeLookupStrategy` names the old name or the block
  ID [D] [tfprov-d].
- Block names become camelCase keys in event data: numbers are kept and text
  in parentheses is dropped (`Test Block (3)` becomes `testBlock`) [D] [io].
- Canvas groups in the sidebar: AI, HTTP, Time, Business Logic, Collections,
  Memory, Errors, Data Tables, Forms, Spacelift Deploy and Note, plus app
  blocks [D] [canvas].

### 3.3 Core block catalogue

The `coreBlock` values are the enum in the flow definition reference
[D] [defref]; behaviour is from the block pages.

| `coreBlock` | Purpose | Inputs and outputs | Config (static, per input) | Source |
|---|---|---|---|---|
| `httpEndpoint` | Receives HTTP requests on the flow's endpoint subdomain and answers them | Output `requests` emits the request; input `default` takes the response | Static `path` (with `{param}` segments), `methods`, `audience` (`world`, `organization`, `project`); input `statusCode`, `body`, `headers` | [D] [http-blocks]; [D] [defref]; [D] [tfprov-d] |
| `httpRequest` | Calls an external HTTP API | Input `default`, output with the response | Input `url`, `method`, `body` (strings sent as-is, other values JSON-encoded), `headers`, `agentPool` | [D] [http-blocks]; [D] [defref] |
| `schedule` | Emits on a cron or interval with a timezone | Trigger, no input | Static `definition` (`type` `cron` with `expression` and `location`, or `frequency` with `interval` and `unit` `seconds`, `minutes` or `hours`) | [D] [time-blocks]; [D] [defref] |
| `sleep` | Delays each event | Input `default` | Input `sleep`, seconds, may be an expression | [D] [time-blocks]; [D] [defref] |
| `condition` | Routes on a boolean expression | Two outputs, true and false; the true output is named `then` | Input `condition` | [D] [bl-blocks]; [D] [tfprov-d] |
| `transform` | Builds a new event from an expression | Input `default` | Expression on the input | [D] [bl-blocks]; [D] [defref] |
| `collect` | Batches events by count, delay, group key or condition | Emits one event with `items` | Input `group`, `count`, `emit` (sees `outputs`, `items`, `group`), `item`, `delay` | [D] [coll-blocks]; [D] [defref] |
| `explode` | Emits one event per list item | | Input `items`, `emittedLimit` | [D] [coll-blocks]; [D] [defref] |
| `memorySet` | Writes a key in a named collection, with optional TTL | | Input `collection` (static), `key`, `value` (sees `stored`), `ttl` | [D] [mem-blocks]; [D] [defref] |
| `memoryGet` | Reads a key | Emits `{collection, key, found, value?, ttl}` | Input `collection`, `key` | [D] [mem-blocks] |
| `memoryList` | Lists a collection page | Emits `{items, nextStartingKey}`; `limit` 1 to 100 | Input `collection`, `limit`, `startingKey` | [D] [mem-blocks]; [D] [defref] |
| `mcpTool` | One tool of the flow's MCP server | Output emits `{arguments, user{email, role}}`; input takes the response | Static `inputSchema` (map of name to `{required, type}`); input `responseBody` | [D] [ai-blocks]; [D] [defref] |
| `handleError` | Catches failures of blocks that name it | Output emits the error event (section 4.4) | | [D] [err-block] |
| `group` | Groups blocks behind named sockets | Group inputs and outputs, each with `name`, `links`, `outputCustomization`, `sensitivePaths` | | [D] [defref] |
| `concurrencyGroup` | Admits one item per group key at a time | Inputs `acquire` and `release` | Input `groupKey`, `itemKey` | [D] [defref]; [D] [templates-batch] |
| `dataTableCreateRow`, `dataTableGetRow`, `dataTableUpdateRow`, `dataTableQueryRows`, `dataTableDeleteRow`, `dataTableShareRow` | Row operations on a project data table | | Static `table` (and `limit` for queries); input `row`, `ttl`, `rowID`, `rowIDs`, `filter`, `queryOrders`, `continuationToken`, `assignees` | [D] [defref] |
| `formOpened` | Trigger: a form is opened, such as through the Service Catalog | Trigger | | [D] [inv]; [D] [defref] |
| `showForm` | Waits for a form submission | Holds the invocation open | | [D] [inv]; [D] [defref] |
| `spacelift` | Spacelift Deploy blocks; the specific block is chosen by `kind` | Per kind (section 15) | Static per kind; input per kind | [D] [tfprov-d]; [D] [deploy-blocks] |

The Data Tables, Forms, Group and Concurrency Group blocks appear only in the
generated definition reference and the template pages; no page describes their
behaviour [I] from the page index [D] [llms-flows].

### 3.4 Expressions and event data

| Element | Meaning | Source |
|---|---|---|
| Language | JavaScript expressions; anonymous functions are allowed for multi-step logic | [D] [io]; [D] [bl-blocks] |
| `!expr` | YAML tag marking a value as an expression; untagged values are literals | [D] [tfprov-d]; [D] [defref] |
| `outputs.<block>.<field>` | Data accumulated from upstream blocks on the event's lineage | [D] [io] |
| `signals.<block>.<signal>` | Static outputs of lifecycle blocks in `READY`; creates a configuration dependency without a link | [D] [signals] |
| `secrets.<KEY>` | Project secret, injected at runtime | [D] [secrets] |
| `stored` | Previous value of the key, in Memory Set only | [D] [mem-blocks] |
| `items`, `group` | Collected items and group key, in Collect's `emit` only | [D] [coll-blocks] |
| `config`, `event` | Block configuration and the current event, per the definition reference | [D] [defref] |

Each block adds its output under its camelCase name, so a downstream event
carries every upstream output on its primary lineage [D] [io]. Secondary
parents are lineage for display only; their data is not available downstream
[D] [sdk-events].

### 3.5 Lifecycle blocks and signals

Some blocks manage external resources (for example an S3 bucket or a
PagerDuty webhook subscription): they create the resource on confirmation,
update it when configuration changes, remove it when the block is deleted,
and publish signals [D] [blockmgmt]; [D] [signals]. Creation and change need
user confirmation unless the app marks the block `autoconfirm`
[D] [sdk-lc]; [S] [sdk] L907-L912.

| State (docs) | SDK value | Meaning | Source |
|---|---|---|---|
| `DRAFT` | `draft` | Configured, nothing created | [D] [signals]; [S] [sdk] L1101 |
| `IN_PROGRESS` | `in_progress` | Creating or updating | [D] [signals]; [S] [sdk] L1101 |
| `READY` | `ready` | Created; signals available | [D] [signals]; [S] [sdk] L1101 |
| `FAILED` | `failed` | Creation or update failed | [D] [signals]; [S] [sdk] L1101 |
| `DRAINING` | `draining` | Being deleted | [D] [signals]; [S] [sdk] L1101 |
| | `draining_failed` | Clean-up failed | [S] [sdk] L1101 |
| | `drained` | Clean-up complete; the entity can be removed | [S] [sdk] L1101 |
| | `drifted` | Appears only in `blocks.list` (its status filter and each listed block's `lifecycleStatus`) and as a non-ready terminal state in the provider | [S] [sdk] L53, L69; [S] [prov-ai-status] |

Transitions in the docs are `DRAFT` to `IN_PROGRESS` on confirmation then
`READY`; `READY` to `IN_PROGRESS` on a configuration change then `READY`; and
`READY` to `DRAINING` on deletion, after which the block is removed
[D] [signals]. The SDK adds that `onSync` can return `failed`, and `onDrain`
can return `draining_failed` or `drained` [D] [sdk-lc]. The docs list five
states. The SDK's `EntityLifecycleStatus` (L1101) and `AppLifecycleStatus`
(L1166) have seven values each; `drifted` is an eighth value that appears
only in `blocks.list`, as a filter value and in its output. The SDK, being the contract the
platform executes, is the more complete source [I] from comparing the two.

### 3.6 Secrets and sensitive values

- Secrets are project-scoped keys referenced as `secrets.KEY` in any
  expression and in app installation configuration [D] [secrets].
- Viewers and above see secret names and can reference them; creating,
  updating and deleting secrets needs the project Admin role [D] [secrets].
- Secret values are never shown after creation and are masked in logs, debug
  output, errors and event views; a secret echoed back by an API into a block
  output becomes visible downstream [D] [secrets].
- **Sensitive paths** are RFC 9535 JSONPath expressions on a block output
  whose matches are shown as `******` in the event viewer and the API; the
  real values still flow downstream [D] [sensitive].
- Rules: non-matching paths are ignored silently, paths apply after output
  customization, a parent path covers its children, and app schemas can mark
  properties `sensitive: true` so the paths are configured automatically
  [D] [sensitive]; [S] [sdk] L846.
- App and block signals carry `sensitive` and a `sensitivity` of
  `hide_by_default`, `never_show` or `not_sensitive` [S] [sdk] L863-L868.

## 4. Events, invocations and limits

### 4.1 Events and lineage

- An event is counted each time a block emits on an output, including
  triggers, core and app block outputs and manual debug events
  [D] [limits].
- Each emitted event records its predecessor; an event with no predecessor
  starts a new **invocation**, and all descendants belong to it [D] [inv].
- In an app's `onEvent` handler the event being handled becomes the parent
  automatically; events emitted from timers, HTTP handlers, messages or
  lifecycle handlers have no parent unless the app passes `parentEventId`
  [D] [sdk-events]; [S] [sdk] L75-L102.
- **Echo**: an event emitted with `echo: true` is handed back as
  `input.event.echo` (`id`, `body`, `outputKey`) when a later event whose
  ancestry contains it reaches the same block; this is how request and
  response blocks pair up [D] [sdk-events]; [S] [sdk] L1117-L1134.
- **Pending events** represent a response still to come, with a status
  description, optional predicted payload and output; they are updated,
  completed (or completed by `emit` with `complete`) or cancelled with a
  reason, and can be linked to a timer [D] [sdk-events]; [S] [sdk] L104-L133.
- A user can re-emit a past event from an output; the copy starts a new
  invocation and keeps a history link to the original [D] [inv].
- Failed events can be retried manually from a block's input list; a retry
  can reactivate a terminal invocation [D] [inv]; [D] [limits].

### 4.2 Invocations

What starts an invocation [D] [inv]:

| Trigger | Counts when |
|---|---|
| HTTP Endpoint | An accepted request; unmatched paths, disallowed methods and disabled flows or blocks are rejected before an event exists |
| Schedule | Each firing on an enabled flow and block |
| MCP Tool | Each tool call by an AI agent |
| Form Opened | Each time a user opens the form |
| Spacelift Deploy subscription blocks | Each matching run event or notification |
| App blocks | Each event emitted outside the handling of an incoming event (webhook, timer, HTTP callback, `onSync`) unless the app sets a parent |
| Debug event | Manual send to an input; shown as *Triggered manually* |
| Re-emit | Manual re-emit from an output |
| Collect with delay | A group emitted because its delay elapsed (collected events become secondary parents; downstream `outputs` holds only `items`) |
| Handle Error without an originating event | A timer, HTTP callback or message handler failure |

| Status | Meaning |
|---|---|
| In progress | No failure so far, with work still pending in some block |
| Failed in progress | Some block has failed while others are still working |
| Processed | All work is done with no failure; a disabled block's ignored event counts as done |
| Failed | All work is done and one or more blocks failed, even when a Handle Error block caught the failure |

`Processed` and `Failed` are terminal; status is recomputed shortly after any
input changes state [D] [inv]. Invocations waiting on Sleep, Show Form or an
external approval stay *In progress* indefinitely [D] [inv]. History is
kept 60 days after an invocation becomes terminal, then the invocation, its
events and payloads are deleted; active invocations are kept regardless, and
dashboard counts are aggregated separately and survive deletion [D] [inv].
The invocations sidebar filters by status, creation time and update time,
searches by trigger block name, and opens the event tree [D] [inv].

### 4.3 Limits

| Limit | Default | Change | Source |
|---|---|---|---|
| Events per flow per calendar month | 50,000 | Editors set 0 to the ceiling; 0 stops processing | [D] [limits] |
| Organization ceiling for a flow's monthly limit | 1,000,000 | Support raises it | [D] [limits] |
| Events per flow per hour | 50,000 | Fixed | [D] [limits] |
| Explode | `emittedLimit` per input event | Per block | [D] [coll-blocks]; [D] [defref] |
| Memory List page | 1 to 100 | Per block | [D] [mem-blocks] |
| Audit OTEL batch | 1 to 1000 records | Per organization | [D] [audit] |

At the monthly limit, events arriving at inputs fail with `flow monthly event
limit exceeded`, the flow stays enabled, triggers are still recorded and
failed events are not retried automatically [D] [limits]. At the hourly limit,
blocks cannot emit until the hour resets, work is retried automatically, the
block logs `flow event rate limit exceeded` with the reset time, and manual
debug events are rejected [D] [limits]. The UI shows *Limit almost reached*
above 80% and *Limit reached* at 100% [D] [limits].

### 4.4 Errors

A Handle Error block assigned as a block's error handler receives an event
with `error.error_handler_id`, `error.message`, optional
`error.previous_error`, `operation` (for example `block.onEvent`),
`entity.id`, `entity.type` (for example `app_block`) and
`retry.was_retried` [D] [err-block]. One Handle Error block can serve many
blocks [D] [err-block]. App code signals failure by throwing; the runtime
catches and reports the error [S] [app-ld-claude].

## 5. Triggers and endpoints

| Trigger | Address | Auth | Source |
|---|---|---|---|
| HTTP Endpoint block | `https://<random-words>.endpoints.<appDomain>/<path>`; one subdomain per flow, one path per endpoint | `audience`: `world` (none), `organization` (organization members), `project` (project members); API keys with the `endpoints:access` capability, and service-account keys, can call non-public endpoints | [D] [http-blocks]; [D] [authz]; [D] [sa] |
| App and block HTTP | A URL per app installation, and a path per block under it (`input.app.http.url`, `input.block.http.url`); placeholders `{appEndpointUrl}` and `{appEndpointHost}` in installation instructions | Handled by app code | [D] [sdk-http]; [S] [sdk] L1062-L1065 |
| Flow MCP server | Address shown in the flow sidebar; tools are the flow's MCP Tool blocks | API keys or OAuth2 authorizations with `mcp` and explicit MCP flow access | [D] [ai-blocks]; [D] [authz] |
| Schedule | Cron with timezone, or interval | n/a | [D] [time-blocks] |
| Form Opened | Service Catalog forms | Consumer role | [D] [nav]; [D] [inv] |
| Deploy subscriptions | Run state changes and Deploy notifications | Deploy role bindings (section 15) | [D] [deploy-blocks] |

## 6. Apps, installations and registries

### 6.1 Apps and installations

- Apps are TypeScript or JavaScript packages run in Node.js that contribute
  blocks and app-level behaviour [D] [apps]; [D] [dev-start].
- Flows ships few built-in blocks and exposes core services that apps use
  for higher-level features [D] [dev].
- An app can be installed several times per project, each installation
  configured separately (for example one per AWS region) [D] [apps-ov].
- Installation steps: choose the app in the project's registry view, name
  the installation (it starts as **Draft**), fill the configuration
  following the app's instructions, save, **Confirm**, and wait for
  **Ready** or **Failed** [D] [app-inst].
- An installation can be assigned an agent pool so its lifecycle code and
  blocks run on that pool's agents [D] [agents].
- Installation states follow the app lifecycle values in section 3.5; the
  provider waits for `ready` and treats `failed`, `drifted`, `draining`,
  `draining_failed` and `drained` as failures [S] [prov-ai-status].

### 6.2 Registries

| Registry | URL | Maintainer | Source |
|---|---|---|---|
| Core | `https://registry.useflows.com/core` | Spacelift, supported | [D] [apps-ov]; [D] [registries] |
| Community | `https://registry.useflows.com/community` | Community, curated, unsupported; artefacts come from GitHub | [D] [apps-ov]; [D] [sh-adv] |
| Third-party | Any HTTPS URL, public or private | Customer | [D] [registries] |

Every organization is subscribed to Core and Community on creation; more
registries are added under **Settings**, **App Registries** or through an API
call, and are refreshed hourly [D] [registries]. A live fetch of the Core
registry on 2026-10-28 returned the manifest format below [C]
<https://registry.useflows.com/core>.

| File | Field | Type | Meaning |
|---|---|---|---|
| Registry manifest | `apps` | map | App key (URL-safe: letters, digits, hyphens, underscores) to app entry |
| | `apps.<key>.name` | string | Display name |
| | `apps.<key>.description` | string | Short description |
| | `apps.<key>.blockStyle.iconUrl` | URL | PNG or SVG icon, under 1 MB recommended |
| | `apps.<key>.blockStyle.color` | hex | Block colour |
| | `apps.<key>.appMetadataUrl` | URL | The app's versions file |
| App metadata | `versions[]` | array, order irrelevant | Published versions |
| | `versions[].version` | SemVer | Version |
| | `versions[].artifactUrl` | URL | `.tar.gz` bundle from `flowctl version bundle` |
| | `versions[].artifactChecksum` | `sha256:<lowercase hex>` | Verified by Flows on download |

Source: [D] [registries]. CORS must allow `GET` and `HEAD` when the registry
is on another origin [D] [registries]. The `flows-core-registry` repository
holds one directory per app with `manifest.json` (`name`, `description`,
`color`) and a logo, and app repositories publish versions through reusable
workflows of the core and community registry repositories
[S] [core-reg-readme]; [S] [tpl-core-ci]. The registry held 116 core and 10
community apps at the pinned commits [I] from a directory count.

### 6.3 Custom apps

- Created with `flowctl app create`, then versioned with `flowctl version
  create`; a version starts as a **draft**, visible only to developers, and
  is published with `flowctl version publish` [D] [custom-apps].
- Only drafts can be updated (`flowctl` refuses otherwise) [S] [fc-version].
- Scope: project-scoped by default; organization-wide apps are created
  without a project or promoted by an administrator [D] [custom-apps].
  `flowctl app create` always sends a project ID and prompts for one when
  `-p` is absent, so the CLI path to an organization-wide app is
  *(unverified)* [S] [fc-app].
- Project Admins and organization admins create custom apps; the
  `apps:admin` capability manages them through the API [D] [nav];
  [D] [authz].

### 6.4 Bundle format

`flowctl version bundle` builds two files and packs them into a gzip tarball
(`bundle.tar.gz` by default) [S] [fc-bundle]:

| File | Built from | esbuild settings | Source |
|---|---|---|---|
| `app.js` | `--entrypoint` | Bundled ESM for `node22`, `@slflows/sdk/*` and `*.node` left external, a banner that defines `require`, `__dirname` and `__filename` | [S] [fc-build] |
| `ui.js` (optional) | `--uiEntrypoint` | Bundled, minified ESM for the browser, `es2020` | [S] [fc-build] |

`version create` and `version update` send the same two strings as `code`
and `uiCode` instead of a tarball [S] [fc-api]. The SDK import is left
external, so the runtime supplies `@slflows/sdk/v1` [I] from the external
list and the types-only package (section 7).

### 6.5 App repositories

Generated by script over the shallow clones.
The repositories live in two GitHub organizations: nine older ones in
`spacelift-io` and the current set in `spacelift-flows-apps`. The last column
counts `: AppBlock =` declarations, so it under-counts apps that build blocks
another way and over-counts generated API apps [I] from the method.

| Repository | Commit | Licence | `@slflows/sdk` | `AppBlock` hits |
|---|---|---|---|---|
| spacelift-io/flows-app-brave-search | `eff5cf1d2f0b` | MIT | `^0.0.7` | 3 |
| spacelift-io/flows-app-clickup | `7134448600a9` | MIT | `^0.13.0` | 0 |
| spacelift-io/flows-app-conversations | `4e4f09105088` | MIT | `^0.5.1` | 2 |
| spacelift-io/flows-app-featurebase | `3e5fcc9de159` | MIT | `^0.14.0` | 38 |
| spacelift-io/flows-app-hcp-terraform | `90cd06a96982` | MIT | `^0.0.7` | 39 |
| spacelift-io/flows-app-launchdarkly | `b1d04642d657` | MIT | `^0.5.1` | 0 |
| spacelift-io/flows-app-openrouter | `e22283c5f0b5` | MIT | `^0.0.7` | 2 |
| spacelift-io/flows-app-pinecone | `733d5e12fcb3` | MIT | `^0.14.0` | 9 |
| spacelift-io/flows-app-segment | `27ff3f20825f` | MIT | `^0.0.7` | 3 |
| spacelift-flows-apps/agent-skills | `b2a2bf4964cb` | MIT | none | 0 |
| spacelift-flows-apps/flows-app-anthropic | `4d114adabe3e` | MIT | `^0.13.0` | 6 |
| spacelift-flows-apps/flows-app-argocd | `9966eae9d901` | MIT | `^0.13.0` | 12 |
| spacelift-flows-apps/flows-app-aws-api | `ae6c85e88a4f` | MIT | none at the root | 2960 |
| spacelift-flows-apps/flows-app-aws-resources | `ed5351d51952` | MIT | `^0.4.0` | 2 |
| spacelift-flows-apps/flows-app-aws-sts | `c9c79e4dd96f` | MIT | `^0.0.8` | 1 |
| spacelift-flows-apps/flows-app-aws-toolkit | `a4db54248504` | MIT | `^0.13.0` | 2 |
| spacelift-flows-apps/flows-app-azure-api | `ea84a050cf75` | MIT | none at the root | 3034 |
| spacelift-flows-apps/flows-app-azure-oidc | `aee857616e59` | MIT | `^0.0.8` | 1 |
| spacelift-flows-apps/flows-app-azure-toolkit | `527051bbc88e` | MIT | `^0.13.0` | 1 |
| spacelift-flows-apps/flows-app-backstage | `f5e02f246259` | MIT | `^0.14.0` | 1 |
| spacelift-flows-apps/flows-app-cloudflare | `a75faa2758c9` | MIT | `^0.0.8` | 28 |
| spacelift-flows-apps/flows-app-confluence | `0b97a11fcb0a` | MIT | `^0.0.8` | 10 |
| spacelift-flows-apps/flows-app-datadog | `b357e4d934ad` | MIT | `^0.13.0` | 1 |
| spacelift-flows-apps/flows-app-gcp-api | `45dfc9e21ad9` | MIT | `^0.9.0` | 1150 |
| spacelift-flows-apps/flows-app-gcp-toolkit | `0da6803f35e7` | MIT | `^0.13.0` | 1 |
| spacelift-flows-apps/flows-app-gcp-workflow-identity-federation | `54a5aac4035d` | MIT | `^0.0.8` | 1 |
| spacelift-flows-apps/flows-app-github | `b07de4efed93` | MIT | `^0.13.0` | 0 |
| spacelift-flows-apps/flows-app-gitlab | `2193fb6871bf` | MIT | `^0.13.0` | 0 |
| spacelift-flows-apps/flows-app-grafana | `8189d38f4179` | MIT | `^0.13.0` | 4 |
| spacelift-flows-apps/flows-app-honeycomb | `19cedcafd7cf` | MIT | `^0.13.0` | 3 |
| spacelift-flows-apps/flows-app-imap | `528d7e3bbb4b` | MIT | `^0.13.0` | 2 |
| spacelift-flows-apps/flows-app-incident-io | `373b520102f9` | MIT | `0.13.0` | 120 |
| spacelift-flows-apps/flows-app-jira | `8b9b46b263ae` | MIT | `^0.13.0` | 25 |
| spacelift-flows-apps/flows-app-k8s | `3c24de1d8f77` | MIT | `^0.0.8` | 8 |
| spacelift-flows-apps/flows-app-linear | `af364f0e0b16` | MIT | `^0.0.8` | 6 |
| spacelift-flows-apps/flows-app-mongodb | `eae0457c4de4` | MIT | `^0.0.8` | 10 |
| spacelift-flows-apps/flows-app-mssql | `c911b52c2164` | MIT | `^0.0.8` | 5 |
| spacelift-flows-apps/flows-app-notion | `1f4800c529a0` | MIT | `^0.13.0` | 25 |
| spacelift-flows-apps/flows-app-oauth2 | `e6fd0e44b564` | MIT | `^0.2.0` | 1 |
| spacelift-flows-apps/flows-app-oidc | `6ef53a02a3d1` | MIT | `^0.14.0` | 0 |
| spacelift-flows-apps/flows-app-openai | `e5d86b6a1b08` | MIT | `^0.14.0` | 6 |
| spacelift-flows-apps/flows-app-pagerduty | `982a22a093d7` | MIT | `^0.13.0` | 7 |
| spacelift-flows-apps/flows-app-postgresql | `4f7f7bcd9e25` | MIT | `^0.0.8` | 6 |
| spacelift-flows-apps/flows-app-prometheus | `7a11052f192a` | MIT | `^0.0.8` | 9 |
| spacelift-flows-apps/flows-app-rundeck | `8dbdc03b0ecd` | MIT | `^0.8.0` | 5 |
| spacelift-flows-apps/flows-app-sentry | `7f28f78fcfb0` | MIT | `^0.13.0` | 8 |
| spacelift-flows-apps/flows-app-servicenow | `389f1d689c58` | MIT | `^0.13.0` | 6 |
| spacelift-flows-apps/flows-app-slack | `293265d0a2e5` | MIT | `^0.13.0` | 27 |
| spacelift-flows-apps/flows-app-smtp | `1d6e7c8f4447` | MIT | `^0.0.8` | 1 |
| spacelift-flows-apps/flows-app-spacelift | `952465d7badc` | MIT | `^0.13.0` | 49 |
| spacelift-flows-apps/flows-app-ssh | `90bea698c88a` | MIT | `^0.0.8` | 2 |
| spacelift-flows-apps/flows-app-statuspage | `817d3d0018d1` | MIT | `^0.0.8` | 112 |
| spacelift-flows-apps/flows-app-teams-bot | `78d3c20c809d` | MIT | `^0.13.0` | 6 |
| spacelift-flows-apps/flows-app-teams-management | `e0ec3d6f4a23` | MIT | `^0.13.0` | 23 |
| spacelift-flows-apps/flows-app-template | `eef5060ceec9` | MIT | `^0.0.7` | 1 |
| spacelift-flows-apps/flows-app-template-core | `5d55723b17cc` | MIT | `^0.0.8` | 1 |
| spacelift-flows-apps/flows-app-utilities | `fdbc652b98b6` | none | `^0.5.1` | 14 |
| spacelift-flows-apps/flows-app-variables | `a694bac7b777` | MIT | `^0.5.1` | 0 |
| spacelift-flows-apps/flows-app-zendesk | `de71d0f3ba14` | MIT | `^0.13.0` | 12 |
| spacelift-flows-apps/flows-community-registry | `f0b1b1d131bb` | MIT | none | 0 |
| spacelift-flows-apps/flows-core-registry | `bcd2033a5c2f` | MIT | none | 0 |

The docs point at the template as `spacelift-io/flows-app-template`; GitHub
serves it from `spacelift-flows-apps` [D] [apps-ov]; [C]
<https://github.com/spacelift-flows-apps/flows-app-template> read
2026-10-28. The Utilities app (no licence) supplies the blocks other docs
lean on: `capture`, `deduplicate`, `display`, `encryptionKey`,
`htmlToMarkdown`, `label`, `loop`, `merge`, `mutex`, `observe`, `rateLimit`,
`stage`, `subroutineCall`, `subroutineDefinition`, `throttle` and `timeout`
[S] [utils-blocks]; the docs cite its mutex and subroutine blocks as examples
of `onSync` serialisation and echo [D] [sdk-lc]; [D] [sdk-events].

## 7. The app SDK contract

The SDK is the npm package `@slflows/sdk`, imported as `@slflows/sdk/v1`. At
0.14.0 (published 2026-05-25) it ships a single declaration file and no
runtime code, and declares no licence [S] [sdk-npm]; [C] npm registry
metadata for `@slflows/sdk` read 2026-10-28. The functions it declares are
supplied by the Flows runtime when the bundle runs [I] from the types-only
package and `flowctl` marking it external [S] [fc-build]. Line numbers below
are in `dist/v1/index.d.ts` [S] [sdk].

### 7.1 App definition (`AppSchema`, L768-L828)

| Field | Type | Meaning |
|---|---|---|
| `name` | string | Display name |
| `installationInstructions` | Markdown | Shown during installation |
| `config` | map of `AppConfigField` | Installation configuration, visible to all blocks |
| `signals` | map of `AppSignal` | Values the app publishes to its blocks |
| `onSync` | `(AppInput) => AppLifecycleCallbackOutput` | Reconcile the installation |
| `onDrain` | same | Clean up on removal |
| `draftCustomStatusDescription` | string | Status text while in draft |
| `onInternalMessage` | `(AppOnInternalMessageInput) => void` | Messages from blocks |
| `blocks` | map of block type ID to `AppBlock` | Required; each becomes a canvas block |
| `http` | `{onRequest}` | App-level HTTP handler |
| `ui` | `{onRequest}` | App-level UI requests |
| `schedules` | map of `AppSchedule` | Declarative schedules |
| `onTimer` | `(AppOnTimerInput) => void` | Imperative timers |
| `autoconfirm` | bool, default false | Skip the confirmation step |

`defineApp(app: AppSchema): AppSchema` (L731) returns the schema it is given
and is a named export of the SDK (L1262). The app's own entry module
default-exports the result of `defineApp(...)`, as the SDK's examples show
(L698, L742) [S] [sdk].

### 7.2 Block definition (`AppBlock`, L891-L920)

| Field | Type | Meaning |
|---|---|---|
| `name`, `description`, `category` | string | Palette metadata |
| `config` | map of `AppBlockConfigField` | Block (static) configuration |
| `inputs` | map of `AppBlockComponentInput` | Each has `name`, `description`, `config` (map of `AppBlockInputConfigField`) and a required `onEvent` |
| `outputs` | map of `AppBlockComponentOutput` | `order`, `name`, `description`, `default`, `possiblePrimaryParents` (input keys used for type inference), `secondary`, `type` |
| `signals` | map of `AppBlockSignal` | `name`, `description`, `sensitive`, `sensitivity` |
| `onSync`, `onDrain` | `(EntityInput) => EntityLifecycleCallbackOutput` | Lifecycle |
| `draftCustomStatusDescription` | string | Draft status text |
| `onInternalMessage` | handler | Messages |
| `http` | `{onRequest}` | Block HTTP handler |
| `ui` | `{onRequest, views}` | Custom widgets; views have `id`, `label`, `type` (`default`, `fullScreen`, `regular`) |
| `schedules` | map of `AppBlockSchedule` | Declarative schedules |
| `onTimer` | handler | Block timers |
| `autoconfirm` | bool, default false | Skip lifecycle confirmation |
| `entrypoint` | bool, default false | Marks a trigger block that starts new event chains |

### 7.3 Configuration fields (L832-L986)

| Field | App config | Block config | Input config | Meaning |
|---|---|---|---|---|
| `name` | yes | yes | yes | Label, required |
| `description` | yes | yes | yes | Help text |
| `type` | yes | yes | yes | A simple type (`string`, `number`, `boolean`, `any`, `jsonSchemaObject`), a one-element array of one, or a JSON Schema subset (`type`, `const`, `title`, `properties`, `items`, `required`, `enum`, `anyOf`, `oneOf`, `description`, `sensitive`, `additionalProperties`) |
| `required` | yes | yes | yes | Must be filled, statically or by expression |
| `default` | yes | yes | yes | Default value |
| `fixed` | yes | yes | no | Cannot change after installation or provisioning |
| `sensitive` | yes | yes | yes | Masked in the UI |
| `populateFrom` | yes | no | yes | `{blockKey, outputProperty}`: fill from an upstream block of the same installation |
| `fieldKey` | no | yes | no | Undocumented key override |
| `suggestValues` | no | yes | yes | Autocomplete callback; gets `app`, `block`, `searchPhrase`, `staticInputConfig`; returns `suggestedValues[{label, value}]` and `message` |

Sources: [S] [sdk] L832-L986; [D] [sdk-config]. Block configuration is
visible only to its block; the app reads it through `blocks.list`
[D] [sdk-config].

### 7.4 Services

| Service | Function | Signature essentials | Meaning | Line |
|---|---|---|---|---|
| `events` | `emit` | `(event, {outputKey?, parentEventId?, secondaryParentEventIds?, echo?, complete?})` | Emit on an output; an unknown `outputKey` throws; it may be omitted when the block has one output | L208-L214 |
| | `createPending` | `({event?, outputKey?, parentEventId?, secondaryParentEventIds?, statusDescription})` returns ID | Announce a future event | L104-L117 |
| | `updatePending` | `(id, {...})` | Replace pending data or status | L122-L133 |
| | `completePending` | `(id)` | Emit the pending event | L213 |
| | `cancelPending` | `(id, reason)` | Cancel it | L212 |
| `kv.app`, `kv.block` | `get`, `getMany` | key or keys, returns `KVPair` | Read | L319-L360 |
| | `set`, `setMany` | `KVPair` with `ttl` and `lock{id, timeout}`, returns bool | Write; false on a lock conflict | L319-L360 |
| | `list` | `{keyPrefix, startingKey?}` returns `{pairs, nextStartingKey?}` | Prefix scan with paging | L29-L44 |
| | `delete` | keys | Delete | L327 |
| | `releaseLock`, `renewLock` | `{key, lockId}`, `{key, lock{id, timeout}}` | Lock management | L328-L338 |
| `messaging` | `sendToBlocks`, `sendToApp` | `{body, blockIds}`, `{body}` | Private messages inside one installation | L382-L390 |
| `blocks` | `list` | `{typeIds?, statuses?}` returns `{id, typeId, config, lifecycleStatus}[]` | Discover blocks of the installation | L49-L71, L405-L407 |
| `http` | `respond` | `(requestId, {statusCode?, headers?, body?})` | Answer a request; status defaults to 200; a `Uint8Array` body is sent as binary | L421-L427 |
| `lifecycle` | `sync` | `()` | Queue an idempotent sync (or a drain while draining) | L613-L620 |
| | `proceed` | `()` | Undocumented continuation | L615 |
| | `prompt.create`, `prompt.delete` | `(key, description, {redirect{url, method, formFields?}})`, `(key)` | Browser redirect prompts on the installation page (apps only) | L138-L148, L616-L619 |
| `timers` | `app.set`, `block.set`, `set` | `(delaySeconds, {inputPayload?, pendingEventId? (block), promptKey? (app), description?})` returns ID | One-shot timers | L636-L660 |
| | `unset` | `(id)` | Cancel | L636-L660 |
| `ui` | `widget.set`, `widget.get` | `{blockId?, elements}` | Custom block widgets | L678-L686 |
| `getInvocationMetadata` | | returns `{invocationId, timestamp}` | Correlation | L153-L176 |

Behaviour from the service pages:

- **KV**: values are serialised automatically (primitives, objects, arrays,
  null); TTL is in seconds; block storage follows the block even when it is
  detached and reattached to another installation, while app storage stays
  with the installation [D] [sdk-kv].
- **KV locks**: a set with a lock succeeds only when the key is unlocked or
  its lock expired; only the same lock ID can change or release it; a lock
  without a timeout lasts until released; atomicity comes from PostgreSQL
  row-level locks [D] [sdk-kv]; [S] [sdk] L222-L241.
- **Messaging**: messages never cross installation boundaries, are invisible
  in the UI, are buffered and delivered as a batch when the handler
  completes, and are dropped if the handler fails [D] [sdk-msg].
- **Lifecycle**: `onSync` runs on confirmation, configuration change, code
  change, manual sync and `lifecycle.sync()`; only one `onSync` runs at a
  time per entity [D] [sdk-lc].
- **Lifecycle callbacks** return `newStatus`, optional `signalUpdates`,
  `outputUpdates` (add, change or remove outputs at runtime) and
  `customStatusDescription`; `in_progress` and `draining` results may set
  `nextScheduleDelay` in seconds, and null stops rescheduling
  [S] [sdk] L1135-L1166.
- **Scheduling**: declarative schedules (`cron` with `expression` and
  `location`, or `frequency` with `interval` and `unit`) call `onTrigger` with
  `schedule.time`; `customizable: true` lets users edit the schedule as block
  configuration [D] [sdk-sched]; [S] [sdk] L872-L890, L1205-L1214.
- **Timers**: `onTimer` receives `timer.id`, `timer.payload` and, for blocks,
  the linked `pendingEvent{id, body, status}`, or for apps the linked
  `prompt{id, description}` [S] [sdk] L1184-L1204.
- **HTTP**: each entity receives every request to its URL and routes by
  `request.path` and `request.method` itself [D] [sdk-http].

### 7.5 Handler inputs

| Input | Carries | Line |
|---|---|---|
| `AppInput.app` (`AppContext`) | `config`, `status`, `prompts`, `http.url`, `installationUrl`, `signals` | L1037-L1058 |
| `EntityInput.block` (`EntityContext`) | `id`, `name`, `description`, `config`, `lifecycle{status, signals}` or null, `http{url}` or null, `outputs` (with a `modified` flag) | L1070-L1104 |
| `EventInput.event` (`EventContext`) | `id`, `inputConfig`, `echo{id, body, outputKey}` | L1109-L1134 |
| `request` (`HTTPRequest`) | `requestId`, `path`, `method`, `headers`, `query`, `params`, `rawBody`, `body` | L1215-L1230 |
| `request` (`UIRequest`) | `type`, `payload` | L1231-L1240 |
| `message` | `body` | L1174-L1183 |

A block sees its own lifecycle data and that of its installation, read-only
[D] [sdk-lc]. `AppOnCreateOutput` (`ok` or `redirect{url, method}`) is
declared but not referenced by any handler in the file [S] [sdk] L1167-L1173.

## 8. Agents and agent pools

Agents execute the JavaScript of flow expressions and app code. They hold a
WebSocket to the Flows **gateway** and run code in isolated Node.js runtimes
[D] [agents]; [D] [sh-eks]. The gateway routes evaluations to agents and
runtimes [D] [sh-eks].

| Fact | Value | Source |
|---|---|---|
| Public Agent Pool | Default; reaches public endpoints only | [D] [agents] |
| Private agent pool | Organization-scoped, created by organization admins, exposed to listed projects only; shows its connected agent count | [D] [agents] |
| Credentials | Pool ID and token; the token is shown once at creation | [D] [agents] |
| Assignment | An app installation (its lifecycle code and blocks) or a single HTTP Request block (`agentPool`) | [D] [agents]; [D] [defref] |
| Flow definition | `agentPools` map of `{name, id}` referenced by blocks | [D] [defref] |
| Runtime image | `node:24-alpine` from Docker Hub by default; overridable | [D] [sh-adv] |
| Agent image | `public.ecr.aws/w5z2f6e8/spacelift-flows-agent`, tag `agent-latest`; the binary is `/usr/bin/agent` and runs as uid 1983 | [D] [agents]; [S] [agent-deploy] |
| Readiness | HTTP `/health` on port 8080 | [S] [agent-deploy] |
| Sandboxing | The EC2 module runs runtimes under gVisor (`runsc`) with all capabilities dropped and `no-new-privileges` | [D] [agents]; [S] [ec2-ud] L196 |

Agent environment variables, from the Docker example, the Helm chart and
the EC2 user data:

| Variable | Meaning | Source |
|---|---|---|
| `FLOWS_GATEWAY_ENDPOINT` | Gateway URL, `https://gateway.<domain>` | [D] [agents]; [S] [agent-deploy] L34-L35 |
| `FLOWS_ENDPOINT` | Backend URL, `https://<domain>` | [D] [agents]; [S] [agent-deploy] L46-L47 |
| `FLOWS_AGENT_POOL_ID` | Pool ID | [D] [agents] |
| `FLOWS_AGENT_POOL_TOKEN` | Pool token | [D] [agents] |
| `FLOWS_EXECUTOR` | `docker` (sibling containers through the Docker socket) or `kubernetes` (pods) | [D] [agents]; [S] [agent-deploy] L48-L49 |
| `FLOWS_DOCKER_MOUNTS_TEMPDIR` | Host directory mounted into each runtime; must have the same absolute path on host and in the agent | [D] [agents] |
| `FLOWS_DOCKER_RUNTIME_IMAGE` | Runtime image override | [S] [ec2-ud] L183-L186 |
| `FLOWS_DOCKER_EXTRA_PARAMS` | Extra `docker run` flags for runtimes | [S] [ec2-ud] L196 |
| `FLOWS_ADDITIONAL_ROOT_CAS` | Base64 JSON `{caCertificates: [base64 PEM, ...]}` | [D] [sh-adv]; [S] [ec2-ud] L192-L194 |
| `FLOWS_POOL_SIZE` | Runtime pool size (chart default 3) | [S] [agent-deploy] L50-L51 |
| `FLOWS_KUBERNETES_IMAGE` | JS executor image for the Kubernetes executor (`k8s-js-executor-latest`) | [S] [agent-deploy] L54-L55 |
| `KUBERNETES_NAMESPACE`, `POD_NAME`, `POD_NAMESPACE` | Where runtime pods go and owner references | [S] [agent-deploy] L52-L64 |
| `OTLP_COLLECTOR_ENDPOINT` | OpenTelemetry collector when enabled | [S] [agent-deploy] L65-L69 |
| `HTTP_PROXY`, `HTTPS_PROXY`, `NODE_USE_ENV_PROXY` | Proxy for the agent and its runtimes | [S] [ec2-ud] L188-L196 |

A healthy agent logs `Connected to the server` and raises the pool's
connected count [D] [agents]. An installation moves from *In Progress* to
*Ready* only once an agent of its pool runs the app's lifecycle handler
[D] [agents].

## 9. Access control and identity

### 9.1 Roles

| Level | Role | Can | Source |
|---|---|---|---|
| Organization | Regular user | Organization membership grants platform access; project work needs a project role | [D] [nav]; [D] [perms] |
| Organization | Organization Admin | Manage the organization: projects, users, teams, service accounts, registry subscriptions, agent pools, OIDC, two-factor enforcement, audit logs | [D] [nav]; [D] [sa]; [D] [oidc]; [D] [2fa]; [D] [audit] |
| Project | Consumer | Call the project's HTTP endpoints, MCP servers and forms (through the Service Catalog); cannot see flow configuration | [D] [nav]; [D] [perms] |
| Project | Viewer | See everything in the project, including flows, events and logs; see secret names | [D] [perms]; [D] [secrets] |
| Project | Editor | Create and change flows and app installations; raise a flow's event limit | [D] [perms]; [D] [limits] |
| Project | Admin | Manage project settings and members, custom apps and secrets | [D] [perms]; [D] [secrets] |

Roles are granted to users and teams under the project's **Membership**
page [D] [perms]. The docs describe the Consumer role's forms as coming
*soon* on one page and available on another [D] [perms]; [D] [nav].

### 9.2 Authentication

| Mechanism | Behaviour | Source |
|---|---|---|
| Email magic link | Default login | [D] [oidc] |
| OIDC SSO | One provider per email domain: issuer, client ID and secret, endpoint discovery; callback `https://<domain>/api/auth/oidc/callback`; options to auto-provision users into the *Everyone* team and to require a verified email claim; a test login; domain proof by a DNS TXT record (one-off); disabling falls back to magic links | [D] [oidc] |
| Two-factor | FIDO2 security keys and passkeys, any number per user, with email or SSO; organization admins can enforce it | [D] [2fa] |
| Self-hosted admin password | At least 32 characters; enables `https://<domain>/auth/admin-login` for the admin email | [D] [sh-eks]; [S] [eks-vars] L89-L99 |
| API keys | Authenticate the Flows API and HTTP Endpoint blocks; personal keys under **Settings**, **Authorizations**, **API Keys**; service-account keys under the organization's users page; prefix `sfapi_` | [D] [apikeys]; [D] [sa]; [S] [fc-readme]; [S] [prov-readme] |
| OAuth2 | Authorization code with PKCE and dynamic client registration, used by `flowctl`, MCP clients and other tools | [D] [authz]; [S] [fc-auth] |
| Service accounts | Machine users with names, roles and API keys, so actions are not attributed to a person and survive their departure | [D] [sa] |

### 9.3 Authorizations and capabilities

API keys and OAuth2 grants both produce **authorizations**, each bounded by
the granting user's own permissions and editable or revocable later
[D] [authz]. An authorization has three parts: capabilities, project access
(per project, with the same four roles) and MCP flow access (an explicit list
of flow MCP servers) [D] [authz].

| Capability | Allows | In live `scopes_supported` |
|---|---|---|
| `mcp` | Flow MCP servers | MCP resource only |
| `api` | The Flows API; needed by the CLI and the provider | API resource |
| `apps:view` | View apps | API resource |
| `apps:admin` | Manage custom apps | API resource |
| `flows:view` | View flows | API and MCP resources |
| `flows:edit` | Create and change flows | API and MCP resources |
| `app_installations:view` | View installations | API and MCP resources |
| `app_installations:edit` | Manage installations | API and MCP resources |
| `secrets:view` | View secret keys | API resource |
| `secrets:edit` | Manage secrets | API resource |
| `data_tables:view` | View data tables | API and MCP resources |
| `data_tables:edit` | Manage data tables | API and MCP resources |
| `endpoints:access` | Call flow HTTP endpoints as the user | Not listed |

Every `:edit` capability needs its `:view` partner [D] [authz]. Scopes are
not expanded server-side, so a client must request each capability it needs
[S] [fc-auth] L13-L17. The live column comes from the protected-resource
metadata of `https://useflows.eu` and `https://mcp.useflows.eu` [C]
<https://useflows.eu/.well-known/oauth-protected-resource> and
<https://mcp.useflows.eu/.well-known/oauth-protected-resource>, read
2026-10-28.

### 9.4 Audit logs

| Fact | Value | Source |
|---|---|---|
| Visibility | Organization admins, under **Organization**, **Audit Logs**, with event-type filter and payload search | [D] [audit] |
| Entry fields | Event Type, Actor (with the session type for API key or OAuth2 sessions), Project (empty for organization actions), Metadata (JSON, varies by type), Date | [D] [audit] |
| Export | Every new entry is sent as one OTLP Logs record over HTTP to a configured endpoint (`http` or `https`), with optional headers and a max batch size of 1 to 1000; the last error is shown | [D] [audit] |
| Event type list and retention | Not documented | *(unverified)* |

## 10. The Flows API

Flows has no published API specification. It does have an HTTP API that the
docs call the Flows API, gated by the `api` capability, and its first-party
clients show its shape [D] [apikeys]; [D] [authz].

| Property | Value | Source |
|---|---|---|
| Style | RPC: every call is `POST <base><path>` with a JSON body | [S] [fc-api] L12-L24; [S] [prov-client] L12-L23 |
| Auth | `Authorization: Bearer <API key or OAuth access token>` | [S] [fc-auth] L333-L341; [S] [prov-client] L22 |
| Envelope | `{"data": ..., "error": "..."}`; a non-empty `error` is a failure; the provider also fails on a non-200 status | [S] [prov-client] L36-L50; [S] [fc-api] L26-L48 |
| Unauthenticated call | `401` with the body `Authorization header required` | [C] `POST https://useflows.eu/cli/projects` read 2026-10-28 |
| Families | `/cli/...` for `flowctl`, `/provider/...` for the Terraform provider; `/api/...` for browser flows (OIDC callback, OAuth authorize, registry subscription) | [S] [fc-api]; [S] [prov-docs]; [D] [oidc]; [D] [registries] |
| OAuth discovery | `/.well-known/oauth-protected-resource` on the app domain (resource `https://<domain>/stable_api`) and on `mcp.<domain>`; authorization server `oauth.<domain>` | [C] metadata URLs above |
| Health | `GET /health` on the server, gateway and agent | [S] [helm-server] L54-L59; [S] [helm-gateway] L60-L65; [S] [agent-deploy] L72-L77 |

### 10.1 Operations used by `flowctl`

| Path | Request fields | Response | Source |
|---|---|---|---|
| `/cli/projects` | none | `projects[{id, name}]` | [S] [fc-api] L123-L126 |
| `/cli/apps/list_apps` | `projectId` | `data.apps` | [S] [fc-api] L51-L55 |
| `/cli/apps/create_app` | `name`, `description`, `projectId`, `blockStyle{color, iconUrl}` | `data.app.id` | [S] [fc-api] L57-L75 |
| `/cli/apps/create_version` | `customAppId`, `version`, `code`, `uiCode` | `data.appVersion.id` | [S] [fc-api] L77-L90 |
| `/cli/apps/get_version` | `id` | `data.appVersion` (with `draft`) | [S] [fc-api] L92-L95 |
| `/cli/apps/update_version` | `id` with `code` and `uiCode`, or `draft: false` to publish | | [S] [fc-api] L97-L121 |
| `/cli/apps/list_versions` | `customAppId` | `data.versions[{id, version, draft}]` | [S] [fc-api] L104-L109 |
| `/cli/flows/list_flows` | `projectId` | `data.flows[{id, name}]` | [S] [fc-api] L128-L131 |
| `/cli/flows/export` | `flowId`, `omitIds`, `omitLayout`, `omitAppSchemas` | `data.data` (YAML) | [S] [fc-api] L133-L148 |

### 10.2 Operations used by the provider

Generated by script; 33 distinct paths.

| Path | Request fields (where read) | Source |
|---|---|---|
| `/provider/flows/create` | `projectId`, `name`, `eventLimit` returns `flow.id` | [S] [prov-flow-structs] |
| `/provider/flows/apply_config` | `flowId`, `definition`, `appInstallationMapping` | [S] [prov-flow-structs] |
| `/provider/flows/update` | `id`, `name`, `active`, `eventLimit` (null resets) | [S] [prov-flow-structs] |
| `/provider/flows/get` | `flowId` returns `name`, `active`, `eventLimit`, `blocks{name: {id}}` | [S] [prov-flow-structs] |
| `/provider/flows/delete` | `id` | [S] [prov-flow-structs] |
| `/provider/flows/plan_changes` | `flowId`, `definition`, `appInstallationMapping` returns `plan.operations[{type}]`, `readablePlan` | [S] [prov-flow-structs] |
| `/provider/flows/export_definition` | `flowId` returns `definition` | [S] [prov-flow-structs] |
| `/provider/flows/get_entity_lifecycle_status` | `entityId` returns `status` | [S] [prov-entity] |
| `/provider/flows/confirm_entity_lifecycle` | `id` | [S] [prov-entity] |
| `/provider/apps/create_installation` | `projectId`, `name`, `app`, `styleOverride` returns `id`, `draft` | [S] [prov-ai] |
| `/provider/apps/get_installation`, `get_installation_status`, `get_installation_config_field` | installation ID | [S] [prov-ai] |
| `/provider/apps/update_installation_config` | `id`, `configFields` (null removes a key) returns `draft` | [S] [prov-ai] |
| `/provider/apps/update_installation_metadata`, `update_installation_version` | installation ID and the changed part | [S] [prov-ai] |
| `/provider/apps/confirm_installation`, `delete_installation` | `id` | [S] [prov-ai] |
| `/provider/apps/get_version_id` | app name, registry, version | [S] [prov-appver] |
| `/provider/datatables/create`, `get`, `update`, `delete` | data table fields | [S] [prov-dt] |
| `/provider/datatables/create_datatable_column`, `get_datatable_column`, `update_datatable_column`, `delete_datatable_column` | column fields | [S] [prov-dtc] |
| `/provider/datatables/attach_to_flow`, `detach_from_flow` | data table and flow IDs | [S] [prov-dta] |
| `/provider/organization/create_secret`, `read_secret`, `update_secret`, `delete_secret` | project, key, value | [S] [prov-secret] |

### 10.3 MCP surfaces

| Surface | What it is | Source |
|---|---|---|
| Flows MCP server | Built in; lets an AI assistant build and change flows; at `https://mcp.useflows.eu`, `https://mcp.useflows.us` or `https://mcp.<domain>` self-hosted; access is granted per project through OAuth2 authorizations; assistants should call the `usage_instructions` tool and turn off lazy tool loading | [D] [mcp] |
| Flow MCP servers | Each flow with MCP Tool blocks is a remote MCP server whose tools are those blocks | [D] [ai-blocks] |

The Flows MCP server's tool list beyond `usage_instructions` is not
documented *(unverified)*. Its OAuth scopes are `mcp` plus the view and edit
scopes for flows, installations and data tables [C] MCP resource metadata
above.

### 10.4 Live OAuth authorization server metadata

`https://oauth.useflows.eu/.well-known/oauth-authorization-server`, read
2026-10-28 [C]: issuer `https://oauth.useflows.eu`; authorization endpoint
`https://useflows.eu/api/oauth/authorize`; token endpoint
`https://oauth.useflows.eu/oauth/token`; registration endpoint
`https://oauth.useflows.eu/oauth/register`; response type `code`; grants
`authorization_code` and `refresh_token`; PKCE `plain` and `S256`; token
endpoint auth `client_secret_post` and `client_secret_basic`; scopes
`profile`, `mcp`, `api`, `apps:viewer`, `apps:editor`, `apps:admin`. The
scope names `apps:viewer` and `apps:editor` differ from the documented
`apps:view`, and `flowctl` registers a public client (`token_endpoint_auth_method`
`none`) that the metadata does not list *(unverified)*.

## 11. flowctl

`flowctl` is the npm package `@useflows/flowctl` (0.7.0, MIT), a citty CLI
for custom app development and flow export [S] [fc-pkg]; [D] [flowctl-d].
Generated by script.

| Command | Arguments (alias, default) | Behaviour | Source |
|---|---|---|---|
| `flowctl auth login` | `--base-url` | Prompts for `https://useflows.eu`, `https://useflows.us` or a custom URL; runs OAuth2 discovery, dynamic registration (`client_name` `Spaceflows CLI`, redirect `http://127.0.0.1:<port>/callback`), PKCE S256 with `state`; stores the token | [S] [fc-auth-cmd] L13-L84; [S] [fc-auth] L94-L282 |
| `flowctl auth logout` | none | Deletes the stored token | [S] [fc-auth-cmd] L85-L95 |
| `flowctl auth token` | none | Prints a valid access token, refreshing it when it expires within 60 s | [S] [fc-auth-cmd] L96-L105; [S] [fc-auth] L319-L331 |
| `flowctl app create` | `--project` (`-p`) | Prompts for name, description, block colour (`#fff`) and icon path (`/static/empty.svg`); prints the app ID | [S] [fc-app] |
| `flowctl version create` | `--entrypoint` (`-e`, required), `--uiEntrypoint` (`-u`), `--app` (`-a`), `--project` (`-p`), `--version`, `--debug` (`-d`, false) | Builds and uploads a draft; without `--version` it offers a major, minor or patch bump of the highest version (default minor), or `1.0.0` for the first | [S] [fc-version] L18-L144 |
| `flowctl version update` | `--id`, `--entrypoint` (`-e`, required), `--uiEntrypoint` (`-u`), `--project` (`-p`), `--app` (`-a`), `--watch` (`-w`, false) | Rebuilds and replaces a draft's code; refuses a non-draft; `--watch` rebuilds on file changes with a 200 ms debounce | [S] [fc-version] L188-L308 |
| `flowctl version publish` | `--id`, `--project` (`-p`), `--app` (`-a`) | Sets `draft: false`; the picker lists drafts only | [S] [fc-version] L144-L186; [S] [fc-shared] L82-L109 |
| `flowctl version list` | `--app` (`-a`), `--project` (`-p`) | Lists versions | [S] [fc-version] L309-L345 |
| `flowctl version bundle` | `--entrypoint` (`-e`, required), `--uiEntrypoint` (`-u`), `--output` (`-o`, `bundle.tar.gz`), `--debug` (`-d`) | Builds `app.js` and `ui.js` into a tarball, offline | [S] [fc-bundle] |
| `flowctl flow export` | `--id`, `--project` (`-p`), `--file` (`-f`), `--no-ids`, `--no-layout`, `--no-app-schemas` | Writes a flow's YAML to stdout or a file; without `--id` it needs a terminal to prompt; `--id` must be a UUID; the `--no-*` flags drop project-scoped IDs, canvas layout and inline app block schemas | [S] [fc-flow-export] |

| Setting | Value | Source |
|---|---|---|
| Token file | `~/.spaceflows/cli-token.json` with `access_token`, `refresh_token`, `expires_at`, `client_id`, `base_url`, `token_endpoint` | [S] [fc-auth] L11, L50-L57 |
| Requested scopes | `api apps:admin apps:view flows:view flows:edit` | [S] [fc-auth] L17 |
| `FLOWCTL_API_KEY` | API key auth, which overrides the stored token | [S] [fc-auth] L19-L40 |
| `FLOWCTL_BASE_URL` | Required with `FLOWCTL_API_KEY` | [S] [fc-auth] L19-L40 |

The docs list `-v` for `--version` and a `--ui-entrypoint` spelling; the
source declares no `-v` alias and names the flag `uiEntrypoint`
[D] [custom-apps]; [S] [fc-version] L24-L55. Whether citty accepts the
kebab-case spelling is *(unverified)*.

## 12. terraform-provider-flows

The provider (`spacelift-io/flows`, v0.7.0, MIT) is published to the OpenTofu
registry and works with Terraform through the full registry path
`registry.opentofu.org/spacelift-io/flows` [D] [tfprov-d]; [S] [prov-readme].
Attribute tables are generated by script
from the provider's generated docs.

### 12.1 Provider configuration

| Attribute | Type | Meaning | Source |
|---|---|---|---|
| `endpoint` | string, required | Flows base URL; `https` is assumed when no scheme is given | [S] [prov-provider] L39-L53, L74-L81 |
| `token` | string, sensitive, optional | API key or OAuth token; falls back to `FLOWS_TOKEN`, and configuration fails without either | [S] [prov-provider] L64-L72 |

The README asks for a key with editor access to the relevant projects and the
`api` and `flows:edit` capabilities, or a token from `flowctl auth token`
[S] [prov-readme].

### 12.2 Resources

| Resource | Attribute | Type | Kind | Meaning |
|---|---|---|---|---|
| `flows_flow` | `project_id` | string | required | Project |
| | `name` | string | required | Flow name |
| | `definition` | string | required | YAML definition (section 13) |
| | `app_installation_mapping` | map(string) | optional | App key to installation ID, filling or overriding IDs in the YAML |
| | `enabled` | bool | optional | Default true |
| | `event_limit` | number | optional | Monthly limit override |
| | `id` | string | read-only | Flow ID |
| | `blocks` | map of `{id}` | read-only | Block IDs by name |
| `flows_app_installation` | `project_id`, `name` | string | required | Project and installation name |
| | `app.version_id` | string | required | Selects the app and version |
| | `app.custom` | bool | optional | The version belongs to a custom app |
| | `config_fields` | map(string) | optional | Configuration values (JSON-encoded strings in the example) |
| | `style_override.color`, `style_override.icon_url` | string | optional | Block style |
| | `confirm` | bool | optional | Confirm a draft installation |
| | `wait_for_ready` | bool | optional | Wait for `ready` after confirming |
| | `id` | string | read-only | Installation ID |
| `flows_app_installation_config_field` | `app_installation_id`, `key` | string | required | One configuration key |
| | `value` | string | optional | `"null"` removes the key |
| `flows_app_installation_confirmation` | `app_installation_id` | string | required | Confirms the installation |
| | `wait_for_ready` | bool | optional | Wait for `ready` |
| | `status` | string | read-only | Final status |
| `flows_app_installation_wait_for_ready` | `app_installation_id` | string | required | Waits for `ready` |
| | `status` | string | read-only | Final status |
| `flows_entity_confirmation` | `entity_id` | string | required | Block (entity) UUID to confirm |
| | `status` | string | read-only | Final status |
| `flows_secret` | `project_id`, `key` | string | required | Secret identity |
| | `value` | string, sensitive, write-only | required | Secret value |
| | `id` | string | read-only | `project_id/key` |
| | `updated_at` | string | read-only | Last update time |
| `flows_data_table` | `project_id`, `name` | string | required | Data table |
| | `id` | string | read-only | Table ID |
| `flows_data_table_column` | `data_table_id`, `name`, `type` | string | required | Column; `type` is `boolean`, `datetime`, `float`, `integer`, `json`, `row_ref` or `string` |
| | `ref_table_id` | string | optional | Target table for `row_ref` |
| | `id` | string | read-only | Column ID |
| `flows_data_table_attachment` | `data_table_id`, `flow_id` | string | required | Attach a table to a flow |
| | `id` | string | read-only | Composite of the two IDs |

Source: [S] [prov-docs].

### 12.3 Data sources

| Data source | Attribute | Type | Kind | Meaning |
|---|---|---|---|---|
| `flows_app_version` | `name` | string | required | App name |
| | `registry` | string | optional | Registry to look in |
| | `version` | string | optional | Version; latest when omitted |
| | `id` | string | read-only | Version ID for `app.version_id` |
| `flows_data_table_columns` | `data_table_id` | string | required | Table |
| | `columns[]` | `{id, name, type, ref_table_id}` | read-only | Columns |

Source: [S] [prov-docs].

### 12.4 Behaviour

- **Semantic diff.** On read and plan, the provider sends the configured
  YAML to `plan_changes`; with no operations it keeps the stored definition,
  so formatting differences do not show as drift; with operations it plans
  the configured YAML and surfaces `readablePlan` as a warning
  [S] [prov-flow-plan].
- **Drift.** If the server's flow differs from state, or the plan call fails,
  the read replaces the definition with `export_definition`
  [S] [prov-flow-read].
- **Create.** `create`, then `apply_config`, then `update` for the enabled
  flag [S] [prov-flow-create].
- **Polling.** Installation confirmation polls status every 5 s up to 60
  times; entity confirmation does the same; `draft` and `in_progress` keep
  polling, `ready` succeeds, and `failed`, `drifted`, `draining`,
  `draining_failed` and `drained` fail [S] [prov-ai-status]; [S] [prov-entity].
- **Import.** `flows_flow` imports by flow ID; `flows_secret` imports by
  `project_id/key` in source, while its generated doc shows
  `$ORGANIZATION_ID/$SECRET_KEY`; the source wins [S] [prov-secret-import];
  [S] [prov-secret-doc].
- **Naming.** The docs call the lifecycle confirmation resource
  `flows_entity_lifecycle_confirmation`; the provider registers
  `flows_entity_confirmation`, which wins [D] [tfprov-d]; [S] [prov-entity].
- Agent pools have no resource; flows reference them by ID in the YAML
  `agentPools` map [I] from the resource list and [D] [defref].

## 13. The flow definition format

Flows have a YAML form, copied from the canvas (all blocks with nothing
selected, or a selection), exported by `flowctl flow export` or the provider,
and applied by the provider [D] [tfprov-d]; [S] [fc-flow-export]. It covers the
configuration layer only: lifecycle confirmation and the event limit are not
in it [D] [tfprov-d]; [D] [limits]. Spacelift blocks share `coreBlock:
spacelift` and pick the block with `kind`; changing `kind` recreates the
block [D] [tfprov-d].

The table is generated by script from the generated
reference page: 61 objects and 125 properties [D] [defref].

| Object | Property | Type |
|---|---|---|
| `Flow` | `appInstallations` | map of `AppInstallation` |
| | `dataTables` | map of `DataTable` |
| | `agentPools` | map of `AgentPool` |
| | `blocks` | array of `Block` |
| | `notes` | array of `Note` |
| `Block` | `name` | string |
| | `description` | string |
| | `alternativeLookupStrategy` | `BlockLookupStrategy` (`id` UUID or `name`) |
| | `type` | `BlockType` |
| | `position` | `Position` (`x`, `y`; written as `[x, y]` in examples) |
| | `config` | one of the block config objects below, by block type; `AppBlockConfig` and Spacelift config are maps of name to `Expression` |
| | `inputs` | `CoreBlockInputs` (`default`), `AppBlockInputs` or `SpaceliftBlockInputs` (map of socket to `{links, config}`), `ConcurrencyGroupBlockInputs` (`acquire`, `release`) or group IO |
| | `outputs` | map of output to `CoreBlockOutput` (`outputCustomization`, `sensitivePaths`) or group IO |
| | `errorHandler` | string (Handle Error block) |
| | `dynamicOutputKeys` | array of string |
| | `group` | string (enclosing group) |
| `Note` | `body` (Markdown), `position`, `style` (`width`, `height`, `color`), `alternativeLookupStrategy` (`id`) | |
| `AppInstallation` | `appName`, `appVersion`, `id` (UUID) | |
| `DataTable` | `id` (UUID), `name`, `schema.columns[]` (`name`, `type`, `dataTableRef`) | column types `string`, `integer`, `float`, `boolean`, `datetime`, `json`, `row_ref` |
| `AgentPool` | `name`, `id` (UUID) | |
| `BlockType` | `coreBlock` (enum in section 3.3), `kind`, or `appBlock` (`block`, `appInstallation`, `schema`) | |
| `BlockIOLink` | `block` (name), `output` (socket) | |
| `HTTPEndpointBlockConfig` | `path`, `methods`, `audience` (`world`, `organization`, `project`) | |
| `ScheduleBlockConfig` | `definition` (`type` `cron` or `frequency`; `cron.expression`, `cron.location`; `frequency.interval`, `frequency.unit` `seconds`, `minutes`, `hours`) | |
| `MCPToolBlockConfig` | `inputSchema` (map of name to `{required, type}` JSON Schema) | |
| `DataTable*Config` | `table`, plus `limit` for queries | |
| Input configs | `condition`; `sleep`; `url`, `method`, `body`, `headers`, `agentPool`; `statusCode`, `body`, `headers`; `items`, `emittedLimit`; `group`, `count`, `emit`, `item`, `delay`; `key`, `collection`, `value`, `ttl`; `collection`, `limit`, `startingKey`; `responseBody`; `groupKey`, `itemKey`; `row`, `ttl`, `rowID`, `rowIDs`, `filter`, `queryOrders`, `continuationToken`, `assignees` | per block, section 3.3 |
| `GroupBlockIOLinks` | `name`, `links`, `outputCustomization`, `sensitivePaths` | |
| `Expression` | a literal, or `!expr` JavaScript with `outputs`, `config` and `event` in scope | |

## 14. Self-hosted Flows

### 14.1 Components

| Component | Runs | Role | Ports | Source |
|---|---|---|---|---|
| server | `/usr/bin/spacelift-flows server` in `spacelift-flows-backend` | HTTP API, embedded frontend, OAuth and MCP endpoints, flow HTTP endpoints | `public` 8080 | [D] [sh-eks]; [S] [helm-server] L37-L46 |
| worker | `/usr/bin/spacelift-flows worker` | Recurring tasks and asynchronous jobs | none exposed | [D] [sh-eks]; [S] [helm-worker] L37-L42 |
| gateway | `/usr/bin/spacelift-flows gateway` | WebSocket server for agents; routes JavaScript evaluations to agents and runtimes | `http` 3000, `grpc` 6237, `public` 8080 | [D] [sh-eks]; [S] [helm-gateway] L37-L49 |
| agent | `/usr/bin/agent` in `spacelift-flows-agent` | Runs app code and expressions in Node.js runtimes; one or more pools | `public` 8080 (health) | [D] [agents]; [S] [agent-deploy] L25-L32 |
| runtime | `node:24-alpine` container, or a Kubernetes JS executor pod | Executes one Node.js runtime | | [D] [sh-adv]; [S] [agent-deploy] L54-L55 |

All three backend components use one image, take their configuration from
`CONFIG_FILE=/etc/flows/config.yaml` mounted from the Kubernetes secret, and
are probed on `/health` [S] [helm-server] L44-L59. The roles of the
gateway's `http` and `grpc` ports are not documented *(unverified)*. The EKS
guide says agents must run outside Kubernetes (on EC2), while the
`spacelift-flows-agent` chart runs agents in Kubernetes with a `kubernetes`
executor; whether that chart is supported for self-hosted installs is
*(unverified)* [D] [sh-eks]; [S] [agent-values].

### 14.2 Networking

| Host | Routed to | Purpose | Source |
|---|---|---|---|
| `<appDomain>` | server `public` | UI and API | [S] [helm-ingress] L17-L27 |
| `*.endpoints.<appDomain>` | server `public` | Flow HTTP endpoints | [S] [helm-ingress] L29-L38 |
| `mcp.<appDomain>` | server `public` | MCP | [S] [helm-ingress] L40-L49 |
| `oauth.<appDomain>` | server `public` | OAuth authorization server | [S] [helm-ingress] L51-L60 |
| `gateway.<appDomain>` | gateway `public` | Agent WebSockets; only with `ingress.exposeGateway` | [S] [helm-ingress] L61-L72 |

The ingress needs `ingress.className` and `appDomain`, and terminates TLS for
`appDomain` [S] [helm-ingress] L13-L16. The EKS module's ACM certificate
covers the apex, `*.endpoints`, `oauth`, `mcp` and, with `expose_gateway`,
`gateway` [S] [eks-main] L89-L100. DNS gets one CNAME per host to the load
balancer [D] [sh-eks]. The EKS ingress class uses EKS Auto Mode's ALB
controller, internet-facing on public subnets or internal on private
subnets [S] [eks-ingress]; [S] [eks-internal-ingress]. The database security
group admits TCP 5432 from the cluster's primary security group
[S] [eks-sg] L9-L19.

Outbound hosts a restricted network must allow [D] [sh-adv]:

| Destination | Purpose |
|---|---|
| `public.ecr.aws/w5z2f6e8/spacelift-flows-backend`, `public.ecr.aws/w5z2f6e8/spacelift-flows-agent` | Images |
| Docker Hub `node:24-alpine` | JavaScript runtimes |
| Anthropic API (`160.79.104.0/23`, `2607:6bc0::/48`) | The built-in AI assistant |
| `https://registry.useflows.com/core`, `https://registry.useflows.com/community`, `https://github.com` | App registries |

Proxies are set with `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` in each
component's `extraEnv`, and with the agent module's `http_proxy`; extra CAs
go in `custom_ca_certificates` on both modules [D] [sh-adv].

### 14.3 External dependencies

| Dependency | Required | Provided by the EKS module | Source |
|---|---|---|---|
| PostgreSQL | Yes (`databaseUrl`) | Aurora PostgreSQL 16.11, database and user `flows`, 7-day backups, KMS-encrypted, CloudWatch `postgresql` logs, instance `db.t4g.medium` by default; or an external URL. The guide shows reusing the self-hosted Spacelift RDS with a separate `flows` database | [S] [eks-db] L13-L37; [D] [sh-eks] |
| Object storage | Yes (`objectStorage.s3`) | One S3 bucket `flows-<hex>`, versioned, SSE-KMS, public access blocked; S3-compatible endpoints accepted by the config (`endpoint`, `insecure`, static keys) | [S] [eks-buckets] L6-L37; [S] [helm-secret] L49-L56 |
| KMS | Module default | One key with rotation, 7-day deletion window, usable by S3, RDS, SES, Secrets Manager and CloudWatch Logs | [S] [eks-kms] |
| Email | Recommended | SMTP (`tls`, `starttls`, `none`), Amazon SES (domain identity, DKIM, a `flows-emails-<hex>` bucket that a receipt rule fills under `emails/`), or dev mode that logs magic links | [S] [eks-tpl] L17-L36; [S] [eks-ses]; [D] [sh-eks] |
| Anthropic API key | Optional, recommended | `ai.anthropic.apiKey` for the AI assistant | [S] [eks-tpl] L9-L12; [D] [sh-eks] |
| OpenTelemetry collector | Optional | `openTelemetry.collectorEndpoint` and `environment` | [S] [eks-tpl] L4-L8 |
| Licence token | Yes | `selfHosted.licenseToken` | [S] [eks-tpl] L40-L46 |
| Kubernetes | Yes | EKS Auto Mode cluster (`general-purpose` node pool, version 1.34 by default) or an existing one | [S] [eks-eks]; [S] [eks-vars] L176-L198 |
| IAM | Yes | IRSA role for the service account with S3 object and bucket actions, KMS data-key actions and, with SES, send actions | [S] [eks-iam] |
| Container registry | Optional | Two ECR repositories, `spacelift-flows-<hex>` and `spacelift-flows-agent-<hex>` | [S] [eks-ecr] |

No message queue, MQTT broker or cache appears in the chart, the config or
the modules; the gateway carries agent traffic over WebSockets [I] from the
absence in [S] [helm-values], [S] [eks-tpl] and the module resources, and
[D] [agents].

### 14.4 `config.yaml`

Keys the EKS module writes into the `spacelift-flows-secret` secret
[S] [eks-tpl]; [S] [eks-outputs] L2-L50:

| Key | Source variable or value | Required |
|---|---|---|
| `appDomain` | `app_domain` | yes |
| `databaseUrl` | the module's Aurora URL or `database_connection_url` | yes |
| `openTelemetry.collectorEndpoint`, `openTelemetry.environment` | `opentelemetry_collector_endpoint`, `opentelemetry_environment` | when the endpoint is set |
| `ai.anthropic.apiKey` | `anthropic_api_key` | when set |
| `objectStorage.s3.bucket`, `objectStorage.s3.region` | module bucket, `aws_region` | yes |
| `email.dev.enabled` | `email_dev_enabled` | yes |
| `email.ses.region`, `email.ses.fromAddress` (`noreply@<domain>`), `email.ses.fromName` | with `enable_ses` | either |
| `email.smtp.host`, `port`, `username`, `password`, `fromAddress`, `fromName`, `encryption` | `smtp_*` | or |
| `defaultAgentPool.id`, `defaultAgentPool.token` | generated UUID and a 64-character lower-case alphanumeric token | yes |
| `selfHosted.organizationName`, `adminEmail`, `adminPassword`, `licenseToken` | matching variables | yes, password optional |
| `server.publicPort`, `server.internalPort` | `server_port` (8080) | yes |
| `additionalRootCas` | `custom_ca_certificates` | when set |
| `spaceliftInstances` | JSON map of Spacelift hostname to `{api_url}` | when set |
| `jwtSigningKeyPem` | base64 of `jwt_signing_key_pem` or a generated RSA 2048 key | yes |

The chart's example secret instead shows `server.listeners.http.port`,
`server.public.listeners` and `server.internal.listeners`, and S3 static
credentials with `endpoint` and `insecure` [S] [helm-secret]. Which server
listener form the current backend reads is *(unverified)*.

### 14.5 Helm chart `spacelift-flows`

Chart 0.4.0, appVersion 0.1.0 [S] [helm-chart]. Generated by
a script (60 keys) and condensed here.

| Key | Default | Meaning |
|---|---|---|
| `global.image.repository` | `public.ecr.aws/w5z2f6e8/spacelift-flows-backend` | Backend image |
| `global.image.tag`, `global.image.pullPolicy` | `latest`, `IfNotPresent` | The guide pins a release such as `1.1.0` |
| `global.imagePullSecrets` | unset | Used by the templates, absent from values |
| `global.nameOverride`, `global.fullnameOverride` | `""` | Naming |
| `serviceAccount.create`, `.name`, `.annotations` | `true`, `spacelift-flows`, `{}` | IRSA role annotation goes here |
| `secretName` | `spacelift-flows-secret` | Secret holding `config.yaml` |
| `appDomain` | `""` | Required for the ingress |
| `server.replicaCount` | 1 | |
| `server.service.type`, `.port`, `.targetPort`, `.annotations` | `ClusterIP`, 8080, 8080, `{}` | |
| `server.resources` | requests and limits 512m CPU, 1024Mi | |
| `server.lifecycle` | `{}` | Container lifecycle hooks, server only |
| `worker.replicaCount` | 1 | |
| `worker.resources` | requests and limits 1000m CPU, 2048Mi | |
| `gateway.replicaCount` | 1 | |
| `gateway.service.type`, `.ports.http`, `.ports.grpc`, `.ports.public`, `.annotations` | `ClusterIP`, 3000, 6237, 8080, `{}` | |
| `gateway.resources` | requests and limits 512m CPU, 1024Mi | |
| `<component>.nodeSelector`, `.tolerations`, `.affinity`, `.podAnnotations`, `.podSecurityContext`, `.securityContext`, `.extraEnv` | empty | For `server`, `worker` and `gateway` |
| `ingress.enabled` | `true` | |
| `ingress.className` | `""` | Required |
| `ingress.annotations` | `{}` | ALB scheme and health check in the guide |
| `ingress.exposeGateway` | `false` | Adds `gateway.<domain>`; the guide sets it to `true` |

Source: [S] [helm-values]. The chart README lists different defaults
(250m and 256Mi requests, image `spacelift-flows`) and `server.initMigration`
resources that no template uses; `values.yaml` and the templates win
[S] [helm-readme]; [S] [helm-values].

### 14.6 Helm chart `spacelift-flows-agent`

Chart 0.3.0, appVersion 0.1.0 [S] [agent-chart]. Generated by the same
script (40 keys) and condensed.

| Key | Default | Meaning |
|---|---|---|
| `image.repository`, `image.tag`, `image.pullPolicy` | `public.ecr.aws/w5z2f6e8/spacelift-flows-agent`, `agent-latest`, `IfNotPresent` | Agent image |
| `jsExecutorImage.repository`, `jsExecutorImage.tag` | same repository, `k8s-js-executor-latest` | Runtime pod image |
| `replicaCount` | 1 | Agents |
| `serviceAccount.create`, `.name`, `.annotations` | `true`, `spacelift-flows-agent`, `{}` | |
| `rbac.create` | `true` | Creates a Role and RoleBinding |
| `rbac.rules` | `pods` create, get, list, delete, watch; `pods/log` get, list; `pods/exec` create, get | Lets the agent manage runtime pods |
| `agent.gatewayEndpoint` | `""` | Required, `FLOWS_GATEWAY_ENDPOINT` |
| `agent.endpoint` | `""` | Required, `FLOWS_ENDPOINT` |
| `agent.secretName`, `agent.poolTokenKey`, `agent.poolIdKey` | `spacelift-flows-agentpool`, `token`, `pool-id` | Pool credentials secret |
| `agent.poolSize` | 3 | `FLOWS_POOL_SIZE` |
| `agent.executor` | `kubernetes` | `FLOWS_EXECUTOR` |
| `agent.kubernetes.namespace` | `default` | Runtime pod namespace |
| `agent.extraEnv` | `[]` | |
| `otel.enabled`, `otel.collectorEndpoint` | `false`, `http://otel-collector:4317` | `OTLP_COLLECTOR_ENDPOINT` |
| `resources` | requests 100m CPU, 256Mi; limits 500m, 512Mi | |
| `podAnnotations`, `podSecurityContext`, `securityContext`, `nodeSelector`, `tolerations`, `affinity` | empty | |

Source: [S] [agent-values]; [S] [agent-rbac]. The EKS module's
`agent_pool_secret_manifest` output writes exactly this secret, with keys
`token` and `pool-id` [S] [eks-outputs] L52-L75.

### 14.7 Module `terraform-aws-eks-spacelift-flows-selfhosted`

No licence; cited by file and line only. The inventory script counts 43 root
variables (63 with submodules) and 51 resource, data and module blocks.
Meanings below are this report's own summaries.

| Variable | Type | Default | Meaning |
|---|---|---|---|
| `aws_region` | string | `eu-central-1` | Region |
| `license_token` | string, sensitive | required | Flows licence |
| `app_domain` | string | required | Base domain |
| `k8s_namespace` | any | `default` | Namespace for generated secrets |
| `service_account_name` | string | `spacelift-flows` | IRSA trust subject |
| `organization_name`, `admin_email` | string | required | First organization and admin |
| `admin_password` | string, sensitive | `""` | Empty, or at least 32 characters |
| `server_port` | number | 8080 | Server ports in the config |
| `s3_retain_on_destroy` | bool | `true` | Keep buckets on destroy |
| `rds_delete_protection_enabled` | bool | `true` | RDS deletion protection |
| `rds_cluster_configuration` | object `{instance_identifier, instance_class}` | `flows-instance-1`, `db.t4g.medium` | Aurora instance |
| `enable_database` | bool | `true` | Create Aurora |
| `database_connection_url` | string, sensitive | `""` | External PostgreSQL URL |
| `database_security_group_ids` | list(string) | `[]` | Reuse security groups |
| `opentelemetry_collector_endpoint`, `opentelemetry_environment` | string | `""`, `spacelift-flows` | Telemetry |
| `anthropic_api_key` | string, sensitive | `""` | AI assistant |
| `email_dev_enabled` | bool | `false` | Log magic links instead of sending |
| `enable_ses` | bool | `false` | Use SES |
| `smtp_host`, `smtp_port`, `smtp_username`, `smtp_password`, `smtp_from_address`, `smtp_from_name`, `smtp_encryption` | various | `""`, 587, `""`, `""`, `""`, `Spacelift Flows`, `starttls` | SMTP; encryption is `tls`, `starttls` or `none` |
| `enable_vpc`, `vpc_id`, `private_subnet_ids`, `public_subnet_ids` | bool, string, lists | `true`, null, `[]`, `[]` | Create a VPC (10.0.0.0/16, two AZs, single NAT) or reuse one |
| `enable_eks_cluster`, `eks_cluster_name`, `eks_cluster_version` | bool, any, string | `true`, null, `1.34` | Create EKS Auto Mode or reuse |
| `cert_arn` | string | null | Existing certificate instead of ACM |
| `expose_gateway` | bool | `false` | Add `gateway.<domain>` to the certificate |
| `custom_ca_certificates` | string | `""` | Extra CAs |
| `enable_ecr`, `ecr_image_tag_mutability`, `ecr_scan_on_push`, `ecr_force_delete` | bool, string, bool, bool | `false`, `MUTABLE`, `true`, `false` | Private image mirror |
| `spacelift_instances` | map of `{api_url}` | `{}` | Spacelift Deploy instances Flows may link to |
| `jwt_signing_key_pem` | string, sensitive | null | JWT signing key; generated when null |

Source: [S] [eks-vars]. Resources it creates (by submodule): `network` (VPC
module), `kms` (key and alias), `buckets` (storage bucket with versioning,
encryption and public access block), `database` (subnet group, Aurora cluster
and instance, Secrets Manager secret `flows-db-credentials`), `ses` (domain
identity, DKIM, emails bucket with policy, receipt rule set, active rule set,
`store-emails` rule), `ecr` (two repositories), and at the root the EKS
module, the IRSA role and policies, the ACM certificate, the database
security group and ingress rule, the default agent pool UUID and token, and
the JWT key [S] [eks-main]; [S] [eks-iam]; [S] [eks-sg]; [S] [eks-db];
[S] [eks-ses]. Outputs are Kubernetes manifests to apply with `kubectl`
(`config_secret_manifest`, `agent_pool_secret_manifest`, `ingress_manifest`,
`internal_ingress_manifest`), a `shell` export block, cluster details, the
agent pool ID and token, VPC IDs, ECR details and the IRSA role ARN
[S] [eks-outputs].

### 14.8 Module `terraform-aws-spacelift-flows-agentpool-ec2`

No licence; cited by file and line only. 27 variables and 18 blocks by the
inventory script.

| Variable | Type | Default | Meaning |
|---|---|---|---|
| `name` | string | `flows` | Resource name prefix |
| `aws_region` | string | `eu-central-1` | Region |
| `backend_endpoint`, `gateway_endpoint` | string | required | `FLOWS_ENDPOINT`, `FLOWS_GATEWAY_ENDPOINT` |
| `agent_instance_type` | string | `c7i.xlarge` | amd64 or arm64; burstable families are discouraged because runtimes start often |
| `min_size`, `max_size`, `desired_capacity` | number | 1, 10, 2 | Auto Scaling group size |
| `ecr_repository_url`, `ecr_repository_arn` | string | public agent repository, null | Agent image source |
| `agent_image_tag`, `agent_image_tag_ssm_param` | string, object | `latest`, null | Tag, read at boot from an SSM parameter the module creates unless one is given |
| `create_credentials_secret`, `agent_pool_id`, `agent_pool_token`, `existing_credentials_secret_arn`, `credentials_secret_kms_key_arn` | | `true`, null, null, null, null | Pool credentials in Secrets Manager, created or reused |
| `reuse_vpc_id`, `reuse_vpc_subnet_ids` | | null | Existing VPC |
| `network_mode` | string | `private` | `private` (NAT) or `public` (public IPs, no NAT) |
| `additional_tags`, `tag_specifications` | map, list | `{}`, `[]` | Tags |
| `flows_docker_runtime_image` | string | null | Runtime image override |
| `custom_ca_certificates`, `http_proxy` | string | null | CAs and proxy |
| `cloudwatch_log_retention_days` | number | 7 | Log retention |
| `custom_userdata_inject` | string | null | Extra user-data shell |

Source: [S] [ec2-vars]; [S] [ec2-readme]. Behaviour:

- Instances boot the latest Spacelift worker AMI for the instance's
  architecture (`spacelift-<10 digits>-<arch>`) [S] [ec2-main] L36-L55.
- User data reads the pool credentials from Secrets Manager, pulls the agent
  image, copies `/usr/bin/agent` out of it to `/usr/bin/flows-agent` on the host and runs it with the
  `docker` executor and gVisor runtimes [S] [ec2-ud] L155-L210.
- A launch lifecycle hook holds new instances in `Pending:Wait` until the
  agent is alive (10-minute heartbeat, default `ABANDON`), and a terminate
  hook gives 5 minutes for shutdown (default `CONTINUE`)
  [S] [ec2-main] L240-L257.
- The terminate hook is named `<name>-agent-termination-hook`, while user data
  completes `flows-agent-termination-hook`, so the names only match with the
  default `name` [I] from [S] [ec2-main] L240-L247 and [S] [ec2-ud] L22-L29.
- Health checks are EC2 type with a 30 s grace period [S] [ec2-main] L185-L199.

### 14.9 Version and release notes

| Item | Value | Source |
|---|---|---|
| Backend image tag in the guide | `1.1.0` | [D] [sh-eks] |
| EKS module tag in the guide | `v1.1.0`; the pinned commit is after it | [D] [sh-eks]; [C] GitHub releases read 2026-10-28 |
| Agent module tag in the docs | `v1.2.0`; latest is `v1.3.0` | [D] [agents]; [C] GitHub releases read 2026-10-28 |
| Self-hosted Flows release notes | Not published in the mirror | *(unverified)* |

## 15. Spacelift Deploy integration

- **Linking.** A Deploy account admin (root space admin) opens Flows from
  the product switcher and either creates a Flows organization or signs in
  to an existing one; after linking, Deploy members can open Flows through
  the switcher and their Flows roles are managed in Flows [D] [deploy-setup].
- **Access to Deploy.** A Deploy admin grants each Flows project Deploy roles
  in chosen spaces under **Integrate Services**, **Flows**; a project's
  subscription blocks see events from any space where it has at least read
  access [D] [deploy-access]; [D] [deploy-blocks].
- **Self-hosted.** The Flows config carries `spaceliftInstances`, a map of
  Spacelift hostnames to optional API URLs, for linking to self-hosted
  Deploy [S] [eks-vars] L263-L269.

GraphQL types on the Deploy side [G]:

| Type | Fields |
|---|---|
| `FlowsInstance` | `redirectURL: String!`, `apiURL: String!` |
| `FlowsIntegration` | `organizationID: String!`, `organizationName: String!`, `baseURL: String!`, `createdAt: Int!`, `updatedAt: Int!` |
| `FlowsProject` | `id: ID!` (Flows project UUID), `name: String!`, `roleBindings: [FlowsProjectRoleBinding!]!` (only in spaces the caller administers) |
| `FlowsProjectRoleBinding` | `id: ID!`, `flowsProjectID: ID!`, `space: Space!`, `role: Role!` |
| `FlowsProjectRoleBindingInput` | `flowsProjectID: ID!`, `spaceID: ID!` (slug), `roleID: ID!` (ULID) |
| `FlowsProjectRoleBindingBatchInput` | `bindings: [FlowsProjectRoleBindingInput!]!` |
| `AuditTrailFlowsProjectRoleBindingMetadata` | `id`, `flowsProjectID`, `spaceID`, `roleULID`; audit resource type `FLOWS_PROJECT_ROLE_BINDING` |

| Operation | REST projection | Meaning |
|---|---|---|
| `flowsInstances` | `GET /flows-instances` | Flows deployments available for linking |
| `flowsIntegration` | `GET /flows-integration` | The account's link, if any |
| `flowsProjects` | `GET /flows-projects` | Projects of the linked organization with their bindings |
| `flowsProjectRoleBinding(id)` | `GET /flows-project-role-bindings/{id}` | One binding; admin on its space |
| `flowsProjectRoleBindingBatchCreate(input)` | `POST /flows-project-role-binding-batches` | Create bindings; admin on every target space |
| `flowsProjectRoleBindingDelete(id)` | `DELETE /flows-project-role-bindings/{id}` | Remove one binding |

Sources: [G]; the REST routes are from `spacelift.openapi.yaml` and are
inventoried in [04](04-platform-api-inventory.md). The linking mutation
itself is not in the schema *(unverified)*.

Spacelift Deploy blocks (`coreBlock: spacelift`) [D] [deploy-blocks]:

| Block | `kind` | Behaviour |
|---|---|---|
| Run Events | `runEvents` | Subscribe to run state changes, filtered by stack, run type and `states`; output `runs` |
| Notifications | *(unverified)* | Subscribe to Deploy system notifications such as VCS errors |
| Trigger Run | `triggerRun` | Start a run, report progress and completion; input `trigger` with `stackId` |
| Trigger Run Batch | *(unverified)* | Start tracked runs on several stacks in dependency order, with batched progress |
| Trigger Task | *(unverified)* | Run a task on a stack and report completion |
| Confirm Run | *(unverified)* | Confirm an unconfirmed run |
| Review Run | *(unverified)* | Approve or reject for approval policies |
| Deploy Template | `deployTemplate` | Deploy a template; its form inputs become config fields |
| Deploy Blueprint | *(unverified)* | Create stacks from a blueprint; its inputs become config fields |
| GraphQL Request | *(unverified)* | Run any GraphQL query; variables become config fields |

`kind` values are from the YAML examples [D] [tfprov-d]; the others are not
shown in any source. The same Deploy actions also exist as the MIT
`flows-app-spacelift` app [S] [app-spacelift].

## What this means for infraweave

- The plan needs a Flows domain separate from Deploy: organizations,
  projects, flows, blocks, links, notes, app installations, secrets, data
  tables, agent pools, teams, service accounts and registry subscriptions,
  each with UUID identity.
- The event engine must model events with primary and secondary parents,
  invocations derived from lineage, the four invocation statuses, 60-day
  retention, per-flow monthly and hourly event limits with their failure
  modes, manual retry and re-emit, echo and pending events.
- Every core block in section 3.3, including the undocumented Data Table,
  Form, Group and Concurrency Group blocks, needs an owner and a behaviour
  source.
- Expressions are JavaScript; the plan must account for a sandboxed
  JavaScript runtime with `outputs`, `signals`, `secrets`, `stored`, `items`
  and `group` in scope.
- App compatibility means implementing the `@slflows/sdk/v1` surface in
  section 7 (services, handler inputs, lifecycle callbacks, the seven
  lifecycle states and the `drifted` value of `blocks.list`) and accepting `app.js` and `ui.js` bundles and the
  registry JSON formats.
- Execution happens on agents that dial a gateway over WebSockets and run
  Node.js runtimes in Docker or Kubernetes, with public and private pools,
  pool tokens and per-project exposure.
- Access control needs organization and project roles (Consumer, Viewer,
  Editor, Admin), authorizations with capabilities, project access and MCP
  flow access, OAuth2 with dynamic registration and PKCE, OIDC with domain
  verification, FIDO2 two-factor and an audit log with OTLP export.
- Spacelift's Flows API is a private RPC surface; infraweave's REST API has
  no upstream contract to mirror for it, while `flowctl` and the provider
  define the operations a client needs (sections 10 to 12).
- Two MCP surfaces are in scope: a platform MCP server and per-flow MCP
  servers built from MCP Tool blocks.
- Self-hosting is three backend processes (server, worker, gateway) plus
  agents, with PostgreSQL, one object storage bucket, optional SMTP or SES,
  an optional Anthropic key and optional OpenTelemetry, and no queue or
  broker.
- The Deploy link (project role bindings into spaces and the Deploy blocks)
  ties report 14's RBAC to Flows.

## Inferred and undocumented

| Item | Basis |
|---|---|
| The SDK is a types-only contract implemented by the runtime | The package has only `index.d.ts`, and `flowctl` leaves `@slflows/sdk/*` external |
| Docs and self-hosted Flows docs are the same pages | A recursive diff of the mirror directories |
| Flows has no Deploy changelog entries | Search of `product/changelog.md` |
| Example IDs are UUIDv7 | Version nibble of the documented IDs |
| No queue or MQTT broker in self-hosted Flows | Absent from the chart, config and modules; agents use the gateway WebSocket |
| The EC2 terminate hook only completes with the default `name` | Hook name built from `var.name` against a fixed name in user data |
| The provider's `event_limit` contradicts the docs' claim that limits are unmanaged by the provider | `eventLimit` in the create and update requests |
| Agent pools are referenced but not managed by the provider | Resource list and the `agentPools` map |
| Pricing and tier of Flows | Not stated anywhere *(unverified)* |
| Audit event types and retention | Not documented *(unverified)* |
| Flows MCP server tools beyond `usage_instructions` | Not documented *(unverified)* |
| `apps:viewer` and `apps:editor` scopes, and public-client registration | Live metadata differs from docs and `flowctl` *(unverified)* |
| `--ui-entrypoint` and `-v` in `flowctl` | Docs differ from source *(unverified)* |
| Organization-wide custom apps through `flowctl` | CLI always sends a project *(unverified)* |
| Kubernetes-hosted agents for self-hosted | Chart exists, guide says EC2 only *(unverified)* |
| Server listener keys in `config.yaml` | Chart example and module template differ *(unverified)* |
| Gateway `http` and `grpc` port roles | Not documented *(unverified)* |
| Spacelift block `kind` values other than three | Not shown *(unverified)* |
| The Deploy linking operation | Not in the schema *(unverified)* |
| Self-hosted Flows release notes | Not in the mirror *(unverified)* |

## Versions at the research baseline

| Component | Version or commit | Licence |
|---|---|---|
| docs.spacelift.io `flows/` | read 2026-10-28 | none stated |
| Introspection snapshot | `b3255fe38f5e` | |
| `@slflows/sdk` | 0.14.0 (npm), tarball SHA-256 `2fa0135a6886f40a20933af123d0db02fdc6a7d2c3001e2fe44fc209835e85d3` | none declared |
| flowctl (`@useflows/flowctl`) | 0.7.0, `3e7d42dabb01` | MIT |
| terraform-provider-flows | v0.7.0, `9d1a7ad4d937` | MIT |
| spacelift-helm-charts (`spacelift-flows` 0.4.0, `spacelift-flows-agent` 0.3.0) | `4e02e1d22e73` | MIT |
| terraform-aws-eks-spacelift-flows-selfhosted | `b7d32fa51c7e` (after v1.1.0) | none |
| terraform-aws-spacelift-flows-agentpool-ec2 | v1.3.0, `6bb853a4d0a1` | none |
| `flows-app-*` and registry repositories | section 6.5 | MIT except `flows-app-utilities` (none) |
| Flows backend image in the guide | `1.1.0` | |

## Sources

Docs pages, read 2026-10-28:

- [home]: <https://docs.spacelift.io/>
- [intro]: <https://docs.spacelift.io/flows/introduction>
- [llms-flows]: <https://docs.spacelift.io/llms-flows.txt>
- [blockmgmt]: <https://docs.spacelift.io/flows/core-concepts-and-ui/block-management>
- [limits]: <https://docs.spacelift.io/flows/core-concepts-and-ui/event-limits>
- [canvas]: <https://docs.spacelift.io/flows/core-concepts-and-ui/flow-canvas-interface-overview>
- [nav]: <https://docs.spacelift.io/flows/core-concepts-and-ui/flow-navigation-and-organization>
- [inv]: <https://docs.spacelift.io/flows/core-concepts-and-ui/invocations>
- [mcp]: <https://docs.spacelift.io/flows/core-concepts-and-ui/mcp-server>
- [authz]: <https://docs.spacelift.io/flows/access-control/api-keys-and-oauth2>
- [audit]: <https://docs.spacelift.io/flows/access-control/audit-logs>
- [perms]: <https://docs.spacelift.io/flows/access-control/managing-permissions>
- [apikeys]: <https://docs.spacelift.io/flows/authentication-and-sso/api-keys>
- [oidc]: <https://docs.spacelift.io/flows/authentication-and-sso/oidc>
- [sa]: <https://docs.spacelift.io/flows/authentication-and-sso/service-accounts>
- [2fa]: <https://docs.spacelift.io/flows/authentication-and-sso/two-factor>
- [ai-blocks]: <https://docs.spacelift.io/flows/core-blocks-reference/ai-blocks>
- [bl-blocks]: <https://docs.spacelift.io/flows/core-blocks-reference/business-logic-blocks>
- [coll-blocks]: <https://docs.spacelift.io/flows/core-blocks-reference/collections-blocks>
- [err-block]: <https://docs.spacelift.io/flows/core-blocks-reference/error-block>
- [http-blocks]: <https://docs.spacelift.io/flows/core-blocks-reference/http-blocks>
- [mem-blocks]: <https://docs.spacelift.io/flows/core-blocks-reference/memory-blocks>
- [time-blocks]: <https://docs.spacelift.io/flows/core-blocks-reference/time-blocks>
- [io]: <https://docs.spacelift.io/flows/configuration-and-expressions/input-output-event-data>
- [signals]: <https://docs.spacelift.io/flows/configuration-and-expressions/references-and-signals>
- [secrets]: <https://docs.spacelift.io/flows/configuration-and-expressions/secrets>
- [sensitive]: <https://docs.spacelift.io/flows/configuration-and-expressions/sensitive-values>
- [apps-ov]: <https://docs.spacelift.io/flows/app-integrations/apps-overview>
- [app-inst]: <https://docs.spacelift.io/flows/app-integrations/app-installation>
- [apps]: <https://docs.spacelift.io/flows/apps>
- [dev]: <https://docs.spacelift.io/flows/developers>
- [dev-start]: <https://docs.spacelift.io/flows/developers/building-apps>
- [sdk-config]: <https://docs.spacelift.io/flows/developers/building-apps/configuration>
- [sdk-events]: <https://docs.spacelift.io/flows/developers/building-apps/events>
- [sdk-http]: <https://docs.spacelift.io/flows/developers/building-apps/http>
- [sdk-kv]: <https://docs.spacelift.io/flows/developers/building-apps/kv-storage>
- [sdk-lc]: <https://docs.spacelift.io/flows/developers/building-apps/lifecycle>
- [sdk-msg]: <https://docs.spacelift.io/flows/developers/building-apps/messaging>
- [sdk-sched]: <https://docs.spacelift.io/flows/developers/building-apps/scheduling>
- [registries]: <https://docs.spacelift.io/flows/developers/deploying-apps/app-registries>
- [custom-apps]: <https://docs.spacelift.io/flows/developers/deploying-apps/custom-apps>
- [defref]: <https://docs.spacelift.io/flows/automating-flows/flow-definition-reference>
- [flowctl-d]: <https://docs.spacelift.io/flows/automating-flows/flowctl>
- [tfprov-d]: <https://docs.spacelift.io/flows/automating-flows/flows-tf-provider>
- [quick]: <https://docs.spacelift.io/flows/getting-started/quick-start>
- [agents]: <https://docs.spacelift.io/flows/private-agents/private-agents>
- [selfhost]: <https://docs.spacelift.io/flows/self-hosting>
- [sh-adv]: <https://docs.spacelift.io/flows/self-hosting/advanced-settings>
- [sh-eks]: <https://docs.spacelift.io/flows/self-hosting/deploy-to-eks>
- [deploy-access]: <https://docs.spacelift.io/flows/spacelift-deploy-integration/flows-spacelift-access>
- [deploy-setup]: <https://docs.spacelift.io/flows/spacelift-deploy-integration/setup>
- [deploy-blocks]: <https://docs.spacelift.io/flows/spacelift-deploy-integration/spacelift-blocks>
- [templates-batch]: <https://docs.spacelift.io/flows/templates/spacelift-staged-batch-rollout>

Packages and source:

- [sdk]: <https://unpkg.com/@slflows/sdk@0.14.0/dist/v1/index.d.ts>
- [sdk-npm]: <https://www.npmjs.com/package/@slflows/sdk/v/0.14.0>
- [fc-pkg]: <https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/package.json>
- [fc-readme]: <https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/README.md#L19-L26>
- [fc-api]: <https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/src/api.ts>
- [fc-auth]: <https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/src/auth.ts>
- [fc-auth-login]: <https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/src/commands/auth.ts#L39-L46>
- [fc-auth-cmd]: <https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/src/commands/auth.ts>
- [fc-app]: <https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/src/commands/app.ts>
- [fc-flow-export]: <https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/src/commands/flow.ts>
- [fc-version]: <https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/src/commands/version.ts>
- [fc-bundle]: <https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/src/commands/version.ts#L347-L466>
- [fc-build]: <https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/src/build.ts>
- [fc-shared]: <https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/src/shared.ts>
- [prov-readme]: <https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/README.md>
- [prov-docs]: <https://github.com/spacelift-io/terraform-provider-flows/tree/9d1a7ad4d937/docs>
- [prov-client]: <https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/client.go>
- [prov-provider]: <https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/provider.go>
- [prov-flow-schema]: <https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/docs/resources/flow.md>
- [prov-flow-structs]: <https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/flow_resource.go#L175-L278>
- [prov-flow-create]: <https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/flow_resource.go#L125-L160>
- [prov-flow-read]: <https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/flow_resource.go#L247-L270>
- [prov-flow-plan]: <https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/flow_resource.go#L320-L370>
- [prov-ai]: <https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/app_installation_resource.go#L29-L43>
- [prov-ai-status]: <https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/app_installation_resource.go#L695-L730>
- [prov-appver]: <https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/app_version_datasource.go>
- [prov-dt]: <https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/data_table_resource.go>
- [prov-dtc]: <https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/data_table_column_resource.go>
- [prov-dta]: <https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/data_table_attachment_resource.go>
- [prov-entity]: <https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/entity_confirmation_resource.go>
- [prov-secret]: <https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/secret_resource.go>
- [prov-secret-import]: <https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/secret_resource.go#L233-L247>
- [prov-secret-doc]: <https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/docs/resources/secret.md#L43-L47>
- [helm-chart]: <https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows/Chart.yaml>
- [helm-values]: <https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows/values.yaml>
- [helm-readme]: <https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows/README.md>
- [helm-secret]: <https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows/example-secret.yaml>
- [helm-server]: <https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows/templates/server-deployment.yaml>
- [helm-worker]: <https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows/templates/worker-deployment.yaml>
- [helm-gateway]: <https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows/templates/gateway-deployment.yaml>
- [helm-ingress]: <https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows/templates/ingress.yaml>
- [agent-chart]: <https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows-agent/Chart.yaml>
- [agent-values]: <https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows-agent/values.yaml>
- [agent-deploy]: <https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows-agent/templates/deployment.yaml>
- [agent-rbac]: <https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows-agent/templates/rbac.yaml>
- [eks-main]: <https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/main.tf>
- [eks-vars]: <https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/variables.tf>
- [eks-tpl]: <https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/config.yaml.tftpl>
- [eks-outputs]: <https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/outputs.tf>
- [eks-eks]: <https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/eks.tf#L5-L31>
- [eks-iam]: <https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/iam.tf>
- [eks-sg]: <https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/security_group.tf>
- [eks-ingress]: <https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/ingress.yaml.tftpl>
- [eks-internal-ingress]: <https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/internal_ingress.yaml.tftpl>
- [eks-db]: <https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/modules/database/main.tf>
- [eks-buckets]: <https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/modules/buckets/main.tf>
- [eks-kms]: <https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/modules/kms/main.tf>
- [eks-ses]: <https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/modules/ses/main.tf>
- [eks-ecr]: <https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/modules/ecr/main.tf>
- [ec2-vars]: <https://github.com/spacelift-io/terraform-aws-spacelift-flows-agentpool-ec2/blob/6bb853a4d0a1/variables.tf>
- [ec2-main]: <https://github.com/spacelift-io/terraform-aws-spacelift-flows-agentpool-ec2/blob/6bb853a4d0a1/main.tf>
- [ec2-ud]: <https://github.com/spacelift-io/terraform-aws-spacelift-flows-agentpool-ec2/blob/6bb853a4d0a1/user_data.tftpl>
- [ec2-readme]: <https://github.com/spacelift-io/terraform-aws-spacelift-flows-agentpool-ec2/blob/6bb853a4d0a1/README.md>
- [app-ld-claude]: <https://github.com/spacelift-io/flows-app-launchdarkly/blob/b1d04642d657/CLAUDE.md>
- [app-spacelift]: <https://github.com/spacelift-flows-apps/flows-app-spacelift/tree/952465d7badc>
- [utils-blocks]: <https://github.com/spacelift-flows-apps/flows-app-utilities/tree/fdbc652b98b6/blocks>
- [core-reg-readme]: <https://github.com/spacelift-flows-apps/flows-core-registry/blob/bcd2033a5c2f/README.md>
- [tpl-core-ci]: <https://github.com/spacelift-flows-apps/flows-app-template-core/tree/5d55723b17cc/.github/workflows>

Live captures, read 2026-10-28: the OAuth metadata URLs in section 9.3 and
10.4, the Core registry manifest in section 6.2, an unauthenticated
`POST /cli/projects`, npm registry metadata for `@slflows/sdk` and
`@useflows/flowctl`, and the GitHub repository listings of `spacelift-io`
and `spacelift-flows-apps`.

[home]: https://docs.spacelift.io/
[intro]: https://docs.spacelift.io/flows/introduction
[llms-flows]: https://docs.spacelift.io/llms-flows.txt
[blockmgmt]: https://docs.spacelift.io/flows/core-concepts-and-ui/block-management
[limits]: https://docs.spacelift.io/flows/core-concepts-and-ui/event-limits
[canvas]: https://docs.spacelift.io/flows/core-concepts-and-ui/flow-canvas-interface-overview
[nav]: https://docs.spacelift.io/flows/core-concepts-and-ui/flow-navigation-and-organization
[inv]: https://docs.spacelift.io/flows/core-concepts-and-ui/invocations
[mcp]: https://docs.spacelift.io/flows/core-concepts-and-ui/mcp-server
[authz]: https://docs.spacelift.io/flows/access-control/api-keys-and-oauth2
[audit]: https://docs.spacelift.io/flows/access-control/audit-logs
[perms]: https://docs.spacelift.io/flows/access-control/managing-permissions
[apikeys]: https://docs.spacelift.io/flows/authentication-and-sso/api-keys
[oidc]: https://docs.spacelift.io/flows/authentication-and-sso/oidc
[sa]: https://docs.spacelift.io/flows/authentication-and-sso/service-accounts
[2fa]: https://docs.spacelift.io/flows/authentication-and-sso/two-factor
[ai-blocks]: https://docs.spacelift.io/flows/core-blocks-reference/ai-blocks
[bl-blocks]: https://docs.spacelift.io/flows/core-blocks-reference/business-logic-blocks
[coll-blocks]: https://docs.spacelift.io/flows/core-blocks-reference/collections-blocks
[err-block]: https://docs.spacelift.io/flows/core-blocks-reference/error-block
[http-blocks]: https://docs.spacelift.io/flows/core-blocks-reference/http-blocks
[mem-blocks]: https://docs.spacelift.io/flows/core-blocks-reference/memory-blocks
[time-blocks]: https://docs.spacelift.io/flows/core-blocks-reference/time-blocks
[io]: https://docs.spacelift.io/flows/configuration-and-expressions/input-output-event-data
[signals]: https://docs.spacelift.io/flows/configuration-and-expressions/references-and-signals
[secrets]: https://docs.spacelift.io/flows/configuration-and-expressions/secrets
[sensitive]: https://docs.spacelift.io/flows/configuration-and-expressions/sensitive-values
[apps-ov]: https://docs.spacelift.io/flows/app-integrations/apps-overview
[app-inst]: https://docs.spacelift.io/flows/app-integrations/app-installation
[apps]: https://docs.spacelift.io/flows/apps
[dev]: https://docs.spacelift.io/flows/developers
[dev-start]: https://docs.spacelift.io/flows/developers/building-apps
[sdk-config]: https://docs.spacelift.io/flows/developers/building-apps/configuration
[sdk-events]: https://docs.spacelift.io/flows/developers/building-apps/events
[sdk-http]: https://docs.spacelift.io/flows/developers/building-apps/http
[sdk-kv]: https://docs.spacelift.io/flows/developers/building-apps/kv-storage
[sdk-lc]: https://docs.spacelift.io/flows/developers/building-apps/lifecycle
[sdk-msg]: https://docs.spacelift.io/flows/developers/building-apps/messaging
[sdk-sched]: https://docs.spacelift.io/flows/developers/building-apps/scheduling
[registries]: https://docs.spacelift.io/flows/developers/deploying-apps/app-registries
[custom-apps]: https://docs.spacelift.io/flows/developers/deploying-apps/custom-apps
[defref]: https://docs.spacelift.io/flows/automating-flows/flow-definition-reference
[flowctl-d]: https://docs.spacelift.io/flows/automating-flows/flowctl
[tfprov-d]: https://docs.spacelift.io/flows/automating-flows/flows-tf-provider
[quick]: https://docs.spacelift.io/flows/getting-started/quick-start
[agents]: https://docs.spacelift.io/flows/private-agents/private-agents
[selfhost]: https://docs.spacelift.io/flows/self-hosting
[sh-adv]: https://docs.spacelift.io/flows/self-hosting/advanced-settings
[sh-eks]: https://docs.spacelift.io/flows/self-hosting/deploy-to-eks
[deploy-access]: https://docs.spacelift.io/flows/spacelift-deploy-integration/flows-spacelift-access
[deploy-setup]: https://docs.spacelift.io/flows/spacelift-deploy-integration/setup
[deploy-blocks]: https://docs.spacelift.io/flows/spacelift-deploy-integration/spacelift-blocks
[templates-batch]: https://docs.spacelift.io/flows/templates/spacelift-staged-batch-rollout
[sdk]: https://unpkg.com/@slflows/sdk@0.14.0/dist/v1/index.d.ts
[sdk-npm]: https://www.npmjs.com/package/@slflows/sdk/v/0.14.0
[fc-pkg]: https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/package.json
[fc-readme]: https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/README.md#L19-L26
[fc-api]: https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/src/api.ts
[fc-auth]: https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/src/auth.ts
[fc-auth-login]: https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/src/commands/auth.ts#L39-L46
[fc-auth-cmd]: https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/src/commands/auth.ts
[fc-app]: https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/src/commands/app.ts
[fc-flow-export]: https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/src/commands/flow.ts
[fc-version]: https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/src/commands/version.ts
[fc-bundle]: https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/src/commands/version.ts#L347-L466
[fc-build]: https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/src/build.ts
[fc-shared]: https://github.com/spacelift-io/flowctl/blob/3e7d42dabb01/src/shared.ts
[prov-readme]: https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/README.md
[prov-docs]: https://github.com/spacelift-io/terraform-provider-flows/tree/9d1a7ad4d937/docs
[prov-client]: https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/client.go
[prov-provider]: https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/provider.go
[prov-flow-schema]: https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/docs/resources/flow.md
[prov-flow-structs]: https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/flow_resource.go#L175-L278
[prov-flow-create]: https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/flow_resource.go#L125-L160
[prov-flow-read]: https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/flow_resource.go#L247-L270
[prov-flow-plan]: https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/flow_resource.go#L320-L370
[prov-ai]: https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/app_installation_resource.go#L29-L43
[prov-ai-status]: https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/app_installation_resource.go#L695-L730
[prov-appver]: https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/app_version_datasource.go
[prov-dt]: https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/data_table_resource.go
[prov-dtc]: https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/data_table_column_resource.go
[prov-dta]: https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/data_table_attachment_resource.go
[prov-entity]: https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/entity_confirmation_resource.go
[prov-secret]: https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/secret_resource.go
[prov-secret-import]: https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/internal/provider/secret_resource.go#L233-L247
[prov-secret-doc]: https://github.com/spacelift-io/terraform-provider-flows/blob/9d1a7ad4d937/docs/resources/secret.md#L43-L47
[helm-chart]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows/Chart.yaml
[helm-values]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows/values.yaml
[helm-readme]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows/README.md
[helm-secret]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows/example-secret.yaml
[helm-server]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows/templates/server-deployment.yaml
[helm-worker]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows/templates/worker-deployment.yaml
[helm-gateway]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows/templates/gateway-deployment.yaml
[helm-ingress]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows/templates/ingress.yaml
[agent-chart]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows-agent/Chart.yaml
[agent-values]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows-agent/values.yaml
[agent-deploy]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows-agent/templates/deployment.yaml
[agent-rbac]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-flows-agent/templates/rbac.yaml
[eks-main]: https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/main.tf
[eks-vars]: https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/variables.tf
[eks-tpl]: https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/config.yaml.tftpl
[eks-outputs]: https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/outputs.tf
[eks-eks]: https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/eks.tf#L5-L31
[eks-iam]: https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/iam.tf
[eks-sg]: https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/security_group.tf
[eks-ingress]: https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/ingress.yaml.tftpl
[eks-internal-ingress]: https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/internal_ingress.yaml.tftpl
[eks-db]: https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/modules/database/main.tf
[eks-buckets]: https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/modules/buckets/main.tf
[eks-kms]: https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/modules/kms/main.tf
[eks-ses]: https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/modules/ses/main.tf
[eks-ecr]: https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted/blob/b7d32fa51c7e/modules/ecr/main.tf
[ec2-vars]: https://github.com/spacelift-io/terraform-aws-spacelift-flows-agentpool-ec2/blob/6bb853a4d0a1/variables.tf
[ec2-main]: https://github.com/spacelift-io/terraform-aws-spacelift-flows-agentpool-ec2/blob/6bb853a4d0a1/main.tf
[ec2-ud]: https://github.com/spacelift-io/terraform-aws-spacelift-flows-agentpool-ec2/blob/6bb853a4d0a1/user_data.tftpl
[ec2-readme]: https://github.com/spacelift-io/terraform-aws-spacelift-flows-agentpool-ec2/blob/6bb853a4d0a1/README.md
[app-ld-claude]: https://github.com/spacelift-io/flows-app-launchdarkly/blob/b1d04642d657/CLAUDE.md
[app-spacelift]: https://github.com/spacelift-flows-apps/flows-app-spacelift/tree/952465d7badc
[utils-blocks]: https://github.com/spacelift-flows-apps/flows-app-utilities/tree/fdbc652b98b6/blocks
[core-reg-readme]: https://github.com/spacelift-flows-apps/flows-core-registry/blob/bcd2033a5c2f/README.md
[tpl-core-ci]: https://github.com/spacelift-flows-apps/flows-app-template-core/tree/5d55723b17cc/.github/workflows
