# Option 2: Hexagonal (ports and adapters), detailed layout

Detailed repository layout for option 2 of the
[architecture options](README.md): one application core for the whole of
infraweave, surrounded by adapters, with every dependency pointing at the
core and a second, small hexagon for the worker role. Every package in
today's tree is mapped; roadmap packages are marked with the phase of
[report 26](../reference/26-scope-and-tiers.md#14-provisional-phase-spine)
that creates them.

Conventions in the trees:

- `← internal/server/problem` names where the code lives today.
- `(P7)` marks something a later phase creates.
- A line with neither is new structure for code that exists today or for
  P1, the phase in progress.

## Summary

The tree is organised by the direction of dependency. One application core,
`internal/core/`, holds the domain model and the application services of
every Deploy and Flows domain: accounts, spaces, stacks, configuration,
runs, schedules, workers, vendors, state, VCS, policies, cloud
integrations, the registry, events, templates, resources, Intelligence and
Flows. The core declares two kinds of port. Driving ports, in
`internal/core/port/driving`, are the interfaces through which the outside
world asks the core to do something: create a stack, trigger a run, report a
worker's progress, ingest a webhook, read a state file. Driven ports, in
`internal/core/port/driven`, are the interfaces through which the core
reaches the outside world: the PostgreSQL stores and the unit of work,
object storage, the work queue, the policy engine, VCS providers, cloud
credential issuers, the token signer, secrets sealing, outbound delivery,
AI providers and the clock.

Adapters implement ports and live in `internal/adapter/`. Driving adapters,
in `internal/adapter/driving/`, turn something outside into a call on a
driving port: the HTTP server (the generated `deploy` strict server, the
worker long-poll API and the hand-written protocols for VCS webhooks, the
Terraform state backend and the registry), the queue consumers and cron of
the scheduler role, and the gateways for VCS agents and Flows agents. Driven
adapters, in `internal/adapter/driven/`, implement driven ports over real
infrastructure: PostgreSQL, S3-compatible and local object storage, the
queue that decision 0011 picks, embedded OPA, GitHub and the other VCS
providers, AWS, Azure and GCP, KMS, SMTP and Slack.

The worker role is a second hexagon, `internal/worker/`, because it runs on
customer hosts with outbound HTTPS only
([report 10](../reference/10-workers-and-execution.md#42-transports)). Its
core holds the launcher loop, the phase executor and the per-vendor command
sets; its ports name the control plane, the container runtime, workspace
transfer and tool binaries; its adapters speak the worker protocol, Docker
and Kubernetes. It shares no package with the server's core or adapters, so
the worker role's construction path links no PostgreSQL adapter, no object
storage credentials and no other domain's internals.

Dependency rules:

1. `internal/core/...` imports nothing under `internal/adapter/`,
   `internal/app/`, `internal/worker/`, `internal/tooling/` or `cmd/`, and
   no infrastructure library: no `pgx`, no AWS, Azure or GCP SDK, no OPA, no
   chi, no `net/http` server code.
2. `internal/core/port/...` and `internal/core/kernel` import only the
   standard library and `internal/core/kernel`.
3. A domain package `internal/core/<domain>/...` imports another core
   domain only through `internal/core/port/driving`; it never imports
   `internal/core/<other>` directly.
4. `internal/adapter/driven/...` imports from `internal/core/` only `port`
   and `kernel`, and nothing under `internal/adapter/driving/`.
5. `internal/adapter/driving/...` imports from `internal/core/` only `port`
   and `kernel`, and nothing under `internal/adapter/driven/`.
6. `internal/worker/...` imports nothing under `internal/core/`,
   `internal/adapter/` or `internal/app/`, and no database driver;
   `internal/worker/core/...` imports nothing under
   `internal/worker/adapter/`.
7. No package under `internal/core/`, `internal/adapter/` or
   `internal/worker/` imports `internal/app/`. Only `internal/app/...` and
   `cmd/...` construct adapters and wire them to the core.
8. `internal/tooling/...` is imported only by `tools/...`; no runtime
   package imports it.
9. `pkg/...` imports nothing under `internal/` or `cmd/`.

Rule 3 is the one that makes a single core hold together at infraweave's
size: twenty domain packages in one hexagon would otherwise import each
other freely, and the hexagon would say nothing about which domain may
reach which. With rule 3, a cross-domain call (runs asking policies for a
decision, templates creating stacks) is a call on a driving port that
`internal/app` wires, and a domain can be tested with a fake of every port it
uses.

## Repository root

```text
infraweave/
  cmd/
    infraweave/             main.go only: builds the cobra root from internal/app/cli
                            and exits; roles serve, scheduler, worker, all (hidden: worker exec),
                            vcs-agent, flows-agent; migrate up, migrate status, version
    infraweavectl/          (P24) the CLI, a client over pkg/client/deploy; no server code
  internal/
    app/                    NEW: composition root, one file per role, config, cobra tree
    buildinfo/              ← internal/buildinfo (unchanged)
    core/                   NEW: the hexagon (kernel, ports, one package per domain)
    adapter/                NEW: driving/ and driven/ adapters
    worker/                 NEW (P8): the worker role's own hexagon (core, ports, adapters)
    contract/               NEW (P2): contract suites for driven ports
    tooling/                NEW: specref, openapibundle, stubgen, docscheck (dev tooling, not runtime)
  pkg/
    client/deploy/          ← pkg/client/deploy (unchanged; generated)
  api/                      unchanged layout; oapi-server-*.yaml outputs move to
                            internal/adapter/driving/http/gen/<spec>; adds src/worker (P8)
  test/
    acceptance/             ← test/acceptance; features per domain, testcontainers (decision 0016)
  tools/                    unchanged; the three spec tools import internal/tooling/*
  docs/                     unchanged
  deploy/                   (P27) Helm chart, Terraform modules for AWS, Azure and GCP, images
  Makefile                  generate writes servers to internal/adapter/driving/http/gen/<spec>;
                            stubgen path follows; build unchanged (cmd/* cross-compiled, CGO off)
  .golangci.yml             depguard enabled with the nine rules above (one entry per core domain)
  .testcoverage.yml         exclusions: ^cmd/, ^tools/, ^internal/adapter/driving/http/gen/,
                            ^pkg/client/, ^internal/worker/adapter/controlplane/client/
  go.mod                    one module, unchanged path
```

What changes at the root and why:

- `internal/` gains four top-level areas (`core`, `adapter`, `worker`,
  `contract`) and loses the `server` directory. Everything a reader needs to
  know about a package's permissions follows from which of the four it is
  under.
- `cmd/infraweave` appears with P1 and stays a few lines long. The command
  tree lives in `internal/app/cli`, because building a role means building
  adapters, and only the composition root may do that (rule 7).
- `pkg/` keeps only the generated `deploy` client. Store, object storage and
  queue interfaces are driven ports owned by the core, not public packages,
  because the hexagon's contract is internal and publishing it would freeze
  it.
- `api/` keeps its role as the source of truth for specifications. Only the
  generated server output path moves, from `internal/server/gen` to the
  driving HTTP adapter.

## Full package tree

Status of each area at P1 and the phase that completes it:

| Area | Status at P1 | Created or completed by |
| --- | --- | --- |
| `app`, `buildinfo`, `tooling` | full | P1 |
| `core/kernel`, `core/port` | skeleton: IDs, principal, page cursor; stack ports | P1 to P5, growing with every domain |
| `core/stack` | stub: the 72 served operations answer 501 | P5 |
| `adapter/driving/http` | router, problem details, generated `deploy` server, stubs | P1, then every phase that serves an API |
| `adapter/driven/postgres`, `objectstore` | absent | P2 |
| `core/account`, `core/space` | absent | P3, P4, P23 |
| `core/config` | absent | P6 |
| `core/run`, `adapter/driving/scheduler`, `adapter/driven/queue` | absent | P7, P9, P10 |
| `core/worker`, `adapter/driving/http/workerapi`, `internal/worker` | absent | P8, P14 |
| `core/iac` | absent | P9 (Terraform, OpenTofu), P20 (the rest) |
| `core/state`, `core/vcs`, `core/policy`, `core/cloud`, `core/schedule` | absent | P11, P12 and P19, P13, P15, P16 |
| `core/registry`, `core/event` | absent | P17, P18 |
| `core/template`, `core/resource`, `core/intel`, `core/flows` | absent | P21, P22, P25, P26 |

```text
internal/
  app/                                     composition root; the only package that sees everything
    cli/                                   cobra tree: serve, scheduler, worker, all, vcs-agent, flows-agent,
                                           migrate up, migrate status, version; hidden worker exec; binds
                                           setting flags only (not --config)
    config/                                strict YAML decoded with viper UnmarshalExact; flag > env > file;
                                           one Settings struct per role and per driven adapter
    (files listed under "Composition root")
  buildinfo/                               ← internal/buildinfo
  tooling/                                 libraries behind tools/; never linked into a role
    specref/                               ← internal/specref
    openapibundle/                         ← internal/openapibundle
    stubgen/                               ← internal/stubgen
    docscheck/                             ← internal/docscheck

  core/
    kernel/                                value types every domain shares; no behaviour beyond parsing
      ids.go                               ULID, Slug, UUID (Flows), parse and format
      principal.go                         Principal: user, API key, run, worker, stack; account; spaces
      space.go                             SpaceID, SpacePath; the root space constant
      labels.go                            Labels, autoattach:<label> parsing
      page.go                              Page, Cursor (decision 0003 cursor encoding)
      problem.go                           domain error kinds the HTTP adapter maps to problem types
    port/
      driving/                             what adapters may ask the core to do
        stacks.go                          Stacks: Create, Get, List, Update, Delete, Lock, Unlock,
                                           Enable, Disable, SetHooks, Reslug
        config.go                          (P6) Contexts, ConfigElements, Attachments
        runs.go                            (P7) Runs: Trigger, TriggerBatch, CreateTask, Confirm,
                                           Discard, Cancel, Stop, Kill, Retry, Promote, Review,
                                           TargetedReplan, SetPriority, Comment
        progress.go                        (P8) RunProgress: Claim, Report, Heartbeat, Finish
        scheduling.go                      (P7) Scheduler: Schedule(stack), Expire (timers)
        workers.go                         (P8) WorkerPools, Workers: register, drain, cycle, reset
        vendors.go                         (P9) Vendors: Validate, PlanInput, Changes, Versions
        state.go                           (P11) StateBackend: Get, Put, Lock, Unlock; Versions, Rollback
        vcs.go                             (P12) Webhooks: Ingest, Process; Integrations; Feedback
        policies.go                        (P13) Policies CRUD; Decisions: Push, Plan, Approval,
                                           Trigger, Notification, Login, Intent
        cloud.go                           (P15) CloudIntegrations, Credentials
        schedules.go                       (P16) ScheduledJobs, Drift, Dependencies
        registry.go                        (P17) Modules, ModuleVersions, Providers, GPGKeys, Protocol
        events.go                          (P18) Subscriber: Handle(ctx, Event); Notifications; Webhooks;
                                           Audit
        templates.go                       (P21) Templates, Blueprints, Deployments
        resources.go                       (P22) Resources, Search
        identity.go                        (P3) Accounts, Users, APIKeys, Sessions, Authenticator
        access.go                          (P4) Spaces, Roles, Bindings, Authorizer (Decider per principal)
        sso.go                             (P23) SSO, SCIM
        intel.go                           (P25) AIIntegrations, Assistant, Intent
        flows.go                           (P26) Organizations, Projects, Flows, Blocks, Invocations, Apps
        types.go                           request and result DTOs shared by the files above
      driven/                              what the core needs from outside
        tx.go                              (P2) UnitOfWork: Tx(ctx, fn), AfterCommit(ctx, fn)
        stores.go                          (P2) one interface per aggregate: StackStore, RunStore,
                                           ContextStore, PolicyStore, WorkerStore, ... (one file
                                           per domain from P5: stackstore.go, runstore.go, ...)
        objects.go                         (P2) ObjectStore: Put, Get, Head, Delete, SignURL; logical
                                           stores as key prefixes, objects written once (decision 0005)
        queue.go                           (P7) Queue: Enqueue(ctx, Job) joins the caller's Tx;
                                           Consume is driving side (implementation: decision 0011)
        outbox.go                          (P7) Outbox: Append(ctx, Event) inside the Tx: the events row
                                           and its events.fanout job (decision 0015)
        audit.go                           (P18) AuditLog: Record(ctx, Entry) inside the Tx (decision 0015)
        clock.go                           Clock, IDs (ULID source)
        sealer.go                          (P6) Sealer: Seal, Open; envelope keys (decision 0013)
        signer.go                          (P3) TokenSigner: sign and verify JWTs, JWKS (decision 0006)
        policyengine.go                    (P13) PolicyEngine: Compile, Evaluate (decision 0009)
        vcsprovider.go                     (P12) VCSProvider: VerifyWebhook, Parse, Files, Archive,
                                           SetCheck, Comment, Deployment; one per provider kind
        cloudissuer.go                     (P15) CloudIssuer: AWS assume role, Azure, GCP, Vault
        delivery.go                        (P18) Delivery: signed HTTP POST with retry record
        chat.go                            (P18) Chat: Slack messages and interactions
        mail.go                            (P3) Mail: invitations
        idp.go                             (P23) IdentityProvider: SAML, OIDC
        ai.go                              (P25) AIProvider: chat, tools (decision 0019)
        flowsruntime.go                    (P26) Runtime: evaluate expressions, run app handlers on agents
    stack/                                 (P5) model, validation, settings, lock, enable, reslug
    config/                                (P6) contexts, config elements, attachments, auto-attach,
                                           runtime config merge, priority order
    run/                                   (P7, P9, P10) run model, state machine, actions, gates,
                                           priority, timers, workflow grouping, PR feedback events
    schedule/                              (P16) scheduled runs, tasks and deletes, drift detection,
                                           stack dependencies and output references
    worker/                                (P8, P14) pools, workers, assignment, drain, cycle, metadata
    iac/                                   (P9) Vendor interface and registry, version catalogue
      terraform/ opentofu/                 (P9) settings, plan policy input, changes model
      terragrunt/ pulumi/ cloudformation/ kubernetes/ ansible/   (P20)
    state/                                 (P11) state versions, locks, run-scoped access, rollback, import
    vcs/                                   (P12) integrations, event normalisation, routing to stacks
                                           and modules, deduplication, checks and comments
    policy/                                (P13) policies, attachment, input builders per type,
                                           combination per type, flags, receipts, sampling
    cloud/                                 (P15) AWS, Azure, GCP integrations, OIDC claims, attachments
    registry/                              (P17) modules, versions, test cases, providers, GPG keys, sharing
    event/                                 (P18) event catalogue, inbox, named and stack webhooks,
                                           audit trail, notification routing
    template/                              (P21) templates, versions, deployments, blueprints
    resource/                              (P22) managed entities, scans, search
    account/                               (P3) accounts, users, API keys, sessions, invitations;
                                           (P23) SSO settings, SCIM reconciliation
    space/                                 (P4) space tree, inheritance, actions, roles, bindings, Decider
    intel/                                 (P25) AI integrations, Infra Assistant, Intent projects
    flows/                                 (P26) organizations, projects, flows, blocks, apps, agent pools
      engine/                              (P26) events, lineage, invocations, limits, retries

  adapter/
    driving/
      http/                                chi router, middleware, problem mapping
        problem/                           ← internal/server/problem
        middleware/                        request ID, recovery, authentication (calls driving.Authenticator),
                                           idempotency and rate limits (decision 0003), body limits
        gen/
          deploy/                          ← internal/server/gen/deploy (server.gen.go, unimplemented.gen.go)
          worker/                          (P8) worker protocol strict server
        deploy/                            strict server composition for the deploy spec
          server.go                        embeds every family's partial implementation, stubs one level deeper
          stacks/                          implements the Stacks operations over driving.Stacks
          runs/                            (P7) runs, tasks, reviews, logs
          config/ spaces/ identity/ workers/ policies/ vcs/ cloud/ schedules/ state/ registry/
          events/ templates/ resources/ intel/            (P3 to P25) one package per family
        workerapi/                         (P8) implements gen/worker over driving.RunProgress and
                                           driving.Workers: register, long-poll, signals, presigned URLs
        webhook/                           (P12) POST /webhooks/vcs/{provider}/{integration}: store, then
                                           acknowledge (decision 0012)
        statebackend/                      (P11) Terraform http backend: GET, POST, LOCK, UNLOCK
        registryproto/                     (P17) /.well-known/terraform.json, modules.v1, providers.v1,
                                           login.v1 and the token endpoint
        flowsapi/                          (P26) Flows API, flow HTTP endpoints, MCP endpoints
        mcp/                               (P25) platform MCP server
      scheduler/                           (P7) the scheduler role's loops
        consume.go                         one handler per job kind; calls the matching driving port
        cron.go                            cron and timers: drift, schedules, run expiry, retention
        ticker.go                          decision 0011's scheduler ticker: due rows claimed with
                                           SKIP LOCKED on every replica, no leader
      outbox/                              (P7) the events.fanout job: hands each event to the
                                           driving.Subscriber implementations
      gateway/                             (P19) VCS agent long-poll (decision 0012); (P26) Flows agent WebSockets
    driven/
      postgres/                            (P2) pool, UnitOfWork, migrator, readiness
        migrations/                        one migration stream for the whole schema, embedded,
                                           NNNNN_*.sql (decision 0005)
        stacks/ runs/ config/ workers/ state/ vcs/ policies/ cloud/ schedules/ registry/ events/
        templates/ resources/ accounts/ spaces/ intel/ flows/
                                           one package per store port family (P2 to P26)
        outbox/                            (P7) the events table of decision 0015, fan-out marks, retention
      objectstore/                         (P2)
        s3/                                S3-compatible, presigned SigV4
        fs/                                local disk for single-node installs and tests
        gcs/ azureblob/                    (P27)
      queue/                               (P7) the decision 0011 implementation behind driven.Queue
      opa/                                 (P13) embedded OPA v1: prepared queries, capabilities, timeouts
      vcs/
        github/                            (P12) App and Enterprise, HMAC verification, checks, comments
        gitlab/ bitbucketcloud/ bitbucketdc/ azuredevops/ rawgit/   (P19)
        agentpool/                         (P19) any provider reached through the VCS agent gateway
      cloud/
        aws/ azure/ gcp/ vault/            (P15) credential issuance
      keys/                                (P3) JWT signing keys, JWKS; (P6) local master key
      kms/                                 (P6) AWS KMS and other roots for the Sealer (decision 0013)
      delivery/                            (P18) HMAC-signed HTTP delivery, request and response capture
      slack/                               (P18)
      smtp/                                (P3)
      idp/
        saml/ oidc/                        (P23)
      ai/
        anthropic/ openai/ bedrock/        (P25) per decision 0019
      flowsruntime/                        (P26) routes evaluations to agents through the gateway
      clock/                               system clock and ULID source

  worker/                                  (P8) the worker role's hexagon; links nothing above
    core/
      launcher/                            register, poll, accept job, start container, relay signals
      executor/                            phases and hooks inside the run container (worker exec)
      vcsagent/                            (P19) vcs-agent: long-polls the gateway, checks each request
                                           against its provider allowlist, forwards it (decision 0012)
      flowsagent/                          (P26) flows-agent: WebSocket to the gateway, runtime pool
                                           (decision 0018)
      iac/                                 per-vendor command sets: init, plan, show, apply, destroy
        terraform/ opentofu/               (P9)
        terragrunt/ pulumi/ cloudformation/ kubernetes/ ansible/   (P20)
      initpolicy/                          (P14) worker-side initialization policy decision
      port/
        controlplane.go                    ControlPlane: Register, Poll, Spec, Report, UploadURLs,
                                           Deregister
        containers.go                      Containers: Start, Wait, Signal, Remove
        workspace.go                       Workspace: Download, Upload (presigned URLs only)
        binaries.go                        Binaries: Resolve(vendor, constraint), Verify
        rego.go                            Rego: Evaluate(policy, input) for initpolicy
    adapter/
      controlplane/                        long-poll client over the generated worker client
        client/                            (P8) generated from api/worker.openapi.yaml
      docker/                              (P8) Docker socket
      kubernetes/                          (P14) pods per run; the controller for WorkerPool resources
      workspace/                           presigned HTTP transfer
      binaries/                            download, checksum and signature checks
      rego/                                (P14) OPA evaluation with exec allowed, worker side only
      vcsproxy/                            (P19) HTTP to the private VCS for vcsagent
      runtimes/                            (P26) Node.js runtimes in Docker or Kubernetes for flowsagent

  contract/                                (P2) suites every implementation of a driven port runs
    storetest/                             one file per store port; run against postgres
    objecttest/                            run against s3 (MinIO container) and fs
    queuetest/                             (P7) claims, leases and fencing, retries and the dead state,
                                           ordering keys, priority (decision 0011)
    policytest/                            (P13) the example library corpus against opa
    vcstest/                               (P12) recorded provider payloads per adapter
    controlplanetest/                      (P8) worker protocol against workerapi and controlplane

pkg/
  client/
    deploy/                                ← pkg/client/deploy (generated)
```

### Where each domain's code sits

| Domain | Core package | Driving adapter | Driven adapters | Worker side |
| --- | --- | --- | --- | --- |
| Stacks | `core/stack` | `http/deploy/stacks` | `postgres/stacks` | none |
| Contexts and configuration | `core/config` | `http/deploy/config` | `postgres/config`, `kms` | mounted files, env |
| Runs | `core/run` | `http/deploy/runs`, `workerapi`, `scheduler` | `postgres/runs`, `queue`, `objectstore` | `executor` |
| Workers | `core/worker` | `http/deploy/workers`, `workerapi` | `postgres/workers` | `launcher`, `docker`, `kubernetes` |
| Vendors | `core/iac/<name>` | none | none | `core/iac/<name>` |
| State | `core/state` | `statebackend`, `http/deploy/state` | `postgres/state`, `objectstore` | backend config written by `executor` |
| VCS | `core/vcs` | `webhook`, `http/deploy/vcs`, `gateway` | `vcs/<provider>`, `postgres/vcs` | source fetch with the run's credential; `core/vcsagent` |
| Policies | `core/policy` | `http/deploy/policies` | `opa`, `postgres/policies`, `objectstore` | `initpolicy`, `rego` |
| Cloud integrations | `core/cloud` | `http/deploy/cloud` | `cloud/<provider>`, `keys` | credentials in the job |
| Schedules, drift, dependencies | `core/schedule` | `http/deploy/schedules`, `scheduler` | `postgres/schedules` | none |
| Registry | `core/registry` | `registryproto`, `http/deploy/registry` | `postgres/registry`, `objectstore` | `.terraformrc` written by `executor` |
| Events | `core/event` | `http/deploy/events`, `outbox` | `delivery`, `slack`, `postgres/events` | none |
| Accounts, spaces, SSO | `core/account`, `core/space` | `http/middleware`, `http/deploy/identity`, `http/deploy/spaces` | `postgres/accounts`, `postgres/spaces`, `keys`, `idp/*` | none |
| Templates | `core/template` | `http/deploy/templates` | `postgres/templates` | none |
| Resources and search | `core/resource` | `http/deploy/resources` | `postgres/resources` | none |
| Intelligence | `core/intel` | `http/deploy/intel`, `mcp` | `ai/<provider>`, `postgres/intel` | none |
| Flows | `core/flows`, `core/flows/engine` | `flowsapi`, `gateway` | `postgres/flows`, `flowsruntime` | `core/flowsagent`, the `flows-agent` subcommand (decision 0018) |

## Deep dives

### Stack configuration write path

```text
internal/core/port/driving/stacks.go          Stacks interface, implemented by stack.Service
internal/core/port/driving/types.go           StackInput, StackPatch, VendorConfig (opaque per vendor)
internal/core/port/driven/stackstore.go       StackStore: Get, GetBySlug, Insert, Update, Delete, List
internal/core/stack/
  stack.go                                    Stack aggregate: settings, labels, flags, lock, state
  validate.go                                 project root, branch, runner image, labels, name to slug
  service.go                                  Service implements driving.Stacks
  lock.go                                     human lock, lock note, autodeploy off while locked
  enable.go                                   disable and enable; disabled stacks create no runs
  reslug.go                                   stackReslug: slug regenerated, ULID unchanged
  audit.go                                    audit entries for create, update and delete (decision 0015)
  *_test.go                                   fakes of StackStore, UnitOfWork, AuditLog, Vendors, Authorizer
internal/adapter/driving/http/deploy/stacks/
  stacks.go                                   StacksAPI: StackCreate, StackUpdate, Stack, Stacks, ...
  convert.go                                  generated request types to driving DTOs and back
  *_test.go                                   handler tests against fake driving ports
internal/adapter/driven/postgres/stacks/
  stacks.go                                   StackStore over pgx; unique (account, slug)
  labels.go                                   label array with GIN index (report 28 section 12.1)
internal/adapter/driven/postgres/migrations/00003_stacks.sql
internal/contract/storetest/stacks.go         slug uniqueness, reslug, soft delete, paging order
```

A stack write crosses the hexagon's edge twice: in from the strict server,
out to the store. The handler holds `driving.Stacks`, never a service struct,
so it can be tested with a fake and cannot reach past the port. Vendor
settings are opaque to `core/stack`: it asks `driving.Vendors.Validate` for
the vendor block, because the vendor rules live in `core/iac/<name>`
and rule 3 forbids importing them. The space and permission check is a call
on `driving.Authorizer`, which `core/space` implements
([report 14](../reference/14-spaces-rbac-and-identity.md#3-roles-and-actions)).

```go
// internal/core/stack/service.go (shape)
package stack

var _ driving.Stacks = (*Service)(nil)

type Service struct {
	Tx      driven.UnitOfWork
	Stacks  driven.StackStore
	Audit   driven.AuditLog  // (P18); a no-op before the audit trail exists
	Vendors driving.Vendors  // core/iac through its driving port (rule 3)
	Authz   driving.Authorizer
	IDs     driven.IDs
}

func (s *Service) Update(ctx context.Context, id kernel.Slug, p driving.StackPatch) (driving.Stack, error) {
	var out Stack
	err := s.Tx.Tx(ctx, func(ctx context.Context) error {
		st, err := s.Stacks.GetBySlug(ctx, id)
		if err != nil {
			return err
		}
		if err := s.Authz.Require(ctx, kernel.ActionStackUpdate, st.Space); err != nil {
			return err
		}
		if p.Vendor != nil {
			if err := s.Vendors.Validate(ctx, st.Vendor, *p.Vendor); err != nil {
				return err
			}
		}
		if out, err = st.Apply(p); err != nil { // domain rules: lock, disabled, immutable fields
			return err
		}
		if err := s.Stacks.Update(ctx, out); err != nil {
			return err
		}
		return s.Audit.Record(ctx, auditEntry("stack.update", st, out)) // commits with the change
	})
	return out.View(), err
}
```

Contexts and configuration follow the same path through `core/config`, with
one difference: secret values are sealed by `driven.Sealer` before they reach
`postgres/config`, and the store never sees plaintext (decision 0013,
[report 13](../reference/13-configuration.md#1-the-configuration-element)).
Auto-attachment by `autoattach:<label>` is a query `core/config` issues
through `driven.ContextStore`, scoped by the space inheritance that
`driving.Authorizer` reports.

### Run execution path

```text
internal/core/port/driving/runs.go            Runs: Trigger, CreateTask, Confirm, Discard, Cancel, ...
internal/core/port/driving/progress.go        RunProgress: Claim, Report, Heartbeat, Finish (worker side)
internal/core/port/driving/scheduling.go      Scheduler: Schedule(stack), Expire(now)
internal/core/port/driven/runstore.go         RunStore: Insert, Get, Transition, ClaimNext(pool, worker),
                                              BlockingRun(stack), Queue(pool), Transitions(run)
internal/core/port/driven/queue.go            Queue: Enqueue(ctx, Job) inside the Tx
internal/core/run/
  run.go                                      Run aggregate: type, state, state version, commit, priority
  machine.go                                  report 08's 43 transitions and decision 0008's T44 as a table;
                                              Can(from, to, type)
  machine_test.go                             every allowed and every forbidden pair
  trigger.go                                  T1: new run in QUEUED, workflow ID, dedupe key (stack, sha, type)
  gates.go                                    T2: stack lock, approval, upstream runs, external dependencies
  actions.go                                  confirm, discard, cancel, stop, kill, retry, promote, replan
  priority.go                                 three presets, seven tiers (report 08 priority)
  timers.go                                   2, 7 and 30 day rules; expired flag
  progress.go                                 implements driving.RunProgress for the worker API
  feedback.go                                 RunStateChanged events that VCS and notifications consume
  service.go                                  Service implements driving.Runs and driving.Scheduler
internal/adapter/driven/postgres/runs/
  runs.go transitions.go                      runs and run_state_transitions tables
  claim.go                                    ClaimNext: SELECT ... FOR UPDATE SKIP LOCKED ordered by
                                              tier, created_at (report 28 section 12.3 pool index)
internal/adapter/driving/scheduler/consume.go  job kinds run.schedule, run.expire, run.dependants
internal/adapter/driving/http/workerapi/       poll and report endpoints over driving.RunProgress
```

The run is the record and the queue is a delivery mechanism
([report 27](../reference/27-reference-materials.md#102-river)): a run waits
in `QUEUED`, `UNCONFIRMED` or `PENDING_REVIEW` for days, which no queue
job should do. The core therefore owns the state machine and the
assignment order; the queue only wakes the scheduler. Every transition
happens in one unit of work that writes the run row, appends a
`run_state_transitions` row, enqueues follow-up work and appends the
outbox event, so a crash never leaves a run in a state no consumer has heard
of:

```go
// internal/core/run/service.go (shape)
func (s *Service) transition(ctx context.Context, id kernel.ULID, to State, why Cause) error {
	return s.Tx.Tx(ctx, func(ctx context.Context) error {
		r, err := s.Runs.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if !Machine.Can(r.State, to, r.Type) { // report 08 section 3, table-driven
			return kernel.Conflict("run %s cannot go from %s to %s", r.ID, r.State, to)
		}
		r = r.Enter(to, why, s.Clock.Now()) // state version increments when a state recurs
		if err := s.Runs.Transition(ctx, r); err != nil {
			return err
		}
		if to.Terminal() && r.Type.Blocking() {
			if err := s.Queue.Enqueue(ctx, driven.Job{Kind: "run.schedule", Key: r.Stack.String()}); err != nil {
				return err // the next blocked run on the stack may now be READY
			}
		}
		return s.Outbox.Append(ctx, RunStateChanged(r))
	})
}
```

Scheduling has two levels, the shape OTF uses
([report 27](../reference/27-reference-materials.md#31-architecture)): per
stack, the gates in `gates.go` decide `QUEUED` to `READY`; per pool,
`ClaimNext` hands the oldest run of the highest tier to the worker that
polls ([report 08](../reference/08-run-lifecycle.md#4-queueing-the-stack-lock-and-scheduling),
[report 10](../reference/10-workers-and-execution.md#53-scheduling-inside-a-pool)).
Claiming is the transition `READY` to `PREPARING` (T7), executed by the
`serve` role inside the long-poll request, not by the scheduler: the worker
is waiting on that request, and a claim made elsewhere would need a second
hop to reach it. The `scheduler` role runs everything with no client
waiting: gate re-evaluation after a terminal run, the timers of
[report 08](../reference/08-run-lifecycle.md#timers), stale-run sweeps and
dependants (P16).

### Policy evaluation with embedded OPA

```text
internal/core/port/driving/policies.go        Policies CRUD; Decisions: Push, Plan, Approval, Trigger,
                                              Notification, Login, Intent; Simulate
internal/core/port/driven/policyengine.go     PolicyEngine: Compile(body, engineType) (Compiled, error);
                                              Evaluate(ctx, Compiled, input, now) (Result, error)
internal/core/port/driven/policystore.go      PolicyStore, AttachmentStore, ReceiptStore, SampleStore
internal/core/policy/
  policy.go                                   Policy: type, engine type (REGO_V0, REGO_V1), body, labels
  types.go                                    the 13 type names, three aliases normalised
  attach.go                                   explicit attachments, autoattach labels, space inheritance
  input/                                      input documents per type (report 09 section 4)
    shared.go run.go push.go plan.go approval.go trigger.go notification.go login.go intent.go
  combine.go                                  per-type combination: deny wins, all approvals, priorities
  flags.go                                    flags, reset ordering, receipt flags
  sample.go                                   hourly quota, sensitive values removed, 7-day retention
  decide.go                                   Decisions implementation: select, build input, evaluate,
                                              combine, store receipts
internal/adapter/driven/opa/
  engine.go                                   implements driven.PolicyEngine with v1/rego
  capabilities.go                             six built-ins removed (decision 0009); exec only on the worker side
  cache.go                                    PreparedEvalQuery per (body hash, engine type)
  preamble.go                                 per-type result object and defaults
  timeout.go                                  per-type and per-event deadlines on the context
internal/contract/policytest/                 the 67 example-library templates and 61 tests
```

The policy engine is a driven port, so OPA appears in exactly one adapter,
`adapter/driven/opa`, and the core never imports
`github.com/open-policy-agent/opa`
([report 27](../reference/27-reference-materials.md#91-the-go-api)). The
domain rules that make Spacelift's policies what they are (which policies
apply, what the input document holds, how several results combine) are core
code in `core/policy`, testable with a fake engine that returns canned
results. Evaluation points sit in other domains
([report 09](../reference/09-policies.md#8-where-each-type-is-evaluated)):
`core/vcs` asks for push decisions, `core/run` for plan and approval
decisions, `core/event` for notification decisions, `core/account` for
login decisions. Each holds `driving.PolicyDecisions`; none imports
`core/policy` (rule 3).

```go
// internal/core/port/driving/policies.go (excerpt)
type PolicyDecisions interface {
	// Plan evaluates every plan policy attached to the run's stack, directly
	// or by label, with the plan input, combines the results and stores one
	// receipt per policy. Warnings and denials come back for the caller to
	// apply to the run, because only core/run may move a run's state.
	Plan(ctx context.Context, run RunRef, plan PlanDocument) (PlanDecision, error)
	Approval(ctx context.Context, run RunRef, reviews []Review) (ApprovalDecision, error)
	Push(ctx context.Context, target Target, event PushEvent) (PushDecision, error)
	Notification(ctx context.Context, ev Event) ([]Route, error)
}

// internal/core/port/driven/policyengine.go
type PolicyEngine interface {
	Compile(ctx context.Context, p CompileRequest) (Compiled, error) // rejects disabled built-ins
	Evaluate(ctx context.Context, c Compiled, input any, now time.Time) (Result, error)
}
```

Receipts and samples are written in the caller's unit of work, so a plan
decision and the run's move to `UNCONFIRMED` commit together; the sampled
input body goes to the policy inputs bucket through `driven.ObjectStore`
([report 24](../reference/24-self-hosted-reference.md#32-object-storage-buckets)).

### Worker protocol and pools

```text
api/src/worker/                               (P8) info.yaml, paths-workers.yaml: register, poll,
                                              deregister, spec, reports, upload URLs, log chunks,
                                              artifacts, workspace (decision 0008)
internal/adapter/driving/http/gen/worker/     (P8) generated strict server
internal/adapter/driving/http/workerapi/
  server.go                                   implements gen/worker over driving.RunProgress, driving.Workers
  poll.go                                     heartbeat and long-poll: hold up to the worker's timeout,
                                              claim on wake
  signals.go                                  stop and kill delivered on the next poll or a held one
  auth.go                                     pool credentials and job-scoped tokens (decision 0006)
internal/core/worker/
  pool.go                                     WorkerPool: credentials, drift limit, preparing timeout
  worker.go                                   Worker: status, busy, drained, metadata, availableAt
  registry.go                                 register, heartbeat decay, drain, undrain, cycle, reset
  service.go                                  implements driving.Workers
internal/worker/                              the worker role (see the next tree)
```

```text
internal/worker/
  core/
    launcher/
      launcher.go                             Run(ctx): register, then loop Poll → Accept → Start
      job.go                                  Job: run ID, phase plan, image, env, mounts, URLs, token
      signals.go                              stop and kill relayed to the container
    executor/
      exec.go                                 entry for `infraweave worker exec` inside the container
      phases.go                               INITIALIZING, PLANNING, APPLYING, PERFORMING, DESTROYING
      hooks.go                                before and after hooks in one shell session per phase
      logs.go                                 masked log streaming in chunks through presigned URLs
    iac/
      iac.go                                  Commands interface: Init, Plan, ShowPlan, Apply, Destroy, Outputs
      terraform/ opentofu/                    (P9)
    port/                                     ControlPlane, Containers, Workspace, Binaries, Rego
  adapter/
    controlplane/                             implements port.ControlPlane over the generated client
    docker/                                   implements port.Containers over the Docker socket
    kubernetes/                               (P14) pod per run, WorkerPool controller
    workspace/ binaries/ rego/
```

The worker role is a hexagon of its own with nothing in common with the
server's core except the wire format. Its core decides what to do with a
job; its ports name the four things it needs (the control plane, a
container runtime, workspace transfer and tool binaries); its adapters are
the only code that touches HTTP or Docker. Two consequences follow. First,
`internal/app/worker.go` builds the role from `internal/worker/...` alone, so
a role that never opens a database cannot reach a PostgreSQL adapter by
accident (rule 6 and the `app/worker.go` depguard entry). Second, the job
payload is defined twice: by the `worker` spec for the wire, and by
`internal/worker/core/launcher/job.go` for the worker's own logic, with the
conversion in `adapter/controlplane`. The duplication is the price of keeping
generated types out of a core.

Pools grow by adapter: Docker workers at P8, Kubernetes at P14
([report 10](../reference/10-workers-and-execution.md#7-docker-based-and-kubernetes-workers)).
A worker's binary is the same static `infraweave` executable; the launcher
mounts it read-only into the runner image and starts `infraweave worker
exec` as the container's root process, which is how Spacelift's launcher
and worker binary split maps onto one binary
([report 10](../reference/10-workers-and-execution.md#4-the-launcher-and-the-worker-protocol)).

### VCS webhook ingest

```text
internal/adapter/driving/http/webhook/
  webhook.go                                  POST /webhooks/vcs/{provider}/{integration}; reads the body
                                              with a cap,
                                              calls driving.Webhooks.Ingest, answers 202
internal/core/port/driving/vcs.go             Webhooks: Ingest(ctx, integration, headers, body) error;
                                              Process(ctx, deliveryID) error
internal/core/port/driven/vcsprovider.go      VCSProvider per kind: Verify, Parse, ListFiles, Archive,
                                              SetCheck, Comment, Deployment
internal/core/vcs/
  integration.go                              VCSIntegration: provider, space, default, check mode, secrets
  ingest.go                                   verify through the provider, store the delivery, enqueue
                                              vcs.process_delivery on vcs-inbox in one Tx, return
  normalize.go                                provider payload to one PushEvent or PullRequestEvent
  route.go                                    match stacks and modules by integration, repository, branch;
                                              project root and globs
  dedupe.go                                   (stack, sha, run type); /spacelift comments bypass
  process.go                                  per target: driving.PolicyDecisions.Push, then
                                              driving.Runs.Trigger or an ignored-trigger record
  feedback.go                                 Subscriber for RunStateChanged: checks, PR comments,
                                              deployments (report 11 section 12)
internal/adapter/driven/vcs/github/
  verify.go                                   X-Hub-Signature-256 HMAC
  parse.go                                    push, pull_request, issue_comment, check_run payloads
  app.go                                      installation tokens from the App key
  checks.go comments.go deployments.go
internal/adapter/driven/vcs/{gitlab,bitbucketcloud,bitbucketdc,azuredevops,rawgit}/   (P19)
internal/adapter/driven/postgres/vcs/          integrations, deliveries, pull_requests
internal/contract/vcstest/                    recorded deliveries per provider: verify, parse, normalise
```

Intake is durable before acknowledgement
([report 27](../reference/27-reference-materials.md#12-cross-tool-comparison)):
the HTTP adapter does no work beyond handing the raw body to the core, and
the core's `Ingest` verifies, stores the delivery and enqueues processing in
one transaction. The `scheduler` role then runs `Process`, which turns the
event into targets
([report 11](../reference/11-vcs-integrations.md#112-what-an-event-becomes))
and drives the push policy and run creation through driving ports. The
provider split is the textbook hexagonal case: everything provider-specific
(signatures, payload shapes, tokens, check and comment APIs) is a driven
adapter behind `driven.VCSProvider`, and `core/vcs` sees one normalised
event. Adding Bitbucket Data Center is one adapter package, one contract
fixture set and one registration line in `internal/app`.

### State backend

```text
internal/adapter/driving/http/statebackend/
  backend.go                                  GET, POST /state/v1/stacks/{stack}; LOCK and UNLOCK methods
                                              (Terraform http backend, decision 0010); hand-written chi routes
  auth.go                                     run-scoped credentials from the Authorization header
internal/core/port/driving/state.go           StateBackend: Read, Write, Lock, Unlock; StateVersions:
                                              List, Rollback, Import, Presign
internal/core/state/
  access.go                                   credentials must belong to an active run whose state allows
                                              reading or writing (report 16 section 1.1)
  lock.go                                     lock ID checks; plan locking unless disabled
  version.go                                  ManagedStateVersion rows, one written-once object each
  rollback.go                                 rollback as a new version; import markers
  external.go                                 (P11) external state access switch and permissions
internal/core/port/driven/objects.go          ObjectStore: Put, Get, Head, Delete, SignURL
internal/adapter/driven/postgres/state/       managed_state_versions, state_locks
internal/adapter/driven/objectstore/s3/       the states store: one sealed object per version
```

The state backend shows the hexagon's strength for protocols that are not
infraweave's own REST API: the Terraform `http` backend is one driving
adapter, and nothing in `core/state` knows HTTP methods such as `LOCK`. The
liveness rule ("only an active run in a phase that reads or writes state",
[report 16](../reference/16-state-management.md#11-how-a-run-reaches-it))
needs the run's current state, which `core/state` reads through
`driving.Runs.Get`, not through `core/run`'s store. Spacelift builds state
history on bucket versioning
([report 16](../reference/16-state-management.md#6-state-history-and-rollback));
decision 0010 writes each version as its own object, so the object storage
port needs no version calls and the `fs` adapter needs no emulation.

### Module and provider registry protocol

```text
internal/adapter/driving/http/registryproto/
  discovery.go                                /.well-known/terraform.json: login.v1, modules.v1,
                                              providers.v1, state.v2 (report 15 section 9.2)
  modules.go                                  {namespace}/{name}/{provider}/versions and /download
                                              (204 with X-Terraform-Get, a short-lived presigned URL)
  providers.go                                {namespace}/{type}/versions, download/{os}/{arch}
  login.go                                    login.v1 authorization and token endpoints
  errors.go                                   the protocol's own error shapes, not problem details
internal/core/port/driving/registry.go        Modules, ModuleVersions, Providers, ProviderVersions,
                                              GPGKeys; Protocol: Versions, Download
internal/core/registry/
  module.go version.go                        version numbering rules, PENDING, ACTIVE, FAILED, yanked
  testcase.go                                 module test cases as TESTING runs through driving.Runs
  metadata.go                                 inputs, outputs, providers, submodules
  provider.go gpg.go verify.go                drafts, publish, revoke, checksums, signatures
  share.go                                    space inheritance, explicit sharing, public flag
  protocol.go                                 implements driving.RegistryProtocol
internal/adapter/driven/postgres/registry/    modules, module_versions, providers, provider_versions,
                                              provider_version_platforms, gpg_keys
internal/adapter/driven/objectstore/          modules store, no expiry
```

The registry protocol is a driving adapter with its own wire rules
([report 15](../reference/15-module-and-provider-registry.md#9-registry-protocol)),
excluded from oapi-codegen like every non-REST protocol. Module test runs
are runs: `core/registry` asks `driving.Runs.Trigger` for a `TESTING` run per
test case and listens for `RunStateChanged` to move the version from
`PENDING` to `ACTIVE` or `FAILED`
([report 15](../reference/15-module-and-provider-registry.md#6-versions)).
The download URL is a presigned object URL from `driven.ObjectStore`, with
the expiry the protocol tolerates.

### Access, identity and spaces

```text
internal/adapter/driving/http/middleware/authn.go   credentials to kernel.Principal through driving.Authenticator
internal/core/port/driving/identity.go        Authenticator, Accounts, Users, APIKeys, Sessions
internal/core/port/driving/access.go          Spaces, Roles, Bindings; Authorizer: For(ctx, Principal)
                                              (Decider, error); Decider: Allows(action, space),
                                              Reachable() []SpaceID
internal/core/account/
  account.go user.go apikey.go session.go     (P3) API key exchange, session snapshots
  token.go                                    (P3) short-lived tokens over driven.TokenSigner
  login.go                                    (P13) login policy through driving.PolicyDecisions
  sso.go scim.go                              (P23)
internal/core/space/
  tree.go                                     (P4) root, parent, inheritance toggle, re-parenting checks
  actions.go                                  (P4) the 150-value action catalogue and legacy fallback
  role.go binding.go                          (P4) system and custom roles; five actor kinds
  decide.go                                   (P4) Decider: propagation down, read up, implicit stack read
internal/adapter/driven/keys/                 signing keys, JWKS publication
internal/adapter/driven/idp/{saml,oidc}/      (P23)
internal/adapter/driven/postgres/{accounts,spaces}/
```

The middleware authenticates once per request and stores a
`kernel.Principal`; every core service that must authorise asks
`driving.Authorizer.For(ctx, principal)` for a `Decider` and asks it
questions
([report 14](../reference/14-spaces-rbac-and-identity.md#4-role-bindings)).
The space filter that almost every list needs
([report 28](../reference/28-data-model.md#121-shapes-that-recur)) is
expressed as `Decider.Reachable()`, which every store port takes as a
parameter, so the SQL adapters can push it into the `WHERE` clause without
knowing how reachability was computed. Run-scoped principals (the state
backend, the registry inside a run, the worker API) are a principal kind of
their own, minted by `core/account` and checked by the same `Decider`.

### Events: notifications, webhooks and audit

```text
internal/core/port/driven/outbox.go           Outbox: Append(ctx, Event): events row and events.fanout job
internal/core/port/driven/audit.go            AuditLog: Record(ctx, Entry) in the change's Tx
internal/core/port/driving/events.go          Subscriber: Kinds() []string; Handle(ctx, Event) error
internal/adapter/driving/outbox/
  fanout.go                                   the events.fanout job: calls each subscriber, which enqueues
                                              one events.deliver job per target; at least once
internal/adapter/driven/postgres/outbox/      the events table of decision 0015, retention
internal/core/event/
  catalogue.go                                event names and payload versions
  inbox.go                                    notification inbox: grouping key, occurrences, dismissal
  route.go                                    Subscriber for run and module events: notification policy
                                              routes through driving.PolicyDecisions.Notification
  webhooks.go                                 named and stack webhooks; auto-disable after failures
  audit.go                                    audit trail reads, retention and the ordered audit webhook
                                              shipper; entries arrive through driven.AuditLog
  delivery.go                                 delivery records; bodies to the deliveries bucket
internal/core/port/driven/delivery.go         Delivery: Send(ctx, Request) (Response, error)
internal/adapter/driven/delivery/             HMAC SHA-256 signatures, secret headers, timeouts
internal/adapter/driven/slack/
```

Events fit the hexagon as mechanism: appending is a driven port called
inside the producer's unit of work, fan-out is a driving adapter, and
consumers are core services behind `driving.Subscriber`
([report 19](../reference/19-events-notifications-audit.md#1-the-outbound-surfaces)).
The subscribers at P27 are `core/event` (inbox, webhooks, audit, Slack),
`core/vcs` (checks and PR comments), `core/schedule` (dependants and drift
reconciliation), `core/registry` (test results), `core/policy` (trigger
policies) and `core/flows` (Deploy subscription blocks). What the hexagon
does not supply is a rule about who may subscribe to what: every
subscriber is in the same core, the catalogue is one package, and the event
payload types live in `core/port/driving/types.go` so that subscribers can
read them without importing the producer.

### Flows engine

```text
internal/core/port/driving/flows.go           Organizations, Projects, Flows, Blocks, Apps,
                                              Installations, AgentPools, Invocations, Secrets, DataTables
internal/core/port/driven/flowsruntime.go     Runtime: Evaluate(expr, scope), Invoke(handler, input)
internal/core/flows/
  org.go project.go membership.go             (P26) own tenancy: organizations and projects, UUIDs
  flow.go block.go link.go definition.go      (P26) flow definition YAML, block catalogue
  app.go installation.go registry.go          (P26) app versions, bundles, subscriptions
  agentpool.go                                (P26) pools, tokens, project exposure
  deploy.go                                   (P26) Deploy subscription blocks: Subscriber for run events
  engine/
    event.go lineage.go                       events with primary and secondary parents
    invocation.go                             derived status, 60-day retention
    limits.go                                 monthly and hourly event limits
    dispatch.go                               block execution through driven.Runtime
internal/adapter/driving/http/flowsapi/        REST for flowctl and the provider; flow HTTP endpoints; MCP
internal/adapter/driving/gateway/flows.go      agent WebSockets
internal/adapter/driven/flowsruntime/          routes evaluations and app handlers to agents
internal/adapter/driven/postgres/flows/        flows_* tables
```

Flows is a separate product with its own tenancy and identifiers
([report 23](../reference/23-flows.md#2-resource-hierarchy)). In a single
hexagon it is two core packages and a handful of adapters, separated from
Deploy only by rule 3: it reaches Deploy through `driving.Runs` and
`driving.Subscriber`, and Deploy never reaches it. The engine's queueing
(events per block, retries, the hourly limit,
[report 23](../reference/23-flows.md#4-events-invocations-and-limits)) uses
the same `driven.Queue` as runs, under its own job kinds. JavaScript never
runs in the server: block expressions and app handlers run on agents that
dial the gateway, and `driven.Runtime` is the port to them (decision 0018).

### Composition root

```text
internal/app/
  app.go                                      Open(ctx, cfg): shared driven adapters; Close
  serve.go                                    the serve role: router with every driving HTTP adapter
  scheduler.go                                (P7) the scheduler role: job handlers, cron, the ticker
  worker.go                                   (P8) the worker role: internal/worker only
  agents.go                                   (P19, P26) vcs-agent and flows-agent: internal/worker only
  all.go                                      serve and scheduler in one process
  services.go                                 constructs every core service from driven adapters
  ports.go                                    a struct holding every driving port, for the adapters
  router.go                                   chi router: middleware, strict servers, hand-written routes
  migrate.go                                  (P2) migrations at start-up, and migrate up and migrate status
  cli/                                        cobra commands that call the files above
  config/
```

```go
// internal/app/services.go at P13 (shape)
func buildServices(d *driven) *ports {
	p := &ports{}
	p.Authorizer = &space.Service{Tx: d.tx, Spaces: d.pg.Spaces(), Roles: d.pg.Roles()}
	p.Vendors = iac.NewRegistry(terraform.Vendor{}, opentofu.Vendor{})
	p.Policies = &policy.Service{
		Tx: d.tx, Policies: d.pg.Policies(), Engine: d.opa, Objects: d.objects, Authz: p.Authorizer,
	}
	p.Runs = &run.Service{
		Tx: d.tx, Runs: d.pg.Runs(), Queue: d.queue, Outbox: d.outbox, Clock: d.clock,
		Policies: p.Policies, Vendors: p.Vendors, Authz: p.Authorizer,
	}
	p.Stacks = &stack.Service{
		Tx: d.tx, Stacks: d.pg.Stacks(), Outbox: d.outbox, Vendors: p.Vendors, Authz: p.Authorizer,
	}
	p.Webhooks = &vcs.Service{
		Tx: d.tx, Store: d.pg.VCS(), Providers: d.vcs, Queue: d.queue,
		Policies: p.Policies, Runs: p.Runs, Stacks: p.Stacks,
	}
	// a service whose port is needed by one constructed later takes a setter,
	// because Go has no forward references between struct literals
	return p
}

// internal/app/worker.go (P8): the worker role never calls buildServices
func Worker(ctx context.Context, cfg config.Worker) error {
	cp := controlplane.New(cfg.ServerURL, cfg.PoolToken)
	return launcher.New(cp, docker.New(cfg.Docker), workspace.New(), binaries.New(cfg.Cache)).Run(ctx)
}
```

Roles are functions over the same constructors. `serve` builds every
service and every HTTP driving adapter; `scheduler` builds every service and
the queue handlers, cron and the event fan-out; `all` does both in one process
for single-node installs and the acceptance suite; `worker` builds the
worker hexagon only. Spacelift ships the same split, a server and a drain
from one binary
([report 24](../reference/24-self-hosted-reference.md#21-process-inventory)),
and the hexagon expresses it without a structural change: a role is a choice
of driving adapters. The cost is that `services.go` is the one file that
names every service and every cross-domain port; at P27 it constructs about
twenty-five services and runs to several hundred lines, and every phase
edits it.

### Generated code

```text
api/
  src/deploy/*.yaml                           fragments; make scaffold adds paths-<family>.yaml
  deploy.openapi.yaml                         bundled by internal/tooling/openapibundle
  oapi-server-deploy.yaml                     output: internal/adapter/driving/http/gen/deploy/server.gen.go
  oapi-client-deploy.yaml                     output: pkg/client/deploy/client.gen.go (unchanged)
  src/worker/*.yaml                           (P8) worker protocol
  oapi-server-worker.yaml                     (P8) output: internal/adapter/driving/http/gen/worker/server.gen.go
  oapi-client-worker.yaml                     (P8) output: internal/worker/adapter/controlplane/client/client.gen.go
internal/tooling/specref/                     reference generation, scaffold, inventory (report 29)
internal/tooling/stubgen/                     UnimplementedStrict for each generated strict server
```

| Spec | Generated server | Implemented by | Client |
| --- | --- | --- | --- |
| deploy | `adapter/driving/http/gen/deploy` | `adapter/driving/http/deploy/<family>`, composed in `deploy/server.go` | `pkg/client/deploy` (public: the provider and `infraweavectl` import it) |
| worker (P8) | `adapter/driving/http/gen/worker` | `adapter/driving/http/workerapi` | `internal/worker/adapter/controlplane/client` (internal: both ends ship in one binary and version together) |
| none: hand-written | none | `webhook`, `statebackend`, `registryproto`, `flowsapi`, `mcp` | none |

Generated code is entirely an adapter concern; no core package imports a
generated type. The `deploy` strict interface spans every domain, so each
family package implements its slice and `deploy/server.go` composes them:

```go
// internal/adapter/driving/http/deploy/server.go (shape at P13)
type Server struct {
	*stacks.API   // StackCreate, StackUpdate, Stacks, ...
	*runs.API     // RunTrigger, RunConfirm, ... (P7)
	*config.API   // ContextCreate, ... (P6)
	*policies.API // PolicyCreate, PolicyAttach, ... (P13)
	stubs         // depth two: every operation answers 501 until a family claims it
}

type stubs struct{ deploy.UnimplementedStrict }

var _ deploy.StrictServerInterface = (*Server)(nil) // two families claiming one operation fail here
```

Each family package holds the driving ports it needs and converts generated
request objects to the DTOs in `core/port/driving/types.go`. The Makefile's
`generate` loop runs stubgen against
`internal/adapter/driving/http/gen/$$s/server.gen.go`, and `verify-generate`
diffs that directory instead of `internal/server/gen`.

### Tests and enforcement

| Kind | Location |
| --- | --- |
| Unit tests | beside the code in every core, adapter and worker package |
| Core service tests | `core/<domain>/*_test.go`, with fakes of driven ports and of the driving ports of other domains |
| State machine tests | `core/run/machine_test.go`: every pair of report 08's states, allowed or forbidden |
| Driven adapter contract runs | each adapter's tests call `contract/<port>test.Run`; PostgreSQL and MinIO through testcontainers |
| Handler tests | `adapter/driving/http/...`, with fakes of driving ports |
| Worker tests | `worker/core/...` with fake ports; `worker/adapter/docker` against a Docker daemon in CI |
| Acceptance | `test/acceptance`: godog features against `infraweave all` and a real worker (decision 0016) |

The nine rules are depguard lists in `.golangci.yml`. depguard matches
imports by plain string prefix. In `lax` mode it resolves an import matched
by both an allow and a deny entry in favour of the longer prefix, so "deny
the core, allow its ports" is one list; in the default `original` mode the
deny wins and a non-empty allow list denies everything it does not name,
the standard library included, so rules 3 and 4 set `lax`:

```yaml
linters:
  enable: [depguard]
  settings:
    depguard:
      rules:
        core:                                   # rule 1
          files: ["**/internal/core/**"]
          deny:
            - pkg: github.com/weaveplatform/infraweave/internal/adapter
              desc: the core never imports an adapter
            - pkg: github.com/weaveplatform/infraweave/internal/app
            - pkg: github.com/weaveplatform/infraweave/internal/worker
            - pkg: github.com/weaveplatform/infraweave/internal/tooling
            - pkg: github.com/jackc/pgx
            - pkg: github.com/open-policy-agent/opa
            - pkg: github.com/aws/aws-sdk-go-v2
            - pkg: github.com/go-chi/chi
            - pkg: net/http
        core-run:                               # rule 3, one list per core domain
          list-mode: lax
          files: ["**/internal/core/run/**"]
          deny:
            - pkg: github.com/weaveplatform/infraweave/internal/core/
              desc: reach other domains through internal/core/port/driving
          allow:
            - github.com/weaveplatform/infraweave/internal/core/run
            - github.com/weaveplatform/infraweave/internal/core/port
            - github.com/weaveplatform/infraweave/internal/core/kernel
        driven:                                 # rule 4
          list-mode: lax
          files: ["**/internal/adapter/driven/**"]
          deny:
            - pkg: github.com/weaveplatform/infraweave/internal/core/
            - pkg: github.com/weaveplatform/infraweave/internal/adapter/driving
            - pkg: github.com/weaveplatform/infraweave/internal/app
          allow:
            - github.com/weaveplatform/infraweave/internal/core/port
            - github.com/weaveplatform/infraweave/internal/core/kernel
        worker:                                 # rule 6
          files: ["**/internal/worker/**", "**/internal/app/worker.go"]
          deny:
            - pkg: github.com/weaveplatform/infraweave/internal/core
            - pkg: github.com/weaveplatform/infraweave/internal/adapter
            - pkg: github.com/jackc/pgx
            - pkg: github.com/aws/aws-sdk-go-v2/service/s3
```

Rule 3's list is written once per core domain, because a deny entry is a
prefix and the allowed own-domain prefix differs per domain: about six
lists at P6 and twenty-one at P27. A domain added without its list is not
an error, only unguarded, so the review checklist names the list. The
`.golangci.yml` setting `new: true` with `whole-files: true` reports a
forbidden import in any file a change touches, which covers every new import.

Coverage implications:

- About 25 packages at P1, about 175 at P27.
- Port-only packages (`core/port/driving`, `core/port/driven`) hold
  interfaces and DTOs; DTO helpers need small tests to clear 90%.
- `internal/app` and `internal/app/cli` are mostly wiring and are covered by
  the acceptance suite's `infraweave all`, merged into the gate.
- `adapter/driving/gateway`, `worker/adapter/kubernetes` and
  `adapter/driven/cloud/*` need real or emulated endpoints; they are the
  packages most likely to sit under the per-package floor.

## Walkthroughs

### A GitHub push becomes a proposed run, a plan policy and a PR comment

1. `internal/adapter/driving/http/router.go` (built in `internal/app/router.go`): chi routes `POST /webhooks/vcs/github/{integration}` to `adapter/driving/http/webhook/webhook.go`.
2. `webhook.go`: reads the body under a size cap and calls `driving.Webhooks.Ingest`.
3. `internal/core/vcs/ingest.go`: loads the integration through `driven.VCSStore`; asks `driven.VCSProvider` (`internal/adapter/driven/vcs/github/verify.go`) to check `X-Hub-Signature-256`.
4. `ingest.go`: in one `driven.UnitOfWork.Tx` (`internal/adapter/driven/postgres`), stores the delivery (`postgres/vcs`) and enqueues `vcs.process_delivery` on the `vcs-inbox` queue through `driven.Queue` (`internal/adapter/driven/queue`); `webhook.go` answers `202`.
5. Scheduler role, `internal/adapter/driving/scheduler/consume.go`: claims the job and calls `driving.Webhooks.Process`.
6. `internal/core/vcs/normalize.go` with `vcs/github/parse.go`: the pull request event becomes one `PullRequestEvent`; `route.go` finds the stacks bound to the repository; `dedupe.go` drops a second event for the same SHA and run type.
7. `internal/core/vcs/process.go`: calls `driving.PolicyDecisions.Push`, implemented by `internal/core/policy/decide.go`, which builds the push input (`policy/input/push.go`) and evaluates attached push policies through `driven.PolicyEngine` (`internal/adapter/driven/opa/engine.go`); the result is `propose`.
8. `process.go`: calls `driving.Runs.Trigger` with type `PROPOSED`; `internal/core/run/trigger.go` inserts the run in `QUEUED` (T1), appends `RunStateChanged` to the outbox and enqueues `run.schedule`, all in one unit of work.
9. Scheduler role: `run.schedule` runs `core/run/gates.go`; a proposed run takes no stack lock, so it moves to `READY` (T2).
10. Serve role, `internal/adapter/driving/http/workerapi/poll.go`: a public-pool worker's held long-poll wakes and calls `driving.RunProgress.Claim`; `postgres/runs/claim.go` picks the run by tier and age; `core/run/progress.go` moves it to `PREPARING` (T7) and returns the job.
11. Worker role, `internal/worker/core/launcher/launcher.go`: starts the runner container through `worker/adapter/docker`; inside it, `worker/core/executor/phases.go` runs `INITIALIZING` and `PLANNING` with `worker/core/iac/opentofu`, reporting each transition through `worker/adapter/controlplane` (T10, T12).
12. `executor/phases.go`: uploads the plan JSON through a presigned URL and reports the plan; `workerapi/server.go` calls `driving.RunProgress.Report`.
13. `internal/core/run/progress.go`: asks `driving.Vendors.PlanInput` (`internal/core/iac/opentofu`) for the plan policy input, then `driving.PolicyDecisions.Plan`; `core/policy/decide.go` evaluates the plan policies, combines warnings and stores receipts.
14. `core/run/progress.go`: no warning and a proposed run, so `PLANNING` to `FINISHED` (T17) in one unit of work with the receipts and the `RunStateChanged` outbox event.
15. Scheduler role, `internal/adapter/driving/outbox/fanout.go`: hands the event to subscribers; `internal/core/vcs/feedback.go` sets the commit check, and `internal/core/event/route.go` asks `driving.PolicyDecisions.Notification` for routes.
16. A notification policy `pull_request` rule returns a comment route with a deduplication key; `core/vcs/feedback.go` calls `driven.VCSProvider.Comment` (`vcs/github/comments.go`), which creates or edits the PR comment ([report 11](../reference/11-vcs-integrations.md#123-pull-request-comments)).

### A tracked run awaits approval and applies on a private worker

1. A push to the tracked branch follows steps 1 to 7 above with the push decision `track`; `core/run/trigger.go` creates a `TRACKED` run on a stack whose worker pool is private.
2. `core/run/gates.go`: the stack has an approval policy; `driving.PolicyDecisions.Approval` evaluates it on entering `QUEUED` and approves, and the stack lock is free, so `READY` (T2).
3. Serve role, `workerapi/poll.go`: the private pool's worker, authenticated by its pool credentials (`workerapi/auth.go`), claims the run; `READY` to `PREPARING` (T7).
4. Worker role on the customer's host: `worker/core/launcher` starts the container; `executor` plans with the OpenTofu command set; the state backend configuration it writes points at `adapter/driving/http/statebackend`, which accepts the run-scoped token because `core/state/access.go` sees a run in `PLANNING`.
5. `core/run/progress.go`: the plan has changes and autodeploy is off, so `PLANNING` to `UNCONFIRMED` (T20); the worker uploads the encrypted workspace to the workspace bucket and leaves the run.
6. `driving.PolicyDecisions.Approval` runs again on `UNCONFIRMED`; it requires two approvals, so `Run.canConfirm` is false.
7. Two reviewers call `POST /runs/.../review`; `adapter/driving/http/deploy/runs` calls `driving.Runs.Review`; `core/run/actions.go` stores each review and re-evaluates approval; the second approves.
8. A writer calls confirm; `core/run/actions.go` checks `canConfirm` and moves `UNCONFIRMED` to `CONFIRMED` (T26) with an outbox event.
9. The private worker's next poll claims the confirmed run; `CONFIRMED` to `PREPARING_APPLY` (T33); the launcher starts a fresh container and the executor restores the workspace through a presigned URL.
10. `executor/phases.go`: apply; the state backend accepts writes because the run is in `APPLYING`; `PREPARING_APPLY` to `APPLYING` to `FINISHED` (T35, T37).
11. `core/run/service.go`: the terminal transition of a blocking run enqueues `run.schedule` for the stack, so the next queued tracked run can become `READY`; the event fan-out delivers `RunStateChanged` to `core/vcs/feedback.go` (GitHub deployment marked active) and `core/event` (webhooks, and the audit webhook when it includes runs).

### Adding an IaC vendor: Pulumi (P20)

1. `api/src/deploy/components-*.yaml`: the `pulumi` vendor block (`loginURL`, `stackName`) in the stack input and output, if the scaffold has not already copied it; `make generate`.
2. `internal/core/iac/pulumi/pulumi.go`: `Vendor` implementing `iac.Vendor`: settings validation, default runner image, workflow keys.
3. `internal/core/iac/pulumi/input.go`: the plan policy input block for Pulumi ([report 20](../reference/20-vendors.md#2-plan-policy-input-per-vendor)).
4. `internal/core/iac/pulumi/changes.go`: preview JSON to the common changes model.
5. `internal/core/iac/pulumi/*_test.go` and `testdata/` with recorded previews.
6. `internal/app/services.go`: add `pulumi.Vendor{}` to `iac.NewRegistry`.
7. `internal/worker/core/iac/pulumi/commands.go`: `pulumi login`, `stack select --create`, `preview`, `up`, `destroy` with the documented flags.
8. `internal/worker/core/iac/pulumi/commands_test.go`.
9. `internal/worker/core/iac/registry.go`: register the command set.
10. `internal/adapter/driving/http/deploy/stacks/convert.go`: map the generated `pulumi` block to the opaque `VendorConfig`.
11. `.golangci.yml`: nothing; both `iac` packages fall under existing lists.
12. `test/acceptance/features/iac_pulumi.feature` and its steps, using a Pulumi runner image.

About 10 new files and 4 edited, in two hexagons: the server side decides
what Pulumi settings and plans mean, the worker side decides how to run it.
Neither side imports the other; the job payload carries the vendor name and
settings as data.

### Adding a business domain: templates (P21)

1. `make scaffold TAG=Templates FAMILY=templates` writes `api/src/deploy/paths-templates.yaml`; `make generate` regenerates `gen/deploy`, the stubs and `pkg/client/deploy`.
2. `internal/core/port/driving/templates.go`: `Templates`, `TemplateVersions`, `TemplateDeployments`, `Blueprints`; DTOs added to `types.go`.
3. `internal/core/port/driven/templatestore.go`: `TemplateStore`, `TemplateVersionStore`, `DeploymentStore`.
4. `internal/core/template/template.go`, `version.go` (draft, published, deprecated), `deployment.go`, `render.go` (inputs into stack definitions), `blueprint.go`, `service.go` and tests ([report 17](../reference/17-blueprints-and-templates.md#9-template-deployments)).
5. `internal/core/template/deployment.go`: creates and updates stacks through `driving.Stacks` inside the same unit of work; the transaction travels in the context.
6. `internal/adapter/driven/postgres/templates/templates.go`, `versions.go`, `deployments.go`.
7. `internal/adapter/driven/postgres/migrations/NNNNN_templates.sql`: the tables plus the nullable `template_deployment` column on `stacks`.
8. `internal/contract/storetest/templates.go`.
9. `internal/adapter/driving/http/deploy/templates/templates.go`, `convert.go` and tests.
10. `internal/adapter/driving/http/deploy/server.go`: embed `*templates.API`.
11. `internal/app/services.go` and `ports.go`: construct the service and expose its port.
12. `.golangci.yml`: a `core-template` list for rule 3.
13. `test/acceptance/features/templates_deployments.feature` and steps.

About 20 new files and 6 edited. The domain touches every top-level area of
`internal/` (`core/port`, `core/template`, `adapter/driven/postgres`,
`contract`, `adapter/driving/http`, `app`) plus the shared `types.go`,
`migrations/` stream and `services.go`, which every other domain also edits.

## Package mapping

| Today's package | New location | Notes |
| --- | --- | --- |
| `cmd/` (`cmd/README.md`) | `cmd/infraweave/main.go`, `cmd/README.md` | `main.go` builds the cobra root from `internal/app/cli`; `cmd/infraweavectl` arrives with P24 |
| `api` (`api/doc.go`, `api/deploy_test.go`) | `api` | unchanged; the embedded specs feed the router and the parity test |
| `api/src/deploy` | `api/src/deploy` | unchanged; families added by `make scaffold`; `api/src/worker` joins at P8 |
| `api/reference/spacelift` | `api/reference/spacelift` | unchanged; read only by `internal/tooling/specref` and tests |
| `internal/buildinfo` | `internal/buildinfo` | unchanged path, so the Makefile's `-ldflags -X` paths stay valid |
| `internal/docscheck` | `internal/tooling/docscheck` | `make docs-check` path changes |
| `internal/openapibundle` | `internal/tooling/openapibundle` | |
| `internal/server/gen/deploy` | `internal/adapter/driving/http/gen/deploy` | generated; output path in `api/oapi-server-deploy.yaml` |
| `internal/server/problem` | `internal/adapter/driving/http/problem` | maps `kernel` error kinds to problem types; slugs stay in decision 0003 and 0022 |
| `internal/specref` | `internal/tooling/specref` | |
| `internal/stubgen` | `internal/tooling/stubgen` | |
| `pkg/client/deploy` | `pkg/client/deploy` | unchanged |
| `tools/openapi-bundle` | `tools/openapi-bundle` | imports `internal/tooling/openapibundle` |
| `tools/specref` | `tools/specref` | imports `internal/tooling/specref` |
| `tools/stubgen` | `tools/stubgen` | imports `internal/tooling/stubgen` |
| `test/acceptance` | `test/acceptance` | gains a feature per domain and a harness that starts `infraweave all` and a worker |
| `api/oapi-server-deploy.yaml` | same file | `output: internal/adapter/driving/http/gen/deploy/server.gen.go` |
| `Makefile` | same file | stubgen and `verify-generate` read `internal/adapter/driving/http/gen`; `docs-check` runs `./internal/tooling/docscheck` |
| `.golangci.yml` | same file | depguard enabled with the nine rules; per-domain lists for rule 3 |
| `.testcoverage.yml` | same file | `^internal/server/gen/` becomes `^internal/adapter/driving/http/gen/`; adds the internal worker client |

## Numbers

Estimates, rounded.

| Measure | Today (P1) | End of P27 |
| --- | --- | --- |
| Go packages | about 25 (13 today plus `cmd/infraweave`, `app`, `app/cli`, `app/config`, `core/kernel`, two port packages, `core/stack`, `http`, `http/middleware`, `http/deploy`, `http/deploy/stacks`) | about 175 |
| Top-level directories under `internal/` | 6 (`app`, `buildinfo`, `core`, `adapter`, `tooling`, plus `contract` from P2) | 7 (`worker` from P8) |
| Dependency rules | 9 | 9 |
| depguard lists and entries | 9 lists, about 35 entries | about 30 lists (21 for rule 3), about 130 entries |
| Files touched to add a vendor | about 10 new, 4 edited | same |
| Files touched to add a domain | about 20 new, 6 edited | same, with the shared files longer each time |
| Packages a stack update crosses | 6 hand-written: `http/middleware`, `http/deploy/stacks`, `core/port/driving`, `core/stack`, `core/port/driven`, `postgres/stacks`; plus `gen/deploy` | same |
| Largest package | `adapter/driving/http/gen/deploy`, about 24k generated lines | `gen/deploy`, about 240k generated lines at full scope (725 reference operations); hand-written: `core/run`, about 10k lines |
| Thin packages at risk under the 90% floor | `app/cli`, `core/port/driving` DTO helpers | about 12: `app`, `app/cli`, the port packages, `gateway`, `worker/adapter/kubernetes`, `cloud/*`, `idp/*`, `ai/*` |

## Placement choices and friction

Placement choices settled in this file:

- **One core, split into domain packages, with rule 3.** A single
  `core/service` directory with twenty-five siblings and no rule between
  them would be the hexagon's default. infraweave adds rule 3 so that
  cross-domain calls go through `core/port/driving`, which keeps every
  domain testable alone and every cross-domain dependency visible in
  `internal/app/services.go`.
- **One driving and one driven port package, files per domain.** One package
  per port would add about forty packages with no statements; one package
  per side keeps the ports in two places a reader can scan.
- **One PostgreSQL schema and migration stream, one store package per
  domain.** The hexagon has one data model, so foreign keys may cross
  domains (a run references its stack, a policy receipt its policy). Store
  packages are split per domain only to keep package size and coverage
  manageable.
- **No in-memory store adapter.** Core tests use small fakes of the ports
  they call, and store behaviour is proven by the contract suites against
  PostgreSQL in a container. A second full store implementation would double
  every table's code for no production use.
- **The worker role is a second hexagon, not an adapter of the first.** Its
  logic (the launcher loop, phases, vendor command sets) is business logic
  of its own, and its isolation from server code is a security property,
  not a convenience.
- **Claiming runs is a serve-role transition.** The long-poll request is
  where the worker waits, so the claim happens there; the scheduler role
  owns everything with no client waiting.
- **The worker protocol client is internal.** Both ends ship in one binary;
  `pkg/client/deploy` stays the only public package.
- **IaC vendor code lives under `iac/`, not `vendor/`.** The go command
  treats any directory named `vendor` as vendored dependencies: `go list
  ./...` skips it and an import of a path through it fails, so
  `core/iac/<name>` and `worker/core/iac/<name>` hold the vendor packages.
- **Non-REST protocols are driving adapters beside the strict server.**
  Webhooks, the state backend and the registry protocol are hand-written chi
  routes under `adapter/driving/http`, excluded from code generation by the
  `handwritten` tag.

Where this layout rubs against the system:

- **Shared files for every phase.** `core/port/driving/types.go`,
  `core/port/driven/stores.go`, the single migration stream and
  `internal/app/services.go` are edited by every domain on the spine.
  Parallel phases (P19 to P23 can run side by side) meet in these files.
- **A domain lives in five places.** Templates sit in `core/port/driving`,
  `core/port/driven`, `core/template`, `adapter/driven/postgres/templates`
  and `adapter/driving/http/deploy/templates`, far apart in the tree.
  Ownership and review of one domain span the whole of `internal/`.
- **DTOs leave their domains.** Request and result types must live in
  `core/port/driving/types.go` so that handlers and other domains can use
  them without importing the domain package. `types.go` becomes the largest
  hand-written file in the core and couples every domain's API to one file.
- **Wide driving ports.** `driving.Runs` carries about fifteen actions and
  `driving.RunProgress` the whole worker conversation; fakes of them are large,
  and every new action edits the interface, its fake and its adapter.
- **The run transaction crosses ports.** A plan decision, its receipts and
  the run's move to `UNCONFIRMED` must commit together, so `core/policy` writes
  receipts inside a unit of work that `core/run` opened, through a driving
  port. The context-carried transaction makes it work, but no rule says
  which driving ports may be called inside another domain's transaction.
- **Rule 3 costs one depguard list per domain**, and a domain added without
  its list is silently unguarded. The hexagon's own rules (1, 2, 4 to 9) are
  stable; rule 3 grows with the spine.
- **The worker hexagon repeats server code it may not import.** Decision
  0009 has the launcher link the server's OPA engine, and decision 0012
  compiles the VCS agent's allowlist from the provider clients' route
  tables; rule 6 keeps both out of reach, so `worker/adapter/rego` repeats
  the capability set and `core/vcsagent` needs a generated copy of the
  route tables, each with a parity test.
- **Vendors are split across two hexagons.** Adding Pulumi edits
  `core/iac/pulumi` and `worker/core/iac/pulumi`, with the job payload
  as the only contract between them; a mismatch shows only in acceptance.
- **Flows shares the core without sharing its tenancy.** Organizations and
  projects are a second tenancy model inside the same core, separated from
  spaces only by rule 3; extracting Flows into its own service later means
  splitting the schema stream and `services.go`, not just moving a directory.
- **The composition root concentrates the system.** `internal/app` is the
  only package that sees everything; at P27 it is the largest hand-written
  coordination point and the first place a merge conflict appears.
