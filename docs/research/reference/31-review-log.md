# 31: Review log

Checked against: the RES-02 to RES-08, ARC-01 and IMP-01 fact-check reviews, reports 01 and 03 to 28, decisions 0004 to 0022, the architecture options and the implementation documents as they stand after those reviews' fixes, 2026-10-28; the DES-05 correction pass on report 23, with the 46 Flows templates and six MIT `scenario.yaml` files read 2026-11-03; the DES-07 cross-read of decisions 0001 to 0024 and the implementation documents, 2026-11-03; the DES-09 pass over decisions 0001 to 0024, report 30 and the served deploy bundle, 2026-11-03; the DES-10 ratification of decision 0004 and the architecture options, 2026-11-04; introspection `b3255fe38f5e`; docs.spacelift.io read 2026-10-28.

Research baseline: **2026-10-28**.

This log records what the independent fact-checks of the reference reports
and the proposed decision records found and what each finding changed, so
that a reader can tell which statements in reports 01 and 03 to 28, in
decisions 0004 to 0022, in the architecture options and in the
implementation documents are corrections rather than first drafts. It
covers thirteen passes, one per ticket from RES-02 to RES-08, ARC-01,
IMP-01, DES-05, DES-07, DES-09 and DES-10, and keeps every
finding, shortened. Report 02 predates these
tickets and had no pass. The open items the passes raised are entries in
[30](30-open-questions.md), cited here by number; the terms are in the
[glossary](glossary.md).

| Label | Means |
|---|---|
| *wrong* | The report or record stated something a primary source, or another record, contradicts |
| *unsupported* | The claim may hold, but its cited source does not show it, or its marker claimed more than the source gives |
| *missing* | A fact in scope that a cited or obvious source states was absent |
| *copied* | Text matched a run of 12 or more words on docs.spacelift.io, or quoted a source without the attribution its licence needs |
| *style* | Tense, wording, history narration, link labels or internal references |

## Method

Every report went through three steps.

1. **Draft.** A drafting agent wrote the reports for one ticket from the
   local sources: the docs mirror, the committed schema, Bruno and provider
   snapshots, and the pinned Spacelift repositories. It left notes with
   glossary candidates, open questions and coverage gaps.
2. **Independent fact-check.** A second agent, which had not drafted the
   reports, checked them. It wrote its own scripts (`python3 -I`) and read
   none of the drafter's scripts. Where a ticket had many reports, the
   checker gave some to further independent sub-checkers and spot-checked
   their corrections at source. Every pass ran the same mechanical checks:
   - runs of 12 or more words shared with any of the 702 mirrored docs pages;
   - every docs.spacelift.io URL resolves to a mirrored page;
   - every GitHub link names a pinned repository at its commit, an existing
     file and a line range inside it, and the lines show the claim;
   - schema tables, enums and signatures re-derived from `introspection.json`,
     Terraform tables from `terraform-schema.json`, and REST paths from
     `spacelift.openapi.yaml`;
   - the report shape, em and en dashes, and
     `GOWORK=off go test -count=1 -run '^TestUnitDocs$' ./internal/docscheck`.

   It also checked a sample of claims by hand against the cited page,
   schema entry, changelog entry or source line, weighted toward defaults,
   enums, transitions, precedence and protocol details.
3. **Fix in place.** The checker edited the reports directly, recorded each
   finding with its severity and change, listed what survived, and added
   the open questions its findings raised.

Some passes made live, read-only captures where the local sources fell
short: RES-05 re-captured the module and provider registry endpoints, and
RES-06 read the live Flows OAuth metadata. RES-07 read the pricing page's
raw HTML and the GitHub release list. RES-03 fetched the policy
contract.

RES-08 reviewed decision records rather than reports. Two helpers each
checked half the records against the reports and primary sources, and the
checker compared every shared name across all 18 records (problem slugs,
configuration keys, job kinds, channels, tables, sealed columns, test and
feature names, process roles), counting a contradiction between records as
*wrong*. It also renamed the records' process and binary names to decision
0004's.

ARC-01 reviewed the five architecture options, their comparison and
decision 0004. Two helpers took options 1 to 4; the checker took option 5,
the comparison and 0004, and settled Go and depguard claims by experiment
rather than from documentation.

IMP-01 reviewed the implementation plan, the capability roadmap, the
deviations register, the repository layout and the definition of done. A
helper took the roadmap and the register; the checker took the plan's
phase graph, tasks, exit criteria and milestones, the layout and the
definition of done, settling dependencies by tracing each task's packages
and tables to the phase that builds them, and build status against the
GitHub issues. A task that uses something no phase it depends on builds
counts as *wrong*.

DES-05 corrects report 23's account of Flows expressions, on which decision
[0024](../decisions/0024-expression-language.md) relies. Its source is a survey of every published Flows template and of the MIT apps' test
flows; a second script, written for this pass, re-derived each count and
cited line before it entered the report, and where the survey's figures
and the recount differed, the recount is what the report records.

DES-07 read decisions 0001 to 0024, all Accepted, against each other and
the implementation documents against the records, after the plan was
reordered around them. A contradiction between records is *wrong* in the
record that departs from the owning one, and is fixed there.

DES-09 applied the maintainer's answers on tenancy and names to decisions
0001 to 0024, then read them against each other for what the answers
changed. A second reader, who made none of the changes, cross-read the
records, and each contradiction it found is fixed in the record that does
not own the fact or listed as left open. The counting rule is DES-07's.

DES-10 tested decision 0004 before any code moves into its layout. Three
reviewers who had not written the options argued for DDD, stress-tested the
modular monolith, and audited the one-way doors of the decisions 0004
rests on; each claim they cited was read against the files before it
counted. The counting rule is DES-07's.

## Findings

Every finding is a record of
[review-findings.json](../../data/review-findings.json): its ID is the pass
and the finding's number in that pass, such as `RES-02.06-16`, and it
carries the severity label above, the finding, what the pass changed and a
status. A `folded` finding names the phrases that show its change in the
tree today, and the docs check fails when one of them disappears or when a
removed error comes back; a `superseded` one says what later rewrite
replaced the passage; an `open` one names the question of
[30](30-open-questions.md) that still holds it. The counts by pass, label
and status are queries of the register.

