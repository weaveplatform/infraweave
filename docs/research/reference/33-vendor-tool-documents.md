# 33: The vendors' own documents

Checked against: Pulumi CLI v3.276.0, k3s and kubectl v1.37.2+k3s1 and ansible-core 2.22.0, each run locally in Docker from its official image on 2026-11-17 with no credentials and no cloud, their documents kept in [`test/fixtures/vendor-documents`](../../../test/fixtures/vendor-documents/README.md); AWS CloudFormation's API reference (API version 2010-05-15), its user guide and the CloudFormation CLI's resource schema reference, read 2026-11-17; Spacelift's Pulumi and Ansible runs and policy inputs of 2026-11-17 ([32](32-spacelift-console-survey.md) section 16.8, [`test/fixtures/policy-inputs`](../../../test/fixtures/policy-inputs/README.md)); report [20](20-vendors.md) at commit `c4787489`.

Research baseline: **2026-11-17**.

Report [20](20-vendors.md) records how Spacelift drives Pulumi,
CloudFormation, Kubernetes and Ansible. This report records what each tool
itself writes when it is driven that way: the commands per phase and their
exit codes, the plan document a runner can read, the outputs, where state
lives, how a secret appears in each document, what sanitization has to
hash, and the block each vendor gives the plan policy input. Each tool ran
at its latest release; CloudFormation, which needs an AWS account, comes
from AWS's published reference instead. Decisions
[0008](../decisions/0008-run-execution-and-worker-protocol.md),
[0009](../decisions/0009-policy-engine-opa.md),
[0010](../decisions/0010-state-backend.md),
[0016](../decisions/0016-testing-and-acceptance.md) and
[0024](../decisions/0024-expression-language.md) state each vendor's
contract from these findings, and P20 and P20b of the
[implementation plan](../../implementation/plan.md) build it.

| Label | Means |
|---|---|
| **[T]** | A document in `test/fixtures/vendor-documents`, named by its path under that directory |
| **[L]** | A command run locally on 2026-11-17, with the same images, and quoted here; its output is not kept as a fixture |
| **[A]** | AWS's published reference, read 2026-11-17, by the short names below |
| **[C]** | Spacelift's console, driven on 2026-11-17 ([32](32-spacelift-console-survey.md) section 16.8), and its policy inputs in `test/fixtures/policy-inputs` |
| **[I]** | Inferred; the text says from what |
| *(unverified)* | No recording or reference confirms it; listed in [30](30-open-questions.md) |

| Short name | URL |
|---|---|
| create-change-set | <https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_CreateChangeSet.html> |
| describe-change-set | <https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DescribeChangeSet.html> |
| execute-change-set | <https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ExecuteChangeSet.html> |
| change | <https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_Change.html> |
| resource-change | <https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ResourceChange.html> |
| change-detail | <https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ResourceChangeDetail.html> |
| target | <https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ResourceTargetDefinition.html> |
| output | <https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_Output.html> |
| stack | <https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_Stack.html> |
| stack-event | <https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_StackEvent.html> |
| drift | <https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DetectStackDrift.html> |
| parameters | <https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/parameters-section-structure.html> |
| change-sets-view | <https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/using-cfn-updating-stacks-changesets-view.html> |
| resource-schema | <https://docs.aws.amazon.com/cloudformation-cli/latest/userguide/resource-type-schema.html> |

## 1. Versions

| Tool | Version | Image |
|---|---|---|
| Pulumi | CLI v3.276.0, `pulumi-language-yaml` 1.38.16 bundled, `random` provider 4.21.6 downloaded at the first preview | `pulumi/pulumi-base`, Debian 13 |
| Kubernetes | k3s and kubectl v1.37.2+k3s1, kustomize v5.8.1 | `rancher/k3s` at the release tag `v1.37.2-k3s1` |
| Ansible | ansible-core 2.22.0, Python 3.15.2, Jinja 3.1.6 | `python`, Debian 13, with `pip install ansible-core` |
| CloudFormation | API version 2010-05-15 | none |

The fixtures' README names each image's digest. `rancher/k3s:latest`
resolves to v1.34.1+k3s1, built 2025-09-22, more than a year behind the
release tags [L], so a pinned k3s follows its release tags.

## 2. Pulumi

### 2.1 Commands per phase

