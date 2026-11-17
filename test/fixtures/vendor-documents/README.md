# Vendor documents

Each file here is a document a vendor's own tool wrote, recorded in Docker
on 2026-11-17 from the tool's official image with no credentials and no
cloud. They are the observed counterpart of each vendor's contract in
[report 33](../../../docs/research/reference/33-vendor-tool-documents.md):
the plan document a runner reads, the outputs it reads, and how each tool
writes a secret. P20's vendors parse them in their `iactest` plan cases
and sanitization tests, and P20b's Ansible callback tests run `ansible/project`
and compare their records with the ones here.

Every name is a placeholder (`example`, `example-dev`, `localhost`) and
every secret is a dummy (`dummy-api-key-v1`, the Pulumi passphrase
`res11-dummy-passphrase`). Each file is as the tool wrote it, except that
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
