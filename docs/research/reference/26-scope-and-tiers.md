# 26: Scope and tiers

Checked against: docs read 2026-10-28 (SaaS and `self-hosted/latest`, v7.1.0); spacelift.io/pricing read 2026-10-28; schema `b3255fe38f5e`; spacelift-api-bruno `7cad4574954b`; terraform-provider-spacelift v1.57.0 (`c72b9bd2f3c3`); the specification reference `api/reference/spacelift/spacelift.openapi.yaml` and `coverage.md`; infraweave's `api/deploy.openapi.yaml` at the work tree's commit; reports 01 to 25; decision [0021](../decisions/0021-capability-scope-and-supersession.md) as accepted on 2026-11-03.

Research baseline: **2026-10-28**.

This report places every Spacelift capability on two axes: which edition
offers it, and how far infraweave has come with it. It lists Spacelift's
editions, and its research on the 31 `BillingTierFeature` values that
gate features by plan, the 53 tags of the specification reference, and the
56 resources and 62 data sources of the Terraform provider. That research is
one record per value, tag, resource and data source in the register
[docs/data/capabilities.json](../../data/capabilities.json): the record's
`spacelift` member names the report that owns the behaviour, the plan that
offers it and the tier features it pairs with, and its `infraweave` member
holds infraweave's availability state and the plan's phase. Section 7
records every interface excluded as superseded, with its evidence. Decision
[0021](../decisions/0021-capability-scope-and-supersession.md) turns the `plan`
member into infraweave's seeded tiers and maps each `BillingTierFeature`
value to infraweave's own feature; this report keeps Spacelift's names. The
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

Each record of [capabilities.json](../../data/capabilities.json) carries these members. Their text is
markdown and keeps this report's source labels.

| Member | Content | Source |
|---|---|---|
| `ops` | Operations the specification reference files under the tag | `spacelift.openapi.yaml`, counted by script |
| `spacelift.inventory` | The report that lists the tag's operations one by one | [03](03-deploy-core-api-inventory.md), [04](04-platform-api-inventory.md) |
| `spacelift.owningReport` | The report that records the behaviour; the first named owns most of it | reports 07 to 25 |
| `spacelift.plan` | The lowest SaaS plan that offers the capability, in the vocabulary of section 1.3 | the cited docs notice [D] or the pricing page [C] |
| `spacelift.tierFeatures` [I] | `BillingTierFeature` values that reports 03 and 04 pair with the operations in the row, by name | computed from those reports' tables; the pairing is an inference, as report 03 section 1.3 explains |
| `spacelift.rootFields` | Terraform rows only: GraphQL root fields the provider calls for the resource or data source | `terraform-operations.json`, filtered to root fields of the schema [G]; where the map records no call but the provider's source makes one, the member gives the count from the source, marked [S] |
| `infraweave.state` | infraweave's availability state (section 1.2) | computed against `api/deploy.openapi.yaml` |
| `infraweave.phase`, `infraweave.apiPhase` | The phase of the [implementation plan](../../implementation/plan.md) that delivers the row | the plan |

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

