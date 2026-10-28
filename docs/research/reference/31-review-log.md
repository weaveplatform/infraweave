# 31: Review log

Checked against: the RES-02 to RES-07 fact-check reviews and reports 01 and 03 to 28 as they stand after those reviews' fixes, 2026-10-28; introspection `b3255fe38f5e`; docs.spacelift.io read 2026-10-28.

Research baseline: **2026-10-28**.

This log records what the independent fact-checks of the reference reports
found and what each finding changed, so that a reader can tell which
statements in reports 01 and 03 to 28 are corrections rather than first
drafts. It covers six passes, one per research ticket from RES-02 to
RES-07, and keeps every finding, shortened. Report 02 predates these
tickets and had no pass. The open items the passes raised are entries in
[30](30-open-questions.md), cited here by number; the terms are in the
[glossary](glossary.md).

| Label | Means |
|---|---|
| *wrong* | The report stated something a primary source contradicts |
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

## Totals

| Pass | Reports | Claims checked by hand | wrong | unsupported | missing | copied | style | Findings |
|---|---|---|---|---|---|---|---|---|
| RES-02 | [01](01-spacelift-landscape.md), [03](03-deploy-core-api-inventory.md), [04](04-platform-api-inventory.md), [05](05-terraform-provider.md), [06](06-spacectl-and-clients.md) | about 320, plus every table row by script | 34 | 13 | 20 | 1 | 5 | 73 |
| RES-03 | [07](07-stack-model.md), [08](08-run-lifecycle.md), [09](09-policies.md), [10](10-workers-and-execution.md), [13](13-configuration.md), [16](16-state-management.md) | 459 | 27 | 24 | 29 | 3 | 4 | 87 |
| RES-04 | [11](11-vcs-integrations.md), [12](12-cloud-integrations-and-oidc.md), [14](14-spaces-rbac-and-identity.md), [19](19-events-notifications-audit.md) | about 400, plus about 500 table rows by script | 14 | 19 | 18 | 0 | 7 | 58 |
| RES-05 | [15](15-module-and-provider-registry.md), [17](17-blueprints-and-templates.md), [18](18-scheduling-drift-dependencies.md), [20](20-vendors.md), [21](21-resources-scans-search.md), [25](25-other-integrations.md) | about 500, plus every schema table row by script | 21 | 16 | 25 | 2 | 2 | 66 |
| RES-06 | [22](22-intelligence.md), [23](23-flows.md), [24](24-self-hosted-reference.md) | about 255, plus every link and generated row by script | 8 | 2 | 10 | 1 | 1 | 22 |
| RES-07 | [26](26-scope-and-tiers.md), [27](27-reference-materials.md), [28](28-data-model.md) | about 175, plus every table row, link and typed field by script | 18 | 4 | 2 | 0 | 0 | 24 |
| **All** | 27 reports | about 2,105 | **122** | **78** | **104** | **7** | **19** | **330** |

The counts are of the findings tables' rows. Two reviews give summary
counts that differ from their own rows. In RES-02, the 05 summary gives 15
*wrong* where the rows give 12, and the 06 summary gives 15 *wrong*, 5
*unsupported* and 6 *missing* where the rows give 14, 4 and 7. In RES-06,
the summary gives 10 *wrong* and 8 *missing* where the rows give 8 and 10.
The tables below follow the rows.

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

### Findings by severity

| Report | wrong | unsupported | missing | copied | style | Total |
|---|---|---|---|---|---|---|
| [01](01-spacelift-landscape.md) | 5 | 4 | 5 | 0 | 1 | 15 |
| [03](03-deploy-core-api-inventory.md) | 1 | 1 | 1 | 1 | 1 | 5 |
| [03](03-deploy-core-api-inventory.md) and [04](04-platform-api-inventory.md) | 2 | 1 | 1 | 0 | 0 | 4 |
| [04](04-platform-api-inventory.md) | 0 | 0 | 1 | 0 | 0 | 1 |
| [05](05-terraform-provider.md) | 12 | 3 | 5 | 0 | 1 | 21 |
| [06](06-spacectl-and-clients.md) | 14 | 4 | 7 | 0 | 2 | 27 |
| **All** | **34** | **13** | **20** | **1** | **5** | **73** |

### Findings

