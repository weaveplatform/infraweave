# 30: Open questions

Checked against: reports 01 to 28 in this directory and the RES-02 to RES-06 fact-check reviews, as they stand on 2026-10-28, with reports 22 and 26 as they stand on 2026-11-03; introspection `b3255fe38f5e`; docs.spacelift.io read 2026-10-28.

Research baseline: **2026-10-28**.

This report lists what the research set could not settle: the product
decisions a maintainer must take, the facts that only a live Spacelift
account, a capture or Spacelift itself can confirm, and the upstream changes
that should send a reader back to a report. The entries are the records of
[`open-questions.json`](../../data/open-questions.json), which reports and
decisions cite by ID; this report says how to read and resolve them. Every
report that marks a claim *(unverified)* is listed in a record and cites
its ID, and `make docs-check` holds both; the
[coverage table](#coverage-of-the-unverified-markers) counts the markers.
The questions come from the drafters' notes and the fact-checkers' reviews
of RES-02 to RES-06, merged and deduplicated across tickets. The design
records (decisions 0004 onward), report [26](26-scope-and-tiers.md)'s availability states and the
implementation plan resolve or carry these entries; the review findings
behind them are in [31](31-review-log.md).

| Label | Means |
|---|---|
| *(unverified)* | A claim in reports 01 to 28 that no primary source confirms; each cites the entry that carries it |

## How to use this list

- **Numbering.** Product decisions are `PD-01` to `PD-16`, captures `C1`
  to `C271` and re-verify triggers `RV-1` to `RV-28`. The prefixes keep
  them apart from the plan's phases (P1, P1b) and from other registers' IDs.
  IDs are stable: a resolved entry keeps its ID and gains a status.
- **Reports.** A record's `reports` lists the reports it concerns. The first
  holds the claim; others repeat or depend on it. Each cites the record's
  ID at the claim, after its *(unverified)* marker or in its "Inferred and
  undocumented" section, so a reader can go from either end to the other.
- **Resolving a capture.** Check the captured request and response in as a
  fixture, change the report's *(unverified)* marker to **[C]** with the
  capture's date, and set the record's `status` to `resolved`, saying how
  in its question.
- **Resolving a decision.** Record it in a decision record and link that
  record from the entry's `resolver`; once the record is Accepted, set the
  entry's `status` to `answered`.
- **Capture safety.** A capture needs an account Spacelift has authorized
  for that use: a trial or demo account, or an existing customer account
  with permission. Questions marked "ask Spacelift" need support or sales.

## Product decisions

The product decisions are the register's records of kind
`product-decision`, `PD-01` to `PD-16`. Each holds the question, its
options, who resolves it and the decision record that answers or proposes
an answer to it, and what waits on it. A decision that an Accepted record
answers has the status `answered`; PD-12, which the research pull requests
settled, is `resolved`.

## Captures needed

The captures are the records of kind `capture`, `C1` to `C271`. Each holds
the question, how to capture the answer and what the answer blocks; its
`area` groups it with the reports it came from, and its `reports` name the
report that holds the claim first.

## Re-verify triggers

The re-verify triggers are the records of kind `re-verify`, `RV-1` to
`RV-28`. Each names an upstream change, the reports and sections to read
again when it happens (`recheckReports` lists the reports), and what to look
for. RV-26, a capture being made, applies to every capture.

## Coverage of the unverified markers

`make docs-check` fails on a report that marks a claim *(unverified)* but
no record lists, and on a record whose listed report does not cite its ID.
The table counts each report's markers on 2026-11-03; which records a
report cites is in the register's `reports` members. "Defining the marker"
counts the label-table rows, and the method text in 01, that define the
marker rather than qualify a claim. Reports 02 and 29 have no markers.

| Report | Markers | Defining the marker | Claims |
|---|---|---|---|
| [01](01-spacelift-landscape.md) | 10 | 1 | 9 |
| [02](02-graphql-to-rest-mapping.md) | 0 | 0 | 0 |
| [03](03-deploy-core-api-inventory.md) | 9 | 0 | 9 |
| [04](04-platform-api-inventory.md) | 9 | 0 | 9 |
| [05](05-terraform-provider.md) | 3 | 0 | 3 |
| [06](06-spacectl-and-clients.md) | 5 | 1 | 4 |
| [07](07-stack-model.md) | 12 | 1 | 11 |
| [08](08-run-lifecycle.md) | 35 | 1 | 34 |
| [09](09-policies.md) | 29 | 1 | 28 |
| [10](10-workers-and-execution.md) | 20 | 1 | 19 |
| [11](11-vcs-integrations.md) | 8 | 1 | 7 |
| [12](12-cloud-integrations-and-oidc.md) | 31 | 1 | 30 |
| [13](13-configuration.md) | 15 | 1 | 14 |
| [14](14-spaces-rbac-and-identity.md) | 15 | 1 | 14 |
| [15](15-module-and-provider-registry.md) | 10 | 1 | 9 |
| [16](16-state-management.md) | 12 | 1 | 11 |
| [17](17-blueprints-and-templates.md) | 20 | 1 | 19 |
| [18](18-scheduling-drift-dependencies.md) | 23 | 1 | 22 |
| [19](19-events-notifications-audit.md) | 38 | 1 | 37 |
| [20](20-vendors.md) | 9 | 1 | 8 |
| [21](21-resources-scans-search.md) | 22 | 1 | 21 |
| [22](22-intelligence.md) | 18 | 1 | 17 |
| [23](23-flows.md) | 36 | 1 | 35 |
| [24](24-self-hosted-reference.md) | 11 | 1 | 10 |
| [25](25-other-integrations.md) | 14 | 1 | 13 |
| [26](26-scope-and-tiers.md) | 9 | 1 | 8 |
| [27](27-reference-materials.md) | 5 | 1 | 4 |
| [28](28-data-model.md) | 8 | 1 | 7 |
| **All** | **436** | **24** | **412** |

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
- The open-questions register,
  [`open-questions.json`](../../data/open-questions.json), and its schema;
  `internal/devtools/docscheck` checks it with every other register.
