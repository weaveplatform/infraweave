# Vendor documents

Each file here is a document a vendor's own tool wrote, recorded in Docker
on 2026-11-17 from the tool's official image with no credentials and no
cloud. They are the observed counterpart of each vendor's contract in
[report 33](../../../docs/research/reference/33-vendor-tool-documents.md):
the plan document a runner reads, the outputs it reads, and how each tool
writes a secret. Each vendor parses them in its `iactest` plan cases and
sanitization tests (P13 and P20), P16 reads the drift plans, P17 the test
stream, P13's engine tests compare `opa/` with the recorded decisions, and
P20b's Ansible callback tests run `ansible/project` and compare their
records with the ones here.

Every name is a placeholder (`example`, `example-dev`, `localhost`,
`network`, `app`) and every secret is a dummy (`dummy-api-key-v1`,
`dummy-marked-v1`, `dummy-session-token-v1`, `dummy-tg-api-key`, the
Pulumi passphrase `res11-dummy-passphrase`, the OpenTofu passphrase
`res11-dummy-state-passphrase`); the passwords are the tools' own random
values. Each file is as the tool wrote it, except that
trailing spaces at line ends are removed.

## Pulumi

Pulumi CLI v3.276.0 with `pulumi-language-yaml` 1.38.16 and the `random`
provider 4.21.6, in `pulumi/pulumi-base` at
`sha256:a444a865ac1b96ea1f58f742123426a1e772c8a1aad3e8a98048662e67daf82a`.
The stack `dev` of project `example` lives in a `file://` backend, with
the passphrase secrets provider. `Pulumi.v1.yaml` creates a pet, a
password and a string whose `keepers` read the secret configuration value
`apiKey`; `Pulumi.v2.yaml` lengthens the pet (a replacement), drops the
string (a delete), adds an integer (a create), protects the password and
changes a secret output. Previews and updates ran with
`--refresh --diff --show-replacement-steps --non-interactive` and
`--event-log <file>`, under `PULUMI_DEBUG_COMMANDS=true`.

| File | Command | What it shows |
|---|---|---|
| `preview-create.events.jsonl` | `pulumi preview` of v1 on a new stack | the engine events: `progressEvent`s of the plugin download, a `resOutputsEvent` per step with the default provider's step among them, and Pulumi's unknown-value sentinel `04da6b54-80e4-46f7-96ec-b56ff0331ba9` |
| `preview-none.events.jsonl` | `pulumi preview` of v1 after `up` | a `refresh` step then a `same` step per resource; `summaryEvent.resourceChanges` `{"same":4}` |
| `preview-change.events.jsonl`, `preview-change.log` | `pulumi preview` of v2 | `create-replacement`, `replace` and `delete-replaced` for one replacement, `create`, `delete`, and a protected resource's `same`; the human diff the run log keeps |
| `up-change.events.jsonl`, `up-change.log` | `pulumi up --yes` of v2 | an update that previews first and then updates |
| `destroy-protected.events.jsonl`, `destroy-protected.log` | `pulumi destroy --yes` with the password protected | the preview fails, exit 1, and nothing is deleted |
| `destroy.events.jsonl` | `pulumi destroy --yes` after `pulumi state unprotect --all` | every resource deleted, the stack resource and the provider last |
| `stack-output.json` | `pulumi stack output --json` after the v1 update | secret outputs as the string `[secret]` |
| `stack-export.json` | `pulumi stack export` after the v1 update | state version 3; secrets as the signature object `{"4dabf18193072939515e22adb298388d": "1b47061264138c4ac30d75fd1eb44270", "ciphertext": "v1:…"}` |

## Kubernetes

k3s and kubectl v1.37.2+k3s1 with kustomize v5.8.1, in `rancher/k3s`
v1.37.2-k3s1 at
`sha256:21ea6e0fac4e3930c2ce8aa40ddd41a8b18cb3e9a0222c030dc005e7e88c4332`,
privileged, with CoreDNS, Traefik, the metrics server, local storage, the
service load balancer, the Helm controller and the cloud controller
disabled, so no image is pulled. `v1/` holds a ConfigMap, a second
ConfigMap, a Secret with `data` and `stringData`, and a Deployment of no
replicas; `v2/` changes the ConfigMap and the Secret and drops the second
ConfigMap; `v3/` also drops the Secret. Each `kustomization.yaml` labels
the objects through the `labels` transformer. The plans ran
`kubectl apply -k ./ --prune --prune-allowlist=core/v1/ConfigMap --prune-allowlist=core/v1/Secret --prune-allowlist=apps/v1/Deployment -l infraweave-stack=example --namespace=example -o json`
with the dry run named below.

