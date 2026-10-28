# 20: Vendors

Checked against: docs.spacelift.io read 2026-10-28 (local mirror, `vendors/` and the run and stack pages it relies on); introspection `b3255fe38f5e`; terraform-provider-spacelift v1.57.0 (`c72b9bd2f3c3`); runner-terraform `6c75296f90e3`, runner-opentofu `41c9f80cb86b`, runner-pulumi `6120ea0266dd`, runner-ansible `d222259098b3`; plugins `930c4509a789`.

Research baseline: **2026-10-28**.

This report records how Spacelift drives each infrastructure-as-code tool it
supports, the seven `StackVendor` values: Terraform, OpenTofu, Terragrunt,
Pulumi, CloudFormation, Kubernetes and Ansible. For each vendor it lists the
settings that matter at run time, the command each run phase executes, the
workflow tool and the custom workflow file `.spacelift/workflow.yml`, how the
tool binary and its version are chosen, the runner image, and what Spacelift
does not support. It also covers the cross-vendor features that live on the
vendor pages: cost estimation with Infracost, resource sanitization, the
migration of legacy stacks to the native OpenTofu and Terragrunt vendors, and
Terragrunt workspaces. infraweave's run executor (the part that turns a run
phase into a command line inside a runner container) is designed from this
report. The full field tables for each vendor's configuration, with defaults and
Terraform provider attributes, are in report 07; run states and phases are in
report 08; plan policy inputs are in report 09; runner image contents and
worker behaviour are in report 10; the managed state backend and external state
access are in report 16; the module and provider registries, including external
modules, are in report 15; the resources view and the Ansible configuration
management view are in report 21; plugins, including the Infracost plugin as a
packaged integration, and the Docker runner image page are in report 25. The
Spacelift Terraform provider is described in
[05-terraform-provider.md](05-terraform-provider.md).

| Label | Means |
|---|---|
| **[D]** | A docs.spacelift.io page, read 2026-10-28 |
| **[G]** | The introspection snapshot `b3255fe38f5e` ([`introspection.json`](../../../api/reference/spacelift/introspection.json)) |
| **[S]** | Source code at the cited commit |
| **[RN]** | The Spacelift changelog, <https://docs.spacelift.io/product/changelog> |
| **[I]** | Inferred; the text says from what |
| *(unverified)* | No primary source confirms it; listed in 30-open-questions |

Short names for the docs pages cited below, all read 2026-10-28:

| Short name | URL |
|---|---|
| terraform | <https://docs.spacelift.io/vendors/terraform> |
| tf-workflow | <https://docs.spacelift.io/vendors/terraform/workflow-tool> |
| tf-version | <https://docs.spacelift.io/vendors/terraform/version-management> |
| tf-terragrunt | <https://docs.spacelift.io/vendors/terraform/terragrunt> |
| cdktf | <https://docs.spacelift.io/vendors/terraform/cdktf> |
| cli-config | <https://docs.spacelift.io/vendors/terraform/cli-configuration> |
| debugging | <https://docs.spacelift.io/vendors/terraform/debugging-guide> |
| lock-file | <https://docs.spacelift.io/vendors/terraform/dependency-lock-file> |
| tfvars | <https://docs.spacelift.io/vendors/terraform/handling-tfvars> |
| complex-vars | <https://docs.spacelift.io/vendors/terraform/storing-complex-variables> |
| infracost | <https://docs.spacelift.io/vendors/terraform/infracost> |
| sanitization | <https://docs.spacelift.io/vendors/terraform/resource-sanitization> |
| opentofu | <https://docs.spacelift.io/vendors/opentofu> |
| tofu-workflow | <https://docs.spacelift.io/vendors/opentofu/workflow-tool> |
| tofu-migration | <https://docs.spacelift.io/vendors/opentofu/migration> |
| terragrunt | <https://docs.spacelift.io/vendors/terragrunt> |
| tg-start | <https://docs.spacelift.io/vendors/terragrunt/getting-started> |
| tg-limits | <https://docs.spacelift.io/vendors/terragrunt/limitations> |
| tg-reference | <https://docs.spacelift.io/vendors/terragrunt/reference> |
| tg-run-all | <https://docs.spacelift.io/vendors/terragrunt/run-all> |
| tg-tool | <https://docs.spacelift.io/vendors/terragrunt/terragrunt-tool> |
| pulumi | <https://docs.spacelift.io/vendors/pulumi> |
| pulumi-lang | <https://docs.spacelift.io/vendors/pulumi/c-sharp>, `/golang`, `/python`, `/typescript` |
| cfn | <https://docs.spacelift.io/vendors/cloudformation> |
| cfn-start | <https://docs.spacelift.io/vendors/cloudformation/getting-started> |
| cfn-reference | <https://docs.spacelift.io/vendors/cloudformation/reference> |
| cfn-cdk | <https://docs.spacelift.io/vendors/cloudformation/integrating-with-cdk> |
| cfn-sam | <https://docs.spacelift.io/vendors/cloudformation/integrating-with-sam> |
| cfn-serverless | <https://docs.spacelift.io/vendors/cloudformation/integrating-with-the-serverless-framework> |
| k8s | <https://docs.spacelift.io/vendors/kubernetes> |
| k8s-auth | <https://docs.spacelift.io/vendors/kubernetes/authenticating> |
| k8s-crd | <https://docs.spacelift.io/vendors/kubernetes/custom-resources> |
| k8s-start | <https://docs.spacelift.io/vendors/kubernetes/getting-started> |
| helm | <https://docs.spacelift.io/vendors/kubernetes/helm> |
| kustomize | <https://docs.spacelift.io/vendors/kubernetes/kustomize> |
| k8s-workflow | <https://docs.spacelift.io/vendors/kubernetes/workflow-tool> |
| ansible | <https://docs.spacelift.io/vendors/ansible> |
| ansible-start | <https://docs.spacelift.io/vendors/ansible/getting-started> |
| ansible-reference | <https://docs.spacelift.io/vendors/ansible/reference> |
| ansible-galaxy | <https://docs.spacelift.io/vendors/ansible/ansible-galaxy> |
| ansible-policies | <https://docs.spacelift.io/vendors/ansible/policies> |
| ansible-cm | <https://docs.spacelift.io/vendors/ansible/configuration-management> |
| create-stack | <https://docs.spacelift.io/concepts/stack/creating-a-stack> |
| stack-settings | <https://docs.spacelift.io/concepts/stack/stack-settings> |
| runtime-config | <https://docs.spacelift.io/concepts/configuration/runtime-configuration> |
| run | <https://docs.spacelift.io/concepts/run> |
| proposed | <https://docs.spacelift.io/concepts/run/proposed> |
| tracked | <https://docs.spacelift.io/concepts/run/tracked> |
| plan-policy | <https://docs.spacelift.io/concepts/policy/terraform-plan-policy> |
| docker | <https://docs.spacelift.io/integrations/docker> |
| worker-pools | <https://docs.spacelift.io/concepts/worker-pools> |
| liftoff-generate | <https://docs.spacelift.io/product/migrating-to-spacelift/generate> |

## 1. Vendor inventory

`StackVendor` has seven values [G]. A stack's vendor is fixed at creation; only
the migration mutations in sections 5 and 6.8 change it (report 07, section 3).

| `StackVendor` | Vendor input (`StackConfigVendorInput.*`) | Tool binary comes from | Plan phase runs | Apply phase runs | Default runner image |
|---|---|---|---|---|---|
| `Terraform` | `terraform` | Downloaded per run, verified and mounted read-only at `/bin/terraform` [D] tf-version | `terraform plan` [D] tf-workflow | `terraform apply` of the saved plan file [D] tf-workflow | `public.ecr.aws/spacelift/runner-terraform:latest` [D] docker |
| `OpenTofu` | `opentofu` | Downloaded per run, verified and mounted; not baked into the image [D] opentofu | `tofu plan` [D] tofu-workflow | `tofu apply` of the saved plan file [D] tofu-workflow | `public.ecr.aws/spacelift/runner-opentofu:latest` [D] opentofu |
| `Terragrunt` | `terragrunt` | `terragrunt` at the resolved `terragruntVersion`; how that binary reaches the container is not documented *(unverified)*. The underlying OpenTofu or Terraform is downloaded and pointed to by `TG_TFPATH` [D] tg-tool | `terragrunt plan`, or `run-all` [D] tg-run-all | A fresh plan and apply unless `skipReplan` [D] tg-limits [G] | runner-terraform, the default for every vendor except native OpenTofu [D] docker; it carries `terragrunt` v0.93.3 [S] [rt-df] |
| `Pulumi` | `pulumi` | The runner image; the docs state that Pulumi versions follow the Docker image [D] runtime-config, and `StackConfigVendorPulumi` has only `loginURL` and `stackName` [G] | `pulumi preview --refresh --diff --show-replacement-steps` [D] pulumi | `pulumi up --refresh --diff --show-replacement-steps` [D] pulumi | runner-terraform by the general default [D] docker, which has no Pulumi CLI [S] [rt-df]; the docs tell users to set a `runner-pulumi-<language>` image [D] pulumi-lang |
| `CloudFormation` | `cloudformation` | AWS APIs; no IaC binary | Uploads templates to the template bucket and builds a change set [D] cfn-reference [D] plan-policy | Applies the change and streams stack events [D] cfn-start | runner-terraform, the general default [D] docker |
| `Kubernetes` | `kubernetes` | `kubectl` downloaded at the chosen `kubectlVersion` [D] create-stack | `kubectl apply --dry-run=server` or `=client` [D] k8s-start | `kubectl apply` then `kubectl rollout status` [D] k8s-workflow | runner-terraform, the general default [D] docker; the getting-started guide asks for a custom image with `kubectl` [D] k8s-start |
| `Ansible` | `ansible` | The runner image | The playbook in Ansible check mode [D] ansible-reference | The playbook [D] ansible-reference | `public.ecr.aws/spacelift/runner-ansible:latest`, the launcher's default for Ansible runs [D] worker-pools |

