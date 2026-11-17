# 32: Spacelift console survey

Checked against: Spacelift's SaaS web console as an Enterprise trial account showed it on 2026-11-17, read without saving anything (sections 2 to 15) and then driven in two approved sessions that created, ran and deleted objects (sections 16 and 17); the code on `main` at commit `52c6180b` for sections 16, 17 and 20; the [implementation plan](../../implementation/plan.md), decisions 0006 to 0024 and reports 09 to 31 at commit `3d822e48`; introspection `b3255fe38f5e`.

Research baseline: **2026-11-17**.

This report records what Spacelift's web console shows of the product, page
by page, and compares each observation with the plan's later phases, P13 to
P28, and the decisions they cite. The rewrite of those phases in the
[implementation plan](../../implementation/plan.md) rests on it. Earlier
reports read the GraphQL schema, the documentation, the Terraform provider
and Spacelift's public repositories; none had read the console, which shows
defaults, bounds, form order, tier gates and help text that the schema does
not carry. Sections 16 and 17 record what the console did when driven: the
runs, states, policy inputs and audit entries that only writes produce.
Every observation here is a summary in this report's own words.
It quotes no documentation and no console text beyond short labels, names no
account, user or ID, and keeps no screenshots. Route paths omit the host.
The terms are in the [glossary](glossary.md); open items are records of
[`open-questions.json`](../../data/open-questions.json), cited by ID.