| Phase | Command | Result |
|---|---|---|
| Initializing | `pulumi login --non-interactive <loginURL>` | `Logged in to <host> as <user> (<loginURL>)` [L] |
| | `pulumi stack select --create <stackName> --non-interactive` | `Created stack 'dev'` on stderr when the stack is new; it writes `Pulumi.<stack>.yaml`, holding the passphrase provider's `encryptionsalt`, into the project directory [L] |
| | `pulumi stack select --create --select <stackName>`, as report 20 section 7 gives it | `error: unknown flag: --select`; the stack is an argument or `--stack` [L] |
| Planning | `pulumi preview --refresh --diff --show-replacement-steps --non-interactive --event-log <file>` with `PULUMI_DEBUG_COMMANDS=true` | the human diff on standard output and the engine events in the file [T] `pulumi/preview-change.log`, `pulumi/preview-change.events.jsonl`; exit 0 whether or not anything changes |
| | the same without `PULUMI_DEBUG_COMMANDS` | `error: unknown flag: --event-log`: the flag is listed only when that variable is set, as "Log events to a file at this path" [L] |
| Applying | `pulumi up --refresh --diff --show-replacement-steps --yes --non-interactive` | `Previewing update (dev):` and the diff, then `Updating (dev):` [T] `pulumi/up-change.log` |
| | the same with `--skip-preview` | `Updating (dev):` with progress lines alone [L] |
| Outputs | `pulumi stack output --json`; `pulumi stack export` | section 2.4 |
| Destroying | `pulumi destroy --refresh --yes --non-interactive` | previews, then deletes every resource, the stack resource and its provider last [T] `pulumi/destroy.events.jsonl` |
| | the same with a protected resource | the preview fails, exit 1, and nothing is deleted [T] `pulumi/destroy-protected.log` |
| | `pulumi destroy --yes --skip-preview` with a protected resource | `2 deleted, 2 errored`, exit 1: part of the stack is gone [L] |

- **The passphrase.** With a `file://` backend and the default secrets
  provider, creating a stack and every preview fail unless
  `PULUMI_CONFIG_PASSPHRASE` or `PULUMI_CONFIG_PASSPHRASE_FILE` is set:
  `passphrase must be set with PULUMI_CONFIG_PASSPHRASE or
  PULUMI_CONFIG_PASSPHRASE_FILE environment variables`. An empty value is
  accepted [L].
- **Secret configuration.** A secret configuration key cannot take a
  `default` in `Pulumi.yaml` (`configuration key 'token' must be encrypted
  as it's secret`); `pulumi config set --secret` sets it [L].
- **Plugins.** A program with no provider downloads nothing. The `random`
  program downloads `random` 4.21.6 at its first preview, which the event
  log reports as `progressEvent`s [T] `pulumi/preview-create.events.jsonl`.
  `~/.pulumi/plugins` lives outside the workspace.
- **Binaries.** The CLI and its YAML, Node.js and Python language hosts are
  statically linked (`ldd`: "not a dynamic executable") [L], so nothing in
  them depends on Debian's libc. Whether the CLI and a provider plugin run
  on Alpine's musl is *(unverified)* (C299).
