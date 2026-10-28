# Option 3: Clean architecture, detailed layout

Detailed repository layout for option 3 of the
[architecture options](README.md): four concentric rings (entities, use
cases, adapters, frameworks) with every dependency pointing inward, and a
second axis that keeps the worker role's package graph apart from the
server's. Every package in today's tree is mapped; roadmap packages are
marked with the phase of
[report 26](../reference/26-scope-and-tiers.md#14-provisional-phase-spine)
that creates them.

Conventions in the trees:

- `← internal/server/problem` names where the code lives today.
- `(P7)` marks something a later phase creates.
- A line with neither is new structure for code that exists today or for
  P1, the phase in progress.

## Summary

The tree is organised by ring first and by subject second. Ring 1,
`internal/entity`, holds infraweave's enterprise rules as pure Go with no
I/O: the run state machine with its 22 `RunState` values and the
transition table of [report 08](../reference/08-run-lifecycle.md#3-transitions),
the seven priority tiers, policy combination semantics per type, space
reachability and inheritance, module and provider version numbering, the
per-vendor command sets and change models, and the Flows invocation status
rules. Ring 2, `internal/usecase`, holds one package per operation family.
Each package is an interactor with input and output boundary structs and a
`port.go` that declares, from the consumer's side, every interface it needs
(stores, object storage, the queue, policy decisions, VCS calls). Ring 3,
`internal/adapter`, converts between the use cases and the outside world:
controllers implement the generated strict server and the hand-written
protocols, presenters render boundary structs to REST bodies and RFC 9457
problems, and gateways implement the ports over PostgreSQL, object storage,
OPA, VCS provider APIs and the queue. Ring 4, `internal/infrastructure`, is
frameworks and drivers: the chi router and `http.Server` lifecycle, the pgx
pool and migrator, strict configuration, telemetry. `internal/app` is the
composition root and builds the `serve`, `scheduler`, `worker` and `all`
roles of the single `infraweave` binary.

Rings say how code depends; they say nothing about which side of the wire it
runs on. A private worker runs on a customer host with outbound HTTPS only
([report 10](../reference/10-workers-and-execution.md#42-transports)), so
this option adds one orthogonal cut: worker-side code lives under
`usecase/agent/`, `adapter/agent/` and `app/workerapp/` in every ring it
touches, and that subtree may import only entities, itself and a short
allow-list. The rings keep the server honest about frameworks; the cut keeps
the worker honest about the database.

The organising question for a contributor is "which ring", not "which
domain". Stack rules and policy rules both sit in ring 2, and nothing in the
tree groups the five packages that make up the run lifecycle. The rings are
strict about direction and silent about cohesion.

Dependency rules:

1. `internal/entity/...` imports only the standard library, minus I/O
   packages (`net/http`, `database/sql`, `os`, `os/exec`, `io/fs`), and the
   small pure libraries the rule allow-lists (ULID, semver). It imports no
   other ring. The cron grammar of decision 0011 is infraweave's own code,
   so it is an entity, not a library.
2. `internal/usecase/<family>/...` imports `internal/entity/...`, its own
   package tree, and the four shared contract packages
   `internal/usecase/unitofwork`, `internal/usecase/event`,
   `internal/usecase/job` and `internal/usecase/runstate`. One use case never
   imports another; composition goes through ports that `app` fills.
3. `internal/adapter/...` imports `internal/entity` and `internal/usecase`
   (to call interactors and implement ports) and third-party client
   libraries; it never imports `internal/infrastructure` or `internal/app`.
   An adapter imports another adapter only inside the same family
   (`adapter/rest/...` among themselves, `adapter/postgres/...` among
   themselves).
4. `internal/infrastructure/...` imports adapters, use cases and entities;
   it never imports `internal/app`.
5. Only `internal/app/...` and `cmd/...` import every ring.
6. Worker side: `internal/usecase/agent/...`, `internal/adapter/agent/...`
   and `internal/app/workerapp` import only `internal/entity/...`, the
   worker-side subtree itself, `internal/adapter/opa` and an allow-list of
   standard and container libraries. They never import a server use case,
   `adapter/postgres`, `adapter/objectstore` credentials code or
   `infrastructure/postgres`.
7. `pkg/...` imports nothing under `internal/` or `cmd/`.
8. `internal/devtool/...` imports no ring, and no ring imports
   `internal/devtool`.

One exception is written down rather than hidden: the generated strict
server lives in ring 3 (`adapter/rest/gen/deploy`), not ring 4, so that
controllers implementing it do not import outward.

## Repository root

```text
infraweave/
├── cmd/
│   ├── infraweave/             NEW (P1): cobra root; serve, scheduler, all, worker, hidden worker exec,
│   │                           vcs-agent, flows-agent, migrate up and status, version;
│   │                           hands off to internal/app or app/workerapp
│   └── infraweavectl/          (P24) CLI over pkg/client/deploy, not a server role
├── internal/
│   ├── entity/                 NEW ring 1: pure rules, no I/O
│   ├── usecase/                NEW ring 2: one interactor package per operation family; agent/ is worker side
│   ├── adapter/                NEW ring 3: controllers, presenters, gateways; agent/ is worker side;
│   │                           absorbs internal/server/problem and internal/server/gen/deploy
│   ├── infrastructure/         NEW ring 4: chi assembly, http.Server, pgx, config, telemetry, buildinfo
│   ├── app/                    NEW composition root: one constructor per role; workerapp/ for the worker
│   └── devtool/                specref, openapibundle, stubgen, docscheck: maintainer tooling, outside the rings
├── pkg/
│   └── client/
│       ├── deploy/             unchanged: generated client of the served API
│       └── flows/              (P26) generated client of the Flows API
├── api/                        unchanged layout; oapi-server-*.yaml outputs move into internal/adapter
│   └── src/deploy/ src/worker/ (P8) src/flows/ (P26)
├── test/
│   └── acceptance/             unchanged; features grow per phase
├── tools/                      openapi-bundle, specref, stubgen keep their roles and import internal/devtool;
│                               depguardgen (P1) writes the per-use-case depguard rules
├── docs/                       research, decisions; the implementation plan (IMP-01) describes the rings
├── deploy/                     (P27) Helm chart, Terraform modules, container image definitions
├── Makefile                    generate writes to internal/adapter/.../gen; adds verify-lint-config
├── .golangci.yml               enables depguard: ring rules, the worker-side rule, one rule per use case
├── .testcoverage.yml           excludes the moved gen paths
└── go.mod                      one module, unchanged path
```

What changes at the root and why:

- `cmd/infraweave` appears because P1 needs a served binary; it stays a
  thin cobra shim, as `cmd/README.md` already requires, and calls
  `internal/app`.
- `internal/server/` disappears as a name. Its two packages are adapter
  code: problem details are a presenter concern, and the generated strict
  server is the controller contract.
- `internal/devtool/` gathers the four maintainer libraries. They are not
  part of any ring, and grouping them makes rule 8 one depguard entry.
- `pkg/` stays as small as today. Entities are the inside of the system;
  publishing them would let other repositories depend on ring 1 and freeze
  it. The worker protocol client stays internal (see Generated code).
- `tools/depguardgen` exists because rule 2 differs per use case and
  depguard has no "own package" variable; the generated rules are checked
  in and verified by the gate.

## Full package tree

Ring status today and at the end of the roadmap:

| Ring or subtree | Today (P1) | Grows in |
| --- | --- | --- |
| `entity` | absent; first packages are `id` and `problem` slugs only if needed | P2 to P26, every domain phase |
| `usecase` (server) | `system` only (version, debug info) | P3 to P26 |
| `usecase/agent` (worker side) | absent | P8, P9, P14, P20 |
| `adapter/rest` | `problem`, `gen/deploy`, the strict-server aggregate | every phase with REST operations |
| `adapter/protocol` | absent | P11 state, P12 webhooks, P17 registry |
| `adapter/postgres`, `adapter/objectstore` | absent | P2 |
| `adapter/opa`, `adapter/vcs`, `adapter/keyring` | absent | P13, P12, P2 and P15 |
| `adapter/agent` | absent | P8, P14 |
| `infrastructure` | `buildinfo`, `httpserver`, `config` | P2 `postgres`, P18 `telemetry` |
| `app` | `serve` and `all` roles | P7 `scheduler`, P8 `worker` |
| `devtool` | `specref`, `openapibundle`, `stubgen`, `docscheck` | stable |

```text
internal/
├── entity/                                  RING 1: pure rules, no I/O
│   ├── id/                                  ULID, slug, slug-from-name (stackReslug)  (P2)
│   ├── tier/                                BillingTierFeature set and gates  (P3)
│   ├── identity/                            Principal, actor kinds, API key, session snapshot  (P3)
│   ├── space/                               tree, root immutability, re-parent checks, reachability,
│   │                                        inheritance toggle  (P4)
│   ├── rbac/                                the 150-value action catalogue, system roles, Decide(subject,
│   │                                        action, space) as a pure function  (P4)
│   ├── stack/                               settings, validation, StackState, lock rules (human, policy key,
│   │                                        blocking run), enable and disable  (P5)
│   ├── config/                              configuration element, write-only rule, checksum, precedence
│   │                                        across stack, attached and auto-attached contexts  (P6)
│   ├── label/                               label matching, autoattach:<label> and autoattach:*  (P6)
│   ├── run/                                 RunType, 22 RunState values, transition table, guards
│   │   │                                    (canConfirm and the rest), timers, follow-up jobs  (P7, P10)
│   │   ├── machine.go transitions.go guards.go timers.go
│   │   ├── priority.go                      presets and the seven pool tiers  (P7)
│   │   └── hooks.go                         phase hook ordering across stack and contexts  (P9)
│   ├── worker/                              pool, worker, status, drain and undrain rules, busy  (P8)
│   ├── iac/                                 the IaC vendor contract and registry  (P9)
│   │   ├── iac.go                           Vendor: Commands, ParseChanges, PolicyInput, ValidateSettings
│   │   ├── vendors.go                       Kind → Vendor
│   │   ├── change.go                        the change model (changesV3), delta counts
│   │   ├── terraform/ opentofu/             (P9)
│   │   └── terragrunt/ pulumi/ cloudformation/ kubernetes/ ansible/   (P20)
│   ├── state/                               state version, rollback marker, access rule by run phase  (P11)
│   ├── vcs/                                 normalized Push and PullRequest, dedupe key, default push
│   │                                        rule, HMAC and token webhook verification  (P12)
│   ├── policy/                              13 PolicyType names and aliases, flags, per-type result
│   │   │                                    shapes, combination rules, input document types  (P13)
│   │   ├── combine.go                       deny wins, ignore wins, all approvals, priority agreement
│   │   ├── input/                           input document structs per type, the sanitized helper
│   │   └── receipt.go sample.go
│   ├── cloud/                               integration kinds, OIDC subject template  (P15)
│   ├── schedule/                            decision 0011's cron grammar, next fire, timezone  (P16)
│   ├── dependency/                          stack dependency graph, references, cycle check  (P16)
│   ├── registry/                            module version numbering (no reuse, no skips), version
│   │                                        state, yanked; provider version, platforms  (P17)
│   ├── event/                               the domain event catalogue: RunStateChanged, StackChanged, ...  (P2)
│   ├── seal/                                decision 0013's segmented AES-256-GCM format and HKDF content
│   │                                        keys, shared by the keyring and the runner  (P2, P9)
│   ├── mask/                                log masking rules of decision 0013  (P9)
│   ├── notification/                        inbox item, grouping key, severity  (P18)
│   ├── webhook/                             delivery record, signature, auto-disable after 10 failures  (P18)
│   ├── audit/                               entry, resource types, sanitising  (P18)
│   ├── template/                            template, version state, inputs, deployment  (P21)
│   ├── blueprint/                           (P21)
│   ├── resource/                            managed entity, drift flag, search filters  (P22)
│   ├── intent/                              project, resource, operation, review  (P25)
│   └── flows/                               organization, project, flow, block, socket, event lineage,
│                                            invocation status, monthly and hourly limits  (P26)
│
├── usecase/                                 RING 2
│   ├── unitofwork/                          shared contract: Do(ctx, fn), AfterCommit  (P2)
│   ├── event/                               shared contract: Appender (outbox write in the caller's tx)  (P2)
│   ├── job/                                 shared contract: job kinds, Enqueuer, Handler  (P7)
│   ├── runstate/                            shared contract: Applier.Apply(run, event) in one tx  (P7)
│   ├── system/                              version, debug info, readiness facts
│   ├── account/                             account settings, tier answers  (P3)
│   ├── authenticate/                        API key, session, run token → Principal  (P3)
│   ├── token/                               API key exchange, short-lived JWT  (P3)
│   ├── user/ apikey/ session/               (P3)
│   ├── space/ role/ rolebinding/            (P4)
│   ├── authorize/                           loads bindings, calls entity/rbac.Decide  (P4)
│   ├── stack/                               create, update, delete, get, list, search, reslug  (P5)
│   ├── stackcontrol/                        enable, disable, lock, unlock, set or sync commit  (P5)
│   ├── context/ configelement/ attachment/  (P6)
│   ├── runtimeconfig/                       effective environment and mounted files for a run  (P6)
│   ├── scheduler/                           the run.schedule job: stack lock and gates, QUEUED → READY  (P7)
│   ├── timer/                               2, 7 and 30 day rules  (P10)
│   ├── workerpool/                          pool CRUD, credentials, settings  (P8)
│   ├── workerregistry/                      register, heartbeat, drain, cycle  (P8)
│   ├── jobdispatch/                         long-poll claim in tier order, stop and kill signals  (P8)
│   ├── runreport/                           worker-reported phases, plan ready, outputs  (P8, P9)
│   ├── runtrigger/                          trigger, propose with overrides, task create, retry  (P9)
│   ├── runaction/                           confirm, discard, stop, cancel, replan, prioritize, promote  (P10)
│   ├── runreview/                           reviews and comments  (P10)
│   ├── runquery/                            get, list by stack, runs in range, logs URLs  (P9)
│   ├── statebackend/                        Terraform http backend: get, put, lock, unlock  (P11)
│   ├── stateversion/                        history, rollback, import, pre-signed URLs  (P11)
│   ├── vcsintegration/                      integration CRUD, default per provider, test  (P12)
│   ├── vcsingest/                           verify, store delivery, enqueue; durable before 202  (P12)
│   ├── vcsevent/                            match stacks, push policy, create runs, cancel superseded  (P12)
│   ├── vcsfeedback/                         commit checks, GitHub deployments  (P12)
│   ├── vcsagentpool/                        (P19)
│   ├── policy/                              CRUD, attach, autoattach  (P13)
│   ├── policyeval/                          evaluate one type, combine, receipts, sampling  (P13)
│   ├── policysimulate/                      simulate with body, input, libraries, engine  (P13)
│   ├── cloudintegration/ oidctoken/         (P15)
│   ├── schedule/ drift/ stackdependency/    (P16)
│   ├── module/ moduleversion/ moduletest/   (P17)
│   ├── provider/ providerversion/ gpgkey/   (P17)
│   ├── registryprotocol/                    discovery, modules.v1, providers.v1, login.v1  (P17)
│   ├── notification/                        inbox and notification policy routing  (P18)
│   ├── webhook/ webhookdeliver/             named and stack webhooks, delivery, retries  (P18)
│   ├── audit/                               record, search, audit trail webhook  (P18)
│   ├── usage/                               usage, workerUsage, metrics read models  (P18)
│   ├── blueprint/ template/ templateversion/ templatedeployment/   (P21)
│   ├── resource/ search/                    (P22)
│   ├── sso/ scim/                           (P23)
│   ├── aiintegration/ assistant/ intent/ mcp/   (P25)
│   ├── flowsorg/ flowsproject/ flow/        (P26)
│   ├── flowengine/                          event processing, invocations, limits  (P26)
│   ├── flowsapp/ flowsagentpool/            apps, installations, registries, agent pools  (P26)
│   └── agent/                               WORKER SIDE (rule 6)
│       ├── launcher/                        register, poll, accept, start container, relay signals  (P8)
│       ├── executor/                        the phase loop inside the run container  (P8, P9)
│       ├── initpolicy/                      worker-side initialization policy  (P14)
│       ├── toolchain/                       resolve vendor binary versions  (P9)
│       ├── vcsagent/                        infraweave vcs-agent: long-polls serve, calls the private VCS  (P19)
│       └── flowsagent/                      infraweave flows-agent: dials the gateway, runs Node.js runtimes  (P26)
│
├── adapter/                                 RING 3
│   ├── rest/
│   │   ├── gen/deploy/                      server.gen.go, unimplemented.gen.go  ← internal/server/gen/deploy
│   │   ├── server.go                        aggregate: embeds every controller, stubs one level deeper
│   │   ├── problem/                         RFC 9457 writer, slugs of decision 0003  ← internal/server/problem
│   │   ├── middleware/                      request ID, body limits, authn, rate limits, idempotency  (P3)
│   │   ├── controller/                      one package per use-case family group
│   │   │   ├── system/                      health and version
│   │   │   ├── stack/ context/ run/ worker/ policy/ vcs/ state/ space/ identity/ (P3 to P13)
│   │   │   ├── cloud/ schedule/ registry/ events/ template/ resource/ ai/ (P15 to P25)
│   │   │   └── flows/                       implements gen/flows  (P26)
│   │   ├── presenter/                       boundary struct → gen response or problem, per family
│   │   └── gen/flows/                       (P26)
│   ├── protocol/                            hand-written chi routes, tag handwritten
│   │   ├── statehttp/                       Terraform http backend  (P11)
│   │   ├── vcswebhook/                      /webhooks/vcs/{integration}, one decoder per provider  (P12)
│   │   ├── discovery/                       /.well-known/terraform.json, login.v1  (P17)
│   │   └── registry/                        modules.v1, providers.v1  (P17)
│   ├── workerapi/                           implements gen/worker for the long-poll endpoints  (P8)
│   │   └── gen/                             worker strict server  (P8)
│   ├── jobrunner/                           driving adapter: claims jobs, dispatches to job.Handler  (P7)
│   ├── outboxrelay/                         events.fanout and events.deliver handlers (decision 0015)  (P7)
│   ├── postgres/                            one concrete store for every storage port  (P2)
│   │   ├── unitofwork.go                    ctx-carried transaction
│   │   ├── outbox.go queue.go               Appender and Enqueuer in the caller's tx
│   │   ├── <table family>.go                stacks.go runs.go policies.go workers.go ...
│   │   ├── migrations/                      embedded *.sql
│   │   └── storetest/                       contract suite run against PostgreSQL by testcontainers
│   ├── objectstore/                         buckets: run logs, states, workspaces, modules, uploads,
│   │   │                                    deliveries, policy inputs; pre-signed URLs  (P2)
│   │   ├── s3/ fs/
│   │   └── objectstoretest/
│   ├── keyring/                             envelope encryption, local key  (P2)
│   │   └── awskms/ gcpkms/ azurekv/ vault/  (P15)
│   ├── jwt/                                 run tokens, OIDC tokens, JWKS  (P3, P15)
│   ├── opa/                                 Engine: prepare per policy and Rego version, capabilities,
│   │                                        deadlines  (P13); also used worker side
│   ├── vcs/                                 implements vcsevent and vcsfeedback ports per provider
│   │   ├── github/                          App auth, compare, checks, comments, deployments, archive  (P12)
│   │   └── gitlab/ bitbucketcloud/ bitbucketdc/ azuredevops/ rawgit/   (P19)
│   ├── vcsagent/                            long-poll request relay to VCS agents (decision 0012)  (P19)
│   ├── cloud/                               AWS STS, Azure, GCP credential exchange  (P15)
│   ├── notify/                              slack/ http/  (P18)
│   ├── ai/                                  provider gateways behind decision 0019  (P25)
│   ├── flowsgateway/                        WebSocket sessions with Flows agents  (P26)
│   └── agent/                               WORKER SIDE (rule 6)
│       ├── workerclient/                    wraps the generated worker client  (P8)
│       │   └── gen/                         worker client  (P8)
│       ├── container/                       docker/ kubernetes/ (P8, P14)
│       ├── workspace/                       source archive and workspace restore via pre-signed URLs  (P9)
│       └── toolchain/                       download and verify vendor binaries  (P9)
│
├── infrastructure/                          RING 4
│   ├── httpserver/                          chi assembly, middleware order, listeners, readiness port
│   ├── postgres/                            pgx pool, migrator runner  (P2)
│   ├── config/                              strict YAML via viper UnmarshalExact, flag > env > file
│   ├── buildinfo/                           ← internal/buildinfo
│   └── telemetry/                           slog, OpenTelemetry, Prometheus  (P18)
│
├── app/                                     composition root
│   ├── roles.go                             serve, scheduler, all
│   ├── usecases.go controllers.go jobs.go events.go
│   └── workerapp/                           worker, worker exec, vcs-agent, flows-agent; worker-side graph only  (P8)
│
└── devtool/
    ├── specref/                                                        ← internal/specref
    ├── openapibundle/                                                  ← internal/openapibundle
    ├── stubgen/                                                        ← internal/stubgen
    └── docscheck/                                                      ← internal/docscheck

pkg/
└── client/
    ├── deploy/                                                         ← pkg/client/deploy
    └── flows/                               (P26)
```

### What the Stacks tag spreads over

The served Stacks tag holds 69 operations, and they are not one family.
Clean architecture splits them by use case, so one tag reaches many
packages:

| Stacks operations | Use case | Phase |
| --- | --- | --- |
| `Stacks`, `StackCreate`, `SearchStacks`, `Stack`, `StackUpdate`, `StackDelete`, `StackReslug` | `usecase/stack` | P5 |
| `StackEnable`, `StackDisable`, `StackLock`, `StackUnlock`, `StackSetCurrentCommit`, `StackSyncCommit`, star | `usecase/stackcontrol` | P5 |
| config element and configs operations | `usecase/configelement` | P6 |
| attachable contexts, policies, worker pools, AWS and Azure integrations | `usecase/attachment` and the owners' queries | P6, P8, P13, P15 |
| runs, run, proposed runs, tasks, runs in range | `usecase/runtrigger`, `usecase/runquery` | P9 |
| managed state import, rollback, state download and upload URLs, state version search | `usecase/stateversion` | P11 |
| policy attachment | `usecase/policy` | P13 |
| stack dependencies and references | `usecase/stackdependency` | P16 |
| scheduled runs, tasks and deletes | `usecase/schedule` | P16 |
| webhooks integrations | `usecase/webhook` | P18 |
| role bindings | `usecase/rolebinding` | P4 |
| managed entity searches, ignored triggers | `usecase/resource`, `usecase/runquery` | P22, P12 |
| migrate vendor | `usecase/stack` with `entity/iac` | P20 |

## Deep dives

### Stack configuration write path

```text
internal/entity/stack/
  stack.go           Stack, Settings, Validate(settings, iac.Vendor) error
  lock.go            CanLock, CanTrigger(principal), autodeploy off while locked
  state.go           StackState and the rule that queued, task, canceled runs do not change it
internal/entity/config/
  element.go         Element{Kind, Name, Value or Sealed, WriteOnly, Runtime, FileMode, Checksum}
  precedence.go      Effective(stack, attached []Attached) by priority, auto-attached last
internal/usecase/stack/
  port.go            StackStore, SpaceReader, Authorizer, VendorValidator, Sealer, Clock,
                     event.Appender (shared)
  input.go           CreateInput, UpdateInput, the input boundary
  output.go          Output, the output boundary (never a gen type, never a row)
  create.go update.go delete.go query.go reslug.go
  errors.go          ErrNotFound, ErrConflict, *ValidationError
internal/usecase/configelement/  port.go add.go delete.go query.go
internal/usecase/attachment/     port.go attach.go detach.go autoattach.go
internal/adapter/rest/controller/stack/
  stack.go           StackCreate, StackUpdate, ... : gen request → input, output → gen response
internal/adapter/rest/presenter/stack.go   Output → deploy.Stack; error → problem
internal/adapter/postgres/stacks.go        satisfies stack.StackStore, stackcontrol.Locker, ...
internal/adapter/keyring/                   satisfies stack.Sealer for write-only values (decision 0013)
```

Every stack write passes through five packages that exist only to hold the
boundary. That is the cost the option pays for keeping `entity/stack` free
of the generated types. For `StackUpdate`:

```go
// internal/adapter/rest/controller/stack/stack.go
func (c *Controller) StackUpdate(ctx context.Context, req deploy.StackUpdateRequestObject) (deploy.StackUpdateResponseObject, error) {
	in, err := presenter.StackUpdateInput(req.StackId, req.Body) // gen body → stack.UpdateInput
	if err != nil {
		return presenter.StackUpdateProblem(err), nil
	}
	out, err := c.stacks.Update(ctx, identity.PrincipalFrom(ctx), in)
	if err != nil {
		return presenter.StackUpdateProblem(err), nil // ErrNotFound → 404, *ValidationError → 422
	}
	return presenter.StackUpdate200(out), nil
}

// internal/usecase/stack/update.go
func (uc *Interactor) Update(ctx context.Context, p identity.Principal, in UpdateInput) (Output, error) {
	var out Output
	err := uc.tx.Do(ctx, func(ctx context.Context) error {
		s, err := uc.stacks.ForUpdate(ctx, in.ID)
		if err != nil {
			return err
		}
		if err := uc.authz.Require(ctx, p, rbac.StackManage, s.Space); err != nil {
			return err
		}
		next, err := s.Apply(in.Patch) // entity/stack: settings validation, vendor check
		if err != nil {
			return err
		}
		if err := uc.stacks.Save(ctx, next); err != nil {
			return err
		}
		out = toOutput(next)
		return uc.events.Append(ctx, event.StackChanged{Stack: next.ID, By: p})
	})
	return out, err
}
```

The audit trail ([report 19](../reference/19-events-notifications-audit.md#8-the-audit-trail))
needs every resource-changing mutation; in this option each interactor
appends its own event, and the audit consumer writes the entry. Nothing
forces an interactor to append; review and the acceptance features catch a
missing one.

### Run execution path

The run state machine is ring 1. Every package that changes a run's state
(trigger, actions, scheduler, worker reports, timers, VCS ingest, stack
dependencies) goes through one shared contract in ring 2, `runstate`, so
the transition, the stack lock, the follow-up jobs and the outbox event
commit together.

```text
internal/entity/run/
  machine.go         Next(run, Event) (State, error): the only legal way to change state
  transitions.go     the table of report 08: (type, from, event) → to, guard
  guards.go          canConfirm, canDiscard, canRetry, canStop, blocking run rules
  priority.go        Tier(run) 1..7; Less(a, b) by tier then creation time
  timers.go          deadlines: 2 days (proposed, testing), 7 days (blocking), 30 days (all)
  followups.go       FollowUps(run, to) []job.Kind: schedule, evaluate approval, notify
internal/usecase/runstate/
  port.go            RunStore (ForUpdate, Save, AppendTransition), StackLocker, job.Enqueuer,
                     event.Appender, Clock
  apply.go           Applier.Apply(ctx, runID, run.Event) (run.Run, error)
  create.go          Applier.Create(ctx, run.New) for runtrigger, vcsevent, stackdependency
internal/usecase/runtrigger/   port.go trigger.go propose.go task.go retry.go
internal/usecase/runaction/    port.go confirm.go discard.go stop.go cancel.go replan.go promote.go
internal/usecase/scheduler/
  port.go            QueueReader (blocker, approvals, upstream and external dependencies)
  schedule.go        run.schedule for one stack: lock and gate checks, QUEUED → READY, NOTIFY
internal/usecase/timer/         port.go expire.go (handler of the run.timers periodic task)
internal/usecase/job/           kinds.go: run.schedule, run.timers, vcs.process_delivery, events.fanout ...
internal/adapter/jobrunner/     claims jobs from decision 0011's jobs table; dispatches by kind
internal/adapter/postgres/runs.go queue.go outbox.go
internal/app/jobs.go            job kind → interactor
```

```go
// internal/usecase/runstate/apply.go
//
// Apply is the one place a run changes state. The row lock, the stack lock
// change, the transition record, the follow-up jobs and the outbox event
// commit in one transaction, so a crash never leaves a run READY with no
// scheduling job or FINISHED with the stack still locked.
func (a *Applier) Apply(ctx context.Context, id id.Run, e run.Event) (run.Run, error) {
	var out run.Run
	err := a.tx.Do(ctx, func(ctx context.Context) error {
		r, err := a.runs.ForUpdate(ctx, id)
		if err != nil {
			return err
		}
		to, err := run.Next(r, e)
		if err != nil {
			return err // *run.IllegalTransitionError → 409 at the presenter
		}
		if change := stack.LockChange(r, to); change.Any() {
			if err := a.stacks.ApplyLock(ctx, r.Stack, change); err != nil {
				return err
			}
		}
		out = r.Moved(to, e, a.now())
		if err := a.runs.AppendTransition(ctx, out, e); err != nil {
			return err
		}
		for _, k := range run.FollowUps(out, to) {
			if err := a.jobs.Enqueue(ctx, job.For(k, out.ID)); err != nil {
				return err
			}
		}
		return a.events.Append(ctx, event.RunStateChanged{Run: out.ID, Stack: out.Stack, From: r.State, To: to})
	})
	return out, err
}
```

Scheduling has two levels, as decision 0008 sets out. The scheduler role
runs `usecase/scheduler` for the `run.schedule` job of one stack: the gates
before `READY` (stack lock, approval in `QUEUED`, upstream dependencies,
external dependencies,
[report 08 section 4](../reference/08-run-lifecycle.md#4-queueing-the-stack-lock-and-scheduling))
are predicates in `entity/run` over facts the port returns, so they are
tested without a database, and a passing run moves to `READY` through
`runstate`. The pool order of
[report 08](../reference/08-run-lifecycle.md#priority) is applied by the
claim: a worker's poll in `jobdispatch` takes the pool's claimable runs in
`run.Less` order with `SKIP LOCKED` and moves the head to `PREPARING`,
`PREPARING_APPLY` or `PREPARING_REPLAN` through `runstate`.

`runstate` is the fourth shared contract and the clearest strain on rule 2:
seven use cases need the same transaction procedure, and a per-consumer port
for each would produce seven copies that drift.

### Policy evaluation with embedded OPA

```text
internal/entity/policy/
  types.go           13 PolicyType names, 3 aliases normalised, deprecated flags
  combine.go         CombinePush, CombinePlan, CombineApproval, CombineTrigger, ... per type
  flags.go           flag reset before set, multi-owner reset
  input/             one struct per type: PushInput, PlanInput, ApprovalInput, ..., sanitized()
  receipt.go         Receipt{Policy, Type, Outcome, Flags, Sampled, Engine}
  sample.go          hourly quota decision, 7-day retention
internal/usecase/policyeval/
  port.go            Engine (Prepare, Eval), PolicyFinder (attached and auto-attached for a target),
                     ReceiptStore, SampleStore (object storage for captured inputs)
  push.go plan.go approval.go trigger.go notification.go login.go intent.go
  evaluate.go        evaluate every policy of a type under the per-type and per-event deadlines
internal/adapter/opa/
  engine.go          rego.New with SetRegoVersion per policy, capabilities without http.send,
                     opa.runtime, rego.parse_module, time.now_ns, trace; PrepareForEval cache
  preamble.go        per-type preamble that defines the result object and defaults
  engine_test.go     the 67 example-library templates and 61 tests as a parity corpus
```

Consumers declare the decision they need; none imports `policyeval`:

```go
// internal/usecase/runreport/port.go
//
// PlanPolicy is consulted after the vendor preview succeeds. The decision
// comes back combined (deny wins, warn collected) with its receipts already
// stored, so this package never sees Rego or OPA.
type PlanPolicy interface {
	EvaluatePlan(ctx context.Context, s policy.PlanSubject) (policy.PlanDecision, error)
}

// internal/usecase/vcsevent/port.go
type PushPolicy interface {
	EvaluatePush(ctx context.Context, s policy.PushSubject) (policy.PushDecision, error)
}

// internal/app/usecases.go (excerpt)
eval := policyeval.New(policyeval.Ports{Engine: opa.New(cfg.Policy), Policies: rt.store, ...})
report := runreport.New(runreport.Ports{PlanPolicy: eval, Runs: rt.applier, ...})
ingest := vcsevent.New(vcsevent.Ports{PushPolicy: eval, ...})
```

The evaluation points of
[report 09 section 8](../reference/09-policies.md#8-where-each-type-is-evaluated)
map to ports declared by `vcsevent` (push), `runstate` follow-ups (approval
on `QUEUED`, `UNCONFIRMED`, `PENDING_REVIEW`), `runreport` (plan),
`runreview` (approval on each review), consumers dispatched by
`outboxrelay` (trigger, notification), `authenticate` (login) and `intent` (Intent). The OPA
details follow [report 27 section 9](../reference/27-reference-materials.md#91-the-go-api)
and decision 0009. Because `policyeval` is the only implementer of every
port, the method set of one interactor answers six packages' interfaces;
the per-consumer ports cost little here and prevent the cycle that would
form if `policyeval` read runs while runs imported `policyeval`.

### Worker protocol and pools

```text
internal/entity/worker/       pool.go worker.go status.go drain.go credentials.go
internal/usecase/workerpool/  port.go crud.go credentials.go (CSR signing, reset)
internal/usecase/workerregistry/
  port.go            WorkerStore, CertVerifier
  register.go heartbeat.go drain.go cycle.go
internal/usecase/jobdispatch/
  port.go            ClaimStore, Waiter (wake on a claimable run or a signal)
  poll.go            Poll(ctx, worker, wait) (Assignment | Signal | none): claim, then the long-poll hold
  signal.go          stop and kill, delivered on the next poll
internal/usecase/runreport/   port.go phase.go plan.go apply.go outputs.go logs.go
internal/adapter/workerapi/
  gen/               strict server for api/src/worker  (P8)
  controller.go      Register, Poll (the heartbeat), Deregister, Spec, Reports, UploadURLs, LogChunks,
                     Artifacts, Workspace: the /worker/v1 operations of decision 0008
  auth.go            pool certificate and per-run token check
internal/usecase/agent/launcher/      WORKER SIDE
  port.go            Server (the worker API), Containers (Run, Stop, Kill), InitPolicy, Clock
  launcher.go        register, loop: poll, evaluate init policy, start container, relay signals
internal/usecase/agent/executor/
  port.go            Server, Workspace, Toolchain, Shell
  executor.go        phases: prepare, before_init, init, after_init, plan, ..., after_run
internal/adapter/agent/workerclient/  implements launcher.Server and executor.Server over gen/
internal/adapter/agent/container/docker/ kubernetes/   implement launcher.Containers
internal/app/workerapp/      worker (launcher) and worker exec (executor) composition
```

The worker role's graph is entities plus `usecase/agent`, `adapter/agent`,
`adapter/opa` (for the worker-side initialization policy of
[report 10 section 10](../reference/10-workers-and-execution.md#10-signed-runs-and-worker-side-policy))
and container libraries. `cmd/infraweave worker` calls
`workerapp.Launcher(cfg)` and `worker exec` calls `workerapp.Exec(cfg)`;
neither constructs a store. The launcher mounts its own static binary
(the Makefile already builds with `CGO_ENABLED=0` for six platforms) into
the runner image as the container's root process, which then runs
`worker exec`.

Long-poll is a controller detail: `workerapi.Poll` holds the request until
`jobdispatch.Poll` returns or the configured hold (60 seconds by default,
[report 10](../reference/10-workers-and-execution.md#42-transports)) runs
out. The waiter that wakes it is a port, implemented by
`adapter/postgres` with `LISTEN` on decision 0005's `infraweave_runs`
channel, so any `serve` replica can answer a worker whose run became
claimable on any replica. Decision 0008 makes HTTP long-poll the only
transport, so there is no MQTT adapter.

### VCS webhook ingest

```text
internal/adapter/protocol/vcswebhook/
  routes.go          POST /webhooks/vcs/{provider}/{integrationId}; bounded body; headers only
  github.go gitlab.go bitbucket.go azuredevops.go   map headers to entity/vcs.Delivery
internal/usecase/vcsingest/
  port.go            IntegrationReader, DeliveryStore, job.Enqueuer, unitofwork
  receive.go         verify (entity/vcs), store delivery, enqueue vcs.process_delivery, one tx; then 202
internal/usecase/vcsevent/
  port.go            Normalizer (payload → vcs.Push or vcs.PullRequest), StackMatcher,
                     ChangedFiles, PushPolicy, runstate.Creator, Canceller
  process.go         per stack: project root and globs, push policy, dedupe, create or ignore
  ignored.go         ignored-trigger records, 7-day retention
internal/usecase/vcsfeedback/  port.go checks.go deployments.go (outbox consumer)
internal/adapter/vcs/github/
  app.go             installation tokens
  normalize.go       satisfies vcsevent.Normalizer
  compare.go         satisfies vcsevent.ChangedFiles
  checks.go comments.go deployments.go archive.go
```

Durable intake comes first ([report 27](../reference/27-reference-materials.md#12-cross-tool-comparison)):
`vcsingest.Receive` commits the delivery row and the `vcs.process_delivery` job
before the controller answers, and does no matching or policy work in the
request. Processing runs in the scheduler role. Signature verification is
pure (HMAC SHA-256, shared token, basic auth per provider) and lives in
`entity/vcs`, so the provider adapters only decode. The steps of
[report 11 section 11.2](../reference/11-vcs-integrations.md#112-what-an-event-becomes)
map one to one onto `vcsevent/process.go`, with the deduplication key and
the default push rule in `entity/vcs`.

Six provider packages implement the same four or five ports. Adding a
provider (P19) touches `adapter/vcs/<provider>`, one decoder in
`vcswebhook`, the integration input in the spec and the wiring; no use case
changes unless the provider has a behaviour the ports cannot express.

### State backend

```text
internal/entity/state/
  version.go         Version{Stack, Serial, Lineage, Key, Run, Rollback, ImportedBy}
  access.go          Allowed(run phase, op): plan reads and locks, apply and destroy write, task writes
  lock.go            lock ID check, Terragrunt unit locks
internal/usecase/statebackend/
  port.go            RunTokenVerifier, StateObjects (Get, Put versioned), VersionStore, LockStore
  get.go put.go lock.go unlock.go
internal/usecase/stateversion/  port.go history.go rollback.go import.go presign.go export.go
internal/adapter/protocol/statehttp/
  routes.go          GET, POST, LOCK, UNLOCK on /state/v1/stacks/{stack}[/workspaces/{workspace}]
  credentials.go     basic auth carrying the per-run token
internal/adapter/objectstore/   states bucket with versioning (report 24, section 3.2)
internal/adapter/postgres/state_versions.go
```

The protocol is not infraweave's REST API, so `statehttp` is a hand-written
controller: chi routes for the custom `LOCK` and `UNLOCK` methods, tagged
`handwritten` so the generated router never claims the path. Authorization
follows [report 16 section 1.1](../reference/16-state-management.md#11-how-a-run-reaches-it):
the token must belong to a live run whose phase allows the operation, a pure
check in `entity/state/access.go` over facts `RunTokenVerifier` returns.
Content goes to the versioned states bucket; the version row and the
transition to the new serial commit in one transaction after the object
write, so a failed commit leaves an orphan object, not a missing state.

### Module and provider registry protocol

```text
internal/entity/registry/
  module.go          address (namespace, name, provider), version numbering: major.minor.patch,
                     no reuse of a successful number, no skipped number
  version.go         PENDING, ACTIVE, FAILED, yanked
  provider.go        provider version: draft, published, revoked; platforms; checksum rules
internal/usecase/module/ moduleversion/ moduletest/ provider/ providerversion/ gpgkey/
internal/usecase/registryprotocol/
  port.go            ModuleReader, ProviderReader, Presigner, Authorizer
  discovery.go       login.v1, modules.v1, providers.v1, state.v2 keys
  modules.go         versions list, download → X-Terraform-Get with a short-lived URL
  providers.go       versions, download with signing_keys
  login.go           login.v1 authz code and token exchange
internal/adapter/protocol/discovery/   /.well-known/terraform.json, /terraform-login, /terraform/token
internal/adapter/protocol/registry/    /registry/modules/v1/..., /registry/providers/v1/...
internal/adapter/objectstore/          modules bucket, provider archives, pre-signed GET
```

Like state, the registry speaks HashiCorp's protocols
([report 15 section 9](../reference/15-module-and-provider-registry.md#9-registry-protocol)),
so its controllers are hand-written and its presenters render the
protocol's JSON, not problem details: the module download answers `204`
with `X-Terraform-Get`, an unknown version `404` in plain text. The
presenter carries those quirks; the use case only returns "not found" or a
URL. Module test runs are runs: `moduletest` creates `TESTING` runs through
`runstate`, which is the second use of the shared contract from outside the
run family.

### Access, identity and spaces

```text
internal/entity/identity/    principal.go apikey.go session.go actor.go
internal/entity/space/       tree.go reach.go inherit.go
internal/entity/rbac/        actions.go (150 values, 18 disabled) roles.go decide.go
internal/usecase/authenticate/  port.go apikey.go session.go runtoken.go login.go
internal/usecase/token/      port.go exchange.go jwt.go
internal/usecase/authorize/
  port.go            BindingReader, SpaceReader
  authorize.go       Require(ctx, principal, action, space) error; Reachable(ctx, principal) []space.ID
internal/usecase/space/ role/ rolebinding/ user/ apikey/ session/
internal/usecase/sso/ scim/                     (P23)
internal/adapter/rest/middleware/authn.go       calls authenticate, puts the Principal in ctx
internal/adapter/rest/controller/space/ identity/
internal/adapter/jwt/                           signing keys, rotation, JWKS (decision 0006)
```

Space reachability is the filter on almost every read
([report 28 section 1.2](../reference/28-data-model.md#12-identity-and-tenancy)).
Each interactor that lists or reads declares an `Authorizer` port and asks
`Reachable` once per call, then passes the space set into its store port.
The decision itself (`rbac.Decide`, propagation down, read up through
inheritance, implicit stack read) is pure and lives in ring 1, which is
where clean architecture is at its best: the 150-action catalogue is
tested as a table with no fixtures.

The repetition is the cost: every listing port in every use case carries a
`spaces []space.ID` argument, and forgetting it compiles. A store contract
test in `adapter/postgres/storetest` asserts the filter for each listing.

### Events: notifications, webhooks and audit

```text
internal/entity/event/         catalogue.go: RunStateChanged, StackChanged, ModuleVersionChanged,
                               InternalError, WorkerUsage, MutationAudited, ...
internal/usecase/event/        Appender, Consumer{Kinds(); Handle(ctx, event.Envelope)}
internal/adapter/postgres/outbox.go   Appender in the caller's tx
internal/adapter/outboxrelay/  handles events.fanout (one events.deliver job per target) and events.deliver
internal/usecase/notification/ port.go route.go inbox.go (notification policy, four sinks)
internal/usecase/webhookdeliver/ port.go deliver.go sign.go retry.go disable.go
internal/usecase/audit/        port.go record.go search.go stream.go (audit trail webhook)
internal/usecase/vcsfeedback/  consumer for checks and deployments
internal/adapter/notify/slack/ http/
internal/app/events.go         consumer registry: which use case hears which event kinds
```

Producers append to the `events` outbox in their own transaction, which
also enqueues the `events.fanout` job (decision 0015); consumers are
ordinary interactors registered in `app/events.go`. The notification policy's `pull_request` rule reaches the
VCS through a `PullRequestCommenter` port declared by `notification` and
implemented by `adapter/vcs/github`. Delivery records and their bodies
follow [report 19 section 6](../reference/19-events-notifications-audit.md#6-named-webhooks)
(deliveries bucket, auto-disable after ten failures, which itself
notifies). Decision 0015 owns the event model.

What a reader cannot see in the tree is the fan-out: a run state change
reaches the inbox, Slack, named webhooks, stack webhooks, PR comments,
GitHub deployments, the audit stream and Flows subscription blocks, and
the only place that lists them is `app/events.go`.

### Flows engine

```text
internal/entity/flows/
  model.go           Organization, Project, Flow, Block, Socket, AppInstallation (UUID identity)
  lineage.go         primary and secondary parents, invocation from lineage, echo, pending events
  invocation.go      in progress, failed in progress, processed, failed
  limits.go          50,000 per month and per hour defaults, 1,000,000 ceiling, failure modes
internal/usecase/flowsorg/ flowsproject/ flow/ flowsapp/ flowsagentpool/
internal/usecase/flowengine/
  port.go            EventStore, InvocationStore, Runtime (evaluate expression, run block handler),
                     Limits, job.Enqueuer
  emit.go route.go retry.go reemit.go timers.go
internal/adapter/rest/gen/flows/       (P26) strict server for api/src/flows
internal/adapter/rest/controller/flows/
internal/adapter/flowsgateway/         WebSocket sessions; implements flowengine.Runtime by routing to agents
internal/adapter/postgres/flows_*.go   flows_* tables, UUIDv7 keys (decision 0018)
```

Flows has its own tenancy (organizations, projects) and its own identifiers
([report 23](../reference/23-flows.md#2-resource-hierarchy)),
and decision 0018 owns the runtime. In this option Flows is one subject
spread across all four rings, recognisable only by the `flows` prefix:
`entity/flows`, five `usecase/flow*` packages, `adapter/flowsgateway`,
`controller/flows`, `postgres/flows_*.go`. Extracting it into a separate
service later means collecting those by name. The rings keep its inner
rules clean; they do nothing to keep Flows apart from Deploy. The agent
that runs the Node.js runtimes is the `flows-agent` subcommand of decision
0018, built in `usecase/agent/flowsagent`, so rule 6 fences it like the
launcher.

### Composition root

```text
internal/app/
  roles.go           Serve(ctx, cfg), Scheduler(ctx, cfg), All(ctx, cfg): open what each role needs
  runtime.go         pgx pool, migrations, object storage, keyring, queue, outbox
  usecases.go        constructs every interactor with its ports (about 110 at P27)
  controllers.go     constructs controllers, presenters and the strict-server aggregate
  jobs.go            job kind → job.Handler (scheduler role)
  events.go          event kind → consumers (scheduler role)
  workerapp/         Launcher, Exec, VCSAgent, FlowsAgent: worker-side graph only
cmd/infraweave/main.go   cobra root; binds only setting flags; subcommands call app
```

```go
// internal/app/roles.go (excerpt)
//
// Serve and Scheduler build the same use cases over the same runtime; a
// role differs only in which driving adapters it starts. That keeps "all"
// a union rather than a third wiring.
func Serve(ctx context.Context, cfg config.Config) error {
	rt, err := openRuntime(ctx, cfg)
	if err != nil {
		return err
	}
	defer rt.Close()
	uc := newUseCases(rt, cfg)
	return httpserver.Run(ctx, cfg.HTTP, newControllers(uc, cfg))
}

func Scheduler(ctx context.Context, cfg config.Config) error {
	rt, err := openRuntime(ctx, cfg)
	if err != nil {
		return err
	}
	defer rt.Close()
	uc := newUseCases(rt, cfg)
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return jobrunner.Run(ctx, rt.queue, newJobs(uc, newConsumers(uc))) })
	g.Go(func() error { return jobrunner.Tick(ctx, rt.queue) }) // decision 0011's schedule ticker
	return g.Wait()
}
```

`usecases.go` is long and flat: every port assignment is visible, which is
the point, and at about 110 interactors it is the strongest argument for a
wiring aid that the implementation plan can weigh.

### Generated code

| Spec | Fragments | Strict server output | Implemented by | Client output |
| --- | --- | --- | --- | --- |
| deploy | `api/src/deploy/` | `internal/adapter/rest/gen/deploy` | `adapter/rest/server.go` aggregate over `controller/*` | `pkg/client/deploy` |
| worker (P8) | `api/src/worker/` | `internal/adapter/workerapi/gen` | `adapter/workerapi` | `internal/adapter/agent/workerclient/gen` |
| flows (P26) | `api/src/flows/` | `internal/adapter/rest/gen/flows` | `controller/flows` | `pkg/client/flows` |
| hand-written | none (`handwritten` tag) | none | `adapter/protocol/*` | none |

The worker client stays internal: only `infraweave worker` speaks the
protocol, the binary on both ends ships from this repository, and
external autoscalers read pool load through the deploy API.

```go
// internal/adapter/rest/server.go
//
// Controllers are embedded at depth one and the generated stubs at depth
// two. An implemented operation shadows its 501 stub; two controllers that
// implement the same operation make the method ambiguous, so the assertion
// below fails to compile.
type deployServer struct {
	*stackctl.Controller
	*runctl.Controller
	*contextctl.Controller
	*statectl.Controller
	stubs
}

type stubs struct{ deploy.UnimplementedStrict }

var _ deploy.StrictServerInterface = (*deployServer)(nil)
```

`devtool/specref` keeps generating the reference, `devtool/openapibundle`
the bundles and `devtool/stubgen` the stubs, behind unchanged `tools/*`
mains. The Makefile's `generate` loop and `verify-generate` hard-code
`internal/server/gen/$$s`; both take a per-spec output directory instead,
read from `api/oapi-server-<spec>.yaml`.

### Tests and enforcement

| Kind | Location |
| --- | --- |
| Entity tables | beside the code: transition table, tiers, combination rules, rbac decisions, version numbering |
| Use-case tests | beside the code, with tiny fakes of the package's own ports |
| Store contract | `adapter/postgres/storetest`, against PostgreSQL by testcontainers (decision 0016) |
| Object storage contract | `adapter/objectstore/objectstoretest` over `fs` and an S3-compatible container |
| Policy parity | `adapter/opa` over the example library corpus |
| Controller tests | `adapter/rest/controller/*` with fake interactors |
| Acceptance | `test/acceptance`, godog, `infraweave all` plus a worker against real dependencies |

Enforcement is depguard. Rules 1, 3, 4, 7 and 8 are a handful of entries
each; rule 6 is one strict allow-list; rule 2 is one rule per use case,
generated:

```yaml
# .golangci.yml (excerpt)
linters:
  enable: [depguard]
  settings:
    depguard:
      rules:
        entity:                       # rule 1
          list-mode: strict
          files: ["**/internal/entity/**"]
          allow:
            - $gostd
            - github.com/weaveplatform/infraweave/internal/entity
            - github.com/oklog/ulid/v2
            - golang.org/x/mod/semver
          deny:
            - pkg: net/http
              desc: entities do no I/O
            - pkg: database/sql
            - pkg: os
            - pkg: io/fs
        adapter:                      # rule 3
          files: ["**/internal/adapter/**"]
          deny:
            - pkg: github.com/weaveplatform/infraweave/internal/infrastructure
            - pkg: github.com/weaveplatform/infraweave/internal/app
        worker-side:                  # rule 6
          list-mode: strict
          files:
            - "**/internal/usecase/agent/**"
            - "**/internal/adapter/agent/**"
            - "**/internal/app/workerapp/**"
          allow:
            - $gostd
            - github.com/weaveplatform/infraweave/internal/entity
            - github.com/weaveplatform/infraweave/internal/usecase/agent
            - github.com/weaveplatform/infraweave/internal/adapter/agent
            - github.com/weaveplatform/infraweave/internal/adapter/opa
            - github.com/open-policy-agent/opa/v1
            - github.com/oapi-codegen/runtime
            - github.com/go-jose/go-jose/v4
            - github.com/moby/moby/client
            - k8s.io/client-go
        # generated by tools/depguardgen, one per use-case package (rule 2)
        usecase-stack:
          list-mode: strict
          files: ["**/internal/usecase/stack/**"]
          allow:
            - $gostd
            - github.com/weaveplatform/infraweave/internal/entity
            - github.com/weaveplatform/infraweave/internal/usecase/stack$
            - github.com/weaveplatform/infraweave/internal/usecase/stack/
            - github.com/weaveplatform/infraweave/internal/usecase/unitofwork$
            - github.com/weaveplatform/infraweave/internal/usecase/event$
            - github.com/weaveplatform/infraweave/internal/usecase/job$
            - github.com/weaveplatform/infraweave/internal/usecase/runstate$
```

depguard matches by string prefix. In strict mode an import that matches
both lists is allowed only when the allow entry is longer, and `$gostd`
expands to the standard library's top-level directories, so rule 1's
`os` deny (the same length as the `os` allow) also denies `os/exec` and
`os/signal`, while `net/http` beats the shorter `net` allow and `net/url`
stays allowed. The same string matching would let a plain
`usecase/stack` entry admit `usecase/stackcontrol`, and `usecase/job`
admit `usecase/jobdispatch`; that is why the generated entries end in `$`
(exact match) or `/` (the package's own subtree). Rule 2 cannot be one
rule because depguard has no variable for the importing file's own
package: "any `usecase/<x>` except your own" needs one rule per use case.
`tools/depguardgen` writes those rules between two marker
comments in `.golangci.yml`, and a `verify-lint-config` gate step fails
when a new use-case directory has no rule. The `usecase/agent` subtree is
covered by both its own generated rules and rule 6.

Coverage: thin boundary packages are easy to cover. Packages likely to
struggle with the 90% floor are the controller packages for families with
many small operations (each needs a test per operation for its error
branches), `adapter/rest/presenter`, `infrastructure/httpserver`,
`adapter/jobrunner` and `adapter/outboxrelay` (concurrency paths), and the
cloud and VCS gateways, whose failure paths need recorded fixtures.
`.testcoverage.yml` replaces `^internal/server/gen/` with
`^internal/adapter/rest/gen/`, `^internal/adapter/workerapi/gen/` and
`^internal/adapter/agent/workerclient/gen/`.

## Walkthroughs

### A GitHub push becomes a proposed run, a plan policy and a PR comment

1. `infrastructure/httpserver/router.go` (serve role) has mounted `adapter/protocol/vcswebhook` at `/webhooks/vcs/{provider}/{integrationId}`.
2. `adapter/protocol/vcswebhook/github.go` reads the bounded body and the `X-Hub-Signature-256` and `X-GitHub-Event` headers into an `entity/vcs.Delivery`.
3. `usecase/vcsingest/receive.go` loads the integration through `IntegrationReader` (`adapter/postgres/vcs_integrations.go`), verifies the HMAC with `entity/vcs/verify.go`, and in one `unitofwork.Do` stores the delivery and enqueues `vcs.process_delivery` (`adapter/postgres/queue.go`).
4. `vcswebhook` answers `202`. Nothing else happens in the request.
5. `adapter/jobrunner` (scheduler role) claims the job and calls `usecase/vcsevent/process.go`.
6. `adapter/vcs/github/normalize.go` turns the payload into an `entity/vcs.Push` for a branch that is not tracked.
7. `vcsevent` finds matching stacks through `StackMatcher` (`adapter/postgres/stacks.go`, index on integration, repository, branch) and the changed files through `ChangedFiles` (`adapter/vcs/github/compare.go`).
8. `vcsevent` calls its `PushPolicy` port; `usecase/policyeval/push.go` builds `entity/policy/input.PushInput`, evaluates attached push policies through `adapter/opa/engine.go`, combines with `entity/policy/combine.go` and stores the sample; outcome `propose`.
9. `entity/vcs/dedupe.go` gives the key (stack, SHA, `PROPOSED`); `runstate/create.go` inserts the run `QUEUED`, cancels superseded proposed runs on the branch through `Apply`, appends `RunStateChanged` and enqueues `run.schedule`, in one transaction.
10. `usecase/scheduler/schedule.go` (the `run.schedule` job) finds no gate closed for a proposed run and moves it to `READY` through `runstate`, which issues `NOTIFY infraweave_runs` for the pool.
11. `adapter/workerapi/controller.go` (serve role) wakes the worker's held poll; `usecase/jobdispatch/poll.go` claims the pool's runs in `entity/run/priority.go` order (tier 3, normal proposed), moves this one to `PREPARING` and returns the assignment with its run token.
12. On the worker, `usecase/agent/launcher` starts the runner container; `worker exec` runs `usecase/agent/executor`, which takes the plan commands from `entity/iac/terraform`, uploads the plan JSON to a pre-signed URL and calls `ReportPlan`.
13. `usecase/runreport/plan.go` reads the plan through its `PlanObjects` port (`adapter/objectstore`), parses changes with `entity/iac/terraform`, and calls its `PlanPolicy` port (`policyeval/plan.go`): no deny, no warn.
14. `runreport` applies `PLANNING → FINISHED` through `runstate`; the outbox gets `RunStateChanged`.
15. `adapter/outboxrelay` delivers it to `usecase/vcsfeedback/checks.go` (commit check through `adapter/vcs/github/checks.go`) and to `usecase/notification/route.go`, which evaluates notification policies; a `pull_request` rule calls `PullRequestCommenter`, implemented by `adapter/vcs/github/comments.go`, with the rule's deduplication key.

### A tracked run awaits approval and applies on a private worker

1. A push to the tracked branch follows steps 1 to 8 above; the push policy says `track`.
2. `runstate/create.go` inserts a `TRACKED` run; `entity/stack/lock.go` says a blocking run takes the stack lock, applied in the same transaction.
3. The follow-up `approval.evaluate` job runs `policyeval/approval.go` for `QUEUED`; no approval policy blocks queueing.
4. `usecase/scheduler` finds the stack lock free and moves the run to `READY`; the private pool's claim order puts it in tier 2.
5. A launcher on the customer's host, started as `infraweave worker` with the pool's certificate, holds a poll on `adapter/workerapi`; `workerapi/auth.go` checks the session token against `usecase/workerregistry`, and `jobdispatch` claims the run to `PREPARING`.
6. `usecase/agent/launcher` evaluates the worker-side initialization policy (`usecase/agent/initpolicy` over `adapter/opa`) and starts the container through `adapter/agent/container/docker`.
7. `usecase/agent/executor` restores the source archive (`adapter/agent/workspace`), downloads the vendor binary (`adapter/agent/toolchain`), plans, and reports. Terraform reads state through `adapter/protocol/statehttp` with the run token; `entity/state/access.go` allows read and lock in `PLANNING`.
8. `runreport/plan.go` evaluates plan policies; a `warn` sends the run to `UNCONFIRMED`. The executor uploads the workspace to the workspaces bucket and exits.
9. The follow-up job evaluates approval policies for `UNCONFIRMED`: one approval required.
10. A reviewer calls `POST /api/v1/stacks/{stackId}/runs/{runId}/reviews` (route per the deploy spec); `controller/run` calls `usecase/runreview`, which re-evaluates approval through its `ApprovalPolicy` port: approved.
11. The stack writer calls the confirm operation; `usecase/runaction/confirm.go` checks `entity/run/guards.go` (`canConfirm`) and applies `UNCONFIRMED → CONFIRMED`, which is claimable.
12. A poll of the same pool claims the run (`jobdispatch`, `CONFIRMED → PREPARING_APPLY`); the launcher restores the workspace and the runner applies. `statehttp` accepts the write because `entity/state/access.go` allows it in `APPLYING`; `usecase/statebackend/put.go` writes the object and the version row.
13. `runreport/apply.go` applies `APPLYING → FINISHED`; `entity/stack/lock.go` releases the lock in the same transaction; consumers run trigger policies, notifications and the GitHub deployment update.

### Adding an IaC vendor: Pulumi (P20)

1. `internal/entity/iac/pulumi/pulumi.go`: `Vendor` with `Commands(phase, settings)` for preview, up, destroy and outputs.
2. `internal/entity/iac/pulumi/changes.go`: preview JSON to the change model.
3. `internal/entity/iac/pulumi/policy.go`: the `pulumi` block of plan policy input ([report 20 section 2](../reference/20-vendors.md#2-plan-policy-input-per-vendor)).
4. `internal/entity/iac/pulumi/settings.go`: login URL and stack name validation.
5. `internal/entity/iac/pulumi/*_test.go` and `testdata/`.
6. `internal/entity/iac/vendors.go`: register the kind.
7. `internal/adapter/agent/toolchain/pulumi.go` and its test: binary resolution, or a no-op when the runner image carries it.
8. `api/src/deploy/components-common.yaml`: the Pulumi vendor input; `make generate`.
9. `internal/adapter/rest/presenter/stack.go`: map the new vendor input to `stack.Settings`.
10. `test/acceptance/features/vendor_pulumi.feature` and its steps.

What else changes: no use case. `stack.Validate`, `runreport`, `policyeval`
and the executor read the vendor through `entity/iac`. About 10 new files
and 3 edited, two of them in rings other than ring 1.

### Adding a business domain: templates (P21)

1. `internal/entity/template/template.go version.go deployment.go inputs.go` and tests: version states, input validation ([report 17 section 7](../reference/17-blueprints-and-templates.md#7-templates-and-template-versions)).
2. `internal/entity/event/template.go`: `TemplateVersionPublished`, `DeploymentChanged`.
3. `internal/usecase/template/port.go crud.go *_test.go`.
4. `internal/usecase/templateversion/port.go publish.go deprecate.go *_test.go`.
5. `internal/usecase/templatedeployment/port.go deploy.go update.go delete.go *_test.go`: creates stacks through a `StackCreator` port and runs through `runstate`.
6. `internal/usecase/stack/input.go`: `CreateInput` gains the owning deployment.
7. `internal/adapter/postgres/templates.go`, `migrations/NNNNN_templates.sql` (decision 0005's single stream), `storetest/templates.go`.
8. `api/src/deploy/paths-templates.yaml`; `make generate`.
9. `internal/adapter/rest/controller/template/*.go` and `presenter/template.go`.
10. `internal/adapter/rest/server.go`: embed the controller.
11. `internal/app/usecases.go controllers.go`: wire three interactors; `StackCreator` is filled with `stack.Interactor`.
12. `.golangci.yml`: `make lint-config` adds three generated rules.
13. `test/acceptance/features/templates_deployments.feature` and steps.

What else changes: `stack` gains a field, nothing else in ring 2. About 28
new files and 6 edited, across four rings, the spec and the lint config.

## Package mapping

| Today's package | New location | Notes |
| --- | --- | --- |
| `cmd/` (`cmd/README.md` only) | `cmd/infraweave`, `cmd/infraweavectl` (P24) | thin cobra shims over `internal/app` and `app/workerapp`; README kept |
| `api` (`api/doc.go`, `api/deploy_test.go`) | `api` | unchanged; embeds stay; gains `Worker` (P8) and `Flows` (P26) embeds |
| `api/src/deploy` | `api/src/deploy` | unchanged; gains fragments per family |
| `api/reference/spacelift` | `api/reference/spacelift` | unchanged |
| `internal/buildinfo` | `internal/infrastructure/buildinfo` | `-ldflags -X` path in the Makefile follows |
| `internal/docscheck` | `internal/devtool/docscheck` | `docs-check` target path follows |
| `internal/openapibundle` | `internal/devtool/openapibundle` | |
| `internal/server/gen/deploy` | `internal/adapter/rest/gen/deploy` | ring 3, so controllers do not import outward |
| `internal/server/problem` | `internal/adapter/rest/problem` | presenters use it; slugs stay those of decision 0003 |
| `internal/specref` | `internal/devtool/specref` | |
| `internal/stubgen` | `internal/devtool/stubgen` | writes `unimplemented.gen.go` beside each strict server |
| `pkg/client/deploy` | `pkg/client/deploy` | unchanged, public |
| `tools/openapi-bundle` | `tools/openapi-bundle` | imports `internal/devtool/openapibundle` |
| `tools/specref` | `tools/specref` | imports `internal/devtool/specref` |
| `tools/stubgen` | `tools/stubgen` | imports `internal/devtool/stubgen` |
| (new) | `tools/depguardgen` | writes the rule-2 depguard entries |
| `test/acceptance` | `test/acceptance` | unchanged; starts `infraweave all` and a worker |
| `api/oapi-server-deploy.yaml` | same file | `output: internal/adapter/rest/gen/deploy/server.gen.go` |
| `Makefile` | same file | per-spec generate outputs, `verify-generate` paths, buildinfo ldflags path, `docs-check` path, `lint-config` (runs `tools/depguardgen`) and `verify-lint-config` |
| `.golangci.yml` | same file | depguard enabled with the rules above |
| `.testcoverage.yml` | same file | gen exclusions follow the new paths |

## Numbers

Estimates, rounded.

| Measure | Today (P1) | End of P27 |
| --- | --- | --- |
| Go packages (internal, pkg, cmd, tools) | about 20 | about 245 |
| Top-level directories under internal/ | 6 (entity, usecase, adapter, infrastructure, app, devtool) | 6 |
| Dependency rules | 8 | 8 |
| depguard rule entries | about 8 rules, 25 entries | about 120 rules, 750 entries (110 generated) |
| Files touched to add a vendor | n/a | about 10 new, 3 edited |
| Files touched to add a domain | n/a | about 28 new, 6 edited |
| Packages a request crosses for a stack update | 3 (middleware, controller aggregate, problem) | 8 (middleware, controller, presenter, use case, authorize, entity/stack, entity/space, adapter/postgres) |
| Largest package | `internal/adapter/rest/gen/deploy` (24k generated lines) | generated: `adapter/rest/gen/deploy`; hand-written: `adapter/postgres`, about 30k lines |
| Thin packages at risk under the 90% floor | `infrastructure/httpserver` | about 25: controller packages for small families, presenters, cloud and VCS gateways, `jobrunner`, `outboxrelay` |
| Use-case packages | 1 | about 110 (4 shared contracts, about 100 server, 6 worker side) |
| Entity packages | 0 | about 32 plus 7 vendor packages |

## Placement choices and friction

Placement choices settled in this file:

- **Generated servers sit in ring 3.** The strict-server interface is the
  controller contract, so `gen/deploy` lives beside its controllers and no
  rule needs an outward exception.
- **Four shared contracts in ring 2.** `unitofwork`, `event`, `job` and
  `runstate` are the only use-case packages others import, because a run
  transition, its lock, its jobs and its event must commit together from
  seven callers.
- **Vendors are entities.** Command sets, change parsing, plan policy input
  and settings validation are pure, so one `entity/iac/<name>` package
  serves both the server and the worker; only binary download is a gateway.
- **IaC vendor code lives under `iac/`, not `vendor/`.** The go tool treats any
  path element named `vendor` as vendored code: `go list ./...` skips it and
  importing it fails, so `entity/iac/<name>` holds the per-vendor packages.
- **Webhook signatures are entities.** HMAC and token checks are pure, so
  provider adapters only decode.
- **The worker side is a subtree in each ring**, fenced by one strict
  depguard allow-list, and built by `app/workerapp`.
- **One SQL store.** `adapter/postgres` is one concrete type satisfying
  every storage port; tables belong to the ring, not to a use case.
- **Hand-written protocols are controllers too.** State, registry,
  discovery and webhooks sit in `adapter/protocol`, with presenters that
  keep each protocol's quirks out of the use cases.
- **Maintainer tooling leaves the rings.** `devtool/` holds specref,
  openapibundle, stubgen and docscheck.

Where this layout rubs against the system:

- **CRUD ceremony is the bulk of the code.** Most of the surface is
  resource CRUD over 53 tags. Each operation needs a controller method, an
  input boundary, an output boundary and two presenter functions, so the
  Stacks family alone carries thousands of lines of mapping between gen
  types, boundaries and rows that say the same thing three times.
- **Domains are invisible.** The run lifecycle is `entity/run`,
  `runstate`, `runtrigger`, `runaction`, `runreview`, `runreport`,
  `runquery`, `scheduler`, `timer` and `jobdispatch`; nothing in the tree
  says they belong together, and nothing stops `usecase/intent` (P25) from
  declaring a port over run tables.
- **Rule 2 needs generated lint config.** depguard's prefix matching cannot
  express "no sibling use case", so about 110 generated rules and a gate
  step stand in for one sentence.
- **`runstate` is a service in disguise.** It is shared by seven use cases
  and grows with every run behaviour; the per-consumer-port principle holds
  everywhere except where the system's centre of gravity is.
- **The worker cut is a second axis.** Rings and sides cross, so a
  worker-side change is checked by two rule sets and a reader has to know
  both.
- **Event fan-out lives in one file.** `app/events.go` is the only map of
  which consumers hear a run state change; with eight sinks and Flows
  subscriptions that file becomes the architecture.
- **Flows is not separable by structure.** Its separate tenancy is a naming
  convention across four rings, not a boundary.
- **The Stacks tag does not map to a package.** Its 69 operations span 13
  use cases across 13 phases, so a tag-level question ("is Stacks done")
  has no single place to look.
- **The composition root grows long.** `usecases.go` constructs about 110
  interactors by P27, and every new port is one more line to keep right.