Each vendor input carries the settings below. Types, defaults and Terraform
attribute names for every field are tabulated in report 07, sections 3.1 to 3.7.

| Vendor | Settings [G] | Notes |
|---|---|---|
| Terraform | `version`, `workspace`, `useSmartSanitization`, `externalStateAccessEnabled`, `workflowTool` | `StackInput.terraformVersion` takes precedence over `vendorConfig.terraform.version` [G]. |
| OpenTofu | `version`, `workspace`, `useSmartSanitization`, `externalStateAccessEnabled`, `concise`, `enableCPUProfiling`, `workflowTool`; read-only `effectiveVersion` (the highest supported match of a range, null for an exact version) and `supportedFeatures` | `version` is required; empty or `latest` is rejected [D] opentofu. |
| Terragrunt | `terraformVersion`, `terragruntVersion`, `useRunAll`, `useSmartSanitization`, `tool`, `useStateManagement`, `skipReplan`, `skipReplanWhenRunAll` (deprecated), `prefixResourceNamesWithModuleName`; read-only `effectiveVersions` | Section 6. |
| Pulumi | `loginURL`, `stackName` (both required) | Section 7. |
| CloudFormation | `entryTemplateFile`, `templateBucket`, `stackName`, `region` (all required) | Section 8. |
| Kubernetes | `namespace` (required in the input, may be empty), `kubectlVersion`, `kubernetesWorkflowTool` | Section 9. |
| Ansible | `playbook` (required) | Section 10. |

`Stack.skipVendorBinaryDownload` (also on `StackInput` and on `Module`) turns
off the binary download, for images that ship their own binary; it is API-only
and a run without the binary then fails because `tofu` is not on the `PATH`
[G] [D] opentofu.

### 1.1 Version catalogue queries

The UI and clients resolve versions with these root queries [G]:

| Query | Arguments | Returns | Purpose |
|---|---|---|---|
| `terraformVersions` | none | `[String!]!` | Terraform versions Spacelift supports |
| `terraformEffectiveVersion` | `constraints: String!` | `String!` | The version a constraint resolves to |
| `openTofuVersions` | none | `[String!]!` | Supported OpenTofu versions |
| `openTofuEffectiveVersion` | `constraints: String!` | `String!` | The version a constraint resolves to |
| `openTofuFeatures` | `version: String!` | `[OpenTofuFeatureCheck!]!` | Which OpenTofu features a version supports (section 4.3) |
| `terragruntVersions` | none | `[String!]!` | Supported Terragrunt versions |
| `terragruntEffectiveVersion` | `terragruntConstraints: String!`, `terraformConstraints: String!`, `tool: TerragruntTool` | `EffectiveTerragruntVersionResult!` | Compatible pair of Terragrunt and tool versions (section 6.1); `tool` accepts only `TERRAFORM_FOSS` or `OPEN_TOFU` and defaults to `TERRAFORM_FOSS` |
| `kubectlVersions` | none | `[String!]!` | Supported `kubectl` versions |

`Stack.effectiveTerraformVersion` is the highest version in a range that the
next run will use, and is null when the version is not a range or is not set [G].

### 1.2 Phases common to every vendor

| Phase | Behaviour | Source |
|---|---|---|
| Initializing | Runs on the worker: `before_init` hooks, then the vendor's initialization, in one shell session, so variables exported by a hook reach the vendor command. Failure fails the run. | [D] run |
| Planning | Runs the vendor's preview command; its result (managed resources, outputs, planned changes) feeds plan policies and the delta. | [D] proposed |
| Applying | Runs the vendor's apply command. Skipped without execution when `SPACELIFT_SKIP_APPLYING` is `true`. | [D] tracked |
| Destroying | The vendor's destroy command, used by module test cases and by stack deletion with resources. | [D] stack-settings; report 08 |
| Hooks | `before_`/`after_` for `init`, `plan`, `apply`, `destroy`, `perform`, plus `after_run`. | [D] stack-settings; [G] `RuntimeConfig` |

Any non-zero exit code from a workflow command is an error, so flags such as
`terraform plan -detailed-exitcode` break runs [D] tf-workflow.

## 2. Plan policy input per vendor

Only the block for the stack's vendor is populated in plan policy input; the
others are null, except `terragrunt`, which is absent for non-Terragrunt stacks
[D] plan-policy. Report 09 owns the full input document.

| Vendor | Block | Content |
|---|---|---|
| Terraform | `terraform` | `checks`, `resource_changes`, `terraform_version` |
| OpenTofu | `opentofu`, mirrored in `terraform` | `checks`, `resource_changes`, `opentofu_version` |
| Terragrunt | `terragrunt` | One entry per project with `resource_changes`, `terraform_version`, `checks` |
| Pulumi | `pulumi` | `steps` from the preview; `op` is one of `same`, `refresh`, `create`, `update`, `delete`, `create-replacement`, `delete-replaced` [D] pulumi |
| CloudFormation | `cloudformation` | `changes` from the change set |
| Ansible | `ansible` | Ansible's own output, for example `dark` for unreachable hosts [D] ansible-policies |
| Kubernetes | `kubernetes` | The vendor's own output |

Since 2026-08-18 `terraform_version` and `terraform_workflow_tool` in policy
input are null for every vendor except Terraform, including native OpenTofu;
Terraform stacks using the OpenTofu workflow tool keep both [RN]. Approval,
notification, push, task and trigger policy documents carry no `vendor` field;
initialization policies see `static_run_environment.vendor_specific_config.vendor`,
which is `"opentofu"` for native OpenTofu stacks [D] opentofu.

Run changes by phase (`Run.changesV3`, `searchEntityChanges`,
`searchEntityChangesSuggestions`) work only for Terraform, OpenTofu and
Terragrunt; since 2026-09-23 they return an error for other vendors, which use
`changesV2` [G] [RN]. Report 21 owns the entity change model.

## 3. Terraform

### 3.1 Workflow tool

`TerraformWorkflowTool` [G]:

| Value | Schema description | Docs |
|---|---|---|
| `TERRAFORM_FOSS` | Open source Terraform | Default [D] runtime-config [S] [tf-stack-wt]; Spacelift downloads the chosen version [D] tf-workflow |
| `CUSTOM` | Custom workflow tool | Commands come from `.spacelift/workflow.yml`; no version is set and nothing is downloaded [D] tf-workflow |
| `OPEN_TOFU` | OpenTofu | The docs mark this option deprecated in favour of the native OpenTofu vendor [D] tf-workflow; the schema carries no deprecation on the value [G]. Modules and Terragrunt still use it (section 4.4). The schema is authoritative for the wire contract, so the value stays valid. |

The same enum is `Module.workflowTool` [G]; the provider's `spacelift_module`
accepts `OPEN_TOFU`, `TERRAFORM_FOSS` or `CUSTOM` and defaults to
`TERRAFORM_FOSS` [S] [tf-module-wt].

### 3.2 Commands per phase

The built-in workflow is expressed as the keys of `.spacelift/workflow.yml`.
Each value is a Go template [D] tf-workflow. The built-in Terraform commands:

| Key | Used for | Built-in command | Template parameters |
|---|---|---|---|
| `init` | Initializing the root module | `terraform init -input=false` | none |
| `workspaceSelect` | Selecting the workspace; only with an external backend and the workspace setting | `terraform workspace select "{{ .WorkspaceName }}"` | `.WorkspaceName` |
| `workspaceNew` | Creating the workspace when select fails to find it; same conditions | `terraform workspace new "{{ .WorkspaceName }}"` | `.WorkspaceName` |
| `plan` | Planning | `terraform plan -input=false -lock={{ .Lock }}`, plus `-refresh=false` when `.Refresh` is false, `-out={{ .PlanFileName }}`, and one `-target='…'` per entry of `.Targets` | `.Lock`, `.Refresh`, `.PlanFileName`, `.Targets` (targeted replan, report 08) |
| `showState` | Reading state as JSON | `terraform show -json` | none |
| `showPlan` | Converting the plan file to JSON | `terraform show -json "{{ .PlanFileName }}"` | `.PlanFileName` |
| `getOutputs` | Reading outputs | `terraform output -json` | none |
| `apply` | Applying the saved plan | `terraform apply -auto-approve -input=false "{{ .PlanFileName }}"` | `.PlanFileName` |
| `destroy` | Destroying on stack deletion | `terraform destroy -auto-approve -input=false` | none |

Source: [D] tf-workflow. The apply step consumes the plan file from planning,
so the applied change is the planned change [I] from the `apply` template.

### 3.3 Custom workflow tool

| Aspect | Behaviour | Source |
|---|---|---|
| Purpose | Run a different binary through the same nine steps; the binary is third-party software the user must be licensed for | [D] tf-workflow |
| Providing the binary | A custom runner image, or a `before_init` hook that downloads it | [D] tf-workflow |
| File location | `{repository root}/.spacelift/workflow.yml`; only the `.yml` extension is read | [D] tf-workflow |
| Ways to supply the file | Committed to the repository; a mounted file in the stack's environment; a mounted file from a context (report 13) | [D] tf-workflow |
| Validation | During initialization every key must be present and non-empty, and every template is parsed; a missing key or a template syntax error fails the run then, not when the command is first used. The error names the missing keys. | [D] tf-workflow |
| Missing file | Initialization fails with an error that `source/.spacelift/workflow.yml` cannot be read | [D] tf-workflow |
| Wrong mount path | A file mounted at `/mnt/workspace/.spacelift/workflow.yml` is outside the source; the path must be under `/mnt/workspace/source/` | [D] tf-workflow |
| Missing binary | The shell exits 127 and the phase fails | [D] tf-workflow |
| Per-run override | `.spacelift/config.yml` key `terraform_workflow_tool: CUSTOM`, per stack or per module test case | [D] tf-workflow; report 07, section 7 |
| UI | Choosing Custom hides the version selector | [D] tf-workflow |

