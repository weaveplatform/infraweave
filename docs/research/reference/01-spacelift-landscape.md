# 01: The Spacelift API landscape

Checked against: docs read 2026-10-28; schema `b3255fe38f5e`; spacelift-api-bruno `7cad4574954b`; terraform-provider-spacelift v1.57.0; spacectl `6bf167cb62a6`; the spacelift-io repositories at the commits in the repository table; the GitHub organization listing read 2026-10-28.

Research baseline: **2026-10-28**.

This report records what Spacelift exposes, where each definition is
published, and the client-side contracts infraweave inherits: the single
GraphQL endpoint, the documentation set and its indexes, the self-hosted
documentation and version, every `spacelift-io` repository with its licence,
authentication, error behaviour and rate limits. It closes with the research
method and the specification reference pipeline that the rest of the set
uses as its parity oracle. Every other report cites the sources listed here;
[02](02-graphql-to-rest-mapping.md) explains the REST projection, and 03 and
04 inventory its operations.

| Label | Means |
|---|---|
| **[D]** | A docs.spacelift.io page, read 2026-10-28 |
| **[G]** | The introspection snapshot `b3255fe38f5e` |
| **[B]** | spacelift-api-bruno at `7cad4574954b` |
| **[S]** | Source code at a cited commit |
| **[C]** | A community source or a live listing, with its URL and date |
| **[I]** | Inferred; the text says from what |

## 1. One GraphQL endpoint

