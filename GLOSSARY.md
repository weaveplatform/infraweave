# infraweave

infraweave is self-hosted infrastructure orchestration that offers Spacelift's capabilities behind its own REST API. This glossary is infraweave's own language. Spacelift's terms, in Spacelift's sense, are in the [research glossary](docs/research/reference/glossary.md).

## Language

### Tenancy

**Installation**:
One deployment of infraweave, run by an operator and hosting one or more accounts.
_Avoid_: instance, cluster, environment

**Account**:
A tenant of an installation, with its own spaces, users, tier and data.
_Avoid_: organization, org, workspace

**Operator**:
The person or team that runs an installation and manages its accounts.
_Avoid_: admin, superuser

### Scope

**Capability parity**:
Offering everything Spacelift lets a user do, with Spacelift as the reference foundation rather than an interface to copy.
_Avoid_: compatibility, clone, port

**Superseded interface**:
A Spacelift interface that a newer one replaces, with evidence, so infraweave offers only the newer one.
_Avoid_: deprecated feature, legacy feature

**Feature**:
An entry in infraweave's feature catalogue: a capability, an option or a limit. Every feature carries a tier, and an operator can switch any feature off.
_Avoid_: edition, add-on, flag

**Capability**:
A feature that gates a set of operations, such as drift detection or targeted replan.
_Avoid_: module, product area

**Option**:
A feature that gates one value or mode inside an operation, such as aggregated VCS checks.
_Avoid_: setting, toggle

**Limit**:
A feature that is a quantity, such as seats or private workers.
_Avoid_: quota, cap

**Tier**:
A named set of features and limits. Tiers are data, not an ordered ladder.
_Avoid_: edition, plan, SKU

**Grant**:
A feature or limit given to one account on top of its tier.
_Avoid_: add-on, override

**Entitlement**:
What an account may use: its tier plus its grants, minus the features an operator has switched off.
_Avoid_: licence, access

**Billing**:
The feature that turns entitlement enforcement on. While it is off, every account behaves as Enterprise.
_Avoid_: licensing, paywall

### Structure and processes

**Domain**:
A business area of infraweave, such as runs, policies or the registry, that owns its data and is reached by other domains only through its published interface.
_Avoid_: module, component, service

**Process mode**:
One way of running the single `infraweave` binary, such as `serve`, `scheduler`, `launcher` or `all`.
_Avoid_: role, service, component

**Scheduler**:
The process mode that consumes the work queue and fires schedules.
_Avoid_: drain, cron

**Launcher**:
The process mode that registers a pool's workers and starts their run containers.
_Avoid_: worker (for the process)

**Runner**:
The program that executes a run's phases inside the run container.
_Avoid_: worker binary, executor

### Execution

**Worker**:
A logical executor in a worker pool that runs one run at a time.
_Avoid_: agent, runner, launcher

**Public worker pool**:
The worker pool an operator runs for every account of an installation.
_Avoid_: shared pool, managed pool

**Offline worker**:
A worker that has stopped sending heartbeats for long enough that it receives no new work.
_Avoid_: dead worker, lost worker

**Lost run**:
A run that fails because its worker stayed offline past the run's tolerance.
_Avoid_: orphaned run, zombie run

### Access

**Role**:
A named set of permissions granted to an identity in a space.
_Avoid_: process role, permission set

### Registry

**Module**:
A versioned, reusable infrastructure package published to infraweave's module registry.
_Avoid_: using "module" for a domain

### Clients

**SDK**:
infraweave's client library for its REST API, on which every infraweave client is built.
_Avoid_: API client, bindings

**Provider**:
infraweave's Terraform and OpenTofu provider, covering Deploy and Flows.
_Avoid_: plugin

**Console**:
infraweave's web user interface.
_Avoid_: UI, dashboard, frontend, web app

### Languages

**Expression**:
A typed formula a user writes to read and shape data, evaluated by infraweave's own code rather than by code the user supplies.
_Avoid_: script, code, JavaScript

**Text template**:
A string with embedded expressions, rendered into text.
_Avoid_: Go template, interpolation string

**Policy**:
A Rego program that decides whether, or how, an action proceeds.
_Avoid_: rule, guard

**Infrastructure code**:
Code that runs on workers or agents: hooks, tasks, the IaC itself and Flows app code.
_Avoid_: script, expression

### Deploy and Flows

**Deploy**:
infraweave's infrastructure orchestration: stacks, runs, policies, state and the services around them.
_Avoid_: core, platform

**Flows**:
infraweave's event-driven automation, built on the same events as Deploy from the start.
_Avoid_: workflows, pipelines, automations

**Event**:
A record that something happened in infraweave; Deploy and Flows share one event model.
_Avoid_: message, notification

**Flows app**:
A packaged extension that contributes blocks to flows.
_Avoid_: plugin, integration

**Flows agent**:
The process mode that runs Flows apps for an agent pool.
_Avoid_: runtime, worker
