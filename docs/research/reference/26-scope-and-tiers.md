# 26: Scope and tiers

Checked against: docs read 2026-10-28 (SaaS and `self-hosted/latest`, v7.1.0); spacelift.io/pricing read 2026-10-28; schema `b3255fe38f5e`; spacelift-api-bruno `7cad4574954b`; terraform-provider-spacelift v1.57.0 (`c72b9bd2f3c3`); the specification reference `api/reference/spacelift/spacelift.openapi.yaml` and `coverage.md`; infraweave's `api/deploy.openapi.yaml` at the work tree's commit; reports 01 to 25.

Research baseline: **2026-10-28**.

This report places every Spacelift capability on two axes: which edition
offers it, and how far infraweave has come with it. It lists Spacelift's
editions, the 31 `BillingTierFeature` values that gate features by plan, and
one row for each of the 53 tags of the specification reference, the 56
resources and the 62 data sources of the Terraform provider. Each row names
the report that owns the behaviour, the plan that offers it, infraweave's
availability state and the provisional phase that delivers it. Section 7
records every interface excluded as superseded, with its evidence. The
implementation plan (IMP-01, [#31](https://github.com/weaveplatform/infraweave/issues/31))
takes its scope and its ordering from here; the inventory reports
[03](03-deploy-core-api-inventory.md), [04](04-platform-api-inventory.md) and
[05](05-terraform-provider.md) hold the operations behind each row.

| Label | Means |
|---|---|
| **[D]** | A docs.spacelift.io page, read 2026-10-28 |
| **[G]** | The introspection snapshot `b3255fe38f5e` |
| **[B]** | spacelift-api-bruno at `7cad4574954b` |
| **[S]** | Source code at the cited commit |
| **[RN]** | The Spacelift changelog, <https://docs.spacelift.io/product/changelog> |
| **[C]** | <https://spacelift.io/pricing>, read 2026-10-28; a marketing page outside the docs, so below [D] in precedence |
| **[I]** | Inferred; the cell or sentence says from what |
| *(unverified)* | No primary source confirms it; listed in 30-open-questions |

## 1. How to read this report

### 1.1 Columns

| Column | Content | Source |
|---|---|---|
| Ops | Operations the specification reference files under the tag | `spacelift.openapi.yaml`, counted by script |
| Inventory | The report that lists the tag's operations one by one | [03](03-deploy-core-api-inventory.md), [04](04-platform-api-inventory.md) |
| Owning report | The report that records the behaviour; the first named owns most of it | reports 07 to 25 |
| Plan | The lowest SaaS plan that offers the capability, in the vocabulary of section 1.3 | the cited docs notice [D] or the pricing page [C] |
| Tier features [I] | `BillingTierFeature` values that reports 03 and 04 pair with the operations in the row, by name | computed from those reports' tables; the pairing is an inference, as report 03 section 1.3 explains |
| Root fields called | Terraform rows only: GraphQL root fields the provider calls for the resource or data source | `terraform-operations.json`, filtered to root fields of the schema [G]; where the map records no call but the provider's source makes one, the cell gives the count from the source, marked [S] |
| State | infraweave's availability state (section 1.2) | computed against `api/deploy.openapi.yaml` |
| Phase | The provisional phase that delivers the row (section 1.4) | this report |

### 1.2 Availability states

| State | Meaning | Rows in this state at the baseline |
|---|---|---|
| `implemented` | Every operation is served with Spacelift's behaviour and covered by acceptance tests | none |
| `partial` | Some operations are served with behaviour, others are not | none |
| `stub` | Every operation is routed and answers 501 through the generated server; no behaviour exists | the Stacks tag; Terraform rows whose root fields are all Stacks or Runs stubs |
| `missing` | infraweave has no route for at least one operation | everything else |
| `superseded` | A newer Spacelift interface replaces it; infraweave does not build it (section 7) | Terraform rows only; no whole tag is superseded |

How the states are computed:

- infraweave's served surface is `api/deploy.openapi.yaml`: 72 operations,
  69 under `Stacks` and 3 under `Runs`. Every one is answered by
  `UnimplementedStrict`, which returns 501
  ([`internal/server/gen/deploy/unimplemented.gen.go`](../../../internal/server/gen/deploy/unimplemented.gen.go),
  [`internal/stubgen/stubgen.go`](../../../internal/stubgen/stubgen.go)).
- A tag is `stub` when every one of its operations in the reference has an
  operation of the same ID in the deploy document. Runs has three of its 35
  (`runTrigger`, `proposeWithOverrides`, `taskCreate`) and stays `missing`;
  its row names them.
- A Terraform resource or data source is `stub` when every root field it
  calls is a deploy operation. infraweave has no Terraform provider yet
  (P24), so `stub` here means only that the routes exist.
- A row is `superseded` only on evidence listed in section 7.

Totals at the baseline: tags `missing` 52, `stub` 1; resources
`missing` 44, `stub` 8, `superseded` 4; data sources `missing` 51, `stub` 7, `superseded` 4.

### 1.3 Plan vocabulary

| Value | Meaning |
|---|---|
| All | Every plan, Free included; no gate is stated anywhere |
| Paid | The docs say "paid accounts"; the lowest paid plan sold is Starter+ [C] |
| Starter+ | The docs or the pricing page name Starter+ and above |
| Business | Business and above |
| Enterprise | Enterprise and above |
| Not stated | No docs page, no schema description and no pricing line gives a plan *(unverified)* |
| UI support | Operations behind the web UI's own state; no plan applies |

A row with several gates names each, for example "All; targeted replan
Business". Self-hosted pages carry none of the SaaS tier notices (reports
[11](11-vcs-integrations.md), [15](15-module-and-provider-registry.md),
[17](17-blueprints-and-templates.md), [19](19-events-notifications-audit.md))
[D]; section 2.3 lists what self-hosted lacks instead.

### 1.4 Provisional phase spine

The phases are a draft. The implementation plan (IMP-01,
[#31](https://github.com/weaveplatform/infraweave/issues/31)) confirms or
changes them, and this report's phase column follows it. A row's phase is
where the capability is delivered first; superseded rows have none.

| Phase | Delivers |
|---|---|
| P0 | Reference: the specification reference, the research set and the parity oracle |
| P1 | Served skeleton: the REST server, generated routes and 501 stubs |
| P2 | Persistence |
| P3 | Identity and tokens: accounts, users, API keys, sessions |
| P4 | Spaces and RBAC |
| P5 | Stacks CRUD |
| P6 | Contexts and configuration |
| P7 | Work queue and scheduler |
| P8 | Worker protocol and the public pool |
| P9 | First tracked run end to end |
| P10 | Run lifecycle completeness |
| P11 | State backend |
| P12 | VCS, GitHub first |
| P13 | Policies |
| P14 | Private worker pools, Docker and Kubernetes workers |
| P15 | Cloud integrations and OIDC |
| P16 | Scheduling, drift and dependencies |
| P17 | Module and provider registry |
| P18 | Notifications, webhooks, audit and observability |
| P19 | Remaining VCS providers and VCS agent pools |
| P20 | Other vendors |
| P21 | Blueprints and templates |
| P22 | Resources, scans and search |
| P23 | SSO and SCIM |
| P24 | infraweave Terraform provider and CLI |
| P25 | Intelligence |
| P26 | Flows |
| P27 | Reference deployments and self-hosted packaging |

Every Terraform row also needs P24, which builds the provider; the phase
column names the phase that delivers the API the row calls.

## 2. Editions

### 2.1 SaaS plans

The pricing page lists no prices: every paid plan is "request a quote", and
Free is free with no card and no time limit [C]. Each plan includes the one
before it [C]. Its comparison table has one column per paid plan and none for
Free. It ticks every feature row for Starter+ except blueprints, the private
provider registry, targeted replans and advanced scheduling (Business and
above) and the audit trail, internal source control and OIDC API keys
(Enterprise and above). Rows ticked from Starter+ include contexts, custom
tasks, stack dependencies, plan and approval policies, the private module
registry, webhooks, notifications, session management and drift detection
[C].

| Edition | Workers [C] | Users [C] | What it adds [C] | Docs notices that name it [D] |
|---|---|---|---|---|
| Free | 1 public worker | 2 | The base product | Infra Assistant on the Spacelift-provided integration has a USD 10 budget per 30 days, unlimited on paid plans (report [22](22-intelligence.md)) |
| Starter | 1 private [C] | limited [D] | No plan card or comparison column; the FAQ describes a Starter plan payable monthly by card whose upgrade for a second private worker is Starter+ [C]; see section 2.2 | `sessions` and `sessionDelete` are "not available in the Starter tier" [G]; Free and Starter have seat limits [D] <https://docs.spacelift.io/concepts/identity-access-management/admin> |
| Starter+ | 2 public; 1 private, 2 available | unlimited | Spaces, IaC support, workflow customisation, VCS integration, resource visualisation, cloud cost estimation, the Spacelift Terraform provider, dependencies, cloud integrations, drift detection, the policy engine; Bronze support | Drift detection, private worker pools, ignored-run warnings ([18](18-scheduling-drift-dependencies.md), [10](10-workers-and-execution.md), [08](08-run-lifecycle.md)) |
| Business | 3 private; public configurable | unlimited | Blueprints, templates, advanced scheduling, the private provider registry, targeted replans; Silver support with a customer success manager | Scheduling, aggregated VCS checks, targeted replan, the provider registry, blueprints and templates |
| Enterprise | 5 to 30 private | unlimited | Concurrent VCS connections, audit trail, SAML 2.0 SSO, OIDC API keys, internal source control (VCS agent pools), MFA; Gold support | Audit trail, SAML SSO, SCIM, MFA, OIDC API keys, VCS agent pools, space-level VCS integrations, ServiceNow |
| Enterprise+ | 12 to 30 private; 100 and more on request | unlimited | Self-hosted, FedRAMP, on-premises and air-gapped installs, executive escalation; single tenancy and a named TAM on request; "unmanaged resources" announced as coming | none |

Two plan words in the docs have no line on the pricing page:
- "paid accounts", used for policies, webhooks, notifications, the module
  registry, OIDC SSO and the OIDC token [D] (reports [09](09-policies.md),
  [19](19-events-notifications-audit.md), [15](15-module-and-provider-registry.md),
  [14](14-spaces-rbac-and-identity.md), [12](12-cloud-integrations-and-oidc.md));
- "Cloud tier or above", used for notification-policy integrations such as
  Datadog and Teams [D] (report [19](19-events-notifications-audit.md)).
  `CLOUD` and `V3_CLOUD` are older `BillingTier` generations (section 2.2), so
  the notice predates the current plan names [I].

### 2.2 The `BillingTier` enum

`tier` returns one of 18 values [G]; `tierFeatures` returns the
`BillingTierFeature` list the tier enables (section 3). The generation prefix
and the edition names pair by name [I]:

| Edition | `BillingTier` values [G] |
|---|---|
| Free | `FREE`, `V3_FREE`, `V4_FREE`, `V5_FREE` |
| Cloud (older generations) | `CLOUD`, `V3_CLOUD` |
| Starter | `V4_STARTER`, `V5_STARTER` |
| Starter+ | `V4_STARTER_PLUS`, `V5_STARTER_PLUS`, `V5_STARTER_PLUS_2` |
| Business | `V4_BUSINESS`, `V5_BUSINESS` |
| Enterprise | `ENTERPRISE`, `V4_ENTERPRISE`, `V5_ENTERPRISE` |
| Enterprise+ | `V5_ENTERPRISE_PLUS` |
| Strategic | `V5_STRATEGIC` |

`V4_ENTERPRISE` carries the description "BUSINESS Billing tier." [G]. The
name is the higher evidence for which edition it is, and the description is
treated as a copy error [I]. Of a plain Starter tier, only fragments are
published: a seat limit [D], one private worker [C] and no session listing
[G]. Its full feature set, and which features separate `V5_STARTER_PLUS` from
`V5_STARTER_PLUS_2`, are published nowhere *(unverified)*. No schema field maps a tier to its features; only an
account's own `tierFeatures` is readable [G].

### 2.3 Self-Hosted

| Aspect | Fact | Source |
|---|---|---|
| Offer | Listed under Enterprise+ with on-premises and air-gapped installs | [C] |
| Feature set | The Enterprise tier's functionality, except where a feature does not apply to self-hosting; new features reach SaaS first and self-hosted only with a release | [D] <https://docs.spacelift.io/self-hosted>; report [01](01-spacelift-landscape.md) |
| Licence | A JWT licence token with a validity window (`LICENSE_TYPE=jwt`, `LICENSE_TOKEN`); `debugInfo.selfHostedLicence` reports it, and it is empty on SaaS | [D], [G]; report [24](24-self-hosted-reference.md) |
| Version at the baseline | v7.1.0, documented under `self-hosted/latest` | [D]; report 24 |
| Tier notices | Absent from the self-hosted pages for registries, blueprints, templates, VCS features and webhooks | [D]; reports 11, 15, 17, 19 |
| What `tier` and `tierFeatures` return | Not documented | *(unverified)*; report 14 |
| Cloud integrations | AWS and generic OIDC only; the Azure and GCP integrations are not available, and OIDC federation is the recommended substitute | [D] <https://docs.spacelift.io/self-hosted>; report [12](12-cloud-integrations-and-oidc.md) |
| AI | Intent, the MCP server, Infra Assistant and Intent policies work; the Spacelift-provided AI integration does not, so a bring-your-own-model integration is needed; run summarisation (Saturnhead Assist) is not yet available | [D] <https://docs.spacelift.io/self-hosted/latest/concepts/intelligence>; report 22 |
| GitHub | No Marketplace app; a custom GitHub app is needed | [D]; report 11 |
| Slack | The install creates its own Slack app; `slackAppConfig` and its mutations work only there | [D], [G]; report 19 |
| Public pool | Not available: the customer runs every worker. The Prometheus exporter reports zero pending public-pool runs on self-hosted, which agrees | [D] <https://docs.spacelift.io/self-hosted>; [S], report 19 |
| Usage | Usage data may be shared with Spacelift through the usage exporter | [D], [S]; reports 19 and 24 |
| Flows | Supported on AWS EKS only, with its own licence token | [D]; report [23](23-flows.md) |

### 2.4 FedRAMP

| Aspect | Fact | Source |
|---|---|---|
| Authorisation | FedRAMP Moderate | [D] [fedramp] |
| Offer | Listed under Enterprise+ | [C] |
| Eligibility | US federal agencies, government contractors, state and local governments, and businesses pursuing federal contracts | [D] [fedramp] |
| Features | The same as SaaS, except two limits: no public workers, and restricted collection of private-worker errors | [D] [fedramp] |
| Consequence | The first private worker pool cannot be created from a public-worker run; it has to be bootstrapped from outside, such as a CI pipeline | [D] [fedramp] |
| Hosting | AWS `us-east-2` (commercial partition), disaster recovery in `us-west-1`, through Knox Systems | [D] [fedramp] |
| Separation from commercial work | Either a second account (separate admin, IdP, audit and billing), or isolation inside one tenant with spaces, RBAC, private pools, per-pool or per-integration customer-managed keys, policies and IdP-independent MFA | [D] [fedramp] |
| Onboarding | Through an account executive: NDA, identity verification, a trial sign-up URL, and an account that starts disabled until activated | [D] [fedramp] |

[fedramp]: https://docs.spacelift.io/product/fedramp

## 3. `BillingTierFeature`

The enum has 31 values [G]. Only `AUDITING`, `SCIM` and `SCANS` carry a
description, and `AUDIT_WEBHOOKS` is deprecated in favour of `AUDITING` [G],
listed on 2026-10-20 [RN]. Where the meaning cell says "[I] from the name" or
"from the operations gated", nothing published defines the value. "Operations
tagged" counts the operations that reports 03 and 04 pair with the value; a
zero means no operation's name matches it, not that nothing checks it.

| # | Value [G] | Meaning | Minimum SaaS plan | Self-hosted and FedRAMP | Operations tagged [I] | Owning report |
|---|---|---|---|---|---|---|
| 1 | `ACCESS_POLICIES` | Per-stack and per-module access policies (`ACCESS` policy type) [I] from the name | Not stated | Access policies are disabled for all accounts since 2026-05-30 [D]; superseded (section 7.4) | 0 | [14](14-spaces-rbac-and-identity.md), [09](09-policies.md) |
| 2 | `ADDITIONAL_PUBLIC_WORKER` | Public worker capacity above the plan default [I] from the name | Free has 1 public worker, Starter+ 2, Business and above configurable [C] | FedRAMP has no public workers [D] | 0 | [10](10-workers-and-execution.md) |
| 3 | `ADVANCED_SCHEDULING` | Scheduled runs, tasks and deletes [I] from the operations gated | Business [D] [C] | No notice on self-hosted pages [D] | 9 | [18](18-scheduling-drift-dependencies.md) |
| 4 | `AGGREGATED_VCS_CHECKS` | `AGGREGATED` commit checks on a VCS integration | Business [D] | No notice on self-hosted pages [D] | 10 | [11](11-vcs-integrations.md) |
| 5 | `API_KEYS_OIDC` | OIDC-based API keys | Enterprise [D] [C] |  | 1 | [14](14-spaces-rbac-and-identity.md) |
| 6 | `AUDITING` | The audit trail, including audit webhooks [G] | Enterprise [D] [C] | No notice on self-hosted pages [D] | 6 | [19](19-events-notifications-audit.md) |
| 7 | `AUDIT_WEBHOOKS` | Audit webhooks; deprecated in favour of `AUDITING` [G] (deprecated [G]) | Enterprise, as `AUDITING` | Superseded (section 7.5) | 0 | [19](19-events-notifications-audit.md) |
| 8 | `BLUEPRINTS` | Blueprints, and templates by shared naming [I] | Business [D] [C] | No notice on self-hosted pages [D] | 26 | [17](17-blueprints-and-templates.md) |
| 9 | `CAN_UPDATE_SSO_SAML` | Changing SAML settings after setup [I] from the name | Enterprise [I] with SAML |  | 0 | [14](14-spaces-rbac-and-identity.md) |
| 10 | `CLOUD_CREDENTIAL_GENERATION` | Cloud credentials generated by Spacelift for runs [I] from the name | Not stated; cloud integrations are listed from Starter+ [C] | Self-hosted offers AWS only [D] | 0 | [12](12-cloud-integrations-and-oidc.md) |
| 11 | `CUSTOM_SSO_SAML` | SAML single sign-on | Enterprise [D] [C] |  | 4 | [14](14-spaces-rbac-and-identity.md) |
| 12 | `DRIFT_DETECTION` | Drift detection | Starter+ [D] [C] | Private workers only [D]; so none on a Free account, which has no private worker [I] from [C] | 3 | [18](18-scheduling-drift-dependencies.md) |
| 13 | `MFA` | Enforced multi-factor authentication | Enterprise, with SSO on [D] [C] |  | 2 | [14](14-spaces-rbac-and-identity.md) |
| 14 | `MULTIPLE_VCS` | More than one integration per VCS provider [I] from the name | Enterprise: concurrent VCS connections [C]; space-level integrations [D] | No notice on self-hosted pages [D] | 0 | [11](11-vcs-integrations.md) |
| 15 | `NOTIFICATION_POLICIES` | Notification policies and the inbox | Paid [D] |  | 2 | [19](19-events-notifications-audit.md), [09](09-policies.md) |
| 16 | `PRIVATE_MODULE_SHARING` | Sharing private modules with other accounts [I] from the name | Not stated; the module registry is paid [D] | No notice on self-hosted pages [D] | 2 | [15](15-module-and-provider-registry.md) |
| 17 | `PRIVATE_VCS` | VCS agent pools for VCS systems the internet cannot reach | Enterprise [D] [C] | No notice on self-hosted pages [D] | 6 | [11](11-vcs-integrations.md) |
| 18 | `PRIVATE_WORKERS` | Private worker pools | Starter+ [D] [C] | FedRAMP runs on private workers only [D] | 6 | [10](10-workers-and-execution.md) |
| 19 | `RUN_STATE_CHANGE_WEBHOOKS` | Stack, module and named webhooks | Paid [D] | No notice on self-hosted pages [D] | 4 | [19](19-events-notifications-audit.md) |
| 20 | `TARGETED_REPLAN` | Targeted replan of an unconfirmed run | Business [D] [C] |  | 1 | [08](08-run-lifecycle.md) |
| 21 | `TERRAFORM_PROVIDER_VERSIONS` | The private provider registry | Business [D] [C] | Self-hosted has both registries [D] | 12 | [15](15-module-and-provider-registry.md) |
| 22 | `UNLIMITED_SEATS` | No cap on users | Starter+ and above: unlimited users; Free: 2 users [C]; Free and Starter have seat limits [D] |  | 0 | [14](14-spaces-rbac-and-identity.md) |
| 23 | `WORKFLOW_POLICIES` | Policies that drive the run workflow [I] from the name | Paid [D]; policy engine from Starter+ [C] | Which types it covers is *(unverified)* | 3 | [09](09-policies.md) |
| 24 | `OIDC_FEDERATION_TOKEN_GENERATION` | The OIDC token in every run, and the subject template | Paid [D] |  | 1 | [12](12-cloud-integrations-and-oidc.md) |
| 25 | `RUN_IGNORED_TRIGGERS` | The ignored-runs list and warnings | Starter+ [D] |  | 1 | [08](08-run-lifecycle.md), [11](11-vcs-integrations.md) |
| 26 | `ACCOUNT_AI_CONTROL` | Account switches for Spacelift Intelligence and Infra Assistant [I] from the operations gated | Not stated |  | 4 | [22](22-intelligence.md) |
| 27 | `SPACELIFT_INTELLIGENCE` | Spacelift Intelligence: AI integrations and Intent [I] from the operations gated | Not stated; Infra Assistant is on every plan [D] | Self-hosted: Intent and Infra Assistant work, the Spacelift-provided AI integration does not [D] | 23 | [22](22-intelligence.md) |
| 28 | `SERVICE_NOW_INTEGRATION` | The ServiceNow external integration | Enterprise [D] |  | 1 | [25](25-other-integrations.md) |
| 29 | `BACKSTAGE_INTEGRATION` | The Backstage plugins [I] from the name | Not stated |  | 0 | [25](25-other-integrations.md) |
| 30 | `SCIM` | SCIM v2 provisioning APIs [G] | Enterprise [D] |  | 4 | [14](14-spaces-rbac-and-identity.md) |
| 31 | `SCANS` | Scans [G] | Not stated |  | 6 | [21](21-resources-scans-search.md) |

## 4. Capability table: API tags

One row per tag of the specification reference, 53 in all, in the
reference's order. The 8 untagged operations (report 04 section 14) are not
a tag and are outside this table.

| Tag | Ops | Inventory | Owning report | Plan | Tier features [I] | State | Phase | Notes |
|---|---|---|---|---|---|---|---|---|
| AI Integrations | 5 | [04](04-platform-api-inventory.md) | [22](22-intelligence.md) | All [D] | `SPACELIFT_INTELLIGENCE` | `missing` | P25 | Spacelift-provided integration absent on self-hosted [D] |
| API Keys | 8 | [04](04-platform-api-inventory.md) | [14](14-spaces-rbac-and-identity.md) | All; OIDC keys Enterprise [D] | `API_KEYS_OIDC` | `missing` | P3 |  |
| Account | 7 | [04](04-platform-api-inventory.md) | [04](04-platform-api-inventory.md), [10](10-workers-and-execution.md) | All | none | `missing` | P3 | Default runner image reads belong to report 10 |
| Account Settings | 42 | [04](04-platform-api-inventory.md) | [04](04-platform-api-inventory.md), [14](14-spaces-rbac-and-identity.md), [22](22-intelligence.md) | All; MFA enforcement Enterprise [D] | `ACCOUNT_AI_CONTROL`, `MFA`, `OIDC_FEDERATION_TOKEN_GENERATION` | `missing` | P3 | Settings span P3, P4, P18 and P25 by subject |
| Ansible | 7 | [04](04-platform-api-inventory.md) | [20](20-vendors.md), [21](21-resources-scans-search.md) | Not stated | none | `missing` | P20 |  |
| Audit Trail | 6 | [04](04-platform-api-inventory.md) | [19](19-events-notifications-audit.md) | Enterprise [D] [C] | `AUDITING` | `missing` | P18 |  |
| Auth | 6 | [04](04-platform-api-inventory.md) | [14](14-spaces-rbac-and-identity.md), [01](01-spacelift-landscape.md) | All | none | `missing` | P3 |  |
| Billing | 14 | [04](04-platform-api-inventory.md) | [04](04-platform-api-inventory.md), [19](19-events-notifications-audit.md) | All | none | `missing` | P18 | Subscription and tier mutations are SaaS commerce; usage queries are report 19 |
| Blob Storage | 4 | [04](04-platform-api-inventory.md) | [11](11-vcs-integrations.md) | Not stated | none | `missing` | P19 | No docs page; a `BLOB` source provider |
| Blueprints | 15 | [04](04-platform-api-inventory.md) | [17](17-blueprints-and-templates.md) | Business [D] [C] | `BLUEPRINTS` | `missing` | P21 | Single-body blueprints are live; the versioned family is superseded and not in the reference (section 7.2) |
| Cloud Integrations | 27 | [04](04-platform-api-inventory.md) | [12](12-cloud-integrations-and-oidc.md) | Starter+ [C]; no docs notice | none | `missing` | P15 | Self-hosted offers AWS only [D]; GCP service accounts superseded (section 7.6) |
| Contexts | 13 | [03](03-deploy-core-api-inventory.md) | [13](13-configuration.md) | Starter+ [C]; Free not stated | none | `missing` | P6 |  |
| Debug | 1 | [04](04-platform-api-inventory.md) | [24](24-self-hosted-reference.md) | All | none | `missing` | P27 | Licence and build details; empty on SaaS [G] |
| Drift Detection | 3 | [04](04-platform-api-inventory.md) | [18](18-scheduling-drift-dependencies.md) | Starter+ [D] | `DRIFT_DETECTION` | `missing` | P16 | Private workers only [D] |
| External Integrations | 4 | [04](04-platform-api-inventory.md) | [25](25-other-integrations.md) | Enterprise for ServiceNow [D] | `SERVICE_NOW_INTEGRATION` | `missing` | P21 | ServiceNow attaches to blueprints |
| Feature Flags | 1 | [04](04-platform-api-inventory.md) | [04](04-platform-api-inventory.md) | UI support | none | `missing` | P3 | Spacelift-set flags; read-only |
| Flows | 6 | [04](04-platform-api-inventory.md) | [23](23-flows.md) | Not stated | none | `missing` | P26 |  |
| Forms | 3 | [04](04-platform-api-inventory.md) | [04](04-platform-api-inventory.md) | UI support | none | `missing` | P22 | Web UI questionnaires |
| GPG Keys | 4 | [04](04-platform-api-inventory.md) | [15](15-module-and-provider-registry.md) | Business [I] from the provider registry gate | none | `missing` | P17 |  |
| Infrastructure | 3 | [04](04-platform-api-inventory.md) | [24](24-self-hosted-reference.md) | All | none | `missing` | P27 | Installation internals for self-hosted operators |
| Intent | 40 | [04](04-platform-api-inventory.md) | [22](22-intelligence.md) | All [D] | `SPACELIFT_INTELLIGENCE` | `missing` | P25 |  |
| Managed Users | 23 | [04](04-platform-api-inventory.md) | [14](14-spaces-rbac-and-identity.md) | All; Free holds 2 users [C] | none | `missing` | P3 | IdP group mappings are P23 |
| Modules | 34 | [03](03-deploy-core-api-inventory.md) | [15](15-module-and-provider-registry.md) | Paid [D] | `PRIVATE_MODULE_SHARING` | `missing` | P17 |  |
| Notifications | 5 | [04](04-platform-api-inventory.md) | [19](19-events-notifications-audit.md) | Paid [D] | `NOTIFICATION_POLICIES` | `missing` | P18 |  |
| OAuth | 1 | [04](04-platform-api-inventory.md) | [14](14-spaces-rbac-and-identity.md) | All | none | `missing` | P3 | The Slack redirect belongs to report 19 |
| OpenTofu Migration | 4 | [04](04-platform-api-inventory.md) | [20](20-vendors.md) | Not stated | none | `missing` | P20 |  |
| Origin Integration | 4 | [04](04-platform-api-inventory.md) | [11](11-vcs-integrations.md) | Not stated | none | `missing` | P19 | No docs page; an `ORIGIN` source provider |
| Personal API Keys | 4 | [04](04-platform-api-inventory.md) | [14](14-spaces-rbac-and-identity.md) | Not stated | none | `missing` | P3 |  |
| Plugins | 9 | [04](04-platform-api-inventory.md) | [25](25-other-integrations.md) | Not stated | none | `missing` | P20 | Plugins run inside runs; placed with vendor tooling |
| Policies | 21 | [03](03-deploy-core-api-inventory.md) | [09](09-policies.md) | Paid [D]; Starter+ [C] | `WORKFLOW_POLICIES` | `missing` | P13 |  |
| Repos | 12 | [04](04-platform-api-inventory.md) | [11](11-vcs-integrations.md) | Not stated | none | `missing` | P19 | Spacelift Repos, the `SPACELIFT` source provider |
| Resources | 4 | [04](04-platform-api-inventory.md) | [21](21-resources-scans-search.md) | Starter+ [C] | none | `missing` | P22 |  |
| Roles | 25 | [04](04-platform-api-inventory.md) | [14](14-spaces-rbac-and-identity.md) | Not stated | none | `missing` | P4 |  |
| Runs | 35 | [03](03-deploy-core-api-inventory.md) | [08](08-run-lifecycle.md) | All; tasks paid [D]; targeted replan Business [D]; ignored triggers Starter+ [D] | `TARGETED_REPLAN` | `missing` | P10 | P9 delivers the tracked path first; 3 of 35 operations are 501 stubs: `proposeWithOverrides`, `runTrigger`, `taskCreate` |
| SSO | 11 | [04](04-platform-api-inventory.md) | [14](14-spaces-rbac-and-identity.md) | SAML and SCIM Enterprise; OIDC SSO paid [D] | `CUSTOM_SSO_SAML`, `SCIM` | `missing` | P23 |  |
| Saved Filters | 5 | [04](04-platform-api-inventory.md) | [21](21-resources-scans-search.md) | UI support | none | `missing` | P22 |  |
| Scans | 17 | [04](04-platform-api-inventory.md) | [21](21-resources-scans-search.md) | Not stated | `SCANS` | `missing` | P22 | No docs page |
| Search Suggestions | 30 | [04](04-platform-api-inventory.md) | [21](21-resources-scans-search.md) | UI support | none | `missing` | P22 |  |
| Sessions and Security Keys | 5 | [04](04-platform-api-inventory.md) | [14](14-spaces-rbac-and-identity.md) | Not on Starter [G]; session management from Starter+ [C] | none | `missing` | P3 | Security keys are MFA, Enterprise [D] |
| Slack | 7 | [04](04-platform-api-inventory.md) | [19](19-events-notifications-audit.md) | Not stated | none | `missing` | P18 |  |
| Spaces | 22 | [03](03-deploy-core-api-inventory.md) | [14](14-spaces-rbac-and-identity.md) | Starter+ [C]; Free not stated | none | `missing` | P4 |  |
| Stacks | 69 | [03](03-deploy-core-api-inventory.md) | [07](07-stack-model.md), [16](16-state-management.md), [18](18-scheduling-drift-dependencies.md), [19](19-events-notifications-audit.md), [21](21-resources-scans-search.md) | All; scheduling Business [D]; dependencies Starter+ [C]; ignored triggers Starter+ [D] | `ADVANCED_SCHEDULING`, `RUN_IGNORED_TRIGGERS`, `RUN_STATE_CHANGE_WEBHOOKS` | `stub` | P5 | Sub-tags: state P11, schedules and dependencies P16, webhooks P18 |
| Templates | 23 | [04](04-platform-api-inventory.md) | [17](17-blueprints-and-templates.md) | Business [D] [C] | `BLUEPRINTS` | `missing` | P21 |  |
| Terraform Providers | 17 | [04](04-platform-api-inventory.md) | [15](15-module-and-provider-registry.md) | Business [D] [C] | `TERRAFORM_PROVIDER_VERSIONS` | `missing` | P17 |  |
| Tofu Workspaces | 3 | [04](04-platform-api-inventory.md) | [16](16-state-management.md) | Not stated | none | `missing` | P11 |  |
| UI State | 2 | [04](04-platform-api-inventory.md) | [21](21-resources-scans-search.md) | UI support | none | `missing` | P22 |  |
| User Guide | 20 | [04](04-platform-api-inventory.md) | [04](04-platform-api-inventory.md) | UI support | none | `missing` | P22 |  |
| VCS Agent Pools | 6 | [04](04-platform-api-inventory.md) | [11](11-vcs-integrations.md) | Enterprise [D] [C] | `PRIVATE_VCS` | `missing` | P19 |  |
| VCS Integrations | 44 | [04](04-platform-api-inventory.md) | [11](11-vcs-integrations.md) | All; aggregated checks Business; space-level integrations Enterprise [D] | `AGGREGATED_VCS_CHECKS` | `missing` | P12 | GitHub in P12; GitLab, Bitbucket and Azure DevOps in P19 |
| Version Catalog | 7 | [04](04-platform-api-inventory.md) | [20](20-vendors.md), [17](17-blueprints-and-templates.md) | All | none | `missing` | P5 | Tool versions a stack may select |
| Viewer | 1 | [04](04-platform-api-inventory.md) | [14](14-spaces-rbac-and-identity.md) | All | none | `missing` | P3 |  |
| Webhooks | 9 | [04](04-platform-api-inventory.md) | [19](19-events-notifications-audit.md) | Paid [D] | `RUN_STATE_CHANGE_WEBHOOKS` | `missing` | P18 |  |
| Worker Pools | 19 | [03](03-deploy-core-api-inventory.md) | [10](10-workers-and-execution.md) | Public pool all plans; private pools Starter+ [D] | `PRIVATE_WORKERS` | `missing` | P14 | The public pool is P8 |

## 5. Capability table: Terraform resources

One row per resource of terraform-provider-spacelift v1.57.0, 56 in all.
Report [05](05-terraform-provider.md) maps each to its operations and routes.

| Name | Owning report | Plan | Tier features [I] | Root fields called | State | Phase | Notes |
|---|---|---|---|---|---|---|---|
| `spacelift_ai_integration` | [22](22-intelligence.md) | All [D] | `SPACELIFT_INTELLIGENCE` | 6 | `missing` | P25 |  |
| `spacelift_api_key` | [14](14-spaces-rbac-and-identity.md) | All; OIDC keys Enterprise [D] | `API_KEYS_OIDC` | 4 | `missing` | P3 |  |
| `spacelift_audit_trail_webhook` | [19](19-events-notifications-audit.md) | Enterprise [D] | `AUDITING` | 3 | `missing` | P18 |  |
| `spacelift_aws_integration` | [12](12-cloud-integrations-and-oidc.md) | Starter+ [C] | none | 4 | `missing` | P15 |  |
| `spacelift_aws_integration_attachment` | [12](12-cloud-integrations-and-oidc.md) | Starter+ [C] | none | 4 | `missing` | P15 |  |
| `spacelift_aws_role` | [12](12-cloud-integrations-and-oidc.md) | Not stated | none | 4 | `superseded` | none | Legacy per-stack role; replaced by `spacelift_aws_integration` (section 7.7) |
| `spacelift_azure_devops_integration` | [11](11-vcs-integrations.md) | All | `AGGREGATED_VCS_CHECKS` | 4 | `missing` | P19 |  |
| `spacelift_azure_integration` | [12](12-cloud-integrations-and-oidc.md) | Starter+ [C] | none | 4 | `missing` | P15 | Not offered on self-hosted [D] |
| `spacelift_azure_integration_attachment` | [12](12-cloud-integrations-and-oidc.md) | Starter+ [C] | none | 4 | `missing` | P15 |  |
| `spacelift_bitbucket_datacenter_integration` | [11](11-vcs-integrations.md) | All | `AGGREGATED_VCS_CHECKS` | 4 | `missing` | P19 |  |
| `spacelift_blueprint` | [17](17-blueprints-and-templates.md) | Business [D] | `BLUEPRINTS` | 5 | `missing` | P21 | Single-body blueprint; not superseded |
| `spacelift_context` | [13](13-configuration.md) | Starter+ [C] | none | 4 | `missing` | P6 |  |
| `spacelift_context_attachment` | [13](13-configuration.md) | Starter+ [C] | none | 3 | `missing` | P6 |  |
| `spacelift_default_runner_image` | [10](10-workers-and-execution.md) | All | none | 3 | `missing` | P8 |  |
| `spacelift_drift_detection` | [18](18-scheduling-drift-dependencies.md) | Starter+ [D] | `DRIFT_DETECTION` | 5 | `missing` | P16 |  |
| `spacelift_environment_variable` | [13](13-configuration.md) | Not stated | none | 7 | `missing` | P6 |  |
| `spacelift_gcp_service_account` | [12](12-cloud-integrations-and-oidc.md) | Not stated | none | 4 | `superseded` | none | Calls the deprecated `stackIntegrationGcp*` mutations (section 7.6) |
| `spacelift_gitlab_integration` | [11](11-vcs-integrations.md) | All | `AGGREGATED_VCS_CHECKS` | 4 | `missing` | P19 |  |
| `spacelift_idp_group_mapping` | [14](14-spaces-rbac-and-identity.md) | Not stated | none | 4 | `missing` | P23 | Calls deprecated V1 group mutations (section 7.5); the resource is live |
| `spacelift_module` | [15](15-module-and-provider-registry.md) | Paid [D] | `PRIVATE_MODULE_SHARING` | 6 | `missing` | P17 |  |
| `spacelift_mounted_file` | [13](13-configuration.md) | Not stated | none | 7 | `missing` | P6 |  |
| `spacelift_named_webhook` | [19](19-events-notifications-audit.md) | Paid [D] | `RUN_STATE_CHANGE_WEBHOOKS` | 5 | `missing` | P18 |  |
| `spacelift_named_webhook_secret_header` | [19](19-events-notifications-audit.md) | Paid [D] | none | 4 | `missing` | P18 |  |
| `spacelift_plugin` | [25](25-other-integrations.md) | Not stated | none | 4 | `missing` | P20 |  |
| `spacelift_plugin_template` | [25](25-other-integrations.md) | Not stated | none | 3 | `missing` | P20 |  |
| `spacelift_policy` | [09](09-policies.md) | Paid [D] | `WORKFLOW_POLICIES` | 4 | `missing` | P13 |  |
| `spacelift_policy_attachment` | [09](09-policies.md) | Paid [D] | `WORKFLOW_POLICIES` | 3 | `missing` | P13 |  |
| `spacelift_repo` | [11](11-vcs-integrations.md) | Not stated | none | 4 | `missing` | P19 |  |
| `spacelift_repo_file` | [11](11-vcs-integrations.md) | Not stated | none | 2 | `missing` | P19 | Calls the deprecated `revisionCreate`; `repoCommitCreate` replaces the call, not the resource |
| `spacelift_role` | [14](14-spaces-rbac-and-identity.md) | Not stated | none | 4 | `missing` | P4 |  |
| `spacelift_role_attachment` | [14](14-spaces-rbac-and-identity.md) | Not stated | none | 12 | `missing` | P4 |  |
| `spacelift_run` | [08](08-run-lifecycle.md) | All | none | 1 | `stub` | P9 |  |
| `spacelift_saved_filter` | [21](21-resources-scans-search.md) | UI support | none | 4 | `missing` | P22 |  |
| `spacelift_scheduled_delete_stack` | [18](18-scheduling-drift-dependencies.md) | Business [D] | `ADVANCED_SCHEDULING` | 4 | `stub` | P16 |  |
| `spacelift_scheduled_run` | [18](18-scheduling-drift-dependencies.md) | Business [D] | `ADVANCED_SCHEDULING` | 4 | `stub` | P16 |  |
| `spacelift_scheduled_task` | [18](18-scheduling-drift-dependencies.md) | Business [D] | `ADVANCED_SCHEDULING` | 4 | `stub` | P16 |  |
| `spacelift_security_email` | [04](04-platform-api-inventory.md) | All | none | 2 | `missing` | P3 |  |
| `spacelift_space` | [14](14-spaces-rbac-and-identity.md) | Starter+ [C] | none | 4 | `missing` | P4 |  |
| `spacelift_stack` | [07](07-stack-model.md) | All | none | 6 | `stub` | P5 |  |
| `spacelift_stack_activator` | [07](07-stack-model.md) | All | none | 3 | `stub` | P5 |  |
| `spacelift_stack_aws_role` | [12](12-cloud-integrations-and-oidc.md) | Not stated | none | 4 [S] | `superseded` | none | Deprecated alias of `spacelift_aws_role` (section 7.7) |
| `spacelift_stack_dependency` | [18](18-scheduling-drift-dependencies.md) | Starter+ [C] | none | 4 | `stub` | P16 |  |
| `spacelift_stack_dependency_reference` | [18](18-scheduling-drift-dependencies.md) | Starter+ [C] | none | 5 | `missing` | P16 |  |
| `spacelift_stack_destructor` | [07](07-stack-model.md) | All | none | 4 | `missing` | P5 | Also calls `runDiscardAll` (Runs) |
| `spacelift_stack_gcp_service_account` | [12](12-cloud-integrations-and-oidc.md) | Not stated | none | 4 [S] | `superseded` | none | Deprecated alias of `spacelift_gcp_service_account` (section 7.6) |
| `spacelift_task` | [08](08-run-lifecycle.md) | Paid [D] | none | 1 | `stub` | P10 |  |
| `spacelift_template` | [17](17-blueprints-and-templates.md) | Business [D] | `BLUEPRINTS` | 4 | `missing` | P21 |  |
| `spacelift_template_deployment` | [17](17-blueprints-and-templates.md) | Business [D] | `BLUEPRINTS` | 4 | `missing` | P21 |  |
| `spacelift_template_version` | [17](17-blueprints-and-templates.md) | Business [D] | `BLUEPRINTS` | 5 | `missing` | P21 |  |
| `spacelift_terraform_provider` | [15](15-module-and-provider-registry.md) | Business [D] | `TERRAFORM_PROVIDER_VERSIONS` | 5 | `missing` | P17 |  |
| `spacelift_user` | [14](14-spaces-rbac-and-identity.md) | All; Free holds 2 users [C] | none | 4 | `missing` | P3 | Calls the deprecated `managedUserInvite` (section 7.5); the resource is live |
| `spacelift_vcs_agent_pool` | [11](11-vcs-integrations.md) | Enterprise [D] | `PRIVATE_VCS` | 4 | `missing` | P19 |  |
| `spacelift_version` | [15](15-module-and-provider-registry.md) | Paid [D] | none | 2 | `missing` | P17 |  |
| `spacelift_webhook` | [19](19-events-notifications-audit.md) | Paid [D] | `RUN_STATE_CHANGE_WEBHOOKS` | 6 | `missing` | P18 |  |
| `spacelift_worker_pool` | [10](10-workers-and-execution.md) | Starter+ [D] | `PRIVATE_WORKERS` | 5 | `missing` | P14 |  |
| `spacelift_worker_pool_recycle` | [10](10-workers-and-execution.md) | Starter+ [D] | `PRIVATE_WORKERS` | 1 | `missing` | P14 |  |

## 6. Capability table: Terraform data sources

One row per data source, 62 in all.

| Name | Owning report | Plan | Tier features [I] | Root fields called | State | Phase | Notes |
|---|---|---|---|---|---|---|---|
| `spacelift_account` | [04](04-platform-api-inventory.md) | All | none | 3 | `missing` | P3 |  |
| `spacelift_ai_integration` | [22](22-intelligence.md) | All [D] | none | 1 | `missing` | P25 |  |
| `spacelift_ai_integrations` | [22](22-intelligence.md) | All [D] | none | 1 | `missing` | P25 |  |
| `spacelift_aws_integration` | [12](12-cloud-integrations-and-oidc.md) | Starter+ [C] | none | 2 | `missing` | P15 |  |
| `spacelift_aws_integration_attachment` | [12](12-cloud-integrations-and-oidc.md) | Starter+ [C] | none | 1 | `missing` | P15 |  |
| `spacelift_aws_integration_attachment_external_id` | [12](12-cloud-integrations-and-oidc.md) | Starter+ [C] | none | 2 | `missing` | P15 |  |
| `spacelift_aws_integrations` | [12](12-cloud-integrations-and-oidc.md) | Starter+ [C] | none | 1 | `missing` | P15 |  |
| `spacelift_aws_role` | [12](12-cloud-integrations-and-oidc.md) | Not stated | none | 2 | `superseded` | none | Legacy per-stack role; replaced by `spacelift_aws_integration` (section 7.7) |
| `spacelift_azure_devops_integration` | [11](11-vcs-integrations.md) | All | none | 1 | `missing` | P19 |  |
| `spacelift_azure_integration` | [12](12-cloud-integrations-and-oidc.md) | Starter+ [C] | none | 2 | `missing` | P15 | Not offered on self-hosted [D] |
| `spacelift_azure_integration_attachment` | [12](12-cloud-integrations-and-oidc.md) | Starter+ [C] | none | 1 | `missing` | P15 |  |
| `spacelift_azure_integrations` | [12](12-cloud-integrations-and-oidc.md) | Starter+ [C] | none | 1 | `missing` | P15 |  |
| `spacelift_bitbucket_cloud_integration` | [11](11-vcs-integrations.md) | All | none | 4 | `missing` | P19 |  |
| `spacelift_bitbucket_datacenter_integration` | [11](11-vcs-integrations.md) | All | none | 4 | `missing` | P19 |  |
| `spacelift_context` | [13](13-configuration.md) | Starter+ [C] | none | 1 | `missing` | P6 |  |
| `spacelift_context_attachment` | [13](13-configuration.md) | Starter+ [C] | none | 1 | `missing` | P6 |  |
| `spacelift_contexts` | [13](13-configuration.md) | Starter+ [C] | none | 1 | `missing` | P6 |  |
| `spacelift_current_space` | [14](14-spaces-rbac-and-identity.md) | All | none | 2 | `missing` | P8 | Resolves the space of the stack or module the run belongs to |
| `spacelift_current_stack` | [07](07-stack-model.md), [05](05-terraform-provider.md) | All | none | 0 | `missing` | P8 | No API call: reads the `sub` claim of the in-run token [S] |
| `spacelift_drift_detection` | [18](18-scheduling-drift-dependencies.md) | Starter+ [D] | none | 1 [S] | `stub` | P16 | The operation map records no call; the read reuses the resource's `stack` query [S] [`data_drift_detection.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/data_drift_detection.go#L54-L62) |
| `spacelift_environment_variable` | [13](13-configuration.md) | Not stated | none | 3 | `missing` | P6 |  |
| `spacelift_gcp_service_account` | [12](12-cloud-integrations-and-oidc.md) | Not stated | none | 2 [S] | `superseded` | none | Reads `stack` or `module` and the deprecated `StackIntegrations.gcp` field through the resource's read; the operation map records no call [S] [`data_gcp_service_account.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/data_gcp_service_account.go#L62-L75) (section 7.6) |
| `spacelift_github_enterprise_integration` | [11](11-vcs-integrations.md) | All | none | 4 | `missing` | P12 |  |
| `spacelift_gitlab_integration` | [11](11-vcs-integrations.md) | All | none | 4 | `missing` | P19 |  |
| `spacelift_gitlab_webhook_endpoint` | [11](11-vcs-integrations.md) | All | none | 1 | `missing` | P19 |  |
| `spacelift_idp_group_mapping` | [14](14-spaces-rbac-and-identity.md) | Not stated | none | 1 | `missing` | P23 | Reads `managedUserGroups`; its `policy` attribute is deprecated (section 7.4) [S] |
| `spacelift_ips` | [04](04-platform-api-inventory.md), [10](10-workers-and-execution.md) | All | none | 1 | `missing` | P8 | Outgoing addresses of the public pool |
| `spacelift_module` | [15](15-module-and-provider-registry.md) | Paid [D] | none | 1 | `missing` | P17 |  |
| `spacelift_modules` | [15](15-module-and-provider-registry.md) | Paid [D] | none | 1 | `missing` | P17 |  |
| `spacelift_mounted_file` | [13](13-configuration.md) | Not stated | none | 3 | `missing` | P6 |  |
| `spacelift_named_webhook` | [19](19-events-notifications-audit.md) | Paid [D] | none | 1 | `missing` | P18 |  |
| `spacelift_plugin` | [25](25-other-integrations.md) | Not stated | none | 1 | `missing` | P20 |  |
| `spacelift_plugin_template` | [25](25-other-integrations.md) | Not stated | none | 1 | `missing` | P20 |  |
| `spacelift_policies` | [09](09-policies.md) | Paid [D] | none | 5 | `missing` | P13 |  |
| `spacelift_policy` | [09](09-policies.md) | Paid [D] | none | 1 | `missing` | P13 |  |
| `spacelift_repo` | [11](11-vcs-integrations.md) | Not stated | none | 1 | `missing` | P19 |  |
| `spacelift_repos` | [11](11-vcs-integrations.md) | Not stated | none | 1 | `missing` | P19 |  |
| `spacelift_role` | [14](14-spaces-rbac-and-identity.md) | Not stated | none | 1 | `missing` | P4 |  |
| `spacelift_role_actions` | [14](14-spaces-rbac-and-identity.md) | Not stated | none | 0 | `missing` | P4 | Reads the `Action` enum by introspection [S] |
| `spacelift_saved_filter` | [21](21-resources-scans-search.md) | UI support | none | 4 | `missing` | P22 |  |
| `spacelift_saved_filters` | [21](21-resources-scans-search.md) | UI support | none | 1 | `missing` | P22 |  |
| `spacelift_scheduled_delete_stack` | [18](18-scheduling-drift-dependencies.md) | Business [D] | none | 1 | `stub` | P16 |  |
| `spacelift_scheduled_run` | [18](18-scheduling-drift-dependencies.md) | Business [D] | none | 1 | `stub` | P16 |  |
| `spacelift_scheduled_task` | [18](18-scheduling-drift-dependencies.md) | Business [D] | none | 1 | `stub` | P16 |  |
| `spacelift_space` | [14](14-spaces-rbac-and-identity.md) | Starter+ [C] | none | 1 | `missing` | P4 |  |
| `spacelift_space_by_path` | [14](14-spaces-rbac-and-identity.md) | Starter+ [C] | none | 1 | `missing` | P4 |  |
| `spacelift_spaces` | [14](14-spaces-rbac-and-identity.md) | Starter+ [C] | none | 1 | `missing` | P4 |  |
| `spacelift_stack` | [07](07-stack-model.md) | All | none | 1 | `stub` | P5 |  |
| `spacelift_stack_aws_role` | [12](12-cloud-integrations-and-oidc.md) | Not stated | none | 2 [S] | `superseded` | none | Deprecated alias of `spacelift_aws_role` (section 7.7) |
| `spacelift_stack_gcp_service_account` | [12](12-cloud-integrations-and-oidc.md) | Not stated | none | 2 [S] | `superseded` | none | Deprecated alias of `spacelift_gcp_service_account` (section 7.6) |
| `spacelift_stack_outputs` | [07](07-stack-model.md), [18](18-scheduling-drift-dependencies.md) | All | none | 1 | `stub` | P10 | Outputs come from runs; output references are report 18 |
| `spacelift_stacks` | [07](07-stack-model.md) | All | none | 1 | `stub` | P5 |  |
| `spacelift_template` | [17](17-blueprints-and-templates.md) | Business [D] | none | 1 | `missing` | P21 |  |
| `spacelift_template_deployment` | [17](17-blueprints-and-templates.md) | Business [D] | none | 1 | `missing` | P21 |  |
| `spacelift_template_version` | [17](17-blueprints-and-templates.md) | Business [D] | none | 1 | `missing` | P21 |  |
| `spacelift_tool_versions` | [20](20-vendors.md) | All | none | 4 | `missing` | P5 |  |
| `spacelift_user` | [14](14-spaces-rbac-and-identity.md) | All; Free holds 2 users [C] | none | 1 | `missing` | P3 | Reads `managedUsers` [S] |
| `spacelift_vcs_agent_pool` | [11](11-vcs-integrations.md) | Enterprise [D] | `PRIVATE_VCS` | 1 | `missing` | P19 |  |
| `spacelift_vcs_agent_pools` | [11](11-vcs-integrations.md) | Enterprise [D] | `PRIVATE_VCS` | 1 | `missing` | P19 |  |
| `spacelift_webhook` | [19](19-events-notifications-audit.md) | Paid [D] | none | 2 | `missing` | P18 |  |
| `spacelift_worker_pool` | [10](10-workers-and-execution.md) | Starter+ [D] | none | 1 | `missing` | P14 |  |
| `spacelift_worker_pools` | [10](10-workers-and-execution.md) | Starter+ [D] | none | 1 | `missing` | P14 |  |

## 7. Supersession boundaries

infraweave mirrors everything Spacelift offers except an interface that a
newer Spacelift interface supersedes, and each exclusion needs evidence
([README](../README.md), "Decisions fixed before writing"). This section is
that evidence. The changelog has published GraphQL deprecations and removals
since 2026-07-16 [RN].

### 7.1 Root fields outside the reference

`coverage.md` excludes 43 root fields [`coverage.md`](../../../api/reference/spacelift/coverage.md).
Four are web UI analytics (`groupUserEvent`, `identifyUserEvent`,
`pageUserEvent`, `trackUserEvent`): not superseded, just not an API of the
platform. The other 39 are superseded. 35 carry `isDeprecated` in the schema
[G]; four are V1 twins that the reference drops as superseded by their V2
although the schema does not deprecate them (`bitbucketCloudIntegrationCreate`,
`bitbucketCloudIntegrationUpdate`, `moduleUpdate`,
`terraformProviderVersionRegisterPlatform`) [`rest-mapping.yaml`](../../../api/reference/spacelift/rest-mapping.yaml).
The last column names the replacement where the reference routes it.

| Field | Kind | Reason in `coverage.md` | Deprecation in the changelog [RN] | Replacement live in the reference |
|---|---|---|---|---|
| `acceptedTermsAndConditionsAI` | query | deprecated upstream: Use spaceliftIntelligenceTermsAccepted instead. | 2026-08-27 | `spaceliftIntelligenceTermsAccepted` |
| `bedrockInferenceProfiles` | query | deprecated upstream: Use availableAIModels with the Bedrock provider, providerIntegrationId and providerConfig.bedrock.region instead. | 2026-08-27 | `availableAIModels` |
| `bitbucketCloudIntegrationCreate` | mutation | superseded by bitbucketCloudIntegrationCreateV2 | not listed | `bitbucketCloudIntegrationCreateV2` |
| `bitbucketCloudIntegrationUpdate` | mutation | superseded by bitbucketCloudIntegrationUpdateV2 | not listed | `bitbucketCloudIntegrationUpdateV2` |
| `blueprintDeployment` | query | deprecated upstream: Use templateDeployment instead | 2026-07-16 | `templateDeployment` |
| `blueprintDeploymentCreate` | mutation | deprecated upstream: Use templateDeploymentCreate instead | 2026-07-16 | `templateDeploymentCreate` |
| `blueprintDeploymentDelete` | mutation | deprecated upstream: Use templateDeploymentDelete instead | 2026-07-16 | `templateDeploymentDelete` |
| `blueprintDeploymentUpdate` | mutation | deprecated upstream: Use templateDeploymentUpdate instead | 2026-07-16 | `templateDeploymentUpdate` |
| `blueprintVersionCreate` | mutation | deprecated upstream: Use templateVersionCreate instead | 2026-07-16 | `templateVersionCreate` |
| `blueprintVersionDelete` | mutation | deprecated upstream: Use templateVersionDelete instead | 2026-07-16 | `templateVersionDelete` |
| `blueprintVersionParseTemplate` | mutation | deprecated upstream: Use the templateVersionParseTemplate query instead | 2026-07-16 | `templateVersionParseTemplate` |
| `blueprintVersionUpdate` | mutation | deprecated upstream: Use templateVersionUpdate instead | 2026-07-16 | `templateVersionUpdate` |
| `blueprintVersionedGroup` | query | deprecated upstream: Use template instead | 2026-07-16 | `template` |
| `blueprintVersionedGroupCreate` | mutation | deprecated upstream: Use templateCreate instead | 2026-07-16 | `templateCreate` |
| `blueprintVersionedGroupDelete` | mutation | deprecated upstream: Use templateDelete instead | 2026-07-16 | `templateDelete` |
| `blueprintVersionedGroupUpdate` | mutation | deprecated upstream: Use templateUpdate instead | 2026-07-16 | `templateUpdate` |
| `contextCreate` | mutation | deprecated upstream: Please use contextCreateV2. | 2026-07-16 | `contextCreateV2` |
| `contextUpdate` | mutation | deprecated upstream: Please use contextUpdateV2. | 2026-07-16 | `contextUpdateV2` |
| `groupUserEvent` | mutation | Product analytics sent by the Spacelift web UI; not an API of the platform. | not listed | n/a |
| `identifyUserEvent` | mutation | Product analytics sent by the Spacelift web UI; not an API of the platform. | not listed | n/a |
| `latestTermsAndConditionsVersionAI` | query | deprecated upstream: Use spaceliftIntelligenceTermsLatestVersion instead. | 2026-08-27 | `spaceliftIntelligenceTermsLatestVersion` |
| `managedUserGroupCreate` | mutation | deprecated upstream: Please use managedUserGroupCreateV2. | 2026-07-16 | `managedUserGroupCreateV2` |
| `managedUserGroupUpdate` | mutation | deprecated upstream: Please use managedUserGroupUpdateV2. | 2026-07-16 | `managedUserGroupUpdateV2` |
| `managedUserInvite` | mutation | deprecated upstream: Please use managedUserInviteV2. | 2026-07-16 | `managedUserInviteV2` |
| `moduleUpdate` | mutation | superseded by moduleUpdateV2 | not listed | `moduleUpdateV2` |
| `pageUserEvent` | mutation | Product analytics sent by the Spacelift web UI; not an API of the platform. | not listed | n/a |
| `policyCreate` | mutation | deprecated upstream: Use PolicyCreatev2 instead. | 2026-07-16 | `policyCreatev2` |
| `policySimulate` | mutation | deprecated upstream: Use policySimulatev2 instead. | 2026-07-16 | `policySimulatev2` |
| `policyUpdate` | mutation | deprecated upstream: Use PolicyUpdatev2 instead. | 2026-07-16 | `policyUpdatev2` |
| `revision` | query | deprecated upstream: Use repoCommit instead. | 2026-10-20 | `repoCommit` |
| `revisionCreate` | mutation | deprecated upstream: Use repoCommitCreate instead. | 2026-10-20 | `repoCommitCreate` |
| `runPrioritizeSet` | mutation | deprecated upstream: Use runPrioritySet instead. | 2026-10-20 | `runPrioritySet` |
| `searchBlueprintDeployments` | query | deprecated upstream: Use searchTemplateDeployments instead | 2026-07-16 | `searchTemplateDeployments` |
| `searchBlueprintVersionedGroups` | query | deprecated upstream: Use searchTemplates instead | 2026-07-16 | `searchTemplates` |
| `searchBlueprintVersionedGroupsSuggestions` | query | deprecated upstream: Use searchTemplatesSuggestions instead | 2026-07-16 | `searchTemplatesSuggestions` |
| `searchRevisions` | query | deprecated upstream: Use searchRepoCommits instead. | 2026-10-20 | `searchRepoCommits` |
| `searchRevisionsSuggestions` | query | deprecated upstream: Use searchRepoCommitsSuggestions instead. | 2026-10-20 | `searchRepoCommitsSuggestions` |
| `sharedModules` | query | deprecated upstream: Use sharedModulesV2 field instead. | 2026-08-21 | `sharedModulesV2` |
| `stackIntegrationGcpCreate` | mutation | deprecated upstream: Use OIDC integration instead. | 2026-07-16 | n/a |
| `stackIntegrationGcpDelete` | mutation | deprecated upstream: Use OIDC integration instead. | 2026-07-16 | n/a |
| `templateVersionParseTemplate` | mutation | deprecated upstream: Use the templateVersionParseTemplate query instead | 2026-07-16 | `templateVersionParseTemplate` |
| `terraformProviderVersionRegisterPlatform` | mutation | superseded by terraformProviderVersionRegisterPlatformV2 | not listed | `terraformProviderVersionRegisterPlatformV2` |
| `trackUserEvent` | mutation | Product analytics sent by the Spacelift web UI; not an API of the platform. | not listed | n/a |

### 7.2 The versioned-blueprint API, replaced by templates

| Evidence | Source |
|---|---|
| Templates announced on 2026-02-18 | [RN] |
| The versioned-blueprint family (`blueprintVersionedGroup*`, `blueprintVersion*`, `blueprintDeployment*`, `searchBlueprint*`, `Stack.blueprintDeployment`) deprecated on 2026-07-16, each naming its template replacement | [RN], [G] |
| `blueprintDeploymentRollback`, `blueprintVersionDeprecate` and `blueprintV2Schema` removed on 2026-10-20 | [RN] |
| The Blueprints folder says blueprints are being superseded by templates | [B] |

The boundary: single-body blueprints (`blueprint`, `blueprintCreate`,
`blueprintCreateStack`, `blueprintClone` and the ServiceNow attachments)
carry no deprecation and stay in scope, as does the Blueprints tag. The
`blueprintWithGroup*` mutations belong to the versioned family but carry no
deprecation, so they stay in the reference; whether they remain supported is
*(unverified)*. Report [17](17-blueprints-and-templates.md) section 2 holds
the full name mapping.

### 7.3 Policy types

| Type | Replaced by | Evidence | Boundary |
|---|---|---|---|
| `ACCESS` (alias `STACK_ACCESS`) | Spaces and RBAC roles | Disabled on 2026-05-30 [D] <https://docs.spacelift.io/concepts/policy/stack-access-policy>; `STACK_ACCESS` deprecated 2026-10-20 [RN] | Superseded (section 7.4) |
| `INITIALIZATION` (server-side) | `APPROVAL`, or the worker-side initialization policy | Deprecated in the docs [D] <https://docs.spacelift.io/concepts/policy/deprecated>; not deprecated in the schema [G] | Live but deprecated: both sources rank equal, so report [09](09-policies.md) keeps the type in scope |
| `TASK` (alias `TASK_RUN`) | `APPROVAL` | Deprecated in the docs [D]; `TASK_RUN` deprecated 2026-10-20 [RN]; `TASK` not deprecated in the schema [G] | Live but deprecated, as above; the alias is superseded by `TASK` |
| `TERRAFORM_PLAN` | `PLAN` | Deprecated 2026-10-20 [RN] [G] | Superseded alias; rename only |

### 7.4 Access policies and legacy access rules, replaced by spaces and RBAC

| Interface | Replaced by | Evidence |
|---|---|---|
| Access policies (per-stack Rego `read`, `write`, `deny`, `deny_write`, evaluated in `legacy`) | Spaces with role bindings, granted by login policy `roles` or by IAM | Deprecated, then disabled for all accounts on 2026-05-30 [D] <https://docs.spacelift.io/concepts/policy/deprecated/migrate-to-spaces>; residue `BillingTierFeature.ACCESS_POLICIES` and `User.cannotEvaluateAccessPolicies` [G] |
| Login policy `space_admin`, `space_write`, `space_read` rules | The login policy `roles` rule | Deprecated [D] <https://docs.spacelift.io/concepts/policy/login-policy> |
| The stack `administrative` flag | A Space Admin role attachment on the stack | Deprecated on 2025-11-10 and disabled on 2026-06-01; the flag is always false in policy input since [RN] [D] |
| The `policy` block of `spacelift_user` and `spacelift_idp_group_mapping` | Role bindings (`spacelift_role_attachment`) | Deprecated in provider v1.57.0 [S] `terraform-schema.json` |

The boundary: login policies themselves are live. `LOGIN_POLICIES` is one of
two `AuthorizationScheme` values beside `MANAGED_USERS`, and both are in
scope (report [14](14-spaces-rbac-and-identity.md) section 5). Whether
Spacelift still evaluates access policies after 2026-05-30, when one docs page
still describes them as working, is *(unverified)* (report 09 section 1.2).

### 7.5 V1 twins and renamed fields

| Superseded | Replacement | Evidence |
|---|---|---|
| `contextCreate`, `contextUpdate` | `contextCreateV2`, `contextUpdateV2` | 2026-07-16 [RN] [G] |
| `policyCreate`, `policyUpdate`, `policySimulate` | `policyCreatev2`, `policyUpdatev2`, `policySimulatev2` | 2026-07-16 [RN] [G] |
| `managedUserGroupCreate`, `managedUserGroupUpdate`, `managedUserInvite` | their `V2` forms | 2026-07-16 [RN] [G] |
| `sharedModules` | `sharedModulesV2` | 2026-08-21 [RN] [G] |
| `bitbucketCloudIntegrationCreate`, `bitbucketCloudIntegrationUpdate`, `moduleUpdate`, `terraformProviderVersionRegisterPlatform` | their `V2` forms | Not deprecated [G]; dropped by `rest-mapping.yaml` |
| `revision`, `searchRevisions`, `searchRevisionsSuggestions`, `revisionCreate` | `repoCommit`, `searchRepoCommits`, `searchRepoCommitsSuggestions`, `repoCommitCreate` | 2026-10-20 [RN] [G] |
| `runPrioritizeSet`, `Run.isPrioritized` | `runPrioritySet`, `Run.priorityPreset` | 2026-10-20 [RN] [G] |
| `acceptedTermsAndConditionsAI`, `latestTermsAndConditionsVersionAI`, `bedrockInferenceProfiles` | `spaceliftIntelligenceTermsAccepted`, `spaceliftIntelligenceTermsLatestVersion`, `availableAIModels` | 2026-08-27 [RN] [G] |
| `BillingTierFeature.AUDIT_WEBHOOKS` | `AUDITING` | 2026-10-20 [RN] [G] |
| The flat hook fields on `Stack` (`afterApply`, `beforeInit` and the rest) | `Stack.hooks` | 2026-07-16 [RN] [G] |

Three provider resources still call superseded mutations
(`spacelift_idp_group_mapping`, `spacelift_user`, `spacelift_repo_file`) and
stay in scope: the resource is live, only its call is replaced (report
[05](05-terraform-provider.md) section 4). Deprecated output fields and enum
values beyond the root fields are listed field by field in report 29.

### 7.6 GCP service-account integration, replaced by OIDC

`stackIntegrationGcpCreate`, `stackIntegrationGcpDelete` and
`StackIntegrations.gcp` are deprecated with the reason "Use OIDC integration
instead" [G], listed on 2026-07-16 [RN]. `spacelift_gcp_service_account`
calls them, and `spacelift_stack_gcp_service_account` is its deprecated alias
[S] `terraform-schema.json`. Both resources and both data sources are
`superseded`. GCP remains reachable through the OIDC federation token
(report [12](12-cloud-integrations-and-oidc.md) sections 5 and 7.2).

### 7.7 Legacy per-stack AWS role, replaced by AWS integrations

The provider marks `spacelift_aws_role` and `spacelift_stack_aws_role`
deprecated: the first is replaced by `spacelift_aws_integration`, and the
second is an alias of the first [S] `terraform-schema.json`. The GraphQL
mutations they call, `stackIntegrationAwsCreate` and
`stackIntegrationAwsDelete`, are not deprecated [G] and stay in the
reference, and `AwsIntegration.legacy` marks integrations they create [G].
The two resources are `superseded`. The `spacelift_aws_role` data source is
not marked deprecated, but it reads the same legacy `StackIntegrations.aws`
field and has no non-legacy use, so it is `superseded` too [I] from the
resource's deprecation. Whether infraweave still needs the two legacy
mutations, for example to read stacks that have them, is a question for the
plan (report 12 section 2).

### 7.8 Other dated supersessions

| Interface | Replaced by | Evidence | Effect on scope |
|---|---|---|---|
| Worker transport over AWS IoT Core (`worker-iot.spacelift.io`) | HTTP long-poll | Long-poll added 2026-07-20; IoT Core sunset on 2027-03-30 [RN] | The MQTT transport stays in scope for self-hosted's own broker; the SaaS IoT Core endpoint is out (report [10](10-workers-and-execution.md) section 4.2) |
| The `pulumi-spacelift` provider | Pulumi's Terraform bridge over the Spacelift Terraform provider | Retirement and archival set for 2026-12-10 [RN] | No infraweave Pulumi provider is needed (report [06](06-spacectl-and-clients.md)) |
| `changeLLMVendor`, `llmVendor` | none | Removed on 2026-08-21 [RN] | Absent from the schema [G] |
| `PENDING` in `RunState` | none | "This state is no longer used", 2026-10-20 [RN] [G] | Not reachable (report [08](08-run-lifecycle.md)) |
| The `feature:add_plan_pr_comment` stack label | Notification policies with `pull_request` rules | [D] <https://docs.spacelift.io/concepts/stack/stack-settings> | Report 09 section 1.2 |

## 8. Completeness check

The tables in sections 3 to 7.1 were generated by script. It read the specification reference, the deploy document,
the introspection snapshot, `terraform-schema.json`,
`terraform-operations.json`, `coverage.md`, the changelog and reports 03 and
04, filled the tables, and then checked:

| Check | Result at the baseline |
|---|---|
| Every spec tag appears exactly once in section 4 | 53 of 53 |
| Every spec tag name appears verbatim in this report | 53 of 53 |
| Every Terraform resource appears exactly once in section 5 | 56 of 56 |
| Every Terraform data source appears exactly once in section 6 | 62 of 62 |
| Every `BillingTierFeature` value appears exactly once in section 3 | 31 of 31 |
| Every root field `coverage.md` excludes appears in section 7.1 | 43 of 43 |

The curated cells (owning report, plan, phase, notes) are fixed in the script
so that a rerun reproduces them; the counts, tier-feature columns and states
are recomputed on every run. The RES-07 fact-check corrected some cells by
hand (report [31](31-review-log.md)): the plans taken from the pricing
page's comparison table, the Stacks row's tier features, and the root-field
counts, states and notes of the `spacelift_drift_detection`,
`spacelift_gcp_service_account`, `spacelift_idp_group_mapping` and
`spacelift_user` data sources, and the root-field counts of the two deprecated
alias resources and data sources, which the operation map leaves empty. A
rerun must fold those corrections in.

## What this means for infraweave

- Scope is every row of sections 4 to 6 whose state is not `superseded`: 53
  tags, 52 resources and 58 data sources, plus the 8 untagged operations of
  report 04.
- `BillingTierFeature` is a per-account feature set separate from RBAC. A
  self-hosted, open-source product still has to answer `tier` and
  `tierFeatures`, and the plan must say what they return.
- Several capabilities have no stated plan; the plan has to place them
  without a tier signal.
- FedRAMP shows that every feature must work with private workers only; the
  public pool is optional infrastructure.
- Self-hosted Spacelift lacks the public worker pool, the Azure and GCP
  integrations and the hosted AI integration; infraweave's self-hosted packaging (P27) does not inherit
  those limits by default, and the plan must say which it keeps.
- Superseded interfaces still appear in stored data (legacy AWS
  integrations, `legacy` space, access-policy residue); an importer from
  Spacelift meets them even though infraweave does not serve them.

## Inferred and undocumented

- The pairing of `BillingTierFeature` values with operations is by name
  [I] (report 03 section 1.3); only the docs notice for each product feature
  is a fact.
- The pairing of `BillingTier` values with editions is by name [I]; the
  `V4_ENTERPRISE` description contradicts its name.
- Only `AUDITING`, `SCIM` and `SCANS` have a schema description. The meaning
  of the other 28 values is read from the name, matched to the docs feature
  of the same name [I]. Twelve have no docs feature with an obvious match and
  say so in their meaning cell: `ACCESS_POLICIES`, `ADDITIONAL_PUBLIC_WORKER`,
  `ADVANCED_SCHEDULING`, `BLUEPRINTS`, `CAN_UPDATE_SSO_SAML`,
  `CLOUD_CREDENTIAL_GENERATION`, `MULTIPLE_VCS`, `PRIVATE_MODULE_SHARING`,
  `WORKFLOW_POLICIES`, `ACCOUNT_AI_CONTROL`, `SPACELIFT_INTELLIGENCE` and
  `BACKSTAGE_INTEGRATION`.
- The phase column is provisional until IMP-01 confirms the spine.
- Open, marked *(unverified)* above:
  - what a plain Starter tier includes, and what separates `V5_STARTER_PLUS`
    from `V5_STARTER_PLUS_2`;
  - the plan of every capability whose plan reads "Not stated";
  - what `tier` and `tierFeatures` return on a self-hosted install;
  - whether the `blueprintWithGroup*` mutations remain supported;
  - whether access policies are still evaluated after 2026-05-30;
  - which policy types `WORKFLOW_POLICIES` covers.

## Versions at the research baseline

| Input | Version |
|---|---|
| Introspection | `b3255fe38f5e`, fetched 2026-10-28 |
| spacelift-api-bruno | `7cad4574954b` |
| terraform-provider-spacelift | v1.57.0 (`c72b9bd2f3c3`) |
| Docs mirror | read 2026-10-28; self-hosted v7.1.0 |
| Pricing page | read 2026-10-28 |
| infraweave deploy document | `api/deploy.openapi.yaml`, 72 operations |

## Sources

- [`api/reference/spacelift/spacelift.openapi.yaml`](../../../api/reference/spacelift/spacelift.openapi.yaml): tags and operations.
- [`api/reference/spacelift/introspection.json`](../../../api/reference/spacelift/introspection.json): `BillingTierFeature`, `BillingTier`, deprecations **[G]**.
- [`api/reference/spacelift/terraform-schema.json`](../../../api/reference/spacelift/terraform-schema.json) and [`terraform-operations.json`](../../../api/reference/spacelift/terraform-operations.json): resources, data sources, provider deprecations **[S]**.
- [`api/reference/spacelift/coverage.md`](../../../api/reference/spacelift/coverage.md) and [`rest-mapping.yaml`](../../../api/reference/spacelift/rest-mapping.yaml): excluded root fields.
- [`api/deploy.openapi.yaml`](../../../api/deploy.openapi.yaml): infraweave's served operations.
- <https://spacelift.io/pricing> **[C]**, read 2026-10-28.
- <https://docs.spacelift.io/product/fedramp> **[D]**
- <https://docs.spacelift.io/self-hosted>, <https://docs.spacelift.io/concepts/identity-access-management/admin>, <https://docs.spacelift.io/concepts/run/task> **[D]**
- <https://docs.spacelift.io/product/changelog> **[RN]**
- <https://docs.spacelift.io/self-hosted/latest/concepts/intelligence> **[D]**
- <https://docs.spacelift.io/concepts/policy/deprecated>, <https://docs.spacelift.io/concepts/policy/deprecated/migrate-to-spaces>, <https://docs.spacelift.io/concepts/policy/stack-access-policy>, <https://docs.spacelift.io/concepts/policy/login-policy> **[D]**
- <https://docs.spacelift.io/concepts/stack/stack-settings> **[D]**
- The tier notices cited per feature in reports [03](03-deploy-core-api-inventory.md) section 10, [04](04-platform-api-inventory.md), [14](14-spaces-rbac-and-identity.md) section 11 and [19](19-events-notifications-audit.md) section 12 **[D]**.
