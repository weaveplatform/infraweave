# Repository layout

This guide is the target package tree and the rules for placing new code. The
shape is [decision 0004](../research/decisions/0004-repository-architecture.md),
which adopts option 5 of the architecture options,
[the modular monolith](../research/architecture-options/5-modular-monolith.md):
business domains under `internal/<domain>/`, each with a published `api/`
package, an `assembly/` constructor package, a private `internal/` tree and its own
migration stream, over shared `platform`, `kernel` and `iac` trees, with every
worker-side process in `internal/agent`. Phase status lives in the
[implementation plan](plan.md); the [definition of done](definition-of-done.md)
holds the checks every pull request passes.

Module: `github.com/weaveplatform/infraweave`. One Go module, two release
binaries (`infraweave` and `infraweavectl`), and developer commands under
`tools/`. Builds run with `GOWORK=off`, so a local `go.work` never hides a
dependency missing from `go.mod`.

Today's tree holds the served skeleton and the reference tooling in their
pre-0004 places (`internal/server/...`, `internal/specref` and so on). The move
into this layout, the depguard rules and the path changes in `Makefile`,
`.golangci.yml` and `.testcoverage.yml` are
[P1b](plan.md#p1b-domain-layout-and-depguard). Until P1b lands, new code goes
where this guide places it, not beside today's packages.

## Annotated tree

`(Pn)` marks the plan phase that creates a package or directory, as the
[plan](plan.md) assigns it. A package with no mark either exists today (`← path` names where)
or arrives with the first phase that uses it, which the [plan](plan.md) names.

```text
infraweave/
├── cmd/
│   ├── infraweave/                one binary, one cobra file per process mode; excluded from coverage
│   │   ├── main.go                cobra root; binds setting flags only, never --config or --help
│   │   ├── serve.go               app.Serve
│   │   ├── scheduler.go           app.Scheduler
│   │   ├── all.go                 app.All: serve and scheduler in one process
│   │   ├── migrate.go             migrate up, migrate status: app.Migrate, then exit (P2)
│   │   ├── secrets.go             secrets generate-kek, rewrap, rotate (P2b)
│   │   ├── version.go             platform/buildinfo
│   │   ├── launcher.go            agent/launcher.Run; imports no internal/app (P8)
│   │   ├── runner.go              hidden runner: agent/exec.Main inside the run container (P9)
│   │   ├── vcsagent.go            vcs-agent: agent/vcsagent.Run (P19b)
│   │   └── flowsagent.go          flows-agent: agent/flowsagent.Run (P26b)
│   └── infraweavectl/             the CLI over pkg/client/deploy; a client, not a process mode (P24b)
├── api/                           specifications; the source of truth for every served API
│   ├── src/deploy/                editable fragments: info.yaml, components-common.yaml, paths-<family>.yaml
│   ├── src/worker/                worker protocol fragments (P8)
│   ├── src/flows/                 Flows API fragments (P26)
│   ├── deploy.openapi.yaml        bundled by make bundle; committed; drift-checked
│   ├── oapi-server-<spec>.yaml    oapi-codegen server config; output internal/platform/gen/<spec>
│   ├── oapi-client-<spec>.yaml    oapi-codegen client config; output pkg/client/<spec> or internal/agent/wire
│   ├── doc.go deploy_test.go      embeds of the bundles and the spec checks
│   └── reference/
│       ├── spacelift/             the Spacelift specification reference and its snapshots; generated
│       └── provider-parity.yaml   Terraform provider renames, type changes and omissions (P24)
├── internal/
│   ├── app/                       composition root: serve, scheduler, all, migrate
│   │   ├── config/                strict YAML (UnmarshalExact), flag > env > file; one Settings field per domain
│   │   ├── platform.go            open PostgreSQL, migrate every stream, object stores, keyring, queue, outbox, cron
│   │   ├── domains.go             construct domains in dependency order; late-bind the three back edges
│   │   ├── router.go              chi router, middleware, strict servers composed by embedding
│   │   ├── discovery.go           /.well-known/terraform.json from domain contributions (P11, P17)
│   │   └── modes.go               which domain parts (Routes, Consumers, Cron) each process mode starts
│   ├── devtools/                  libraries behind tools/; never linked into cmd/
│   │   ├── specref/               reference generation, scaffold, inventory, parity       ← internal/specref
│   │   ├── openapibundle/         fragment bundler                                         ← internal/openapibundle
│   │   ├── stubgen/               501 UnimplementedStrict generator                        ← internal/stubgen
│   │   └── docscheck/             links, report headers, decision statuses, plan structure ← internal/docscheck
│   ├── kernel/                    value types shared by domain APIs; parsing only, no I/O
│   │   ├── ids.go                 ULID, Slug, UUID (Flows)
│   │   ├── account.go space.go    AccountID, SpaceID, the root space
│   │   ├── principal.go           user, API key, run, worker, stack, Flows project
│   │   ├── labels.go              labels and autoattach: matching
│   │   ├── run.go                 RunState (22 values), RunType, StackState, RunPriority wire names
│   │   ├── vendor.go              VendorKind
│   │   ├── audit.go               AuditEntry
│   │   ├── page.go                Cursor and Page of decision 0003
│   │   ├── search/                SearchInput predicate, order and cursor types (P5)
│   │   └── vcsroutes/             provider API route tables shared by vcs and agent/vcsagent (P19b)
│   ├── platform/                  infrastructure with no business rules
│   │   ├── buildinfo/             version, commit, build date stamped by make build        ← internal/buildinfo
│   │   ├── sql/                   pgx pool and the store conventions of decision 0005 (P2)
│   │   │   ├── migrate/           embedded streams, schema_migrations keyed by stream, one advisory lock
│   │   │   ├── notify/            LISTEN and NOTIFY fan-out between replicas (P2)
│   │   │   ├── search/            compiles SearchInput predicates to SQL under Reachable (P5)
│   │   │   └── sqltest/           testcontainers PostgreSQL for store and domain tests
│   │   ├── tx/                    Tx(ctx, fn), AfterCommit; the unit of work on the context (P2)
│   │   ├── audit/                 Record(ctx, kernel.AuditEntry) in the caller's transaction
│   │   ├── outbox/                Append(ctx, Event) in the caller's transaction (P2c)
│   │   ├── events/                Subscribe(kind, consumer, Handler); one job per consumer (P2c)
│   │   ├── queue/                 decision 0011's PostgreSQL job queue (P2c)
│   │   │   └── queuetest/         contract suite, TestAcc
│   │   ├── cron/                  five-field grammar; ticker claims due rows with SKIP LOCKED (P2c)
│   │   ├── objectstore/           Put, Get, versions, presigned GET and PUT (P2b)
│   │   │   ├── s3/ gcs/ azureblob/ fs/
│   │   │   └── objectstoretest/   contract suite: TestAcc on MinIO, TestUnit on fs
│   │   ├── crypt/                 decision 0013's envelope encryption, seal and open (P2b)
│   │   │   └── kek/               local, awskms, gcpkms, azurekv, vault
│   │   ├── features/              decision 0021's BillingTierFeature switch middleware
│   │   ├── rego/                  embedded OPA v1: prepared queries, capabilities, timeouts (P13)
│   │   ├── httpkit/
│   │   │   ├── problem/           RFC 9457 problems, decision 0003 slugs                   ← internal/server/problem
│   │   │   ├── middleware/        request ID, recovery, body limits, idempotency keys, rate limits
│   │   │   ├── page/              cursor encode and decode for list operations
│   │   │   ├── client/            outbound HTTP with retries; agent and adapters use it
│   │   │   └── strict/            principal and problem mapping, merge-patch and If-Match for strict handlers (P5)
│   │   ├── gen/                   GENERATED strict servers, one package per spec
│   │   │   ├── deploy/            server.gen.go, unimplemented.gen.go                      ← internal/server/gen/deploy
│   │   │   ├── worker/            (P8)
│   │   │   └── flows/             (P26)
│   │   ├── observe/               slog, OpenTelemetry, /metrics, readiness on its own port (P2, P18b)
│   │   └── testkit/               fake clock, fake object store, HTTP recorder helpers
│   ├── iac/                       pure IaC vendor knowledge shared by server and agent (P5, P9, P20, P20b)
│   │   ├── iac.go registry.go     Vendor: Kind, ValidateSettings, Workflow, ParsePlan; Kind → Vendor
│   │   ├── terraform/ opentofu/   settings validation (P5); plan parsing (P9)
│   │   ├── terragrunt/ pulumi/ cloudformation/ kubernetes/ (P20); ansible/ (P20b)
│   │   └── iactest/               contract suite every vendor passes (P5)
│   ├── agent/                     runs on customer hosts; links no domain and no server persistence (P8)
│   │   ├── config/                launcher settings: pool token, comms URL, executor, mounts
│   │   ├── launcher/              register, long-poll jobs and signals, heartbeat, drain
│   │   ├── wire/                  GENERATED worker protocol client
│   │   ├── executor/docker/       one run container per job, own binary mounted (P8)
│   │   ├── executor/kubernetes/   WorkerPool and Worker controller, pod per run (P14)
│   │   ├── exec/                  runner: phases, hooks, workspace, log streaming (P9)
│   │   ├── vendors/               command sets per vendor (P9, P20, P20b)
│   │   ├── initpolicy/            worker-side initialization policy over platform/rego (P14)
│   │   ├── mask/                  log masking of decision 0013 (P9)
│   │   ├── tools/                 tool binary download, verification and caching (P9)
│   │   ├── vcsagent/              long-polls /vcs-agent/v1 on serve (P19b)
│   │   ├── flowsagent/            dials the Flows gateway on serve (P26b)
│   │   └── agenttest/             fake server over the generated worker spec
│   │
│   │   Business domains. Hexagon domains split internal/core from internal/adapters;
│   │   flat domains hold one logic package, an http package and a pg package.
│   │
│   ├── access/                    hexagon: accounts, users, groups, API keys, sessions, tokens, spaces, RBAC (P3 to P4b, P13b, P23)
│   │   ├── api/                   Authenticator, Authorizer, Spaces, Tokens, identity reads, events
│   │   ├── assembly/              New(Deps); Routes; Consumers; Cron; Close; DeployAPI alias
│   │   └── internal/
│   │       ├── core/              account apikey session token (P3); user group (P3b); space authz (P4); role binding (P4b);
│   │       │                      login (P13b); sso scim mfa (P23); port
│   │       └── adapters/          http (incl. login.v1, P17); pg; saml oidc (P23)
│   ├── stacks/                    flat: stacks, settings, hooks, labels, human lock (P5); dependency (P16)
│   │   └── api/ assembly/ internal/{stacks,dependency,http,pg}/
│   ├── configuration/             flat: config elements, contexts, attachments, auto-attach (P6)
│   │   └── api/ assembly/ internal/{configuration,http,pg}/
│   ├── runs/                      hexagon: owns every table a run transition writes (P7, P9, P10, P16)
│   │   ├── api/                   Runs, Scheduler, Reports, StackStatus, StateAccess, events
│   │   ├── assembly/
│   │   └── internal/
│   │       ├── core/              machine lifecycle gates queuegate schedule handover timers
│   │       │                      promote replan forceapply dependencies port
│   │       └── adapters/          http (Runs tag, log streaming); pg (runs, transitions, receipts,
│   │                              assignments, stack_run_locks, stack_run_state, stack_outputs)
│   ├── workers/                   hexagon: pools, workers, credentials, worker protocol endpoints (P8, P14)
│   │   └── api/ assembly/ internal/{core/{pool,worker,credential,drain,waiter,port},adapters/{http,protocol,pg}}/
│   ├── vendors/                   flat: tool version catalogue (P5, P20)
│   │   └── api/ assembly/ internal/{vendors,http,pg}/
│   ├── state/                     flat: Terraform http backend, versions, locks, import, rollback (P8b, P11)
│   │   └── api/ assembly/ internal/{state,backend,tfe,http,pg}/
│   ├── vcs/                       hexagon: integrations, webhooks, routing, feedback (P12, P19, P19b)
│   │   └── api/ assembly/ internal/{core/{integration,intake,normalize,route,feedback,pullrequest,port},
│   │                              adapters/{http,github,gitlab,bitbucketcloud,bitbucketdc,azuredevops,rawgit,agentgateway,pg}}/
│   ├── policies/                  flat: policies, attachments, inputs, combination, samples, simulation (P13, P13b)
│   │   └── api/ assembly/ internal/{policies,inputs,combine,sample,http,pg}/
│   ├── cloud/                     flat: AWS, Azure, GCP integrations, attachments, OIDC run tokens (P15)
│   │   └── api/ assembly/ internal/{cloud,oidc,aws,azure,gcp,http,pg}/
│   ├── schedules/                 flat: scheduled runs, tasks, deletes, drift detection (P16)
│   │   └── api/ assembly/ internal/{schedules,http,pg}/
│   ├── registry/                  hexagon: modules, versions, test cases, providers, GPG keys (P17, P17b)
│   │   └── api/ assembly/ internal/{core/{module,version,testcase,provider,gpg,sharing,port},adapters/{http,protocol,pg}}/
│   ├── events/                    hexagon: inbox, named and stack webhooks, audit trail, Slack, Teams (P18, P18b)
│   │   └── api/ assembly/ internal/{core/{catalogue,inbox,webhook,delivery,audit,metrics,port},adapters/{http,slack,pg}}/
│   ├── templates/                 flat: blueprints, templates, versions, deployments (P21)
│   │   └── api/ assembly/ internal/{templates,render,http,pg}/
│   ├── resources/                 flat: managed entities, scans, generic search (P22, P22b)
│   │   └── api/ assembly/ internal/{resources,search,scans,http,pg}/
│   ├── intelligence/              hexagon: AI integrations, Infra Assistant, Intent, MCP (P25, P25b)
│   │   └── api/ assembly/ internal/{core/{aiintegration,assistant,intent,tools,port},adapters/{http,mcp,providers,pg}}/
│   └── flows/                     hexagon: Flows with its own tenancy and PostgreSQL schema (P26, P26b)
│       └── api/ assembly/ internal/{core/{org,project,flow,block,invocation,event,app,agentpool,secret,datatable,port},
│                                  adapters/{http,gateway,mcp,pg}}/
├── pkg/
│   └── client/
│       ├── deploy/                GENERATED deploy client; infraweavectl and the Terraform provider import it
│       └── flows/                 GENERATED Flows client (P26)
├── test/
│   ├── acceptance/                godog features, steps and the harness; ubuntu only
│   │   ├── features/              <area>_<topic>.feature, tagged with dependencies and @op:<operationId>
│   │   ├── images.go              every container image, pinned by digest
│   │   └── out/<scenario>/        logs and database dump of a failed scenario; CI artefact, not committed
│   ├── fixtures/                  Terraform and OpenTofu projects, Git repositories, policy corpus (P9)
│   │   └── captures/C<n>/         meta.yaml, request.graphql, response.json, expect.yaml (report 30 captures; P1c)
│   └── terraform/<resource>/      provider acceptance configurations, translated from Spacelift's (P24)
├── tools/                         thin mains over internal/devtools; excluded from coverage
│   ├── specref/                   fetch, generate, check, scaffold, inventory, parity, provider-parity
│   ├── openapi-bundle/
│   └── stubgen/
├── deploy/                        decision 0020's packaging and reference deployments (P27)
│   ├── images/                    image definitions: runner-terraform and runner-opentofu (P9), the other
│   │                              runners (P20, P20b), flows-runtime (P26b), the server and FIPS images (P27)
│   ├── compose/compose.yaml       one infraweave all and one infraweave launcher on Docker
│   ├── helm/infraweave/           chart for serve and scheduler, one Deployment per process mode
│   ├── helm/infraweave-workers/   chart for infraweave launcher
│   ├── terraform/modules/<cloud>/ aws, azure, google
│   ├── terraform/examples/<cloud>/<scenario>/
│   └── check.sh                   static checks of every deployment root, run in the quality gate
├── docs/                          research, decisions, architecture options, this implementation plan
├── Makefile                       every gate target; generate writes internal/platform/gen and internal/agent/wire
├── .golangci.yml                  the only linter; depguard rules below
├── .testcoverage.yml              95% total, 90% per package; generated paths excluded
└── go.mod                         one module, full patch Go version
```

No decision yet places the Terraform provider's source, here or in a
repository of its own (the plan's
[open items](plan.md#open-items)). Either way it imports
`pkg/client/deploy`, and this repository holds its parity oracle
(`api/reference/spacelift/terraform-schema.json`), its mapping file
(`api/reference/provider-parity.yaml`) and its acceptance configurations
under `test/terraform/`.

## Where today's packages go

From option 5's
[package mapping](../research/architecture-options/5-modular-monolith.md#package-mapping).
[P1b](plan.md#p1b-domain-layout-and-depguard) does the moves in one pull request
with no behaviour change.

| Today | Target | What else changes |
|---|---|---|
| `cmd/` (`README.md` only) | `cmd/infraweave`, then `cmd/infraweavectl` (P24b) | one file per process mode |
| `api`, `api/src/deploy`, `api/reference/spacelift` | unchanged | `api/src/deploy` grows one `paths-<family>.yaml` per family |
| `api/oapi-server-deploy.yaml` | unchanged | `output: internal/platform/gen/deploy/server.gen.go` |
| `internal/buildinfo` | `internal/platform/buildinfo` | `LDFLAGS` paths in `Makefile` |
| `internal/server/gen/deploy` | `internal/platform/gen/deploy` | regenerated by `make generate`, not moved by hand |
| `internal/server/problem` | `internal/platform/httpkit/problem` | every domain's HTTP adapter imports it |
| `internal/specref` | `internal/devtools/specref` | `tools/specref` import path |
| `internal/openapibundle` | `internal/devtools/openapibundle` | `tools/openapi-bundle` import path |
| `internal/stubgen` | `internal/devtools/stubgen` | `tools/stubgen` import path; `make generate` stub input path |
| `internal/docscheck` | `internal/devtools/docscheck` | `make docs-check` package path |
| `pkg/client/deploy` | unchanged | |
| `tools/openapi-bundle`, `tools/specref`, `tools/stubgen` | unchanged | library import paths |
| `test/acceptance` | unchanged | runs the built binary as a subprocess from P1c |
| `Makefile` | unchanged path | `generate` and `verify-generate` paths, `LDFLAGS`, `docs-check` path |
| `.golangci.yml` | unchanged path | depguard enabled with the six rules |
| `.testcoverage.yml` | unchanged path | `^internal/platform/gen/` and `^internal/agent/wire/` replace `^internal/server/gen/` |

### Package names in the decision records

Decisions 0005 to 0022 name packages by responsibility. Decision 0004 places
them; read a record's package name through this table.

| Named in a record | Place in this layout |
|---|---|
| `internal/store`, `internal/store/migrations` | `internal/platform/sql`, `internal/platform/sql/migrate`; each domain's SQL files under its `pg/migrations` |
| `internal/store/storetest` | `internal/platform/sql/sqltest` plus each domain's `pg` store tests |
| `internal/queue`, `internal/queue/queuetest`, `internal/cron` | `internal/platform/queue`, `internal/platform/queue/queuetest`, `internal/platform/cron` |
| `internal/objectstore`, `internal/objectstore/objectstoretest` | `internal/platform/objectstore`, `internal/platform/objectstore/objectstoretest` |
| `internal/secrets` | `internal/platform/crypt` |
| `internal/secrets/mask` | `internal/agent/mask` |
| `internal/policy`, `internal/policy/input`, `internal/policy/combine.go` | `internal/platform/rego` for the engine; `internal/policies` for types, inputs, combination, receipts and sampling |
| `internal/run`, `internal/workerapi` | `internal/runs`; `internal/workers` (the `/worker/v1` adapter) |
| `internal/vcs/<provider>`, `internal/vcs/agent` | `internal/vcs/internal/adapters/<provider>`; `internal/agent/vcsagent`, with route tables in `internal/kernel/vcsroutes` |
| `internal/authn`, `internal/authz` | `internal/access` |
| `internal/authz/actions.yaml`, `internal/authz/actions_gen.go` | `internal/access/api`, because callers name an action when they call the authorizer |
| `internal/statebackend`, `internal/registry` | `internal/state`, `internal/registry` |
| `internal/events`, `internal/audit` | `internal/events`; the outbox in `internal/platform/outbox`, the audit write in `internal/platform/audit` |
| `internal/flows`, `internal/flows/agent` | `internal/flows`, `internal/agent/flowsagent` |
| `internal/ai`, `internal/ai/tools`, `internal/intent` | `internal/intelligence` |
| `internal/features` | `internal/platform/features`: an operator switch with no business rule |
| `internal/problem`, `internal/buildinfo`, `internal/telemetry`, `internal/clock` | `internal/platform/httpkit/problem`, `internal/platform/buildinfo`, `internal/platform/observe`, `internal/platform/testkit` |

A package no row names joins `internal/platform` when it holds no business
rule, and the domain that owns its rule otherwise.

## Code placement

| Code | Place | Rule |
|---|---|---|
| A business domain | `internal/<domain>/` | One directory per domain in the tree above; a new domain gets the four-part skeleton |
| What other domains call | `internal/<domain>/api/` | Interfaces, input and output types and events only; imports `internal/kernel`, `internal/platform/outbox` and the standard library |
| Domain assembly | `internal/<domain>/assembly/` | `New(Deps)`, `Routes`, `Consumers`, `Cron`, `Close`, and a `DeployAPI` alias of the domain's strict partial; only `internal/app` and `cmd/` import it |
| Domain logic | `internal/<domain>/internal/core/...` (hexagon) or `internal/<domain>/internal/<domain>/` (flat) | Core never imports adapters |
| Rules with no I/O | a package of their own inside the owning domain | The run transition table, priority tiers, policy combination and version numbering are tested as tables |
| HTTP handlers | `internal/<domain>/internal/.../http` | A strict-server partial for the domain's operations; hand-written chi routes for protocols that are not the REST API |
| SQL | `internal/<domain>/internal/.../pg` | The domain's store and its `migrations/` stream; no foreign key crosses a domain boundary |
| Provider shapes | one package per provider inside the owning domain | Each VCS provider's webhook and API shapes stay in `vcs/internal/adapters/<provider>` behind one normalised event |
| Infrastructure | `internal/platform/...` | Anything with no business rule: SQL, unit of work, outbox, queue, cron, object storage, encryption, HTTP kit, OPA runtime, generated servers, observability |
| Value types across domain APIs | `internal/kernel/...` | Parsing only, no I/O |
| IaC vendor knowledge | `internal/iac/<vendor>` | Pure; the server validates and parses with it, the agent runs commands with it |
| Code that runs on a customer host | `internal/agent/...` | Never a domain, SQL, the queue, object storage credentials or encryption |
| Wiring and process modes | `internal/app/...` | The only place that sees every domain; back edges are bound after construction |
| Entry points | `cmd/infraweave`, `cmd/infraweavectl` | Thin cobra shims; logic belongs in `internal/` |
| Developer commands | `tools/<name>` over `internal/devtools/<name>` | `internal/devtools` is imported only by `tools/` and its own tests |
| Public Go API | `pkg/client/<spec>` | Generated clients only; nothing under `pkg/` imports `internal/` |

Further rules:

- **Consistency draws the boundary.** The `runs` domain owns every table a run
  transition writes, including the stack's run lock and run state, and commits
  a transition in one transaction. A stack list filtered by run state reads
  across the two schemas through one sanctioned read view.
- **Cross-domain effects.** Queries, authorisation and policy decisions are
  synchronous calls on `api/` interfaces. Consequences are events appended to
  the outbox in the producer's transaction. `policies` owns the input
  contracts and imports none of its callers.
- **No `vendor` path element.** The go command treats a directory named
  `vendor` as vendored code, so IaC vendor code lives under `internal/iac` and
  `internal/agent/vendors`, and the catalogue domain is `internal/vendors`.
- **Hand-written protocols.** VCS webhooks, the Terraform `http` state backend,
  `tfe`, service discovery, `modules.v1`, `providers.v1` and `login.v1` are chi
  routes the owning domain contributes through `Routes`.
- **One operation, one owner.** `internal/app/router.go` composes the strict
  server by embedding each domain's partial at depth one and
  `UnimplementedStrict` at depth two, so an operation two domains implement
  fails to compile.
- **The launcher imports `internal/agent` only.** `cmd/infraweave/launcher.go`
  is the one place the worker path meets the rest of the binary.

## Naming

- **Domains** are plural nouns as in the tree (`stacks`, `runs`,
  `policies`), except where the domain is a mass noun (`access`, `state`,
  `configuration`, `registry`, `intelligence`). The constructor package of
  every domain is `assembly`; `internal/app` imports it under an alias
  (`stacksassembly`, `runsassembly`).
- **Provider and vendor packages** use the product name in lower case without
  separators: `github`, `gitlab`, `bitbucketcloud`, `bitbucketdc`,
  `azuredevops`, `opentofu`, `cloudformation`.
- **Generated Go files** end in `.gen.go`, so lint and coverage exclusions
  match them; generated directories are `internal/platform/gen/<spec>`,
  `internal/agent/wire` and `pkg/client/<spec>`.
- **Spec fragments** are `api/src/<spec>/paths-<family>.yaml`, one per family,
  created with `make scaffold TAG=<reference tag> FAMILY=<family>`; shared
  schemas stay in `components-common.yaml`. An `operationId` keeps the
  reference's name, so the parity check matches it.
- **Migrations** are `NNNNN_snake_case.sql`, five digits, gap-free within a
  domain's stream, under `internal/<domain>/internal/.../pg/migrations/`
  ([0005](../research/decisions/0005-persistence-postgresql.md#migrations)).
  Migrations are forward only; a file that must run outside a transaction
  starts with `-- infraweave:no-transaction`.
- **Fakes of external HTTP APIs** live in a package named `<client>fake` beside
  the client, built on a chi router with selectable fault modes.

## Tests and fixtures

From [decision 0016](../research/decisions/0016-testing-and-acceptance.md) and
option 5's
[tests and enforcement](../research/architecture-options/5-modular-monolith.md#tests-and-enforcement).

| Kind | Name | Location | Runs on |
|---|---|---|---|
| Unit | `TestUnit<Name>` | beside the code in `_test.go`; `core/` packages use fakes of other domains' `api` | ubuntu, macOS, Windows |
| Store | `TestAcc<Name>` | `internal/<domain>/internal/.../pg/*_test.go` through `internal/platform/sql/sqltest` | ubuntu |
| Domain | `TestAcc<Name>` | `internal/<domain>/assembly/assembly_test.go`: the domain on `sqltest` with fake `api` dependencies | ubuntu |
| Contract suites | `TestAcc` (local disk `TestUnit`) | `internal/platform/queue/queuetest`, `internal/platform/objectstore/objectstoretest`, `internal/iac/iactest`, `internal/agent/agenttest` | ubuntu; local disk everywhere |
| Machine | `TestUnit<Name>` | `internal/runs/internal/core/machine`: every transition row and every illegal pair | all three |
| Policy corpus | `TestUnitPolicyLibrary` | `internal/policies/internal/combine` and `internal/platform/rego` over the example library | all three |
| Acceptance | `TestAccFeatures` | `test/acceptance/features/<area>_<topic>.feature`, godog `Strict: true` | ubuntu |
| Captures | `TestAccCaptures`, `TestUnitCaptureRedaction` | `test/fixtures/captures/C<n>/` | ubuntu; redaction everywhere |
| Provider | `TestAccProvider` | `test/terraform/<resource>/` | ubuntu |
| CLI | `TestAccFeatures` | `test/acceptance/features/cli_<area>.feature`, one per row of report 06 section 8 | ubuntu |

- **Kind by name.** The kind belongs to the test function, not the package. A
  top-level `Test` function that is neither `TestUnit…` nor `TestAcc…` fails
  `TestUnitTestNames`.
- **Feature files** are named `<area>_<topic>.feature`, where `<area>` is the
  domain or concern (`runs_terraform.feature`, `events_webhooks.feature`,
  `authz_spaces.feature`). A scenario carries dependency tags (`@postgres`,
  `@objectstore`, `@terraform`, `@tofu`, `@oidc`, `@vcs`, `@ai`,
  `@kubernetes`, `@flows-runtime`) and one `@op:<operationId>` tag per
  operation it exercises.
- **Capture fixtures** hold `meta.yaml`, `request.graphql`, `response.json` and
  `expect.yaml` per capture
  ([0016](../research/decisions/0016-testing-and-acceptance.md#capture-fixtures)).
- **Package fixtures** stay in `testdata/` beside the package.
- **Time** comes from the fake clock in `internal/platform/testkit`; code that
  waits is tested under `testing/synctest`.

## depguard rules

The compiler's `internal` rule keeps every `internal/<domain>/internal/...`
package private to its domain. golangci-lint's depguard enforces the rest, with
these rules from option 5's
[tests and enforcement](../research/architecture-options/5-modular-monolith.md#tests-and-enforcement)
section, where `$m` is `github.com/weaveplatform/infraweave`.

| Rule | Files | Mode | Entries |
|---|---|---|---|
| `api-packages` (rule 3) | `**/internal/*/api/**` | strict allow | `$gostd`, `$m/internal/kernel`, `$m/internal/platform/outbox` |
| `constructors` (rule 4) | everything except `**/internal/app/**` and `**/cmd/**` | deny | `$m/internal/<domain>/assembly` for each of the 17 domains |
| `core-not-adapters` (rule 5) | `**/internal/*/internal/core/**` | deny | `$m/internal/<domain>/internal/adapters` for each of the 8 hexagon domains |
| `shared-below-domains` (rule 6) | `**/internal/platform/**`, `**/internal/kernel/**`, `**/internal/iac/**` | deny | the 17 domain roots, `$m/internal/agent`, `$m/internal/app` |
| `agent-isolation` (rule 7) | `**/internal/agent/**` | deny | `$m/internal/platform/{sql,tx,outbox,audit,queue,objectstore,crypt}`, `github.com/jackc/pgx`, the 17 domain roots, `$m/internal/app` |
| `devtools` (rule 8) | everything except `**/tools/**` and `**/internal/devtools/**` | deny | `$m/internal/devtools` |

Rule 2 (a domain reaches another only through its `api/`) needs no entry: the
compiler stops imports of another domain's `internal/`, and `constructors`
stops imports of its `assembly/`. A new domain adds one entry to `constructors`,
`shared-below-domains` and `agent-isolation`, and one to `core-not-adapters` if
it is a hexagon. `internal/agent` may import only `internal/kernel`,
`internal/iac`, `internal/platform/buildinfo`, `internal/platform/rego` and
`internal/platform/httpkit/client` from outside its own tree. The 17 domains
are `access`, `stacks`, `configuration`, `vendors`, `workers`, `policies`,
`runs`, `state`, `vcs`, `cloud`, `schedules`, `registry`, `events`,
`templates`, `resources`, `intelligence` and `flows`; the 8 hexagons are
`runs`, `vcs`, `workers`, `access`, `registry`, `events`, `intelligence` and
`flows`.

## Generated code

Generated files are committed and drift-checked; nobody edits them by hand.

| Output | Source | Regenerate | Verify |
|---|---|---|---|
| `api/<spec>.openapi.yaml` | `api/src/<spec>/*.yaml` | `make bundle` (part of `make generate`) | `make verify-generate` |
| `api/src/deploy/paths-<family>.yaml` (first draft) | a reference tag | `make scaffold TAG=<tag> FAMILY=<family>`, then edited by hand | reviewed; the bundle is verified |
| `internal/platform/gen/<spec>/server.gen.go` | the bundle, `api/oapi-server-<spec>.yaml` | `make generate` | `make verify-generate` |
| `internal/platform/gen/<spec>/unimplemented.gen.go` | the generated server | `make generate` (stubgen) | `make verify-generate` |
| `pkg/client/<spec>`, `internal/agent/wire` | the bundle, `api/oapi-client-<spec>.yaml` | `make generate` | `make verify-generate` |
| `api/reference/spacelift/spacelift.openapi.yaml`, `coverage.md` | the snapshots and `rest-mapping.yaml` | `make reference` | `make verify-reference` |
| `api/reference/spacelift/` snapshots and `sources.lock.yaml` | upstream at the pinned versions | `make reference-fetch` (network) | `openapi-spec-update.yml` |
| [`docs/research/reference/29-data-model-inventory.md`](../research/reference/29-data-model-inventory.md) | the introspection snapshot | `make reference-inventory` | `make verify-reference` |
| `internal/access/api/actions_gen.go` | the `Action` enum of the introspection snapshot | `specref` | `make verify-reference` ([0007](../research/decisions/0007-authorization-spaces-and-roles.md)) |
| `api/reference/provider-parity.yaml` | hand-maintained mapping (P24) | none | `specref provider-parity` |

One generated `StrictServerInterface` spans many domains. `internal/app`
composes it by embedding, and each domain's `assembly/` package re-exports its
partial as `DeployAPI`, because `internal/app` cannot import the domain's
`internal/.../http` package
([generated code](../research/architecture-options/5-modular-monolith.md#generated-code)).
