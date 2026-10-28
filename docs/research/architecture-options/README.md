# Architecture options

infraweave is one Go module that serves Spacelift's feature set behind a
REST API ([decision 0001](../decisions/0001-rest-first-spacelift-surface.md)).
Today it holds a served skeleton: a bundled OpenAPI document, a generated
strict chi server whose 72 operations answer 501, a generated client,
problem details, and the tooling that builds the Spacelift reference. The
roadmap in [report 26](../reference/26-scope-and-tiers.md#14-provisional-phase-spine)
grows it through 27 phases to runs, workers, policies, VCS, state, registries,
events, templates, Intelligence and Flows.

This directory lays out five repository architectures for that roadmap, each
in full against infraweave's own domains, compares them, weighs the process
topology, and recommends one. [Decision 0004](../decisions/0004-repository-architecture.md)
records the recommendation.

## Index

| Option | File | Shape in one line |
|---|---|---|
| 1 | [Layered](1-layered.md) | Horizontal layers: transport, service, repository and infrastructure, model; imports point down |
| 2 | [Hexagonal](2-hexagonal.md) | One application core with ports; driving and driven adapters around it |
| 3 | [Clean](3-clean.md) | Concentric rings: entities, use cases, interface adapters, frameworks; imports point inward |
| 4 | [Domain-driven design](4-ddd.md) | Bounded contexts, each with domain, application, infrastructure and interfaces packages, joined by a context map |
| 5 | [Modular monolith](5-modular-monolith.md) | Business modules with published `api/` packages, an outbox between them, hexagons inside the larger modules |

Every option file has the same sections in the same order, so the options
can be read side by side:

- Summary, with the dependency rules;
- Repository root;
- Full package tree;
- Deep dives on infraweave's domains: the stack configuration write path, the
  run execution path, policy evaluation with embedded OPA, the worker protocol
  and pools, VCS webhook ingest, the state backend, the module and provider
  registry protocol, access, identity and spaces, events, the Flows engine,
  the composition root, generated code, and tests and enforcement;
- Walkthroughs: a GitHub push to a proposed run, a plan policy and a PR
  comment; a tracked run that awaits approval and applies on a private
  worker; adding the Pulumi vendor; adding the templates domain;
- Package mapping from today's tree;
- Numbers;
- Placement choices and friction.

Facts every option shares, so that the comparison is about structure alone:

- one module and one server binary, `cmd/infraweave`, with the roles
  `serve`, `scheduler`, `worker` and `all`, the hidden `worker exec`, and
  the `vcs-agent`, `flows-agent`, `migrate` and `version` subcommands
  ([Process topology](#process-topology)), plus the separate client
  binary `cmd/infraweavectl`;
- PostgreSQL and object storage (decision 0005), decision 0011's own job
  queue in PostgreSQL, embedded OPA (decision 0009), and HTTP long-poll for
  workers (decision 0008) and VCS agents (decision 0012);
- the worker role links no server-side persistence, because a private
  worker runs on customer hosts with outbound HTTPS only
  ([report 10, section 4.2](../reference/10-workers-and-execution.md#42-transports));
- one generated strict server for the `deploy` API, composed from
  per-domain partial implementations by embedding, with the 501 stubs one
  level deeper; protocols that are not infraweave's own REST API (VCS
  webhooks, the Terraform `http` state backend, service discovery, the
  registry protocols) are hand-written chi routes;
- golangci-lint's depguard is the only enforcement of import rules.

## The options

Each option file argues its shape at full strength. This section condenses
each into what it does well, what it costs, and how it meets the phase
spine of [report 26](../reference/26-scope-and-tiers.md#14-provisional-phase-spine).

### Option 1: Layered

[1-layered.md](1-layered.md) cuts the tree horizontally into `model` and
`support`, `repository` and `infra`, `service` (one package per domain),
`transport`, and a separate `worker` column. Services never import each
other, so cross-domain work sits in a second business tier, `workflow`,
whose packages (`runflow`, `stackflow`, `vcsevent`, `notifyflow` and later
ones) are kept in a declared order.

| Pros | Cons |
|---|---|
| The most familiar shape; each layer rule is one depguard rule | The `workflow` tier knows every domain; `workflow/runflow` grows to about 10k lines and every run feature lands in it |
| One writer for the run state machine, `runflow`, so run row, stack lock, queue job and outbox event commit together | Stack CRUD already needs a workflow at P5, so the early phases carry the full structure |
| One partial strict-server implementation per `transport/http/api/<family>`, composed by embedding | A domain spreads over six trees; parallel lanes collide on `model`, migrations, `strict.go`, `app/services.go` and `.golangci.yml` |
| The worker column keeps pgx and server code off private workers | About 60 depguard rules by P27, and the workflow order is maintained by hand |
| A new vendor is about 10 new files and no change to `runflow` | Flows, or any domain, has no single directory, so extracting it later is hard |

**Roadmap verdict.** Comfortable for P1 to P4, bending from P5, held
together from P7 to P13 only by the workflow tier and its order, and
steadily more expensive after P13: every late domain (P21 to P23, P25,
P26) spreads across six trees and edits shared files and lint rules.

### Option 2: Hexagonal

[2-hexagonal.md](2-hexagonal.md) puts every domain in one core,
`internal/core/`, with driving and driven ports in `core/port/`, adapters in
`internal/adapter/driving` and `internal/adapter/driven`, and the worker as a
second, separate hexagon in `internal/worker/`. A core domain reaches another
only through a driving port.

| Pros | Cons |
|---|---|
| OPA, pgx, the cloud SDKs and each VCS provider sit in exactly one adapter; core tests use fakes | A domain lives in five places: two port files, its core package, a store package and an HTTP family package |
| Non-REST protocols (webhooks, state backend, registry, worker long-poll) are ordinary driving adapters | `core/port/driving/types.go`, `core/port/driven/stores.go`, the single migration stream and `app/services.go` are edited by every phase |
| A new VCS provider or worker runtime is one adapter package plus contract fixtures | One depguard list per domain for rule 3; a domain added without its list is silently unguarded |
| The worker hexagon shares no package with the server | Wide driving ports (`Runs`, `RunProgress`) and large fakes |
| Roles are choices of driving adapters over the same constructors | No rule says which domains may write inside another's transaction; Flows shares the core and the schema stream |

**Roadmap verdict.** Strong on the infrastructure-heavy phases (P2, P7,
P8, P11, P12, P13, P14, P15, P19), each of which lands as an adapter behind
a port the core already names. Weaker from P16 onward, where each new domain
is another sibling in a single core held apart only by rule 3, and parallel
lanes contend for the shared port files. Extracting Flows later means
splitting the schema and the composition root.

### Option 3: Clean

[3-clean.md](3-clean.md) arranges rings: `internal/entity` (pure rules:
the run state machine, priority tiers, policy combination, space
reachability, version numbering, vendor change models),
`internal/usecase/<family>` (interactors with boundaries and consumer-side
ports), `internal/adapter` (controllers, presenters, gateways) and
`internal/infrastructure`. A second cut keeps worker-side code in
`usecase/agent`, `adapter/agent` and `app/workerapp`.

| Pros | Cons |
|---|---|
| The rule-heavy cores (run machine, tiers, policy combination, RBAC, version numbering) are pure and table-tested | Every operation needs a controller, input and output boundaries and presenters; the Stacks family alone is thousands of mapping lines |
| Per-consumer ports remove policy cycles: run, VCS and review use cases each declare a decision port | Domains are invisible: the run lifecycle spans about ten unrelated-looking packages |
| Framework swaps (chi, pgx, OPA, SDKs) stay inside one adapter | depguard has no variable for the importing package, so "no sibling use case" takes about 110 generated rules and a new `tools/depguardgen` |
| A vendor is mostly one pure entity package shared by server and worker | `usecase/runstate` is a shared service at the system's centre |
| Inward-only direction plus a worker allow-list keeps persistence off workers | Event fan-out and Flows' separate tenancy are a file and a naming prefix, not structure |

**Roadmap verdict.** Excellent for P7 to P10, P13 and P17, whose rules are
pure entities. Expensive for the CRUD-heavy phases (P5, P6, P15, P16, P18,
P21 to P23), which add boundaries and presenters for little rule content.
No structure for the late, separable domains: Intelligence (P25) reads
across everything unfenced, and Flows (P26) cannot express its own
tenancy.

### Option 4: Domain-driven design

[4-ddd.md](4-ddd.md) draws sixteen bounded contexts (identity,
stackconfig, orchestration, execution, vendors, state, sourcecontrol,
governance, cloud, registry, notifications, audit, templates, inventory,
intelligence, flows), each with `domain`, `application`, `infrastructure`
and `interfaces`, joined by a context map with an anticorruption layer per
VCS provider and published events through an outbox and bus.
`orchestration/domain/queue.StackQueue` holds a stack's open runs, its
blocking run and both locks.

| Pros | Cons |
|---|---|
| The run state machine and stack lock form one aggregate | P2 to P6 CRUD pays for aggregates, repositories, in-memory and SQL implementations, contract suites and events nobody reads yet |
| Flows separates cleanly: its own spec, tables and tenancy behind one anticorruption layer | The outbox and bus, with idempotency and dead letters, must exist before the first run |
| A VCS provider is one package; P19 touches no other context | Consequences are eventually consistent (stack state, checks, audit); acceptance tests wait for effects |
| Governance as an open host service gives policy hooks without cycles | 38 depguard rules and about 400 entries; every new context edits every boundary rule |
| The context map makes every cross-domain effect explicit; any context could become a service | About 330 packages, about 60 thin; policy receipts commit before the transition that uses them; `StackQueue` is a hot aggregate |

**Roadmap verdict.** The strongest shape for the middle and late spine:
run integrity (P7 to P10), policy hooks (P13), VCS growth (P12, P19), event
fan-out (P18), and separable late contexts (P21, P22, P25, P26). The
weakest start: plain CRUD pays the full tactical cost, and event
infrastructure must precede the first run. It suits a team that expects to
split into services more than one that wants the quickest path through 27
phases in one binary.

### Option 5: Modular monolith

[5-modular-monolith.md](5-modular-monolith.md) makes each business domain
a module, `internal/<module>/`, with a published `api/` package, a
`module/` constructor that contributes routes, queue consumers and cron, an
`internal/` directory the compiler keeps private, and its own migration
stream. Seventeen modules at P27 (access, stacks, configuration, runs,
workers, vendors, state, vcs, policies, cloud, schedules, registry, events,
templates, resources, intelligence, flows); shared infrastructure in
`internal/platform`, value types in `internal/kernel`, pure vendor code in
`internal/iac`, and everything that runs on a worker host in
`internal/agent`.

| Pros | Cons |
|---|---|
| A run transition (run row, transition, stack run lock, queue item, receipts, outbox event) is one transaction inside one module | `runs` holds about a fifth of the hand-written code and is touched by P7, P9, P10, P13 and P16 |
| The Go compiler enforces the module boundary through nested `internal/`; depguard needs 6 rules and about 75 entries | Stack state is runs data; a stack list filtered by state needs a read view across two schemas |
| Policies own the input contract and return decisions as data, so policy hooks form no cycle | Three back edges (stacks to runs, runs to vcs, runs to cloud) are bound after construction |
| `internal/agent` imports no module and no persistence | About 290 packages, about 35 thin ones under the 90% floor |
| Each module contributes `Routes`, `Consumers` and `Cron`, which map straight onto `serve` and `scheduler` | Long import paths and every constructor package named `module`, so imports need aliases |
| A late domain is a new directory plus about 5 edits; Flows has its own schema and can be extracted | The single binary carries server code onto worker hosts, although the agent never imports it |

**Roadmap verdict.** Fits the spine phase by phase: P1 to P6 add one
module each; P7 to P10 happen inside `runs`, whose single-transaction
transitions already hold them; P11 to P19 are new modules over existing
`api/` packages and the outbox; P20 touches only `internal/iac` and
`internal/agent`; P21 to P26 add directories without changing existing
ones. The cost comes early and stays: the module skeleton, thin packages
under the coverage floor, and the concentration of P7 to P16 work in
`runs`.

## Comparison matrix

Ratings are Good, Fair or Poor. Every criterion is rated for every
option. Phases name where the criterion starts to bite.

### Today's shape

How each option meets the tree as it stands and the CRUD phases right after
it (P1 to P6).

| Criterion | 1 Layered | 2 Hexagonal | 3 Clean | 4 DDD | 5 Modular monolith |
|---|---|---|---|---|---|
| Distance from today's tree (moves and new tooling at P1) | Good | Good | Fair | Fair | Good |
| Fit with the generated strict server and stubgen | Good | Good | Fair | Fair | Good |
| Cost of the CRUD phases P2 to P6 | Fair | Fair | Poor | Poor | Fair |
| Finding one domain's code | Poor | Fair | Poor | Good | Good |

Notes:
- Every option moves the same five packages (`docscheck`,
  `openapibundle`, `specref`, `stubgen`, `server/problem`), every option
  but hexagonal also moves `buildinfo`, and all change the generated output
  path; the appendix lists the destinations. Clean adds a
  lint-config generator and two Makefile targets; DDD and Clean start with
  more top-level directories than they have code for.
- Strict-server fit is Fair where a resource does not map to one owner:
  Clean wraps each generated type in boundaries and presenters, and DDD
  assembles the Stacks representation from two contexts.

### Roadmap shape

How each option carries the 27-phase spine.

| Criterion | 1 Layered | 2 Hexagonal | 3 Clean | 4 DDD | 5 Modular monolith |
|---|---|---|---|---|---|
| Run-transaction integrity: run, lock, queue item, receipts and event in one transaction (P7 to P10) | Good | Good | Good | Fair | Good |
| Policy hooks at run state points without import cycles (P13) | Fair | Good | Good | Good | Good |
| Worker-side isolation: the worker role links no server persistence (P8, P14) | Good | Good | Good | Good | Good |
| VCS provider growth (P12, P19) | Fair | Good | Good | Good | Good |
| IaC vendor growth (P9, P20) | Good | Fair | Good | Fair | Good |
| Event fan-out to inbox, Slack, webhooks, PR comments and audit (P18) | Fair | Fair | Fair | Good | Good |
| Flows separability, with its own tenancy (P26) | Poor | Fair | Poor | Good | Good |
| Late-domain addition without touching existing cores (P21, P22, P25) | Poor | Fair | Fair | Good | Good |
| Process roles fall out of the structure (P7, P27) | Good | Good | Fair | Good | Good |
| Extractability of a domain into a separate service | Poor | Poor | Poor | Good | Good |

Notes:
- DDD is Fair on run integrity because policy receipts commit in
  governance's transaction before the run transition that relies on them
  ([4-ddd.md](4-ddd.md#placement-choices-and-friction)).
- Layered is Fair on policy hooks because acyclicity rests on the
  hand-kept workflow order rather than on the domains.
- Hexagonal and DDD are Fair on vendors: a vendor spans two hexagons in
  option 2, and the kernel plus four contexts in option 4.
- Clean is Fair on roles because the worker side is a second axis across
  the rings.

### Cost of the structure

What each option costs to keep, measured at the end of P27 from each
file's Numbers section.

| Criterion | 1 Layered | 2 Hexagonal | 3 Clean | 4 DDD | 5 Modular monolith |
|---|---|---|---|---|---|
| Package count | Good | Good | Fair | Poor | Fair |
| depguard rule burden | Fair | Fair | Poor | Poor | Good |
| Coverage-floor friction (thin packages under 90%) | Good | Good | Fair | Poor | Fair |
| Indirection per request (packages a stack update crosses) | Fair | Fair | Poor | Fair | Fair |
| Files touched to add a domain | Fair | Fair | Fair | Poor | Good |
| Shared hotspot files for parallel phases | Poor | Poor | Poor | Fair | Good |

Clean is Poor on shared hotspots for the same reason as layered and
hexagonal: every phase edits `app/usecases.go`, `app/controllers.go`,
`app/events.go`, `adapter/rest/server.go`, the single migration stream and
the generated lint block.

The numbers behind the cost ratings:

| Measure at P27 | 1 Layered | 2 Hexagonal | 3 Clean | 4 DDD | 5 Modular monolith |
|---|---|---|---|---|---|
| Go packages | about 200 | about 175 | about 245 | about 330 | about 290 |
| Top-level directories under `internal/` | 11 | 7 | 6 | 20 | 23 |
| Dependency rules | 8 | 9 | 8 | 9 | 8 (1 by the compiler) |
| depguard rules, entries | about 60, about 230 | about 30, about 130 | about 120, about 750 (110 generated) | 38, about 400 | 6, about 75 |
| Thin packages at risk under the 90% floor | a handful | about 12 | about 25 | about 60 | about 35 |
| Packages a stack update crosses | 7 | 6 | 8 | 7 | 6 |
| Files to add a vendor | about 10 new, 5 edited | about 10 new, 4 edited | about 10 new, 3 edited | about 6 new, 5 edited | about 12 new, 4 edited |
| Files to add a domain | about 18 new, 8 edited, plus workflow rules | about 20 new, 6 edited | about 28 new, 6 edited | about 35 new, 6 edited, plus 17 lint entries | about 20 new, 5 edited |
| Largest hand-written package | `workflow/runflow`, about 10k lines | `core/run`, about 10k lines | `adapter/postgres`, about 30k lines | `orchestration/domain/queue`, about 4k lines | `runs/internal/core/lifecycle`, about 5k lines |

The generated `deploy` server is the largest package in every option: 24k
lines for today's 72 operations and roughly 240k at the reference's 725.

### Totals

| | 1 Layered | 2 Hexagonal | 3 Clean | 4 DDD | 5 Modular monolith |
|---|---|---|---|---|---|
| Good | 8 | 9 | 5 | 9 | 16 |
| Fair | 7 | 9 | 8 | 6 | 4 |
| Poor | 5 | 2 | 7 | 5 | 0 |

The totals count criteria, not weight them. The recommendation below
weighs the roadmap table most, because the spine runs to P27 and today's
tree is 13 packages.

## Process topology

The repository shape and the process shape are separate questions. All five
options build one Go module, and all five can produce either topology below;
what differs is how cleanly each option's package graph yields the roles.

### Evidence

| Source | What runs | Evidence |
|---|---|---|
| Spacelift self-hosted | Every backend process is the one `spacelift` binary in one image, started with a different subcommand: `backend server`, `backend drain`, the legacy `scheduler`, and the optional `backend vcs-gateway`. Server and drain are stateless, share one PostgreSQL database and run migrations on start | [24, section 2.1](../reference/24-self-hosted-reference.md#21-process-inventory) |
| Spacelift, the scheduler fold | The standalone scheduler is folded into the drain from v6.4.0, so the core is two services: one serving requests, one consuming every queue and firing cron | [24, section 2.1](../reference/24-self-hosted-reference.md#21-process-inventory) |
| Spacelift queues | Eight logical queues, all consumed by the drain, on SQS or on PostgreSQL with no other broker; per-queue concurrency per drain instance | [24, section 3.3](../reference/24-self-hosted-reference.md#33-message-queues) |
| Spacelift workers | The launcher is a separate binary and image, run by the customer; it embeds the matching worker binary and starts one container per run; it needs outbound HTTPS only on the long-poll transport | [24, section 2.1](../reference/24-self-hosted-reference.md#21-process-inventory), [10, section 4.2](../reference/10-workers-and-execution.md#42-transports) |
| Spacelift Flows self-hosted | `server`, `worker` and `gateway` are three subcommands of one `spacelift-flows` binary in one image; agents are a separate image | [23, section 14.1](../reference/23-flows.md#141-components) |
| OTF | `otfd` holds API, UI, scheduler and an embedded runner; `otf-agent` is optional; singleton subsystems hold a PostgreSQL advisory lock so they run on one replica | [27, section 3.1](../reference/27-reference-materials.md#31-architecture) |

The pattern across all three products is the same: one backend binary,
several stateless processes chosen by subcommand, one PostgreSQL database
shared by them, and a separate executor on the customer's side that dials
out.

### The choices

| Concern | One binary, roles by subcommand | Separate binaries from one module | Separate services |
|---|---|---|---|
| What ships | `infraweave` with `serve`, `scheduler`, `worker`, `all`, the hidden `worker exec`, `vcs-agent`, `flows-agent`, `migrate` and `version`; one image | `infraweave-server`, `infraweave-scheduler`, `infraweave-worker`; one image each | A service per domain or role, each with its own release |
| Version skew | None between roles: one build | None if released together; possible if not | A real concern: API contracts between services need versioning |
| A run transition in one transaction | Yes: serve and scheduler share the database and the same module code | Yes | No, once services own separate data: run, lock, queue and receipts need sagas or a shared database |
| Independent scaling | Yes: each role is its own process and deployment | Yes | Yes |
| Isolation of API latency from queue load | Yes: `serve` never consumes queues | Yes | Yes |
| Singletons (cron firing, timers, stale-run sweeps) | None needed: every `scheduler` replica's ticker claims due rows with `FOR UPDATE SKIP LOCKED` (decision 0011), where OTF instead runs exclusive subsystems on one replica under an advisory lock | Same | Same, per service |
| Worker binary size and attack surface on customer hosts | Carries server code it never runs | Smallest: links only `worker` code | Smallest |
| Mounting the binary into runner images (`worker exec`) | The same static binary the launcher runs | The worker binary | The worker binary |
| Single-node installs and acceptance tests | `infraweave all` | Two processes | Many processes |
| Configuration | One strict YAML schema, sections per role | One schema per binary, or a shared one | One per service |
| Matches Spacelift's shape | Yes for the backend; Spacelift ships its launcher separately | Close | No |

Each option file shows how its structure produces the roles, and they
differ only in where the wiring lives:

| Option | `serve` and `scheduler` | `worker` |
|---|---|---|
| 1 Layered | `internal/app` starts `transport` entry points: the router, or queue consumers and cron | `internal/worker`, a column beside the layers |
| 2 Hexagonal | `internal/app` picks driving adapters over the same core | `internal/worker`, a second hexagon |
| 3 Clean | `internal/app` starts controllers or job runners | `app/workerapp` over `usecase/agent` and `adapter/agent` |
| 4 DDD | `internal/app` starts each context's `interfaces/rest` or `interfaces/consumers` and cron | `internal/execution/agent` |
| 5 Modular monolith | `internal/app/roles.go` calls each module's `Routes`, or its `Consumers` and `Cron` | `internal/agent`, wired from `cmd/infraweave/worker.go` without `internal/app` |

### Verdict

**One `infraweave` binary with the roles `serve`, `scheduler` and
`worker`, plus `all` for single-node installs and acceptance tests, and a
hidden `worker exec` inside run containers.** Separate services buy
nothing the roles do not already give (independent scaling, isolation of
API latency from queue load) and cost the one property the run lifecycle
cannot do without: a run transition, its stack lock, its queue item, its
policy receipts and its event committing together
([report 08, section 4](../reference/08-run-lifecycle.md#4-queueing-the-stack-lock-and-scheduling)).
Spacelift reached two stateless processes from one binary by folding its
scheduler into its drain, and its Flows product repeats the pattern.

infraweave keeps `scheduler` as the drain's equivalent with cron inside it,
not a separate cron process, the shape Spacelift's drain has from v6.4.0.
A separate cron process is one more deployment that must be running: the
Spacelift chart warns that with the standalone scheduler forced off on an
older version nothing fires drift detection, scheduled runs, tasks or
deletes ([report 24](../reference/24-self-hosted-reference.md#22-components-each-module-creates)).
A gateway for VCS agents and Flows agents starts as listeners inside
`serve`, and becomes a fourth role only if its long-lived connections need
their own scaling.

The worker is the one place where a separate build earns a mention. All
five options keep worker-side code free of server packages, so a slim
`cmd/infraweave-worker` that links only the worker tree is a one-file
addition. It is not the default: one binary keeps the launcher and the
mounted `worker exec` at the same version as the server by construction,
which Spacelift gets by embedding the worker binary in its launcher
([report 24](../reference/24-self-hosted-reference.md#21-process-inventory)).

## Recommendation

**Option 5, the modular monolith, built as one `infraweave` binary with
the `serve`, `scheduler`, `worker` and `all` roles.** It leads the roadmap
table outright, is rated Poor on nothing, and is the only option whose
boundary is enforced mainly by the Go compiler rather than by a growing
lint configuration.

Three refinements from the other options carry over, because they fit
inside option 5 without changing its rules:

- **Pure rules as pure packages (from option 3).** The run state machine
  and its transition table, the seven priority tiers, policy combination
  per type, space reachability and version numbering live in packages with
  no I/O inside their modules (`runs/internal/core/lifecycle` and its
  peers) and are tested as tables.
- **An anticorruption package per VCS provider (from option 4).** Each
  provider's webhook shapes, signatures and API quirks stay in its own
  package inside `vcs`, behind one normalised push and pull request event
  ([report 11, section 11.2](../reference/11-vcs-integrations.md#112-what-an-event-becomes)).
- **Hexagons inside the large modules (from option 2).** `runs`, `vcs`,
  `workers`, `access`, `registry`, `events`, `intelligence` and `flows`
  split `core/` from `adapters/`, as option 5 already lays out.

### What makes it hold

- **The consistency boundary is a module boundary.** `runs` owns every
  table a transition writes, so the five-part transition commits in one
  transaction without a cross-module write
  ([5-modular-monolith.md](5-modular-monolith.md#run-execution-path)).
- **Policies return decisions as data.** `policies` owns the input
  contract of [report 09](../reference/09-policies.md#8-where-each-type-is-evaluated)
  and imports none of its callers, so push, approval, plan, trigger and
  notification evaluation add no cycles.
- **The compiler does most of the enforcement.** Nested `internal/`
  directories make a module's insides unreachable; depguard covers six
  rules with about 75 entries at P27, against 130 to 750 for the other
  options.
- **The worker tree stays server-free.** `internal/agent` imports no module
  and no persistence, which keeps private workers outbound-only and keeps
  the slim worker build a one-file option.
- **Roles are module contributions.** `Routes`, `Consumers` and `Cron`
  decide what `serve` and `scheduler` start, so a new module joins both
  roles by implementing three methods.
- **Late domains are additive.** Templates, resources, Intelligence and
  Flows are new directories with their own migration streams; Flows keeps
  its own tenancy and can leave the binary if it must.

### What would change it

- **Decision 0011 reversed in favour of Temporal.** 0011 rejects Temporal
  today. A workflow engine run as a separate
  cluster would own run orchestration, moving the run state machine out of
  a PostgreSQL transaction and into workflow history. The `runs` module
  would become a workflow definition plus activities, and option 4's
  event-driven contexts would fit that shape better.
- **`runs` outgrowing one module.** If the run module passes about a
  quarter of the hand-written code, or two phases routinely collide in it,
  split it along its own seams (lifecycle, scheduling, handover,
  dependencies) as sub-modules with their own `api/`, keeping one
  migration stream.
- **A requirement to deploy Flows or Intelligence separately.** The
  modules' `api/` seams make extraction feasible; if it becomes a launch
  requirement rather than an option, option 4's published-event context
  map is the better starting point for those domains.
- **Modules landing without their depguard entries.** Rules 4, 6 and 7
  name each module, so a new module that skips its three or four entries
  is unguarded on those rules until someone notices. If that recurs, the
  entries have to be generated from the directory list, as option 3 does
  for its rule 2, and the enforcement cost rises toward the other options'.
- **The coverage floor.** If the thin `api/` and `module/` packages cannot
  reach 90% without tests written for the floor, the coverage exclusions,
  not the layout, are the place to adjust, and that adjustment needs its
  own decision.
- **Customer review of the worker binary.** If customers require a worker
  build without server code, ship `cmd/infraweave-worker` alongside the
  one binary; nothing else in the layout changes.

## Appendix: distance from today's tree

Today's tree is small: 13 Go packages, of which one is generated server
code and one a generated client. Every option keeps `api/`, `pkg/client/deploy`,
`tools/*` and `test/acceptance` where they are, and moves the rest.

| Today | 1 Layered | 2 Hexagonal | 3 Clean | 4 DDD | 5 Modular monolith |
|---|---|---|---|---|---|
| `cmd/` (only `README.md`) | `cmd/infraweave`, `cmd/infraweavectl` (P24) | same | same | same | same |
| `api` (`doc.go`, `deploy_test.go`) | unchanged | unchanged | unchanged | unchanged | unchanged |
| `api/src/deploy` | unchanged; `api/src/worker` (P8) | same | same | same; `api/src/flows` (P26) | same |
| `api/reference/spacelift` | unchanged | unchanged | unchanged | unchanged | unchanged |
| `internal/buildinfo` | `internal/support/buildinfo` | unchanged | `internal/infrastructure/buildinfo` | `internal/shared/buildinfo` | `internal/platform/buildinfo` |
| `internal/docscheck` | `internal/tooling/docscheck` | `internal/tooling/docscheck` | `internal/devtool/docscheck` | `internal/tooling/docscheck` | `internal/devtools/docscheck` |
| `internal/openapibundle` | `internal/tooling/openapibundle` | `internal/tooling/openapibundle` | `internal/devtool/openapibundle` | `internal/tooling/openapibundle` | `internal/devtools/openapibundle` |
| `internal/specref` | `internal/tooling/specref` | `internal/tooling/specref` | `internal/devtool/specref` | `internal/tooling/specref` | `internal/devtools/specref` |
| `internal/stubgen` | `internal/tooling/stubgen` | `internal/tooling/stubgen` | `internal/devtool/stubgen` | `internal/tooling/stubgen` | `internal/devtools/stubgen` |
| `internal/server/gen/deploy` | `internal/transport/http/gen/deploy` | `internal/adapter/driving/http/gen/deploy` | `internal/adapter/rest/gen/deploy` | `internal/shared/gen/deploy` | `internal/platform/gen/deploy` |
| `internal/server/problem` | `internal/transport/http/problem` | `internal/adapter/driving/http/problem` | `internal/adapter/rest/problem` | `internal/shared/httpkit/problem` | `internal/platform/httpkit/problem` |
| `pkg/client/deploy` | unchanged | unchanged | unchanged | unchanged | unchanged |
| worker protocol client (P8) | internal, `internal/worker/protoclient` | internal | internal, `adapter/agent/workerclient/gen` | public, `pkg/client/worker` | internal, `internal/agent/wire` |
| `tools/openapi-bundle`, `tools/specref`, `tools/stubgen` | unchanged; import paths follow | same | same; adds `tools/depguardgen` | same | same |
| `test/acceptance` | unchanged | unchanged | unchanged | unchanged | unchanged |

Root files that change in every option:

| File | Change |
|---|---|
| `api/oapi-server-deploy.yaml` | `output` names the new generated-server path |
| `Makefile` | `generate` passes the new path to stubgen; `verify-generate` diffs it; `LDFLAGS` follow `buildinfo` where it moves; `docs-check` runs the moved package |
| `.golangci.yml` | depguard enabled with the option's rules; it is not enabled today |
| `.testcoverage.yml` | the `^internal/server/gen/` exclusion follows the generated path, plus the internal worker client where an option keeps one |

The moves are mechanical at today's size: five or six packages, one
generated path, four root files. The distance that matters is the one each option
puts between P1 and the spine's later phases, which the matrix above
measures.
