# Capability roadmap

Every capability in infraweave's scope has an owning phase in the
[implementation plan](plan.md): each of the 53 tags of the Spacelift reference
and its 8 untagged operations, each of the 56 resources and 62 data sources of
the Terraform provider, and each of the 31 `BillingTierFeature` values. Scope,
plans and availability states come from report
[26](../research/reference/26-scope-and-tiers.md); the rule that decides what
is in scope is decision
[0021](../research/decisions/0021-capability-scope-and-supersession.md). What
is not built, and why, is the [supersession register](#supersession-register)
at the end.

## How to read this roadmap

- **Phase** is the owning phase: the phase that delivers the row's core and
  whose exit makes the row `implemented` under decision
  [0016](../research/decisions/0016-testing-and-acceptance.md)'s evidence rule.
  **Also** names the phases that deliver some of the row's operations, each
  with the subject that owns them (a setting, an attachment, a search).
- **API phase** and **Provider phase** split each Terraform row: the API
  phase serves the core operations the resource or data source calls, and
  P24 builds the resource in infraweave's own provider. Where a row also
  calls an operation of a later phase, its notes name that phase. A
  Terraform row is `implemented` only when all of them are done; P24
  depends on every one.
- **State at baseline** is report 26's computed state on 2026-10-28. The
  current state is whatever `specref parity --states` computes; report 26's
  state column is checked against it, and this roadmap does not repeat it.
- **Plan** is the lowest Spacelift SaaS plan that offers the capability, in
  report 26's vocabulary. infraweave has no editions: every capability is
  available on every install, and an operator turns a feature off with
  `features.disabled` (P3b).
- Every phase named in a table row is a phase of [plan.md](plan.md);
  `make docs-check` fails otherwise.
- A row moves phase only in the pull request that changes the plan, and
  report 26's phase column follows.

## Summary by phase

Counts of rows each phase owns. The phases that own no row (P0 to P2c,
P7, P8b, P13b, P20, P24, P24b, P26b and P28) build foundations, behaviour
behind other rows, or the provider and clients. Superseded counts the
Terraform rows that no phase builds; the two superseded `BillingTierFeature`
values count under P3b, which rejects them.

| Phase | Tags | Resources (API) | Data sources (API) | `BillingTierFeature` values |
|---|---|---|---|---|
| P3 | 5 | 1 | 1 | 1 |
| P3b | 5 | 3 | 2 | 3 |
| P4 | 1 | 1 | 4 | 0 |
| P4b | 1 | 2 | 1 | 0 |
| P5 | 2 | 3 | 3 | 0 |
| P6 | 1 | 4 | 5 | 0 |
| P8 | 1 | 3 | 2 | 1 |
| P9 | 0 | 1 | 2 | 0 |
| P10 | 1 | 1 | 1 | 1 |
| P11 | 1 | 0 | 0 | 0 |
| P12 | 1 | 0 | 1 | 2 |
| P13 | 1 | 2 | 2 | 1 |
| P14 | 0 | 0 | 1 | 1 |
| P15 | 1 | 4 | 7 | 2 |
| P16 | 1 | 6 | 4 | 2 |
| P17 | 1 | 2 | 2 | 1 |
| P17b | 2 | 1 | 0 | 1 |
| P18 | 4 | 3 | 2 | 2 |
| P18b | 1 | 1 | 0 | 1 |
| P19 | 0 | 3 | 5 | 1 |
| P19b | 4 | 3 | 4 | 1 |
| P20b | 3 | 2 | 2 | 0 |
| P21 | 3 | 4 | 3 | 3 |
| P22 | 6 | 1 | 2 | 0 |
| P22b | 1 | 0 | 0 | 1 |
| P23 | 1 | 0 | 0 | 4 |
| P25 | 1 | 1 | 2 | 2 |
| P25b | 1 | 0 | 0 | 0 |
| P26 | 1 | 0 | 0 | 0 |
| P27 | 2 | 0 | 0 | 0 |
| Superseded | 0 | 4 | 4 | 0 |
| Total | 53 | 56 | 62 | 31 |

## API tags

One row per tag of `api/reference/spacelift/spacelift.openapi.yaml`, in the
reference's order. Ops is the tag's operation count in the reference.

| Tag | Ops | Owning report | Plan | Phase | Also | Notes |
|---|---|---|---|---|---|---|
| AI Integrations | 5 | [22](../research/reference/22-intelligence.md) | All | P25 |  | Spacelift-provided integration absent on self-hosted |
| API Keys | 8 | [14](../research/reference/14-spaces-rbac-and-identity.md) | All; OIDC keys Enterprise | P3 | P4b (`apiKeyApiKeyRoleBindings`) |  |
| Account | 7 | [04](../research/reference/04-platform-api-inventory.md), [10](../research/reference/10-workers-and-execution.md) | All | P3 | P8, P14, P15 | Default runner image reads belong to report 10 |
| Account Settings | 42 | [04](../research/reference/04-platform-api-inventory.md), [14](../research/reference/14-spaces-rbac-and-identity.md), [22](../research/reference/22-intelligence.md) | All; MFA enforcement Enterprise | P3b | P4, P8, P10, P12, P13b, P15, P18b, P20b, P23, P25 | Each setting lands with the phase that owns its subject |
| Ansible | 7 | [20](../research/reference/20-vendors.md), [21](../research/reference/21-resources-scans-search.md) | Not stated | P20b |  |  |
| Audit Trail | 6 | [19](../research/reference/19-events-notifications-audit.md) | Enterprise | P18b |  |  |
| Auth | 6 | [14](../research/reference/14-spaces-rbac-and-identity.md), [01](../research/reference/01-spacelift-landscape.md) | All | P3 | P23 (`oauthRedirect`, `oauthToken`, `oauthUser`) |  |
| Billing | 14 | [04](../research/reference/04-platform-api-inventory.md), [19](../research/reference/19-events-notifications-audit.md) | All | P3b | P18b (`usage`) | Subscription and tier mutations are SaaS commerce; usage queries are report 19 |
| Blob Storage | 4 | [11](../research/reference/11-vcs-integrations.md) | Not stated | P19b |  | No docs page; a `BLOB` source provider |
| Blueprints | 15 | [17](../research/reference/17-blueprints-and-templates.md) | Business | P21 |  | Single-body blueprints are live; the versioned family is superseded and not in the reference (report 26 section 7.2) |
| Cloud Integrations | 27 | [12](../research/reference/12-cloud-integrations-and-oidc.md) | Starter+; no docs notice | P15 | P25b (Intent project attachments) | Self-hosted offers AWS only; GCP service accounts superseded (report 26 section 7.6) |
| Contexts | 13 | [13](../research/reference/13-configuration.md) | Starter+; Free not stated | P6 |  |  |
| Debug | 1 | [24](../research/reference/24-self-hosted-reference.md) | All | P27 |  | Licence and build details; empty on SaaS |
| Drift Detection | 3 | [18](../research/reference/18-scheduling-drift-dependencies.md) | Starter+ | P16 |  | Private workers only |
| External Integrations | 4 | [25](../research/reference/25-other-integrations.md) | Enterprise for ServiceNow | P21 |  | ServiceNow attaches to blueprints |
| Feature Flags | 1 | [04](../research/reference/04-platform-api-inventory.md) | UI support | P3b |  | Spacelift-set flags; read-only |
| Flows | 6 | [23](../research/reference/23-flows.md) | Not stated | P26 |  |  |
| Forms | 3 | [04](../research/reference/04-platform-api-inventory.md) | UI support | P22 |  | Web UI questionnaires |
| GPG Keys | 4 | [15](../research/reference/15-module-and-provider-registry.md) | Business from the provider registry gate | P17b |  |  |
| Infrastructure | 3 | [24](../research/reference/24-self-hosted-reference.md) | All | P27 | P2c (`messageQueueStats`), P13 (`policyRuntime`) | Installation internals for self-hosted operators |
| Intent | 40 | [22](../research/reference/22-intelligence.md) | All | P25b |  |  |
| Managed Users | 23 | [14](../research/reference/14-spaces-rbac-and-identity.md) | All; Free holds 2 users | P3b | P4b (role bindings), P18 (group integrations) | Group integrations give a Slack channel access rules (report 14 section 4.3) |
| Modules | 34 | [15](../research/reference/15-module-and-provider-registry.md) | Paid | P17 |  |  |
| Notifications | 5 | [19](../research/reference/19-events-notifications-audit.md) | Paid | P18 |  |  |
| OAuth | 1 | [14](../research/reference/14-spaces-rbac-and-identity.md) | All | P18 |  | The Slack redirect belongs to report 19 |
| OpenTofu Migration | 4 | [20](../research/reference/20-vendors.md) | Not stated | P20b |  |  |
| Origin Integration | 4 | [11](../research/reference/11-vcs-integrations.md) | Not stated | P19b |  | No docs page; an `ORIGIN` source provider |
| Personal API Keys | 4 | [14](../research/reference/14-spaces-rbac-and-identity.md) | Not stated | P3b |  |  |
| Plugins | 9 | [25](../research/reference/25-other-integrations.md) | Not stated | P20b |  | Plugins run inside runs; placed with vendor tooling |
| Policies | 21 | [09](../research/reference/09-policies.md) | Paid; Starter+ | P13 | P13b, P25b (Intent project attachments) |  |
| Repos | 12 | [11](../research/reference/11-vcs-integrations.md) | Not stated | P19b |  | Spacelift Repos, the `SPACELIFT` source provider |
| Resources | 4 | [21](../research/reference/21-resources-scans-search.md) | Starter+ | P22 |  |  |
| Roles | 25 | [14](../research/reference/14-spaces-rbac-and-identity.md) | Not stated | P4b |  |  |
| Runs | 35 | [08](../research/reference/08-run-lifecycle.md) | All; tasks paid; targeted replan Business; ignored triggers Starter+ | P10 | P7, P9, P13, P16, P18b, P20b, P22 | 3 of 35 operations are 501 stubs at the baseline: `proposeWithOverrides`, `runTrigger`, `taskCreate` |
| SSO | 11 | [14](../research/reference/14-spaces-rbac-and-identity.md) | SAML and SCIM Enterprise; OIDC SSO paid | P23 |  |  |
| Saved Filters | 5 | [21](../research/reference/21-resources-scans-search.md) | UI support | P22 |  |  |
| Scans | 17 | [21](../research/reference/21-resources-scans-search.md) | Not stated | P22b |  | No docs page |
| Search Suggestions | 30 | [21](../research/reference/21-resources-scans-search.md) | UI support | P22 | each suggestion operation lands with its search, from P5 |  |
| Sessions and Security Keys | 5 | [14](../research/reference/14-spaces-rbac-and-identity.md) | Not on Starter; session management from Starter+ | P3 | P3b (`securityEmail`), P23 (security keys) | Security keys are MFA, Enterprise |
| Slack | 7 | [19](../research/reference/19-events-notifications-audit.md) | Not stated | P18 | P12 (`githubAppGenerateManifest`, `githubAppCreateFromManifest`) |  |
| Spaces | 22 | [14](../research/reference/14-spaces-rbac-and-identity.md) | Starter+; Free not stated | P4 | P4b, P6, P8, P12, P13, P15, P21, P22 |  |
| Stacks | 69 | [07](../research/reference/07-stack-model.md), [16](../research/reference/16-state-management.md), [18](../research/reference/18-scheduling-drift-dependencies.md), [19](../research/reference/19-events-notifications-audit.md), [21](../research/reference/21-resources-scans-search.md) | All; scheduling Business; dependencies Starter+; ignored triggers Starter+ | P5 | P4b (`stackRoleBindings`), P6, P7, P8, P9, P11, P12, P13, P15, P16, P18, P20b, P22 | Every operation is a 501 stub at the baseline; state, schedules, dependencies and webhooks land with their own phases |
| Templates | 23 | [17](../research/reference/17-blueprints-and-templates.md) | Business | P21 |  |  |
| Terraform Providers | 17 | [15](../research/reference/15-module-and-provider-registry.md) | Business | P17b |  |  |
| Tofu Workspaces | 3 | [16](../research/reference/16-state-management.md) | Not stated | P11 |  |  |
| UI State | 2 | [21](../research/reference/21-resources-scans-search.md) | UI support | P22 |  |  |
| User Guide | 20 | [04](../research/reference/04-platform-api-inventory.md) | UI support | P22 |  |  |
| VCS Agent Pools | 6 | [11](../research/reference/11-vcs-integrations.md) | Enterprise | P19b |  |  |
| VCS Integrations | 44 | [11](../research/reference/11-vcs-integrations.md) | All; aggregated checks Business; space-level integrations Enterprise | P12 | P19 (GitLab, Bitbucket, Azure DevOps) | GitHub and GitHub Enterprise first; the other providers later |
| Version Catalog | 7 | [20](../research/reference/20-vendors.md), [17](../research/reference/17-blueprints-and-templates.md) | All | P5 | P20 (`terragruntVersions`, `terragruntEffectiveVersion`, `kubectlVersions`) | Tool versions a stack may select |
| Viewer | 1 | [14](../research/reference/14-spaces-rbac-and-identity.md) | All | P3 |  |  |
| Webhooks | 9 | [19](../research/reference/19-events-notifications-audit.md) | Paid | P18 |  |  |
| Worker Pools | 19 | [10](../research/reference/10-workers-and-execution.md) | Public pool all plans; private pools Starter+ | P8 | P14, P25b | Private pools first; the public pool later, as an option |

## Untagged operations

The 8 reference operations that carry no tag.

| Operation | Phase |
|---|---|
| `identityFederationUnlink` | P23 |
| `openTofuMigrationQueueClean` | P20b |
| `openTofuMigrate` | P20b |
| `deleteOauthClientForSCIM` | P23 |
| `resetOauthClientForSCIM` | P23 |
| `userSecurityKeyDeleteAll` | P23 |
| `sessionDeleteAll` | P3 |
| `stackMigrateVendorAll` | P20b |

## Terraform resources

One row per resource of terraform-provider-spacelift v1.57.0.

| Name | Owning report | API phase | Provider phase | State at baseline | Notes |
|---|---|---|---|---|---|
| `spacelift_ai_integration` | [22](../research/reference/22-intelligence.md) | P25 | P24 | `missing` |  |
| `spacelift_api_key` | [14](../research/reference/14-spaces-rbac-and-identity.md) | P3 | P24 | `missing` |  |
| `spacelift_audit_trail_webhook` | [19](../research/reference/19-events-notifications-audit.md) | P18b | P24 | `missing` |  |
| `spacelift_aws_integration` | [12](../research/reference/12-cloud-integrations-and-oidc.md) | P15 | P24 | `missing` |  |
| `spacelift_aws_integration_attachment` | [12](../research/reference/12-cloud-integrations-and-oidc.md) | P15 | P24 | `missing` |  |
| `spacelift_aws_role` | [12](../research/reference/12-cloud-integrations-and-oidc.md) | none | none | `superseded` | Legacy per-stack role; replaced by `spacelift_aws_integration` (report 26 section 7.7); see the [supersession register](#supersession-register) |
| `spacelift_azure_devops_integration` | [11](../research/reference/11-vcs-integrations.md) | P19 | P24 | `missing` |  |
| `spacelift_azure_integration` | [12](../research/reference/12-cloud-integrations-and-oidc.md) | P15 | P24 | `missing` | Not offered on self-hosted |
| `spacelift_azure_integration_attachment` | [12](../research/reference/12-cloud-integrations-and-oidc.md) | P15 | P24 | `missing` |  |
| `spacelift_bitbucket_datacenter_integration` | [11](../research/reference/11-vcs-integrations.md) | P19 | P24 | `missing` |  |
| `spacelift_blueprint` | [17](../research/reference/17-blueprints-and-templates.md) | P21 | P24 | `missing` | Single-body blueprint; not superseded |
| `spacelift_context` | [13](../research/reference/13-configuration.md) | P6 | P24 | `missing` |  |
| `spacelift_context_attachment` | [13](../research/reference/13-configuration.md) | P6 | P24 | `missing` |  |
| `spacelift_default_runner_image` | [10](../research/reference/10-workers-and-execution.md) | P8 | P24 | `missing` | Also reads `defaultPublicWorkerPoolRunnerImage` (P14) |
| `spacelift_drift_detection` | [18](../research/reference/18-scheduling-drift-dependencies.md) | P16 | P24 | `missing` |  |
| `spacelift_environment_variable` | [13](../research/reference/13-configuration.md) | P6 | P24 | `missing` |  |
| `spacelift_gcp_service_account` | [12](../research/reference/12-cloud-integrations-and-oidc.md) | none | none | `superseded` | Calls the deprecated `stackIntegrationGcp*` mutations (report 26 section 7.6); see the [supersession register](#supersession-register) |
| `spacelift_gitlab_integration` | [11](../research/reference/11-vcs-integrations.md) | P19 | P24 | `missing` |  |
| `spacelift_idp_group_mapping` | [14](../research/reference/14-spaces-rbac-and-identity.md) | P3b | P24 | `missing` | A managed user group: calls `managedUserGroup` and `managedUserGroupDelete`, and deprecated V1 group mutations whose `V2` forms P3b serves (report 26 section 7.5); the resource is live. Logins map IdP groups onto it from P23 |
| `spacelift_module` | [15](../research/reference/15-module-and-provider-registry.md) | P17 | P24 | `missing` |  |
| `spacelift_mounted_file` | [13](../research/reference/13-configuration.md) | P6 | P24 | `missing` |  |
| `spacelift_named_webhook` | [19](../research/reference/19-events-notifications-audit.md) | P18 | P24 | `missing` |  |
| `spacelift_named_webhook_secret_header` | [19](../research/reference/19-events-notifications-audit.md) | P18 | P24 | `missing` |  |
| `spacelift_plugin` | [25](../research/reference/25-other-integrations.md) | P20b | P24 | `missing` |  |
| `spacelift_plugin_template` | [25](../research/reference/25-other-integrations.md) | P20b | P24 | `missing` |  |
| `spacelift_policy` | [09](../research/reference/09-policies.md) | P13 | P24 | `missing` |  |
| `spacelift_policy_attachment` | [09](../research/reference/09-policies.md) | P13 | P24 | `missing` |  |
| `spacelift_repo` | [11](../research/reference/11-vcs-integrations.md) | P19b | P24 | `missing` |  |
| `spacelift_repo_file` | [11](../research/reference/11-vcs-integrations.md) | P19b | P24 | `missing` | Calls the deprecated `revisionCreate`; `repoCommitCreate` replaces the call, not the resource |
| `spacelift_role` | [14](../research/reference/14-spaces-rbac-and-identity.md) | P4b | P24 | `missing` |  |
| `spacelift_role_attachment` | [14](../research/reference/14-spaces-rbac-and-identity.md) | P4b | P24 | `missing` |  |
| `spacelift_run` | [08](../research/reference/08-run-lifecycle.md) | P9 | P24 | `stub` |  |
| `spacelift_saved_filter` | [21](../research/reference/21-resources-scans-search.md) | P22 | P24 | `missing` |  |
| `spacelift_scheduled_delete_stack` | [18](../research/reference/18-scheduling-drift-dependencies.md) | P16 | P24 | `stub` |  |
| `spacelift_scheduled_run` | [18](../research/reference/18-scheduling-drift-dependencies.md) | P16 | P24 | `stub` |  |
| `spacelift_scheduled_task` | [18](../research/reference/18-scheduling-drift-dependencies.md) | P16 | P24 | `stub` |  |
| `spacelift_security_email` | [04](../research/reference/04-platform-api-inventory.md) | P3b | P24 | `missing` |  |
| `spacelift_space` | [14](../research/reference/14-spaces-rbac-and-identity.md) | P4 | P24 | `missing` |  |
| `spacelift_stack` | [07](../research/reference/07-stack-model.md) | P5 | P24 | `stub` | Also calls `stateUploadUrl` (P11) and `stackMigrateVendor` (P20b) |
| `spacelift_stack_activator` | [07](../research/reference/07-stack-model.md) | P5 | P24 | `stub` |  |
| `spacelift_stack_aws_role` | [12](../research/reference/12-cloud-integrations-and-oidc.md) | none | none | `superseded` | Deprecated alias of `spacelift_aws_role` (report 26 section 7.7); see the [supersession register](#supersession-register) |
| `spacelift_stack_dependency` | [18](../research/reference/18-scheduling-drift-dependencies.md) | P16 | P24 | `stub` |  |
| `spacelift_stack_dependency_reference` | [18](../research/reference/18-scheduling-drift-dependencies.md) | P16 | P24 | `missing` |  |
| `spacelift_stack_destructor` | [07](../research/reference/07-stack-model.md) | P5 | P24 | `missing` | Also calls `runDiscardAll` (Runs, P10) |
| `spacelift_stack_gcp_service_account` | [12](../research/reference/12-cloud-integrations-and-oidc.md) | none | none | `superseded` | Deprecated alias of `spacelift_gcp_service_account` (report 26 section 7.6); see the [supersession register](#supersession-register) |
| `spacelift_task` | [08](../research/reference/08-run-lifecycle.md) | P10 | P24 | `stub` |  |
| `spacelift_template` | [17](../research/reference/17-blueprints-and-templates.md) | P21 | P24 | `missing` |  |
| `spacelift_template_deployment` | [17](../research/reference/17-blueprints-and-templates.md) | P21 | P24 | `missing` |  |
| `spacelift_template_version` | [17](../research/reference/17-blueprints-and-templates.md) | P21 | P24 | `missing` |  |
| `spacelift_terraform_provider` | [15](../research/reference/15-module-and-provider-registry.md) | P17b | P24 | `missing` |  |
| `spacelift_user` | [14](../research/reference/14-spaces-rbac-and-identity.md) | P3b | P24 | `missing` | Calls the deprecated `managedUserInvite` (report 26 section 7.5); the resource is live |
| `spacelift_vcs_agent_pool` | [11](../research/reference/11-vcs-integrations.md) | P19b | P24 | `missing` |  |
| `spacelift_version` | [15](../research/reference/15-module-and-provider-registry.md) | P17 | P24 | `missing` |  |
| `spacelift_webhook` | [19](../research/reference/19-events-notifications-audit.md) | P18 | P24 | `missing` |  |
| `spacelift_worker_pool` | [10](../research/reference/10-workers-and-execution.md) | P8 | P24 | `missing` |  |
| `spacelift_worker_pool_recycle` | [10](../research/reference/10-workers-and-execution.md) | P8 | P24 | `missing` |  |

## Terraform data sources

One row per data source of terraform-provider-spacelift v1.57.0.

| Name | Owning report | API phase | Provider phase | State at baseline | Notes |
|---|---|---|---|---|---|
| `spacelift_account` | [04](../research/reference/04-platform-api-inventory.md) | P3 | P24 | `missing` | Also reads `tier` (P3b) and `spaceliftAwsAccountId` (P15) |
| `spacelift_ai_integration` | [22](../research/reference/22-intelligence.md) | P25 | P24 | `missing` |  |
| `spacelift_ai_integrations` | [22](../research/reference/22-intelligence.md) | P25 | P24 | `missing` |  |
| `spacelift_aws_integration` | [12](../research/reference/12-cloud-integrations-and-oidc.md) | P15 | P24 | `missing` |  |
| `spacelift_aws_integration_attachment` | [12](../research/reference/12-cloud-integrations-and-oidc.md) | P15 | P24 | `missing` |  |
| `spacelift_aws_integration_attachment_external_id` | [12](../research/reference/12-cloud-integrations-and-oidc.md) | P15 | P24 | `missing` |  |
| `spacelift_aws_integrations` | [12](../research/reference/12-cloud-integrations-and-oidc.md) | P15 | P24 | `missing` |  |
| `spacelift_aws_role` | [12](../research/reference/12-cloud-integrations-and-oidc.md) | none | none | `superseded` | Legacy per-stack role; replaced by `spacelift_aws_integration` (report 26 section 7.7); see the [supersession register](#supersession-register) |
| `spacelift_azure_devops_integration` | [11](../research/reference/11-vcs-integrations.md) | P19 | P24 | `missing` |  |
| `spacelift_azure_integration` | [12](../research/reference/12-cloud-integrations-and-oidc.md) | P15 | P24 | `missing` | Not offered on self-hosted |
| `spacelift_azure_integration_attachment` | [12](../research/reference/12-cloud-integrations-and-oidc.md) | P15 | P24 | `missing` |  |
| `spacelift_azure_integrations` | [12](../research/reference/12-cloud-integrations-and-oidc.md) | P15 | P24 | `missing` |  |
| `spacelift_bitbucket_cloud_integration` | [11](../research/reference/11-vcs-integrations.md) | P19 | P24 | `missing` |  |
| `spacelift_bitbucket_datacenter_integration` | [11](../research/reference/11-vcs-integrations.md) | P19 | P24 | `missing` |  |
| `spacelift_context` | [13](../research/reference/13-configuration.md) | P6 | P24 | `missing` |  |
| `spacelift_context_attachment` | [13](../research/reference/13-configuration.md) | P6 | P24 | `missing` |  |
| `spacelift_contexts` | [13](../research/reference/13-configuration.md) | P6 | P24 | `missing` |  |
| `spacelift_current_space` | [14](../research/reference/14-spaces-rbac-and-identity.md) | P9 | P24 | `missing` | Resolves the space of the stack or module the run belongs to |
| `spacelift_current_stack` | [07](../research/reference/07-stack-model.md), [05](../research/reference/05-terraform-provider.md) | P9 | P24 | `missing` | No API call: reads the `sub` claim of the in-run token |
| `spacelift_drift_detection` | [18](../research/reference/18-scheduling-drift-dependencies.md) | P16 | P24 | `stub` | The operation map records no call; the read reuses the resource's `stack` query [`data_drift_detection.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/data_drift_detection.go#L54-L62) |
| `spacelift_environment_variable` | [13](../research/reference/13-configuration.md) | P6 | P24 | `missing` |  |
| `spacelift_gcp_service_account` | [12](../research/reference/12-cloud-integrations-and-oidc.md) | none | none | `superseded` | Reads `stack` or `module` and the deprecated `StackIntegrations.gcp` field through the resource's read; the operation map records no call [`data_gcp_service_account.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/data_gcp_service_account.go#L62-L75) (report 26 section 7.6); see the [supersession register](#supersession-register) |
| `spacelift_github_enterprise_integration` | [11](../research/reference/11-vcs-integrations.md) | P12 | P24 | `missing` |  |
| `spacelift_gitlab_integration` | [11](../research/reference/11-vcs-integrations.md) | P19 | P24 | `missing` |  |
| `spacelift_gitlab_webhook_endpoint` | [11](../research/reference/11-vcs-integrations.md) | P19 | P24 | `missing` |  |
| `spacelift_idp_group_mapping` | [14](../research/reference/14-spaces-rbac-and-identity.md) | P3b | P24 | `missing` | Reads `managedUserGroups`; its `policy` attribute is deprecated (report 26 section 7.4) |
| `spacelift_ips` | [04](../research/reference/04-platform-api-inventory.md), [10](../research/reference/10-workers-and-execution.md) | P14 | P24 | `missing` | Outgoing addresses of the public pool |
| `spacelift_module` | [15](../research/reference/15-module-and-provider-registry.md) | P17 | P24 | `missing` |  |
| `spacelift_modules` | [15](../research/reference/15-module-and-provider-registry.md) | P17 | P24 | `missing` |  |
| `spacelift_mounted_file` | [13](../research/reference/13-configuration.md) | P6 | P24 | `missing` |  |
| `spacelift_named_webhook` | [19](../research/reference/19-events-notifications-audit.md) | P18 | P24 | `missing` |  |
| `spacelift_plugin` | [25](../research/reference/25-other-integrations.md) | P20b | P24 | `missing` |  |
| `spacelift_plugin_template` | [25](../research/reference/25-other-integrations.md) | P20b | P24 | `missing` |  |
| `spacelift_policies` | [09](../research/reference/09-policies.md) | P13 | P24 | `missing` |  |
| `spacelift_policy` | [09](../research/reference/09-policies.md) | P13 | P24 | `missing` |  |
| `spacelift_repo` | [11](../research/reference/11-vcs-integrations.md) | P19b | P24 | `missing` |  |
| `spacelift_repos` | [11](../research/reference/11-vcs-integrations.md) | P19b | P24 | `missing` |  |
| `spacelift_role` | [14](../research/reference/14-spaces-rbac-and-identity.md) | P4b | P24 | `missing` |  |
| `spacelift_role_actions` | [14](../research/reference/14-spaces-rbac-and-identity.md) | P4 | P24 | `missing` | Reads the `Action` enum by introspection |
| `spacelift_saved_filter` | [21](../research/reference/21-resources-scans-search.md) | P22 | P24 | `missing` |  |
| `spacelift_saved_filters` | [21](../research/reference/21-resources-scans-search.md) | P22 | P24 | `missing` |  |
| `spacelift_scheduled_delete_stack` | [18](../research/reference/18-scheduling-drift-dependencies.md) | P16 | P24 | `stub` |  |
| `spacelift_scheduled_run` | [18](../research/reference/18-scheduling-drift-dependencies.md) | P16 | P24 | `stub` |  |
| `spacelift_scheduled_task` | [18](../research/reference/18-scheduling-drift-dependencies.md) | P16 | P24 | `stub` |  |
| `spacelift_space` | [14](../research/reference/14-spaces-rbac-and-identity.md) | P4 | P24 | `missing` |  |
| `spacelift_space_by_path` | [14](../research/reference/14-spaces-rbac-and-identity.md) | P4 | P24 | `missing` |  |
| `spacelift_spaces` | [14](../research/reference/14-spaces-rbac-and-identity.md) | P4 | P24 | `missing` |  |
| `spacelift_stack` | [07](../research/reference/07-stack-model.md) | P5 | P24 | `stub` |  |
| `spacelift_stack_aws_role` | [12](../research/reference/12-cloud-integrations-and-oidc.md) | none | none | `superseded` | Deprecated alias of `spacelift_aws_role` (report 26 section 7.7); see the [supersession register](#supersession-register) |
| `spacelift_stack_gcp_service_account` | [12](../research/reference/12-cloud-integrations-and-oidc.md) | none | none | `superseded` | Deprecated alias of `spacelift_gcp_service_account` (report 26 section 7.6); see the [supersession register](#supersession-register) |
| `spacelift_stack_outputs` | [07](../research/reference/07-stack-model.md), [18](../research/reference/18-scheduling-drift-dependencies.md) | P10 | P24 | `stub` | Outputs come from runs; output references are report 18 |
| `spacelift_stacks` | [07](../research/reference/07-stack-model.md) | P5 | P24 | `stub` |  |
| `spacelift_template` | [17](../research/reference/17-blueprints-and-templates.md) | P21 | P24 | `missing` |  |
| `spacelift_template_deployment` | [17](../research/reference/17-blueprints-and-templates.md) | P21 | P24 | `missing` |  |
| `spacelift_template_version` | [17](../research/reference/17-blueprints-and-templates.md) | P21 | P24 | `missing` |  |
| `spacelift_tool_versions` | [20](../research/reference/20-vendors.md) | P5 | P24 | `missing` | Also reads `terragruntVersions` and `kubectlVersions` (P20) |
| `spacelift_user` | [14](../research/reference/14-spaces-rbac-and-identity.md) | P3b | P24 | `missing` | Reads `managedUsers` |
| `spacelift_vcs_agent_pool` | [11](../research/reference/11-vcs-integrations.md) | P19b | P24 | `missing` |  |
| `spacelift_vcs_agent_pools` | [11](../research/reference/11-vcs-integrations.md) | P19b | P24 | `missing` |  |
| `spacelift_webhook` | [19](../research/reference/19-events-notifications-audit.md) | P18 | P24 | `missing` |  |
| `spacelift_worker_pool` | [10](../research/reference/10-workers-and-execution.md) | P8 | P24 | `missing` |  |
| `spacelift_worker_pools` | [10](../research/reference/10-workers-and-execution.md) | P8 | P24 | `missing` |  |

## `BillingTierFeature` values

One row per value of the enum. The phase is where the capability the value
gates is delivered; P3b delivers the `features.disabled` switch, the
`x-infraweave-feature` annotations and `tierFeatures` for all of them.

| # | Value | Report 26 meaning | Phase | What the phase delivers |
|---|---|---|---|---|
| 1 | `ACCESS_POLICIES` | Per-stack and per-module access policies (`ACCESS` policy type) from the name | P3b | Superseded: rejected by `features.disabled` and absent from `tierFeatures`; see the [supersession register](#supersession-register) |
| 2 | `ADDITIONAL_PUBLIC_WORKER` | Public worker capacity above the plan default from the name | P14 | Public pool `parallelism` per account |
| 3 | `ADVANCED_SCHEDULING` | Scheduled runs, tasks and deletes from the operations gated | P16 | Scheduled runs, tasks and deletes |
| 4 | `AGGREGATED_VCS_CHECKS` | `AGGREGATED` commit checks on a VCS integration | P12 | `AGGREGATED` and `ALL` check modes |
| 5 | `API_KEYS_OIDC` | OIDC-based API keys | P3 | OIDC API keys, `@oidc` scenarios |
| 6 | `AUDITING` | The audit trail, including audit webhooks | P18b | Audit trail and audit webhook |
| 7 | `AUDIT_WEBHOOKS` | Audit webhooks; deprecated in favour of `AUDITING` (deprecated) | P3b | Superseded by `AUDITING`: rejected by `features.disabled` and absent from `tierFeatures` |
| 8 | `BLUEPRINTS` | Blueprints, and templates by shared naming | P21 | Blueprints and templates |
| 9 | `CAN_UPDATE_SSO_SAML` | Changing SAML settings after setup from the name | P23 | SAML settings updates |
| 10 | `CLOUD_CREDENTIAL_GENERATION` | Cloud credentials generated by Spacelift for runs from the name | P15 | AWS, Azure and GCP credentials for runs |
| 11 | `CUSTOM_SSO_SAML` | SAML single sign-on | P23 | SAML single sign-on |
| 12 | `DRIFT_DETECTION` | Drift detection | P16 | Drift detection integrations |
| 13 | `MFA` | Enforced multi-factor authentication | P23 | Enforced MFA and security keys |
| 14 | `MULTIPLE_VCS` | More than one integration per VCS provider from the name | P19 | More than one integration per provider and space-level integrations |
| 15 | `NOTIFICATION_POLICIES` | Notification policies and the inbox | P18 | Notification policy routing and the inbox |
| 16 | `PRIVATE_MODULE_SHARING` | Sharing private modules with other accounts from the name | P17 | `moduleShare`, `sharedModulesV2` |
| 17 | `PRIVATE_VCS` | VCS agent pools for VCS systems the internet cannot reach | P19b | VCS agent pools |
| 18 | `PRIVATE_WORKERS` | Private worker pools | P8 | Private worker pools |
| 19 | `RUN_STATE_CHANGE_WEBHOOKS` | Stack, module and named webhooks | P18 | Per-stack, per-module and named webhooks |
| 20 | `TARGETED_REPLAN` | Targeted replan of an unconfirmed run | P10 | `runTargetedReplan` |
| 21 | `TERRAFORM_PROVIDER_VERSIONS` | The private provider registry | P17b | Provider registry |
| 22 | `UNLIMITED_SEATS` | No cap on users | P3b | `seats` reports no limit |
| 23 | `WORKFLOW_POLICIES` | Policies that drive the run workflow from the name | P13 | Workflow policy types; the gated set is open (capture C48) |
| 24 | `OIDC_FEDERATION_TOKEN_GENERATION` | The OIDC token in every run, and the subject template | P15 | Run OIDC token and subject template |
| 25 | `RUN_IGNORED_TRIGGERS` | The ignored-runs list and warnings | P12 | `stackSearchRunIgnoredTriggers` and ignored-run warnings |
| 26 | `ACCOUNT_AI_CONTROL` | Account switches for Spacelift Intelligence and Infra Assistant from the operations gated | P25 | Account AI switches |
| 27 | `SPACELIFT_INTELLIGENCE` | Spacelift Intelligence: AI integrations and Intent from the operations gated | P25 | AI integrations, Infra Assistant; Intent in P25b |
| 28 | `SERVICE_NOW_INTEGRATION` | The ServiceNow external integration | P21 | ServiceNow external integration |
| 29 | `BACKSTAGE_INTEGRATION` | The Backstage plugins from the name | P21 | Backstage integration key and seat exemption (capture C257) |
| 30 | `SCIM` | SCIM v2 provisioning APIs | P23 | SCIM v2 provisioning |
| 31 | `SCANS` | Scans | P22b | Scans |

## Supersession register

Decision [0021](../research/decisions/0021-capability-scope-and-supersession.md)
leaves an interface out only when a supersession rule holds: S1 retired, S2
schema-deprecated with a replacement infraweave serves, S2a a shadowed `V1`
twin, or S3 deprecated, with a named replacement, by the source that owns an
interface the schema does not hold. Report
[26](../research/reference/26-scope-and-tiers.md#7-supersession-boundaries)
section 7 holds the evidence for each row. No phase builds these; the
replacement's phase is named so a reader can find what serves the need
instead. How the importer from Spacelift treats data that uses them is
outside decision 0021 and is not planned here.

| Interface | Kind | Rule | Replacement | Replacement phase | Evidence |
|---|---|---|---|---|---|
| `ACCESS` policies and the `STACK_ACCESS` alias, with `BillingTierFeature.ACCESS_POLICIES` | Policy type | S1 | Space role bindings | P4b | Disabled for every account on 2026-05-30; `STACK_ACCESS` deprecated on 2026-10-20 (report 26 sections 7.3 and 7.4) |
| The stack `administrative` flag | Field | S1 | A space admin role binding on the stack | P4b | Deprecated on 2025-11-10, disabled on 2026-06-01 (report 26 section 7.4) |
| `RunState.PENDING` | Enum value | S1 | None | none | "No longer used", 2026-10-20 (report 26 section 7.8) |
| `WebhooksIntegration.secret`; the `STACK_WEBHOOK_CREATE`, `STACK_WEBHOOK_UPDATE` and `STACK_WEBHOOK_DELETE` actions | Field and actions | S1 | None | none | "No longer used" and "Disabled. Do not use." in the schema (report [19](../research/reference/19-events-notifications-audit.md) section 7) |
| `changeLLMVendor`, `llmVendor` | Operations | S1 | None | none | Removed on 2026-08-21 (report 26 section 7.8) |
| `TERRAFORM_PLAN` | Policy type alias | S2 | `PLAN` | P13 | Deprecated on 2026-10-20 (report 26 section 7.3) |
| `TASK_RUN` | Policy type alias | S2 | `TASK` | P13b | Deprecated on 2026-10-20 (report 26 section 7.3) |
| `policyCreate`, `policyUpdate` | Operations | S2 | `policyCreatev2`, `policyUpdatev2` | P13 | Deprecated on 2026-07-16 (report 26 section 7.5) |
| `policySimulate` | Operation | S2 | `policySimulatev2` | P13b | Deprecated on 2026-07-16 (report 26 section 7.5) |
| The versioned-blueprint family: `blueprintVersionedGroup*`, `blueprintVersion*`, `blueprintDeployment*`, `searchBlueprintDeployments`, `searchBlueprintVersionedGroups`, `searchBlueprintVersionedGroupsSuggestions`, `Stack.blueprintDeployment` | Operations and field | S2 | Templates, template versions and template deployments | P21 | Deprecated on 2026-07-16; three members removed on 2026-10-20 (report 26 section 7.2) |
| `stackIntegrationGcpCreate`, `stackIntegrationGcpDelete`, `StackIntegrations.gcp` | Operations and field | S2 | The OIDC federation token | P15 | Deprecated on 2026-07-16 (report 26 section 7.6) |
| `ApiKeyInput.admin` | Input field | S2, through the changelog | A `space-admin` role binding on `root` | P4b | Deprecated in the changelog on 2026-07-16 (report [14](../research/reference/14-spaces-rbac-and-identity.md) section 8.1; decision 0021). Decision 0006 rejects the field, and the key's `admin` reads the binding ([DV-013](deviations.md#identity-and-access-decisions-0006-and-0007)) |
| `contextCreate`, `contextUpdate`, `managedUserGroupCreate`, `managedUserGroupUpdate`, `managedUserInvite` | Operations | S2 | Their `V2` forms | P6, P3b | Deprecated on 2026-07-16 (report 26 section 7.5) |
| `sharedModules` | Operation | S2 | `sharedModulesV2` | P17 | Deprecated on 2026-08-21 (report 26 section 7.5) |
| `revision`, `revisionCreate`, `searchRevisions`, `searchRevisionsSuggestions` | Operations | S2 | `repoCommit`, `repoCommitCreate`, `searchRepoCommits`, `searchRepoCommitsSuggestions` | P19b | Deprecated on 2026-10-20 (report 26 section 7.5) |
| `runPrioritizeSet`, `Run.isPrioritized` | Operation and field | S2 | `runPrioritySet`, `Run.priorityPreset` | P7 | Deprecated on 2026-10-20 (report 26 section 7.5) |
| `acceptedTermsAndConditionsAI`, `latestTermsAndConditionsVersionAI`, `bedrockInferenceProfiles` | Operations | S2 | `spaceliftIntelligenceTermsAccepted`, `spaceliftIntelligenceTermsLatestVersion`, `availableAIModels` | P25 | Deprecated on 2026-08-27 (report 26 section 7.5) |
| The mutation form of `templateVersionParseTemplate`, and `blueprintVersionParseTemplate` | Operations | S2 | The `templateVersionParseTemplate` query | P21 | Deprecated on 2026-07-16 (report 26 section 7.1) |
| `BillingTierFeature.AUDIT_WEBHOOKS` | Enum value | S2 | `AUDITING` | P18b | Deprecated on 2026-10-20 (report 26 section 7.5) |
| The flat hook fields on `Stack` (`afterApply`, `beforeInit` and the rest) | Fields | S2 | `Stack.hooks` | P5 | Deprecated on 2026-07-16 (report 26 section 7.5) |
| `bitbucketCloudIntegrationCreate`, `bitbucketCloudIntegrationUpdate` | Operations | S2a | Their `V2` forms | P19 | The reference routes the `V2` twin (report 26 section 7.5) |
| `moduleUpdate`, `terraformProviderVersionRegisterPlatform` | Operations | S2a | Their `V2` forms | P17, P17b | The reference routes the `V2` twin (report 26 section 7.5) |
| `spacelift_aws_role`, `spacelift_stack_aws_role` (resources and data sources) | Provider | S3 | `spacelift_aws_integration` and `spacelift_aws_integration_attachment` | P15 | Deprecated in provider v1.57.0 (report 26 section 7.7) |
| `spacelift_gcp_service_account`, `spacelift_stack_gcp_service_account` (resources and data sources) | Provider | S3 | The OIDC federation token | P15 | Deprecated in provider v1.57.0 and calls the S2 mutations above (report 26 section 7.6) |
| The `policy` block of `spacelift_user` and `spacelift_idp_group_mapping` | Provider | S3 | `spacelift_role_attachment` | P4b | Deprecated in provider v1.57.0 (report 26 section 7.4) |
| The login policy `space_admin`, `space_write` and `space_read` rules | Policy rules | S3 | The login policy `roles` rule | P13b | Deprecated in the docs (report 26 section 7.4) |
| `/intent/mcp` | Endpoint | S3 | `/mcp` | P25 | Merged into `/mcp` on 2026-04-13, announced for removal (report [22](../research/reference/22-intelligence.md) section 7; decision [0019](../research/decisions/0019-ai-provider-boundary.md)) |
| The standalone `spacelift-intent` stdio server | Client | S3 | The hosted `intent` tool, with `infraweavectl mcp` for stdio clients | P25b, P24b | The repository is archived and its README names the hosted tools (decision 0019) |
| The MQTT worker transport, its built-in broker and the IoT Core endpoint | Protocol | S3 | HTTP long-poll | P8 | Long-poll added 2026-07-20, IoT Core sunset on 2027-03-30, broker deprecated in self-hosted v7.1.0 (decisions [0008](../research/decisions/0008-run-execution-and-worker-protocol.md) and 0021; report 26 section 7.8 still keeps it and is amended when 0021 is accepted) |
| `pulumi-spacelift` | Client | S3 | Pulumi's Terraform bridge over infraweave's provider | P24 | Retirement set for 2026-12-10 (report 26 section 7.8) |

Four root fields are left out without being superseded: `groupUserEvent`,
`identifyUserEvent`, `pageUserEvent` and `trackUserEvent` are product
analytics the Spacelift web UI sends, not an API of the platform (report 26
section 7.1).

Operations backed by a service only Spacelift operates (billing, usage
reporting to Spacelift, the GitHub Marketplace app) are not superseded: they
are served, and answer from infraweave's own data or with
`installation-unsupported`, as decision 0021's vendor-bound table sets out.
Their phases are those of their tags above.

### Deprecated but served

These stay served and carry `deprecated: true`, because the schema or a
decision keeps them live (decision 0021, "Deprecated and superseded
candidates"):

| Interface | Phase | Why it stays |
|---|---|---|
| `stackIntegrationAwsCreate`, `stackIntegrationAwsDelete` | P15 | Live in the schema; `AwsIntegration.legacy` is `true` for integrations they create |
| `TerraformWorkflowTool.OPEN_TOFU`, for stacks | P5 | Live in the schema; modules and Terragrunt still need it |
| `INITIALIZATION` and `TASK` policy types | P13b | Deprecated in the docs, live in the schema |
| The `feature:add_plan_pr_comment` stack label | P12 | Decision [0012](../research/decisions/0012-vcs-integration-model.md) posts its comments beside notification policy `pull_request` rules |
| Per-stack and per-module webhooks, the per-stack Slack subscription | P18 | In scope, on the same delivery path as named webhooks (decision [0015](../research/decisions/0015-events-notifications-webhooks-audit.md)) |
| `blueprintWithGroupCreate`, `blueprintWithGroupUpdate`, `blueprintWithGroupDelete` | P21 | Carry no deprecation; whether Spacelift still supports them is unverified (report 26 section 7.2) |
