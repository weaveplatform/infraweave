# Option 5: Modular monolith, detailed layout

Detailed repository layout for option 5 of the
[architecture options](README.md): business domains under
`internal/<domain>/`, each owning its model, its tables, its HTTP surface and
a published `api/` package, with ports and adapters inside the larger
domains, one binary with several process modes and one database. Every package in
today's tree is mapped; roadmap packages are marked with the phase of
[report 26](../reference/26-scope-and-tiers.md#14-provisional-phase-spine)
that creates them.

Conventions in the trees:

- `← internal/server/problem` names where the code lives today.
- `(P7)` marks something a later phase creates.
- `(Flows)` marks the Flows phases, which directly follow the events phase
  (P18).
- A line with neither is new structure for code that exists today or for
  P1, the phase in progress.

## Summary

The tree is organised by business domain first. Each domain is a directory
under `internal/<domain>/` with the same four parts: a published `api/`
package (interfaces, input and output types, events), an `assembly/` package
holding the constructor and the domain's contributions to each process mode,
an `internal/` directory with everything else, and one migration stream.
Domains talk to each other only through `api/` packages: synchronously for
queries, authorisation and policy decisions, and asynchronously through
events appended to a transactional outbox in the producer's own transaction.
The Go compiler enforces most of the boundary, because a domain's
`internal/<domain>/internal/...` packages are importable only from inside
`internal/<domain>/`.

Domain boundaries are drawn by consistency. A run transition changes the run
row, its transition record, the stack's run lock and run state, the queue item
that moves it on, the policy receipts that justified it and the event other
domains hear, and all of that commits or none of it does. So the `runs`
domain owns every one of those tables: the run lock and the stack's
`StackState` mirror live in `runs`, not in `stacks`, and `stacks` reads them
through `runs/api`. The large domains (`runs`, `vcs`, `workers`, `access`,
`registry`, `events`, `intelligence`, `flows`) are hexagons inside, with
`internal/core/` and `internal/adapters/`; the small ones are flat.

Shared infrastructure with no business rules lives in `internal/platform/`:
PostgreSQL access, the context-carried unit of work, the outbox, decision
0011's job queue and cron ticker, object storage with its four drivers,
FIPS-approved sealing and the keyring built on it, the expression language,
the HTTP kit with problem details and server-sent-event streams, the
generated servers, and the embedded OPA runtime. Value types used across
domain APIs, such as IDs, spaces, principals, feature names and the run
state vocabulary, live in `internal/kernel/`. Pure vendor knowledge
(settings, workflow keys, plan parsing) that both the server and the worker
need lives in `internal/iac/`. Everything that runs on a worker or agent
host (the launcher, the runner, the VCS agent and the Flows agent) lives in
`internal/agent/`, which links no domain and no server-side persistence.

Deploy and Flows share one event model from the queue phase onward: a
Flows trigger block is one more consumer of the outbox's fan-out, so a run
state change or a drift result reaches a flow without a bridge. The
`features` domain sits low in the graph, so every domain that creates a
counted thing can ask it for a limit, and the console, `infraweavectl` and
the provider reach all of it only through the public API.

Dependency rules:

1. Nothing outside `internal/<domain>/` imports
   `internal/<domain>/internal/...`. The Go compiler enforces this; depguard
   does not need to.
2. A package under `internal/<domain>/` imports only `internal/platform/...`,
   `internal/kernel/...`, `internal/iac/...`, its own domain, the `api/`
   packages of the domains its entry in the declared graph lists, and
   third-party libraries. The graph is one depguard rule per domain and is
   acyclic; back edges are bound in `internal/app`, not imported.
3. An `internal/<domain>/api/` package imports only `internal/kernel/...`,
   `internal/platform/outbox` (for the event envelope) and the standard
   library, so `api/` packages can never form a cycle.
4. Only `internal/app/...` and `cmd/...` import `internal/<domain>/assembly`,
   the constructor packages.
5. Inside a domain, `internal/<domain>/internal/core/...` never imports
   `internal/<domain>/internal/adapters/...`.
6. `internal/platform/...`, `internal/kernel/...` and `internal/iac/...`
   import no domain, no `internal/agent/...` and no `internal/app/...`.
7. `internal/agent/...` imports only `internal/kernel/...`,
   `internal/iac/...`,
   `internal/platform/{buildinfo,observe,rego,httpkit/client,httpkit/serve,seal,expr}`,
   `pkg/flows/app/v1` and its own tree. It never imports a domain (not even an `api/` package),
   `internal/platform/{sql,tx,outbox,audit,queue,objectstore,crypt}` or a
   database driver.
8. `internal/devtools/...` is imported only by `tools/...` and its own tests.
9. `pkg/...` imports nothing under `internal/`, so other repositories can
   depend on the SDK and the app SDK without server code (decision 0001).

Rules 2 to 9 are depguard rules in `.golangci.yml`; rule 1 is the compiler's
`internal` rule. The section
[Tests and enforcement](#tests-and-enforcement) lists every depguard entry.

The schema follows the same graph. A domain's tables may reference the
tables of a domain whose `api/` package it imports, and no others, so `runs`
may reference `stacks` but `stacks` never references `runs`. Every
cross-domain foreign key is `ON DELETE RESTRICT`: deleting a row never
removes or rewrites another domain's rows behind its code, and the
referencing domain removes its own rows first, through the owner's `api/`
or in response to the owner's event. Migration streams run in dependency
order, each after the streams of every domain it imports, so a referenced
table exists before the reference. `TestAccSchemaDependencies` reads the
per-domain depguard rules and fails on a key against the graph, a cascading
key, a cycle or a stream registered too early (decision 0004).

## Repository root

```text
infraweave/
├── cmd/
│   ├── infraweave/             one binary: serve, scheduler, all, launcher, hidden runner,
│   │                           vcs-agent, flows-agent, migrate up and status, auth, secrets, version
│   ├── infraweave-agent/       slim build for worker and agent hosts: launcher, runner, vcs-agent and
│   │                           flows-agent, linking internal/agent only
│   └── infraweavectl/          (P24b) the CLI over pkg/sdk; includes `operator` and `mcp`
├── internal/                   app/ kernel/ platform/ iac/ agent/ devtools/ and one directory per domain
├── pkg/
│   ├── sdk/                    (P24) the SDK: token exchange, Idempotency-Key, 429 retries, iterators,
│   │                           typed problem errors and stream iterators over the generated clients
│   ├── client/
│   │   ├── api/                generated client of the served API, Deploy and Flows
│   │   └── operator/           (P3b) generated operator client
│   └── flows/
│       ├── app/v1/             (Flows) generated App and Host gRPC stubs, used by apps and by the agent
│       └── appsdk/             (Flows) the Go app SDK: Serve, typed blocks, KV, timers, widgets; apptest/
├── api/                        the served document's files, named `api`; oapi-server-*.yaml outputs move to internal/platform/gen
│   ├── src/api/                the served REST API fragments: Deploy, and Flows under /api/v1/flows/ (Flows)
│   ├── src/worker/             (P8) the worker protocol fragments
│   ├── src/operator/           (P3b) the operator API: organizations, tiers, organization tiers, grants, billing
│   ├── proto/flows/app/v1/     (Flows) the `App` and `Host` gRPC services, package infraweave.flows.app.v1
│   ├── reference/spacelift/    unchanged
│   └── reference/spacelift-flows/  (Flows) the Flows reference: provider and flowctl RPC paths projected to REST
├── web/
│   └── console/                (P24c) the console: React 19, TypeScript strict, Vite; orval client and
│                               TanStack Query hooks generated from the served bundle and committed
├── test/
│   ├── acceptance/             godog features against real PostgreSQL, object storage and a real run
│   └── fixtures/               (P9) Terraform and OpenTofu projects, Git repositories, policy corpus
├── tools/                      unchanged: thin mains over internal/devtools
├── deploy/                     (P27) decision 0020's images, charts, Compose file and Terraform modules
│   ├── images/                 server and runner (P9, P20) image definitions
│   ├── compose/                compose.yaml: one infraweave all and one launcher
│   ├── helm/                   infraweave/ (serve, scheduler) and infraweave-workers/ (launcher)
│   ├── terraform/              modules/<cloud>/ for aws, azure and google, and examples/
│   └── check.sh                static checks of every deployment root
├── docs/                       research, decisions, the implementation plan
├── Makefile                    generate writes to internal/platform/gen, internal/agent/wire,
│                               pkg/client, the App stubs and the console's client
├── .golangci.yml               depguard enabled with the rules in "Tests and enforcement"
├── .testcoverage.yml           exclusions follow the generated paths
└── go.mod                      one Go module
```

What changes at the root and why:

- `cmd/` gains its binaries. `cmd/infraweave` holds one cobra file per
  process mode and hands off to `internal/app` (serve, scheduler, all, migrate) or
  to `internal/agent` (launcher, runner, vcs-agent, flows-agent). Report 24 shows Spacelift running
  its server, drain and scheduler as subcommands of one binary
  ([24, section 2.1](../reference/24-self-hosted-reference.md#21-process-inventory)),
  and this layout produces the same shape. `cmd/infraweave-agent` builds
  the same agent process modes from `internal/agent` alone, for hosts whose
  image size or review rules out server code (decision 0004).
  `cmd/infraweavectl` is a client, not a process mode, and reaches the API
  only through `pkg/sdk`.
- `pkg/` holds what other repositories import: the generated clients, the
  SDK over them, and the Flows app SDK. The Terraform provider lives in its
  own repository, `weaveplatform/terraform-provider-infraweave`, and imports
  `pkg/sdk` at a pinned version. Every domain, the platform and the agent
  are internal, because publishing them would freeze domain internals as a
  public API. The worker protocol client is internal too (see
  [Worker protocol and pools](#worker-protocol-and-pools)).
- `internal/` gains one directory per domain. The set grows phase by phase,
  and a domain directory appears with the phase that creates it.
- `api/` stays the source of truth for specifications; only the generated
  server output paths move, to `internal/platform/gen/<spec>`. The `App`
  contract is protobuf, because apps outlive releases and `buf breaking`
  holds the contract to protobuf's compatibility rules (decision 0018).
- `web/console/` is a TypeScript project with its own gate. Its build output
  is not committed; Vite writes it into the directory that
  `internal/platform/httpkit/console` embeds (see
  [Console and clients](#console-and-clients)).
- `deploy/` arrives with packaging (P27); runner images arrive earlier,
  with the first run (P9) and the other vendors (P20). Flows apps are OCI
  images their authors build and publish; infraweave ships no image to run
  them in.

## Full package tree

### Domain status by phase

| Domain or tree | Status at P1 | Created or completed by |
| --- | --- | --- |
| `platform`, `kernel`, `devtools`, `app` | partial: buildinfo, httpkit/problem, gen/api, devtools, the serve process mode | P1 to P2; seal and crypt P2; queue P7; expr and httpkit/sse P9; rego P13; observe P18 |
| `features` | absent | P3b catalogue, tiers, grants, billing and the operator API |
| `access` | absent | P3 identity, tokens and the tenancy model, P3b the `Organization` tag and the authorization server, P4 spaces and RBAC, P23 organization identity providers and SCIM |
| `stacks` | routes only (69 Stacks operations answer 501) | P5; stack dependencies P16 |
| `configuration` | absent | P6 |
| `runs` | routes only (3 Runs operations answer 501) | P7 scheduler and queue gates, P9 first run, P10 completeness, P16 dependencies |
| `workers` | absent | P8 protocol and public pool, P14 private pools |
| `agent` (worker-side tree) | absent | P8 launcher, P9 runner, P14 Docker and Kubernetes executors, P19 VCS agent, P20 vendors, Flows agent with the Flows phases |
| `iac` (pure library), `vendors` (catalogue domain) | absent | P9 Terraform and OpenTofu, P20 the rest |
| `state` | absent | P11 |
| `vcs` | absent | P12 GitHub, P19 other providers and VCS agent pools |
| `policies` | absent | P13 |
| `cloud` | absent | P15 |
| `schedules` | absent | P16 |
| `registry` | absent | P17 |
| `events` | absent | envelope, outbox and fan-out P7; sinks and streams P18 |
| `expressions` | absent | P9 check and evaluate; completion with the first editor client |
| `flows` | absent | the Flows phases, directly after P18 |
| `templates` | absent | P21 |
| `resources` | absent | P22 |
| `intelligence` | absent | P25 |
| `web/console`, `pkg/sdk`, `cmd/infraweavectl` | absent | SDK P24, CLI P24b, console P24c |

Every domain has the same skeleton. A flat domain has one logic package and
one store package under its `internal/`; a hexagon splits `core/` from
`adapters/`.

```text
internal/<domain>/
├── api/                       PUBLISHED: interfaces other domains call, input and output types, events
├── assembly/                  New(Deps) (*Assembly, error); Routes(); Consumers(); Cron(); Surfaces(); Close()
└── internal/
    ├── core/                  hexagon only: domain types, services, ports; no adapters import
    │   └── port/              driven ports: store, object storage, clocks, outbound clients
    ├── adapters/              hexagon only
    │   ├── http/              strict-server partial implementation and hand-written chi routes
    │   ├── pg/                PostgreSQL store; migrations/ embedded, one stream per domain
    │   └── <outbound>/        provider clients, protocol gateways
    ├── <domain>/              flat only: logic, ports and handlers in one package
    └── pg/                    flat only: the store
```

### The tree

The complete tree at the end of P27:

```text
internal/
├── app/                                  composition root for serve, scheduler and all
│   ├── config/                           strict YAML, flag > env > file; one Settings field per domain
│   ├── platform.go                       open PostgreSQL, migrate, object storage, keyring, queue, outbox
│   ├── domains.go                        construct domains in dependency order; register gates
│   ├── router.go                         chi router, middleware, strict servers composed by embedding;
│   │                                     the console at / (P24c)
│   ├── discovery.go                      /.well-known/terraform.json from domain contributions (P11, P17)
│   ├── modes.go                          Serve, Scheduler, All: which domain parts each process mode starts
│   └── *_test.go                         lifecycle tests per process mode
├── devtools/                             libraries behind tools/; never linked into cmd/infraweave
│   ├── specref/                                                         ← internal/specref
│   ├── openapibundle/                                                   ← internal/openapibundle
│   ├── stubgen/                                                         ← internal/stubgen
│   └── docscheck/                                                       ← internal/docscheck
│
├── kernel/                               value types shared by domain APIs; parsing only, no I/O
│   ├── ids.go                            ULID, Slug, UUID (Flows)
│   ├── organization.go account.go        OrganizationID, AccountID; root is the installation's node, not a value
│   ├── space.go                          SpaceID; the account's top space, addressed by the account's slug
│   ├── principal.go                      Principal: member, API key, organization key, run, worker, stack,
│   │                                     Flows project, operator; it names one organization and one account
│   ├── feature.go                        Feature: catalogue names as a type (P3b)
│   ├── actions.go                        AllowedActions: the list every returned resource carries
│   ├── labels.go                         Labels, autoattach: matching
│   ├── run.go                            RunState (22 values), RunType, StackState, RunPriority: wire names
│   ├── vendor.go                         VendorKind
│   ├── vcsroutes/                        provider API route tables shared by vcs and agent/vcsagent (P19b)
│   ├── audit.go                          AuditEntry: the entry every domain records
│   └── page.go                           Cursor and Page of decision 0003
│
├── platform/                             infrastructure with no business rules
│   ├── buildinfo/                                                       ← internal/buildinfo
│   ├── sql/                              pgx pool; the migrator, whose advisory lock is the only one (decision 0005)
│   │   ├── migrate/                      embedded streams, schema_migrations keyed by domain
│   │   ├── notify/                       LISTEN and NOTIFY fan-out between replicas (P7)
│   │   └── sqltest/                      testcontainers PostgreSQL, a database per case from a migrated template
│   ├── tx/                               Tx(ctx, fn), AfterCommit; the unit of work on the context
│   ├── audit/                            Record(ctx, kernel.AuditEntry): audit_trail_entries row in the caller's tx
│   │                                     (decision 0015); the table is platform's, as events and jobs are
│   ├── outbox/                           the event envelope and kind registry shared by Deploy and Flows;
│   │                                     Append(ctx, Event): an events row, an events.fanout job and
│   │                                     pg_notify in the caller's tx (decision 0015) (P7)
│   ├── events/                           Subscribe(kind, consumer, Handler); fan-out enqueues one job per consumer (P7)
│   ├── queue/                            decision 0011's PostgreSQL job queue: EnqueueTx, leases fenced by generation,
│   │   │                                 ordering keys, the dead state, workers per logical queue (`flows` among
│   │   │                                 them); a SkipGate bound in app completes a gated feature's job as
│   │   │                                 skipped at dequeue (P7)
│   │   └── queuetest/                    TestAcc contract suite against PostgreSQL
│   ├── cron/                             five-field grammar and the ticker that claims due rows with
│   │                                     FOR UPDATE SKIP LOCKED; no leader (decision 0011) (P7)
│   ├── objectstore/                      Store: Put, Get, Head, Delete, SignURL; blob_objects and blob.expire;
│   │   │                                 keys written once, so no bucket versioning (P2)
│   │   ├── s3/ gcs/ azureblob/ fs/       the four drivers of decision 0005, each on its provider's own SDK
│   │   └── objectstoretest/              one contract suite: put, get, delete, multipart, SHA-256 checksums,
│   │                                     signed URLs, expiry
│   ├── seal/                             decision 0013's sealed formats, each led by a version byte: object
│   │                                     segments (AES-256-GCM, random nonces, index and last flag in AAD) and
│   │                                     the pool envelope (ECDH P-256, HKDF-SHA256, AES-256-GCM) (P2)
│   ├── crypt/                            decision 0013's keyring over seal: data keys, DeriveKey, rotation (P2)
│   │   ├── kek/                          KEK providers: local, awskms, gcpkms, azurekv, vault
│   │   └── signer/                       token signing with a local key or the same KMS providers'
│   │                                     signing keys (decision 0006) (P3)
│   ├── expr/                             decision 0024's language: CEL through cel-go, `${{ }}` text templates,
│   │   │                                 the function library, cost and time limits, compiled-program cache,
│   │   │                                 JSON Schema type provider, fixed now and seed, shell quoting (P9)
│   │   ├── editor/                       check, complete and evaluate over a typed environment
│   │   └── surface.go                    Surface: a name and Env(ctx, principal, ref), contributed by domains
│   ├── rego/                             embedded OPA v1: prepared queries per (policy, version), capabilities,
│   │                                     timeouts, print capture; no knowledge of policy types (P13)
│   ├── httpkit/
│   │   ├── problem/                      RFC 9457 problems, decision 0003 and 0022 slugs ← internal/server/problem
│   │   ├── middleware/                   request ID, recovery, body limits, idempotency keys, rate limits
│   │   ├── page/                         cursor encode and decode for list operations
│   │   ├── sse/                          text/event-stream writer: retry, keepalive comments, Last-Event-ID
│   │   │                                 resume, slow-consumer and shutdown `end` (P9)
│   │   ├── console/                      the console's build embedded through embed.FS, index.html fallback,
│   │   │                                 cache headers, CSP with a per-response style nonce (P24c)
│   │   ├── client/                       outbound HTTP with retries, used by agent and adapters
│   │   ├── serve/                        listener lifecycle and drain for the API and admin listeners, used by
│   │   │                                 every process mode
│   │   ├── dispatch/                     Caller: runs one REST operation by operationId in process, as the
│   │   │                                 calling principal, through the composed router; bound by
│   │   │                                 internal/app for flows and intelligence (P18c, P25)
│   │   └── strict/                       helpers for strict handlers: principal, problem mapping
│   ├── gen/
│   │   ├── api/                          server.gen.go, unimplemented.gen.go, features.gen.go (operation to
│   │   │                                 capability, from x-infraweave-feature)       ← internal/server/gen/deploy
│   │   ├── worker/                       worker protocol strict server (P8)
│   │   └── operator/                     operator API strict server (P3b)
│   ├── observe/                          slog, OpenTelemetry, /metrics, readiness on its own port (P18)
│   └── testkit/                          fake clock, fake object store, HTTP recorder helpers
│
├── iac/                                  pure vendor knowledge shared by server and agent (P9, P20)
│   ├── iac.go                            Vendor: Kind, ValidateSettings, Workflow, ParsePlan, PolicyBlock
│   ├── registry.go                       Kind → Vendor
│   ├── terraform/ opentofu/              (P9) settings, workflow.yml keys, plan JSON → changes, version rules
│   ├── terragrunt/ pulumi/ cloudformation/ kubernetes/ ansible/   (P20)
│   └── iactest/                          contract suite every vendor passes
│
├── agent/                                worker and agent host tree; links no domain and no server persistence (P8)
│   ├── cli/                              cobra commands of the four agent process modes, shared by
│   │                                     cmd/infraweave and cmd/infraweave-agent
│   ├── config/                           launcher settings: pool token, comms URL, executor, mounts
│   ├── launcher/                         register, long-poll for jobs and signals, heartbeat, drain; opens the
│   │                                     spec's pool envelope through platform/seal
│   ├── wire/                             generated worker protocol client (P8)
│   ├── executor/
│   │   ├── docker/                       one run container per job, own binary mounted as root process (P8)
│   │   └── kubernetes/                   controller for WorkerPool and Worker resources, pod per run (P14)
│   ├── exec/                             `runner`: phases, hooks, workspace sealed through platform/seal, log
│   │                                     streaming; renders .infraweave/workflow.yml through platform/expr (P9)
│   ├── vendors/                          command sets per vendor; implements iac.Workflow on the host
│   │   ├── terraform/ opentofu/          (P9)
│   │   └── terragrunt/ pulumi/ cloudformation/ kubernetes/ ansible/   (P20)
│   ├── initpolicy/                       worker-side initialization policy over platform/rego (P14)
│   ├── mask/                             log masking of decision 0013: literal values, ::add-mask, patterns (P9)
│   ├── tools/                            binary download, verification and caching per version (P9)
│   ├── cloudauth/                        assumes a cloud integration's role on the worker with the run's OIDC
│   │                                     token, when the integration says so (P15)
│   ├── vcsagent/                         `vcs-agent`: long-polls /vcs-agent/v1 on serve (decision 0012) (P19)
│   ├── flowsagent/                       `flows-agent` (decision 0018) (Flows)
│   │   ├── conn/                         dials /flows/agent/v1/connect on serve; hello, task, call, reply,
│   │   │                                 result and ping frames
│   │   ├── apps/                         one container per installation and app version digest; digest check,
│   │   │                                 reuse, 15-minute idle recycle; gRPC App client over pkg/flows/app/v1
│   │   ├── host/                         the Host service the agent serves to its apps; relays KV, locks,
│   │   │                                 ListBlocks and SetWidget to the server
│   │   └── executor/{docker,kubernetes,process}/   sandboxed sibling container (optional gVisor), pod with
│   │                                     mutual TLS and a NetworkPolicy, or a local process for development
│   └── agenttest/                        fake server over the generated worker spec; fake Flows gateway
│
├── features/                             flat: the feature catalogue, tiers, organization tiers, grants, pooled
│   │                                     limits and account limits, billing (P3b)
│   ├── api/                              Entitlement.For(organization) → Has(feature), Limit(name); Limits.Check(ctx,
│   │                                     account, limit, count) against the pool and the account limit;
│   │                                     Gate(feature) for the queue and cron;
│   │                                     events: EntitlementChanged
│   ├── assembly/                         Middleware() for app/router.go; operator API partial; Consumers()
│   └── internal/
│       ├── catalogue/                    catalogue.gen.go, generated: capabilities, options, limits, requirements,
│       │                                 seeded tiers; a test fails on an operation or gated field without one
│       ├── features/                     entitlement: tier plus grants, minus features.disabled, minus unmet
│       │                                 requirements; per-organization snapshot rebuilt on infraweave_invalidate
│       ├── middleware/                   chi middleware after authentication: capability of the route, then
│       │                                 feature-disabled or feature-not-in-tier
│       ├── http/                         operator partial: tiers, organization tiers, grants, billing; the
│       │                                 Organization tag's account limits and usage
│       └── pg/                           tiers, tier_features, tier_limits, organization_grants, account_limits,
│                                         installation_billing
│
├── access/                               organizations, accounts, members, groups, API keys, sessions, tokens,
│                                         the OAuth authorization server, spaces, RBAC
│   ├── api/
│   │   ├── authn.go                      Authenticator → kernel.Principal, operator principals included
│   │   ├── authz.go                      Authorizer.For(ctx, Principal) → Decider: Can, ReachableSpaces, Allowed
│   │   ├── spaces.go                     Spaces: Get, Ancestors, Subtree, Inherits
│   │   ├── tokens.go                     Tokens: IssueRunToken, IssueWorkerToken, Introspect, JWKS
│   │   ├── identity.go                   Organizations, Accounts, Members, Groups, APIKeys (reads)
│   │   └── events.go                     UserCreated, APIKeyRevoked, SpaceChanged, RoleBindingChanged
│   ├── assembly/
│   └── internal/
│       ├── core/
│       │   ├── organization/ account/ member/ apikey/ session/ token/   (P3)
│       │   ├── user/ group/ oauth/       (P3b) seats checked through featuresapi.Limits
│       │   ├── space/ role/ binding/ authz/                     (P4) action catalogue, inheritance walks
│       │   ├── login/                    entering an account: its LOGIN policies through policies/api (P13b)
│       │   ├── sso/ scim/ mfa/ linking/  the organization's identity providers (P23)
│       │   └── port/
│       └── adapters/
│           ├── http/                     strict partial: users, API keys, spaces, roles, bindings, the
│           │                             Organization tag; operator partial: organizations; authn middleware;
│           │                             OAuth metadata, registration, authorization and token; login.v1 (P17)
│           ├── pg/                       migrations: organizations (tier references features' tiers, RESTRICT),
│           │                             accounts, members, member_identities, spaces, roles, role_actions,
│           │                             api_keys, organization_keys, role_bindings, sessions, oauth_clients,
│           │                             identity_providers, scim_clients
│           └── saml/ oidc/               (P23)
│
├── stacks/                               flat: stacks, settings, hooks, labels, human lock, enable and disable
│   ├── api/                              Stacks: Get, ByRepository, LockForRun, Settings; events: StackChanged
│   ├── assembly/
│   └── internal/
│       ├── stacks/                       service, validation through iac.ValidateSettings, hooks, reslug
│       ├── dependency/                   stack dependency DAG, references, cycle rejection (P16)
│       ├── http/                         strict partial for the Stacks tag
│       └── pg/                           stacks, stack_hooks, stack_dependencies, references
│
├── configuration/                        flat: config elements, contexts, attachments, auto-attach (P6)
│   ├── api/                              Environment.Resolve(target) → ordered elements and hooks
│   ├── assembly/
│   └── internal/{configuration,http,pg}/ config_elements, contexts, context_attachments
│
├── runs/                                 the run engine; owns every table a transition touches
│   ├── api/
│   │   ├── runs.go                       Runs: Create, Get, ListByStack, Act (confirm, discard, retry, stop)
│   │   ├── scheduler.go                  Scheduler: Claim(pool, worker) → Assignment (P7, P8)
│   │   ├── report.go                     Reports: Phase, Logs, Plan, Outputs from workers (P9)
│   │   ├── stackstatus.go                StackStatus: State, Blocker, IsLocked for the Stacks API
│   │   ├── access.go                     StateAccess(run, stack) → read, write, lock (P11)
│   │   └── events.go                     RunCreated, RunStateChanged, RunFinished, PlanReady
│   ├── assembly/
│   └── internal/
│       ├── core/
│       │   ├── machine/                  T1 to T44: report 08's 43 transitions and decision 0008's T44; guards
│       │   ├── lifecycle/                Create, Transition, Act: one tx per transition
│       │   ├── gates/                    policy evaluation at each state point through policies/api
│       │   ├── queuegate/                stack lock, approval hold, upstream and external dependencies
│       │   ├── schedule/                 pool queue order: 3 presets, 7 tiers, FIFO in a tier (P7)
│       │   ├── handover/                 job payload: settings, environment, credentials, URLs (P8)
│       │   ├── timers/                   2, 7 and 30 day rules; notification rules (P10)
│       │   ├── promote/ replan/ forceapply/   (P10)
│       │   ├── dependencies/             dependent runs, skip on unchanged outputs (P16)
│       │   └── port/                     store, objectstore, clock
│       └── adapters/
│           ├── http/                     strict partial for the Runs tag; run log, run state and stack run
│           │                             streams over platform/httpkit/sse (P9)
│           └── pg/                       runs, run_state_transitions, run_reviews, run_comments,
│                                         run_policy_receipts, run_flags, run_dependencies,
│                                         run_external_dependencies, run_assignments, stack_run_locks,
│                                         stack_run_state, stack_outputs
│
├── workers/                              pools, workers, credentials, the worker protocol endpoints (P8, P14)
│   ├── api/                              Pools: Get, ForStack; Workers: Busy, Drain; events: WorkerChanged
│   ├── assembly/
│   └── internal/
│       ├── core/{pool,worker,credential,drain,waiter,port}/
│       └── adapters/
│           ├── http/                     strict partial: pools, workers (api spec); pool queue streams
│           ├── protocol/                 worker spec server: register, jobs long-poll, signals, heartbeat
│           └── pg/                       worker_pools, workers, public_pool; private-workers checked through
│                                         featuresapi.Limits
│
├── vendors/                              flat: version catalogue for Terraform, OpenTofu, kubectl (P9, P20)
│   └── api/ assembly/ internal/{vendors,http,pg}/
├── state/                                Terraform http backend, versions, locks, import, rollback (P11)
│   ├── api/                              Versions: Latest, Outputs; events: StateVersionCreated
│   ├── assembly/
│   └── internal/{state,backend,tfe,http,pg}/
├── vcs/                                  integrations, webhooks, routing, feedback (P12, P19)
│   ├── api/                              Integrations, Sources (archive URL, checkout spec), Feedback, PullRequests
│   ├── assembly/
│   └── internal/
│       ├── core/{integration,intake,normalize,route,feedback,pullrequest,port}/
│       └── adapters/
│           ├── http/                     strict partial; webhook receivers (hand-written)
│           ├── github/                   (P12) App auth, signatures, checks, comments, deployments
│           ├── gitlab/ bitbucketcloud/ bitbucketdc/ azuredevops/ rawgit/   (P19)
│           ├── agentgateway/             (P19) long-poll endpoints for VCS agents (decision 0012)
│           └── pg/                       vcs_integrations, vcs_deliveries, pull_requests, vcs_agent_pools
├── policies/                             policy objects, attachments, evaluation, samples, simulation (P13)
│   ├── api/                              Evaluator and the input contract per type; events: PolicyChanged
│   ├── assembly/
│   └── internal/{policies,inputs,combine,sample,http,pg}/
├── cloud/                                AWS, Azure, GCP integrations, attachments, OIDC run tokens (P15)
│   └── api/ assembly/ internal/{cloud,oidc,aws,azure,gcp,http,pg}/
├── schedules/                            scheduled runs, tasks, deletes, drift detection (P16)
│   └── api/ assembly/ internal/{schedules,http,pg}/
├── registry/                             modules, versions, test cases, providers, GPG keys (P17)
│   ├── api/                              Modules: ByRepository, CreateVersion; Providers; events
│   ├── assembly/
│   └── internal/
│       ├── core/{module,version,testcase,provider,gpg,sharing,port}/
│       └── adapters/{http,protocol,pg}/
├── events/                               sinks of the shared event model: inbox, named and stack webhooks, audit
│   │                                     trail, Slack, Teams (P18)
│   ├── api/                              Router (notification policy sink choice), Inbox; audit query
│   ├── assembly/                         Surfaces(): notification text
│   └── internal/
│       ├── core/{catalogue,inbox,webhook,delivery,audit,metrics,port}/   delivery renders text templates
│       └── adapters/{http,slack,pg}/
├── expressions/                          flat: the editor endpoints of decision 0024; owns no tables (P9)
│   ├── assembly/                         New(Deps{Surfaces}): every domain's surfaces, collected by internal/app;
│   │                                     no api/ package, because no domain calls it
│   └── internal/
│       ├── expressions/                  look up a surface by name, authorize through it, run platform/expr/editor
│       └── http/                         strict partial for /expressions/check, complete and evaluate
├── templates/                            blueprints, templates, versions, deployments (P21)
│   └── api/ assembly/ internal/{templates,http,pg}/   bodies rendered through platform/expr
├── resources/                            managed entities, scans, generic search (P22)
│   └── api/ assembly/ internal/{resources,search,http,pg}/
├── intelligence/                         AI integrations, Infra Assistant, Intent, MCP (P25)
│   ├── api/ assembly/
│   └── internal/
│       ├── core/{aiintegration,assistant,intent,tools,port}/
│       └── adapters/{http,mcp,providers,pg}/
└── flows/                                the Flows engine: the account's projects, flows, events, apps (Flows)
    ├── api/ assembly/
    └── internal/
        ├── core/{org,project,flow,block,delivery,invocation,timer,app,agentpool,secret,named,datatable,
        │        form,deploy,widget,port}/
        └── adapters/{http,trigger,gateway,registry,mcp,endpoint,pg}/

pkg/
├── sdk/                                  (P24) wraps the generated clients below
├── client/
│   ├── api/                                                             ← pkg/client/deploy
│   └── operator/                         (P3b)
└── flows/{app/v1,appsdk}/                (Flows)
```

## Deep dives

### Stack configuration write path

```text
internal/stacks/
├── api/stacks.go          Stacks: Get, Search, ByRepository(integration, repo, branch), LockForRun
├── api/events.go          StackCreated, StackUpdated, StackDeleted, StackLocked
├── assembly/assembly.go   New(Deps{Tx, SQL, Outbox, Audit, Authz accessapi.Authorizer, Spaces accessapi.Spaces,
│                          Status runsapi.StackStatus, Pools workersapi.Pools, Vendors iac.Registry})
└── internal/
    ├── stacks/
    │   ├── service.go     Create, Update, Delete, Enable, Disable, Lock, Unlock, Reslug
    │   ├── settings.go    the settings model of report 07; vendor union validated by internal/iac
    │   ├── hooks.go       before and after lists per phase
    │   ├── labels.go      label rules, autoattach: labels kept as data
    │   └── *_test.go      service tests with fake api dependencies
    ├── http/
    │   ├── strict.go      API: the 69 Stacks operations, embedded in app/router.go
    │   └── convert.go     generated request types ↔ settings model
    └── pg/
        ├── store.go       stacks, stack_hooks
        └── migrations/    0001_stacks.sql 0002_stack_hooks.sql (P5); 0003_stack_dependencies.sql (P16)
internal/configuration/internal/configuration/   attach, detach, auto-attach on label change (P6)
```

A `PATCH /stacks/{id}` passes through `platform/httpkit/middleware`
(idempotency key, rate limit), the access domain's authn middleware, then
`stacks/internal/http/strict.go`, which converts the generated request type
and calls the service. The service checks `stack:update` (the action name is
decision 0007's) on the stack's space through `accessapi.Decider`, validates
the vendor block through `internal/iac`, and writes in one transaction
with an audit entry (decision 0015's `audit.Record`) and a `StackUpdated` event in the outbox:

```go
// Update applies a stack update. Labels can change which contexts and
// policies auto-attach, and those domains learn of it from StackUpdated in
// the same transaction, so a rolled-back update never re-attaches anything.
func (s *Service) Update(ctx context.Context, p kernel.Principal, id kernel.Slug, in Input) (Stack, error) {
	return tx.Do(ctx, s.tx, func(ctx context.Context) (Stack, error) {
		cur, err := s.store.GetForUpdate(ctx, id)
		if err != nil {
			return Stack{}, err
		}
		dec, err := s.authz.For(ctx, p)
		if err != nil {
			return Stack{}, err
		}
		if !dec.Can(accessapi.ActionStackUpdate, cur.Space) {
			return Stack{}, accessapi.ErrForbidden{Action: accessapi.ActionStackUpdate}
		}
		next, err := cur.Apply(in, s.vendors) // iac.ValidateSettings for the vendor block
		if err != nil {
			return Stack{}, err
		}
		if err := s.store.Put(ctx, next); err != nil {
			return Stack{}, err
		}
		if err := s.audit.Record(ctx, kernel.Audited(p, "STACK", "UPDATE", next.ID)); err != nil {
			return Stack{}, err
		}
		return next, s.outbox.Append(ctx, stacksapi.StackUpdated{ID: next.ID, Space: next.Space, Labels: next.Labels})
	})
}
```

A stack's response carries `state`, `blocked`, `blocker` and `lockedBy`. The
first three are runs-owned and come from `runsapi.StackStatus`; only
`lockedBy` (the human lock) is a stacks column. A list filtered by state
joins across two domains, which `runsapi.StackStatus` answers from
`runs`' stack-by-run-state view (see the friction list). Configuration elements and contexts are a separate domain
because they attach to stacks and contexts alike, through polymorphic
owners ([28, section 12.1](../reference/28-data-model.md#121-shapes-that-recur));
a registry module's elements and attachments are `registry`'s rows, since
`registry` imports `configuration`, `policies` and `cloud`.
`configuration` subscribes to `StackUpdated` to recompute auto-attachments,
and `runs` calls `configurationapi.Environment.Resolve` at handover to get
the precedence order of [report 13](../reference/13-configuration.md#5-contexts).

### Run execution path

```text
internal/runs/internal/core/
├── machine/
│   ├── machine.go         State, Event, Transition table: report 08's 43 rows and decision 0008's T44
│   ├── guards.go          canConfirm, canDiscard, canRetry, canStop and the other guard fields
│   └── machine_test.go    table test over every row and every illegal pair
├── lifecycle/
│   ├── create.go          Create: dedupe (stack, sha, type), external dependencies, initial QUEUED
│   ├── transition.go      Transition: one transaction per state change (below)
│   ├── act.go             Confirm, Discard, Retry, Stop, Cancel, Review, Comment
│   └── receipts.go        policy receipts written with the transition they justify
├── queuegate/
│   ├── gate.go            QUEUED → READY when the stack lock, approval and dependencies allow
│   └── lock.go            blocking runs (tracked, task, destroy) take stack_run_locks
├── schedule/
│   ├── order.go           tiers 1 to 7, oldest first; private pools only for presets
│   └── claim.go           Claim: SELECT ... FOR UPDATE SKIP LOCKED, assignment, READY → PREPARING
├── handover/
│   ├── payload.go         settings snapshot, environment, hooks, cloud credentials, run token
│   └── urls.go            presigned workspace, plan and log URLs from platform/objectstore
├── timers/                cron jobs registered for the scheduler process mode (P10)
└── gates/                 described under "Policy evaluation with embedded OPA"
```

The path from trigger to worker:

1. **Trigger.** A VCS consumer, a user (`POST /stacks/{id}/runs`), a
   schedule, a trigger policy or a dependency calls `runsapi.Runs.Create`.
   `lifecycle/create.go` takes `stacksapi.Stacks.LockForRun`, a
   `SELECT ... FOR SHARE` on the stack row inside the same transaction, so a
   human lock or a disable that commits first is seen
   ([07, section 5](../reference/07-stack-model.md#5-lock-and-unlock-enable-and-disable)).
   It inserts the run in `QUEUED`, enqueues `run.schedule` and appends
   `RunCreated`.
2. **Queue gates.** The scheduler process mode consumes `run.schedule`.
   `queuegate/gate.go` evaluates approval policies on entering `QUEUED`,
   checks the stack lock for blocking runs, upstream runs and external
   dependencies
   ([08, section 4](../reference/08-run-lifecycle.md#4-queueing-the-stack-lock-and-scheduling)),
   and moves the run to `READY` when all pass. A blocked run stays queued and
   is re-gated by the event that unblocks it (`RunFinished` of the blocker,
   a review, an external dependency completion).
3. **Scheduler.** A run in `READY` waits for a worker in its pool. There is
   no push: the worker's long-poll calls `runsapi.Scheduler.Claim`, which
   picks the first run in the pool's order
   ([08, priority](../reference/08-run-lifecycle.md#priority)) with
   `FOR UPDATE SKIP LOCKED`, inserts a `run_assignments` row and moves the
   run to `PREPARING` in one transaction. The worker's busy flag is derived
   from the assignment row, so it cannot disagree with the run.
4. **Worker.** The worker reports `INITIALIZING`, `PLANNING` and later
   states through `runsapi.Reports.Phase`; each report is a transition with
   a state-version check, so a stale or duplicated report is rejected.
5. **State transitions.** Every transition, from any source, goes through
   one function:

```go
// Transition moves a run from one state to the next. The policy decision
// that justifies it is taken before the transaction, because an evaluation
// may run for seconds and must not hold row locks; the state version check
// rejects the transition if anything moved the run meanwhile, and the caller
// re-evaluates.
func (l *Lifecycle) Transition(ctx context.Context, t machine.Request, d gates.Decision) (Run, error) {
	return tx.Do(ctx, l.tx, func(ctx context.Context) (Run, error) {
		run, err := l.store.GetForUpdate(ctx, t.Run)
		if err != nil {
			return Run{}, err
		}
		if run.StateVersion != t.StateVersion {
			return Run{}, ErrStale
		}
		next, err := l.machine.Apply(run, t.Event, d)
		if err != nil {
			return Run{}, err
		}
		if err := l.store.SaveTransition(ctx, run, next, t.By, t.Note); err != nil { // runs, run_state_transitions
			return Run{}, err
		}
		if err := l.store.SaveReceipts(ctx, next.ID, d.Receipts); err != nil { // run_policy_receipts
			return Run{}, err
		}
		if err := l.locks.Apply(ctx, run, next); err != nil { // stack_run_locks, stack_run_state
			return Run{}, err
		}
		if err := l.queue.EnqueueTx(ctx, tx.From(ctx), next.FollowUp()...); err != nil { // decision 0011
			return Run{}, err
		}
		return next, l.outbox.Append(ctx, runsapi.RunStateChanged{
			Run: next.ID, Stack: next.Stack, From: run.State, To: next.State, Version: next.StateVersion,
		})
	})
}
```

Timers, the 2, 7 and 30 day rules of
[report 08](../reference/08-run-lifecycle.md#timers), are cron jobs in
`timers/` that call `Transition` like any other source. Report 24's
evidence that Spacelift moved cron into the drain
([24, section 2.1](../reference/24-self-hosted-reference.md#21-process-inventory))
is why `runs` registers both its queue consumers and its cron jobs with the
scheduler process mode rather than a separate cron process mode.

### Policy evaluation with embedded OPA

```text
internal/platform/rego/
├── engine.go          Engine: Prepare(ctx, Module{Body, Version}) → Prepared; cache keyed by (policy ID, body hash)
├── capabilities.go    capabilities minus http.send, opa.runtime, rego.parse_module, time.now_ns, trace
├── eval.go            Eval(ctx, Prepared, input) with a context deadline; print output captured
└── engine_test.go
internal/policies/
├── api/
│   ├── evaluator.go   Evaluator: Push, Approval, Plan, Trigger, Notification, Login, Intent, Initialization
│   ├── inputs.go      the input contract per type (report 09 section 4), built by the caller
│   ├── decisions.go   PushDecision, ApprovalDecision, PlanDecision, ... each with []Receipt
│   └── events.go      PolicyChanged, PolicyAttached
├── assembly/
└── internal/
    ├── policies/      CRUD, attachment, auto-attach by label and space inheritance, aliases of 3 legacy types
    ├── inputs/        JSON rendering of the contract; sanitized helper; custom inputs
    ├── combine/       per-type combination: deny wins, all approvals, priorities must agree, lock keys agree
    ├── sample/        sampled evaluations to the policy-inputs bucket, hourly quota, 7-day retention
    ├── http/          strict partial for the Policies tag, simulate
    └── pg/            policies, policy_attachments, policy_evaluations
```

The input contract belongs to `policies` because it is the published
contract of [report 09](../reference/09-policies.md#4-input-documents).
The domains that own a state point build the input and call the evaluator;
`policies` never imports `runs`, `vcs`, `access` or `events`, so no cycle can
form. The state points of
[report 09, section 8](../reference/09-policies.md#8-where-each-type-is-evaluated)
map to callers:

| Policy type | Caller | State point |
| --- | --- | --- |
| `GIT_PUSH` | `vcs/internal/core/route` | per stack and registry module for each event, before any run exists |
| `APPROVAL` | `runs/internal/core/gates` | on entering `QUEUED`, `UNCONFIRMED`, `PENDING_REVIEW`, and on each review |
| `PLAN` | `runs/internal/core/gates` | when the worker reports a successful plan; on each targeted replan |
| `TRIGGER` | `runs/internal/core/gates` | when a tracked run or task reaches one of five terminal states; `registry` on module release |
| `NOTIFICATION` | `events/internal/core/catalogue` | on every outbox event it routes |
| `LOGIN` | `access/internal/core/login` | each login and API key exchange |
| `INITIALIZATION` | `runs` (deprecated server side), `agent/initpolicy` (worker side) | before `INITIALIZING` |
| `INTENT` | `intelligence/internal/core/intent` | each Intent operation |

```go
// internal/runs/internal/core/gates/plan.go
//
// Plan evaluates the plan policies attached to the run's stack. The
// decision is applied by Transition, which writes the receipts in the same
// transaction as the state it justifies; trigger decisions likewise come
// back as data (stacks to trigger), and runs creates those runs itself, so
// policies never calls back into runs.
func (g *Gates) Plan(ctx context.Context, r Run, plan PlanReport) (Decision, error) {
	in, err := g.inputs.Plan(ctx, r, plan) // stack, run, changes from internal/iac, flags so far
	if err != nil {
		return Decision{}, err
	}
	d, err := g.policies.Plan(ctx, policiesapi.Target{Kind: policiesapi.TargetStack, ID: r.Stack}, in)
	if err != nil {
		return Decision{}, err // an error, a timeout included, fails closed as a deny (decision 0009)
	}
	return planOutcome(r, d), nil // FAILED, UNCONFIRMED, PENDING_REVIEW, APPLYING or FINISHED
}
```

Prepared queries are cached per policy body and Rego version, per
[report 27, section 9](../reference/27-reference-materials.md#91-the-go-api);
`platform/rego` knows nothing of policy types, which is why it is platform
and why the agent's worker-side initialization policy can import it.
Sampling writes to `policies`' own table after the decision, outside the
caller's transaction, because a lost sample is acceptable and a slow sample
write must not slow a run.

### Worker protocol and pools

```text
api/src/worker/                       (P8) info.yaml, paths-worker.yaml, components.yaml
internal/platform/gen/worker/         generated strict server
internal/agent/wire/                  generated client (oapi-client-worker.yaml output)
internal/workers/
├── api/pools.go                      Pools: Get, ForStack, Settings (drift limit, preparing timeout)
├── api/workers.go                    Workers: List, Drain, Undrain, Cycle; events: WorkerRegistered, WorkerGone
├── assembly/
└── internal/
    ├── core/
    │   ├── pool/                     public and private pools, credentials (CSR-signed certificate)
    │   ├── worker/                   registration, metadata, status, heartbeats, status decay
    │   ├── drain/                    drain, undrain, cycle; drain-safe scale-in fields
    │   ├── waiter/                   in-process long-poll waiters woken by platform/sql/notify
    │   └── port/
    └── adapters/
        ├── http/                     api-spec partial: WorkerPools tag
        ├── protocol/
        │   ├── register.go           POST /worker/v1/register (JWS over the pool key), deregister
        │   ├── poll.go               POST /worker/v1/poll: heartbeat, held up to the poll timeout;
        │   │                         runsapi.Scheduler.Claim; signals and the drain flag ride on the answer
        │   ├── spec.go               GET /worker/v1/runs/{run}/spec and /workspace
        │   ├── reports.go            POST /worker/v1/runs/{run}/reports: runsapi.Reports
        │   └── uploads.go            upload-urls, log-chunks, artifacts (decision 0008's operations)
        └── pg/
internal/agent/
├── launcher/launcher.go              Run(ctx, cfg): register, then loop: poll, start executor, report
├── executor/docker/                  docker run <runner image> with the binary mounted at /opt/infraweave/bin/infraweave (decision 0008)
├── exec/                             `infraweave runner`: phases from the job, hooks, vendor commands
└── vendors/<kind>/                   command sets for init, plan, show, outputs, apply, destroy
```

The operations are decision 0008's: one `POST /worker/v1/poll` carries the
heartbeat, the assignment, stop and kill signals and the drain flag, so the
launcher holds one request open at a time.

The worker protocol is HTTP long-poll initiated by the worker, the transport
Spacelift keeps while MQTT retires
([10, section 4.2](../reference/10-workers-and-execution.md#42-transports)),
and the one OTF uses ([27, section 3.1](../reference/27-reference-materials.md#31-architecture)).
A long-poll lands on any `serve` replica; the waiter blocks on an in-process
channel that `platform/sql/notify` wakes when a run in the pool becomes
claimable (`READY`, `CONFIRMED` or `REPLAN_REQUESTED`), so a run queued through another replica reaches the waiting worker
within one notification.

The worker protocol client is internal (`internal/agent/wire`), not
`pkg/client/worker`. The only clients of the protocol are infraweave's own
launcher and Kubernetes executor, and a public package would invite
third-party code to depend on a protocol that changes with the launcher. The
wire protocol is still versioned (`/worker/v1`), because customers upgrade
launchers later than servers.

The launcher process mode imports nothing from `workers`: rule 7 keeps the agent tree
to the generated client, `internal/iac` and seven named platform packages. The private
pool's encryption of run state to the pool key
([report 10, sections 1.1](../reference/10-workers-and-execution.md#11-public-and-private-pools-compared)
and [2.3](../reference/10-workers-and-execution.md#23-credentials))
splits the same way: `workers/internal/core/credential` signs the CSR,
`runs/internal/core/handover` seals the spec's secret part to the pool's
P-256 public key, and `agent/launcher` holds the private key and opens it.
Both sides call `platform/seal`, which holds only the sealed formats of
decision 0013 (the pool envelope and object segments, each led by a version
byte) and no keys. The keyring, the KEK providers and every data key stay in
`platform/crypt`, which rule 7 keeps off worker hosts. All of it uses only
algorithms the Go Cryptographic Module approves, so release builds made with
`GOFIPS140=certified` run unchanged under `GODEBUG=fips140=only`.

### VCS webhook ingest

```text
internal/vcs/
├── api/
│   ├── sources.go       Sources: ArchiveURL(stack, sha), CheckoutSpec (sparse paths, merge commit)
│   ├── feedback.go      Feedback: Check, Comment(key, body), Deployment
│   ├── pullrequests.go  PullRequests: ForStack, Get
│   └── events.go        PushReceived, PullRequestChanged, CommentCommand
├── assembly/
└── internal/
    ├── core/
    │   ├── intake/      Accept(delivery): store and enqueue in one tx; nothing else before the ack
    │   ├── normalize/   provider payload → one push or pull request input (report 11 section 11.2)
    │   ├── route/       targets: stacksapi.ByRepository, registryapi.Modules.ByRepository; push policy;
    │   │                runsapi.Runs.Create; dedupe; superseded proposed runs; 10-minute event budget
    │   ├── feedback/    subscribes to RunStateChanged: checks, aggregated checks, deployments
    │   ├── pullrequest/ PR records for the stack's PR tab
    │   └── port/        Provider: VerifySignature, Parse, Check, Comment, Archive
    └── adapters/
        ├── http/webhook.go   POST /webhooks/vcs/{integration}: hand-written chi route
        ├── github/           signature.go appauth.go parse.go checks.go comments.go deployments.go
        └── pg/               vcs_integrations, vcs_deliveries, pull_requests
```

Intake is durable from the moment the provider gets its answer, as
[report 27](../reference/27-reference-materials.md#12-cross-tool-comparison)
recommends after Atlantis and Digger: the receiver verifies the signature
([11, section 4.4](../reference/11-vcs-integrations.md#44-webhook-verification)),
inserts the delivery and enqueues `vcs.process_delivery` on the `vcs-inbox`
queue in one transaction, and answers `202` (`200` for a duplicate,
decision 0012). Large bodies go to object storage first, as Spacelift's large
queue messages bucket does
([24, section 3.2](../reference/24-self-hosted-reference.md#32-object-storage-buckets)).
The scheduler process mode consumes `vcs.process_delivery`: `normalize` builds the provider
neutral input, `route` finds the targets and evaluates each one's push policy
through `policiesapi.Evaluator.Push`, and calls `runsapi.Runs.Create` with a
dedupe key of stack, commit SHA and run type
([11, section 11.2](../reference/11-vcs-integrations.md#112-what-an-event-becomes)).
`vcs` depends on `runs/api`, `stacks/api`, `registry/api` and
`policies/api`; none of them depend on `vcs` except through `vcs/api`
(`runs` asks `Sources` for the archive URL at handover), so the graph stays
acyclic at the package level.

VCS agent pools (P19) split the same way as workers. `vcs/internal/adapters/agentgateway`
serves the `/vcs-agent/v1` long-poll inside `serve`, and the `vcs-agent`
subcommand runs `internal/agent/vcsagent`, which imports no domain. The
provider route tables that the agent's allowlist compiles in (decision 0012)
are pure data in `internal/kernel/vcsroutes`, the one package both sides
import, so the allowlist and the provider clients cannot drift.

### State backend

```text
internal/state/
├── api/versions.go       Versions: Latest(stack), Outputs(stack) for dependencies and templates
├── assembly/
└── internal/
    ├── backend/          Terraform http backend: GET, POST, LOCK, UNLOCK; lock ID checks
    ├── state/            versions, import (100 MB cap), rollback as a new version, 90-day non-current retention
    ├── tfe/              read-only Terraform Cloud state API subset for external access
    ├── http/             hand-written chi routes /state/v1/stacks/{stack}; chi.RegisterMethod("LOCK") and ("UNLOCK");
    │                     strict partial for state version operations
    └── pg/               managed_state_versions, state_locks
```

Runs reach the backend with the run token issued at handover. The handler
introspects it through `accessapi.Tokens`, gets a run principal, and asks
`runsapi.StateAccess` whether that run, in its current state, may read,
lock or write
([16, section 1.1](../reference/16-state-management.md#11-how-a-run-reaches-it)).
Each state version is its own object in the `states` store, written once
under a key that ends in the version's ULID and sealed in decision 0013's
object format through `platform/crypt`; the `managed_state_versions` row
records the key and SHA-256. History and rollback rest on those rows, not
on bucket versioning, which decision 0005 does not use
([16, section 6](../reference/16-state-management.md#6-state-history-and-rollback)).
The domain contributes `state.v2` and `tfe.v2*` to service discovery through
`assembly.Discovery()`, which `app/discovery.go` merges.

### Module and provider registry protocol

```text
internal/registry/
├── api/modules.go        Modules: ByRepository, CreateVersion(from push policy), Consumers
├── api/events.go         ModuleVersionCreated, ModuleVersionFailed, ProviderVersionPublished
├── assembly/
└── internal/
    ├── core/
    │   ├── module/       module as a stack variant without state: source, space, pool, environment
    │   ├── version/      major.minor.patch, no reuse, no skipped numbers, PENDING ACTIVE FAILED, yanked
    │   ├── testcase/     .infraweave/config.yml test cases → runsapi.Runs.Create(TESTING), depends_on order
    │   ├── provider/     drafts, publish, revoke, platforms with presigned uploads
    │   ├── gpg/          account-level keys, revocation, signature and checksum verification
    │   └── sharing/      space inheritance, space availability, shares between accounts of one
    │                     organization (422 validation-failed for another's), public
    └── adapters/
        ├── protocol/     hand-written: modules.v1 versions and download (204 + X-Terraform-Get),
        │                 providers.v1 versions and download; lenient version parsing
        ├── http/         strict partial for Modules and Providers tags
        └── pg/           modules, module_versions, module_version_consumers, module_shares,
                          module configuration elements and attachments,
                          terraform_providers, provider_versions, provider_version_platforms, gpg_keys
```

The protocol endpoints follow
[report 15, section 9](../reference/15-module-and-provider-registry.md#9-registry-protocol):
service discovery from `app/discovery.go` (contributions from `registry`,
`state` and `access` for `login.v1`), version lists, and downloads that
answer with presigned URLs from `platform/objectstore` rather than streaming
archives through the server. A module test case is a `TESTING` run created
through `runsapi`, so the registry needs no run machinery of its own.
`vcs` routes tag pushes to `registryapi.Modules.CreateVersion` after the push
policy decides the version number.

### Access, identity and spaces

```text
internal/access/
├── api/authz.go          type Decider interface { Can(Action, kernel.SpaceID) bool; Reachable(Action) []kernel.SpaceID }
├── assembly/assembly.go  New(...); Middleware() for app/router.go; Routes(); Cron() (session and key expiry)
└── internal/core/
    ├── space/            tree under the account's immutable top space, re-parenting with reachability,
    │                     inheritance toggle
    ├── role/ binding/    150-value action catalogue, system roles, custom roles, five actor kinds
    ├── authz/            propagation down, Read up through inheritance, implicit stack read
    ├── token/            JWT signing keys, rotation, JWKS; run, worker and API tokens (decision 0006)
    ├── oauth/            the built-in authorization server for /mcp and /flows/mcp: metadata,
    │                     registration, PKCE S256, consent, rotating refresh tokens
    └── login/            sessions bound to one account, API key exchange, LOGIN policies through
                          policiesapi on the account's top space
```

How a request gets identity and permission:

1. `app/router.go` installs `access/assembly.Middleware()` before every
   strict and hand-written route. It authenticates the bearer token or
   session and puts a `kernel.Principal` in the context. The principal
   names one organization and one account: a member authenticates at the
   organization, and the account's `LOGIN` policies admitted it when it
   entered the account, so nothing a handler does reaches another account.
2. A handler such as `stacks/internal/http/strict.go` reads the principal
   with `kernel.PrincipalFrom(ctx)` and calls its own service.
3. The service asks `accessapi.Authorizer.For(ctx, principal)` once per
   operation and checks `Can(action, space)`; list operations filter by
   `Reachable(action)`, because almost every table carries a space column
   ([28, section 1.2](../reference/28-data-model.md#12-identity-and-tenancy)).
4. Role bindings whose actor is a stack (administrative stacks) or a Flows
   project are rows in `access`; the run and the Flows project carry that
   identity as a principal kind, so `access` never imports `runs` or
   `flows`.
5. Every resource a handler returns carries `allowedActions`. The handler
   asks `Decider.Allowed(resource)` for the actions the caller's grants
   permit and, for a resource with a state machine, intersects them with
   the transitions its owning domain allows (see
   [Console and clients](#console-and-clients)).

### Features and tiers

```text
internal/features/
├── api/entitlement.go     Entitlement.For(ctx, organization) → Snapshot{Has(feature), Limit(name)}; Billing()
├── api/limits.go          Limits.Check(ctx, account, limit, count): the organization's pool, then the
│                          account limit → limit-reached problem or nil
├── api/gate.go            Gate(feature) for platform/queue's SkipGate and platform/cron
├── api/events.go          EntitlementChanged
├── assembly/              Middleware(); OperatorAPI (alias of the operator partial); Consumers()
└── internal/
    ├── catalogue/
    │   ├── catalogue.gen.go capabilities, options, limits, requirements and seeded tiers (decision 0021),
    │   │                    generated from docs/data/features.json by make generate
    │   └── coverage_test.go  every operation's x-infraweave-feature and every gated field names an entry
    ├── features/          entitlement: tier plus grants, minus features.disabled and unmet requirements; billing off
    │                      means Enterprise minus features.disabled; seed once, then operator data
    ├── middleware/        after access's authn middleware: route → capability from gen/<spec>/features.gen.go
    ├── http/              operator partial: tiers, organization tiers, grants, billing
    └── pg/                tiers, tier_features, tier_limits, organization_grants, account_limits,
                           installation_billing
api/src/operator/          the operator API; only operator principals reach it (decision 0021)
internal/kernel/feature.go Feature, a typed catalogue name, so an api/ signature can carry one
```

`features` sits at the bottom of the declared graph: it imports no domain,
and every domain that creates a counted thing imports `featuresapi`. That
puts each check where decision 0021 asks for it:

| Check | Where | How |
| --- | --- | --- |
| Capability | `features/internal/middleware`, mounted by `app/router.go` after authentication and before every strict and hand-written route | The generated `features.gen.go` maps each route to the one capability its operation names; hand-written routes (state backend, registry protocols) name theirs when they mount |
| Option | The owning domain's input validation, such as `vcs` for `vcsChecks: AGGREGATED` or `stacks` for each vendor | `Snapshot.Has(option)` only when a write changes the field to the gated value |
| Limit | The domain that creates the counted thing: `access` (seats), `workers` (private workers), `runs` (public worker slots, a concurrency limit at claim), `intelligence` (AI budget) | `featuresapi.Limits.Check` inside the creating transaction |
| Background work | `platform/queue` and `platform/cron` | A job kind or cron entry names its feature; a `SkipGate` bound in `internal/app` to `featuresapi.Gate` completes the job as skipped at dequeue and fires no schedule, so downgrade pauses work and deletes nothing |

While `billing` is off, the snapshot is every feature but
`features.disabled`, with no limits, so no check can refuse a caller for a
tier. The operator API (`api/operator.openapi.yaml`, generated into
`platform/gen/operator`) keeps the installation-level actions and is served
by two partials: `features` for tiers, grants and the billing switch, and
`access` for organizations, where creating one creates its first account.
Organization admins create and remove accounts through the served
`Organization` tag, which `access` serves, and set account limits through
`features`' part of it. A write bumps `organization_entitlements.entitlement_version`
and sends `entitlement:<organization>` on `infraweave_invalidate`, so every
replica rebuilds its snapshot without a restart, as `access` does for
authorization (decision 0007). A limit is pooled: `Limits.Check` counts the
thing across the organization's accounts and then against the account's
own limit, if an organization admin set one.

The foreign keys follow the graph. `organizations.tier` in `access`
references `features`' `tiers` with `ON DELETE RESTRICT`, because `access`
imports `featuresapi`; deleting a tier an organization holds is refused by
PostgreSQL as well as by the handler's `409 invalid-state`.
`organization_grants` and `account_limits` hold their IDs without a key,
because `features` does not import `access`; the `features` consumers of
`OrganizationDeleted` and `AccountDeleted` remove them. The
callers' problems are decision 0022's `feature-disabled`,
`feature-not-in-tier` (with the tiers that include the feature) and
`limit-reached` (with the limit and its value), written by
`platform/httpkit/problem` helpers.

### Events: notifications, webhooks and audit

```text
internal/platform/outbox/    the envelope (id, kind, account, subject, occurred_at, document) and the kind
                             registry with JSON Schemas; Append(ctx, Event): events row, events.fanout job
                             and pg_notify in the caller's tx; retention
internal/platform/events/    Subscribe(kind, consumer, Handler); fan-out jobs consumed in the scheduler process mode
internal/platform/httpkit/sse/   stream writer; replicas LISTEN on infraweave_events and infraweave_logs
internal/events/
├── api/router.go            Router: Route(ctx, NotificationInput) → []Route (inbox, slack, webhook, pull_request)
├── api/inbox.go             Inbox: Raise(ctx, Notification) for internal errors from any domain
├── assembly/
└── internal/
    ├── core/
    │   ├── catalogue/       outbox topics → notification policy input documents (report 19 section 3)
    │   ├── inbox/           grouping key, occurrences, dismissal, 14-day retention
    │   ├── webhook/         named webhooks per space, stack and module webhooks on every run state change
    │   ├── delivery/        HMAC SHA-256 signatures, secret headers, retries, auto-disable after 10 failures
    │   ├── audit/           search and retention over audit_trail_entries; the audit trail webhook shipper
    │   └── metrics/         exporter-equivalent series (P18)
    └── adapters/{http,slack,pg}/   webhooks, webhook_deliveries, notifications; reads audit_trail_entries
```

One outbox carries every event of Deploy and Flows: `Append` inserts the
`events` row and enqueues an `events.fanout` job in the producer's
transaction, as decision 0015 records, and the fan-out enqueues one job per
subscribed consumer (notifications, stack webhooks, VCS feedback, resources
indexing and Flows triggers), each keyed so that a second fan-out adds
nothing. The envelope, the kind registry and the emit path are platform
packages, below every domain that emits, so `runs`, `workers`, `policies`
and `flows` raise `run_updated`, `drift_detected`, `policy_evaluated`,
`worker_updated` and `flows.event` without importing the `events` domain,
which holds only sinks. The Flows trigger consumer belongs to `flows`, so
`events` never imports `flows` and the graph stays acyclic (see
[Flows engine](#flows-engine)). Only the audit shipper keeps a cursor. Audit entries are not events: each domain
records them through `platform/audit` in the change's transaction, so a
rolled-back write leaves no entry and the audit trail never records a change
that did not happen
([19, section 8](../reference/19-events-notifications-audit.md#8-the-audit-trail)).
The notification policy chooses sinks: the catalogue builds the input,
`policiesapi.Evaluator.Notification` returns routes, and a `pull_request`
route calls `vcsapi.Feedback.Comment` with the policy's deduplication key.
Every string a route carries is a text template of decision 0024, rendered
by `delivery` through `platform/expr` against the event document, with
`logs(phase)` reading the run's log tail. Delivery bodies go to the
`deliveries` store with a short expiry
([24, section 3.2](../reference/24-self-hosted-reference.md#32-object-storage-buckets)).

The server-sent-event streams of decision 0015 are part of the public API
and live with the data they stream: run logs and run state in `runs`, pool
queues in `workers`, Flows events in `flows`. Each is an operation of the
served document with a `text/event-stream` response, written through
`platform/httpkit/sse`, authorized as its polling equivalent when it opens
and per message as it sends. `platform/sql/notify` holds one `LISTEN`
connection per `serve` replica and wakes the local streams; a reconnect
with `Last-Event-ID` replays from the `events` row after it.

### Flows engine

```text
api/proto/flows/app/v1/          app.proto (App), host.proto (Host); package infraweave.flows.app.v1
pkg/flows/app/v1/                generated gRPC stubs, used by apps and by the agent
pkg/flows/appsdk/                Serve(app); typed blocks and configuration from Go types → Describe; KV, locks,
                                 timers, messages and widgets; apptest/ runs blocks without an agent or a server
internal/flows/
├── api/flows.go                 Projects for Deploy role bindings; the Deploy trigger kinds a block may name
├── assembly/assembly.go         Routes() (REST, events stream, MCP, HTTP endpoints, agent gateway),
│                                Consumers() (the flows queue and the trigger subscription), Cron() (registries,
│                                retention), Surfaces() (input, static, output and Transform configuration)
└── internal/
    ├── core/
    │   ├── project/             the account's projects, sharing Deploy's identity; sandboxes
    │   ├── flow/ block/         definition, links, YAML import and export with !expr, lifecycle, error handlers
    │   ├── delivery/            flows.deliver: evaluate the input, run the block, return buffered effects
    │   ├── invocation/          lineage, the four statuses (flows.invocation.status), re-emit, manual retry
    │   ├── timer/               Sleep, Show Form, pending events and app timers as rows; flows.timer
    │   ├── app/                 installations, versions (image digest, contract versions, Describe manifest),
    │   │                        custom apps; a JavaScript bundle is refused with 422 validation-failed
    │   ├── agentpool/           pools, hashed tokens, flows.agent_task partitioned by pool
    │   ├── named/               named expressions per project; acyclic calls; callers re-checked on save
    │   ├── deploy/              coreBlock: deploy: Trigger Run and Task, Confirm and Review Run (runsapi),
    │   │                        Deploy Template and Blueprint (templatesapi), API Request by operationId
    │   ├── widget/              declarative widget types; SetWidget validated against the type's schema
    │   ├── datatable/ form/
    │   └── port/                Store, AgentTasks, Clock
    └── adapters/
        ├── http/                strict partial for the Flows tags of api/api.openapi.yaml under /api/v1/flows/;
        │                        the events stream; the Deploy-side Flows tag operations
        ├── trigger/             platform/events consumer: matching Deploy trigger blocks become root flow_events
        ├── gateway/             /flows/agent/v1/connect: frames, claims of flows.agent_task for connected pools
        ├── registry/            hourly registry subscriptions and digests
        ├── mcp/ endpoint/       platform and flow MCP servers; HTTP Endpoint and form routes
        └── pg/                  schema flows: flows_projects, flows, flow_blocks,
                                 flow_events, flow_invocations, flows_timers, flows_kv, app_installations,
                                 app_versions, agent_pools, flows_secrets, flows_named_expressions, data tables
internal/agent/flowsagent/       conn, apps, host and executor, as in the tree above
```

Flows is a domain like the others, last in the declared graph: it imports
`runsapi`, `stacksapi`, `templatesapi`, `accessapi` and `featuresapi`, and
no Deploy domain imports `flows`. Its tables live in their own PostgreSQL
schema, `flows`, with their own migration stream registered after every
domain it imports, so `flows_projects.account_id` can reference
`access`' accounts with `ON DELETE RESTRICT`. Flows belongs to the
account, which takes the place of Spacelift Flows' organization: its
projects and UUIDs remain its own, its members, teams and service accounts
are the account's, and Spacelift Flows' organization admin is the account
admin (decision 0018).

**The engine is queue work in `scheduler`.** Every Flows job runs on decision
0011's queue under the logical queue `flows`, so a busy flow never delays
Deploy's fan-out:

| Job kind | Handler | Does |
| --- | --- | --- |
| `flows.deliver` | `core/delivery` | Evaluates the input's configuration through `platform/expr` with the event's stored `now` and seed, runs a core block in Go or hands an app block to an agent, and commits the buffered effects (events, pending events, timers, messages) with the delivery's completion, so a retried delivery never emits twice |
| `flows.timer` | `core/timer` | A due Sleep, Show Form, pending event or app timer; a wait of days is one row |
| `flows.lifecycle` | `core/app`, `core/block` | App and block syncs and drains, keyed by entity so one runs at a time |
| `flows.invocation.status` | `core/invocation` | Recomputes the four statuses from the invocation's deliveries |
| `flows.agent_task` | `adapters/gateway` | One `App` call for an agent pool, claimed by the `serve` replica holding a socket for that pool |

KV operations are the exception to buffering: they return values and take
locks, so the agent relays them live through the `Host` service. A failed
delivery, an expression's `fail(message)` included, becomes a failed event
on the block's input and goes to its `errorHandler`; only infrastructure
failures retry automatically.

**Deploy events reach flows without a bridge.** Flows events and Deploy
events share `platform/outbox`'s envelope and emit path. `adapters/trigger`
subscribes through `platform/events` to the Deploy kinds a trigger block can
name (`run_updated`, `drift_detected`, `policy_evaluated`,
`notification_created`, `module_version`); its fan-out job finds every
enabled trigger block whose kind and filter match and, in that job's
transaction, inserts the root `flow_events` row linked to the Deploy event
and one `flows.deliver` job per linked input. An event a block emits is
appended through the same outbox, so it reaches the Flows events stream and
audit like any other.

**Apps are gRPC services in containers.** An app version is an OCI image
whose entrypoint serves the `App` service of `api/proto/flows/app/v1/`;
`flows` stores the manifest and digest, never the image. Within
`infraweave.flows.app.v1` changes are additive, `buf breaking` against the
last release blocks a break, and `Describe` lists the major versions an app
serves. `flows-agent`, from `internal/agent/flowsagent`, dials
`/flows/agent/v1/connect` on any `serve` replica with its pool ID and token,
receives `task` frames, starts or reuses one container per installation and
digest through its executor (`docker` with optional gVisor, `kubernetes`
with mutual TLS and a `NetworkPolicy`, or `process` for development), and
calls the app over gRPC that never leaves the host or the pod network the
agent controls. The agent serves `Host` to its apps and relays each live
call as a `call` frame. The app receives evaluated configuration and only
the secrets it resolved to; it never sees an expression. Apps contribute
declarative widgets only, rendered by the console; no app ships browser
code.

**Flows switches off like any feature.** `flows-engine`, `flows-apps`,
`flows-agents`, `flows-data-tables`, `flows-forms` and `flows-mcp-tools`
are catalogue entries: the features middleware gates `/api/v1/flows/` routes
and the gateway, and the queue's `SkipGate` completes a gated feature's jobs
as skipped, so a downgrade pauses timers, schedules and deliveries and
deletes nothing.

Spacelift self-hosts Flows as server, worker and gateway from one image,
with agents in a separate image
([23, section 14.1](../reference/23-flows.md#141-components)). Here the
server and gateway are `serve`, the worker is `scheduler`, and the agent is
the `flows-agent` process mode of either `infraweave` or the slim
`infraweave-agent` build, so Flows adds no server process mode.

### Expressions

```text
internal/platform/expr/
├── env.go             Env: one surface's variables and types; provider.go maps JSON Schema to CEL types
├── compile.go         Compile(env, source, form) → Program; cached by source hash, surface and declared types
├── eval.go            Eval(ctx, prog, vars, Context{Now, Seed}) under expressions.cost_limit and .timeout
├── template.go        ${{ }} text templates, the $${{ escape, the rules for rendering values into text
├── shell.go           POSIX shell quoting of every interpolated value, for workflow command templates
├── lib/               cel-go's strings, lists, regex and math extensions; merge, pick, omit; url and json
│                      encoding; random.* drawn from the seed; fail(message)
├── editor/            Check, Complete, Evaluate over an Env: diagnostics with ranges, static cost estimate
└── surface.go         Surface{Name, Env(ctx, principal, ref)} and the registry internal/app fills
internal/expressions/  /expressions/check, complete and evaluate over that registry
```

The language is platform because it carries no business rule and both
sides of the binary need it: the server evaluates every surface, and the
runner renders `.infraweave/workflow.yml`. Rule 7 names `platform/expr`, so
the runner links cel-go and the function library but nothing that loads a
variable, a secret or an event. What each surface means stays with the
domain that owns it:

| Surface | Owner and code path | Process mode |
| --- | --- | --- |
| Flows input, output and Transform configuration, steps form, named expressions | `flows/internal/core/delivery`, `core/named` | `scheduler`, per event |
| Flows static and app installation configuration | `flows/internal/core/block`, `core/app` | `serve` at save; `scheduler` when a signal changes |
| Blueprint and template bodies | `templates` | `serve` for a synchronous create; `scheduler` for a queued deployment change |
| Output references and environment interpolation | `runs/internal/core/handover`, over `configurationapi.Environment` and `stateapi.Versions` | `serve`, at the run's handover |
| Notification text | `events/internal/core/delivery` | `scheduler`, in the delivery job |
| Workflow command templates | rendered by `agent/exec`; the editor surface is contributed by `vendors`, from each `iac` vendor's workflow keys | `runner`, at initialization, with only the fixed run parameters in scope |

Each owner contributes its surfaces through `assembly.Surfaces()`: a name
and an `Env` built from its own schemas (core blocks' and apps' `Describe`
schemas, the event kind schemas of `platform/outbox`, template inputs, a
run's variables). `internal/app` collects them and hands them to the
`expressions` domain, so the editor endpoints reach every surface without
importing a domain, and a surface's `Env` authorizes the caller as a read
of its context. Flows contexts reach the same operations through the same
`editor` package, with no copies under `/api/v1/flows/`, and `POST /policies/check`
belongs to `policies`, because Rego is not an expression. Saving a flow, a
template version, a policy's notification text or a variable type-checks
through the owner's surface and refuses an error with `422
validation-failed`.

Secrets resolve only in the owning domain: a Flows surface loads the
secrets an expression names with a literal key, and the result is masked
by 0013's masker. `now` and the seed are part of the evaluation context,
stored with the Flows delivery or the template change record, so a retry or
a replay evaluates the same values. Every evaluation runs under
`expressions.cost_limit` and `expressions.timeout`, so an expression in
`scheduler` needs no sandbox and cannot hold a queue worker.

### Console and clients

```text
pkg/sdk/                       one Client across the api and operator documents (P24)
├── auth.go                    API key or OIDC key exchanged at POST /auth/token, cached until validUntil
├── transport.go               Idempotency-Key per call, reused on its retries; 429 and 503 with Retry-After
├── iter.go                    list operations as Go iterators over nextCursor
├── problem.go                 a typed error per decision 0022 slug, for errors.As
└── stream.go                  typed stream iterators from x-infraweave-stream-messages
pkg/client/{api,operator}/            generated by oapi-codegen; outside the coverage gate
cmd/infraweavectl/             (P24b) cobra and viper over pkg/sdk: stack, module, policy, workerpool, flows,
                               operator, expr, mcp, api and the rest of the command tree of decision 0001
web/console/                   (P24c)
├── src/routes/                TanStack Router: typed routes and search parameters
├── src/api/                   orval client and TanStack Query hooks, generated and committed
├── src/streams/               stream subscriptions that update the Query cache; polling while closed
├── src/components/            Radix primitives with Tailwind 4, owned in the repository; light and dark tokens
├── src/canvas/ src/editor/    React Flow; CodeMirror 6 with diagnostics from /expressions/*
├── src/messages/              the English message catalogue
└── vite.config.ts             build output written to internal/platform/httpkit/console/dist
internal/platform/httpkit/console/
├── console.go                 //go:embed all:dist; Handler: files, index.html fallback, CSP nonce, cache headers
└── dist/                      a committed marker file; without index.html, a placeholder page
```

Every client is built on the public API and nothing else. `pkg/sdk` wraps
the generated `pkg/client/<spec>` packages rather than replacing them, so
the generated code stays mechanical and drift-checked while the
hand-written layer stays small and inside the coverage gate. It imports
nothing under `internal/`, so `infraweavectl` and the provider in
`weaveplatform/terraform-provider-infraweave` depend on it without pulling
in server code.

The console is one more client. `app/router.go` mounts
`platform/httpkit/console` at `/` after every other route, and paths that
other handlers own (`/api/`, `/flows/`, `/mcp`, `/.well-known/`, the state
backend and registry protocols) never fall back to `index.html`. It
authenticates with `access`' browser session cookie from the API's own
origin, loads nothing from another origin, and is switched off with the
`console` feature like any other. It calls only operations of the served
bundle, so no domain has a route for the console alone.

The console decides no permission and no state transition. Each domain's
handler fills `allowedActions` from code it already runs to enforce them:
`accessapi.Decider.Allowed` for the caller's grants, intersected with the
owning domain's own rule, such as `runs/internal/core/machine` guards for a
run or the human lock for a stack, whose run state `stacks` reads through
`runsapi.StackStatus` in the same request. Because the same functions
refuse the action when it is called, the list and the enforcement cannot
disagree.

### Composition root

```text
cmd/infraweave/
├── main.go               cobra root; binds setting flags only (not --config or --help)
├── serve.go              app.Serve(ctx, cfg)
├── scheduler.go          app.Scheduler(ctx, cfg)
├── all.go                app.All(ctx, cfg): serve and scheduler in one process
├── agent.go              registers agent/cli: launcher, hidden runner, vcs-agent (P19), flows-agent (Flows);
│                         imports no internal/app
├── migrate.go            `migrate up` and `migrate status`: app.Migrate, then exit
├── auth.go               `auth hash-password` (decision 0006), then exit
├── secrets.go            `secrets generate-kek`, `secrets rewrap`, `secrets rotate` (decision 0013), then exit
└── version.go            `version`: platform/buildinfo
cmd/infraweave-agent/
└── main.go               registers the same agent/cli commands and nothing else, so both builds take the
                          same flags and configuration
internal/app/
├── config/               config.go (Settings: Server, Database, ObjectStore, Crypt, Queue, Expressions,
│                         Features, one field per domain)
├── platform.go           open pgx pool, migrate every stream in dependency order, object stores, keyring,
│                         queue, outbox, cron
├── domains.go            construct domains in dependency order; hand api values to dependants; bind back
│                         edges, the queue's SkipGate and the expression surfaces
├── router.go             chi router: middleware, deploy, operator and flows strict servers, hand-written
│                         mounts, the console last
├── discovery.go          service discovery document merged from Discovery() contributions
└── modes.go              which domain parts each process mode starts
```

Every domain's `assembly` package returns a value with the same method set,
and the process modes pick from it:

```go
// internal/app/modes.go
//
// part is what every domain contributes. A process mode starts only the
// parts it needs: serve mounts routes and consumes no queue except the
// Flows agent tasks its sockets deliver (decision 0018), so a
// burst of webhook processing cannot starve API latency; scheduler consumes
// queues and fires cron and serves only the readiness port.
type part interface {
	Routes(r chi.Router)               // hand-written routes; strict partials go through Strict()
	Consumers(q queue.Registry)        // queue handlers and outbox subscriptions
	Cron(c cron.Registry)              // periodic tasks; every replica's ticker claims due rows, no leader
	Surfaces(s *expr.Registry)         // expression surfaces; both serve and scheduler evaluate them
	Close(ctx context.Context) error
}

func Scheduler(ctx context.Context, cfg config.Settings) error {
	p, m, err := open(ctx, cfg) // platform, then domains in dependency order
	if err != nil {
		return err
	}
	for _, x := range m.all() {
		x.Consumers(p.queue)
		x.Cron(p.cron)
		x.Surfaces(p.surfaces)
	}
	return runWorkers(ctx, p) // per-queue concurrency from cfg.Queue, as Spacelift's DRAIN_CONCURRENCY_*
}
```

Wiring order at the end of P27, from `domains.go`, is the declared graph's
order (see [Tests and enforcement](#tests-and-enforcement)): features,
access, workers, vendors, stacks, configuration, policies, runs, state,
cloud, registry, vcs, schedules, events, templates, resources,
intelligence, flows, expressions. A domain's `Deps` takes only `api` values
of domains built before it, plus late-bound interfaces for the six back
edges that the order cannot satisfy: `stacks` reads `runsapi.StackStatus`;
`runs` reads `vcsapi.Sources`, `cloudapi.Credentials` and
`stateapi.Versions` at handover; `workers` claims and reports through
`runsapi.Scheduler` and `runsapi.Reports`; `access` evaluates `LOGIN`
policies through `policiesapi.Evaluator`. Those are set with `SetX` calls
after construction, so they add no edge to the declared graph and no
foreign key. The same step binds `platform/httpkit/dispatch` to the
composed router for `flows` and `intelligence`. The same order registers the migration
streams, so a stream always runs after the streams of the domains it
imports. The function stays a linear list of about 270 lines.

The process modes map onto Spacelift's processes: `serve` is its server and
its VCS and Flows gateways, `scheduler` its drain with cron folded in, and
`launcher` and `flows-agent` its launcher and Flows agent
([24, section 2.1](../reference/24-self-hosted-reference.md#21-process-inventory)).
The agent process modes are wired outside `internal/app`: their cobra
commands live in `internal/agent/cli`, and `cmd/infraweave/agent.go` is the
only place where the agent path meets the rest of the binary. That is what
makes `cmd/infraweave-agent` a short `main.go`: it registers the same four
commands and links nothing outside `internal/agent`, its seven platform
packages, `internal/kernel`, `internal/iac` and `pkg/flows/app/v1`.

### Generated code

| Spec | Fragments | Generated server | Implemented by | Client |
| --- | --- | --- | --- | --- |
| api | `api/src/api/`: Deploy, and Flows (Flows) | `internal/platform/gen/api` | each domain's `internal/.../http` partial, `flows/internal/adapters/http` among them, composed in `app/router.go` | `pkg/client/api`, the console's orval client |
| worker | `api/src/worker/` (P8) | `internal/platform/gen/worker` | `workers/internal/adapters/protocol` | `internal/agent/wire` |
| operator | `api/src/operator/` (P3b) | `internal/platform/gen/operator` | `features/internal/http` and `access/internal/adapters/http` | `pkg/client/operator` |
| `App` and `Host` gRPC | `api/proto/flows/app/v1/` (Flows) | `pkg/flows/app/v1` (buf generate) | apps (`App`); `agent/flowsagent/host` (`Host`) | `agent/flowsagent/apps` |
| none (hand-written) | | | VCS webhooks (`vcs`), http state backend and `tfe` (`state`), discovery (`app`), modules.v1 and providers.v1 (`registry`), `login.v1` (`access`), the Flows agent gateway, HTTP endpoints and MCP (`flows`) | |

`internal/devtools/specref` keeps generating the Spacelift reference and
scaffolding fragments; `internal/devtools/openapibundle` bundles them;
oapi-codegen writes the strict servers; `internal/devtools/stubgen` writes
`UnimplementedStrict` next to each, and `features.gen.go`, the route to
capability table the features middleware reads. One generated
`StrictServerInterface` spans many domains, so the router composes it by
embedding:

```go
// internal/app/router.go (excerpt)
//
// apiServer implements api.StrictServerInterface. Each domain's
// partial sits at depth one and the 501 stubs at depth two, so an
// implemented operation shadows its stub, and two domains implementing the
// same operation make the method ambiguous, so the assertion below fails to
// compile; without it the ambiguity would pass.
// internal/app cannot import a domain's internal/.../http package,
// so each assembly/ package re-exports its partial as an alias, named after
// its domain because an embedded field takes its type's name:
// type StacksAPI = http.API.
type apiServer struct {
	*stacksassembly.StacksAPI
	*runsassembly.RunsAPI
	*accessassembly.AccessAPI
	*policiesassembly.PoliciesAPI       // (P13)
	*expressionsassembly.ExpressionsAPI // (P9)
	*flowsassembly.FlowsAPI             // (Flows)
	stubs
}

type stubs struct{ api.UnimplementedStrict }

var _ api.StrictServerInterface = apiServer{}
```

The operator server composes the same way. The `Makefile`
generate loop keeps its shape; only the paths change:
`api/oapi-server-<spec>.yaml` writes `internal/platform/gen/<spec>`,
`api/oapi-client-<spec>.yaml` writes `pkg/client/<spec>`, and stubgen reads
`internal/platform/gen/<spec>/server.gen.go`. The same target runs `buf
generate` for the `App` contract and orval for the console's client and
hooks; all of it is committed, and `make verify-generate` fails on any
drift.

### Tests and enforcement

| Kind | Location |
| --- | --- |
| Unit tests | beside the code in every package; `core/` packages use fakes of other domains' `api` |
| Store tests | `internal/<domain>/internal/.../pg/*_test.go` against PostgreSQL through `platform/sql/sqltest` (testcontainers) |
| Contract suites | `platform/queue/queuetest`; `platform/objectstore/objectstoretest` against RustFS, fake-gcs-server, Azurite and local disk; `iac/iactest`; `agent/agenttest` |
| Domain tests | `internal/<domain>/assembly/assembly_test.go`: the domain built on sqltest with fake `api` dependencies |
| Machine tests | `runs/internal/core/machine`: every transition row and every illegal pair |
| Policy corpus | `policies/internal/combine` and `platform/rego` over the 67 example-library templates ([report 09, section 10.2](../reference/09-policies.md#102-counts)) |
| Expression tables | `platform/expr`: the function library, rendering rules, shell quoting, cost limits, the same `now` and seed giving the same result |
| Catalogue coverage | `features/internal/catalogue`: every operation of every served document and every gated field names a catalogue entry |
| App contract | `pkg/flows/appsdk/apptest` runs a fixture app; `TestUnitAppContractCompatible` runs `buf breaking` against the last release |
| Schema graph | `TestAccSchemaDependencies`: each stream applied in registration order, foreign keys and views checked against the per-domain depguard rules |
| Rule entries | `TestUnitDepguardFixtures`: every domain directory has its graph rule and its `constructors` and `shared-below-domains` entries, and its `core-not-adapters` entry if it is a hexagon |
| Acceptance | `test/acceptance/`: the binary in `all` mode plus a `launcher` and a `flows-agent`, real PostgreSQL, the object store drivers' emulators, a real OpenTofu run |
| FIPS | the unit and acceptance suites again under `GODEBUG=fips140=only`, a blocking job |
| Console | `web/console/`: `tsc --strict`, ESLint with typescript-eslint's strict configuration, Vitest at 95% in total and 90% per directory, Playwright journeys against a real `infraweave all` |

Enforcement is golangci-lint's depguard, the repository's only Go linter,
plus the compiler's `internal` rule. `$m` below stands for
`github.com/weaveplatform/infraweave`. depguard v2 matches each entry as a
plain string prefix of the import path, so a bare `$m/internal/stacks/api`
would also admit a sibling such as `$m/internal/stacks/apiutil`. Every
allow entry therefore ends in `$`, which matches that package exactly, or
in `/`, which matches the packages below it; a tree whose root is also a
package, such as `kernel`, takes both. A bare deny entry over-matches,
which only refuses more, so deny entries stay bare.

```yaml
linters:
  enable: [depguard]
  settings:
    depguard:
      rules:
        graph-runs:                         # rule 2: one rule per domain, the declared graph
          list-mode: lax                    # the longer match wins, so each allow beats the deny
          files: ["**/internal/runs/**"]
          deny:
            - pkg: $m/internal
          allow:
            - $m/internal/platform/
            - $m/internal/kernel$
            - $m/internal/kernel/
            - $m/internal/iac$
            - $m/internal/iac/
            - $m/internal/runs/
            - $m/internal/features/api$
            - $m/internal/access/api$
            - $m/internal/stacks/api$
            - $m/internal/configuration/api$
            - $m/internal/workers/api$
            - $m/internal/vendors/api$
            - $m/internal/policies/api$
        # ... 19 graph rules, one per domain, as the declared graph below lists;
        # TestAccSchemaDependencies reads the same rules
        api-packages:                       # rule 3
          list-mode: strict
          files: ["**/internal/*/api/**"]
          allow: [$gostd, $m/internal/kernel$, $m/internal/kernel/, $m/internal/platform/outbox$]
        constructors:                       # rule 4: one entry per domain
          files: ["$all", "!**/internal/app/**", "!**/cmd/**"]
          deny:
            - pkg: $m/internal/access/assembly
            - pkg: $m/internal/stacks/assembly
            - pkg: $m/internal/runs/assembly
            # ... 19 entries, one per domain
        core-not-adapters:                  # rule 5: one entry per hexagon domain
          files: ["**/internal/*/internal/core/**"]
          deny:
            - pkg: $m/internal/runs/internal/adapters
            - pkg: $m/internal/vcs/internal/adapters
            # ... 8 entries
        shared-below-domains:               # rule 6
          files: ["**/internal/platform/**", "**/internal/kernel/**", "**/internal/iac/**"]
          deny:
            - pkg: $m/internal/access
            - pkg: $m/internal/stacks
            # ... 19 domain roots, then:
            - pkg: $m/internal/agent
            - pkg: $m/internal/app
        agent-isolation:                    # rule 7: an allow list, so a new platform package is refused
          list-mode: lax                    # the longer match wins, so each allow beats the deny
          files: ["**/internal/agent/**", "**/cmd/infraweave/agent.go", "**/cmd/infraweave-agent/**"]
          deny:
            - pkg: $m/internal
            - pkg: $m/pkg
            - pkg: github.com/jackc/pgx
          allow:
            - $m/pkg/flows/app/v1$
            - $m/internal/agent/
            - $m/internal/kernel$
            - $m/internal/kernel/
            - $m/internal/iac$
            - $m/internal/iac/
            - $m/internal/platform/buildinfo$
            - $m/internal/platform/observe$
            - $m/internal/platform/rego$
            - $m/internal/platform/httpkit/client$
            - $m/internal/platform/httpkit/serve$
            - $m/internal/platform/seal$
            - $m/internal/platform/expr$
        devtools:                           # rule 8
          files: ["$all", "!**/tools/**", "!**/internal/devtools/**"]
          deny:
            - pkg: $m/internal/devtools
        public-packages:                    # rule 9
          files: ["**/pkg/**"]
          deny:
            - pkg: $m/internal
```

The graph rules carry rule 2. The compiler already stops imports of
`internal/<other>/internal/...`, and each graph rule allows only the `api/`
packages its domain declares, so the rule is both the import check and the
input of the schema check: code and schema read one list and cannot
disagree. In total there are 26 rules and about 270 entries, the `$` and
`/` pairs included; a new domain
adds its graph rule (about 8 entries), one entry to `constructors`, one to
`shared-below-domains` and, if it is a hexagon, one to `core-not-adapters`.
`agent-isolation` needs no entry, because it allows rather than denies. A
domain directory that lacks any of its entries fails
`TestUnitDepguardFixtures`, and a domain whose tables land without a graph
rule fails `TestAccSchemaDependencies`, because its stream has no place in
the declared graph.

The declared graph at the end of P27, in construction order; each domain
imports the `api/` packages of the domains its row names, and only domains
earlier in the order:

| Domain | Imports the `api/` of | Back edges bound in `internal/app` |
| --- | --- | --- |
| `features` | none | |
| `access` | features | `policiesapi.Evaluator` for `LOGIN` policies |
| `workers` | features, access | `runsapi.Scheduler`, `runsapi.Reports` for the worker protocol |
| `vendors` | features, access | |
| `stacks` | features, access, workers, vendors | `runsapi.StackStatus` |
| `configuration` | features, access, stacks | |
| `policies` | features, access, stacks | |
| `runs` | features, access, stacks, configuration, workers, vendors, policies | `vcsapi.Sources`, `cloudapi.Credentials`, `stateapi.Versions` at handover |
| `state` | features, access, stacks, runs | |
| `cloud` | features, access, stacks | |
| `registry` | features, access, workers, configuration, policies, runs, cloud | |
| `vcs` | features, access, stacks, runs, registry, policies | |
| `schedules` | features, access, stacks, runs | |
| `events` | features, access, workers, stacks, runs, policies, vcs | |
| `templates` | features, access, stacks, runs | |
| `resources` | features, access, stacks, runs, state, workers | |
| `intelligence` | features, access, workers, stacks, policies, runs, cloud | `platform/httpkit/dispatch` for the `query` and `mutate` tools |
| `flows` | features, access, stacks, runs, templates | `platform/httpkit/dispatch` for the API Request block |
| `expressions` | none; `internal/app` hands it every domain's `Surfaces()` | |

A back edge is a call to a domain constructed later, set after
construction; it adds no import and no foreign key. Intent operations and
scan runs reach worker pools through `runsapi`, so `intelligence` and
`resources` import `runs` rather than `workers`' protocol.

Coverage:

- `.testcoverage.yml` excludes `^cmd/`, `^tools/`, `^internal/platform/gen/`,
  `^internal/agent/wire/`, `^pkg/client/` and `^pkg/flows/app/`; the
  console's generated client is excluded from Vitest's coverage the same
  way.
- About 325 packages at the end of P27, most of them `core/` packages with
  real logic. Packages likely to struggle with the 90% floor: `api/`
  packages that hold only interfaces and types (keep constructors and
  validation there so each has something to test, or exclude interface-only
  files), `assembly/` packages (covered by `assembly_test.go`), migration embed
  packages, `platform/httpkit/console`, the Kubernetes executors of both
  agents, and `cmd/infraweave-agent`'s single file, which the `^cmd/`
  exclusion already covers.
- Store and domain tests need PostgreSQL, so they are `TestAcc` suites
  (decision 0016) that run on ubuntu only; the merged profile takes their
  coverage from that run, which `make cover` already merges. Playwright
  runs the binary built with `-cover`, so console journeys add to the
  merged Go profile.

## Walkthroughs

### A GitHub push becomes a proposed run, a plan policy and a PR comment

1. `vcs/internal/adapters/http/webhook.go` (serve): receives the GitHub
   delivery for integration `{id}`; `adapters/github/signature.go` verifies
   `X-Hub-Signature-256`.
2. `vcs/internal/core/intake`: inserts the `vcs_deliveries` row and
   enqueues `vcs.process_delivery` through `platform/queue` in one `platform/tx`
   transaction; the handler answers `202`.
3. `vcs/internal/core/normalize` (scheduler, `vcs.process_delivery` consumer): turns
   the `pull_request` payload into the neutral pull request input and
   upserts the `pull_requests` record.
4. `vcs/internal/core/route`: `stacksapi.Stacks.ByRepository` finds the
   stacks on that repository; for each, `policiesapi.Evaluator.Push`
   (implemented by `policies/internal/policies` over `platform/rego`)
   returns `propose`.
5. `runsapi.Runs.Create` (`runs/internal/core/lifecycle/create.go`):
   dedupe on stack, SHA and `PROPOSED`; the run is inserted in `QUEUED`,
   `run.schedule` enqueued, `RunCreated` appended to the outbox.
6. `vcs/internal/core/feedback` (outbox consumer): posts a queued check
   through `adapters/github/checks.go`.
7. `runs/internal/core/queuegate` (`run.schedule` consumer): proposed runs take
   no stack lock; no approval policy is attached; `READY`.
8. A public-pool worker's long-poll in `workers/internal/adapters/protocol/jobs.go`
   calls `runsapi.Scheduler.Claim`; `runs/internal/core/schedule/claim.go`
   assigns the run and moves it to `PREPARING`; `handover/payload.go` builds
   the job with `vcsapi.Sources.ArchiveURL` and a run token from
   `accessapi.Tokens`.
9. `agent/launcher` starts the runner container through
   `agent/executor/docker`; `agent/exec` runs `INITIALIZING` and
   `PLANNING` with `agent/vendors/opentofu`, reporting each phase to
   `workers/internal/adapters/protocol/reports.go`, which calls
   `runsapi.Reports.Phase`.
10. The worker uploads the plan JSON to its presigned URL and reports it;
    `runs/internal/core/gates/plan.go` parses changes with
    `internal/iac/opentofu`, builds the plan input and calls
    `policiesapi.Evaluator.Plan`: one `warn`.
11. `runs/internal/core/lifecycle/transition.go`: with a warn on a proposed
    run and no approval policy, the run goes to `FINISHED`; receipts, the
    transition and `RunStateChanged` commit together.
12. `events/internal/core/catalogue` (outbox consumer) builds the
    notification input; `policiesapi.Evaluator.Notification` returns a
    `pull_request` route; `vcsapi.Feedback.Comment` posts or edits the
    comment by its deduplication key
    ([08, PR comments](../reference/08-run-lifecycle.md#pr-comments-and-commit-checks)).
13. `vcs/internal/core/feedback` sets the check to success with the change
    count; no audit entry is recorded, because no
    resource changed.

### A tracked run awaits approval and applies on a private worker

1. A push to the tracked branch follows steps 1 to 4 above; the push policy
   returns `track`, and `runs` creates a `TRACKED` run.
2. `runs/internal/core/queuegate/lock.go`: the run takes the stack's run lock
   in `stack_run_locks`; `stack_run_state` becomes `QUEUED` for the stack.
3. `runs/internal/core/gates/approval.go`: no approval needed in `QUEUED`;
   `READY`. The run is on a private pool, so its tier comes from
   `schedule/order.go`.
4. `agent/launcher` on the customer host, registered with the pool's
   certificate, holds a long-poll; `Claim` assigns it; `PREPARING`.
5. `agent/exec` runs `INITIALIZING` and `PLANNING`; the plan has changes; the
   plan policy passes; autodeploy is off, so `Transition` moves the run to
   `UNCONFIRMED`. The workspace is saved to the workspace bucket through a
   presigned URL.
6. `gates/approval.go` evaluates approval policies on entering
   `UNCONFIRMED`: two approvals required, none yet.
7. A reviewer calls `POST /runs/{id}/review` (`runs/internal/adapters/http`):
   `act.go` records the review, re-evaluates approval, both approvals
   arrive, and the run can be confirmed. `vcs/internal/core/feedback`
   updates the GitHub deployment to pending.
8. `POST /runs/{id}/confirm`: `Transition` to `CONFIRMED`, which is
   claimable, and `NOTIFY` wakes a held poll for the pool. `Claim` moves it
   to `PREPARING_APPLY`;
   `handover` sends the saved workspace URL.
9. `agent/exec` runs `APPLYING`; the OpenTofu `http` backend writes state to
   `state/internal/http`, which checks the run token and
   `runsapi.StateAccess` and stores a new version in the states bucket.
10. The worker reports outputs and `FINISHED`; `Transition` releases the
    stack lock, sets `stack_run_state` to `FINISHED` and appends
    `RunFinished`.
11. `gates/trigger.go` evaluates trigger policies and creates tracked runs on
    the stacks they name; `runs/internal/core/dependencies` queues the
    dependants
    ([18, section 4](../reference/18-scheduling-drift-dependencies.md#4-stack-dependencies)).
12. Outbox consumers: `events` delivers stack webhooks and ships the audit entry,
    `vcs` marks the deployment active, `resources` (P22) indexes the managed
    entities, and `flows` (Flows) starts any flow whose Deploy trigger block
    matches the `run_updated` event, as the next walkthrough shows.

### A drift event starts a flow

1. `schedules/internal/schedules` holds the stack's drift detection entry
   in `platform/cron` (scheduler). The entry names the `drift-detection`
   feature, so the features gate lets it fire only while the entitlement
   of the account's organization holds it; with billing off it always
   does. The handler
   calls `runsapi.Runs.Create` for a proposed drift
   detection run.
2. The run follows steps 7 to 10 of the first walkthrough on the stack's
   private pool: `agent/exec` plans with `agent/vendors/opentofu`, and the
   plan has changes.
3. `runs/internal/core/lifecycle/transition.go` moves the run to `FINISHED`
   and, in the same transaction, appends `RunStateChanged` (kind
   `run_updated`) and `DriftDetected` (kind `drift_detected`) through
   `platform/outbox`. Each `events` row carries the shared envelope and a
   document of the kind's schema; `pg_notify` wakes the run state streams.
4. The `events.fanout` job (scheduler, `platform/events`) enqueues one job
   per subscribed consumer: `events`' notification catalogue,
   `vcs/internal/core/feedback`, and `flows/internal/adapters/trigger`.
5. `flows/internal/adapters/trigger` (scheduler) finds the enabled Deploy
   trigger block of kind `drift_detected` whose filter matches the stack,
   and in its job's transaction inserts the root `flow_events` row, linked
   to the Deploy event's ID, with a fixed `now` and seed, and one
   `flows.deliver` job per input linked to the trigger's output. The
   `flows-engine` feature gates the job like any other. No translation
   happens: the trigger block's output is the `drift_detected` document.
6. `flows/internal/core/delivery` (scheduler, logical queue `flows`) loads
   the next block, an app block that posts a message, and evaluates its
   input configuration through `platform/expr`: the text template
   `Drift on ${{ outputs.driftTrigger.stack.name }}`, type-checked at save
   against the `EventDriftDetected` schema, and the installation's
   `secrets.CHAT_TOKEN`, resolved from `flows_secrets`.
7. The block is an app block, so `port.AgentTasks` enqueues
   `flows.agent_task` for the installation's agent pool, carrying the
   evaluated input, never the expression.
8. `flows/internal/adapters/gateway` on the `serve` replica that holds the
   pool's socket claims the job within the agent's free capacity and pushes
   a `task` frame.
9. `agent/flowsagent/apps` reuses the container for that installation and
   app version digest, or starts one through `executor/docker`, and calls
   `HandleEvent` over gRPC with `pkg/flows/app/v1`. The app posts the
   message and returns one emitted event on its `sent` output as a buffered
   effect.
10. The `result` frame reaches the gateway, which completes the
    `flows.agent_task` job and the delivery in one transaction: the emitted
    `flow_events` row, the deliveries it causes, the delivery's completion
    and a `flows.invocation.status` job. The emitted event goes through
    `platform/outbox` as `flows.event`, so audit and streams see it.
11. `flows/internal/adapters/http` streams `flows.event` and
    `invocation_updated` on `/api/v1/flows/flows/{flowId}/events/stream`
    through `platform/httpkit/sse`; the console's open stream updates its
    Query cache and the canvas shows the invocation as `processed`.

Deploy and Flows meet in step 4 and nowhere else: the drift result is one
`events` row with one envelope, and Flows reads it as a fan-out consumer
like the notification catalogue does. Neither side imports the other's
internals, and `events` never imports `flows`.

### Adding an IaC vendor: Pulumi (P20)

1. `api/src/api/`: the Pulumi settings variant of the stack vendor union;
   `make generate`.
2. `internal/kernel/vendor.go`: `VendorPulumi`.
3. `internal/iac/pulumi/pulumi.go`, `settings.go`, `changes.go`
   (preview JSON to the changes model), `policyinput.go` (the `pulumi` block
   of [report 20, section 2](../reference/20-vendors.md#2-plan-policy-input-per-vendor)),
   `pulumi_test.go`, `testdata/`.
4. `internal/iac/registry.go`: register the vendor; `iactest.Run` in
   `pulumi_test.go`.
5. `internal/agent/vendors/pulumi/commands.go`: login, stack select,
   preview, up, destroy, outputs; `commands_test.go`.
6. `internal/agent/vendors/registry.go`: register the command set.
7. `stacks/internal/http/convert.go`: map the generated variant.
8. `docs/data/features.json`: the `vendor-pulumi` option on `StackVendor`, in every
   seeded tier; `make generate` rewrites
   `internal/features/internal/catalogue/catalogue.gen.go`.
9. `deploy/images/runner-pulumi/Dockerfile` and its CI build.
10. `test/acceptance/features/pulumi.feature`, its steps, and a fixture
    project under `test/fixtures/`.

No domain's core changes: stacks validate through `internal/iac`, runs
parse through it, and the agent executes through its own registry. About 12
new files and 5 edited.

### Adding a business domain: templates (P21)

1. `api/src/api/paths-templates.yaml` (scaffolded with `make scaffold`
   from the reference tags); `make generate`.
2. `internal/templates/api/templates.go`: `Deployments` for Flows and
   intelligence; `api/events.go`: `TemplateVersionPublished`,
   `DeploymentChanged`.
3. `internal/templates/internal/templates/`: templates, versions with
   one-way publication, deployments with the eight-value state, change
   records and rollback
   ([report 17](../reference/17-blueprints-and-templates.md#9-template-deployments)).
4. `internal/templates/internal/templates/render.go`: bodies rendered as
   text templates through `platform/expr`, with `now` and the seed stored on
   the change record; `assembly.Surfaces()` contributes the body surface, so
   the editor endpoints check template bodies with no change to
   `expressions`.
5. `internal/templates/internal/pg/` with `migrations/0001_templates.sql`
   (templates, template_versions, template_deployments, changes, blueprints).
6. `internal/templates/internal/http/strict.go`: the partial for the
   Templates and Blueprints tags.
7. `internal/templates/assembly/assembly.go`: `New(Deps{Stacks stacksapi.Stacks,
   Runs runsapi.Runs, Authz accessapi.Authorizer, ...})`; subscribes to
   `RunStateChanged` to compute deployment state.
8. `internal/app/domains.go`, `router.go` (embed the partial),
   `config/config.go` (Settings field).
9. `.golangci.yml`: the `graph-templates` rule (allowing `features/api`,
   `access/api`, `stacks/api` and `runs/api`), and one entry each in
   `constructors` and `shared-below-domains`.
10. `docs/data/features.json`, and
    `internal/features/internal/catalogue/catalogue.gen.go` from it: the
    `templates` and `blueprints` capabilities at Business; the coverage test fails until
    every new operation names one.
11. `test/acceptance/features/templates.feature` and its steps.

Managed stacks are created through `stacksapi.Stacks.Create` with a
`managedBy` reference; `stacks` gains a read-only check for managed stacks,
the only edit to an existing domain. The `templates` tables reference
`stacks` with `ON DELETE RESTRICT`, so its stream registers after the
stacks stream. About 20 new files and 6 edited.

## Package mapping

| Today's package | New location | Notes |
| --- | --- | --- |
| `cmd/` (`cmd/README.md` only) | `cmd/infraweave`, `cmd/infraweave-agent`, later `cmd/infraweavectl` (P24b) | one file per process mode; README kept |
| `api` (`api/doc.go`, `api/deploy_test.go`) | `api` (`api/doc.go`, `api/api_test.go`) | embeds stay, reading `api.openapi.yaml`; the `TestUnitDeploy…` tests become `TestUnitAPI…`; a `Worker` embed joins at P8 |
| `api/src/deploy` | `api/src/api` | grows one `paths-<family>.yaml` per family, Deploy and Flows |
| `api/deploy.openapi.yaml` | `api/api.openapi.yaml` | the bundle of `api/src/api` |
| `api/parity-baseline.json` | `api/parity-baseline.deploy.json` | read by `TestUnitAPIParity`; `api/parity-baseline.flows.json` joins it (Flows) |
| `api/reference/spacelift` | `api/reference/spacelift` | unchanged |
| `internal/buildinfo` | `internal/platform/buildinfo` | `-ldflags` paths in the `Makefile` change |
| `internal/docscheck` | `internal/devtools/docscheck` | `make docs-check` path changes |
| `internal/openapibundle` | `internal/devtools/openapibundle` | |
| `internal/server/gen/deploy` | `internal/platform/gen/api` | regenerated, not moved by hand; package `api` |
| `internal/server/problem` | `internal/platform/httpkit/problem` | used by every domain's HTTP adapter |
| `internal/specref` | `internal/devtools/specref` | |
| `internal/stubgen` | `internal/devtools/stubgen` | |
| `pkg/client/deploy` | `pkg/client/api` | regenerated; `pkg/sdk` wraps it from P24 |
| `tools/openapi-bundle` | `tools/openapi-bundle` | import path of the library changes |
| `tools/specref` | `tools/specref` | import path of the library changes |
| `tools/stubgen` | `tools/stubgen` | import path of the library changes |
| `test/acceptance` | `test/acceptance` | features gain the binary and a worker from P9 |
| `api/oapi-server-deploy.yaml`, `api/oapi-client-deploy.yaml` | `api/oapi-server-api.yaml`, `api/oapi-client-api.yaml` | `output: internal/platform/gen/api/server.gen.go` and `pkg/client/api` |
| `Makefile` | same file | generate and verify-generate paths; ldflags; docs-check path |
| `.golangci.yml` | same file | depguard enabled with the seven fixed rules and one graph rule per domain |
| `.testcoverage.yml` | same file | `^internal/platform/gen/`, `^internal/agent/wire/` replace `^internal/server/gen/` |

## Numbers

Estimates, from the domains of report 26 and the trees above.

| Measure | Today (P1) | End of P27 |
| --- | --- | --- |
| Go packages | 16 (13 today plus `cmd/infraweave`, `internal/app`, `app/config`) | about 325 |
| Top-level directories under `internal/` | 4 (`app`, `kernel`, `platform`, `devtools`) | 25 (6 shared plus 19 domains) |
| Dependency rules | 9 (1 compiler, 8 depguard-checked) | same |
| depguard rules and entries | 7 rules, about 20 entries | 26 rules (7 fixed, 19 graph), about 270 entries |
| Files touched to add a vendor | n/a | about 12 new, 5 edited (the catalogue gains the vendor's option) |
| Files touched to add a domain | n/a | about 20 new, 6 edited |
| Packages a stack update crosses | 4 (`platform/httpkit/middleware`, `stacks/internal/http`, `stacks/internal/stacks`, `stacks/internal/pg`) | 7 (adds the `access` and `features` middleware and `accessapi`) |
| Largest package | `internal/platform/gen/api` (generated, 24k lines) | `runs/internal/core/lifecycle` among hand-written ones, about 5k lines |
| Thin packages at risk under the 90% floor | `app/config` | about 40: `api/` and `assembly/` packages, migration embeds, `httpkit/console`, the Kubernetes executors |

## Placement choices and friction

Placement choices settled in this file:

- **Runs own every table a transition writes.** The run lock, the stack's
  run state, assignments, receipts and outputs are runs tables, so a
  transition is one transaction inside one domain.
- **Policies own the input contract and never call back.** Callers build
  inputs and act on decisions; trigger decisions come back as data.
- **Domain internals sit under a nested `internal/`**, so the compiler
  enforces the main boundary and depguard handles only the rest.
- **The agent tree is outside every domain**, imports no persistence, and
  shares only `internal/kernel`, `internal/iac` and seven platform packages
  (`buildinfo`, `rego`, `httpkit/client`, `seal`, `expr`) with the server,
  so `cmd/infraweave-agent` is a short `main.go`.
- **Vendor knowledge is a pure library**, used by stacks for validation, by
  runs for plan parsing and by the agent for commands.
- **Shared IaC vendor code lives under `internal/iac/`, not `internal/vendor/`**:
  the go command treats any `vendor` path element as a vendor directory, so
  `go list ./...` skips it and imports of it fail.
- **Object storage, sealing, encryption, queue, outbox, expressions and
  OPA are platform**, because several domains use each and none of them
  carry business rules. Sealed formats are split from the keyring so the
  worker side can seal and open without holding a key it should not.
- **One migration stream per domain, foreign keys along the declared
  graph.** A key goes only from a domain to one it imports, never cascades,
  and streams run in dependency order; Flows gets its own schema as well as
  its own stream.
- **`features` sits at the bottom of the graph**, so a limit check is a
  synchronous call inside the creating transaction, and
  `organizations.tier` can reference `tiers`.
- **The event model belongs to the platform**, so Deploy and Flows share
  one envelope, emit path and kind registry, and a Flows trigger is a
  fan-out consumer rather than a bridge.
- **Expression surfaces are contributions**, like routes and consumers, so
  the editor endpoints serve every surface without a domain importing
  another.
- **`allowedActions` is filled by the enforcing code**, so the console
  shows only what the server would accept.
- **The worker protocol client is internal**; `pkg/` holds the generated
  public clients, the SDK and the Flows app SDK.

Where this layout rubs against the system:

- **Stack reads cross into runs.** Stack state, blocker and run lock are
  runs data; a stack list filtered or sorted by state needs a read view
  across two schemas, the one sanctioned cross-domain read. The view lives
  in the `runs` stream and is served through `runsapi.StackStatus`.
- **Run creation reads the stack row inside its transaction**
  (`LockForRun`), a cross-domain transaction that couples `runs` to the
  stacks schema's locking.
- **`runs` is the gravity well.** It holds the machine, gates, scheduler,
  handover, timers, promotion and dependencies: about a fifth of the
  hand-written code, and the domain most phases (P7, P9, P10, P13, P16)
  touch.
- **Back edges need late binding.** `stacks` reads `runsapi`; `runs` reads
  `vcsapi`, `cloudapi` and `stateapi`; `workers` reads `runsapi`; `access`
  reads `policiesapi`. `domains.go` sets six dependencies after
  construction, and binds the operation dispatcher for `flows` and
  `intelligence`.
- **One binary carries server code to worker hosts.** The agent packages do
  not import it, but the linked binary contains it; `cmd/infraweave-agent`
  is the slim build for hosts whose image size or review demands it, and
  rule 7 covers its `main.go` too.
- **The declared graph is lint data with a schema test behind it.** One
  graph rule per domain is about 150 of the 270 depguard entries, and
  every new import edge between domains is a reviewed edit to
  `.golangci.yml`. Foreign keys make the graph harder to bend later: a
  domain that must reference a table against the import direction has to
  move the table, or the code, rather than add a key.
- **The agent's platform list grows with each shared format.** Sealing,
  expressions, observability and the admin listener joined `buildinfo`,
  `rego` and `httpkit/client`; each new
  package the runner, launcher or Flows agent needs is a rule 7 edit, and a
  package that later gains a store or a key would have to split first.
- **`features` is imported by every domain that counts something.** A
  limit check is one call, but `featuresapi` is one of the widest
  dependencies in the graph, so its interfaces have to stay small and
  stable.
- **Expressions span domains through registration.** The editor endpoints
  depend on every domain contributing its surfaces in `Surfaces()`; a
  surface left out type-checks at save in its own domain but has no editor
  support until it is registered.
- **Constructor packages all named `assembly`** need import aliases in
  `internal/app`, and the nested `internal/` makes import paths long
  (`internal/runs/internal/core/lifecycle`).
- **Boundaries for intelligence and flows are drawn before their phases.**
  The tree commits to their existence, their own tables and their `api/`
  seams, and for Flows to its job kinds and the `App` contract, because the
  shared event model starts at P7 and Flows follows P18 directly.
