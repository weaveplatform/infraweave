# Option 1: Layered, detailed layout

Detailed repository layout for option 1 of the
[architecture options](README.md): horizontal technical layers (transport,
workflow, service, repository and infrastructure, model) stacked over each
other, with packages grouped by technical role first and by business domain
second. Every package in today's tree is mapped; roadmap packages are marked
with the phase of
[report 26](../reference/26-scope-and-tiers.md#14-provisional-phase-spine)
that creates them.

Conventions in the trees:

- `← internal/server/problem` names where the code lives today.
- `(P7)` marks something a later phase creates.
- A line with neither is new structure for code that exists today or for
  P1, the phase in progress.

## Summary

The tree is cut horizontally. **Transport** turns a protocol into calls:
the strict REST server generated from `api/deploy.openapi.yaml`, the
hand-written protocols (VCS webhooks, the Terraform `http` state backend,
service discovery, the module and provider registry protocols,
`login.v1`), the worker long-poll API, and the scheduler's queue consumers
and cron entries. **Service** holds one package per business domain:
stacks, contexts, runs, workers, policies, VCS, state, registry, identity,
spaces, events and the rest. Each service owns the rules of its own
entities and the transaction boundary of an operation that touches only
them. **Repository** holds SQL per domain over one PostgreSQL database, and
**infrastructure** holds every other external system: object storage, the
work queue, VCS provider clients, cloud credential clients, envelope
encryption, outbound HTTP. **Model** holds the entity structs and enums
that every layer passes around, and **support** holds pure libraries with
no layer of their own, including the embedded OPA wrapper.

The classic layered pattern lets services call each other freely. In
infraweave that freedom produces cycles at once: a run needs the stack
lock, the stack's policies, the pool's workers and the VCS integration for
feedback; a VCS event needs stacks, policies and runs; a stack delete
needs a destroy run. This layout therefore splits the middle into two
tiers. Services never import each other, apart from `service/authz`, which
every service asks for a decision. Every operation that crosses domains
lives one tier up, in a **workflow** package (`workflow/runflow`,
`workflow/stackflow`, `workflow/vcsevent` and later ones), which composes
services inside one context-carried transaction. Workflows are kept in a
declared order and a workflow imports only workflows earlier in that order,
so the run state machine (`runflow`, first in the order) can be reached by
everything and reaches nothing above it.

The worker role sits beside the layers, not in them. `internal/worker/...`
is a separate column (launcher, phase executor, vendor command sets,
container runtimes) that talks to the server only through a generated
worker-protocol client, so a private worker on a customer host links no
repository, infrastructure or service code.

Dependency rules:

1. `internal/model/...` imports nothing under `internal/` except other
   `internal/model/...` packages. `internal/support/...` imports only `internal/model/...` and external
   modules.
2. `internal/repository/...` imports only `model`, `support` and
   `internal/infra/crypto` (sealed columns). `internal/infra/...` imports
   only `model`, `support` and external modules, never `repository`.
3. `internal/service/<domain>/...` imports `repository`, `infra`, `model`,
   `support`, its own domain's subpackages and `internal/service/authz`. It
   never imports another service. `service/authz` imports no service.
4. `internal/workflow/<w>/...` imports `service`, `repository` (the
   transaction runner and read-only queries), `infra/queue`, `model` and
   `support`. Of the workflows it imports only those earlier in the
   declared order: `runflow`, `stackflow`, `vcsevent`, `notifyflow`,
   `templateflow` (P21), `intentflow` (P25), `flowsdeploy` (P26).
5. `internal/transport/...` imports `workflow`, `service`, `model`,
   `support` and `internal/config`. It never imports `repository` or
   `infra`.
6. `internal/worker/...` imports only `internal/model/...`,
   `internal/support/...` and its own subpackages, which include the
   generated worker-protocol client. Nothing outside `internal/worker` and
   `cmd/` imports `internal/worker`.
7. `pkg/...` imports nothing under `internal/` or `cmd/`.
8. Only `cmd/...` imports `internal/app`; only `tools/...` imports
   `internal/tooling/...`.

Rules 1, 2 and 5 are the layered pattern itself. Rules 3 and 4 replace the
pattern's missing rule between peers with a two-tier middle; they are what
keeps runs, policies, stacks and VCS acyclic, and they are the main cost of
this option. Rule 6 exists because one binary carries a second program.

## Repository root

```text
infraweave/
├── cmd/
│   ├── infraweave/             NEW: cobra root; serve, scheduler, all, worker, hidden worker exec,
│   │                           vcs-agent, flows-agent, migrate up, migrate status, version;
│   │                           thin: builds config and calls internal/app or internal/worker
│   └── infraweavectl/          (P24) the CLI over pkg/client/deploy
├── internal/                   REORGANISED: model, support, repository, infra, service, workflow,
│                               transport, worker, app, config, tooling
├── pkg/
│   └── client/deploy/          unchanged: the generated public client of the served API
├── api/                        unchanged layout; oapi-server-*.yaml outputs move to
│   │                           internal/transport/http/gen/<spec>
│   └── src/<spec>/             src/deploy today; src/worker (P8) for the worker protocol
├── test/
│   └── acceptance/             unchanged; features grow per domain; real PostgreSQL, object
│                               storage and an OpenTofu run through testcontainers
├── tools/                      unchanged: thin mains over internal/tooling
├── docs/                       research and decisions; the implementation plan (IMP-01) adds the
│                               annotated layout
├── deploy/                     (P27) Helm chart, Terraform modules, container image definitions
├── Makefile                    generate writes to internal/transport/http/gen; verify-generate
│                               diffs the new path; build unchanged (CGO disabled, six platforms)
├── .golangci.yml               depguard enabled with the rules under Tests and enforcement
├── .testcoverage.yml           exclusions follow the generated paths
└── go.mod                      one module, unchanged path
```

What changes at the root and why:

- `cmd/infraweave` appears with P1 so the served skeleton has an entry
  point. It stays thin, as `cmd/README.md` asks: each subcommand reads its
  configuration and calls one function: `app.Serve`, `app.Schedule`,
  `app.All`, `worker.Launch`, `worker.Exec`, `vcsagent.Run` (P19),
  `flowsagent.Run` (P26), `app.Migrate` or `app.MigrateStatus`; `version`
  prints `support/buildinfo`.
- `internal/` is grouped by layer. Today's flat packages (`specref`,
  `openapibundle`, `stubgen`, `docscheck`, `buildinfo`, `server/...`) each
  find a layer or the tooling corner.
- `pkg/` keeps only the generated client. The layered pattern has no public
  contract tier: storage interfaces live in `repository`, beside their
  implementations, and nothing in a layer is meant for another repository.
- `api/` stays the source of truth for specifications; only the
  `output` paths in `api/oapi-server-*.yaml` move.

## Full package tree

### Layer and domain status

| Layer or domain | Status today (P1) | Created or completed by |
| --- | --- | --- |
| model, support, tooling, config | support/buildinfo and tooling exist (moved); model and config are new | P1, P2 onwards |
| transport/http root, problem, gen/deploy | exist (moved); 501 for every operation | P1 |
| transport/http/api/stacks | strict handlers for the 69 Stacks operations, answering 501 through the stub | P1, behaviour P5 |
| repository root, migrations, infra/objectstore, infra/crypto | absent | P2 |
| service/identity, service/authz, service/spaces | absent | P3, P4; SSO and SCIM P23 |
| service/stacks, service/config, workflow/stackflow | absent | P5, P6 |
| infra/queue, transport/scheduler, service/runs, workflow/runflow | absent | P7, completed P9 and P10 |
| service/workers, transport/http/workerapi, worker column | absent | P8, private pools P14 |
| service/iac/terraform, opentofu; worker/iac/terraform, opentofu | absent | P9 |
| service/state, transport/http/statebackend | absent | P11 |
| service/vcs, infra/vcsclient/github, transport/http/webhook, workflow/vcsevent | absent | P12; other providers P19 |
| service/policy, support/rego | absent | P13 |
| service/cloud, infra/cloud | absent | P15 |
| service/schedules (cron, drift, dependencies) | absent | P16 |
| service/registry, transport/http/registry | absent | P17 |
| service/notify, webhooks, audit, metrics; workflow/notifyflow | absent | P18 |
| other vendors | absent | P20 |
| service/templates, blueprints; workflow/templateflow | absent | P21 |
| service/resources, search | absent | P22 |
| service/intelligence, workflow/intentflow, transport/mcp | absent | P25 |
| service/flows, workflow/flowsdeploy, transport/ws/flowsgateway, worker/flowsagent | absent | P26 |
| deploy/ (root) | absent | P27 |

### The tree

