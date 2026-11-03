# Option 4: Domain-driven design, detailed layout

Detailed repository layout for option 4 of the [architecture options](README.md):
bounded contexts drawn around infraweave's business language, each with the same four
tactical layers (`domain/`, `application/`, `infrastructure/`, `interfaces/`), a
published `api/` per context, a shared kernel of identifiers, and domain events that
carry every consequence one context's change has in another. Every package in today's
tree is mapped; roadmap packages are marked with the phase of [report
26](../reference/26-scope-and-tiers.md#14-provisional-phase-spine) that creates them.

Conventions in the trees:

- `← internal/server/problem` names where the code lives today.
- `(P7)` marks something a later phase creates.
- `(Flows)` marks the Flows phases, which directly follow the events phase (P18).
- A line with neither is new structure for code that exists today or for P1, the phase
  in progress.

## Summary

The tree is organised by subdomain first. Eighteen bounded contexts each own their model,
their tables, their slice of the served REST API, their protocol endpoints and their
tests. The contexts follow the language the research uses: a stack is configured in
**stack configuration**, a run moves through its states in **run orchestration**, a
worker claims and executes it in **execution**, policies decide in **governance**,
pushes arrive in **source control**, and so on. Inside every context the layering is the
same: `domain/` holds aggregates, entities, value objects, domain events and repository
interfaces, with no I/O; `application/` holds command and query handlers and process
managers; `infrastructure/` holds persistence and external clients; `interfaces/` holds
the REST slice, protocol handlers and event consumers. `api/` is the only package
another context may import.

The organising idea is the aggregate as the consistency boundary. One transaction
changes one aggregate and appends that aggregate's domain events to a transactional
outbox; everything another context must do as a result happens later, in that context's
own transaction, when the event bus delivers the event. The aggregate that matters most
is `orchestration/domain/queue.StackQueue`: the stack's non-terminal runs, its blocker,
its human lock and its policy lock key. Because the stack lock and the run state machine
are one aggregate, every run transition, report 08's 43 and decision 0008's T44 in the
[run-transitions register](../../data/run-transitions.json), commits in one transaction with the
lock it depends on, and nothing else is in that transaction.

Contexts reach each other in two ways only: a synchronous call to another context's
`api/` for a query or a decision the caller cannot proceed without (an authorization
check, an entitlement check, a policy decision, a run specification), and an
asynchronous domain event for every consequence. The context map in [Full package
tree](#full-package-tree) names each relationship.

Three things every context must read identically sit in the shared kernel rather than in
any context: the event envelope and kind registry that Deploy and Flows share (decisions
0015 and 0018), the expression language (decision 0024), and the sealed formats of
decision 0013. The first two are the price DDD pays for the accepted design's one event
model and one expression language: they cut across context boundaries that this option
otherwise keeps separate.

Dependency rules:

1. A package under `internal/<context>/` imports only the standard library,
   `internal/kernel/...`, `internal/shared/...`, `pkg/...`, its own context's subtree,
   and other contexts' `internal/<other>/api`.
2. `internal/<context>/domain/...` imports only the standard library,
   `internal/kernel/...` and its own context's `domain/...`. It never imports
   `application/`, `infrastructure/`, `interfaces/`, `api/` or `internal/shared/...`,
   and no third-party module except those named in the domain rule (a ULID library,
   `semver`).
3. `internal/<context>/application/...` never imports any `infrastructure/` or
   `interfaces/` package; it reaches persistence and external systems through interfaces
   declared in its own `domain/` or `application/port/`.
4. `internal/<context>/api` imports only the standard library, `internal/kernel/...` and
   the value types of its own `domain/` that it chooses to publish.
5. `internal/kernel/...` imports only the standard library and the libraries rule 2
   names, plus cel-go in `internal/kernel/expr` alone.
6. `internal/shared/...` imports no context and no `internal/tooling/...`.
7. `internal/execution/agent/...`, the worker side, and the two other customer-host
   agents, `internal/sourcecontrol/agent/...` and `internal/flows/agent/...`, import only
   the standard library, `internal/kernel/...` (which carries the expression language
   for the runner and the sealed formats for the launcher), `internal/vendors/api`,
   `pkg/client/worker`, the generated `App` contract in `pkg/flows/app/v1`, gRPC, the
   container runtime clients, `go-jose` and the OPA `v1` packages. They never import
   `internal/shared/...`, any other context, or the rest of their own context.
8. Only `internal/app` imports a context's root constructor package
   (`internal/<context>`) or more than one context's `interfaces/`, and only
   `cmd/infraweave` imports `internal/app` or an `agent/` tree; `cmd/infraweave-agent`
   imports the `agent/` trees only.
9. `internal/tooling/...` is imported only by `tools/...`; `pkg/...` never imports
   `internal/...` or `cmd/...`.

Rule 7 is what keeps the worker side honest: the launcher and the in-container runner
run on customer hosts with outbound HTTPS only ([report
10](../reference/10-workers-and-execution.md#42-transports)), so their package graph
holds no SQL driver, no object-storage credential and no other context's model.

## Repository root

```text
infraweave/
├── cmd/
│   ├── infraweave/             NEW: cobra root; serve, scheduler, all, launcher, hidden runner,
│   │                           vcs-agent, flows-agent, migrate up and status, secrets, version
│   ├── infraweave-agent/       the slim build for worker and agent hosts: launcher, runner, vcs-agent
│   │                           and flows-agent from the three agent/ trees only (decision 0004)
│   └── infraweavectl/          (P24b) the CLI over pkg/sdk only; operator and mcp command groups
├── internal/                   REORGANISED: kernel/ shared/ tooling/ app/ and one directory per context
├── pkg/
│   ├── sdk/                    (P24) hand-written layer over pkg/client: token exchange, Idempotency-Key,
│   │                           429 retries, pagination iterators, typed problem errors, typed streams
│   ├── client/                 deploy/ (generated, unchanged); worker/ (P8), the execution context's
│   │                           published language; operator/ (P3b); flows/ (Flows)
│   └── flows/                  (Flows) app/v1/ generated App and Host stubs; appsdk/ and appsdk/apptest/
├── api/                        unchanged layout; oapi-server-*.yaml outputs move to internal/shared/gen
│   ├── src/deploy/             fragments stay flat; each paths-*.yaml has one owning context
│   ├── src/worker/             (P8) the worker protocol
│   ├── src/operator/           (P3b) the operator API, bundled to api/operator.openapi.yaml
│   ├── src/flows/              (Flows) the Flows REST API under /flows/v1/
│   ├── proto/flows/app/v1/     (Flows) the App and Host gRPC services, package infraweave.flows.app.v1
│   └── reference/spacelift/    unchanged
├── web/
│   └── console/                (P24c) React 19, TypeScript strict, Vite; orval client and TanStack Query
│                               hooks; embed.go exposes the built files as an embed.FS
├── test/
│   └── acceptance/             unchanged runner; features grouped by context in file names
├── tools/                      unchanged commands; imports move to internal/tooling
├── deploy/                     (P27) Helm chart, Terraform modules, server and runner image definitions
├── docs/                       research/ unchanged; the implementation plan adds the context map
├── Makefile                    generate writes servers to internal/shared/gen, protobuf stubs to
│                               pkg/flows/app, the console client to web/console; LDFLAGS name
│                               internal/shared/buildinfo; docs-check runs ./internal/tooling/docscheck
├── .golangci.yml               depguard enabled: one boundary rule and one domain rule per context,
│                               plus kernel, shared, api, application, agent, tooling and pkg rules
├── .testcoverage.yml           exclusions follow ^internal/shared/gen/, ^pkg/client/ and ^pkg/flows/app/
└── go.mod                      one module
```

What changes at the root and why:

- `cmd/infraweave` is the first binary. Its `launcher` and `runner` subcommands import only
  `internal/execution/agent/...`, and `vcs-agent` and `flows-agent` only their contexts'
  `agent/` trees; its `serve`, `scheduler`, `all` and `migrate` subcommands import
  `internal/app`. The binary links both graphs, which is the cost of one artefact; the
  import rules, not the binary, keep the worker side free of server code.
- `pkg/client/worker` is public because the worker protocol is a published language: the
  Kubernetes controller of P14 and any customer-built launcher speak it from outside
  this repository.
- `cmd/infraweave-agent` links only the three `agent/` trees, which rule 7 already keeps
  free of server code, so the slim build costs one file. Decision 0004 builds it when
  image size or a customer's review asks for a binary without server code.
- `api/src/flows` is a separate spec because Flows is a separate context with its own
  projects and UUID identity ([report 23](../reference/23-flows.md#2-resource-hierarchy))
  and its own API under `/flows/v1/` (decision 0018); Flows belongs to the account, in
  place of Spacelift Flows' organization, and shares identity's members. Only the six
  Deploy-side operations of the
  reference `Flows` tag stay in `deploy`.
- `api/src/operator` is the operator-only API of decision 0021 (organizations, tiers,
  grants, billing); Spacelift has no counterpart, so it sits outside the parity surface.
- `api/proto/flows/app/v1` is the `App` contract every Flows app serves. Its generated Go
  stubs are public in `pkg/flows/app/v1`, because app authors and `pkg/flows/appsdk`
  import them, and so do the server and the Flows agent.
- `pkg/sdk` is the supported entry point for every Go client (decision 0001): it wraps the
  generated `pkg/client/<spec>` packages and imports nothing under `internal/`.
  `infraweavectl` uses it; the Terraform provider lives in its own repository,
  `weaveplatform/terraform-provider-infraweave`, and imports it at a pinned version.
- `web/console` is the console (decision 0023). `serve` embeds its build output; Flows
  apps are OCI images their authors build, so `deploy/images` holds no image for them.
- `internal/tooling` separates the maintainer libraries behind `tools/` from the server,
  so no context can import the reference generator.

## Full package tree

Context status at P1 and the phase that completes each:

| Context | Package root | Status at P1 | Created or completed by |
|---|---|---|---|
| shared kernel | `internal/kernel` | identifiers only | P2 onwards; event envelope P7; expressions P9 |
| shared infrastructure | `internal/shared` | gen, httpkit, problem, buildinfo | P1, P2 (sql, objectstore, crypt, outbox, bus), P7 (queue), P9 (streams) |
| identity and access | `internal/identity` | absent | P3, P4, P23 |
| features and tiers | `internal/features` | absent | P3b |
| stack configuration | `internal/stackconfig` | REST skeleton: the 69 Stacks operations answer 501 | P5, P6, P16 (dependency definitions) |
| run orchestration | `internal/orchestration` | REST skeleton: 3 Runs operations answer 501 | P7, P9, P10, P16 |
| execution | `internal/execution` | absent | P8, P14 |
| vendors | `internal/vendors` | absent | P9 (Terraform, OpenTofu), P20 |
| state | `internal/state` | absent | P11 |
| source control | `internal/sourcecontrol` | absent | P12, P19 |
| governance | `internal/governance` | absent | P13 |
| cloud credentials | `internal/cloud` | absent | P15 |
| registry | `internal/registry` | absent | P17 |
| notifications | `internal/notifications` | absent | P18 |
| audit | `internal/audit` | absent | P18 |
| templates | `internal/templates` | absent | P21 |
| inventory | `internal/inventory` | absent | P22 |
| intelligence | `internal/intelligence` | absent | P25 |
| expressions (editor) | `internal/expressions` | absent | P9, growing with each surface |
| flows | `internal/flows` | absent | Flows |

### Context map

Upstream contexts supply; downstream contexts consume. Every arrow is an import of the
upstream `api/` (synchronous) or a subscription to its events (asynchronous), never both
directions of synchronous calls between the same pair unless the table says so.

| Upstream | Downstream | Relationship | Synchronous calls | Events crossing |
|---|---|---|---|---|
| identity | every context | open host service; downstreams are conformist to its space model | `Authorizer.For(principal)`, `Principals`, `RunTokens.Issue` | `RoleBindingChanged`, `SpaceMoved`, `APIKeyRevoked` |
| identity | features | customer/supplier | none | `OrganizationCreated`, `OrganizationDeleted` create and remove the organization's entitlement; `AccountDeleted` removes an account's limits |
| features | every context | open host service; downstreams are conformist to the catalogue | `Entitlements.For(organization)`: `Has(feature)`, `Limit(name)`; `Limits.Check(account, name, n)` against the pool and the account limit | `EntitlementChanged` |
| stackconfig | orchestration, execution | customer/supplier | `Stacks.RunSpec(stack)`: settings, environment, hooks and attachments, read at handover | `StackUpdated`, `StackDisabled`, `StackDeleted` |
| orchestration | stackconfig | customer/supplier (reverse direction is events only) | none | `RunStateChanged` feeds the stack's `StackState` projection |
| sourcecontrol | orchestration | customer/supplier | `Runs.Trigger`, `Runs.CancelSuperseded` | none |
| orchestration | sourcecontrol | published language | none | `RunStateChanged`, `RunFinished` drive checks, deployments and the delta comment |
| governance | orchestration, sourcecontrol, identity, notifications, registry, intelligence | open host service with a published language (the per-type input documents of [report 09](../reference/09-policies.md#4-input-documents)) | `Evaluator.Push`, `Approval`, `Plan`, `Trigger`, `Notification`, `Login`, `Intent` | `PolicyEvaluated` |
| orchestration | execution | conformist: execution claims runs through orchestration's `Dispatcher` | `Dispatcher.Claim`, `Runs.Advance`, `Runs.RecordPlan` | `RunClaimed`, `RunStopRequested` |
| execution | orchestration | customer/supplier for worker reports | none | `WorkerLost` ends the runs it held |
| orchestration | state | customer/supplier | `RunAuthority.MayRead`, `MayWrite` | `RunFinished` releases plan-time locks |
| cloud | execution | customer/supplier | `Credentials.ForRun` at handover | none |
| sourcecontrol | registry | published language | none | `TagPushed` creates module versions |
| registry | orchestration | customer/supplier | `Runs.Trigger(TESTING)` for module test cases | `ModuleVersionPublished` (trigger policies, notifications) |
| vendors | stackconfig, orchestration, governance, execution agent | shared library behind a published contract; behaviour only, no tables | `vendors/api.For(kind)` | none |
| every context | notifications, audit | conformist subscribers | `sourcecontrol/api.Comments` for PR comments | every published event |
| stackconfig, orchestration | templates | customer/supplier | `Stacks.CreateFromTemplate`, `Runs.Trigger` | `StackDeleted`, `RunStateChanged` |
| orchestration, state | inventory | conformist | none | `RunFinished` with the managed-entity report |
| every Deploy context | intelligence | read-only customer through each `api/` query | queries only | none |
| every emitting Deploy context | flows | shared kernel for the event model; conformist to every kind's schema | none | every kind of decision 0015: `run_updated`, `drift_detected`, `policy_evaluated`, `notification_created`, `module_version`, `internal_error`, `worker_usage`, `worker_updated` |
| orchestration, templates | flows | anticorruption layer inside flows, outbound only | `Runs.Trigger`, run actions, template and blueprint operations from Deploy blocks | none |
| flows, templates, notifications, stackconfig, vendors | expressions | conformist customer: each surface owner publishes the typed context of its surfaces | `ExpressionSurfaces.Context(ref)` | none |

The shared kernel holds what several contexts must read identically: identifiers, the
principal, labels, paging, the vendor settings value objects, the event envelope and
kind registry, the expression language and the sealed formats. Anything with business
behaviour stays in a context. The event envelope is in the kernel rather than in each
producer's published language because decisions 0015 and 0018 require one event model
for Deploy and Flows with no bridge: a Flows trigger block reads a Deploy event's
document as the producer wrote it, so Flows is conformist to every producer's kind
schema instead of translating through an anticorruption layer.

```text
internal/
├── app/                                     composition root: contexts, subscriptions, strict server, process modes
│   ├── app.go                               Serve(ctx), Schedule(ctx), All(ctx); open shared infrastructure, close
│   ├── contexts.go                          one constructor call per context, in dependency order
│   ├── subscriptions.go                     every bus.Subscribe line, the readable map of who reacts to what
│   ├── strict.go                            composes the deploy StrictServerInterface from context slices
│   ├── protocols.go                         mounts hand-written protocol routes (webhooks, state, registry)
│   ├── modes.go                             which consumers, cron entries and listeners each process mode starts
│   ├── gates.go                             binds features/api to the queue and bus dequeue gates (P3b)
│   ├── console.go                           mounts shared/httpkit/console at / last (P24c)
│   └── config/                              strict YAML, flag > env > file; one section per context
│
├── kernel/                                  shared kernel: value types, no behaviour beyond parsing
│   ├── ids/                                 ULID, Slug, OrganizationID, AccountID, SpaceID, StackID, RunID,
│   │                                        PoolID, ModuleID
│   ├── principal/                           Principal (member, API key, organization key, run, stack, Flows
│   │                                        project; one organization and one account), FromContext
│   ├── labels/                              Labels, autoattach:<label> matching
│   ├── page/                                Cursor and Page (decision 0003 cursor pagination)
│   ├── clock/                               Clock interface for timers and tests
│   ├── vcsref/                              Repository, Branch, CommitSHA, PullRequestRef
│   ├── iac/                                 Kind and per-vendor settings value objects (terraform.go,
│   │                                        opentofu.go, terragrunt.go, pulumi.go (P20), ...)
│   ├── feature/                             Feature and Limit names: the catalogue's public contract (P3b)
│   ├── event/                               the envelope (id, kind, account, subject, occurred_at, document),
│   │                                        the kind registry and each kind's JSON Schema; Deploy and
│   │                                        Flows share it (decisions 0015 and 0018) (P7)
│   ├── expr/                                decision 0024's language: cel-go environments per surface, the
│   │                                        ${{ }} text template and its $${{ escape, the curated library
│   │                                        (cel.bind, merge, pick, omit, encodings, regex, maths, time,
│   │                                        random.* from the seed, fail), the JSON Schema to CEL type
│   │                                        provider, cost limit and timeout, a program cache keyed by
│   │                                        source hash, fixed now and seed, POSIX shell quoting for
│   │                                        workflow command templates; no I/O (P9)
│   └── sealfmt/                             decision 0013's sealed formats, each led by a version byte:
│                                            object segments (AES-256-GCM through NewGCMWithRandomNonce,
│                                            segment index and last flag in the associated data) and the
│                                            pool envelope (ECDH P-256, HKDF-SHA256, AES-256-GCM); shared
│                                            by shared/crypt, execution's sealer and the agent (P2)
│
├── shared/                                  infrastructure with no business rules
│   ├── buildinfo/                                                               ← internal/buildinfo
│   ├── httpkit/                             chi router, middleware order, request ID, recovery, body limits
│   │   ├── problem/                         RFC 9457 writer, decision 0003 slugs ← internal/server/problem
│   │   ├── idempotency/                     Idempotency-Key middleware (decision 0003), store on shared/sql
│   │   ├── ratelimit/                       cost-class buckets (decision 0003)
│   │   ├── strictkit/                       error mapping from domain errors to problem types (decision 0022)
│   │   ├── sse/                             text/event-stream writer: id, event and data lines, retry: 3000,
│   │   │                                    a comment every 15 s, Last-Event-ID resume, reset, slow-consumer
│   │   │                                    and shutdown end messages (decision 0015) (P9)
│   │   └── console/                         embed.FS handler: index.html fallback except /api/, /mcp,
│   │                                        /.well-known/ and protocol paths; per-response CSP nonce;
│   │                                        placeholder page when built without the console (P24c)
│   ├── gen/                                 generated strict servers
│   │   ├── deploy/                          server.gen.go, unimplemented.gen.go ← internal/server/gen/deploy
│   │   ├── worker/                          (P8)
│   │   ├── operator/                        (P3b)
│   │   └── flows/                           (Flows)
│   ├── sql/                                 pgx pool, embedded migrations per context, unit of work (P2, decision 0005)
│   │   ├── uow/                             Do(ctx, fn): context-carried transaction, AfterCommit
│   │   ├── migrate/                         one migration stream per context, platform stream first
│   │   ├── listen/                          one LISTEN connection per replica: infraweave_events,
│   │   │                                    infraweave_logs, infraweave_invalidate, routed to local waiters (P7)
│   │   └── sqltest/                         testcontainers PostgreSQL harness (decision 0016)
│   ├── objectstore/                         Store: Put, Get, Head, Delete, SignURL; keys written once (P2)
│   │   ├── s3/ gcs/ azureblob/ fs/          the four drivers of decision 0005, each on its provider's SDK
│   │   └── objectstoretest/                 one contract suite: round trips, multipart, SHA-256 checksums,
│   │                                        signed URLs, expiry; no driver relies on bucket versioning
│   ├── crypt/                               keyring and KEK providers (local, awskms, gcpkms, azurekv, vault);
│   │                                        seals through kernel/sealfmt (P2, decision 0013)
│   ├── outbox/                              Append(ctx, Event) inside the caller's unit of work (P2)
│   ├── eventbus/                            Subscribe(name, handler); dispatch from the outbox on the queue;
│   │                                        idempotency per (event, subscriber); dead letters; replay (P7)
│   ├── queue/                               decision 0011's queue: jobs table, logical queues (flows among
│   │                                        them), leases, attempts, dead state, periodic tasks, the schedule
│   │                                        ticker and a dequeue Gate that app binds to features/api (P7)
│   ├── observe/                             slog, OpenTelemetry, /metrics, readiness on its own port (P18, decision 0017)
│   └── testkit/                             fakes for clock, queue and object store
│
├── tooling/                                 maintainer libraries behind tools/; never imported by a context
│   ├── specref/                                                                 ← internal/specref
│   ├── openapibundle/                                                           ← internal/openapibundle
│   ├── stubgen/                                                                 ← internal/stubgen
│   └── docscheck/                                                               ← internal/docscheck
│
├── identity/                                CONTEXT: organizations, accounts, members, groups, API keys, sessions,
│   │                                        the OAuth authorization server, spaces, roles (P3, P3b, P4, P23)
│   ├── identity.go                          New(Deps) (*Context, error); API(); Strict(); Consumers()
│   ├── api/                                 Authorizer, Decider (Can(action, space)), Principals, RunTokens,
│   │                                        Spaces (Reachable, Ancestors), events.go
│   ├── domain/
│   │   ├── organization/                    Organization aggregate: its accounts, members and identity
│   │   │                                    providers; created by the operator with its first account; no
│   │   │                                    tier (features owns it); root holds organizations and nothing
│   │   │                                    inherits from root or from the organization
│   │   ├── account/                         Account aggregate: immutable slug, authorization strategy; the
│   │   │                                    data boundary; created and removed by organization admins
│   │   ├── member/                          Member aggregate: one per person per organization, its linked
│   │   │                                    identities; a seat counts a member once
│   │   ├── user/ group/                     User, IdPGroup aggregates; membership per account
│   │   ├── apikey/                          APIKey aggregate: secret or OIDC, expiry, owner, teams
│   │   ├── session/                         Session aggregate with its access snapshot; one account at a time
│   │   ├── oauthclient/                     OAuthClient aggregate: registered client, loopback or allowed
│   │   │                                    redirect URIs, mcp:read and mcp:write scopes (P3b)
│   │   ├── space/                           SpaceTree aggregate: the account's top space addressed by its
│   │   │                                    slug, parent, inherit flag, reachability
│   │   ├── role/                            Role aggregate and the action catalogue
│   │   ├── binding/                         RoleBinding aggregate: actor kind, actor, role, space
│   │   ├── access/                          Decide: propagation down, read up through inheritance, stack read
│   │   └── events.go                        UserCreated, APIKeyRevoked, RoleBindingChanged, SpaceMoved, LoggedIn
│   ├── application/
│   │   ├── authenticate/                    API key exchange, session issue at the organization, the account's
│   │   │                                    login policy through governance/api on entry
│   │   ├── tokens/                          short-lived API tokens, run tokens, JWKS (decision 0006)
│   │   ├── spaces/ roles/ bindings/ keys/ users/
│   │   ├── organization/                    the Organization tag: accounts, members, organization keys (P3b)
│   │   ├── oauth/                           metadata, registration, authorization, token; PKCE S256 only (P3b)
│   │   └── sso/ scim/ linking/              the organization's identity providers (P23)
│   ├── infrastructure/
│   │   ├── persistence/postgres/            migrations/, one repository per aggregate
│   │   ├── persistence/memory/              reference repositories for application tests
│   │   ├── persistence/persistencetest/     contract suite for both
│   │   ├── signing/                         JWT signing keys, rotation
│   │   └── saml/ oidc/                      (P23)
│   └── interfaces/
│       ├── rest/                            strict slice: accounts, users, API keys, spaces, roles, bindings,
│       │                                    the Organization tag
│       ├── operatorrest/                    operator slice: create, list and delete organizations (P3b)
│       ├── oauthserver/                     OAuth metadata, registration, authorization, token (P3b)
│       ├── middleware/                      authn: credentials to kernel/principal on the request context
│       ├── terraformlogin/                  login.v1 authorization and token endpoints (hand-written)
│       ├── scim/                            SCIM 2.0 server (P23)
│       └── consumers/                       StackDeleted removes stack-actor bindings
│
├── features/                                CONTEXT: catalogue, tiers, grants, billing, entitlement (P3b)
│   ├── features.go
│   ├── api/                                 Entitlements.For(organization): Has(feature), Limit(name);
│   │                                        Limits.Check(account, name, n); Catalogue.FeatureOf(operationID);
│   │                                        events.go (EntitlementChanged)
│   ├── domain/
│   │   ├── catalogue/                       Catalogue: capabilities, options and limits from catalogue.yaml,
│   │   │                                    requirements, coverage of every operation and gated field
│   │   ├── tier/                            Tier aggregate: a name, a feature set and limit values; data,
│   │   │                                    not a ladder; Enterprise fixed and holding everything
│   │   ├── entitlement/                     OrganizationEntitlement aggregate: one tier, grants, limit overrides;
│   │   │                                    Compute: tier plus grants minus switch-offs minus unmet requirements
│   │   ├── accountlimit/                    AccountLimit: an organization admin's limit for one account, below
│   │   │                                    the organization's pool
│   │   ├── billing/                         Billing aggregate: the installation's switch, off by default
│   │   └── events.go                        EntitlementChanged, TierChanged, BillingSwitched
│   ├── application/
│   │   ├── operator/                        tiers, organization tiers, grants, billing; bump entitlement_version,
│   │   │                                    NOTIFY infraweave_invalidate
│   │   ├── snapshot/                        per-replica entitlement snapshot per organization, rebuilt on invalidation
│   │   ├── limits/                          pooled limits: count across the organization's accounts, then
│   │   │                                    against the account limit
│   │   └── seed/                            the five seeded tiers, installed once
│   ├── infrastructure/
│   │   ├── catalogue.yaml                   embedded; the one catalogue file of decision 0021
│   │   └── persistence/{postgres,memory,persistencetest}/   tiers, tier_features, tier_limits,
│   │                                        organization_entitlements, grants, account_limits, billing
│   └── interfaces/
│       ├── middleware/                      capability gate after authn, before the handler: feature-disabled,
│       │                                    feature-not-in-tier
│       ├── operatorrest/                    operator slice: tiers, grants, billing (operator principals only)
│       ├── rest/                            the Organization tag's account limits and usage
│       └── consumers/                       OrganizationCreated, OrganizationDeleted, AccountDeleted
│
├── stackconfig/                             CONTEXT: stacks, contexts, configuration, hooks, attachments (P5, P6)
│   ├── stackconfig.go
│   ├── api/                                 Stacks (Get, RunSpec, ByRepository, CreateFromTemplate),
│   │                                        Contexts, events.go
│   ├── domain/
│   │   ├── stack/                           Stack aggregate: slug, space, source, vendor (kernel/iac),
│   │   │                                    flags, labels, worker pool, runner image, StackState projection
│   │   ├── context/                         Context aggregate: elements, hooks, labels
│   │   ├── config/                          ConfigElement value object, write-only secrets, file mode
│   │   ├── attachment/                      ContextAttachment: priority, autoattached
│   │   ├── dependency/                      StackDependency aggregate, output references (P16)
│   │   ├── precedence/                      domain service: the computed environment of report 13
│   │   └── events.go                        StackCreated, StackUpdated, StackDisabled, StackDeleted, ContextAttached
│   ├── application/
│   │   ├── stacks/                          create, update (PUT and PATCH per decision 0022), delete, reslug
│   │   ├── contexts/ attachments/ config/
│   │   ├── runspec/                         RunSpec query: resolved environment and hooks for one run
│   │   └── stackstate/                      projects RunStateChanged onto Stack.state
│   ├── infrastructure/persistence/{postgres,memory,persistencetest}/
│   └── interfaces/
│       ├── rest/                            strict slice: Stacks tag (69 operations today), contexts
│       └── consumers/                       RunStateChanged, TemplateDeploymentChanged (P21)
│
├── orchestration/                           CONTEXT: runs, the stack queue, scheduling, timers (P7, P9, P10, P16)
│   ├── orchestration.go
│   ├── api/                                 Runs (Trigger, Advance, RecordPlan, Confirm, Review, ...),
│   │                                        Dispatcher (Claim), RunAuthority, RunState and RunType enums,
│   │                                        events.go (published language)
│   ├── domain/
│   │   ├── queue/                           StackQueue aggregate: non-terminal runs, blocker, human lock,
│   │   │                                    policy lock key; every transition goes through it
│   │   ├── run/                             Run entity, RunState machine (22 states; report 08's 43
│   │   │                                    transitions and decision 0008's T44), RunType, Priority
│   │   │                                    and tier, guards (canConfirm, canPromote)
│   │   ├── review/                          RunReview value object, newest-per-reviewer rule
│   │   ├── timer/                           the 2, 7 and 30 day rules as a domain service
│   │   ├── schedule/                        ScheduledJob aggregate: run, task, delete (P16)
│   │   ├── drift/                           DriftSchedule aggregate (P16)
│   │   ├── dependency/                      run dependency graph and external dependencies (P16)
│   │   └── events.go                        RunCreated, RunStateChanged, RunClaimed, RunFinished,
│   │                                        RunStopRequested, StackLocked, StackUnlocked
│   ├── application/
│   │   ├── trigger/                         Trigger, TriggerBatch, Task, Promote, LocalPreview
│   │   ├── dispatch/                        Claim: the pool's top claimable run by tier, SKIP LOCKED
│   │   ├── advance/                         worker-reported phase transitions
│   │   ├── plan/                            RecordPlan: plan policy through governance/api, then transition
│   │   ├── actions/                         confirm, discard, cancel, stop, kill, retry, review, replan, prioritize
│   │   ├── timers/                          the run.timers periodic task (decision 0011)
│   │   ├── triggerpolicy/                   on RunFinished: trigger policies, dependants (P13, P16)
│   │   └── schedules/                       cron firing of scheduled jobs and drift (P16)
│   ├── infrastructure/
│   │   ├── persistence/{postgres,memory,persistencetest}/   stack_queues, runs, run_state_transitions,
│   │   │                                    run_reviews, run_dependencies, scheduled_jobs, drift_detection
│   │   └── logs/                            run log objects and retention (object storage keys)
│   └── interfaces/
│       ├── rest/                            strict slice: Runs tag, run actions, logs; allowedActions from
│       │                                    StackQueue guards and the Decider
│       ├── streams/                         run log and run state SSE streams over shared/httpkit/sse (P9)
│       └── consumers/                       StackDeleted, StackDisabled, WorkerLost, ModuleVersionPublished
│
├── execution/                               CONTEXT: worker pools, workers, the worker protocol (P8, P14)
│   ├── execution.go
│   ├── api/                                 Pools (for stackconfig validation), events.go
│   ├── domain/
│   │   ├── pool/                            WorkerPool aggregate: credentials (CSR certificate), settings
│   │   ├── worker/                          Worker aggregate: status, busy, drained, metadata, heartbeat
│   │   ├── job/                             Assignment value object: the handover payload, sealed for private pools
│   │   └── events.go                        WorkerRegistered, WorkerDrained, WorkerLost
│   ├── application/
│   │   ├── register/ heartbeat/ drain/
│   │   ├── poll/                            long-poll: Dispatcher.Claim, then payload assembly
│   │   ├── payload/                         RunSpec, cloud credentials, state and registry credentials,
│   │   │                                    source archive URL, presigned log and workspace URLs
│   │   └── report/                          phase reports and plan results forwarded to orchestration/api
│   ├── infrastructure/
│   │   ├── persistence/{postgres,memory,persistencetest}/
│   │   └── seal/                            the spec's secret part sealed to the pool's P-256 key in the
│   │                                        kernel/sealfmt pool envelope
│   ├── interfaces/
│   │   ├── rest/                            strict slice: worker pools, workers, drain, cycle
│   │   ├── streams/                         worker pool queue SSE streams (P9)
│   │   ├── workerproto/                     implements shared/gen/worker: register, poll, report, signals (P8)
│   │   └── consumers/                       RunClaimed marks the worker busy; RunStopRequested signals it
│   └── agent/                               WORKER SIDE: rule 7; builds into the launcher and runner only
│       ├── launcher/                        infraweave launcher: register, long-poll, open the spec's pool
│       │                                    envelope with kernel/sealfmt, start a container per run
│       ├── protocol/                        thin wrapper over pkg/client/worker, retries, backoff
│       ├── docker/                          Docker socket runtime (P8)
│       ├── kubernetes/                      pod per run (P14)
│       ├── initpolicy/                      worker-side initialization policy with OPA v1 (P14)
│       ├── runner/                          infraweave runner: phases and hooks inside the container; renders
│       │                                    .infraweave/workflow.yml with kernel/expr, every value shell-quoted (P9)
│       ├── masking/                         secret masking in log streams
│       └── workspace/                       workspace archive, sealed with kernel/sealfmt, upload and restore
│
├── vendors/                                 CONTEXT (generic subdomain): IaC tool behaviour, no tables (P9, P20)
│   ├── api/                                 Vendor contract, For(kind), version catalogue queries
│   ├── domain/
│   │   ├── terraform/ opentofu/             command sets, workflow.yml keys, plan JSON to ChangeSet, policy block
│   │   ├── terragrunt/ pulumi/ cloudformation/ kubernetes/ ansible/   (P20)
│   │   └── changeset/                       ChangeSet and delta value objects shared by every vendor
│   ├── application/catalogue/               version constraint resolution, effective version
│   ├── infrastructure/releases/             release feeds and checksums for tool downloads
│   └── interfaces/rest/                     strict slice: versions and effective-version operations
│
├── state/                                   CONTEXT: managed Terraform and OpenTofu state (P11)
│   ├── api/                                 StateBackends (Enabled(stack)), events.go
│   ├── domain/{backend,version,lock}/       StateBackend aggregate per stack, StateVersion, Lock
│   ├── application/{read,write,lock,import,rollback,external}/
│   ├── infrastructure/persistence/{postgres,memory,persistencetest}/, objects/ (one sealed object per version)
│   └── interfaces/
│       ├── httpbackend/                     Terraform http backend: GET, POST, LOCK, UNLOCK (hand-written)
│       └── rest/                            strict slice: state versions, presigned URLs, rollback
│
├── sourcecontrol/                           CONTEXT: VCS integrations, deliveries, feedback (P12, P19)
│   ├── api/                                 Integrations, Comments (Upsert), Archives (SourceURL), events.go
│   ├── domain/
│   │   ├── integration/                     VCSIntegration aggregate: provider, space, default, check mode
│   │   ├── delivery/                        Delivery aggregate: stored before acknowledgement, processed once
│   │   ├── event/                           Push, PullRequest, Comment, Tag: provider-neutral value objects
│   │   ├── pullrequest/                     PullRequest record per stack
│   │   ├── agentpool/                       VCSAgentPool aggregate (P19)
│   │   └── events.go                        DeliveryReceived, PushNormalized, TagPushed
│   ├── application/{ingest,routing,feedback,integrations}/
│   ├── infrastructure/                      ANTICORRUPTION LAYER, one package per provider
│   │   ├── github/                          verify.go translate.go client.go checks.go comments.go deployments.go
│   │   ├── gitlab/ bitbucketcloud/ bitbucketdc/ azuredevops/ rawgit/   (P19)
│   │   ├── gateway/                         VCS agent long-poll relay (decision 0012) (P19)
│   │   └── persistence/{postgres,memory,persistencetest}/
│   ├── agent/                               CUSTOMER HOST, rule 7: infraweave vcs-agent long-polls serve (P19)
│   └── interfaces/
│       ├── webhook/                         POST /webhooks/vcs/{provider}/{integrationId} (hand-written)
│       ├── rest/                            strict slice: integrations, test, repositories
│       └── consumers/                       DeliveryReceived, RunStateChanged, RunFinished
│
├── governance/                              CONTEXT: policies and their evaluation (P13)
│   ├── api/                                 Evaluator, input documents per type, Decision, events.go
│   ├── domain/
│   │   ├── policy/                          Policy aggregate: type, engine version, body, labels
│   │   ├── attachment/                      PolicyAttachment: target kind, target, autoattached
│   │   ├── decision/                        combination rules per type (deny wins, all approve, ...)
│   │   ├── flags/                           flag set and reset rules
│   │   ├── receipt/                         PolicyReceipt, Sample
│   │   └── events.go                        PolicyEvaluated, PolicyChanged
│   ├── application/{evaluate,attach,simulate,sample}/
│   ├── infrastructure/
│   │   ├── opa/                             prepared queries per policy and Rego version, capabilities,
│   │   │                                    deadlines (decision 0009)
│   │   └── persistence/{postgres,memory,persistencetest}/, samples/ (object storage)
│   └── interfaces/{rest,consumers}/
│
├── cloud/                                   CONTEXT: AWS, Azure, GCP integrations and OIDC (P15)
│   ├── api/                                 Credentials.ForRun
│   ├── domain/{integration,attachment}/
│   ├── application/{integrations,credentials}/
│   ├── infrastructure/{aws,azure,gcp,oidc}/ token issue through identity/api signing
│   └── interfaces/rest/
│
├── registry/                                CONTEXT: modules, providers, GPG keys, sharing (P17)
│   ├── api/                                 Modules, Providers, events.go
│   ├── domain/{module,moduleversion,provider,providerversion,gpgkey,sharing}/
│   ├── application/{versions,testcases,publish,share,metadata}/
│   ├── infrastructure/persistence/..., archives/ (object storage, signed URLs)
│   └── interfaces/
│       ├── protocol/                        discovery, modules.v1, providers.v1 (hand-written)
│       ├── rest/                            strict slice: modules, providers, versions, GPG keys
│       └── consumers/                       TagPushed, RunFinished (TESTING)
│
├── notifications/                           CONTEXT: inbox, named and stack webhooks, Slack (P18)
│   ├── api/                                 Inbox, events.go (NotificationRaised)
│   ├── domain/{notification,webhook,delivery,slack}/
│   ├── application/{route,deliver,inbox}/   notification policy routing through governance/api; message
│   │                                        strings rendered as kernel/expr text templates in delivery
│   ├── infrastructure/{httpsender,slack,persistence/...}/
│   └── interfaces/{rest,slack,consumers}/
│
├── audit/                                   CONTEXT: the audit trail and its webhook (P18)
│   ├── api/
│   ├── domain/{entry,stream}/
│   ├── application/{record,search,stream}/
│   ├── infrastructure/persistence/...
│   └── interfaces/{rest,consumers}/         one consumer subscribed to every published event
│
├── templates/                               CONTEXT: templates, versions, deployments, blueprints (P21)
├── inventory/                               CONTEXT: managed entities, resources, scans, search (P22)
├── intelligence/                            CONTEXT: AI integrations, Infra Assistant, Intent, MCP (P25)
│   ├── api/ domain/{integration,conversation,intent}/ application/
│   ├── infrastructure/{providers,persistence}/   provider boundary (decision 0019)
│   └── interfaces/{rest,mcp}/
├── expressions/                             CONTEXT (generic subdomain): the editor endpoints, no tables (P9)
│   ├── api/                                 ExpressionSurfaces: the contract each surface owner implements
│   ├── application/editor/                  check, complete, evaluate over kernel/expr with the typed context
│   │                                        the owning context returns; masked secrets; same cost limits
│   └── interfaces/rest/                     strict slice: /expressions/check, complete, evaluate
└── flows/                                   CONTEXT: projects, flows, the engine, apps and agents (Flows)
    ├── api/                                 Projects for Deploy role bindings; ExpressionSurfaces for flows
    ├── domain/
    │   ├── project/                         the account's projects, sandboxes, UUIDv7
    │   ├── flow/ block/                     Flow aggregate with its blocks, links, configuration, error handlers
    │   ├── event/ invocation/               FlowEvent (kernel/event envelope plus lineage, stored now and seed),
    │   │                                    Invocation and its four statuses
    │   ├── delivery/                        Delivery: one per (event, block, input); buffered effects
    │   ├── timer/ kv/                       timers as due rows; KV entries and locks
    │   ├── namedexpr/                       named expressions per project, acyclic calls
    │   ├── coreblock/                       every core block in Go; Transform with the steps form
    │   ├── app/                             AppVersion: image digest, contract versions, Describe manifest;
    │   │                                    installations, registries, declarative widgets
    │   ├── agentpool/                       pools, hashed tokens
    │   └── limits/                          monthly and hourly event limits, retention
    ├── application/
    │   ├── edit/                            definition import and export with CEL, type-check at save
    │   ├── trigger/                         Deploy trigger blocks: root FlowEvent and deliveries for a
    │   │                                    matching kernel/event kind
    │   ├── deliver/                         flows.deliver: evaluate inputs with kernel/expr, run a core block
    │   │                                    or enqueue flows.agent_task; commit effects with completion
    │   ├── timers/ lifecycle/ status/       flows.timer, flows.lifecycle, flows.invocation.status
    │   ├── apps/                            registries, custom apps, version publish by image reference
    │   └── retention/                       60-day deletion in batches
    ├── infrastructure/
    │   ├── deploy/                          ACL, outbound only: Deploy blocks call orchestration/api and
    │   │                                    templates/api
    │   └── persistence/...                  flows schema, its own migration stream
    ├── interfaces/
    │   ├── rest/                            strict slice of shared/gen/flows; Flows editor endpoints
    │   ├── streams/                         Flows events SSE stream
    │   ├── agentgateway/                    wss /flows/agent/v1/connect in serve; claims flows.agent_task
    │   │                                    jobs per pool up to the agent's capacity; JSON frames
    │   ├── mcp/ httptrigger/ forms/
    │   └── consumers/                       every Deploy kind, for trigger blocks
    └── agent/                               CUSTOMER HOST, rule 7: infraweave flows-agent (decision 0018)
        ├── gateway/                         dials serve, frames, relays Host calls
        ├── host/                            the Host gRPC service: KV, locks, ListBlocks, SetWidget
        └── executor/{docker,kubernetes,process}/   one container per installation and digest; gVisor
                                             optional; mTLS and NetworkPolicy on Kubernetes

pkg/
├── sdk/                                     (P24) the SDK: wraps every pkg/client/<spec>
├── client/
│   ├── deploy/                              generated Go client                 ← pkg/client/deploy
│   ├── worker/                              (P8) generated; imported by execution/agent
│   ├── operator/                            (P3b)
│   └── flows/                               (Flows)
└── flows/                                   (Flows)
    ├── app/v1/                              buf-generated App and Host stubs
    └── appsdk/                              appsdk.Serve, typed blocks and schemas from Go types, apptest/
```

Templates, inventory and intelligence have the same four folders as the contexts drawn
in full; the walkthrough for templates lists its packages.

## Deep dives

### Stack configuration write path

```text
internal/stackconfig/
├── interfaces/rest/
│   ├── strict.go            Strict: the Stacks and Contexts operations of gen/deploy; embeds nothing
│   ├── stacks.go            StackCreate, StackUpdate, StackDelete, StackEnable, StackDisable, StackLock (forwarded)
│   ├── mapping.go           generated request types to application commands; kernel/iac settings
│   └── stacks_test.go       handler tests against a fake application service
├── application/stacks/
│   ├── create.go            CreateStack command handler
│   ├── update.go            UpdateStack (PUT replaces, PATCH merges, decision 0022)
│   ├── delete.go            DeleteStack, with destroyResources asking orchestration through events
│   ├── validate.go          vendor settings checked through vendors/api; pool through execution/api
│   └── *_test.go            handlers over persistence/memory
├── domain/stack/
│   ├── stack.go             Stack aggregate: NewStack factory, invariants, Update, Disable, Reslug
│   ├── source.go            Source value object: integration, repository, branch, project root, globs
│   ├── flags.go             autodeploy, autoretry, protectFromDeletion, localPreview, manageState
│   ├── state.go             StackState projection, set only by ApplyRunState
│   ├── repository.go        Repository interface: Get, BySlug, Save, Delete
│   └── events.go            StackCreated, StackUpdated, StackDisabled, StackDeleted
└── infrastructure/persistence/postgres/
    ├── stacks.go            Repository over stacks, stack_hooks; optimistic version column
    └── migrations/0001_stacks.sql 0002_contexts.sql 0003_config_elements.sql  (stream per context)
internal/shared/sql/uow/uow.go       Do(ctx, fn); the outbox joins the same transaction
internal/shared/outbox/outbox.go     Append(ctx, evt)
```

A stack update touches one aggregate. Authorization and validation happen before the
transaction; the transaction saves the aggregate and appends its event:

```go
// internal/stackconfig/application/stacks/update.go
func (h *Handler) Update(ctx context.Context, cmd UpdateStack) (stack.Stack, error) {
	p := principal.FromContext(ctx)
	s, err := h.stacks.Get(ctx, cmd.ID)
	if err != nil {
		return stack.Stack{}, err
	}
	d, err := h.authz.For(ctx, p) // identity/api
	if err != nil {
		return stack.Stack{}, err
	}
	if !d.Can(identityapi.ActionStackManage, s.Space()) {
		return stack.Stack{}, stack.ErrForbidden
	}
	if err := h.vendors.For(cmd.Vendor.Kind()).ValidateSettings(cmd.Vendor); err != nil {
		return stack.Stack{}, err // a 400 problem through shared/httpkit/strictkit
	}
	err = h.uow.Do(ctx, func(ctx context.Context) error {
		evt, err := s.Update(cmd.Changes(), h.clock.Now()) // invariants live in the aggregate
		if err != nil {
			return err
		}
		if err := h.stacks.Save(ctx, s); err != nil {
			return err
		}
		return h.outbox.Append(ctx, evt)
	})
	return s, err
}
```

Consequences arrive later: governance recomputes auto-attachments when labels change,
source control re-syncs webhooks when the repository changes, audit records the change.
None of them is in the stack's transaction, and a stack update never waits for any of
them.

The human stack lock (`stackLock`) is the exception to "stack settings live in
stackconfig". It gates run triggering and autodeploy ([report
07](../reference/07-stack-model.md#5-lock-and-unlock-enable-and-disable)), so it belongs
to the aggregate that enforces those rules, `orchestration/domain/queue.StackQueue`. The
REST operation sits in the Stacks tag, so `stackconfig/interfaces/rest/stacks.go`
forwards `StackLock` and `StackUnlock` to `orchestration/api.Runs.Lock`; the stack's
REST representation reads the lock back from the orchestration query. One HTTP resource
is assembled from two contexts.

### Run execution path

```text
internal/orchestration/
├── domain/queue/
│   ├── queue.go             StackQueue aggregate: Enqueue, Claim, Advance, Confirm, Discard, Finish
│   ├── lock.go              blocking runs (TRACKED, TASK, DESTROY) hold the stack; human and policy locks
│   ├── ready.go             QUEUED to READY gates: lock, approval, upstream runs, external dependencies
│   └── repository.go        Load(ctx, stack) with SELECT ... FOR UPDATE on stack_queues; Save
├── domain/run/
│   ├── state.go             the 22 RunState values, terminal and passive flags
│   ├── machine.go           allowed transitions T1 to T44 as a table, per run type: report 08's 43
│   │                        and decision 0008's T44, from docs/data/run-transitions.json
│   ├── priority.go          presets and the seven pool tiers of report 08
│   └── run.go               Run entity: type, state, state version, commit, priority, workflow ID
├── domain/timer/rules.go    2, 7 and 30 day rules
├── application/
│   ├── trigger/trigger.go   Trigger: approval at QUEUED, Enqueue
│   ├── dispatch/claim.go    Claim(pool, worker): the pool's top READY, CONFIRMED or REPLAN_REQUESTED run by tier
│   ├── advance/advance.go   worker reports: PREPARING to INITIALIZING to PLANNING and onwards
│   ├── plan/recorded.go     RecordPlan: plan policies, then PLANNING to its next state
│   └── timers/sweep.go      handler of the run.timers periodic task
└── infrastructure/persistence/postgres/
    ├── queues.go runs.go transitions.go
    └── readyindex.go        (worker_pool, state, tier, created_at) index scan with SKIP LOCKED
internal/shared/queue/       jobs, periodic tasks and the schedule ticker (decision 0011)
```

Trigger to worker, as aggregates see it:

1. `trigger.go` records the run's type, commit and target. The run specification is
   not stored: execution's `payload` reads `stackconfig/api.Stacks.RunSpec` at handover,
   because decision 0013 opens sealed values only when a spec is built.
2. In one transaction `StackQueue.Enqueue` creates the run in `QUEUED`, evaluates the
   readiness gates and, when nothing blocks, moves it to `READY` (T1, T2). Approval
   policies are evaluated before the transaction through
   `governance/api.Evaluator.Approval`, because the aggregate needs the decision as a
   fact, not a call.
3. The transaction issues `NOTIFY infraweave_runs` for the pool (decision 0008), which
   PostgreSQL delivers at commit and which wakes a held poll.
4. A worker's long-poll reaches `execution/application/poll`, which calls
   `orchestration/api.Dispatcher.Claim`. Claim picks the pool's next run in tier order
   (report 08 [priority](../reference/08-run-lifecycle.md#priority)), locks its
   `StackQueue` and moves it to `PREPARING` (T7), `PREPARING_APPLY` (T33) or
   `PREPARING_REPLAN` (T30), recording the worker.
5. Execution marks the worker busy in its own transaction before the poll answers, and
   `RunClaimed` reconciles the flag if that write is lost. Report 10's scale-in path
   drains a worker and then reads `busy`
   ([section 3.3](../reference/10-workers-and-execution.md#33-drain-cycle-and-recycle)),
   so a worker whose claim is not yet reflected looks idle and can be terminated with a
   run just handed to it. Decision 0008 closes that window by marking the worker busy in
   the claim's own transaction, which two aggregates in two contexts cannot do; this
   option narrows it instead.

Every transition is a method on the aggregate, and the transaction is the aggregate's:

```go
// internal/orchestration/application/dispatch/claim.go
func (h *Claimer) Claim(ctx context.Context, pool ids.PoolID, worker ids.WorkerID) (api.Assignment, error) {
	var a api.Assignment
	err := h.uow.Do(ctx, func(ctx context.Context) error {
		cand, err := h.ready.Next(ctx, pool) // SKIP LOCKED over the ready index, tier order
		if err != nil {
			return err // run.ErrNone: the poll handler holds the request open until a wakeup
		}
		q, err := h.queues.Load(ctx, cand.Stack) // FOR UPDATE on the stack's queue row
		if err != nil {
			return err
		}
		evts, err := q.Claim(cand.Run, worker, h.clock.Now()) // READY to PREPARING, or CONFIRMED to PREPARING_APPLY
		if err != nil {
			return err // the run changed since the index read; the caller takes the next one
		}
		if err := h.queues.Save(ctx, q); err != nil {
			return err
		}
		a = api.AssignmentFrom(q.Run(cand.Run))
		return h.outbox.Append(ctx, evts...)
	})
	return a, err
}
```

`StackQueue` loads the stack's non-terminal runs only. A finished run leaves the
aggregate in the transaction that finishes it and survives as a row for queries, so the
aggregate stays small for stacks with long histories. Stacks with many open pull
requests carry many proposed runs in `QUEUED` or `READY`, and each claim loads all of
them; that is the aggregate's cost, and the place to watch.

### Policy evaluation with embedded OPA

```text
internal/governance/
├── api/
│   ├── evaluator.go         Evaluator: Push, Approval, Plan, Trigger, Notification, Login, Intent
│   ├── inputs.go            input documents per type, the published language of report 09 section 4
│   └── decision.go          Decision: outcome, flags, messages, receipt IDs
├── domain/
│   ├── policy/policy.go     Policy aggregate: type (13 names, 3 aliases normalised), engine (REGO_V0, REGO_V1)
│   ├── attachment/          explicit and autoattach:<label> resolution across the space tree
│   ├── decision/combine.go  per-type combination: deny wins, ignore wins, every approval needed, ...
│   ├── flags/flags.go       flags carried on the run, reset-before-set
│   └── receipt/receipt.go   PolicyReceipt, Sample (hourly quota, 7-day retention)
├── application/evaluate/
│   ├── evaluate.go          resolve attached policies, build preamble, run engine, combine, store receipts
│   ├── push.go plan.go approval.go trigger.go notification.go login.go intent.go
│   └── budget.go            per-type deadlines and the per-event budget (10 minutes for push)
└── infrastructure/opa/
    ├── engine.go            implements application/port.Engine with github.com/open-policy-agent/opa/v1/rego
    ├── prepared.go          PrepareForEval per (policy ID, body hash, Rego version), shared across goroutines
    ├── capabilities.go      the six disabled built-ins removed from ast.CapabilitiesForThisVersion()
    └── preamble/            per-type preamble modules, embedded
```

Governance is an open host service: the caller assembles the input document in
governance's published language and receives a decision. Governance never imports
orchestration, source control or identity's internals; it imports their `api/` packages
only for authorization of its own REST surface. The callers depend on governance, not
the other way, so the hooks of [report
09](../reference/09-policies.md#8-where-each-type-is-evaluated) form no cycle:

```go
// internal/governance/api/evaluator.go
// Evaluator decides for a target and an input document. A decision is
// final for the event that asked: a timeout or engine error comes back as
// a Decision with outcome Error, never as a Go error, so a caller cannot
// mistake an evaluation failure for an infrastructure failure and retry
// into a second, different decision.
type Evaluator interface {
	Push(ctx context.Context, target Target, in PushInput) (Decision, error)
	Approval(ctx context.Context, target Target, in ApprovalInput) (Decision, error)
	Plan(ctx context.Context, target Target, in PlanInput) (Decision, error)
	Trigger(ctx context.Context, target Target, in TriggerInput) (Decision, error)
	Notification(ctx context.Context, space ids.SpaceID, in NotificationInput) (Decision, error)
	Login(ctx context.Context, in LoginInput) (Decision, error)
}
```

Evaluation writes receipts and samples in governance's own transaction before it
returns. The caller's transaction then records the receipt IDs on the run. If the
caller's transaction fails, governance holds receipts for a transition that did not
happen; they are harmless, carry the run ID and state version, and are pruned by
retention. That is the price of two aggregates in two contexts, and a departure from
decision 0009, which writes receipts in the transaction of the transition they cause. The
OPA usage follows
[report 27](../reference/27-reference-materials.md#91-the-go-api): prepared queries,
`SetRegoVersion` per policy, a capability set without the disabled built-ins, and
context deadlines from [report 09](../reference/09-policies.md#23-evaluation-timeouts).

The worker-side initialization policy ([report
10](../reference/10-workers-and-execution.md#10-signed-runs-and-worker-side-policy))
does not use governance. It is evaluated on the customer's host by
`execution/agent/initpolicy`, which imports the OPA `v1` packages directly under rule 7,
because the worker side cannot reach governance's tables.

### Worker protocol and pools

```text
internal/execution/
├── interfaces/workerproto/          implements shared/gen/worker.StrictServerInterface (P8)
│   ├── register.go                  JWS signed by the pool key; Worker aggregate created
│   ├── poll.go                      long-poll held up to the worker's timeout, also the heartbeat;
│   │                                wakeups on infraweave_runs (decision 0008)
│   ├── spec.go                      the run specification, secrets sealed to the pool key
│   ├── report.go                    phase reports, plan results, outcome, managed entities
│   ├── uploads.go                   upload URLs, log chunks, artifacts, workspace download
│   └── signals.go                   stop, kill and cycle delivered on the next poll or an open poll
├── application/poll/poll.go         Claim through orchestration/api, then payload assembly
├── application/payload/
│   ├── assemble.go                  run spec, hooks, environment, mounted files
│   ├── credentials.go               cloud/api.Credentials.ForRun, state run credentials, registry token
│   └── urls.go                      presigned log upload, workspace upload and restore, source archive
├── infrastructure/seal/             the spec's secret part sealed to the pool's P-256 public key
└── agent/                           worker side (rule 7)
    ├── launcher/launcher.go         register, poll loop, one container per job, spot notice handling
    ├── launcher/mount.go            mounts os.Executable() into the runner image as the root process
    ├── docker/ kubernetes/          container runtimes
    ├── runner/phases.go             infraweave runner: before and after hooks, vendor commands
    ├── runner/report.go             state changes over pkg/client/worker
    └── workspace/                   archive, encrypt and upload at UNCONFIRMED; restore at PREPARING_APPLY
pkg/client/worker/                   generated client (P8)
api/src/worker/                      paths-workers.yaml: register, poll, deregister, spec, reports, uploads
```

The protocol is HTTP long-poll initiated by the worker, the surviving transport of
[report 10](../reference/10-workers-and-execution.md#42-transports), written as the
`worker` spec so the agent gets a generated client and the server a strict interface.
Pools and workers are aggregates in execution; runs are not. Execution conforms to
orchestration's run model: it may claim, report and forward, but every run state change
is made by orchestration's aggregate.

The launcher and runner link only `execution/agent`:

```go
// cmd/infraweave/launcher.go
func newLauncherCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "launcher",
		Short: "Run a launcher for a worker pool",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := agentconfig.Load(cmd) // strict YAML, flag > env > file
			if err != nil {
				return err
			}
			return launcher.Run(cmd.Context(), cfg) // internal/execution/agent/launcher
		},
	}
	return cmd
}
```

Public pool workers are the same launcher started by the operator with a public pool
token. Private pools add credentials, sealing, drain and the Kubernetes runtime in P14
without a change to orchestration.

The spec's secret part travels in decision 0013's pool envelope: an ephemeral P-256 key,
HKDF-SHA256 bound to both public keys and the format version, and AES-256-GCM with a
random nonce, with the run ID and state version as associated data. Both sides need the
same format, and rule 7 keeps the agent away from `shared/crypt`, so the format is
`kernel/sealfmt`: `execution/infrastructure/seal` seals with it on the server,
`agent/launcher` opens with it on the customer's host, and `agent/workspace` seals
workspaces in its object format under a content key that travels in the same envelope.
Every construction is FIPS 140-3 approved, so the binary built with
`GOFIPS140=certified` runs the same code under `GODEBUG=fips140=only`.

### VCS webhook ingest

```text
internal/sourcecontrol/
├── interfaces/webhook/receive.go       POST /webhooks/vcs/{provider}/{integrationId}; verify, store, 202
├── application/ingest/
│   ├── receive.go                      Delivery aggregate saved with its raw body before acknowledgement
│   └── process.go                      on DeliveryReceived: translate, route, evaluate, trigger
├── application/routing/
│   ├── match.go                        stackconfig/api.Stacks.ByRepository(integration, repo)
│   ├── push.go                         governance/api.Evaluator.Push per stack or module
│   └── dedupe.go                       commit SHA plus run type per stack; comment commands bypass
├── application/feedback/
│   ├── checks.go                       individual or aggregated checks per integration settings
│   ├── comments.go                     Comments.Upsert with a deduplication key
│   └── deployments.go                  GitHub deployments from tracked run states
├── domain/event/                       Push, PullRequest, Comment, Tag: provider-neutral
└── infrastructure/github/              ANTICORRUPTION LAYER
    ├── verify.go                       X-Hub-Signature-256 HMAC
    ├── translate.go                    push, pull_request, issue_comment, check_run to domain/event
    ├── client.go                       app installation tokens, rate limits
    └── checks.go comments.go deployments.go
```

Ingest is durable from the moment of acknowledgement: `receive.go` saves the `Delivery`
aggregate (large bodies in object storage) and appends `DeliveryReceived` in one
transaction, then answers `202`. Processing runs in the scheduler process mode from the event,
whose dispatch plays the part of decision 0012's `vcs.process_delivery` job.
The rule comes from [report
27](../reference/27-reference-materials.md#12-cross-tool-comparison), which records the
failure of processing in a goroutine that outlives the request.

Each provider is an anticorruption layer. GitHub's `pull_request`, GitLab's merge
request event and Bitbucket's PR events become one `domain/event.PullRequest`, with the
per-provider meanings of `approved`, `mergeable` and `head_owner` resolved in the
provider package ([report
11](../reference/11-vcs-integrations.md#112-what-an-event-becomes)). Nothing outside
`sourcecontrol/infrastructure/<provider>` knows a provider's payload, so a new provider
in P19 adds a package and touches no other context.

### State backend

```text
internal/state/
├── interfaces/httpbackend/
│   ├── routes.go           GET, POST, LOCK, UNLOCK at /state/v1/stacks/{stack}[/workspaces/{workspace}]
│   │                       (decision 0010); run credentials in basic auth
│   └── routes_test.go
├── application/
│   ├── read.go write.go    orchestration/api.RunAuthority.MayRead and MayWrite before any object access
│   ├── lock.go             Lock aggregate per stack (and per Terragrunt unit), lock ID checks
│   ├── import.go           at creation and on a locked idle stack, 100 MB cap
│   ├── rollback.go         rollback as a new version
│   └── external.go         read-only external access, per-stack switch, space permissions
├── domain/
│   ├── backend/            StateBackend aggregate: managed flag fixed at creation, external access switch
│   ├── version/            StateVersion: object key, size, SHA-256, serial, lineage, run, rollback and
│   │                       import markers
│   └── lock/               Lock: holder, ID, created
└── infrastructure/
    ├── objects/            one sealed object per version through shared/objectstore, written once
    └── persistence/postgres/  managed_state_versions, state_locks
```

The backend speaks Terraform's `http` protocol to runs ([report
16](../reference/16-state-management.md#11-how-a-run-reaches-it)). Credentials are per
run and checked against the run's liveness and phase, which only orchestration knows;
state asks through `orchestration/api.RunAuthority` on every request. Each version is its
own object under a new key, sealed in the object format of decision 0013 and never
overwritten, so history and rollback rest on rows, not on bucket versioning (decisions
0005 and 0010). A write stores the object as `pending` first, then records the
`StateVersion` and marks the object `live` in state's transaction. A crash between the
two leaves a pending object that `blob.expire` deletes after 24 hours, never a recorded
version without its object.

### Module and provider registry protocol

```text
internal/registry/
├── interfaces/protocol/
│   ├── discovery.go        /.well-known/terraform.json: login.v1, modules.v1, providers.v1, state.v2
│   ├── modules.go          {ns}/{name}/{provider}/versions, /{version}/download (204, X-Terraform-Get)
│   ├── providers.go        {ns}/{type}/versions, /{version}/download/{os}/{arch}
│   └── auth.go             bearer tokens through identity/api; empty lists on missing access
├── domain/
│   ├── module/             Module aggregate: source, space, workflow tool, sharing
│   ├── moduleversion/      Version: number rules (no reuse, no gaps), PENDING, ACTIVE, FAILED, yanked
│   ├── provider/ providerversion/ gpgkey/
│   └── sharing/            space inheritance, explicit availability between accounts of one organization
│                           (422 validation-failed for another organization's), public visibility
├── application/
│   ├── versions/           on TagPushed: create PENDING version, trigger TESTING runs
│   ├── testcases/          on RunFinished for TESTING runs: ACTIVE or FAILED
│   └── publish/            provider drafts, per-platform presigned uploads, signature checks
└── infrastructure/archives/    module and provider archives, short-lived signed download URLs
```

The protocols are hand-written chi routes, outside oapi-codegen, matching [report
15](../reference/15-module-and-provider-registry.md#9-registry-protocol) including its
departures from the protocol specification (an unknown module answers `200` with an
empty list). Module test cases are runs: registry triggers `TESTING` runs through
`orchestration/api.Runs.Trigger` with a module target and learns their outcome from
`RunFinished`. A module is therefore not a stack in this layout; it shares the run
machinery through orchestration's published `Target` (stack or module), and its source,
environment and attachments are registry's own, which duplicates the shape of
stackconfig's `Source` and attachment types.

### Access, identity and spaces

```text
internal/identity/
├── interfaces/middleware/authn.go      API key, session cookie, run token or bearer to kernel/principal
├── api/
│   ├── authorizer.go                   For(ctx, principal) (Decider, error)
│   ├── decider.go                      Can(action, space) bool; Reachable() []SpaceID for list filters
│   └── runtokens.go                    Issue(run, audience): state, registry and API tokens for a run
├── domain/space/tree.go                SpaceTree aggregate: one per account under its top space; reparent with
│                                       reachability checks
├── domain/access/decide.go             domain service: propagation down, read up through inheritance,
│                                       implicit stack read; grant views RESIDENT, PROPAGATED, INHERITED
├── domain/role/catalogue.go            the action catalogue and system roles
└── application/authenticate/login.go   the account's login policy through governance/api.Evaluator.Login
```

A member authenticates at its organization, through the organization's identity
providers, and acts in one account at a time: entering an account evaluates that
account's `LOGIN` policies on its top space, and an organization key reaches only the
`Organization` tag. Every request carries a `kernel/principal.Principal` from the
middleware, naming one organization and one account. Each context's
application layer receives `identity/api.Authorizer` in its constructor and asks for a
`Decider` once per command. Listing endpoints filter by `Decider.Reachable()`, because
nearly every table carries a space column ([report
28](../reference/28-data-model.md#121-shapes-that-recur)). The space tree is one
aggregate per account, rooted at the account's top space, so a reparent and its
reachability check commit together; a large
tree makes every space edit contend on one row, which is acceptable for an operation
that is rare.

Identity calls governance for login policies and governance calls identity to authorize
policy edits. Both directions go through `api/` packages that import only the kernel, so
Go sees no cycle, but the constructor order needs a setter (see the composition root).

### Features and tiers

```text
internal/features/
├── api/entitlements.go                 Entitlements.For(ctx, organization) (Entitlement, error):
│                                       Has(feature) bool, Limit(name) (value, unlimited bool)
├── api/limits.go                       Limits.Check(ctx, account, name, n): the organization's pool,
│                                       then the account limit
├── api/catalogue.go                    FeatureOf(operationID), OptionOf(schema, field, value)
├── domain/catalogue/catalogue.go       capabilities, options, limits, requirements; Validate fails when an
│                                       operation or a gated field has no feature
├── domain/tier/tier.go                 Tier aggregate: name, features, limits; Enterprise immutable
├── domain/entitlement/compute.go       Compute(tier, grants, disabled, catalogue): set membership, then
│                                       drop every feature whose requirements are missing
├── application/operator/               SetOrganizationTier, Grant, Revoke, SetLimit, CreateTier,
│                                       SwitchBilling; each write bumps the organization's
│                                       entitlement_version (a column of features' own
│                                       organization_entitlements, since identity owns the organizations
│                                       table) and sends NOTIFY infraweave_invalidate
├── application/snapshot/               per-replica map organization → Entitlement, rebuilt on invalidation;
│                                       billing off answers Enterprise minus features.disabled
├── interfaces/middleware/gate.go       after identity's authn, before the strict handler
└── interfaces/operatorrest/            the operator API slice: tiers, grants, billing
internal/identity/interfaces/operatorrest/   the operator API slice: organizations, each created with its
                                             first account
internal/shared/gen/operator/           generated from api/src/operator (api/operator.openapi.yaml)
internal/app/gates.go                   queue and bus dequeue gates bound to Entitlements
```

The catalogue, tiers, grants and billing form a context of their own rather than part of
identity. Their language (feature, tier, grant, entitlement, billing) and their actor (the
operator, not a member) differ from identity's, and the catalogue is read by every
context, while the organization is one aggregate among identity's. Identity's
`Organization` therefore holds no tier: `OrganizationCreated` makes features create the
organization's `OrganizationEntitlement` with the installation's default tier, and the
snapshot answers that default for an organization whose event is still in flight. The
entitlement belongs to the organization because its accounts share one tier, one set of
grants and one pool of each limit; an organization admin may set an `AccountLimit` below
the pool through the served `Organization` tag. Decision 0021's model is data
throughout: a tier is a name, a set of features and a set of limit values; seeded Free,
Starter, Starter+, Business and Enterprise are installed once from the seed; grants
belong to the organization and survive a change of tier; `features.disabled` stays operator
configuration, decoded strictly.

Where each check sits:

| Kind | Check | Place in this option |
|---|---|---|
| Capability | Every operation's `x-infraweave-feature` | `features/interfaces/middleware/gate.go`, installed by `internal/app` after authn; answers `feature-disabled` (403) or `feature-not-in-tier` (403, naming the tiers that include it, smallest first) |
| Option | A write that sets a gated value, such as `vcsChecks: AGGREGATED` | the owning context's application handler, through `features/api.Entitlements.Has` during input validation |
| Limit | Creating a counted thing | the context that owns the thing: identity for `seats`, execution for `private-workers`, orchestration's dispatch for `public-worker-slots`, intelligence for `ai-budget`; the count spans the organization's accounts and then the account limit; answers `limit-reached` (403) |
| Background work | A queued job or schedule of a feature outside the entitlement | the dequeue gate in `shared/queue` and `shared/eventbus`, bound in `internal/app/gates.go`; the job completes as skipped and is counted in `infraweave.features.skipped` |

`billing` is off by default, so the snapshot answers every organization as Enterprise minus
`features.disabled`, and no tier or limit check runs. Operators still edit tiers and
grants through the operator API (`api/operator.openapi.yaml`, operator principals only,
wrapped by `infraweavectl operator`), and a change takes effect on every replica at its
next invalidation, without a restart. A downgrade deletes nothing.

Limits expose the context boundary. The count lives in the caller's context (identity
counts users and API keys, execution counts workers) and the limit value lives in
features. DDD offers two shapes and this option takes the first:

- **Open host service, synchronously.** The caller reads `Limit(name)` from the
  snapshot before its own transaction, then counts and inserts inside it. The count and
  the insert are consistent with each other, but the limit value is read outside that
  transaction, so a create that starts just before a downgrade commits against the old
  limit. Decision 0021 tolerates an account over a limit (nothing stops, nothing more is
  added), so the race costs at most one extra item per concurrent create.
- **A replica in each caller.** Each context keeps its own copy of the limits it
  enforces, fed by `EntitlementChanged`. The read is local, but the replica lags by one
  delivery, and the lag is unbounded while the bus is behind; this option rejects it for
  limits.

The capability gate and the dequeue gates are cross-cutting: they need the operation's
feature for every context, which DDD does not draw as a context's concern. The gate runs
as middleware from features' `interfaces`, and the queue and bus learn of features only
through a function `internal/app` binds, because rule 6 keeps `shared` free of contexts.

### Events: notifications, webhooks and audit

```text
internal/kernel/event/                  the shared envelope, the kind registry and each kind's JSON Schema
internal/<context>/domain/events.go     events defined by their producer, with Name() and a version
internal/<context>/api/events.go        the published copies other contexts subscribe to, in the
                                        kernel/event envelope
internal/shared/outbox/                 one outbox table; Append joins the producer's unit of work, enqueues
                                        the events.fanout job and NOTIFY infraweave_events at commit
internal/shared/eventbus/
├── bus.go                              Subscribe(name, subscriber, handler); dispatch on shared/queue
├── idempotency.go                      processed (event ID, subscriber) rows
├── deadletter.go replay.go             parked events, operator replay
└── bustest/                            ordering per key, redelivery, rollback drops the event
internal/notifications/
├── interfaces/consumers/run.go         RunStateChanged, ModuleVersionPublished, WorkerUsage, InternalError
├── application/route/route.go          notification policy through governance/api.Evaluator.Notification
├── application/deliver/
│   ├── inbox.go                        grouping key, occurrences, 14-day retention
│   ├── webhook.go                      named webhooks: HMAC SHA-256, secret headers, retries, auto-disable
│   ├── stackwebhook.go                 one POST per run state change on a stack or module
│   ├── slack.go
│   └── prcomment.go                    sourcecontrol/api.Comments.Upsert
└── infrastructure/httpsender/          delivery log bodies in object storage
internal/audit/
├── interfaces/consumers/all.go         one subscriber for every published event
├── application/record/                 actor, action, affected and related resource, IP
└── application/stream/                 the account's audit trail webhook
internal/flows/interfaces/consumers/    Deploy trigger blocks: one subscriber for every Deploy kind
internal/shared/httpkit/sse/            the stream writer; orchestration, execution and flows serve streams
```

One outbox carries every published event; every downstream is a subscriber with its own
idempotency rows. The outbound surfaces of [report
19](../reference/19-events-notifications-audit.md#1-the-outbound-surfaces) are
subscribers in two contexts: notifications routes by policy, audit records everything.
Audit is conformist: it accepts each producer's event shape and maps it to its 61
resource types, so a producer changing an event changes audit's mapping in the same pull
request.

Audit needs the actor and IP of the request that caused a change. The producer's
aggregate does not know them, so `shared/outbox.Append` reads the principal and request
metadata from the context and stores them in the event envelope, outside the domain
event.

Decision 0015 makes the envelope one model for Deploy and Flows: the same `id`, `kind`,
`account`, subject, `occurred_at` and `document`, the same emit path and one kind
registry (`run_updated`, `drift_detected`, `policy_evaluated`, `notification_created`,
`module_version`, `internal_error`, `worker_usage`, `worker_updated`, `flows.event`). In a
DDD layout each context would publish events in its own language, and a downstream such
as Flows would translate them through an anticorruption layer. That translation is the
bridge decisions 0015 and 0018 rule out, so the envelope and the kind schemas move into
the shared kernel, `kernel/event`: every event a producer publishes travels in that
envelope, and the kinds that streams, webhooks and Flows read are registered there with
typed documents. The kind schemas are therefore a contract every producer shares with every
consumer, including Flows trigger blocks and the expressions that read them: a field a
producer adds changes the shared kernel, and the change reaches every context in one pull
request.

The fan-out targets are subscribers: notifications (inbox, the notification policy,
named and stack webhooks, Slack), source control (commit checks), audit (the audit
webhook) and flows, whose subscriber finds every enabled Deploy trigger block whose kind
and filter match and, in its own transaction, inserts the root `flow_events` row linked
to the Deploy event and one `flows.deliver` job per linked input. Every string a
notification policy returns is a text template of decision 0024, rendered by
`notifications/application/deliver` against the event document with `kernel/expr`.

The server-sent-event streams of decision 0015 are part of the public API, each with a
polling equivalent: run logs and run state (`orchestration/interfaces/streams`), the
worker pool queue (`execution/interfaces/streams`) and Flows events
(`flows/interfaces/streams`). Each writes through `shared/httpkit/sse` and wakes on
`shared/sql/listen`, which holds one `LISTEN` connection per `serve` replica for
`infraweave_events` and `infraweave_logs`. A stream authorizes as its polling
equivalent when it opens and checks each message's subject against the caller's
permissions as it sends.

### Flows engine

```text
internal/flows/
├── domain/
│   ├── event/ invocation/              FlowEvent: kernel/event envelope plus flow, block, output, primary
│   │                                   and secondary parents, echo, pending status, stored now and seed
│   ├── delivery/delivery.go            Delivery(event, block, input): Complete(result) returns the buffered
│   │                                   effects (events, pending events, timers, messages) to commit
│   ├── coreblock/                      core blocks in Go; Transform takes one expression or the steps form
│   ├── app/                            AppVersion: OCI image digest, contract versions, Describe manifest
│   └── limits/                         monthly and hourly counters per flow, UTC boundaries
├── application/
│   ├── trigger/deploy.go               subscriber on every Deploy kind: matching trigger blocks, the root
│   │                                   FlowEvent and its flows.deliver jobs in one transaction
│   ├── deliver/deliver.go              flows.deliver on the flows logical queue in scheduler
│   ├── deliver/complete.go             commits effects with the delivery's completion; retries only
│   │                                   infrastructure failures; fail() routes to the errorHandler
│   ├── timers/ lifecycle/ status/      flows.timer, flows.lifecycle (one per entity), flows.invocation.status
│   └── apps/                           registries fetched hourly, digest checks, custom app versions
├── infrastructure/deploy/              ACL, outbound: Trigger Run, Confirm Run, Deploy Template call
│                                       orchestration/api and templates/api with the project's role bindings
├── interfaces/agentgateway/            wss /flows/agent/v1/connect in serve: hello, task, call, reply,
│                                       result and ping frames; claims flows.agent_task jobs per pool
└── agent/                              infraweave flows-agent (rule 7)
    ├── gateway/                        dials any serve replica with the pool ID and token
    ├── host/                           Host gRPC service: KV, locks, ListBlocks, SetWidget, relayed live
    └── executor/{docker,kubernetes,process}/   one container per installation and digest, reused 15 minutes
api/proto/flows/app/v1/                 App: Describe, SyncApp, DrainApp, SyncBlock, DrainBlock, HandleEvent,
                                        HandleHTTP, HandleTimer, HandleMessage, HandleSchedule; Host
pkg/flows/app/v1/                       buf-generated stubs; buf breaking against the last release in CI
pkg/flows/appsdk/                       appsdk.Serve(app), typed blocks, schemas from Go types, apptest
api/src/flows/                          the Flows REST spec; internal/shared/gen/flows; pkg/client/flows
```

Flows is the clearest bounded context in the system: its own projects, identifiers,
schema and migration stream, and its own API under `/flows/v1/`. Flows belongs to the
account, which takes the place of Spacelift Flows' organization, and shares identity's
members, teams and service accounts; Spacelift Flows' organization admin is the account
admin; Flows roles are flows rows, and Deploy-side Flows project role bindings are rows in identity with
actor kind Flows project.

The engine is application code in this context on decision 0011's queue: every job runs
in the `scheduler` process mode on the logical queue `flows`, so a busy flow never delays
Deploy's fan-out. Emitting an event inserts the `flow_events` row and one `flows.deliver`
job per linked input in one transaction. `deliver.go` evaluates the input's configuration
with `kernel/expr` against the event's stored `now` and seed, then runs a core block in
the same job or enqueues a `flows.agent_task` for an app block. Effects come back
buffered and commit with the delivery's completion, so a retried delivery never emits
twice; KV calls are the exception and run live. Sleep, Show Form, pending events and SDK
timers are rows with a due time that the queue's schedule ticker turns into `flows.timer`
jobs, so a wait of days costs one row. The Flows features (`flows-engine`, `flows-apps`,
`flows-agents`, `flows-data-tables`, `flows-forms`, `flows-mcp-tools`) are catalogue
entries like any other: the middleware gates the operations, and the dequeue gate skips
the jobs of a feature outside the entitlement of the account's organization.

Apps are versioned protobuf `App` gRPC services, package `infraweave.flows.app.v1`. An
app version is an OCI image plus the manifest captured at publish; infraweave stores the
manifest, never the image. `infraweave flows-agent` dials the gateway in `serve`, receives
`task` frames whose requests are the protobuf messages in their JSON encoding, starts or
reuses the app's container (Docker with optional gVisor, a Kubernetes pod with mutual TLS
and a `NetworkPolicy`, or a local process for development), calls the app over gRPC on a
channel only the agent shares, and serves the `Host` service for the calls that must
happen live. gRPC never crosses the network to the server, and the worker long-poll of
decision 0008 is untouched. An app receives evaluated configuration and only the secrets
it resolved to; it never sees an expression. Apps add declarative widgets only (text,
Markdown, key and value list, table, status, link, chart), which the console renders, so
no app code runs in the browser. `pkg/flows/appsdk` is the Go app SDK, and any language
with gRPC can serve `App`.

DDD places all of this well inside one context. What it does not place well is the link
to Deploy. The context map's usual answer, an anticorruption layer that turns Deploy's
published events into block events, is the bridge decisions 0015 and 0018 exclude: a
trigger block's output is the Deploy event's document as its producer wrote it, typed by
the shared kernel's schema, and the expressions downstream are type-checked against that
schema. So `flows/application/trigger` is conformist to every Deploy producer through
`kernel/event`, and only the outbound Deploy blocks keep an anticorruption layer. Flows
could still leave the monolith, but it would take the shared kernel with it and receive
Deploy events over a published stream rather than in the fan-out's transaction.

### Expressions

```text
internal/kernel/expr/                   the language, its library, the type provider and the limits
├── env.go                              one cel-go environment per surface: declared variables and types
├── template.go                         ${{ }} text templates, $${{ escape, rendering of values into text
├── library/                            strings, lists, maps (merge, pick, omit), regex, encodings, maths,
│                                       time, random.* from the seed, fail(message)
├── schema.go                           JSON Schema to CEL object types; additionalProperties to map(dyn)
├── limits.go                           expressions.cost_limit, timeout, max_source, max_result
├── cache.go                            compiled programs keyed by hash of source, surface and types
└── shell.go                            POSIX quoting of every interpolated value in workflow templates
internal/expressions/                   the editor endpoints, over each owner's ExpressionSurfaces
internal/<owner>/api/surfaces.go        flows (inputs, static config, output customization, Transform,
                                        named expressions), templates (bodies), notifications (message
                                        text), stackconfig (environment interpolation, output references),
                                        vendors (workflow command templates)
internal/governance/interfaces/rest/    POST /policies/check: Rego diagnostics, beside simulation
```

The language belongs to no context, so it is a shared kernel package. Evaluation happens
in the context that owns each surface, in that surface's code path:

| Surface | Evaluated by | Process mode |
|---|---|---|
| Flows input and static configuration, output customization, Transform | `flows/application/deliver` | `scheduler` |
| Blueprint and template bodies | `templates/application` | `serve` for a synchronous create, `scheduler` for a queued deployment change |
| Output references, environment interpolation | `execution/application/payload` at handover, with typed contexts from `stackconfig/api` | `serve` |
| Notification text | `notifications/application/deliver` | `scheduler` |
| Workflow command templates | `execution/agent/runner` (rule 7 allows the kernel) | the runner |

Typed contexts come from each owner's published language: block schemas from Flows'
core blocks and app manifests, template inputs from templates, the event kind schemas
from `kernel/event`. Those schemas are already the contracts DDD publishes between
contexts, so type-checking at save and the editor's completions reuse them rather than
inventing new ones. Secrets resolve on the server, only by literal key, and a value
derived from one is masked; agents never see an expression. Every evaluation runs under
cel-go's cost limit and a timeout, and `now` and the seed are fixed and stored with the
event or change record, so retries and replays evaluate the same values.

The editor endpoints (`/expressions/check`, `complete`, `evaluate`) span every
surface, and one REST operation has one owning slice, so they sit in `expressions`, a
generic subdomain with no tables like `vendors`. It is a conformist customer of each
surface owner's `ExpressionSurfaces`, which resolves a reference (a flow block and input,
a template, a stack) to its typed context after authorizing the caller as a read of it.
Flows serves the same operations under `/flows/v1/` from its own slice over the same
code. `infraweavectl expr` and the provider's plan-time check call the same endpoints.

### Console and clients

```text
pkg/client/{deploy,operator,flows}/     generated by oapi-codegen from the served bundles
pkg/sdk/                                the SDK: one client over every spec, token exchange, idempotency,
                                        retries, pagination iterators, typed problems and streams (P24)
cmd/infraweavectl/                      cobra and viper over pkg/sdk only: stack, module, workerpool,
                                        flows, operator, expr, mcp and the other groups (P24b)
web/console/                            React 19, TypeScript strict, Vite, TanStack Router and Query,
                                        orval-generated client and hooks, Radix and Tailwind, React Flow,
                                        CodeMirror 6; embed.go exposes the build (P24c)
internal/shared/httpkit/console/        serves the embed.FS at / with the CSP and the deep-link fallback
internal/<context>/interfaces/rest/     each response's allowedActions, from the context that enforces them
```

Every client is built on the public API and nothing else. The SDK wraps the generated
clients (decision 0001); `infraweavectl` calls only the SDK; the Terraform provider lives
in `weaveplatform/terraform-provider-infraweave` and imports the SDK at a pinned
version. The console is one more client (decision 0023): it calls only operations of the
served bundle with the browser session cookie, keeps its cache current from the
server-sent-event streams with polling as the fallback, and has no endpoint of its own.
`serve` answers it at `/` from the embedded build, with the strict content security
policy and no file from any other origin; a `go build` without the console build serves
a placeholder page.

The console never decides a permission or a state transition. Each resource carries
`allowedActions`, computed by the code that enforces those actions: the caller's grants
from identity's `Decider`, and for a resource with a state machine, the transitions its
aggregate allows. For a run that is orchestration's `StackQueue`, which serves runs
directly. A stack is harder, because its representation is assembled from two contexts:
its settings are stackconfig's, but whether it can be locked, unlocked, triggered or
deleted depends on orchestration's `StackQueue`. A single stack read asks
`orchestration/api` for the queue's guards; a stack list cannot make one call per row and
reads stackconfig's `StackState` projection, which `RunStateChanged` updates one delivery
later. So a list can offer an action the server then refuses with `409 invalid-state`,
and the console has to show that refusal from its problem response. Every option keeps
the client packages in the same places; this option differs in where `allowedActions`
comes from.

### Composition root

```text
internal/app/
├── app.go               Serve, Schedule, All: open shared infrastructure, build contexts, start the process mode
├── contexts.go          constructors in dependency order; setters for the two api cycles
├── subscriptions.go     every bus.Subscribe; about 70 lines at P27
├── strict.go            deploy strict server composed from context slices
├── protocols.go         webhooks, state backend, registry protocol, login.v1, worker protocol
├── modes.go             serve: router; scheduler: bus dispatch, cron, timers, process managers
├── gates.go             features/api bound to the queue and bus dequeue gates
├── console.go           the console handler mounted last, so every other route wins
└── config/              config.go plus one section type per context
cmd/infraweave/
├── main.go              cobra root
├── serve.go scheduler.go all.go migrate.go secrets.go   call internal/app
├── launcher.go runner.go              call internal/execution/agent only
├── vcsagent.go flowsagent.go          call their contexts' agent/ trees only
└── version.go                         prints shared/buildinfo
cmd/infraweave-agent/
└── main.go              launcher, runner, vcs-agent and flows-agent over the three agent/ trees
cmd/infraweavectl/
└── main.go              the CLI over pkg/sdk (P24b)
```

```go
// internal/app/contexts.go (shape at P13)
func buildContexts(rt *runtime) (*contexts, error) {
	fea := features.New(features.Deps{UoW: rt.uow, DB: rt.db, Outbox: rt.outbox, Listen: rt.listen,
		Disabled: rt.cfg.Features.Disabled}) // learns of organizations from identity's events, imports no context
	rt.queue.SetGate(fea.API().Entitlements)
	idn := identity.New(identity.Deps{UoW: rt.uow, DB: rt.db, Outbox: rt.outbox, Keys: rt.keys,
		Limits: fea.API().Entitlements}) // seats
	gov := governance.New(governance.Deps{UoW: rt.uow, DB: rt.db, Outbox: rt.outbox,
		Authz: idn.API().Authorizer, Objects: rt.objects})
	idn.SetLoginPolicy(gov.API().Evaluator) // identity and governance call each other's api
	ven := vendors.New(vendors.Deps{Releases: rt.http})
	stk := stackconfig.New(stackconfig.Deps{UoW: rt.uow, DB: rt.db, Outbox: rt.outbox,
		Authz: idn.API().Authorizer, Vendors: ven.API(), Crypt: rt.crypt})
	orc := orchestration.New(orchestration.Deps{UoW: rt.uow, DB: rt.db, Outbox: rt.outbox,
		Queue: rt.queue, Authz: idn.API().Authorizer, Stacks: stk.API().Stacks,
		Policies: gov.API().Evaluator, Vendors: ven.API()})
	exe := execution.New(execution.Deps{UoW: rt.uow, DB: rt.db, Outbox: rt.outbox,
		Dispatcher: orc.API().Dispatcher, Runs: orc.API().Runs, Objects: rt.objects,
		RunTokens: idn.API().RunTokens})
	stk.SetPools(exe.API().Pools) // stack validation reads pools; execution reads stacks' run specs
	scm := sourcecontrol.New(sourcecontrol.Deps{UoW: rt.uow, DB: rt.db, Outbox: rt.outbox,
		Stacks: stk.API().Stacks, Runs: orc.API().Runs, Policies: gov.API().Evaluator})
	st := state.New(state.Deps{UoW: rt.uow, DB: rt.db, Objects: rt.objects,
		Authority: orc.API().RunAuthority, Authz: idn.API().Authorizer})
	return &contexts{idn, fea, gov, ven, stk, orc, exe, scm, st}, nil
}
```

Features is built first: it imports no context and learns of organizations and accounts
from identity's events, so identity can take its limits in the constructor. The router installs the middleware
in a fixed order: `shared/httpkit` (request ID, recovery, body limits, idempotency, rate
limits), `identity/interfaces/middleware`, then `features/interfaces/middleware`, then
the strict server and the protocol routes, with the console handler last.

Process modes start different interfaces of the same contexts:

| Process mode | Starts |
|---|---|
| `serve` | the chi router: strict servers (deploy, operator, flows), protocol routes, worker protocol, the VCS and Flows agent gateways, SSE streams, the console at `/`, readiness on its own port |
| `scheduler` | `shared/eventbus` dispatch, every context's `interfaces/consumers`, the `flows` logical queue among the others, cron entries (timers, schedules, drift, retention), process managers |
| `all` | both, in one process, for single-node installs and acceptance tests |
| `launcher` | `execution/agent/launcher` only; `internal/app` is not called |
| `runner` (hidden) | `execution/agent/runner` inside the run container |
| `vcs-agent` | `sourcecontrol/agent` only, long-polling `serve` (decision 0012) |
| `flows-agent` | `flows/agent` only, dialling the gateway in `serve` and running app containers (decision 0018) |
| `migrate up`, `migrate status` | `shared/sql/migrate`, then exit (decision 0005) |
| `secrets generate-kek`, `rewrap`, `rotate` | `shared/crypt` key operations, then exit (decision 0013) |
| `version` | `shared/buildinfo` |

`cmd/infraweave-agent` builds the four agent process modes from the `agent/` trees alone.
Rule 7 already keeps those trees free of server code, so the slim build adds one file and
no rule.

### Generated code

| Spec | Fragments | Generated server | Implemented by | Client |
|---|---|---|---|---|
| deploy | `api/src/deploy/` (flat; one owning context per `paths-*.yaml`) | `internal/shared/gen/deploy` | `internal/app/strict.go` composes the `interfaces/rest` slices of identity, stackconfig, orchestration, execution, vendors, state, sourcecontrol, governance, cloud, registry, notifications, audit, templates, inventory, intelligence, expressions | `pkg/client/deploy`, wrapped by `pkg/sdk` |
| worker | `api/src/worker/` (P8) | `internal/shared/gen/worker` | `execution/interfaces/workerproto` | `pkg/client/worker` |
| operator | `api/src/operator/` (P3b) | `internal/shared/gen/operator` | the `operatorrest` slices of features and identity | `pkg/client/operator`, wrapped by `pkg/sdk` |
| flows | `api/src/flows/` (Flows) | `internal/shared/gen/flows` | `flows/interfaces/rest` | `pkg/client/flows`, wrapped by `pkg/sdk` |
| `App` contract | `api/proto/flows/app/v1/` (Flows) | `pkg/flows/app/v1` by `buf generate` | Flows apps; `Host` by `flows/agent/host` | the same stubs, used by `flows/agent` |
| console client | the served bundles | `web/console/src/api` by orval, committed | the console's TanStack Query hooks | none |
| hand-written | none (tagged `handwritten`, excluded) | none | `sourcecontrol/interfaces/webhook`, `state/interfaces/httpbackend`, `registry/interfaces/protocol`, `identity/interfaces/terraformlogin`, the agent gateways, the SSE streams' framing | none |

```go
// internal/app/strict.go
type deployServer struct {
	*identityrest.Strict
	*stackconfigrest.Strict
	*orchestrationrest.Strict
	*executionrest.Strict
	*governancerest.Strict
	stubs // depth two: an implemented operation shadows its 501 stub
}

type stubs struct{ deploy.UnimplementedStrict }

var _ deploy.StrictServerInterface = (*deployServer)(nil)
```

Each context's `Strict` implements only its operations. Two contexts that implement the
same operation make the method ambiguous at depth one, the method leaves the method set,
and the assertion above fails to compile. `internal/tooling/stubgen` keeps generating
`UnimplementedStrict`; `internal/tooling/openapibundle` keeps reading one flat directory
per spec, which is why fragments are owned by naming convention rather than by
subdirectory. `api/oapi-server-deploy.yaml` changes only its output to
`internal/shared/gen/deploy/server.gen.go`, and the Makefile's stubgen call and
`verify-generate` diff path follow it. `make generate` also runs `buf generate` for the
`App` contract and orval for the console's client, and `verify-generate` fails when
either differs from its source.

### Tests and enforcement

| Kind | Location |
|---|---|
| Domain unit tests | beside the code in `domain/`, no I/O: the run state machine table, combination rules, space reachability |
| Application tests | `application/` with `infrastructure/persistence/memory` repositories and fake `api` dependencies |
| Repository contract suites | `infrastructure/persistence/persistencetest/` per context, run against memory and against PostgreSQL through `shared/sql/sqltest` (testcontainers) |
| Port contract suites | `shared/objectstore/objectstoretest` for every driver, against RustFS (`s3`), fake-gcs-server (`gcs`), Azurite (`azureblob`) and local disk (`fs`); `shared/eventbus/bustest`, `shared/queue` suite, `governance/infrastructure/opa` parity against the policy example library |
| Kernel tests | `kernel/expr`: every library function, cost limits, determinism and shell quoting as tables; `kernel/sealfmt`: format vectors for both sealed formats; `kernel/event`: each kind's schema |
| Catalogue test | `features/domain/catalogue`: fails on any served operation or gated field without a feature |
| Contract compatibility | `buf breaking` on `api/proto/flows/app/v1` against the last release |
| Interface tests | `interfaces/rest` handlers against fake application services; `workerproto` against a fake dispatcher |
| Agent tests | `execution/agent/...` against a fake worker server built from `shared/gen/worker`'s test helpers; `flows/agent` against a fixture app built from `pkg/flows/appsdk` |
| Acceptance | `test/acceptance/` features per context, real PostgreSQL, object storage and a Terraform run (decision 0016) |
| FIPS | the whole suite under `GODEBUG=fips140=only` in a blocking CI job; release builds use `GOFIPS140=certified` |
| Console | `tsc --strict`, ESLint with typescript-eslint's strict configuration, Vitest at 95% in total and 90% per directory, Playwright journeys against a real `infraweave all`; each blocks |

Enforcement is depguard. Rules differ per context, and depguard matches package paths
by string prefix with no variable for the importing file's own context, so the boundary
and domain rules are written once per context. `lax` mode lets a longer `allow` prefix
override a shorter `deny` prefix (`strict` does too; the default mode lets any deny
win), which is how one rule says
"nothing internal except my own context, the kernel, shared infrastructure and other
contexts' `api`":

```yaml
# .golangci.yml (excerpt)
linters:
  enable: [depguard]
  settings:
    depguard:
      rules:
        stackconfig-boundary:                       # rule 1; one per context (18)
          list-mode: lax
          files: ["**/internal/stackconfig/**"]
          deny:
            - pkg: github.com/weaveplatform/infraweave/internal
              desc: other contexts only through their api package
          allow:
            - github.com/weaveplatform/infraweave/internal/stackconfig
            - github.com/weaveplatform/infraweave/internal/kernel
            - github.com/weaveplatform/infraweave/internal/shared
            - github.com/weaveplatform/infraweave/internal/identity/api
            - github.com/weaveplatform/infraweave/internal/orchestration/api
            - github.com/weaveplatform/infraweave/internal/execution/api
            - github.com/weaveplatform/infraweave/internal/vendors/api
            - github.com/weaveplatform/infraweave/internal/features/api
            # ... every other context's api: 17 entries
        stackconfig-domain:                         # rule 2; one per context (18)
          list-mode: strict
          files: ["**/internal/stackconfig/domain/**"]
          allow:
            - $gostd
            - github.com/weaveplatform/infraweave/internal/kernel
            - github.com/weaveplatform/infraweave/internal/stackconfig/domain
            - github.com/oklog/ulid/v2
        application:                                # rule 3; one rule for all contexts
          files: ["**/internal/*/application/**"]
          deny:
            - pkg: github.com/weaveplatform/infraweave/internal/stackconfig/infrastructure
              desc: use a port
            - pkg: github.com/weaveplatform/infraweave/internal/stackconfig/interfaces
              desc: interfaces call application
            # ... the same two entries for each context: 36 entries
        agent:                                      # rule 7
          list-mode: strict
          files:
            - "**/internal/execution/agent/**"
            - "**/internal/sourcecontrol/agent/**"
            - "**/internal/flows/agent/**"
          allow:
            - $gostd
            - github.com/weaveplatform/infraweave/internal/execution/agent/
            - github.com/weaveplatform/infraweave/internal/sourcecontrol/agent/
            - github.com/weaveplatform/infraweave/internal/flows/agent/
            - github.com/weaveplatform/infraweave/internal/kernel
            - github.com/weaveplatform/infraweave/internal/vendors/api
            - github.com/weaveplatform/infraweave/pkg/client/worker
            - github.com/weaveplatform/infraweave/pkg/flows/app
            - google.golang.org/grpc
            - github.com/open-policy-agent/opa/v1
            - github.com/go-jose/go-jose/v4
            - github.com/moby/moby/client
            - k8s.io/client-go
```

Totals at P27: 18 boundary rules of about 21 entries each, 18 domain rules of about 4,
the shared application rule of 36, and the kernel, shared, api, agent, tooling and `pkg`
rules: 43 rules and about 520 entries. A new context adds two rules and one `allow` line
to each of the other seventeen boundary rules. The kernel rule allows cel-go for
`kernel/expr` alone, so no other kernel package gains behaviour by accident. `.golangci.yml` also reports only new
issues against the merge base (`new: true`, `whole-files: true`), so a violation in an
untouched file is not reported until the file changes; the rule set has to be introduced
with a clean run over the whole tree.

Coverage: the floor applies per package. The packages that struggle are the uniform
layers' thin ones: `api/` packages (interfaces and event structs),
`domain/events.go`-only packages, `interfaces/consumers` with one subscription, and
skeleton contexts in the phase that creates them.

## Walkthroughs

### A GitHub push becomes a proposed run, a plan policy and a PR comment

Serve process mode:

1. `internal/app/protocols.go`: the router mounts `sourcecontrol/interfaces/webhook` at `/webhooks/vcs/{integration}`, outside the strict server.
2. `internal/shared/httpkit`: request ID, body limit, recovery.
3. `internal/sourcecontrol/interfaces/webhook/receive.go`: loads the `VCSIntegration` and calls `infrastructure/github/verify.go`, which checks `X-Hub-Signature-256` against the integration's webhook secret.
4. `internal/sourcecontrol/application/ingest/receive.go`: in one `shared/sql/uow` transaction saves the `Delivery` aggregate and appends `DeliveryReceived` through `shared/outbox`; answers `202`.

Scheduler process mode:

5. `internal/shared/eventbus/bus.go`: delivers `DeliveryReceived` to `sourcecontrol/interfaces/consumers/delivery.go`.
6. `internal/sourcecontrol/infrastructure/github/translate.go`: the `pull_request` payload becomes a `domain/event.PullRequest` with head SHA, branch, diff paths and the GitHub meaning of `mergeable`.
7. `internal/sourcecontrol/application/routing/match.go`: asks `stackconfig/api.Stacks.ByRepository` for the stacks bound to the integration and repository.
8. `internal/sourcecontrol/application/routing/push.go`: per stack, builds a `governance/api.PushInput` and calls `Evaluator.Push`.
9. `internal/governance/application/evaluate/push.go`: no push policy is attached, so the default policy runs: a changed file under the project root on a non-tracked branch gives `propose`. `PolicyEvaluated` and a receipt are stored in governance's transaction.
10. `internal/sourcecontrol/application/routing/dedupe.go`: no run exists for this SHA and run type on the stack.
11. `internal/orchestration/api.Runs.Trigger` into `orchestration/application/trigger/trigger.go`: records the run's type, commit and target; the run specification is read at handover in step 16; no approval policy is attached.
12. `internal/orchestration/domain/queue/queue.go`: `Enqueue` creates the `PROPOSED` run in `QUEUED` and moves it to `READY` in the same transaction (proposed runs take no stack lock); `RunCreated` and `RunStateChanged` go to the outbox; `AfterCommit` wakes the pool.
13. `internal/sourcecontrol/interfaces/consumers/run.go`: `RunStateChanged` sets a pending check through `application/feedback/checks.go` and `infrastructure/github/checks.go`.

Launcher, runner and serve process modes:

14. `internal/execution/agent/launcher/launcher.go` (on the public pool's host): its long-poll to `execution/interfaces/workerproto/poll.go` is open.
15. `internal/execution/application/poll/poll.go`: `orchestration/api.Dispatcher.Claim` moves the run to `PREPARING` (see [Run execution path](#run-execution-path)).
16. `internal/execution/application/payload/assemble.go`: run spec, `identity/api.RunTokens.Issue` for state and API access, the source archive URL from `sourcecontrol/api.Archives`, presigned log URLs.
17. `internal/execution/agent/launcher/mount.go` and `agent/docker/`: starts the runner container with the binary mounted as root process.
18. `internal/execution/agent/runner/phases.go`: `infraweave runner` runs `before_init`, `vendors/api.For(terraform)` init and plan commands, and reports each phase through `runner/report.go`.
19. `internal/execution/interfaces/workerproto/report.go` to `orchestration/api.Runs.Advance`: `INITIALIZING`, then `PLANNING`.
20. The runner uploads the plan JSON with a presigned URL and reports `PlanReady` with the vendor's `ChangeSet`.
21. `internal/orchestration/application/plan/recorded.go`: builds a `governance/api.PlanInput` with the vendor block from `vendors/api` and calls `Evaluator.Plan`. The plan policy passes with no warning.
22. `internal/orchestration/domain/queue/queue.go`: `PLANNING` to `FINISHED` (T17) with the receipt IDs; `RunStateChanged` and `RunFinished` to the outbox.

Scheduler process mode, consequences:

23. `internal/sourcecontrol/application/feedback/checks.go`: the check becomes success with the change summary.
24. `internal/notifications/interfaces/consumers/run.go` to `application/route/route.go`: `governance/api.Evaluator.Notification` returns a `pull_request` rule with a body and a deduplication key.
25. `internal/notifications/application/deliver/prcomment.go`: `sourcecontrol/api.Comments.Upsert` posts the comment through `infrastructure/github/comments.go`, or edits the one with the same key.
26. `internal/audit/interfaces/consumers/all.go`: records the run's creation and finish.

### A tracked run awaits approval and applies on a private worker

1. `POST /stacks/{id}/runs` reaches `orchestration/interfaces/rest/runs.go` through the composed strict server; `identity/interfaces/middleware/authn.go` has set the principal.
2. `internal/orchestration/application/trigger/trigger.go`: `identity/api.Decider.Can(TriggerRun, space)`; the run's type, commit and target are recorded, and the run specification is read at handover.
3. `governance/api.Evaluator.Approval` at `QUEUED` approves.
4. `internal/orchestration/domain/queue/queue.go`: `Enqueue` creates the `TRACKED` run; no other blocking run holds the stack, so the run takes the lock and moves to `READY`.
5. `internal/execution/agent/launcher` on the customer's host polls with the private pool's credentials; `Dispatcher.Claim` gives `PREPARING`.
6. `internal/execution/infrastructure/seal/seal.go`: the spec's secret part is sealed to the pool's public key in the `kernel/sealfmt` pool envelope before it leaves the server; `agent/launcher` opens it with the pool's private key.
7. The runner plans; `orchestration/application/plan/recorded.go` evaluates plan policies, which warn, so `PLANNING` goes to `UNCONFIRMED` (T20).
8. `internal/execution/agent/workspace/`: the runner archives, encrypts and uploads the workspace with a presigned URL and leaves the run; the container exits.
9. On entering `UNCONFIRMED`, `orchestration/application/plan/recorded.go` calls `Evaluator.Approval` again; the attached approval policy needs two reviews, so `canConfirm` is false.
10. `POST /stacks/{id}/runs/{run}/reviews` twice reaches `orchestration/application/actions/review.go`: each review is saved on the run inside `StackQueue`, then `Evaluator.Approval` is re-evaluated with the reviews; after the second it approves.
11. `POST .../confirm` reaches `actions/confirm.go`: `UNCONFIRMED` to `CONFIRMED` (T26).
12. The private launcher's next poll: `Dispatcher.Claim` takes the confirmed run to `PREPARING_APPLY` (T33); the payload holds a presigned workspace download.
13. `internal/execution/agent/workspace/` restores the workspace; the runner applies; the run moves to `APPLYING` (T35).
14. Terraform writes state to `state/interfaces/httpbackend/routes.go` with the run's credentials. `state/application/write.go` asks `orchestration/api.RunAuthority.MayWrite`, writes the version as a new sealed object through `state/infrastructure/objects`, then records the `StateVersion`.
15. The runner reports success: `APPLYING` to `FINISHED` (T37). In the same transaction `StackQueue` releases the lock and moves the next queued blocking run to `READY`.
16. Consequences, each in its own transaction: stackconfig's `StackState` projection, the GitHub deployment marked active, notifications, audit, `orchestration/application/triggerpolicy` for trigger policies and dependent stacks (P16), inventory's managed entities (P22), and Flows trigger blocks on `run_updated` (Flows).

### A drift event starts a flow

Scheduler process mode, Deploy side:

1. `internal/shared/queue`'s schedule ticker claims the stack's due drift detection entry; the dequeue gate finds `drift-detection` in the entitlement of the account's organization (with billing off, every feature not switched off is).
2. `internal/orchestration/application/schedules/` fires the `DriftSchedule` aggregate and calls `trigger.go`, which creates a `PROPOSED` drift detection run; `StackQueue.Enqueue` moves it to `READY`.

Launcher and runner, then serve:

3. A private launcher's long-poll claims the run through `Dispatcher.Claim`; `agent/runner` plans and reports a plan with changes through `workerproto/report.go`.
4. `internal/orchestration/application/plan/recorded.go`: plan policies pass; `StackQueue` moves the run to `FINISHED` and, in the same transaction, `shared/outbox.Append` writes `run_updated` and `drift_detected` in the `kernel/event` envelope, enqueues the `events.fanout` job and issues `NOTIFY infraweave_events`.

Scheduler process mode, fan-out:

5. `internal/shared/eventbus` delivers the event to every subscriber, each in its own transaction: `notifications` routes `run_updated` through the notification policy, `sourcecontrol` and `audit` handle it as for any run, and `flows/interfaces/consumers` receives `drift_detected`.
6. `internal/flows/application/trigger/deploy.go`: finds the enabled Deploy trigger block of kind `drift_detected` in the account's flows whose filter matches the stack; `flows-engine` is in the organization's entitlement. In one transaction it inserts the root `flow_events` row, whose document is the `drift_detected` document unchanged and whose envelope links the Deploy event's ID, stores the evaluation's `now` and seed, and enqueues one `flows.deliver` job per linked input.
7. `internal/flows/application/deliver/deliver.go` (the `flows` logical queue): the next block is an app block whose input configuration holds the text template `Drift on ${{ outputs.driftTrigger.stack.name }}`. `kernel/expr` evaluates it with the stored `now` and seed under the cost limit, type-checked at save against `drift_detected`'s schema from `kernel/event`. The block runs on an agent, so the handler enqueues `flows.agent_task` for the pool.

Serve process mode and the Flows agent:

8. `internal/flows/interfaces/agentgateway`: the `serve` replica holding the pool's agent socket claims the job up to the agent's free capacity and pushes a `task` frame carrying the `HandleEvent` request in its JSON encoding, the installation and the app version digest.
9. `internal/flows/agent/executor/docker` (in `infraweave flows-agent`): reuses the container for that installation and digest, or starts one from the image pulled by digest, and the agent calls `HandleEvent` over gRPC on the Unix socket it shares with the container, using `pkg/flows/app/v1`.
10. The app posts its message and returns an emitted event as a buffered effect; a KV call during the handler went live through `flows/agent/host`, relayed as `call` and `reply` frames. The agent sends the `result` frame.
11. `internal/flows/application/deliver/complete.go`: in one transaction, completes the job, inserts the emitted `flow_events` row and its deliveries, and increments the flow's monthly and hourly counters.

Scheduler and serve, consequences:

12. `internal/flows/application/status/`: `flows.invocation.status` recomputes the invocation's status from its deliveries.
13. `internal/flows/interfaces/streams/`: the Flows events stream, woken through `shared/sql/listen`, sends `flows.event` and `invocation_updated` to the console, which updates its cache without polling.

Deploy and Flows read the same event: the trigger block's output is the `drift_detected` document of `kernel/event`, so no anticorruption layer translates it. The cost is visible in step 6: `flows` is conformist to orchestration's published kind, and a field orchestration changes reaches every flow that reads it.

### Adding an IaC vendor: Pulumi (P20)

1. `internal/kernel/iac/pulumi.go`: the `Pulumi` kind and its settings value object (login URL, stack name), read identically by stackconfig, orchestration and governance.
2. `internal/vendors/domain/pulumi/pulumi.go`: the `Vendor` implementation: commands per phase, runner image default, settings validation.
3. `internal/vendors/domain/pulumi/changes.go`: `pulumi preview` JSON to
   `changeset.ChangeSet`.
4. `internal/vendors/domain/pulumi/policy.go`: the `pulumi` block of the plan policy input ([report 20](../reference/20-vendors.md#2-plan-policy-input-per-vendor)).
5. `internal/vendors/domain/pulumi/*_test.go` and `testdata/`.
6. `internal/vendors/api/registry.go`: register the vendor in `For`.
7. `api/src/deploy/components-stacks.yaml`: the Pulumi vendor input and output schemas;
   `make generate`.
8. `internal/stackconfig/interfaces/rest/mapping.go`: generated types to
   `kernel/iac.Pulumi`.
9. `internal/stackconfig/domain/stack/stack.go`: the vendor union gains a case (stacks store vendor settings as one JSON column, so no migration).
10. `internal/governance/api/inputs.go`: `PlanInput` gains the `pulumi` field, filled from the vendor's policy block.
11. `test/acceptance/features/vendor_pulumi.feature` and its steps, with a Pulumi runner image in testcontainers.

What else changes: nothing in orchestration, execution's server side or the agent's
runner, which drive any vendor through `vendors/api`. About 6 new files and 5 edited,
in four contexts plus the kernel; the kernel edit is the cost of a shared vendor
vocabulary.

### Adding a business domain: templates (P21)

1. `api/src/deploy/paths-templates.yaml`: templates, versions, deployments and blueprints, scaffolded from the reference with `make scaffold`; `make generate`.
2. `internal/templates/templates.go`: `New(Deps{UoW, DB, Outbox, Authz, Stacks stackconfigapi.Stacks, Runs orchestrationapi.Runs})`.
3. `internal/templates/api/templates.go` and `events.go`: `TemplateDeploymentChanged`.
4. `internal/templates/domain/template/`: `Template` aggregate and `TemplateVersion` (state, raw body, inputs, published and deprecated) ([report 17](../reference/17-blueprints-and-templates.md#7-templates-and-template-versions)).
5. `internal/templates/domain/deployment/`: `Deployment` aggregate: pinned version, inputs, state, scheduled delete.
6. `internal/templates/domain/blueprint/` and `domain/events.go`.
7. `internal/templates/application/{versions,deploy,update,remove}/`: deploying renders the body's `${{ }}` text templates with `kernel/expr` (decision 0024), then calls `stackconfig/api.Stacks.CreateFromTemplate` and `orchestration/api.Runs.Trigger`.
8. `internal/templates/api/surfaces.go`: the template body surface for the `expressions` editor endpoints.
9. `internal/templates/interfaces/consumers/`: `RunStateChanged` and `StackDeleted` update the deployment's state.
10. `internal/templates/infrastructure/persistence/{postgres,memory,persistencetest}/` with `migrations/0001_templates.sql`.
11. `internal/templates/interfaces/rest/strict.go`: the slice of the deploy strict
    server.
12. `internal/stackconfig/api/stacks.go` and `application/stacks/`: `CreateFromTemplate`, and the stack's nullable deployment reference.
13. `internal/app/contexts.go`, `subscriptions.go`, `strict.go`: construct, subscribe,
    embed; hand the template surface to `expressions`.
14. `internal/features/infrastructure/catalogue.yaml`: the `templates` and `blueprints` capabilities at Business, so every new operation has its feature.
15. `.golangci.yml`: two new rules (`templates-boundary`, `templates-domain`) and `internal/templates/api` added to the seventeen other boundary rules.
16. `test/acceptance/features/templates_deployments.feature` and steps.

About 14 new packages and 36 files; edits outside `internal/templates` touch
`stackconfig` (one supplier operation), `internal/app` (three files), the feature
catalogue, `.golangci.yml` (19 places) and the spec.

## Package mapping

| Today's package | New location | Notes |
|---|---|---|
| `cmd/` (`cmd/README.md` only) | `cmd/infraweave`, `cmd/infraweave-agent`, `cmd/infraweavectl` (P24b) | serve, scheduler, all and migrate call `internal/app`; launcher and runner call `internal/execution/agent`; vcs-agent and flows-agent call their contexts' `agent/` trees; `infraweave-agent` links the three `agent/` trees only |
| `api` (`api/doc.go`, `api/deploy_test.go`) | `api` | unchanged; gains embeds for the worker (P8), operator (P3b) and flows (Flows) specs |
| `api/src/deploy` | `api/src/deploy` | stays flat; each `paths-*.yaml` names its owning context in its header comment |
| `api/reference/spacelift` | `api/reference/spacelift` | unchanged |
| `internal/buildinfo` | `internal/shared/buildinfo` | the Makefile's `LDFLAGS` `-X` paths change with it |
| `internal/docscheck` | `internal/tooling/docscheck` | `make docs-check` runs `./internal/tooling/docscheck` |
| `internal/openapibundle` | `internal/tooling/openapibundle` | |
| `internal/server/gen/deploy` | `internal/shared/gen/deploy` | composed in `internal/app/strict.go` |
| `internal/server/problem` | `internal/shared/httpkit/problem` | domain errors map to it in `httpkit/strictkit` |
| `internal/specref` | `internal/tooling/specref` | |
| `internal/stubgen` | `internal/tooling/stubgen` | |
| `pkg/client/deploy` | `pkg/client/deploy` | unchanged; `pkg/sdk` (P24) wraps it |
| `tools/openapi-bundle` | `tools/openapi-bundle` | imports `internal/tooling/openapibundle` |
| `tools/specref` | `tools/specref` | imports `internal/tooling/specref` |
| `tools/stubgen` | `tools/stubgen` | imports `internal/tooling/stubgen`; the Makefile passes `internal/shared/gen/<spec>/server.gen.go` |
| `test/acceptance` | `test/acceptance` | unchanged runner; feature files prefixed by context |
| `api/oapi-server-deploy.yaml` | `api/oapi-server-deploy.yaml` | `output: internal/shared/gen/deploy/server.gen.go` |
| `Makefile` | `Makefile` | generate, verify-generate, LDFLAGS and docs-check paths follow the moves |
| `.golangci.yml` | `.golangci.yml` | depguard enabled with the rules of [Tests and enforcement](#tests-and-enforcement) |
| `.testcoverage.yml` | `.testcoverage.yml` | `^internal/server/gen/` becomes `^internal/shared/gen/`; `^pkg/flows/app/` joins it |

## Numbers

Estimates, stated as such.

| Measure | Today (P1) | End of P27 |
|---|---|---|
| Go packages | about 22 (kernel 2, shared 6, tooling 4, two REST skeletons, app, cmd, tools 3, api, client, acceptance) | about 375 (18 contexts averaging about 17, shared 28, kernel 12, agents 16, tooling 4, app, cmd 3, tools 3, clients 4, `pkg/sdk`, `pkg/flows` 3, api, acceptance) |
| Top-level directories under `internal/` | 6 (`app`, `kernel`, `shared`, `tooling`, `stackconfig`, `orchestration`) | 22 (18 contexts plus 4) |
| Dependency rules | 9 | 9 |
| depguard rules and entries | 8 rules, about 60 entries (two contexts) | 43 rules, about 520 entries |
| Files touched to add a vendor | n/a | about 6 new, 5 edited, in 4 contexts and the kernel |
| Files touched to add a domain | n/a | about 36 new in 14 packages, 7 edited plus 19 places in `.golangci.yml` |
| Packages a request crosses for a stack update | n/a | 8: `shared/httpkit`, `identity/interfaces/middleware`, `features/interfaces/middleware`, `shared/gen/deploy`, `stackconfig/interfaces/rest`, `stackconfig/application/stacks`, `stackconfig/domain/stack`, `stackconfig/infrastructure/persistence/postgres` (plus `identity/api`, `vendors/api`, `shared/outbox` as calls) |
| Largest package | `internal/shared/gen/deploy`, 24k generated lines | `internal/shared/gen/deploy`, about 240k generated lines at full scope (725 reference operations); hand-written: `orchestration/domain/queue` with the state machine, about 4k lines |
| Bus subscriptions | 0 | about 80, including flows' subscriber on every Deploy kind |
| Thin packages at risk under the 90% floor | none | about 70: every `api/` (18), `domain/events`-only and single-consumer `interfaces/consumers` packages, `kernel/*`, the `expressions` context, skeleton contexts in their first phase |

## Placement choices and friction

Placement choices settled in this file:

- **The stack lock lives with the runs.** `StackQueue` in orchestration owns
  blocking-run serialization, the human lock and the policy lock key, because the lock's
  invariant and the run state machine must commit together. Stack settings stay in
  stackconfig.
- **Dispatch is orchestration's, workers are execution's.** Claiming a run is a
  transition of the run, so `Dispatcher.Claim` is an orchestration operation that
  execution calls; execution then marks the worker busy in its own transaction, a
  departure from decision 0008's single claim transaction.
- **Governance is an open host service.** Callers assemble inputs in its published
  language; governance imports none of them. Receipts are written in governance's
  transaction, before the caller's.
- **Every provider is an anticorruption layer** inside source control; provider payloads
  never leave `sourcecontrol/infrastructure/<provider>`.
- **Vendor settings value objects live in `kernel/iac`, not `kernel/vendor`.** The Go
  tool treats any path element named `vendor` as a vendored dependency: `go list ./...`
  skips such directories, and in module mode importing one fails with "must be imported
  as <name>". The context `internal/vendors` is a legal name and keeps it.
- **Vendors are a generic subdomain** with behaviour and no tables; their settings
  vocabulary is in the shared kernel because three contexts read it.
- **The worker protocol is a published language**, so its client is public in
  `pkg/client/worker` and the agent side is fenced by rule 7.
- **Flows has its own spec, tables and projects**, reads Deploy events through the
  shared kernel's envelope, and calls Deploy through an outbound anticorruption layer.
- **Persistence is per context**, one migration stream each, no foreign keys across
  contexts; identifiers cross as kernel IDs. Decision 0005 describes one gap-free
  stream in `internal/store/migrations`, so this option needs that decision to allow
  per-context streams.
- **The sealed formats sit in the kernel.** Decision 0013 has the runner seal
  workspaces and the launcher open specs with the same code the server uses, and rule 7
  keeps the agent away from `shared/crypt`, so the object and pool envelope formats are
  `kernel/sealfmt`; the keyring and KEK providers stay in `shared/crypt`.
- **The expression language and the event envelope sit in the kernel too.** The runner
  renders workflow command templates, so the language must be reachable under rule 7,
  and decisions 0015 and 0018 require one event model, so the envelope cannot belong to
  any producer. `kernel/expr` is the one kernel package with a third-party dependency.
- **Features and tiers are a context**, not part of identity: an open host service
  every context reads synchronously, with its own aggregates for tiers, grants, the
  organization's entitlement and account limits.
- **The expression editor is a generic subdomain**, `expressions`, conformist to the
  contexts that own each surface, because one REST operation spans all of them.
- **Clients sit outside `internal/`**: `pkg/sdk`, `cmd/infraweavectl` and `web/console`
  reach infraweave through the public API, and each context's REST slice computes the
  `allowedActions` of its own resources.

Where this layout rubs against the system:

- **Early phases pay for structure they do not use.** P2 to P6 are CRUD over stacks,
  contexts, users and spaces. Each family still needs an aggregate with a factory, a
  repository interface, a memory and a PostgreSQL implementation with a contract suite,
  a command handler, a REST mapping and an event appended to an outbox that no
  subscriber reads until P12 or P18. A stack field added in P5 touches five packages.
- **The outbox and bus are prerequisites, not features.** Because every cross-context
  consequence is an event, `shared/outbox` and `shared/eventbus` with ordering,
  idempotency, dead letters and replay must exist before the first run (P9), not with
  the webhooks of P18.
- **Consequences become eventually consistent.** A stack's `state` field lags its run by
  one delivery; a GitHub check lags its run; audit lags the change. Acceptance features
  must wait for effects instead of reading them after the response.
- **Some resources span two contexts.** The Stacks REST representation combines
  stackconfig's settings and orchestration's lock and blocker; `stackLock` lives in the
  Stacks tag but changes orchestration's aggregate. Modules duplicate stack-like source
  and attachment shapes in registry.
- **Two aggregates in two transactions for one decision.** Policy receipts commit before
  the run transition that uses them; a failed transition leaves orphan receipts that
  retention removes.
- **`StackQueue` is a hot aggregate.** A stack with many open pull requests loads every
  non-terminal proposed run on each claim and transition, and concurrent events on one
  stack serialize on its row.
- **api cycles need setters.** identity and governance call each other, as do
  stackconfig and execution; `internal/app/contexts.go` breaks both with setters, and
  the pattern recurs as contexts grow.
- **One event model cuts across the context map.** Decisions 0015 and 0018 make Deploy
  and Flows share the envelope and each kind's schema, so the shared kernel grows a
  contract every producer and every flow depends on. Flows is conformist to every Deploy
  producer instead of insulated by an anticorruption layer, and extracting Flows would
  take the kernel with it.
- **Limits sit across two contexts.** The count is the caller's and the limit is
  features'; read synchronously before the caller's transaction, a create racing a
  downgrade can commit against the old limit, which decision 0021 tolerates by never
  removing what exists.
- **Cross-cutting gates are not a context's concern.** The capability middleware and
  the dequeue gates need every operation's and every job's feature; they run from
  features' `interfaces` and from a function `internal/app` binds into `shared`.
- **Stack `allowedActions` lag in lists.** The stack representation spans stackconfig
  and orchestration; a list reads the `StackState` projection, so the console can offer
  an action the server refuses.
- **The depguard burden grows with the context list.** 43 rules and about 520 entries at
  P27; every new context edits every other boundary rule. depguard reports only new
  issues against the merge base in this repository's configuration, so the rules must
  start from a clean whole-tree run.
- **Uniform layering creates thin packages.** Every context has `api/`, four layers and
  a consumers package; about 70 small packages sit under the 90% per-package floor's
  scrutiny with little logic to test.
- **Boundaries for intelligence, inventory and templates are drawn before their
  phases.** The context map commits to their existence and their `api/` seams; their
  internals are indicative.
