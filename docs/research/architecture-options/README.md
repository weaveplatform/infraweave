# Architecture options

infraweave is one Go module that serves Spacelift's feature set behind a
REST API ([decision 0001](../decisions/0001-rest-first-spacelift-surface.md)).
Today it holds a served skeleton: a bundled OpenAPI document, a generated
strict chi server whose 72 operations answer 501, a generated client,
problem details, and the tooling that builds the Spacelift reference. The
roadmap in [report 26](../reference/26-scope-and-tiers.md#14-provisional-phase-spine)
grows it through 27 phases to runs, workers, policies, VCS, state, registries,
events, templates, Intelligence and Flows. Flows is designed in from the
start: its phases follow the events phase (P18) directly, on the event model
the queue phase (P7) lays down.

This directory lays out five repository architectures for that roadmap, each
in full against infraweave's own domains and the accepted decisions, compares
them, weighs the process topology, and recommends one.
[Decision 0004](../decisions/0004-repository-architecture.md) records the
recommendation. Every option places the same accepted design: the SDK, CLI
and provider of [0001](../decisions/0001-rest-first-spacelift-surface.md),
the shared event model and streams of
[0015](../decisions/0015-events-notifications-webhooks-audit.md), the Go
Flows engine and `App` contract of
[0018](../decisions/0018-flows-runtime.md), the feature catalogue and tiers
of [0021](../decisions/0021-capability-scope-and-supersession.md), the
console of [0023](../decisions/0023-console.md) and the expression language
of [0024](../decisions/0024-expression-language.md).

## Index

| Option | File | Shape in one line |
|---|---|---|
| 1 | [Layered](1-layered.md) | Horizontal layers: transport, service, repository and infrastructure, model; imports point down |
| 2 | [Hexagonal](2-hexagonal.md) | One application core with ports; driving and driven adapters around it |
| 3 | [Clean](3-clean.md) | Concentric rings: entities, use cases, interface adapters, frameworks; imports point inward |
| 4 | [Domain-driven design](4-ddd.md) | Bounded contexts, each with domain, application, infrastructure and interfaces packages, joined by a context map |
| 5 | [Modular monolith](5-modular-monolith.md) | Business domains with published `api/` packages, an outbox between them, hexagons inside the larger domains |

Every option file has the same sections in the same order, so the options
can be read side by side:

- Summary, with the dependency rules;
- Repository root;
- Full package tree;
- Deep dives on infraweave's domains: the stack configuration write path, the
  run execution path, policy evaluation with embedded OPA, the worker protocol
  and pools, VCS webhook ingest, the state backend, the module and provider
  registry protocol, access, identity and spaces, features and tiers, events,
  the Flows engine, expressions, the console and clients, the composition
  root, generated code, and tests and enforcement;
- Walkthroughs: a GitHub push to a proposed run, a plan policy and a PR
  comment; a tracked run that awaits approval and applies on a private
  worker; a drift event that starts a flow; adding the Pulumi vendor; adding
  the templates domain;
- Package mapping from today's tree;
- Numbers;
- Placement choices and friction.

Facts every option shares, so that the comparison is about structure alone:

- one Go module and one server binary, `cmd/infraweave`, with the process
  modes `serve`, `scheduler`, `launcher` and `all`, the hidden `runner`, and
  the `vcs-agent`, `flows-agent`, `migrate`, `auth hash-password`,
  `secrets generate-kek`, `secrets rewrap`, `secrets rotate` and `version`
  subcommands
  ([Process topology](#process-topology)); the slim `cmd/infraweave-agent`
  build of the agent process modes alone; and the separate client binary
  `cmd/infraweavectl` over the SDK, `pkg/sdk`, which wraps the generated
  `pkg/client/<spec>` packages. The Terraform provider lives in its own
  repository, `weaveplatform/terraform-provider-infraweave`;
- PostgreSQL and object storage behind one contract suite for the `s3`,
  `gcs`, `azureblob` and `fs` drivers, with no bucket versioning (decision
  0005), decision 0011's own job queue in PostgreSQL, embedded OPA
  (decision 0009), and HTTP long-poll for workers (decision 0008) and VCS
  agents (decision 0012);
- FIPS 140-3 from the start: release builds use `GOFIPS140=certified`, and
  every sealed format (random-nonce GCM object segments, the ECDH P-256
  pool envelope) carries a version byte (decision 0013);
- one event model for Deploy and Flows, with Flows trigger blocks as a
  fan-out target and server-sent-event streams in the public API
  (decision 0015);
- Flows as a Go engine on the queue's `flows` logical queue in
  `scheduler`, with apps as versioned protobuf `App` gRPC services in
  containers run by `flows-agent`, the Go app SDK `pkg/flows/appsdk`, and
  no JavaScript runtime for apps or expressions (decision 0018);
- root, organization, account and spaces: root holds organizations, an
  organization holds accounts, members, identity providers, the tier,
  grants and limits pooled across its accounts, and an account is the
  data boundary whose top space is addressed by its slug; members
  authenticate at the organization and are admitted per account by its
  `LOGIN` policies, and the account admin holds `space-admin` on the top
  space (decisions 0006, 0007 and 0021);
- one feature catalogue of capabilities, options and limits with seeded
  tiers, a tier and grants per organization with account limits below the
  pool, `billing` off by default, an operator-only API that creates
  organizations, and one chi middleware (decision 0021);
- CEL expressions and `${{ }}` text templates in one package, evaluated by
  the server process modes and, for workflow command templates, by the
  runner, with editor endpoints under `/expressions/` (decision
  0024);
- the console in `web/console/`, React 19 and TypeScript built with Vite,
  embedded through `embed.FS` and served by `serve`, reaching infraweave
  only through the public API, with `allowedActions` on resources
  (decision 0023);
- the launcher, runner and agent process modes link no server-side
  persistence, because private workers and agents run on customer hosts
  with outbound HTTPS only
  ([report 10, section 4.2](../reference/10-workers-and-execution.md#42-transports));
- one generated strict server per served document (`deploy`, `worker`,
  `operator`, `flows`), composed from per-domain partial implementations by
  embedding, with the 501 stubs one level deeper; protocols that are not
  infraweave's own REST API (VCS webhooks, the Terraform `http` state
  backend, service discovery, the registry protocols, the Flows agent
  gateway) are hand-written chi routes;
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
| The worker column keeps pgx and server code off private workers and Flows agents | About 63 depguard rules by P27, and the workflow order is maintained by hand |
| A new vendor is about 10 new files and no change to `runflow` | Flows, or any domain, has no single directory, so extracting it later is hard |
| One shared `model/event` makes Deploy and Flows one event model for free; expressions and sealed formats sit in `support`, which the worker already imports | Features need a second sanctioned service import and a dequeue hook from `app`; typed expression contexts need an eighth workflow, `exprflow` |

**Roadmap verdict.** Comfortable for P1 to P4, bending from P5, held
together from P7 to P13 only by the workflow tier and its order, and
steadily more expensive after P13: every late domain (P21 to P23, P25 and
the Flows phases) spreads across six trees and edits shared files and lint
rules.

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
| The worker hexagon shares only `shared/expr` and `shared/seal` with the server | Wide driving ports (`Runs`, `RunProgress`) and large fakes |
| Process modes are choices of driving adapters over the same constructors | No rule says which domains may write inside another's transaction; Flows shares the core and the schema stream |
| One core makes the cross-cutting design cheap: the shared event model, entitlement checks and `allowedActions` are calls inside the core | `driving.Entitlements`, `driving.Surface` and `AllowedActions` widen every domain's ports, fakes and the shared `types.go` |

**Roadmap verdict.** Strong on the infrastructure-heavy phases (P2, P7,
P8, P11, P12, P13, P14, P15, P19), each of which lands as an adapter behind
a port the core already names, and on the accepted design's cross-cutting
concerns, which a single core reaches without new seams. Weaker from P16
onward, where each new domain is another sibling in a single core held
apart only by rule 3, and parallel lanes contend for the shared port files.
Extracting Flows later means splitting the schema and the composition
root.

### Option 3: Clean

[3-clean.md](3-clean.md) arranges rings: `internal/entity` (pure rules:
the run state machine, priority levels, policy combination, space
reachability, version numbering, vendor change models),
`internal/usecase/<family>` (interactors with boundaries and consumer-side
ports), `internal/adapter` (controllers, presenters, gateways) and
`internal/infrastructure`. A second cut keeps worker-side code in
`usecase/agent`, `adapter/agent` and `app/workerapp`.

| Pros | Cons |
|---|---|
| The rule-heavy cores (run machine, tiers, policy combination, RBAC, version numbering) are pure and table-tested | Every operation needs a controller, input and output boundaries and presenters; the Stacks family alone is thousands of mapping lines |
| Per-consumer ports remove policy cycles: run, VCS and review use cases each declare a decision port | Domains are invisible: the run lifecycle spans about ten unrelated-looking packages |
| Framework swaps (chi, pgx, OPA, SDKs) stay inside one adapter | depguard has no variable for the importing package, so "no sibling use case" takes about 116 generated rules and a new `tools/depguardgen` |
| A vendor is mostly one pure entity package shared by server and worker | `usecase/runstate` is a shared service at the system's centre |
| Inward-only direction plus a worker allow-list keeps persistence off workers | Event fan-out and Flows' separate model are a file and a naming prefix, not structure |
| Entitlement, sealed formats and the expression language are pure entities the runner can share | Limits need a consumer-side port in every creating interactor, and `allowedActions` travels through every output boundary and presenter |

**Roadmap verdict.** Excellent for P7 to P10, P13 and P17, whose rules are
pure entities. Expensive for the CRUD-heavy phases (P5, P6, P15, P16, P18,
P21 to P23), which add boundaries and presenters for little rule content.
No structure for the late, separable domains: Intelligence (P25) reads
across everything unfenced, and the Flows phases cannot express Flows' own
projects and identifiers.

### Option 4: Domain-driven design

[4-ddd.md](4-ddd.md) draws eighteen bounded contexts (identity, features,
stackconfig, orchestration, execution, vendors, state, sourcecontrol,
governance, cloud, registry, notifications, audit, templates, inventory,
intelligence, expressions, flows), each with `domain`, `application`, `infrastructure`
and `interfaces`, joined by a context map with an anticorruption layer per
VCS provider and published events through an outbox and bus.
`orchestration/domain/queue.StackQueue` holds a stack's open runs, its
blocking run and both locks.

| Pros | Cons |
|---|---|
| The run state machine and stack lock form one aggregate | P2 to P6 CRUD pays for aggregates, repositories, in-memory and SQL implementations, contract suites and events nobody reads yet |
| Flows separates cleanly: its own spec, tables and projects behind one anticorruption layer | The outbox and bus, with idempotency and dead letters, must exist before the first run |
| A VCS provider is one package; P19 touches no other context | Consequences are eventually consistent (stack state, checks, audit); acceptance tests wait for effects |
| Governance as an open host service gives policy hooks without cycles | 43 depguard rules and about 520 entries; every new context edits every boundary rule |
| The context map makes every cross-domain effect explicit; any context could become a service | About 375 packages, about 70 thin; policy receipts commit before the transition that uses them; `StackQueue` is a hot aggregate |
| The expression language and sealed formats fit a shared kernel; event schemas double as typed expression contexts | One event model for Deploy and Flows cuts across the context map, so the envelope joins the kernel; limits and stack `allowedActions` read across contexts |

**Roadmap verdict.** The strongest shape for the middle and late spine:
run integrity (P7 to P10), policy hooks (P13), VCS growth (P12, P19), event
fan-out (P18), and separable late contexts (P21, P22, P25 and the Flows
phases). The weakest start: plain CRUD pays the full tactical cost, and event
infrastructure must precede the first run. The accepted design's
cross-cutting pieces (one event model, one feature gate, `allowedActions`
on resources that span contexts) pull against its context boundaries. It suits a team that expects to
split into services more than one that wants the quickest path through 27
phases in one binary.

### Option 5: Modular monolith

[5-modular-monolith.md](5-modular-monolith.md) gives each business domain
its own directory, `internal/<domain>/`, with a published `api/` package, an
`assembly/` constructor that contributes routes, queue consumers and cron, an
`internal/` directory the compiler keeps private, and its own migration
stream. Nineteen domains at P27 (features, access, stacks, configuration,
runs, workers, vendors, state, vcs, policies, cloud, schedules, registry,
events, templates, resources, intelligence, flows, expressions), joined by
one declared graph that both depguard and the schema check read; shared
infrastructure in `internal/platform`, value types in `internal/kernel`,
pure vendor code in `internal/iac`, and everything that runs on a worker or
agent host in `internal/agent`.

| Pros | Cons |
|---|---|
| A run transition (run row, transition, stack run lock, queue item, receipts, outbox event) is one transaction inside one domain | `runs` holds about a fifth of the hand-written code and is touched by P7, P9, P10, P13 and P16 |
| The Go compiler enforces the domain boundary through nested `internal/`; one depguard rule per domain declares the graph, and no foreign key crosses it | Stack state is runs data; a stack list filtered by state reads a projection runs writes through `stacksapi` |
| Policies own the input contract and return decisions as data, so policy hooks form no cycle | Six back edges (stacks to runs; runs to vcs, cloud and state; workers to runs; access to policies) are bound after construction |
| `internal/agent` imports no domain and no persistence, so `cmd/infraweave-agent` is a short `main.go` | About 325 packages, about 40 thin ones under the 90% floor; 26 depguard rules and about 270 entries, most of them the graph |
| Each domain contributes `Routes`, `Consumers`, `Cron` and `Surfaces`, which map straight onto `serve` and `scheduler` | Long import paths and every constructor package named `assembly`, so imports need aliases |
| A late domain is a new directory plus about 6 edits; Flows has its own schema and can be extracted | A held ID costs a hold, a guard and a purge in code that a foreign key would give |
| The event model, sealing and expressions are platform, `features` sits at the bottom of the graph, and Flows triggers are one more fan-out consumer | The agent's platform allow-list holds seven packages, and `featuresapi` is one of the widest dependencies |

**Roadmap verdict.** Fits the spine phase by phase: P1 to P6 add one
domain each; P7 to P10 happen inside `runs`, whose single-transaction
transitions already hold them; P11 to P19 are new domains over existing
`api/` packages and the outbox; P20 touches only `internal/iac` and
`internal/agent`; the Flows phases, P21 to P23 and P25 add directories
without changing existing ones. The cost comes early and stays: the domain skeleton, thin packages
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
| Fit with the generated strict server and stubgen | Good | Good | Fair | Fair | Fair |
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
  Clean wraps each generated type in boundaries and presenters, DDD
  assembles the Stacks representation from two contexts, and option 5
  reads a stack's state, blocker and run lock from `runs` through a back
  edge.

### Roadmap shape

How each option carries the 27-phase spine.

| Criterion | 1 Layered | 2 Hexagonal | 3 Clean | 4 DDD | 5 Modular monolith |
|---|---|---|---|---|---|
| Run-transaction integrity: run, lock, queue item, receipts and event in one transaction (P7 to P10) | Good | Good | Good | Fair | Good |
| Policy hooks at run state points without import cycles (P13) | Fair | Good | Good | Good | Good |
| Worker-side isolation: the launcher process mode links no server persistence (P8, P14) | Good | Good | Good | Good | Good |
| VCS provider growth (P12, P19) | Fair | Good | Good | Good | Good |
| IaC vendor growth (P9, P20) | Good | Fair | Good | Fair | Good |
| Event fan-out to inbox, Slack, webhooks, PR comments and audit (P18) | Fair | Fair | Fair | Good | Good |
| Flows separability, with its own projects, identifiers and schema (Flows phases) | Poor | Fair | Poor | Good | Good |
| Late-domain addition without touching existing cores (P21, P22, P25) | Poor | Fair | Fair | Good | Good |
| Process modes fall out of the structure (P7, P27) | Good | Good | Fair | Good | Good |
| Extractability of a domain into a separate service | Poor | Poor | Poor | Good | Fair |

Notes:
- DDD is Fair on run integrity because policy receipts commit in
  governance's transaction before the run transition that relies on them
  ([4-ddd.md](4-ddd.md#placement-choices-and-friction)).
- Layered is Fair on policy hooks because acyclicity rests on the
  hand-kept workflow order rather than on the domains.
- Hexagonal and DDD are Fair on vendors: a vendor spans two hexagons in
  option 2, and the kernel plus four contexts in option 4.
- Clean is Fair on process modes because the worker side is a second axis across
  the rings.
- The modular monolith is Fair on extractability: a domain's `api/` seam
  is ready and no foreign key crosses it, but the holds taken in the
  caller's transaction, the shared `jobs` table and, for Flows, the fan-out
  transaction that inserts a trigger's first event all bind it to the one
  database.

### The accepted design

How each option places the concerns that decisions 0001, 0015, 0018, 0021,
0023 and 0024 add across the whole system. Each one cuts across domains, so
these rows reward a shape with one obvious place for cross-cutting work.

| Criterion | 1 Layered | 2 Hexagonal | 3 Clean | 4 DDD | 5 Modular monolith |
|---|---|---|---|---|---|
| Flows engine and app contract: a Go engine on Deploy's event model and queue, the `App` gRPC contract, `flows-agent` with app containers (P7, Flows phases) | Fair | Good | Fair | Fair | Good |
| Expressions: one CEL package for Flows, templates, notifications, environment and the runner, with typed contexts from each owner (P9 onward) | Fair | Good | Good | Good | Good |
| Features and tiers: one catalogue gate, option checks in validation, limit checks where the counted thing is created, gated background work (P3b onward) | Fair | Good | Fair | Fair | Good |
| Console and clients: `pkg/sdk`, `infraweavectl` and the console over the public API only, with `allowedActions` from the enforcing code (P24 to P24c) | Good | Good | Fair | Fair | Good |

Notes:
- Flows: layered spreads the engine over eight places and the workflow
  tier, and clean over use-case families with a naming prefix. DDD separates
  Flows best, but decisions 0015 and 0018 require one event model with no
  bridge, so its per-context published language becomes a shared kernel
  and the anticorruption layer covers only outbound Deploy calls
  ([4-ddd.md](4-ddd.md#flows-engine)). Hexagonal and the modular monolith
  treat a Flows trigger as one more consumer of the same fan-out.
- Expressions: every option keeps the language pure and reachable from the
  runner. Layered is Fair because typed contexts span services that may not
  import each other, which needs an eighth workflow,
  `exprflow` ([1-layered.md](1-layered.md#expressions)).
- Features: layered breaks its service rule a second time and hands the
  queue a closure from `app`; clean declares a limit port in every creating
  interactor; DDD reads limits across contexts, either synchronously
  through an open host service or with a downgrade race
  ([4-ddd.md](4-ddd.md#features-and-tiers)). Hexagonal's single core and
  option 5's `features` domain at the bottom of the graph make every check
  one call.
- Console and clients: every option places `pkg/sdk`, `cmd/infraweavectl`
  and `web/console` alike. The difference is `allowedActions`: clean adds
  it to every output boundary and presenter, and in DDD a stack's actions
  come from a projection of orchestration's `StackQueue`, so a list can
  offer an action the server then refuses
  ([4-ddd.md](4-ddd.md#console-and-clients)).

### Cost of the structure

What each option costs to keep, measured at the end of P27 from each
file's Numbers section.

| Criterion | 1 Layered | 2 Hexagonal | 3 Clean | 4 DDD | 5 Modular monolith |
|---|---|---|---|---|---|
| Package count | Good | Good | Fair | Poor | Fair |
| depguard rule burden | Fair | Fair | Poor | Poor | Fair |
| Coverage-floor friction (thin packages under 90%) | Good | Good | Fair | Poor | Fair |
| Indirection per request (packages a stack update crosses) | Fair | Fair | Poor | Fair | Fair |
| Files touched to add a domain | Fair | Fair | Fair | Poor | Good |
| Shared hotspot files for parallel phases | Poor | Poor | Poor | Fair | Fair |

Clean is Poor on shared hotspots for the same reason as layered and
hexagonal: every phase edits `app/usecases.go`, `app/controllers.go`,
`app/events.go`, `adapter/rest/server.go`, the single migration stream and
the generated lint block. The modular monolith is Fair on depguard burden:
it has the fewest rules, but decision 0004's declared graph, one rule per
domain that the schema check also reads, puts its entries between
hexagonal's and layered's. Hexagonal's and option 5's six edits to add a
domain differ in kind: hexagonal's include the shared port files that every
phase edits, option 5's are the composition root, the router, the
configuration, the lint file, the catalogue and the domain's own graph
rule. Those six are still the modular monolith's hotspots, with the `runs`
migration stream that P7 to P16 share, which is why it is Fair rather than
Good on shared hotspots.

The numbers behind the cost ratings:

| Measure at P27 | 1 Layered | 2 Hexagonal | 3 Clean | 4 DDD | 5 Modular monolith |
|---|---|---|---|---|---|
| Go packages | about 230 | about 200 | about 285 | about 375 | about 325 |
| Top-level directories under `internal/` | 11 | 8 | 6 | 22 | 25 |
| Dependency rules | 8 | 10 | 9 | 9 | 9 (1 by the compiler) |
| depguard rules, entries | about 63, about 290 | about 33, about 145 | about 125, about 800 (116 generated) | 43, about 520 | 26, about 270 |
| Thin packages at risk under the 90% floor | about 10 | about 14 | about 30 | about 70 | about 40 |
| Packages a stack update crosses | 8 | 6 | 10 | 8 | 7 |
| Files to add a vendor | about 10 new, 6 edited | about 10 new, 4 edited | about 10 new, 4 edited | about 6 new, 5 edited | about 12 new, 5 edited |
| Files to add a domain | about 18 new, 10 edited, plus workflow rules | about 20 new, 6 edited | about 28 new, 7 edited | about 36 new, 7 edited, plus 19 lint places | about 20 new, 6 edited |
| Largest hand-written package | `workflow/runflow`, about 10k lines | `core/run`, about 10k lines | `adapter/postgres`, about 30k lines | `orchestration/domain/queue`, about 4k lines | `runs/internal/core/lifecycle`, about 5k lines |

Every count includes the features gate a stack update passes: it is a
package of its own in layered, clean, DDD and option 5, and a file in
hexagonal's existing middleware package.

The generated `deploy` server is the largest package in every option: 24k
lines for today's 72 operations and roughly 240k at the reference's 725.

### Totals

| | 1 Layered | 2 Hexagonal | 3 Clean | 4 DDD | 5 Modular monolith |
|---|---|---|---|---|---|
| Good | 9 | 13 | 6 | 10 | 16 |
| Fair | 10 | 9 | 11 | 9 | 8 |
| Poor | 5 | 2 | 7 | 5 | 0 |

The totals count 24 criteria, not weight them. The recommendation below
weighs the roadmap and accepted-design tables most, because the spine runs
to P27, the accepted design is fixed, and today's tree is 13 packages.

## Process topology

The repository shape and the process shape are separate questions. All five
options build one Go module, and all five can produce either topology below;
what differs is how cleanly each option's package graph yields the process modes.

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

| Concern | One binary, process modes by subcommand | Separate binaries from one Go module | Separate services |
|---|---|---|---|
| What ships | `infraweave` with `serve`, `scheduler`, `launcher`, `all`, the hidden `runner`, `vcs-agent`, `flows-agent`, `migrate`, `auth hash-password`, the `secrets` key operations and `version`; the slim `infraweave-agent` with only the agent process modes; Flows apps as their authors' own images | `infraweave-server`, `infraweave-scheduler`, `infraweave-launcher`, `infraweave-flows-agent`; one image each | A service per domain or process mode, each with its own release |
| Version skew | None between process modes: one build | None if released together; possible if not | A real concern: API contracts between services need versioning |
| A run transition in one transaction | Yes: serve and scheduler share the database and the same domain code | Yes | No, once services own separate data: run, lock, queue and receipts need sagas or a shared database |
| Independent scaling | Yes: each process mode is its own process and deployment | Yes | Yes |
| Isolation of API latency from queue load | Yes: `serve` consumes no queue except the Flows agent tasks its sockets deliver | Yes | Yes |
| Singletons (cron firing, timers, stale-run sweeps) | None needed: every `scheduler` replica's ticker claims due rows with `FOR UPDATE SKIP LOCKED` (decision 0011), where OTF instead runs exclusive subsystems on one replica under an advisory lock | Same | Same, per service |
| Launcher and agent binary size and attack surface on customer hosts | `infraweave` carries server code it never runs; `infraweave-agent` links only the agent tree | Smallest: links only launcher code | Smallest |
| Flows apps on agent hosts | `flows-agent` starts one container per installation and app version digest and speaks gRPC to it locally; the agent needs Docker or a Kubernetes namespace | Same | Same |
| Mounting the binary into runner images (`runner`) | The same static binary the launcher runs | The launcher binary | The launcher binary |
| Single-node installs and acceptance tests | `infraweave all` | Two processes | Many processes |
| Configuration | One strict YAML schema, sections per process mode | One schema per binary, or a shared one | One per service |
| Matches Spacelift's shape | Yes for the backend; Spacelift ships its launcher separately | Close | No |

Each option file shows how its structure produces the process modes, and they
differ only in where the wiring lives:

| Option | `serve` and `scheduler` | `launcher` | `flows-agent` | `infraweave-agent` links |
|---|---|---|---|---|
| 1 Layered | `internal/app` starts `transport` entry points: the router, or queue consumers and cron | `internal/worker`, a column beside the layers | `worker/flowsagent` | the worker column |
| 2 Hexagonal | `internal/app` picks driving adapters over the same core | `internal/worker`, a second hexagon | `worker/core/flowsagent` with its container and gRPC adapters | `internal/app/agentcli` over the worker hexagon |
| 3 Clean | `internal/app` starts controllers or job runners | `app/workerapp` over `usecase/agent` and `adapter/agent` | `usecase/agent/flowsagent` with `adapter/agent` gateways | `app/workerapp` |
| 4 DDD | `internal/app` starts each context's `interfaces/rest` or `interfaces/consumers` and cron | `internal/execution/agent` | `internal/flows/agent` | the three context `agent/` trees |
| 5 Modular monolith | `internal/app/modes.go` calls each domain's `Routes`, or its `Consumers`, `Cron` and `Surfaces` | `internal/agent`, wired through `internal/agent/cli` without `internal/app` | `internal/agent/flowsagent` | `internal/agent` and seven platform packages |

### Verdict

**One `infraweave` binary with the process modes `serve`, `scheduler` and
`launcher`, plus `all` for single-node installs and acceptance tests, and a
hidden `runner` inside run containers.** Separate services buy
nothing the process modes do not already give (independent scaling, isolation of
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
The gateways for VCS agents and Flows agents start as routes inside `serve`,
and become a process mode of their own only if their long-lived connections
need their own scaling. The Flows gateway routes work through the queue:
the replica holding an agent's socket claims that pool's `flows.agent_task`
jobs, so any replica serves any agent and a lost replica loses only
sockets (decision 0018).

The Flows engine adds no server process mode: its jobs run in `scheduler`
on the `flows` logical queue, and its REST API, streams and gateway run in
`serve`. `flows-agent` is to Flows what `launcher` is to Deploy: it runs on
the customer's side, dials out, and runs untrusted code (here, Flows apps
serving the `App` contract) in containers it starts, speaking gRPC to them
only on the host or pod network it controls.

The agent hosts are where a separate build earns its place. All five
options keep worker-side and agent-side code free of server packages, so
`cmd/infraweave-agent`, which builds `launcher`, `runner`, `vcs-agent` and
`flows-agent` from the agent tree alone, is a short `main.go` in every
option. It ships beside the full binary for hosts whose image size or
review rules out server code (decision 0004). The full binary stays the
default: it keeps the launcher and the mounted `runner` at the same version
as the server by construction, which Spacelift gets by embedding the worker
binary in its launcher
([report 24](../reference/24-self-hosted-reference.md#21-process-inventory)),
and both builds come from one release.

## Recommendation

**Option 5, the modular monolith, still holds against the accepted design,
built as one `infraweave` binary with the `serve`, `scheduler`, `launcher`
and `all` process modes, and with the agent process modes also built alone
as `infraweave-agent`.** Weighed against decisions 0001 to 0024 as accepted,
it leads the roadmap table outright, rates Good on all four rows of the
accepted design, and is rated Poor on nothing. Hexagonal narrows the gap,
because its single core reaches the cross-cutting concerns as easily, but it
keeps its Poor ratings on shared hotspots and extractability and its Fair
ratings on Flows separability and late domains. Decision 0004's declared
graph costs option 5 its lead on lint burden: it adds about 150 depguard
entries, in exchange for an import graph the compiler and the linter hold,
and a schema check that keeps every key and view inside its domain.

Three refinements from the other options carry over, because they fit
inside option 5 without changing its rules:

- **Pure rules as pure packages (from option 3).** The run state machine
  and its transition table, the seven priority levels, policy combination
  per type, space reachability and version numbering live in packages with
  no I/O inside their domains (`runs/internal/core/lifecycle` and its
  peers) and are tested as tables. The expression language and the sealed
  formats follow the same rule in `internal/platform`.
- **An anticorruption package per VCS provider (from option 4).** Each
  provider's webhook shapes, signatures and API quirks stay in its own
  package inside `vcs`, behind one normalised push and pull request event
  ([report 11, section 11.2](../reference/11-vcs-integrations.md#112-what-an-event-becomes)).
- **Hexagons inside the large domains (from option 2).** `runs`, `vcs`,
  `workers`, `access`, `registry`, `events`, `intelligence` and `flows`
  split `core/` from `adapters/`, as option 5 already lays out.

### What makes it hold

- **The consistency boundary is a domain boundary.** `runs` owns every
  table a transition writes, so the five-part transition commits in one
  transaction without a cross-domain write
  ([5-modular-monolith.md](5-modular-monolith.md#run-execution-path)).
- **Policies return decisions as data.** `policies` owns the input
  contract of [report 09](../reference/09-policies.md#8-where-each-type-is-evaluated)
  and imports none of its callers, so push, approval, plan, trigger and
  notification evaluation add no cycles.
- **The compiler and one declared graph do the enforcement.** Nested
  `internal/` directories make a domain's insides unreachable. One depguard
  rule per domain names the `api/` packages it imports, and
  `TestAccSchemaDependencies` fails on any foreign key or view between two
  domains, whose references are held through the owner's `api/` instead
  ([5-modular-monolith.md](5-modular-monolith.md#tests-and-enforcement)).
- **One event model, no bridge.** The envelope, kind registry and emit path
  are platform packages below every domain, and a Flows trigger is one
  more consumer of the outbox's fan-out, owned by `flows`, so a run state
  change or a drift result starts a flow and `events` never imports
  `flows` ([5-modular-monolith.md](5-modular-monolith.md#a-drift-event-starts-a-flow)).
- **Each cross-cutting concern has one home.** `features` sits at the
  bottom of the graph, so every limit check is a call inside the creating
  transaction; the expression language is `platform/expr`, and each domain
  contributes its surfaces as it contributes routes; `allowedActions` is
  filled by the code that enforces the actions; the console is embedded by
  `platform/httpkit/console` and calls only the public API.
- **The worker and agent tree stays server-free.** `internal/agent` imports
  no domain and no persistence, only `internal/kernel`, `internal/iac` and
  seven platform packages, which keeps private workers and Flows agents
  outbound-only and makes `cmd/infraweave-agent` a short `main.go`.
- **Process modes are domain contributions.** `Routes`, `Consumers`,
  `Cron` and `Surfaces` decide what `serve` and `scheduler` start, so a new
  domain joins both process modes by implementing four methods, and Flows
  adds no server process mode.
- **Late domains are additive.** Templates, resources, Intelligence and
  Flows are new directories with their own migration streams; Flows keeps
  its own schema, projects and identifiers and can leave the binary if it
  must.

### What would change it

- **Decision 0011 reversed in favour of Temporal.** 0011 rejects Temporal,
  and 0018 builds the Flows engine on the same queue. A workflow engine run
  as a separate cluster would own run orchestration, moving the run state
  machine out of a PostgreSQL transaction and into workflow history. The
  `runs` domain would become a workflow definition plus activities, and
  option 4's event-driven contexts would fit that shape better.
- **`runs` outgrowing one domain.** If the `runs` domain passes about a
  quarter of the hand-written code, or more than a quarter of the pull
  requests touching it from P11 to P16 conflict inside it, move the parts
  outside the transition (scheduling, dependencies) into sub-domains with
  their own `api/`. Lifecycle and every table a transition writes stay in
  `runs`, so the transition keeps its one transaction.
- **A requirement to deploy Flows or Intelligence separately.** The
  domains' `api/` seams make extraction feasible, after turning their
  holds on `access` into remote checks, moving their work off the shared `jobs` table
  and, for Flows, replacing the fan-out transaction with delivery of
  published events; if it becomes a launch requirement rather than
  an option, option 4's published-event context map is the better starting
  point for those domains.
- **Deploy and Flows events needing separate contracts.** Decisions 0015
  and 0018 make one event model the design. If the two had to version
  their events apart, the shared envelope in the platform would become a
  translation layer, and option 4's published languages would fit better.
- **Guards that multiply.** Every held ID adds a guard to its owner's
  delete and move, and a purge to account deletion. If one owner gathers
  many guards, or a guard's query grows slow, the owner's deletes slow with
  it; that is the point to revisit the hold protocol, not the domain
  layout, and it needs its own decision.
- **Domains landing without their depguard entries.** Rules 4 and 6 name
  each domain. A domain directory without its graph rule or those entries
  fails `TestUnitDepguardFixtures`, and one with tables but no graph rule
  fails `TestAccSchemaDependencies`. If the entries still drift, they have
  to be generated from the directory list, as option 3 does for its rule 2,
  and the enforcement cost rises toward the other options'.
- **The agent's shared code outgrowing the platform.** If the runner, the
  launcher or the Flows agent needs an eighth platform package, or one of the
  seven gains a store or a key, the code both sides share moves into a tree
  of its own, as option 2's `internal/shared`, and rule 7 names that tree.
- **The coverage floor.** If the thin `api/` and `assembly/` packages cannot
  reach 90% without tests written for the floor, the coverage exclusions,
  not the layout, are the place to adjust, and that adjustment needs its
  own decision.

## Appendix: distance from today's tree

Today's tree is small: 13 Go packages, of which one is generated server
code and one a generated client. Every option keeps `api/`, `pkg/client/deploy`,
`tools/*` and `test/acceptance` where they are, and moves the rest.

| Today | 1 Layered | 2 Hexagonal | 3 Clean | 4 DDD | 5 Modular monolith |
|---|---|---|---|---|---|
| `cmd/` (only `README.md`) | `cmd/infraweave`, `cmd/infraweave-agent`, `cmd/infraweavectl` (P24b) | same | same | same | same |
| `api` (`doc.go`, `deploy_test.go`) | unchanged | unchanged | unchanged | unchanged | unchanged |
| `api/src/deploy` | unchanged; `api/src/worker` (P8), `api/src/operator` (P3b), `api/src/flows` and `api/proto/flows/app/v1` (Flows) | same | same | same | same |
| `api/reference/spacelift` | unchanged | unchanged | unchanged | unchanged | unchanged |
| `internal/buildinfo` | `internal/support/buildinfo` | unchanged | `internal/infrastructure/buildinfo` | `internal/shared/buildinfo` | `internal/platform/buildinfo` |
| `internal/docscheck` | `internal/tooling/docscheck` | `internal/tooling/docscheck` | `internal/devtool/docscheck` | `internal/tooling/docscheck` | `internal/devtools/docscheck` |
| `internal/openapibundle` | `internal/tooling/openapibundle` | `internal/tooling/openapibundle` | `internal/devtool/openapibundle` | `internal/tooling/openapibundle` | `internal/devtools/openapibundle` |
| `internal/specref` | `internal/tooling/specref` | `internal/tooling/specref` | `internal/devtool/specref` | `internal/tooling/specref` | `internal/devtools/specref` |
| `internal/stubgen` | `internal/tooling/stubgen` | `internal/tooling/stubgen` | `internal/devtool/stubgen` | `internal/tooling/stubgen` | `internal/devtools/stubgen` |
| `internal/server/gen/deploy` | `internal/transport/http/gen/deploy` | `internal/adapter/driving/http/gen/deploy` | `internal/adapter/rest/gen/deploy` | `internal/shared/gen/deploy` | `internal/platform/gen/deploy` |
| `internal/server/problem` | `internal/transport/http/problem` | `internal/adapter/driving/http/problem` | `internal/adapter/rest/problem` | `internal/shared/httpkit/problem` | `internal/platform/httpkit/problem` |
| `pkg/client/deploy` | unchanged; `pkg/sdk` (P24) wraps it | same | same | same | same |
| generated `App` stubs (Flows) | inside `pkg/flows/appsdk` | `pkg/flows/appsdk/appv1` | `pkg/flows/appv1` | `pkg/flows/app/v1` | `pkg/flows/app/v1` |
| console embedding (P24c) | `web/console` embed package, served by `transport/http/console` | `web/console/console.go`, served by `adapter/driving/http/console` | `web/console/console.go`, served by `infrastructure/console` | `web/console/embed.go`, served by `shared/httpkit/console` | build written into `internal/platform/httpkit/console/dist` and embedded there |
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