```text
internal/
├── app/                                   COMPOSITION ROOT; only cmd/ imports it
│   ├── app.go                             Open(ctx, cfg): database, migrations, object store, queue, keyring
│   ├── services.go                        builds every service from repositories and infra   (P2 onwards)
│   ├── workflows.go                       builds workflows in the declared order              (P5 onwards)
│   ├── serve.go                           Serve: chi router, strict server, hand-written mounts
│   ├── schedule.go                        Schedule: queue workers and the scheduler ticker of decision 0011 (P7)
│   ├── migrate.go                         Migrate, MigrateStatus: the embedded migrations of decision 0005 (P2)
│   ├── all.go                             All: Serve and Schedule in one process
│   └── *_test.go                          role start-up and shutdown against testcontainers PostgreSQL
├── config/                                strict YAML (UnmarshalExact), flag > env > file; Server, Scheduler,
│                                          Worker sections; only setting flags are bound
├── tooling/                               libraries behind tools/; not part of any role
│   ├── specref/                           Spacelift reference generator      ← internal/specref
│   ├── openapibundle/                     fragment bundler                   ← internal/openapibundle
│   ├── stubgen/                           UnimplementedStrict writer         ← internal/stubgen
│   └── docscheck/                         docs tree checks                   ← internal/docscheck
│
├── model/                                 SHARED TYPES, every layer and the worker column
│   ├── ids.go                             ULID-backed IDs: StackID, RunID, PoolID, ...; Slug
│   ├── account.go space.go                Account, Space, RoleBinding, Action                (P3, P4)
│   ├── stack.go                           Stack, StackSettings, StackState, Lock              (P5)
│   ├── config.go                          ConfigElement, Context, Attachment                 (P6)
│   ├── run.go                             RunType, RunState (22 values), RunPriorityPreset, Run,
│   │                                      Transition; Terminal(), Passive(), Blocking()      (P7)
│   ├── worker.go                          WorkerPool, Worker, WorkerStatus, Job, Assignment  (P8)
│   ├── policy.go                          PolicyType (13 names), EngineType, Outcome, Receipt (P13)
│   ├── vcs.go                             VCSIntegration, Provider, PushEvent, PullRequest   (P12)
│   ├── state.go                           StateVersion, StateLock                            (P11)
│   ├── registry.go                        Module, ModuleVersion, Provider, ProviderVersion   (P17)
│   ├── event/                             EVENT CATALOGUE, decision 0015's kinds: RunStateChanged (run_updated),
│   │                                      ModuleVersionChanged, InternalError, WorkerUsage; AuditEntry;
│   │                                      Kind(), Encode() (P7, P18)
│   ├── iac/                               Vendor enum; per-vendor settings structs shared with the worker
│   │   └── terraform.go opentofu.go       (P9); terragrunt.go pulumi.go cloudformation.go kubernetes.go
│   │                                      ansible.go (P20)
│   ├── template.go blueprint.go           (P21)
│   ├── resource.go                        ManagedEntity, Scan (P22)
│   ├── intent.go                          IntentProject, IntentOperation (P25)
│   └── flows/                             Organization, Project, Flow, Block, Event, Invocation (UUIDs) (P26)
│
├── support/                               PURE LIBRARIES, no layer, no I/O
│   ├── buildinfo/                         version, commit, date from -ldflags   ← internal/buildinfo
│   ├── ulid/ slug/                        identifiers and stackReslug rules     (P2)
│   ├── labels/                            autoattach:<label> matching, label sets (P6)
│   ├── cronexpr/                          decision 0011's cron parser: five fields, time zones (P7)
│   ├── vcsroutes/                         per-provider API route tables: the VCS clients call only these, and
│   │                                      the VCS agent's allowlist is compiled from them (decision 0012) (P19)
│   ├── rego/                              embedded OPA v1: compile with capabilities, Rego v0 and v1, prepared
│   │                                      query cache, per-type deadlines, masking            (P13)
│   └── jsonschema/                        policy input contracts and template input validation (P13, P21)
│
├── repository/                            LAYER: SQL per domain over one PostgreSQL database (decision 0005)
│   ├── db.go                              Open(pgx pool); Tx(ctx, fn) joins a context-carried transaction;
│   │                                      AfterCommit(ctx, fn); ErrNotFound, ErrConflict      (P2)
│   ├── migrate.go migrations/             embedded NNNNN_*.sql, one sequence for every table (decision 0005) (P2)
│   ├── page.go                            keyset cursor encoding of decision 0003           (P2)
│   ├── outbox/                            Append(ctx, event.Event): the events row and its events.fanout job in
│   │                                      the caller's transaction (decision 0015) (P7)
│   ├── idempotency/ ratelimit/            decision 0003 stores moved out of memory          (P2)
│   ├── account/ identity/ spaces/         accounts, users, groups, API keys, sessions; space tree, roles,
│   │                                      role_bindings                                     (P3, P4)
│   ├── stacks/                            stacks, stack_hooks, locks, push-routing index    (P5)
│   ├── config/                            config_elements, contexts, context_attachments   (P6)
│   ├── runs/                              runs, run_state_transitions, run_reviews, receipts, dependencies;
│   │                                      pool queue index (pool, state, priority, type, created_at) (P7)
│   ├── workers/                           worker_pools, workers, assignments                (P8)
│   ├── state/                             managed_state_versions, state locks              (P11)
│   ├── vcs/                               vcs_integrations, deliveries, pull_requests, ignored triggers (P12)
│   ├── policies/                          policies, policy_attachments, policy_evaluations  (P13)
│   ├── cloud/                             cloud_integrations, attachments                    (P15)
│   ├── schedules/                         scheduled_jobs, drift_detection, stack_dependencies (P16)
│   ├── registry/                          modules, versions, providers, platforms, gpg_keys  (P17)
│   ├── events/                            webhooks, deliveries, notifications; audit_trail_entries with
│   │                                      RecordAudit in the caller's transaction (decision 0015) (P18)
│   ├── templates/                         templates, versions, deployments, blueprints      (P21)
│   ├── resources/                         managed_entities, scans                            (P22)
│   ├── intelligence/                      intent_*, ai_integrations, chat_*                  (P25)
│   ├── flows/                             flows_* tables, flow_events, flow_invocations      (P26)
│   └── repotest/                          harness: one testcontainers PostgreSQL per package run (P2)
│
├── infra/                                 LAYER: external systems other than the database
│   ├── objectstore/                       Store: Put, Get, Head, Delete, SignURL; logical stores as key
│   │   │                                  prefixes, objects written once (decision 0005) (P2)
│   │   ├── s3/ fs/                        S3-compatible and local disk                      (P2)
│   │   ├── gcs/ azureblob/                (P27)
│   │   └── objectstoretest/               contract suite run by every backend
│   ├── crypto/                            envelope encryption, local key; sealed columns (decision 0013) (P2)
│   │   └── kms/                           AWS, GCP, Azure and Vault key backends            (P15, P27)
│   ├── queue/                             decision 0011's queue on the jobs table: EnqueueTx, Register,
│   │   │                                  CompleteTx, the scheduler ticker                   (P7)
│   │   └── queuetest/                     contract suite: claims, leases and fencing, retries and the dead
│   │                                      state, ordering keys, transactional enqueue
│   ├── jwt/                               signing keys, rotation, JWKS (decision 0006)      (P3)
│   ├── vcsclient/                         provider API clients: checks, comments, statuses, archives
│   │   ├── github/                        (P12)
│   │   └── gitlab/ bitbucketcloud/ bitbucketdc/ azuredevops/ rawgit/ (P19)
│   ├── vcsgateway/                        request envelope to private VCS through agent polls (P19)
│   ├── cloud/aws/ azure/ gcp/             role assumption and token exchange for integrations (P15)
│   ├── vault/                             (P15)
│   ├── httpout/                           signed outbound HTTP with retries for named webhooks (P18)
│   ├── slack/ smtp/                       (P18)
│   ├── idp/saml/ idp/oidc/                (P23)
│   └── ai/                                provider clients behind decision 0019's boundary  (P25)
│
├── service/                               LAYER: one package per domain; no service imports another,
│   │                                      except service/authz
│   ├── authz/                             Decider: reachable spaces, actions, bindings; For(ctx, principal) (P4)
│   ├── identity/                          accounts, users, groups, API keys, token exchange, sessions (P3)
│   │   ├── sso/ scim/                     (P23)
│   ├── spaces/                            space tree CRUD, re-parenting, inheritance switch  (P4)
│   ├── stacks/                            stack CRUD rules, settings validation, locks, enable, reslug (P5)
│   ├── config/                            contexts, config elements, attachments, auto-attach (P6)
│   ├── runs/                              the run state machine table T1 to T44, action guards, timers,
│   │                                      priority tiers, dedupe keys                       (P7, P9, P10)
│   ├── workers/                           pools, registration, credentials, drain, assignment (P8, P14)
│   ├── iac/                               server side of each IaC vendor
│   │   ├── iac.go                         Vendor interface: Validate, PlanInput, Changes, Versions
│   │   ├── terraform/ opentofu/           (P9)
│   │   └── terragrunt/ pulumi/ cloudformation/ kubernetes/ ansible/ (P20)
│   ├── state/                             state versions, locks, rollback, import, presign  (P11)
│   ├── vcs/                               integrations, intake, verification, normalisation, checks, comments
│   │                                      (P12, P19)
│   ├── policy/                            policy CRUD, attachments, Evaluate, combination per type, receipts,
│   │                                      sampling, simulate                                (P13)
│   ├── cloud/                             AWS, Azure, GCP integrations, OIDC token minting  (P15)
│   ├── schedules/                         scheduled runs, tasks and deletes; drift; dependency graph (P16)
│   ├── registry/                          modules, versions, providers, GPG keys, sharing   (P17)
│   ├── notify/                            inbox, grouping, dismissal                        (P18)
│   ├── webhooks/                          named and stack webhooks, delivery log           (P18)
│   ├── audit/                             audit trail entries, audit webhook               (P18)
│   ├── metrics/                           Prometheus series of decision 0017               (P18)
│   ├── templates/ blueprints/             (P21)
│   ├── resources/ search/                 managed entities, scans, generic search          (P22)
│   ├── intelligence/                      AI integrations, Infra Assistant, Intent projects (P25)
│   └── flows/                             Flows has its own tenancy; subpackages count as one domain (P26)
│       ├── org/ project/ flow/            organizations, projects, memberships, flow definitions
│       ├── engine/                        events, lineage, invocations, limits, echo, pending events
│       ├── apps/                          app installations, registries, bundles
│       └── agents/                        agent pools, tokens, routing of evaluations
│
├── workflow/                              TIER ABOVE SERVICES: cross-domain operations, in declared order
│   ├── runflow/                           1: create, transition, schedule, report, review, confirm (P7)
│   ├── stackflow/                         2: stack create, update, delete with destroy run   (P5)
│   ├── vcsevent/                          3: webhook delivery to push policy to runs; module releases (P12)
│   ├── notifyflow/                        4: notification policy routing, checks, PR comments (P18)
│   ├── templateflow/                      5: template deployments create stacks and runs    (P21)
│   ├── intentflow/                        6: Intent operations with policy and workers      (P25)
│   └── flowsdeploy/                       7: Flows Deploy blocks to runs and run events to flows (P26)
│
├── transport/                             LAYER: everything that speaks a protocol
│   ├── http/
│   │   ├── router.go                      chi router, mounts, base URL
│   │   ├── middleware.go                  request ID, recovery, body limits, idempotency, rate limits
│   │   ├── authn.go                       credentials to model.Principal on the context    (P3)
│   │   ├── strict.go                      composes the deploy StrictServerInterface by embedding
│   │   ├── problem/                       RFC 9457 problem details        ← internal/server/problem
│   │   ├── gen/
│   │   │   ├── deploy/                    generated strict server and UnimplementedStrict
│   │   │   │                              ← internal/server/gen/deploy
│   │   │   └── worker/                    generated worker-protocol server (P8)
│   │   ├── api/                           one partial strict implementation per resource family
│   │   │   ├── stacks/                    69 Stacks operations; behaviour P5
│   │   │   ├── runs/                      (P7, P10)
│   │   │   ├── account/ users/ apikeys/ sessions/ (P3)
│   │   │   ├── spaces/ roles/             (P4)
│   │   │   ├── contexts/                  (P6)
│   │   │   ├── workerpools/               (P8, P14)
│   │   │   ├── vcs/                       (P12, P19)
│   │   │   ├── policies/                  (P13)
│   │   │   ├── cloud/                     (P15)
│   │   │   ├── schedules/                 (P16)
│   │   │   ├── modules/ providers/        (P17)
│   │   │   ├── notifications/ webhooks/ audit/ usage/ (P18)
│   │   │   ├── templates/ blueprints/     (P21)
│   │   │   ├── resources/ search/         (P22)
│   │   │   ├── sso/ scim/                 (P23)
│   │   │   ├── intelligence/              (P25)
│   │   │   └── flows/                     (P26)
│   │   ├── webhook/                       hand-written VCS intake, one package per provider
│   │   │   ├── github/                    (P12)
│   │   │   └── gitlab/ bitbucketcloud/ bitbucketdc/ azuredevops/ (P19)
│   │   ├── workerapi/                     implements gen/worker: register, poll, reports, upload URLs (P8)
│   │   ├── statebackend/                  hand-written Terraform http backend               (P11)
│   │   ├── registry/                      discovery, modules.v1, providers.v1, login.v1     (P17)
│   │   ├── metrics/ health/               /metrics, /readiness, /startup                    (P18)
│   │   └── mcp/                           platform MCP server                               (P25)
│   ├── vcsgateway/                        VCS agent long-poll endpoints (decision 0012)     (P19)
│   ├── ws/flowsgateway/                   Flows agents over WebSockets                      (P26)
│   └── scheduler/                         queue consumers and cron entries; calls workflows  (P7)
│
└── worker/                                SEPARATE COLUMN: the worker role; imports model, support, own code
    ├── protoclient/                       generated worker-protocol client (from api/src/worker) (P8)
    ├── launcher/                          register, long-poll, start a container per job, stop and kill (P8)
    ├── runtime/docker/                    Docker socket, mounts the infraweave binary as root process (P8)
    ├── runtime/kubernetes/                pod per run                                       (P14)
    ├── controller/                        Kubernetes WorkerPool controller                  (P14)
    ├── exec/                              `worker exec`: phases, hooks, log shipping, workspace save and restore (P8)
    ├── iac/                               command sets per vendor
    │   ├── iac.go                         Commands: Init, Plan, Show, Outputs, Apply, Destroy
    │   ├── terraform/ opentofu/           (P9)
    │   └── terragrunt/ pulumi/ cloudformation/ kubernetes/ ansible/ (P20)
    ├── initpolicy/                        worker-side initialization policy over support/rego (P14)
    ├── vcsagent/                          `vcs-agent`: long-polls transport/vcsgateway; allowlist from
    │                                      support/vcsroutes                                 (P19)
    └── flowsagent/                        `flows-agent`: dials ws/flowsgateway; Node.js runtimes (P26)

pkg/
└── client/
    └── deploy/                            generated client of the served API  ← pkg/client/deploy
```

