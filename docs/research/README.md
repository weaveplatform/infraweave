# Research: Spacelift's feature set behind a REST API

Research baseline: **2026-10-28**. This set of documents records what Spacelift
offers, how each feature behaves, what its API, Terraform provider and CLI
call, and the design that follows. It is the foundation for infraweave:
- the numbered [reference reports](#document-map) hold facts with citations;
- the [decision records](decisions/README.md) hold decisions and their
  rationale;
- the [architecture options](#document-map) weigh repository shapes against
  those facts;
- the implementation plan in `docs/implementation/` holds the sequencing.

The reports are being written under
[#21](https://github.com/weaveplatform/infraweave/issues/21). The
[document map](#document-map) names the ticket that owns each report still to
be written.

## Purpose

infraweave mirrors Spacelift's feature set, every tier including Intelligence
and Flows, behind a REST API described by OpenAPI 3
([decision 0001](decisions/0001-rest-first-spacelift-surface.md)). Mirroring
behaviour, not just API shapes, requires a written account of three things:
- what each feature does;
- which tier offers it;
- how the parts behave together, such as runs, policies, workers and state.

The research supplies that account, marks what could not be verified, and
gives every capability an owner in the implementation plan. The only
capabilities left out are interfaces that a newer one supersedes, and each
exclusion is recorded with its evidence.

## How to read

1. Start with the [landscape](reference/01-spacelift-landscape.md): where
   Spacelift publishes its definitions, how the specification reference is
   built, and the research method.
2. [02](reference/02-graphql-to-rest-mapping.md) explains how GraphQL root
   fields become REST routes. Reports [03](reference/03-deploy-core-api-inventory.md) and [04](reference/04-platform-api-inventory.md) inventory every operation by
   family.
3. [05](reference/05-terraform-provider.md) and [06](reference/06-spacectl-and-clients.md) are the client side: what
   the Terraform provider and spacectl call.
4. Reports 07 to 25 describe behaviour, one feature area each.
5. Reports 26 to 29 bring it together:
   - scope and tiers;
   - reference materials from other tools;
   - the data model, with an inventory generated from the schema.
6. Report 30 lists open questions; 31 records what the fact-check passes
   changed.
7. The [decision records](decisions/README.md) state the contracts and
   rationale.

## Document map

| Document | Purpose | Status |
|---|---|---|
| [01-spacelift-landscape.md](reference/01-spacelift-landscape.md) | Where Spacelift's definitions live, the docs indexes, every spacelift-io repository with its licence, authentication and tokens, error behaviour, rate limits, the research method and the specref parity oracle | Written |
| [02-graphql-to-rest-mapping.md](reference/02-graphql-to-rest-mapping.md) | How root fields map to REST routes, `rest-mapping.yaml`, and the path from reference to served API | Written |
| [03-deploy-core-api-inventory.md](reference/03-deploy-core-api-inventory.md) | Stacks, runs, contexts, policies, spaces, worker pools and modules: every operation per family, with its permission, tier gate and side effects | Written |
| [04-platform-api-inventory.md](reference/04-platform-api-inventory.md) | Every remaining tag, from account and SSO to Intent and Flows, plus the untagged operations | Written |
| [05-terraform-provider.md](reference/05-terraform-provider.md) | The Terraform provider: every resource and data source mapped to its operations and routes, import IDs, attributes with no input field, and behaviour worth copying | Written |
| [06-spacectl-and-clients.md](reference/06-spacectl-and-clients.md) | spacectl commands and their operations, its MCP tools, the Pulumi provider, the Kubernetes operator, Backstage plugins, and what an infraweave CLI must cover | Written |
| [07-stack-model.md](reference/07-stack-model.md) | Stack settings, vendor configuration, runtime YAML and typing traps | Written |
| [08-run-lifecycle.md](reference/08-run-lifecycle.md) | Run types and states, transitions, phases and hooks, confirm, discard, retry and stop, promotion, targeted replan, PR feedback | Written |
| [09-policies.md](reference/09-policies.md) | Policy types, Rego versions, inputs per type, flags, sampling, auto-attach and deprecations | Written |
| [10-workers-and-execution.md](reference/10-workers-and-execution.md) | Public and private pools, the launcher and worker protocol, runner images, caching, drain, autoscaling and signed runs | Written |
| [11-vcs-integrations.md](reference/11-vcs-integrations.md) | GitHub, GitLab, Bitbucket, Azure DevOps, raw git, VCS agent pools, webhooks and checks | Written |
| [12-cloud-integrations-and-oidc.md](reference/12-cloud-integrations-and-oidc.md) | AWS, Azure and GCP integrations, OIDC tokens and subject claims, Vault | Written |
| [13-configuration.md](reference/13-configuration.md) | Contexts, environment variables, mounted files, auto-attach and precedence | Written |
| [14-spaces-rbac-and-identity.md](reference/14-spaces-rbac-and-identity.md) | Spaces and inheritance, roles and bindings, login policies, SSO, SCIM, MFA, API keys and sessions | Written |
| [15-module-and-provider-registry.md](reference/15-module-and-provider-registry.md) | The module and provider registries, versions, test cases, sharing, GPG keys and the registry protocol | Written |
| [16-state-management.md](reference/16-state-management.md) | The managed state backend, external access, import, rollback and state URLs | Written |
| [17-blueprints-and-templates.md](reference/17-blueprints-and-templates.md) | Blueprints, templates, deployments, the workbench and the version catalog | Written |
| [18-scheduling-drift-dependencies.md](reference/18-scheduling-drift-dependencies.md) | Scheduled runs, tasks and deletes, drift detection, stack dependencies and output references | Written |
| [19-events-notifications-audit.md](reference/19-events-notifications-audit.md) | Notifications, Slack and Teams, webhooks, the audit trail and metrics exporters | Written |
| [20-vendors.md](reference/20-vendors.md) | Terraform, OpenTofu, Terragrunt, Pulumi, CloudFormation, Kubernetes and Ansible; workflow tools, cost estimation and sanitization | Written |
| [21-resources-scans-search.md](reference/21-resources-scans-search.md) | The resources view, entity changes, scans, saved filters and search | Written |
| 22-intelligence.md | AI integrations, the Infra Assistant, Intent and the MCP server | Planned, [#27](https://github.com/weaveplatform/infraweave/issues/27) |
| 23-flows.md | Spacelift Flows: model, apps, flowctl, the Flows provider, self-hosted Flows and agent pools | Planned, [#27](https://github.com/weaveplatform/infraweave/issues/27) |
| 24-self-hosted-reference.md | Self-hosted Spacelift's components, external dependencies, networking and reference deployments | Planned, [#27](https://github.com/weaveplatform/infraweave/issues/27) |
| [25-other-integrations.md](reference/25-other-integrations.md) | Plugins, Backstage, ServiceNow, Teleport, Docker and the Kubernetes operator | Written |
| 26-scope-and-tiers.md | Editions and tier features against infraweave's availability state and owning phase; supersession boundaries | Planned, [#28](https://github.com/weaveplatform/infraweave/issues/28) |
| 27-reference-materials.md | Atlantis, OTF, Terrakube, Digger, Burrito, Terramate, the Flux tofu-controller, OPA, Temporal and River: what to borrow | Planned, [#28](https://github.com/weaveplatform/infraweave/issues/28) |
| 28-data-model.md | Wire entities, their relationships and the storage tables they imply | Planned, [#28](https://github.com/weaveplatform/infraweave/issues/28) |
| 29-data-model-inventory.md | Generated from the introspection snapshot: every object type, field and enum | Planned, [#28](https://github.com/weaveplatform/infraweave/issues/28) |
| 30-open-questions.md | Product decisions, captures needed and re-verify triggers | Planned, [#28](https://github.com/weaveplatform/infraweave/issues/28) |
| 31-review-log.md | What each fact-check pass found and changed | Planned, [#28](https://github.com/weaveplatform/infraweave/issues/28) |
| [glossary.md](reference/glossary.md) | Terms used across the set | Skeleton; completed in [#28](https://github.com/weaveplatform/infraweave/issues/28) |
| architecture-options/ | Five repository architecture options, their comparison and a recommendation | Planned, [#30](https://github.com/weaveplatform/infraweave/issues/30) |
| [decisions/](decisions/README.md) | Decision records, with their index | 0001 to 0003; 0004 to 0021 planned in [#29](https://github.com/weaveplatform/infraweave/issues/29) and [#30](https://github.com/weaveplatform/infraweave/issues/30) |

## Versions at the research baseline

| Component | Version | Note |
|---|---|---|
| Spacelift SaaS GraphQL schema | introspection `b3255fe38f5e`, fetched 2026-10-28 | `demo.app.spacelift.io`; the schema carries no version of its own |
| spacelift-api-bruno | `7cad4574954b` | MIT |
| terraform-provider-spacelift | v1.57.0 | MIT |
| Go | 1.27.3 | module baseline |
| chi | v5.5.0 | |
| kin-openapi | v0.152.0 | OpenAPI 3.0.3 validation |
| oapi-codegen | v2.8.1 | strict chi server and client |
| godog | v0.16.0 | Gherkin acceptance |

The snapshot pins live in
[`sources.lock.yaml`](../../api/reference/spacelift/sources.lock.yaml), which
the weekly specification update workflow refreshes. A report's
`Checked against` line names the pins it was last checked against.

## Target version and sources

infraweave targets the current Spacelift SaaS release. That release is what
docs.spacelift.io describes and what the demo account's schema exposes.
Self-hosted Spacelift lags SaaS, and its documentation is versioned under
`docs.spacelift.io/self-hosted/`. When a report relies on self-hosted
behaviour it names the self-hosted version.

Research uses these sources, in this order:

1. **Spacelift's documentation and the introspected schema.** Documentation
   pages are listed in <https://docs.spacelift.io/llms.txt>, and each page is
   available as Markdown by appending `.md` to its URL. The schema is the
   committed snapshot.
2. **The changelog**, <https://docs.spacelift.io/product/changelog>, for behaviour that
   changed after a page was written.
3. **Support articles** at support.spacelift.io.
4. **Spacelift's open-source repositories**, cited at a commit:
   - spacectl;
   - the Terraform provider;
   - the self-hosted Helm chart and Terraform modules;
   - the worker and runner images;
   - the policy example library, vcs-agent, the Prometheus exporter and the
     worker-pool autoscaler;
   - the Flows provider and flowctl;
   - spacelift-intent.

   What a client sends, or accepts back, shows what the service answers.
5. **Community sources**: blog posts, issues on the repositories above, and
   live captures. Only for what the others leave out.

When sources disagree, the higher one wins, and the disagreement is recorded
in the report.

### Evidence markers

Every claim carries one of these markers, with a URL or a repository path.

| Label | Means |
|---|---|
| **[D]** | A docs.spacelift.io page, cited by URL with the date it was read |
| **[G]** | The introspection snapshot pinned in `sources.lock.yaml` |
| **[B]** | spacelift-api-bruno at the pinned commit |
| **[S]** | Source code at a cited commit |
| **[RN]** | The Spacelift changelog |
| **[KB]** | A support.spacelift.io article |
| **[C]** | A community source or a live capture |
| **[I]** | Inferred; the report says from what |
| *(unverified)* | No primary source confirms it; listed in 30-open-questions |

A report's own label table lists only the markers it uses.

### Licence

- **docs.spacelift.io states no licence.** Its pages are cited by URL and
  their facts restated in our own words. Nothing from them is copied into
  the repository. Names of operations, fields, settings and states are facts
  about the product and may be used.
- **MIT repositories** (spacelift-api-bruno, the Terraform provider, spacectl
  and most other `spacelift-io` repositories) may be quoted, with attribution
  in [`NOTICE`](../../api/reference/spacelift/NOTICE) when material is
  committed.
- **spacelift-intent is Apache-2.0** and is treated the same way, with its
  notice carried alongside anything copied.

[Decision 0002](decisions/0002-reference-sources-and-licensing.md) sets the
rules for committed snapshots.

## Conventions

- **Header.** Every numbered report opens with a `Checked against:` line and a
  `Research baseline:` date, and ends with a `## Sources` section. Larger
  reports also end with `## Inferred and undocumented` and
  `## Versions at the research baseline`.
- **Citations.** Every external claim cites a URL. Claims about this
  repository cite a path.
- **Unverified claims.** Claims that could not be confirmed against a primary
  source are marked *(unverified)* and collected in 30-open-questions.
- **Facts and decisions are separate.** Reports hold facts; decisions live in
  [decision records](decisions/README.md), whose status is Proposed, Accepted
  or Superseded.
- **Style.** Plain present-tense sentences, tables for inventories, and no
  em-dashes.
- **Checked by `make docs-check`:**
  - relative links resolve;
  - every report carries its header and `## Sources`;
  - every decision carries a valid status and is indexed.

## Decisions fixed before writing

1. **Scope.** infraweave mirrors everything Spacelift offers, at every tier,
   including Intelligence and Flows. Only an interface that a newer one
   supersedes may be excluded, with evidence.
2. **REST, not GraphQL.** infraweave's public API is REST with OpenAPI 3.
   Spacelift's GraphQL surface is the reference, not the contract
   ([0001](decisions/0001-rest-first-spacelift-surface.md)).
3. **The quality gate** from the weaveplatform template applies:
   - 95% merged coverage, and 90% per package;
   - blocking lint;
   - acceptance tests against real dependencies.