| Pass | Reports | Claims checked by hand |
|---|---|---|
| RES-02 | [01](01-spacelift-landscape.md), [03](03-deploy-core-api-inventory.md), [04](04-platform-api-inventory.md), [05](05-terraform-provider.md), [06](06-spacectl-and-clients.md) | about 320, plus every table row by script |
| RES-03 | [07](07-stack-model.md), [08](08-run-lifecycle.md), [09](09-policies.md), [10](10-workers-and-execution.md), [13](13-configuration.md), [16](16-state-management.md) | 459 |
| RES-04 | [11](11-vcs-integrations.md), [12](12-cloud-integrations-and-oidc.md), [14](14-spaces-rbac-and-identity.md), [19](19-events-notifications-audit.md) | about 400, plus about 500 table rows by script |
| RES-05 | [15](15-module-and-provider-registry.md), [17](17-blueprints-and-templates.md), [18](18-scheduling-drift-dependencies.md), [20](20-vendors.md), [21](21-resources-scans-search.md), [25](25-other-integrations.md) | about 500, plus every schema table row by script |
| RES-06 | [22](22-intelligence.md), [23](23-flows.md), [24](24-self-hosted-reference.md) | about 255, plus every link and generated row by script |
| RES-07 | [26](26-scope-and-tiers.md), [27](27-reference-materials.md), [28](28-data-model.md) | about 175, plus every table row, link and typed field by script |
| RES-08 | decisions [0005](../decisions/0005-persistence-postgresql.md) to [0022](../decisions/0022-api-errors-and-update-semantics.md) | about 765, plus every link, slug and shared name by script |
| ARC-01 | [architecture options](../architecture-options/README.md) and decision [0004](../decisions/0004-repository-architecture.md) | about 450, plus every heading, depguard block, matrix cell and mapping row by script |
| IMP-01 | the [implementation documents](../../implementation/plan.md) and option 5 | about 400, plus every operationId, roadmap row and DV row by script |
| DES-05 | [23](23-flows.md) | about 40, plus every `!expr` value and `ref(` call by script |
| DES-07 | decisions [0001](../decisions/0001-rest-first-spacelift-surface.md) to [0024](../decisions/0024-expression-language.md) and the [implementation documents](../../implementation/plan.md) | about 250, plus the phase graph, every roadmap row and every changed file's dashes by script |
| DES-09 | decisions [0001](../decisions/0001-rest-first-spacelift-surface.md) to [0024](../decisions/0024-expression-language.md), [30](30-open-questions.md) and the served deploy bundle | about 300, plus every served name and every reference Spacelift and root identifier by script |
| DES-10 | decision [0004](../decisions/0004-repository-architecture.md), the [architecture options](../architecture-options/README.md), the [repository layout](../../implementation/repo-layout.md), the [plan](../../implementation/plan.md), decisions [0005](../decisions/0005-persistence-postgresql.md), [0015](../decisions/0015-events-notifications-webhooks-audit.md) and [0016](../decisions/0016-testing-and-acceptance.md) | about 180, plus the strict-server composition compiled in a scratch module |
| **All** | 28 reports, 24 records, 5 options, 5 implementation documents | about 4,490 |

## RES-02: platform API inventories, provider and clients

### Claims checked

- **01:** about 45 by hand: the authentication table, token facts, error
  behaviour, rate limits, the self-hosted changelog and landing page, Bruno
  and specification counts, the workflow schedule and the specref claims.
- **03 and 04:** about 35 docs notes by hand (run timeouts, priorities,
  targeted replan, stack locks, the task tier, RBAC docs and the SSO, SCIM
  and MFA gates). Scripts compared all 725 operation rows with the
  specification (53 tags and 8 untagged operations), every Purpose cell
  with the schema and Bruno, the 150-value `Action` catalogue and every
  RBAC cell, the tier statement on each of 19 cited pages, 295 docs URLs
  and 13 line links.
- **05** (independent sub-checker): about 100 claims by hand, and by script
  every table row: 56 resources, 62 data sources, 334 REST routes, the
  211/205/6 field counts and 60 GitHub links.
- **06** (independent sub-checker): about 140 claims by hand, and by script
  the 108-command tree with every flag, the 74 reachable root fields, the
  section 4 routes and 233 links.
- **Copy check:** two 12-word runs in 03, both rewritten; none elsewhere.

### Findings

The pass's findings are the `RES-02.` records of the register
[review-findings.json](../../data/review-findings.json).

### What survived

- **The mechanical columns of 03 and 04:** every method, path and GraphQL
  field, the query, mutation and `on Type` marks, tag placement, sub-tag
  counts, the 53 tags plus 8 untagged operations, and the 213 and 512 split.
- **Purposes:** 609 cells are the field's own schema description; the 8
  hand-written ones follow from argument types.
- **The RBAC catalogue in 03 section 9:** all 150 actions, docs labels and
  fallbacks, the footnote folding `UPDATE_BLUEPRINT_DEPLOYMENT*` into
  `UPGRADE_BLUEPRINT_DEPLOYMENT`, and the 18 disabled notes.
- **Tier gates:** 18 of the 19 docs pages state the plan the report gives.
- **01's authentication and error behaviour**, against source at the pinned
  commits: the `apiKeyUser` and `oauthUser` exchanges, OIDC key matching and
  its Enterprise gate, spacectl's GitHub OIDC minting, the unverified use of
  `aud` as the endpoint, the 30-second refresh padding, the credential order
  disagreement, seat billing, `/apikeytoken` and `.terraformrc`, the
  provider's error joining, the `unauthorized` to `viewer` fallback, the
  raw-HTTP 401 mapping, and the open status-code and `extensions` items.
- **01's counts and pins:** the `llms.txt` counts, 702 pages, 99 and 53 Flows
  pages, self-hosted v7.1.0 with 49 entries; the organization's 136, 93 and
  43 repositories and the licence split; the `spacelift-io/graphql` fork
  versions; 673 Bruno requests in 91 folders; 725 operations, 53 tags and
  665, 622 and 43 root fields; the policy sampling limits, the 500-run VCS
  limit, the provider's opt-in rate limiter and the weekly schedule.
- **Docs URLs:** all 295 in 01, 03 and 04 resolve.
- **05:** every resource and data source, all REST routes and field counts.
- **06:** the 108 command paths, anchors, version gates and flag sets; the
  operations of the 88 acting commands apart from the rows fixed; profiles,
  transport and browser login; auto-confirm and log paging; local preview,
  state download, provider publishing and drain polling; the 37 MCP tools;
  and the Pulumi, Helm, `WorkerPool` CRD, `setup-spacectl` and Backstage
  facts.