The service layer reaches about 45 packages at P27, the transport layer
about 45, and the workflow tier seven. Repository packages mirror the
service domains one for one, except that `repository/events` serves
`notify`, `webhooks` and `audit`, and identity and spaces share
`repository/identity` and `repository/spaces` with `service/authz`.

## Deep dives

### Stack configuration write path

```text
internal/transport/http/api/stacks/
├── stacks.go              type Stacks struct; StackCreate, StackUpdate, StackDelete, Stack, Stacks, SearchStacks
├── convert.go             generated request bodies to model.StackSettings and back; vendor blocks per vendor
├── lock.go enable.go      StackLock, StackUnlock, StackEnable, StackDisable
├── config.go              StackConfigAdd, StackConfigDelete, StackConfigElement (P6)
├── dependencies.go        StackDependency* operations (P16)
├── attachable.go          StackAttachable* listings (P6, P13, P15)
└── *_test.go              handler tests over fake workflows and services
internal/workflow/stackflow/
├── create.go              Create: authz, vendor validation, VCS binding check, pool space check, insert, outbox
├── update.go              Update: same checks; reslug; state-management switch refused after creation
├── delete.go              Delete: protectFromDeletion, optional destroy run through runflow (P10)
├── attach.go              context, policy and cloud attachments that cross domains (P6, P13, P15)
└── *_test.go
internal/service/stacks/   stacks.go (rules on the stack row), settings.go, lock.go, reslug.go, labels.go
internal/service/iac/   Validate(settings) per vendor, used by stackflow and runflow
internal/service/config/   contexts.go elements.go attach.go autoattach.go (P6)
internal/repository/stacks/  stacks.go hooks.go locks.go routing.go (vcs_integration, repository, branch)
internal/repository/config/  elements.go contexts.go attachments.go
internal/infra/crypto/     seals write-only configuration values (decision 0013)
internal/model/stack.go config.go
```

A stack update looks like CRUD, but in this layout it is a workflow. The
settings it carries belong to five domains: the vendor block (the vendor
service), the repository and branch (the VCS integration), the worker pool
and its space (workers and authz), and labels that auto-attach contexts and
policies (config and policy). Each check is a call to a different service,
and services may not call each other, so the composition sits in
`workflow/stackflow`. The cost shows up from P5: the first real domain
already needs the second tier.

```go
// internal/workflow/stackflow/update.go
// Update applies a whole-input stack update (decision 0022). Every check
// that reads another domain runs before the transaction, so no external
// call holds a row lock; the write and its audit entry commit together.
func (f *Flow) Update(ctx context.Context, p model.Principal, id model.StackID, in model.StackSettings) (model.Stack, error) {
	cur, err := f.stacks.Get(ctx, id)
	if err != nil {
		return model.Stack{}, err
	}
	if err := f.authz.For(ctx, p).Require(model.ActionStackWrite, cur.Space, in.Space); err != nil {
		return model.Stack{}, err
	}
	if err := f.vendors.For(in.Vendor.Kind).Validate(ctx, in.Vendor); err != nil {
		return model.Stack{}, err
	}
	if err := f.vcs.CheckBinding(ctx, in.Space, in.Source); err != nil { // (P12)
		return model.Stack{}, err
	}
	if err := f.workers.CheckPool(ctx, in.Space, in.WorkerPool); err != nil { // (P8)
		return model.Stack{}, err
	}
	var out model.Stack
	err = f.db.Tx(ctx, func(ctx context.Context) error {
		if out, err = f.stacks.Update(ctx, cur, in); err != nil {
			return err
		}
		return f.audit.RecordAudit(ctx, event.AuditEntry{Action: "stack.update", By: p, Before: cur, After: out}) // (P18)
	})
	return out, err
}
```

The audit entry is not a call to `service/audit`: workflows and services
write it through `repository/events` in the transaction of the change, as
decision 0015's `audit.Record` requires, and `service/audit` serves reads,
retention and the audit webhook (see Events). Writing through the
repository keeps rule 3 intact, because no service imports `service/audit`.

### Run execution path

