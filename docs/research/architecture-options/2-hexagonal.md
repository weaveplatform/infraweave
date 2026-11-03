# Option 2: Hexagonal (ports and adapters), detailed layout

Detailed repository layout for option 2 of the
[architecture options](README.md): one application core for the whole of
infraweave, surrounded by adapters, with every dependency pointing at the
core and a second, small hexagon for the worker side. Every package in
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

The tree is organised by the direction of dependency. One application core,
`internal/core/`, holds the domain model and the application services of
every Deploy and Flows domain: organizations, accounts, spaces, stacks, configuration,
runs, schedules, workers, vendors, state, VCS, policies, cloud
integrations, the registry, events, features and tiers, expressions,
templates, resources, Intelligence and Flows. The core declares two kinds of port. Driving ports, in
`internal/core/port/driving`, are the interfaces through which the outside
world asks the core to do something: create a stack, trigger a run, report a
worker's progress, ingest a webhook, read a state file. Driven ports, in
`internal/core/port/driven`, are the interfaces through which the core
reaches the outside world: the PostgreSQL stores and the unit of work,
object storage, the work queue, the policy engine, VCS providers, cloud
credential issuers, the token signer, secrets sealing, outbound delivery,
AI providers, Flows app tasks and the clock.

Adapters implement ports and live in `internal/adapter/`. Driving adapters,
in `internal/adapter/driving/`, turn something outside into a call on a
driving port: the HTTP server (the generated `deploy` strict server, the
worker long-poll API and the hand-written protocols for VCS webhooks, the
Terraform state backend and the registry), the queue consumers and cron of
the scheduler process mode, the server-sent event streams, the console's
static files and the gateways for VCS agents and Flows agents. Driven
adapters, in `internal/adapter/driven/`, implement driven ports over real
infrastructure: PostgreSQL, the four object store drivers (`s3`, `gcs`,
`azureblob`, `fs`), decision 0011's queue, embedded OPA, GitHub and the
other VCS providers, AWS, Azure and GCP, KMS, SMTP and Slack.

