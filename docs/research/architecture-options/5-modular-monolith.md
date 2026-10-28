# Option 5: Modular monolith, detailed layout

Detailed repository layout for option 5 of the
[architecture options](README.md): business modules under
`internal/<module>/`, each owning its model, its tables, its HTTP surface and
a published `api/` package, with ports and adapters inside the larger
modules, one binary with several roles and one database. Every package in
today's tree is mapped; roadmap packages are marked with the phase of
[report 26](../reference/26-scope-and-tiers.md#14-provisional-phase-spine)
that creates them.

Conventions in the trees:

- `← internal/server/problem` names where the code lives today.
- `(P7)` marks something a later phase creates.
- A line with neither is new structure for code that exists today or for
  P1, the phase in progress.

## Summary

The tree is organised by business domain first. Each domain is a module
under `internal/<module>/` with the same four parts: a published `api/`
package (interfaces, input and output types, events), a `module/` package
holding the constructor and the module's contributions to each process role,
an `internal/` directory with everything else, and one migration stream.
Modules talk to each other only through `api/` packages: synchronously for
queries, authorisation and policy decisions, and asynchronously through
events appended to a transactional outbox in the producer's own transaction.
The Go compiler enforces most of the boundary, because a module's
`internal/<module>/internal/...` packages are importable only from inside
`internal/<module>/`.

Module boundaries are drawn by consistency. A run transition changes the run
row, its transition record, the stack's run lock and run state, the queue item
that moves it on, the policy receipts that justified it and the event other
modules hear, and all of that commits or none of it does. So the `runs`
module owns every one of those tables: the run lock and the stack's
`StackState` mirror live in `runs`, not in `stacks`, and `stacks` reads them
through `runs/api`. The large modules (`runs`, `vcs`, `workers`, `access`,
`registry`, `events`, `intelligence`, `flows`) are hexagons inside, with
`internal/core/` and `internal/adapters/`; the small ones are flat.

Shared infrastructure with no business rules lives in `internal/platform/`:
PostgreSQL access, the context-carried unit of work, the outbox, decision
0011's job queue and cron ticker, object storage, envelope encryption, the
HTTP kit with problem details, the generated servers, and the embedded OPA
runtime. Value types used across module APIs, such as IDs, spaces,
principals and the run state vocabulary, live in `internal/kernel/`. Pure
vendor knowledge (settings, workflow keys, plan parsing) that both the server
and the worker need lives in `internal/iac/`. Everything that runs on a
worker host lives in `internal/agent/`, which links no module and no
server-side persistence.

Dependency rules:

1. Nothing outside `internal/<module>/` imports
   `internal/<module>/internal/...`. The Go compiler enforces this; depguard
   does not need to.
2. A package under `internal/<module>/` imports only `internal/platform/...`,
   `internal/kernel/...`, `internal/iac/...`, its own module, other
   modules' `internal/<other>/api/...`, and third-party libraries.
3. An `internal/<module>/api/` package imports only `internal/kernel/...`,
   `internal/platform/outbox` (for the event envelope) and the standard
   library, so `api/` packages can never form a cycle.
4. Only `internal/app/...` and `cmd/...` import `internal/<module>/module`,
   the constructor packages.
5. Inside a module, `internal/<module>/internal/core/...` never imports
   `internal/<module>/internal/adapters/...`.
6. `internal/platform/...`, `internal/kernel/...` and `internal/iac/...`
   import no module, no `internal/agent/...` and no `internal/app/...`.
7. `internal/agent/...` imports only `internal/kernel/...`,
   `internal/iac/...`, `internal/platform/{buildinfo,rego,httpkit/client}`
   and its own tree. It never imports a module (not even an `api/` package),
   `internal/platform/{sql,tx,outbox,audit,queue,objectstore,crypt}` or a database
   driver.
8. `internal/devtools/...` is imported only by `tools/...` and its own tests.

Rules 2 to 8 are depguard rules in `.golangci.yml`; rule 1 is the compiler's
`internal` rule. The section
[Tests and enforcement](#tests-and-enforcement) lists every depguard entry.

## Repository root

```text
infraweave/
├── cmd/
│   ├── infraweave/             one binary: serve, scheduler, all, worker, hidden worker exec,
│   │                           vcs-agent, flows-agent, migrate up and status, version
│   └── infraweavectl/          (P24) the CLI, a client over pkg/client/deploy
├── internal/                   app/ kernel/ platform/ iac/ agent/ devtools/ and one directory per module
├── pkg/
│   └── client/deploy/          generated deploy client; the Terraform provider repository imports it
├── api/                        unchanged layout; oapi-server-*.yaml outputs move to internal/platform/gen
│   ├── src/deploy/             the served REST API fragments
│   ├── src/worker/             (P8) the worker protocol fragments
│   └── reference/spacelift/    unchanged
├── test/
│   ├── acceptance/             godog features against real PostgreSQL, object storage and a real run
│   └── fixtures/               (P9) Terraform and OpenTofu projects, Git repositories, policy corpus
├── tools/                      unchanged: thin mains over internal/devtools
├── deploy/                     (P27) container images, runner images, Helm chart, Compose file
│   ├── images/                 server image and runner images (P9, P20)
│   └── helm/infraweave/        one Deployment per role
├── docs/                       research, decisions, the implementation plan
├── Makefile                    generate writes to internal/platform/gen and internal/agent/wire
├── .golangci.yml               depguard enabled with the rules in "Tests and enforcement"
├── .testcoverage.yml           exclusions follow the generated paths
└── go.mod                      one module
```

What changes at the root and why:

- `cmd/` gains its first binary. `cmd/infraweave` holds one cobra file per
  role and hands off to `internal/app` (serve, scheduler, all, migrate) or
  to `internal/agent` (worker, worker exec, vcs-agent, flows-agent). Report 24 shows Spacelift running
  its server, drain and scheduler as subcommands of one binary
  ([24, section 2.1](../reference/24-self-hosted-reference.md#21-process-inventory)),
  and this layout produces the same shape.
- `pkg/` stays small: generated clients only. Every module, the platform
  and the agent are internal, because publishing them would freeze module
  internals as a public API. The worker protocol client is internal too
  (see [Worker protocol and pools](#worker-protocol-and-pools)).
- `internal/` gains one directory per module. The set grows phase by phase,
  and a module directory appears with the phase that creates it.
- `api/` stays the source of truth for specifications; only the generated
  server output paths move, to `internal/platform/gen/<spec>`.
- `deploy/` arrives with packaging (P27); runner images arrive earlier,
  with the first run (P9) and the other vendors (P20).

## Full package tree

### Module status by phase

| Module or tree | Status at P1 | Created or completed by |
| --- | --- | --- |
| `platform`, `kernel`, `devtools`, `app` | partial: buildinfo, httpkit/problem, gen/deploy, devtools, the serve role | P1 to P2; queue P7; rego P13; observe P18 |
| `access` | absent | P3 identity and tokens, P4 spaces and RBAC, P23 SSO and SCIM |
| `stacks` | routes only (69 Stacks operations answer 501) | P5; stack dependencies P16 |
| `configuration` | absent | P6 |
| `runs` | routes only (3 Runs operations answer 501) | P7 scheduler and queue gates, P9 first run, P10 completeness, P16 dependencies |
| `workers` | absent | P8 protocol and public pool, P14 private pools |
| `agent` (worker-side tree) | absent | P8 launcher, P9 phase executor, P14 Docker and Kubernetes executors, P20 vendors |
| `iac` (pure library), `vendors` (catalogue module) | absent | P9 Terraform and OpenTofu, P20 the rest |
| `state` | absent | P11 |
| `vcs` | absent | P12 GitHub, P19 other providers and VCS agent pools |
| `policies` | absent | P13 |
| `cloud` | absent | P15 |
| `schedules` | absent | P16 |
| `registry` | absent | P17 |
| `events` | absent | P18 |
| `templates` | absent | P21 |
| `resources` | absent | P22 |
| `intelligence` | absent | P25 |
| `flows` | absent | P26 |

Every module has the same skeleton. A flat module has one logic package and
one store package under its `internal/`; a hexagon splits `core/` from
`adapters/`.

```text
internal/<module>/
├── api/                       PUBLISHED: interfaces other modules call, input and output types, events
├── module/                    New(Deps) (*Module, error); Routes(); Consumers(); Cron(); Close()
└── internal/
    ├── core/                  hexagon only: domain types, services, ports; no adapters import
    │   └── port/              driven ports: store, object storage, clocks, outbound clients
    ├── adapters/              hexagon only
    │   ├── http/              strict-server partial implementation and hand-written chi routes
    │   ├── pg/                PostgreSQL store; migrations/ embedded, one stream per module
    │   └── <outbound>/        provider clients, protocol gateways
    ├── <module>/              flat only: logic, ports and handlers in one package
    └── pg/                    flat only: the store
```

The complete tree at the end of P27:

```text
internal/
├── app/                                  composition root for serve, scheduler and all
│   ├── config/                           strict YAML, flag > env > file; one Settings field per module
│   ├── platform.go                       open PostgreSQL, migrate, object storage, keyring, queue, outbox
│   ├── modules.go                        construct modules in dependency order; register gates
│   ├── router.go                         chi router, middleware, strict servers composed by embedding
│   ├── discovery.go                      /.well-known/terraform.json from module contributions (P11, P17)
│   ├── roles.go                          Serve, Scheduler, All: which module parts each role starts
│   └── *_test.go                         lifecycle tests per role
├── devtools/                             libraries behind tools/; never linked into cmd/infraweave
│   ├── specref/                                                         ← internal/specref
│   ├── openapibundle/                                                   ← internal/openapibundle
│   ├── stubgen/                                                         ← internal/stubgen
│   └── docscheck/                                                       ← internal/docscheck
│
├── kernel/                               value types shared by module APIs; parsing only, no I/O
│   ├── ids.go                            ULID, Slug, UUID (Flows)
│   ├── account.go space.go               AccountID, SpaceID, the root space
│   ├── principal.go                      Principal: user, API key, run, worker, stack, Flows project
│   ├── labels.go                         Labels, autoattach: matching
│   ├── run.go                            RunState (22 values), RunType, StackState, RunPriority: wire names
│   ├── vendor.go                         VendorKind
│   ├── vcsroutes/                        provider API route tables shared by vcs and agent/vcsagent (P19)
│   ├── audit.go                          AuditEntry: the entry every module records
│   └── page.go                           Cursor and Page of decision 0003
│
├── platform/                             infrastructure with no business rules
│   ├── buildinfo/                                                       ← internal/buildinfo
│   ├── sql/                              pgx pool; the migrator, whose advisory lock is the only one (decision 0005)
│   │   ├── migrate/                      embedded streams, schema_migrations keyed by module
│   │   ├── notify/                       LISTEN and NOTIFY fan-out between replicas (P7)
│   │   └── sqltest/                      testcontainers PostgreSQL, one schema per test
│   ├── tx/                               Tx(ctx, fn), AfterCommit; the unit of work on the context
│   ├── audit/                            Record(ctx, kernel.AuditEntry): audit_trail_entries row in the caller's tx
│   │                                     (decision 0015); the table is platform's, as events and jobs are
│   ├── outbox/                           Append(ctx, Event): an events row and an events.fanout job in the
│   │                                     caller's tx (decision 0015) (P7)
│   ├── events/                           Subscribe(kind, consumer, Handler); fan-out enqueues one job per consumer (P7)
│   ├── queue/                            decision 0011's PostgreSQL job queue: EnqueueTx, leases fenced by generation,
│   │   │                                 ordering keys, the dead state, workers per logical queue (P7)
│   │   └── queuetest/                    TestAcc contract suite against PostgreSQL
│   ├── cron/                             five-field grammar and the ticker that claims due rows with
│   │                                     FOR UPDATE SKIP LOCKED; no leader (decision 0011) (P7)
│   ├── objectstore/                      Store port: Put, Get, versions, presigned GET and PUT (P2)
│   │   ├── s3/ gcs/ azureblob/ fs/       the four drivers of decision 0005
│   │   └── objectstoretest/              contract suite (decision 0016's name)
│   ├── crypt/                            decision 0013's keyring: envelope encryption, seal and open (P2)
│   │   └── kek/                          KEK providers: local, awskms, gcpkms, azurekv, vault
│   ├── rego/                             embedded OPA v1: prepared queries per (policy, version), capabilities,
│   │                                     timeouts, print capture; no knowledge of policy types (P13)
│   ├── httpkit/
│   │   ├── problem/                      RFC 9457 problems, decision 0003 slugs        ← internal/server/problem
│   │   ├── middleware/                   request ID, recovery, body limits, idempotency keys, rate limits
│   │   ├── page/                         cursor encode and decode for list operations
│   │   ├── client/                       outbound HTTP with retries, used by agent and adapters
│   │   └── strict/                       helpers for strict handlers: principal, problem mapping
│   ├── gen/
│   │   ├── deploy/                       server.gen.go, unimplemented.gen.go           ← internal/server/gen/deploy
│   │   └── worker/                       worker protocol strict server (P8)
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
├── agent/                                worker-side tree; links no module and no server persistence (P8)
│   ├── config/                           launcher settings: pool token, comms URL, executor, mounts
│   ├── launcher/                         register, long-poll for jobs and signals, heartbeat, drain
│   ├── wire/                             generated worker protocol client (P8)
│   ├── executor/
│   │   ├── docker/                       one run container per job, own binary mounted as root process (P8)
│   │   └── kubernetes/                   controller for WorkerPool and Worker resources, pod per run (P14)
│   ├── exec/                             `worker exec`: phases, hooks, workspace, log streaming (P9)
│   ├── vendors/                          command sets per vendor; implements iac.Workflow on the host
│   │   ├── terraform/ opentofu/          (P9)
│   │   └── terragrunt/ pulumi/ cloudformation/ kubernetes/ ansible/   (P20)
│   ├── initpolicy/                       worker-side initialization policy over platform/rego (P14)
│   ├── mask/                             log masking of decision 0013: literal values, ::add-mask, patterns (P9)
│   ├── tools/                            binary download, verification and caching per version (P9)
│   ├── vcsagent/                         `vcs-agent`: long-polls /vcs-agent/v1 on serve (decision 0012) (P19)
│   ├── flowsagent/                       `flows-agent`: dials the Flows gateway on serve (decision 0018) (P26)
│   └── agenttest/                        fake server over the generated worker spec
│
├── access/                               accounts, users, groups, API keys, sessions, tokens, spaces, RBAC
│   ├── api/
│   │   ├── authn.go                      Authenticator → kernel.Principal
│   │   ├── authz.go                      Authorizer.For(ctx, Principal) → Decider: Can, ReachableSpaces
│   │   ├── spaces.go                     Spaces: Get, Ancestors, Subtree, Inherits
│   │   ├── tokens.go                     Tokens: IssueRunToken, IssueWorkerToken, Introspect, JWKS
│   │   ├── identity.go                   Users, Groups, APIKeys (reads)
│   │   └── events.go                     UserCreated, APIKeyRevoked, SpaceChanged, RoleBindingChanged
│   ├── module/
│   └── internal/
│       ├── core/
│       │   ├── account/ user/ group/ apikey/ session/ token/   (P3)
│       │   ├── space/ role/ binding/ authz/                     (P4) action catalogue, inheritance walks
│       │   ├── login/                    login flow; calls policies/api for LOGIN policies (P13)
│       │   ├── sso/ scim/ mfa/           (P23)
│       │   └── port/
│       └── adapters/
│           ├── http/                     strict partial: accounts, users, API keys, spaces, roles, bindings;
│           │                             authn middleware; login.v1 for terraform login (P17)
│           ├── pg/                       migrations: accounts, spaces, roles, role_actions, users, api_keys,
│           │                             role_bindings, sessions, sso_settings, scim_clients
│           └── saml/ oidc/               (P23)
│
├── stacks/                               flat: stacks, settings, hooks, labels, human lock, enable and disable
│   ├── api/                              Stacks: Get, ByRepository, LockForRun, Settings; events: StackChanged
│   ├── module/
│   └── internal/
│       ├── stacks/                       service, validation through iac.ValidateSettings, hooks, reslug
│       ├── dependency/                   stack dependency DAG, references, cycle rejection (P16)
│       ├── http/                         strict partial for the Stacks tag
│       └── pg/                           stacks, stack_hooks, stack_dependencies, references
│
├── configuration/                        flat: config elements, contexts, attachments, auto-attach (P6)
│   ├── api/                              Environment.Resolve(target) → ordered elements and hooks
│   ├── module/
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
│   ├── module/
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
│           ├── http/                     strict partial for the Runs tag; log streaming
│           └── pg/                       runs, run_state_transitions, run_reviews, run_comments,
│                                         run_policy_receipts, run_dependencies, run_external_dependencies,
│                                         run_assignments, stack_run_locks, stack_run_state, stack_outputs
│
├── workers/                              pools, workers, credentials, the worker protocol endpoints (P8, P14)
│   ├── api/                              Pools: Get, ForStack; Workers: Busy, Drain; events: WorkerChanged
│   ├── module/
│   └── internal/
│       ├── core/{pool,worker,credential,drain,waiter,port}/
│       └── adapters/
│           ├── http/                     strict partial: pools, workers (deploy spec)
│           ├── protocol/                 worker spec server: register, jobs long-poll, signals, heartbeat
│           └── pg/                       worker_pools, workers, public_pool
│
├── vendors/                              flat: version catalogue for Terraform, OpenTofu, kubectl (P9, P20)
│   └── api/ module/ internal/{vendors,http,pg}/
├── state/                                Terraform http backend, versions, locks, import, rollback (P11)
│   ├── api/                              Versions: Latest, Outputs; events: StateVersionCreated
│   ├── module/
│   └── internal/{state,backend,tfe,http,pg}/
├── vcs/                                  integrations, webhooks, routing, feedback (P12, P19)
│   ├── api/                              Integrations, Sources (archive URL, checkout spec), Feedback, PullRequests
│   ├── module/
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
│   ├── module/
│   └── internal/{policies,inputs,combine,sample,http,pg}/
├── cloud/                                AWS, Azure, GCP integrations, attachments, OIDC run tokens (P15)
│   └── api/ module/ internal/{cloud,oidc,aws,azure,gcp,http,pg}/
├── schedules/                            scheduled runs, tasks, deletes, drift detection (P16)
│   └── api/ module/ internal/{schedules,http,pg}/
├── registry/                             modules, versions, test cases, providers, GPG keys (P17)
│   ├── api/                              Modules: ByRepository, CreateVersion; Providers; events
│   ├── module/
│   └── internal/
│       ├── core/{module,version,testcase,provider,gpg,sharing,port}/
│       └── adapters/{http,protocol,pg}/
├── events/                               inbox, named and stack webhooks, audit trail, Slack, Teams (P18)
│   ├── api/                              Router (notification policy sink choice), Inbox; audit query
│   ├── module/
│   └── internal/
│       ├── core/{catalogue,inbox,webhook,delivery,audit,metrics,port}/
│       └── adapters/{http,slack,pg}/
├── templates/                            blueprints, templates, versions, deployments (P21)
│   └── api/ module/ internal/{templates,render,http,pg}/
├── resources/                            managed entities, scans, generic search (P22)
│   └── api/ module/ internal/{resources,search,http,pg}/
├── intelligence/                         AI integrations, Infra Assistant, Intent, MCP (P25)
│   ├── api/ module/
│   └── internal/
│       ├── core/{aiintegration,assistant,intent,tools,port}/
│       └── adapters/{http,mcp,providers,pg}/
└── flows/                                Flows, with its own tenancy (P26)
    ├── api/ module/
    └── internal/
        ├── core/{org,project,flow,block,invocation,event,app,agentpool,secret,datatable,port}/
        └── adapters/{http,gateway,mcp,pg}/

pkg/
└── client/
    └── deploy/                                                          ← pkg/client/deploy
```

## Deep dives

### Stack configuration write path

```text
internal/stacks/
├── api/stacks.go          Stacks: Get, Search, ByRepository(integration, repo, branch), LockForRun
├── api/events.go          StackCreated, StackUpdated, StackDeleted, StackLocked
├── module/module.go       New(Deps{Tx, SQL, Outbox, Audit, Authz accessapi.Authorizer, Spaces accessapi.Spaces,
│                          Status runsapi.StackStatus, Pools workersapi.Pools, Vendors iac.Registry})
└── internal/
    ├── stacks/
    │   ├── service.go     Create, Update, Delete, Enable, Disable, Lock, Unlock, Reslug
    │   ├── settings.go    the settings model of report 07; vendor union validated by internal/iac
    │   ├── hooks.go       before and after lists per phase
    │   ├── labels.go      label rules, autoattach: labels kept as data
    │   └── *_test.go      service tests with fake api dependencies
    ├── http/
    │   ├── strict.go      DeployAPI: the 69 Stacks operations, embedded in app/router.go
    │   └── convert.go     generated request types ↔ settings model
    └── pg/
        ├── store.go       stacks, stack_hooks
        └── migrations/    0001_stacks.sql 0002_stack_hooks.sql (P5); 0003_stack_dependencies.sql (P16)
internal/configuration/internal/configuration/   attach, detach, auto-attach on label change (P6)
```

A `PATCH /api/v1/stacks/{id}` passes through `platform/httpkit/middleware`
(idempotency key, rate limit), the access module's authn middleware, then
`stacks/internal/http/strict.go`, which converts the generated request type
and calls the service. The service checks `stack:update` (the action name is
decision 0007's) on the stack's space through `accessapi.Decider`, validates
the vendor block through `internal/iac`, and writes in one transaction
with an audit entry (decision 0015's `audit.Record`) and a `StackUpdated` event in the outbox:

```go
// Update applies a stack update. Labels can change which contexts and
// policies auto-attach, and those modules learn of it from StackUpdated in
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
joins across two modules, which the store answers with a read of
`runs`' `stack_run_state` view granted to the stacks schema (see the
friction list). Configuration elements and contexts are a separate module
because they attach to stacks, modules and contexts alike, through polymorphic
owners ([28, section 12.1](../reference/28-data-model.md#121-shapes-that-recur)):
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
├── timers/                cron jobs registered for the scheduler role (P10)
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
2. **Queue gates.** The scheduler role consumes `run.schedule`.
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
scheduler role rather than a separate cron role.

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
├── module/
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
The modules that own a state point build the input and call the evaluator;
`policies` never imports `runs`, `vcs`, `access` or `events`, so no cycle can
form. The state points of
[report 09, section 8](../reference/09-policies.md#8-where-each-type-is-evaluated)
map to callers:

| Policy type | Caller | State point |
| --- | --- | --- |
| `GIT_PUSH` | `vcs/internal/core/route` | per stack and module for each event, before any run exists |
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
├── module/
└── internal/
    ├── core/
    │   ├── pool/                     public and private pools, credentials (CSR-signed certificate)
    │   ├── worker/                   registration, metadata, status, heartbeats, status decay
    │   ├── drain/                    drain, undrain, cycle; drain-safe scale-in fields
    │   ├── waiter/                   in-process long-poll waiters woken by platform/sql/notify
    │   └── port/
    └── adapters/
        ├── http/                     deploy-spec partial: WorkerPools tag
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
├── exec/                             `infraweave worker exec`: phases from the job, hooks, vendor commands
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

The worker role imports nothing from `workers`: rule 7 keeps the agent tree
to the generated client, `internal/iac` and `platform/rego`. The private
pool's encryption of run state to the pool key
([report 10, sections 1.1](../reference/10-workers-and-execution.md#11-public-and-private-pools-compared)
and [2.3](../reference/10-workers-and-execution.md#23-credentials))
splits the same way: `workers/internal/core/credential` signs the CSR, and
`agent/launcher` holds the private key and decrypts.

### VCS webhook ingest

```text
internal/vcs/
├── api/
│   ├── sources.go       Sources: ArchiveURL(stack, sha), CheckoutSpec (sparse paths, merge commit)
│   ├── feedback.go      Feedback: Check, Comment(key, body), Deployment
│   ├── pullrequests.go  PullRequests: ForStack, Get
│   └── events.go        PushReceived, PullRequestChanged, CommentCommand
├── module/
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
The scheduler role consumes `vcs.process_delivery`: `normalize` builds the provider
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
subcommand runs `internal/agent/vcsagent`, which imports no module. The
provider route tables that the agent's allowlist compiles in (decision 0012)
are pure data in `internal/kernel/vcsroutes`, the one package both sides
import, so the allowlist and the provider clients cannot drift.

### State backend

```text
internal/state/
├── api/versions.go       Versions: Latest(stack), Outputs(stack) for dependencies and templates
├── module/
└── internal/
    ├── backend/          Terraform http backend: GET, POST, LOCK, UNLOCK; lock ID checks
    ├── state/            versions, import (100 MB cap), rollback as a new version, retention
    ├── tfe/              read-only Terraform Cloud state API subset for external access
    ├── http/             hand-written chi routes /state/v1/{stack}; chi.RegisterMethod("LOCK") and ("UNLOCK");
    │                     strict partial for state version operations
    └── pg/               managed_state_versions, state_locks
```

Runs reach the backend with the run token issued at handover. The handler
introspects it through `accessapi.Tokens`, gets a run principal, and asks
`runsapi.StateAccess` whether that run, in its current state, may read,
lock or write
([16, section 1.1](../reference/16-state-management.md#11-how-a-run-reaches-it)).
State bodies live in the states bucket with object versions; the
`managed_state_versions` row records the object version ID, so history and
rollback rest on the store's versioning
([16, section 6](../reference/16-state-management.md#6-state-history-and-rollback)).
The module contributes `state.v2` and `tfe.v2*` to service discovery through
`module.Discovery()`, which `app/discovery.go` merges.

### Module and provider registry protocol

```text
internal/registry/
├── api/modules.go        Modules: ByRepository, CreateVersion(from push policy), Consumers
├── api/events.go         ModuleVersionCreated, ModuleVersionFailed, ProviderVersionPublished
├── module/
└── internal/
    ├── core/
    │   ├── module/       module as a stack variant without state: source, space, pool, environment
    │   ├── version/      major.minor.patch, no reuse, no skipped numbers, PENDING ACTIVE FAILED, yanked
    │   ├── testcase/     .spacelift/config.yml test cases → runsapi.Runs.Create(TESTING), depends_on order
    │   ├── provider/     drafts, publish, revoke, platforms with presigned uploads
    │   ├── gpg/          account-level keys, revocation, signature and checksum verification
    │   └── sharing/      space inheritance, space availability, cross-account, public
    └── adapters/
        ├── protocol/     hand-written: modules.v1 versions and download (204 + X-Terraform-Get),
        │                 providers.v1 versions and download; lenient version parsing
        ├── http/         strict partial for Modules and Providers tags
        └── pg/           modules, module_versions, module_version_consumers, module_shares,
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
├── module/module.go      New(...); Middleware() for app/router.go; Routes(); Cron() (session and key expiry)
└── internal/core/
    ├── space/            tree, immutable root, re-parenting with reachability, inheritance toggle
    ├── role/ binding/    150-value action catalogue, system roles, custom roles, five actor kinds
    ├── authz/            propagation down, Read up through inheritance, implicit stack read
    ├── token/            JWT signing keys, rotation, JWKS; run, worker and API tokens (decision 0006)
    └── login/            sessions, API key exchange, LOGIN policies through policiesapi
```

How a request gets identity and permission:

1. `app/router.go` installs `access/module.Middleware()` before every
   strict and hand-written route. It authenticates the bearer token or
   session and puts a `kernel.Principal` in the context.
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

### Events: notifications, webhooks and audit

```text
internal/platform/outbox/    Append(ctx, Event): events row and events.fanout job in the caller's tx; retention
internal/platform/events/    Subscribe(kind, consumer, Handler); fan-out jobs consumed in the scheduler role
internal/events/
├── api/router.go            Router: Route(ctx, NotificationInput) → []Route (inbox, slack, webhook, pull_request)
├── api/inbox.go             Inbox: Raise(ctx, Notification) for internal errors from any module
├── module/
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

One outbox carries every domain event: `Append` inserts the `events` row and
enqueues an `events.fanout` job in the producer's transaction, as decision
0015 records, and the fan-out enqueues one job per subscribed consumer
(notifications, stack webhooks, VCS feedback, resources indexing and Flows
subscriptions), each keyed so that a second fan-out adds nothing. Only
the audit shipper keeps a cursor. Audit entries are not events: each module
records them through `platform/audit` in the change's transaction, so a
rolled-back write leaves no entry and the audit trail never records a change
that did not happen
([19, section 8](../reference/19-events-notifications-audit.md#8-the-audit-trail)).
The notification policy chooses sinks: the catalogue builds the input,
`policiesapi.Evaluator.Notification` returns routes, and a `pull_request`
route calls `vcsapi.Feedback.Comment` with the policy's deduplication key.
Delivery bodies go to the deliveries bucket with a short expiry
([24, section 3.2](../reference/24-self-hosted-reference.md#32-object-storage-buckets)).

### Flows engine

```text
internal/flows/
├── api/flows.go             Projects for Deploy role bindings; FlowEvents for Deploy subscription blocks
├── module/module.go         Routes() (REST, MCP, flow HTTP endpoints), Gateway() listener, Consumers()
└── internal/
    ├── core/
    │   ├── org/ project/    organizations and projects: Flows' own tenancy, UUID identity
    │   ├── flow/ block/     definition, links, static and dynamic configuration, error handlers
    │   ├── invocation/      lineage, the four statuses, 60-day retention
    │   ├── event/           emit, echo, pending events, re-emit, retry; monthly and hourly limits
    │   ├── app/             app installations, registries, bundles, the SDK service surface
    │   ├── agentpool/       pools, hashed tokens, per-project exposure
    │   ├── deploy/          Deploy blocks: runsapi, stacksapi; subscriptions to RunStateChanged
    │   └── port/            AgentDispatch: Evaluate(expr), Invoke(handler) on an agent
    └── adapters/
        ├── gateway/         WebSocket server for agents; routes evaluations to a connected agent
        ├── mcp/             platform MCP server and per-flow MCP Tool servers
        ├── http/            REST for flowctl and the Flows Terraform provider operations
        └── pg/              its own schema: flows_organizations, flows_projects, flows, flow_blocks,
                             flow_events, flow_invocations, app_installations, agent_pools, secrets, tables
```

Flows is a module like the others but with its own tenancy ([report 23, section 2](../reference/23-flows.md#2-resource-hierarchy))
and its own PostgreSQL schema, `flows`, as decision 0018 sets; nothing in Deploy imports `flows/api`, while
`flows` imports `runsapi`, `stacksapi` and `accessapi`. Event processing is
queue work in the scheduler role (`flows.deliver` jobs, one per linked
input of an emitted event, decision 0018), and JavaScript runs on agents,
started by the `flows-agent` subcommand from `internal/agent/flowsagent`,
that dial the gateway
([23, section 8](../reference/23-flows.md#8-agents-and-agent-pools)). Spacelift
self-hosts Flows as server, worker and gateway from one image
([23, section 14.1](../reference/23-flows.md#141-components)), which maps
onto this layout's serve (REST and gateway listener) and scheduler roles
without a new server role.

### Composition root

```text
cmd/infraweave/
├── main.go               cobra root; binds setting flags only (not --config or --help)
├── serve.go              app.Serve(ctx, cfg)
├── scheduler.go          app.Scheduler(ctx, cfg)
├── all.go                app.All(ctx, cfg): serve and scheduler in one process
├── worker.go             agent/launcher.Run(ctx, agentcfg); imports no internal/app
├── exec.go               hidden `worker exec`: agent/exec.Main inside the run container
├── vcsagent.go           `vcs-agent`: agent/vcsagent.Run (P19)
├── flowsagent.go         `flows-agent`: agent/flowsagent.Run (P26)
├── migrate.go            `migrate up` and `migrate status`: app.Migrate, then exit
└── version.go            `version`: platform/buildinfo
internal/app/
├── config/               config.go (Settings: Server, Database, ObjectStore, Crypt, Queue, one field per module)
├── platform.go           open pgx pool, migrate every stream, object stores, keyring, queue, outbox, cron
├── modules.go            construct modules in dependency order; hand api values to dependants
├── router.go             chi router: middleware, deploy strict server, hand-written mounts
├── discovery.go          service discovery document merged from Discovery() contributions
└── roles.go              which module parts each role starts
```

Every module's `module` package returns a value with the same method set,
and the roles pick from it:

```go
// internal/app/roles.go
//
// part is what every module contributes. A role starts only the parts it
// needs: serve mounts routes and never consumes the queue, so a burst of
// webhook processing cannot starve API latency; scheduler consumes queues
// and fires cron and serves only the readiness port.
type part interface {
	Routes(r chi.Router)               // hand-written routes; strict partials go through Strict()
	Consumers(q queue.Registry)        // queue handlers and outbox subscriptions
	Cron(c cron.Registry)              // periodic tasks; every replica's ticker claims due rows, no leader
	Close(ctx context.Context) error
}

func Scheduler(ctx context.Context, cfg config.Settings) error {
	p, m, err := open(ctx, cfg) // platform, then modules in dependency order
	if err != nil {
		return err
	}
	for _, x := range m.all() {
		x.Consumers(p.queue)
		x.Cron(p.cron)
	}
	return runWorkers(ctx, p) // per-queue concurrency from cfg.Queue, as Spacelift's DRAIN_CONCURRENCY_*
}
```

Wiring order at the end of P27, from `modules.go`: access, stacks,
configuration, vendors, workers, policies, runs, state, vcs, cloud,
schedules, registry, events, templates, resources, intelligence, flows. A
module's `Deps` takes only `api` values of modules built before it, plus
late-bound interfaces for the three back edges that the dependency order
cannot satisfy: `stacks` reads `runsapi.StackStatus`, `runs` reads
`vcsapi.Sources` and `cloudapi.Credentials` at handover. Those are set with
`SetX` calls after construction, so the function stays a linear list of
about 250 lines.

The roles map onto Spacelift's processes: `serve` is its server, `scheduler`
its drain with cron folded in, and `worker` its launcher
([24, section 2.1](../reference/24-self-hosted-reference.md#21-process-inventory)).
The worker role is wired outside `internal/app`, so `cmd/infraweave/worker.go`
is the only place where the worker path meets the rest of the binary, and
it imports `internal/agent` only.

### Generated code

| Spec | Fragments | Generated server | Implemented by | Client |
| --- | --- | --- | --- | --- |
| deploy | `api/src/deploy/` | `internal/platform/gen/deploy` | each module's `internal/.../http` partial, composed in `app/router.go` | `pkg/client/deploy` |
| worker | `api/src/worker/` (P8) | `internal/platform/gen/worker` | `workers/internal/adapters/protocol` | `internal/agent/wire` |
| flows | `api/src/flows/` (P26) | `internal/platform/gen/flows` | `flows/internal/adapters/http` | `pkg/client/flows` |
| none (hand-written) | | | VCS webhooks (`vcs`), http state backend and `tfe` (`state`), discovery (`app`), modules.v1 and providers.v1 (`registry`), `login.v1` (`access`) | |

`internal/devtools/specref` keeps generating the Spacelift reference and
scaffolding fragments; `internal/devtools/openapibundle` bundles them;
oapi-codegen writes the strict servers; `internal/devtools/stubgen` writes
`UnimplementedStrict` next to each. One generated `StrictServerInterface`
spans many modules, so the router composes it by embedding:

```go
// internal/app/router.go (excerpt)
//
// deployServer implements deploy.StrictServerInterface. Each module's
// partial sits at depth one and the 501 stubs at depth two, so an
// implemented operation shadows its stub, and two modules implementing the
// same operation make the method ambiguous and the assignment below fails
// to compile. internal/app cannot import a module's internal/.../http
// package, so each module/ package re-exports its partial as an alias:
// type DeployAPI = http.DeployAPI.
type deployServer struct {
	*stacksmodule.DeployAPI
	*runsmodule.DeployAPI
	*accessmodule.DeployAPI
	*policiesmodule.DeployAPI // (P13)
	stubs
}

type stubs struct{ deploy.UnimplementedStrict }

var _ deploy.StrictServerInterface = deployServer{}
```

The `Makefile` generate loop keeps its shape; only the paths change:
`api/oapi-server-<spec>.yaml` writes `internal/platform/gen/<spec>`, and
stubgen reads `internal/platform/gen/<spec>/server.gen.go`.

### Tests and enforcement

| Kind | Location |
| --- | --- |
| Unit tests | beside the code in every package; `core/` packages use fakes of other modules' `api` |
| Store tests | `internal/<module>/internal/.../pg/*_test.go` against PostgreSQL through `platform/sql/sqltest` (testcontainers) |
| Contract suites | `platform/queue/queuetest`, `platform/objectstore/objectstoretest`, `iac/iactest`, `agent/agenttest` |
| Module tests | `internal/<module>/module/module_test.go`: the module built on sqltest with fake `api` dependencies |
| Machine tests | `runs/internal/core/machine`: every transition row and every illegal pair |
| Policy corpus | `policies/internal/combine` and `platform/rego` over the 67 example-library templates ([report 09, section 10.2](../reference/09-policies.md#102-counts)) |
| Acceptance | `test/acceptance/`: the binary in `all` mode plus a `worker`, real PostgreSQL, S3-compatible store, a real OpenTofu run |

Enforcement is golangci-lint's depguard, the repository's only linter, plus
the compiler's `internal` rule. depguard lists match package path prefixes,
so `$m` below stands for `github.com/weaveplatform/infraweave`.

```yaml
linters:
  enable: [depguard]
  settings:
    depguard:
      rules:
        api-packages:                       # rule 3
          list-mode: strict
          files: ["**/internal/*/api/**"]
          allow: [$gostd, $m/internal/kernel, $m/internal/platform/outbox]
        constructors:                       # rule 4: one entry per module
          files: ["$all", "!**/internal/app/**", "!**/cmd/**"]
          deny:
            - pkg: $m/internal/access/module
            - pkg: $m/internal/stacks/module
            - pkg: $m/internal/runs/module
            # ... 17 entries, one per module
        core-not-adapters:                  # rule 5: one entry per hexagon module
          files: ["**/internal/*/internal/core/**"]
          deny:
            - pkg: $m/internal/runs/internal/adapters
            - pkg: $m/internal/vcs/internal/adapters
            # ... 8 entries
        shared-below-modules:               # rule 6
          files: ["**/internal/platform/**", "**/internal/kernel/**", "**/internal/iac/**"]
          deny:
            - pkg: $m/internal/access
            - pkg: $m/internal/stacks
            # ... 17 module roots, then:
            - pkg: $m/internal/agent
            - pkg: $m/internal/app
        agent-isolation:                    # rule 7
          files: ["**/internal/agent/**"]
          deny:
            - pkg: $m/internal/platform/sql
            - pkg: $m/internal/platform/tx
            - pkg: $m/internal/platform/outbox
            - pkg: $m/internal/platform/audit
            - pkg: $m/internal/platform/queue
            - pkg: $m/internal/platform/objectstore
            - pkg: $m/internal/platform/crypt
            - pkg: github.com/jackc/pgx
            # plus the 17 module roots and internal/app
        devtools:                           # rule 8
          files: ["$all", "!**/tools/**", "!**/internal/devtools/**"]
          deny:
            - pkg: $m/internal/devtools
```

Rule 2 (modules reach other modules only through `api/`) costs no entry:
the compiler already stops imports of `internal/<other>/internal/...`, and
rule 4 stops imports of `internal/<other>/module`, so the `api/` package is
the only importable part of another module. In total there are 6 rules and
about 75 entries; a new module adds one entry to `constructors`, one to
`shared-below-modules`, one to `agent-isolation` and, if it is a hexagon,
one to `core-not-adapters`.

Coverage:

- `.testcoverage.yml` excludes `^cmd/`, `^tools/`, `^internal/platform/gen/`,
  `^internal/agent/wire/` and `^pkg/client/`.
- About 290 packages at the end of P27, most of them `core/` packages with
  real logic. Packages likely to struggle with the 90% floor: `api/`
  packages that hold only interfaces and types (keep constructors and
  validation there so each has something to test, or exclude interface-only
  files), `module/` packages (covered by `module_test.go`), migration embed
  packages, and the Kubernetes executor.
- Store and module tests need PostgreSQL, so they are `TestAcc` suites
  (decision 0016) that run on ubuntu only; the merged profile takes their
  coverage from that run, which `make cover` already merges.

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
    entities, `flows` (P26) feeds subscription blocks.

### Adding an IaC vendor: Pulumi (P20)

1. `api/src/deploy/`: the Pulumi settings variant of the stack vendor union;
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
8. `deploy/images/runner-pulumi/Dockerfile` and its CI build.
9. `test/acceptance/features/pulumi.feature`, its steps, and a fixture
   project under `test/fixtures/`.

No module's core changes: stacks validate through `internal/iac`, runs
parse through it, and the agent executes through its own registry. About 12
new files and 4 edited.

### Adding a business domain: templates (P21)

1. `api/src/deploy/paths-templates.yaml` (scaffolded with `make scaffold`
   from the reference tags); `make generate`.
2. `internal/templates/api/templates.go`: `Deployments` for Flows and
   intelligence; `api/events.go`: `TemplateVersionPublished`,
   `DeploymentChanged`.
3. `internal/templates/internal/templates/`: templates, versions with
   one-way publication, deployments with the eight-value state, change
   records and rollback
   ([report 17](../reference/17-blueprints-and-templates.md#9-template-deployments)).
4. `internal/templates/internal/render/`: the CEL renderer with `${{ }}`
   delimiters.
5. `internal/templates/internal/pg/` with `migrations/0001_templates.sql`
   (templates, template_versions, template_deployments, changes, blueprints).
6. `internal/templates/internal/http/strict.go`: the partial for the
   Templates and Blueprints tags.
7. `internal/templates/module/module.go`: `New(Deps{Stacks stacksapi.Stacks,
   Runs runsapi.Runs, Authz accessapi.Authorizer, ...})`; subscribes to
   `RunStateChanged` to compute deployment state.
8. `internal/app/modules.go`, `router.go` (embed the partial),
   `config/config.go` (Settings field).
9. `.golangci.yml`: one entry each in `constructors`,
   `shared-below-modules` and `agent-isolation`.
10. `test/acceptance/features/templates.feature` and its steps.

Managed stacks are created through `stacksapi.Stacks.Create` with a
`managedBy` reference; `stacks` gains a read-only check for managed stacks,
the only edit to an existing module. About 20 new files and 5 edited.

## Package mapping

| Today's package | New location | Notes |
| --- | --- | --- |
| `cmd/` (`cmd/README.md` only) | `cmd/infraweave`, later `cmd/infraweavectl` (P24) | one file per role; README kept |
| `api` (`api/doc.go`, `api/deploy_test.go`) | `api` | unchanged; embeds stay, a `Worker` embed joins at P8 |
| `api/src/deploy` | `api/src/deploy` | grows one `paths-<family>.yaml` per family |
| `api/reference/spacelift` | `api/reference/spacelift` | unchanged |
| `internal/buildinfo` | `internal/platform/buildinfo` | `-ldflags` paths in the `Makefile` change |
| `internal/docscheck` | `internal/devtools/docscheck` | `make docs-check` path changes |
| `internal/openapibundle` | `internal/devtools/openapibundle` | |
| `internal/server/gen/deploy` | `internal/platform/gen/deploy` | regenerated, not moved by hand |
| `internal/server/problem` | `internal/platform/httpkit/problem` | used by every module's HTTP adapter |
| `internal/specref` | `internal/devtools/specref` | |
| `internal/stubgen` | `internal/devtools/stubgen` | |
| `pkg/client/deploy` | `pkg/client/deploy` | unchanged |
| `tools/openapi-bundle` | `tools/openapi-bundle` | import path of the library changes |
| `tools/specref` | `tools/specref` | import path of the library changes |
| `tools/stubgen` | `tools/stubgen` | import path of the library changes |
| `test/acceptance` | `test/acceptance` | features gain the binary and a worker from P9 |
| `api/oapi-server-deploy.yaml` | same file | `output: internal/platform/gen/deploy/server.gen.go` |
| `Makefile` | same file | generate and verify-generate paths; ldflags; docs-check path |
| `.golangci.yml` | same file | depguard enabled with the six rules |
| `.testcoverage.yml` | same file | `^internal/platform/gen/`, `^internal/agent/wire/` replace `^internal/server/gen/` |

## Numbers

Estimates, from the domains of report 26 and the trees above.

| Measure | Today (P1) | End of P27 |
| --- | --- | --- |
| Go packages | 16 (13 today plus `cmd/infraweave`, `internal/app`, `app/config`) | about 290 |
| Top-level directories under `internal/` | 4 (`app`, `kernel`, `platform`, `devtools`) | 23 (6 shared plus 17 modules) |
| Dependency rules | 8 (1 compiler, 7 depguard-checked, of which rule 2 needs no entry) | same |
| depguard rules and entries | 6 rules, about 20 entries | 6 rules, about 75 entries |
| Files touched to add a vendor | n/a | about 12 new, 4 edited |
| Files touched to add a domain | n/a | about 20 new, 5 edited |
| Packages a stack update crosses | 4 (`platform/httpkit/middleware`, `stacks/internal/http`, `stacks/internal/stacks`, `stacks/internal/pg`) | 6 (adds `access` middleware and `accessapi`) |
| Largest package | `internal/platform/gen/deploy` (generated, 24k lines) | `runs/internal/core/lifecycle` among hand-written ones, about 5k lines |
| Thin packages at risk under the 90% floor | `app/config` | about 35: `api/` and `module/` packages, migration embeds, the Kubernetes executor |

## Placement choices and friction

Placement choices settled in this file:

- **Runs own every table a transition writes.** The run lock, the stack's
  run state, assignments, receipts and outputs are runs tables, so a
  transition is one transaction inside one module.
- **Policies own the input contract and never call back.** Callers build
  inputs and act on decisions; trigger decisions come back as data.
- **Module internals sit under a nested `internal/`**, so the compiler
  enforces the main boundary and depguard handles only the rest.
- **The agent tree is outside every module**, imports no persistence, and
  shares only `internal/kernel`, `internal/iac` and three platform packages
  with the server.
- **Vendor knowledge is a pure library**, used by stacks for validation, by
  runs for plan parsing and by the agent for commands.
- **Shared IaC vendor code lives under `internal/iac/`, not `internal/vendor/`**:
  the go command treats any `vendor` path element as a vendor directory, so
  `go list ./...` skips it and imports of it fail.
- **Object storage, encryption, queue, outbox and OPA are platform**, because
  several modules use each and none of them carry business rules.
- **One migration stream per module, no cross-module foreign keys**; Flows
  gets its own schema as well as its own stream.
- **The worker protocol client is internal**; only `pkg/client/deploy`
  (and `pkg/client/flows` at P26) are public.

Where this layout rubs against the system:

- **Stack reads cross into runs.** Stack state, blocker and run lock are
  runs data; a stack list filtered or sorted by state needs a read view
  across two schemas, the one sanctioned cross-module read.
- **Run creation reads the stack row inside its transaction**
  (`LockForRun`), a cross-module transaction that couples `runs` to the
  stacks schema's locking.
- **`runs` is the gravity well.** It holds the machine, gates, scheduler,
  handover, timers, promotion and dependencies: about a fifth of the
  hand-written code, and the module most phases (P7, P9, P10, P13, P16)
  touch.
- **Back edges need late binding.** `stacks` reads `runsapi`, and `runs`
  reads `vcsapi` and `cloudapi`, so `modules.go` sets three dependencies
  after construction.
- **One binary carries server code to worker hosts.** The agent packages do
  not import it, but the linked binary contains it; a separate
  `cmd/infraweave-worker` is the fallback if image size or a customer's
  review demands it.
- **Constructor packages all named `module`** need import aliases in
  `internal/app`, and the nested `internal/` makes import paths long
  (`internal/runs/internal/core/lifecycle`).
- **Boundaries for intelligence and flows are drawn before their phases.**
  The tree commits only to their existence, their own tables and their
  `api/` seams.