```text
internal/transport/http/api/runs/       RunTrigger, TaskCreate, RunConfirm, RunDiscard, RunReview, RunStop,
                                        RunPromote, RunTargetedReplan, RunPrioritySet, ...       (P7, P10)
internal/transport/scheduler/
├── scheduler.go           Run(ctx): workers per logical queue and the scheduler ticker; every replica
│                          alike, no leader (decision 0011)
├── timers.go              2, 7 and 30 day timers; stale handover sweep                        (P10)
├── cron.go                scheduled runs, tasks, deletes; drift detection fire times         (P16)
└── consumers.go           handlers on the default, events, vcs-inbox, deliveries and maintenance
                           queues of decision 0011 (P7, P12, P18, P26)
internal/workflow/runflow/
├── create.go              Create(ctx, RunSpec): insert QUEUED, dedupe (stack, commit, type), cancel rules
├── transition.go          Transition(ctx, run, to, cause): one transaction per transition
├── gates.go               QUEUED to READY: stack lock, approval policies, upstream runs, external dependencies
├── schedule.go            the run.schedule job: QUEUED to READY for one stack (decision 0008)
├── claim.go               READY, CONFIRMED, REPLAN_REQUESTED to the PREPARING states: SKIP LOCKED claim
│                          in tier order from a worker's poll; run token issued
├── report.go              worker reports: phase start, plan ready, apply done, failure, logs closed
├── plan.go                plan policy input, evaluation, UNCONFIRMED, PENDING_REVIEW, APPLYING or FINISHED
├── review.go confirm.go   reviews, approval re-evaluation, confirm, discard, targeted replan  (P10)
├── trigger.go             trigger policies on terminal blocking runs; dependants          (P13, P16)
├── policyinput.go         builds run, stack and VCS input documents for every policy type  (P13)
└── *_test.go              table tests over the transitions, repository tests on PostgreSQL
internal/service/runs/
├── machine.go             the transition table T1 to T44 as data; Apply(run, to, cause) (Run, error)
├── actions.go             guard fields: CanConfirm, ConfirmBlocker, CanRetry, CanPromote, ...
├── priority.go            seven tiers from preset and type; PickOrder for the pool queue
├── timers.go              automatic termination rules                                      (P10)
└── dedupe.go              event deduplication key (stack, commit, type)
internal/service/workers/  wake order of held polls; Release; drain and availability          (P8)
internal/repository/runs/  runs.go (FOR UPDATE), transitions.go, reviews.go, receipts.go, queue.go
internal/infra/queue/      EnqueueTx joins the caller's transaction (decision 0011)
internal/model/run.go      RunState, RunType, Run, Transition
```

