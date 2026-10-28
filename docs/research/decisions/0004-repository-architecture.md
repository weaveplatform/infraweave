# 0004: Repository architecture and process topology

Status: Proposed

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
- **Flows has its own tenancy** (organizations, projects, UUIDs) and
  Intelligence reads across every domain
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
with its dependency rules as written there. In summary:

| Tree | Holds | May import |
|---|---|---|
| `internal/<module>/` | One business domain: a published `api/` package, a `module/` constructor package, a private `internal/` tree, and its own migration stream | `internal/platform`, `internal/kernel`, `internal/iac`, its own tree, other modules' `api/` packages, third-party libraries |
| `internal/<module>/api/` | Interfaces, input and output types and events other modules use | `internal/kernel`, `internal/platform/outbox`, the standard library |
| `internal/platform/` | Infrastructure with no business rules: SQL and the unit of work, the outbox, decision 0011's job queue and cron ticker, object storage, envelope encryption, the HTTP kit with problem details, the generated servers, the embedded OPA runtime, build information | no module, no `internal/agent`, no `internal/app` |
| `internal/kernel/` | Value types shared by module APIs: IDs, spaces, principals, the run state vocabulary | the same as `platform` |
| `internal/iac/` | Pure IaC vendor knowledge used by both the server and the worker | the same as `platform` |
| `internal/agent/` | Everything that runs on a customer host: the launcher, the phase executor, vendor command sets, container runtimes, log masking, the internal worker-protocol client, the VCS agent and the Flows agent | `internal/kernel`, `internal/iac`, three named `platform` packages, its own tree; never a module, never SQL, the queue, object storage credentials or encryption |
| `internal/app/` | The composition root and the roles | everything |
| `internal/devtools/` | Libraries behind `tools/` (`specref`, `openapibundle`, `stubgen`) and the docs check (`docscheck`, run by `make docs-check`) | imported only by `tools/` and their own tests |
| `pkg/client/` | Generated public clients | nothing under `internal/` |