| File | Command | What it shows |
|---|---|---|
| `dry-run-client-create.json` | `--dry-run=client` of v1 on an empty namespace | the Secret's `stringData` in plaintext |
| `dry-run-create.json` | `--dry-run=server` of v1 on an empty namespace | one `List`, `stringData` merged into `data`, and the last-applied annotation holding `stringData` in plaintext |
| `get-v1.json` | `kubectl get -o json --show-kind --ignore-not-found -l infraweave-stack=example --namespace=example configmaps,secrets,deployments.apps` after applying v1 | the live objects a plan compares with |
| `dry-run-none.json` | `--dry-run=server` of v1 after applying it | equal to `get-v1.json` once server-set fields are dropped |
| `dry-run-change.json` | `--dry-run=server` of v2 | a stream of two JSON values: the `List` of applied objects, then the pruned ConfigMap |
| `dry-run-prune-secret.json` | `--dry-run=server` of v3 after applying v1 | the pruned Secret after the `List`, with its `data` and annotation |
| `diff-change.txt` | `kubectl diff` of v2, full context | `data` masked, the last-applied annotation not |
| `diff-prune-secret.txt` | `kubectl diff` of v3 | the pruned Secret unmasked |

## Ansible

ansible-core 2.22.0 on Python 3.15.2, installed from PyPI in `python` at
`sha256:46837ed3105db0029b6d656816185c6258e3b39690532fb782428e0a5fd76182`.
`project/` runs a role and eight tasks on `localhost`, among them a template
that renders a secret variable, a `copy` with `no_log: true`, a `copy`
without it that writes an inventory variable, a `command`, which check mode
skips, and a task skipped by `when`. An aggregate callback written for the
recording wrote each task result and the play recap as JSON lines:
`{event, playbook, host, task, action, role, check_mode, no_log, result}`
per result, where `result` is the module's result as ansible-core hands it
to a callback, and `{event: "stats", playbook, stats, summarize}` last,
where `stats` is ansible-core's `AggregateStats`.

| File | Command | What it shows |
|---|---|---|
| `check.log`, `check.records.jsonl` | `ansible-playbook -i inventory.ini playbook.yml --check -v` | a check-mode plan: `changed` 6, `skipped` 2 |
| `check-diff.log` | the same with `--diff` and without `-v` | the rendered template's secret and the inventory variable in the diff |
| `apply.records.jsonl` | `ansible-playbook -i inventory.ini playbook.yml -v` | results carrying file checksums and a command's output; the `no_log` task censored |
| `check-none.records.jsonl` | the check after the apply | `changed` empty |
| `failure.records.jsonl` | `ansible-playbook -i inventory-unreachable.ini playbook-fail.yml --check`, exit 4 | `dark` for the unreachable host and `failures` for the failed task |
| `inventory.json` | `ansible-inventory -i inventory.ini --list` | `hostvars` in plaintext |

## Terraform and OpenTofu

Terraform 1.17.2 in `hashicorp/terraform` at
`sha256:6e9c1e647c31a960dc9b59e13272f9111d90010c10b717f77ffb324767660475`
and OpenTofu 1.13.9 in `ghcr.io/opentofu/opentofu` at
`sha256:f5aef7f2632b490e3c289a239d7974497d0583f3df6bfa35e8eb31a367ee5ec0`,
each with `hashicorp/random` 3.9.1 and `hashicorp/local` 2.9.1 from its own
registry, ran `terraform/project` with a local backend. `main.tf` is the
first configuration and `main.tf.v2` the second; `invalid/` fails
validation, `write-only/` uses Terraform's `terraform_data.store`, and
`opentofu/project-encrypted/` is the encrypted project. Variables came from
`TF_VAR_api_key` and `TF_VAR_session_token`. `opentofu/` holds OpenTofu's
documents under the names `terraform/` uses, beside its own.