| Label | Means |
|---|---|
| **[UI]** | Seen in the console on 2026-11-17: a page, form, field, default, bound or hint |
| **[Driven]** | Observed on 2026-11-17 by driving the console: creating objects, pushing commits, acting on runs and reading what followed |
| *settles* | The observation answers the capture named, whose record is resolved |
| *consistent* | The plan or a decision already says the same |
| *new* | A detail the plan at `3d822e48` lacks; the phase named carries it |
| *conflict* | The observation contradicts a report, a decision or the plan; [section 19](#19-contradictions-and-their-resolution) says how each is resolved |
| *open* | Only a write shows it; the capture named carries it |

## 1. Method and limits

- **Read only.** The survey of sections 2 to 15 opened pages, tabs, menus and create forms,
  read their fields, options, defaults and hints, and closed each form with
  its Cancel control, Escape or by navigating away. It saved, toggled,
  created, deleted, invited, connected and installed nothing, with one
  exception: the SCIM card's set-up control acts on click rather than
  opening a form ([section 6.5](#65-scim)).
- **Operation names.** Every console request is a `POST` to `/graphql`, and
  the browser's request list does not show the operation names in the
  bodies. The reference operations named below are matched by the fields a
  page shows, not read from traffic.
- **An empty account.** At survey time the account held no stacks, no
  policies and no private worker pools, so pages that exist only for one of
  those (a policy's workbench, a stack's settings, a private pool's workers)
  could not be read. [Section 21](#21-what-the-trial-could-not-show) lists
  each with the capture that carries it.
- **Tier.** The trial runs at the Enterprise tier, so every gated page
  opens. Tier gates appear as the billing page's plan cards and as trial
  banners, not as refusals.
- **Driven sessions.** Two later sessions on the same account, each with an
  approved list of actions, created stacks, contexts, policies, a module, a
  blueprint and a template on a throwaway repository, drove runs through
  pushes, triggers, reviews, confirmations, discards, retries, replans and
  tasks, and deleted everything afterwards. They read operation names from
  the page's own requests, and policy inputs from the policies' sample
  views; the inputs are checked in, redacted with stable placeholders and
  every key kept. Only the public worker pool existed, so nothing that needs
  a private worker ran.

## 2. Navigation and the console's areas

The sidebar groups pages as follows **[UI]**.

| Group | Pages and routes | Plan phase |
|---|---|---|
| Get Insights | Dashboard `/dashboard` (tabs Overview, Metrics, DORA and custom tabs), LaunchPad `/launchpad`, Notifications `/notifications` | P22, P24c, P18 |
| Ship Infra | Stacks `/stacks`, Resources `/resources`, Runs `/runs`, Templates `/templates`, Intent Projects `/intent-projects` | P10, P22, P21, P25b |
| Share Code and Config | Module Registry `/modules`, Providers `/providers`, GPG Keys `/gpg-keys`, Contexts `/contexts`, Blueprints `/blueprints`, Templates Workbench `/templates-workbench`, Repos `/spacelift-repos` | P17, P17b, P6, P21, P19b |
| Enforce Guardrails | Spaces `/spaces`, Policies `/policies` | P4, P13 |
| Integrate Services | Integrations `/integrations`, Plugins `/plugins`, Flows `/flows` | P12, P15, P18, P20b, P18c |
| Manage Organization | Billing, Usage, Worker Pools `/worker-pools`, Audit Trail `/audit-trail`, IAM `/settings/users`, Settings `/settings` | P3b, P18b, P8, P14 |

- **LaunchPad** holds learning tracks with certificates, and the user menu's
  Mission control shows them. They are the User Guide operations
  (`userGuide*`, `userGuideCertification*`, `onboardingProgress`), which
  P22 serves from an operator-extensible catalogue. *consistent*
- **The command palette** (Cmd-K) offers create actions for stacks, modules
  and contexts, navigation to every page, and search scopes for stacks,
  modules, policies and contexts only; a search hands off to the list
  page's own search. *new* for P24c.
- **List pages** share one pattern: saved views, a filter builder whose
  fields are the search operation's filter fields, a sort in the URL query
  (`sort`, `sortDirection`), column settings, and an empty state with a
  create control and a documentation link. *new* for P24c.

## 3. Policies

| Observation | Bears on | Class |
|---|---|---|
| `/policies` has tabs for the account's policies and for templates. Filters: folder, labels, space, type | P13, P24c | *consistent* |
| The create form is a drawer: name, type (default plan), space, description and labels, with a hint that the label `autoattach:<label>` attaches the policy by label; continuing leads to a body editor | P13 | *consistent* |
| The type list offers six types: approval, push, plan, trigger, notification and Intent. `LOGIN`, `ACCESS`, `INITIALIZATION` and `TASK` cannot be created there | P13, P13b | *new* |
| Login policies have their own list under `/settings/login-policy`, marked inactive while the IAM strategy is in effect | P13b | *new* |
| `/policies-templates` lists 59 templates with a body preview and an import control each; filters by labels and type. The bodies declare `package spacelift` and import `rego.v1` | P13b, C54 | *new* |
| The template types seen are trigger, push, approval, plan and notification; several plan templates read third-party scanner results from the policy input | P13b, P20b | *consistent* |
| A policy's workbench, samples and simulation live on the policy's own page, which an account without policies cannot show | P13b | *open* (C273); section 16.1 narrows it |

The console treats `TASK` and `INITIALIZATION` as types that exist but are no
longer created, which matches their deprecation in decision
[0009](../decisions/0009-policy-engine-opa.md). The template catalogue is
smaller than the MIT library's 67 templates (report
[09](09-policies.md)), so the in-product catalogue is a selection or a
different source; C54 stays open with that narrowed.

## 4. Worker pools and runtime

| Observation | Bears on | Class |
|---|---|---|
| The pool list shows workers, busy workers and queue length per pool; the public worker pool shows its worker count as a ceiling ("up to 2" on the trial) | P14 | *new* |
| A pool's page has two tabs: its queue, sorted by position, with filters for commit, drift detection, prioritized, stack name, status and type; and the stacks and modules that use it | P14 (`workerPoolSearchSchedulableRuns`, `workerPoolSearchUsers`) | *consistent* |
| The create drawer takes a name, a certificate signing request file, a space, a description, labels and a switch that reveals a limit on concurrent drift detection runs | P8, P16 | *consistent*; the limit's field is *open* (C274) |
| No form, page or setting concerns Kubernetes; the controller's pool is an ordinary pool, and the system role Worker pool controller exists for the controller's API key | P14 | *consistent* |
| `/settings/runtime-security` sets two default runner images, one for public and one for private workers; blank means the vendor's default | P14, P20 | *consistent* with `accountUpdateDefaultWorkerPoolRunnerImages` |
| A private pool's page, its workers, drain, reset, cycle and configuration download need a pool, which the account lacked | P8, P14 | *open* (C275) |

The blank-means-vendor-default rule supports the P20 analysis that each
vendor needs its own default image ([section 20](#20-gaps-in-shipped-work)).

## 5. Cloud integrations and OIDC

| Observation | Bears on | Class |
|---|---|---|
| The AWS drawer takes a name, a space (default the top space), one role ARN, "Assume role on worker" (off), a duration (default 1 hour, from 15 minutes up to the role's maximum and no more than 1 hour when the server assumes the role), an optional region, labels, auto-attach (off) and "Enable tag session" (off) | P15 | *consistent* with report [12](12-cloud-integrations-and-oidc.md) section 2 |
| The drawer shows an example trust policy whose external ID condition matches the account's prefix with a wildcard | P15 | *consistent* |
| The Azure drawer takes a name, a space, the tenant ID, an optional default subscription, labels and auto-attach. Admin consent is recorded afterwards by a confirm action and cleared by another; the console states that Spacelift neither grants nor checks the permissions | P15 (`azureIntegrationConsentConfirm`, `azureIntegrationConsentClear`) | *new*: no phase named these operations |
| GCP has an integration card that links to documentation only; GCP access is the run's OIDC token | P15 | *consistent* |
| `/settings/oidc-subject-template` sets the subject template, blank for the default `space:{spaceId}:{callerType}:{callerId}:run_type:{runType}:scope:{scope}`, with a warning to change trust policies first | P15 | *consistent* with decision [0006](../decisions/0006-authentication-and-tokens.md) |

## 6. Identity, access and sessions

### 6.1 Management strategy and login policies

- `/settings/management-strategy` offers two strategies, exactly one in
  effect: identity access management, where role bindings decide, and login
  policy, where a Rego policy decides at login and grants roles through its
  rule. The inactive strategy's settings are kept **[UI]**. These are
  `MANAGED_USERS` and `LOGIN_POLICIES` of decision
  [0007](../decisions/0007-authorization-spaces-and-roles.md). *consistent*
- Login policies are listed and created on their own settings page, not
  among the policies of `/policies` **[UI]**. *new* for P13b and P24c.

### 6.2 Users and IdP groups

- The invite dialog takes a username (the GitHub login on a GitHub-login
  account), an e-mail address, an optional Slack member ID and role
  bindings built as role and space pairs **[UI]**. *consistent* with P3b and
  P4b.
- The IdP group mapping dialog takes a group ID, a description and the same
  role and space pairs **[UI]**. *consistent* with P23 task 2.

### 6.3 API keys

- Creation is a two-step page: the key's details, then its access. The
  secret is shown once, as a file **[UI]**. *consistent*
- The type is secret (default) or OIDC. An OIDC key takes an issuer URL, a
  client ID that is the audience, a subject expression and claim mappings,
  of which only the `groups` claim is accepted **[UI]**. *consistent* with
  0006.
- An expiration switch is off by default; its field and bounds, and the
  access step's contents, need a valid first step **[UI]**. *open* (C276)
- A page switch allows API keys to create, update and delete API keys
  (`accountToggleAPIKeyManagementFromNonHumans`) **[UI]**. *consistent*

### 6.4 Single sign-on

- `/settings/sso` warns that switching to SSO invalidates the current users
  and group mappings, warns that after the trial only Enterprise accounts
  can change SAML and SCIM settings, and advises creating an API key as a
  fallback credential first **[UI]**. *new* for P23.
- The SAML drawer shows the assertion consumer URL `/saml/acs` and the
  entity ID `/saml/metadata`; offers NameID formats transient (default),
  e-mail address and persistent; has a "Dynamic configuration" switch that
  replaces the static metadata XML; takes claim mappings; and names the
  attributes FirstName, LastName and Teams **[UI]**. The switch's field is
  *open* (C283).
- The OIDC drawer shows the redirect URL `/oidc/exchange`, requires the
  `openid` and `email` scopes and requests `profile` and `groups` when the
  provider offers them, and takes a client ID, a secret, a provider URL and
  claim mappings **[UI]**. *consistent*

### 6.5 SCIM

The SCIM card's set-up control creates the SCIM OAuth client as soon as it is
clicked: no form precedes it **[UI]**. The dialog that follows shows the
connector base URL `/scim/v2`, the token endpoint `/oauth2/token` for the
client-credentials grant, a client ID prefixed `scim-` and a client secret
shown once; the card then reads active, and the audit trail records the
action `account.create_oauth_client_for_scim`. `createOauthClientForSCIM`
therefore takes no input, which P23 states. *new*

### 6.6 MFA and sessions

- `/settings/mfa` needs single sign-on first: without an SSO provider the
  page offers only a link to set one up. With SSO, the organization owner
  can require every member to register a security key at next login
  **[UI]**. *new*: decision 0006 did not state the precondition. Whether
  the API refuses enforcement without SSO is *open* (C282).
- `/settings/sessions` lists every session with its user, ID, sign-in
  method, IP address, creation and expiry, a revoke control per row and one
  that ends all sessions. A web session signed in through GitHub expires 10
  hours after it starts, and no setting changes the lifetime **[UI]**. This
  settles C119 and *conflicts* with decision 0006's browser session
  defaults of 12 hours idle and 7 days absolute.
- Personal settings let a member register security keys (after SSO), see
  the IdP groups assigned to it, connect GitHub, Slack and Microsoft Teams
  accounts, and choose to be notified about ignored runs **[UI]**.
  *consistent*

## 7. Roles and spaces

| Observation | Bears on | Class |
|---|---|---|
| `/settings/roles` lists four system roles: Space reader, Space writer, Space admin and Worker pool controller. No drift detection system role appears | 0007, C116 | *conflict* with infraweave's seeded `drift-detection-manager` |
| The custom role form groups actions under stacks, runs, scheduling, space, tasks, context, worker pool, module, Terraform provider, Intent, templates and integrations; space read is preselected | 0007, P4b | *consistent* |
| No action governs policies, webhooks, the audit trail, users, API keys, blueprints, plugins or cloud integrations other than Intent's AWS attachment and Azure consent; those stay with space admins | 0007, C253 | *consistent* |
| The scheduling group has create, update and delete for drift detection, scheduled deletes, scheduled tasks and scheduled runs | P16 | *consistent* |
| The stack creation wizard has an "Assign roles" step, which binds roles to the stack itself | 0007 stack bindings, P24c | *new*; its contents are *open* (C286) |
| `/spaces` has list and diagram views; a space takes a name, description, labels, parent and an inheritance switch | P4 | *consistent* |
| `/settings/spaces` has a switch that lets space admins create and edit child spaces; inheritance stays outside their reach | P4, 0007 | *new*: narrows 0007's open point on what such admins may edit |

## 8. Module and provider registry

- **Modules.** `/new/module` is an eight-step wizard: source (space, GitHub
  or raw Git, repository, branch, project root, sparse checkout paths),
  details, behaviour, availability (sharing with other spaces), cloud
  integrations, policies, contexts and a summary. Steps cannot be skipped,
  so steps 2 to 4 need a valid source **[UI]**. The module list sorts by
  favourites and filters on twenty fields. *new* for P17; steps 2 to 4 are
  *open* (C277).
- **Mark as bad.** The custom role catalogue has an action to mark a module
  version as bad, alongside publish, delete version and local workspace
  uploads **[UI]**. It is the reference's `versionYank`. *consistent*
- **Providers.** The create drawer takes a type, a space, a description and
  labels. The type is unique in the account and holds lowercase letters and
  digits only. There is no visibility at creation and no version form:
  versions are created, given platforms, published and revoked through the
  API **[UI]**. The digits *conflict* with report
  [15](15-module-and-provider-registry.md)'s lowercase letters only.
- **GPG keys.** The drawer takes a name, the ASCII-armoured public key and a
  description; keys belong to the account, with no space **[UI]**.
  *consistent* with decision [0014](../decisions/0014-module-and-provider-registry.md).

## 9. Events, notifications and audit

| Observation | Bears on | Class |
|---|---|---|
| `/new/webhook` takes a name, an endpoint, a space, a secret, an enable switch, a retry switch whose help promises up to three retries on a 5xx, labels and custom headers | P18 | *consistent* with decision [0015](../decisions/0015-events-notifications-webhooks-audit.md) |
| The notification inbox shows 14 days, newest occurrence first, with filters for body, dismissed, grouping key, severity, target, title and type | P18 | *consistent* |
| Slack connects through Slack's OAuth app installation; members can store a Slack member ID | P18 | *consistent* |
| The audit trail's log tab carries a 14-day label, filters on action, affected and related resource, event type and username, and shows actions as `<resource>.<verb>` and actors as `<provider>::<login>` | P18b, C180 | The label is the default time range, and retention is 30 days (section 16.9, C279) |
| The audit trail's configuration is one webhook per account: endpoint, optional secret, enable, a retry switch with the same three-retries help, "Include runs" for run state change events, and custom headers | P18b, C181, C182 | *new*: the retry switch narrows C181 |
| `/settings/usage` has a workers tab for the billing cycle that counts private workers only, and a runs tab for the last 31 days with private and public minutes and a chart by run type or state; both export CSV | P18b, C280 | *new* |
| `/settings/limits` shows a 60-day run log retention and a switch that caps the runs one VCS event creates | P10, P12b | *consistent*; the cap's bound is *open* (C285) |

Actions seen in the trail include `stack.update`, `stack.delete`,
`run.trigger`, `run.confirm`, `github_integration.update` and
`account.create_oauth_client_for_scim`, each with event type `CREATE`,
`UPDATE` or `DELETE` **[UI]**. They extend, but do not complete, the list C180
asks for.

## 10. Contexts, blueprints and templates

- **Contexts.** `/new/context` is a five-step wizard: details with a live
  count of the stacks the auto-attach labels would match, attachments,
  environment variables and mounted files, hooks, and a summary; a context
  can be created from any step **[UI]**. *consistent* with P6.
- **Blueprints.** The create drawer takes a name, space, description and
  labels and creates a draft; the body is edited afterwards. A banner steers
  users to templates **[UI]**. *consistent* with P21.
- **Templates.** `/templates` is the catalogue users deploy from;
  `/templates-workbench` is where templates are written. Creating a template
  takes a name, space, description, labels, the first version's identifier
  (`0.0.1` suggested) and optional Markdown deployment instructions. The role
  catalogue adds actions for deployments, deployment tasks and upgrading a
  deployment's version **[UI]**. *consistent* with P21; the instructions
  field is *new*.

## 11. Source providers

- **VCS integration forms** (GitLab, Bitbucket Data Center and others):
  integration name (unique and immutable), default for all spaces or
  space-specific with a space, API host URL, user-facing host URL, token
  (and username for Bitbucket Data Center), "Use Git checkout" (needed for
  sparse checkout), individual checks with "failures only" and "skip
  unchanged stacks" options or aggregated checks, labels and a description
  **[UI]**. *consistent* with decision
  [0012](../decisions/0012-vcs-integration-model.md).
- **VCS agent pools.** The create dialog takes a name and a description
  only. No VCS integration form has an agent pool field; the Bitbucket Data
  Center card says it connects through agent pools **[UI]**. How an
  integration reaches its pool stays *open* (C77).
- **Origin** is Cursor's Git platform, offered as a VCS provider card
  **[UI]**. *consistent* with P19b's `ORIGIN` provider; settles part of C11.
- **Repos** (`/spacelift-repos`) are Git repositories hosted by Spacelift,
  with name, labels and space; the billing page lists an internal source
  control system among Enterprise features **[UI]**. *consistent* with
  P19b; the tier *conflicts* with `features.json` (section 20).

## 12. Plugins, resources and scans

- **Plugins.** `/plugins` lists installed plugins and a template catalogue
  with a control to create custom templates. The catalogue holds thirteen
  templates: AWS SAM, Checkov, Environment Manager, Envsubst, Infracost,
  Module Test Coverage, OpenTofu Tracing, Sops, SSM Parameter Store,
  Terrascan, Trivy, Trufflehog and Wiz **[UI]**. This settles C256.
- **How a plugin works.** A plugin is a parameterised bundle of phase hooks
  (`before_init`, `before_plan`, `after_plan`) attached by label. It writes a
  Markdown report shown on the run, and scanners write their results into
  the plan policy input under `third_party_metadata.custom.<plugin>`, where
  plan policies set thresholds **[UI]**. *consistent* with report
  [25](25-other-integrations.md); it places scanning in P20b and P13, not
  P22b.
- **Resources.** `/resources` has one view, IaC management, grouped by
  stack, provider or type, with filters for drift, labels, parent, provider,
  resource type, space, stack and vendor, columns for drift and vendor, and
  50 rows a page **[UI]**. *consistent* with P22's managed entity
  operations.
- **Scans.** No page, tab, filter or setting concerns scans, discovery or
  unmanaged resources; the billing page lists unmanaged resources as coming
  soon on self-hosted **[UI]**. The worker pool form has no discovery type
  (C194). This supports P22b's `x-infraweave-basis: schema-only`.

## 13. Intelligence and Intent

- `/settings/ai` enables AI features for the organization after the terms
  are accepted; the trial had them off **[UI]**. *consistent* with P25 task
  3.
- `/integrations/ai` lists AI integrations with provider, name, status,
  model and space. A Bedrock integration supplied by Spacelift is listed,
  disabled, before any is created **[UI]**. The create drawer takes a name,
  description, space, provider (Anthropic, OpenAI, Google or Bedrock), an
  API key, an optional base URL and labels. *consistent* with decision
  [0019](../decisions/0019-ai-provider-boundary.md)'s installation default
  and base URLs; the seeded entry is *open* (C284).
- The Infra Assistant has chat and build modes, an approval mode that asks
  before each action, and suggested prompts grouped as troubleshoot,
  understand, find, and access and policy **[UI]**. *consistent* with P25.
- An Intent project takes a name, description, space, worker pool (the
  public worker pool by default), an optional runner image and labels; the list
  filters on IaC access, locked and worker pool **[UI]**. *consistent* with
  P25b.

## 14. Account settings, billing and tiers

| Observation | Bears on | Class |
|---|---|---|
| `/settings/security` holds a security contact e-mail (`accountUpdateSecurityEmail`) | P3b | *consistent* |
| `/settings/oauth-redirect-allowlist` takes hosts as a bare hostname or one leading `*.`, with no scheme, port or path | P3b, 0006 | *consistent* |
| `/settings/account-deletion` marks the account for deletion: run creation stops at once, and permanent deletion opens after 7 days | P3b (`accountToggleDeletionMark`, `accountCanBeDeletedAt`) | *consistent* |
| The billing page shows four plans: Starter (with 10 users and 2 public workers), Business, Enterprise and Self-Hosted. It shows no Free or Starter+ plan | 0021, report [26](26-scope-and-tiers.md), `features.json` | *conflict* (section 17); resolved by the 2026-11-17 pricing read (C287) |
| Starter lists OIDC integrations, the private module registry, webhooks, policies and notifications; Business adds drift detection, blueprints, templates, advanced scheduling, the private provider registry and targeted replans; Enterprise adds concurrent VCS connections, the audit trail, SAML, OIDC API keys, internal source control and MFA | `features.json` | *consistent* except drift detection and internal source control |

## 15. Dashboard and runs explorer

- The overview tab shows stack states, runs needing attention, users active
  in the last 28 days and recent failures, as widgets a member arranges and
  saves as tabs **[UI]**.
- The metrics tab shows drift detection coverage, stack failures, daily
  deployments, managed resources, median run duration and resource health;
  the DORA tab shows lead time for changes, time to recovery, deployment
  frequency and change failure rate over 30 days **[UI]**. Which operations
  feed them is *open* (C281); the reference has `metrics` and
  `metricsRange`, which P22 serves.
- `/runs` explores runs across the account with a filter builder, a date
  range (default the last hour) and grouping by trigger, type, stack, module,
  state, branch, repository, commit or user **[UI]**. *new* for P24c.

## 16. Driven: policies, runs and the registry

Every observation in this section is **[Driven]**. The first session
created two OpenTofu stacks on one repository, the second depending on the
first, a Pulumi stack, an Ansible stack, six policies, a module and a
blueprint; pushed commits and acted on the runs they created; and deleted
everything afterwards. The policy inputs it sampled are checked in, redacted,
as [`test/fixtures/policy-inputs`](../../../test/fixtures/policy-inputs/README.md).

### 16.1 The policy page: samples, simulation and templates

| Observation | Bears on | Class |
|---|---|---|
| Creating a policy is two steps: the drawer carries name, type, space and labels to the body editor, and nothing exists until the body is saved. The editor opens a commented template for the type, and an engine select on Rego V1 | P13 | *consistent* with decision 0009's default; the API's own default is *open* (C49) |
| Each type's template documents its input, and says that a preamble defines the predefined rules, so giving one another type is a conflict error | P13 | *consistent* with decision 0009's preamble |
| A policy's page has tabs for the body, "Used by" and samples, and a simulation panel. A notification policy's page has no "Used by" tab | P13, P13b | *new*; narrows C273 |
| The samples tab lists evaluations newest first, with an outcome badge and filters per type: outcome, pull request and branch for push; run type and drift for plan; module and run type for notification. A sample opens read-only as JSON, and only root admins may read sampled inputs | P13b | *new*; the operation names stay *open* (C273) |
| Simulation takes a sample, lets its input be edited, and answers the whole decision document; it stores nothing and adds no sample | P13b | *consistent* with decision 0009 |
| The push decision has 20 keys, `reset_flag` and `priority` among them, which the editor's template does not list; the plan decision has `deny`, `flag`, `info`, `reset_flag`, `sample` and `warn` | P13 | *new*; narrows C51 |
| No page shows the OPA version | P13 | *open* (C57) |
| The catalogue's first page shows 50 of its 59 entries, every one a library template. The library templates it lacks are the six login templates and nine others (two approval, three plan, one push and three trigger), as many as the entries left unread | P13b | *open* (C54, narrowed) |

### 16.2 Policies on runs

| Observation | Bears on | Class |
|---|---|---|
| A push policy whose rules match nothing ignores the push; the commit is listed among the stack's ignored runs, naming the policy, and the stack page offers a sync | P13 | *consistent* |
| Without a push policy, a commit that touches nothing in a stack's project root is ignored for that stack, and its page says so | P12b | *consistent* |
| A pull request event and the branch push of the same commit create one proposed run; the pull request event's input already lists that run in `in_progress` and has no `vcs_integration` key | P12b, P13 | *new* |
| A push policy `cancel` naming a run that awaits confirmation discards it: `UNCONFIRMED` to `DISCARDED` in the second the new run is queued, with the note "canceled by push policy in favor of run <id>". The stack's state becomes `DISCARDED`, and the discarded run cannot be retried because the stack has moved on | P13, 0008 | *settles* C37 |
| The approval policy is evaluated when a run enters `QUEUED`, before it starts, and again on entering `UNCONFIRMED`, for proposed runs as for tracked ones. A review counts only in the state it was made in; earlier ones reach the policy in `reviews.older`. Undecided, the run waits, offering approve and reject with an optional note, and `UNCONFIRMED` shows no confirm control until the policy approves | P13, 0009 | *consistent*; per-state reviews and proposed runs are *new* |
| A reject in `QUEUED` fails the run at once, with the note "Run rejected by approval policies", and the run offers a retry | P13, 0008 | *settles* C40; `PENDING_REVIEW` and `UNCONFIRMED` are *open* (C291) |
| With two approval policies, one approving and one rejecting, the aggregate is a reject and the run fails | P13, 0009 | *settles* C41 |
| A plan policy's `warn` or `deny` adds a history entry with its messages, deny first; an `allow` with no message adds none. A deny on a proposed run fails it with the note "denied by plan policy" | P13 | *new* |
| A plan policy's flag is absent from its own evaluation's input, present in `run.flags` for the same run's later approval, trigger and notification evaluations and in the next run's `previous_run`, and absent from runs a trigger policy creates | P13, 0009 | *open* (C50, narrowed) |
| A denied run's GitHub check reads "Job has failed" with a link to the run and no deny message, and no pull request comment is posted | P12b | *new* |

### 16.3 Trigger and notification policies

| Observation | Bears on | Class |
|---|---|---|
| The trigger policy is evaluated when a tracked run ends `FINISHED`, `FAILED` or `DISCARDED`, a run a push policy discarded included. A `trigger` queues a tracked run on the named stack in the same second, on that stack's own tracked commit, with `triggered_by` `policy/<stack>/<run>` and `trigger` null | P13, P16 | *new* |
| Each `stacks[]` entry carries `queued_runs`, an integer, and no `worker_pool`; the trigger input's `stack.space` is null where the other types carry an object. The input adds `run.states_history`, `run.priority` and a top-level `workflow` | P13 | *settles* C55 for runs; module events stay *open* |
| A notification policy is evaluated on every state change of every run in its space, vendor stacks included. Its input carries `policy_receipts` at the `run_updated` level, one per policy evaluated on the run, beside `plan_policy_decision`, `timing` and `urls` | P18 | *settles* C184 |
| The inbox sits under Get Insights with an unread count; each `inbox` rule adds an entry with the title, body and severity the rule set and a type tag | P18 | *consistent* |

### 16.4 Stack dependencies

| Observation | Bears on | Class |
|---|---|---|
| A stack's dependencies tab shows what it depends on and what depends on it around the stack, a graph view, and an add control with an "Always trigger runs" switch, off by default | P16 | *new* |
| An output reference maps an upstream output, listed with its current value, to an input name, and has its own "Trigger always" switch; a dependency with references runs the dependant only when a referenced output changes | P16 | *consistent* with report 18 |
| When one commit changes both stacks, the dependant's run is created with the upstream's, waits in `QUEUED` with a history entry naming the upstream run and its state, and plans after the upstream finishes, with its new output value | P16 | *new* |
| A commit that changes only the upstream still creates a dependant run, at the moment the upstream run is created, on the dependant's own tracked commit, with `triggered_by` `dependencies/<stack>` and `trigger` naming the upstream run and stack | P16, 0008 | *new* |
| The referenced variable shows in the dependant's environment tab as computed, with the upstream stack as its source, and is absent from the plan policy input's `runtime_config.environment` | P16, P13 | *new*; C101 |
| No numeric bound shows on dependencies or references | P16 | *settles* C278 for dependencies |

### 16.5 Schedules and stack settings

| Observation | Bears on | Class |
|---|---|---|
| A stack's settings are details, source code, vendor, behaviour, integrations and roles, with deletion in a danger area; dependencies, schedules and notifications are tabs of the stack, not settings | P16, P24c | *settles* C278 |
| The scheduling tab creates four kinds, drift detection, stack deletion, task and run, each marked as working on private workers only | P16 | *new*; firing is *open* (C290) |
| Drift detection takes one or more cron expressions (every 15 minutes by default), a timezone (the browser's by default), and reconcile and ignore state, both off; `*/5` passes with no warning | P16 | *consistent*; C157 narrowed |
| A deletion schedule takes a date and time, local or UTC, and deletes or keeps resources, keep by default | P16 | *new* |
| A task schedule takes a command and either cron expressions with a timezone or one date and time | P16 | *consistent* |
| A run schedule takes an optional name, an optional custom runtime configuration, and a recurrence or one time | P16 | *open* (C153, narrowed) |
| A task schedule on the public pool is accepted, then shown with an error that its tasks are not scheduling on an unsupported pool | P16 | *new* |
| Behaviour holds the wizard's defaults: public pool, no runner image, run promotion, autodeploy, autoretry and local preview off, secret masking on, deletion protection off, and transfer of sensitive outputs across dependencies off | P10, P16 | *consistent*; sensitive outputs are *open* (C288) |
| Deleting a stack is in its settings: delete or keep resources, keep by default, confirmed by typing "delete", and asynchronous. An Ansible stack offers no choice and warns that its configuration stays | P10, P20b | *new* |

### 16.6 Modules

| Observation | Bears on | Class |
|---|---|---|
| The wizard's details step names the module behind a fixed `terraform-<provider>-` prefix, with the provider inferred from the repository name or typed, and creates the module | P17 | *settles* C277 |
| The behaviour step offers the workflow tool (OpenTofu by default, Terraform or custom), a runner image, administrative, local preview and deletion protection, on by default; the availability step lists the origin and inherited spaces and adds others; the policy step offers push, approval, plan and trigger | P17 | *settles* C277 |
| "Trigger" on a module creates a version from the branch head at once, with no form, numbered by `module_version` in `.spacelift/config.yml` | P17, 0014 | *consistent* |
| A push that changes `module_version` creates that version within seconds; a push that leaves it unchanged creates none, and nothing appears among failed versions | P17, 0014 | *new*; tags are *open* (C293) |
| Triggering a number that exists fails with an error naming the version and commit | P17, 0014 | *consistent* with 0014's no-reuse rule |
| A version's test case runs `QUEUED`, `INITIALIZING`, `PLANNING`, `APPLYING`, `DESTROYING` and `FINISHED`, with no confirmation and no plan policy | P17, 0008 | *new* |
| A version's page shows its README, changelog, inputs, outputs, dependencies, resources, consumers, test cases, submodules and examples; its actions are copy ID, instructions, edit description, mark as bad and delete, and the instructions give a `source` of the form `<host>/<owner>/<name>/<provider>` | P17 | *consistent* |

### 16.7 Blueprints

| Observation | Bears on | Class |
|---|---|---|
| A blueprint's ID is its name with a ULID appended; it starts as a draft | P21 | *new* |
| The body page has a schema tree, a YAML editor and a live form preview; the preview does not check input values against their validations | P21 | *new* |
| A body that fails the schema replaces the preview with one error card per failure, each naming its JSON path, and publishing is disabled | P21 | *new* |
| The schema the page serves requires exactly one of `stack` and `stacks`, caps `stacks` at 5, lets a text input omit `type`, and has no `clouds.gcp` | P21 | *open* (C140, narrowed) |
| Integrations attach only to a published blueprint | P21 | *open* (C289) |

### 16.8 Vendors

| Observation | Bears on | Class |
|---|---|---|
| The vendor step asks: CloudFormation a region, stack name, entry template file and template bucket, with no defaults; Kubernetes an optional namespace, a workflow tool and a kubectl version (1.37.1 by default); Terragrunt a version (1.2.2 by default), a tool (OpenTofu, Terraform or manually provisioned) and its version, smart sanitization on, run-all off, skip replan (beta) off, resource name prefixing off and managed state off; Ansible a playbook; Pulumi a login URL and a stack name | P20, P20b | *new* |
| A Pulumi stack needs a runner image, with none offered, and `before_init` and `before_apply` hooks before it can be created, which happens at the hooks step | P20 | *new* |
| A Pulumi run's `before_init` hooks run before the login and before the stack is selected, or created when missing; `after_init` hooks run after both. Spacelift installs a resource plugin of its own during initialization | P20 | *settles* C190 |
| Planning prints `pulumi preview` output and applying `pulumi up` output; a file backend under the workspace survives from plan to apply in the workspace archive, and no passphrase is asked for | P20 | *open* (C193, narrowed) |
| The Pulumi plan input carries `pulumi.steps`, changes keyed by URN, and Pulumi's own unknown-value sentinel, a constant UUID, for each value unknown at preview ([33](33-vendor-tool-documents.md) section 2.3); the console's changes view fails for a Pulumi run, pointing to `changesV2` | P20, P13, P22 | *new* |
| An Ansible run uses the `runner-ansible` image by default, plans with `--check` and applies with a real run; its plan input carries the play recap as `ansible`, and its changes carry host, playbook, role and task with no address | P20b, P13 | *settles* C193's Ansible part |
| An Ansible stack's header has a switch, on by default and kept across reloads, that shows the configuration management view in place of resources and outputs; no other vendor's stack shows it. An Ansible run's tasks tab lists hosts, playbooks and tasks, with check-mode badges while planning | P20b, P22 | *settles* C199 |

### 16.9 The audit trail

| Observation | Bears on | Class |
|---|---|---|
| The log tab lists 50 entries at a time by cursor, newest first, over a time range that defaults to 14 days, with presets from 1 hour to 14 days and a custom range; `auditTrailRetentionDays` answers 30 | P18b, 0015 | *settles* C279 |
| Entries are the mutations callers make: creating, updating, deleting, attaching and detaching stacks, policies, dependencies, schedules, modules, versions and blueprints, and triggering, confirming, reviewing, discarding, retrying and replanning runs. A GitHub user's actor is `github::<login>` | P18b, 0015 | *open* (C180, narrowed) |
| Work Spacelift does on its own records nothing: runs a push, a dependency or a policy creates, run state changes, policy evaluations, versions a push creates, failed calls, and attachments removed with a deleted stack. No entry lists a run state change | P18b, 0015 | *new*; C182 narrowed |
| An entry's body is the mutation's arguments: a policy's body, a schedule's command and a dependency's references are absent, a blueprint's whole template present | P18b | *new* |
| A detail drawer shows the metadata and the body; the filters are action, affected and related resource type and ID, event type and username; a version's related resource is its module, typed as a stack | P18b | *consistent* |
| Two sessions of one user are indistinguishable: no session, address or agent shows | P18b | *new* |

### 16.10 Resources and outputs

| Observation | Bears on | Class |
|---|---|---|
| The account's resources view counts outputs and each stack's root module as entities beside resources, groups by stack, provider or type, and models a Pulumi stack resource as type `module` | P22 | *new* |
| Ansible hosts and tasks have a configuration management tab beside IaC management | P22, P20b | *new* |
| A resource's drawer shows its ID (`<stack>/<address>`), type, drift, stack, the runs that created and last updated it, its parent, address, vendor and vendor data | P22 | *consistent* |
| The page's requests are `SearchManagedEntities`, `GetStackOutputs` and `SearchAnsibleHosts` with their suggestion queries | P22 | *consistent* |
| A stack's outputs tab lists each output's name and value with a copy control | P22 | *consistent*; sensitive outputs are *open* (C288) |

## 17. Driven: contexts, runs, tasks and templates

Every observation in this section is **[Driven]**. The second session
created a stack and two contexts on the same repository, ran tracked runs
and tasks on it, created, published and deployed a template, and deleted
everything afterwards.

### 17.1 Contexts

| Observation | Bears on | Class |
|---|---|---|
| The context wizard has details, attachments, environment, hooks and a summary, and creates from any step; variables and mounted files are secret by default, and files mount under `/mnt/workspace/` | P6 | *consistent* |
| A directly attached context has a priority, 0 by default, edited on the stack's contexts tab; of two that set one variable, the lower number wins, and either wins over an auto-attached context. The stack's environment tab lists one row per name, the winning value and its context | P6 | *consistent* with report 13 |
| An auto-attached context has no priority field and is detached only by its labels; the hooks view labels the auto-attached order reverse alphabetical | P6 | *open* (C99, narrowed); *conflict* with main (section 20) |
| Hooks nest: before a phase, the contexts' hooks in rank order, then the stack's; after it, the stack's, then the contexts' in reverse. A run's runtime configuration records the merged lists in that order | P6, 0008 | *consistent* with report 13 (C29 narrowed); the recorded lists are *new* |
| The console refuses a stack variable whose name an attached context sets | P6 | *open* (C101, narrowed) |
| A secret variable printed by a hook shows as five asterisks whatever its length; a secret mounted file printed with `cat` shows in clear | P6, 0013 | *consistent* for variables; infraweave masks mounted files too, as decision 0013 chooses |
| Mounted files land at `/mnt/workspace/<path>`; the checkout is `/mnt/workspace/source/`, and hooks run in the project root inside it | P9 | *conflict* with main (section 20) |

### 17.2 The run environment

| Observation | Bears on | Class |
|---|---|---|
| A run on the public pool has 17 `TF_VAR_spacelift_*` variables, `graphql_endpoint` among them, and six `SPACELIFT_*` ones: the API and OIDC tokens, two request rate settings, the sensitive output upload switch and a vendor binary cache flag; beside them `TF_IN_AUTOMATION`, `TF_APPEND_USER_AGENT`, `TG_TF_REGISTRY_TOKEN` and trace variables. The environment tab lists the 17 and the two tokens | P10, P15, P18b | *settles* C100; decision 0008 now names the five main lacks (section 20) |
| The workspace holds the OIDC token as a file beside the hook environment files | P15 | *consistent* |

### 17.3 Run states, discard, retry and targeted replan

| Observation | Bears on | Class |
|---|---|---|
| A tracked run passes `QUEUED`, `READY`, `PREPARING`, `INITIALIZING`, `PLANNING` and `UNCONFIRMED`; confirming adds a preparing phase, on another worker, before `APPLYING`. The history's simplified view, on by default, hides the ready and preparing entries | P10, 0008 | *consistent* |
| A tracked run with no changes goes from `PLANNING` to `FINISHED`, skipping `UNCONFIRMED` with autodeploy off | P10 | *consistent*; the force-applied case is C167 |
| Discarding at `UNCONFIRMED` asks no confirmation and ends the run `DISCARDED`; the run then offers a retry | P10 | *consistent* |
| A retry creates a new run on the same commit, triggered by the user, with the retried run's runtime configuration, so a hook removed from the stack since still runs. Neither run links to the other, and a finished run offers no retry | P10, 0008 | *settles* C10; *conflict* with main (section 20) |
| A targeted replan starts from the run's changes tab, where resources, not outputs, can be selected. The same run goes `UNCONFIRMED`, `PREPARING`, `PLANNING`, `UNCONFIRMED`: it restores the archived workspace with no checkout, no mounted files and no `INITIALIZING`, plans with `-target`, and evaluates plan policies again | P10, P13 | *consistent* with decision 0008 |
| A run's menu holds its origin, its runtime configuration, its ID, a log command and trace export; a running phase offers stop and force kill | P10, P24c | *new* |

### 17.4 Tasks

| Observation | Bears on | Class |
|---|---|---|
| A task is a command, with a switch to skip initialization; it runs `QUEUED`, `READY`, `PREPARING`, `INITIALIZING` and `PERFORMING` and ends `FINISHED` or `FAILED`, with no confirmation, and blocks the stack as a tracked run does | P10 | *consistent* |
| With initialization skipped, `INITIALIZING` still appears and only logs the skip: neither the init hooks nor the vendor's init run | P10, 0008 | *settles* C42; *conflict* with main (section 20) |
| A task leaves the stack's state as it was, is not listed among tracked runs, and offers to perform it again once ended | P10 | *consistent* |

### 17.5 Lock and queueing

| Observation | Bears on | Class |
|---|---|---|
| Locking takes an optional note, shown in the stack's banner; the locker can still trigger runs and tasks, which proceed as usual; unlocking asks no confirmation; the lock is not a stack state | P10 | *consistent*; another member's refusal is *open* (C294) |
| Runs blocked by the stack's own run wait in `QUEUED` with a banner naming it, are released oldest first across tasks and tracked runs, and are absent from the pool's queue | P10, P14 | *consistent* |
| No control prioritizes a run on the public pool; the pool's queue has a prioritized filter | P14 | *consistent* with decision 0008's public pool rule; private pools are *open* (C292) |

### 17.6 Templates and deployments

| Observation | Bears on | Class |
|---|---|---|
| Templates have a workbench for authors and a catalogue for users. Creating one takes a name, space, description, labels, the first version's identifier and Markdown instructions, and nothing about a source; a template with versions cannot be deleted | P21 | *consistent* |
| A version's page has the schema tree, the YAML editor and a live form preview. Publishing asks for confirmation, cannot be undone and makes the body read-only; a published version can be duplicated, deprecated or deleted | P21 | *new* |
| The template schema the console serves accepts `available_spaces` as an input type and `validations.required` on `boolean` and `select` inputs, has a deployment-level `space` and `scheduled_delete`, and adds `external_depends_on` to a stack. The default body uses `${{ inputs.* }}`, `${{ context.deployment.name }}`, `${{ context.template.name }}`, `${{ maps.* }}` and conditional expressions | P21, 0024 | *settles* C141 and C142 |
| A draft can be test-deployed; deploying from the catalogue takes the version from the card and asks for a name and the inputs, with no space | P21 | *new* |
| A deployment creates its stack and a tracked run at once, triggered by `blueprint_deployment/<name>`; its state follows the run, in progress, unconfirmed until the creator confirms, then success; its page shows stacks, outputs and a history of input changes | P21, 0008 | *open* (C150, narrowed) |
| A deployment's stack is managed: triggering, locking, its environment and its deletion are disabled, a banner points to the deployment, and only the body's labels apply. Behaviour the body leaves out takes values other than the wizard's: secret masking off, run promotion and sensitive output transfer on | P21 | *new* |
| Deleting a deployment queues a destroy run on its stack and deletes the stack, with no choice to keep resources | P21 | *new* |

## 18. Findings by phase

| Phase | What the survey changes in the plan |
|---|---|
| P6 | Context priority, auto-attachment and hook nesting as report 13 has them; the name clash the console refuses (C101); secret files unmasked |
| P10 | Run states as decision 0008 has them; retry with the retried run's configuration and not after `FINISHED`; a skipped initialization runs no init hook; the lock lets its holder run |
| P13 | Creatable types in the console; the plan policy input carries plugin results under `third_party_metadata.custom`; approval per state and on proposed runs; reject fails (C40, C41); push `cancel` discards (C37); trigger on failed and discarded runs; recorded inputs for `TestUnitPolicyContract` |
| P13b | Login policies on their own settings page; the in-product catalogue (59 templates) against the library (67), narrowed (C54); the policy page's samples and simulation (C273) |
| P14 | Public pool shown as a ceiling of workers; queue filters; runtime default images per pool kind; no Kubernetes surface |
| P15 | Azure consent operations, which no phase names at `3d822e48`; AWS duration bounds; subject template default |
| P16 | Four schedule kinds, each for private workers only; the pool drift limit (C274); stack settings (C278); dependency runs created with the upstream's and their `triggered_by`; firing on a private pool (C290) |
| P17 | Wizard steps (C277); versions from `module_version` on push and on trigger; test case states; tags (C293) |
| P17b | Provider type rule; no version form; GPG keys without a space |
| P18 | Webhook form; inbox window; `isGovSlackAvailable`, which no phase names at `3d822e48` |
| P18b | Audit webhook switches; retention 30 days (C279); callers' mutations only; actor format; usage tabs (C280); `runTraceSpans`, which no phase names at `3d822e48` |
| P18c, P18d | No change: Flows was outside the survey |
| P19b | Agent pool form; no pool field on integrations (C77); Origin and Repos settled |
| P20 | Blank runner image means the vendor's default; each vendor's wizard fields; Pulumi's required image and hooks; hook order (C190); preview and up (C193) |
| P20b | Thirteen plugin templates; plugins as scanners; Ansible check mode and its policy input; the configuration management switch (C199) |
| P21 | Template version identifier and instructions; blueprints steered to templates; blueprint drafts, validation and publishing (C140, C289); template schema (C141, C142); deployments and their managed stacks (C150) |
| P22 | LaunchPad as the User Guide operations; resources view with outputs and root modules as entities; dashboard data (C281); sensitive outputs (C288) |
| P22b | No scan surface at all; eight Scans operations that no phase names at `3d822e48` |
| P23 | MFA needs SSO; SCIM in one click; SAML options; the SSO switch warning; session lifetime |
| P24, P24b | No console finding; operations added above reach both through their resources and commands |
| P24c | Console patterns: drawers, wizards, saved views, filter builders, the palette, login policies' page |
| P25, P25b | Seeded provider integration; Intent's defaults |
| P27 | No change: packaging has no console surface |
| P28 | The captures this report raises, and the console as a capture source |

## 19. Contradictions and their resolution

Each item names what `3d822e48` holds, what the console shows, and where
the resolution lives.

1. **Browser session lifetime.** At `3d822e48`, decision 0006 defaults
   browser sessions to 12 hours idle and 7 days absolute, and DV-017 rests
   on Spacelift's lifetime being undocumented. The console shows 10 hours
   with no setting. Decision 0006 defaults both limits to 10 hours and keeps
   them configurable, and DV-017 records the configurability alone. The
   shipped defaults differ ([section 20](#20-gaps-in-shipped-work)).
2. **MFA without SSO.** The console offers MFA only after SSO is set up.
   Decision 0006 states that enforcing MFA needs an OIDC or SAML identity
   provider in the organization and answers `422` otherwise; C282 checks
   the API's own answer.
3. **Audit webhook retries.** Decision 0015 retries every failed audit
   delivery whatever `retryOnFailure` says, citing the docs; the console
   offers the audit webhook a retry switch promising up to three retries on a
   5xx. The decision keeps its schedule, states both sources and stores
   `retryOnFailure`, and C181 settles which holds.
4. **Audit retention.** The log tab's 14-day label stood against the docs'
   30 days that 0015 adopts. `auditTrailRetentionDays` answers 30, and the
   label is the tab's default time range (section 16.9); 0015 keeps 30 days
   and names the label as the range.
5. **Drift detection system role.** Decision 0007 seeds
   `drift-detection-manager`; the console lists no such role. 0007 defines
   infraweave's own system roles and waits on no capture, so it keeps the
   role; C116 records the console's list, and section 20 lists the shipped
   role for review.
6. **Provider type characters.** Report 15 cites the docs' lowercase
   letters only; the console allows digits too. Report 15 gives both, and
   P17b validates `^[a-z0-9]+$`.
7. **Tiers.** `features.json`, decision 0021 and report
   [26](26-scope-and-tiers.md) follow the pricing page as read 2026-11-17:
   Free, Starter, Business and Enterprise, with Starter at 10 users and
   2 public workers, and drift detection and private worker pools from
   Business. The billing page places Repos at Enterprise. This resolves
   C287; C260 checks sessions and ignored runs on Starter.

8. **Approval reject.** Report 08 and the approval docs gave a reject as
   failing or canceling the run. The console fails it, and one reject among
   approving policies fails it too (section 16.2); report 09, the run
   register's T5, T24 and T29 and decisions 0008 and 0009 say so, and C291
   checks the states not driven.
9. **Push policy cancel of an unconfirmed run.** Report 08 left the state
   open between `CANCELED` and `DISCARDED`. The console discards it (section
   16.2); the register's T27 and decision 0008's drivers carry the edge, and
   section 20 lists the shipped machine's missing driver.
10. **Pulumi hook order.** Report 20 found the Pulumi page placing the login
    before `before_init` hooks against the general rule. A run shows the
    general rule (section 16.8); report 20 and P20 follow it.
11. **Audit trail scope.** Decision 0015 recorded every mutating operation
    and offered run state changes only on the webhook. The console's trail
    holds callers' mutations alone (section 16.9); 0015 states which work
    records nothing, and its actor format.
12. **Blueprint and template schemas.** Report 17 read the docs' schemas: no
    rule on `stack` and `stacks`, no `available_spaces`, no validations on
    `boolean` and `select`. The schemas the console serves have all three
    (sections 16.7 and 17.6); report 17 records both, and P21 validates
    against the served ones.
13. **Retry.** Decision 0008 retried any ended run with its runtime
    configuration and overrides. The console offers no retry after
    `FINISHED` and reruns the retried run's configuration as it was
    (section 17.3); 0008 says so, and section 20 lists main's difference.

A review of P20 against the decisions and the code finds further
contradictions between P20's tasks and decisions 0008, 0009, 0010, 0016
and 0024 and the code at `3d822e48`. P20 names each one and the decision
its first task rewrites; four rows of section 20 are the ones that
concern shipped code.

## 20. Gaps in shipped work

These need tickets of their own; this report changes no code.

| Gap | Where | Source |
|---|---|---|
| Browser sessions default to 12 hours idle and 7 days absolute, against decision 0006's 10 hours | `internal/access/assembly/settings.go` | Section 6.6 |
| `drift-detection-manager` is seeded as a system role that Spacelift's console does not show | `internal/access/internal/core/role/role.go` | Section 7, C116 |
| Tier values follow the 2026-11-17 pricing read | `docs/data/features.json` and the generated catalogue | Section 14, C287 resolved |
| An OpenTofu stack with no image named runs on the Terraform runner image, while Spacelift's blank default is the vendor's image | `internal/workers/internal/core/images/images.go`, `internal/agent/config/config.go` | Section 4; P20 |
| `useRunAll: false` still runs `terragrunt run --all` | `internal/iac/terragrunt`, the runner | P20 |
| No plan document is sanitized before upload, which decision 0009 requires | `internal/agent/exec` | P13 |
| `iactest`'s plan cases are Terraform's documents, so no other vendor can pass them | `internal/iac/iactest` | P20 |
| A push policy `cancel` cannot discard a run awaiting confirmation: T27's drivers are the API and the timers, not the scheduler, which decision 0008 names for it; T9's push policy cancel in the register has no scheduler driver either | `internal/runs/internal/core/machine/machine.go` | Section 16.2; decision 0008, "Who drives each transition" |
| The stacks, runs, configuration, VCS, workers and cloud domains record no audit entry for any caller's mutation, which decision 0015 requires of every one; P18b task 1 adds them | `internal/stacks`, `internal/runs`, `internal/configuration`, `internal/vcs`, `internal/workers`, `internal/cloud` | Section 16.9; decision 0015, "Audit trail" |
| Audit entries carry the bare login as `actor.username`, where decision 0015 has `<source>::<login>` | `internal/access/internal/core/{group,settings,user}`, `internal/state/internal/core` | Section 16.9; decision 0015, "Audit trail" |
| The runner sets `TF_VAR_infraweave_api_endpoint` from its launcher's endpoint, the four `INFRAWEAVE_*` run settings and `TF_APPEND_USER_AGENT`; C308, C309 and C310 capture Spacelift's rate defaults, cache rule and user agent | `internal/agent/config/config.go`, `internal/agent/exec/prepare.go` | Section 17.2; decision 0008, "infraweave's own variables" |
| Auto-attached contexts rank alphabetically for variables while their hooks run in reverse alphabetical order; the console labels the order reverse alphabetical, and C99 checks which wins a variable | `internal/configuration/internal/core/environment.go` (`ranked`) | Section 17.1; report 13 section 7.1 |
| A retry copies the retried run's recorded hooks, settings and releases, while variables and files are read afresh; a `FINISHED` run and a tracked run whose stack moved to another commit cannot retry | `internal/runs/internal/core/control/actions.go`, `internal/runs/internal/core/read/guards.go`, `internal/runs/internal/core/handover/handover.go` | Sections 16.2 and 17.3; decision 0008, `runRetry` |
| A run records its runtime configuration at its first successful handover; later handovers reuse its hooks, settings and releases while rereading variables and mounted files | `internal/runs/internal/core/handover/handover.go`, `internal/runs/internal/adapters/http/http.go` | Section 17.3; decision 0008 |
| A stack's resolved environment is never answered; #564 requires `Stack.runtimeConfig` to show one winning stored element per name with its context, plus missing computed run-variable names without values, while `config` remains own-only | `internal/configuration/internal/core`, `internal/stacks/internal/http` | Sections 17.1 and 17.2; report 13 sections 1.3, 1.4, 6 and 8 |
| A task that skips initialization still runs its `before_init` and `after_init` hooks and logs no skip | `internal/agent/exec/exec.go` | Section 17.4; decision 0008, `skipInitialization` |
| The source is checked out at `/mnt/workspace` rather than `/mnt/workspace/source`, so hooks run in `/mnt/workspace/<project root>` | `internal/agent/source/source.go`, `internal/agent/executor/docker/docker.go` | Section 17.1; report 13 section 3; no decision states the layout |
| A stack variable may take a name an attached context sets, and wins; the console refuses it, and C101 asks whether the API does | `internal/configuration/internal/core/stack.go` | Section 17.1; report 13 section 7 |

Two differences are infraweave's on purpose: decision 0013 masks a secret
mounted file's content in logs, which Spacelift prints in clear, and
decision 0008 re-runs initialization inside a targeted replan's planning,
where Spacelift restores the archived workspace alone.

Reference operations that no phase names at `3d822e48`, with the phase that carries each: `azureIntegrationConsentConfirm`
and `azureIntegrationConsentClear` (P15), `isGovSlackAvailable` (P18),
`runTraceSpans` (P18b), and `awsIntegrationExternalIdForScan`, `scanCoverage`,
`scanCoverageGenerate`, `scanLatestRun`, `scanResourcesIgnore`,
`scanResourcesUnignore`, `scanSearchIgnoredResources` and
`scanSearchIgnoredResourcesSuggestions` (P22b).

## 21. What the trial could not show

| Not shown | Why | Capture |
|---|---|---|
| The operations a policy's page calls | The driven sessions read samples and simulation but not their requests | C273 |
| The drift run limit a worker pool takes | Behind a switch the survey did not turn on | C274 |
| A private pool's page, workers and actions | No private pool existed | C275 |
| An API key's access step and expiry field | Needs a valid first step and a switch | C276 |
| The usage windows | Only the exports show them | C280 |
| The dashboard's data sources | Operation names are not visible | C281 |
| The API's answer to MFA without SSO | Only a write shows it | C282 |
| SAML dynamic configuration's field | Behind a switch | C283 |
| The seeded Bedrock integration's model and owner | Not shown in its row | C284 |
| The VCS event run cap's bound | Behind a switch | C285 |
| The stack wizard's role step | Follows earlier steps | C286 |
| The plans offered and their limits | Pricing page read 2026-11-17 and the billing page's Repos placement | C287 resolved |
| Event streams, the Kubernetes controller and Flows | No console surface in scope | C21; Flows is report [23](23-flows.md) |
| Backstage and ServiceNow forms | Their set-up controls may act on click, as SCIM's did | C257, C258 |
| Sensitive outputs and their transfer to dependants | No output was sensitive | C288 |
| A published blueprint, its integrations and a stack created from it | The blueprint stayed a draft | C289 |
| Schedules firing, and drift detection | Only the public pool existed, and schedules need private workers | C290 |
| An approval reject in `PENDING_REVIEW` and `UNCONFIRMED` | Only `QUEUED` was rejected | C291 |
| Prioritizing a run | No control showed on the public pool | C292 |
| Module versions from tags | The config file's `module_version` was always set | C293 |
| A lock refusing another member | The account had one member | C294 |
| The API's default engine, the OPA version, and module-version trigger inputs | Not shown, or not driven | C49, C57, C55 |

## What this means for infraweave

- The console is a client of the same operations the reference spec holds,
  so P24c covers it by covering the operations; its patterns (drawers,
  wizards, saved views, filter builders, the palette) are the shape the
  console should take.
- Scanning reaches users as plugins and plan policies, not as a scan
  service; P22b stays a schema-only surface.
- Every setting the console shows has an operation in the reference, and
  every such operation has a phase.
- Driving the console settles what the docs leave between two readings:
  rejects fail, a push cancel discards, `before_init` runs before Pulumi's
  login, and the audit trail holds callers' changes alone. The decisions
  state each as infraweave's behaviour, and the gaps in section 20 are where
  main has not caught up.
- The recorded policy inputs are the contract P13's structs must accept;
  they show where Spacelift's documents differ from its published contract.

## Inferred and undocumented

| Claim | Basis |
|---|---|
| The catalogue's nine unread entries are the nine non-login library templates its first page lacks | Inferred from the counts; C54 |
| A deployment's stack takes the API's defaults for behaviour the template leaves out | Inferred from its secret masking, run promotion and sensitive output settings, which differ from the wizard's; C150 |
| Spacelift substitutes a UUID for each value unknown at Pulumi preview time | Inferred from the plan input holding UUIDs where the log shows unknowns |
| A plugin's results reach plan policies under `third_party_metadata.custom` for every scanner | Inferred from the Checkov, Module Test Coverage, Terrascan, Trivy and Wiz descriptions; C191 asks it for Infracost |
| The seeded Bedrock integration is Spacelift's installation-provided model | Inferred from its name and its disabled state before any integration exists; C284 |

## Sources

- Spacelift's web console, an Enterprise trial account, read on 2026-11-17
  through Chrome, and driven the same day in two approved sessions on a
  throwaway repository. The account, its users, stacks and repository are
  not named here.
- The redacted policy inputs and decisions,
  [`test/fixtures/policy-inputs`](../../../test/fixtures/policy-inputs/README.md).
- The reference spec, `api/reference/spacelift/spacelift.openapi.yaml`, for
  operation names and tags.
- The served spec, `api/api.openapi.yaml`, and the code paths section 20
  names, on `main` at `52c6180b` for its driven rows.
- The [implementation plan](../../implementation/plan.md), decisions
  [0006](../decisions/0006-authentication-and-tokens.md),
  [0007](../decisions/0007-authorization-spaces-and-roles.md),
  [0008](../decisions/0008-run-execution-and-worker-protocol.md),
  [0009](../decisions/0009-policy-engine-opa.md),
  [0014](../decisions/0014-module-and-provider-registry.md),
  [0015](../decisions/0015-events-notifications-webhooks-audit.md) and
  [0019](../decisions/0019-ai-provider-boundary.md), and reports
  [08](08-run-lifecycle.md), [09](09-policies.md),
  [12](12-cloud-integrations-and-oidc.md), [13](13-configuration.md),
  [14](14-spaces-rbac-and-identity.md),
  [15](15-module-and-provider-registry.md),
  [17](17-blueprints-and-templates.md), [19](19-events-notifications-audit.md),
  [20](20-vendors.md), [21](21-resources-scans-search.md),
  [25](25-other-integrations.md)
  and [26](26-scope-and-tiers.md).
- The open-questions register,
  [`open-questions.json`](../../data/open-questions.json), and the
  feature catalogue, [`features.json`](../../data/features.json).