- **Boundaries.** A module reaches another module only through its `api/`
  package. The Go compiler enforces the private part through the nested
  `internal/` directory; golangci-lint's depguard enforces the rest, with
  the rules listed in the option file's
  [tests and enforcement](../architecture-options/5-modular-monolith.md#tests-and-enforcement)
  section. depguard is enabled in `.golangci.yml` as part of this
  decision.
- **Consistency.** Module boundaries follow consistency: the `runs` module
  owns every table a run transition writes, including the stack's run lock
  and run state, and commits a transition in one transaction
  ([run execution path](../architecture-options/5-modular-monolith.md#run-execution-path)).
  No foreign key crosses a module boundary.
- **Cross-module effects.** Queries, authorisation and policy decisions are
  synchronous calls on `api/` interfaces. Consequences are events appended
  to a transactional outbox in the producer's transaction. The `policies`
  module owns the input contracts and returns decisions as data; it
  imports none of its callers.
- **Inside a module.** Large modules split `internal/core/` from
  `internal/adapters/`, and core never imports adapters. Rules with no
  I/O, such as the run transition table, priority tiers, policy combination
  and version numbering, sit in packages of their own and are tested as
  tables. Each VCS provider's webhook and API shapes stay in a package of
  its own inside `vcs`, behind one normalised event.
- **Generated code.** Each spec's strict server is generated into
  `internal/platform/gen/<spec>`; `internal/app` composes the per-module
  partial implementations by embedding, with `UnimplementedStrict` one
  level deeper, so an operation with two owners fails to compile. Protocols
  that are not infraweave's REST API (VCS webhooks, the Terraform `http`
  state backend, service discovery, the registry protocols, `login.v1`)
  are hand-written chi routes that modules contribute.
- **No package path element is named `vendor`**, because the go tool
  treats such a directory as vendored code; IaC vendor code lives under
  `internal/iac`.
- **Package names in other records.** Decisions 0005 to 0022 name packages
  by responsibility (`internal/store`, `internal/run` and so on). Their
  places in this layout are:

  | Named in a record | Place |
  |---|---|
  | `internal/store`, `internal/store/migrations` | `internal/platform/sql`; each module's migrations under its `pg/migrations` |
  | `internal/queue`, `internal/cron` | `internal/platform/queue`, `internal/platform/cron` |
  | `internal/objectstore` | `internal/platform/objectstore` |
  | `internal/secrets` | `internal/platform/crypt`; `internal/secrets/mask` is `internal/agent/mask` |
  | `internal/policy` | `internal/platform/rego` for the engine; `internal/policies` for types, inputs, combination, receipts and sampling |
  | `internal/run`, `internal/workerapi` | `internal/runs`; `internal/workers` (the `/worker/v1` adapter) |
  | `internal/vcs/<provider>`, `internal/vcs/agent` | `internal/vcs` adapters; `internal/agent/vcsagent`, with route tables in `internal/kernel/vcsroutes` |
  | `internal/authn`, `internal/authz` | `internal/access` |
  | `internal/statebackend`, `internal/registry` | `internal/state`, `internal/registry` |
  | `internal/events`, `internal/audit` | `internal/events`, with the outbox in `internal/platform/outbox` |
  | `internal/flows`, `internal/flows/agent` | `internal/flows`, `internal/agent/flowsagent` |
  | `internal/ai`, `internal/intent` | `internal/intelligence` |
  | `internal/problem`, `internal/buildinfo`, `internal/telemetry`, `internal/clock` | `internal/platform/httpkit/problem`, `internal/platform/buildinfo`, `internal/platform/observe`, `internal/platform/testkit` |

  A package no row names joins `internal/platform` when it holds no
  business rule, and the module that owns its rule otherwise.
- **Today's packages** move as the
  [package mapping](../architecture-options/5-modular-monolith.md#package-mapping)
  lists; `api/`, `pkg/client/deploy`, `tools/` and `test/acceptance` stay
  where they are.

### Process topology: one binary, several roles

infraweave ships one binary, `infraweave`, built from `cmd/infraweave`,
with these cobra subcommands:

| Role | Starts | Spacelift equivalent |
|---|---|---|
| `serve` | Each module's routes: the `deploy` REST API, webhooks, the state backend, the registry protocols, the worker long-poll endpoints, the VCS agent long-poll endpoints (decision 0012) and the Flows agent WebSocket gateway (decision 0018); readiness on its own port. It consumes no queue | server, and the VCS and Flows gateways |
| `scheduler` | Each module's queue consumers and outbox subscriptions, and the cron ticker, which every replica runs: due rows are claimed with `FOR UPDATE SKIP LOCKED`, so no leader is elected (decision 0011) | drain, with the scheduler folded in |
| `worker` | The launcher for a worker pool, from `internal/agent` only; it never calls `internal/app` | launcher |
| `all` | `serve` and `scheduler` in one process, for single-node installs and acceptance tests | none |
| `worker exec` (hidden) | The phase executor inside a run container, from the same static binary the launcher mounts | worker binary |
| `vcs-agent` | The VCS agent for a VCS agent pool; it long-polls `serve` (decision 0012) | VCS agent |
| `flows-agent` | The Flows agent for a Flows agent pool (decision 0018) | Flows agent |
| `migrate up`, `migrate status` | Applies or reports the embedded migrations, then exits (decision 0005) | none |
| `version` | Build information from `internal/platform/buildinfo` | none |
| `secrets generate-kek`, `secrets rewrap`, `secrets rotate` | Key operations of decision 0013 against the database, then exits | none |

The roles share one PostgreSQL database and are each stateless, so each
scales as its own deployment. `serve` also holds the VCS and Flows agent
gateways; a gateway becomes a role of its own only if its long-lived
connections need separate scaling
([process topology](../architecture-options/README.md#process-topology)).
The infraweave CLI is a separate client binary, `infraweavectl`, built from
`cmd/infraweavectl` over `pkg/client/deploy`; it is not a role. Packaging
both binaries is decision 0020's.

## Rationale

The [comparison matrix](../architecture-options/README.md#comparison-matrix)
rates all five options on 20 criteria. Option 5 rates Good on 16 and Poor on
none, and leads the roadmap table outright.

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
- **Option 4, DDD**, separates late domains best but pays the full tactical
  cost on the CRUD phases, needs the outbox and event bus before the first
  run, makes stack state and checks eventually consistent, commits policy
  receipts apart from the transition that uses them, and carries the
  heaviest lint and package count.

Option 5 keeps the property option 4 is strongest at (separable domains
with their own tables) and the property options 1 to 3 keep (one
transaction for a run transition), while the compiler, not a growing lint
file, carries most of the boundary. The
[recommendation](../architecture-options/README.md#recommendation) lists
what makes it hold.

For topology, the [process topology](../architecture-options/README.md#process-topology)
section weighs one binary against separate binaries and separate services.
Separate services would split the data a run transition must commit
together and add version skew between roles, while the roles already give
independent scaling and keep queue load away from API latency. Spacelift
reached the same shape: two stateless processes from one binary after
folding its scheduler into its drain, and the same pattern for Flows.

**Departure from Spacelift.** Spacelift ships its launcher as a separate
binary and image that embeds the worker binary. infraweave's worker role is
the same `infraweave` binary, so the launcher, the mounted executor and the
server share a version by construction. The worker's package graph stays
free of server code, so a separate worker build remains a one-file change.

## Constraints

- **`runs` concentrates work.** It holds the state machine, queue gates,
  scheduling, handover, timers, promotion and dependencies. If it
  dominates the hand-written code or causes routine collisions, it splits
  into sub-modules along those seams, keeping one migration stream.
- **Back edges** (stacks reading run state, runs reading VCS sources and
  cloud credentials at handover) are bound after construction in
  `internal/app`. A stack list filtered by run state reads across two
  schemas through one sanctioned read view.
- **The worker binary carries server code** it never executes. If image
  size or a customer's review requires it, `cmd/infraweave-worker` links
  only `internal/agent`.
- **Thin packages and the 90% floor.** `api/` and `module/` packages hold
  little logic; keeping validation and constructors there gives each a
  test surface. Changing coverage exclusions needs its own decision.
- **depguard reports only new issues** against the merge base in this
  repository's configuration, so the rules are introduced with a clean run
  over the whole tree.
- **Undecided neighbours.** The queue implementation is decision 0011's; a
  workflow engine run as a separate cluster would move run orchestration
  out of the `runs` transaction and reopen this decision. Packaging and
  deployment artefacts are decision 0020's; this decision owns the code
  layout and the roles.

## Verification

- `.golangci.yml` enables depguard with the rules of the
  [tests and enforcement](../architecture-options/5-modular-monolith.md#tests-and-enforcement)
  section; `make lint`, inside `make gate`, fails on a forbidden import.
- `make build` cross-compiles `cmd/infraweave` with CGO disabled, which the
  worker role needs to mount the binary into runner images.
- Acceptance features start `infraweave all` and a `worker` against real
  PostgreSQL and object storage (decision 0016).
- `make verify-generate` checks the generated servers at their
  `internal/platform/gen/<spec>` paths.
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
  0016 testing and acceptance, 0018 Flows runtime, 0020 reference
  deployments and packaging.
