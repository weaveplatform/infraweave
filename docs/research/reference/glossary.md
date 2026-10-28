# Glossary

Checked against: reports 01 to 28 in this directory and the RES-02 to RES-07 drafters' notes, as they stand on 2026-10-28.

Research baseline: **2026-10-28**.

Terms used across this research set, in the sense Spacelift uses them. Where
infraweave uses a term differently, the infraweave sense follows. A term's
presence here does not mean infraweave implements it. Each meaning links
the report that defines the term; where several reports use it, the first
link is the defining one. Terms that several products share, such as agent
or drain, carry the product or component in brackets.

| Term | Meaning |
|---|---|
| Access propagation | A role binding applies to its space and every space below it; this is always on ([14](14-spaces-rbac-and-identity.md)) |
| Action | A value of the GraphQL `Action` enum (150 values), the smallest permission in custom RBAC, such as `STACK_CREATE` or `RUN_TRIGGER` ([03](03-deploy-core-api-inventory.md) section 9, [14](14-spaces-rbac-and-identity.md)) |
| Action resource | A Terraform resource that performs a one-off operation when created (`spacelift_run`, `spacelift_task`, `spacelift_version`, `spacelift_worker_pool_recycle`); its read does nothing and its `keepers` map forces a new operation ([05](05-terraform-provider.md)) |
| Active project | The Intent project that a caller's MCP or UI commands currently target (`isActive`, per caller) ([22](22-intelligence.md)) |
| Admin consent | The Microsoft Entra step that installs a Spacelift Azure integration's application into the customer's tenant ([12](12-cloud-integrations-and-oidc.md)) |
| Admin login | `/admin-login` on self-hosted, enabled by `ADMIN_USERNAME` and `ADMIN_PASSWORD`, for first setup and break-glass access; empty values disable it ([24](24-self-hosted-reference.md), [14](14-spaces-rbac-and-identity.md)) |
| Admin stack (liftoff) | `liftoff-admin`, a stack on a Spacelift-managed repository that applies the migration module liftoff generates ([25](25-other-integrations.md)) |
| Advisory lock | A PostgreSQL lock on a number the application chooses (`pg_advisory_lock`); OTF takes one so that only one replica runs each singleton subsystem ([27](27-reference-materials.md)) |
| Affected resource, related resource | In an audit entry, the resource the action changed, and an unchanged resource linked to it, such as the blueprint behind a stack ([19](19-events-notifications-audit.md)) |
| Agent (Flows) | A process that connects to the Flows gateway over WebSockets and runs app code and expressions in Node.js runtimes ([23](23-flows.md)) |
| Agent pool (Flows) | A named group of Flows agents with an ID and a token; public or private, and exposed to chosen projects ([23](23-flows.md)) |
| Agent pool (OTF, TFC) | A group of agent processes, `otf-agent` in OTF, that share pool tokens and run the jobs of workspaces set to agent execution ([27](27-reference-materials.md)) |
| AI integration | A stored, space-scoped connection to an LLM provider (Anthropic, OpenAI, Google or Bedrock) that powers Infra Assistant ([22](22-intelligence.md)) |
| App (Flows) | A versioned bundle (`app.js`, optionally `ui.js`) built on `@slflows/sdk` that provides blocks and an installation-level lifecycle ([23](23-flows.md)) |
| App installation | A configured instance of an app version in a Flows project; flows refer to it by ID ([23](23-flows.md)) |
| App registry | A JSON manifest of apps and versions that a Flows organization subscribes to; Core and Community are the public ones ([23](23-flows.md)) |
| Ask mode | The Infra Assistant mode that reads, navigates and searches without changing infrastructure ([22](22-intelligence.md)) |
| Assume role on worker | `generateCredentialsInWorker`: the private worker assumes the AWS role with its own credentials, which allows sessions of up to 12 hours ([12](12-cloud-integrations-and-oidc.md)) |
| Attachment | In the data model, a row in its own right that links a policy, context or cloud integration to a stack, module or Intent project, with its own ID and settings ([28](28-data-model.md), [05](05-terraform-provider.md)) |
| Attachment resource | A Terraform resource that links two objects, such as a context, policy, cloud integration or role binding to a stack, instead of a field on the stack ([05](05-terraform-provider.md)) |
| Audit trail | The account's log of every resource-changing action (`AuditTrailEntry`), kept 30 days; an Enterprise feature ([19](19-events-notifications-audit.md)) |
| Audit trail webhook | The one account-level endpoint that receives every audit event, optionally with run state changes (`includeRuns`) ([19](19-events-notifications-audit.md)) |
| Authorization (Flows) | An API key or OAuth2 grant bounded by capabilities, project access and MCP flow access ([23](23-flows.md)) |
| Authorization scheme | `MANAGED_USERS` (Identity Access Management) or `LOGIN_POLICIES`: whether users are invited and managed by Spacelift or admitted by login policies; exactly one is active ([14](14-spaces-rbac-and-identity.md)) |
| Auto-attachment | Attaching contexts, policies, cloud integrations or Intent policies to every stack or module with a matching label: `autoattach:<label>` on the attachable (`autoattach:*` matches all; `autoattach_read:` and `autoattach_write:` limit cloud integrations) ([13](13-configuration.md), [07](07-stack-model.md), [09](09-policies.md)) |
| Auto-registration | The Kubernetes worker-pool controller creating and owning a Spacelift worker pool through the API, from a `WorkerPool` resource that carries no credentials (`managedByK8sController`), using an API key with the Worker pool controller role ([10](10-workers-and-execution.md), [06](06-spacectl-and-clients.md)) |
| Availability state | infraweave's progress on a capability row: `implemented`, `partial`, `stub`, `missing` or `superseded`, computed from the served API and its tests ([26](26-scope-and-tiers.md) section 1.2) |
| `BillingTier` | The schema enum of 18 billing tiers across Spacelift's plan generations, from `FREE` to `V5_STRATEGIC`; an account reads its own through `tier` ([26](26-scope-and-tiers.md) section 2) |
| Binaries bucket | The bucket of the self-hosted AWS module that holds launcher binaries; the `downloads` bucket of version 2 installs ([24](24-self-hosted-reference.md)) |
| Block (Flows) | A node in a flow (a core block, an app block or a Spacelift Deploy block) with inputs, outputs, configuration and a lifecycle state ([23](23-flows.md)) |
| Blocker | The run currently holding a stack's lock, exposed as `Stack.blocker` ([08](08-run-lifecycle.md)) |
| Blocking run | A run that takes the stack's exclusive lock: tracked runs, tasks and destroy runs. `runConfirm` and `runDiscard` act on the blocking run when no run ID is given ([08](08-run-lifecycle.md), [03](03-deploy-core-api-inventory.md)) |
| Blueprint | A YAML body with inputs that creates one or more ordinary stacks once; the stacks are independent afterwards ([17](17-blueprints-and-templates.md)) |
| Blueprint schema | The draft-07 JSON Schema of a blueprint body, served by `blueprintSchema` ([17](17-blueprints-and-templates.md)) |
| Browser login | spacectl's `profile login` flow: the browser opens `/cli_login`, and Spacelift returns an encrypted token to a listener on localhost ([01](01-spacelift-landscape.md), [06](06-spacectl-and-clients.md)) |
| Build mode | The Infra Assistant mode that adds Intent resource, state and dependency operations, behind an admin setting and Intent policies ([22](22-intelligence.md)) |
| Built-in MQTT broker | The MQTT broker embedded in the self-hosted server, the alternative to AWS IoT Core; deprecated with the MQTT transport ([24](24-self-hosted-reference.md), [10](10-workers-and-execution.md)) |
| Bulk action | One action applied to a selection of entities in a list view, executed per entity ([21](21-resources-scans-search.md)) |
| Bundle (OPA) | A tarball of Rego policies and data that OPA reads from a file or polls from a service, optionally signed through `.signatures.json` ([27](27-reference-materials.md)) |
| Burst budget | Minutes per billing cycle that a contract allows above its worker limit; the complement of the billed percentile ([19](19-events-notifications-audit.md)) |
| BYOM | Bring your own model: an AI integration that uses the customer's own provider credentials and is billed by that provider ([22](22-intelligence.md)) |
| Capabilities (OPA) | The built-ins, keywords and network hosts a policy may use; using a built-in that the set leaves out fails at compile time ([27](27-reference-materials.md)) |
| Capability (Flows) | A permission name, such as `api` or `flows:edit`, that a Flows authorization carries; it doubles as an OAuth scope ([23](23-flows.md)) |
| celplate | Spacelift's CEL-based `${{ }}` template engine, used by blueprints and templates ([17](17-blueprints-and-templates.md)) |
| Change detection | Choosing the stacks a commit affects by comparing it with a base ref, as Terramate, Atlantis autoplan and Spacelift project globs do ([27](27-reference-materials.md)) |
| Change phase | `DRIFT`, `PLAN` or `APPLY`: the run phase an entity change belongs to ([21](21-resources-scans-search.md)) |
| Change record | `TemplateDeploymentChangeRecord`: the version and inputs a deployment had after one change; a revert target ([17](17-blueprints-and-templates.md)) |
| Change result | `PLANNED`, `SUCCESSFUL`, `FAILED`, `PARTIAL`, `UNPLANNED` or `UNKNOWN` for an entity change ([21](21-resources-scans-search.md)) |
| Cloud integration | A space-scoped object (AWS, Azure; GCP as a legacy form) that gives attached stacks and modules short-lived cloud credentials ([12](12-cloud-integrations-and-oidc.md)) |
| Cloud SQL Auth Proxy | The native sidecar the Helm chart adds on GKE to reach Cloud SQL over a private IP with IAM authentication ([24](24-self-hosted-reference.md)) |
| Compatible version | A published template version a deployment may move to: the same stack keys and no new required inputs ([17](17-blueprints-and-templates.md)) |
| Computed value | A configuration element known only at run time, injected by Spacelift or an integration (`runtime: true`) ([13](13-configuration.md)) |
| Concise output | The OpenTofu `-concise` flag, which trims progress lines; on by default from OpenTofu 1.7 ([20](20-vendors.md)) |
| Configuration element | An environment variable (`ENVIRONMENT_VARIABLE`) or mounted file (`FILE_MOUNT`) held by a stack, module or context; GraphQL `ConfigElement` and `ConfigInput` ([13](13-configuration.md), [05](05-terraform-provider.md)) |
| Configuration management view | The Ansible counterpart of the resources view: hosts, tasks and task executions ([21](21-resources-scans-search.md)) |
| Consumer (Flows) | A project role that may call HTTP endpoints, MCP servers and forms without seeing the flow's configuration ([23](23-flows.md)) |
| Context | A named bundle of configuration elements and hooks that attaches to stacks and modules ([13](13-configuration.md)) |
| Context attachment | The link between a context and a stack or module, with an integer priority (lower wins) and `isAutoattached` ([13](13-configuration.md)) |
| Contracted percentile | The percentile, such as P95, at which private worker concurrency is billed ([19](19-events-notifications-audit.md)) |
| Core block | A built-in Flows block type, named by `coreBlock` in the flow YAML ([23](23-flows.md)) |
| CPU profile | One pprof file per run state, captured through `TOFU_CPU_PROFILE` on OpenTofu 1.11 or later ([20](20-vendors.md)) |
| Cross-account sharing | Sharing a module with other Spacelift accounts by subdomain (`sharedAccounts`, `moduleShare` `accounts`) ([15](15-module-and-provider-registry.md)) |
| Custom workflow file | `.spacelift/workflow.yml`: Go-templated command lines, one per workflow key, validated during initialization ([20](20-vendors.md)) |
| Cycle (worker pool) | Sending every worker in a pool a signal to shut down (`workerPoolCycle`) ([10](10-workers-and-execution.md)) |
| Danger Zone | The Bruno folder for irreversible or account-wide overwriting requests; its eight requests outside any resource folder are the reference's untagged operations ([04](04-platform-api-inventory.md)) |
| Decision log (OPA) | One record per policy decision (`decision_id`, `input`, `result`, `bundles` and more), masked and batched before upload ([27](27-reference-materials.md)) |
| Deduplication key (runs) | Commit SHA plus run type, per stack: duplicate push and pull request events for one commit create one run ([11](11-vcs-integrations.md)) |
| Default integration | The VCS integration a stack or module uses when it names none; at most one per provider, always in the root space ([11](11-vcs-integrations.md)) |
| Default sanitization | Hashing every string value in plan policy input and the resources view ([20](20-vendors.md)) |
| Delta | Counts of resources a run, or a stack's blocking run, adds, changes, deletes, imports, moves and forgets, plus Ansible outcome counts ([21](21-resources-scans-search.md)) |
| DORA metrics | Deployment frequency, lead time for changes, change failure rate and time to recovery, computed from tracked runs ([21](21-resources-scans-search.md)) |
| Drain (self-hosted process) | The `spacelift backend drain` process (`drain-local` in the Helm chart) that consumes every message queue and, from v6.4.0, fires cron jobs ([24](24-self-hosted-reference.md)) |
| Drain (worker) | Marking a worker as accepting no new work (`workerDrainSet`) ([10](10-workers-and-execution.md)) |
| Drift detection | Scheduled proposed runs (`Run.driftDetection = true`) that compare a stable stack with its infrastructure ([18](18-scheduling-drift-dependencies.md)) |
| Drift detection run limit | `WorkerPool.driftDetectionRunLimit`: the cap on concurrent drift detection runs in one private pool ([18](18-scheduling-drift-dependencies.md), [10](10-workers-and-execution.md)) |
| Encryption type | `ENCRYPTION_TYPE`, `kms` or `rsa`; fixed at install ([24](24-self-hosted-reference.md)) |
| Enterprise+ | The top plan on the pricing page, which carries self-hosted, FedRAMP, on-premises and air-gapped installs; `V5_ENTERPRISE_PLUS` in the schema ([26](26-scope-and-tiers.md) section 2) |
| Entity change | A per-entity record of what a run's drift, plan or apply phase did (`EntityChangeV2`, `EntityChangeV3`), with its type and result ([21](21-resources-scans-search.md)) |
| Event (Flows) | A message a block output emits; its parent link forms the invocation's lineage ([23](23-flows.md)) |
| Executor (OTF) | How an OTF runner starts a job: `fork`, a child process, or `kubernetes`, one Job per job ([27](27-reference-materials.md)) |
| Export to template | The UI conversion of a published blueprint into a new template with a draft version `0.1` ([17](17-blueprints-and-templates.md)) |
| External dependency | `RunExternalDependency`: a named marker a push policy puts on a run, which keeps the run `QUEUED` until it is marked `FINISHED` or `SKIPPED` ([18](18-scheduling-drift-dependencies.md), [08](08-run-lifecycle.md), [09](09-policies.md)) |
| External ID | The `sts:ExternalId` Spacelift sends when assuming an AWS role: `<account>@<integration-id>@<stack-or-module>@<read or write>` on shared workers, or a custom value in worker mode ([12](12-cloud-integrations-and-oidc.md)) |
| External integration | `ExternalIntegration`: a typed, space-scoped integration with its own API key and a configuration union; ServiceNow is the only type ([25](25-other-integrations.md)) |
| External state access | A per-stack switch that lets processes outside the stack's own runs read its managed state; off by default and read-only ([16](16-state-management.md)) |
| Federation | A linked external identity (GitHub, Slack, Teams, SSO) on a user's `Identity` ([14](14-spaces-rbac-and-identity.md)) |
| FedRAMP environment | Spacelift's FedRAMP Moderate SaaS in AWS `us-east-2`, which runs on private workers only ([26](26-scope-and-tiers.md) section 2) |
| Field route | A REST route for a field of an object type rather than a root field, such as a stack's runs. The reference has 103 ([02](02-graphql-to-rest-mapping.md)) |
| Flag reset | The `reset_flag` rule, which removes a flag only when every policy that set it agrees; policies without a `PolicyULID` cannot use it ([09](09-policies.md)) |
| Flow | A graph of blocks in a Flows project that processes events; it has an enabled flag and a monthly event limit ([23](23-flows.md)) |
| Flow definition | The YAML form of a flow, used by `flowctl flow export` and `terraform-provider-flows` ([23](23-flows.md)) |
| flowctl | The Flows CLI (`@useflows/flowctl`), for custom app versions and flow export ([23](23-flows.md)) |
| Flows | Spacelift's event-driven workflow product, separate from Spacelift Deploy, at `useflows.eu`, `useflows.us` or self-hosted ([23](23-flows.md)) |
| Flows project role binding | A Spacelift Deploy grant of a space role to a Flows project ([23](23-flows.md), [14](14-spaces-rbac-and-identity.md)) |
| Force apply | Applying a tracked run despite a plan with no changes (`ForceApplyMode` `SINGLE`), optionally also for its dependants (`CASCADE`) ([08](08-run-lifecycle.md), [18](18-scheduling-drift-dependencies.md)) |
| Gateway (Flows) | The self-hosted Flows component that agents connect to ([23](23-flows.md)) |
| Global queue (OTF) | OTF's pool-wide queue of run phases (plan, apply) waiting for a runner, separate from each workspace's queue of runs ([27](27-reference-materials.md)) |
| GPG key (registry) | An account-level public key a root admin registers; it signs provider `SHA256SUMS` files ([15](15-module-and-provider-registry.md)) |
| Grouping key | `Notification.groupingKey`: identifies repeats of one issue, which collapse into one notification with an occurrence count ([19](19-events-notifications-audit.md)) |
| Handover | The server-to-worker transfer that happens in the `PREPARING`, `PREPARING_APPLY` and `PREPARING_REPLAN` states ([08](08-run-lifecycle.md)) |
| Head commit | The commit a stack would apply, set by `track` in a push policy ([11](11-vcs-integrations.md)) |
| Hook | A list of shell commands run before or after a run phase, from `before_init` to `after_run` ([07](07-stack-model.md), [08](08-run-lifecycle.md)) |
| HTTP long-poll transport | Workers make HTTPS requests that the server holds open until work arrives; it replaces MQTT ([10](10-workers-and-execution.md)) |
| IdP group mapping | A `ManagedUserGroup`: an identity-provider group name, matched exactly against the groups in a login assertion ([14](14-spaces-rbac-and-identity.md)) |
| Ignored trigger | A record of a VCS push to a tracked branch that created no run, with its reason; kept seven days ([08](08-run-lifecycle.md)) |
| Infra Assistant | The AI chat panel in the Spacelift UI, with Ask and Build modes ([22](22-intelligence.md)) |
| Input capture | `PolicyInputCapture`: a stored real input per policy type, with the values the contract marks sensitive removed ([09](09-policies.md)) |
| In-run token | The JWT Spacelift injects as `SPACELIFT_API_TOKEN` into runs of stacks that have roles: `iss` is `spacelift`, `aud` the API base URL and `sub` a path naming the stack; valid three hours and only in `PLANNING`, `APPLYING` and `PERFORMING` ([05](05-terraform-provider.md), [01](01-spacelift-landscape.md), [13](13-configuration.md)) |
| Integration attachment | The link between a cloud integration and one stack or module, flagged `read`, `write` or both; a stack has at most one read and one write attachment ([12](12-cloud-integrations-and-oidc.md)) |
| Integration key | An admin API key for one space, created for an external integration such as Backstage, that uses no API key seat and issues short-lived tokens ([25](25-other-integrations.md), [06](06-spacectl-and-clients.md)) |
| Intent | Natural-language provisioning that calls provider schemas and operations directly, inside an Intent project ([22](22-intelligence.md)) |
| Intent operation | One lifecycle action on an Intent resource, with policy results, reviews and a state: `PENDING`, `PENDING_REVIEW`, `READY_FOR_EXECUTION`, `FINISHED` or `FAILED` ([22](22-intelligence.md)) |
| Intent policy | The `INTENT` policy type: `allow` and `deny` rules over a resource operation; when no rule matches, the operation waits for review ([22](22-intelligence.md), [09](09-policies.md)) |
| Intent project | The governed container for Intent resources, with their state, history, policies, integrations, environment, worker pool and runner image ([22](22-intelligence.md)) |
| Intent resource | One resource, or one `spacelift/module` deployment, held in an Intent project's state ([22](22-intelligence.md)) |
| Intent to IaC | Exporting an Intent project to HCL (`main.tofu`), optionally with an OpenTofu stack holding the imported state; the project becomes `READ_ONLY` ([22](22-intelligence.md)) |
| Invocation | The tree of events descended from one root event, with the status In progress, Failed in progress, Processed or Failed ([23](23-flows.md)) |
| Keepers | A provider map attribute whose change forces replacement of an action resource ([05](05-terraform-provider.md)) |
| Launcher | The program that registers workers, receives jobs and starts run containers: a Go binary on VMs, or the controller with the launcher image on Kubernetes ([10](10-workers-and-execution.md)) |
| Lease (Kubernetes) | A `coordination.k8s.io` object; Burrito uses one per layer as a lock ([27](27-reference-materials.md)) |
| Legacy AWS integration | An AWS integration bound to one stack, created by `stackIntegrationAwsCreate` or `spacelift_aws_role` (`legacy: true`) ([12](12-cloud-integrations-and-oidc.md)) |
| Legacy fallback | The legacy space access level (Reader, Writer, Admin, from `SpaceAccessLevel`) that grants an action when no custom role does ([14](14-spaces-rbac-and-identity.md), [03](03-deploy-core-api-inventory.md)) |
| `legacy` space | The space that keeps pre-spaces accounts working; access policies applied only there ([14](14-spaces-rbac-and-identity.md)) |
| Licence token | The JWT licence (`LICENSE_TYPE=jwt`, `LICENSE_TOKEN`) with a validity window, which replaces AWS License Manager ([24](24-self-hosted-reference.md)) |
| liftoff | Spacelift's CLI for migrating from Terraform Cloud and Enterprise; it generates OpenTofu that the Spacelift provider applies through an admin stack ([25](25-other-integrations.md)) |
| Local preview | A proposed run, or a module test run, built from an uploaded copy of the local working tree rather than a VCS commit (`uploadLocalWorkspace`, then `runProposeLocalWorkspace`; `spacectl stack local-preview` and `spacectl module local-preview`) ([06](06-spacectl-and-clients.md), [07](07-stack-model.md), [15](15-module-and-provider-registry.md)) |
| Locked by template deployment | `Stack.isLockedByTemplateDeployment`: the stack is read-only because an active deployment manages it ([17](17-blueprints-and-templates.md)) |
| Logical queue | One of the eight self-hosted message queues: `async-jobs`, `async-jobs.fifo`, `cronjobs`, `dlq`, `dlq.fifo`, `events-inbox`, `iot` and `webhooks` ([24](24-self-hosted-reference.md)) |
| Magic label | A stack label with built-in behaviour, such as `feature:*`, `ghenv:*`, `infracost`, `terragrunt` or `folder:*` ([07](07-stack-model.md)) |
| Managed entity | One resource, output, module or other item a stack's state holds, as the resources view lists it (`ManagedEntity`) ([21](21-resources-scans-search.md)) |
| Managed or unmanaged (scan) | Whether a discovered cloud resource's identity matches an entity in some stack's state ([21](21-resources-scans-search.md)) |
| Managed state | The OpenTofu and Terraform state backend Spacelift hosts, chosen when the stack is created ([16](16-state-management.md)) |
| Managed state version | One entry in a stack's state history; may be an import or a rollback ([16](16-state-management.md)) |
| Maps | A two-level lookup table in a blueprint or template body, read as `maps[...]` in expressions ([17](17-blueprints-and-templates.md)) |
| `mcp:read`, `mcp:write` | The two OAuth scopes of Spacelift's MCP server: read gives `discover`, `query` and `provider`; write adds `mutate` and `intent` ([22](22-intelligence.md)) |
| Mocked outputs | Placeholder Terragrunt dependency outputs, which can appear in plans and in plan policy input ([20](20-vendors.md)) |
| Module | A stack-like entity that tracks a repository, runs test cases and publishes versions to the private module registry; it holds no state ([15](15-module-and-provider-registry.md)) |
| Module version | `Version`: one released `major.minor.patch` of a module, in state `PENDING`, `ACTIVE` or `FAILED`, possibly yanked ([15](15-module-and-provider-registry.md)) |
| MQTT transport | Server push to workers through AWS IoT Core or a broker built into the server, on per-worker `readonly` and `writeonly` topics; retired on 2027-03-30 ([10](10-workers-and-execution.md), [24](24-self-hosted-reference.md)) |
| Named webhook | A space-scoped HTTP endpoint (`NamedWebhooksIntegration`) that notification policy `webhook` rules address by slug ([19](19-events-notifications-audit.md)) |
| Native OpenTofu migration | The queued conversion of Terraform stacks that use the `OPEN_TOFU` workflow tool into stacks of the `OpenTofu` vendor, with rollback ([20](20-vendors.md)) |
| Negative test case | A module test case marked `negative: true`, expected to fail; exposed as `Run.expectFailure` ([15](15-module-and-provider-registry.md), [08](08-run-lifecycle.md)) |
| Notification inbox | The admin-only list of `Notification` records about failed interactions with external systems and messages policies create; kept 14 days ([19](19-events-notifications-audit.md)) |
| Notification policy | A space-level `NOTIFICATION` policy whose `inbox`, `slack`, `webhook` and `pull_request` rules route and rewrite notifications ([19](19-events-notifications-audit.md)) |
| OAuth redirect allowlist | The account-level list of extra `https` hosts allowed as MCP OAuth redirect targets ([22](22-intelligence.md)) |
| OIDC API key | An API key exchanged with a JWT from an external identity provider whose `iss`, `aud` and `sub` match the key's settings ([14](14-spaces-rbac-and-identity.md), [01](01-spacelift-landscape.md)) |
| OIDC federation token | The one-hour JWT each run of a paid account receives in `SPACELIFT_OIDC_TOKEN` and in `/mnt/workspace/spacelift.oidc`, for exchange with a cloud or Vault ([12](12-cloud-integrations-and-oidc.md)) |
| Output reference | `StackDependencyReference`: maps an upstream stack's output (`outputName`) to a downstream input (`inputName`) ([18](18-scheduling-drift-dependencies.md)) |
| Override | A hand-curated route or exclusion in `api/reference/spacelift/rest-mapping.yaml`, for a root field no rule can map ([02](02-graphql-to-rest-mapping.md)) |
| Parity oracle | A pinned upstream artifact that infraweave's surface is measured against: the specification reference for the API, and `terraform-schema.json` for the provider ([01](01-spacelift-landscape.md)) |
| Passive state | A run state in which nothing executes on the server's side ([08](08-run-lifecycle.md)) |
| Passthrough import | A Terraform importer that stores the given import ID unchanged as the resource ID ([05](05-terraform-provider.md)) |
| Pending review | The Intent policy outcome when no rule matches: the operation waits for a manual `ALLOW` or `DENY` ([22](22-intelligence.md)) |
| Personal API key | An API key owned by a user, carrying that user's access and seat ([14](14-spaces-rbac-and-identity.md)) |
| Phase spine | The provisional order of infraweave's delivery phases, `P0` to `P27`, that report 26 gives each capability; the implementation plan confirms or changes it ([26](26-scope-and-tiers.md) section 1.4) |
| Plan store (Atlantis) | The Atlantis interface that keeps plan files between plan and apply, on local disk or in S3-compatible storage ([27](27-reference-materials.md)) |
| Plugin | An installation of a plugin template into a space, with parameter values and a stack label; the resources it manages are read-only and are deleted with it ([25](25-other-integrations.md)) |
| Plugin output | Markdown a plugin uploads during a run through a signed URL, shown on the run's Plugin Outputs tab ([25](25-other-integrations.md)) |
| Plugin template | A YAML manifest in the spaceforge schema that defines parameters, contexts, policies and webhooks; global templates come from Spacelift and custom ones from users ([25](25-other-integrations.md)) |
| Policy contract | Spacelift's published JSON Schema of each policy type's input and rules, at `https://app.spacelift.io/.well-known/policy-contract.json` ([09](09-policies.md)) |
| Policy flag | An arbitrary string a policy attaches to a run; policies of other types that run later read it in `run.flags` ([09](09-policies.md)) |
| Policy receipt | `RunPolicyReceipt`: the record of one policy evaluation that affected a run, with its outcome, flags and whether it was sampled ([09](09-policies.md)) |
| Policy set enforcement level | The outcome a Terrakube or Terraform Cloud policy carries: advisory, soft-mandatory or hard-mandatory ([27](27-reference-materials.md)) |
| Policy template | `PolicyTemplate`: an example policy from Spacelift's policy library that a user can import ([09](09-policies.md)) |
| Policy type | A `PolicyType` value; it decides where a policy is evaluated, which rules it may define and which input it receives ([09](09-policies.md)) |
| Policy workbench | The UI for replaying stored samples against an edited policy body ([09](09-policies.md)) |
| Postgres queue | `MESSAGE_QUEUE_TYPE=postgres`: the database-backed alternative to SQS for the logical queues ([24](24-self-hosted-reference.md)) |
| Potential merge commit | GitHub's virtual merge of a mergeable pull request into its base, which Spacelift checks out for pull request runs ([11](11-vcs-integrations.md)) |
| Preamble | Code Spacelift prepends to each policy: the `result` object, rule defaults and empty set placeholders ([09](09-policies.md)) |
| Predicate | One field filter in a search: a field, a constraint and an optional exclusion ([21](21-resources-scans-search.md)) |
| `PrepareForEval` (OPA) | Compiling a Rego query once into a prepared query that can be evaluated many times, safely from several goroutines ([27](27-reference-materials.md)) |
| `private://` address | `private://<pool>/<path>`: an integration host whose traffic goes through a VCS agent pool ([11](11-vcs-integrations.md)) |
| Private worker pool | A customer-run worker pool authenticated by a certificate signed from the customer's CSR and a private key the customer keeps ([10](10-workers-and-execution.md)) |
| Profile | A named set of stored spacectl credentials (an endpoint plus an API key, GitHub token or API token) in `config.json`; one is current, and `SPACELIFT_PROFILE` overrides it for a process ([06](06-spacectl-and-clients.md)) |
| Project globs | Extra path patterns whose changes trigger a stack's runs without being mounted into them ([07](07-stack-model.md)) |
| Project lock | Exclusive write access to an Intent project for one session, released after about five minutes without activity ([22](22-intelligence.md)) |
| Project root | The directory inside the repository where the vendor runs ([07](07-stack-model.md)) |
| Provider type | The unique name of a provider in an account, lowercase letters only; also its ID ([15](15-module-and-provider-registry.md)) |
| Provider verification | `terraformProviderVersionVerify`: checks the `SHA256SUMS` signature, platform coverage and archive checksums; the result is informational ([15](15-module-and-provider-registry.md)) |
| Provider version status | `DRAFT`, `PUBLISHED` or `REVOKED` ([15](15-module-and-provider-registry.md)) |
| Prune allowlist | Kubernetes pruning of resources labelled `spacelift-stack=<slug>` that have left the repository ([20](20-vendors.md)) |
| Public module or provider | A module or provider anyone may use, including people outside Spacelift; irreversible for modules ([15](15-module-and-provider-registry.md)) |
| Public worker pool | The Spacelift-run pool every account has; its concurrency is `PublicWorkerPool.parallelism` ([10](10-workers-and-execution.md)) |
| Raw Git | The one-way `GIT` provider: a public Git URL, with no webhooks and manual sync ([11](11-vcs-integrations.md)) |
| Reconciliation run | The tracked run drift detection starts when it finds drift and `reconcile` is on; not available for CloudFormation ([18](18-scheduling-drift-dependencies.md)) |
| Reference architecture | Spacelift's self-hosted install method from version 3: Terraform or OpenTofu modules for ECS, EKS, AKS and GKE plus a Helm chart, on any container platform ([24](24-self-hosted-reference.md)) |
| Rego v0, Rego v1 | The two OPA language dialects a Spacelift policy can use, chosen by `engineType` (`REGO_V0`, `REGO_V1`). In OPA, v0 is the syntax before OPA 1.0 and v1 the default since; a v2 that adds `and` and `or` is unreleased at the pinned OPA commit ([09](09-policies.md), [27](27-reference-materials.md)) |
| Reset (worker pool) | Issuing a pool new credentials from a new CSR (`workerPoolReset`) ([10](10-workers-and-execution.md)) |
| Resource inheritance | The per-space `inheritEntities` setting: the child space can use its parent's attachable objects, and its users get Read on the parent ([14](14-spaces-rbac-and-identity.md)) |
| Resources view | The account-level and stack-level inventory of managed entities, grouped by stack, type or provider ([21](21-resources-scans-search.md)) |
| `ro_` and `wo_` prefixes | Variable name prefixes that limit a variable to the read stages or to the applying stage ([13](13-configuration.md), [10](10-workers-and-execution.md)) |
| Role binding | A grant of a role in a space to a user, IdP group, API key, stack or Flows project ([14](14-spaces-rbac-and-identity.md)) |
| Root field | A field of the GraphQL `Query` or `Mutation` type. Spacelift has 665 ([01](01-spacelift-landscape.md)) |
| Root space admin | Space admin on `root`, which holds account-wide administration: SSO, VCS, users, roles, API keys, sessions and the audit trail ([14](14-spaces-rbac-and-identity.md)) |
| RPC-shaped | A mutation served as `POST /<operation>` because no rule or override gives it a resource shape. The reference allows none ([02](02-graphql-to-rest-mapping.md)) |
| Run observability bucket | The bucket for worker traces, CPU profiles and JSON logs (`OBJECT_STORAGE_BUCKET_RUN_OBSERVABILITY`) ([24](24-self-hosted-reference.md), [10](10-workers-and-execution.md)) |
| Run priority preset | `high`, `normal` or `low`: the scheduling tier of a run waiting in a private worker pool's queue ([08](08-run-lifecycle.md)) |
| Run promotion | Creating a tracked run for the commit of a proposed run; `githubActionDeploy` enables it ([08](08-run-lifecycle.md), [07](07-stack-model.md)) |
| Run summary | An Infra Assistant explanation of one run phase (Summarize) or of a whole failed run (Explain) ([22](22-intelligence.md)) |
| Run-all | The Terragrunt mode that runs each command across all units in dependency order; it forces unit-prefixed resource names ([20](20-vendors.md)) |
| Run-minutes | The durations between consecutive run state changes, counted on the UTC day each state began ([19](19-events-notifications-audit.md)) |
| Runner image | The container image a run executes in; by default `runner-terraform`, `runner-opentofu` or `runner-ansible` ([10](10-workers-and-execution.md), [20](20-vendors.md)) |
| Runtime configuration | `.spacelift/config.yml` at the repository root, read per run and able to override stack settings; also the per-run YAML spacectl sends with `runTrigger` as `runtimeConfig.yaml` ([07](07-stack-model.md), [13](13-configuration.md), [06](06-spacectl-and-clients.md)) |
| Sample (policy) | A stored policy evaluation (input, body and result), recorded when the policy's `sample` rule is true ([09](09-policies.md)) |
| Saved filter | A named set of filters, search text and sort order for one list view, private or shared with the account ([21](21-resources-scans-search.md)) |
| Scan | A per-space configuration that discovers cloud resources through a cloud integration on a worker pool; known only from the schema and Bruno ([21](21-resources-scans-search.md), [04](04-platform-api-inventory.md)) |
| Scan run | One execution of a scan, with its state, discovered resources and non-fatal failures ([21](21-resources-scans-search.md)) |
| Scan scope | One resource type a scan lists, with the list call's parameters and a filter applied after listing ([21](21-resources-scans-search.md)) |
| Scheduled delete | `ScheduledDelete`: a one-off time at which a stack is deleted, optionally after a destroy run (`shouldDeleteResources`) ([18](18-scheduling-drift-dependencies.md)) |
| Scheduled run | `ScheduledRun`: a per-stack schedule, a cron list or one timestamp, that creates tracked runs, optionally with a custom runtime configuration ([18](18-scheduling-drift-dependencies.md)) |
| Scheduled task | `ScheduledTask`: a per-stack schedule that runs one command as a task ([18](18-scheduling-drift-dependencies.md)) |
| Scheduler (self-hosted process) | The older standalone cron process (`spacelift scheduler`), deployed only below v6.4.0 and in CloudFormation installs ([24](24-self-hosted-reference.md)) |
| Search input | `SearchInput`: the shared request of every paged list, with paging, full-text search, predicates and ordering ([21](21-resources-scans-search.md)) |
| Search suggestions | The twin of a search field that returns the filterable and orderable fields and their values with counts ([21](21-resources-scans-search.md)) |
| Secret header | A write-only request header stored on a named webhook; its key is readable, its value never returned ([19](19-events-notifications-audit.md)) |
| Served API | infraweave's own API, `api/deploy.openapi.yaml`, built from `api/src/deploy/` fragments. It starts from the reference and records each divergence ([02](02-graphql-to-rest-mapping.md)) |
| Server (self-hosted process) | The `spacelift backend server` process: the UI, the GraphQL and REST APIs, webhooks and the built-in MQTT broker ([24](24-self-hosted-reference.md)) |
| Service account (Flows) | A machine user with roles and API keys, created by organization admins ([23](23-flows.md)) |
| Service discovery | `/.well-known/terraform.json`, which names the `login.v1`, `modules.v1`, `providers.v1` and state endpoints ([15](15-module-and-provider-registry.md)) |
| Session access snapshot (PSA) | The per-space access computed at login and stored with a session ([14](14-spaces-rbac-and-identity.md)) |
| Session tagging | STS session tags `run`, `stack` and `space`, set by `tagAssumeRole` on native integrations or the `feature:aws_oidc_session_tagging` label under OIDC ([12](12-cloud-integrations-and-oidc.md)) |
| Signal (Flows) | A value a block publishes through its lifecycle for other blocks to reference ([23](23-flows.md)) |
| Signed run | A run whose user-provided metadata carries a signature that a worker-side initialization policy verifies ([10](10-workers-and-execution.md)) |
| `SKIP LOCKED` | A PostgreSQL row-locking clause that passes over rows other transactions hold; River fetches jobs with it ([27](27-reference-materials.md)) |
| Skip replan | A Terragrunt setting that applies the saved plan instead of planning again; mocked outputs are then applied ([20](20-vendors.md)) |
| Slug | The readable `id`, unique in the account, that stacks, modules, spaces, contexts, policies, templates and blueprints carry. A stack's slug comes from its name at creation or from `stackCreate(slug:)`, and only `stackReslug` changes it ([07](07-stack-model.md), [28](28-data-model.md)) |
| Smart sanitization | Hashing only the values the tool marks sensitive, instead of every string ([20](20-vendors.md)) |
| Space | A node in the account's single tree rooted at `root`; the unit of authorization scope ([14](14-spaces-rbac-and-identity.md)) |
| Space availability | Sharing a module with chosen spaces (`moduleShare` `spaces`; Terraform `space_shares`) ([15](15-module-and-provider-registry.md)) |
| Space path | The `spacePath` claim: the path of space IDs from `root` to the run's space ([12](12-cloud-integrations-and-oidc.md)) |
| spaceforge | Spacelift's MIT-licensed Python SDK that generates plugin manifests from a Python class and runs plugin hooks inside a run ([25](25-other-integrations.md)) |
| Space-level integration | A VCS integration that is not the default, usable only from its space and spaces that inherit from it ([11](11-vcs-integrations.md)) |
| Spacelift environment | The `TF_VAR_spacelift_*` metadata, API token and OIDC token Spacelift injects into runs ([13](13-configuration.md)) |
| Spacelift Intelligence | The family of Spacelift's AI features: AI integrations, Infra Assistant with run summaries, Intent and the MCP server ([22](22-intelligence.md)) |
| Spacelift MCP server | The remote MCP endpoint at `/mcp`, with the tools `discover`, `query`, `provider`, `mutate` and `intent` ([22](22-intelligence.md)) |
| `spacelift-intent` | An Apache-2.0 standalone MCP server over stdio that drives provider plugins directly and keeps state in SQLite; its repository is archived ([22](22-intelligence.md), [01](01-spacelift-landscape.md)) |
| spacelift-promex | Spacelift's Prometheus exporter, which turns GraphQL reads into gauges: 22 metric series plus `spacelift_error` ([19](19-events-notifications-audit.md)) |
| Spacelift-provided integration | The managed default AI integration (Bedrock serving Claude models), SaaS only, with a token budget on the free tier ([22](22-intelligence.md)) |
| Specification reference | `api/reference/spacelift/spacelift.openapi.yaml`, the REST projection of Spacelift's GraphQL API. A reference for the served API, not that API ([01](01-spacelift-landscape.md)) |
| Stack | A unit that pairs source code (repository, branch, project root) with the state it manages and its configuration, isolated from other stacks ([07](07-stack-model.md)) |
| Stack dependency | `StackDependency`: a directed edge that orders tracked runs from an upstream stack to a downstream one ([18](18-scheduling-drift-dependencies.md)) |
| Stack destructor | `spacelift_stack_destructor`: a Terraform resource whose deletion destroys a stack's managed resources and then deletes the stack ([05](05-terraform-provider.md)) |
| Stack key | The body-local identifier of a stack, used by `depends_on` and `from_stack` ([17](17-blueprints-and-templates.md)) |
| Stack label (plugin) | A plugin's `labelIdentifier`; stacks carrying it receive the plugin's managed resources through auto-attachment ([25](25-other-integrations.md)) |
| Stack lock | A user's exclusive lock on a stack, distinct from the run lock `Stack.blocker` describes and from state workspace locks ([07](07-stack-model.md)) |
| Stack resolution | How spacectl picks a stack when `--id` is absent: from `--run` through `runStack`, or from the Git repository name and project root through `searchStacks` ([06](06-spacectl-and-clients.md)) |
| Stack webhook | The older per-stack or per-module `WebhooksIntegration`, which sends every run state change ([19](19-events-notifications-audit.md)) |
| Starter+ | The lowest paid SaaS plan on Spacelift's pricing page; docs notices gate features on "Starter+ and above". It maps to `V4_STARTER_PLUS`, `V5_STARTER_PLUS` and `V5_STARTER_PLUS_2` ([26](26-scope-and-tiers.md) section 2) |
| State rollback | Making an older state version current again by writing it as a new version ([16](16-state-management.md)) |
| State version | A counter that tells repeated visits to one state within a run apart; logs are keyed by it ([08](08-run-lifecycle.md)) |
| Stray instance | A cloud instance with no matching Spacelift worker for more than 10 minutes; the autoscaler terminates it ([10](10-workers-and-execution.md)) |
| Stub | In infraweave, a tag or row whose every operation is routed by the generated server and answers 501, with no behaviour behind it ([26](26-scope-and-tiers.md) section 1.2) |
| Subject template | The account-level format of the OIDC `sub` claim, built from placeholders such as `{spaceId}` and `{spacePath}` ([12](12-cloud-integrations-and-oidc.md)) |
| Sub-tag | A Bruno sub-folder under a tag, such as Stacks: Webhooks, used to group operations in reports 03 and 04 ([03](03-deploy-core-api-inventory.md)) |
| Superseded | In infraweave, a Spacelift interface that a newer one replaces, with evidence, so infraweave does not build it ([26](26-scope-and-tiers.md) section 7) |
| Sync window (Burrito) | A window, per action, that allows or blocks it, defined by a cron schedule and a duration ([27](27-reference-materials.md)) |
| Targeted replan | Re-planning an unconfirmed tracked run, limited to chosen resource addresses ([08](08-run-lifecycle.md)) |
| Task queue (Temporal) | A named queue that Temporal workers long-poll for workflow and activity tasks ([27](27-reference-materials.md)) |
| Teleport `spacelift` join method | Teleport's bot join method that trusts a Spacelift-signed ID token, filtered by space, caller type and caller ID ([25](25-other-integrations.md)) |
| Template | The versioned, managed successor to blueprints: a container of template versions (GraphQL `Template`; RBAC calls it the versioned blueprint group) ([17](17-blueprints-and-templates.md)) |
| Template bucket | The S3 bucket where a CloudFormation stack's processed templates are uploaded, under a prefix of the run ID ([20](20-vendors.md)) |
| Template deployment | An instance of a template version with input values; it owns its stacks, which stay read-only while it exists ([17](17-blueprints-and-templates.md)) |
| Template schema | The JSON Schema of a template body, served by `templateSchema`: `stacks` only, a top-level `space` and `scheduled_delete`, and `vcs.reference` ([17](17-blueprints-and-templates.md)) |
| Template version | One body of a template, `DRAFT` or `PUBLISHED`; publishing is one-way and pins the VCS reference to a commit ([17](17-blueprints-and-templates.md)) |
| Templates Workbench, Templates List | The template UIs for authors and for end users ([17](17-blueprints-and-templates.md)) |
| Terminal state | A run state nothing follows: `FINISHED`, `FAILED`, `CANCELED`, `DISCARDED`, `STOPPED` or `SKIPPED` ([08](08-run-lifecycle.md)) |
| Terragrunt tool | `TerragruntTool`: the binary Terragrunt drives (`OPEN_TOFU`, `TERRAFORM_FOSS`, `MANUALLY_PROVISIONED`), found through `TG_TFPATH`; the API input defaults to `TERRAFORM_FOSS` ([20](20-vendors.md)) |
| Test case | One entry in `tests` of a module's `.spacelift/config.yml`; it runs as a `TESTING` run that applies and then destroys ([15](15-module-and-provider-registry.md), [08](08-run-lifecycle.md)) |
| Test deployment | A deployment of type `TEST`, which may use a draft version ([17](17-blueprints-and-templates.md)) |
| Tier feature | A value of `BillingTierFeature`, the schema enum of 31 features that an account's billing tier switches on; an account reads its own list through `tierFeatures`, and no field maps a tier to its features ([26](26-scope-and-tiers.md) section 3, [03](03-deploy-core-api-inventory.md)) |
| Token scope | The OIDC `scope` claim, `read` or `write`, derived from run type and phase ([12](12-cloud-integrations-and-oidc.md)) |
| Tracked branch | The branch a stack follows; pushes to it that affect the stack create tracked runs by default ([07](07-stack-model.md), [11](11-vcs-integrations.md)) |
| Transactional enqueue | Inserting a job in the same database transaction as the data it refers to, so both commit or neither does ([27](27-reference-materials.md)) |
| Trigger always | `triggerAlways` on a dependency or reference: the downstream stack runs even when the referenced outputs did not change ([18](18-scheduling-drift-dependencies.md)) |
| Trigger chain | The recursive `run.trigger` object in policy input, naming upstream runs and stacks, up to 20 levels ([18](18-scheduling-drift-dependencies.md)) |
| Undecided | The approval policy outcome when `approve` and `reject` are both false or both true ([09](09-policies.md)) |
| Usage data exporter | `self-hosted-usage-data-exporter`: pulls `/selfhosted/metrics` and can upload the result to Spacelift ([24](24-self-hosted-reference.md), [19](19-events-notifications-audit.md)) |
| Usage export | A CSV of worker counts or run-minutes from `/usageanalytics/csv`; on self-hosted, also JSON table exports sent to Spacelift ([19](19-events-notifications-audit.md), [24](24-self-hosted-reference.md)) |
| User-provided metadata (run metadata) | Opaque strings attached to a run when it is created or confirmed, visible to policies; spacectl sends them in the `Spacelift-User-Provided-Run-Metadata` header ([08](08-run-lifecycle.md), [06](06-spacectl-and-clients.md)) |
| V1 twin | A superseded root field whose `V2` (or `v2`) form replaces it; the reference drops the V1 form ([26](26-scope-and-tiers.md) section 7.5) |
| v2-to-v3 kit | `self-hosted-v2-to-v3-kit`: generates Terraform imports that move a CloudFormation install onto the modules ([24](24-self-hosted-reference.md)) |
| VCS agent pool | A named group of VCS agents that carry Spacelift's calls to a private VCS over a gRPC stream ([11](11-vcs-integrations.md)) |
| VCS check | A commit status or check run Spacelift writes for a run: `INDIVIDUAL` per stack, `AGGREGATED` as `spacelift/tracked`, `spacelift/proposed` and `spacelift/modules`, or `ALL` ([11](11-vcs-integrations.md)) |
| VCS Gateway | The Spacelift service that VCS agents connect to over gRPC; on self-hosted, an optional `spacelift backend vcs-gateway` process ([11](11-vcs-integrations.md), [24](24-self-hosted-reference.md)) |
| VCS integration | A configured connection to one source control host (GitHub App, GitLab, Bitbucket Cloud or Data Center, Azure DevOps), scoped to a space ([11](11-vcs-integrations.md)) |
| Vendor | The IaC tool family a stack drives, one of the seven `StackVendor` values, expressed by the concrete type of `vendorConfig`; fixed at creation except through migration ([20](20-vendors.md), [07](07-stack-model.md)) |
| Vendor binary download | The per-run download, verification and read-only mount of the Terraform, OpenTofu or kubectl binary; `skipVendorBinaryDownload` turns it off ([20](20-vendors.md)) |
| Version catalog | The queries that list supported Terraform, OpenTofu, Terragrunt and kubectl versions and resolve a constraint to an effective version; the name is a Bruno folder ([17](17-blueprints-and-templates.md), [20](20-vendors.md)) |
| Version gating | spacectl hiding commands the target Spacelift instance cannot serve, based on `debugInfo.selfHostedVersion` ([06](06-spacectl-and-clients.md)) |
| Versioned-blueprint family | `BlueprintVersionedGroup`, `BlueprintDeployment` and the `blueprintVersion*` fields: the template model under its older names, deprecated on 2026-07-16 ([26](26-scope-and-tiers.md), [17](17-blueprints-and-templates.md)) |
| Webhook delivery | The stored record of one outbound request (`WebhookDelivery`, `WebhookDeliveryDetails`) with its status, headers and bodies ([19](19-events-notifications-audit.md)) |
| Webhook inbox | Committing each incoming webhook delivery before acknowledging it, then claiming and processing it later, as Terrakube does ([27](27-reference-materials.md)) |
| Worker | A logical executor that runs one run at a time; its status is `IDLE`, `BUSY` or `DRAINED` ([10](10-workers-and-execution.md)) |
| Worker binary | The program that executes run phases as the root process of the run container, fetched afresh for each job ([10](10-workers-and-execution.md)) |
| Worker outcome category | `RunWorkerOutcomeCategory`: `Healthy`, `Finished`, `Slow`, `Interrupted` or `Failed` ([10](10-workers-and-execution.md)) |
| Worker pool | A named set of workers that stacks, modules, Intent projects and scans are assigned to; public (run by Spacelift) or private (run by the customer) ([10](10-workers-and-execution.md)) |
| Worker pool token | The base64-encoded JSON `config` returned when a pool is created or reset, given to workers as `SPACELIFT_TOKEN` ([10](10-workers-and-execution.md)) |
| WorkerPool and Worker resources | The `workers.spacelift.io/v1beta1` custom resources that the Kubernetes worker-pool controller manages ([10](10-workers-and-execution.md)) |
| Worker-side initialization policy | A Rego file a private worker's launcher evaluates before starting a run, named by `SPACELIFT_LAUNCHER_RUN_INITIALIZATION_POLICY` ([10](10-workers-and-execution.md), [09](09-policies.md)) |
| Workflow ID | `Run.workflowId`: a ULID shared by every run in one chain of dependency, trigger-policy or promotion runs ([08](08-run-lifecycle.md), [18](18-scheduling-drift-dependencies.md), [28](28-data-model.md)) |
| Workflow tool | The per-vendor choice between Spacelift's built-in commands and a custom tool (`TerraformWorkflowTool`, `OpenTofuWorkflowTool`, `KubernetesWorkflowTool`; `CUSTOM` reads `.spacelift/workflow.yml`) ([20](20-vendors.md), [07](07-stack-model.md)) |
| Workflow (trigger policy) | The first run plus every run it caused, listed in trigger policy input; its ID is the workflow ID ([09](09-policies.md)) |
| Workspace queue (OTF) | OTF's queue of runs for one workspace, whose head blocks the others until it completes ([27](27-reference-materials.md)) |
| Write-only argument | A provider attribute ending in `_wo` (Terraform and OpenTofu 1.11 or later) that is sent but never stored in state, paired with a `_wo_version` counter whose change applies it ([05](05-terraform-provider.md)) |
| Write-only (secret) | A configuration element whose value the API and UI never return; only a checksum is readable ([13](13-configuration.md)) |
| Yank (mark as bad) | Marking a module version as bad (`versionYank`): permanent, it stops new consumers downloading the version and records `yanked`, `yankedBy` and `yankNote` ([15](15-module-and-provider-registry.md)) |