### 3.4 Extra flags without a custom tool

With the built-in tool, `TF_CLI_ARGS` and `TF_CLI_ARGS_<command>` environment
variables add flags. They are static strings (they cannot use the workspace
name), and flags cannot be added to the apply step [D] tf-workflow. The docs
use `TF_CLI_ARGS` with `-var-file=…` to load a `.tfvars` file [D] tfvars.

### 3.5 Version management

| Rule | Detail | Source |
|---|---|---|
| Supported range | Terraform 0.12.0 and later | [D] create-stack |
| Upper bound of `TERRAFORM_FOSS` | The built-in tool does not provide Terraform releases after 1.5.7; the migration docs route such stacks to `CUSTOM` with a user-supplied image | [D] liftoff-generate |
| Why 1.5.7 | The schema describes `TERRAFORM_FOSS` as the open source licensed Terraform [G]; 1.5.7 being the last such release is [I] from that description and the cutoff above | [G] [I] |
| Distribution | Neither Spacelift nor the runner image ships Terraform; each run or task downloads, verifies and mounts the binary read-only at `/bin/terraform` | [D] tf-version |
| Constraint syntax | An exact version or a range in the `blang/semver` range syntax | [D] tf-version |
| Selection order | Runtime configuration `terraform_version`, then the stack setting, then the newest supported version | [D] tf-version |
| Newest supported | Lags upstream releases while Spacelift verifies each; a faulty version may be withheld | [D] tf-version |
| Pin after apply | After a run applies with some version, Spacelift writes that version to the stack, so a later run never applies with a lower version | [D] tf-version |
| Read model | `Stack.terraformVersion` holds the admin's version or range, or the last applied version; `effectiveTerraformVersion` resolves a range | [G] |
| Provider advice | Stacks created by the provider but versioned through runtime configuration should ignore changes to `terraform_version` | [D] tf-version |

### 3.6 Other Terraform behaviour

| Topic | Behaviour | Source |
|---|---|---|
| CLI configuration | During preparation Spacelift writes `~/.terraformrc` with credentials for remote services (such as the module registry). Mounted files whose names end in `.terraformrc` are appended to it. OpenTofu reads the same file when no `.tofurc` exists, so a mounted `.tofurc` would hide Spacelift's credentials. | [D] cli-config |
| `.tfvars` | No dedicated mechanism: use `TF_CLI_ARGS*`, mounted files at autoloaded names (`terraform.tfvars`, `*.auto.tfvars`, and their `.json` forms), or a `before_init` hook that moves a file into place | [D] tfvars |
| Complex variables | Maps and lists stored as environment variables must be `jsonencode`d; Terraform decodes them without `jsondecode` | [D] complex-vars |
| Dependency lock file | `.terraform.lock.hcl` in the project root is used when present; otherwise providers resolve dynamically. A task can run `terraform init` to produce one. | [D] lock-file |
| Debugging | `TF_LOG` with `TRACE`, `DEBUG`, `INFO`, `WARN` or `ERROR`, set on the stack or a context | [D] debugging |
| CDKTF | A custom image based on runner-terraform with Node.js, `cdktf-cli` and the language toolchain; `before_init` hooks run `cdktf synth` and copy `cdktf.out/stacks/${TF_VAR_spacelift_stack_id}/cdk.tf.json` into the project root; with managed state the local backend CDKTF adds must be removed from that JSON with `jq` | [D] cdktf |
| Legacy Terragrunt | A Terraform stack labelled `terragrunt` runs `terragrunt` instead of `terraform`; deprecated, removed on 2026-10-31 (section 6.8) | [D] tf-terragrunt |

## 4. OpenTofu

OpenTofu became a dedicated stack vendor on 2026-10-09 [RN].

### 4.1 Workflow tool and commands

`OpenTofuWorkflowTool` is `OPENTOFU` (built in, the default) or `CUSTOM` [G]
[D] opentofu. Setting a version together with `CUSTOM` is rejected [D]
tofu-workflow. The `workflow.yml` keys are the nine of section 3.2, with these
differences [D] tofu-workflow:

| Key | Built-in command | Extra template parameters |
|---|---|---|
| `init` | `tofu init -input=false` | none |
| `workspaceSelect`, `workspaceNew` | `tofu workspace select` / `new "{{ .WorkspaceName }}"` | none |
| `plan` | `tofu plan -input=false -lock={{ .Lock }}` with optional `-refresh=false`, `-concise`, `-json-into="{{ .JsonIntoPath }}"`, then `-out={{ .PlanFileName }}` and the `-target` list | `.Refresh` is always true on OpenTofu stacks; `.Concise`; `.JsonIntoPath` |
| `showState`, `showPlan`, `getOutputs` | `tofu show -json`, `tofu show -json "{{ .PlanFileName }}"`, `tofu output -json` | none |
| `apply` | `tofu apply -auto-approve -input=false`, optional `-concise` and `-json-into`, then the plan file | `.Concise`, `.JsonIntoPath` |
| `destroy` | `tofu destroy -auto-approve -input=false`, optional `-concise` | `.Concise` |

A custom workflow gets neither OpenTofu-specific feature: Spacelift passes
`.Concise` as false and `.JsonIntoPath` as an empty string, skips CPU
profiling, and ignores any `-json-into` output the user's commands write, so
the matching stack settings have no effect [D] tofu-workflow. With
`workflow_tool: CUSTOM` in the runtime `opentofu` block, its `version` key is
ignored [D] tofu-workflow. Validation, file location and
troubleshooting match section 3.3; the OpenTofu page adds that the `destroy`
template is parsed too [D] tofu-workflow. Extra flags use `TF_CLI_ARGS_<command>`
with the same two limits as Terraform [D] tofu-workflow.

### 4.2 Version

| Rule | Detail | Source |
|---|---|---|
| Required | An exact version or a constraint such as `>= 1.8.0` or `~> 1.10`; empty or `latest` is rejected | [D] opentofu |
| Resolution | The highest matching version per run | [D] opentofu |
| Validation | A saved constraint is checked against the lowest version it admits; with `concise` on, any constraint admitting a version below 1.7.0 is rejected | [D] opentofu; [D] tf-version |
| Supported | All stable OpenTofu versions, with a short verification lag | [D] opentofu |
| Per-run override | Only the `opentofu` block of `.spacelift/config.yml` (`version`, `workflow_tool`); the legacy `terraform_version` and `opentofu_version` keys are not read, and if one applies to a native OpenTofu stack and no `opentofu` block sits at that level or above it, preparation fails for every run | [D] opentofu; [D] runtime-config |
| Block without `version` | The stack setting no longer applies: such a run gets the latest OpenTofu release Spacelift supports | [D] runtime-config |

### 4.3 Version-gated features

`OpenTofuFeature` [G], with the docs' minimum versions [D] opentofu:

| Value | Feature | Minimum version | Notes |
|---|---|---|---|
| `CONCISE_PLAN` | `-concise` on plan | 1.7 | |
| `CONCISE` | `-concise` on apply and destroy | 1.10 | |
| `STATE_ENCRYPTION` | State encryption | 1.7 | Report 16, section 9 |
| `OTEL_TRACING` | OpenTelemetry tracing | not in the docs table | *(unverified)*; the OpenTofu Tracing plugin (report 25) collects traces [S] [plugin-tracing] |
| `CPU_PROFILING` | `TOFU_CPU_PROFILE` | 1.11 | |
| `EPHEMERAL_RESOURCES` | Ephemeral resources and values | 1.11 | |
| `JSON_INTO` | Structured logs written by `-json-into` | 1.12 | |

`OpenTofuFeatureCheck` has `feature: OpenTofuFeature!`, `supported: Boolean!`
and `requiredVersion: String` (null when supported) [G]. A lower version still
runs; it only loses the feature [D] opentofu.

| Setting | Effect | Default on create | Source |
|---|---|---|---|
| `concise` | Removes "Refreshing state" lines from plan and per-resource progress lines (and provisioner output) from apply and destroy; errors and the final summary remain. Applies to new runs only. | On for 1.7+, off below | [D] opentofu; [G] |
| `enableCPUProfiling` | Sets `TOFU_CPU_PROFILE` and uploads one pprof profile per run state (initializing, planning, applying, destroying), merging every command of that state; the `spacelift.command` sample label separates commands. Best-effort: failure does not affect the run. | On for 1.11+, off below | [D] opentofu; [G] |

Profiles download from the run view or `Run.cpuProfileDownloadURL(state: RunState!): LogsDownloadURL`
(`url`, `ongoing`, `expired`, `deleted`) [G] [D] opentofu. Space reader is enough to download
logs and profiles [D] opentofu. The Terraform provider does not expose CPU
profiling [D] tofu-migration.

### 4.4 Other OpenTofu behaviour

| Topic | Behaviour | Source |
|---|---|---|
| `.tofu` files | Supported on 1.8+; a `.tofu` file overrides the `.tf` file of the same name; push policies see changes to `.tofu` files | [D] opentofu |
| Change documents | `entity_vendor` is `"opentofu"` for native stacks and `"terraform"` for Terraform stacks using the OpenTofu workflow tool | [D] opentofu |
| Shared pages | State management, external state access, external modules, `.tfvars`, CLI configuration, sanitization, complex variables, lock file and debugging apply unchanged | [D] opentofu |
| Scope | Stacks only. Modules keep the Terraform vendor and test with `workflowTool: OPEN_TOFU`; Terragrunt picks OpenTofu through `tool: OPEN_TOFU` | [D] opentofu |
| Runtime record | `RuntimeConfig.opentofu` is `OpenTofuRuntimeConfig`: `workflowTool`, `version` (null for `CUSTOM`), `useSmartSanitization`, `externalStateAccessEnabled`, `enableCPUProfiling`, `concise` | [G] |