The state machine is split in two on purpose. `service/runs/machine.go`
holds the table of report 08's 43 transitions
([report 08](../reference/08-run-lifecycle.md#3-transitions)) and decision
0008's T44 as pure data
and answers "is this transition legal, and what does the run look like
after it". It touches no other domain, so it obeys rule 3. Everything a
transition causes elsewhere, the stack lock, the stack state, the queue,
policies, workers, events, happens in `workflow/runflow`, which is the
single writer of run state.

```go
// internal/workflow/runflow/transition.go
// Transition is the only function that changes a run's state. The run row,
// the stack lock and state, the poll wake-up, the next queue job and the
// event other domains hear commit together, so a crash between them cannot
// leave a finished run still holding the stack lock or a stack whose next
// queued run nobody evaluates.
func (f *Flow) Transition(ctx context.Context, id model.RunID, to model.RunState, c runs.Cause) error {
	return f.db.Tx(ctx, func(ctx context.Context) error {
		cur, err := f.runRepo.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		next, err := f.runs.Apply(cur, to, c) // service/runs: table T1 to T44
		if err != nil {
			return err
		}
		if err := f.runRepo.Save(ctx, next, c); err != nil { // appends run_state_transitions
			return err
		}
		if next.Type.Blocking() {
			if err := f.stacks.MirrorRunState(ctx, next); err != nil { // StackState, stateSetBy, lock release
				return err
			}
		}
		if next.State.Claimable() { // READY, CONFIRMED, REPLAN_REQUESTED
			if err := f.db.Notify(ctx, "infraweave_runs", next.Pool.String()); err != nil { // wakes a held poll
				return err
			}
		}
		if next.Type.Blocking() && next.State.Terminal() { // the stack's next queued run may now be READY
			if err := f.queue.EnqueueTx(ctx, queue.Job{Kind: "run.schedule", UniqueKey: "stack:" + next.Stack.String()}); err != nil {
				return err
			}
		}
		return f.outbox.Append(ctx, event.RunStateChanged{Run: next.ID, From: cur.State, To: next.State})
	})
}
```

How a run moves, and which package drives each step:

| Step | States | Driver |
| --- | --- | --- |
| Trigger | (new) to `QUEUED` | `transport/http/api/runs`, `workflow/vcsevent`, `transport/scheduler/cron.go`, `runflow/trigger.go` call `runflow.Create` |
| Gates | `QUEUED` to `READY` | `runflow/schedule.go` and `gates.go` in the `run.schedule` job, enqueued on create and on every event that can release a gate (lock released, review, upstream finished) |
| Claim | `READY` to `PREPARING` | `transport/http/workerapi/poll.go` in the `serve` role calls `runflow/claim.go`, which takes the oldest run of the highest of [report 08's tiers](../reference/08-run-lifecycle.md#priority) with `SKIP LOCKED` (decision 0008) |
| Execution | `INITIALIZING` to `PLANNING`, `APPLYING`, `PERFORMING`, `DESTROYING` | the worker reports through `transport/http/workerapi`; `runflow/report.go` transitions |
| Plan outcome | `PLANNING` to `UNCONFIRMED`, `PENDING_REVIEW`, `APPLYING`, `FINISHED`, `FAILED` | `runflow/plan.go` after the plan policy |
| People | `UNCONFIRMED` to `CONFIRMED`, `DISCARDED`, `REPLAN_REQUESTED` | `transport/http/api/runs` to `runflow/review.go`, `confirm.go` |
| Timers | any waiting state to `CANCELED`, `DISCARDED`, `FAILED` | `transport/scheduler/timers.go` with [report 08's timers](../reference/08-run-lifecycle.md#timers) |

The queue carries delivery, not the run record: a queue job says "look at
stack X's queued runs", a worker's poll claims runs straight from the run
table, and the run row in `repository/runs` stays the authority, as
[report 27](../reference/27-reference-materials.md#102-river) advises for
any queue choice. `infra/queue` is decision 0011's own queue on the `jobs`
table.

### Policy evaluation with embedded OPA

```text
internal/support/rego/                   pure: no database, no network
├── engine.go              Compile(body, engine, typ) (Prepared, error): capabilities without http.send,
│                          net.lookup_ip_addr, opa.runtime, rego.parse_module, time.now_ns, trace;
│                          SetRegoVersion v0 or v1
├── preamble.go            fixed preamble per policy type: result object and defaults
├── eval.go                Eval(ctx, prepared, input, now) (Result, error); deadline from the caller
├── cache.go               prepared queries keyed by policy ID, body hash and engine version
├── sanitize.go            the sanitized helper and sensitive-value masking for sampled inputs
└── testdata/library/      the 67 example-library policies and 61 tests as a parity corpus
internal/service/policy/
├── policy.go              CRUD, type alias normalisation (13 names), deprecation flags
├── attach.go              explicit attachments, autoattach labels by space inheritance
├── evaluate.go            Evaluate(ctx, typ, target, input) (Decision, error): attached set, per-policy
│                          deadline, combined budget, combination rule per type
├── combine.go             deny wins, ignore wins, all approvals, priority agreement, lock agreement
├── receipts.go            receipts and flags per evaluation; flag reset ordering
├── sample.go              sampled evaluations to object storage with hourly quotas         (P13)
└── simulate.go            simulate endpoint: body, input, libraries, engine
internal/workflow/runflow/policyinput.go   run, stack, VCS and vendor blocks for each run-bound type
internal/workflow/vcsevent/pushinput.go    push and pull request input for GIT_PUSH
internal/workflow/notifyflow/input.go      notification input documents                     (P18)
internal/repository/policies/              policies.go attachments.go evaluations.go
```

The OPA embedding is a support library, not infrastructure: it is a pure
function of a policy body and an input document, it holds no connection,
and the worker column needs it too, for the worker-side initialization
policy ([report 10](../reference/10-workers-and-execution.md#10-signed-runs-and-worker-side-policy)).
Putting it under `infra` would forbid the worker from using it.

Evaluation points follow
[report 09](../reference/09-policies.md#8-where-each-type-is-evaluated):

| Type | Called from |
| --- | --- |
| `LOGIN` | `service/identity` at session creation and API key exchange |
| `GIT_PUSH` | `workflow/vcsevent`, before any run exists |
| `APPROVAL` | `runflow/gates.go` on `QUEUED`; `runflow/plan.go` on `UNCONFIRMED` and `PENDING_REVIEW`; `runflow/review.go` on each review |
| `PLAN` | `runflow/plan.go` inside `PLANNING`, and on each targeted replan |
| `TRIGGER` | `runflow/trigger.go` on the five terminal states of blocking runs; `vcsevent` on module releases |
| `NOTIFICATION` | `workflow/notifyflow` on every event |
| `INITIALIZATION` | `runflow/gates.go` server-side; `worker/initpolicy` worker-side |
| `TASK`, `ACCESS` | `transport/http/api/runs` (task create) and `service/authz` (deprecated access policy) |
| `INTENT` | `workflow/intentflow` (P25) |

The input documents need data from runs, stacks, VCS and vendors, which is
why they are built in workflows and passed to `service/policy` as a value.
`service/policy` never reads a run; it evaluates what it is given.

```go
// internal/service/policy/evaluate.go
// Evaluate runs every policy of one type attached to target, each against
// its own prepared query, then combines the results by the type's rule. The
// per-policy deadline comes from report 09 section 2.3 (plan 45 s, push and
// notification 10 s, others 5 s) and the combined budget bounds the event.
func (s *Service) Evaluate(ctx context.Context, typ model.PolicyType, t model.Target, input any) (Decision, error)
```

### Worker protocol and pools

```text
api/src/worker/                          worker protocol fragments (P8)
internal/transport/http/gen/worker/      generated strict server
internal/transport/http/workerapi/
├── register.go            JWS signed with the pool key, certificate chain, worker row, session token
├── poll.go                heartbeat and long-poll: claims a run through runflow, or answers stop, kill,
│                          cycle, the drain flag and a renewed session token
├── spec.go                the run specification, secrets sealed to the pool key
├── report.go              phase reports, plan ready, apply done, failure; calls runflow
├── uploads.go             pre-signed URLs; log chunk and artifact records; the workspace redirect
└── deregister.go          clean exit
internal/service/workers/
├── pools.go               pool CRUD, credentials (CSR-signed certificate), public pool
├── workers.go             registration, status decay, drain, undrain, cycle, reset
├── wake.go                which held poll a claimable run wakes: the newest idle worker's
└── limits.go              drift run limit, preparing timeout                                  (P14)
internal/repository/workers/             pools.go workers.go assignments.go
internal/worker/                         the column (tree above)
cmd/infraweave/worker.go                 `worker` and hidden `worker exec`
```

The protocol is HTTP long-poll, worker-initiated
([report 10](../reference/10-workers-and-execution.md#42-transports);
decision 0008), so the server never dials a worker. `transport/http/workerapi`
is an ordinary transport package: it calls `service/workers` for
registration and assignment and `workflow/runflow` for every report that
moves a run.

On the worker side, the column is a small program of its own:

| Package | Role |
| --- | --- |
| `worker/protoclient` | generated client of `api/src/worker`; internal because the protocol is versioned with the binary (`/worker/v1`) and is not a public contract |
| `worker/launcher` | registers, long-polls, evaluates the worker-side initialization policy, starts a container per job, relays stop and kill |
| `worker/runtime/docker`, `kubernetes` | how a job container or pod starts; the launcher mounts its own static binary into the runner image |
| `worker/exec` | `infraweave worker exec`, the container's root process: hooks, vendor commands, log shipping by pre-signed URL, workspace encryption to the pool key |
| `worker/iac/<v>` | command sets per vendor (init, plan, show, outputs, apply, destroy) |

The column shares `internal/model` and `internal/support` with the server,
which is the right amount of sharing: the run states, vendor settings and
the Rego engine are the same types on both sides. It cannot import a
service, so a rule the worker must apply (for example which phases may
write state) is either a method on a model type or a value the server sends
in the job.

### VCS webhook ingest

```text
internal/transport/http/webhook/
├── webhook.go             common: size limits, delivery ID, 202 after durable intake
├── github/github.go       POST /webhooks/vcs/github/{integration}: headers, event name (P12)
└── gitlab/ bitbucketcloud/ bitbucketdc/ azuredevops/                                     (P19)
internal/service/vcs/
├── intake.go              Intake(ctx, integration, headers, body): verify, store, enqueue in one transaction
├── verify.go              HMAC SHA-256, token header, basic auth per provider
├── normalize.go           provider payloads to model.PushEvent and model.PullRequest
├── checks.go comments.go  commit statuses, checks, PR comments with dedupe keys          (P12)
├── deployments.go         GitHub deployments and environments
├── integrations.go        CRUD, default per provider, space moves, webhook URL from name
└── agentpools.go          VCS agent pools and tokens                                       (P19)
internal/workflow/vcsevent/
├── handle.go              Handle(ctx, delivery): normalise, match stacks and modules, 10-minute budget
├── push.go                per target: push policy, track, propose, ignore, cancel, lock, priority
├── pushinput.go           push policy input
├── modules.go             tag pushes to module versions and test runs                     (P17)
└── ignored.go             ignored-trigger records
internal/infra/vcsclient/github/         App JWT, installation tokens, API calls, archive download
internal/repository/vcs/                 integrations.go deliveries.go pullrequests.go ignored.go
```

Intake is durable before the acknowledgement
([report 27](../reference/27-reference-materials.md#12-cross-tool-comparison);
decision 0012): the transport package calls `service/vcs.Intake`, which
verifies the signature, stores the delivery and enqueues a
`vcs.process_delivery` job on the `vcs-inbox` queue in the same transaction, then answers `202`. The heavy work runs in the
scheduler role: `transport/scheduler/consumers.go` takes the job and calls
`workflow/vcsevent.Handle`, which follows
[report 11](../reference/11-vcs-integrations.md#112-what-an-event-becomes)
step by step. Provider differences stay in two places, the provider's
transport package and its `infra/vcsclient` package; everything after
normalisation is provider-neutral.

### State backend

```text
internal/transport/http/statebackend/
├── backend.go             GET, POST, LOCK, UNLOCK on /state/v1/stacks/{stack} (decision 0010); Basic auth
├── external.go            read-only external access through the remote API                (P11)
└── *_test.go
internal/service/state/
├── access.go              Authorize(ctx, credential, op): the run is live and its phase may read or write
├── versions.go            write each version as a new object, record managed_state_versions, history
├── locks.go               lock ID checks, plan lock opt-out
├── rollback.go import.go  rollback as a new version; import at creation and on an idle stack
└── presign.go             pre-signed upload and download URLs
internal/repository/state/               versions.go locks.go
internal/repository/runs/                read-only: run phase for Authorize
internal/infra/objectstore/              states store: one sealed object per version, written once
```

`service/state` needs to know whether a run is live and in a phase that may
write state ([report 16](../reference/16-state-management.md#11-how-a-run-reaches-it)).
It cannot ask `service/runs`, a sibling, so it reads the run row through
`repository/runs` and asks the model (`model.RunState.MayWriteState()`) for
the rule. That is legal in the layered pattern, because services may use
any repository, but it means two services read the same table and the rule
must live in `model` to stay single.

### Module and provider registry protocol

```text
internal/transport/http/registry/
├── discovery.go           /.well-known/terraform.json: login.v1, modules.v1, providers.v1, state.v2
├── modules.go             {namespace}/{name}/{provider}/versions, /{version}/download (204, X-Terraform-Get)
├── providers.go           {namespace}/{type}/versions, /{version}/download/{os}/{arch}
├── login.go               login.v1 authorization code and token endpoints
└── *_test.go              protocol tests against captured responses
internal/service/registry/
├── modules.go versions.go numbering, PENDING, ACTIVE, FAILED, yanked; metadata extraction
├── providers.go           drafts, publish, revoke, platforms, checksums
├── gpg.go                 account GPG keys and revocation
├── sharing.go             space availability, cross-account sharing
└── download.go            signed URLs from infra/objectstore
internal/workflow/vcsevent/modules.go    tag push to version and TESTING runs through runflow
internal/transport/http/api/modules/, providers/   the REST resources
internal/repository/registry/
```

The registry protocols are hand-written chi routes outside oapi-codegen,
because their shapes are Terraform's, not infraweave's
([report 15](../reference/15-module-and-provider-registry.md#9-registry-protocol)).
A module test run is an ordinary run of type `TESTING`, so the release flow
goes through `workflow/vcsevent` and `workflow/runflow`; `service/registry`
only records versions and their states.

### Access, identity and spaces

```text
internal/transport/http/authn.go         API key, session cookie, bearer token to model.Principal
internal/service/identity/               users.go groups.go apikeys.go exchange.go sessions.go login.go (P3)
internal/service/spaces/                 tree.go reparent.go inheritance.go                        (P4)
internal/service/authz/
├── authz.go               For(ctx, principal) Decider; Require(action, spaces...); Reachable(ctx)
├── tree.go                propagation down, read up through inheritance, implicit stack read
├── actions.go             the action catalogue and legacy fallback levels
└── bindings.go            five actor kinds; stack escalation rules
internal/infra/jwt/                      signing keys, rotation, JWKS
internal/infra/idp/                      SAML and OIDC (P23)
internal/repository/identity/ spaces/
```

`service/authz` is the one service every other service may import. It
reads `repository/spaces` and `repository/identity` directly rather than
asking `service/spaces`, because a sibling import would break rule 3 for
the one package that must be importable from everywhere. Every list query
also needs the caller's reachable spaces
([report 28](../reference/28-data-model.md#121-shapes-that-recur)), so
repository list functions take a `[]model.SpaceID` filter computed once per
request by `authz.Reachable`.

### Events: notifications, webhooks and audit

```text
internal/model/event/                    the catalogue: RunStateChanged, ModuleVersionChanged, InternalError,
                                         WorkerUsage; AuditEntry; Kind(), Encode()
internal/repository/outbox/              Append: the events row and an events.fanout job in the caller's
                                         transaction (decision 0015)
internal/repository/events/              RecordAudit in the caller's transaction; deliveries, inbox
internal/transport/scheduler/consumers.go   events.fanout computes the targets and enqueues one events.deliver
                                         job per target
internal/workflow/notifyflow/
├── route.go               notification policy per space; inbox, slack, webhook, pull_request rules
├── checks.go              commit statuses and checks for run events (P12)
└── input.go
internal/service/notify/                 inbox entries, grouping keys, dismissal, 14-day retention
internal/service/webhooks/               named and stack webhooks, secret headers, deliveries, auto-disable
internal/service/audit/                  audit entries for every resource-changing event; audit webhook
internal/infra/httpout/                  HMAC-SHA256 and HMAC-SHA1 signature headers, retries (decision 0015)
internal/infra/slack/
```

The layered pattern has no event mechanism, so this layout adds one at the
bottom: an outbox in `repository/outbox` that every workflow and service
appends to inside its transaction, and the fan-out and delivery jobs in
`transport/scheduler`. Producers do not know their consumers. That breaks
the pattern's habit of producer-driven calls on purpose, because
[report 19](../reference/19-events-notifications-audit.md#1-the-outbound-surfaces)
lists six sinks for the same run event; calling each from each producer
would edit every workflow for every new sink. The audit trail is the
exception: its entry commits with the mutation, through
`repository/events`. The cost is that the event catalogue in `model/event` is a
second shared vocabulary every layer edits.

### Flows engine

```text
internal/service/flows/
├── org/ project/          organizations, projects, memberships, roles (Consumer, Viewer, Editor, Admin)
├── flow/                  flow definitions, blocks, links, validation
├── engine/                events with parents, invocations, statuses, monthly and hourly limits, echo,
│                          pending events, retry and re-emit
├── apps/                  installations, registries, bundles
└── agents/                agent pools, tokens, routing of JavaScript evaluations
internal/workflow/flowsdeploy/           Deploy blocks trigger runs through runflow; run events into flows
internal/transport/http/api/flows/       REST for flows, projects, installations (P26)
internal/transport/ws/flowsgateway/      agents dial in over WebSockets
internal/worker/flowsagent/              runs Node.js runtimes in Docker
internal/repository/flows/               all flows_* tables
internal/model/flows/
```

Flows has its own tenancy and UUID identities
([report 23](../reference/23-flows.md#2-resource-hierarchy)), so it is the
first domain big enough to need internal structure. The layered tree has
only one level per domain, so `service/flows` gets subpackages that count
as one domain for rule 3: `engine` may import `flow`, but nothing outside
`service/flows` may. Its tables share the one migration sequence. It
touches Deploy only through `workflow/flowsdeploy` and the event outbox,
which keeps it separable in principle, but every layer of the tree carries
a Flows directory, so extracting it later means cutting six trees.

### Composition root

```text
cmd/infraweave/
├── main.go                cobra root; binds setting flags only (not --config or --help)
├── serve.go scheduler.go all.go   each: config.Load, app.Serve / app.Schedule / app.All
├── worker.go              worker: config.LoadWorker, worker/launcher.Run; worker exec: worker/exec.Main
├── agents.go              vcs-agent: worker/vcsagent.Run (P19); flows-agent: worker/flowsagent.Run (P26)
├── migrate.go             migrate up, migrate status: app.Migrate, app.MigrateStatus (P2)
└── version.go             support/buildinfo
internal/app/              Open, services.go, workflows.go, serve.go, schedule.go, all.go, migrate.go
internal/config/           Config{Server, Scheduler, Worker, Database, ObjectStore, Queue, Crypto}
```

```go
// internal/app/workflows.go (shape at P13)
func buildWorkflows(p *platform, s *services) *workflows {
	run := runflow.New(runflow.Deps{
		DB: p.db, Queue: p.queue, Outbox: p.outbox, RunRepo: p.repos.runs,
		Runs: s.runs, Stacks: s.stacks, Workers: s.workers, Policy: s.policy, Vendors: s.vendors,
	})
	stack := stackflow.New(stackflow.Deps{
		DB: p.db, Outbox: p.outbox, Authz: s.authz, Stacks: s.stacks, Vendors: s.vendors,
		VCS: s.vcs, Workers: s.workers, Config: s.config, Runs: run,
	})
	vcs := vcsevent.New(vcsevent.Deps{
		DB: p.db, VCS: s.vcs, Stacks: s.stacks, Policy: s.policy, Registry: s.registry, Runs: run,
	})
	return &workflows{run: run, stack: stack, vcs: vcs}
}
```

The server roles are functions over one `platform`; the agent and worker
roles never build it:

| Role | Builds | Starts |
| --- | --- | --- |
| `serve` | platform, services, workflows, transport/http | the chi server; no queue consumers |
| `scheduler` | platform, services, workflows, transport/scheduler | queue workers and the scheduler ticker, the same on every replica (decision 0011) |
| `all` | both | both, for single-node installs and acceptance tests |
| `worker` | none of the above | `internal/worker/launcher`, with `worker/protoclient` pointed at the server URL |
| `vcs-agent`, `flows-agent` | none of the above | `internal/worker/vcsagent` or `internal/worker/flowsagent`, dialling `serve` |
| `migrate up`, `migrate status` | the database only | `app.Migrate` or `app.MigrateStatus`, then exit |

`serve` and `scheduler` build the same services and workflows, so the
split costs nothing in code; the difference is which transport packages
start. The worker role does not go through `internal/app` at all, which is
what rule 6 protects. By P27 `services.go` holds about 45 constructors and
`workflows.go` seven, each written out by hand.

### Generated code

| Spec | Fragments | Generated server | Implemented by | Client |
| --- | --- | --- | --- | --- |
| deploy | `api/src/deploy/` | `internal/transport/http/gen/deploy` | `transport/http/strict.go` composing `transport/http/api/<family>` | `pkg/client/deploy` |
| worker (P8) | `api/src/worker/` | `internal/transport/http/gen/worker` | `transport/http/workerapi` | `internal/worker/protoclient` |
| none | hand-written chi routes, tagged `handwritten` in the spec where they appear | none | `transport/http/webhook`, `statebackend`, `registry` | none |

```go
// internal/transport/http/strict.go
// deployServer implements deploy.StrictServerInterface. Each family's
// partial implementation is embedded at depth one and the 501 stubs at
// depth two, so an implemented operation shadows its stub and two families
// claiming one operation make the method ambiguous, which fails to compile
// at the assignment below.
type deployServer struct {
	*stacksapi.Stacks
	*runsapi.Runs           // (P7)
	*contextsapi.Contexts   // (P6)
	*policiesapi.Policies   // (P13)
	stubs
}

type stubs struct{ deploy.UnimplementedStrict }

var _ deploy.StrictServerInterface = (*deployServer)(nil)
```

`internal/tooling/stubgen` keeps writing `UnimplementedStrict` beside each
generated server; the `generate` target in the `Makefile` changes only the
path it passes (`internal/transport/http/gen/$$s/server.gen.go`), and
`verify-generate` diffs `internal/transport/http/gen` instead of
`internal/server/gen`. `api/oapi-server-deploy.yaml` changes its `output`
to `internal/transport/http/gen/deploy/server.gen.go`;
`api/oapi-client-deploy.yaml` is unchanged. `internal/tooling/specref` and
`tools/specref` keep their roles (reference generation, scaffolding, report
29), and `internal/tooling/openapibundle` keeps bundling `api/src/<spec>`.

### Tests and enforcement

| Kind | Location |
| --- | --- |
| Unit tests | beside the code in every package; workflows tested over fake services built from small interfaces declared in the workflow package |
| Repository tests | each `repository/<domain>` against PostgreSQL from testcontainers, through `repository/repotest` |
| Contract suites | `infra/objectstore/objectstoretest`, `infra/queue/queuetest`, `support/rego` with the example-library corpus |
| State machine | `service/runs/machine_test.go`: every row of the transition table and every illegal pair |
| Worker | `internal/worker/exec` against a local OpenTofu binary; `launcher` against an `httptest` worker API |
| Acceptance | `test/acceptance/features/<area>_<topic>.feature` over `infraweave all` with PostgreSQL, object storage and a real Docker worker (decision 0016) |
| Docs | `internal/tooling/docscheck` through `make docs-check` |

Enforcement is depguard in `.golangci.yml`, the only linter. The layer
rules are one entry each; rule 3 needs one entry per service domain and
rule 4 one per workflow:

```yaml
linters:
  enable:
    - depguard
  settings:
    depguard:
      rules:
        model:
          list-mode: lax
          files: ["**/internal/model/**"]
          deny:
            - pkg: github.com/weaveplatform/infraweave/internal
              desc: model imports nothing internal except model
          allow:
            - github.com/weaveplatform/infraweave/internal/model
        infra-no-repository:
          files: ["**/internal/infra/**"]
          deny:
            - pkg: github.com/weaveplatform/infraweave/internal/repository
            - pkg: github.com/weaveplatform/infraweave/internal/service
            - pkg: github.com/weaveplatform/infraweave/internal/workflow
            - pkg: github.com/weaveplatform/infraweave/internal/transport
        transport-no-storage:
          files: ["**/internal/transport/**"]
          deny:
            - pkg: github.com/weaveplatform/infraweave/internal/repository
            - pkg: github.com/weaveplatform/infraweave/internal/infra
        worker-column:
          files: ["**/internal/worker/**"]
          deny:
            - pkg: github.com/weaveplatform/infraweave/internal/repository
            - pkg: github.com/weaveplatform/infraweave/internal/infra
            - pkg: github.com/weaveplatform/infraweave/internal/service
            - pkg: github.com/weaveplatform/infraweave/internal/workflow
            - pkg: github.com/weaveplatform/infraweave/internal/transport
            - pkg: github.com/weaveplatform/infraweave/internal/app
            - pkg: github.com/jackc/pgx
              desc: a private worker never links the database driver
        service-stacks:                       # one rule like this per service domain
          list-mode: lax
          files: ["**/internal/service/stacks/**"]
          deny:
            - pkg: github.com/weaveplatform/infraweave/internal/service
            - pkg: github.com/weaveplatform/infraweave/internal/workflow
            - pkg: github.com/weaveplatform/infraweave/internal/transport
          allow:
            - github.com/weaveplatform/infraweave/internal/service/stacks
            - github.com/weaveplatform/infraweave/internal/service/authz
        workflow-vcsevent:                    # one rule like this per workflow
          files: ["**/internal/workflow/vcsevent/**"]
          deny:
            - pkg: github.com/weaveplatform/infraweave/internal/workflow/notifyflow
            - pkg: github.com/weaveplatform/infraweave/internal/workflow/templateflow
            - pkg: github.com/weaveplatform/infraweave/internal/workflow/intentflow
            - pkg: github.com/weaveplatform/infraweave/internal/workflow/flowsdeploy
            - pkg: github.com/weaveplatform/infraweave/internal/transport
```

The `model` and per-service rules use `lax` mode, in which depguard
resolves an import that matches both a deny and an allow prefix in favour
of the longer one, so `model/event` may import `model` and a domain may
import itself and `service/authz` while every other service is denied. In
the default `original` mode a deny always wins, and a non-empty allow list
denies everything it does not name, the standard library included. Entries
are plain string prefixes, so `internal/service/run` would also match
`internal/service/runs`, and a list must not hold an entry together with a
longer entry it prefixes: depguard v2.2.1 looks up only the nearest entry in
sort order and then misses imports that sort after the longer one. The
workflow rules deny every later workflow, so the
order is spelled out in seven rules and a new workflow edits all the rules
before it. `.golangci.yml` reports only new issues (`new-from-merge-base`
with `whole-files`), which suits these rules: a forbidden import always
arrives in a changed file.

Coverage implications:

- `.testcoverage.yml` excludes `^internal/transport/http/gen/` and
  `^internal/worker/protoclient/` (generated) instead of
  `^internal/server/gen/`.
- `internal/model` and `internal/model/event` hold mostly types; their
  methods (`Terminal`, `Blocking`, `MayWriteState`, `Topic`) need small
  table tests to reach 90%.
- Thin `transport/http/api/<family>` packages (account, usage, sso) are
  mostly conversion code; each needs handler tests per operation.
- `repository/<domain>` packages are tested only against real PostgreSQL,
  so every repository package's coverage comes from testcontainers runs,
  which the unit job on macOS and Windows runners cannot start; their
  coverage depends on the Linux job and the merged profile.

## Walkthroughs

### A GitHub push becomes a proposed run, a plan policy and a PR comment

1. `internal/transport/http/webhook/github/github.go`: `POST /webhooks/vcs/github/{integration}`
   reads the event name and delivery ID and calls `service/vcs.Intake`.
2. `internal/service/vcs/intake.go` and `verify.go`: the HMAC signature is checked with the
   integration's secret (sealed by `infra/crypto`); in one `repository.Tx` the delivery is
   stored by `repository/vcs/deliveries.go` and a `vcs.process_delivery` job is enqueued by
   `infra/queue.EnqueueTx`. The handler answers `202`.
3. `internal/transport/scheduler/consumers.go` (scheduler role): takes the job and calls
   `workflow/vcsevent.Handle`.
4. `internal/workflow/vcsevent/handle.go`: `service/vcs.Normalize` turns the payload into a
   `model.PushEvent` for a branch other than the tracked branch; `service/stacks` matches
   stacks through `repository/stacks/routing.go`.
5. `internal/workflow/vcsevent/push.go` and `pushinput.go`: for each stack, builds the push
   input and calls `service/policy.Evaluate(GIT_PUSH)`, which runs `support/rego` with a 10 s
   deadline; the default policy proposes because a file under the project root changed.
6. `internal/workflow/runflow/create.go`: dedupe on (stack, commit, `PROPOSED`) in
   `repository/runs`; insert the run in `QUEUED`; stop superseded proposed runs on the
   branch; `gates.go` finds no blocker; `transition.go` moves it to `READY`, notifies the
   pool's held polls, and appends `RunStateChanged` to the outbox.
7. `internal/workflow/notifyflow/checks.go`: consumes `RunStateChanged` and asks
   `service/vcs.ReportCheck` to set the GitHub check to in progress through
   `infra/vcsclient/github`.
8. `internal/transport/http/workerapi/poll.go` (serve role): a free worker's held poll wakes;
   `runflow/claim.go` takes the run in `service/runs.PickOrder` (normal proposed is tier 3),
   moves it to `PREPARING` and issues the run token.
9. `internal/worker/launcher/poll.go` on the worker host: its open long-poll returns the
   assignment; the launcher fetches the run specification and starts the runner image
   through `worker/runtime/docker` with the binary mounted.
10. `internal/worker/exec/`: `infraweave worker exec` runs hooks and
    `worker/iac/opentofu` init and plan, shipping logs through pre-signed URLs from
    `workerapi/uploads.go`; each phase start is a report that `runflow/report.go` turns into a
    transition (`INITIALIZING`, `PLANNING`).
11. `internal/worker/exec/plan.go`: uploads the JSON plan and reports "plan ready".
12. `internal/transport/http/workerapi/report.go` to `internal/workflow/runflow/plan.go`:
    `service/iac/opentofu.PlanInput` shapes the vendor block; `policyinput.go` adds run,
    stack and VCS data; `service/policy.Evaluate(PLAN)` runs with a 45 s deadline and
    `receipts.go` stores the receipt. No warning and no approval policy, so the proposed run
    moves to `FINISHED`.
13. `internal/workflow/notifyflow/route.go`: consumes `RunStateChanged` (`FINISHED`), evaluates
    the space's `NOTIFICATION` policy; its `pull_request` rule yields a comment body and a
    deduplication key.
14. `internal/service/vcs/comments.go`: posts or edits the PR comment through
    `infra/vcsclient/github`; `checks.go` completes the check with the change summary.

### A tracked run awaits approval and applies on a private worker

1. `internal/transport/http/api/runs/trigger.go`: `RunTrigger` for a stack on private pool
   `P`; `service/authz` checks the trigger action.
2. `internal/workflow/runflow/create.go`: the run is inserted in `QUEUED` as `TRACKED`;
   `gates.go` takes the stack lock (`service/stacks` through `repository/stacks/locks.go`) and
   evaluates `APPROVAL` on `QUEUED`, which approves; the run moves to `READY`.
3. `internal/transport/http/workerapi/poll.go`: a worker of `P` polls; `runflow/claim.go`
   takes the run in tier order (normal blocking, tier 2); `PREPARING`.
4. `internal/worker/launcher/` on the customer host: registered with the pool's
   certificate, its long-poll returns the job; `initpolicy` evaluates the worker-side
   initialization policy with `support/rego`; the container starts.
5. `internal/worker/exec/`: init and plan; the state backend serves reads with the run's
   one-off credential through `transport/http/statebackend` and `service/state/access.go`.
6. `internal/workflow/runflow/plan.go`: the plan policy passes, autodeploy is off, so the run
   moves to `UNCONFIRMED`; the worker encrypts the workspace to the pool's key and uploads it
   through a pre-signed URL; `APPROVAL` is evaluated on `UNCONFIRMED` and requires a review.
7. `internal/transport/http/api/runs/review.go` to `internal/workflow/runflow/review.go`: a
   reviewer approves; `repository/runs/reviews.go` stores the review and the approval policies
   are evaluated again; `Run.canConfirm` becomes true.
8. `internal/transport/http/api/runs/confirm.go` to `runflow/confirm.go`: `CONFIRMED`; the
   transition notifies pool `P`'s held polls.
9. `internal/transport/http/workerapi/poll.go`: a worker of `P`, possibly another one, claims
   the run for the second handover; `PREPARING_APPLY`.
10. `internal/worker/exec/restore.go`: downloads and decrypts the workspace; `APPLYING`;
    `worker/iac/opentofu` applies the saved plan.
11. `internal/transport/http/statebackend/backend.go`: `LOCK`, `POST` and `UNLOCK` from the
    apply; `service/state/versions.go` writes the new version as a new object in the `states`
    store and a `managed_state_versions` row.
12. `internal/workflow/runflow/report.go`: apply done; `FINISHED`; `transition.go` mirrors the
    stack state and releases the stack lock in the same transaction.
13. `internal/workflow/runflow/trigger.go`: `TRIGGER` policies run for the terminal blocking
    run and may create runs on dependent stacks.
14. Asynchronously: `notifyflow` updates the GitHub deployment and notifications;
    `service/webhooks` delivers stack webhooks; the audit entries for the review and the
    confirm were recorded in their own transactions.

### Adding an IaC vendor: Pulumi (P20)

1. `internal/model/iac/pulumi.go`: the `Pulumi` vendor value and its settings struct
   (login URL, stack name), shared by server and worker.
2. `api/src/deploy/components-common.yaml`: the Pulumi vendor input schema; `make generate`.
3. `internal/transport/http/api/stacks/convert.go`: map the generated vendor input to
   `model/iac.Pulumi` and back.
4. `internal/service/iac/pulumi/pulumi.go`: `Validate`, the `pulumi` block of the plan
   policy input
   ([report 20](../reference/20-vendors.md#2-plan-policy-input-per-vendor)), and the change
   model parsed from `pulumi preview --json`.
5. `internal/service/iac/registry.go`: register the server side.
6. `internal/worker/iac/pulumi/pulumi.go`: commands for init (`pulumi login`, stack
   select), plan (`preview`), apply (`up`), destroy and outputs, with skip flags.
7. `internal/worker/iac/registry.go`: register the worker side.
8. `internal/worker/iac/pulumi/pulumi_test.go`, `testdata/`; `internal/service/iac/pulumi/pulumi_test.go`.
9. `internal/support/rego/testdata/`: plan policy cases that read the `pulumi` block.
10. `test/acceptance/features/vendors_pulumi.feature` and steps: a Pulumi stack plans and
    applies on a Docker worker with a Pulumi runner image.

What else changes: nothing in `workflow/runflow`, because it calls vendors
through `service/iac.Vendor`, and nothing in `repository/stacks`, because
vendor settings are stored as a typed JSON column. About 10 new files and 5
edited, in two trees (`service/iac` and `worker/iac`) plus model and
the spec. The two halves of one vendor live in different layers by
necessity: one runs in the server, the other on a customer host.

### Adding a business domain: templates (P21)

1. `api/src/deploy/paths-templates.yaml`: templates, versions and deployments; `make generate`.
2. `internal/model/template.go`: `Template`, `TemplateVersion`, `TemplateDeployment`.
3. `internal/repository/migrations/NNNNN_templates.sql`: the tables of
   [report 28](../reference/28-data-model.md#12-storage-tables-i) section 12.8.
4. `internal/repository/templates/templates.go`, `versions.go`, `deployments.go`, tests on
   PostgreSQL.
5. `internal/service/templates/templates.go`, `versions.go` (publish, deprecate, input
   validation with `support/jsonschema`), tests.
6. `internal/workflow/templateflow/deploy.go`: render a version with inputs, create or update
   stacks through `workflow/stackflow`, trigger runs through `workflow/runflow`, record the
   deployment change; `delete.go` for scheduled deletes.
7. `internal/transport/http/api/templates/templates.go`: the partial strict implementation.
8. `internal/transport/http/strict.go`: embed `*templatesapi.Templates`.
9. `internal/model/event/template.go`: `TemplateDeploymentChanged` for audit and notifications.
10. `internal/service/audit/catalogue.go`: the audit resource type and metadata.
11. `internal/app/services.go` and `workflows.go`: construct the service and the workflow.
12. `.golangci.yml`: a `service-templates` rule, a `workflow-templateflow` rule, and the
    new workflow added to the deny lists of every earlier workflow rule.
13. `test/acceptance/features/templates_deployments.feature` and steps.

What else changes: `repository/stacks` gains the nullable
`template_deployment` column, and `workflow/stackflow` gains a field for the
deployment that owns a stack. About 18 new files and 8 edited, across six
directories under `internal/` (model, repository, service, workflow,
transport, app) plus the spec, the lint configuration and the tests.

## Package mapping

| Today's package | New location | Notes |
| --- | --- | --- |
| `cmd/` (only `cmd/README.md`) | `cmd/infraweave`, `cmd/infraweavectl` (P24) | `README.md` stays; `cmd/infraweave` is new with P1 and remains excluded from coverage |
| `api` (`api/doc.go`, `api/deploy_test.go`) | `api` | unchanged; still embeds the bundle and the reference; parity test stays |
| `api/src/deploy` | `api/src/deploy` | unchanged; `api/src/worker` added (P8) |
| `api/reference/spacelift` | `api/reference/spacelift` | unchanged |
| `api/oapi-server-deploy.yaml` | same file | `output: internal/transport/http/gen/deploy/server.gen.go` |
| `api/oapi-client-deploy.yaml` | same file | unchanged output `pkg/client/deploy` |
| `internal/buildinfo` | `internal/support/buildinfo` | pure; `LDFLAGS` in the `Makefile` follow the new import path |
| `internal/docscheck` | `internal/tooling/docscheck` | `make docs-check` runs the new path |
| `internal/openapibundle` | `internal/tooling/openapibundle` | |
| `internal/server/gen/deploy` | `internal/transport/http/gen/deploy` | generated; coverage exclusion follows |
| `internal/server/problem` | `internal/transport/http/problem` | used by every transport package; workflows return typed errors that it maps |
| `internal/specref` | `internal/tooling/specref` | |
| `internal/stubgen` | `internal/tooling/stubgen` | |
| `pkg/client/deploy` | `pkg/client/deploy` | unchanged; the only public package |
| `tools/openapi-bundle` | `tools/openapi-bundle` | imports `internal/tooling/openapibundle` |
| `tools/specref` | `tools/specref` | imports `internal/tooling/specref` |
| `tools/stubgen` | `tools/stubgen` | imports `internal/tooling/stubgen` |
| `test/acceptance` | `test/acceptance` | unchanged; features added per domain |
| `Makefile` | `Makefile` | `generate` passes `internal/transport/http/gen/$$s/server.gen.go` to stubgen; `verify-generate` diffs `internal/transport/http/gen`; `LDFLAGS` name `internal/support/buildinfo`; `docs-check` runs `./internal/tooling/docscheck` |
| `.golangci.yml` | `.golangci.yml` | depguard enabled with the rules above |
| `.testcoverage.yml` | `.testcoverage.yml` | `^internal/transport/http/gen/` and `^internal/worker/protoclient/` replace `^internal/server/gen/` |

## Numbers

Estimates, from the tree above and the phase spine.

| Measure | Today (P1) | End of P27 |
| --- | --- | --- |
| Go packages | about 16 (today's 13 plus `cmd/infraweave`, `transport/http` and `transport/http/api/stacks`) | about 200 |
| Top-level directories under `internal/` | 5 (`model`, `support`, `transport`, `tooling`, `config`) | 11 (`app`, `config`, `tooling`, `model`, `support`, `repository`, `infra`, `service`, `workflow`, `transport`, `worker`) |
| Dependency rules | 8 | 8 |
| depguard rule entries | about 6 rules, 15 entries | about 60 rules (8 layer rules, about 45 service rules, 7 workflow rules), about 230 entries |
| Files touched to add a vendor | n/a | about 10 new, 5 edited |
| Files touched to add a domain | n/a | about 18 new, 8 edited, plus every earlier workflow rule when the domain adds a workflow |
| Packages a request crosses for a stack update | 3 (`transport/http`, `api/stacks`, `gen/deploy`, answering 501) | 7 (`transport/http`, `api/stacks`, `workflow/stackflow`, `service/stacks`, `service/authz`, `repository/stacks`, `repository/outbox`) |
| Largest package | `internal/server/gen/deploy` (generated, 24k lines); hand-written: `internal/tooling/specref` (about 5k lines) | generated `transport/http/gen/deploy`; hand-written: `workflow/runflow`, about 10k lines |
| Thin packages at risk under the 90% floor | `transport/http/api/stacks` while it only routes to stubs | `model`, `model/event`, small `transport/http/api/<family>` packages (account, usage, sso), small `repository/<domain>` packages without a Linux testcontainers run |

## Placement choices and friction

Placement choices settled in this file:

- **A workflow tier above services.** Services own one domain each and
  never import each other; anything that spans domains is a workflow. The
  alternative, free service-to-service calls, would make `service/runs`,
  `service/policy`, `service/stacks` and `service/vcs` import each other
  within P13.
- **Workflows in a declared order.** `runflow` is first and imports no
  workflow, so the run state machine is reachable from stacks, VCS,
  templates, Intent and Flows without a cycle.
- **The run state machine is split into data and effects.** The table lives
  in `service/runs`; the transaction that applies it lives in
  `workflow/runflow`, the only writer of run state.
- **`service/authz` is the one shared service.** It reads the identity and
  spaces repositories directly so that importing it never drags another
  service along.
- **Events through an outbox in the repository layer.** Producers append;
  consumers in `transport/scheduler` fan out to notifications, webhooks,
  audit, checks and Flows.
- **OPA in `support`, not `infra`.** It is a pure function and the worker
  column needs it.
- **The worker is a column, not a layer.** It shares `model` and `support`
  and reaches the server only through a generated internal client.
- **Interfaces with implementations.** Repository and infra packages export
  their own interfaces; workflows declare narrow interfaces over services
  for their tests.
- **IaC vendor code lives under `iac/`, not `vendor/`.** The go tool treats
  any directory named `vendor` as vendored code: `go list ./...` skips it
  and importing it fails, so `model/iac`, `service/iac/<v>` and
  `worker/iac/<v>` hold the vendors.
- **`pkg/` keeps only `client/deploy`.** The worker-protocol client is
  internal because the protocol ships with the binary.

Where this layout rubs against the system:

- **Two tiers in the middle are not the layered pattern any more.** The
  workflow tier is a business layer that knows every domain. `runflow`
  imports runs, stacks, workers, policy, vendors and, from P16, schedules;
  it becomes the largest hand-written package, and every run feature lands
  in it.
- **Stack CRUD is already a workflow at P5.** A stack's settings touch five
  domains, so the first ordinary write path needs the second tier, and the
  P2 to P6 phases carry the full structure before most domains exist.
- **One domain is spread across six trees.** Templates live in `model`,
  `repository`, `service`, `workflow`, `transport` and `app`. Parallel lanes
  (P20 to P23, P25, P26) all edit `model`, `model/event`,
  `repository/migrations`, `transport/http/strict.go`, `app/services.go` and
  `.golangci.yml`.
- **The workflow order is maintained by hand.** Adding a workflow means
  placing it in the order and editing the deny list of every earlier
  workflow. A workflow that needs a later one (Intent wanting templates)
  forces a reorder.
- **depguard grows with the domain count.** About 45 per-service rules at
  P27, each relying on `lax` mode's longer-prefix resolution between allow
  and deny, and each written by hand when a domain is added.
- **Services share repositories.** `service/state` reads runs and
  `service/authz` reads spaces through repositories rather than the owning
  service. Rules that must agree move to `model`, which grows behaviour.
- **One migration sequence for every domain.** Flows and Deploy share it,
  so separating Flows into its own database later is a data migration as
  well as a code move.
- **Extracting a service is hard.** A domain has no single directory and
  its consumers call its service package directly, so moving it to another
  process means writing a client for every workflow that calls it.
- **Vendors and the worker column split one concept in two.** A vendor's
  server half (`service/iac/<v>`) and worker half (`worker/iac/<v>`)
  must change together, with only `model/iac` between them.
