# 32: Spacelift console survey

Checked against: Spacelift's SaaS web console as an Enterprise trial account showed it on 2026-11-17, read without saving anything; the [implementation plan](../../implementation/plan.md), decisions 0006 to 0024 and reports 09 to 31 at commit `3d822e48`; introspection `b3255fe38f5e`.

Research baseline: **2026-11-17**.

This report records what Spacelift's web console shows of the product, page
by page, and compares each observation with the plan's later phases, P13 to
P28, and the decisions they cite. The rewrite of those phases in the
[implementation plan](../../implementation/plan.md) rests on it. Earlier
reports read the GraphQL schema, the documentation, the Terraform provider
and Spacelift's public repositories; none had read the console, which shows
defaults, bounds, form order, tier gates and help text that the schema does
not carry. Every observation here is a summary in this report's own words.
It quotes no documentation and no console text beyond short labels, names no
account, user or ID, and keeps no screenshots. Route paths omit the host.
The terms are in the [glossary](glossary.md); open items are records of
[`open-questions.json`](../../data/open-questions.json), cited by ID.

| Label | Means |
|---|---|
| **[UI]** | Seen in the console on 2026-11-17: a page, form, field, default, bound or hint |
| *consistent* | The plan or a decision already says the same |
| *new* | A detail the plan at `3d822e48` lacks; the phase named carries it |
| *conflict* | The observation contradicts a report, a decision or the plan; [section 17](#17-contradictions-and-their-resolution) says how each is resolved |
| *open* | Only a write shows it; the capture named carries it |

## 1. Method and limits

- **Read only.** The survey opened pages, tabs, menus and create forms,
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
  could not be read. [Section 19](#19-what-the-trial-could-not-show) lists
  each with the capture that carries it.
- **Tier.** The trial runs at the Enterprise tier, so every gated page
  opens. Tier gates appear as the billing page's plan cards and as trial
  banners, not as refusals.

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
| A policy's workbench, samples and simulation live on the policy's own page, which an account without policies cannot show | P13b | *open* (C273) |

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
vendor needs its own default image ([section 18](#18-gaps-in-shipped-work)).

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
| The audit trail's log tab carries a 14-day label, filters on action, affected and related resource, event type and username, and shows actions as `<resource>.<verb>` and actors as `<provider>::<login>` | P18b, C180 | *conflict* on retention with 0015's 30 days; *open* (C279) |
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
  P19b; the tier *conflicts* with `features.json` (section 18).

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
| The billing page shows four plans: Starter (with 10 users and 2 public workers), Business, Enterprise and Self-Hosted. It shows no Free or Starter+ plan | 0021, report [26](26-scope-and-tiers.md), `features.json` | *conflict* (section 18); *open* (C287) |
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

## 16. Findings by phase

| Phase | What the survey changes in the plan |
|---|---|
| P13 | Creatable types in the console; the plan policy input carries plugin results under `third_party_metadata.custom` |
| P13b | Login policies on their own settings page; the in-product catalogue (59 templates) against the library (67); the workbench capture C273 |
| P14 | Public pool shown as a ceiling of workers; queue filters; runtime default images per pool kind; no Kubernetes surface |
| P15 | Azure consent operations, which no phase names at `3d822e48`; AWS duration bounds; subject template default |
| P16 | Four schedule kinds as role actions; the pool drift limit (C274); stack settings (C278) |
| P17 | Wizard order and sparse checkout paths (C277) |
| P17b | Provider type rule; no version form; GPG keys without a space |
| P18 | Webhook form; inbox window; `isGovSlackAvailable`, which no phase names at `3d822e48` |
| P18b | Audit webhook switches; retention label (C279); usage tabs (C280); `runTraceSpans`, which no phase names at `3d822e48` |
| P18c, P18d | No change: Flows was outside the survey |
| P19b | Agent pool form; no pool field on integrations (C77); Origin and Repos settled |
| P20 | Blank runner image means the vendor's default |
| P20b | Thirteen plugin templates; plugins as scanners |
| P21 | Template version identifier and instructions; blueprints steered to templates |
| P22 | LaunchPad as the User Guide operations; resources view; dashboard data (C281) |
| P22b | No scan surface at all; eight Scans operations that no phase names at `3d822e48` |
| P23 | MFA needs SSO; SCIM in one click; SAML options; the SSO switch warning; session lifetime |
| P24, P24b | No console finding; operations added above reach both through their resources and commands |
| P24c | Console patterns: drawers, wizards, saved views, filter builders, the palette, login policies' page |
| P25, P25b | Seeded provider integration; Intent's defaults |
| P27 | No change: packaging has no console surface |
| P28 | The captures this report raises, and the console as a capture source |

## 17. Contradictions and their resolution

Each item names what `3d822e48` holds, what the console shows, and where
the resolution lives.

1. **Browser session lifetime.** At `3d822e48`, decision 0006 defaults
   browser sessions to 12 hours idle and 7 days absolute, and DV-017 rests
   on Spacelift's lifetime being undocumented. The console shows 10 hours
   with no setting. Decision 0006 defaults both limits to 10 hours and keeps
   them configurable, and DV-017 records the configurability alone. The
   shipped defaults differ ([section 18](#18-gaps-in-shipped-work)).
2. **MFA without SSO.** The console offers MFA only after SSO is set up.
   Decision 0006 states that enforcing MFA needs an OIDC or SAML identity
   provider in the organization and answers `422` otherwise; C282 checks
   the API's own answer.
3. **Audit webhook retries.** Decision 0015 retries every failed audit
   delivery whatever `retryOnFailure` says, citing the docs; the console
   offers the audit webhook a retry switch promising up to three retries on a
   5xx. The decision keeps its schedule, states both sources and stores
   `retryOnFailure`, and C181 settles which holds.
4. **Audit retention.** The log tab's 14-day label stands against the docs'
   30 days that 0015 adopts. The label may be the tab's window rather than
   the retention, so 0015 keeps its 30-day default and names the label
   until C279 reads `auditTrailRetentionDays`.
5. **Drift detection system role.** Decision 0007 seeds
   `drift-detection-manager`; the console lists no such role. 0007 defines
   infraweave's own system roles and waits on no capture, so it keeps the
   role; C116 records the console's list, and section 18 lists the shipped
   role for review.
6. **Provider type characters.** Report 15 cites the docs' lowercase
   letters only; the console allows digits too. Report 15 gives both, and
   P17b validates `^[a-z0-9]+$`.
7. **Tiers.** `features.json` places private worker pools and drift
   detection at Starter+, repos at Free and Starter's seats at 2; the billing
   page shows no Starter+, a Starter with 10 users and 2 public workers,
   drift detection at Business and internal source control at Enterprise.
   The register is shipped work and keeps its values here; C287 confirms the
   plans and section 18 lists the gap.

A review of P20 against the decisions and the code finds further
contradictions between P20's tasks and decisions 0008, 0009, 0010, 0016
and 0024 and the code at `3d822e48`. P20 names each one and the decision
its first task rewrites; the last four rows of section 18 are the ones that
concern shipped code.

## 18. Gaps in shipped work

These need tickets of their own; this report changes no code.

| Gap | Where | Source |
|---|---|---|
| Browser sessions default to 12 hours idle and 7 days absolute, against decision 0006's 10 hours | `internal/access/assembly/settings.go` | Section 6.6 |
| `drift-detection-manager` is seeded as a system role that Spacelift's console does not show | `internal/access/internal/core/role/role.go` | Section 7, C116 |
| Tier values: `public-worker-slots` and `seats` for Starter, Starter+ rows, `repos` at Free and `drift-detection` at Starter+ | `docs/data/features.json` and the catalogue generated from it | Section 14, C287 |
| An OpenTofu stack with no image named runs on the Terraform runner image, while Spacelift's blank default is the vendor's image | `internal/workers/internal/core/images/images.go`, `internal/agent/config/config.go` | Section 4; P20 |
| `useRunAll: false` still runs `terragrunt run --all` | `internal/iac/terragrunt`, the runner | P20 |
| No plan document is sanitized before upload, which decision 0009 requires | `internal/agent/exec` | P13 |
| `iactest`'s plan cases are Terraform's documents, so no other vendor can pass them | `internal/iac/iactest` | P20 |

Reference operations that no phase names at `3d822e48`, with the phase that carries each: `azureIntegrationConsentConfirm`
and `azureIntegrationConsentClear` (P15), `isGovSlackAvailable` (P18),
`runTraceSpans` (P18b), and `awsIntegrationExternalIdForScan`, `scanCoverage`,
`scanCoverageGenerate`, `scanLatestRun`, `scanResourcesIgnore`,
`scanResourcesUnignore`, `scanSearchIgnoredResources` and
`scanSearchIgnoredResourcesSuggestions` (P22b).

## 19. What the trial could not show

| Not shown | Why | Capture |
|---|---|---|
| A policy's workbench, samples and simulation | No policy existed, and creating one is a write | C273 |
| The drift run limit a worker pool takes | Behind a switch the survey did not turn on | C274 |
| A private pool's page, workers and actions | No private pool existed | C275 |
| An API key's access step and expiry field | Needs a valid first step and a switch | C276 |
| Module wizard steps 2 to 4 | Steps follow a valid source | C277 |
| Stack scheduling, drift detection and dependency settings | No stack existed; they are not creation steps | C278 |
| The audit trail's actual retention | Only an API read shows it | C279 |
| The usage windows | Only the exports show them | C280 |
| The dashboard's data sources | Operation names are not visible | C281 |
| The API's answer to MFA without SSO | Only a write shows it | C282 |
| SAML dynamic configuration's field | Behind a switch | C283 |
| The seeded Bedrock integration's model and owner | Not shown in its row | C284 |
| The VCS event run cap's bound | Behind a switch | C285 |
| The stack wizard's role step | Follows earlier steps | C286 |
| The plans offered and their limits | The trial's billing page shows upgrade targets only | C287 |
| Event streams, the Kubernetes controller and Flows | No console surface in scope | C21; Flows is report [23](23-flows.md) |
| Backstage and ServiceNow forms | Their set-up controls may act on click, as SCIM's did | C257, C258 |

## What this means for infraweave

- The console is a client of the same operations the reference spec holds,
  so P24c covers it by covering the operations; its patterns (drawers,
  wizards, saved views, filter builders, the palette) are the shape the
  console should take.
- Scanning reaches users as plugins and plan policies, not as a scan
  service; P22b stays a schema-only surface.
- Every setting the console shows has an operation in the reference, and
  every such operation has a phase.

## Inferred and undocumented

| Claim | Basis |
|---|---|
| The audit log tab's 14-day label is the view's window, not the retention | Inferred from the docs' 30 days; C279 |
| A plugin's results reach plan policies under `third_party_metadata.custom` for every scanner | Inferred from the Checkov, Module Test Coverage, Terrascan, Trivy and Wiz descriptions; C191 asks it for Infracost |
| The seeded Bedrock integration is Spacelift's installation-provided model | Inferred from its name and its disabled state before any integration exists; C284 |

## Sources

- Spacelift's web console, an Enterprise trial account, read on 2026-11-17
  through Chrome. The account is not named here.
- The reference spec, `api/reference/spacelift/spacelift.openapi.yaml`, for
  operation names and tags.
- The served spec, `api/api.openapi.yaml`, and the code paths section 18
  names.
- The [implementation plan](../../implementation/plan.md), decisions
  [0006](../decisions/0006-authentication-and-tokens.md),
  [0007](../decisions/0007-authorization-spaces-and-roles.md),
  [0009](../decisions/0009-policy-engine-opa.md),
  [0014](../decisions/0014-module-and-provider-registry.md),
  [0015](../decisions/0015-events-notifications-webhooks-audit.md) and
  [0019](../decisions/0019-ai-provider-boundary.md), and reports
  [09](09-policies.md), [12](12-cloud-integrations-and-oidc.md),
  [14](14-spaces-rbac-and-identity.md),
  [15](15-module-and-provider-registry.md),
  [19](19-events-notifications-audit.md), [25](25-other-integrations.md)
  and [26](26-scope-and-tiers.md).
- The open-questions register,
  [`open-questions.json`](../../data/open-questions.json), and the
  feature catalogue, [`features.json`](../../data/features.json).