The schema says concise applies to plan, apply and refresh [G]; the docs say
plan, apply and destroy [D] opentofu. The docs describe the built-in commands
and are the higher source for behaviour; the disagreement is recorded here.

## 5. Native OpenTofu migration

Migration converts a stack from `Terraform` with `workflowTool: OPEN_TOFU` to
the `OpenTofu` vendor. It changes Spacelift configuration only; runs, state and
cloud resources are untouched [D] tofu-migration.

| Carried over | Set to defaults |
|---|---|
| Version or constraint (verbatim), workspace, smart sanitization, external state access | `concise` on for 1.7+ and `enableCPUProfiling` on for 1.11+, judged by the lowest version a constraint admits, so `>= 1.0` gets both off |

Source: [D] tofu-migration.

| Precondition | Source |
|---|---|
| Space Writer or Space Admin on every stack's space | [D] tofu-migration |
| Machine-user API keys cannot migrate or roll back | [D] tofu-migration |
| Stacks with a run in progress are skipped and picked up after the run finishes | [D] tofu-migration |
| Modules, Terragrunt stacks and stacks labelled `terragrunt` are excluded | [D] tofu-migration |
| Clients must understand the vendor first: Terraform provider v1.53.5+, `spacectl` v1.26.0+, and scripts calling `stackUpdate` must send `vendorConfig.opentofu`; older clients omit the vendor on update and get an error that the vendor cannot change after creation | [D] tofu-migration; [RN] 2026-10-09 |
| Blueprints that create such stacks should switch to the `opentofu` block | [D] tofu-migration |

### 5.1 Operations

| Operation | Kind | Arguments | Returns | Behaviour |
|---|---|---|---|---|
| `openTofuMigrate` | Mutation | `input: SearchInput!`, `target: StackVendor!`, `triggerRun: Boolean` | `OpenTofuMigrateResult!` (`queued: Int!`) | Enqueues the stacks matching the predicates; `target: OpenTofu` migrates, `target: Terraform` rolls back; `triggerRun: true` starts a tracked run on each stack after it migrates [G] [D] tofu-migration |
| `openTofuMigrateSearch` | Query | `input: SearchInput!`, `target: StackVendor!` | `SearchStacksOutput!` | The stacks a migrate call with the same predicates would enqueue [G] |
| `openTofuMigrateSuggestions` | Query | `input: SearchSuggestionsInput!`, `target: StackVendor!` | `SearchSuggestionsOutput!` | Filter suggestions for that search [G] |
| `openTofuMigrationQueueSearch` | Query | `input: SearchInput!` | `OpenTofuMigrationQueueSearchOutput!` (`edges`, `pageInfo`) | Pages the queue [G] |
| `openTofuMigrationQueueSearchSuggestions` | Query | `input: SearchSuggestionsInput!` | `SearchSuggestionsOutput!` | Per-state counts that follow the filter [G] |
| `openTofuMigrationCounts` | Query | none | `OpenTofuMigrationCounts!` | Account-wide counters that ignore filters [G] |
| `openTofuMigrationQueueClean` | Mutation | none | `OpenTofuMigrationQueueCleanResult!` (`removed: Int!`) | Removes pending entries ("Clear pending entries" in the UI) [G] [D] tofu-migration |

