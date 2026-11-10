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
binaries (`infraweave` and `infraweavectl`), an optional slim
`infraweave-agent` build for worker hosts, the console's TypeScript source
under `web/console/`, and developer commands under `tools/`. Builds run with `GOWORK=off`, so a local `go.work` never hides a
dependency missing from `go.mod`.

The tree follows this layout, and the [depguard rules](#depguard-rules)
hold it there: `make lint` fails on an import they forbid.

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
│   │   ├── auth.go                auth hash-password for the break-glass login (P3)
│   │   ├── version.go             platform/buildinfo
│   │   └── agent.go               registers agent/cli: launcher (P8), hidden runner (P8 stub, P9), vcs-agent (P19b),
│   │                              flows-agent (P18d); imports no internal/app
│   ├── infraweave-agent/          optional slim build: registers the same agent/cli commands; links only internal/agent
│   └── infraweavectl/             main.go only: runs internal/ctl's command tree; a client, not a process mode
├── api/                           specifications; the source of truth for every served API
│   ├── src/api/                   editable fragments: info.yaml, components-common.yaml, paths-<family>.yaml;
│   │                              Deploy and Flows families alike, Flows under /api/v1/flows/ (P18c)
│   ├── src/worker/                worker protocol fragments (P8)
│   ├── src/operator/              operator API fragments: organizations and break-glass (P3); tiers, grants,
│   │                              billing, queue (P3b)
│   ├── src/registry/              registry protocol description for contract tests; its routes are hand-written (P17)
│   ├── proto/flows/app/v1/        the versioned App and Host gRPC services of Flows apps (P18d)
│   ├── api.openapi.yaml           the served document, Deploy and Flows; bundled by make bundle; committed;
│   │                              drift-checked
│   ├── oapi-server-<spec>.yaml    oapi-codegen server config; output internal/platform/gen/<spec>
│   ├── oapi-client-<spec>.yaml    oapi-codegen client config; output pkg/client/<spec> or internal/agent/wire
│   ├── doc.go api_test.go         embeds of the bundles and the spec checks
│   ├── parity-baseline.*.json     accepted parity gaps per reference: .deploy.json (TestUnitAPIParity),
│   │                              .flows.json (TestUnitFlowsParity, P1d)
│   └── reference/
│       ├── spacelift/             the Spacelift specification reference and its snapshots; generated
│       ├── spacelift-flows/       the Flows reference: provider and flowctl RPC paths projected to REST
│       │                          by rest-mapping.yaml into flows.openapi.yaml and coverage.md; generated (P1d)
│       │                          from the snapshots provider-rpcs.json, flowctl-rpcs.json and
│       │                          sdk-identifiers.json, with the hand-written schemas.yaml for flow YAML
│       └── provider-parity.yaml   Terraform provider renames, type changes and omissions (P24)
├── internal/
│   ├── app/                       composition root: serve, scheduler, all, migrate
│   │   ├── config/                strict YAML (UnmarshalExact), flag > env > file; one Settings field per domain
│   │   ├── platform.go            open PostgreSQL, migrate every stream, object stores, keyring, queue, outbox, cron
│   │   ├── domains.go             construct domains in the declared graph's order; late-bind the six back edges
│   │   │                          and the operation dispatcher
│   │   ├── router.go              chi router, middleware, strict servers composed by embedding
│   │   ├── discovery.go           /.well-known/terraform.json from domain contributions (P17)
│   │   └── modes.go               which domain parts (Routes, Consumers, Cron) each process mode starts
│   ├── ctl/                       infraweavectl's command tree over pkg/sdk only: profile, whoami, version, api,
│   │                              operator (P3b); the spacectl-shaped groups, flows and mcp (P24b)
│   ├── devtools/                  libraries behind tools/; never linked into cmd/
│   │   ├── specref/               reference generation, scaffold, inventory, parity       ← internal/specref
│   │   ├── openapibundle/         fragment bundler                                         ← internal/openapibundle
│   │   ├── stubgen/               501 UnimplementedStrict generator                        ← internal/stubgen
│   │   ├── dataview/              tiers.md from the docs/data registers                    ← internal/dataview
│   │   └── docscheck/             links, report headers, decision statuses, plan structure ← internal/docscheck
│   ├── kernel/                    value types shared by domain APIs; parsing only, no I/O
│   │   ├── ids.go                 ULID, Slug, UUID (Flows)
│   │   ├── organization.go        OrganizationID; root, the installation's top node, is no value here
│   │   ├── account.go space.go    AccountID (P3), SpaceID (P4), the account's top space addressed by its slug
│   │   ├── principal.go           member, API key, organization key, run, operator, worker, stack, Flows project
│   │   ├── feature.go             Feature: catalogue names as a type (P3b)
│   │   ├── actions.go             AllowedActions: the list every returned resource carries (P4)
│   │   ├── labels.go              labels and autoattach: matching (P5)
│   │   ├── run.go                 RunState (22 values), RunType, StackState, RunPriority wire names
│   │   ├── vendor.go              VendorKind (P5)
│   │   ├── audit.go               AuditEntry (P2c)
│   │   ├── page.go                Cursor and Page of decision 0003
│   │   ├── search/                SearchInput predicate, order and cursor types (P4)
│   │   └── vcsroutes/             provider API route tables shared by vcs and agent/vcsagent (P19b)
│   ├── platform/                  infrastructure with no business rules
│   │   ├── buildinfo/             version, commit, build date stamped by make build        ← internal/buildinfo
│   │   ├── sql/                   pgx pool and the store conventions of decision 0005 (P2)
│   │   │   ├── migrate/           embedded streams, schema_migrations keyed by stream, one advisory lock
│   │   │   ├── notify/            LISTEN and NOTIFY fan-out between replicas (P2)
│   │   │   ├── schema/            the platform stream: the platform packages' tables, registered first (P2)
│   │   │   ├── search/            compiles SearchInput predicates to SQL under Reachable (P4)
│   │   │   └── sqltest/           testcontainers PostgreSQL for store and domain tests, on a migrated template
│   │   ├── tx/                    tx.Do(ctx, db, opts, fn), Q, AfterCommit; the unit of work on the context (P2)
│   │   ├── audit/                 Record(ctx, kernel.AuditEntry) in the caller's transaction
│   │   ├── outbox/                the event envelope and kind registry shared by Deploy and Flows; Append(ctx, Event)
│   │   │                          in the caller's transaction; Subscribe(kind, consumer, Handler), one job per consumer (P2c)
│   │   ├── queue/                 decision 0011's PostgreSQL job queue (P2c)
│   │   │   └── queuetest/         contract suite, TestAcc
│   │   ├── cron/                  five-field grammar; ticker claims due rows with SKIP LOCKED (P2c)
│   │   ├── objectstore/           Put, Get, versions, presigned GET and PUT (P2b)
│   │   │   ├── s3/ gcs/ azureblob/ fs/
│   │   │   └── objectstoretest/   contract suite: TestAcc on RustFS, fake-gcs-server and Azurite, TestUnit on fs
│   │   ├── crypt/                 decision 0013's keyring, envelope encryption and DeriveKey (P2b)
│   │   │   ├── kek/               local, awskms, gcpkms, azurekv, vault
│   │   │   └── signer/            token signing with a local key or a KMS signing key (decision 0006) (P3)
│   │   ├── seal/                  sealed column, object stream and pool envelope formats; holds no keys (P2b)
│   │   ├── expr/                  decision 0024's CEL engine and text templates; server and runner (P6)
│   │   ├── mask/                  decision 0013's masker: literal values (P6), well-known patterns (P9); server and runner
│   │   ├── rego/                  embedded OPA v1: prepared queries, capabilities, timeouts (P13)
│   │   ├── httpkit/
│   │   │   ├── problem/           RFC 9457 problems, decision 0003 slugs                   ← internal/server/problem
│   │   │   ├── middleware/        request ID, recovery, body limits, and the problem 404 and 405
│   │   │   │   ├── ratelimit/     token buckets per caller and cost class, the api-disabled switch
│   │   │   │   └── idempotency/   Idempotency-Key replay, in flight and reuse
│   │   │   ├── page/              cursor encode and decode for list operations
│   │   │   ├── client/            outbound HTTP with retries; agent and adapters use it
│   │   │   ├── serve/             listener lifecycle and drain for the API and admin listeners; agents use it
│   │   │   ├── dispatch/          runs one REST operation in process as the caller; bound by internal/app for
│   │   │   │                      flows (P18d) and intelligence (P25)
│   │   │   ├── console/           the console's build under dist/, embedded; index.html fallback, cache headers,
│   │   │   │                      CSP (P24c)
│   │   │   └── strict/            principal and problem mapping, merge-patch and If-Match for strict handlers (P5)
│   │   ├── gen/                   GENERATED strict servers, one package per spec
│   │   │   ├── api/               server.gen.go, unimplemented.gen.go                      ← internal/server/gen/deploy
│   │   │   ├── worker/            (P8)
│   │   │   └── operator/          (P3)
│   │   ├── observe/               slog, OpenTelemetry, /metrics, readiness on its own port (P2, P18b)
│   │   ├── private/               keeps a secret's file to its owner: mode 0600, a protected DACL on Windows
│   │   └── testkit/               fake clock, fake object store, HTTP recorder helpers
│   ├── iac/                       pure IaC vendor knowledge shared by server and agent (P5, P9, P20, P20b)
│   │   ├── iac.go registry.go     Vendor: Kind, ValidateSettings, Workflow, ParsePlan; Kind → Vendor; a stack input's vendorConfig refused field by field
│   │   ├── version/               Terraform and OpenTofu version constraints (P5)
│   │   ├── internal/settings/     the field checks the vendors share (P5)
│   │   ├── terraform/ opentofu/   settings validation (P5); plan parsing (P9)
│   │   ├── terragrunt/ pulumi/ cloudformation/ kubernetes/ (P20); ansible/ (P20b)
│   │   └── iactest/               contract suite every vendor passes (P5)
│   ├── agent/                     runs on customer hosts; links no domain and no server persistence (P8)
│   │   ├── cli/                   cobra commands of the agent process modes, shared by both builds
│   │   ├── config/                launcher settings: pool token, comms URL, executor, mounts
│   │   ├── launcher/              register, long-poll jobs and signals, heartbeat, drain
│   │   ├── wire/                  GENERATED worker protocol client
│   │   ├── executor/docker/       one run container per job, own binary mounted (P8)
│   │   ├── executor/kubernetes/   WorkerPool and Worker controller, pod per run (P14)
│   │   ├── exec/                  runner: phases, hooks, workspace, log streaming (P9)
│   │   ├── vendors/               command sets per vendor (P9, P20, P20b)
│   │   ├── initpolicy/            worker-side initialization policy over platform/rego (P14)
│   │   ├── tools/                 tool binary download, verification and caching (P9)
│   │   ├── cloudauth/             assumes a cloud integration's role on the worker with the run's OIDC token (P15)
│   │   ├── vcsagent/              long-polls /vcs-agent/v1 on serve (P19b)
│   │   ├── flowsagent/            dials the Flows gateway on serve; docker, kubernetes and process executors (P18d)
│   │   └── agenttest/             fake server over the generated worker spec
│   │
│   │   Business domains. Hexagon domains split internal/core from internal/adapters;
│   │   flat domains hold one logic package, internal/core, beside an http package and a pg package.
│   │
│   ├── features/                  flat, at the bottom of the domain graph: catalogue, tiers, grants, entitlement,
│   │   │                          the feature middleware and the operator API's features operations (P3b)
│   │   └── api/ assembly/ internal/{core,operator,http,pg}/ catalogue.gen.go
│   ├── expressions/               flat: the expression and policy check endpoints over each domain's Surfaces() (P6, P13)
│   │   └── assembly/ internal/{core,http}/   no api/: no domain calls it
│   ├── access/                    hexagon: organizations, accounts, members, groups, API keys, sessions, tokens, the OAuth
│   │                              authorization server, spaces, RBAC, the Organization tag (P3 to P4b, P13b, P23)
│   │   ├── api/                   Authenticator, Authorizer, Spaces, Tokens, identity reads, events
│   │   ├── assembly/              New(Deps); Routes; Consumers; Cron; Close; AccessAPI alias
│   │   └── internal/
│   │       ├── core/              organization account member apikey session token (P3); user group oauth (P3b);
│   │       │                      space authz (P4); role binding (P4b); login (P13b); sso scim mfa (P23); port
│   │       └── adapters/          http (incl. login.v1, P17); pg; saml oidc (P23)
│   ├── stacks/                    flat: stacks, settings, hooks, labels, human lock (P5); dependency (P16)
│   │   └── api/ assembly/ internal/{core,dependency,http,pg}/
│   ├── configuration/             flat: config elements, contexts, attachments, auto-attach (P6)
│   │   └── api/ assembly/ internal/{core,http,pg}/
│   ├── runs/                      hexagon: owns every table a run transition writes (P7, P9, P10, P16)
│   │   ├── api/                   Runs, Scheduler, Reports, StackStatus, StateAccess, events
│   │   ├── assembly/
│   │   └── internal/
│   │       ├── core/              machine lifecycle gates queuegate schedule handover timers
│   │       │                      promote replan forceapply dependencies port
│   │       └── adapters/          http (Runs tag, log streaming); pg (runs, transitions, receipts, flags,
│   │                              assignments, stack_run_locks, stack_run_state, stack_outputs)
│   ├── workers/                   hexagon: pools, workers, credentials, worker protocol endpoints, pool samples (P8, P14)
│   │   └── api/ assembly/ internal/{core/{pool,worker,credential,drain,waiter,sample,port},adapters/{http,protocol,pg}}/
│   ├── vendors/                   flat: tool version catalogue (P5, P20), refreshed from the release indexes
│   │   └── api/ assembly/ internal/{core,http,pg,index/indextest}/
│   ├── state/                     flat: Terraform http backend, versions, locks, import, rollback (P8b, P11)
│   │   └── api/ assembly/ internal/{core,backend,http,pg}/
│   ├── vcs/                       hexagon: sources, integrations, webhooks, routing, feedback (P6b, P12, P19, P19b)
│   │   └── api/ assembly/ internal/{core/{integration,intake,normalize,route,feedback,pullrequest,port},
│   │                              adapters/{http,github,gitlab,bitbucketcloud,bitbucketdc,azuredevops,rawgit,agentgateway,pg}}/
│   ├── policies/                  flat: policies, attachments, inputs, combination, samples, simulation (P13, P13b)
│   │   └── api/ assembly/ internal/{core,inputs,combine,sample,http,pg}/
│   ├── cloud/                     flat: AWS, Azure, GCP integrations, attachments, OIDC run tokens (P15)
│   │   └── api/ assembly/ internal/{core,oidc,aws,azure,gcp,http,pg}/
│   ├── schedules/                 flat: scheduled runs, tasks, deletes, drift detection (P16)
│   │   └── api/ assembly/ internal/{core,http,pg}/
│   ├── registry/                  hexagon: modules, versions, test cases, providers, GPG keys (P17, P17b)
│   │   └── api/ assembly/ internal/{core/{module,version,testcase,provider,gpg,sharing,port},adapters/{http,protocol,pg}}/
│   ├── events/                    hexagon: the sinks: inbox, named and stack webhooks, streams, audit trail, Slack, Teams (P18, P18b)
│   │   └── api/ assembly/ internal/{core/{inbox,webhook,delivery,stream,audit,metrics,port},adapters/{http,slack,pg}}/
│   ├── templates/                 flat: blueprints, templates, versions, deployments (P21)
│   │   └── api/ assembly/ internal/{core,render,http,pg}/
│   ├── resources/                 flat: managed entities, scans, generic search (P22, P22b)
│   │   └── api/ assembly/ internal/{core,search,scans,http,pg}/
│   ├── intelligence/              hexagon: AI integrations, Infra Assistant, Intent, MCP (P25, P25b)
│   │   └── api/ assembly/ internal/{core/{aiintegration,assistant,intent,tools,port},adapters/{http,mcp,providers,pg}}/
│   └── flows/                     hexagon: Flows, belonging to the account, in its own PostgreSQL schema; its Deploy
│       │                          triggers are a fan-out consumer here, so events never imports flows (P18c, P18d)
│       └── api/ assembly/ internal/{core/{project,flow,block,invocation,event,trigger,app,agentpool,secret,datatable,port},
│                                  adapters/{http,gateway,mcp,pg}}/
├── pkg/
│   ├── client/
│   │   ├── api/                   GENERATED client of the served API (Deploy and Flows), wrapped by pkg/sdk
│   │   └── operator/              GENERATED operator API client (P3)
│   ├── sdk/                       decision 0001's SDK over pkg/client/<spec>: token exchange, idempotency keys,
│   │                              retries, iterators, typed problem errors; every Go client uses it (P1c)
│   ├── flows/app/v1/              GENERATED App and Host gRPC stubs; the one pkg/ package the agent imports (P18d)
│   └── flows/appsdk/              the Go SDK for Flows apps, with apptest (P18d)
├── test/
│   ├── acceptance/                godog features, steps and the harness; ubuntu only
│   │   ├── features/              <area>_<topic>.feature, tagged with dependencies and @op:<operationId>
│   │   ├── images.go              every container image, pinned by digest
│   │   └── out/<scenario>/        logs and database dump of a failed scenario; CI artefact, not committed
│   ├── depguard/                  a module of its own breaking each depguard rule once; make lint-fixtures
│   ├── fixtures/                  Terraform and OpenTofu projects, Git repositories, policy corpus (P9)
│   │   └── captures/C<n>/         meta.yaml, request.graphql, response.json, expect.yaml (report 30 captures; P1c)
│   ├── terraform/<resource>/      provider acceptance configurations, translated from Spacelift's (P24)
│   └── cloud/<job>/<cloud>/       Terraform for the labs scheduled cloud jobs reach through OIDC, such as
│                                  objectstore-cloud's buckets and identities (P2b); local state, locked providers
├── web/console/                   the console: React and TypeScript, built by Vite into platform/httpkit/console/dist (P24c)
│   └── src/api/                   GENERATED orval client and hooks; committed; drift-checked
├── tools/                         thin mains over internal/devtools; excluded from coverage
│   ├── specref/                   fetch, generate, check, scaffold, inventory, parity, provider-parity
│   ├── openapi-bundle/
│   ├── stubgen/
│   └── dataview/
├── deploy/                        decision 0020's packaging and reference deployments (P27)
│   ├── images/                    image definitions: runner-terraform and runner-opentofu (P9), the other
│   │                              runners (P20, P20b), the server image (P27)
│   ├── compose/compose.yaml       one infraweave all and one infraweave launcher on Docker
│   ├── helm/infraweave/           chart for serve and scheduler, one Deployment per process mode
│   ├── helm/infraweave-workers/   chart for infraweave launcher
│   ├── terraform/modules/<cloud>/ aws, azure, google
│   ├── terraform/examples/<cloud>/<scenario>/
│   └── check.sh                   static checks of every deployment root, run in the quality gate
├── docs/                          research, decisions, architecture options, this implementation plan
├── Makefile                       every gate target; generate writes internal/platform/gen, internal/agent/wire and docs/implementation/tiers.md
├── .golangci.yml                  the only linter; depguard rules below
├── .testcoverage.yml              95% total, 90% per package; generated paths excluded
└── go.mod                         one module, full patch Go version
```

The Terraform and OpenTofu provider's source lives in its own repository,
`weaveplatform/terraform-provider-infraweave`, and imports `pkg/sdk` at a
pinned version of this module
([0001](../research/decisions/0001-rest-first-spacelift-surface.md)). This
repository holds its parity oracle
(`api/reference/spacelift/terraform-schema.json`), its mapping file
(`api/reference/provider-parity.yaml`) and its acceptance configurations
under `test/terraform/`; where the provider's checks run is one of the
plan's [open items](plan.md#open-items). First-party Flows apps are OCI
images of their own, one per app version, built on `pkg/flows/appsdk` and
listed in infraweave's first-party app registry
([0020](../research/decisions/0020-reference-deployments-and-packaging.md)).

## Where today's packages go

From option 5's
[package mapping](../research/architecture-options/5-modular-monolith.md#package-mapping).
[P1b](plan.md#p1b-domain-layout-and-depguard) does the moves in one pull request
with no behaviour change.

| Today | Target | What else changes |
|---|---|---|
| `cmd/` (`README.md` only) | `cmd/infraweave`, then `cmd/infraweavectl` (P3b) | one file per process mode |
| `api`, `api/reference/spacelift` | unchanged | |
| `api/src/deploy` | `api/src/api` | grows one `paths-<family>.yaml` per family, Deploy and Flows |
| `api/deploy.openapi.yaml` | `api/api.openapi.yaml` | bundled from `api/src/api`; `api/doc.go` embeds it |
| `api/oapi-server-deploy.yaml`, `api/oapi-client-deploy.yaml` | `api/oapi-server-api.yaml`, `api/oapi-client-api.yaml` | `output: internal/platform/gen/api/server.gen.go`, package `api`; the client's output `pkg/client/api` |
| `api/parity-baseline.json` | `api/parity-baseline.deploy.json` | `TestUnitDeployParity` and `TestUnitDeployParityChecks` become `TestUnitAPIParity` and `TestUnitAPIParityChecks`, which read it |
| `api/deploy_test.go` | `api/api_test.go` | `TestUnitDeployBundleCurrent`, `TestUnitDeployConventionComponents` and `TestUnitDeployProblemTypes` become `TestUnitAPI…` |
| `internal/buildinfo` | `internal/platform/buildinfo` | `LDFLAGS` paths in `Makefile` |
| `internal/server/gen/deploy` | `internal/platform/gen/api` | regenerated by `make generate`, not moved by hand; `api.StrictServerInterface`, `api.UnimplementedStrict` |
| `internal/server/problem` | `internal/platform/httpkit/problem` | every domain's HTTP adapter imports it |
| `internal/server/{ratelimit,idempotency}` | `internal/platform/httpkit/middleware/{ratelimit,idempotency}` | beside recovery and the `404` and `405` problem handlers from `internal/server`, in `internal/platform/httpkit/middleware` |
| `internal/server/pagination` | `internal/platform/httpkit/page` | encodes `internal/kernel`'s cursor and page |
| `internal/server` listener and drain | `internal/platform/httpkit/serve` | the agent process modes share it from P8 |
| `internal/server` `GET /livez` | `internal/platform/observe` | |
| `internal/server` routing and `operations.go` | `internal/app/router.go`, `internal/app/operations.go` | `apiServer` composition and its assertion |
| `internal/app/config.go` | `internal/app/config` | keys, flags and strictness unchanged |
| `internal/specref` | `internal/devtools/specref` | `tools/specref` import path |
| `internal/openapibundle` | `internal/devtools/openapibundle` | `tools/openapi-bundle` import path |
| `internal/stubgen` | `internal/devtools/stubgen` | `tools/stubgen` import path; `make generate` stub input path |
| `internal/docscheck` | `internal/devtools/docscheck` | `make docs-check` package path |
| `internal/dataview` | `internal/devtools/dataview` | `tools/dataview` import path |
| `pkg/client/deploy` | `pkg/client/api` | regenerated by `make generate` |
| `tools/openapi-bundle`, `tools/specref`, `tools/stubgen`, `tools/dataview` | unchanged | library import paths |
| `test/acceptance` | unchanged | runs the built binary as a subprocess from P1c |
| `Makefile` | unchanged path | `generate` and `verify-generate` paths, `LDFLAGS`, `docs-check` path |
| `.golangci.yml` | unchanged path | depguard enabled with option 5's rules |
| `.testcoverage.yml` | unchanged path | `^internal/platform/gen/` and `^internal/agent/wire/` replace `^internal/server/gen/` |

### Package names in the decision records

Decisions 0005 to 0024 name packages by responsibility. Decision 0004 places
them; read a record's package name through this table.

| Named in a record | Place in this layout |
|---|---|
| `internal/store`, `internal/store/migrations` | `internal/platform/sql`, `internal/platform/sql/migrate`; each domain's SQL files under its `pg/migrations` |
| `internal/store/storetest` | `internal/platform/sql/sqltest` plus each domain's `pg` store tests |
| `internal/queue`, `internal/queue/queuetest`, `internal/cron` | `internal/platform/queue`, `internal/platform/queue/queuetest`, `internal/platform/cron` |
| `internal/objectstore`, `internal/objectstore/objectstoretest` | `internal/platform/objectstore`, `internal/platform/objectstore/objectstoretest` |
| `internal/secrets` | `internal/platform/crypt` |
| `internal/secrets/mask` | `internal/platform/mask` |
| `internal/policy`, `internal/policy/input`, `internal/policy/combine.go` | `internal/platform/rego` for the engine; `internal/policies` for types, inputs, combination, receipts and sampling |
| `internal/run`, `internal/workerapi` | `internal/runs`; `internal/workers` (the `/worker/v1` adapter) |
| `internal/vcs/<provider>`, `internal/vcs/agent` | `internal/vcs/internal/adapters/<provider>`; `internal/agent/vcsagent`, with route tables in `internal/kernel/vcsroutes` |
| `internal/authn`, `internal/authz` | `internal/access` |
| `internal/authz/actions.yaml`, `internal/authz/actions.gen.go` | `internal/access/api`, because callers name an action when they call the authorizer |
| `internal/statebackend`, `internal/registry` | `internal/state`, `internal/registry` |
| `internal/events`, `internal/audit` | `internal/events` for the sinks; the envelope, the kind registry and the outbox in `internal/platform/outbox`, the audit write in `internal/platform/audit` |
| `internal/expr` | `internal/platform/expr`; the editor endpoints in `internal/expressions` |
| `internal/flows`, `internal/flows/agent` | `internal/flows`, `internal/agent/flowsagent` |
| `internal/ai`, `internal/ai/tools`, `internal/intent` | `internal/intelligence` |
| `internal/features` | `internal/features`: the domain at the bottom of the domain graph that holds the catalogue, tiers, grants and entitlement |
| `internal/problem`, `internal/buildinfo`, `internal/telemetry`, `internal/clock` | `internal/platform/httpkit/problem`, `internal/platform/buildinfo`, `internal/platform/observe`, `internal/platform/testkit` |

A package no row names joins `internal/platform` when it holds no business
rule, and the domain that owns its rule otherwise.

## Code placement

| Code | Place | Rule |
|---|---|---|
| A business domain | `internal/<domain>/` | One directory per domain in the tree above; a new domain gets the four-part skeleton |
| What other domains call | `internal/<domain>/api/` | Interfaces, input and output types and events only; imports `internal/kernel`, `internal/platform/outbox` and the standard library |
| Domain assembly | `internal/<domain>/assembly/` | `New(Deps)`, `Routes`, `Consumers`, `Cron`, `Close`, and an alias of the domain's strict partial named after the domain (`StacksAPI`); only `internal/app` and `cmd/` import it |
| Domain logic | `internal/<domain>/internal/core/...` (hexagon) or `internal/<domain>/internal/<domain>/` (flat) | Core never imports adapters |
| Rules with no I/O | a package of their own inside the owning domain | The run transition table, priority levels, policy combination and version numbering are tested as tables |
| HTTP handlers | `internal/<domain>/internal/.../http` | A strict-server partial for the domain's operations; hand-written chi routes for protocols that are not the REST API |
| SQL | `internal/<domain>/internal/.../pg` | The domain's store and its `migrations/` stream; foreign keys stay within the domain or point at the platform's tables, and another domain's ID is a plain column protected by a hold and a guard ([0004](../research/decisions/0004-repository-architecture.md)) |
| Provider shapes | one package per provider inside the owning domain | Each VCS provider's webhook and API shapes stay in `vcs/internal/adapters/<provider>` behind one normalised event |
| Infrastructure | `internal/platform/...` | Anything with no business rule: SQL, unit of work, outbox and event envelope, queue, cron, object storage, encryption and sealed formats, the expression engine, HTTP kit, OPA runtime, generated servers, observability |
| Value types across domain APIs | `internal/kernel/...` | Parsing only, no I/O |
| IaC vendor knowledge | `internal/iac/<vendor>` | Pure; the server validates and parses with it, the agent runs commands with it |
| Code that runs on a customer host | `internal/agent/...` | Never a domain, SQL, the queue, object storage credentials or the keyring |
| Wiring and process modes | `internal/app/...` | The only place that sees every domain; back edges are bound after construction |
| Entry points | `cmd/infraweave`, `cmd/infraweave-agent`, `cmd/infraweavectl` | Thin cobra shims; logic belongs in `internal/`, for the CLI `internal/ctl` over `pkg/sdk` |
| Developer commands | `tools/<name>` over `internal/devtools/<name>` | `internal/devtools` is imported only by `tools/` and its own tests |
| Public Go API | `pkg/client/<spec>`, `pkg/sdk`, `pkg/flows/appsdk` | Generated clients, the SDK over them, and the Flows app SDK; nothing under `pkg/` imports `internal/` |
| The console | `web/console/` | TypeScript only; it calls the served REST API through its generated client and nothing else |

Further rules:

- **Consistency draws the boundary.** The `runs` domain owns every table a run
  transition writes, including the stack's run lock and run state, and commits
  a transition in one transaction. The transition also writes the stack's
  state projection through `stacksapi`, so a stack list filters, sorts and
  counts by run state from `stacks`' own table.
- **No foreign keys between domains.** A domain holds another's row through
  its `api/` (`Hold`, `FOR KEY SHARE` in the caller's transaction), guards
  its own rows' deletes and moves with the guards other domains register,
  and purges its rows of a deleted account or organization when it
  consumes `access`' `account.deleted` event
  ([0004](../research/decisions/0004-repository-architecture.md#repository-shape-a-modular-monolith)).
- **Cross-domain effects.** Queries, authorisation and policy decisions are
  synchronous calls on `api/` interfaces. Consequences are events appended to
  the outbox in the producer's transaction. `policies` owns the input
  contracts and imports none of its callers.
- **Every package has a `doc.go`.** Each Go package under `internal/` and
  `pkg/`, generated ones included, has a `doc.go` that holds its package
  comment: what the package is and does, then an `# Input and output`
  section (the network, files, processes, environment, clock and logs it
  touches, or none) and a `# Dependencies` section (what it imports and who
  may import it). docscheck fails a package without one.
- **No `vendor` path element.** The go command treats a directory named
  `vendor` as vendored code, so IaC vendor code lives under `internal/iac` and
  `internal/agent/vendors`, and the catalogue domain is `internal/vendors`.
- **Hand-written protocols.** VCS webhooks, the Terraform `http` state backend,
  service discovery, `modules.v1`, `providers.v1`, `login.v1`, the Flows
  agent gateway and the MCP surfaces are chi routes the owning domain
  contributes through `Routes`.
- **One operation, one owner.** `internal/app/router.go` composes the strict
  server by embedding each domain's partial at depth one and
  `UnimplementedStrict` at depth two. Each partial is embedded under its
  domain's alias, since an embedded field takes its type's name, and the
  assertion `var _ api.StrictServerInterface = apiServer{}` makes an
  operation two domains implement fail to compile.
- **The agent process modes import `internal/agent` only.** `cmd/infraweave/agent.go`
  is the one place the agent path meets the rest of the binary.

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
| Console unit | Vitest | beside the code in `web/console/` | ubuntu |
| Console journeys | Playwright | `web/console/e2e/`, against a real `infraweave all` | ubuntu |

- **Kind by name.** The kind belongs to the test function, not the package. A
  top-level `Test` function that is neither `TestUnit…` nor `TestAcc…` fails
  `TestUnitTestNames`.
- **Feature files** are named `<area>_<topic>.feature`, where `<area>` is the
  domain or concern (`runs_terraform.feature`, `events_webhooks.feature`,
  `authz_spaces.feature`). A scenario carries dependency tags (`@postgres`,
  `@objectstore`, `@vault`, `@terraform`, `@tofu`, `@oidc`, `@vcs`, `@ai`,
  `@kubernetes`, `@flows-agent`), one `@op:<operationId>` tag per
  operation it exercises, and `@case:success`, `@case:denial` or
  `@case:failure` for the kind of path it proves; a Terraform provider
  scenario also carries `@provider:<capability record id>`.
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
section, where `$m` is `github.com/weaveplatform/infraweave`. The table
lists the cross-cutting rules; the per-domain rules of decision 0004, one
per domain listing the other domains' `api/` packages it may import, make
up the rest. depguard matches entries as plain string prefixes, so every
allow entry ends in `$` (that package exactly) or `/` (the packages below
it): a bare `$m/internal/stacks/api` would also admit
`$m/internal/stacks/apiutil`. Deny entries stay bare, since over-matching
only refuses more.

| Rule | Files | Mode | Entries |
|---|---|---|---|
| `api-packages` (rule 3) | `**/internal/*/api/**` | strict allow | `$gostd`, `$m/internal/kernel$`, `$m/internal/kernel/`, `$m/internal/platform/outbox$` |
| `constructors` (rule 4) | everything except `**/internal/app/**` and `**/cmd/**` | deny | `$m/internal/<domain>/assembly` for each of the 19 domains |
| `core-not-adapters` (rule 5) | `**/internal/*/internal/core/**` | deny | `$m/internal/<domain>/internal/adapters` for each of the 8 hexagon domains |
| `shared-below-domains` (rule 6) | `**/internal/platform/**`, `**/internal/kernel/**`, `**/internal/iac/**` | deny | the 19 domain roots, `$m/internal/agent`, `$m/internal/app` |
| `agent-isolation` (rule 7) | `**/internal/agent/**`, `**/cmd/infraweave/agent.go`, `**/cmd/infraweave-agent/**` | lax: deny, then allow | deny `$m/internal`, `$m/pkg` and `github.com/jackc/pgx`; allow `$m/pkg/flows/app/v1$`, `$m/internal/agent/`, `$m/internal/kernel$` and `/`, `$m/internal/iac$` and `/`, and `$m/internal/platform/{buildinfo,observe,rego,httpkit/client,httpkit/serve,seal,expr}$` |
| `devtools` (rule 8) | everything except `**/tools/**`, `**/internal/devtools/**`, and the tests of `api/` and `test/acceptance/`, which check the committed documents with `specref` | deny | `$m/internal/devtools` |
| `public-packages` (rule 9) | `**/pkg/**` | deny | `$m/internal` |
| `ctl` (rule 10) | `**/internal/ctl/**`, `**/cmd/infraweavectl/**` | lax: deny, then allow | deny `$m/internal` and `$m/pkg/client`; allow `$m/internal/ctl$`, `$m/internal/platform/buildinfo$` and `$m/internal/platform/private$` |
| `iac-pure` (rule 11) | `**/internal/iac/**` | lax: deny, then allow | deny `$m/internal`, `$m/pkg` and `github.com/jackc/pgx`; allow `$m/internal/iac$` and `/`, and `$m/internal/kernel$` and `/` |

Rule 2 is the declared graph below: one rule per domain, which allows
`$m/internal/platform/`, `kernel` and `iac`, its own tree and the `api/`
packages its row names, and denies the rest of `$m/internal`. The compiler
stops imports of another domain's `internal/`, and `constructors` stops
imports of its `assembly/`. depguard rejects a rule with an empty list, so
`constructors` and `shared-below-domains` name all 19 domains and
`core-not-adapters` all 8 hexagons before the domains exist; a deny entry
for a package that does not exist yet refuses nothing. A new domain adds its
graph rule, and `TestUnitDepguardFixtures` fails on a domain directory that
lacks it or that the shared rules do not name. `agent-isolation` allows rather than denies, so it needs
no entry, and a new platform package stays out of the agent until the rule
names it. `agent-isolation` allows all of `internal/iac`, so `iac-pure`
holds `iac` to `kernel` and itself: an `iac` package cannot carry another
import into the agent. `internal/agent` may import only `internal/kernel`, `internal/iac`,
`pkg/flows/app/v1` and seven `platform` packages from outside its own tree:
`buildinfo`, `observe` and `httpkit/serve` (every agent process mode logs,
traces and answers its probes), `rego`, `httpkit/client`, `seal` (the runner
seals workspaces) and `expr` (the runner renders workflow command templates). The 8 hexagons
are `runs`, `vcs`, `workers`, `access`, `registry`, `events`, `intelligence`
and `flows`.

`make lint-fixtures` proves each shared rule fires. It lints `test/depguard`,
a module of its own with the repository's path, using the repository's
`.golangci.yml`, so the fixtures cannot drift from the rules. Its packages
break each rule once, and break the compiler's `internal` rule and the
`time.Sleep` ban; a stub module in `stub/pgx` stands in for the driver, so
the `pgx` denials fire without its dependencies. The target fails unless the
findings, reduced to file, rule and import, are exactly `test/depguard/expected.txt`. Deleting any rule,
or dropping the `$` from `$m/internal/platform/outbox$`, fails it.

### Declared graph

The 19 domains in construction order, which `internal/app/domains.go`
follows and in which the migration streams run
([0004](../research/decisions/0004-repository-architecture.md)). Each domain
imports the `api/` packages its row names, all of them earlier in the
order, so the graph is acyclic. Its tables reference no other domain's. A
back edge is a call to a domain constructed later, set after construction;
it adds no import.

| Domain | Imports the `api/` of | Back edges bound in `internal/app` |
|---|---|---|
| `features` | none | |
| `access` | features | `policiesapi.Evaluator` for `LOGIN` policies |
| `workers` | features, access | `runsapi.Scheduler`, `runsapi.Reports` for the worker protocol |
| `vendors` | features, access | |
| `stacks` | features, access, workers, vendors | `runsapi.StackStatus`; `stacksapi.StackConfiguration`, which `configuration` implements, for the stack view's `attachedContexts` and `config` |
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

Intent operations and scan runs reach worker pools through `runsapi`, so
workers claim every job through one path. Each domain's `features` edge is
how it checks a limit or a feature; `access` supplies the authorizer, spaces
and tokens every domain uses.

## Generated code

Generated files are committed and drift-checked; nobody edits them by hand.

| Output | Source | Regenerate | Verify |
|---|---|---|---|
| `api/<spec>.openapi.yaml` | `api/src/<spec>/*.yaml` | `make bundle` (part of `make generate`) | `make verify-generate` |
| `api/src/api/paths-<family>.yaml` (first draft) | a reference tag, Deploy or Flows | `make scaffold [PRODUCT=flows] TAG=<tag> FAMILY=<family>`, then edited by hand | reviewed; the bundle is verified |
| `internal/platform/gen/<spec>/server.gen.go` | the bundle, `api/oapi-server-<spec>.yaml` | `make generate` | `make verify-generate` |
| `internal/platform/gen/<spec>/unimplemented.gen.go` | the generated server | `make generate` (stubgen) | `make verify-generate` |
| `pkg/client/<spec>`, `internal/agent/wire` | the bundle, `api/oapi-client-<spec>.yaml` | `make generate` | `make verify-generate` |
| `web/console/src/api/` | the served bundles, through orval | `make generate` (needs the pinned Node.js) | `make verify-generate` |
| The `App` and `Host` stubs | `api/proto/flows/app/v1/` | `make generate` | `make verify-generate`; `buf breaking` against the last release |
| `api/reference/spacelift/spacelift.openapi.yaml`, `coverage.md` | the snapshots and `rest-mapping.yaml` | `make reference` | `make verify-reference` |
| `api/reference/spacelift/` snapshots and `sources.lock.yaml` | upstream at the pinned versions | `make reference-fetch` (network) | `openapi-spec-update.yml` |
| `api/reference/spacelift-flows/flows.openapi.yaml`, `coverage.md` | the provider structs, flowctl's `src/api.ts`, `schemas.yaml` and `rest-mapping.yaml` | `make reference` | `make verify-reference` |
| `api/reference/spacelift-flows/` snapshots and `sources.lock.yaml` | upstream at the pinned versions | `make reference-fetch` (network) | `openapi-spec-update.yml` |
| `docs/data/flows-sdk-inventory.json` | the pinned `@slflows/sdk` `index.d.ts`, identifiers only | `make reference-inventory` | `make verify-reference` |
| [`docs/data/graphql-inventory.json`](../data/graphql-inventory.json), [`docs/research/reference/29-data-model-inventory.md`](../research/reference/29-data-model-inventory.md) | the introspection snapshot | `make reference-inventory` | `make verify-reference` |
| `internal/features/catalogue.gen.go` | [`docs/data/features.json`](../data/features.json), with the `tier-feature:` records of [`capabilities.json`](../data/capabilities.json) | `make generate` | `make verify-generate` ([0021](../research/decisions/0021-capability-scope-and-supersession.md#the-feature-catalogue-pd-05)) |
| [`docs/implementation/tiers.md`](tiers.md) | `docs/data/features.json` and `capabilities.json` | `make generate` (dataview) | `make verify-generate`; `TestUnitCommittedView` |
| `internal/access/api/actions.gen.go` | the `Action` enum of the introspection snapshot | `specref` | `make verify-reference` ([0007](../research/decisions/0007-authorization-spaces-and-roles.md)) |
| `api/reference/provider-parity.yaml` | hand-maintained mapping (P24) | none | `specref provider-parity` |

One generated `StrictServerInterface` spans many domains. `internal/app`
composes it by embedding, and each domain's `assembly/` package re-exports its
partial under its domain's name (`StacksAPI`), because `internal/app` cannot import the domain's
`internal/.../http` package
([generated code](../research/architecture-options/5-modular-monolith.md#generated-code)).