## RES-03: stacks, runs, policies, workers, configuration and state

### Claims checked

459 claims, by report: 07 about 110 (63 by script: 38 `StackInput` and 25
vendor configuration fields); 08 all 43 transitions plus 32 other claims,
with the diagram checked against the table and every `RunState`'s edges in
and out; 09 84, with the `PolicyType` enum, every policy root field, the
contract's inputs and rules for nine types and the 73 library policies; 10
74, with the worker schema, 18 worker operations, 33 CRD keys and 53 EC2
module variables; 13 62, with the `TF_VAR_spacelift_*` list against the
environment page; 16 54. The checker opened 101 GitHub line links and
fetched the policy contract (113,924 bytes). The copy check found three
12-word runs, in 07, 08 and 10, all rewritten.

### Findings

The pass's findings are the `RES-03.` records of the register
[review-findings.json](../../data/review-findings.json).

### What survived

- **08:** all 22 `RunState` and 7 `RunType` values and their descriptions;
  the docs' terminal and passive flags; the 43 transitions, each edge's
  states and diagram ID matching the table, every reachable state with an
  edge in and out, terminal states leading only to the end, and `UNKNOWN`
  and `PENDING` justified as unreachable; the inferred edges T14, T30 and
  T32 with their basis; the 2-day, 7-day and 30-day timers; the seven
  priority levels; the human and policy locks; the 11 hook lists and their
  order; the run actions' RBAC; `Run`'s 57 fields; the metadata header.
- **09:** the 13 `PolicyType` values and aliases; the 2026-10-20
  deprecations and the `ACCESS` disable date; how rules combine per type;
  the evaluation points; the timeouts (45 s plan, 10 s push and
  notification, 5 s for the rest, 5 minutes combined, the 10-minute push
  ceiling, the 500 ms access budget); sampling limits and retention; flag
  reset; the contract's rules for nine types; the library's 73 policies, 67
  templates and 61 tests.
- **10:** the transports, their selectors, defaults and URLs, and
  `POLL_LONG_TIMEOUT`; the MQTT retirement on 2027-03-30 with a switch
  advised by 2027-01-01; the self-hosted MQTT topics, variables, ports and
  IoT rule; every worker field and enum; cycle and reset; the launcher
  variables and timeouts; the CRD defaults and 33 `spec.pod` keys; the
  autoscaler and its 10-minute stray rule; runner image contents; signed
  runs.
- **07:** all 38 `StackInput` fields and the 7 vendor configurations; the
  `StackVendor`, `VCSProvider` and `StackState` enums; `Stack`'s 121 fields
  and the runtime configuration types; every cited provider default and
  both `ForceNew` attributes; the mutation signatures and routes; the
  `.spacelift/config.yml` keys, magic and auto-attach labels, and the lock
  rules.
- **13:** context priority (lower wins, provider default 0, `ForceNew`,
  explicit above auto-attached, auto-attached alphabetical); the precedence
  order once the version note moved; `ro_` and `wo_` by stage; the complete
  `TF_VAR_spacelift_*` table; the 2 MB limit and masking; the OIDC token.
- **16:** `manageState` fixed at creation; external access off by default
  and read-only; the URL types, `ManagedStateVersion`, import and rollback;
  the 100 MB cap and 30-day retention; the self-hosted buckets.
- **Cross-report:** no contradiction among 07, 08, 09, 13 and 16; 10's guess
  about `ro_` and `wo_` was the only one, fixed as 10-3. The pass noted that
  24's MQTT dates leaned on the v7.1.0 notes, which RES-06 then fixed.
- **Acceptance:** every report passes the shape check; 08 covers every run
  state and type except `PARSE`, which no source documents (C33).

## RES-04: VCS, cloud integrations, identity and events

### Claims checked

About 400 claims by hand (11 about 60, 12 about 50, 14 about 180, 19 about
110) and about 500 table rows by script. Two independent sub-checkers took
14 and 19; the checker spot-checked the API key `ForceNew` source, the new
section 6.4 of 14 and the exporter's metric count. Scripts re-derived every
field table and mutation signature in 11 and 12, the Terraform tables, all
REST paths in 12, 14's 55 tables (308 rows, 116 root fields) and the
150-value `Action` catalogue against 147 docs rows, and 19's 61 audit
resource types, 59 metadata types and 43 routes. They checked 117 GitHub
links, 106 docs URLs, the exporter's 23 metric descriptors and the
`NOTIFICATION` contract entry (254 paths, 9 differences from the docs). The
copy check found only identifier lists and one example `sub` value.

### Findings

The pass's findings are the `RES-04.` records of the register
[review-findings.json](../../data/review-findings.json).

### What survived

- **Schema tables:** every enum and field table in the four reports, about
  500 rows, with no invented values: `VCSProvider` (11) and
  `GitHubAppStatus` (9), every VCS integration object, `AwsIntegration`,
  `AzureIntegration` and their attachment views, the 150-value `Action`
  catalogue with 147 docs fallbacks, the 55 tables in 14, and
  `Notification`, `NamedWebhooksIntegration`, `AuditTrailEntry` and the 61
  audit resource types.
- **Mutation signatures:** every one in 11 and 12.
- **11:** the per-provider webhook events and their effects, token scopes,
  GitLab's one-hook rule, the mandatory Bitbucket Data Center secret, Azure
  DevOps basic authentication and the iteration-ID rule; the push policy's
  deduplication key, comment bypass, 10-minute budget, `head_owner`,
  `approved` and `mergeable` per provider, ignored runs and the 500-run cap;
  the vcs-agent's token fields, stream metadata, TLS 1.2, 25-second timeout,
  parallelism of 4, 5-second respawn, codeload rewrite, blocklist schema and
  allowlist call counts.
- **12:** all 14 OIDC claims, the scope table, the default subject, the 7
  placeholders, the template limits (1000 and 2048 characters and the
  character set), GCP's 127-character limit, matching per relying party, the
  self-hosted issuer and JWKS overrides; the AWS session durations, the
  docs' one-hour default against Terraform's 900, the external ID formats,
  session tags and auto-attach labels; Azure secret rotation about every 24
  hours, admin consent and Graph permissions.