| # | Report | Finding | Change |
|---|---|---|---|
| 1 | 01 | *wrong*: section 5 promised a repository table with a "Cloned at" column but held a copy of 04's Repos API tables (12 operation rows), so the repository inventory was absent | Replaced with a 93-row table generated from the GitHub API listing: licence, archived status, pin and purpose (the GitHub description, or a README summary) |
| 2 | 01 | *wrong*: schema counts included the introspection meta-types (576 objects, 92 enums) | Now 570 objects, `Query` and `Mutation` included, and 90 enums, with the exclusion stated |
| 3 | 01 | *unsupported*: the claim that the web UI, spacectl and the Terraform and Pulumi providers all use the one GraphQL endpoint cited a page that names only the provider and spacectl | Restated: the docs cover the endpoint and that subset; the Pulumi provider bridges terraform-provider-spacelift v1.9.3 (`provider/go.mod`); the web UI is inferred |
| 4 | 01 | *wrong*: said two Bruno examples are dropped; `coverage.md` lists eight, six more being VCS and blob storage integration updates | Corrected, with the second cause |
| 5 | 01 | *wrong*: the browser-login link `browserauth.go#L206-L250` ran past the file's 248 lines and did not show the `/cli_login?key=&port=` URL | Linked L113-L120 for the URL and L206-L246 for the token handling |
| 6 | 01 | *missing*: the in-run token row lacked the documented limits: three hours, only in `PLANNING`, `APPLYING` and `PERFORMING`, and no access to state, runs, tasks or logs | Added from the docs |
| 7 | 01 | *missing*: Spacelift tokens last about 10 hours, API keys may carry an expiry, and `validUntil` is a Unix timestamp | Added from the docs and schema |
| 8 | 01 | *missing*: the self-hosted removals lacked the end of CloudFormation installs after v7 (v8.0.0 ships through a container registry) and the 2027-01-01 MQTT switch date | Added |
| 9 | 01 | *unsupported*: said OIDC federation replaces the Azure and GCP integrations on self-hosted; the page only recommends it | Reworded |
| 10 | 01 | *missing*: did not say that `spacelift-intent` and `spacelift-operator` are archived | Added, with an archived column in the table |
| 11 | 01 | *unsupported*: the unauthenticated demo introspection endpoint had no source | Cited the API page's GraphDoc link and `internal/specref/fetch.go`; that the demo runs current SaaS is inferred |
| 12 | 01 | *style*: said the changelog runs back to v0.0.7; its oldest entry is the change from v0.0.7 to v0.0.8 | Reworded |
| 13 | 01 | *wrong*: "the 40 cloned" repositories; 50 clones exist, and report 23 pins the 10 `flows-app-*` clones, not `repos.tsv` | Reworded |
| 14 | 03, 04 | *unsupported*: 131 RBAC cells (76 in 03, 55 in 04) paired operations with actions under schema and docs markers, which reads as documented; the pairing is a name inference | Every cell marks the pairing as inferred by name and the fallback as schema and docs; 03 section 1.2 says what the inference covers |
| 15 | 03 | *wrong*: the `stackScheduledDeleteUpdate` cell cited the docs for `STACK_SCHEDULED_DELETE_UPDATE`, which the docs tables omit | Now cites the schema and notes the omission |
| 16 | 03 | *missing*: section 1.2 named only the context, policy and cloud actions as disabled; the schema disables 18, `STACK_ROLE_BINDING_*` and `STACK_WEBHOOK_*` included | All 18 listed |
| 17 | 03 | *unsupported*: section 1.3 implied that a docs page's plan gate applies to every operation under the tag | States that the plan belongs to the product feature and the operation's tag is inferred |
| 18 | 03, 04 | *wrong*: `CUSTOM_SSO_SAML` (Enterprise) was attached to `oidcCreate`, `oidcUpdate`, `oidcDelete` and `oidcSettings`, and 04 section 2.7 put OIDC SSO on Enterprise; the SSO page asks only for a paid account, as report 14 says | Fixed the 03 section 10 row, four rows in 04 and the 2.7 prose; the gating feature is C14 |
| 19 | 03 | *copied*: the `QUEUED` `RunState` and `low` `RunPriorityPreset` descriptions shared 12 or more words with the docs, because the schema text repeats the docs | Paraphrased, with a note |
| 20 | 03, 04 | *wrong*: six Purpose cells only restated the field name | Rewritten from argument and return types |
| 21 | 04 | *missing*: the `oauthUser` row gave only Bruno's note that the browser flow cannot complete with an API key, while the docs and spacectl use it to exchange a GitHub token | Added the docs fact, noting that the docs win |
| 22 | 03, 04 | *missing*: 102 Purpose cells are Bruno request names (all Intent rows, role bindings, templates and managed users): correct but saying little | Not changed: no local source describes most of them; recorded as C16 |
| 23 | 03 | *style*: section 1.1 credits the tables to generators in the research scratchpad, which is not committed | Reworded: reports describe their method and name no uncommitted scripts (P12) |
| 24 | 01 | *unsupported*: that errors arrive at HTTP 200 was inferred from clients matching message text, but spacectl matches the text of non-200 bodies too | Now open (C2); the inferred list updated |
| 25 | 01 | *missing*: did not say that the Pulumi provider is deprecated and goes read-only on 2026-12-10 | Added from its README |
| 05-1 | 05 | *wrong*: six line anchors ran one line past the end of the file | Ranges end at the last line |
| 05-2 | 05 | *wrong*: four anchors missed the cited code (`resource_user.go`, `resource_idp_group_mapping.go`, `resource_repo_file.go`, `resource_role_attachment.go`) | Re-anchored |
| 05-3 | 05 | *wrong*: the `runTrigger` argument is `commitSha`, not `sha` | Corrected |
| 05-4 | 05 | *wrong*: `spacelift_role_actions` queries `__schema` and keeps the `Action` enum; it does not use a nested `__type` | Rewritten, citing `internal/introspection.go` |
| 05-5 | 05 | *wrong*: `spacelift_scheduled_delete_stack` has only `at` (required) and `delete_resources` (default true), with no `every` or `timezone` | Bullet split |
| 05-6 | 05 | *missing*: `spacelift_webhook` import also accepts `module/$MODULE_ID/$WEBHOOK_ID` | Added |
| 05-7 | 05 | *missing*: five VCS integration data sources take an optional `id` | Added |
| 05-8 | 05 | *missing*: seven data sources showed no lookup keys or filter blocks | Added |
| 05-9 | 05 | *missing*: the deprecated `policy` blocks on `spacelift_idp_group_mapping` and `spacelift_user` | Two rows added |
| 05-10 | 05 | *unsupported*: the configuration element checksum was marked inferred, though the schema has `ConfigElement.checksum` | Now cites the schema; the hex encoding stays inferred |
| 05-11 | 05 | *unsupported*: that the token carries its API base URL was marked inferred; the docs state it | Now cites the docs |
| 05-12 | 05 | *missing*: the in-run token's documented limits | Added |
| 05-13 | 05 | *unsupported*: the schema-loading claim cited the snapshot rather than `internal/specref/terraform.go` | Re-cited |
| 05-14 | 05 | *wrong*: the matcher's `_id` candidate order was wrong, and its input object order was missing | Corrected, citing `enrich.go` |
| 05-15 | 05 | *wrong*: AWS trust retries are five attempts with four 10-second sleeps | Reworded |
| 05-16 | 05 | *wrong*: the stack destructor's create is not a no-op: it reads the stack and drops itself if the stack is gone | Reworded |
| 05-17 | 05 | *wrong*: wait semantics: the resource polls until `finished` or `unconfirmed`, and the final state, lower-cased, must be in `continue_on_state` | Reworded |
| 05-18 | 05 | *wrong*: template version validation runs when the planned state is `PUBLISHED` and `template` changes | Reworded |
| 05-19 | 05 | *wrong*: the one-minute timeout applies to each HTTP attempt | Reworded |
| 05-20 | 05 | *wrong*: the `unauthorized` hint replaces the GraphQL error formatting rather than adding to it | Reworded |
| 05-21 | 05 | *style*: section 7 was in the future tense | Present tense |
| 06-1 | 06 | *wrong*: 29 of the 37 MCP "Defined at" anchors pointed into the previous tool's body | Repointed to each `register…Tool` function |
| 06-2 | 06 | *style*: three link labels disagreed with their URL lines | Fixed |
| 06-3 | 06 | *wrong*: the `spacelift-io/graphql` link used a tag-object SHA (404) and the wrong range | Commit `87ff6fa23f4f`, L103 to L135 |
| 06-4 | 06 | *wrong*: client headers were said to go on every request; the `Do` path and the key and OAuth exchanges do not send them | Qualified |
| 06-5 | 06 | *missing*: presigned uploads and downloads use Go's default client, without the TLS and proxy settings, and the 60-second timeout was unstated | Added |
| 06-6 | 06 | *missing*: an unknown instance type offers only `all` variants, and the schema calls `debugInfo` invalid on SaaS | Added (C20) |
| 06-7 | 06 | *unsupported*: the API-token expiry behaviour was imprecise | Rewritten from source |
| 06-8 | 06 | *missing*: profile file permissions (`0700`, `0600`); the reserved-alias link had no anchor | Added |
| 06-9 | 06 | *missing*: the browser-login AES details (IV, PKCS#7, base64url) | Added |
| 06-10 | 06 | *wrong*: the `unauthorized` check matches any error text, non-200 bodies included | Section 2.4 rewritten; it also changed 01 (finding 24) |
| 06-11 | 06 | *wrong*: `runStack` returns a GraphQL `not found` error, not a null entity | Rewritten with exact lines (C5) |
| 06-12 | 06 | *unsupported*: "no published rate limits" carried no marker, and "no retry" is wrong because proxy authentication retries once | Inferred marker added; the retry stated |
| 06-13 | 06 | *wrong*: the stack prompt and `SPACECTL_SKIP_STACK_PROMPT` semantics, the viewer check and the `local-preview` paging limit | Section 2.5 fixed |
| 06-14 | 06 | *wrong*: `run-external-dependency --status` also accepts `skipped` | Fixed |
| 06-15 | 06 | *missing*: `workerpool watch` also sends `publicWorkerPool` or `workerPool.searchSchedulableRuns` | Added; operations from 73 to 74 |
| 06-16 | 06 | *wrong*: `mcp server` was marked as running the viewer check | Mark removed |
| 06-17 | 06 | *wrong*: `RegisterPlatformV2` is used from 3.0.0, not only on `latest` | Fixed in four places |
| 06-18 | 06 | *wrong*: section 4's "used by" entries for `runConfirm`, `stack`, `runStack` and `viewer` | Fixed |
| 06-19 | 06 | *wrong*: the MCP trigger, confirm and discard tools each send only their own mutation | Fixed |
| 06-20 | 06 | *wrong*: logs poll only with `--tail`; otherwise they make one pass | Section 5.1 fixed |
| 06-21 | 06 | *style*: "Self-Hosted 2.5.0+ variant" wording for the upload headers | Reworded |
| 06-22 | 06 | *missing*: the Pulumi provider is deprecated and goes read-only on 2026-12-10 | Added, also in 01 |
| 06-23 | 06 | *unsupported*: the drafter's `pulumi_cov.py` was cited as evidence | Replaced with a schema comparison |
| 06-24 | 06 | *wrong*: the operator's `Run` CRD listed `run` as a root field; the "archive date" was the last commit date; past tense | Fixed |
| 06-25 | 06 | *wrong*: `contextCreateV2` was called an old surface; the deprecated calls are `policyCreate` and `policyUpdate` | Fixed from the schema |
| 06-26 | 06 | *unsupported*: Backstage "latest release 0.3.3" was cited to a `<VERSION>` placeholder | Re-sourced to the GitHub releases API |
| 06-27 | 06 | *missing*: the method did not say that interface dispatch is not followed | Noted |

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

### Findings by severity

| Report | wrong | unsupported | missing | copied | style | Total |
|---|---|---|---|---|---|---|
| [07](07-stack-model.md) | 6 | 6 | 4 | 1 | 0 | 17 |
| [08](08-run-lifecycle.md) | 1 | 6 | 6 | 1 | 2 | 16 |
| [09](09-policies.md) | 5 | 4 | 5 | 0 | 2 | 16 |
| [10](10-workers-and-execution.md) | 7 | 4 | 4 | 1 | 0 | 16 |
| [13](13-configuration.md) | 3 | 3 | 4 | 0 | 0 | 10 |
| [16](16-state-management.md) | 5 | 1 | 6 | 0 | 0 | 12 |
| **All** | **27** | **24** | **29** | **3** | **4** | **87** |

### Findings

| # | Report | Finding | Change |
|---|---|---|---|
| 08-1 | 08 | *copied*: the section 4 row on order within a stack shared a 12-word run with the run concepts page | Rewritten |
| 08-2 | 08 | *wrong*: sections 1 and 6 said `spacelift_run` creates runs through `runResourceCreate`; at `c72b9bd2f3c3` the resource calls `runTrigger(runType:)` and nothing in the provider calls `runResourceCreate` | Both corrected with line links; `runResourceCreate` kept as a schema-only stub |
| 08-3 | 08 | *unsupported*: T5, T24 and T29 gave an approval `reject` as `FAILED` only, while the approval page's walkthrough says Reject cancels the run | Both statements recorded; the target is open (C40) |
| 08-4 | 08 | *unsupported*: T5 put drift runs on a drift-disabled pool on the `QUEUED` to `FAILED` edge; the drift page says such runs are created failed | Moved to the implied transitions; open (C43) |
| 08-5 | 08 | *unsupported*: T13 tied `skipInitialization` to the `INITIALIZING` to `PERFORMING` edge with no source showing that such a task enters `INITIALIZING` | Open (C42); anchor corrected to `task#skipping-initialization` |
| 08-6 | 08 | *style*: T16's run types ("all but `TASK` in `PERFORMING`") made no sense for an edge out of `INITIALIZING` | Reworded |
| 08-7 | 08 | *missing*: `DESTROYING` reached by `DESTROY` was marked inferred, though `StackState.DESTROYING` is schema evidence | Raised to the schema |
| 08-8 | 08 | *missing*: the `TESTING` creation route had no URL, and negative test cases were not covered | Link added; `negative: true` and `Run.expectFailure` recorded; the final state is open (C45) |
| 08-9 | 08 | *missing*: no failed-handover edge for `PREPARING_REPLAN`, though T11 and T36 cover the other preparing states | Added to the implied transitions; open (C44) |
| 08-10 | 08 | *unsupported*: "excess drift runs are skipped" left unclear whether that means the `SKIPPED` state | Clarified; open (C43) |
| 08-11 | 08 | *missing*: the trigger policy priority rules lacked the deferral rule and the failure on an invalid value | Added |
| 08-12 | 08 | *unsupported*: `runCancel` being valid from `QUEUED` and `READY` was given as schema fact; the schema says only that the run has not started | Marked inferred |
| 08-13 | 08 | *unsupported*: `runPromote` from a finished `PROPOSED` run had no source; the docs' prerequisites are a newer commit, promotion enabled and the permission | Replaced with the documented prerequisites |
| 08-14 | 08 | *missing*: the spacectl command list lacked `priority`, `prioritize` and `deprioritize` | Added |
| 08-15 | 08 | *missing*: terminal states were justified only by a table column | A paragraph cites the docs for four states, the table for `CANCELED` and `SKIPPED`, and `RunStateTransition.terminal` |
| 08-16 | 08 | *style*: `PENDING` being unused was attributed to the schema without saying it is the deprecation reason | Cited with the changelog entry of 2026-10-20 as well |
| 07-1 | 07 | *wrong*: `StackInput.terraformVersion` was mapped to `terraform_version`; the provider never sends it, and `terraform_version` writes `vendorConfig.terraform.version` | Attribute set to none with the source line; added to the typing traps |
| 07-2 | 07 | *copied*: a hook bullet shared a 12-word run with the stack settings page | Rewritten |
| 07-3 | 07 | *wrong*: the Kubernetes `namespace` "may be empty"; the provider rejects an explicit `""` and sends `""` only when the attribute is omitted | Restated with `#L535` (C32) |
| 07-4 | 07 | *wrong*: sensitive outputs were said to need the worker-pool variable everywhere; the public pool enables them, and only private pools need `SPACELIFT_SENSITIVE_OUTPUT_UPLOAD_ENABLED=true` | Rewritten |
| 07-5 | 07 | *unsupported*: `#L48`, the start of `CustomizeDiff`, was cited for four different claims | Re-pointed to four specific lines |
| 07-6 | 07 | *unsupported*: `#L869`, `#L1072` and `#L1472` pointed at function starts, not the checks | Re-pointed; the import refusal text added |
| 07-7 | 07 | *unsupported*: the workflow-tool defaults were given as provider defaults; the attributes are optional and computed with no default | Now described as applied by the server, citing the attribute lines |
| 07-8 | 07 | *unsupported*: "the Pulumi version comes from the runner image" cited a vendor page that does not say it | Re-cited to stack settings; language SDK selection added |
| 07-9 | 07 | *wrong*: the `stackConfigAdd` and `stackConfigDelete` return types were merged and their argument types missing | `ConfigElement!` and `ConfigElement`, with `ID!` arguments |
| 07-10 | 07 | *unsupported*: the `autoattach:*` scope blended two statements (contexts: own space and children; policies: inheriting spaces) | Split, each cited |
| 07-11 | 07 | *wrong*: the stack list tabs omitted Failed and Finished and mislabelled the in-progress tab | All five tabs and the unlisted states recorded |
| 07-12 | 07 | *missing*: OpenTofu `concise` covers plan, apply and destroy in the docs but plan, apply and refresh in the schema; the conflict was unrecorded | Both recorded (C31) |
| 07-13 | 07 | *missing*: the RBAC levels of stack mutations | Added: Admin for create, update, delete and reslug; Writer for setting and syncing the tracked commit |
| 07-14 | 07 | *missing*: the docs' per-vendor destroy commands for a task run before deletion | Added to 5.3 |
| 07-15 | 07 | *missing*: a Kubernetes run fails when pruning leaves no YAML; the plugin label `*` attaches to every stack in the space | Added |
| 07-16 | 07 | *unsupported*: the Terragrunt `terragruntVersion` default was uncited | Terragrunt reference page cited |
| 07-17 | 07 | *wrong*: the Sources named an unused changelog entry (2026-08-18) and omitted three that the changelog markers rely on (2025-11-10, 2026-06-10, 2026-07-16) | Corrected |
| 09-1 | 09 | *wrong*: 4.2 said the approval sample shows "the first four" `runtime_config` fields without naming them; they are `before_init`, `project_root`, `runner_image` and `terraform_version` | Fields named |
| 09-2 | 09 | *missing*: the push input's `module` lacked the contract field `current_version` | Added (`0.0.0` before the first version) |
| 09-3 | 09 | *missing*: trigger `stacks[]` items were said to match `stack`; the contract adds `queued_runs` | Added (C55) |
| 09-4 | 09 | *missing*: push `commits[]` items carry their own `affected_files`, oldest first | Added |
| 09-5 | 09 | *unsupported*: that notification input holds exactly one event object is stated by neither the docs nor the contract | Reworded |
| 09-6 | 09 | *wrong*: said the provider rejects a login policy attachment; the provider has no such check, and the API refuses it | Reworded |
| 09-7 | 09 | *wrong*: the default push policy was said to propose only on other branches; it proposes for every affected push or pull request, tracked branch included | Fixed |
| 09-8 | 09 | *unsupported*: `policyDetach(id)` taking the attachment ID was marked as schema, but the argument is undescribed | Re-cited to the provider's `resource_policy_attachment.go#L162-L169` |
| 09-9 | 09 | *unsupported*: `createdAt` and `updatedAt` were given as Unix seconds; the schema says only Unix timestamp | Unit marked as unstated (C56) |
| 09-10 | 09 | *wrong*: `Policy.hasAttachedStacks` was said to count both kinds; it is a boolean | Reworded |
| 09-11 | 09 | *style*: the CI exclusion reason was marked inferred, but the workflow comment at L37 states it | Marker removed; link widened |
| 09-12 | 09 | *wrong*: `continuous-integration.yaml#L19-L51` missed the `opa test` step | Widened to L16-L58 |
| 09-13 | 09 | *missing*: the Ansible plan input `ansible.dark` (unreachable hosts), with its page uncited | Added with its source |
| 09-14 | 09 | *missing*: the migration table omitted that `input.run.creator_session` is new for initialization policies | Added |
| 09-15 | 09 | *unsupported*: `deny_admin` keeping non-admin access comes from the contract, not the login page | Contract cited |
| 09-16 | 09 | *style*: "was added on 2026-09-04" narrated history | Present tense |
| 10-1 | 10 | *wrong*: the EC2 lifecycle manager was said to undrain a busy worker; it keeps the worker drained and heartbeats the hook, and only the autoscaler undrains | Split; the real retry schedule added |
| 10-2 | 10 | *wrong*: `AUTOSCALING_SCALE_DOWN_DELAY_USE_IDLE_TIME` was called a declared flag; it switches the query to `availableAt`, and the default counts from `createdAt` | Rewritten |
| 10-3 | 10 | *wrong*: the meaning of `ro_` and `wo_` was called undocumented and guessed as a run-type split; the environment page defines it by stage, as 13 says | The documented rule cited; the whitelist and mount behaviour left open (C66) |
| 10-4 | 10 | *wrong*: the EC2 module has no `secure_env_vars` at `b83474a27024`; secrets go in `env_vars` with `sensitive = true` or in `secret_env_var_arns` | Fixed |
| 10-5 | 10 | *wrong*: the module was said to refuse a `configuration` that exports the token; `secure_strings.tf` L40-L46 only warns | Rewritten |
| 10-6 | 10 | *unsupported*: the order between the account default and launcher default runner images was marked inferred; the runtime configuration page states it | Raised to the docs |
| 10-7 | 10 | *unsupported*: the MQTT server URL coming from the token had no support in the docs | Cited to `broker.endpoint` in `saas.tftpl` |
| 10-8 | 10 | *missing*: self-hosted transport facts: long-poll from v6.4.0, MQTT deprecated in v7.1.0 with no removal release named, 2027-03-30 on the self-hosted page, changelog 2026-07-20 | Added (C70) |
| 10-9 | 10 | *missing*: the Kubernetes upgrade rule: a stored `commsProtocol` is kept, and with none stored the pool switches to `poll` | Added |
| 10-10 | 10 | *missing*: the CEL rule requiring `maxRuns` when `disabled` is false | Both CRD rows fixed |
| 10-11 | 10 | *wrong*: the `gcp` and FIPS runner targets build on other bases, not `alpine:3.24` | Fixed |
| 10-12 | 10 | *wrong*: `intentProjectDetachWorkerPool` takes only `intentProject` | Fixed |
| 10-13 | 10 | *missing*: the autoscaler's sanity check also requires a desired capacity above the group minimum (`auto_scaler.go` L94) | Added |
| 10-14 | 10 | *unsupported*: the Azure and GCP metadata keys were cited to `aws_controller.go` | The right files and lines cited |
| 10-15 | 10 | *unsupported*: several anchors missed their content (`state.go`, `autoscaler.tf`, `aws.pkr.hcl`, `statefulset.yaml`, the EC2 variables) | Widened or split |
| 10-16 | 10 | *copied*: the IoT topic-rule SQL matched the self-hosted message queues page; the same SQL is MIT code in `iot.tf` L55 | Restated in prose, citing the source first |
| 13-1 | 13 | *wrong*: every environment variable and mounted file attribute was said to be `ForceNew`; `value_wo` and `content_wo` change through their `*_wo_version` | Narrowed and cited |
| 13-2 | 13 | *unsupported*: interpolation was said to work across variables from every source; the page says variables defined in different ways, contexts included | Reworded |
| 13-3 | 13 | *unsupported*: the secret table's download and visibility rows were uncited | Docs cited |
| 13-4 | 13 | *wrong*: the self-hosted context page was said to lack hooks; its priority section ranks them | Corrected |
| 13-5 | 13 | *missing*: `SPACELIFT_API_TOKEN`'s three-hour validity, its states (`PLANNING`, `APPLYING`, `PERFORMING`) and its scope | Added |
| 13-6 | 13 | *missing*: the provider page contradicts itself on tokens for stacks without roles | Recorded with an inferred reading (C104) |
| 13-7 | 13 | *wrong*: the runtime-file `version` note sat on precedence rank 1, though it swaps ranks 2 and 3 | Moved; the version 1 order stated |
| 13-8 | 13 | *missing*: the configuration page's single context rank, and `feature:ignore_runtime_config`, which removes ranks 1 and 2 | Added |
| 13-9 | 13 | *missing*: `RuntimeConfigInput` was described as environment and hooks only; it also has `yaml` (exclusive), `projectRoot`, `runnerImage` and `gitSparseCheckoutPaths` | Completed |
| 13-10 | 13 | *unsupported*: that contexts contribute only variables, files and hooks was cited to a page that does not say it | Re-cited to the context page |
| 16-1 | 16 | *wrong*: the provider was said to fold `manage_state` and Terragrunt `use_state_management` into one `isStateManaged`; it sends both (defaults true and false) and combines them with OR only in its own checks | Section 2.3 rewritten; the API's behaviour is open (C113) |
| 16-2 | 16 | *wrong*: `terraform_external_state_access` was cited to L583-588, the opentofu block | L763-768 added |
| 16-3 | 16 | *unsupported*: "takes precedence" was cited to L100-105, which does not say it | L826-831 cited |
| 16-4 | 16 | *missing*: the provider's external-access check covers only the Terraform flag | Added |
| 16-5 | 16 | *wrong*: state history built on object versioning was marked inferred; the self-hosted object storage page states it | Now cites the docs |
| 16-6 | 16 | *wrong*: a cross-reference gave section 5 for state history; it is section 6 | Fixed |
| 16-7 | 16 | *missing*: the S3-compatible store's requirements: version listing and copy, SigV4 pre-signed GET and PUT, multipart, path-style addressing and lifecycle rules | Added |
| 16-8 | 16 | *missing*: the AWS module's conditional public access block, and the states bucket's lack of a lifecycle rule | Added (C249) |
| 16-9 | 16 | *wrong*: Terragrunt stacks were said to be unable to read other state at all; the limit is on external dependencies reading state outside the stack | Narrowed |
| 16-10 | 16 | *missing*: `searchTofuWorkspacesSuggestions`, and that no mutation matches `STACK_WORKSPACE_LOCK` | Added; the lock reading is inferred (C8) |
| 16-11 | 16 | *missing*: the runtime key `terragrunt.use_state_management`, the per-run `RuntimeConfig` records, and the runtime file's inability to override OpenTofu external access | Added; open (C114) |
| 16-12 | 16 | *missing*: the OpenTofu vendor migration keeps external access; CDKTF stacks must drop their local backend | Added with sources |

### What survived

- **08:** all 22 `RunState` and 7 `RunType` values and their descriptions;
  the docs' terminal and passive flags; the 43 transitions, each edge's
  states and diagram ID matching the table, every reachable state with an
  edge in and out, terminal states leading only to the end, and `UNKNOWN`
  and `PENDING` justified as unreachable; the inferred edges T14, T30 and
  T32 with their basis; the 2-day, 7-day and 30-day timers; the seven
  priority tiers; the human and policy locks; the 11 hook lists and their
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

### Findings by severity

| Report | wrong | unsupported | missing | copied | style | Total |
|---|---|---|---|---|---|---|
| [11](11-vcs-integrations.md) | 3 | 3 | 1 | 0 | 2 | 9 |
| [12](12-cloud-integrations-and-oidc.md) | 4 | 3 | 2 | 0 | 1 | 10 |
| [14](14-spaces-rbac-and-identity.md) | 1 | 7 | 7 | 0 | 2 | 17 |
| [19](19-events-notifications-audit.md) | 6 | 6 | 8 | 0 | 2 | 22 |
| **All** | **14** | **19** | **18** | **0** | **7** | **58** |

### Findings

| # | Report | Finding | Change |
|---|---|---|---|
| 1 | 11 | *wrong*: sections 14.5 and 14.6 said the vcs-agent applies an allowlist with `--use-allowlist` and a blocklist with `--blocklist-path`; at `553aba3b3e54` both flags are defined but absent from the command's `Flags` list, so the agent always runs the empty blocklist, validates nothing, and ignores `--allowed-projects` | 14.6 and both flag rows rewritten, citing `main.go` L140-L153; urfave/cli's lookup behaviour is inferred; open (C78, P4) |
| 2 | 11 | *unsupported*: the `--bugsnag-api-key` default was "built in"; the flag default is empty, and release builds embed the key through `-X main.BugsnagAPIKey` | Cited `.goreleaser.yaml` L11-L12 |
| 3 | 11 | *wrong*: `VCSCheckOptions` fields were typed `Boolean`; they are `Boolean!` on the output type and `Boolean` only on the input | Fixed |
| 4 | 11 | *unsupported*: the GitHub event list was presented as webhook subscriptions, while the docs step labels it organization permissions | List kept as docs; the subscription reading marked inferred |
| 5 | 11 | *unsupported*: the pull request opened and synchronized rows said push policies are evaluated, without a marker; the GitHub webhook table says only that the record is stored or updated | Markers and the push policy citation added |
| 6 | 11 | *missing*: only new comments drive comment actions; edits and deletions are never received | Added |
| 7 | 11 | *wrong*: the addressing table put GitHub under the Terraform examples; there is no GitHub Enterprise resource | Noted |
| 8 | 11 | *style*: the purpose paragraph named the drafter's scratch scripts | Reworded |
| 9 | 11 | *style*: history narration ("still describes", "now carries", "the older one", "now takes") | Present tense |
| 10 | 12 | *wrong*: the AWS session name conflict was settled for the provider, against the precedence rule, and missed the self-hosted first-setup page, which puts the account name in default session names | A three-source table; the docs' internal contradiction stated; the exact string open (C83) |
| 11 | 12 | *wrong*: `spacelift_gcp_service_account.token_scopes` "forces new"; it has no `ForceNew`, needs at least one non-empty value, and updates in place by re-running `stackIntegrationGcpCreate` | Corrected |
| 12 | 12 | *wrong*: that every space but `root` (and `legacy`) has a ULID suffix cited a page naming only `root`; the Azure page names both, and the overview's example subject shows a suffixed `legacy` | The three-page conflict recorded; open (C94) |
| 13 | 12 | *unsupported*: the integration's space was said to be chosen at create; `space` is optional and its default undocumented | Open (C87) |
| 14 | 12 | *missing*: the operations tables lacked `AwsIntegration.attachedStack`, `externalIdForStack`, `AzureIntegration.attachedStack` and the Azure `attachable*` fields, all of which the projection serves | Added with their REST paths |
| 15 | 12 | *wrong*: all operations were said to carry the `Cloud Integrations` tag; six carry `Spaces`, `Stacks`, `Modules` or `Account` | Corrected |
| 16 | 12 | *unsupported*: that AWS matches only `sub` and `aud` was stated as fact, while the docs hedge it | Restated with the hedge |
| 17 | 12 | *unsupported*: the discovery document path, documented only for self-hosted, was applied to SaaS | The SaaS case marked inferred (C95) |
| 18 | 12 | *missing*: the provider's 900 seconds equals the 15-minute minimum, and `durationSeconds` is nullable | Added (C82) |
| 19 | 12 | *style*: the purpose paragraph named the drafter's scratch scripts | Reworded |
| 20 | 14 | *missing*: the per-IdP SSO guides were not cited; AWS IAM Identity Center SAML, Okta OIDC and OneLogin OIDC were absent, with no settings, claim mappings or differences between identity providers | New section 6.4: a table of what Spacelift shows and needs per protocol, and a six-IdP table (settings, identity claim, group claim, provider URL, caveats such as Entra's 200-group limit and object IDs, Okta's `Teams` statement and AWS group support), plus the notes on Google Workspace and Cognito and the rule that groups arrive on the login assertion |
| 21 | 14 | *wrong*: every OIDC block field of `spacelift_api_key` was said to force replacement; only `issuer`, `client_id`, `subject_expression` and adding or removing the block do | Corrected from `resource_api_key.go` L50-L80 and L233-L275 |
| 22 | 14 | *unsupported*: the tier table paired `BillingTierFeature` names with capabilities as schema fact; only `SCIM` and `AUDITING` have descriptions naming the feature | Pairing marked inferred by name; tiers confirmed from docs notices and the pricing page (spaces from Starter+; SAML SSO, OIDC API keys and MFA on Enterprise); Free-tier spaces, custom roles and login policy plans open (C121) |
| 23 | 14 | *missing*: `SpaceAccessLevel` had no semantics, propagation or inheritance | The docs' semantics and worked example added; the generalisation inferred; `ManagedUserUpdateInput.accessRules` from the schema (C126) |
| 24 | 14 | *unsupported*: that Read climbs past the immediate parent was given as docs | Marked inferred with its basis (C127) |
| 25 | 14 | *unsupported*: the non-root admin Edit cell ignored the settings page's contrary statement | Both recorded; open (C115) |
| 26 | 14 | *unsupported*: the `space-admin` slug was cited to a page that does not show it | Re-cited to the roles-for-stacks page and the Terraform schema |
| 27 | 14 | *unsupported*: that binding changes recompute without a new login was cited to a page that says the opposite | Cited to the changelog; the docs statement recorded (C128) |
| 28 | 14 | *unsupported*: the IAM against login policy table made claims no source supports (an exception for keys a policy names; deactivated users under IAM) | Restated per the docs; deactivation inferred |
| 29 | 14 | *unsupported*: `ADMIN_USERNAME` and `ADMIN_PASSWORD` were cited to a page that does not name them | General configuration page cited; empty values disable admin login |
| 30 | 14 | *missing*: legacy blueprints have no RBAC actions | Added |
| 31 | 14 | *missing*: who may change login policies | Added |
| 32 | 14 | *missing*: SCIM details: Okta SCIM needs a SAML or SWA app; group deletion; a provisioned user with no role is refused | Added |
| 33 | 14 | *missing*: the provider always sends `ApiKeyInput.admin = false` | Added (C124) |
| 34 | 14 | *missing*: the `spacelift_space_by_path` path rules, and re-parenting in place through `spaceUpdate` | Added |
| 35a | 14 | *style*: an unused repository pin | Removed |
| 35b | 14 | *style*: references to the drafter's scratch scripts | Reworded |
| 36 | 19 | *wrong*: `policy_receipts` was placed under `run_updated.run`; the contract, and Spacelift's own Datadog module, put it at `run_updated.policy_receipts` | Moved; the conflict recorded; open (C184) |
| 37 | 19 | *wrong*: `run.type` was given as `PROPOSED` or `TRACKED`; the contract also allows `TASK`, `TESTING`, `DESTROY` and `PARSE` | Fixed |
| 38 | 19 | *missing*: contract fields: `plan_policy_decision`, `pull_request_id`, `exist_on_tracked_branch`, `idp_subject`, the 19-value change `action` enum, `moved`, `git_sparse_checkout_paths`, the stack's `worker_pool`, `contexts`, globs and `terraform_workflow_tool`, `timing` semantics, and mutually exclusive events | Added from the contract |
| 39 | 19 | *missing*: the `pull_request` rule lacked `truncate_body` and the integer type of `id` | Added; what `truncate_body` does is open (C174) |
| 40 | 19 | *missing*: the audit action list lacked `identity.link` and `identity.unlink` | An action table added |
| 41 | 19 | *unsupported*: `account.login_failed` cited only the changelog | The login policy page added |
| 42 | 19 | *unsupported*: HMAC-SHA1 and HMAC-SHA256 were given as docs; the docs say only SHA1 or SHA256 by GitHub's mechanism | HMAC marked inferred, backed for SHA-256 by the example receiver's source, which has no licence and is not quoted |
| 43 | 19 | *unsupported*: the audit payload's `remoteIP` and millisecond `timestamp` appear only in samples, and the `SecretSHA` the sample shows in place of the secret was left out | Marked as sample-only, milliseconds inferred; `SecretSHA` added |
| 44 | 19 | *unsupported*: the `NOTIFICATION_POLICIES` and `RUN_STATE_CHANGE_WEBHOOKS` gates were marked as schema | Marked inferred by name; stack webhooks gained the docs' paid gate |
| 45 | 19 | *missing*: the provider describes `include_runs` differently from the schema and docs | Conflict recorded; the schema and docs win (C182) |
| 46 | 19 | *missing*: worker usage events go nowhere without a policy, apply to negotiated contracts only, and work on SaaS and self-hosted | Added |
| 47 | 19 | *missing*: delivery retention was wholly open, though the self-hosted deliveries bucket is documented (one day suggested; Spacelift does not expire it) | Added; only SaaS retention stays open (C178) |
| 48 | 19 | *missing*: managing the audit trail needs root space admin | Added |
| 49 | 19 | *wrong*: every route was said to be in Bruno; `deliveries` and `deliveryDetails` are not | Reworded |
| 50 | 19 | *wrong*: `spacelift_build_info` was "always 1"; it is absent on failed strict-mode scrapes | Corrected |
| 51 | 19 | *wrong*: every secret header attribute was said to force replacement; `value_wo` does not | The four `ForceNew` attributes named |
| 52 | 19 | *missing*: the `concurrencyPercentiles` default `[75, 90, 95, 99]` | Added |
| 53 | 19 | *wrong*: the usage exporter was said to always write a local file; `--send-to-spacelift` skips it, and the script authenticates again for each batch | Reworded |
| 54 | 19 | *unsupported*: the spacectl audit link L18-L21 did not cover the limit and search claims | Widened to L18-L52 |
| 55a | 19 | *style*: scratchpad script paths in the header | Removed |
| 55b | 19 | *style*: "no longer" history narration | Present tense |
| 56 | 19 | *unsupported*: `Notification.type` was glossed as the kind of target and marked as schema | Reworded; the target reading marked inferred |

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

### Findings by severity

| Report | wrong | unsupported | missing | copied | style | Total |
|---|---|---|---|---|---|---|
| [15](15-module-and-provider-registry.md) | 1 | 1 | 5 | 0 | 0 | 7 |
| [17](17-blueprints-and-templates.md) | 5 | 2 | 4 | 0 | 0 | 11 |
| [18](18-scheduling-drift-dependencies.md) | 2 | 0 | 3 | 0 | 0 | 5 |
| [20](20-vendors.md) | 4 | 9 | 8 | 2 | 1 | 24 |
| [21](21-resources-scans-search.md) | 3 | 3 | 4 | 0 | 0 | 10 |
| [25](25-other-integrations.md) | 6 | 1 | 1 | 0 | 1 | 9 |
| **All** | **21** | **16** | **25** | **2** | **2** | **66** |

### Findings

| # | Report | Finding | Change |
|---|---|---|---|
| 1 | 15 | *wrong*: an unknown version's download was recorded as `404` with a JSON `not found` body; a re-capture returns `404` with the plain-text body `404 page not found`, and the JSON body belongs to unmatched paths, served as `text/plain` | Rows corrected from the capture |
| 2 | 15 | *missing*: version parsing on download: `9.1` and `v9.1.0` resolve to the `9.1.0` archive, and `abc` returns `400 invalid module version: "abc"` | Rows added from the capture |
| 3 | 15 | *missing*: an unknown module's versions call returns `200` with an empty list, which departs from the module registry protocol's `404` | Noted, with the protocol URL |
| 4 | 15 | *missing*: whether failed or yanked versions appear in the list was left open, but the live list has gaps (`2.11.0`, `3.0.2`, `4.4.0`, `5.6.0`, `6.0.2`) that download `404`; as version numbers cannot be skipped, the protocol omits unusable versions | Recorded from the capture with an inference; the question narrowed to which state causes each gap (C133) |
| 5 | 15 | *missing*: the archive layout and the signed URL's lifetime were not measured | Entries sit at the archive root; `Expires` measured 588 s after the request |
| 6 | 15 | *unsupported*: the `workflow_tool` default `TERRAFORM_FOSS` was marked as a provider default; the attribute is computed with no default, and only its description names the server's default | Reworded |
| 7 | 15 | *missing*: neither a module nor a version can be deleted while an Intent module deployment uses it | Added to the transitions table |
| 8 | 18 | *wrong*: drift detection runs were said to come after every other run; the prioritization list puts all high-priority runs first whatever their type | Row rewritten from the run page |
| 9 | 18 | *wrong*: the template schema's `maxLength: 1` on drift `cron` items was read as a restriction; draft-07 ignores keywords beside `$ref`, and such a cap would reject every cron expression | Rewritten as inferred |
| 10 | 18 | *missing*: the cron pattern's gaps: no stepped range (`1-5/2`) and no comma list of ranges | Added (C156) |
| 11 | 18 | *missing*: the UI marks a scheduled run's name optional while the schema requires it | Added (C153) |
| 12 | 18 | *missing*: the provider's description narrows `ignoreState` to any final state | Added, with a new open item (C163) |
| 13 | 20 | *missing*: the vendor pages' self-hosted parity was not recorded | Section 15: every vendor page exists in self-hosted v7.1.0, native OpenTofu and its migration included; four page groups differ (no Kubernetes Azure and GCP authentication, an added version-pinning note, different registry tier notes and hosts) |
| 14 | 17 | *wrong*: `coverage.md` excludes 15 deprecated blueprint root fields, not 18 | Count corrected |
| 15 | 17 | *wrong*: every deprecated item was said to point to templates as its replacement; the two `Action` values and `BlueprintStackCreation.stackID` and `runID` give other reasons, and input fields carry no deprecation | Rewritten |
| 16 | 17 | *unsupported*: exactly one of `stack` or `stacks` was said to be allowed in a blueprint body; the schema neither requires nor forbids either | Restated as the schema rule; open (C140) |
| 17 | 17 | *wrong*: `TemplateVersion.createdAt` and `updatedAt` are `Int!`, not `Int` | Fixed |
| 18 | 17 | *wrong*: the ServiceNow beta shipped in self-hosted v2.6.0, not v2.5.0, because the 2.x headings name the change between versions; this also contradicted 25 | Fixed |
| 19 | 17 | *missing*: the blueprint-to-template name map lacked `blueprintVersion(id)`, `searchBlueprintVersions` and its suggestions, `compatibleVersions`, `latestVersion`, `blueprintWithGroup*`, the change records and `sensitive` | Rows added, with the template-only features |
| 20 | 17 | *missing*: the ticket asked which workflows templates replace; the report answered only per operation | New section 2.3: templates replace the versioned family, but not one-off stack creation, cloning or the ServiceNow attach; export is one-way; plain blueprints have no removal date |
| 21 | 17 | *missing*: `BillingTierFeature.BLUEPRINTS` was absent, and the enum has no template value | Added; template gating inferred |
| 22 | 17 | *unsupported*: `templateCreate.initialVersion` was said to create a draft, but the input has no `state` | Marked inferred |
| 23 | 17 | *wrong*: a "default holder" column misread the RBAC page's legacy fallback, and "owner" should read "creator" | Fixed |
| 24 | 17 | *missing*: several consumers were ignored: spacectl's MCP blueprint tools, the Flows Deploy Template and Deploy Blueprint blocks, the OpenTofu migration note, the Bruno note, and the docs calling `type` required where the schema does not | Added |
| 25 | 21 | *wrong*: the provider was said never to call `Stack.runResourceState`; `wait.go#L139-L147` polls it, and the claim contradicted 05 and 08 | Fixed |
| 26 | 21 | *wrong*: spacectl's ordering: the stack, module, policy, blueprint and template list pagers drop `orderBy`, stacks are not sent `starred DESC`, and the audit trail is not ordered | Rewritten, naming the commands that do send an order |
| 27 | 21 | *unsupported*: ordering was called unverified although `searchRuns`' own description names orderable fields and saved views store a sort | Both schema texts recorded; ordering of `orderable` fields inferred (C200) |
| 28 | 21 | *missing*: `searchRuns`' `offset` semantics and `SearchRunsOutput.total` | Added |
| 29 | 21 | *wrong*: the suggestions twin of `openTofuMigrateSearch` is `openTofuMigrateSuggestions` | Fixed |
| 30 | 21 | *unsupported*: `starSet` for modules was marked inferred; its description says stack or module | Now cites the schema |
| 31 | 21 | *unsupported*: `hasAnsibleStacks` was described as a UI switch; the schema says only that the account has one | Its use marked inferred |
| 32 | 21 | *missing*: the migration widgets were inferred only, and the OpenTofu tab was missing | Terragrunt and OpenTofu migration pages cited; mapped to `openTofuMigrationCounts` |
| 33 | 21 | *missing*: the drift detection page (drifted resources), the organizing-stacks columns and the Ansible configuration management page were ignored | Added |
| 34 | 21 | *missing*: the page-size evidence lacked spacectl `draw` asking for 100 worker pools per page | Added |
| 35 | 20 | *unsupported*: the Terragrunt binary coming "from the image or a hook" had no docs support | Restated as the resolved `terragruntVersion`; delivery open (C185) |
| 36 | 20 | *unsupported*: the default runner images for CloudFormation, Kubernetes, Terragrunt and Pulumi were inferred from 10 | The Docker page cited: runner-terraform is the default for all but native OpenTofu, and it has no Pulumi CLI |
| 37 | 20 | *unsupported*: the Ansible default image was cited only to 10 | The worker pools page cited (`SPACELIFT_DEFAULT_ANSIBLE_RUNNER_IMAGE`) |
| 38 | 20 | *unsupported*: "no Pulumi version setting" was inferred | The runtime configuration page cited: the version follows the image |
| 39 | 20 | *missing*: the FOSS Terraform upper bound: liftoff routes Terraform after 1.5.7 to `CUSTOM` | Added |
| 40 | 20 | *missing*: OpenTofu's `effectiveVersion` | Added |
| 41 | 20 | *missing*: `Module.skipVendorBinaryDownload` | Added |
| 42 | 20 | *missing*: `terragruntEffectiveVersion`'s `tool` takes only `TERRAFORM_FOSS` or `OPEN_TOFU` and defaults to `TERRAFORM_FOSS` | Added |
| 43 | 20 | *wrong*: `effectiveTerraformVersion` is also null when the version is unset | Fixed |
| 44 | 20 | *wrong*: a cross-reference pointed to section 4.6, which does not exist | Now 4.4 |
| 45 | 20 | *unsupported*: the `TERRAFORM_FOSS` default was cited to a page that does not say it | Runtime configuration page and `resource_stack.go#L781-L785` cited |
| 46 | 20 | *copied*: the paragraph on OpenTofu `CUSTOM` restrictions copied docs prose | Rewritten |
| 47 | 20 | *copied*: a sentence on runs ignoring the stack's version for the newest copied runtime configuration prose | Rewritten |
| 48 | 20 | *missing*: `LogsDownloadURL` lacked `deleted` | Added |
| 49 | 20 | *missing*: `stackMigrateVendor` was offered for the single-stack OpenTofu migration, though its description limits it to Terragrunt | Narrowed; still open (C187) |
| 50 | 20 | *unsupported*: Terragrunt's `OPEN_TOFU` was called the default without saying that the API input and the runtime block both default to `TERRAFORM_FOSS` | Both stated, matching 07 |
| 51 | 20 | *missing*: the runtime `terragrunt` block: six keys, their defaults, and replacement rather than merging | Table added |
| 52 | 20 | *wrong*: the Kubernetes custom workflow was said to share section 3.3's validation, but the page says nothing of parsing up front or `.yml` only | Row rewritten |
| 53 | 20 | *unsupported*: `ANSIBLE_CONFIG` was cited to the wrong page | Re-cited to getting started |
| 54 | 20 | *wrong*: runner-terraform was said to be Alpine 3.24 for every flavour; the `gcp` target is `google-cloud-cli:alpine` | Fixed from `docker-bake.hcl` |
| 55 | 20 | *unsupported*: which vendors run Infracost was left open, though the plan policy page answers it (Terraform, OpenTofu and Terragrunt) | Docs cited; the open question removed |
| 56 | 20 | *unsupported*: the custom-input merge path was cited only to 09 | Plan policy page cited |
| 57 | 20 | *style*: an inconsistent Sources bullet | Fixed |
| 58 | 25 | *wrong*: the link `self-hosted/latest/integrations/` does not exist | Replaced with the five existing pages |
| 59 | 25 | *wrong*: `name_prefix` was cited to `schema.json#L5`; the text is at L4 | Fixed |
| 60 | 25 | *wrong*: the SDK was said to rewrite `secretFromParameter`, but `generator.py#L256-L282` maps only variables | Fixed; an inferred row added (C254) |
| 61 | 25 | *wrong*: in the external integration operations an input field sat in the REST column, and three operations lacked paths | Paths added from the projection |
| 62 | 25 | *missing*: `IntegrationsCount.backstage` and `.serviceNow` | Added |
| 63 | 25 | *wrong*: a stack in any other `StackState` was said to fail the Backstage zod check, but the zod enum holds all 16 values | Fixed |
| 64 | 25 | *wrong*: the operator's `StackStatus` has only `id`, not an ID and a state | Fixed from `stack_types.go#L116-L118` |
| 65 | 25 | *style*: the operator README fact was marked as a community source | Now source |
| 66 | 25 | *unsupported*: `k8s-spacelift-operator` was called the operator's key identity; it is one example name in a login policy sample | Rewritten |

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

### Findings by severity

| Report | wrong | unsupported | missing | copied | style | Total |
|---|---|---|---|---|---|---|
| [22](22-intelligence.md) | 0 | 0 | 1 | 0 | 0 | 1 |
| [23](23-flows.md) | 2 | 0 | 0 | 0 | 0 | 2 |
| [24](24-self-hosted-reference.md) | 6 | 2 | 9 | 1 | 1 | 19 |
| **All** | **8** | **2** | **10** | **1** | **1** | **22** |

### Findings

| # | Report | Finding | Change |
|---|---|---|---|
| 1 | 24 | *unsupported*: the 2027 MQTT dates cited the v7.1.0 release notes beside the worker pools page; the notes only ask for the switch by 2027-01-01 and name no removal date or release, and 2027-03-30 comes only from the self-hosted worker pools page | Section 3.4 attributes 2027-03-30 to that page and states what v7.1.0 does and does not say; consistent with 10 (C70) |
| 2 | 24 | *wrong*: Secrets Manager was listed among what the master KMS key encrypts; neither the RDS connection-string secret nor the ECS shared secrets set a KMS key | Removed; the secrets use the AWS-managed key, cited |
| 3 | 24 | *wrong*: `kms_arn` was described as bringing one's own key; it removes the whole `kms` module, so the in-application encryption and JWT signing keys are not created and their outputs are null | New bullet in 3.5 citing `main.tf` L13-L25 and `outputs.tf` L11-L19 (C248) |
| 4 | 24 | *missing*: the EKS module's fourth KMS key, `aws_kms_key.ebs`, created when EBS encryption is on without a key ARN | Added to 3.5 and the section 6 encryption row |
| 5 | 24 | *wrong*: section 14.1 left 97 schema types blank (types behind `$ref` unresolved) and listed `ingress.annotations` and `ingressV6.annotations` as `null` leaves; the real leaf is the `proxy-body-size` annotation with value `"0"` | Types filled from the schema; the two rows corrected to L229 and L254 |
| 6 | 24 | *copied*: section 14.2 quoted 299 variable descriptions from MIT module repositories that no `NOTICE` covers | Description column dropped; a note points to the linked lines (P13) |
| 7 | 24 | *wrong*: noncurrent-version expiry and multipart abort were applied to every bucket; expiry applies to 6 buckets and the abort to 9, the modules, states and binaries buckets keep every noncurrent version, binaries has no encryption, public access block or lifecycle, and versioning is on for 9 | Notes rewritten with line ranges (C249) |
| 8 | 24 | *wrong*: SQS long polling was said to be on the async queues; it is on all six queues that are not dead-letter queues, the queues exist only with `create_sqs`, and the drain consuming every queue lacked the observability guide's support | Corrected and cited |
| 9 | 24 | *missing*: `ENVIRONMENT=prod`, which every module sets | Row added; the table grows from 76 to 77 rows |
| 10 | 24 | *wrong*: release-note labels "v3.x", "v4.x" and "v4.1.0 area" were vague and partly wrong: agent pools are v3.4.0, the ECS v2.0.0 and EKS v3.0.0 module updates are v4.0.0, FIPS images are v5.0.0, and the VCS Gateway on EKS, GKE and AKS is v4.1.0 | Sections 2.1, 9.3 and 12 relabelled |
| 11 | 24 | *missing*: the Slack reference page: `SLACK_APP_CLIENT_ID`, `SLACK_APP_CLIENT_SECRET`, `SLACK_SECRET` and the GovSlack variables (environment only, read at start), one Slack integration at a time, and public reachability for callbacks | Two rows in 3.7; Sources updated |
| 12 | 24 | *missing*: the air-gapped guide: VCS agents, the launcher from S3 through `s3_uri` with cycling on upgrade, and runner images with a provider cache | A paragraph in section 4 |
| 13 | 24 | *missing*: `DATABASE_IAM_URL` and `DATABASE_IAM_READ_ONLY_URL` in the database secret (AWS module v3.2.0 or later, with `rds_iam_username`) | Added to the 3.1 IAM row |
| 14 | 24 | *missing*: schema-only Helm keys (`podAnnotations`, `lifecycle`, `terminationGracePeriodSeconds`, `pdb.minAvailable` and `maxUnavailable`, `vcsGateway.service.type` and `appProtocol`, `mqttService.additionalSpec`), two of which GKE uses; every Kubernetes module passes the deprecated `shared.image` | A note after the section 7 table |
| 15 | 24 | *unsupported*: "amd64 only" on ECS was argued from the task definitions alone, though the ECS guide states it | ECS guide cited |
| 16 | 24 | *missing*: the condition on the ECS VCS Gateway service and task (`count` on `vcs_gateway_security_group_id`) | Added to 2.2 |
| 17 | 24 | *missing*: the scheduler decision: a tag counts only if it matches `^v?X.Y.Z([-+].*)?$`, and with a digest only an explicit tag is read | A sentence added to 2.2 |
| 18 | 24 | *missing*: the CloudFormation pages on advanced installations, disaster recovery, PostgreSQL upgrades and Slack setup were not covered | Recorded as a gap (C250) |
| 19 | 24 | *style*: the ECR repositories were cited without lines | `ecr` L1 and L39 |
| 20 | 23 | *wrong*: `defineApp(app)` was called the module's default export at L731; L731 declares it, it is a named SDK export (L1262), and an app's own module default-exports `defineApp(...)` | Rewritten |
| 21 | 23 | *wrong*: the SDK was said to have eight lifecycle states; `EntityLifecycleStatus` and `AppLifecycleStatus` have seven, and `drifted` appears only in `blocks.list` | The 3.5 table, paragraph and closing bullet corrected (C240) |
| 22 | 22 | *missing*: AI terms section 3 says Spacelift-hosted AI consumes Spacelift credits under usage limits, beside section 4's free-of-charge statement | A row added to 2.2; its relation to the USD 10 budget is open (C213) |

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

### Findings by severity

| Report | wrong | unsupported | missing | copied | style | Total |
|---|---|---|---|---|---|---|
| [26](26-scope-and-tiers.md) | 9 | 1 | 2 | 0 | 0 | 12 |
| [27](27-reference-materials.md) | 2 | 0 | 0 | 0 | 0 | 2 |
| [28](28-data-model.md) | 7 | 3 | 0 | 0 | 0 | 10 |
| **All** | **18** | **4** | **2** | **0** | **0** | **24** |

### Findings

| # | Report | Finding | Change |
|---|---|---|---|
| 1 | 26 | *wrong*: how SaaS tiers apply to self-hosted was called undocumented; the self-hosted overview gives the Enterprise feature set where it applies, as report 01 says | "Feature set" row added; the open item narrowed to what `tier` and `tierFeatures` return (C122) |
| 2 | 26 | *wrong*: whether self-hosted has a public pool was *(unverified)*; the self-hosted overview lists it as not available | Row cites the page; C262 resolved |
| 3 | 26 | *missing*: the pricing comparison table was treated as unreadable; its HTML ticks place contexts, custom tasks, the module registry, webhooks, notifications and session management on Starter+ | Paragraph in 2.1; contexts and sessions rows corrected (C261) |
| 4 | 26 | *wrong*: Starter was "not listed" on the pricing page; its FAQ describes a Starter plan with one private worker, and the docs give Free and Starter seat limits | Starter row, 2.2 and `UNLIMITED_SEATS` corrected (C260) |
| 5 | 26 | *missing*: tasks need a paid account (report 03 records it) | Runs row and `spacelift_task` corrected |
| 6 | 26 | *wrong*: the Stacks row omitted `RUN_IGNORED_TRIGGERS` (`searchRunIgnoredTriggers` is a Stacks operation) | Added |
| 7 | 26 | *wrong*: the `spacelift_drift_detection` data source was `missing` because the operation map records no call; its read reuses the served `stack` query | `stub`, `1 [S]`; totals 51 missing and 7 stub |
| 8 | 26 | *wrong*: the `spacelift_gcp_service_account` data source was said to call the deprecated GCP mutations; it reads `stack` or `module` and the deprecated `StackIntegrations.gcp` | Note and count corrected; still superseded |
| 9 | 26 | *wrong*: the `spacelift_idp_group_mapping` and `spacelift_user` data sources were said to call deprecated mutations; they call `managedUserGroups` and `managedUsers` | Notes corrected |
| 10 | 26 | *wrong*: the four deprecated alias rows gave 0 root fields; the map omits aliases | 4 [S] and 2 [S] |
| 11 | 26 | *unsupported*: "no drift detection on Free [D]" joins a docs fact to a pricing fact | Marked [I] from [C] |
| 12 | 26 | *wrong*: `pulumi-spacelift` "retired" on 2026-12-10, after the baseline | "Retirement set for" |
| 13 | 27 | *wrong*: the Digger `ee/` licence was said to allow modification only for development and testing; it allows patches owned by Digger and usable only under licence, and forbids distribution | Cell rewritten from the file |
| 14 | 27 | *wrong*: River v0.50.0 dated 2026-10-26; the GitHub release is 2026-10-27 UTC | Three dates corrected |
| 15 | 28 | *wrong*: `ACCOUNT \|\|--\|\| PUBLIC_WORKER_POOL`; `publicWorkerPool` is nullable and absent on self-hosted and FedRAMP | `\|\|--o\|` |
| 16 | 28 | *wrong*: `MANAGED_USER \|\|--o{ API_KEY`; `ApiKey.owner` is a nullable login | `\|o--o{` |
| 17 | 28 | *wrong*: `TEMPLATE_DEPLOYMENT \|\|--o{ STACK` against nullable `Stack.templateDeployment` | `\|o--o{` |
| 18 | 28 | *wrong*: `EXTERNAL_INTEGRATION \|\|--\|\| API_KEY` tied every API key to an integration | `}o--\|\|` |
| 19 | 28 | *wrong*: `SPACE \|\|--o{ REPO` against nullable `Repo.space` | `\|o--o{` |
| 20 | 28 | *wrong*: `STACK \|\|--\|\| STACK_SLACK_SUBSCRIPTION` against nullable `Stack.integrations` | `\|\|--o\|` |
| 21 | 28 | *wrong*: group role bindings were said to share `role: Role!`; `UserGroupRoleBinding` has only `roleID` | Row corrected |
| 22 | 28 | *unsupported*: `POLICY \|\|--o{ NOTIFICATION`; no `Notification` field names a policy | `\|o--o{` |
| 23 | 28 | *unsupported*: `REPO_COMMIT \|o--o\| REPO_COMMIT` limited a commit to one child | `\|o--o{` |
| 24 | 28 | *unsupported*: pull request to runs was one to many in the row and many to many in the diagram; no `Run` field names a pull request | Row rewritten, *(unverified)* (C269) |

Two cross-report items are recorded, not fixed: report 14 section 11 still
calls the self-hosted tier mapping undocumented, and report 08 does not
record that tasks need a paid account.

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
  `NOTICE` entry were removed (P13).
- **Most corrections are precision, not direction.** Of the 330 findings,
  122 are *wrong*; most of those are line anchors, types, counts, defaults
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
- **Left as they were:** the 102 Purpose cells that are Bruno request names
  (C16); the uncommitted generator scripts named by 03, 04, 05, 10 and 22 to
  24 (P12); and one cross-report note from RES-05, that report 06 could
  record the spacectl list pagers dropping `orderBy`, which 06 does not yet
  do. From RES-07: report 14 section 11 still calls the self-hosted tier
  mapping undocumented, and report 08 does not record that tasks need a
  paid account.

## What this means for infraweave

- The reports' corrected statements, not their first drafts, are the
  basis for design; where a finding left a question open, the plan works
  from the entry in 30, not from either source alone.
- Generated tables carry fact-check fixes made by hand, so regenerating them
  without folding those fixes in would reintroduce the errors (P12).

## Inferred and undocumented

This log adds no claims about Spacelift. Its counts come from the findings
tables of the six reviews; where a review's own summary differs from its
rows, the rows are counted and the difference is stated under Totals.

## Versions at the research baseline

| Component | Version |
|---|---|
| Spacelift SaaS GraphQL schema | introspection `b3255fe38f5e`, fetched 2026-10-28 |
| docs.spacelift.io | read 2026-10-28; self-hosted pages at v7.1.0 |
| terraform-provider-spacelift | v1.57.0 (`c72b9bd2f3c3`) |
| spacectl | `6bf167cb62a6` |
| Policy contract | fetched 2026-10-28, 113,924 bytes |

## Sources

- The fact-check reviews of RES-02, RES-03, RES-04, RES-05, RES-06 and
  RES-07, and the drafters' notes for the same tickets, written 2026-10-28.
- Reports [01](01-spacelift-landscape.md) and [03](03-deploy-core-api-inventory.md) to [28](28-data-model.md) in this directory, as edited by those
  reviews.
- <https://spacelift.io/pricing>, read 2026-10-28 as raw HTML for RES-07.
- The open questions the reviews raised: [30](30-open-questions.md).