- **Extra arguments.** Spacelift's `SPACELIFT_PULUMI_CLI_ARGS_preview`,
  `_up` and `_destroy` append to these commands
  ([20 section 7](20-vendors.md#7-pulumi)).

### 2.2 The plan document: engine events

`--event-log` writes one JSON object per line, `{sequence, timestamp,
<event>}`, the format the streaming `--json` mode
(`PULUMI_ENABLE_STREAMING_JSON_PREVIEW=1 pulumi preview --json`) writes on
standard output [L] [T] `pulumi/preview-create.events.jsonl`:

| Event | Fields |
|---|---|
| `preludeEvent` | `config`, a secret value as `"[secret]"` |
| `resourcePreEvent` | `metadata` and `planning: true` in a preview; `metadata.new.outputs` is `{}` |
| `resOutputsEvent` | `metadata` with the step's outputs, and `planning: true` in a preview |
| `diagnosticEvent` | `prefix`, `message`, `color`, `severity`; the YAML runtime writes `debug` lines such as `Registering resource [pet]` |
| `progressEvent` | a plugin download: `type`, `id`, `message`, `received`, `total`, `done` |
| `summaryEvent` | `maybeCorrupt`, `durationSeconds`, `resourceChanges`, `PolicyPacks`, `isPreview`, `result` (`succeeded` or `failed`) |
| `cancelEvent` | `{}`, last |

A step's `metadata` is `{op, urn, type, old, new, keys, diffs,
detailedDiff, logical, provider}`, and `old` and `new` are
`{type, urn, custom, id, parent, inputs, outputs, provider}` [T]
`pulumi/preview-change.events.jsonl`.

- **Steps.** Every resource, the stack resource `pulumi:pulumi:Stack` and
  each default provider (`pulumi:providers:random`) included, has one
  `resOutputsEvent` per step. The stack resource's first step carries no
  outputs; its last carries the stack's outputs, old and new [T].
- **Refresh.** Under `--refresh` every existing resource first has a
  `refresh` step [T] `pulumi/preview-none.events.jsonl`.
- **No changes.** Every resource's step is `same`, and
  `summaryEvent.resourceChanges` is `{"same":4}` [T]
  `pulumi/preview-none.events.jsonl`.
- **Replacement.** Under `--show-replacement-steps` one replacement is three
  steps: `create-replacement`, `replace` and `delete-replaced`, in that
  order for a resource that creates before it deletes; without the flag
  only `replace` appears [T] [L]. `summaryEvent.resourceChanges` counts it
  once: `{"create":1,"delete":1,"replace":1,"same":2}` [T]
  `pulumi/preview-change.events.jsonl`, which bears on C198.
- **Changed properties.** `keys` and `diffs` list them (`["length"]`);
  `detailedDiff` is null for every `random` step [T].
- **Unknown values.** A value not known until apply is Pulumi's sentinel
  string `04da6b54-80e4-46f7-96ec-b56ff0331ba9`, in outputs and in a new
  default provider's URN; the human diff prints `[unknown]` [T]
  `pulumi/preview-change.events.jsonl`, `pulumi/preview-change.log`.
- **Ops recorded:** `same`, `create`, `replace`, `create-replacement`,
  `delete-replaced`, `delete` and `refresh`. `update`, `read`, `import` and
  `discard` are not: every `random` input forces a replacement, and a
  change of `protect` alone is `same` [T]. How those four appear is
  *(unverified)* (C301).
- **A failed preview.** A destroy preview refused by a protected resource
  writes its `delete` steps, a `diagnosticEvent` of severity `error` and
  `summaryEvent.result` `failed` [T] `pulumi/destroy-protected.events.jsonl`.

The other machine-readable plans and why a runner does not read them:

| Document | Shape | Against the event log |
|---|---|---|
| `pulumi preview --json` | one document `{config, steps[], duration, changeSummary}`, `diagnostics[]` on failure; a step is `{op, urn, provider, oldState, newState, diffReasons, replaceReasons, detailedDiff}`, and each state adds `sourcePosition` and a `stackTrace` of the YAML runtime's frames [L] | replaces the human diff on standard output, has no default provider's step and no stack outputs, and carries build paths |
| streaming `--json` | the engine events on standard output [L] | the same events, but the run log loses the human diff |
| `pulumi preview --save-plan <file>` | `{manifest, config, resourcePlans{<urn>: {goal, steps, state, seed}}}`, a preview feature that `pulumi up --plan` constrains an update to [L] | holds secrets as passphrase ciphertext |

### 2.3 Spacelift's steps

Spacelift's `pulumi.steps[]` is the engine events' step shape, key for key:
`{op, urn, type, provider, old, new}`, four steps for the stack, its
default provider, a pet and the stack again with its outputs [C]
`plan-pulumi.json`. That is one entry per `resOutputsEvent` of the preview,
in order, with `keys`, `diffs`, `detailedDiff` and `logical` left out
[T] `pulumi/preview-create.events.jsonl`. The UUID in place of each value
unknown at preview in Spacelift's input is Pulumi's own sentinel above.

### 2.4 Outputs

| Command | A secret output | A plain output |
|---|---|---|
| `pulumi stack output --json` | the string `"[secret]"` [T] `pulumi/stack-output.json` | its value |
| `pulumi stack output --json --show-secrets` | its plaintext, unmarked [L] | its value |
| `pulumi stack export`, the root `pulumi:pulumi:Stack` resource's `outputs` | the signature object with `ciphertext` [T] `pulumi/stack-export.json` | its value |
| `pulumi stack export --show-secrets` | the signature object with `plaintext`, the value as JSON text [L] | its value |

`"[secret]"` from `stack output --json` cannot be told from a literal
string `[secret]`; the export's signature object can. After a destroy,
`stack output --json` prints `{}` [L].

### 2.5 State

- **Where.** Wholly in the `loginURL` backend. A `file://` backend holds
  `.pulumi/meta.yaml`, `stacks/<project>/<stack>.json` with a `.bak`,
  `history/` checkpoints, `backups/`, and an `.attrs` file beside each
  [L].
- **The checkpoint** is `{"version":3,"checkpoint":{"stack":"organization/<project>/<stack>","latest":<deployment>}}`;
  `pulumi stack export` writes `{"version":3,"deployment":{manifest,
  secrets_providers, resources, metadata}}`, each resource with `urn,
  custom, id, type, inputs, outputs, parent, provider,
  propertyDependencies, additionalSecretOutputs, created, modified` [T]
  `pulumi/stack-export.json`.
- **The salt.** The passphrase provider's salt is in the backend's
  `secrets_providers` and in `Pulumi.<stack>.yaml`, so a fresh workspace
  logged in to the same backend still decrypts [T] [L].

### 2.6 Secrets

| Document | How a secret appears |
|---|---|
| Human output: the diff, progress, `stack output` | `[secret]` [T] `pulumi/preview-change.log` |
| `preview --json`, `stack output --json`, `preludeEvent.config` | `"[secret]"` [T] [L] |
| Engine events | `{"4dabf18193072939515e22adb298388d": "1b47061264138c4ac30d75fd1eb44270", "ciphertext": "[secret]"}` [T] `pulumi/preview-change.events.jsonl` |
| `stack export`, `--save-plan`, the checkpoint | the same signature key with `"ciphertext": "v1:<ciphertext>"` [T] `pulumi/stack-export.json` |
| `stack export --show-secrets` | the same signature key with `"plaintext": "<JSON text>"` [L] |

- A secret reaches the documents through configuration, inputs such as a
  `keepers` map, outputs a provider marks (`additionalSecretOutputs`) and
  `fn::secret` [T].
- A secret's change is invisible: `wrapped` changes value and every
  document shows `[secret]` on both sides [T]
  `pulumi/preview-change.events.jsonl`.
- Not every value derived from a secret is secret: `RandomString.result`
  is plain although its `keepers` are secret [T].
- No dummy secret value appears in any event log [T].

### 2.7 Compared with Spacelift and report 20

| Report 20 or Spacelift | Recorded |
|---|---|
| Section 7: `pulumi stack select --create --select <stackName>` | `--select` is an unknown flag; the form is `pulumi stack select --create <stackName>` [L] |
| Section 7: apply is `pulumi up --refresh --diff --show-replacement-steps` | Spacelift's apply log shows a preview then the update, which is `pulumi up --yes` without `--skip-preview` [C] [T] `pulumi/up-change.log` |
| Section 7: destroy is inferred (C193) | `pulumi destroy --yes` previews first and deletes nothing when the preview fails; Spacelift's destroy is still unread, *(unverified)* (C295) |
| Section 2: `op` is one of `same`, `refresh`, `create`, `update`, `delete`, `create-replacement`, `delete-replaced` | `replace` also appears [T] |
| Section 13: Pulumi secrets appear as `[secret]` | In the human output and `--json` documents; the event log and state use the signature object [T] |
| Spacelift's run needs no `PULUMI_CONFIG_PASSPHRASE` from the user ([32](32-spacelift-console-survey.md) section 16.8) | The CLI refuses a passphrase stack without one, so Spacelift's runner sets it; to what is *(unverified)* (C300) |
| Spacelift installs a resource plugin of its own while initializing [C] | No step of the recorded program needs it; what it does is *(unverified)* (C300) |
| Spacelift's planning log is `pulumi preview`'s with `[urn=…]` lines and `--outputs:--` [C] | That is the `--diff` format [T] `pulumi/preview-change.log`; without `--diff` the preview prints progress lines [L] |

These settle C193's Pulumi plan and apply parts.

## 3. Kubernetes

### 3.1 Commands

Spacelift's seven workflow keys ([20 section 9.1](20-vendors.md#91-commands)),
run with infraweave's label `infraweave-stack`:

| Key | Command | Result |
|---|---|---|
| `createFromStdin` | `kubectl create -f - -o yaml` with a `SelfSubjectAccessReview` | `status.allowed: true` [L] |
| `apiResources` | `kubectl api-resources --no-headers --verbs=list,create --namespaced -o name` | 33 names on k3s, among them `addons.k3s.cattle.io` and the deprecated `endpoints` [L] |
| `get` | `kubectl get -o json --show-kind --ignore-not-found -l infraweave-stack=example --namespace=example configmaps,secrets,deployments.apps` | a `List` [T] `kubernetes/get-v1.json`; with nothing matching, empty standard output and exit 0 [L]; `managedFields` are left out |
| `apply`, planning | `kubectl apply -k ./ --prune --prune-allowlist=core/v1/ConfigMap --prune-allowlist=core/v1/Secret --prune-allowlist=apps/v1/Deployment -l infraweave-stack=example --dry-run=server -o json --namespace=example` | section 3.2 |
| `apply`, applying | the same without `--dry-run` and `-o` | `configmap/example-config configured`, `configmap/example-extra pruned` [L] |
| `rolloutStatus` | `kubectl rollout status --namespace=example --timeout 60s -w deployment/example-app` | `deployment "example-app" successfully rolled out` [L] |
| `delete` | `kubectl delete --ignore-not-found -l infraweave-stack=example --namespace=example <every apiResources name, comma-joined>` | `configmap "example-config" deleted from example namespace`, and so on; a warning that v1 Endpoints is deprecated [L] |

- **Kustomize.** `kubectl kustomize` applies a `labels` transformer's pairs
  to each object's `metadata` and leaves selectors and pod templates alone
  [L] [T] `kubernetes/v1/kustomization.yaml`.
- **The prune flags.** `--prune-whitelist` is an unknown flag
  (`error: unknown flag: --prune-whitelist`), and `--prune -l` without an
  allowlist works with a deprecation warning that pruning will stop
  covering cluster-scoped kinds under `--namespace` [L].
- **`kubectl diff`** exits 1 when anything differs and 0 when nothing does
  [L].
- **Server-side apply** writes no last-applied annotation, and over objects
  that client-side apply manages it fails with a field-manager conflict
  unless `--force-conflicts` is passed [L].

### 3.2 The dry run is the plan

- **A stream, not a document.** `apply --dry-run=server -o json` writes one
  `List` of the applied objects as the server would store them, then one
  JSON object per pruned object [T] `kubernetes/dry-run-change.json`,
  `kubernetes/dry-run-prune-secret.json`. A reader decodes a sequence of
  JSON values.
- **New objects** come back with a server-assigned `uid` and
  `creationTimestamp` and no `resourceVersion` [T]
  `kubernetes/dry-run-create.json`.
- **No changes.** A dry run of what is applied returns objects equal to
  `get`'s [T] `kubernetes/dry-run-none.json`, `kubernetes/get-v1.json`; a
  changed object differs in its content and its last-applied annotation,
  and keeps its `resourceVersion` [T] `kubernetes/dry-run-change.json`.
  Comparing with `uid`, `creationTimestamp`, `resourceVersion`,
  `generation`, `managedFields`, the last-applied annotation, controller
  annotations such as `deployment.kubernetes.io/revision`, and `status`
  left out tells a change from none on both releases recorded.
- **The first run.** A server dry run into a namespace that does not exist,
  or that the same apply creates, fails with `namespaces "<name>" not
  found`; a client dry run succeeds [L]. That is why Spacelift's first run
  uses `--dry-run=client` ([20 section 9.1](20-vendors.md#91-commands)).
- **Custom resources.** A custom resource definition and a resource of its
  kind in one apply fail in every mode, client and server dry run and the
  real apply alike, with `no matches for kind "Widget" … ensure CRDs are
  installed first`; the real apply creates the definition first [L]. A
  stack applies its definitions in an earlier run or another stack.

### 3.3 State and outputs

The cluster is the state: a stack's resources are the objects carrying its
label, and nothing is stored beside them. Kubernetes has no outputs.

### 3.4 Secrets

| Document | How a Secret's values appear |
|---|---|
| Client dry run | `data` in base64, `stringData` in plaintext, and the last-applied annotation holding both [T] `kubernetes/dry-run-client-create.json` |
| Server dry run | `stringData` merged into `data` in base64, and the last-applied annotation holding `stringData` in plaintext [T] `kubernetes/dry-run-create.json` |
| `get` | the same as the server dry run [T] `kubernetes/get-v1.json` |
| A pruned Secret in the dry-run stream | its `data` and annotation [T] `kubernetes/dry-run-prune-secret.json` |
| `kubectl diff` of a changed Secret | `data` masked as `'***'`, `'*** (before)'` and `'*** (after)'`; the last-applied annotation unmasked, with `stringData` in plaintext [T] `kubernetes/diff-change.txt` |
| `kubectl diff` of a pruned Secret | everything unmasked [T] `kubernetes/diff-prune-secret.txt` |
| apply, delete and rollout text | names only [L] |

Base64 is an encoding, so `data` is as sensitive as `stringData`.

### 3.5 Compared with Spacelift and report 20

| Report 20 or Spacelift | Recorded |
|---|---|
| Section 9.1: `--prune-whitelist` below kubectl 1.26 behind a label | Unknown on current kubectl; a template emits it only for a release that lacks `--prune-allowlist` [L] |
| Section 9.1: planning dry-runs on the server, or on the client when nothing exists | The server dry run is a stream; a client dry run is what works before the namespace exists [T] [L] |
| Section 2: plan policies get "the vendor's own output" | No single output exists once anything is pruned; what Spacelift passes, and how a Secret appears in it, is *(unverified)* (C297) |
| Section 13: no sanitization setting for Kubernetes | A Secret's values reach every dry run and `get`, and `kubectl diff` leaks them [T] |
| Section 9.3: Helm through a `helm template` hook | Not recorded, as Helm was not among the approved downloads; *(unverified)* (C298) |

## 4. Ansible

### 4.1 Commands per phase

| Phase | Command | Result |
|---|---|---|
| Planning | `ansible-playbook -i inventory.ini playbook.yml --check -v` | exit 0; `command` tasks are skipped in check mode [T] `ansible/check.log` |
| | the same with `--diff` | the rendered content of each file, secrets included [T] `ansible/check-diff.log` |
| | `ansible-playbook playbook.yml --check -v`, no inventory | `[WARNING]: No inventory was parsed, only implicit localhost is available`, as Spacelift's planning log shows [L] [C] |
| Applying | `ansible-playbook -i inventory.ini playbook.yml -v` | the change; a second check reports nothing changed [T] `ansible/apply.records.jsonl`, `ansible/check-none.records.jsonl` |
| Inventory | `ansible-inventory -i inventory.ini --list` | `_meta.hostvars` in plaintext [T] `ansible/inventory.json` |

- **Exit codes.** 0 on success, 2 when a task fails, 4 when a host is
  unreachable, which wins over a failure [T]
  `ansible/failure.records.jsonl` [L].
- **No JSON callback.** `ANSIBLE_STDOUT_CALLBACK=json` fails with
  `[ERROR]: Could not load 'json' callback plugin.` ansible-core's callbacks
  are `default`, `junit`, `minimal`, `oneline` and `tree` [L]; the `json`
  callback is in the `ansible.posix` collection. A runner needs that
  collection, ansible-runner, or a callback of its own.

### 4.2 What a callback receives

An aggregate callback receives each task result and, last, the play's
`AggregateStats` [T] `ansible/check.records.jsonl`:

```json
{"processed": {"localhost": 1}, "ok": {"localhost": 7}, "changed": {"localhost": 6},
 "dark": {}, "failures": {}, "skipped": {"localhost": 2}, "rescued": {}, "ignored": {}, "custom": {}}
```

- **Unreachable hosts** are counted in `dark`, failed tasks in `failures`
  [T] `ansible/failure.records.jsonl`.
- **Each result** names its host, its task (`common : Ensure the work
  directory` for a role's task), the resolved action
  (`ansible.builtin.copy`), the role, whether it ran in check mode and
  whether it is `no_log`, and the module's result [T].
- **The module's result** carries what the module returns: a file's
  checksums and paths, a command's output, a diff [T]
  `ansible/apply.records.jsonl`.
- **`no_log`** replaces the result with `{"_ansible_no_log": true,
  "censored": "the output has been hidden due to the fact that 'no_log:
  true' was specified for this result", "changed": true}`, and no diff is
  printed for it [T].

Spacelift's `ansible` block is these dictionaries without `custom`, plus
`playbook` [C] `plan-ansible.json`. That is the event data of
ansible-runner's `playbook_on_stats` event, which runner-ansible installs
([20 section 11](20-vendors.md#11-runner-images)), so Spacelift reads the
recap through ansible-runner [C]. Its change entries carry `host_name`,
`playbook_name`, `role_name`, `task_action` and `task_name` with `action`
`ok` or `changed`, which a callback's results supply; whether
`task_name` carries the role's prefix as ansible-core's does is
*(unverified)* (C302).

### 4.3 State and outputs

The hosts are the state, and Ansible has no outputs.

### 4.4 Secrets

| Where | What appears |
|---|---|
| `--diff`, on screen and in each result's `diff` | the rendered content, a template's secret and an inventory variable included [T] `ansible/check-diff.log` |
| Verbose results (`-v`) | the module's result: checksums and paths for a `copy`, standard output for a `command` [T] `ansible/apply.records.jsonl` |
| A `no_log` task | the censored result above [T] |
| `ansible-inventory --list` | `hostvars` in plaintext [T] `ansible/inventory.json` |
| The recap | host names and counts [T] |

### 4.5 Compared with Spacelift and report 20

| Report 20 or Spacelift | Recorded |
|---|---|
| Section 10: Ansible runs `ansible-playbook`, inferred (C193) | Spacelift plans with `ansible-playbook <playbook> --check` at verbosity 1, with no `--diff` and no `-i`, and applies the same without `--check`: its log prints `No config file found; using defaults` and one result line per task, which only verbosity 1 prints, and no diff [C] [T] `ansible/check.log` |
| Section 10: plan policies get "Ansible's own output" | `AggregateStats` and the playbook's name [C] [T] |
| Section 11: runner-ansible installs ansible-runner | ansible-core alone has no JSON callback [L] |
| Spacelift's log prints "Succesfully exported Spacelift's Ansible config defaults" beside `No config file found` [C] | The defaults are environment variables, not an `ansible.cfg` [I] from the two lines together |

## 5. CloudFormation

Spacelift's docs name no API call; templates are uploaded under
`<templateBucket>/<run id>/` while planning
([20 section 8](20-vendors.md#8-cloudformation)). The calls a runner makes
for each phase, from AWS's reference:

| Phase | Calls |
|---|---|
| Initializing | `DescribeStacks`, which decides whether the change set creates or updates |
| Planning | upload the entry template, and each nested `AWS::CloudFormation::Stack` whose `TemplateURL` is a local path, to S3; `CreateChangeSet{StackName, ChangeSetName, ChangeSetType, TemplateURL, Parameters, Capabilities, IncludeNestedStacks}`; poll `DescribeChangeSet` until `Status` is `CREATE_COMPLETE` or `FAILED`, reading every page through `NextToken` [A] create-change-set, describe-change-set |
| Applying | `ExecuteChangeSet{ChangeSetName, StackName, ClientRequestToken}`; `DescribeStackEvents` until the stack settles; `DescribeStacks` for outputs [A] execute-change-set, stack-event, stack |
| Destroying | `DeleteStack`, then `DescribeStacks` until it is gone |
| Drift | `DetectStackDrift`, `DescribeStackDriftDetectionStatus`, `DescribeStackResourceDrifts`, which does not reach into nested stacks [A] drift |

- **The change set's name** matches `[a-zA-Z][-a-zA-Z0-9]*`, is at most
  128 characters, and is unique per stack [A] create-change-set.
- **`ChangeSetType`.** `UPDATE` refuses a stack that does not exist and
  `CREATE` one that does; a `CREATE` change set leaves the new stack in
  `REVIEW_IN_PROGRESS` until it is executed [A] create-change-set.
- **Executing one deletes the others:** "When you execute a change set,
  CloudFormation deletes all other change sets associated with the stack
  because they aren't valid for the updated stack" [A] execute-change-set.
- **`TemplateURL`** is an `https://` URL of a template of at most 1 MB;
  `TemplateBody` takes at most 51,200 bytes [A] create-change-set.
- **`CAPABILITY_AUTO_EXPAND`** has no effect on a change set, so a macro
  that wraps nested stacks needs `CreateStack` or `UpdateStack` [A]
  create-change-set.
- **Nested stacks.** With `IncludeNestedStacks`, each nested stack's change
  names its own change set in `ResourceChange.ChangeSetId`, and
  `DescribeChangeSet` of that ID describes it, with `ParentChangeSetId` and
  `RootChangeSetId` [A] describe-change-set, resource-change.
- **No changes.** A change set with nothing to change ends `FAILED`; the
  exact `StatusReason` it carries is *(unverified)* (C303).

### 5.1 The change set document

`DescribeChangeSet` answers `{Changes[], ChangeSetId, ChangeSetName,
StackName, Status, StatusReason, ExecutionStatus, Parameters[],
Capabilities[], IncludeNestedStacks, ParentChangeSetId, RootChangeSetId,
NextToken, …}` [A] describe-change-set.

- **A change** is `{Type: "Resource", HookInvocationCount,
  ResourceChange}` [A] change.
- **`ResourceChange`** holds `Action` (`Add`, `Modify`, `Remove`,
  `Import`, `Dynamic` or `SyncWithActual`), `LogicalResourceId`,
  `PhysicalResourceId`, `ResourceType`, `Replacement` (`True`, `False` or
  `Conditional`), `Scope[]`, `Details[]`, `PolicyAction` and, for a nested
  stack, `ChangeSetId` [A] resource-change.
- **A detail** is `{ChangeSource, CausingEntity, Evaluation, Target}`, and
  `Target` is `{Attribute, Name, Path, RequiresRecreation,
  AttributeChangeType, BeforeValue, AfterValue}` [A] change-detail, target.
- **Property values.** Only a change set created with
  `IncludePropertyValues` carries `BeforeValue`, `AfterValue`,
  `BeforeContext` and `AfterContext`, the last two JSON text of the
  resource's properties [A] describe-change-set, change-sets-view.

| CloudFormation | Change type |
|---|---|
| `Add` | `ADD` |
| `Remove` | `DELETE` |
| `Import` | `IMPORT` |
| `Modify`, `Replacement: False` | `CHANGE` |
| `Modify`, `Replacement: True` | a replacement, which CloudFormation makes by creating first and deleting after |
| `Modify`, `Replacement: Conditional`, and `Dynamic` | `CHANGE`, the most known at plan time |

### 5.2 Outputs and state

`DescribeStacks` answers `Stacks[0].Outputs[]` as `{OutputKey,
OutputValue, Description, ExportName}` [A] stack, output. An output has no
sensitive flag, and `NoEcho` does not mask the template's `Outputs` [A]
parameters, so every output is plain. CloudFormation holds the stack's
state; the template bucket keeps each run's templates under its ID.

### 5.3 Secrets

- **`NoEcho` parameters.** CloudFormation "returns the parameter value
  masked as asterisks (\*\*\*\*\*) for any calls that describe the stack
  or stack events", except what the template's `Metadata`, its `Outputs`
  and a resource's `Metadata` attribute hold [A] parameters.
- **Write-only properties.** A resource type's schema lists
  `writeOnlyProperties`, "often used to contain passwords, secrets, or
  other sensitive data", as JSON pointers of the form `Target.Path` takes
  [A] resource-schema.
- **Not stated** by any page read: whether `DescribeChangeSet`'s
  `Parameters[]`, `BeforeValue`, `AfterValue` and the contexts mask a
  `NoEcho` value or a write-only property *(unverified)* (C303).
- **Without `IncludePropertyValues`** a change set carries names, paths
  and actions, and no property value [A] describe-change-set.

Spacelift's CloudFormation runs are unread: which calls they make, what
their policy block holds and what deleting a stack with resources does are
*(unverified)* (C296).

## 6. What each contract takes

| | Pulumi | Kubernetes | Ansible | CloudFormation |
|---|---|---|---|---|
| Plan document | the preview's `resOutputsEvent`s from `--event-log` | `get` and the dry-run stream, as `before` and `after` per object | the callback's task results and recap | every `DescribeChangeSet` page, root and nested |
| Outputs | the root stack resource's `outputs` in `stack export`, a signature object marking a sensitive one | none | none | `DescribeStacks` outputs, all plain |
| State | the `loginURL` backend | the cluster, by label | the hosts | AWS |
| A secret | `[secret]`, or a signature object | base64 `data`, plaintext `stringData` and annotation | a module's result, a diff, `hostvars` | masked or unknown (C303) |
| Sanitization | collapse each signature object to `"[secret]"`; hash nothing | hash each Secret's `data` and `stringData` values and its last-applied annotation | keep names and counts; drop results and diffs | hash each parameter value; request no property values |
| Plan policy block | `pulumi.steps`, one per output event, `{op, urn, type, provider, old, new}` | `kubernetes.resources`, the sanitized plan document (C297) | `ansible`: the recap without `custom`, plus `playbook` | `cloudformation.changes`, the API's changes |

The decisions name where each lives: commands, documents, images and tools
in [0008](../decisions/0008-run-execution-and-worker-protocol.md#vendors-runs),
sanitization and the blocks in [0009](../decisions/0009-policy-engine-opa.md#vendor-plan-documents),
state in [0010](../decisions/0010-state-backend.md#how-a-run-reaches-it),
acceptance in [0016](../decisions/0016-testing-and-acceptance.md#acceptance-harness)
and templates in [0024](../decisions/0024-expression-language.md#workflow-command-templates).

## 7. Open

- Spacelift's Pulumi destroy (C295), CloudFormation runs (C296) and
  Kubernetes runs (C297).
- Helm through a `helm template` hook (C298).
- The Pulumi CLI and a provider plugin on Alpine (C299).
- Spacelift's Pulumi passphrase and resource plugin (C300).
- Pulumi's `update`, `read`, `import` and `discard` steps (C301).
- The role prefix of Spacelift's Ansible task names (C302).
- CloudFormation's change sets and `NoEcho`, write-only properties and an
  empty change set (C303).
- How Spacelift's `Delta` counts a replacement stays C198; Pulumi's own
  summary counts it once.

## Sources

- The tools, run in Docker on 2026-11-17 on arm64: `pulumi/pulumi-base`,
  `rancher/k3s` at `v1.37.2-k3s1` and `python` with ansible-core from PyPI,
  with the `random` provider plugin from Pulumi's GitHub releases. The
  documents kept are
  [`test/fixtures/vendor-documents`](../../../test/fixtures/vendor-documents/README.md),
  with each image's digest.
- AWS's reference, read 2026-11-17: the pages under the short names above.
- Spacelift's console on 2026-11-17: [32](32-spacelift-console-survey.md)
  section 16.8 and
  [`test/fixtures/policy-inputs`](../../../test/fixtures/policy-inputs/README.md)
  (`plan-pulumi.json`, `plan-ansible.json`).
- Report [20](20-vendors.md) and the open-questions register,
  [`open-questions.json`](../../data/open-questions.json).
