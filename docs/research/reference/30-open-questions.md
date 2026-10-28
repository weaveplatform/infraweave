# 30: Open questions

Checked against: reports 01 to 28 in this directory and the RES-02 to RES-06 fact-check reviews, as they stand on 2026-10-28; introspection `b3255fe38f5e`; docs.spacelift.io read 2026-10-28.

Research baseline: **2026-10-28**.

This report lists what the research set could not settle: the product
decisions a maintainer must take, the facts that only a live Spacelift
account, a capture or Spacelift itself can confirm, and the upstream changes
that should send a reader back to a report. Every *(unverified)* marker in
reports 01 to 28 maps to an entry here; a script checks the mapping, and
the [coverage table](#coverage-of-the-unverified-markers) gives its result.
The questions come from the drafters' notes and the fact-checkers' reviews
of RES-02 to RES-06, merged and deduplicated across tickets. The design
records (decisions 0004 onward), report [26](26-scope-and-tiers.md)'s availability states and the
implementation plan resolve or carry these entries; the review findings
behind them are in [31](31-review-log.md).

| Label | Means |
|---|---|
| *(unverified)* | A claim in reports 01 to 28 that no primary source confirms; each maps to an entry below |

## How to use this list

- **Numbering.** Product decisions are `P1` to `P15`, captures `C1` to
  `C269` and re-verify triggers `R1` to `R28`. Numbers are stable: a
  resolved entry is marked resolved, not renumbered.
- **Report column.** The first report listed holds the claim; others repeat
  or depend on it.
- **Resolving a capture.** Check the captured request and response in as a
  fixture, change the report's *(unverified)* marker to **[C]** with the
  capture's date, and mark the entry resolved here.
- **Resolving a decision.** Record it in a decision record and link that
  record from the entry.
- **Capture safety.** A capture needs an account Spacelift has authorized
  for that use: a trial or demo account, or an existing customer account
  with permission. Questions marked "ask Spacelift" need support or sales.

## Product decisions

| # | Question | Options | Resolver | Blocks |
|---|---|---|---|---|
| P1 | How do Spacelift's GraphQL errors map onto HTTP statuses and error bodies in the served API? Spacelift documents neither its status codes nor stable `extensions` codes, and spacectl and the provider match message text such as `not found` and `unauthorized` | (a) a status per error class with a typed error body; (b) as (a), keeping Spacelift's message strings so that ported clients still match; (c) a GraphQL-style error envelope beside the REST status | maintainer, in a decision record; captures C2, C3 and C5 inform it | the served API's error contract; ports of spacectl and the provider ([01](01-spacelift-landscape.md), [05](05-terraform-provider.md), [06](06-spacectl-and-clients.md)) |
| P2 | Which deprecated or superseded Spacelift interfaces does infraweave serve? Candidates: `ACCESS` policies (disabled 2026-05-30); the deprecated `policyCreate` and `policyUpdate`; the versioned-blueprint family and plain blueprints beside templates; the legacy per-stack AWS integration and the legacy GCP integration; per-stack webhooks beside named webhooks; the per-stack Slack subscription; `/intent/mcp`; `ApiKeyInput.admin`; the `PENDING` run state; `TerraformWorkflowTool.OPEN_TOFU`; the MQTT worker transport; the Pulumi provider, read-only from 2026-12-10 | (a) serve each until Spacelift removes it; (b) serve only current interfaces and record each exclusion with its evidence in report 26; (c) accept and store without acting, as for `ACCESS` policies | maintainer, with report 26's supersession boundaries | availability states in report 26; API scope in [03](03-deploy-core-api-inventory.md), [04](04-platform-api-inventory.md), [06](06-spacectl-and-clients.md), [09](09-policies.md), [10](10-workers-and-execution.md), [12](12-cloud-integrations-and-oidc.md), [17](17-blueprints-and-templates.md), [19](19-events-notifications-audit.md), [22](22-intelligence.md) |
| P3 | Does infraweave implement features known only from the schema and Bruno, with no docs page: scans, the Origin (Cursor) integration, the `BLOB` VCS provider and the server side of plugin templates? | (a) implement the schema shape; (b) serve the routes as stubs until Spacelift documents them; (c) leave them out until they are a documented capability | maintainer, with report 26; capture C11 informs it | scope of [04](04-platform-api-inventory.md), [11](11-vcs-integrations.md), [21](21-resources-scans-search.md), [25](25-other-integrations.md) |
| P4 | Where Spacelift's docs and its own clients disagree, which does infraweave's CLI and VCS agent follow? Cases: spacectl's credential order (docs: token, GitHub, API key; source: token, API key, GitHub); profile storage (docs: a file per alias and a `current` link; source: one `config.json`); the vcs-agent default (docs: a strict allowlist; source at the pin: no validation) | (a) the docs, by the research precedence rule; (b) the released clients, for drop-in compatibility; (c) a choice per case, recorded with its reason | maintainer; capture C78 informs the agent case | infraweave CLI and VCS agent defaults ([06](06-spacectl-and-clients.md), [11](11-vcs-integrations.md)) |
| P5 | Does infraweave gate features by Spacelift's editions and its 31 `BillingTierFeature` values, and if so, how do the gates apply to a self-hosted install? | (a) no gates; (b) an edition setting that mirrors Spacelift's tiers; (c) one switch per feature | maintainer, with report 26 | the tier model in report 26; tier checks in [03](03-deploy-core-api-inventory.md), [04](04-platform-api-inventory.md), [14](14-spaces-rbac-and-identity.md), [19](19-events-notifications-audit.md); captures C14, C121 and C122 |
| P6 | Does infraweave's worker protocol offer MQTT, or HTTP long-poll only? Spacelift retires MQTT for workers on 2027-03-30 | (a) long-poll only; (b) long-poll plus an MQTT mode for existing launchers | maintainer | the worker protocol ([10](10-workers-and-execution.md)) and self-hosted parity ([24](24-self-hosted-reference.md)); whether capture C65 is needed at all |
| P7 | Do infraweave's run credentials reproduce Spacelift's formats exactly, so that existing trust policies, provider code and identity-provider rules keep working? This covers the in-run API token (`iss`, `aud`, the `sub` path), the OIDC federation claims and subject template placeholders, the AWS external ID and the AWS `RoleSessionName` | (a) identical formats; (b) identical by default, with a configurable issuer and templates; (c) infraweave's own formats with a migration guide | maintainer | [05](05-terraform-provider.md), [12](12-cloud-integrations-and-oidc.md), [13](13-configuration.md); captures C17, C81, C83, C91 and C94 |
| P8 | How does a REST update behave where Spacelift's GraphQL mutation takes a whole input object (`stackUpdate`, or `stackConfigAdd` with an existing `id`)? | (a) `PUT` with full replacement; (b) `PATCH` with merge semantics; (c) both | maintainer, in the REST conventions decision | the served API's update routes ([07](07-stack-model.md), [13](13-configuration.md)); captures C26 and C97 |
| P9 | How strictly does infraweave parse inputs that Spacelift leaves loosely specified: unknown keys in `.spacelift/config.yml`, label length and characters, cron forms and timezone names? | (a) strict, rejecting anything Spacelift's schemas do not list; (b) lenient, accepting whatever Spacelift may accept; (c) strict, with a compatibility switch | maintainer | the runtime YAML parser ([07](07-stack-model.md)), label validation, schedules ([18](18-scheduling-drift-dependencies.md)); captures C24, C30, C155 and C156 |
| P10 | Does infraweave mirror the archived standalone `spacelift-intent` stdio server, only the hosted `/mcp` Intent tools, or both? | (a) the hosted tools only; (b) both | maintainer, after capture C15 | Intent parity scope ([01](01-spacelift-landscape.md), [22](22-intelligence.md)) |
| P11 | Does infraweave ship a default AI integration with a usage budget, as Spacelift does on SaaS, or bring-your-own-model only, as self-hosted Spacelift requires? | (a) bring-your-own-model only; (b) that plus an optional operator-configured default with a budget | maintainer | AI integrations ([22](22-intelligence.md)); capture C213 |
| P12 | Are the research generators and check scripts committed, or do the reports describe their method without scratchpad paths? **Resolved:** the reports describe their method without naming scripts; generated tables are maintained by hand from here, and report 29 is the one generated, drift-checked artefact | (a) commit the scripts; (b) drop the script references and maintain the tables by hand (chosen) | maintainer, in the research pull requests | none |
| P13 | Do the MIT self-hosted module repositories get a `NOTICE` entry, so that 24 section 14.2 can carry their variable descriptions? | (a) add the entry and restore the descriptions; (b) keep the line links only | maintainer, in decision 0002 | [24](24-self-hosted-reference.md) section 14.2 |
| P14 | Is Spacelift's policy contract (`https://app.spacelift.io/.well-known/policy-contract.json`) pinned as a committed snapshot beside the introspection schema? | (a) pin it under `api/reference/spacelift/` and refresh it weekly; (b) keep it as a network source | maintainer (specref) | stable citations for policy inputs in [09](09-policies.md) and [19](19-events-notifications-audit.md); trigger R7 |
| P15 | Does the snapshot fetcher request input-field deprecations (`includeDeprecated: true` on `inputFields`)? The snapshot carries none, so input deprecations such as `ModuleCreateInput.space` and `ScheduledDeleteInput.cronSchedule` are visible only in the changelog | (a) fetch them and regenerate report 29; (b) keep the changelog as the source | maintainer (specref) | input deprecations in [15](15-module-and-provider-registry.md), [17](17-blueprints-and-templates.md), [18](18-scheduling-drift-dependencies.md) and report 29 |

## Captures needed

### Platform, API and clients (reports 01 to 06)

| # | Question | Report | How to capture | Blocks |
|---|---|---|---|---|
| C1 | Do feature-flagged fields reach real tenants before they appear in the demo schema? | [01](01-spacelift-landscape.md) | Introspect a trial tenant and diff it against the snapshot | completeness of the specification reference |
| C2 | Which HTTP status accompanies a GraphQL error: always 200, or 4xx and 5xx by cause? | [01](01-spacelift-landscape.md) | Capture an unauthorized request and a not-found request | P1 |
| C3 | Does Spacelift put stable error codes in `extensions`? | [01](01-spacelift-landscape.md) | The same captures as C2; search spacectl and the provider for `extensions` keys | P1; clients keying on codes rather than text |
| C4 | Does the GraphQL API have a request rate limit, and how is it signalled (429, headers)? Which statuses does the provider's go-retryablehttp retry? | [01](01-spacelift-landscape.md), [05](05-terraform-provider.md) | Ask Spacelift or search the KB; read `DefaultRetryPolicy` at the version in the provider's `go.sum`; a load capture | throttling and retry semantics |
| C5 | Does a lookup of a missing object answer a GraphQL error containing `not found`, or null data? spacectl relies on the error for `runStack`, the provider on the text for reads | [05](05-terraform-provider.md), [06](06-spacectl-and-clients.md) | Capture `stack(id:)` and `runStack` with an unknown ID | P1; the REST 404 mapping |
| C6 | Which permission do attachment mutations check while the schema marks their 18 actions (context, policy and cloud integration attach and detach, `STACK_ROLE_BINDING_*`, `STACK_WEBHOOK_*`) as disabled, and can those actions be put in a custom role? | [03](03-deploy-core-api-inventory.md), [12](12-cloud-integrations-and-oidc.md), [14](14-spaces-rbac-and-identity.md) | Custom roles holding single actions; `roleCreate` with a disabled action | attachment permissions in the RBAC model |
| C7 | Is the operation-to-action pairing, made by name in 03 and 04, what the service checks? | [03](03-deploy-core-api-inventory.md), [04](04-platform-api-inventory.md) | Call each mutation with a custom role holding one action | the per-operation permission table |
| C8 | What enforces the actions no mutation carries: the Intent resource actions, `STACK_STATE_READ`, the `SPACE_*` actions and `STACK_WORKSPACE_LOCK`? | [03](03-deploy-core-api-inventory.md), [16](16-state-management.md), [22](22-intelligence.md) | Test the state backend, the MCP tools and the workspace tab with roles lacking each action; ask Spacelift | permission checks outside GraphQL |
| C9 | What does `force` change on `contextDelete` and `policyDelete`: attached projects, plugin-managed objects, or both? | [03](03-deploy-core-api-inventory.md), [09](09-policies.md), [13](13-configuration.md) | Delete an attached context and an attached policy with and without `force` | delete semantics |
| C10 | Does `runRetry` create a new run or reuse the old one, and which run types may be retried (the schema says pull-request runs, spacectl says failed runs)? | [03](03-deploy-core-api-inventory.md), [08](08-run-lifecycle.md) | Retry a proposed and a tracked run and compare the returned IDs | run history model |
| C11 | What are scans, the Origin (Cursor) integration, the `BLOB` provider and plugin templates in product terms, and do they exist on self-hosted? | [04](04-platform-api-inventory.md), [11](11-vcs-integrations.md), [21](21-resources-scans-search.md) | Search the KB and changelog; inspect a trial account's UI | P3 |
| C12 | Does `vcsAgentPoolReset` invalidate the old agent credentials at once? | [04](04-platform-api-inventory.md) | Reset a pool while an agent is connected and watch the agent | credential rotation semantics |
| C13 | Which plan gates the external integrations other than ServiceNow, such as Backstage? | [04](04-platform-api-inventory.md), [25](25-other-integrations.md) | Pricing page; compare `tierFeatures` on two plans | tier mapping in report 26 |
| C14 | Which `BillingTierFeature`, if any, gates OIDC single sign-on? The docs put it on paid accounts and no feature name matches | [03](03-deploy-core-api-inventory.md), [04](04-platform-api-inventory.md), [14](14-spaces-rbac-and-identity.md) | Compare `tierFeatures` on a free trial and a paid account | P5; the SSO gate |
| C15 | Does the archived `spacelift-intent` repository mean the open-source Intent server is retired, or that it moved? | [01](01-spacelift-landscape.md), [22](22-intelligence.md) | Changelog, the repository README, the Intent docs; ask Spacelift | P10 |
| C16 | What do the Intent, template, role binding and managed-user operations do beyond their Bruno request names (102 Purpose cells)? | [03](03-deploy-core-api-inventory.md), [04](04-platform-api-inventory.md) | Reports 14, 17 and 22 cover most; capture the UI's calls for the rest | operation semantics in the served API |
| C17 | Is the last path segment of the in-run token's `sub` claim the run ID? | [05](05-terraform-provider.md) | Decode a `SPACELIFT_API_TOKEN` from a live run | P7 |
| C18 | Can `spacelift_run` and `spacelift_task` work with an in-run stack token, given that the docs say stack tokens reach no runs or tasks? | [05](05-terraform-provider.md) | Apply those resources from inside a stack that has an admin role | what infraweave stack tokens may do |
| C19 | Does Spacelift check an AWS role's trust policy when an integration is attached, as the provider's retry on its trust error suggests? | [05](05-terraform-provider.md), [12](12-cloud-integrations-and-oidc.md) | Attach an integration whose role does not trust Spacelift | attach-time validation |
| C20 | Does SaaS answer `debugInfo` with an empty `selfHostedVersion`, although the schema calls the field invalid on SaaS? | [06](06-spacectl-and-clients.md) | Capture `debugInfo` on SaaS | spacectl's version gating |
| C21 | Which GraphQL operations does the Kubernetes worker-pool controller (v0.0.47) send? Its source is not public | [06](06-spacectl-and-clients.md), [10](10-workers-and-execution.md) | Proxy the controller's traffic against a trial account; ask Spacelift | auto-registered pools and controller-managed workers |
| C22 | Does the `Spacelift-Client-Type: spacectl` header make the server record sessions with `SessionChannel.SPACECTL`? | [06](06-spacectl-and-clients.md), [14](14-spaces-rbac-and-identity.md) | Log in with spacectl and read the session list | whether sessions carry a client channel |
| C23 | What do the server sides of the browser login (`/cli_login`, `/auth_success`, `/auth_failure`) and of `GET /usageanalytics/csv` accept and return? Only the client halves are known | [01](01-spacelift-landscape.md), [06](06-spacectl-and-clients.md), [19](19-events-notifications-audit.md) | Capture a `spacectl profile login` and a CSV export | the browser login and usage export endpoints |

### Stacks, runs and policies (reports 07 to 09)

| # | Question | Report | How to capture | Blocks |
|---|---|---|---|---|
| C24 | What length and character rules apply to stack labels? | [07](07-stack-model.md) | Create stacks with long and unusual labels | P9; auto-attach matching |
| C25 | What happens to queued and running runs when a stack is disabled? | [07](07-stack-model.md) | Disable a stack with a run queued and one running | `stackDisable` side effects |
| C26 | Does `stackUpdate` keep or reset optional fields the input omits? | [07](07-stack-model.md) | Update with a minimal `StackInput` and read the stack back | P8 |
| C27 | What does the runtime YAML `version` key do? One docs page says `"2"` changes precedence and an absent key means `1`; the YAML reference says the key is ignored | [07](07-stack-model.md), [13](13-configuration.md) | Runs with `version: "1"`, `"2"` and no key, each with a conflicting `stack_defaults` value | precedence between `stack_defaults` and stack settings |
| C28 | When `config.yml` has no entry for a stack, do stack settings outrank `stack_defaults`, as one sentence of the runtime configuration page says? | [07](07-stack-model.md), [13](13-configuration.md) | A run with `stack_defaults.runner_image` and a stack-level runner image | the same precedence rule |
| C29 | Do runtime-file hooks replace or extend the stack's hooks, and what happens to context hooks? | [07](07-stack-model.md), [13](13-configuration.md) | A run with hooks in a context, on the stack and in `config.yml` | hook assembly for a run |
| C30 | Are unknown keys in `.spacelift/config.yml` rejected or ignored? | [07](07-stack-model.md) | A run with an unknown key | P9 |
| C31 | Which commands does OpenTofu `concise` affect: plan, apply and destroy (docs) or plan, apply and refresh (schema)? | [07](07-stack-model.md), [20](20-vendors.md) | Run with `concise` on and read the logged commands | runner flags |
| C32 | Does `kubernetes.namespace: ""` behave like leaving the field out? | [07](07-stack-model.md) | Create two stacks and compare their runs | the meaning of an empty value |
| C33 | What is a `PARSE` run: what creates it, which states it passes, and does it take the stack lock? | [08](08-run-lifecycle.md) | Ask Spacelift; look for the type in a trial account's run history | the `RunType` state machine |
| C34 | Does a `DESTROY` run plan or wait for confirmation before `DESTROYING`? | [08](08-run-lifecycle.md) | Delete a stack with `destroyResources: true` and read the run's `history` | the destroy path |
| C35 | Do `TESTING` runs take the module's lock? | [08](08-run-lifecycle.md) | Push two commits to a module quickly and watch the test runs | module test concurrency |
| C36 | Which state does `runKill` leave a run in, and what does the optional `state` argument of `runStop` and `runKill` do? | [08](08-run-lifecycle.md) | Kill a run and read its `history`; call both with a stale `state` | terminal states for forced stops |
| C37 | Which state does a push policy `cancel` give a run awaiting confirmation: `CANCELED` or `DISCARDED`? | [08](08-run-lifecycle.md) | Cancel an unconfirmed run from a push policy | push policy effects |
| C38 | After an upstream failure in a dependency chain, do downstream runs end `CANCELED` or `SKIPPED`? The dependencies page says both | [08](08-run-lifecycle.md), [18](18-scheduling-drift-dependencies.md) | Fail an upstream run and read the downstream runs' states | the run state machine for chains |
| C39 | Which state does a launcher logs timeout or run timeout produce? | [08](08-run-lifecycle.md), [10](10-workers-and-execution.md) | Exceed each timeout on a private worker | worker-side terminations |
| C40 | Does an approval policy `reject` fail the run or cancel it? The approval page's rules section says fail, its walkthrough says cancel | [08](08-run-lifecycle.md), [09](09-policies.md) | Reject a queued run and an unconfirmed run, then read `history` | the targets of T5, T24 and T29 |
| C41 | Does one `reject` from one of several attached approval policies fail the run regardless of the others? | [09](09-policies.md) | Attach two approval policies with opposite outcomes | approval combination rules |
| C42 | Does a task created with `skipInitialization` pass through `INITIALIZING`? | [08](08-run-lifecycle.md) | Capture such a task's `history` | whether T13 or a `PREPARING` to `PERFORMING` edge exists |
| C43 | Is a drift detection run on a pool with drift runs disabled created directly in `FAILED`, and do runs over the drift limit become `SKIPPED` or never get created? | [08](08-run-lifecycle.md), [18](18-scheduling-drift-dependencies.md) | Set both limits on a private pool and read the drift runs | creation edges; the use of `SKIPPED` |
| C44 | Does a failed handover in `PREPARING_REPLAN` end the run in `FAILED`? | [08](08-run-lifecycle.md) | Kill the worker during a targeted replan | the replan loop's failure edge |
| C45 | How is a negative (`negative: true`, `Run.expectFailure`) module test case judged, and which final state does it reach when it fails as expected? Must its destroy still succeed? | [08](08-run-lifecycle.md), [15](15-module-and-provider-registry.md) | Run a module with a negative test case | success criteria for `TESTING` runs |
| C46 | Which label produces the ignored-trigger reason `DISABLED_BY_LABEL`, and how do `CUSTOM_POLICY` and `CUSTOM_POLICY_NO_TRIGGER` differ? | [08](08-run-lifecycle.md) | Ask Spacelift; capture ignored triggers under each setup | ignored-trigger reasons infraweave emits |
| C47 | Are `ACCESS` policies still evaluated after their disable date of 2026-05-30? | [09](09-policies.md), [26](26-scope-and-tiers.md) | Attach an access policy on a trial account and test it | P2 |
| C48 | Which policy types does `BillingTierFeature.WORKFLOW_POLICIES` gate? | [09](09-policies.md), [26](26-scope-and-tiers.md) | Pricing page; compare `tierFeatures` across plans | tier mapping in report 26 |
| C49 | What is the API's default `engineType` when `policyCreatev2` omits it? | [09](09-policies.md) | Create a policy without `engineType` and read it back | the default Rego dialect |
| C50 | What effect does a flag set by a login, access, notification or Intent policy have, given those types touch no run? | [09](09-policies.md) | Sample such a policy and inspect the receipts | whether flags live outside runs |
| C51 | How does a policy get the `PolicyULID` it needs to use `reset_flag`, and which policies lack one? | [09](09-policies.md) | Ask Spacelift; compare old and new policies through the API | flag reset eligibility |
| C52 | Which run state follows a server-side initialization policy `deny`? | [09](09-policies.md) | Attach a deny-all initialization policy and trigger a run | the run state machine ([08](08-run-lifecycle.md)) |
| C53 | When does Spacelift record `PolicyInputCapture` documents, and do they depend on `sample`? | [09](09-policies.md) | Query `policyInputCaptures` before and after turning sampling on | capture storage and privacy |
| C54 | Are the in-product policy templates served from the spacelift-policies-example-library module? | [09](09-policies.md) | Compare `searchPolicyTemplates` with the library's 67 templates | the template corpus |
| C55 | What does `queued_runs` count on each trigger policy `stacks[]` entry, and is it present on module-version events? | [09](09-policies.md) | Sample a trigger policy | trigger policy input |
| C56 | In what unit are `Policy.createdAt` and `updatedAt`? | [09](09-policies.md) | Read a policy back and compare with its creation time | REST timestamp mapping |
| C57 | Which OPA version evaluates policies? | [09](09-policies.md) | Query `policyRuntime` on a live account | Rego compatibility |

### Workers (report 10)

| # | Question | Report | How to capture | Blocks |
|---|---|---|---|---|
| C58 | What are the platform default preparing timeout and the default of `SPACELIFT_LAUNCHER_WORKSPACE_INIT_TIMEOUT`? | [10](10-workers-and-execution.md) | Read `preparingTimeoutSeconds` on a pool with no override; ask Spacelift | defaults bounding `PREPARING` |
| C59 | Which fields does the worker pool token's JSON hold besides `broker.endpoint`? | [10](10-workers-and-execution.md) | Decode the token of a test pool | what a launcher learns from its credential |
| C60 | How does the API encode "drift detection disabled" for a pool: `driftDetectionRunLimit: 0` or something else? | [10](10-workers-and-execution.md), [18](18-scheduling-drift-dependencies.md) | Toggle the setting in the UI and read the field back | one REST encoding for the setting |
| C61 | Which `WorkerStatus` does a worker report while both drained and busy? | [10](10-workers-and-execution.md) | Drain a busy worker and read `status` | status precedence |
| C62 | Does `workerPoolCycle` wait for busy workers to finish? | [10](10-workers-and-execution.md) | Cycle a pool during a long run | recycle safety |
| C63 | Does a drained busy worker finish its current run before going idle? | [10](10-workers-and-execution.md) | Drain a busy worker and watch the run | scale-in safety |
| C64 | What are the HTTP long-poll endpoint paths and payloads? | [10](10-workers-and-execution.md) | Proxy a poll-mode launcher's traffic | the worker protocol infraweave replaces |
| C65 | What is the MQTT payload schema on the `readonly` and `writeonly` topics? | [10](10-workers-and-execution.md) | Capture messages on a self-hosted built-in broker | needed only if P6 keeps MQTT |
| C66 | Do the `SPACELIFT_WHITELIST_ENVS` `ro_` and `wo_` prefixes and the RO and WO extra mounts follow the read and apply stage split? | [10](10-workers-and-execution.md) | A tracked run on a private worker that prints `env` and mounts in plan and apply hooks | how the worker scopes passthrough values |
| C67 | Is the default `spec.pod.activeDeadlineSeconds` 70 minutes (docs) or 81 minutes 20 seconds (CRD)? | [10](10-workers-and-execution.md) | Inspect a run pod created by controller v0.0.47 | pod deadline against run timeout |
| C68 | What is the full input document of the worker-side initialization policy? | [10](10-workers-and-execution.md) | A policy that prints `input`, with a sampled deny | signed-run policy compatibility |
| C69 | Which vendors besides the excluded Terraform vendor produce worker traces and metrics? | [10](10-workers-and-execution.md) | Read `observabilityStatus` on OpenTofu, Pulumi and Ansible runs | scope of the observability API |
| C70 | Which self-hosted release removes MQTT? The v7.1.0 notes name none | [10](10-workers-and-execution.md), [24](24-self-hosted-reference.md) | Later self-hosted changelogs; ask Spacelift | self-hosted parity window |
| C71 | How does the GCP autoscaler find its instance group when the GCP worker-pool module sets no `gcp_igm_id` metadata? | [10](10-workers-and-execution.md) | Read a later module commit; ask Spacelift | GCP autoscaling |

### VCS integrations (report 11)

| # | Question | Report | How to capture | Blocks |
|---|---|---|---|---|
| C72 | Does the stack label `feature:enable_git_checkout` still have any effect now that every integration has `useGitCheckout`? | [11](11-vcs-integrations.md) | Toggle each on a trial account; ask Spacelift | whether checkout mode is per integration only |
| C73 | What name does an individual (per-stack) VCS check carry on each provider? | [11](11-vcs-integrations.md) | Capture a GitHub check run and the GitLab and Bitbucket statuses | branch protection rules that name checks |
| C74 | Does Spacelift act on GitLab comment (note) events for `/spacelift` commands? | [11](11-vcs-integrations.md) | Comment on a GitLab merge request | comment commands on GitLab |
| C75 | How does Bitbucket Cloud write commit statuses and PR comments with only `read:*` token scopes? | [11](11-vcs-integrations.md) | Connect with a read-only token and run a PR | Bitbucket Cloud credential scopes |
| C76 | Does renaming a GitHub organization still rename the Spacelift account? The docs say yes; the 2026-06-08 changelog removes automatic renames | [11](11-vcs-integrations.md) | Ask Spacelift, or rename a test organization | account identity tied to an organization |
| C77 | Which integration field does Spacelift route through a VCS agent: `apiHost` (provider examples) or `userFacingHost` (docs)? | [11](11-vcs-integrations.md), [28](28-data-model.md) | A VCS agent pool with different values in each field | the routing field for private VCS |
| C78 | Does a released vcs-agent register `--use-allowlist` and `--blocklist-path`, or does it validate nothing, as the source at `553aba3b3e54` does? | [11](11-vcs-integrations.md) | Run the released binary with `--help` and with `SPACELIFT_VCS_AGENT_USE_ALLOWLIST=true` against a dummy endpoint | P4; the agent's security default |
| C79 | Do the GitHub check's Preview and Deploy buttons rely on `check_run` `requested_action` events? | [11](11-vcs-integrations.md) | Capture GitHub App webhook deliveries while pressing them | GitHub event handling |
| C80 | How does Spacelift verify each provider's webhook deliveries (signature header, GitLab token)? | [11](11-vcs-integrations.md) | Replay a delivery with a wrong signature against a trial account | webhook verification |

### Cloud integrations and OIDC (report 12)

| # | Question | Report | How to capture | Blocks |
|---|---|---|---|---|
| C81 | Which external ID suffix (`read` or `write`) do tasks, destroy runs and module test runs use? | [12](12-cloud-integrations-and-oidc.md) | Trigger each type against a role with CloudTrail logging | P7; customer trust policies |
| C82 | What duration does `awsIntegrationCreate` apply when `durationSeconds` is omitted: the docs' one hour, or something else? (The provider always sends 900) | [12](12-cloud-integrations-and-oidc.md) | Create through the API without the argument and read it back | default session length |
| C83 | What is the exact AWS `RoleSessionName` on shared workers: the run ID alone, or `$runID@$stackID@$accountName` cut to 64 characters, and what replaces the stack ID for a module? | [12](12-cloud-integrations-and-oidc.md) | Assume a role from a test stack and a test module; read CloudTrail | P7; CloudTrail and SCP matching |
| C84 | Which `RoleSessionName` and session tags does a private worker use when it assumes the role itself? | [12](12-cloud-integrations-and-oidc.md) | Inspect CloudTrail for a worker-mode run | audit parity for worker-side assumption |
| C85 | With `feature:aws_oidc_session_tagging`, who calls `AssumeRoleWithWebIdentity` with tags, given that the IaC tool normally performs the exchange? | [12](12-cloud-integrations-and-oidc.md) | Run a tagged OIDC stack; read CloudTrail and the computed variables | whether Spacelift itself exchanges tokens |
| C86 | Are the native AWS integration's session tags transitive? | [12](12-cloud-integrations-and-oidc.md) | Chain roles from a tagged session and inspect the tags | role-chaining audit |
| C87 | Which space does `awsIntegrationCreate` or `azureIntegrationCreate` use when `space` is omitted? | [12](12-cloud-integrations-and-oidc.md) | Create without `space` and read it back | the create endpoint's default |
| C88 | Which credentials does a phase receive when a stack has only a read or only a write attachment? | [12](12-cloud-integrations-and-oidc.md) | Attach read-only and run a tracked apply | runtime semantics of attachments |
| C89 | Does the Azure integration inject `ARM_SUBSCRIPTION_ID`, from the attachment or the integration default? | [12](12-cloud-integrations-and-oidc.md) | Print `env` on a stack with an Azure attachment | computed variables |
| C90 | Are OIDC tokens refreshed for runs longer than an hour, or reissued for the apply phase? | [12](12-cloud-integrations-and-oidc.md) | A run longer than an hour that decodes its token at apply | long applies with expired tokens |
| C91 | Are OIDC federation tokens signed with the KMS `RSA_4096` key, and with which `alg` (`PS512`, as `kmsjwt` does)? | [12](12-cloud-integrations-and-oidc.md) | Decode a token header and read the JWKS | P7; issuer algorithm choice |
| C92 | Do parse runs receive an OIDC token? | [12](12-cloud-integrations-and-oidc.md) | Inspect a parse run's environment, once C33 says how to get one | token issuance scope |
| C93 | Do module test runs ever carry `scope: read`, as the Azure page's credential list implies? | [12](12-cloud-integrations-and-oidc.md) | Decode tokens from a module test run | federated credential scopes |
| C94 | Does the `legacy` space ID carry a ULID suffix? The OIDC pages disagree | [12](12-cloud-integrations-and-oidc.md) | Read the space, or decode a token from a run in it | P7; subjects for legacy-space stacks |
| C95 | What does the OIDC discovery document list (algorithms, claims), and does SaaS serve it at the path the self-hosted page gives? | [12](12-cloud-integrations-and-oidc.md) | Fetch `/.well-known/openid-configuration` on an account | the issuer's discovery document |
| C96 | How many federated credentials may one Azure app registration hold for Spacelift? The docs mention a cap without a number | [12](12-cloud-integrations-and-oidc.md) | Microsoft's limits for the app; ask Spacelift | Azure OIDC setup at scale |

### Configuration and state (reports 13 and 16)

| # | Question | Report | How to capture | Blocks |
|---|---|---|---|---|
| C97 | Does `stackConfigAdd` with an existing `id` update the element in place? | [13](13-configuration.md) | Add an element twice with the same `id` | P8 |
| C98 | Is the `ro_` or `wo_` prefix stripped from the variable name the process sees? | [13](13-configuration.md) | A task running `env` with prefixed variables | the run environment builder |
| C99 | What priority does an auto-attached context attachment report, and does `autoattach:*` reach child spaces whatever their inheritance setting? | [13](13-configuration.md) | Query `attachedContexts`; test with an inheritance-off child space | the attachment resource and auto-attach resolution |
| C100 | Is the `TF_VAR_spacelift_*` and `SPACELIFT_*` list the report gives complete? | [13](13-configuration.md) | A task printing `env` | computed environment parity |
| C101 | Where do computed values, stack dependency inputs and launcher-forwarded variables rank against contexts and stack variables? | [13](13-configuration.md) | Runs defining one name at each level | the full precedence order |
| C102 | Do per-run overrides (`runtimeConfig.environment`, `environmentVarsOverrides`) win over every stored source? | [13](13-configuration.md) | A run with an override that a context also sets | per-run override precedence |
| C103 | Does `${NAME}` interpolation resolve against the winning value only, and do computed values take part? | [13](13-configuration.md) | A task with `X=${TF_VAR_spacelift_stack_id}` and a name set at two levels | how the environment is built |
| C104 | Does every run get `SPACELIFT_API_TOKEN`, including runs of stacks without roles? The provider page says both | [13](13-configuration.md), [05](05-terraform-provider.md) | Print `env` in a task on a role-less stack | token injection rules |
| C105 | Which run states may read and which may write managed state, and what are the `http` backend's paths, lock semantics and credential format? | [16](16-state-management.md) | Capture the injected backend file and its requests in a task | the state backend |
| C106 | Which Terraform Cloud `remote` API endpoints serve external state access? | [16](16-state-management.md) | `terraform state pull` from outside with request logging | the external access protocol |
| C107 | Does the API itself refuse `stackObjectID` or `externalStateAccessEnabled` on a stack without managed state? | [16](16-state-management.md) | Call the API directly with each | validation rules |
| C108 | How long do state upload and download URLs live, and how long does an uploaded `objectId` stay valid? | [16](16-state-management.md) | Capture the URLs and retry after delays | expiry handling |
| C109 | Which runs count as pending for state import and rollback? | [16](16-state-management.md) | Try both with runs in different states | preconditions |
| C110 | What happens on a rollback to a version past the 30-day retention, and when does `isStateRollback` clear? | [16](16-state-management.md) | Roll back an expired version; read the flag after the next run | rollback rules |
| C111 | Are `TofuWorkspace` locks the managed backend's own locks, and do non-Terragrunt stacks expose workspaces? | [16](16-state-management.md) | Query workspaces on a Terraform stack during a plan | the lock model |
| C112 | How is OpenTofu state encryption configured on Spacelift, and what happens to outputs, the resources view and plan policies when state is encrypted? | [16](16-state-management.md) | Ask Spacelift, or test with an `encryption` block | encryption support |
| C113 | For a Terragrunt stack created with the provider defaults (`manageState: true`, `useStateManagement: false`), which value does the API honour? | [16](16-state-management.md) | Create one and read `managesStateFile` | managed state for default Terragrunt stacks |
| C114 | Can `terragrunt.use_state_management` in `.spacelift/config.yml` change state management for one run? | [16](16-state-management.md) | Flip the key; inspect `RuntimeConfig.terragrunt` and the backend | the creation-only rule |

### Spaces, RBAC and identity (report 14)

| # | Question | Report | How to capture | Blocks |
|---|---|---|---|---|
| C115 | Can a non-root space admin edit their spaces while non-root space management is off? Two docs pages disagree | [14](14-spaces-rbac-and-identity.md) | Test with the setting off | who may change space settings |
| C116 | What are the slugs and action sets of the built-in roles: Space reader, writer and admin, Worker pool controller and the drift detection role? | [14](14-spaces-rbac-and-identity.md) | Query `roles { slug actions isSystem }` | seeding system roles |
| C117 | Which legacy fallback applies to `STACK_SCHEDULED_DELETE_UPDATE`, which the docs tables omit? | [14](14-spaces-rbac-and-identity.md) | Test with a Writer-level user | the fallback table |
| C118 | Is a managed user with no role refused at login (docs) or given a token with empty access (changelog 2026-04-14)? | [14](14-spaces-rbac-and-identity.md) | Log in as an invited user with no bindings | the login decision |
| C119 | How long does a browser session last, and can an account change it? | [14](14-spaces-rbac-and-identity.md) | Decode `validUntil` or the JWT `exp` of a web session; check account settings | session design |
| C120 | How is a FIDO2 security key registered, given that no GraphQL mutation does it? | [14](14-spaces-rbac-and-identity.md), [28](28-data-model.md) | Capture the browser's calls while registering a key | the MFA surface |
| C121 | Which plans include spaces (Free in particular), custom roles and login policies? | [14](14-spaces-rbac-and-identity.md) | Read `tierFeatures` on each plan; ask sales | P5 |
| C122 | How do SaaS billing gates apply to self-hosted installs? The self-hosted overview says the install carries the Enterprise tier's functionality where it applies, but not what `tier` and `tierFeatures` return there | [14](14-spaces-rbac-and-identity.md), [26](26-scope-and-tiers.md) | Self-hosted licence docs; ask Spacelift | P5 |
| C123 | What happens to a personal API key when its owner is removed, beyond `disabledReason = DEPROVISIONED`? | [14](14-spaces-rbac-and-identity.md) | Remove a user who owns a key and read the key | key lifecycle |
| C124 | Does `ApiKeyInput.admin` still work, given that the changelog deprecates it and the schema does not? | [14](14-spaces-rbac-and-identity.md) | Create a key with `admin: true` and compare it with a root admin binding | API compatibility |
| C125 | Is the docs' `EXPIRED` user status derived from invitation age, since `UserStatus` has no such value? | [14](14-spaces-rbac-and-identity.md) | Read `managedUsers` for a stale invitation | the user status model |
| C126 | Do legacy `SpaceAccessLevel` grants propagate down and climb through inheritance exactly as role bindings do? | [14](14-spaces-rbac-and-identity.md) | Grant a level on a parent and read `Space.accessLevel` on children | fallback evaluation |
| C127 | Does inheritance's Read grant climb past the immediate parent? | [14](14-spaces-rbac-and-identity.md) | A three-level tree with inheritance on; check the grandparent | upward visibility |
| C128 | Under Identity Access Management, do binding changes force re-authentication (docs) or recompute access in place (changelog 2026-03-19)? | [14](14-spaces-rbac-and-identity.md) | Change a binding during a live session | session design |

### Module and provider registry (report 15)

| # | Question | Report | How to capture | Blocks |
|---|---|---|---|---|
| C129 | Does the stale-run cleanup documented for self-hosted v7.0.0 (proposed and module test runs left in `READY` cancelled after two days) also apply on SaaS? | [15](15-module-and-provider-registry.md) | Ask Spacelift; watch the SaaS changelog | queue housekeeping |
| C130 | Does `versionTrigger` on a `FAILED` version move it back to `PENDING` before retesting? | [15](15-module-and-provider-registry.md) | Trigger a failed version and poll `state` | the version state machine |
| C131 | What is `versionPush` for, and who calls it? | [15](15-module-and-provider-registry.md) | Ask Spacelift | whether an archive-based version path is needed |
| C132 | Can consumers pinned to a yanked version still download it? | [15](15-module-and-provider-registry.md) | Yank a version and run `terraform init` against it | registry download rules |
| C133 | Is each gap in a module's protocol version list (for example `2.11.0` of `spacelift-workerpool-on-ec2`) a `FAILED` version or a yanked one? | [15](15-module-and-provider-registry.md) | Read `versions(includeFailed: true)` on an account that can see the module, or yank a demo version and recapture | which states the protocol omits |
| C134 | Which host does a US-region account write in module and provider `source` addresses? | [15](15-module-and-provider-registry.md) | Read a US account's instructions snippet; ask Spacelift | discovery and host routing |
| C135 | Does a public provider answer the provider protocol without credentials? | [15](15-module-and-provider-registry.md) | Probe a public provider's `versions` endpoint | anonymous access |
| C136 | Can a `DRAFT` provider version be revoked, and a `REVOKED` one deleted? | [15](15-module-and-provider-registry.md) | Try both mutations | the provider version state machine |
| C137 | How does `terraformProviderVersionUploadDocs` sort archive files into `OVERVIEW`, `RESOURCE`, `DATA_SOURCE` and `UNKNOWN`? | [15](15-module-and-provider-registry.md) | Upload a standard `docs/` archive and read `docs.files` | documentation rendering |
| C138 | Which RBAC action gates `terraformProviderVersionVerify` and `terraformProviderVersionUploadDocs`? | [15](15-module-and-provider-registry.md) | Call both with single-action roles | the permission model |

### Blueprints and templates (report 17)

| # | Question | Report | How to capture | Blocks |
|---|---|---|---|---|
| C139 | Are the versioned-blueprint operations that carry no deprecation (`blueprintWithGroupCreate`, `Update`, `Delete`, `Blueprint.version`, `groupDetails`) still supported? | [17](17-blueprints-and-templates.md), [26](26-scope-and-tiers.md) | Call them on a trial account; watch the changelog's deprecation lists | P2 |
| C140 | Does the service reject a blueprint body with neither or both of `stack` and `stacks`? | [17](17-blueprints-and-templates.md) | `blueprintParseTemplate` on both bodies | the validator rule |
| C141 | How does a body declare an `AVAILABLE_SPACES` input when neither JSON schema accepts that type? | [17](17-blueprints-and-templates.md) | Create a template version with `type: available_spaces` | input type completeness |
| C142 | Are `validations` and `required` accepted on `boolean` and `select` inputs (docs yes, schema no)? | [17](17-blueprints-and-templates.md) | `templateVersionParseTemplate` on such a body | validation parity |
| C143 | Is the template stack limit 10 (docs) or unlimited (schema)? | [17](17-blueprints-and-templates.md) | Parse a body with 11 stacks | validation parity |
| C144 | Can a blueprint attach GCP integrations (the docs list them; the schema has only `aws` and `azure`)? | [17](17-blueprints-and-templates.md) | Parse a blueprint with `attachments.clouds.gcp` | attachment parity |
| C145 | Which cel-go version and extensions does `celplate` use? | [17](17-blueprints-and-templates.md) | Read `github.com/spacelift-io/celplate`, which is not among the local clones | expression compatibility |
| C146 | May two versions of one template share a version number? | [17](17-blueprints-and-templates.md), [28](28-data-model.md) | Create two versions with the same number | the uniqueness constraint |
| C147 | Can a deprecated template version still be deployed? | [17](17-blueprints-and-templates.md) | Deprecate a version, then call `templateDeploymentCreate` | deprecation semantics |
| C148 | What does `TemplateDeployment.inputs` return for secret inputs? | [17](17-blueprints-and-templates.md) | Read a deployment with a secret input | secret handling in the read model |
| C149 | How do `TEST` deployments differ from `REGULAR` ones beyond allowing drafts? | [17](17-blueprints-and-templates.md) | Create both and compare stacks, locks and listing | test deployment behaviour |
| C150 | What rule derives `TemplateDeploymentState` from stack and run states, and which actions are allowed in `NONE`, and revert in `DESTROYING_FAILED` and `DISCARDED`? | [17](17-blueprints-and-templates.md) | Drive deployments through failures, confirmations and discards; try each action | state-gated API behaviour |
| C151 | How often does Spacelift retry a postponed scheduled deletion? | [17](17-blueprints-and-templates.md) | Ask Spacelift, or observe one | scheduler design |
| C152 | Does a revert add a change record? | [17](17-blueprints-and-templates.md) | Revert a deployment and read `searchChangeHistory` | the change history model |

### Scheduling, drift and dependencies (report 18)

| # | Question | Report | How to capture | Blocks |
|---|---|---|---|---|
| C153 | What does the API do with an empty `ScheduledRunInput.name`, which the schema requires and the provider omits when unset? | [18](18-scheduling-drift-dependencies.md) | Create a scheduled run without a name | field validation |
| C154 | Which timezone applies to scheduled runs and tasks when `timezone` is omitted? | [18](18-scheduling-drift-dependencies.md) | Create one without `timezone` and read it back | schedule semantics |
| C155 | Which timezone names are accepted: IANA only, or abbreviations such as `CET`? | [18](18-scheduling-drift-dependencies.md) | Try several names | P9 |
| C156 | Does the API accept cron forms beyond the template schema's five-field pattern, such as names, `@daily`, `?`, seconds or stepped ranges? | [18](18-scheduling-drift-dependencies.md) | Probe `stackIntegrationDriftDetectionCreate` with each form | P9; the cron parser |
| C157 | Is there a minimum interval between firings? | [18](18-scheduling-drift-dependencies.md) | Try `* * * * *`; ask Spacelift | scheduler load limits |
| C158 | How are daylight-saving transitions handled? | [18](18-scheduling-drift-dependencies.md) | Observe a schedule across a transition | firing correctness |
| C159 | What happens to firings missed while workers are down or the stack is locked? | [18](18-scheduling-drift-dependencies.md) | Observe; ask Spacelift | catch-up semantics |
| C160 | Is a fired one-shot schedule removed, or kept with a null `nextSchedule`? | [18](18-scheduling-drift-dependencies.md) | Read a timestamp schedule after it fires | schedule lifecycle |
| C161 | What happens when a scheduled delete's destroy run fails, or when the stack has downstream dependants that block deletion? | [18](18-scheduling-drift-dependencies.md) | Test both | the delete job's failure path |
| C162 | What value does `TF_VAR_spacelift_run_trigger` take for scheduled runs? | [18](18-scheduling-drift-dependencies.md) | Print it in a hook of a scheduled run | environment parity |
| C163 | Which stack states does `ignoreState` admit: every state (schema) or any final state (provider)? | [18](18-scheduling-drift-dependencies.md) | Enable it on a stack left `UNCONFIRMED` and watch for detection runs | when drift detection fires |
| C164 | Is drift detection supported for Kubernetes, Ansible and Terragrunt stacks? | [18](18-scheduling-drift-dependencies.md) | Enable it on each | vendor scope |
| C165 | What is the default `ConfigType` of a stack dependency reference, and does `FILE_MOUNT` work? | [18](18-scheduling-drift-dependencies.md) | Create a reference without `type`; try `FILE_MOUNT` | reference injection |
| C166 | Which side of a dependency needs admin and which reader? | [18](18-scheduling-drift-dependencies.md) | Test with limited roles on each side | the RBAC check |
| C167 | Does a force-applied zero-change run stop in `UNCONFIRMED` when autodeploy is off? | [18](18-scheduling-drift-dependencies.md) | Trigger with `forceApply: SINGLE` on a stack without autodeploy | run transitions |
| C168 | Can an external dependency be marked before any run carries it, and can it be marked again? | [18](18-scheduling-drift-dependencies.md) | Call `runExternalDependencyMarkAsCompleted` in each order | completion semantics |

### Events, notifications and audit (report 19)

| # | Question | Report | How to capture | Blocks |
|---|---|---|---|---|
| C169 | Which tiers include the Slack integration and usage export? | [19](19-events-notifications-audit.md) | Pricing page; a free-account test | tier gating in report 26 |
| C170 | Which `NotificationType` does a worker usage inbox entry carry, given that the docs name `WORKER_USAGE` and the enum has no such value? | [19](19-events-notifications-audit.md) | Trigger the notification on a contract account and read `searchNotifications` | the inbox type set |
| C171 | Whose notification policies run for module version events and for worker usage events: the module's space and the root space, as inferred? | [19](19-events-notifications-audit.md) | Policies in different spaces, with each event | policy selection in the event router |
| C172 | Is there a size limit on `spacelift::logs::*` substitution in webhook payloads and PR comments? | [19](19-events-notifications-audit.md) | Substitute large logs | payload limits and truncation |
| C173 | Which VCS providers support the notification policy `pull_request` route? | [19](19-events-notifications-audit.md) | Test per provider | parity with [11](11-vcs-integrations.md) |
| C174 | What does the contract-only `truncate_body` field on the `pull_request` rule do? | [19](19-events-notifications-audit.md) | Send a long body with and without it | the PR comment contract |
| C175 | What does the legacy per-stack Slack subscription (`/spacelift subscribe`, `StackIntegrationSlack.channelNames`) send? | [19](19-events-notifications-audit.md) | Subscribe a channel on SaaS | P2 |
| C176 | Does `namedWebhooksIntegrationUpdate` honour a new `secret`, given that the docs say secrets cannot change? | [19](19-events-notifications-audit.md) | Update with a new secret and check the signature | secret rotation |
| C177 | When a secret header and a policy rule's `headers` set the same name, which wins? | [19](19-events-notifications-audit.md) | Send both to a capture endpoint | delivery header precedence |
| C178 | What is the outbound webhook contract: retry back-off, request timeout, retryable conditions (network errors, 429), the failure count for auto-disable, `Content-Type`, `User-Agent`, the `X-Signature` prefix, and how long SaaS keeps delivery records? | [19](19-events-notifications-audit.md) | Deliveries against a failing capture endpoint; read old deliveries; ask Spacelift | the delivery contract |
| C179 | What default payload do module version and worker usage events send through a webhook rule with no `payload`? | [19](19-events-notifications-audit.md) | A webhook rule without `payload` for each event | the default payload contract |
| C180 | What is the full set of audit `action` strings, and the `actor` prefixes for API keys, SSO users and other identity sources? | [19](19-events-notifications-audit.md) | Capture the audit webhook over a broad set of mutations; ask Spacelift | audit vocabulary |
| C181 | What is the audit webhook's retry schedule, and does `retryOnFailure` govern it, or are deliveries always retried as the docs say? | [19](19-events-notifications-audit.md) | A failing endpoint with the flag on and off | audit delivery guarantees |
| C182 | Under `includeRuns`, does the audit webhook add run state change events (schema, docs) or annotate audit events (provider), and what shape do they take? | [19](19-events-notifications-audit.md) | Capture with the flag on and off | the audit stream schema |
| C183 | Which tables does the self-hosted `/selfhosted/metrics` export accept, and what is its envelope? | [19](19-events-notifications-audit.md) | Call it with an invalid `tables` value on a self-hosted install; the error lists them | self-hosted usage export |
| C184 | Is `policy_receipts` under `run_updated.run` (docs) or at `run_updated` (contract and Datadog module), or both? | [19](19-events-notifications-audit.md) | Sample a notification policy evaluation | notification policy compatibility |

### Vendors (report 20)

| # | Question | Report | How to capture | Blocks |
|---|---|---|---|---|
| C185 | How does the native Terragrunt vendor supply `terragrunt` at the chosen `terragruntVersion`? | [20](20-vendors.md) | Pin a version other than the image's v0.93.3 and print `terragrunt --version` in `before_init` | executor downloads; scope of `skipVendorBinaryDownload` |
| C186 | What is the minimum OpenTofu version for `OpenTofuFeature.OTEL_TRACING`, and what does Spacelift do with it? | [20](20-vendors.md) | Query `openTofuFeatures(version:)` for several versions | the OpenTofu feature table |
| C187 | Which API does the single-stack OpenTofu migration banner call: `openTofuMigrate` with one stack, or `stackMigrateVendor`? | [20](20-vendors.md) | Capture the UI's request | whether `stackMigrateVendor` must accept `OpenTofu` |
| C188 | Does a Terragrunt stack outside run-all mode, without `skipReplan`, reuse the saved plan in apply (schema) or plan again (limitations page)? | [20](20-vendors.md) | Run one and read the apply log | apply semantics; mocked-output safety |
| C189 | Does deleting a native Terragrunt stack with `destroyResources: true` destroy its resources (changelog 2026-02-26), although the stack page says it is unsupported? | [20](20-vendors.md) | Delete one on a trial account | the Terragrunt destroy path |
| C190 | Do Pulumi login and stack selection run before or after `before_init` hooks? | [20](20-vendors.md) | A Pulumi stack whose `before_init` hook prints; read the log order | Pulumi hook order |
| C191 | Where does the Infracost plugin's `infracost.custom.spacelift.json` land in plan policy input: `third_party_metadata.infracost` or `third_party_metadata.custom.infracost`? | [20](20-vendors.md) | Install the plugin and sample a plan policy | the custom input contract |
| C192 | Is the sanitization hash the last 8 bytes of SHA-256 (policy page) or 8 hexadecimal characters (examples)? | [20](20-vendors.md) | Sanitize a known string and compare with SHA-256 | `sanitized()` compatibility in existing policies |
| C193 | What exact commands do Ansible runs, CloudFormation applies (change set execution) and Pulumi destroys use? The report infers them | [20](20-vendors.md) | Read the logs of each run type | runner command parity |

### Resources, scans and search (report 21)

| # | Question | Report | How to capture | Blocks |
|---|---|---|---|---|
| C194 | How does a worker pool become discovery-type, as `ScanInput.workerPool` requires? | [21](21-resources-scans-search.md) | Create a scan against an ordinary private pool and read the error | the pool kind and its validation |
| C195 | How long are scan resources kept before `resourceStats` drops to zero? | [21](21-resources-scans-search.md) | Ask Spacelift, or observe an old scan run | storage sizing |
| C196 | How is `ManagedEntity.thirdPartyMetadata` written? | [21](21-resources-scans-search.md) | Ask Spacelift; watch later schemas and docs | an entity field to populate or leave null |
| C197 | Which spellings does the `changePhase: String` argument of `Run.searchEntityChanges` accept? | [21](21-resources-scans-search.md) | Call it with `PLAN`, `plan` and an invalid value | request validation |
| C198 | How does a replacement count in `Delta`: once in add and once in delete, or otherwise? | [21](21-resources-scans-search.md) | Plan a replacement and read `Run.delta` | plan summary figures |
| C199 | Where is the Ansible "Enable configuration management view" toggle stored? | [21](21-resources-scans-search.md) | Toggle it and watch the GraphQL calls | whether the API exposes it |
| C200 | Does the server honour `SearchInput.orderBy`, given that its generic description calls ordering unimplemented, and does it apply to fields not flagged `orderable`? | [21](21-resources-scans-search.md) | `searchStacks` with opposite orderings, on orderable and other fields | sort semantics of every list |
| C201 | What are the maximum and default `first` for search fields? | [21](21-resources-scans-search.md) | A search with `first: 1000` and one with no `first` | page-size limits |
| C202 | Are multiple predicates combined with AND? | [21](21-resources-scans-search.md) | Two predicates whose matches differ | filter semantics |
| C203 | Is `stringMatches` case-sensitive? | [21](21-resources-scans-search.md) | Search with differently cased values | filter semantics |
| C204 | Is the space tree the hierarchy behind `HIERARCHY` suggestion fields, and are there others? | [21](21-resources-scans-search.md) | Read `searchStacksSuggestions` | hierarchy constraint semantics |
| C205 | Which values can `SavedFilter.type` take, and what is the full `data` format? | [21](21-resources-scans-search.md) | List saved filters after saving views on several lists | saved filter storage |
| C206 | Does the API restrict `isPublic: true` on saved filters to admins? | [21](21-resources-scans-search.md) | `savedFilterCreate` with `isPublic: true` as a non-admin | saved filter authorization |
| C207 | Where are a user's default filter view, custom dashboard views and list column layouts stored (`uiConfigStore` or elsewhere)? | [21](21-resources-scans-search.md) | Watch `uiConfigGet` and other calls while changing each | per-user UI state storage |
| C208 | Does the stacks bulk Trigger action call `runTrigger` per stack or `runTriggerBatch`? | [21](21-resources-scans-search.md) | Watch the calls during a bulk trigger | whether a batch trigger route is needed |
| C209 | Which queries feed the Active users, Launch Pad and User Activity dashboard widgets? | [21](21-resources-scans-search.md) | Watch the dashboard's calls as an admin | dashboard completeness |
| C210 | In what unit are the run-duration series other than `averageRunDuration` (nanoseconds)? | [21](21-resources-scans-search.md) | Compare `medianRunDuration` with known runs | dashboard metric units |
| C211 | Which window does `metrics` use when no range is given? | [21](21-resources-scans-search.md) | Compare `metrics` with `metricsRange` over 30 days | the default dashboard range |

### Intelligence (report 22)

| # | Question | Report | How to capture | Blocks |
|---|---|---|---|---|
| C212 | Can Infra Assistant's Build mode run arbitrary GraphQL mutations, as the MCP `mutate` tool can? | [22](22-intelligence.md) | Capture Build mode tool calls | the assistant's tool set |
| C213 | How do Spacelift-hosted AI credits (AI terms section 3) relate to the free tier's USD 10 token budget? | [22](22-intelligence.md) | Ask Spacelift; watch the pricing page | P11 |
| C214 | Has Intent left early access? Product pages carry no beta label; the legal page and the UI menu do | [22](22-intelligence.md) | Ask Spacelift; watch the pricing page and changelog | tier and support status in report 26 |
| C215 | What does `BillingTierFeature.ACCOUNT_AI_CONTROL` gate, and on which tier? | [22](22-intelligence.md) | Compare accounts on different plans; ask Spacelift | tier mapping for AI controls |
| C216 | Does the API accept upper-case provider values such as Bruno's `OPENAI` besides `OpenAI`? | [22](22-intelligence.md) | `aiIntegrationCreate` with both spellings | input validation |
| C217 | How are chat messages sent and replies streamed, given that GraphQL has only conversation records and transcripts? | [22](22-intelligence.md) | Capture the browser's traffic for Infra Assistant | the chat transport and token accounting |
| C218 | What does `intentProjectDelete(keepResources: true)` do: keep the cloud resources and drop the state? | [22](22-intelligence.md) | Delete a test project with the flag | Intent delete semantics |
| C219 | Does `intentProjectDisable` set `state` to `READ_ONLY`? | [22](22-intelligence.md) | Disable a project and read `state` | the project state machine |
| C220 | Which state does an Intent operation take when a policy denies it, or a reviewer does: `FAILED`? | [22](22-intelligence.md) | Capture a denied operation of each kind | the operation state machine |
| C221 | Does the `intent` tool's `resume` move a `READY_FOR_EXECUTION` operation to `PENDING`? | [22](22-intelligence.md) | Capture through the MCP `intent` tool | the review flow |
| C222 | What is the default Intent worker image, and what message does the launcher receive for an Intent execution? | [22](22-intelligence.md) | Read a private-pool launcher's logs during an Intent operation | the worker protocol for Intent jobs ([10](10-workers-and-execution.md)) |
| C223 | Does read access to an Intent project follow space read access, given that there is no read action? | [22](22-intelligence.md) | Check with a reader role | the RBAC model |
| C224 | Was `/intent/mcp` removed after 2026-08-01 as announced? | [22](22-intelligence.md) | Request the old endpoint | P2 |
| C225 | What are the input schemas of the hosted `discover`, `query`, `provider`, `mutate` and `intent` tools? | [22](22-intelligence.md) | `tools/list` against `/mcp` with a valid session | the tool contract an infraweave MCP endpoint must match |
| C226 | What are the OAuth endpoint paths (discovery, registration, authorization, token) for `/mcp`? | [22](22-intelligence.md) | Fetch `/.well-known/oauth-protected-resource` and `/.well-known/oauth-authorization-server` on an account | the MCP OAuth implementation |
| C227 | Does the Bedrock bring-your-own-model changelog entry of 2026-04-13 mean Intent can use AI integrations, contrary to the AI integrations page? | [22](22-intelligence.md) | Ask Spacelift; the docs page wins until then | whether Intent calls an LLM server-side |

### Flows (report 23)

| # | Question | Report | How to capture | Blocks |
|---|---|---|---|---|
| C228 | What are Flows' pricing, tiers and plan limits? | [23](23-flows.md) | Ask Spacelift sales; the pricing page | tier mapping in report 26 |
| C229 | How does `flowctl` create an organization-wide custom app when it always sends a project ID? | [23](23-flows.md) | Test with an account; read the Flows UI | app scoping |
| C230 | Which audit log event types exist, and how long are entries kept? | [23](23-flows.md) | Export logs from a live organization; ask Spacelift | audit parity and retention |
| C231 | Which tools does the Flows MCP server expose besides `usage_instructions`? | [23](23-flows.md) | List tools against `https://mcp.useflows.eu` with an MCP client | the MCP surface |
| C232 | Are the live OAuth scopes `apps:viewer` and `apps:editor` the documented `apps:view`, and does the token endpoint accept the public clients `flowctl` registers? | [23](23-flows.md) | A dynamic registration and token exchange against the live server | scope names and client types |
| C233 | Does `flowctl` accept `--ui-entrypoint` and `-v`, as the docs show and the source does not declare? | [23](23-flows.md) | Check citty's flag handling, or run the published CLI in a sandbox | CLI compatibility |
| C234 | What do the gateway's `http` and `grpc` ports serve? | [23](23-flows.md) | Ask Spacelift; inspect a running self-hosted gateway | component interfaces and network policy |
| C235 | Is the `spacelift-flows-agent` chart, which runs agents in Kubernetes, supported for self-hosted installs, given that the guide requires EC2 agents? | [23](23-flows.md) | Ask Spacelift | supported agent topologies |
| C236 | Which server listener keys does the current backend read: `publicPort` and `internalPort` (module) or `listeners` (chart example)? | [23](23-flows.md) | Compare with a backend release; ask Spacelift | configuration schema |
| C237 | Where are self-hosted Flows release notes published? | [23](23-flows.md) | Search the self-hosted changelog; ask Spacelift | version tracking |
| C238 | Which operation links a Spacelift Deploy account to a Flows organization? The schema has only read queries and role binding mutations | [23](23-flows.md) | Capture the traffic while linking | the Deploy integration |
| C239 | Which `kind` values do the Notifications, Trigger Run Batch, Trigger Task, Confirm Run, Review Run, Deploy Blueprint and GraphQL Request blocks use? | [23](23-flows.md) | Export a flow holding each block with `flowctl flow export` | flow YAML compatibility for Deploy blocks |
| C240 | When does a Flows block reach `drifted`, given that no lifecycle callback can return it and the SDK lists it only in `blocks.list`? | [23](23-flows.md) | Watch a drifted block on a live account; ask Spacelift | the block state machine |
| C241 | How do the Data Table, Form, Group and Concurrency Group blocks behave? The definition reference names them without a behaviour page | [23](23-flows.md) | Build a flow with each on a live account | core block parity |
| C242 | How are agent pool tokens rotated, and which credentials do Deploy blocks use? | [23](23-flows.md) | Ask Spacelift; inspect a live organization | credential handling |

### Self-hosted (report 24)

| # | Question | Report | How to capture | Blocks |
|---|---|---|---|---|
| C243 | Are `spacelift backend drain` (ECS module) and `spacelift backend drain-local` (Helm chart, docs) the same service, and what does `-local` change? | [24](24-self-hosted-reference.md) | Ask Spacelift, or run `spacelift backend --help` from a licensed image | process topology |
| C244 | Do the database migrations create any PostgreSQL extension? | [24](24-self-hosted-reference.md) | `\dx` on a migrated test install; ask Spacelift | which managed PostgreSQL offerings work |
| C245 | Does the application use `OBJECT_STORAGE_BUCKET_USAGE_ANALYTICS`? | [24](24-self-hosted-reference.md) | Ask Spacelift; watch the changelog | an undocumented bucket |
| C246 | Does the Azure `DeleteAfter2Days` rule, whose prefixes include `spacelift-states`, delete current state blobs after two days, and is that intended? | [24](24-self-hosted-reference.md) | Check the rule against Azure lifecycle semantics; raise it on `terraform-azure-spacelift-selfhosted` | state retention correctness |
| C247 | Which Helm chart repository is canonical: `spacelift-helm-charts/spacelift-self-hosted` or `spacelift-self-hosted-helm-chart`? | [24](24-self-hosted-reference.md) | Check which publishes to `downloads.spacelift.io/helm`; ask Spacelift | which image values apply |
| C248 | With `kms_arn` set, where do `ENCRYPTION_KMS_ENCRYPTION_KEY_ID` and `ENCRYPTION_KMS_SIGNING_KEY_ID` come from, given that the AWS module's key outputs are then null? | [24](24-self-hosted-reference.md) | Read the ECS and EKS modules' handling of empty outputs; ask Spacelift | bring-your-own-key installs |
| C249 | Is it intended that the AWS modules and states buckets keep noncurrent versions forever? | [24](24-self-hosted-reference.md), [16](16-state-management.md) | Ask Spacelift, or raise it on `terraform-aws-spacelift-selfhosted` | storage growth and state history retention |
| C250 | Do the CloudFormation pages on advanced installations, disaster recovery, PostgreSQL upgrades and Slack setup add anything to the reference architecture? | [24](24-self-hosted-reference.md) | Read the four pages | completeness of [24](24-self-hosted-reference.md) |
| C251 | Does self-hosted Spacelift send email at all? No SMTP setting exists in the docs or modules | [24](24-self-hosted-reference.md) | Ask Spacelift | notification channels on self-hosted |

### Other integrations (report 25)

| # | Question | Report | How to capture | Blocks |
|---|---|---|---|---|
| C252 | When does `PluginTemplateParameterType` take the value `NONE`, given that the manifest schema allows only string, number and boolean? | [25](25-other-integrations.md) | Create a template with an untyped parameter and read it back | the manifest schema |
| C253 | Which role or action governs plugins, plugin templates and external integrations? No `Action` value names them | [25](25-other-integrations.md) | Try the mutations with a non-admin key; ask Spacelift | the permission model |
| C254 | How are parameter values substituted at install, and is the `${param.name}` syntax in the spaceforge README supported? | [25](25-other-integrations.md) | Install a custom template that uses both forms | the manifest templating contract |
| C255 | Does each managed resource a plugin creates carry `autoattach:<labelIdentifier>`? | [25](25-other-integrations.md) | Install a plugin and read the created context's and policy's labels | how installations reach stacks |
| C256 | Are `aws_sam`, `coverage` and `ssm_parameter_store` global templates on SaaS? The docs list ten plugins; the repository has thirteen | [25](25-other-integrations.md) | Query `pluginTemplates` | the global catalogue |
| C257 | How is a Backstage integration key represented in the API, and how is its seat exemption recorded, given that `APIKeyType` has only `SECRET` and `OIDC`? | [25](25-other-integrations.md) | Create one in the UI and read it with `apiKeys`; capture the UI's call | the integration key model |
| C258 | Which GraphQL call does the ServiceNow Business Rule make? The report infers `blueprintCreateStack` | [25](25-other-integrations.md) | Read the script ServiceNow installs; capture its call | the ServiceNow integration |
| C259 | What does the plugin logs upload endpoint (`/worker/plugin_logs_url`) return, and how long do its URLs live? | [25](25-other-integrations.md) | Capture a plugin run's upload | the plugin output contract |

### Scope and tiers (report 26)

| # | Question | Report | How to capture | Blocks |
|---|---|---|---|---|
| C260 | What does a plain Starter tier (`V4_STARTER`, `V5_STARTER`) include, and which features separate `V5_STARTER_PLUS` from `V5_STARTER_PLUS_2`? Only fragments are published: Starter has a seat limit and one private worker, and no session listing | [26](26-scope-and-tiers.md) | Read `tierFeatures` on an account of each tier; ask Spacelift | P5; the tier gates behind `tier` and `tierFeatures` |
| C261 | Which plan offers each capability whose plan reads "Not stated" in report 26: contexts on Free (the pricing comparison table starts at Starter+), roles, Ansible, plugins, Flows, scans, Slack, Repos, Blob Storage, Origin, the OpenTofu migration, Tofu workspaces and personal API keys, and the features `ACCESS_POLICIES`, `ACCOUNT_AI_CONTROL`, `SPACELIFT_INTELLIGENCE`, `BACKSTAGE_INTEGRATION`, `SCANS`, `PRIVATE_MODULE_SHARING` and `CLOUD_CREDENTIAL_GENERATION`? | [26](26-scope-and-tiers.md) | Compare `tierFeatures` on a Free and a paid account; read the pricing page's full comparison table | P5 |
| C262 | **Resolved 2026-10-28 (RES-07 fact-check):** self-hosted has no public worker pool; <https://docs.spacelift.io/self-hosted> lists it among the features self-hosted lacks, and report 26 now cites it. The question was: does self-hosted Spacelift have a public worker pool? | [26](26-scope-and-tiers.md), [19](19-events-notifications-audit.md) | Self-hosted docs; query `publicWorkerPool` on a self-hosted install | whether a self-hosted install needs a public pool |

### Reference materials (report 27)

| # | Question | Report | How to capture | Blocks |
|---|---|---|---|---|
| C263 | What versioning discipline does Temporal require when workflow code changes while executions are in flight? | [27](27-reference-materials.md) | Read the workflow versioning pages for Temporal's Go SDK | whether Temporal suits run orchestration |
| C264 | Are Terramate Cloud's features (drift, deployments, dashboards, policies) sold only by subscription? | [27](27-reference-materials.md) | Read Terramate's pricing and terms pages | how far Terramate can be studied beyond its public docs |
| C265 | Does the Digger orchestrator continue under the OpenTaco name, or is OpenTaco a separate product line? | [27](27-reference-materials.md) | Read the diggerhq/digger README and recent releases, and the OpenTaco docs | whether Digger stays a maintained reference |
| C266 | How is Spacelift's self-hosted PostgreSQL queue implemented: table layout, locking and visibility timeout? Report 27 compares River with it by behaviour only | [27](27-reference-materials.md), [24](24-self-hosted-reference.md) | Ask Spacelift, or read the queue tables on a self-hosted install | the job queue engine choice |

### Data model (report 28)

| # | Question | Report | How to capture | Blocks |
|---|---|---|---|---|
| C267 | Which worker processed a run, and where does Spacelift record it? No schema field names the worker of a run | [28](28-data-model.md), [10](10-workers-and-execution.md) | Read run logs or the worker protocol messages on a private worker | storing the run-to-worker link for audit and drain |
| C268 | How is the owner of an Intent chat conversation stored? History is kept per user, but no schema field names an owner | [28](28-data-model.md), [22](22-intelligence.md) | Query `intentChatConversations` as two users of one account | per-user conversation storage |
| C269 | Can one run be listed under several pull requests? `PullRequest.runs` lists runs, but no `Run` field names a pull request | [28](28-data-model.md), [11](11-vcs-integrations.md) | Open two pull requests on the same head commit and read `runs` on each | whether runs and pull requests need a join table or a column on `runs` |

## Re-verify triggers

| # | Trigger | Re-check | What to look for |
|---|---|---|---|
| R1 | A new introspection snapshot in `sources.lock.yaml` | Every **[G]** table in [03](03-deploy-core-api-inventory.md), [04](04-platform-api-inventory.md), [07](07-stack-model.md) to [25](25-other-integrations.md), [26](26-scope-and-tiers.md) and [29](29-data-model-inventory.md) | Added, removed or deprecated fields and enum values; `Action` values losing their "Disabled" description (C6); a `WORKER_USAGE` `NotificationType` (C170); new `BillingTierFeature` values; descriptions for operations known only by Bruno names (C16) |
| R2 | A refresh of the docs mirror, or a change in the `llms.txt` counts | [01](01-spacelift-landscape.md) section 3; every docs URL in 01 to 28 | Removed or moved pages; new pages on scans, the Origin integration or plugins (C11); changed tier notices |
| R3 | A changelog entry after 2026-10-28 | [09](09-policies.md), [17](17-blueprints-and-templates.md) and [22](22-intelligence.md) deprecation lists; [10](10-workers-and-execution.md) and [24](24-self-hosted-reference.md) transports; [14](14-spaces-rbac-and-identity.md) login and sessions; [20](20-vendors.md) vendors | Deprecations and removals (P2); `/intent/mcp` (C224); Intent leaving early access (C214); account renames (C76); input deprecations (P15) |
| R4 | A new spacelift-api-bruno pin | [03](03-deploy-core-api-inventory.md) and [04](04-platform-api-inventory.md) Purpose cells; dropped examples in `coverage.md` | New request docs for the 102 name-only Purpose cells |
| R5 | A terraform-provider-spacelift release after v1.57.0 | [05](05-terraform-provider.md); provider defaults in [07](07-stack-model.md), [13](13-configuration.md), [16](16-state-management.md), [18](18-scheduling-drift-dependencies.md), [19](19-events-notifications-audit.md) | New resources and data sources (56 and 62 at the baseline); changed defaults and `ForceNew`; the operation map |
| R6 | A spacectl commit after `6bf167cb62a6` | [06](06-spacectl-and-clients.md); the metadata header in [08](08-run-lifecycle.md); list ordering in [21](21-resources-scans-search.md) | The command tree (108), operations (74) and MCP tools (37) |
| R7 | A change to the policy contract (113,924 bytes on 2026-10-28) | [09](09-policies.md), [19](19-events-notifications-audit.md) | Inputs and rules per type; `policy_receipts` placement (C184); `queued_runs` (C55); an `INITIALIZATION` entry |
| R8 | A policy example library commit after `6ff5edb08bc8` | [09](09-policies.md) section 6.3 | Template and test counts (67 and 61); C54 |
| R9 | A self-hosted release after v7.1.0 | [24](24-self-hosted-reference.md); self-hosted rows in [01](01-spacelift-landscape.md), [10](10-workers-and-execution.md), [22](22-intelligence.md) | MQTT removal (C70); the CloudFormation drop in v8.0.0; new environment variables; Flows release notes (C237) |
| R10 | A commit to the self-hosted module repositories or Helm charts | [24](24-self-hosted-reference.md) | Buckets, queues and keys; the environment table (77 rows) and Helm table (135 leaves); C246, C247, C248, C249 |
| R11 | A release of the worker-pool modules, the autoscaler or the Kubernetes controller (v0.0.47) | [10](10-workers-and-execution.md), [06](06-spacectl-and-clients.md) | CRD defaults (C67); controller operations (C21); `gcp_igm_id` (C71) |
| R12 | A runner image release | [10](10-workers-and-execution.md), [20](20-vendors.md) | Base images per target; the bundled Terragrunt version (C185) |
| R13 | A vcs-agent release after `553aba3b3e54` | [11](11-vcs-integrations.md) | Whether the allowlist flags are registered (C78) |
| R14 | A prometheus-exporter commit after `257f12096706`, or a Datadog module change | [19](19-events-notifications-audit.md) | The 23 descriptors; Datadog metrics and tags against the docs |
| R15 | A new `@slflows/sdk` (after 0.14.0), `flowctl`, Flows provider or app registry commit | [23](23-flows.md) | Lifecycle statuses (C240); registry counts (116 and 10); CLI flags (C233) |
| R16 | `spacelift-intent` is unarchived, moved or replaced | [22](22-intelligence.md), [01](01-spacelift-landscape.md) | P10; C15 |
| R17 | The Pulumi provider's read-only date, 2026-12-10, passes | [06](06-spacectl-and-clients.md), [01](01-spacelift-landscape.md) | P2 |
| R18 | The MQTT dates pass: the switch recommended by 2027-01-01 and retirement on 2027-03-30 | [10](10-workers-and-execution.md), [24](24-self-hosted-reference.md), [01](01-spacelift-landscape.md) | Whether MQTT pools still connect; P6 |
| R19 | A change to the pricing page | [26](26-scope-and-tiers.md), [14](14-spaces-rbac-and-identity.md), [19](19-events-notifications-audit.md), [22](22-intelligence.md), [23](23-flows.md) | Plan lists and tier rows (C14, C121, C169, C215, C228, C260, C261) |
| R20 | A change to HashiCorp's registry protocol pages | [15](15-module-and-provider-registry.md) | The module `versions` 404 divergence; provider response fields |
| R21 | An OpenTofu release | [16](16-state-management.md), [20](20-vendors.md) | State encryption (C112); `concise` (C31); `OTEL_TRACING` (C186) |
| R22 | A Terragrunt release or a change to Spacelift's Terragrunt pages | [20](20-vendors.md), [16](16-state-management.md) | Plan reuse (C188); resource deletion (C189) |
| R23 | A change to Spacelift's AI or Intent terms | [22](22-intelligence.md) | Credits (C213); early-access status (C214) |
| R24 | A change in the spacelift-io GitHub organization listing (136 repositories at the baseline) | [01](01-spacelift-landscape.md) section 5 | New, archived or relicensed repositories |
| R25 | A commit to the plugins repository or spaceforge, a Backstage plugin release after 0.3.3, or a Teleport docs change | [25](25-other-integrations.md) | The 13-plugin catalogue (C256); the Teleport join method |
| R27 | A release, archival or licence change in a reference project of report 27 (OTF, OPA, Atlantis, Terrakube, Burrito, Terramate, Digger, River, Temporal) | [27](27-reference-materials.md) | Pinned commits, licences and the comparisons built on them; C263 to C265 |
| R28 | IMP-01 confirms or changes the phase spine | [26](26-scope-and-tiers.md), [28](28-data-model.md) | Report 26's provisional phase column and the capability rows that cite it |
| R26 | A capture in this list is made | The owning report and this list | Replace the *(unverified)* marker with **[C]**, mark the entry resolved and recheck any report that depends on it |

## Coverage of the unverified markers

The check lists every *(unverified)* marker in reports 01 to 28 with the
claim it qualifies (the sentence ending at the marker, or the table cell
with its row and column) and the entries that cover it. It fails on a
marker no entry covers, or on an entry that does not link the marker's
report. "Defining the marker" counts the label-table rows, and the method
text in 01, that define the marker rather than qualify a claim. On
2026-10-28 it reports 401 markers, 377 claims, 233 entries used and no
gaps. Report 02 has no markers.

| Report | Markers | Defining the marker | Claims | Entries covering them |
|---|---|---|---|---|
| [01](01-spacelift-landscape.md) | 9 | 1 | 8 | C1, C2, C3, C4 |
| [02](02-graphql-to-rest-mapping.md) | 0 | 0 | 0 | none |
| [03](03-deploy-core-api-inventory.md) | 6 | 0 | 6 | C6, C7, C8, C9, C10 |
| [04](04-platform-api-inventory.md) | 5 | 0 | 5 | C11, C12, C13 |
| [05](05-terraform-provider.md) | 2 | 0 | 2 | C17 |
| [06](06-spacectl-and-clients.md) | 4 | 1 | 3 | C21, C22 |
| [07](07-stack-model.md) | 9 | 1 | 8 | C24, C25, C26, C27, C28, C29, C30 |
| [08](08-run-lifecycle.md) | 35 | 1 | 34 | C10, C33, C34, C35, C36, C37, C38, C39, C40, C42, C43, C44, C45, C46 |
| [09](09-policies.md) | 21 | 1 | 20 | C9, C41, C47, C48, C49, C50, C51, C52, C53, C54 |
| [10](10-workers-and-execution.md) | 13 | 1 | 12 | C58, C59, C60, C61, C62, C63, C64, C65, C66, C67, C68, C69 |
| [11](11-vcs-integrations.md) | 10 | 1 | 9 | C72, C73, C74, C75, C76, C77, C78 |
| [12](12-cloud-integrations-and-oidc.md) | 30 | 1 | 29 | C6, C81, C82, C83, C84, C85, C86, C87, C88, C89, C90, C91, C92, C93, C94 |
| [13](13-configuration.md) | 15 | 1 | 14 | C9, C27, C28, C29, C97, C98, C99, C100, C101, C102, C103 |
| [14](14-spaces-rbac-and-identity.md) | 17 | 1 | 16 | C6, C115, C116, C117, C118, C119, C120, C121, C122, C123 |
| [15](15-module-and-provider-registry.md) | 13 | 1 | 12 | C45, C129, C130, C131, C132, C133, C134, C135, C136, C137, C138 |
| [16](16-state-management.md) | 12 | 1 | 11 | C105, C106, C107, C108, C109, C110, C111, C112, C113, C114 |
| [17](17-blueprints-and-templates.md) | 20 | 1 | 19 | C139, C140, C141, C142, C143, C144, C145, C146, C147, C148, C149, C150, C151, C152 |
| [18](18-scheduling-drift-dependencies.md) | 21 | 1 | 20 | C38, C60, C153, C154, C155, C156, C157, C158, C159, C160, C161, C162, C163, C164, C165, C166, C167, C168 |
| [19](19-events-notifications-audit.md) | 34 | 1 | 33 | C169, C170, C171, C172, C173, C174, C175, C176, C177, C178, C179, C180, C181, C182, C183, C184 |
| [20](20-vendors.md) | 9 | 1 | 8 | C185, C186, C187, C188, C189, C190, C191, C192 |
| [21](21-resources-scans-search.md) | 22 | 1 | 21 | C11, C194, C195, C196, C197, C198, C199, C200, C201, C202, C203, C204, C205, C206, C207, C208, C209, C210, C211 |
| [22](22-intelligence.md) | 18 | 1 | 17 | C212, C213, C214, C215, C216, C217, C218, C219, C220, C221, C222, C223, C224, C225, C226 |
| [23](23-flows.md) | 31 | 1 | 30 | C228, C229, C230, C231, C232, C233, C234, C235, C236, C237, C238, C239 |
| [24](24-self-hosted-reference.md) | 10 | 1 | 9 | C243, C244, C245, C246, C247, C250 |
| [25](25-other-integrations.md) | 13 | 1 | 12 | C252, C253, C254, C255, C256, C257 |
| [26](26-scope-and-tiers.md) | 8 | 1 | 7 | C47, C48, C122, C139, C260, C261 |
| [27](27-reference-materials.md) | 6 | 1 | 5 | C263, C264, C265, C266 |
| [28](28-data-model.md) | 8 | 1 | 7 | C77, C120, C146, C267, C268, C269 |
| **All** | **401** | **24** | **377** | **233 entries** |

## What this means for infraweave

- The plan must carry every product decision to a decision record before
  the phase it blocks, and every capture to a fixture before the handler it
  blocks is frozen.
- Captures need an authorized Spacelift account; where none is available,
  the plan must record the behaviour infraweave chose and why, rather than
  present a guess as Spacelift's.
- Many captures concern the run state machine (C33 to C46), the environment
  and precedence rules (C27 to C29, C98 to C104) and the webhook and audit
  delivery contracts (C176 to C184); those areas need the most fixtures.

## Inferred and undocumented

This report adds no claims of its own. Each entry restates an open item
from the cited report, where its evidence and markers are.

## Versions at the research baseline

| Component | Version |
|---|---|
| Spacelift SaaS GraphQL schema | introspection `b3255fe38f5e`, fetched 2026-10-28 |
| docs.spacelift.io | read 2026-10-28; self-hosted pages at v7.1.0 |
| Reports 01 to 25 | as fact-checked by RES-02 to RES-06 on 2026-10-28 |
| Reports 26 to 28 | as written for RES-07 on 2026-10-28 |

## Sources

- Reports [01](01-spacelift-landscape.md) to [28](28-data-model.md) in this directory: their *(unverified)* markers and
  "Inferred and undocumented" sections.
- The drafters' notes for RES-02 to RES-07 and the reviews for RES-02 to RES-06,
  summarised in [31](31-review-log.md).
- The marker coverage check: a `python3 -I` script that lists every
  *(unverified)* marker in reports 01 to 28 with the entry covering it, and
  fails on a marker with no entry or an entry that does not name the
  marker's report.
