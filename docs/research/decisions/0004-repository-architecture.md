# 0004: Repository architecture and process topology

Status: Accepted

## Context

infraweave is one Go module that serves Spacelift's feature set behind a
REST API ([0001](0001-rest-first-spacelift-surface.md)). Today it holds a
served skeleton: the bundled `deploy` OpenAPI document, a generated strict
chi server whose operations answer 501 through `UnimplementedStrict`, a
generated client in `pkg/client/deploy`, problem details in
`internal/server/problem`, and the tooling that builds the Spacelift
reference (`internal/specref`, `internal/openapibundle`, `internal/stubgen`,
`internal/docscheck`). `cmd/` has no binary yet.

The scope ([report 26](../reference/26-scope-and-tiers.md#14-provisional-phase-spine))
grows that skeleton into some twenty business domains: identity, spaces,
stacks, configuration, runs, workers, state, VCS, policies, cloud
credentials, schedules, the module and provider registry, events, vendors,
templates, resources, Intelligence and Flows. Four properties of those
domains decide the shape of the repository:

- **A run transition is a consistency unit.** It changes the run, its
  transition record, the stack lock, the queue item that moves it on, the
  policy receipts that justified it and the event other parts hear
  ([report 08](../reference/08-run-lifecycle.md#4-queueing-the-stack-lock-and-scheduling),
  [report 09](../reference/09-policies.md#8-where-each-type-is-evaluated)).
- **Policies hook into many state points** (push, approval, plan, trigger,
  notification), so the policy engine is called by runs, VCS, access and
  events, and an import cycle is easy to create.
- **Workers run on customer hosts with outbound HTTPS only**
  ([report 10](../reference/10-workers-and-execution.md#42-transports)), so
  worker-side code must not depend on server-side persistence.
- **A Flows organization is an infraweave account**, and Flows keeps its own
  projects and UUIDv7 identifiers, and Intelligence reads across every domain
  ([report 23](../reference/23-flows.md#2-resource-hierarchy),
  [report 28](../reference/28-data-model.md#what-this-means-for-infraweave)).

Spacelift's own self-hosted backend runs every process from one binary
with subcommands, sharing one PostgreSQL database
([report 24](../reference/24-self-hosted-reference.md#21-process-inventory)).

The [architecture options](../architecture-options/README.md) lay out five
complete repository shapes against these domains and compare them.

## Decision

### Repository shape: a modular monolith

infraweave uses option 5 of the architecture options,
[the modular monolith](../architecture-options/5-modular-monolith.md),
with its dependency rules as written there, except its "no cross-domain
foreign keys" rule, which the dependency-direction rule below replaces. In
summary:

| Tree | Holds | May import |
|---|---|---|
| `internal/<domain>/` | One business domain: a published `api/` package, an `assembly/` constructor package, a private `internal/` tree, and its own migration stream | `internal/platform`, `internal/kernel`, `internal/iac`, its own tree, other domains' `api/` packages, third-party libraries |
| `internal/<domain>/api/` | Interfaces, input and output types and events other domains use | `internal/kernel`, `internal/platform/outbox`, the standard library |
| `internal/platform/` | Infrastructure with no business rules: SQL and the unit of work, the outbox, decision 0011's job queue and cron ticker, object storage, the audit write (`platform/audit`), envelope encryption and KMS signing (`platform/crypt`), the HTTP kit with problem details, the in-process operation dispatcher (`platform/httpkit/dispatch`) and the console's embedded build (`platform/httpkit/console`), the generated servers, the embedded OPA runtime, the sealed formats (`platform/seal`), the expression engine (`platform/expr`), build information | no domain, no `internal/agent`, no `internal/app` |
| `internal/kernel/` | Value types shared by domain APIs: IDs, spaces, principals, the run state vocabulary | the same as `platform` |
| `internal/iac/` | Pure IaC vendor knowledge used by both the server and the worker | the same as `platform` |
| `internal/agent/` | Everything that runs on a customer host: the launcher, the runner, vendor command sets, container runtimes, log masking, the internal worker-protocol client, the VCS agent and the Flows agent | `internal/kernel`, `internal/iac`, seven named `platform` packages, among them `platform/observe` and `platform/httpkit/serve`, because every agent process mode logs, traces and answers its own probes ([0017](0017-observability-and-health.md)), `platform/seal`, because the runner seals workspaces ([0013](0013-secrets-at-rest.md)), and `platform/expr`, because it renders workflow command templates ([0024](0024-expression-language.md)); `pkg/flows/app/v1`; its own tree; never a domain, never SQL, the queue, object storage credentials or the keyring, which stays in `platform/crypt` |
| `internal/app/` | The composition root and the process modes | everything |
| `internal/devtools/` | Libraries behind `tools/` (`specref`, `openapibundle`, `stubgen`) and the docs check (`docscheck`, run by `make docs-check`) | imported only by `tools/` and their own tests |
| `pkg/client/` | Generated public clients, one per served OpenAPI document | nothing under `internal/` |
| `pkg/sdk/` | The SDK of [0001](0001-rest-first-spacelift-surface.md): the hand-written layer over `pkg/client/<spec>` that every Go client uses | `pkg/client/`, the standard library and third-party libraries; nothing under `internal/` |
| `pkg/flows/appsdk/` | The Go SDK for Flows apps ([0018](0018-flows-runtime.md)) | the generated `App` stubs; nothing under `internal/` |
| `web/console/` | The console's TypeScript source ([0023](0023-console.md)); its build output is embedded by `internal/platform/httpkit/console`, which `serve` mounts | not Go; it reaches infraweave only through the REST API |

- **Boundaries.** A domain reaches another domain only through its `api/`
  package. The Go compiler enforces the private part through the nested
  `internal/` directory; golangci-lint's depguard enforces the rest, with
  the rules listed in the option file's
  [tests and enforcement](../architecture-options/5-modular-monolith.md#tests-and-enforcement)
  section. depguard is enabled in `.golangci.yml` as part of this
  decision.
- **Consistency.** Domain boundaries follow consistency: the `runs` domain
  owns every table a run transition writes, including the stack's run lock
  and run state, and commits a transition in one transaction
  ([run execution path](../architecture-options/5-modular-monolith.md#run-execution-path)).
- **Foreign keys follow the code's dependency direction.** A domain's tables
  may reference the tables of a domain whose `api/` package its code
  imports, and no others. So `runs` may reference `stacks`, because `runs`
  imports `stacks/api`, but `stacks` never references `runs`.
  - **One declared graph.** Which domain may import which is written once,
    as one depguard rule per domain in `.golangci.yml` that lists the other
    domains' `api/` packages it may import. depguard enforces it on code,
    and the schema check below reads the same rules for tables, so code and
    schema cannot disagree.
  - **The graph is acyclic.** Back edges (below) are bound in
    `internal/app`, not imported, so they add no edge. A cycle in the
    declared rules fails the schema check.
  - **Migration streams run in dependency order.** The composition root
    registers each domain's stream after the streams of every domain it
    imports, so a referenced table always exists before the reference
    ([0005](0005-persistence-postgresql.md#migrations)).
  - **Cross-domain references never cascade.** A foreign key from one
    domain's table to another's uses `ON DELETE RESTRICT`, so deleting a row
    never removes or rewrites another domain's rows behind its code. The
    referencing domain removes its own rows first, through its `api/` or in
    response to the owning domain's event. Within a domain, `ON DELETE`
    rules are chosen per key, as 0005 says.
- **Cross-domain effects.** Queries, authorization and policy decisions are
  synchronous calls on `api/` interfaces. Consequences are events appended
  to a transactional outbox in the producer's transaction. The `policies`
  domain owns the input contracts and returns decisions as data; it
  imports none of its callers.
- **Operations called in process.** Flows' API Request block and
  Intelligence's `query` and `mutate` tools run a REST operation by its
  `operationId` through `platform/httpkit/dispatch`, which `internal/app`
  binds to the composed router after construction. The call passes the same
  middleware, authorization and feature gate as an external request, as the
  calling principal, and adds no edge to the declared graph.
- **Worker work that is not a stack run.** Intent operations
  ([0019](0019-ai-provider-boundary.md)) and scan runs reach a worker pool
  through `runsapi`, so workers claim every job through one path
  ([0008](0008-run-execution-and-worker-protocol.md)).
- **Inside a domain.** Large domains split `internal/core/` from
  `internal/adapters/`, and core never imports adapters. Rules with no
  I/O, such as the run transition table, priority levels, policy combination
  and version numbering, sit in packages of their own and are tested as
  tables. Each VCS provider's webhook and API shapes stay in a package of
  its own inside `vcs`, behind one normalized event.
- **Generated code.** Each spec's strict server is generated into
  `internal/platform/gen/<spec>`; `internal/app` composes the per-domain
  partial implementations by embedding, with `UnimplementedStrict` one
  level deeper. An embedded field takes its type's name, so each domain's
  `assembly` package re-exports its partial under a name of its own
  (`StacksDeploy`, `RunsDeploy`). An operation two domains implement makes
  the method ambiguous, and the required assertion
  `var _ deploy.StrictServerInterface = deployServer{}` then fails to
  compile; without the assertion the ambiguity would pass. Protocols
  that are not infraweave's REST API (VCS webhooks, the Terraform `http`
  state backend, service discovery, the registry protocols, `login.v1`)
  are hand-written chi routes that domains contribute.
- **No package path element is named `vendor`**, because the go tool
  treats such a directory as vendored code; IaC vendor code lives under
  `internal/iac`.
- **Domains other records name.** Decision
  [0021](0021-capability-scope-and-supersession.md)'s feature catalogue,
  entitlement and operator API form the `features` domain,
  `internal/features`, at the bottom of the domain graph: every domain may
  import its `api/`, and it imports no domain. It also holds each
  organization's tier, grants, pooled limits and account limits, because
  the entitlement is computed from them; its tables carry organization and
  account IDs without foreign keys, since `access` sits above it. The tenancy of decision
  [0007](0007-authorization-spaces-and-roles.md) (organizations, accounts,
  members, identity providers, sessions, keys, the break-glass credential
  and the OAuth authorization server of
  [0006](0006-authentication-and-tokens.md)) forms the `access` domain,
  `internal/access`, with authorization. Decision
  [0024](0024-expression-language.md)'s expression engine is
  `internal/platform/expr`, with no business rule, which the server and the
  runner both use. Its editor endpoints sit in an `expressions` domain,
  `internal/expressions`, which collects each domain's surfaces through an
  assembly `Surfaces()` method and so imports no domain.
- **Package names in other records.** Decisions 0005 to 0024 name packages
  by responsibility (`internal/store`, `internal/run` and so on). Their
  places in this layout are:

  | Named in a record | Place |
  |---|---|
  | `internal/store`, `internal/store/migrations` | `internal/platform/sql`; each domain's migrations under its `pg/migrations` |
  | `internal/queue`, `internal/cron` | `internal/platform/queue`, `internal/platform/cron` |
  | `internal/objectstore` | `internal/platform/objectstore` |
  | `internal/secrets` | `internal/platform/crypt`; `internal/secrets/mask` is `internal/agent/mask` |
  | `internal/policy` | `internal/platform/rego` for the engine; `internal/policies` for types, inputs, combination, receipts and sampling |
  | `internal/run`, `internal/workerapi` | `internal/runs`; `internal/workers` (the `/worker/v1` adapter) |
  | `internal/vcs/<provider>`, `internal/vcs/agent` | `internal/vcs` adapters; `internal/agent/vcsagent`, with route tables in `internal/kernel/vcsroutes` |
  | `internal/authn`, `internal/authz` | `internal/access` |
  | `internal/statebackend`, `internal/registry` | `internal/state`, `internal/registry` |
  | `internal/events`, `internal/audit` | `internal/events` for the sinks; the envelope, the kind registry and the outbox in `internal/platform/outbox`; the audit write in `internal/platform/audit`, called in the change's own transaction, and the audit trail's search and shipper in `internal/events` |
  | `internal/expr` | `internal/platform/expr`; the editor endpoints in `internal/expressions` |
  | `internal/features` | `internal/features` |
  | `internal/flows`, `internal/flows/agent` | `internal/flows`, `internal/agent/flowsagent` |
  | `internal/ai`, `internal/intent` | `internal/intelligence` |
  | `internal/problem`, `internal/buildinfo`, `internal/telemetry`, `internal/clock` | `internal/platform/httpkit/problem`, `internal/platform/buildinfo`, `internal/platform/observe`, `internal/platform/testkit` |

  A package no row names joins `internal/platform` when it holds no
  business rule, and the domain that owns its rule otherwise.
- **Today's packages** move as the
  [package mapping](../architecture-options/5-modular-monolith.md#package-mapping)
  lists; `api/`, `pkg/client/deploy`, `tools/` and `test/acceptance` stay
  where they are.

### Process topology: one binary, several process modes

infraweave ships one binary, `infraweave`, built from `cmd/infraweave`,
with these cobra subcommands:

| Process mode | Starts | Spacelift equivalent |
|---|---|---|
| `serve` | Each domain's routes: the `deploy` REST API, webhooks, the state backend, the registry protocols, the worker long-poll endpoints, the VCS agent long-poll endpoints (decision 0012) and the Flows agent WebSocket gateway (decision 0018); readiness on its own port. It consumes no queue except the `flows.agent_task` jobs of the Flows agents connected to it (decision 0018) | server, and the VCS and Flows gateways |
| `scheduler` | Each domain's queue consumers and outbox subscriptions, and the cron ticker, which every replica runs: due rows are claimed with `FOR UPDATE SKIP LOCKED`, so no leader is elected (decision 0011) | drain, with the scheduler folded in |
| `launcher` | The launcher for a worker pool, from `internal/agent` only; it never calls `internal/app` | launcher |
| `all` | `serve` and `scheduler` in one process, for single-node installs and acceptance tests | none |
| `runner` (hidden) | The runner, which executes a run's phases inside the run container, from the same static binary the launcher mounts | worker binary |
| `vcs-agent` | The VCS agent for a VCS agent pool; it long-polls `serve` (decision 0012) | VCS agent |
| `flows-agent` | The Flows agent for a Flows agent pool (decision 0018) | Flows agent |
| `migrate up`, `migrate status` | Applies or reports the embedded migrations, then exits (decision 0005) | none |
| `version` | Build information from `internal/platform/buildinfo` | none |
| `auth hash-password` | Reads a password on standard input and prints the break-glass password hash of decision 0006, then exits | none |
| `secrets generate-kek`, `secrets rewrap`, `secrets rotate` | Key operations of decision 0013 against the database, then exits | none |

The process modes share one PostgreSQL database and are each stateless, so each
scales as its own deployment. `serve` also holds the VCS and Flows agent
gateways; a gateway becomes a process mode of its own only if its long-lived
connections need separate scaling
([process topology](../architecture-options/README.md#process-topology)).
The infraweave CLI is a separate client binary, `infraweavectl`, built from
`cmd/infraweavectl` over the SDK, `pkg/sdk`; it is not a process mode. Packaging
both binaries is decision 0020's.

## Rationale

The [comparison matrix](../architecture-options/README.md#comparison-matrix)
rates all five options on 24 criteria. Option 5 rates Good on 16 and Poor on
none, and leads the roadmap table outright. Its depguard burden rates Fair,
about 26 rules and 270 entries, because the nested `internal/` directories
leave the compiler to enforce most of each boundary.

- **Option 1, layered**, keeps runs acyclic only by adding a `workflow`
  tier with a hand-kept order, spreads each domain over six trees, and
  leaves Flows and late domains with nowhere separable to live.
- **Option 2, hexagonal**, isolates infrastructure well but holds every
  domain in one core, separated by a per-domain lint list, with shared port
  and DTO files that every phase edits.
- **Option 3, clean**, makes the rule-heavy cores pure, which this decision
  keeps inside option 5, but charges boundaries and presenters on every
  CRUD operation and cannot express "no sibling use case" without about
  110 generated lint rules.
- **Option 4, DDD**, as drawn, separates late domains best but pays the
  full tactical cost on the CRUD phases, commits policy receipts apart from
  the transition that uses them, and carries the heaviest lint and package
  count. Its strongest variant keeps tactical patterns to the rule-heavy
  domains and uses nested `internal/` directories, and then converges on
  option 5's shape. What remains is DDD's preference for events between
  contexts over synchronous calls, which a run transition committed in one
  transaction does not want.
- **Spacelift's workflow concepts and the three clouds do not separate the
  options.** Flows is a domain on the shared event model and queue in
  options 4 and 5 alike. Custom workflows are pure vendor knowledge in
  `internal/iac` plus the runner's rendering through `platform/expr`. AWS,
  Azure and Google Cloud are drivers behind interfaces in `platform`
  (object storage, KEK and signing, database IAM authentication) and
  adapters inside `cloud`, in every option.

Option 5 keeps the property option 4 is strongest at (separable domains
with their own tables) and the property options 1 to 3 keep (one
transaction for a run transition), while the compiler, not a growing lint
file, carries most of the boundary. The
[recommendation](../architecture-options/README.md#recommendation) lists
what makes it hold.

**Foreign keys along the dependency direction, not none at all.** With no
cross-domain foreign keys, a run would hold a stack ID that nothing checks,
and every orphan would be found by application code or not at all. A domain
that imports another already depends on its contract, so a reference in
the same direction adds no new coupling, and PostgreSQL then guarantees the
reference. Keys against the import direction stay forbidden, because they
would make the imported domain's schema depend on its caller's, and a
cascade would let one domain change rows another owns.

For topology, the [process topology](../architecture-options/README.md#process-topology)
section weighs one binary against separate binaries and separate services.
Separate services would split the data a run transition must commit
together and add version skew between process modes, while the process modes already give
independent scaling and keep queue load away from API latency. Spacelift
reached the same shape: two stateless processes from one binary after
folding its scheduler into its drain, and the same pattern for Flows.

**Departure from Spacelift.** Spacelift ships its launcher as a separate
binary and image that embeds the worker binary. infraweave's launcher process
mode is the same `infraweave` binary, so the launcher, the mounted runner and
the server share a version by construction. The launcher's package graph
stays free of server code, so a separate launcher build remains a one-file
change.

## Constraints

- **`runs` concentrates work and stays one domain.** It holds the state
  machine, queue gates, scheduling, handover, timers, promotion and
  dependencies. Splitting it would move a transition's writes across `api/`
  calls and lose the single transaction. Its core packages reach the store
  only through `lifecycle`, and the gate fails on a duplicate migration
  number within a stream, so parallel phases collide in review, not in
  production.
- **Back edges** are bound after construction in `internal/app`: `stacks`
  reads run state (`runsapi.StackStatus`), `runs` reads VCS sources,
  cloud credentials and state versions at handover (`vcsapi.Sources`,
  `cloudapi.Credentials`, `stateapi.Versions`), the worker protocol in
  `workers` claims and reports through `runsapi.Scheduler` and
  `runsapi.Reports`, and `access` evaluates `LOGIN` policies through
  `policiesapi.Evaluator`. The
  [declared graph](../../implementation/repo-layout.md#declared-graph)
  lists every edge, the back edges and the construction order.
- **The stack-by-run-state read view** lives in the `runs` migration stream
  and is served through `runsapi.StackStatus`; it is the one sanctioned
  read across two schemas, and the schema check holds it to the graph.
- **The binary on worker hosts carries server code** it never executes. If
  image size or a customer's review requires it, `cmd/infraweave-agent` links
  only `internal/agent`.
- **Thin packages and the 90% floor.** `api/` and `assembly/` packages hold
  little logic; keeping validation and constructors there gives each a
  test surface. Coverage exclusions are decision
  [0016](0016-testing-and-acceptance.md#coverage)'s.
- **Every domain carries its rule entries.** `make lint` checks the whole
  tree, not only new issues. `TestUnitDepguardFixtures` fails when a
  directory under `internal/` that is a domain lacks its graph rule or its
  entries in `constructors` and `shared-below-domains`.
- **depguard matches plain prefixes.** An allow entry without a trailing
  `$` or `/` admits every package whose path starts with it, so
  `$m/internal/stacks/api` would let `$m/internal/stacks/apiutil` through.
  Allow entries end in `$` or `/`
  ([repo layout](../../implementation/repo-layout.md#depguard-rules)), and
  P1b's fixtures include a sibling import.
- **Undecided neighbours.** The queue implementation is decision 0011's; a
  workflow engine run as a separate cluster would move run orchestration
  out of the `runs` transaction and reopen this decision. Packaging and
  deployment artefacts are decision 0020's; this decision owns the code
  layout and the process modes.

## Verification

- `.golangci.yml` enables depguard with the rules of the
  [tests and enforcement](../architecture-options/5-modular-monolith.md#tests-and-enforcement)
  section; `make lint`, inside `make gate`, fails on a forbidden import.
- `make build` cross-compiles `cmd/infraweave` with CGO disabled, which the
  launcher process mode needs to mount the binary into runner images.
- Acceptance features start `infraweave all` and an `infraweave launcher` against real
  PostgreSQL and object storage (decision 0016).
- `make verify-generate` checks the generated servers at their
  `internal/platform/gen/<spec>` paths.
- `TestAccSchemaDependencies` applies each domain's migration stream in
  registration order to a fresh database and attributes the tables that
  appear to that stream's domain. It reads the per-domain depguard rules
  from `.golangci.yml` and fails when a foreign key in `pg_constraint` goes
  from one domain to a domain that domain may not import, when a
  cross-domain key cascades or sets null on delete, when the declared
  graph has a cycle, or when a stream is registered before a stream it
  depends on. Views count as references: a view that reads another
  domain's table must follow the graph, which the check reads from
  `pg_depend`.
- `internal/app/router.go` asserts that each composed server satisfies its
  generated interface, so `make build` fails on an operation with two
  owners.
- `make docs-check` checks that the five option files keep one heading
  sequence and that this record's links resolve.

## References

- [Architecture options](../architecture-options/README.md): the
  [options](../architecture-options/README.md#the-options), the
  [comparison matrix](../architecture-options/README.md#comparison-matrix),
  the [process topology](../architecture-options/README.md#process-topology),
  the [recommendation](../architecture-options/README.md#recommendation),
  [what would change it](../architecture-options/README.md#what-would-change-it)
  and the [distance from today's tree](../architecture-options/README.md#appendix-distance-from-todays-tree).
- The five option files: [layered](../architecture-options/1-layered.md),
  [hexagonal](../architecture-options/2-hexagonal.md),
  [clean](../architecture-options/3-clean.md),
  [DDD](../architecture-options/4-ddd.md),
  [modular monolith](../architecture-options/5-modular-monolith.md).
- Report 24, [process inventory](../reference/24-self-hosted-reference.md#21-process-inventory)
  and [message queues](../reference/24-self-hosted-reference.md#33-message-queues).
- Report 27, [OTF's architecture](../reference/27-reference-materials.md#31-architecture)
  and [OPA as the embedded policy engine](../reference/27-reference-materials.md#9-opa-as-the-embedded-policy-engine).
- Report 08, [queueing and the stack lock](../reference/08-run-lifecycle.md#4-queueing-the-stack-lock-and-scheduling);
  report 10, [the launcher and the worker protocol](../reference/10-workers-and-execution.md#4-the-launcher-and-the-worker-protocol).
- Related decisions: 0005 persistence, 0008 run execution and the worker
  protocol, 0009 policy engine, 0011 persistent work queue, 0015 events,
  0016 testing and acceptance, 0018 Flows engine, apps and agents, 0020 reference
  deployments and packaging.