The worker side is a second hexagon, `internal/worker/`, because it runs on
customer hosts with outbound HTTPS only
([report 10](../reference/10-workers-and-execution.md#42-transports)). Its
core holds the launcher loop, the runner's phase loop and the per-vendor command
sets; its ports name the control plane, the container runtime, workspace
transfer and tool binaries; its adapters speak the worker protocol, Docker
and Kubernetes. The Flows agent lives in the same hexagon, beside the
launcher, because it too runs on customer hosts and dials out. The worker
hexagon shares no package with the server's core or adapters, so the worker
side's construction path links no PostgreSQL adapter, no object storage
credentials and no other domain's internals.

Two pure libraries belong to neither hexagon, because both run the same
code: `internal/shared/expr`, the expression language of decision 0024,
which the server evaluates and the runner uses to render workflow command
templates, and `internal/shared/seal`, the sealed formats of decision 0013,
which the server writes and the launcher and runner open and write. They
hold no I/O, no port and no domain type, and they are the first packages the
two hexagons share.

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
   `internal/worker/adapter/`. From `internal/` it may import only its own
   tree and `internal/shared/...`.
7. No package under `internal/core/`, `internal/adapter/` or
   `internal/worker/` imports `internal/app/`. Only `internal/app/...` and
   `cmd/...` construct adapters and wire them to the core.
8. `internal/tooling/...` is imported only by `tools/...`; no runtime
   package imports it.
9. `pkg/...` imports nothing under `internal/` or `cmd/`.
10. `internal/shared/...` imports nothing under `internal/` except
    `internal/shared/...`: the standard library and pure libraries such as
    cel-go only.

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
                            and exits; process modes serve, scheduler, launcher, all (hidden: runner),
                            vcs-agent, flows-agent; migrate up, migrate status, secrets, version
    infraweave-agent/       slim build for worker and agent hosts: launcher, runner, vcs-agent,
                            flows-agent from internal/app/agentcli; links only the worker hexagon
                            and internal/shared; built when image size or a customer's review asks
                            for it (decision 0004)
    infraweavectl/          (P24b) the CLI over pkg/sdk only: spacectl-shaped tree, flows group,
                            infraweavectl operator, infraweavectl mcp, expr check, complete and eval
  internal/
    app/                    NEW: composition root, one file per process mode, config, cobra tree
    buildinfo/              ← internal/buildinfo (unchanged)
    core/                   NEW: the hexagon (kernel, ports, one package per domain)
    adapter/                NEW: driving/ and driven/ adapters
    worker/                 NEW (P8): the worker side's own hexagon (core, ports, adapters)
    shared/                 NEW: pure libraries both hexagons import: expr (P9), seal (P2)
    contract/               NEW (P2): contract suites for driven ports
    tooling/                NEW: specref, openapibundle, stubgen, docscheck (dev tooling, not runtime)
  pkg/
    client/deploy/          ← pkg/client/deploy (unchanged; generated)
    client/flows/           (Flows) generated from api/flows.openapi.yaml
    client/operator/        (P3b) generated from api/operator.openapi.yaml
    sdk/                    (P24) the SDK: hand-written layer over pkg/client/<spec>: token exchange,
                            Idempotency-Key on writes, 429 retries, pagination iterators, typed
                            problem errors, typed SSE stream iterators
    flows/appsdk/           (Flows) the Go app SDK: generated App and Host stubs, Serve(app), typed
                            block definitions, apptest/ for app tests without an agent or a server
  api/                      unchanged layout; oapi-server-*.yaml outputs move to
                            internal/adapter/driving/http/gen/<spec>; adds src/worker (P8),
                            src/operator (P3b, api/operator.openapi.yaml) and src/flows (Flows)
    proto/flows/app/v1/     (Flows) the App and Host gRPC services, package infraweave.flows.app.v1
  web/
    console/                (P24c) the console: React 19, TypeScript strict, Vite; orval-generated
                            client and TanStack Query hooks; console.go embeds dist/ through embed.FS
  test/
    acceptance/             ← test/acceptance; features per domain, testcontainers (decision 0016)
  tools/                    unchanged; the three spec tools import internal/tooling/*
  docs/                     unchanged
  deploy/                   (P27) Helm chart, Terraform modules for AWS, Azure and GCP, server and
                            runner images; Flows apps are OCI images their authors build
  Makefile                  generate writes servers to internal/adapter/driving/http/gen/<spec>,
                            the App stubs with buf and the console client with orval; build
                            cross-compiles cmd/* with CGO off, the console first in release builds
  .golangci.yml             depguard enabled with the ten rules above (one entry per core domain)
  .testcoverage.yml         exclusions: ^cmd/, ^tools/, ^internal/adapter/driving/http/gen/,
                            ^pkg/client/, ^pkg/flows/appsdk/appv1/,
                            ^internal/worker/adapter/controlplane/client/
  go.mod                    one module, unchanged path
```

What changes at the root and why:

- `internal/` gains five top-level areas (`core`, `adapter`, `worker`,
  `shared`, `contract`) and loses the `server` directory. Everything a
  reader needs to know about a package's permissions follows from which of
  them it is under.
- `cmd/infraweave` appears with P1 and stays a few lines long. The command
  tree lives in `internal/app/cli`, because building a process mode means building
  adapters, and only the composition root may do that (rule 7).
  `cmd/infraweave-agent` builds the worker hexagon alone from
  `internal/app/agentcli`, for hosts that should carry no server code.
- `pkg/` holds the generated clients, the SDK that wraps them, and the
  Flows app SDK. Store, object storage and queue interfaces are driven ports
  owned by the core, not public packages, because the hexagon's contract is
  internal and publishing it would freeze it. The Terraform provider lives in
  its own repository, `weaveplatform/terraform-provider-infraweave`, on
  `pkg/sdk` (decision 0001).
- `api/` stays the source of truth for specifications, including the
  protobuf contract of Flows apps. Only the generated server output path
  moves, from `internal/server/gen` to the driving HTTP adapter.
- `web/console/` is a client of the public API like `infraweavectl`; its
  build output is embedded by the binary and served by a driving adapter
  (decision 0023).

## Full package tree

Status of each area at P1 and the phase that completes it:

| Area | Status at P1 | Created or completed by |
| --- | --- | --- |
| `app`, `buildinfo`, `tooling` | full | P1 |
| `core/kernel`, `core/port` | skeleton: IDs, principal, page cursor; stack ports | P1 to P5, growing with every domain |
| `core/stack` | stub: the 72 served operations answer 501 | P5 |
| `adapter/driving/http` | router, problem details, generated `deploy` server, stubs | P1, then every phase that serves an API |
| `adapter/driven/postgres`, `objectstore`, `shared/seal` | absent | P2 |
| `core/account`, `core/space` | absent | P3, P3b, P4, P23 |
| `core/feature`, `adapter/driving/http/operator` | absent | P3b |
| `core/config` | absent | P6 |
| `core/run`, `adapter/driving/scheduler`, `adapter/driven/queue` | absent | P7, P9, P10 |
| `core/worker`, `adapter/driving/http/workerapi`, `internal/worker` | absent | P8, P14 |
| `core/iac` | absent | P9 (Terraform, OpenTofu), P20 (the rest) |
| `shared/expr`, `core/expression`, `adapter/driving/http/sse` | absent | P9 |
| `core/state`, `core/vcs`, `core/policy`, `core/cloud`, `core/schedule` | absent | P11, P12 and P19, P13, P15, P16 |
| `core/registry`, `core/event` | absent | P17, P18 |
| `core/template`, `core/resource`, `core/intel` | absent | P21, P22, P25 |
| `core/flows`, `adapter/driving/gateway` (Flows), `worker/core/flowsagent` | absent | Flows |
| `adapter/driving/http/console` | absent | P24c |

```text
internal/
  app/                                     composition root; the only package that sees everything
    cli/                                   cobra tree: serve, scheduler, launcher, all, vcs-agent, flows-agent,
                                           migrate up, migrate status, version; hidden runner; binds
                                           setting flags only (not --config)
    config/                                strict YAML decoded with viper UnmarshalExact; flag > env > file;
                                           one Settings struct per process mode and per driven adapter
    (files listed under "Composition root")
  buildinfo/                               ← internal/buildinfo
  tooling/                                 libraries behind tools/; never linked into a process mode
    specref/                               ← internal/specref
    openapibundle/                         ← internal/openapibundle
    stubgen/                               ← internal/stubgen
    docscheck/                             ← internal/docscheck

  core/
    kernel/                                value types every domain shares; no behaviour beyond parsing
      ids.go                               ULID, Slug, UUID (Flows), parse and format
      principal.go                         Principal: member, API key, organization key, run, worker, stack,
                                           operator; one organization and one account; spaces
      feature.go                           (P3b) Feature, Limit: catalogue names as typed values
      space.go                             SpaceID, SpacePath; an account's top space is the account's slug
      labels.go                            Labels, autoattach:<label> parsing
      page.go                              Page, Cursor (decision 0003 cursor encoding)
      problem.go                           domain error kinds the HTTP adapter maps to problem types,
                                           including feature-disabled, feature-not-in-tier, limit-reached
      actions.go                           AllowedActions: the set every returned resource carries
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
        identity.go                        (P3) Organizations, Accounts, Members, APIKeys, Sessions,
                                           Authenticator; (P3b) Users, OrganizationAdmin, OAuthServer
        access.go                          (P4) Spaces, Roles, Bindings, Authorizer (Decider per principal)
        sso.go                             (P23) the organization's identity providers, SCIM, linking
        intel.go                           (P25) AIIntegrations, Assistant, Intent
        features.go                        (P3b) Entitlements: For(organization) gives a Snapshot with Has,
                                           Limit, Check(feature), Reserve(account, limit, n) against the
                                           pool and the account limit; Operator: Tiers, Organizations,
                                           Grants, Billing
        expressions.go                     (P9) Expressions: Check, Complete, Evaluate; Surface:
                                           TypedContext(ref), implemented by each domain that owns one
        streams.go                         (P9) EventFeed: Subscribe(filter, lastID) for the SSE adapter
        flows.go                           (Flows) Projects, Flows, Blocks, Invocations,
                                           Apps, AgentPools, NamedExpressions; FlowTasks: Claim, Complete
        types.go                           request and result DTOs shared by the files above; every
                                           resource DTO carries AllowedActions
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
        sealer.go                          (P2) Sealer: Seal, Open, SealToPool; keyring and data keys
                                           (decision 0013)
        notify.go                          (P7) Notifications: Listen(channel) for infraweave_events,
                                           infraweave_logs and infraweave_invalidate
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
        agenttasks.go                      (Flows) AgentTasks: Submit(pool, Task) as a flows.agent_task job;
                                           app calls only, never an expression
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
    event/                                 (P7) the shared event model: envelope, kind registry and schemas,
                                           emit; (P9) EventFeed for the streams; (P18) inbox, named and
                                           stack webhooks, audit trail, notification routing, message
                                           text rendered as text templates
    feature/                               (P3b) catalogue.gen.go, tiers and their seed, organization tiers,
                                           grants, pooled limits and account limits, entitlement
                                           snapshots, the billing switch, operator service
    expression/                            (P9) editor service: Check, Complete, Evaluate over the Surface
                                           of each domain; named expressions are Flows'
    template/                              (P21) templates, versions, deployments, blueprints
    resource/                              (P22) managed entities, scans, search
    account/                               (P3) organizations, accounts, members, API keys, sessions;
                                           (P3b) users, invitations, organization keys, the Organization
                                           tag, the OAuth authorization server; (P23) the organization's
                                           identity providers, SCIM reconciliation, identity linking
    space/                                 (P4) space tree, inheritance, actions, roles, bindings, Decider
    intel/                                 (P25) AI integrations, Infra Assistant, Intent projects
    flows/                                 (Flows) the account's projects, flows, blocks, apps, agent pools,
                                           named expressions, data tables, forms
      engine/                              (Flows) delivery, buffered effects, timers, lifecycle, invocation
                                           status, limits, retention
      blocks/                              (Flows) core blocks and Deploy blocks in Go

  adapter/
    driving/
      http/                                chi router, middleware, problem mapping
        problem/                           ← internal/server/problem
        middleware/                        request ID, recovery, authentication (calls driving.Authenticator),
                                           features (P3b, calls driving.Entitlements), idempotency and rate
                                           limits (decision 0003), body limits
        sse/                               (P9) text/event-stream writer: Last-Event-ID resume, 15 s
                                           keepalive comments, slow-consumer and shutdown end messages
        console/                           (P24c) static files from web/console, index.html fallback,
                                           CSP with a per-response style nonce, placeholder page
        gen/
          deploy/                          ← internal/server/gen/deploy (server.gen.go, unimplemented.gen.go)
          worker/                          (P8) worker protocol strict server
          operator/                        (P3b) operator API strict server
          flows/                           (Flows) Flows API strict server
        deploy/                            strict server composition for the deploy spec
          server.go                        embeds every family's partial implementation, stubs one level deeper
          stacks/                          implements the Stacks operations over driving.Stacks
          runs/                            (P7) runs, tasks, reviews, logs
          config/ spaces/ identity/ workers/ policies/ vcs/ cloud/ schedules/ state/ registry/
          events/ templates/ resources/ intel/ expressions/   (P3 to P25) one package per family
        workerapi/                         (P8) implements gen/worker over driving.RunProgress and
                                           driving.Workers: register, long-poll, signals, presigned URLs
        webhook/                           (P12) POST /webhooks/vcs/{provider}/{integration}: store, then
                                           acknowledge (decision 0012)
        statebackend/                      (P11) Terraform http backend: GET, POST, LOCK, UNLOCK
        registryproto/                     (P17) /.well-known/terraform.json, modules.v1, providers.v1,
                                           login.v1 and the token endpoint
        operator/                          (P3b) implements gen/operator over driving.Operator; operator
                                           principals only
        flowsapi/                          (Flows) implements gen/flows; flow HTTP endpoints, forms, MCP
        mcp/                               (P25) platform MCP server
      scheduler/                           (P7) the scheduler process mode's loops
        consume.go                         one handler per job kind; calls the matching driving port
        cron.go                            cron and timers: drift, schedules, run expiry, retention
        ticker.go                          decision 0011's scheduler ticker: due rows claimed with
                                           SKIP LOCKED on every replica, no leader
      outbox/                              (P7) the events.fanout job: hands each event to the
                                           driving.Subscriber implementations
      gateway/                             (P19) VCS agent long-poll (decision 0012); (Flows) Flows agent
                                           WebSockets: claims flows.agent_task jobs for its sockets
    driven/
      postgres/                            (P2) pool, UnitOfWork, migrator, readiness
        migrations/                        one migration stream for the whole schema, embedded,
                                           NNNNN_*.sql (decision 0005)
        stacks/ runs/ config/ workers/ state/ vcs/ policies/ cloud/ schedules/ registry/ events/
        templates/ resources/ accounts/ spaces/ intel/ flows/
                                           one package per store port family (P2 to P25, Flows)
        outbox/                            (P7) the events table of decision 0015, fan-out marks, retention
      objectstore/                         (P2) the four drivers of decision 0005, each on its own SDK;
                                           objects written once, no bucket versioning
        s3/                                AWS S3 and S3-compatible stores, x-amz-checksum-sha256
        gcs/                               Google Cloud Storage, V4 signed URLs through signBlob
        azureblob/                         Azure Blob Storage, user delegation SAS
        fs/                                local or shared POSIX volume; signed /blobs/{token} URLs
      queue/                               (P7) the decision 0011 implementation behind driven.Queue and
                                           driven.AgentTasks; logical queues include flows
      opa/                                 (P13) embedded OPA v1: prepared queries, capabilities, timeouts
      vcs/
        github/                            (P12) App and Enterprise, HMAC verification, checks, comments
        gitlab/ bitbucketcloud/ bitbucketdc/ azuredevops/ rawgit/   (P19)
        agentpool/                         (P19) any provider reached through the VCS agent gateway
      cloud/
        aws/ azure/ gcp/ vault/            (P15) credential issuance
      keys/                                (P3) JWT signing keys, JWKS
      sealer/                              (P2) keyring over shared/seal: data keys, rewrap, rotation
      kms/                                 (P2) KEK providers: local, awskms, gcpkms, azurekv, vault
      delivery/                            (P18) HMAC-signed HTTP delivery, request and response capture
      slack/                               (P18)
      smtp/                                (P3)
      idp/
        saml/ oidc/                        (P23)
      ai/
        anthropic/ openai/ bedrock/        (P25) per decision 0019
      clock/                               system clock and ULID source

  worker/                                  (P8) the worker side's hexagon; links nothing above
    core/
      launcher/                            register, poll, accept job, start container, relay signals
      runner/                              phases and hooks inside the run container (runner)
      vcsagent/                            (P19) vcs-agent: long-polls the gateway, checks each request
                                           against its provider allowlist, forwards it (decision 0012)
      flowsagent/                          (Flows) flows-agent: WebSocket to the gateway, frames, app
                                           containers per installation and digest, Host relay
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
        apps.go                            (Flows) AppContainers: Start(image digest), Dial, Recycle
    adapter/
      controlplane/                        long-poll client over the generated worker client
        client/                            (P8) generated from api/worker.openapi.yaml
      docker/                              (P8) Docker socket
      kubernetes/                          (P14) pods per run; the controller for WorkerPool resources
      workspace/                           presigned HTTP transfer
      binaries/                            download, checksum and signature checks
      rego/                                (P14) OPA evaluation with exec allowed, worker side only
      vcsproxy/                            (P19) HTTP to the private VCS for vcsagent
      appcontainers/                       (Flows) docker (optional gVisor), kubernetes (pod per app, mTLS,
                                           NetworkPolicy), process (development only)
      appgrpc/                             (Flows) gRPC client to App and server for Host, agent-local

  contract/                                (P2) suites every implementation of a driven port runs
    storetest/                             one file per store port; run against postgres
    objecttest/                            put, get, head, delete, multipart, checksums, signed URLs,
                                           expiry; against RustFS, fake-gcs-server, Azurite and fs
    queuetest/                             (P7) claims, leases and fencing, retries and the dead state,
                                           ordering keys, priority (decision 0011)
    policytest/                            (P13) the example library corpus against opa
    vcstest/                               (P12) recorded provider payloads per adapter
    controlplanetest/                      (P8) worker protocol against workerapi and controlplane

  shared/                                  pure libraries both hexagons import (rule 10)
    expr/                                  (P9) decision 0024: CEL through cel-go, ${{ }} text templates
                                           with the $${{ escape, the curated library (cel.bind, fail,
                                           merge, pick, omit, url, json, random from the seed), the
                                           JSON Schema to CEL type provider, cost limit and timeout,
                                           compiled-program cache, POSIX shell quoting
    seal/                                  (P2) decision 0013's formats, each with a version byte: object
                                           segments in AES-256-GCM through NewGCMWithRandomNonce with
                                           index and last flag in the associated data; the pool envelope
                                           (ECDH P-256, HKDF-SHA256, AES-256-GCM)

pkg/
  client/
    deploy/                                ← pkg/client/deploy (generated)
    flows/ operator/                       (Flows, P3b) generated
  sdk/                                     (P24) the SDK over the generated clients
  flows/appsdk/                            (Flows) Go app SDK; appv1/ holds the buf-generated stubs
```

### Where each domain's code sits

| Domain | Core package | Driving adapter | Driven adapters | Worker side |
| --- | --- | --- | --- | --- |
| Stacks | `core/stack` | `http/deploy/stacks` | `postgres/stacks` | none |
| Contexts and configuration | `core/config` | `http/deploy/config` | `postgres/config`, `kms` | mounted files, env |
| Runs | `core/run` | `http/deploy/runs`, `workerapi`, `scheduler` | `postgres/runs`, `queue`, `objectstore` | `runner` |
| Workers | `core/worker` | `http/deploy/workers`, `workerapi` | `postgres/workers` | `launcher`, `docker`, `kubernetes` |
| Vendors | `core/iac/<name>` | none | none | `core/iac/<name>` |
| State | `core/state` | `statebackend`, `http/deploy/state` | `postgres/state`, `objectstore` | backend config written by `runner` |
| VCS | `core/vcs` | `webhook`, `http/deploy/vcs`, `gateway` | `vcs/<provider>`, `postgres/vcs` | source fetch with the run's credential; `core/vcsagent` |
| Policies | `core/policy` | `http/deploy/policies` | `opa`, `postgres/policies`, `objectstore` | `initpolicy`, `rego` |
| Cloud integrations | `core/cloud` | `http/deploy/cloud` | `cloud/<provider>`, `keys` | credentials in the job |
| Schedules, drift, dependencies | `core/schedule` | `http/deploy/schedules`, `scheduler` | `postgres/schedules` | none |
| Registry | `core/registry` | `registryproto`, `http/deploy/registry` | `postgres/registry`, `objectstore` | `.terraformrc` written by `runner` |
| Events | `core/event` | `http/deploy/events`, `outbox`, `http/sse` | `delivery`, `slack`, `postgres/events`, `postgres/notify` | none |
| Organizations, accounts, spaces, SSO | `core/account`, `core/space` | `http/middleware`, `http/deploy/identity`, `http/deploy/spaces` | `postgres/accounts`, `postgres/spaces`, `keys`, `idp/*` | none |
| Features and tiers | `core/feature` | `http/middleware/features.go`, `operator`, `scheduler` | `postgres/features`, `postgres/notify` | none |
| Expressions | `core/expression`, `shared/expr` | `http/deploy/expressions`, `flowsapi` | none | `shared/expr` in `runner` |
| Templates | `core/template` | `http/deploy/templates` | `postgres/templates` | none |
| Resources and search | `core/resource` | `http/deploy/resources` | `postgres/resources` | none |
| Intelligence | `core/intel` | `http/deploy/intel`, `mcp` | `ai/<provider>`, `postgres/intel` | none |
| Flows | `core/flows`, `core/flows/engine`, `core/flows/blocks` | `flowsapi`, `gateway`, `scheduler` | `postgres/flows`, `queue` (agent tasks), `objectstore` | `core/flowsagent`, `appcontainers`, `appgrpc`; the `flows-agent` subcommand (decision 0018) |
| Console and clients | none: every client calls the API | `http/console`, `http/sse` | none | none |

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
`serve` process mode inside the long-poll request, not by the scheduler: the worker
is waiting on that request, and a claim made elsewhere would need a second
hop to reach it. The `scheduler` process mode runs everything with no client
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
login decisions when a member enters an account. Each holds `driving.PolicyDecisions`; none imports
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
internal/worker/                              the worker side (see the next tree)
```

```text
internal/worker/
  core/
    launcher/
      launcher.go                             Run(ctx): register, then loop Poll → Accept → Start
      job.go                                  Job: run ID, phase plan, image, env, mounts, URLs, token
      signals.go                              stop and kill relayed to the container
    runner/
      exec.go                                 entry for `infraweave runner` inside the container
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

The worker side is a hexagon of its own with nothing in common with the
server's core except the wire format. Its core decides what to do with a
job; its ports name the four things it needs (the control plane, a
container runtime, workspace transfer and tool binaries); its adapters are
the only code that touches HTTP or Docker. Two consequences follow. First,
`internal/app/launcher.go` builds the process mode from `internal/worker/...` alone, so
a process mode that never opens a database cannot reach a PostgreSQL adapter by
accident (rule 6 and the `app/launcher.go` depguard entry). Second, the job
payload is defined twice: by the `worker` spec for the wire, and by
`internal/worker/core/launcher/job.go` for the worker's own logic, with the
conversion in `adapter/controlplane`. The duplication is the price of keeping
generated types out of a core.

The spec's secret part reaches the worker sealed to the pool's P-256 public
key in infraweave's own envelope (decision 0013): `core/run` builds the
spec and calls `driven.Sealer.SealToPool`, which `adapter/driven/sealer`
implements over `internal/shared/seal`. On the host,
`worker/core/launcher/spec.go` opens it with the pool's private key through
the same `shared/seal` package and rejects a spec issued for another run or
state version, and `runner` seals a workspace in the object segment format
before uploading it. Both formats use only FIPS 140-3 approved constructions
(AES-256-GCM through `cipher.NewGCMWithRandomNonce`, ECDH P-256,
HKDF-SHA256) and begin with a version byte. Sealing code is the one place
where the worker hexagon and the server must agree byte for byte, which is
why it is a shared library rather than a copy on each side.

Pools grow by adapter: Docker workers at P8, Kubernetes at P14
([report 10](../reference/10-workers-and-execution.md#7-docker-based-and-kubernetes-workers)).
A worker's binary is the same static `infraweave` executable; the launcher
mounts it read-only into the runner image and starts `infraweave
runner` as the container's root process, which is how Spacelift's launcher
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
  dedupe.go                                   (stack, sha, run type); comment commands bypass
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
one transaction. The `scheduler` process mode then runs `Process`, which turns the
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
decisions 0005 and 0010 write each version as its own sealed object, written
once and never overwritten, so history rests on `managed_state_versions`
rows alone. The object storage port has no version calls, every driver
(`s3`, `gcs`, `azureblob`, `fs`) passes one contract suite, and none needs
bucket versioning.

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
  share.go                                    space inheritance, explicit sharing between accounts of one
                                              organization, public flag
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
internal/core/port/driving/identity.go        Authenticator, Organizations, Accounts, Members, Users, APIKeys,
                                              Sessions
internal/core/port/driving/access.go          Spaces, Roles, Bindings; Authorizer: For(ctx, Principal)
                                              (Decider, error); Decider: Allows(action, space),
                                              Reachable() []SpaceID
internal/core/account/
  organization.go account.go member.go        (P3) root holds organizations; an organization holds accounts
                                              and members; nothing inherits from either
  apikey.go session.go                        (P3) API key exchange; sessions bound to one account
  token.go                                    (P3) short-lived tokens over driven.TokenSigner
  user.go orgadmin.go oauth.go                (P3b) users as members per account; the Organization tag;
                                              the built-in OAuth authorization server
  login.go                                    (P13) the account's login policy through
                                              driving.PolicyDecisions when a member enters it
  sso.go scim.go linking.go                   (P23) the organization's identity providers
internal/core/space/
  tree.go                                     (P4) the account's top space, parent, inheritance toggle,
                                              re-parenting checks
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

### Features and tiers

```text
internal/core/feature/
  catalogue.gen.go                            every capability, option and limit: kind, requirements,
                                              seeded tiers, covered BillingTierFeature values (decision 0021);
                                              generated from docs/data/features.json by make generate
  catalogue.go                                validates the catalogue; strict names
  tier.go seed.go                             tiers as data; the Free, Starter, Starter+, Business and
                                              Enterprise seed, installed once; Enterprise not editable
  grant.go                                    per-organization features and limit values, kept across tier changes
  limit.go                                    pooled limits across the organization's accounts; account limits
  entitlement.go                              tier plus grants minus features.disabled minus unmet
                                              requirements; limit values from grant, else tier
  snapshot.go                                 per-organization snapshot keyed by entitlement_version; rebuilt on
                                              infraweave_invalidate through driven.Notifications
  billing.go                                  the installation's billing switch; off: every organization is Enterprise
  service.go                                  implements driving.Entitlements and driving.Operator
internal/core/port/driving/features.go        Entitlements: For(organization) Snapshot; Snapshot: Has, Check,
                                              Limit, Reserve; Operator: Tiers, Organizations, Grants, Billing
internal/adapter/driving/http/middleware/features.go
                                              after authentication: the operation's x-infraweave-feature,
                                              read from the generated route table, through driving.Entitlements
internal/adapter/driving/http/operator/        implements gen/operator; operator principals only, else 401
internal/adapter/driving/scheduler/consume.go  a job of a feature outside the entitlement completes as skipped
internal/adapter/driven/postgres/features/     tiers, tier_features, tier_limits, organization_tiers, grants,
                                              account_limits
```

The catalogue is one core package that every other core domain reaches
through `driving.Entitlements`, as it reaches `driving.Authorizer`, so the
three gates of decision 0021 sit where each one's information lives:

| Gate | Where | Answer |
| --- | --- | --- |
| Capability | `http/middleware/features.go`, once per request after authentication | `403 feature-disabled` when the operator switched it off, else `403 feature-not-in-tier` naming the tiers that hold it |
| Option | the core service's input validation (`core/stack` for `StackVendor`, `core/vcs` for aggregated checks and multiple integrations), which calls `Snapshot.Check` only when a write sets the gated value | the same two problems |
| Limit | the core service that creates the counted thing: `core/account` for seats, `core/worker` for private workers, `core/run` for public worker slots (a run beyond the limit waits in the queue), `core/intel` for the AI budget | `403 limit-reached` with the limit and its value |

`billing` is off by default: `Snapshot` then answers yes for every feature
not in `features.disabled` and no limit applies, so an installation behaves
as Enterprise without a check. Tiers, organization tiers, grants and the
billing switch are data written only through the operator API
(`api/operator.openapi.yaml`, `infraweavectl operator`), which also
creates organizations, each with its first account; organization admins
set account limits below the pool through the served `Organization` tag.
Each write bumps `organization_entitlements.entitlement_version` and notifies
`infraweave_invalidate` in its
unit of work, and every replica rebuilds the snapshot it holds, so a change
needs no restart. Downgrades delete nothing: the scheduler's job consumer is
a driving adapter, so it asks the snapshot before handing a job to the core
and completes a gated job as skipped, and schedules of a missing feature do
not fire until the account's organization regains it. The hexagon gives the gate one home
because every domain shares one core; the cost is that
`core/port/driving/features.go` is another port every domain's fakes must
cover.

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

Deploy and Flows share this one event model (decision 0015): the envelope,
the emit path through `driven.Outbox` and the kind registry in `core/event`
(`run_updated`, `drift_detected`, `policy_evaluated`,
`notification_created`, `module_version`, `internal_error`,
`worker_usage`, `worker_updated`, `flows.event`), each kind with a JSON
Schema that types webhook payloads, stream messages, Flows trigger outputs
and the expressions that read them. Flows trigger blocks are one more
fan-out target: `core/flows/blocks/deploy.go` is a `driving.Subscriber`
whose `Handle` inserts the root `flow_events` row and its `flows.deliver`
jobs in the fan-out's own unit of work, so no bridge sits between the two.
Every string a notification policy returns is a text template that
`core/event/delivery.go` renders with `internal/shared/expr` in the delivery
job, against the event document.

The server-sent event streams of decision 0015 (run logs, run state,
worker pool queue, Flows events) are driving HTTP handlers in
`http/deploy/runs`, `http/deploy/workers` and `flowsapi`, written through
`http/sse`. Each authorizes as its polling equivalent and reads through
`driving.EventFeed`, which `core/event` implements over `driven.Notifications`
(`LISTEN` on `infraweave_events` and `infraweave_logs` in
`adapter/driven/postgres/notify`), so the driving adapter never reaches a
driven one (rule 5). Every stream has a polling equivalent.

### Flows engine

```text
api/proto/flows/app/v1/                       (Flows) app.proto (App), host.proto (Host); buf breaking in CI
pkg/flows/appsdk/                             (Flows) Serve(app), typed blocks, KV, timers, widgets; apptest/
internal/core/port/driving/flows.go           Projects, Flows, Blocks, Apps, Installations,
                                              AgentPools, Invocations, Secrets, NamedExpressions, DataTables;
                                              FlowTasks: Claim(pool, n), Complete(task, result), Host(call)
internal/core/port/driven/agenttasks.go       AgentTasks: Submit(pool, Task) inside the Tx
internal/core/flows/
  project.go membership.go                    (Flows) the account's projects, UUIDs, roles; the account
                                              admin takes Spacelift Flows' organization admin's place
  flow.go block.go link.go definition.go      (Flows) definition YAML with !expr values, strict import and export
  app.go installation.go registry.go          (Flows) app versions as OCI image digests with captured
                                              Describe manifests; hourly registry subscriptions
  agentpool.go                                (Flows) pools, hashed tokens, project exposure
  engine/
    deliver.go                                flows.deliver: evaluate input configuration with shared/expr
                                              under the stored now and seed, run the block
    effects.go                                buffered events, pending events, timers and messages committed
                                              with the delivery's completion; KV runs live
    timers.go lifecycle.go status.go          flows.timer, flows.lifecycle keyed by entity,
                                              flows.invocation.status keyed by invocation
    limits.go retention.go                    monthly and hourly counters (UTC); 60-day retention
  blocks/
    core.go                                   (Flows) every core block in Go; Transform with the steps form
    deploy.go                                 (Flows) Deploy triggers as a driving.Subscriber; Trigger Run,
                                              Confirm Run and API Request through driving ports
internal/adapter/driving/scheduler/consume.go  the flows logical queue with its own worker count
internal/adapter/driving/gateway/flows.go      wss://<host>/flows/agent/v1/connect; claims flows.agent_task for
                                              its sockets up to their capacity; task, call, reply, result frames
internal/adapter/driving/http/flowsapi/        /flows/v1/ over gen/flows; HTTP Endpoint blocks, forms, MCP
internal/adapter/driven/queue/                 driven.AgentTasks: flows.agent_task partitioned by pool
internal/adapter/driven/postgres/flows/        the flows schema and its tables
internal/worker/core/flowsagent/
  agent.go                                    `flows-agent`: dial, hello, ping every 20 s, capacity
  tasks.go                                    a task frame to an App RPC on the right container; result frame
  host.go                                     Host calls (KV, locks, ListBlocks, SetWidget) relayed as call frames
internal/worker/adapter/appcontainers/         docker (optional gVisor), kubernetes, process (development)
internal/worker/adapter/appgrpc/               App client and Host server over a Unix socket or pod mTLS
```

The engine is core code on the same driven queue as runs: a block delivery
is a `flows.deliver` job on the `flows` logical queue, consumed in the
`scheduler` process mode, so a busy flow never delays Deploy's fan-out and
no second scheduler, lease model or retry policy exists (decision 0018).
A core block runs in Go in the same job. An app block is a call on
`driven.AgentTasks`, which enqueues a `flows.agent_task` job for the pool;
the `serve` replica holding an agent's WebSocket claims it through
`driving.FlowTasks.Claim` and pushes a `task` frame, and the agent's
`result` frame returns the app's buffered effects, which
`FlowTasks.Complete` commits in one unit of work with the delivery's
completion, so a retried delivery never emits twice. Waits (Sleep, Show
Form, pending events, app timers) are rows the scheduler's ticker turns into
`flows.timer` jobs.

Apps are infrastructure code: each is an OCI image serving the versioned
`App` gRPC service of `api/proto/flows/app/v1/`, run as its own container,
one per installation and image digest, by `flows-agent` in the worker
hexagon. The agent serves the `Host` service to its apps and relays the live
calls to the server as frames; gRPC never leaves the agent's host. An app
receives evaluated configuration and only the secrets it resolved to; it
never sees an expression, because `engine/deliver.go` evaluates every input
in the server with `internal/shared/expr`. Apps contribute declarative
widgets only, which the console renders; no app ships browser code. The Go
app SDK, `pkg/flows/appsdk`, is public so app authors build on it, and
first-party apps are ordinary programs on it.

Flows' parts are features of the catalogue (`flows-engine`, `flows-apps`,
`flows-agents`, `flows-data-tables`, `flows-forms`, `flows-mcp-tools`),
gated by the same middleware as Deploy; there is no Flows switch of its
own. Flows belongs to the account, in place of Spacelift Flows'
organization, and shares its members, so `core/flows` reaches identity
through `driving.Authorizer` like any domain. In a single
hexagon Flows is three core packages and a handful of adapters, separated
from Deploy only by rule 3: it reaches Deploy through `driving.Runs`,
`driving.Stacks` and `driving.Subscriber`, and Deploy never reaches it. The
shared core is what makes Deploy events native Flows triggers; it is also
why Flows shares the schema stream and `services.go`.

### Expressions

```text
internal/shared/expr/                         (P9) the language and library of decision 0024; no I/O
  env.go                                      cel-go environment per surface: declared variables and types
  library.go                                  strings, lists, maps, regex, encoding, maths, time, random, fail
  typeprovider.go                             JSON Schema to CEL object types; additionalProperties to dyn
  template.go                                 ${{ }} text templates, $${{ escape, value rendering rules
  shell.go                                    POSIX quoting for workflow command templates
  eval.go                                     cost limit, timeout, source and result caps; now and seed in
                                              the context; programs cached by source, surface and types
internal/core/port/driving/expressions.go     Expressions: Check, Complete, Evaluate; Surface: TypedContext
internal/core/expression/                     (P9) service over the Surface each domain registers
internal/adapter/driving/http/deploy/expressions/   /expressions/check, complete, evaluate
internal/adapter/driving/http/deploy/policies/check.go   /policies/check: Rego diagnostics
```

| Surface | Evaluated by | Process mode |
| --- | --- | --- |
| Flows input, static and output configuration, Transform | `core/flows/engine` | `scheduler` (static configuration also at save in `serve`) |
| Blueprint and template bodies | `core/template` | `serve` for a synchronous create, `scheduler` for a queued deployment change |
| Output references, environment interpolation | `core/run` handover | `serve`, at handover |
| Notification text | `core/event` delivery | `scheduler` |
| `.infraweave/workflow.yml` command templates | `worker/core/runner` | the runner, at initialization, shell-quoted |

The language is one pure library, so every surface evaluates the same
functions under the same limits (`expressions.cost_limit`,
`expressions.timeout`) with the same deterministic `now` and seed. Typed
contexts belong to the domains that own the data: each domain implements
`driving.Surface` (the block's output and signal schemas from
`core/flows`, event kind schemas from `core/event`, template inputs from
`core/template`, run variables from `core/config`), and `core/expression`
reaches each through that driving port, so the editor endpoints answer for
any surface without importing a domain. Secrets resolve in the server; only
literal `secrets.KEY` names load. The runner is the one consumer outside the
server hexagon, and it sees only the fixed run parameters, never a secret.
The cost is structural: the worker hexagon imports a package it does not
own, and rule 10 keeps `shared/expr` free of anything either hexagon would
not accept.

### Console and clients

```text
pkg/client/<spec>/                            generated by oapi-codegen from each served bundle
pkg/sdk/                                      (P24) one client over every spec: API key to token exchange,
                                              Idempotency-Key, 429 and Retry-After, iterators, typed problems,
                                              typed stream iterators
cmd/infraweavectl/                            (P24b) cobra and viper over pkg/sdk only; operator, flows, expr, mcp
web/console/                                  (P24c) React 19, TypeScript strict, Vite, TanStack Router and
                                              Query, orval client from the served bundle, Radix, Tailwind 4,
                                              React Flow, CodeMirror 6; console.go embeds dist/
internal/adapter/driving/http/console/        (P24c) serves the embedded files at /
```

Every client, the console included, calls only operations of the served
OpenAPI bundles; no endpoint exists for one client alone. The SDK wraps the
generated clients rather than replacing them, `infraweavectl` and the
provider (in `weaveplatform/terraform-provider-infraweave`) call the API
only through it, and the console's TypeScript client is generated from the
same bundle. The console is a driving adapter's static files:
`http/console` answers `/` from the `embed.FS` that `web/console/console.go`
declares, falls back to `index.html` for deep links except under `/api/`,
`/mcp`, `/.well-known/` and the protocol paths, sends the strict content
security policy, and serves a placeholder page when the binary was built
without the console. The browser holds the session cookie of decision
0006, never a token, and the SSE streams update its cache.

`allowedActions` is computed where actions are enforced: each core service
fills the `AllowedActions` field of the DTO it returns through its driving
port from the caller's `Decider` and, for a resource with a state machine,
the transitions `core/run/machine.go` allows from its current state. The
HTTP adapter copies the field into the response, and the console shows,
hides or disables controls from it alone. Because every service is in one
core and every DTO is in `types.go`, the field is one addition per DTO; the
cost is that `types.go` grows again.

### Composition root

```text
internal/app/
  app.go                                      Open(ctx, cfg): shared driven adapters; Close
  serve.go                                    the serve process mode: router with every driving HTTP adapter
  scheduler.go                                (P7) the scheduler process mode: job handlers, cron, the ticker
  launcher.go                                 (P8) the launcher process mode: internal/worker only
  agents.go                                   (P19, Flows) vcs-agent and flows-agent: internal/worker only
  agentcli/                                   cobra tree for cmd/infraweave-agent: launcher, runner, vcs-agent,
                                              flows-agent; imports internal/worker and internal/shared only
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

// internal/app/launcher.go (P8): the launcher process mode never calls buildServices
func Launcher(ctx context.Context, cfg config.Worker) error {
	cp := controlplane.New(cfg.ServerURL, cfg.PoolToken)
	return launcher.New(cp, docker.New(cfg.Docker), workspace.New(), binaries.New(cfg.Cache)).Run(ctx)
}
```

Process modes are functions over the same constructors. `serve` builds every
service and every HTTP driving adapter; `scheduler` builds every service and
the queue handlers, cron and the event fan-out; `all` does both in one process
for single-node installs and the acceptance suite; `launcher`, `vcs-agent`
and `flows-agent` build the worker hexagon only, which is why
`cmd/infraweave-agent` can link `internal/app/agentcli` and nothing of the
server. Spacelift ships the same split, a server and a drain
from one binary
([report 24](../reference/24-self-hosted-reference.md#21-process-inventory)),
and the hexagon expresses it without a structural change: a process mode is a choice
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
  src/operator/*.yaml, src/flows/*.yaml       (P3b, Flows) the operator and Flows APIs, servers and clients alike
  proto/flows/app/v1/*.proto                  (Flows) buf generate: pkg/flows/appsdk/appv1
web/console/src/api/                          (P24c) orval client and Query hooks from the bundles, committed
internal/tooling/specref/                     reference generation, scaffold, inventory (report 29)
internal/tooling/stubgen/                     UnimplementedStrict for each generated strict server
```

| Spec | Generated server | Implemented by | Client |
| --- | --- | --- | --- |
| deploy | `adapter/driving/http/gen/deploy` | `adapter/driving/http/deploy/<family>`, composed in `deploy/server.go` | `pkg/client/deploy` (public: the provider and `infraweavectl` import it) |
| worker (P8) | `adapter/driving/http/gen/worker` | `adapter/driving/http/workerapi` | `internal/worker/adapter/controlplane/client` (internal: both ends ship in one binary and version together) |
| operator (P3b) | `adapter/driving/http/gen/operator` | `adapter/driving/http/operator` | `pkg/client/operator` |
| flows (Flows) | `adapter/driving/http/gen/flows` | `adapter/driving/http/flowsapi` | `pkg/client/flows` |
| `App` protobuf (Flows) | none: apps serve it | `pkg/flows/appsdk` for Go apps | `worker/adapter/appgrpc` |
| none: hand-written | none | `webhook`, `statebackend`, `registryproto`, flow HTTP endpoints, `mcp` | none |

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
diffs that directory instead of `internal/server/gen`, plus the App stubs
and the console's generated client. `pkg/sdk` is hand-written over every
`pkg/client/<spec>`, so one SDK client spans Deploy, Flows and the operator
API.

### Tests and enforcement

| Kind | Location |
| --- | --- |
| Unit tests | beside the code in every core, adapter and worker package |
| Core service tests | `core/<domain>/*_test.go`, with fakes of driven ports and of the driving ports of other domains |
| State machine tests | `core/run/machine_test.go`: every pair of report 08's states, allowed or forbidden |
| Driven adapter contract runs | each adapter's tests call `contract/<port>test.Run`; PostgreSQL through testcontainers; `objecttest` against RustFS, fake-gcs-server, Azurite and local disk, blocking |
| Shared libraries | `shared/expr`: the function library, rendering and quoting as tables; `shared/seal`: known-answer vectors per format version |
| FIPS | a blocking CI job runs the suite with `GODEBUG=fips140=only` on a `GOFIPS140=certified` build (decision 0013) |
| Flows apps | `pkg/flows/appsdk/apptest` runs a fixture app; `buf breaking` against the last release blocks |
| Console | `tsc --strict`, ESLint strict, Vitest at 95% total and 90% per directory, Playwright against `infraweave all` |
| Handler tests | `adapter/driving/http/...`, with fakes of driving ports |
| Worker tests | `worker/core/...` with fake ports; `worker/adapter/docker` against a Docker daemon in CI |
| Acceptance | `test/acceptance`: godog features against `infraweave all` and a real worker (decision 0016) |

The ten rules are depguard lists in `.golangci.yml`. depguard matches
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
          files: ["**/internal/worker/**", "**/internal/app/launcher.go", "**/internal/app/agentcli/**"]
          deny:
            - pkg: github.com/weaveplatform/infraweave/internal/core
            - pkg: github.com/weaveplatform/infraweave/internal/adapter
            - pkg: github.com/jackc/pgx
            - pkg: github.com/aws/aws-sdk-go-v2/service/s3
        shared:                                 # rule 10
          list-mode: lax
          files: ["**/internal/shared/**"]
          deny:
            - pkg: github.com/weaveplatform/infraweave/internal/
          allow:
            - github.com/weaveplatform/infraweave/internal/shared
```

Rule 3's list is written once per core domain, because a deny entry is a
prefix and the allowed own-domain prefix differs per domain: about six
lists at P6 and twenty-three at P27. A domain added without its list is not
an error, only unguarded, so the review checklist names the list. The
`.golangci.yml` setting `new: true` with `whole-files: true` reports a
forbidden import in any file a change touches, which covers every new import.

Coverage implications:

- About 25 packages at P1, about 200 at P27.
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
5. Scheduler process mode, `internal/adapter/driving/scheduler/consume.go`: claims the job and calls `driving.Webhooks.Process`.
6. `internal/core/vcs/normalize.go` with `vcs/github/parse.go`: the pull request event becomes one `PullRequestEvent`; `route.go` finds the stacks bound to the repository; `dedupe.go` drops a second event for the same SHA and run type.
7. `internal/core/vcs/process.go`: calls `driving.PolicyDecisions.Push`, implemented by `internal/core/policy/decide.go`, which builds the push input (`policy/input/push.go`) and evaluates attached push policies through `driven.PolicyEngine` (`internal/adapter/driven/opa/engine.go`); the result is `propose`.
8. `process.go`: calls `driving.Runs.Trigger` with type `PROPOSED`; `internal/core/run/trigger.go` inserts the run in `QUEUED` (T1), appends `RunStateChanged` to the outbox and enqueues `run.schedule`, all in one unit of work.
9. Scheduler process mode: `run.schedule` runs `core/run/gates.go`; a proposed run takes no stack lock, so it moves to `READY` (T2).
10. Serve process mode, `internal/adapter/driving/http/workerapi/poll.go`: a public-pool worker's held long-poll wakes and calls `driving.RunProgress.Claim`; `postgres/runs/claim.go` picks the run by tier and age; `core/run/progress.go` moves it to `PREPARING` (T7) and returns the job.
11. Launcher process mode, `internal/worker/core/launcher/launcher.go`: starts the runner container through `worker/adapter/docker`; inside it, `worker/core/runner/phases.go` runs `INITIALIZING` and `PLANNING` with `worker/core/iac/opentofu`, reporting each transition through `worker/adapter/controlplane` (T10, T12).
12. `runner/phases.go`: uploads the plan JSON through a presigned URL and reports the plan; `workerapi/server.go` calls `driving.RunProgress.Report`.
13. `internal/core/run/progress.go`: asks `driving.Vendors.PlanInput` (`internal/core/iac/opentofu`) for the plan policy input, then `driving.PolicyDecisions.Plan`; `core/policy/decide.go` evaluates the plan policies, combines warnings and stores receipts.
14. `core/run/progress.go`: no warning and a proposed run, so `PLANNING` to `FINISHED` (T17) in one unit of work with the receipts and the `RunStateChanged` outbox event.
15. Scheduler process mode, `internal/adapter/driving/outbox/fanout.go`: hands the event to subscribers; `internal/core/vcs/feedback.go` sets the commit check, and `internal/core/event/route.go` asks `driving.PolicyDecisions.Notification` for routes.
16. A notification policy `pull_request` rule returns a comment route with a deduplication key; `core/vcs/feedback.go` calls `driven.VCSProvider.Comment` (`vcs/github/comments.go`), which creates or edits the PR comment ([report 11](../reference/11-vcs-integrations.md#123-pull-request-comments)).

### A tracked run awaits approval and applies on a private worker

1. A push to the tracked branch follows steps 1 to 7 above with the push decision `track`; `core/run/trigger.go` creates a `TRACKED` run on a stack whose worker pool is private.
2. `core/run/gates.go`: the stack has an approval policy; `driving.PolicyDecisions.Approval` evaluates it on entering `QUEUED` and approves, and the stack lock is free, so `READY` (T2).
3. Serve process mode, `workerapi/poll.go`: the private pool's worker, authenticated by its pool credentials (`workerapi/auth.go`), claims the run; `READY` to `PREPARING` (T7).
4. Launcher process mode on the customer's host: `worker/core/launcher` starts the container; `runner` plans with the OpenTofu command set; the state backend configuration it writes points at `adapter/driving/http/statebackend`, which accepts the run-scoped token because `core/state/access.go` sees a run in `PLANNING`.
5. `core/run/progress.go`: the plan has changes and autodeploy is off, so `PLANNING` to `UNCONFIRMED` (T20); the worker uploads the encrypted workspace to the workspace bucket and leaves the run.
6. `driving.PolicyDecisions.Approval` runs again on `UNCONFIRMED`; it requires two approvals, so `Run.canConfirm` is false.
7. Two reviewers call `POST /runs/.../review`; `adapter/driving/http/deploy/runs` calls `driving.Runs.Review`; `core/run/actions.go` stores each review and re-evaluates approval; the second approves.
8. A writer calls confirm; `core/run/actions.go` checks `canConfirm` and moves `UNCONFIRMED` to `CONFIRMED` (T26) with an outbox event.
9. The private worker's next poll claims the confirmed run; `CONFIRMED` to `PREPARING_APPLY` (T33); the launcher starts a fresh container and the runner restores the workspace through a presigned URL.
10. `runner/phases.go`: apply; the state backend accepts writes because the run is in `APPLYING`; `PREPARING_APPLY` to `APPLYING` to `FINISHED` (T35, T37).
11. `core/run/service.go`: the terminal transition of a blocking run enqueues `run.schedule` for the stack, so the next queued tracked run can become `READY`; the event fan-out delivers `RunStateChanged` to `core/vcs/feedback.go` (GitHub deployment marked active) and `core/event` (webhooks, and the audit webhook when it includes runs).

### A drift event starts a flow

1. Scheduler process mode, `internal/adapter/driving/scheduler/cron.go`: the ticker claims a due drift detection schedule and calls `driving.Drift.Run`; `internal/core/schedule` checks `drift-detection` through `driving.Entitlements` (billing is off, so every feature is in) and calls `driving.Runs.Trigger` for a proposed drift detection run.
2. `core/run/trigger.go` and `gates.go`: `QUEUED`, then `READY` on the stack's private pool; the pool's worker claims it in `workerapi/poll.go` and `launcher` opens the sealed spec with `shared/seal`.
3. `worker/core/runner/phases.go`: plans with the OpenTofu command set and reports changes; `core/run/progress.go` finishes the run and, in the same unit of work, appends `run_updated` and `drift_detected` through `driven.Outbox` (`postgres/outbox`), which enqueues one `events.fanout` job per event.
4. Scheduler process mode, `adapter/driving/outbox/fanout.go`: hands `drift_detected` to every `driving.Subscriber`; `core/event` finds no notification route, and `core/flows/blocks/deploy.go` finds an enabled Deploy trigger block of kind `drift_detected` whose filter matches the stack's labels.
5. `blocks/deploy.go`: `flows-engine` is in the entitlement, so in the fan-out's unit of work it inserts the root `flow_events` row, linked to the Deploy event by ID, and one `flows.deliver` job per linked input, keyed `(event, block, input)`; the event's `now` and seed are fixed and stored with it.
6. Scheduler process mode, `flows` queue: `core/flows/engine/deliver.go` evaluates the next block's input configuration with `shared/expr`, for example the text template `Drift on ${{ outputs.driftTrigger.stack.name }}`, typed against the `drift_detected` schema, under the stored `now` and seed.
7. The block is an app block, so `deliver.go` calls `driven.AgentTasks.Submit`; `adapter/driven/queue` enqueues `flows.agent_task` for the project's agent pool.
8. Serve process mode, `adapter/driving/gateway/flows.go`: the replica holding that pool's agent socket claims the job through `driving.FlowTasks.Claim` and pushes a `task` frame carrying the `HandleEvent` request in its JSON encoding.
9. Flows agent, `worker/core/flowsagent/tasks.go`: reuses the container for that installation and image digest, or starts one through `worker/adapter/appcontainers/docker`, and calls `HandleEvent` over gRPC through `worker/adapter/appgrpc`; a KV read the app makes is a `Host` call relayed as a `call` frame.
10. The app returns an emitted event as a buffered effect; the agent sends a `result` frame; `gateway/flows.go` calls `driving.FlowTasks.Complete`, and `engine/effects.go` commits the new `flow_events` row, its deliveries and the delivery's completion in one unit of work.
11. `engine/status.go` (`flows.invocation.status`) recomputes the invocation; `core/event` wakes `driving.EventFeed`, and the Flows events stream in `flowsapi` sends `flows.event` and `invocation_updated` to the console through `http/sse`.

One event model carries the change from the run transition to the flow: the
Deploy event and the root Flows event share decision 0015's envelope and
emit path, and the Flows trigger is a subscriber in the same core, not a
bridge.

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
| `cmd/` (`cmd/README.md`) | `cmd/infraweave/main.go`, `cmd/README.md` | `main.go` builds the cobra root from `internal/app/cli`; `cmd/infraweavectl` arrives with P24b and `cmd/infraweave-agent` with the slim build |
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
| `pkg/client/deploy` | `pkg/client/deploy` | unchanged; `pkg/sdk` wraps it from P24 |
| `tools/openapi-bundle` | `tools/openapi-bundle` | imports `internal/tooling/openapibundle` |
| `tools/specref` | `tools/specref` | imports `internal/tooling/specref` |
| `tools/stubgen` | `tools/stubgen` | imports `internal/tooling/stubgen` |
| `test/acceptance` | `test/acceptance` | gains a feature per domain and a harness that starts `infraweave all` and a worker |
| `api/oapi-server-deploy.yaml` | same file | `output: internal/adapter/driving/http/gen/deploy/server.gen.go` |
| `Makefile` | same file | stubgen and `verify-generate` read `internal/adapter/driving/http/gen`; `docs-check` runs `./internal/tooling/docscheck` |
| `.golangci.yml` | same file | depguard enabled with the ten rules; per-domain lists for rule 3 |
| `.testcoverage.yml` | same file | `^internal/server/gen/` becomes `^internal/adapter/driving/http/gen/`; adds the internal worker client |

## Numbers

Estimates, rounded.

| Measure | Today (P1) | End of P27 |
| --- | --- | --- |
| Go packages | about 25 (13 today plus `cmd/infraweave`, `app`, `app/cli`, `app/config`, `core/kernel`, two port packages, `core/stack`, `http`, `http/middleware`, `http/deploy`, `http/deploy/stacks`) | about 200 |
| Top-level directories under `internal/` | 6 (`app`, `buildinfo`, `core`, `adapter`, `tooling`, plus `contract` from P2) | 8 (`shared` from P2, `worker` from P8) |
| Dependency rules | 10 | 10 |
| depguard lists and entries | 10 lists, about 38 entries | about 33 lists (23 for rule 3), about 145 entries |
| Files touched to add a vendor | about 10 new, 4 edited | same |
| Files touched to add a domain | about 20 new, 6 edited | same, with the shared files longer each time |
| Packages a stack update crosses | 6 hand-written: `http/middleware`, `http/deploy/stacks`, `core/port/driving`, `core/stack`, `core/port/driven`, `postgres/stacks`; plus `gen/deploy` | same |
| Largest package | `adapter/driving/http/gen/deploy`, about 24k generated lines | `gen/deploy`, about 240k generated lines at full scope (725 reference operations); hand-written: `core/run`, about 10k lines |
| Thin packages at risk under the 90% floor | `app/cli`, `core/port/driving` DTO helpers | about 14: `app`, `app/cli`, `app/agentcli`, the port packages, `gateway`, `http/console`, `worker/adapter/kubernetes`, `appcontainers`, `cloud/*`, `idp/*`, `ai/*` |

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
- **The worker side is a second hexagon, not an adapter of the first.** Its
  logic (the launcher loop, phases, vendor command sets) is business logic
  of its own, and its isolation from server code is a security property,
  not a convenience.
- **Claiming runs is a transition in the serve process mode.** The long-poll
  request is where the worker waits, so the claim happens there; the
  scheduler process mode owns everything with no client waiting.
- **The worker protocol client is internal.** Both ends ship in one binary;
  the public packages are the API clients, the SDK and the Flows app SDK.
- **IaC vendor code lives under `iac/`, not `vendor/`.** The go command
  treats any directory named `vendor` as vendored dependencies: `go list
  ./...` skips it and an import of a path through it fails, so
  `core/iac/<name>` and `worker/core/iac/<name>` hold the vendor packages.
- **Two pure libraries sit outside both hexagons.** `shared/expr` and
  `shared/seal` hold code the server and the worker side must run
  identically: the expression language the runner renders workflow
  templates with, and the sealed formats the launcher opens and the runner
  writes. Rule 10 keeps them free of ports and domain types.
- **The feature gate is core, its checks are adapters and services.** The
  catalogue and entitlement are one core package; the capability check is
  HTTP middleware, the job skip is the scheduler's driving adapter, and each
  limit is checked by the core service that creates the counted thing.
- **Flows apps run in the worker hexagon.** `flows-agent` sits beside the
  launcher because both run on customer hosts and dial out; the engine and
  every expression stay in the server's core.
- **The console is a driving adapter's static files.** It reaches the core
  only through the public API, like every other client.
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
- **Flows shares the core and the account.** Flows projects and their
  roles are a second access model inside the same core, within the
  account that owns them, separated from spaces only by rule 3;
  extracting Flows into its own service later means splitting the schema
  stream and `services.go`, not just moving a directory. The same sharing
  makes Deploy events native Flows triggers.
- **The worker hexagon is not self-contained.** `shared/expr` and
  `shared/seal` are imported by both hexagons; a change to either is a
  change to both sides, released together in one binary but versioned
  against launchers that customers upgrade later.
- **Cross-cutting ports multiply.** `driving.Entitlements`,
  `driving.Surface` and the `AllowedActions` field reach every domain, so
  every domain's fakes and DTOs grow with them, and `types.go` with them.
- **The composition root concentrates the system.** `internal/app` is the
  only package that sees everything; at P27 it is the largest hand-written
  coordination point and the first place a merge conflict appears.