Spacelift's programmatic API is one GraphQL endpoint per account,
`https://<account>.app.spacelift.io/graphql`, called with `POST` and a bearer
token; the Terraform provider and `spacectl` use a subset of it
[[D]](https://docs.spacelift.io/integrations/api). The Pulumi provider wraps
the Terraform provider through `pulumi-terraform-bridge`, pinned at
terraform-provider-spacelift v1.9.3
([`provider/go.mod`](https://github.com/spacelift-io/pulumi-spacelift/blob/6abf8237cd1d/provider/go.mod#L11-L13))
**[S]**. That provider is deprecated, and its repository goes read-only on
10 December 2026 ([README](https://github.com/spacelift-io/pulumi-spacelift/blob/6abf8237cd1d/README.md#L2-L4))
**[S]**. No docs page describes a REST API; that the web UI uses the same
endpoint is inferred from the schema's UI-only fields (report 04, section 13)
**[I]**.

| Kind | Count **[G]** |
|---|---|
| Root queries | 285 |
| Root mutations | 380 |
| Object types | 570, including `Query` and `Mutation`; the six `__` introspection types are not counted |
| Input objects | 164 |
| Enums | 90; the two `__` introspection enums are not counted |
| Unions | 17 |
| Interfaces | 3 |
| Scalars | 5 (built-ins only: `ID`, `String`, `Int`, `Float`, `Boolean`) |

Subscriptions are not part of the schema **[G]**. Log streaming is done by
polling (`Run.logs` with a token per run state), which maps naturally onto
REST.

### 1.1 Introspection without an account

`https://demo.app.spacelift.io/graphql` answers the standard introspection
query without credentials; the API page points GraphDoc at that endpoint
[[D]](https://docs.spacelift.io/integrations/api). That is the schema
`specref fetch` snapshots (`internal/specref/fetch.go`).

Caveats:
- The demo account runs the current SaaS release **[I]**, since it is the
  endpoint the docs use for the public schema. Self-hosted Spacelift lags
  it (section 4), and feature-flagged fields can appear on a tenant before
  they reach the demo schema *(unverified)*.
- Introspection gives types, nullability, deprecations and descriptions. It
  gives neither authorization rules nor error semantics; those come from the
  docs, Bruno and client behaviour (sections 7 and 8).
- The schema has no commit or version of its own. It is pinned by the
  SHA-256 of its normalized JSON, and the generated spec is versioned
  `schema-<sha12>`.

## 2. Where Spacelift publishes its definitions

| Definition | Where | Form | Licence |
|---|---|---|---|
| Product documentation | <https://docs.spacelift.io>, each page also as Markdown with `.md` appended | Markdown, indexed by `llms.txt`, `llms-self-hosted.txt` and `llms-flows.txt` (section 3) | none stated |
| Changelog | <https://docs.spacelift.io/product/changelog> | Markdown page, including deprecations | none stated |
| Self-hosted documentation and changelog | <https://docs.spacelift.io/self-hosted/latest> and its `installing-spacelift/changelog` | Markdown, versioned per release (section 4) | none stated |
| GraphQL schema | the account endpoint, or the demo account without credentials | introspection JSON **[G]** | not stated |
| Request collection | [spacelift-api-bruno](https://github.com/spacelift-io/spacelift-api-bruno) | one `.bru` file per operation, with docs and example variables **[B]** | MIT |
| Policy input contract | <https://app.spacelift.io/.well-known/policy-contract.json>, linked from the policy page [[D]](https://docs.spacelift.io/concepts/policy) | JSON schema | not stated |
| Terraform provider schema | [terraform-provider-spacelift](https://github.com/spacelift-io/terraform-provider-spacelift) | Go source and `terraform providers schema -json` (report 05) | MIT |
| CLI | [spacectl](https://github.com/spacelift-io/spacectl) | Go source (report 06) | MIT |
| Example policies | [spacelift-policies-example-library](https://github.com/spacelift-io/spacelift-policies-example-library) | Rego with tests | MIT |
| Support articles | support.spacelift.io | web pages | none stated |

## 3. The documentation set

The local mirror holds 702 pages read 2026-10-28: the 335 pages of
`llms.txt` and the 367 of `llms-self-hosted.txt`.
Both indexes include the 99 Flows pages, so the mirror has 198 pages under a
`flows/` path. Counts per index section, counted by script from the indexes **[D]**:

| Section | `llms.txt` (SaaS) | `llms-self-hosted.txt` |
|---|---|---|
| Getting Started | 12 | 12 |
| Concepts | 87 | 87 |
| Vendors | 51 | 51 |
| Integrations | 39 | 37 |
| Product | 31 | 29 |
| Spacelift Flows | 99 | 99 |
| FAQ | 1 | 1 |
| Legal | 11 | 11 |
| General | 4 | 3 |
| Installation | 0 | 37 |
| **Total** | **335** | **367** |

`llms-flows.txt` lists 53 Flows pages under one `Pages` heading.

The SaaS index's sections hold:

| Section | What it covers |
|---|---|
| Getting Started | Creating a first stack and connecting cloud and source control |
| Concepts | Stacks, runs, policies, contexts, spaces, worker pools, authorization, blueprints and templates, Intelligence and Intent, repos, resources, spacectl |
| Vendors | OpenTofu and Terraform (including the module and provider registries), Terragrunt, Pulumi, CloudFormation, Kubernetes, Ansible |
| Integrations | API, source control, cloud providers and OIDC, SSO, audit trail, webhooks, chat, observability, plugins, external integrations |
| Product | Billing, notifications, security, bulk actions, migration kit, changelog, support |
| Spacelift Flows | The Flows product (report 23) |
| FAQ, Legal, General | Answers, terms and policies, the landing pages |

The self-hosted index adds an `Installation` section of 37 pages and has two
fewer Integrations and Product pages, and one fewer General page **[D]**.

## 4. Self-hosted documentation and version

Self-hosted documentation lives under
<https://docs.spacelift.io/self-hosted/latest>. Its installation changelog
names v7.1.0 as the latest release at the baseline; its 49 entries run back
to the change from v0.0.7 to v0.0.8
[[D]](https://docs.spacelift.io/self-hosted/latest/installing-spacelift/changelog).
That changelog also announces removals:
- Terragrunt through Terraform stacks with the `terragrunt` label goes in the
  first self-hosted release of 2027;
- MQTT worker communication, both AWS IoT Core and the built-in broker, is
  to be removed in favour of HTTP long-poll
  (`SPACELIFT_WORKER_COMMS_PROTOCOL=poll`), with the switch recommended by
  1 January 2027;
- v7 is the last major release with CloudFormation installation; from v8.0.0
  releases ship through a container registry;
- `OBJECT_STORAGE_TYPE=minio` becomes `s3compatible`, with the old variable
  names accepted until v8.0.0.

Self-hosted offers the Enterprise feature set, except the public worker pool
and the Azure and GCP cloud integrations, for which the docs recommend OIDC
federation; new features reach SaaS first [[D]](https://docs.spacelift.io/self-hosted). It runs
on AWS (including GovCloud), Azure, GCP and on-premise Kubernetes **[D]**.
Report 24 records its architecture.

## 5. The spacelift-io repositories

The organization has 136 repositories: 93 of its own and 43 forks of
third-party projects **[C]** (GitHub API `orgs/spacelift-io/repos`, read
2026-10-28). Of the 93, 88 are MIT, `spacelift-intent` is Apache-2.0,
`terraform-provider-smtp` is MPL-2.0, and three carry no licence: the two
Flows Terraform modules and the archived `testing-sl` **[C]**. Seven of the
93 are archived, `spacelift-intent` and `spacelift-operator` among them
**[C]**. The 40 repositories cloned for this report set are pinned at the commits
in the "Cloned at" column; spacelift-api-bruno is
pinned in [`sources.lock.yaml`](../../../api/reference/spacelift/sources.lock.yaml)
instead, and the `flows-app-*` clones are pinned in report 23.

### Repositories (93)

Generated from the GitHub API listing (`orgs/spacelift-io/repos`, read
2026-10-28; forks left out) **[C]**. Licence and status are GitHub's;
"Cloned at" is the commit the research read. Purpose is the GitHub
description, or a one-line summary of the README **[S]** where the
description is empty and the repository is cloned.

| Repository | Licence | Status | Cloned at | Purpose |
|---|---|---|---|---|
| [.github](https://github.com/spacelift-io/.github) | MIT | active | | Contains common information and guidelines for the spacelift-io organization. |
| [aws-cli-alpine](https://github.com/spacelift-io/aws-cli-alpine) | MIT | active | | AWS CLI v2 Docker image built on Alpine. |
| [aws-ec2-worker-pool-example](https://github.com/spacelift-io/aws-ec2-worker-pool-example) | MIT | active | | (no description; not cloned) |
| [backstage-plugins](https://github.com/spacelift-io/backstage-plugins) | MIT | active | `27d06658468a` | Backstage plugins for Spacelift. |
| [celplate](https://github.com/spacelift-io/celplate) | MIT | active | | Templating with Google CEL. |
| [claude-plugins](https://github.com/spacelift-io/claude-plugins) | MIT | active | | (no description; not cloned) |
| [cloudformation-example](https://github.com/spacelift-io/cloudformation-example) | MIT | active | | (no description; not cloned) |
| [cortex-plugin](https://github.com/spacelift-io/cortex-plugin) | MIT | active | | Plugin for cortex.io. |
| [demo-preview-environments-infra](https://github.com/spacelift-io/demo-preview-environments-infra) | MIT | active | | (no description; not cloned) |
| [demo-preview-environments-manager](https://github.com/spacelift-io/demo-preview-environments-manager) | MIT | active | | (no description; not cloned) |
| [demo-preview-environments-service](https://github.com/spacelift-io/demo-preview-environments-service) | MIT | active | | (no description; not cloned) |
| [demo-preview-environments-setup](https://github.com/spacelift-io/demo-preview-environments-setup) | MIT | active | | (no description; not cloned) |
| [ec2-workerpool-autoscaler](https://github.com/spacelift-io/ec2-workerpool-autoscaler) | MIT | active | `ffbe8fbdc110` | Autoscaler for AWS/GCP/Azure worker pools. |
| [fips-images](https://github.com/spacelift-io/fips-images) | MIT | active | | FIPS Enabled Base Images. |
| [flowctl](https://github.com/spacelift-io/flowctl) | MIT | active | `3e7d42dabb01` | README: the CLI for building, managing and deploying Spacelift Flows apps **[S]**. |
| [flows-app-brave-search](https://github.com/spacelift-io/flows-app-brave-search) | MIT | active | | Spacelift Flows app for the Brave Search API. |
| [flows-app-clickup](https://github.com/spacelift-io/flows-app-clickup) | MIT | active | | Spacelift Flows app for ClickUp. |
| [flows-app-conversations](https://github.com/spacelift-io/flows-app-conversations) | MIT | active | | Spacelift Flows app to manage conversations with agents. |
| [flows-app-featurebase](https://github.com/spacelift-io/flows-app-featurebase) | MIT | active | | Spacelift Flows app for Featurebase. |
| [flows-app-hcp-terraform](https://github.com/spacelift-io/flows-app-hcp-terraform) | MIT | active | | Spacelift Flows app for HCP Terraform. |
| [flows-app-launchdarkly](https://github.com/spacelift-io/flows-app-launchdarkly) | MIT | active | | Flows app for LaunchDarkly. |
| [flows-app-openrouter](https://github.com/spacelift-io/flows-app-openrouter) | MIT | active | | Spacelift Flows app for OpenRouter. |
| [flows-app-pinecone](https://github.com/spacelift-io/flows-app-pinecone) | MIT | active | | Spacelift Flows app for Pinecone. |
| [flows-app-segment](https://github.com/spacelift-io/flows-app-segment) | MIT | active | | Spacelift Flows app for Segment. |
| [github.packages.tf](https://github.com/spacelift-io/github.packages.tf) | MIT | archived | | Bridge between GitHub Releases and Terraform. |
| [homebrew-spacelift](https://github.com/spacelift-io/homebrew-spacelift) | MIT | active | | Spacelift Homebrew Tap. |
| [homework-object-storage](https://github.com/spacelift-io/homework-object-storage) | MIT | active | | (no description; not cloned) |
| [homework-object-storage-ws](https://github.com/spacelift-io/homework-object-storage-ws) | MIT | active | | (no description; not cloned) |
| [homework-plan-builder](https://github.com/spacelift-io/homework-plan-builder) | MIT | active | | (no description; not cloned) |
| [hook-plugins](https://github.com/spacelift-io/hook-plugins) | MIT | active | | (no description; not cloned) |
| [kmsjwt](https://github.com/spacelift-io/kmsjwt) | MIT | active | `0372dc710a8c` | KMS implementation of `jwt.SigningMethod`. |
| [kubernetes-helm-example](https://github.com/spacelift-io/kubernetes-helm-example) | MIT | active | | An example repo used to illustrate deploying Helm charts using the Spacelift Kubernetes integration. |
| [local-worker-pool](https://github.com/spacelift-io/local-worker-pool) | MIT | active | `cb4ce86b0a77` | Run a worker pool locally with Docker Compose. |
| [marcinwyszynski-test](https://github.com/spacelift-io/marcinwyszynski-test) | MIT | active | | (no description; not cloned) |
| [multimodule](https://github.com/spacelift-io/multimodule) | MIT | active | | Reference implementation for hosting multiple modules in a single repo. |
| [onboarding](https://github.com/spacelift-io/onboarding) | MIT | active | | (no description; not cloned) |
| [plugins](https://github.com/spacelift-io/plugins) | MIT | active | `930c4509a789` | README: Spaceforge, a Python framework for writing Spacelift plugins as hooks **[S]**. |
| [postgres-image](https://github.com/spacelift-io/postgres-image) | MIT | active | | (no description; not cloned) |
| [prometheus-exporter](https://github.com/spacelift-io/prometheus-exporter) | MIT | active | `257f12096706` | A Prometheus exporter for scraping Spacelift metrics. |
| [public-shared-actions](https://github.com/spacelift-io/public-shared-actions) | MIT | active | | Shared GitHub Actions for use across public Spacelift repositories. |
| [pulumi-spacelift](https://github.com/spacelift-io/pulumi-spacelift) | MIT | active | `6abf8237cd1d` | Pulumi provider to interact with Spacelift. |
| [runner-ansible](https://github.com/spacelift-io/runner-ansible) | MIT | active | `d222259098b3` | README: the Dockerfile of the default Ansible runner image **[S]**. |
| [runner-opentofu](https://github.com/spacelift-io/runner-opentofu) | MIT | active | `41c9f80cb86b` | Spacelift runner image for OpenTofu stacks at Spacelift. |
| [runner-pulumi](https://github.com/spacelift-io/runner-pulumi) | MIT | active | `6120ea0266dd` | Spacelift runner image for Pulumi. |
| [runner-serverless](https://github.com/spacelift-io/runner-serverless) | MIT | archived | | Spacelift image for serverless. |
| [runner-terraform](https://github.com/spacelift-io/runner-terraform) | MIT | active | `6c75296f90e3` | Spacelift runner image for Terraform. |
| [runner-terraform-1password](https://github.com/spacelift-io/runner-terraform-1password) | MIT | active | | A runner image with 1Password CLI added. |
| [self-hosted-usage-data-exporter](https://github.com/spacelift-io/self-hosted-usage-data-exporter) | MIT | active | `3ed9f8feef07` | README: exports usage data from a self-hosted install **[S]**. |
| [self-hosted-v2-to-v3-kit](https://github.com/spacelift-io/self-hosted-v2-to-v3-kit) | MIT | active | `bc80da197853` | This repository contains a Python script that scans your existing V2 infrastructure and generate a Terraform project out of it. |
| [setup-spacectl](https://github.com/spacelift-io/setup-spacectl) | MIT | active | `a359d07b6b87` | A GitHub Action to install spacectl. |
| [signed-run-reference-implementation](https://github.com/spacelift-io/signed-run-reference-implementation) | MIT | active | `379a7b30516e` | Reference implementation for Spacelift signed runs. |
| [spacectl](https://github.com/spacelift-io/spacectl) | MIT | active | `6bf167cb62a6` | Spacelift client and CLI. |
| [spacelift-api-bruno](https://github.com/spacelift-io/spacelift-api-bruno) | MIT | active | | Bruno collection for Spacelift API. |
| [spacelift-api-examples](https://github.com/spacelift-io/spacelift-api-examples) | MIT | active | | (no description; not cloned) |
| [spacelift-helm-charts](https://github.com/spacelift-io/spacelift-helm-charts) | MIT | active | `4e02e1d22e73` | README: Helm charts for Kubernetes private worker pools and the VCS agent **[S]**. |
| [spacelift-intent](https://github.com/spacelift-io/spacelift-intent) | Apache-2.0 | archived | `03a4865d888b` | Intent lets you provision and manage cloud infrastructure using natural language. |
| [spacelift-migration-kit](https://github.com/spacelift-io/spacelift-migration-kit) | MIT | active | | Migrate from your current solution to Spacelift with minimal friction. |
| [spacelift-migration-kit-chilly](https://github.com/spacelift-io/spacelift-migration-kit-chilly) | MIT | active | | (no description; not cloned) |
| [spacelift-operator](https://github.com/spacelift-io/spacelift-operator) | MIT | archived | | A Kubernetes operator to manage Spacelift resources. |
| [spacelift-operator-demo](https://github.com/spacelift-io/spacelift-operator-demo) | MIT | archived | | Demo application for the spacelift operator. |
| [spacelift-policies-example-library](https://github.com/spacelift-io/spacelift-policies-example-library) | MIT | active | `6ff5edb08bc8` | A library of example Spacelift policies. |
| [spacelift-s3-demo](https://github.com/spacelift-io/spacelift-s3-demo) | MIT | active | | (no description; not cloned) |
| [spacelift-self-hosted-helm-chart](https://github.com/spacelift-io/spacelift-self-hosted-helm-chart) | MIT | active | `660c86391811` | Helm chart for Spacelift Self-Hosted. |
| [spacelift-user-guides-library](https://github.com/spacelift-io/spacelift-user-guides-library) | MIT | active | `3b2bfa829667` | Library of user guides for Spacelift - educational content stored as code. |
| [spacelift-worker-image](https://github.com/spacelift-io/spacelift-worker-image) | MIT | active | `be56096a703d` | Building Spacelift-friendly image for private workers. |
| [spcontext](https://github.com/spacelift-io/spcontext) | MIT | active | `b1b211a7ae91` | README: a Go context package with logging and related helpers **[S]**. |
| [stub](https://github.com/spacelift-io/stub) | MIT | active | | (no description; not cloned) |
| [symlinks-test-image](https://github.com/spacelift-io/symlinks-test-image) | MIT | active | | (no description; not cloned) |
| [terraform-aws-ecs-spacelift-selfhosted](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted) | MIT | active | `400fcb9b1a9a` | Terraform module deploying a Spacelift environment to an ECS cluster. |
| [terraform-aws-eks-spacelift-flows-selfhosted](https://github.com/spacelift-io/terraform-aws-eks-spacelift-flows-selfhosted) | none | active | `b7d32fa51c7e` | README: Terraform module deploying self-hosted Spacelift Flows on EKS **[S]**. |
| [terraform-aws-eks-spacelift-selfhosted](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted) | MIT | active | `358b62dedfaa` | README: Terraform module deploying self-hosted Spacelift on EKS **[S]**. |
| [terraform-aws-iam-spacelift-selfhosted](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted) | MIT | active | `73db85af505f` | README: Terraform module creating the default IAM roles for self-hosted Spacelift **[S]**. |
| [terraform-aws-spacelift-flows-agentpool-ec2](https://github.com/spacelift-io/terraform-aws-spacelift-flows-agentpool-ec2) | none | active | `6bb853a4d0a1` | README: Terraform module running a Flows agent pool on EC2 **[S]**. |
| [terraform-aws-spacelift-selfhosted](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted) | MIT | active | `18c59d122853` | Terraform module deploying a basic selfhosted Spacelift infrastructure to AWS. |
| [terraform-aws-spacelift-workerpool-on-ec2](https://github.com/spacelift-io/terraform-aws-spacelift-workerpool-on-ec2) | MIT | active | `b83474a27024` | Terraform module deploying a Spacelift worker pool on AWS EC2 using an autoscaling group. |
| [terraform-azure-spacelift-selfhosted](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted) | MIT | active | `427f0eae36dd` | Terraform module deploying a basic selfhosted Spacelift infrastructure to Azure. |
| [terraform-azure-spacelift-workerpool](https://github.com/spacelift-io/terraform-azure-spacelift-workerpool) | MIT | active | `956e03e85480` | README: Terraform module deploying a worker pool on an Azure VM scale set **[S]**. |
| [terraform-google-spacelift-selfhosted](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted) | MIT | active | `d9cfcb7081b8` | Terraform module deploying a basic selfhosted Spacelift infrastructure to Google Cloud Platform. |
| [terraform-google-spacelift-workerpool](https://github.com/spacelift-io/terraform-google-spacelift-workerpool) | MIT | active | `a658df01a860` | Terraform module deploying a Spacelift worker pool on GCP Compute Engine using an Instance Group Manager. |
| [terraform-provider-flows](https://github.com/spacelift-io/terraform-provider-flows) | MIT | active | `9d1a7ad4d937` | README: the Terraform provider for Spacelift Flows **[S]**. |
| [terraform-provider-smtp](https://github.com/spacelift-io/terraform-provider-smtp) | MPL-2.0 | active | | Trivial provider to send emails using the SMTP protocol. |
| [terraform-provider-spacelift](https://github.com/spacelift-io/terraform-provider-spacelift) | MIT | active | `c72b9bd2f3c3` | Terraform provider to interact with Spacelift. |
| [terraform-spacelift-datadog](https://github.com/spacelift-io/terraform-spacelift-datadog) | MIT | active | `cb36817287f5` | Terraform module providing a notification-based integration between Spacelift and Datadog. |
| [terraform-spacelift-example](https://github.com/spacelift-io/terraform-spacelift-example) | MIT | active | | Example Module which creates a bit of Spacelift infrastructure. |
| [terraform-spacelift-msteams](https://github.com/spacelift-io/terraform-spacelift-msteams) | MIT | active | `2d7020e58441` | Policy-based integration between Spacelift and Microsoft Teams. |
| [terraform-spacelift-policy](https://github.com/spacelift-io/terraform-spacelift-policy) | MIT | active | | Creates a Spacelift policy. |
| [terraform-spacelift-stack](https://github.com/spacelift-io/terraform-spacelift-stack) | MIT | archived | | Creates a Spacelift stack and supporting resources. |
| [terraform-starter](https://github.com/spacelift-io/terraform-starter) | MIT | active | | Starter repository to play with Spacelift. |
| [terragrunt-starter](https://github.com/spacelift-io/terragrunt-starter) | MIT | active | | Starter repository to play with Spacelift + Terragrunt. |
| [testing-sl](https://github.com/spacelift-io/testing-sl) | none | archived | | (no description; not cloned) |
| [testing-spacelift](https://github.com/spacelift-io/testing-spacelift) | MIT | active | | Testing Spacelift workflows. |
| [update-file-action](https://github.com/spacelift-io/update-file-action) | MIT | active | | Create/Update/Delete a file in a repository other than the current one. |
| [vcs-agent](https://github.com/spacelift-io/vcs-agent) | MIT | active | `553aba3b3e54` | Spacelift VCS Agent - a gateway to access internal VCS systems. |

The fork `spacelift-io/graphql` (MIT) is the GraphQL client both spacectl and
the Terraform provider use, through a `replace` of `shurcooL/graphql`: v1.2.0
in [spacectl](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/go.mod#L89)
and v1.3.0 in the [provider](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/go.mod#L72) **[S]**.
`spacelift-operator`, the Kubernetes operator for Spacelift resources, is
archived **[C]**; the worker pool controller chart in `spacelift-helm-charts`
and the `WorkerPool.managedByK8sController` field **[G]** are the live
Kubernetes path (report 06).

## 6. Licence

- **docs.spacelift.io states no licence.** Its pages are cited by URL and
  restated in our own words; names of settings, fields, states, enum values
  and CLI flags are facts and are used as such.
- **The MIT repositories** may be quoted briefly with a link at the pinned
  commit, with attribution in [`NOTICE`](../../../api/reference/spacelift/NOTICE)
  when material is committed.
- **spacelift-intent** is Apache-2.0; its notice travels with anything copied.
- **Repositories with no licence** (the two Flows Terraform modules) are
  cited, never quoted.
- **The schema** carries no licence statement; the committed snapshot is used
  as a reference under [decision 0002](../decisions/0002-reference-sources-and-licensing.md).

## 7. Authentication

Every request carries `Authorization: Bearer <jwt>`
[[D]](https://docs.spacelift.io/integrations/api). The ways to get the JWT:

| Credential | How it becomes a JWT | Evidence |
|---|---|---|
| Secret API key | `apiKeyUser(id, secret)` returns a `User` whose `jwt` the client keeps [D]; `validUntil` is a Unix timestamp of the token's expiry [G]; a key may carry an optional expiry after which it cannot be exchanged [D] | [[D]](https://docs.spacelift.io/integrations/api) **[G]**; [spacectl `api_key.go`](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/client/session/api_key.go#L57-L82) **[S]** |
| OIDC API key | the same mutation, with an OIDC token from the caller's identity provider as `secret`; the key matches `iss`, the client ID (`aud`) and a regular expression over the whole `sub` claim; Enterprise only | [[D]](https://docs.spacelift.io/integrations/api); spacectl mints a fresh GitHub Actions token on every exchange ([`api_key.go`](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/client/session/api_key.go#L38-L41)) **[S]** |
| GitHub personal token | `oauthUser(token)` returns a `User` with a JWT; only for accounts whose identity provider is GitHub | [[D]](https://docs.spacelift.io/integrations/api); [`github_token.go`](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/client/session/github_token.go#L54) **[S]** |
| Browser session (`spacectl profile login`) | spacectl opens `<endpoint>/cli_login?key=<RSA public key>&port=<port>`; after login the browser redirects to a localhost listener with an AES-encrypted JWT and its RSA-OAEP-wrapped key | [`browserauth.go`](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/browserauth/browserauth.go#L113-L120), [token handling](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/browserauth/browserauth.go#L206-L246) **[S]** |
| In-run token | `SPACELIFT_API_TOKEN` injected into runs and tasks; stacks with role attachments get a token that can manage stacks, contexts, integrations and configuration, others a limited one that changes nothing; neither reaches state, runs, tasks or logs; it carries the endpoint, lasts 3 hours and works only while its run is `PLANNING`, `APPLYING` or `PERFORMING` | [[D]](https://docs.spacelift.io/vendors/terraform/terraform-provider) |
| Ready token | `SPACELIFT_API_TOKEN` given to spacectl; the endpoint is the token's single `aud` claim | [[D]](https://docs.spacelift.io/concepts/spacectl); [`api_token.go`](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/client/session/api_token.go#L21-L46) **[S]** |

Token facts clients depend on:
- The JWT's `aud` is the account endpoint; spacectl and the Terraform
  provider read it unverified to find where to send requests **[S]** (report 05).
- spacectl re-exchanges a key 30 seconds before `validUntil`
  ([`api_token.go`](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/client/session/api_token.go#L16-L18)) **[S]**.
- Without `SPACELIFT_API_PREFERRED_METHOD`, spacectl's code tries a ready
  token, then an API key, then a GitHub token
  ([`from_environment.go`](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/client/session/from_environment.go#L86-L96)) **[S]**.
  The docs and a comment in the same file give token, GitHub, API key
  [[D]](https://docs.spacelift.io/concepts/spacectl); the code is what runs,
  so it wins for behaviour, while the docs outrank it as a stated contract
  (report 06).
- Every API key exchanged for a token in a billing cycle counts as a user
  seat [[D]](https://docs.spacelift.io/integrations/api).
- Spacelift tokens expire after about 10 hours; the docs give this as the
  reason to prefer API keys over the GitHub-token exchange for automation
  [[D]](https://docs.spacelift.io/integrations/api).
- The key file offers `/apikeytoken` as a UI login with the key, and a
  `credentials "spacelift.io"` token for the module registry
  [[D]](https://docs.spacelift.io/integrations/api).

The specification reference maps `apiKeyUser` to `POST /auth/token`, its only
public route; every other route requires `bearerAuth` (report 02).

## 8. Error behaviour

GraphQL reports failures in an `errors` array alongside `data`, and the
clients treat a response with errors as failed whatever its HTTP status:
- Both clients go through `spacelift-io/graphql`, which turns the `errors`
  array into `GraphQLErrors`; the Terraform provider joins each error's
  `message` with its `extensions` as `key: value` pairs
  ([`error.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/internal/error.go#L11-L48)) **[S]**.
- Authorization failures arrive as an error whose message contains
  `unauthorized`. spacectl then queries `viewer`: if that succeeds it reports
  a missing permission, otherwise an expired login
  ([`client.go`](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/client/client.go#L47-L67)) **[S]**.
- Raw HTTP calls that bypass GraphQL (downloads, uploads) see a real `401`,
  which spacectl maps to the same re-login message
  ([`client.go`](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/client/client.go#L96-L121)) **[S]**.
- The Terraform provider treats a read error containing `not found` as a
  deleted resource (report 05) **[S]**.

Which HTTP status accompanies these errors is not known: spacectl's
`unauthorized` check matches the error text whether it came from a GraphQL
`errors` array or from a non-200 response body (report 06), so the clients'
text matching does not show that errors arrive at HTTP 200 **[I]**, and no
docs page states the status codes *(unverified)*. Neither source documents stable error codes
in `extensions` *(unverified)*. The specification reference models every
error as a `Problem` response, with 404 for lookups (report 02).

## 9. Rate limits

No docs page read states a request rate limit for the GraphQL API
*(unverified)*. The documented limits are product limits:

| Limit | Value | Evidence |
|---|---|---|
| Policy evaluation samples per hour | 200 notification, 200 push, 500 shared by the other types | [[D]](https://docs.spacelift.io/concepts/policy) |
| Runs created for one VCS event | account setting, at most 500 | [[D]](https://docs.spacelift.io/concepts/run) |

The Terraform provider has an optional client-side limiter, off unless both
`SPACELIFT_MAX_REQUESTS_PER_SECOND` and `SPACELIFT_MAX_REQUESTS_BURST` are set
([`provider.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/provider.go#L302-L322)) **[S]**,
which suggests the service can throttle heavy clients **[I]**.

## 10. spacelift-api-bruno

[spacelift-io/spacelift-api-bruno](https://github.com/spacelift-io/spacelift-api-bruno)
(MIT) is Spacelift's maintained request collection: 673 requests in 91
folders and sub-folders **[B]**. Each `.bru` file holds one GraphQL
operation, its example variables and, in most cases, a `docs` block.

What the reference takes from it:
- **Tags.** The top-level folder (Stacks, Runs, Contexts, …) becomes the
  OpenAPI tag. Advanced uses its sub-folder; Cookbook and Danger Zone fall
  back to the resource's tag, and the eight Danger Zone requests with no
  resource folder stay untagged (report 04).
- **Examples.** Example variables become request examples. An example the
  generated schema rejects is dropped and listed in `coverage.md`. Eight are
  dropped: `stackScheduledTaskCreate` and `stackScheduledTaskUpdate`, because
  GraphQL coerces a single value into a list, and six VCS and blob storage
  integration updates that address the default integration by leaving out
  `customInput`, where REST names every integration in the path.
- **Docs.** The docs text up to the `<!-- notes` marker becomes the operation
  description when introspection has none.

Not every root field has a request. `coverage.md` lists the ones no request
calls.

## 11. terraform-provider-spacelift

[spacelift-io/terraform-provider-spacelift](https://github.com/spacelift-io/terraform-provider-spacelift)
(MIT) has 56 resources and 62 data sources. See
[05-terraform-provider.md](05-terraform-provider.md).

## 12. The specification reference as parity oracle

`internal/specref` turns the pinned upstream inputs into a REST projection
that the rest of the set, and the served API, are measured against:

| Step | Command | Output |
|---|---|---|
| Fetch | `make reference-fetch` (`specref fetch`) | introspection, `bruno.json`, `terraform-schema.json`, `terraform-operations.json` and the pins in [`sources.lock.yaml`](../../../api/reference/spacelift/sources.lock.yaml) |
| Generate | `make reference` (`specref generate`) | [`spacelift.openapi.yaml`](../../../api/reference/spacelift/spacelift.openapi.yaml) (725 operations under 53 tags) and [`coverage.md`](../../../api/reference/spacelift/coverage.md) |
| Verify | `make verify-reference` (`specref check`) | fails when the committed reference differs from what the snapshots generate |
| Scaffold | `make scaffold TAG=… FAMILY=…` | copies one tag into a served-API fragment under `api/src/deploy/` |
| Refresh | `.github/workflows/openapi-spec-update.yml`, Mondays 04:41 UTC | re-fetches when the Bruno head or the latest provider release moves, and opens one PR |

The reference is an oracle in three ways:
- **Coverage.** All 665 root fields are routed (622) or excluded with a
  reason (43), so a missing infraweave route is visible by diffing tags and
  operation IDs ([`coverage.md`](../../../api/reference/spacelift/coverage.md)).
- **Shapes.** Every operation keeps `x-graphql` (field, type, operation) and,
  where Bruno has one, `x-bruno`, so a served route can be traced to the
  upstream field it mirrors (reports 03 and 04).
- **Client contracts.** `terraform-schema.json` and `x-terraform` on matched
  properties tie the provider's attributes to request fields (report 05).

## 13. What is out of scope

- **UI analytics mutations** (`identifyUserEvent`, `trackUserEvent`, …). These
  are excluded in `rest-mapping.yaml`.
- **Deprecated fields and superseded `V1` twins.** These are excluded
  automatically, and each is listed with its upstream reason.
- **Billing and Spacelift-internal SaaS operations.** These are kept in the
  reference so its coverage stays complete. Whether infraweave implements them
  is a later decision.

## 14. Research method

1. **Snapshot first.** The schema, Bruno collection and provider schema are
   fetched once, pinned, and committed; every count in the set comes from them
   with `jq` or `python3 -I`, never by hand.
2. **Mirror the docs.** All 702 pages of the two docs indexes were fetched as
   Markdown on 2026-10-28, so citations point at a
   fixed reading.
3. **Clone the code.** The 40 repositories in the table above are shallow
   clones at pinned commits, read and never executed.
4. **Generate inventories.** Tables of operations, resources, commands and
   repositories are produced by script from the snapshots and sources, then
   enriched by hand with sourced notes.
5. **Rank the sources.** Docs and introspection first, then the changelog,
   support articles, MIT sources, and community sources last; a disagreement
   is recorded with the winner named ([README](../README.md)).
6. **Mark the rest.** A claim no primary source confirms is marked
   *(unverified)* and becomes an open question in report 30.
7. **Fact-check.** An independent pass re-checks each report; its findings go
   to report 31.

## What this means for infraweave

- Clients authenticate by exchanging a key for a short-lived JWT whose `aud`
  names the endpoint; infraweave must issue tokens clients can read the same
  way, and support secret keys, OIDC keys and a browser login for the CLI.
- Clients detect authorization and not-found failures by message text; any
  compatibility layer must preserve those signals or the clients' fallbacks
  break.
- The documentation set is the behavioural reference and the reference spec
  is the coverage reference; both are pinned and refreshed weekly.
- Self-hosted is a supported deployment target with its own version line;
  infraweave's self-hosted story is measured against v7.1.0.
- Licence rules differ per source and must be applied when material is
  committed.

## Inferred and undocumented

- The HTTP status of GraphQL errors: the clients match error text whatever
  the status, so it stays open *(unverified)*.
- Error codes in `extensions`: no stable list found *(unverified)*.
- An API request rate limit: none documented; the provider's limiter hints
  that one exists **[I]** *(unverified)*.
- Feature-flagged fields reaching tenants before the demo schema
  *(unverified)*.
- The browser login's `/cli_login` page behaviour on the server side is known
  only from spacectl's half of the handshake **[S]**.

## Versions at the research baseline

| Component | Version |
|---|---|
| Spacelift SaaS schema | introspection `b3255fe38f5e`, fetched 2026-10-28 |
| Spacelift Self-Hosted | v7.1.0 (installation changelog) |
| spacelift-api-bruno | `7cad4574954b` |
| terraform-provider-spacelift | v1.57.0 (`c72b9bd2f3c3`) |
| spacectl | `6bf167cb62a6` |
| Docs mirror | 702 pages, read 2026-10-28 |
| GitHub organization listing | read 2026-10-28 |

## Sources

- [`api/reference/spacelift/sources.lock.yaml`](../../../api/reference/spacelift/sources.lock.yaml): the pinned inputs.
- [`api/reference/spacelift/coverage.md`](../../../api/reference/spacelift/coverage.md), [`rest-mapping.yaml`](../../../api/reference/spacelift/rest-mapping.yaml), [`spacelift.openapi.yaml`](../../../api/reference/spacelift/spacelift.openapi.yaml).
- <https://demo.app.spacelift.io/graphql>: the introspected schema **[G]**.
- <https://docs.spacelift.io/llms.txt>, <https://docs.spacelift.io/llms-self-hosted.txt>, <https://docs.spacelift.io/llms-flows.txt> **[D]**
- <https://docs.spacelift.io/integrations/api>, <https://docs.spacelift.io/concepts/spacectl>, <https://docs.spacelift.io/vendors/terraform/terraform-provider> **[D]**
- <https://docs.spacelift.io/self-hosted>, <https://docs.spacelift.io/self-hosted/latest/installing-spacelift/changelog> **[D]**
- <https://docs.spacelift.io/concepts/policy>, <https://docs.spacelift.io/concepts/run> **[D]**
- <https://github.com/spacelift-io/spacelift-api-bruno> (MIT) **[B]**
- <https://github.com/spacelift-io/spacectl/tree/6bf167cb62a6> (MIT): `client/`, `client/session/`, `browserauth/` **[S]**
- <https://github.com/spacelift-io/terraform-provider-spacelift/tree/c72b9bd2f3c3> (MIT): `spacelift/provider.go`, `spacelift/internal/` **[S]**
- <https://api.github.com/orgs/spacelift-io/repos>, read 2026-10-28 **[C]**