The queue is processed asynchronously, one stack at a time [D] tofu-migration.
`OpenTofuMigrationQueueEntry` has `id`, `stack`, `state`, `note` (deprecated
for the failed issue's note), `issues: [OpenTofuMigrationIssue!]!`, `createdAt`
and `updatedAt` (Unix seconds) [G].

`OpenTofuMigrationState` [G]:

| Value | Meaning | UI label [D] tofu-migration |
|---|---|---|
| `TO_MIGRATE` | Queued for migration | Queued |
| `MIGRATED` | Migrated to the OpenTofu vendor | Migrated |
| `TO_ROLLBACK` | Queued for rollback to Terraform | Rollback queued |
| `ROLLEDBACK` | Rolled back | Rolled back |
| `FAILED` | Migration or rollback failed; re-issuing a matching `openTofuMigrate` resumes it | Failed |

`OpenTofuMigrationIssue` is a union of `OpenTofuMigrationFailedIssue` (`note`;
only on `FAILED`) and `OpenTofuMigrationWaitingForRunsIssue` (`runs`, the
oldest blocking runs up to 10, and `totalCount`; only on `TO_MIGRATE` or
`TO_ROLLBACK`, processed once the runs finish) [G].

`OpenTofuMigrationCounts` [G]:

| Field | Meaning |
|---|---|
| `eligibleToMigrate` | Stacks a predicate-free `openTofuMigrate(target: OpenTofu)` would enqueue: no entry, or a last entry in `ROLLEDBACK` or `FAILED` (failed migrations only) |
| `queuedToMigrate` | Entries in `TO_MIGRATE` |
| `queuedToRollback` | Entries in `TO_ROLLBACK` |
| `migrated` | Entries in `MIGRATED` |
| `rolledBack` | Entries in `ROLLEDBACK`; normally also eligible |
| `failed` | Entries in `FAILED`; a failed rollback is not eligible, because the stack is still `OpenTofu` |

### 5.2 Entry points and rollback

| Path | Behaviour | Source |
|---|---|---|
| Dashboard | The "Migrate stacks to native vendors" widget, OpenTofu tab, lists ready and queued stacks and leads to a page with stack filters, a preview and "Migrate N matching stacks" | [D] tofu-migration |
| Single stack | A banner on the stack's vendor settings migrates or rolls back one stack | [D] tofu-migration |
| Rollback | Restores `Terraform` with `OPEN_TOFU` and copies version, workspace, sanitization and external state access back; migrating again resets concise and profiling to defaults. Bulk rollback is API-only (`target: Terraform`). Only stacks migrated by this flow can roll back. | [D] tofu-migration |
| Provider | After migration `spacelift_stack` should use an `opentofu` block (`version`, `workspace`, `use_smart_sanitization`, `external_state_access`, `workflow_tool`, nested `logging { concise }`); with the legacy attributes it plans no change but warns that the stack is native OpenTofu | [D] tofu-migration |
| Runtime configuration | Replace `terraform_version` or `opentofu_version` with an `opentofu` block; keep `terraform_version` under individual Terraform stacks rather than `stack_defaults` in mixed repositories | [D] tofu-migration |

Which API the single-stack banner calls is *(unverified)*. The schema
describes `stackMigrateVendor` as migrating between `terragrunt`-labelled
Terraform stacks and native Terragrunt only [G], so a one-stack
`openTofuMigrate` is the likelier path [I] from that description.

## 6. Terragrunt

### 6.1 Tool and versions

`TerragruntTool` [G]:

| Value | Meaning | Source |
|---|---|---|
| `TERRAFORM_FOSS` | Open source Terraform | [G] |
| `OPEN_TOFU` | OpenTofu; the default when creating a Terragrunt stack since 2026-02-04 | [G] [RN] [D] opentofu |
| `MANUALLY_PROVISIONED` | Nothing is downloaded; the user supplies the binary by image or hook and points Terragrunt at it | [G] [D] tg-tool |

The API input and the provider both default `tool` to `TERRAFORM_FOSS` when
unset [G] [S] [tf-stack-tg], and so does the runtime `terragrunt` block
(section 6.5) [D] runtime-config; the `OPEN_TOFU` default applies to stacks
created in the UI. Report 07 records the same conflict.
For OpenTofu or Terraform, Spacelift downloads the chosen version and sets
`TG_TFPATH`, or `TERRAGRUNT_TFPATH` for Terragrunt before v0.73.0; a value the
user already set wins over the automatic one [D] tg-tool. Spacelift picks
compatible pairs using Gruntwork's version compatibility table [D] tg-reference.

`EffectiveTerragruntVersionResult` (on `StackConfigVendorTerragrunt.effectiveVersions`
and from `terragruntEffectiveVersion`) [G]:

| Field | Type | Meaning |
|---|---|---|
| `effectiveTerragruntVersion` | `String` | Resolved Terragrunt version; null for `MANUALLY_PROVISIONED` |
| `effectiveTerraformVersion` | `String` | Resolved tool version; null for `MANUALLY_PROVISIONED` |
| `tool` | `TerragruntTool!` | The tool the check used |

When the provider creates a stack without `terraform_version` or
`terragrunt_version`, the latest versions at creation are stored; removing the
fields later keeps those values; changing `tool` without `terraform_version`
resets the tool version in state [D] tg-reference.

### 6.2 Commands per phase

| Phase | Behaviour | Source |
|---|---|---|
| Initializing | `terragrunt init` (or `run-all init`) with `TG_CLI_ARGS_init` | [D] tg-reference; [I] from the per-phase variables |
| Planning | `terragrunt plan` (or `run-all plan`) with `TG_CLI_ARGS_plan`; mocked outputs may appear in the plan and so in plan policy input | [D] tg-reference; [D] tg-limits |
| Applying | Plans again and applies, ignoring the saved plan file, so mocked outputs are never applied; `TG_CLI_ARGS_apply`. With `skipReplan` the saved plan is applied as is, mocked values included. | [D] tg-limits; [D] tg-reference; [G] |
| Destroying | `TG_CLI_ARGS_destroy`; resource destruction on stack deletion is contested (section 6.6) | [D] tg-reference |

The schema's description of `skipReplanWhenRunAll` says that stacks without
run-all reuse the plan by default [G], which contradicts the limitations page's
statement that plan files are never used in apply [D] tg-limits. The schema is
the higher source for the field, but which behaviour holds for non-run-all stacks without `skipReplan` is *(unverified)*.
`skipReplan` arrived for run-all on 2026-02-20 and for every Terragrunt stack on
2026-05-25; `skipReplanWhenRunAll` is deprecated [RN].

### 6.3 Run-all

| Aspect | Behaviour | Source |
|---|---|---|
| Switch | `useRunAll: true` adds `run-all` to every Terragrunt command, honouring dependencies between units | [D] tg-run-all; [G] |
| Naming | Resource and output addresses are prefixed with the unit they came from; with run-all this is always on and `prefixResourceNamesWithModuleName` has no effect | [D] tg-run-all; [D] tg-reference |
| Prefix default | Off for new stacks, on for stacks that predate the setting | [D] tg-reference |
| Parallelism | The docs recommend `TG_PARALLELISM` to stay within public worker capacity | [D] tg-run-all |
| Explicit stacks | Supported only with run-all, which lets Terragrunt generate the backend configuration | [D] tg-limits |

### 6.4 State, workspaces and caching

With `useStateManagement`, Spacelift configures a remote backend per unit, named
by a base name plus the unit's path, and lists units on the stack's Workspaces
tab with lock status and unlock actions [D] tg-reference. Report 16, section 8,
records the backend and the `TofuWorkspace` operations
(`searchTofuWorkspaces`, `searchTofuWorkspacesSuggestions`,
`tofuWorkspaceUnlock`, `tofuWorkspaceUnlockAll`) [G]. From Terragrunt 0.89.0
with OpenTofu 1.10.0 provider caching is on, at
`/mnt/workspace/.terragrunt-provider-cache`, which survives between phases
[D] tg-reference. Spacelift sets `TG_TF_REGISTRY_TOKEN` so `tfr://spacelift.io/…`
sources authenticate against the module registry (report 15) [D] tg-reference;
the legacy label approach sets it too [D] tf-terragrunt.

### 6.5 Runtime record and per-run override

`RuntimeConfig.terragrunt` is `TerragruntRuntimeConfig`: `terraformVersion`,
`terragruntVersion: String!`, `useRunAll`, `useSmartSanitization`, `tool`,
`useStateManagement`, `prefixResourceNamesWithModuleName` [G]. It may be null
for runs older than the field [G].

The `terragrunt` block of `.spacelift/config.yml` overrides these per run
[D] runtime-config:

| Key | Default when the block is present |
|---|---|
| `terragrunt_tool` | `TERRAFORM_FOSS` (also `OPEN_TOFU`, `MANUALLY_PROVISIONED`) |
| `terragrunt_version` | Latest version |
| `terraform_version` | Latest OpenTofu or Terraform version; must be absent with `MANUALLY_PROVISIONED` |
| `use_run_all` | `false` |
| `use_smart_sanitization` | `false` |
| `use_state_management` | Not stated |

The block replaces every Terragrunt setting rather than merging with the
stack's, so a key left out falls back to its default, and it has no effect on
stacks of other vendors [D] runtime-config.

### 6.6 Limitations

| Limitation | Source |
|---|---|
| A stack cannot read another stack's state, so Terragrunt external dependencies that read a sibling's state do not work | [D] tg-limits |
| Explicit stacks without run-all: no consistent way to inject the backend | [D] tg-limits |
| Mocked outputs can reach plan policies | [D] tg-limits |
| `run_cmd` works only with `--terragrunt-quiet` | [D] tg-limits |
| Resource deletion on stack deletion: the stack page says native Terragrunt does not support it [D] create-stack; the changelog says it was implemented on 2026-02-26 [RN]. Report 07 records the docs statement. The changelog describes the later change, so deletion is probably supported, but this is *(unverified)*. | [D] [RN] |
| Debug logging: `TERRAGRUNT_LOG_LEVEL` (`info` default, `panic`, `fatal`, `error`, `warn`, `debug`, `trace`) or `--terragrunt-log-level debug` through `TF_CLI_ARGS` | [D] tf-terragrunt |

### 6.7 Choosing the tool binary for legacy Terragrunt

On the legacy label approach, the stack's Terraform version governs; Terragrunt
itself is whatever the runner image ships (v0.93.3 in runner-terraform at the
pinned commit) and a specific version needs a custom image [D] tf-terragrunt
[S] [rt-df]. Legacy runs execute `init`, `plan` and `apply` only; run-all needs
the native vendor [D] tf-terragrunt.

### 6.8 Migration from the `terragrunt` label

| Aspect | Behaviour | Source |
|---|---|---|
| Deadline | The `terragrunt` label approach is deprecated and is removed on 2026-10-31 | [D] tf-terragrunt; [D] stack-settings |
| Precondition | Stacks with a custom workflow must return to the built-in tool first, because arbitrary commands cannot be translated | [D] tf-terragrunt |
| Permission | `Stack: Update stack` on every stack migrated in bulk | [D] tf-terragrunt |
| Single stack | Stack settings offer migrate and roll back | [D] tf-terragrunt |
| Bulk | The Terragrunt tab of the dashboard widget migrates all eligible stacks, and offers roll back of all | [D] tf-terragrunt |
| Provider | Replace `labels = ["terragrunt"]`, `terraform_version` and `terraform_workflow_tool` with a `terragrunt` block; `use_state_management` mirrors the old `manage_state`; keep `prefix_resource_names_with_module_name = false` so names and stack dependencies do not change. The provider detects the change and calls `stackMigrateVendor` before `stackUpdate`. | [D] tf-terragrunt; [S] [tf-stack-migrate] |
| Read model | `Stack.wasMigratedFromTerraform` | [G] |

| Operation | Arguments | Returns | Source |
|---|---|---|---|
| `stackMigrateVendor` | `id: ID!`, `targetVendor: StackVendor!`, `config: StackVendorMigrationInput` | `Stack!` | [G] |
| `stackMigrateVendorAll` | `targetVendor: StackVendor!`, `config: StackVendorMigrationInput` | `StackMigrateVendorAllResult!` | [G] |

`StackVendorMigrationInput` has `labels: [String!]` (labels set after
migration) and `terragrunt: TerragruntMigrationInput` (`terragruntVersion:
String`) [G]. `StackMigrateVendorAllResult` has `migrated: Int!` and `left:
Int!`, the eligible stacks not migrated in this call, for example because of a
time limit; clients call again until `left` is 0 [G] [I] from the field
description. The dashboard tile `StacksAttentionTile.stackTerragruntMigration`
is `StackTerragruntMigration` with `migrated` and `readyToMigrate` [G].

## 7. Pulumi

The Pulumi page carries a feature-preview notice [D] pulumi.

| Phase | Command | Source |
|---|---|---|
| Initializing | `pulumi login <loginURL>`, then `pulumi stack select --create --select <stackName>`; pre-initialization hooks run after these | [D] pulumi |
| Planning | `pulumi preview --refresh --diff --show-replacement-steps` | [D] pulumi |
| Applying | `pulumi up --refresh --diff --show-replacement-steps` | [D] pulumi |
| Destroying | `pulumi destroy`, with `SPACELIFT_PULUMI_CLI_ARGS_destroy` | [I] from the variable name; the docs show `pulumi destroy --non-interactive --yes` only as a task [D] pulumi-lang |

The docs order (Pulumi login before pre-initialization hooks) differs from the
general rule that `before_init` hooks run first [D] run; which order the worker
uses is *(unverified)*.

| Setting or variable | Behaviour | Source |
|---|---|---|
| `loginURL` | State backend: a bucket such as `s3://…` or the Pulumi Service `https://api.pulumi.com` with `PULUMI_ACCESS_TOKEN` | [D] pulumi-lang |
| `stackName` | The Pulumi stack that namespaces state; independent of the Spacelift stack name | [D] create-stack |
| `SPACELIFT_PULUMI_CLI_ARGS_preview`, `_up`, `_destroy` | Extra CLI arguments per command | [D] pulumi |
| Secrets | Pulumi secrets appear as `[secret]`; no further string sanitization is applied to Pulumi plans | [D] pulumi |
| Language toolchains | Set by the image: `runner-pulumi-javascript`, `-golang`, `-python`, `-dotnet`, tagged by Pulumi version such as `v2.15.4` or `latest` | [D] pulumi-lang; [S] [rp-readme] |
| Dependencies | `before_init` hooks such as `npm install` or `pip install -r requirements.txt`; `before_apply` is needed when the apply phase loses installed state, set through `.spacelift/config.yml`; .NET needs `NUGET_PACKAGES` under `/mnt/workspace` | [D] pulumi-lang |

Unsupported: module CI/CD (the module registry is Terraform only) and state
import; resources are imported with a task [D] pulumi. The Pulumi provider for
managing Spacelift itself (`pulumi-spacelift`) is a separate matter, retired on
2026-12-10 (report 06) [RN].

## 8. CloudFormation

| Setting | Behaviour | Source |
|---|---|---|
| `region` | AWS region of the CloudFormation stack | [D] cfn-reference |
| `stackName` | Name of the CloudFormation stack, unique in the account | [D] cfn-start |
| `entryTemplateFile` | Path to the root template, JSON or YAML; for generated templates, the generated file | [D] cfn-reference |
| `templateBucket` | S3 bucket for processed templates; each object is prefixed with the run ID (`<run id>/<artifact>`); Spacelift does not create it; a name containing `.` breaks the upload | [D] cfn-reference; [D] cfn |

| Phase | Behaviour | Source |
|---|---|---|
| Planning | Packages the entry template (the provider describes `entry_template_file` as the file `cloudformation package` runs on), uploads templates to the bucket and creates a change set; the change set's `changes` feed plan policies | [S] [tf-stack-cfn]; [D] cfn-reference; [D] plan-policy |
| Applying | Executes the change and shows creation and update events of the root and nested stacks as they occur | [D] cfn-start; [I] that execution uses the change set |
| Deleting | Not documented for stack deletion; the docs give `aws cloudformation delete-stack --stack-name <name>` as a task to run before deleting the stack | [D] create-stack |

| Variable | Behaviour | Source |
|---|---|---|
| `CF_PARAM_<name>` | Value of template parameter `<name>` | [D] cfn-reference |
| `CF_CAPABILITY_<name>` set to `1` | Acknowledges a capability; `IAM`, `NAMED_IAM`, `AUTO_EXPAND` | [D] cfn-reference |
| `CF_METADATA_REGION`, `CF_METADATA_STACK_NAME`, `CF_METADATA_ENTRY_TEMPLATE_FILE`, `CF_METADATA_TEMPLATE_BUCKET` | Computed each run from the stack settings, for hooks | [D] cfn-reference |

Credentials come from the AWS integration (report 12), worker ambient
credentials or environment variables. Separate read and write roles are not
possible because planning uploads templates, which needs the write role
[D] cfn-reference.

Drift detection uses AWS `DetectStackDrift`, but CloudFormation stacks are
excluded from drift reconciliation (report 18) [D] cfn.

| Framework | How it runs | Limits | Source |
|---|---|---|---|
| AWS CDK | Custom image from runner-terraform with `npm`, `aws-cdk` and the language toolchain; `before_plan` hooks run `cdk bootstrap` and `cdk synth` | Only one template is picked up; assets are not uploaded because `cdk deploy` is not used, so Lambda deployments fail | [D] cfn-cdk |
| AWS SAM | Custom image with `aws-sam-cli` in a virtualenv; a `before_init` hook runs `sam package` with `CF_METADATA_REGION`, `CF_METADATA_TEMPLATE_BUCKET` and `--output-template-file ${CF_METADATA_ENTRY_TEMPLATE_FILE}` | None stated | [D] cfn-sam |
| Serverless Framework | Custom image with `serverless`; `before_init` runs `serverless package`, a mounted script copies the artifact to the template bucket, `serverless.yml` sets `provider.deploymentBucket`, and `entryTemplateFile` is `.serverless/cloudformation-template-update-stack.json` | None stated | [D] cfn-serverless |

The plugins repository also has an `aws_sam` plugin (report 25) [S] [plugins-tree].

## 9. Kubernetes

`KubernetesWorkflowTool` is `KUBERNETES` (kubectl, the provider default) or
`CUSTOM` [G] [S] [tf-stack-k8s].

### 9.1 Commands

The built-in workflow, as `workflow.yml` keys [D] k8s-workflow:

| Key | Used for | Built-in command (abridged) | Template parameters |
|---|---|---|---|
| `createFromStdin` | A `SelfSubjectAccessReview` authorization check | `kubectl create -f - -o yaml` | none |
| `apiResources` | Listing listable and creatable API resources | `kubectl api-resources [--no-headers] --verbs=list,create [--namespaced] -o <format>` | `.NoHeaders`, `.IsNamespaced`, `.OutputFormat` (`name` or `wide`) |
| `apply` | Both planning (dry run) and applying | `kubectl apply -k ./ --prune <allowlist> -l "spacelift-stack=<slug>" --dry-run=<strategy> [-o <format>] [--namespace=<ns>]` | `.PruneWhiteList`, `.StackSlug`, `.DryRunStrategy` (`none`, `client`, `server`), `.OutputFormat` (empty or `json`), `.Namespace` |
| `rolloutStatus` | Waiting for rollouts | `kubectl rollout status [--namespace] --timeout <n>s -w <id>` | `.Namespace`, `.RolloutTimeoutInSeconds`, `.Id` |
| `getPods` | Showing pod status during deployment | `kubectl get pods [--namespace or --all-namespaces] -w -l '<selector>'` | `.Namespace`, `.Pods` |
| `delete` | Removing resources on stack deletion | `kubectl delete --ignore-not-found -l spacelift-stack=<slug> [--namespace or --all-namespaces] <resources>` | `.StackSlug`, `.Namespace`, `.ResourcesToDelete` |
| `get` | Reading managed resources | `kubectl get -o json --show-kind --ignore-not-found -l spacelift-stack=<slug> […] <kinds>` | `.StackSlug`, `.Namespace`, `.ResourceKindsToGet` |

| Behaviour | Detail | Source |
|---|---|---|
| Planning | `--dry-run=server` when the resources exist, `--dry-run=client` when none do | [D] k8s-start |
| Applying | Default rollout timeout 10 minutes; `KUBECTL_ROLLOUT_TIMEOUT` changes it | [D] k8s-start |
| Pruning | `--prune` with the stack label removes resources whose YAML left the repository; at least one YAML file must remain or the run fails | [D] k8s-start |
| Prune flag | `.PruneWhiteList` emits `--prune-allowlist`; the label `feature:k8s_keep_using_prune_white_list_flag` switches to `--prune-whitelist` for kubectl below 1.26 | [D] k8s-workflow |
| Labels | Every resource gets `spacelift-stack: <stack-slug>` and `app.kubernetes.io/managed-by: spacelift`; all operations select on the first. A slug over 63 characters is truncated and suffixed with a hash. | [D] kustomize |
| Kustomize | Without a `kustomization.yaml`, Spacelift writes one referencing every YAML file under the project root; with one, it only adds a label transformer | [D] kustomize |
| Namespace | Optional; empty means a multi-namespace stack; the docs advise against leaving it blank because of the privileges needed | [D] create-stack; [D] k8s-start |
| Debug | `SPACELIFT_KUBERNETES_DEBUG=1` | [D] k8s-workflow |
| Custom workflow | Same file location and the same three ways to supply it as section 3.3; a missing or empty command fails validation; the version selector disappears. Unlike the Terraform and OpenTofu pages, the Kubernetes page says nothing about parsing every template up front or reading only `.yml` | [D] k8s-workflow |

### 9.2 Authentication

`kubectl` uses its native configuration. Spacelift sets `KUBECONFIG` to
`/mnt/workspace/.kube/config`; a mounted file or a `before_init` hook supplies
it [D] k8s-auth. The docs give hook recipes: `aws eks update-kubeconfig` for EKS;
`az login` (service principal from the Azure integration's `ARM_*` variables, or
managed identity on private workers), `az aks get-credentials` and `kubelogin
convert-kubeconfig` for AKS; `gcloud container clusters get-credentials` with
the GCP integration's `GOOGLE_OAUTH_ACCESS_TOKEN`, or with
`GOOGLE_APPLICATION_CREDENTIALS` under OIDC, for GKE [D] k8s-auth. The Azure and
GCP recipes need custom images with their CLIs [D] k8s-auth.

### 9.3 Helm and custom resources

There is no native Helm support. A `before_plan` hook runs `helm template` into a
file that a committed `kustomization.yaml` lists; the image must have Helm or a
`before_init` hook installs it; charts that need the full Helm lifecycle may not
work [D] helm. Custom resources are supported [D] k8s-crd.

## 10. Ansible

| Phase | Behaviour | Source |
|---|---|---|
| Initializing | Installs dependencies with Ansible Galaxy from the first `requirements.yml` or `.yaml` found at the root, `roles/` or `collections/`, or from `SPACELIFT_ANSIBLE_REQUIREMENTS_FILE` | [D] ansible-galaxy |
| Planning | Runs the playbook in check mode; modules without check-mode support can fail it. `SPACELIFT_SKIP_PLANNING=true` skips the phase. | [D] ansible-reference |
| Applying | Runs the playbook | [D] ansible-reference |
| Command | `ansible-playbook` [I] from the CLI-flags reference linking `ansible-playbook` options; the runner image installs `ansible-runner` [S] [ra-df] | |

| Setting or variable | Behaviour | Source |
|---|---|---|
| `playbook` | The playbook file, relative to the project root | [D] ansible-reference; [D] ansible-start |
| `SPACELIFT_ANSIBLE_CLI_ARGS`, `_plan`, `_apply` | Extra flags for both phases or one; both apply when set | [D] ansible-reference |
| `SPACELIFT_IGNORE_SKIPPED_TASK_CHANGES=true` | Skipped tasks are subtracted from the change count, so runs with only skipped tasks can autodeploy | [D] ansible-reference |
| `ANSIBLE_PRIVATE_KEY_FILE`, `ANSIBLE_FORCE_COLOR`, `ANSIBLE_VERBOSITY`, `ANSIBLE_SSH_CONTROL_PATH_DIR` (default `/tmp/.ansible/cp`); `ANSIBLE_CONFIG` pointing at a repository `ansible.cfg` | Ansible's own settings | [D] ansible-reference; [D] ansible-start for `ANSIBLE_CONFIG` |
| Key permissions | Mounted files are world-readable, so SSH rejects a mounted private key; a `chmod 400` hook is needed before both plan and apply | [D] ansible-start |

Plan policies receive Ansible's output under `ansible`, for example `dark` for
unreachable hosts, which a policy can turn into a warning and an approval
requirement [D] ansible-policies. Ansible stacks show the configuration
management view (hosts, tasks, task executions) instead of the resources view;
report 21 owns it [D] ansible-cm. Ansible has no managed state (report 16).

## 11. Runner images

Report 10, section 6, records image contents. Summary at the pinned commits:

| Repository | Images | Contents relevant to vendors | Source |
|---|---|---|---|
| runner-terraform `6c75296f90e3` | `runner-terraform:latest` (aws CLI), `gcp-latest`, `azure-latest`, each with `-fips`; amd64 and arm64 | Alpine 3.24 (the `gcp` flavour builds on Google's `google-cloud-cli:alpine`), `infracost`, `terragrunt` v0.93.3, `spaceforge`, git, jq, python3; no Terraform binary | [S] [rt-df]; [S] [rt-readme]; [S] [rt-bake] |
| runner-opentofu `41c9f80cb86b` | Same three cloud flavours | As runner-terraform without `terragrunt`; no `tofu` binary | [S] [ro-df] |
| runner-pulumi `6120ea0266dd` | `runner-pulumi-javascript`, `-golang`, `-python`, `-dotnet`, tagged by Pulumi version and `latest` | Pulumi CLI on a runner-terraform or Debian base | [S] [rp-readme] |
| runner-ansible `d222259098b3` | `runner-ansible:<version>`, `-aws`, `-gcp`, `-azure` | `ansible` (default 10.7), `ansible-runner` 2.4, collections per cloud | [S] [ra-df] |

Terraform and OpenTofu images exclude the binary because Spacelift mounts the
run's version [D] docker. Kubernetes, CloudFormation and the CDK, SAM and
Serverless integrations have no dedicated image: they run on runner-terraform
by default [D] docker, and the docs build custom images from it [D] cfn-cdk
[D] cfn-sam [D] cfn-serverless.

## 12. Cost estimation (Infracost)

| Aspect | Behaviour | Source |
|---|---|---|
| Enabling | Label the stack `infracost` and set `INFRACOST_API_KEY`, typically from a context | [D] infracost |
| When it runs | An Infracost breakdown runs during planning and applying; initialization logs confirm it is enabled | [D] infracost |
| Extra arguments | `INFRACOST_CLI_ARGS` is appended to the breakdown command, for example a usage file | [D] infracost |
| Failure | Fails the run by default; `INFRACOST_WARN_ON_FAILURE=true` turns failures into warnings | [D] infracost |
| Pull requests | The cost summary is posted to the PR | [D] infracost |
| Plan policies | The breakdown JSON is at `third_party_metadata.infracost`: `projects[]` with `breakdown`, `diff`, `pastBreakdown` (`totalHourlyCost`, `totalMonthlyCost` as strings), `metadata`, `path`; plus top-level `resources`, `summary.unsupportedResourceCounts`, `timeGenerated`, `totalHourlyCost`, `totalMonthlyCost`, `version` | [D] infracost |
| Resources view | Per-resource cost (report 21) | [D] infracost |
| Binary | `infracost` from its latest release is baked into runner-terraform and runner-opentofu | [S] [rt-df]; [S] [ro-df] |
| Vendors | Terraform, OpenTofu and Terragrunt only. For Terraform and OpenTofu `third_party_metadata.infracost` holds the report itself; for Terragrunt it is keyed by project; no other vendor produces it, and the key is absent when no report was made | [D] plan-policy |

The Infracost plugin (report 25) is an alternative packaging: a context with
`INFRACOST_API_KEY` and an `after_plan` hook running `infracost breakdown --path
. --out-file infracost.custom.spacelift.json --format json`, plus a `PLAN`
policy that reads `third_party_metadata.infracost` [S] [plugin-infracost]. A
`<key>.custom.spacelift.json` file is merged at
`third_party_metadata.custom.<key>` [D] plan-policy, so the plugin's file would
land at `custom.infracost`, not at the path its policy reads [I] from comparing
the two; the actual path is *(unverified)*.

## 13. Resource sanitization

| Mode | Behaviour | Source |
|---|---|---|
| Default | Every string value in plan policy input and the resources view is replaced by a hash. The policy page describes it as the last 8 bytes of a SHA-256 hash; the examples show 8 hexadecimal characters, which is 4 bytes, so the exact truncation is *(unverified)* | [D] sanitization; [D] plan-policy |
| Smart (`useSmartSanitization`) | Only values Terraform or OpenTofu marks sensitive are hashed; needs Terraform 1.0.1 or later and fails on older versions. On by default for new OpenTofu stacks. | [D] sanitization; [D] opentofu |
| Disabled | Label `feature:disable_resource_sanitization`; future runs are not sanitized | [D] sanitization |
| Policy helper | `sanitized(string)` hashes a constant for comparison; needed under default mode, and under smart mode only for sensitive values | [D] sanitization |
| Scope | Terraform, OpenTofu and Terragrunt have the setting [G]; Pulumi relies on Pulumi's `[secret]` encoding instead [D] pulumi | |
| Terragrunt `checks` | Empty unless sanitization is disabled | [D] plan-policy |

Report 09 covers how policies see sanitized values.

## 14. Unsupported features by vendor

| Vendor | Not supported | Source |
|---|---|---|
| Terraform | Flags on apply through `TF_CLI_ARGS_apply`; workspace templating in `TF_CLI_ARGS`; non-zero-exit flags; versions below 0.12.0; smart sanitization below 1.0.1 | [D] tf-workflow; [D] create-stack; [D] sanitization |
| OpenTofu | Modules as an OpenTofu vendor; concise, CPU profiling and `-json-into` with a custom tool; empty or `latest` versions; legacy version keys in runtime configuration | [D] opentofu; [D] tofu-workflow |
| Terragrunt | Cross-stack state reads; explicit stacks without run-all; `run_cmd` without `--terragrunt-quiet`; custom workflows during migration; resource deletion per the stack page (section 6.6) | [D] tg-limits; [D] tf-terragrunt; [D] create-stack |
| Pulumi | Module CI/CD; state import | [D] pulumi |
| CloudFormation | Bucket names with `.`; separate read and write roles; drift reconciliation; multi-template CDK apps; CDK asset upload | [D] cfn; [D] cfn-reference; [D] cfn-cdk |
| Kubernetes | Native Helm; a repository with no YAML file; `--prune-allowlist` on kubectl below 1.26 without the label | [D] helm; [D] k8s-start; [D] k8s-workflow |
| Ansible | Check mode for modules that lack it (skip planning instead); managed state | [D] ansible-reference; report 16 |

## 15. Page coverage

Every page under `vendors/` and the report that covers it:

| Page | Covered in |
|---|---|
| `vendors/terraform`, `terraform/workflow-tool`, `version-management`, `cli-configuration`, `debugging-guide`, `dependency-lock-file`, `handling-tfvars`, `storing-complex-variables`, `cdktf` | This report, section 3 |
| `terraform/terragrunt` | This report, sections 6.7 and 6.8 |
| `terraform/infracost`, `terraform/resource-sanitization` | This report, sections 12 and 13 |
| `terraform/module-registry`, `terraform/provider-registry`, `terraform/external-modules` | Report 15 |
| `terraform/state-management`, `terraform/external-state-access` | Report 16 |
| `terraform/terraform-provider` | [05-terraform-provider.md](05-terraform-provider.md) |
| `vendors/opentofu`, `opentofu/workflow-tool` | This report, section 4 |
| `opentofu/migration` | This report, section 5 |
| `vendors/terragrunt` and its five subpages | This report, section 6 |
| `vendors/pulumi` and its four language pages | This report, section 7 |
| `vendors/cloudformation` and its five subpages | This report, section 8 |
| `vendors/kubernetes` and its six subpages | This report, section 9 |
| `vendors/ansible`, `getting-started`, `reference`, `policies`, `ansible-galaxy` | This report, section 10 |
| `ansible/configuration-management` | Report 21; summarized in section 10 |

Self-hosted v7.1.0 carries every page under `vendors/`, including native OpenTofu and
the OpenTofu migration page. Compared line by line with blank lines ignored, only four
pages differ from SaaS [D] (each SaaS page against its twin under
<https://docs.spacelift.io/self-hosted/latest/vendors/terraform>, `.../opentofu`, `.../kubernetes` and so on, read 2026-10-28):

| Page | Self-hosted difference |
|---|---|
| `kubernetes/getting-started`, `kubernetes/authenticating` | The Azure (AKS) and GCP (GKE) authentication sections and links are absent; AWS (EKS) remains |
| `terraform/terraform-provider` | Adds a version-compatibility section: the provider depends on API features, so self-hosted users pin a provider version known to work with their release; report 05 owns it |
| `terraform/module-registry`, `provider-registry`, `external-modules` | Tier notes and host names; report 15 owns them |

That a page exists in the self-hosted set shows the feature is documented for v7.1.0,
not that it behaves identically [I].

## What this means for infraweave

- **Seven vendors, one executor contract.** The plan must cover a per-vendor
  command set for init, plan, show, outputs, apply and destroy, the
  `.spacelift/workflow.yml` key sets for Terraform/OpenTofu (nine keys) and
  Kubernetes (seven keys) with their Go template parameters, and validation of
  a custom file during initialization.
- **Binary provisioning.** Terraform, OpenTofu, Terragrunt's underlying tool
  and `kubectl` are downloaded, verified and mounted per run from a version
  catalogue with constraint resolution; runner images do not carry them;
  `skipVendorBinaryDownload` opts out. A version catalogue API
  (`*Versions`, `*EffectiveVersion`, `openTofuFeatures`) is part of the surface.
- **Version rules.** The built-in Terraform tool ends at 1.5.7, so later
  Terraform needs a custom tool and image; Terraform pins the applied version back to the stack;
  OpenTofu validates constraints against their lowest admitted version and
  gates features by the resolved version.
- **Migration machinery.** An asynchronous, resumable OpenTofu migration queue
  with five states, issues, counters and rollback, and a synchronous Terragrunt
  migration with a bulk call that reports `left`. Deadlines: the `terragrunt`
  label ends on 2026-10-31.
- **Vendor-specific policy input and change models**, including Terragrunt
  per-project blocks, OpenTofu mirroring into `terraform`, and the restriction
  of `changesV3` to Terraform-family vendors.
- **Cross-vendor features.** Infracost (label, environment, PR comment, policy
  input, per-resource cost), three sanitization modes with a stable hash, and
  Kubernetes label-scoped pruning.
- **Runner images.** Equivalents for the four runner repositories, or
  documented compatibility with Spacelift's images, including UID 1983 and the
  `/mnt/workspace` layout (report 10).

## Inferred and undocumented

- Neither the Kubernetes nor the CloudFormation page names a runner image; the
  runner-terraform default comes from the general Docker page [D] docker.
- Pulumi destroy runs `pulumi destroy` [I] from `SPACELIFT_PULUMI_CLI_ARGS_destroy`.
- `TERRAFORM_FOSS` stops at Terraform 1.5.7 because later releases are not
  open source [I] from the schema's description of the value and the liftoff
  docs' cutoff.
- A one-stack `openTofuMigrate` backs the single-stack banner [I] from
  `stackMigrateVendor` being described as Terragrunt-only.
- CloudFormation apply executes the change set built in planning [I].
- Ansible runs `ansible-playbook` [I].
- Terragrunt init runs `terragrunt init` [I] from `TG_CLI_ARGS_init`.
- `StackMigrateVendorAllResult.left` implies repeated calls [I].
- Unverified: whether Pulumi login precedes `before_init` hooks; the
  non-run-all Terragrunt apply behaviour without `skipReplan`; native Terragrunt
  resource deletion; `OTEL_TRACING`'s minimum version; which API the
  single-stack OpenTofu migration uses; where the Infracost plugin's output
  lands in policy input; how the native Terragrunt vendor provides the
  `terragrunt` binary at the chosen version.

## Versions at the research baseline

| Component | Version |
|---|---|
| Introspection | `b3255fe38f5e` |
| terraform-provider-spacelift | v1.57.0 (`c72b9bd2f3c3`); `opentofu` block from v1.53.5 [D] tofu-migration |
| spacectl | OpenTofu vendor support from v1.26.0 [D] tofu-migration |
| runner-terraform | `6c75296f90e3` (Alpine 3.24, Terragrunt v0.93.3) |
| runner-opentofu | `41c9f80cb86b` (Alpine 3.24) |
| runner-pulumi | `6120ea0266dd` |
| runner-ansible | `d222259098b3` (Ansible 10.7 default, ansible-runner 2.4) |
| plugins | `930c4509a789` (Infracost plugin 1.0.3, OpenTofu Tracing plugin 2.0.4) |
| Terraform | 0.12.0 minimum supported [D] create-stack |
| OpenTofu features | 1.7, 1.10, 1.11, 1.12 gates (section 4.3) |

## Sources

Docs, read 2026-10-28:
- <https://docs.spacelift.io/vendors/terraform>
- <https://docs.spacelift.io/vendors/terraform/workflow-tool>
- <https://docs.spacelift.io/vendors/terraform/version-management>
- <https://docs.spacelift.io/vendors/terraform/terragrunt>
- <https://docs.spacelift.io/vendors/terraform/cdktf>
- <https://docs.spacelift.io/vendors/terraform/cli-configuration>
- <https://docs.spacelift.io/vendors/terraform/debugging-guide>
- <https://docs.spacelift.io/vendors/terraform/dependency-lock-file>
- <https://docs.spacelift.io/vendors/terraform/handling-tfvars>
- <https://docs.spacelift.io/vendors/terraform/storing-complex-variables>
- <https://docs.spacelift.io/vendors/terraform/infracost>
- <https://docs.spacelift.io/vendors/terraform/resource-sanitization>
- <https://docs.spacelift.io/vendors/opentofu>
- <https://docs.spacelift.io/vendors/opentofu/workflow-tool>
- <https://docs.spacelift.io/vendors/opentofu/migration>
- <https://docs.spacelift.io/vendors/terragrunt>
- <https://docs.spacelift.io/vendors/terragrunt/getting-started>
- <https://docs.spacelift.io/vendors/terragrunt/limitations>
- <https://docs.spacelift.io/vendors/terragrunt/reference>
- <https://docs.spacelift.io/vendors/terragrunt/run-all>
- <https://docs.spacelift.io/vendors/terragrunt/terragrunt-tool>
- <https://docs.spacelift.io/vendors/pulumi>
- <https://docs.spacelift.io/vendors/pulumi/c-sharp>
- <https://docs.spacelift.io/vendors/pulumi/golang>
- <https://docs.spacelift.io/vendors/pulumi/python>
- <https://docs.spacelift.io/vendors/pulumi/typescript>
- <https://docs.spacelift.io/vendors/cloudformation>
- <https://docs.spacelift.io/vendors/cloudformation/getting-started>
- <https://docs.spacelift.io/vendors/cloudformation/reference>
- <https://docs.spacelift.io/vendors/cloudformation/integrating-with-cdk>
- <https://docs.spacelift.io/vendors/cloudformation/integrating-with-sam>
- <https://docs.spacelift.io/vendors/cloudformation/integrating-with-the-serverless-framework>
- <https://docs.spacelift.io/vendors/kubernetes>
- <https://docs.spacelift.io/vendors/kubernetes/authenticating>
- <https://docs.spacelift.io/vendors/kubernetes/custom-resources>
- <https://docs.spacelift.io/vendors/kubernetes/getting-started>
- <https://docs.spacelift.io/vendors/kubernetes/helm>
- <https://docs.spacelift.io/vendors/kubernetes/kustomize>
- <https://docs.spacelift.io/vendors/kubernetes/workflow-tool>
- <https://docs.spacelift.io/vendors/ansible>
- <https://docs.spacelift.io/vendors/ansible/getting-started>
- <https://docs.spacelift.io/vendors/ansible/reference>
- <https://docs.spacelift.io/vendors/ansible/policies>
- <https://docs.spacelift.io/vendors/ansible/ansible-galaxy>
- <https://docs.spacelift.io/vendors/ansible/configuration-management>
- <https://docs.spacelift.io/concepts/stack/creating-a-stack>
- <https://docs.spacelift.io/concepts/stack/stack-settings>
- <https://docs.spacelift.io/concepts/configuration/runtime-configuration>
- <https://docs.spacelift.io/concepts/run>
- <https://docs.spacelift.io/concepts/run/proposed>
- <https://docs.spacelift.io/concepts/run/tracked>
- <https://docs.spacelift.io/concepts/policy/terraform-plan-policy>
- <https://docs.spacelift.io/integrations/docker>
- <https://docs.spacelift.io/concepts/worker-pools>
- <https://docs.spacelift.io/product/migrating-to-spacelift/generate>
- <https://docs.spacelift.io/product/changelog> (entries 2026-02-04, 2026-02-20, 2026-02-26, 2026-05-25, 2026-06-10, 2026-07-16, 2026-08-18, 2026-09-23, 2026-10-09)

Schema: [`introspection.json`](../../../api/reference/spacelift/introspection.json)
at `b3255fe38f5e`: `StackVendor`, `TerraformWorkflowTool`,
`OpenTofuWorkflowTool`, `KubernetesWorkflowTool`, `TerragruntTool`,
`OpenTofuFeature`, `OpenTofuFeatureCheck`, `OpenTofuMigrationState`,
`OpenTofuMigrationQueueEntry`, `OpenTofuMigrationIssue`,
`OpenTofuMigrationCounts`, `OpenTofuMigrateResult`,
`OpenTofuMigrationQueueCleanResult`, `OpenTofuMigrationQueueSearchOutput`,
`EffectiveTerragruntVersionResult`, `StackVendorMigrationInput`,
`TerragruntMigrationInput`, `StackMigrateVendorAllResult`,
`StackTerragruntMigration`, `RuntimeConfig`, `TerraformRuntimeConfig`,
`OpenTofuRuntimeConfig`, `TerragruntRuntimeConfig`, `StackConfigVendor*`,
`SearchTofuWorkspacesOutput`, `TofuWorkspace`, and the root fields in sections
1.1, 5.1 and 6.8. Tables generated with `python3 -I` from the snapshot.

Source code:
- `rt-df`: <https://github.com/spacelift-io/runner-terraform/blob/6c75296f90e3/Dockerfile>
- `rt-readme`: <https://github.com/spacelift-io/runner-terraform/blob/6c75296f90e3/README.md>
- `rt-bake`: <https://github.com/spacelift-io/runner-terraform/blob/6c75296f90e3/docker-bake.hcl#L7-L11>
- `ro-df`: <https://github.com/spacelift-io/runner-opentofu/blob/41c9f80cb86b/Dockerfile>
- `rp-readme`: <https://github.com/spacelift-io/runner-pulumi/blob/6120ea0266dd/README.md>
- `ra-df`: <https://github.com/spacelift-io/runner-ansible/blob/d222259098b3/Dockerfile>
- `tf-stack-cfn`: <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L377>
- `tf-stack-k8s`: <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L548>
- `tf-stack-wt`: <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L781-L785>
- `tf-stack-tg`: <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L847>
- `tf-stack-migrate`: <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L1021>
- `tf-module-wt`: <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_module.go#L348>
- `plugin-infracost`: <https://github.com/spacelift-io/plugins/blob/930c4509a789/plugins/infracost/plugin.py>
- `plugin-tracing`: <https://github.com/spacelift-io/plugins/blob/930c4509a789/plugins/opentofu-tracing/plugin.py>
- `plugins-tree`: <https://github.com/spacelift-io/plugins/tree/930c4509a789/plugins>

[rt-df]: https://github.com/spacelift-io/runner-terraform/blob/6c75296f90e3/Dockerfile
[rt-readme]: https://github.com/spacelift-io/runner-terraform/blob/6c75296f90e3/README.md
[rt-bake]: https://github.com/spacelift-io/runner-terraform/blob/6c75296f90e3/docker-bake.hcl#L7-L11
[tf-stack-wt]: https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L781-L785
[ro-df]: https://github.com/spacelift-io/runner-opentofu/blob/41c9f80cb86b/Dockerfile
[rp-readme]: https://github.com/spacelift-io/runner-pulumi/blob/6120ea0266dd/README.md
[ra-df]: https://github.com/spacelift-io/runner-ansible/blob/d222259098b3/Dockerfile
[tf-stack-cfn]: https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L377
[tf-stack-k8s]: https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L548
[tf-stack-tg]: https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L847
[tf-stack-migrate]: https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L1021
[tf-module-wt]: https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_module.go#L348
[plugin-infracost]: https://github.com/spacelift-io/plugins/blob/930c4509a789/plugins/infracost/plugin.py
[plugin-tracing]: https://github.com/spacelift-io/plugins/blob/930c4509a789/plugins/opentofu-tracing/plugin.py
[plugins-tree]: https://github.com/spacelift-io/plugins/tree/930c4509a789/plugins