- **14:** the spaces rules, propagation and Read-only inheritance, the
  permission matrix and stack bindings; login policy rules and
  `deny_message` aggregation; the SAML and OIDC settings, SCIM, MFA and
  backup credentials; API key types and exchange, the user status
  lifecycle, sessions, and 22 changelog dates.
- **19:** notification rules and the Slack and Teams behaviour; webhook
  retries (three on 5xx), auto-disable after 10 failures, the `sha256=`
  format and FedRAMP's SHA-256 only; 30-day audit retention and the
  Enterprise gate; the Datadog module (4 metrics and 12 tags against the
  docs' 3 and 9); every exporter flag and metric, usage queries and CSV
  export.
- **Cross-report:** the overlaps of 08 and 09, 10 and 24, and 03 and 04 with
  these reports agree.

## RES-05: registry, templates, scheduling, vendors, resources and integrations

### Claims checked

About 500 claims by hand (15 about 75, 18 about 60, 17 about 70, 21 about
60, 20 about 140, 25 about 95) and every schema table row by script. The
checker took 15 and 18 and ran the cross-report scripts over all six
reports; two independent sub-checkers took 17 with 21 and 20 with 25, and
the checker confirmed two of their corrections at source (the
`runResourceState` poll in `wait.go` and the v2.6.0 ServiceNow heading).
Scripts checked the report shape, 146 docs URLs, 135 GitHub links (five
`spacelift-operator` links verified over the network), 35 schema tables in
15, report 18's types, enums, 27 mutations and 27 REST paths, the 70
`SearchInput` and 57 suggestions fields, the blueprint and template body
schemas, the vendor enums and inputs, the 13-plugin catalogue and the
operator CRDs. A live, read-only re-capture covered the three discovery
documents, module versions and downloads, the error paths, provider
versions and downloads, the token and login endpoints and the registry
hosts, compared with HashiCorp's protocol pages. The copy check found seven
prose runs (four in 20, three in 25) and left only a kubectl command and
liftoff commands.

### Findings

The pass's findings are the `RES-05.` records of the register
[review-findings.json](../../data/review-findings.json).

### What survived

- **15:** the discovery documents on all three hosts, key for key, including
  the relative `authz` on `app.*`, the static S3 and CloudFront object with
  `Last-Modified` 2024-09-09, and `no-cache, no-store` on `app.*`; the `204`
  with an `X-Terraform-Get` signed CloudFront URL; the `404`s for the
  latest-download, listing and search paths; provider endpoints answering
  `401 unauthorized` without credentials, even for an unknown type; the
  token endpoint (`405` on GET) and the registry hosts (`403` at the root);
  the inferred provider response fields, which match HashiCorp's protocol,
  including the upper-case `key_id` spacectl produces; every schema table,
  the 41 operations, the 9 changelog dates, the RBAC actions and fallbacks,
  tiers, buckets, naming rules, test cases, version rules, the token format,
  the Dependabot and Renovate settings, and the GPG and provider version
  lifecycle.
- **18:** every schema type, input, enum, mutation and REST path, with input
  deprecations sourced from the changelog; the tier and worker gates; the
  scheduled-delete flow, drift limits and reconciliation (not for
  CloudFormation); the dependency graph, deletion blocking, retry and
  priority rules and the output reference rules (128 kB, uploads from
  2023-08-21, sensitive outputs); `ForceApplyMode` and spacectl
  `--force-apply`; external dependency statuses; the trigger evaluation
  states and 8 changelog dates; the upstream-failure contradiction, left
  open as in 08 (C38).
- **17, 20, 21 and 25:** the deprecation and removal dates (2026-07-16,
  2026-10-20, 2026-02-18), the template body schemas, the entity change
  enums, scans and saved filters, the vendor enums and phase commands, the
  `workflow.yml` key sets, version management, the plugin catalogue, and the
  Backstage and Teleport facts.
- **Cross-report:** 17 against 25 (the ServiceNow version) and 21 against 05
  and 08 (`runResourceState`) are fixed; 18 agrees with 08 and 10.

## RES-06: Intelligence, Flows and self-hosted

### Claims checked

About 255 claims by hand: 22 about 75 (models and providers, the Bedrock
policy, validation rules, the AI and Intent terms, the token budget, 8
self-hosted version labels and about 25 `spacelift-intent` source facts);
23 about 60 (SDK citations, event limits, invocation statuses, retention,
capabilities, live OAuth scopes, provider facts, Helm values, the Flows EKS
module and registry counts); 24 about 120 (every bucket, queue and setting,
the IoT rule, KMS keys, ECS services and commands, Helm deployments,
PostgreSQL versions, MQTT dates, release-note labels, the migration kit and
the usage exporter). Scripts checked 987 GitHub links with 646 name checks,
re-derived 261 module resources, the backend environment variables per
flavour, the 135 Helm values with their schema types, and the 18
`spacelift-intent` tools. The copy check found no prose runs against the
docs; a 6-word check against the unlicensed Flows repositories and the
`@slflows/sdk` typings found none.

### Findings

The pass's findings are the `RES-06.` records of the register
[review-findings.json](../../data/review-findings.json).

### What survived

- **22:** the Spacelift-provided models (Claude Sonnet 4.6 and Claude Opus
  4.8 on Bedrock), the four bring-your-own-model providers, `https` base
  URLs, the Bedrock IAM policy and failure note, and every data-handling row
  against the AI and Intent terms; self-hosted Flows needing a
  bring-your-own model, Intelligence on self-hosted from v6.0.0, run
  summaries from v7.0.0, module deployment in v6.2.0, the Infra Assistant
  page and OAuth allowlist in v6.4.1, the OAuth fixes in v6.1.0 and v6.1.1,
  and the 2026-04-13 Bedrock changelog conflict; the whole
  `spacelift-intent` section at `03a4865d888b` (18 tools with their hints
  and inputs, go-sdk v1.4.1, stdio, SQLite and migrations, flags and
  defaults, registry endpoints, the unchecked `shasum`, plugin protocol 5.9
  and Terraform version `1.6.0`).
- **23:** the Flows model, event limits, invocation statuses, retention,
  capabilities, live OAuth scopes, the provider facts, the Helm values, the
  Flows EKS module and the registry counts; the unlicensed repositories and
  the SDK are cited by file and line and never quoted.
- **24:** the bucket inventory (AWS 12, GCP 11, Azure 11 containers) with
  names and expiries, the Azure lifecycle rules, the 8 queues and their
  settings, the IoT rule, the ECS services, tasks and commands, the Helm
  deployments in both chart copies, the PostgreSQL facts, the v6.4.1 and
  v7.0.0 claims, ECS default sizes, the migration kit and the usage
  exporter; the acceptance check that names every component and bucket with
  a file and line at a pinned commit, now including the KMS keys, ECR
  repositories and conditional resources.
- **Cross-report:** 10 and 24 now agree on 2027-03-30 and on the v7.1.0
  notes naming no removal release; 01 agrees on the 2027-01-01 advice.

## RES-07: scope and tiers, reference materials and the data model

### Claims checked

About 175 claims by hand: 26 about 95 (every pricing-page cell against
the page's HTML, every tier notice against its docs page, the self-hosted
and FedRAMP tables, the changelog evidence of section 7, and the provider
source behind every superseded or annotated Terraform row); 27 about 55 (the
licence file of each project and sub-directory, the Digger `ee/` terms, and
cited lines in all ten projects); 28 about 25 (identity rules, evidence rows
and self-relations). Scripts re-derived every row of 26 (53 tags in
reference order, 56 resources, 62 data sources and 31 `BillingTierFeature`
values, each exactly once, with op counts, tier-feature columns and states
against the 72 served operations) and its 43 excluded root fields against
`coverage.md`, the schema and the changelog. They checked the 224 GitHub
links of 27 against the clones at the commits read, every release tag and
date through the GitHub API, and the 96 typed field claims and 47
child-to-parent edges of 28 against the schema. The pricing page's
comparison table was read from its raw HTML, where the tick icons give each
plan's column. The copy check found one 12-word hit in 26, a schema
deprecation reason quoted as [G]; a 10-word check of 27 against the MPL-2.0
clones and Digger `ee/` found none.

### Findings

The pass's findings are the `RES-07.` records of the register
[review-findings.json](../../data/review-findings.json).

### What survived

- **26:** every inventory row and count, every state under the stated rule
  except the data sources the operation map leaves empty, every [C] fact in
  2.1 against the pricing HTML, every docs tier notice cited, the 18
  `BillingTier` values, the FedRAMP table, all 43 excluded root fields with
  their reasons, dates and replacements, and the dated evidence of 7.2 to
  7.8.
- **27:** every licence against its LICENSE file at the commit read; every
  GitHub link inside its file; the sampled architecture claims in all ten
  projects; every other release tag and date; the only quote is three
  Apache-2.0 lines, and nothing from MPL-2.0 or Digger `ee/` code is quoted.
- **28:** all 96 typed field claims, the other child-to-parent edges, and the
  identity rules of section 1.2.

## RES-08: decision records 0005 to 0022

### Claims checked

About 765 claims by hand: 0005 to 0013 about 315 (every transition ID and
driver of 0008 against report 08, the timers and priority levels, report
24's database and encryption facts, the OIDC claims and subject template,
the policy types, timeouts and quotas, the state backend protocol against
the Terraform and OpenTofu `http` clients, River's features, the VCS
signature schemes and the masking rules); 0014 to 0022 about 360 (every
registry protocol answer against report 15 and HashiCorp's protocols, the
delivery contract against report 19 and its captures, the CI and coverage
facts against the repository, report 19's exporter metrics, report 23's
Flows limits, report 22's AI and Intent facts, report 26's supersession
rows, RFC 9457, RFC 7396 and Go's timezone loader); and about 90
cross-record pairs. Scripts resolved the 480 links and anchors, checked
about 40 Go API symbols at their latest module versions, and compared names
across all 18 records: every problem slug against 0022's table, about 300
configuration keys, job kinds and periodic tasks against 0011, NOTIFY
channels against 0005, sealed columns and stores against 0013, test and
feature-file names against 0016, and 0021's served and not-served table
against the records that own each candidate. The copy check found no
12-word run shared with the docs mirror.

### Findings

The pass's findings are the `RES-08.` records of the register
[review-findings.json](../../data/review-findings.json).

### What survived

- **0005 to 0013:** every transition ID and driver against report 08, the
  timers and tiers, report 24's database and encryption facts, the OIDC
  claims and token lifetimes, the space tree and action catalogue, the
  policy types, timeouts and quotas, the `http` backend's locking and
  status codes, River's claim order and defaults, every VCS signature
  scheme, and the masking mechanisms.
- **0014 to 0022:** every captured registry answer of report 15, report
  19's delivery and audit facts and exporter metrics, report 23's Flows
  limits, report 22's AI and Intent facts, every supersession row against
  report 26, and 0003's five slugs.
- **Consistency:** every problem slug is in 0022's table, every periodic
  task of 0005 to 0013 is in 0011's, every sealed column exists in its
  owner, every test function is `TestUnit` or `TestAcc`, and report 30's PD-01
  to PD-11 pointers name the records that answer them.
- **Rules:** status on line 3, the template's sections, no sequencing, no
  dashes, no contradiction of 0001 to 0003; 0022's amendment of 0003's
  "only list" remains a flagged proposal.

## ARC-01: architecture options and decision 0004

### Claims checked

About 450 claims by hand: option 5, the [comparison](../architecture-options/README.md)
and [decision 0004](../decisions/0004-repository-architecture.md) about 190
(every report link against its section, every decision cross-reference,
the process-topology evidence against reports 10, 23, 24 and 27, and the
deep dives against decisions 0005, 0008, 0009, 0010, 0011, 0012, 0013,
0015, 0016 and 0018); options 1 and 2 about 110; options 3 and 4 about 150.
Scripts compared every option's outline, parsed every depguard block,
recounted every matrix cell and total, and checked every package-mapping
row against `go list ./...`. Experiments in a scratch Go module settled the
Go claims: nested `internal/` visibility, a `vendor` path element, depguard
v2.2.1 under golangci-lint 2.15.0 (list modes, prefix matching, the
map-form `deny`), and embedding strict-server partials across nested
`internal/` directories. The copy check found no docs prose.

### Findings

The pass's findings are the `ARC-01.` records of the register
[review-findings.json](../../data/review-findings.json).

### What survived

- **Template:** the five options share one outline, which docscheck now
  asserts.
- **Matrix:** every criterion is rated for every option in all three
  tables and the totals recount to the cells. Option 5 leads the roadmap
  table and is Poor on nothing, so the recommendation rests on no corrected
  fact; the one rating change widens its lead.
- **Package mapping:** every package `go list` reports, plus `cmd/`,
  `api/src/deploy` and `api/reference/spacelift`, appears in every option.
- **Process topology evidence:** report 24 sections 2.1, 2.2 and 3.3,
  report 23 section 14.1, report 27 section 3.1 and report 10 section 4.2.
- **Numbers:** 72 stubs, about 24,000 generated lines, 725 reference
  operations, 22 run states and 43 transitions, and option 5's 6 depguard
  rules with about 75 entries.
- **depguard behaviour, rechecked before P1b:** under golangci-lint 2.15.0,
  which `.github/golangci-lint-version` pins and which embeds depguard
  v2.2.1, a scratch module shaped like option 5 confirms that `lax` mode
  lets a longer `allow` entry beat a shorter `deny`, that `strict` with
  `$gostd` refuses everything else, and that `!` globs exclude files. It
  also shows that entries are plain string prefixes: `allow:
  $m/internal/stacks/api` admits `$m/internal/stacks/apix`, a trailing `$`
  matches one package exactly and a trailing `/` only the packages below.
  Option 5, the repo layout, decision 0004 and P1b's fixtures now write
  allow entries with `$` or `/`.

## IMP-01: implementation plan and supporting documents

### Claims checked

About 400 claims by hand: every task of the 45 phases of the
[plan](../../implementation/plan.md) (202 tasks), traced package by package
and table by table to the phase that builds what it uses; every
**Depends on** line, exit criterion and milestone; the Build status rows
against the state of issues #5, #8 to #15 and #21 to #31 on GitHub; the
[repository layout](../../implementation/repo-layout.md) and the
[definition of done](../../implementation/definition-of-done.md) against
decisions [0004](../decisions/0004-repository-architecture.md),
[0016](../decisions/0016-testing-and-acceptance.md) and
[0020](../decisions/0020-reference-deployments-and-packaging.md), option 5
and today's `Makefile`, workflows, `.golangci.yml`, `.testcoverage.yml` and
`go list ./...`. Scripts rebuilt the phase graph (cycles, the longest
chain), checked every operationId the plan names against
`spacelift.openapi.yaml` and `deploy.openapi.yaml` (674 named, all exist),
gave every one of the 725 reference operations an owner in the plan or the
[capability roadmap](../../implementation/capability-roadmap.md), recomputed
each tag's owning phases against the exit criteria, and checked test and
feature names against 0016. A helper re-derived every roadmap row (53 tags
plus 8 untagged operations, 56 resources, 62 data sources, 31
`BillingTierFeature` values) against the reference artefacts and report
[26](26-scope-and-tiers.md), traced each row to its phase's tasks, and
checked every row of the [deviations register](../../implementation/deviations.md)
(DV-001 to DV-091) against the record it cites and every departure named in
decisions 0004 to 0022. The copy check found no docs prose.

### Findings

The pass's findings are the `IMP-01.` records of the register
[review-findings.json](../../data/review-findings.json).

### What survived

- **Graph:** the 45 phases form an acyclic graph, every **Depends on** names
  a real phase, and every phase carries its decisions, dependencies, tasks
  and exit criteria.
- **Operations:** all 674 operationIds the plan names exist; all 725
  reference operations have an owner, three groups through counted phrases
  (the five subscription mutations, the 20 User Guide operations and the
  six grant-view suggestions).
- **Counts:** 53 tags plus 8 untagged operations, 56 resources, 62 data
  sources and 31 `BillingTierFeature` values, each with exactly one roadmap
  row whose phase exists.
- **Build status:** P0 done (#5 closed); P1 in progress with #9, #10, #12
  and #13 closed and #11, #14 and #15 open, as GitHub shows.
- **Gaps in today's tree:** each has a task. P1b moves the packages,
  updates the `Makefile` and `.testcoverage.yml` paths and enables depguard
  and forbidigo; P1c adds the `-run '^TestUnit'` filter, the acceptance
  partitions and `generated` in the gate's `needs`; P24 adds
  `provider-parity`.
- **Deviations:** apart from the rows above, every DV row's departure is in
  the record it cites, and every departure in decisions 0004 to 0022 has a
  row.

## DES-05: Flows expressions in report 23

### Claims checked

About 40 claims by hand: every statement of report 23 sections 3.2 to 3.4
and section 13 about expressions, their variables and where they run,
against the 46 flow templates listed in `templates.json`, the
`scenario.yaml` files of flows-app-anthropic, flows-app-github,
flows-app-pagerduty, flows-app-postgresql, flows-app-slack and
flows-app-ssh at the commits of report 23 section 6.5, the block
management and private agents pages, the `agent-skills` app development
guide and the SDK typings. Scripts counted every `!expr` value (784 in the
templates, 237 in the scenarios), every `ref(` call (128, all in the
scenarios), the 19 Transform blocks and their configuration, the
templates whose static configuration reads `secrets`, and the TypeScript
assertions. The recount differs from the survey in four places, and the
report records the recount: 128 `ref(` calls, not 111; two `as string`
and two `as any` assertions, not five `as string`; the `Object(outputs)`
line of the secret rotation template is L129, not L128, which is a
comment; and three templates are served under longer names
(`certificate-expiry-monitoring`, `secret-rotation-orchestrator`,
`spacelift-unconfirmed-runs-slack`). The usage classes and language
feature counts are the survey's; their class counts sum to 785, which the
report states. Template text is restated, never quoted, since the docs
carry no licence.

### Findings

The pass's findings are the `DES-05.` records of the register
[review-findings.json](../../data/review-findings.json).

### What survived

- **23:** the rest of sections 3.2 to 3.4: `!expr` as the marker, untagged
  values as literals, `outputs`, `signals`, `secrets`, `stored`, `items`
  and `group` with their scopes, camelCase block keys and secondary
  parents carrying no data; the docs' statement that agents run
  expressions in Node.js runtimes, confirmed by the private agents page
  and the app development guide; the definition reference's core block
  list.

## DES-07: the plan against the accepted decisions

### Claims checked

About 250 claims by hand: every decision record from 0001 to 0024 read
against the others for shared names (process modes and their owner,
configuration keys, job kinds, channels, signing key purposes, problem
slugs, feature names, test and feature names, CI jobs, package paths and
API surfaces), and every phase, milestone, lane, layout row, gate row and
deviation row of the implementation documents read against the records
they cite. A sweep of decisions 0001 to 0024, the implementation documents
and the architecture options looked for the words the
[glossary](../../../GLOSSARY.md) lists to avoid, used for infraweave's own
concepts, and for Spacelift's names on infraweave's own surfaces;
Spacelift's names stay where the text describes Spacelift. Findings in the
architecture options went to the #54 rewrite, which edits those files, and
four of them came back as decision fixes (findings 26 to 29 and the
priority wording of finding 30). Scripts rebuilt the phase graph from the
**Depends on** lines (46 phases, acyclic, longest chain 24 edges), mapped
every API tag, untagged operation, Terraform resource and data source and
`BillingTierFeature` value of the
[capability roadmap](../../implementation/capability-roadmap.md) to a
feature of decision 0021 and its seeded tier, and checked every changed
file for em and en dashes. The copy check found no docs prose.

### Findings

The pass's findings are the `DES-07.` records of the register
[review-findings.json](../../data/review-findings.json).

### What survived

- **Phase graph:** after the change the 46 phases still form an acyclic
  graph; the critical path keeps 24 edges, with P24c in place of P26b, and
  the Flows chain (P12, P13, P18, P18c, P18d) ties with the vendor chain
  into P24.
- **Records:** the shared names not listed above agree across 0001 to 0024:
  problem slugs, job kinds, configuration keys, sealed columns, feature
  names and the CI partitions.
- **Coverage of the reference:** every tag, untagged operation, Terraform
  resource and data source and `BillingTierFeature` value has exactly one
  roadmap row, now with a feature and a seeded tier, and every phase a row
  names exists.
- **Left open:** see the plan's [open items](../../implementation/plan.md#open-items);
  the remaining cross-record questions (the authorization server, worker
  usage samples, database IAM authentication outside AWS, the owner of
  object deletion, registry sharing across accounts, the Flows
  "organization" name) wait on the maintainer, and none changes the order
  of the phases. DES-09 records their answers.

## DES-09: tenancy, names and the cross-record questions

### Claims checked

About 300 claims by hand: decisions 0001 to 0024 read against the
maintainer's answers to the DES-09 questions and against each other, for
the tenancy levels (root, organization, account, space), the place of the
tier, grants, limits, members, identity providers and keys, the account's
top space, account admins, module shares and Flows' organization, and for
infraweave names that said Spacelift or root. Every record that named an
owner for one of the six cross-record questions DES-07 left open was read
against the record that now decides it. A second reader, who had not made
the changes, read the 24 records for contradictions in tenancy, shared
names, Spacelift names and dashes. Scripts walked the served deploy bundle
for every name a client reads or sends and the reference specification
for every Spacelift and root identifier, which became the rows of 0001's
rename table, and checked every changed file for em and en dashes and the
spelling "organisation". `TestUnitServedNames` now holds the served bundle
to the table. The copy check found no docs prose.

### Findings

The pass's findings are the `DES-09.` records of the register
[review-findings.json](../../data/review-findings.json).

### What survived

- **The six DES-07 questions are answered:** the authorization server
  (0006, in the `access` domain), worker usage samples (0008's
  `worker.sample` and `worker_pool_samples`, read through the workers
  domain), database IAM authentication outside AWS (0005's `azure` and
  `gcp`), the owner of object deletion (`scheduler` alone, 0005 and 0020),
  registry sharing across accounts (within one organization, 0007 and
  0014), and the Flows "organization" name (the account, 0018 and 0001).
- **Records:** the shared names not listed above agree across 0001 to 0024
  after the fixes: problem slugs and their body members, job kinds and
  periodic tasks, configuration keys, tables, feature names, routes and
  path prefixes, and the domains of 0004.
- **The served bundle:** after `SPACELIFT_ENVIRONMENT` and `SPACELIFT`
  became `INFRAWEAVE_ENVIRONMENT` and `INFRAWEAVE`, no served name contains
  spacelift or matches a reference name of the rename table.
- **Settled after the pass:** the sampling task is `worker.sample` with `worker.samples.retention`, matching the singular prefix of `worker.offline_after` and `worker.reap` (`runs.` and `workers.` name tables, not keys); 0018 gives the default Flows agent pool the account isolation 0008 gives the public worker pool; the plan no longer counts C226 and C238 as blocking; served descriptions no longer name Spacelift, and `TestUnitServedNames` reads every served string outside the mapping extensions.
- **Settled after the pass, as style:** records name a store and write its key without the store prefix (0005), and write deploy routes as the document's paths while every other surface, the operator API's `/operator/v1` among them, keeps its prefix (0003). Settling them exposed three contradictions, now resolved: 0005 kept `deliveries` 1 day where 0015 keeps delivery bodies with their rows for 7; plan P1 served `GET /api/v1/health`, which 0017 rejects, and now serves `/livez` on the admin listener; and 0009 bound a sample to its key alone, where 0013 binds every object to its store and key.

## DES-10: ratification of the repository architecture

### Claims checked

About 120 claims by hand. Three reviewers read decisions 0001 to 0024, the
five options and the comparison, reports 08, 09, 10, 20, 23, 24, 26 and 28,
the plan and the repository layout, and artifactweave's options, tree and
refactor history as a comparison case. One argued the strongest case for
DDD, one stress-tested option 5, and one audited the one-way doors of
decisions 0004, 0005, 0006, 0007, 0008, 0011, 0013, 0015, 0018, 0019, 0021
and 0024. Each finding's citations were read against the files, the
strict-server composition was compiled with Go 1.27 in a scratch module,
and the domain graph was rebuilt from every cross-domain `api` call the
option 5 file names. About 60 more claims came from reading the
[plan](../../implementation/plan.md) against the ratified layout: three
readers took a third of the phases each and checked every package path,
every placement and each domain's edges against the declared graph in
phase order, and each edge they found missing was checked for a cycle.

### Findings

The pass's findings are the `DES-10.` records of the register
[review-findings.json](../../data/review-findings.json). Amendments the
one-way-door audit proposed for decisions other than 0004 are folded by the
passes that make them.

### What survived

- **Option 5 holds.** DDD as drawn is worse; its strongest variant, with
  tactical patterns kept to the rule-heavy domains and nested `internal/`
  directories, converges on option 5's shape. Hexagonal keeps its shared
  port files and its single core. Option 5 rates Good on 16 of 24
  criteria, Poor on none, and still leads the roadmap table.
- **Neither workflow concept nor the three clouds separates options 4 and
  5:** Flows is a domain on the shared event model and queue in both, custom
  workflows are `iac` knowledge rendered by the runner, and AWS, Azure and
  Google Cloud are drivers in `platform` and adapters in `cloud`.
- **The maintainer's rulings:** option 5 is kept; every feature ships in
  one deployment from the first release, so no domain is built for
  separate deployment; foreign keys follow the import direction; `runs`
  stays one domain.
- **The `runs` transaction:** a run transition still commits the run, its
  transition, the stack run lock, the queue item, the policy receipts and
  the event in one transaction inside `runs`.

## What survived across the set

- **Mechanical inventories hold.** Every operation, route, field, enum
  value, Terraform resource and data source, spacectl command and flag, and
  generated table the passes re-derived by script matched its source once
  the fixes above were in: 725 operations, 150 actions, 56 resources and 62
  data sources, 108 commands, about 2,000 schema and module table rows.
- **Docs URLs and links hold.** Every docs.spacelift.io URL in the reports
  resolves to a mirrored page, and every GitHub link names a pinned commit,
  an existing file and lines that show the claim.
- **No prose is copied.** After the fixes no report shares a 12-word prose
  run with the docs mirror; the remaining hits are identifier lists,
  commands and one example value. Quotations from MIT sources without a
  `NOTICE` entry were removed (PD-13).
- **Most corrections are precision, not direction.** Of the 337 findings
  on the reports, 123 are *wrong*; most of those are line anchors, types, counts, defaults
  and edge states. The design-relevant ones are the vcs-agent's allowlist
  (11), the AWS session name (12), the approval `reject` outcome (08), the
  `ro_` and `wo_` stage rule (10 against 13), the OIDC SSO tier (03 and 04),
  `policy_receipts` placement (19), state management defaults for
  Terragrunt (16), the `kms_arn` behaviour (24), and the self-hosted
  feature set and public pool (26); each is now recorded
  with its sources and, where still open, as an entry in 30.
- **Cross-report contradictions are resolved:** 10 against 13 (`ro_` and
  `wo_`), 17 against 25 (the ServiceNow version), 21 against 05 and 08
  (`runResourceState`), 24 against 10 (the MQTT dates), 01 against 06
  (error status codes) and 26 against 01 (the self-hosted feature set).
- **Left open:** the 102 Purpose cells that are Bruno request names
  (C16), and whether self-hosted v7.1.0 ships run summaries, on which the
  self-hosted Intelligence page and changelog that reports 22 and 26 cite
  disagree (C270). The generator scripts named by 03, 04, 05, 10 and 22 to 24 are
  not committed (PD-12): the reports describe their method instead.
- **The decision records agree with each other** after RES-08: 19 of its
  58 findings were contradictions between records, and six corrected facts
  affect a proposed design choice, each flagged for the maintainer.

## What this means for infraweave

- The reports' corrected statements, not their first drafts, are the
  basis for design; where a finding left a question open, the plan works
  from the entry in 30, not from either source alone.
- Generated tables carry fact-check fixes made by hand, so regenerating them
  without folding those fixes in would reintroduce the errors (PD-12).

## Inferred and undocumented

This log adds no claims about Spacelift. Its counts of findings come from
the register, one record per finding row of the twelve reviews; where a
review's own summary gave other counts, the rows are what the register
holds.

## Versions at the research baseline

| Component | Version |
|---|---|
| Spacelift SaaS GraphQL schema | introspection `b3255fe38f5e`, fetched 2026-10-28 |
| docs.spacelift.io | read 2026-10-28; self-hosted pages at v7.1.0 |
| terraform-provider-spacelift | v1.57.0 (`c72b9bd2f3c3`) |
| spacectl | `6bf167cb62a6` |
| Policy contract | fetched 2026-10-28, 113,924 bytes |

## Sources

- The fact-check reviews of RES-02, RES-03, RES-04, RES-05, RES-06,
  RES-07, RES-08, ARC-01 and IMP-01, and the drafters' notes for RES-02 to RES-07, written
  2026-10-28.
- The DES-05 correction pass, 2026-11-03, with the 46 Flows templates at
  `https://docs.spacelift.io/flows/templates/<name>.yaml` and the MIT
  `scenario.yaml` files listed in [23 section 3.7](23-flows.md#37-expressions-in-the-published-templates).
- The DES-07 cross-read, 2026-11-03, of decisions [0001](../decisions/0001-rest-first-spacelift-surface.md) to [0024](../decisions/0024-expression-language.md) and the implementation documents, with the [glossary](../../../GLOSSARY.md).
- Reports [01](01-spacelift-landscape.md) and [03](03-deploy-core-api-inventory.md) to [28](28-data-model.md) in this directory, as edited by those
  reviews, decisions [0005](../decisions/0005-persistence-postgresql.md) to [0022](../decisions/0022-api-errors-and-update-semantics.md), as edited by RES-08, and the [architecture options](../architecture-options/README.md) and decision [0004](../decisions/0004-repository-architecture.md), as edited by ARC-01.
- The [implementation plan](../../implementation/plan.md), [capability roadmap](../../implementation/capability-roadmap.md), [deviations register](../../implementation/deviations.md), [repository layout](../../implementation/repo-layout.md) and [definition of done](../../implementation/definition-of-done.md), as edited by IMP-01; GitHub issues #5, #8 to #15 and #21 to #31, read 2026-10-28.
- <https://spacelift.io/pricing>, read 2026-10-28 as raw HTML for RES-07.
- The DES-09 pass, 2026-11-03, of decisions [0001](../decisions/0001-rest-first-spacelift-surface.md) to [0024](../decisions/0024-expression-language.md) against the maintainer's answers to the DES-09 questions, with `api/deploy.openapi.yaml` and the reference specification.
- The DES-10 pass, 2026-11-04, of decision [0004](../decisions/0004-repository-architecture.md) and the [architecture options](../architecture-options/README.md) against decisions 0001 to 0024, with artifactweave's `docs/research/architecture-options/` and its `internal/` tree read the same day.
- The open questions the reviews raised: [30](30-open-questions.md).