| File | Command | What it shows |
|---|---|---|
| `version.json` | `version -json` | the tool's version; Terraform's `format_version` and `terraform_outdated` |
| `validate-invalid.json` | `validate -json` of `invalid/` | diagnostics with ranges; an ephemeral value refused for an attribute that is not write-only |
| `plan-create.json`, `plan-create.ui.jsonl` | `plan -json -out`, then `show -json`, of `main.tf` | every sensitive marker; `variables` with the sensitive variable's value; a failed `check`; no ephemeral value |
| `apply-create.ui.jsonl` | `apply -json` of that plan | the UI stream: ephemeral open and close, a provisioner, `outputs` without sensitive values |
| `state.json`, `state-pull.json`, `outputs.json` | `show -json`, `state pull`, `output -json` after the apply | sensitive values in plaintext beside `sensitive_values`, `sensitive_attributes` and `sensitive` |
| `plan-refresh-only.json`, `plan-refresh-only.ui.jsonl` | `plan -refresh-only` after `out/rendered.txt` is edited out of band | `resource_drift` and no `resource_changes` |
| `plan-drift.json` | `plan` after the same edit | `resource_drift` beside the `create` that repairs it |
| `plan-change.json`, `plan-change.ui.jsonl`, `generated.tf` | `plan -json -generate-config-out=generated.tf -out` of `main.tf.v2` | replace with `replace_paths`, `forget`, import with `generated_config`, move, delete, create, update of sensitive values |
| `plan-target-moved.ui.jsonl` | `plan -target=terraform_data.config` with the move pending | the error naming the target to add |
| `plan-target.json` | the same target after `main.tf.v2` is applied, with the pet lengthened | the target's dependency planned with it; Terraform's `complete: false` |
| `plan-destroy.json`, `apply-destroy.ui.jsonl` | `plan -destroy -out`, then `apply -json` | every resource and output deleted |
| `test.jsonl` | `test -json` of `tests/example.tftest.hcl` | two runs passing and one failing |
| `terraform/plan-write-only.json`, `terraform/state-pull-write-only.json` | `plan`, `show -json` and `state pull` of `write-only/` | `store.input` `null`; its copy in state, sensitive and not |
| `opentofu/plan-exclude.json` | `plan -exclude=random_pet.name` with the pet lengthened | the pet and what depends on it left out |
| `opentofu/state-encrypted.json` | `apply` of `project-encrypted/` with `TF_ENCRYPTION` from `encryption.hcl`, to an `http` backend | the body the backend receives: `serial`, `lineage`, `meta`, `encrypted_data`, `encryption_version` |
| `opentofu/state-pull-encrypted.json` | `state pull` with the key | plaintext state |
| `opentofu/encrypted-read-errors.txt` | `show -json`, `state pull` and `show -json <plan>` without the key | each refusal |

## Terragrunt

Terragrunt 1.2.2, the release's `terragrunt_linux_arm64` with SHA-256
`a730c7daf2fb7dd13e6e7b016b3a89fd4f546bc81a31f8ff19474866c566b755`, as the
release's `SHA256SUMS` lists it, mounted into the OpenTofu image and
driving OpenTofu (`TG_TF_PATH=tofu`). `project/live/` holds the units
`network` and `app`, which sources `project/modules/app` and reads
`network`'s outputs; `project/stack/live/terragrunt.stack.hcl` generates
two units from `project/stack/catalog/`. The commands ran with
`TG_NON_INTERACTIVE` and `TG_NO_COLOR` set.

| File | Command | What it shows |
|---|---|---|
| `version.txt` | `terragrunt --version` | the version |
| `find.json`, `find-dag.json` | `find --json`; with `--dag --dependencies --include --reading` | the units, their order, includes, dependencies and files read |
| `list-long.txt`, `dag-graph.dot` | `list --long --dependencies`, `dag graph` | the same as text and as DOT |
| `render-app.json`, `render-all.json` | `render --json` in `app`; `render --all --json` | the resolved configuration, `inputs` in plaintext, mocked outputs |
| `json-out/<unit>/tfplan.json` | `run --all -- plan -input=false -out=tfplan.tfplan` with `TG_JSON_OUT_DIR` | each unit's `show -json` |
| `show-plan-unapplied.log` | `run --all -- show -json tfplan.tfplan` before any apply | the refusal for a dependency without outputs |
| `output-all.json` | `run --all -- output -json` after `run --all -- apply` | one document per unit, without its name |
| `plan-log-format-json.jsonl` | `run --all --log-format json -- plan` | the JSON log lines on standard output |
| `stack-find-before.json`, `stack-generated.txt`, `stack-find-after.json` | `find --json`, `stack generate`, then `find --json` | the stack, the generated files, the generated units |
| `stack-regeneration.txt` | `run --all` with and without `--no-stack-generate`, after files are written into the generated units | which files survive regeneration |

## Open Policy Agent

OPA 1.22.4 in `openpolicyagent/opa` at
`sha256:ff50f50332ff9e722ffafe2658c195ced1f9b6d4f3064000c2ecfa8cdf7acccf`.
`policies/` holds a minimal Rego v1 policy per type in
`package spacelift`, `check/` policies that fail, and `tests/` a test file.
The inputs are those of [`../policy-inputs`](../policy-inputs/README.md),
and `inputs/plan-proposed-deny.json`, `plan-tracked-warn.json` with
`spacelift.run.type` set to `PROPOSED` and its commit message to
`proposed deny test [deny]`.

| File | Command | What it shows |
|---|---|---|
| `version.txt` | `opa version` | the version and Rego v1 as the default |
| `eval/<input>.json` | `opa eval --format json -d policies/<type>.rego -i <input> data.spacelift` | the result set per input |
| `check-broken.json`, `check-undefined.json` | `opa check --format json` | a parse error and a type error, each at a point |
| `check-v0only.json`, `check-v1only-v0-compatible.json` | `opa check --format json`, the second with `--v0-compatible` | a v0 rule under v1 and a v1 rule under v0 |
| `eval-conflict.json` | `opa eval --format json -d check/conflict.rego` | an evaluation error with its stack trace |
| `test.json` | `opa test --format json policies/plan.rego tests/plan_test.rego` | a failing test among passing ones |