The spine below is the research's draft ordering. The implementation plan
(IMP-01, [#31](https://github.com/weaveplatform/infraweave/issues/31))
refines it, and a record's `infraweave` phase is the plan's; the
`spacelift` notes keep the draft's wording. A row's phase is where the
capability is delivered first; superseded rows have none.

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

Every Terraform row also needs P24, which builds the provider; a Terraform
record's `apiPhase` names the phase that delivers the API the row calls.

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
| AI | Intent, the MCP server, Infra Assistant and Intent policies work; the Spacelift-provided AI integration does not, so a bring-your-own-model integration is needed. The self-hosted Intelligence page lists run summarisation (Saturnhead Assist) as not yet available, while the self-hosted changelog adds run summaries in v7.0.0 (report [22](22-intelligence.md) section 4.3); which holds for v7.1.0 is *(unverified)* | [D] <https://docs.spacelift.io/self-hosted/latest/concepts/intelligence>, <https://docs.spacelift.io/self-hosted/latest/installing-spacelift/changelog>; report 22 |
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

The enum has 31 values [G]. Decision
[0021](../decisions/0021-capability-scope-and-supersession.md) maps each to
the infraweave feature that covers it. Only `AUDITING`, `SCIM` and `SCANS` carry a
description, and `AUDIT_WEBHOOKS` is deprecated in favour of `AUDITING` [G],
listed on 2026-10-20 [RN]. Each value is a `tier-feature:` record of
[capabilities.json](../../data/capabilities.json), whose `number` is its position in the
enum. Where its `meaning` says "[I] from the name" or "from the operations
gated", nothing published defines the value. `operationsTagged` counts the
operations that reports 03 and 04 pair with the value; a zero means no
operation's name matches it, not that nothing checks it. `plan` is the
minimum SaaS plan, and `selfHosted` what self-hosted and FedRAMP offer.

## 4. Capability table: API tags

One `tag:` record of [capabilities.json](../../data/capabilities.json) per tag of the
specification reference, 53 in all, in the reference's order. The 8
untagged operations (report 04 section 14) are not a tag; their
`operation:` records carry infraweave's plan only.

## 5. Capability table: Terraform resources

One `resource:` record of [capabilities.json](../../data/capabilities.json) per resource of
terraform-provider-spacelift v1.57.0, 56 in all. Report
[05](05-terraform-provider.md) maps each to its operations and routes.

## 6. Capability table: Terraform data sources

One `data-source:` record of [capabilities.json](../../data/capabilities.json) per data
source, 62 in all.

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
resource's deprecation. The two legacy mutations stay in scope, served and
marked deprecated, and the importer keeps the integrations they created
(decision [0021](../decisions/0021-capability-scope-and-supersession.md),
report 12 section 2).

### 7.8 Other dated supersessions

| Interface | Replaced by | Evidence | Effect on scope |
|---|---|---|---|
| Worker transport over AWS IoT Core (`worker-iot.spacelift.io`) | HTTP long-poll | Long-poll added 2026-07-20; IoT Core sunset on 2027-03-30 [RN]; the self-hosted v7.1.0 changelog deprecates MQTT, including the built-in broker [D] | Superseded under S3 of decision [0021](../decisions/0021-capability-scope-and-supersession.md): neither the SaaS IoT Core endpoint nor a self-hosted broker is in scope; long-poll is (report [10](10-workers-and-execution.md) section 4.2) |
| The `pulumi-spacelift` provider | Pulumi's Terraform bridge over the Spacelift Terraform provider | Retirement and archival set for 2026-12-10 [RN] | No infraweave Pulumi provider is needed (report [06](06-spacectl-and-clients.md)) |
| `changeLLMVendor`, `llmVendor` | none | Removed on 2026-08-21 [RN] | Absent from the schema [G] |
| `PENDING` in `RunState` | none | "This state is no longer used", 2026-10-20 [RN] [G] | Not reachable (report [08](08-run-lifecycle.md)) |
| The `feature:add_plan_pr_comment` stack label | Notification policies with `pull_request` rules | [D] <https://docs.spacelift.io/concepts/stack/stack-settings> | Report 09 section 1.2 |

## 8. Completeness check

A script generates this report's research in the capability records (each
`spacelift` member, `ops` and the baseline `state`) and the table in
section 7.1. It reads the specification reference, the deploy
document, the introspection snapshot, `terraform-schema.json`,
`terraform-operations.json`, `coverage.md`, the changelog and reports 03 and
04, fills them, and then checks:

| Check | Result at the baseline |
|---|---|
| Every spec tag has exactly one `tag:` record | 53 of 53 |
| Every Terraform resource has exactly one `resource:` record | 56 of 56 |
| Every Terraform data source has exactly one `data-source:` record | 62 of 62 |
| Every `BillingTierFeature` value has exactly one `tier-feature:` record | 31 of 31 |
| Every root field `coverage.md` excludes appears in section 7.1 | 43 of 43 |

`make docs-check` holds the first between runs: it fails when a spec tag has
no `tag:` record, or a `tag:` record no spec tag.

The curated members (owning report, plan, notes) are fixed in the script
so that a rerun reproduces them; the counts, tier-feature members and states
are recomputed on every run. The RES-07 fact-check corrected some cells by
hand (report [31](31-review-log.md)): the plans taken from the pricing
page's comparison table, the Stacks row's tier features, and the root-field
counts, states and notes of the `spacelift_drift_detection`,
`spacelift_gcp_service_account`, `spacelift_idp_group_mapping` and
`spacelift_user` data sources, and the root-field counts of the two deprecated
alias resources and data sources, which the operation map leaves empty. A
rerun must fold those corrections in.

## What this means for infraweave

- Scope is every tag, resource and data source record of
  [capabilities.json](../../data/capabilities.json) whose state is not `superseded`:
  53 tags, 52 resources and 58 data sources, plus the 8 untagged operations
  of report 04.
- `BillingTierFeature` is a per-account feature set separate from RBAC.
  infraweave has tiers of its own: decision
  [0021](../decisions/0021-capability-scope-and-supersession.md) seeds Free,
  Starter, Starter+, Business and Enterprise from the `plan` member, gives
  each account one tier, and answers `tier` with a tier object and
  `tierFeatures` with the account's entitlement. Billing is off by default,
  so every account behaves as Enterprise until an operator turns it on.
- Several capabilities have no stated plan; decision 0021 seeds them in
  every tier until a capture places them.
- FedRAMP shows that every feature must work with private workers only; the
  public pool is optional infrastructure.
- Self-hosted Spacelift lacks the public worker pool, the Azure and GCP
  integrations and the hosted AI integration; infraweave's self-hosted
  packaging (P27) does not inherit those limits (decision 0021).
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
  say so in their `meaning`: `ACCESS_POLICIES`, `ADDITIONAL_PUBLIC_WORKER`,
  `ADVANCED_SCHEDULING`, `BLUEPRINTS`, `CAN_UPDATE_SSO_SAML`,
  `CLOUD_CREDENTIAL_GENERATION`, `MULTIPLE_VCS`, `PRIVATE_MODULE_SHARING`,
  `WORKFLOW_POLICIES`, `ACCOUNT_AI_CONTROL`, `SPACELIFT_INTELLIGENCE` and
  `BACKSTAGE_INTEGRATION`.
- Open, marked *(unverified)* above:
  - what a plain Starter tier includes, and what separates `V5_STARTER_PLUS`
    from `V5_STARTER_PLUS_2`;
  - the plan of every capability whose plan reads "Not stated";
  - what `tier` and `tierFeatures` return on a self-hosted install;
  - whether self-hosted v7.1.0 offers run summaries;
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
- <https://docs.spacelift.io/self-hosted/latest/concepts/intelligence>, <https://docs.spacelift.io/self-hosted/latest/installing-spacelift/changelog> **[D]**
- <https://docs.spacelift.io/concepts/policy/deprecated>, <https://docs.spacelift.io/concepts/policy/deprecated/migrate-to-spaces>, <https://docs.spacelift.io/concepts/policy/stack-access-policy>, <https://docs.spacelift.io/concepts/policy/login-policy> **[D]**
- <https://docs.spacelift.io/concepts/stack/stack-settings> **[D]**
- The tier notices cited per feature in reports [03](03-deploy-core-api-inventory.md) section 10, [04](04-platform-api-inventory.md), [14](14-spaces-rbac-and-identity.md) section 11 and [19](19-events-notifications-audit.md) section 12 **[D]**.
