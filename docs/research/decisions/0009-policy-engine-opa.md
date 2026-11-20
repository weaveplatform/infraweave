# 0009: Policy engine: embedded OPA, Rego v0 and v1

Status: Accepted

## Context

Spacelift's policies are Rego programs that decide logins, what a VCS event does, whether
a plan may proceed, who approves a run, which stacks a finished run triggers and where a
notification goes. Report [09](../reference/09-policies.md) records the ten live types and
three aliases, the two engine versions, the rules each type defines and how several
policies combine, the input document of each type, flags, sampling and the point in the
run lifecycle where each type runs. infraweave must reproduce those semantics, because
users bring policies written for Spacelift and expect the same outcome from the same
input.

Three facts shape the engine:

- **Policies are API data, attached to targets.** The set to evaluate is known from the
  database at evaluation time ([09 section 7](../reference/09-policies.md#7-attaching-policies)),
  so OPA's bundle distribution has nothing to do.
- **Each policy carries its own Rego version** (`engineType`), and Spacelift disables five
  built-ins and prepends a preamble per type
  ([09 section 2](../reference/09-policies.md#2-policy-engine-and-language)).
- **Evaluation sits inside run transitions.** A plan policy decides where a run goes after
  `PLANNING`; an approval policy holds a run in `QUEUED`, `UNCONFIRMED` or
  `PENDING_REVIEW` ([09 section 8](../reference/09-policies.md#8-where-each-type-is-evaluated)).
  The run state machine is decision [0008](0008-run-execution-and-worker-protocol.md).

Report 27 compares the ways other tools run Rego
([27 section 9](../reference/27-reference-materials.md#9-opa-as-the-embedded-policy-engine)).

## Decision

### Engine

- **OPA is linked as a Go library** through `github.com/open-policy-agent/opa/v1/rego`,
  `v1/ast` and `v1/topdown`, at the latest release. The deprecated root `rego` package,
  the `sdk` package, bundles, the decision-log plugin and an `opa` sidecar are not used.
- **`internal/platform/rego`** owns compilation, preambles, capabilities, prepared
  queries, cancellation and print capture. It imports no domain and is usable by
  the server and launcher.
- **`internal/policies`** is a flat domain: policy records and attachments, input
  construction, typed decisions, combination, samples, captures and simulation.
  Server callers use `policiesapi.Evaluator`; the launcher uses `platform/rego`
  directly for its private worker-side initialization policy.
- **`internal/runs`** owns `run_policy_receipts` and `run_flags` in its migration
  stream, because their writes commit with run state/history and outbox records.
  Policies returns decision evidence; it imports none of its callers and never
  writes their tables. No-run evaluations use evaluation metadata/events, not
  fabricated run rows. Cross-domain policy/stack IDs are validated through
  their owning APIs and use 0004's holds/guards/purges, never SQL foreign keys.
  `internal/iac/sanitize` is the shared document/hash code.
- **`policyRuntime.openPolicyAgentVersion`** answers the OPA module version linked into
  the binary, read from `runtime/debug.ReadBuildInfo`.

### Rego versions

| `engineType` | Parser and compiler setting |
|---|---|
| `REGO_V0` | `ast.RegoV0` |
| `REGO_V1` | `ast.RegoV1` |

- A create that omits `engineType` stores `REGO_V1`. An update that omits it keeps the
  stored value.
- Each setting refuses the other's syntax with the parser's own message: a v0 rule
  without `if` and `contains` under `REGO_V1`, and a v1 rule under `REGO_V0` without the
  `future.keywords` import the message names
  ([33 section 8.3](../reference/33-vendor-tool-documents.md#83-rego-v0-and-v1)).
- **Rego v2 is released but deliberately unsupported.** OPA v1.22.0 introduced it;
  the linked v1.22.4 implements it. The supported policy language stays v0/v1,
  independently of the runtime's reported library version. Other engine values
  fail the schema's `enum` and answer `400`, problem `invalid-request` (decision
  [0022](0022-api-errors-and-update-semantics.md)). A v2 API proposal needs its own
  scoped compatibility decision; updating OPA does not enable it.
- `future.keywords` means the original `contains`, `every`, `if` and `in` set.
  Those named imports and `rego.v1` remain supported. Production capabilities
  exclude v2 and its import: `rego.v2`, new `not`/`and`/`or` keyword imports and
  unknowns fail with located compile diagnostics. This preserves existing
  wildcard policies' undefined-negation semantics. The actual embedded-library,
  MIT corpus, preamble/cache and FIPS probes are recorded in
  [34](../reference/34-rego-v2-compatibility.md).
- A policy may switch engine on update. The new body is compiled under the new engine
  before the update commits.

### Preamble, package and save-time validation

- **Every body is `package infraweave`.** Another package answers `422`, problem
  `validation-failed`, at save.
- **Preamble.** For each type, `internal/platform/rego` holds a generated Rego v1 module in
  `package infraweave` that defines `result` as an object naming every rule of the type,
  `default` values for its boolean rules and an always-false placeholder for each set rule
  ([09 section 3](../reference/09-policies.md#3-rules-outcomes-and-combination-per-type)).
  The preamble is compiled beside the user's module, each parsed under its own Rego
  version. A body that redefines a preamble rule with another kind (a boolean `deny` on a
  plan policy, a second `default allow`) fails compilation, and the save answers `422`,
  problem `validation-failed`, with OPA's error and line in the `errors` member. OPA
  locates an error at a point, its file, row and column; the range a diagnostic gives runs
  from the location's byte `Offset` over its `Text`, which `ast.Location` holds and its
  JSON leaves out ([33 section 8.2](../reference/33-vendor-tool-documents.md#82-diagnostics)).
- **The query** is `data.infraweave.result`. The engine reads only the rules of the type
  from the result; anything else the policy defines is ignored. OPA answers only the rules
  a policy defines, a set rule as `[]` when nothing matches and a boolean rule absent unless
  true, with helper rules beside them
  ([33 section 8.1](../reference/33-vendor-tool-documents.md#81-results)), so the preamble's defaults are what give every
  decision Spacelift's full set of keys, as the recorded decisions hold.
- **Save-time compile.** Create, update and simulate compile the body with the preamble,
  the capability set and the custom built-ins before anything is stored, so a policy
  that would fail at evaluation time is never saved.
- **Limits.** A body is at most 256 KiB; a simulate input at most 4 MiB; a policy may not
  `import` anything except `rego.v1`, `future.keywords` and `input`/`data` paths.
- **Type aliases.** `STACK_ACCESS`, `TASK_RUN` and `TERRAFORM_PLAN` are superseded and
  absent from the served enum (decision 0021), as is `ACCESS`. A request that sends one
  fails input validation, and the problem's `detail` names the replacement
  ([09 section 1.1](../reference/09-policies.md#11-the-policytype-enum)).

### Capabilities and built-ins

The capability set is `ast.CapabilitiesForThisVersion()` minus these built-ins, so using
one is a compile error at save:

| Removed | Why |
|---|---|
| `http.send` | Spacelift disables it; network access from the server |
| `net.lookup_ip_addr` | DNS lookups from the server; a departure, see Rationale |
| `opa.runtime` | Spacelift disables it; exposes process environment |
| `rego.parse_module` | Spacelift disables it |
| `time.now_ns` | Spacelift disables it; time comes from the input |
| `trace` | Spacelift disables it; `print` replaces it, see below |

| Custom built-in | Signature | Available to |
|---|---|---|
| `sanitized(x)` | any JSON value to the same type; strings hashed | every type |
| `exec(cmd)` | array of strings to object `{status, stdout, stderr}` | the worker-side initialization policy only |

- **`sanitized`** applies the same hash the worker applies to string values in plan input
  under default sanitization
  ([20 section 13](../reference/20-vendors.md#13-resource-sanitization)): the last eight
  hexadecimal characters of the SHA-256 of the UTF-8 string. Policies compare through the
  helper; C192's real simulation records this same truncation. The worker and
  helper use one implementation, matching the captured `hello` → `938b9824`
  vector. Non-string values pass through unchanged.
- **`print`** is enabled through `rego.EnablePrintStatements` and a `rego.PrintHook` that
  collects output into the evaluation record, at most 64 KiB per evaluation. Print output
  never reaches the server log.
- **FIPS 140-3 enforcement.** When the process runs with
  `GODEBUG=fips140=only` ([0013](0013-secrets-at-rest.md#fips-140-3)), the
  capability set also removes `crypto.md5`, `crypto.sha1`, `crypto.hmac.md5`
  and `crypto.hmac.sha1`, so a policy that uses them fails at save with `422`
  rather than at evaluation. Go refuses MD5 and SHA-1 in that mode, and its
  `crypto/md5.Sum` panics rather than returning an error. A policy saved
  before the mode was switched on fails to compile when it is next
  evaluated, and takes the outcome on error below.
- **Built-in errors** use OPA's default (an erroring built-in makes its expression
  undefined), not `StrictBuiltinErrors`, so policies written for Spacelift behave the same.

### Compilation cache

- Each policy compiles to a `rego.PreparedEvalQuery` keyed by
  `(SHA-256 of body, engineType, policy type, preamble version, effective capabilities)`. The key changes when the
  body or capability mode changes, so replicas need no invalidation messages.
  FIPS capability mode is part of the identity; a query compiled with MD5/SHA-1
  available can never be reused under FIPS enforcement.
- The cache is an LRU of 2,048 entries per process; a miss compiles on the calling
  goroutine. Prepared queries are shared across goroutines, as OPA allows.
- Each attached policy is evaluated with its own prepared query and its own input copy,
  so helper rules never leak between policies
  ([09 section 2.4](../reference/09-policies.md#24-combining-several-policies)). Policies
  of one type for one target evaluate concurrently, at most eight at a time.
  LOGIN is ordered by immutable policy ID because its documented nonempty
  `team` output rewrites the next policy's `session.teams`. Each stage still
  decodes its own document; only that typed team field passes between stages,
  under the same combined deadline.

### Timeouts and resource limits

| Limit | Default | Setting |
|---|---|---|
| Per evaluation: `PLAN` | 45 s | `policy.timeouts.plan` |
| Per evaluation: `GIT_PUSH`, `NOTIFICATION` | 10 s | `policy.timeouts.push`, `policy.timeouts.notification` |
| Per evaluation: every other type | 5 s | `policy.timeouts.<type>` |
| All policies for one event | 5 min | `policy.timeouts.combined` |
| All push policies for one VCS event | 10 min | `policy.timeouts.push_event` |
| Input document size | 64 MiB | `policy.max_input_bytes` |
| Messages per rule kept | 100, each cut at 4 KiB | none |

- Timeouts are context deadlines passed to `Eval`; OPA's topdown checks cancellation, so a
  runaway policy stops at the deadline.
- Settings are Go duration strings in the strict YAML configuration.

### Outcome on error

An evaluation that fails to compile, times out, exceeds a limit or returns a result of the
wrong shape is an **error**, recorded on the receipt with its message. Errors fail closed:

| Type | An error means |
|---|---|
| `LOGIN` | Login denied, with the error as the denial message |
| `GIT_PUSH` | No run for that stack or module; a failed commit status naming the policy; an internal-error notification |
| `PLAN` | As `deny`: the run goes to `FAILED` with the error in the note |
| `APPROVAL` | Undecided: the run keeps waiting; the next review or policy change evaluates again |
| `TRIGGER` | Nothing is triggered; an internal-error notification |
| `NOTIFICATION` | Nothing is routed; an internal-error notification to the inbox |
| `INTENT` | As `deny` |
| `TASK`, `INITIALIZATION` | As `deny` |

### Combination

Each type's rules for merging several policies are those of
[09 section 3](../reference/09-policies.md#3-rules-outcomes-and-combination-per-type),
implemented once per type in `internal/policies/internal/combine`: any `deny` wins for login and
plan; any `ignore` wins for push; every approval policy must approve and any reject
rejects; disagreeing push `priority` or `lock` values fail the evaluation; disagreeing
trigger priorities fall back to `normal` with a notification; login `deny_message` values
are de-duplicated, sorted, joined with `;` and cut at 1,024 characters.

### Evaluation points

Who calls the engine, when, and what the result does to the run of decision
[0008](0008-run-execution-and-worker-protocol.md):

| Type | Caller | When | Effect |
|---|---|---|---|
| `LOGIN` | `internal/access` (decision [0006](0006-authentication-and-tokens.md)) | Entry into an account (a sign-in, an account switch) and API key exchange, under the `LOGIN_POLICIES` scheme of decision [0007](0007-authorization-spaces-and-roles.md), with every `LOGIN` policy of the account's top space | Allow, admin, roles per space, teams, or denial with messages |
| `GIT_PUSH` | the `vcs.evaluate_event` job (decision [0012](0012-vcs-integration-model.md)) | Per affected stack or module, before any run exists | Creates the run in `QUEUED` (T1); cancels runs, a queued one to `CANCELED` (T4) and one awaiting confirmation to `DISCARDED` (T27), each with a note naming the run that replaces it; sets lock, priority, external dependencies, module version, commit status. With no matching rule the push is ignored and listed among the stack's ignored runs |
| `APPROVAL` | `internal/runs` | On entering `QUEUED`, `UNCONFIRMED` and `PENDING_REVIEW`, and on each `runReview`, for proposed and tracked runs alike; a review counts only in the state it was made in, earlier ones reaching the policy as `reviews.older` | `QUEUED` to `READY` waits for approve (T2); reject gives T5, T24 or T29 with the note `Run rejected by approval policies`; approve in `PENDING_REVIEW` gives T23; in `UNCONFIRMED`, confirmation waits for approve |
| `PLAN` | `internal/runs` | When the worker reports a finished plan, and on each targeted replan | Picks the next state: `FAILED` (T18), `UNCONFIRMED` (T20), `APPLYING` (T21), `PENDING_REVIEW` (T22) or `FINISHED` (T17) |
| `INITIALIZATION` (deprecated, server-side) | `internal/runs` | Before `QUEUED` to `READY` | `deny` sends the run to `FAILED` with the messages |
| `TASK` (deprecated) | the task create handler | Before the task run is created | `deny` creates no run and answers `403`, problem `forbidden` (decision [0022](0022-api-errors-and-update-semantics.md)), with the messages in `detail` |
| `TRIGGER` | the `policy.trigger` job | A tracked run or task reaches a terminal state, `FAILED` and `DISCARDED` included; a module version is released | Creates tracked runs on the named stacks, each on its stack's tracked commit, with `triggered_by` `policy/<stack>/<run>` and no `trigger`; the finishing run's flags do not pass to them |
| `NOTIFICATION` | event routing (decision 0015) | Every event decision 0015 defines, so every state change of a run in the policy's space or below | Routes to the inbox, Slack, webhooks, pull request comments; never attached to a stack |
| `INTENT` | Intent operations | Each resource operation | Allow, deny or pending review |
| `ACCESS` | never | n/a | Not served: absent from the served `PolicyType` (decision 0021) |

- **Plan decision.** The worker's report of a finished plan (decision
  [0008](0008-run-execution-and-worker-protocol.md)) carries the plan artifacts; the server
  builds the plan input from them, evaluates within the combined budget and answers the
  worker with the next state. With autodeploy on, the worker continues to `APPLYING` in the
  same container without a second handover, as T21 requires.
- **Trigger and approval are durable.** The transition that ends a blocking run enqueues a
  `policy.trigger` job in its own transaction (decision
  [0011](0011-persistent-work-queue.md)), so a crash between the end of a run and the
  trigger evaluation loses nothing.
- **Inputs are snapshots.** The input is built from the committed state at evaluation time;
  `request.timestamp_ns` and `infraweave.request.timestamp_ns` come from the server clock at
  that moment, which is the only clock a policy sees.

### The worker-side initialization policy

- The launcher (decision [0008](0008-run-execution-and-worker-protocol.md)) links the same
  `internal/platform/rego` engine. It reads the policy file named by its configuration, compiles it
  at start with the initialization preamble, and refuses to start when the file does not
  compile ([10 section 10](../reference/10-workers-and-execution.md#10-signed-runs-and-worker-side-policy)).
- It evaluates the policy during the handover, before it starts the run container, as
  decision [0008](0008-run-execution-and-worker-protocol.md) describes. The input carries
  at least `run.user_provided_metadata`, `run.commit`, `run.type` and
  `static_run_environment` (stack slug, space, pool).
- `exec` runs the command in a short-lived container of the run's runner image, so a
  verifier binary shipped in the image can be called before the run starts. Each call has
  a 60 s deadline, and its stdout and stderr are each capped at 1 MiB.
- A `deny` fails the run from `PREPARING` (T11) with the messages written to the
  preparing log. The server never sees the policy body.

### Receipts, flags and sampling

- **Receipts.** Every evaluation about a run writes a `run_policy_receipts` row (report
  [28 section 12.3](../reference/28-data-model.md#123-stacks-and-runs)): policy, type,
  outcome, flags, `sampled`, engine type and error, in the same transaction as the
  transition it caused.
- **Flags.** Flags live in `run_flags (run_id, flag, policy_id)`, one row per owner. An
  evaluation applies its `reset_flag` entries first, deleting only its own ownership rows,
  then inserts its `flag` entries ([09 section 5](../reference/09-policies.md#5-flags-flag-reset-and-sampling)).
  A flag is visible in `run.flags` while any owner row remains. Every infraweave policy has
  a ULID, so every policy can reset. C51's 2026-11-19 real samples also show a
  newly created Spacelift policy with a slug-shaped public ID resetting its flag;
  public ID syntax does not determine eligibility. The legacy-policy comparison
  remains unobserved and is not proved by this new-policy observation.
- **Sampling.** When `sample` is true (login by default), the engine stores a record of
  input, body, engine type, result, outcome, error and print output.
  - Hourly sample budgets per account: 200 for `NOTIFICATION`, 200 for `GIT_PUSH`, 500 for all
    other types together, counted in `policy_sample_budgets (account_id, hour, bucket)` with
    an atomic upsert. Over budget, the evaluation is recorded but not sampled.
  - The record is JSON, sealed with the streaming blob sealing of decision
    [0013](0013-secrets-at-rest.md) into the `policy-inputs` store (decision
    [0005](0005-persistence-postgresql.md)) under the key
    `<account>/<policy>/<evaluation ulid>`, its content key bound to the
    store and that key as 0013 binds every object.
  - `policy_evaluations` (report [28 section 12.4](../reference/28-data-model.md#124-policies-and-contexts))
    holds the key, outcome, timestamp and object key. Samples expire after 7 days, through
    the `blob.expire` task; a receipt whose sample is gone answers `sampleExpired: true`.
- **Captures.** `policyInputCaptures` keeps the newest input per space and capture kind,
  replaced at most once a minute, with the fields the contract marks sensitive removed,
  sealed in the same store.
- **Simulation.** `policySimulatev2` compiles and evaluates with the same preamble,
  capabilities, built-ins (never `exec`) and timeouts, writes no receipt and no sample, and
  answers the raw result.

### Input documents

- Each type's input is a Go struct in `internal/policies/internal/inputs` whose JSON names follow the
  per-type contract of [09 section 4](../reference/09-policies.md#4-input-documents),
  including absent versus `null` where the report records a difference.
- **The `infraweave` namespace.** Where Spacelift's contract nests fields under a
  `spacelift` key, infraweave's input nests the same fields under `infraweave`, as
  0001's rename table maps `spacelift.*` to `infraweave.*`; every other field keeps its
  contract name. A policy that reads `input.spacelift` finds nothing, so the Spacelift
  importer rewrites `package spacelift` to `package infraweave`, and `input.spacelift`
  and `data.spacelift` to `input.infraweave` and `data.infraweave`, in each imported body
  and test, and reports every rewrite.
- infraweave publishes the JSON Schema it generates from those structs at
  `GET /.well-known/policy-contract.json`, in the shape of Spacelift's contract.
- **Recorded inputs.** `test/fixtures/policy-inputs` holds eighteen documents Spacelift
  evaluated, redacted with every key kept
  ([32 section 16](../reference/32-spacelift-console-survey.md#16-driven-policies-runs-and-the-registry)).
  Every key they hold has a field in the structs, after the `spacelift` to `infraweave`
  mapping. Where they differ from the contract
  ([09 section 4](../reference/09-policies.md#4-input-documents)), the structs follow the
  documents: `reviews.current` and `reviews.older` are objects of `approvals` and
  `rejections`, and each trigger `stacks[]` entry carries `queued_runs`. Values the
  documents leave null where the field is known, such as the trigger input's
  `stack.space`, infraweave fills.
- Under `DEFAULT` and `SMART`, the worker transforms the applicable string values
  before uploading server-readable plan JSON. `NONE` deliberately retains the
  Terraform-family values under the explicit opt-out label; it is outside that
  sanitization guarantee. Every mode still seals artifacts. The binary plan and
  pool-sealed workspace used for apply retain their separate 0008/0013 contracts.

### Vendor plan documents

Each vendor's `plan.json`
([0008](0008-run-execution-and-worker-protocol.md#vendors-runs)) is the
source of its block in plan policy input, and the worker sanitizes it before
upload. Only the stack's vendor's block is filled, as
[20 section 2](../reference/20-vendors.md#2-plan-policy-input-per-vendor)
gives it.

| Vendor | Block | Sanitized |
|---|---|---|
| Terraform, OpenTofu | `terraform`, and `opentofu` for OpenTofu: `{resource_changes, checks, terraform_version}`, `opentofu_version` in `opentofu`, from `show -json`, each change as the tool writes it | by the stack's mode, at the paths below |
| Terragrunt | `terragrunt`, one entry per unit of `plan.json`'s `units`, each with its unit's path, `resource_changes`, `terraform_version` and `checks` | by the stack's mode, unit by unit; `checks` is empty unless sanitization is off |
| Pulumi | `pulumi`, `{"steps": [...]}` as `plan.json` holds them | every secret signature object becomes the string `"[secret]"`, and nothing is hashed, whatever the mode |
| CloudFormation | `cloudformation`, `{"parameters": [...], "changes": [...]}` with the API's key names | every `ParameterValue` is hashed, whatever the mode; the change set carries no property values to hash |
| Kubernetes | `kubernetes`, `{"resources": [...]}` as `plan.json` holds them | each Secret's `data` and `stringData` values, and its `kubectl.kubernetes.io/last-applied-configuration` annotation, are hashed in `before` and `after`, whatever the mode; other values stay as kubectl wrote them |
| Ansible | `ansible`, `{playbook, processed, ok, changed, dark, failures, skipped, rescued, ignored}` from the recap | nothing to hash: the records hold names, statuses and counts |

- **The modes.** The run specification carries the stack's mode, which the
  handover decides: `NONE` when the stack has the label
  `feature:disable_resource_sanitization`, `SMART` when it sets
  `useSmartSanitization`, else `DEFAULT`. `DEFAULT` hashes every string
  value of the plan's resources, outputs and variables with `sanitized`'s
  hash, and leaves the structure a reader needs (addresses, types, names,
  provider names, actions, modes and versions); `SMART` hashes only the
  values the plan marks sensitive and their identical copies in value payloads.
  Recorded Terraform/OpenTofu plans leave some copies of sensitive inputs
  unmarked in resource outputs; SMART gathers sensitive strings before changing
  any value, then hashes every identical payload copy once, including across
  Terragrunt units. Structural names/addresses/versions stay unchanged even if
  they equal a sensitive string (maintainer decision, 2026-11-19). Configuration
  constants and sensitive variable defaults follow the same secret-exclusion
  rules below. The modes apply to the Terraform family
  alone ([20 section 13](../reference/20-vendors.md#13-resource-sanitization)).
- **Terraform and OpenTofu** write every sensitive value in plaintext beside
  a marker, at the same paths in both tools
  ([33 section 6.5](../reference/33-vendor-tool-documents.md#65-secrets-sensitive-ephemeral-and-write-only-values)).
  `SMART` hashes the value at each marked path of `plan.json`: under
  `before_sensitive` and `after_sensitive` in `resource_changes`,
  `resource_drift` and `output_changes`, under each resource's
  `sensitive_values` in `planned_values` and `prior_state`, and each output
  marked `sensitive`; the value under `variables` of each variable that
  `configuration` declares `sensitive`; and every `constant_value` in
  `configuration`, where a literal for a write-only attribute shows
  unmarked. `DEFAULT` hashes every string value of those members. An
  ephemeral value reaches no document, and a write-only attribute reads
  `null`, so neither needs a rule. A Terragrunt unit's plan is sanitized
  the same way; its `variables` hold Terragrunt's `inputs`
  ([33 section 7.2](../reference/33-vendor-tool-documents.md#72-plans-outputs-and-render)).
- **The `terraform` block** holds `resource_changes`, `checks` and the
  version, the three members Spacelift's captured blocks hold
  ([33 section 6.9](../reference/33-vendor-tool-documents.md#69-compared-with-the-parsers-fixtures-and-spacelift));
  `variables`, `output_changes`, `resource_drift`, `prior_state`,
  `planned_values` and `configuration` stay in `plan.json`. A change keeps
  every member the tool writes, `action_reason`, `replace_paths`,
  `previous_address` and `importing` among them; which of those Spacelift
  keeps is C306.
- **State** carries the same values, marked by `sensitive_attributes` and
  `sensitive_values`, and a write-only attribute's stored copy in
  plaintext ([33 section 6.4](../reference/33-vendor-tool-documents.md#64-outputs-and-state)). The backend
  stores it as the tool writes it, sealed
  ([0013](0013-secrets-at-rest.md)), and nothing read from it reaches a
  policy input.
- **Pulumi** writes a secret in its event log as a signature object whose
  `ciphertext` already reads `[secret]`; Spacelift's policy input carries
  the same steps
  ([33 section 2.3](../reference/33-vendor-tool-documents.md#23-spacelifts-steps)),
  so the worker only collapses each object to the string, which is how
  policies read a Pulumi secret. A value unknown at preview stays Pulumi's
  sentinel string.
- **Kubernetes** has no sanitization setting, yet every dry run and `get`
  carries a Secret's values, in base64 under `data` and in plaintext under
  `stringData` and in the last-applied annotation
  ([33 section 3.4](../reference/33-vendor-tool-documents.md#34-secrets)).
  Hashing them whatever the label keeps plaintext secrets off the server,
  which is this record's rule for every plan.
- **CloudFormation** parameter values are hashed because a `NoEcho`
  parameter's value in a change set is not documented as masked (C303).
  The change set is created without property values, so no `BeforeValue`
  or `AfterValue` reaches the plan.
- **Ansible's** callback writes no module result and no diff
  ([0008](0008-run-execution-and-worker-protocol.md#vendors-runs)), so its
  records hold nothing to sanitize.
- `sanitized(x)` hashes as the worker does for every vendor that hashes, so
  a policy compares a Kubernetes Secret's value or a CloudFormation
  parameter the way it compares a Terraform attribute.



### Modern full-plan value families

The full artifact, unlike the published three-member policy projection, also
carries provider action and deferred-resource values. DEFAULT/SMART sanitize
`action_invocations[*].config_values`, with `config_sensitive` supplying SMART
markers, and preserve
`config_unknown` and invocation identity/provenance. Deferred resource changes
use the ordinary change value and marker paths inside
`deferred_changes[*].resource_change.change`; their reason, wildcard address
and unknown index stay structural. Resource identity/import attribute values
and opaque generated HCL follow the existing value rules. SMART collects marked
strings across these families and selected units before hashing identical value
copies. Strict known fields and marker/metadata shapes fail closed without
writing a fallback or echoing unknown keys or values.

These sections stay artifact-only. Terraform/OpenTofu policy input remains
`resource_changes`, `checks` and the version, with the established alias/unit
rules. No new evaluator input member, action execution or experimental release
admission follows from artifact compatibility. NONE preserves valid supported
document bytes and all artifacts remain sealed; existing worker capability and
receipt fences remain in force.

[Report 35](../reference/35-modern-plan-payloads.md) distinguishes real stable
action production from an actual public-alpha resource fixture transported
through the stable worker for compatibility. The public alpha's deferred-action
plan cannot be read as JSON in the recorded probes. Nonempty
`deferred_action_invocations` therefore remain refused under DEFAULT/SMART;
NONE is the explicit sealed opt-out. #675's required real deferred-action
fixture and value/mask acceptance stay open. Source declarations or the
maintainer's separate workflow-capture inference approval do not settle them.


### Policy phase ownership and capture gates

P13 constructs `policies` after configuration and before runs. Runs and VCS use
`policiesapi.Evaluator`; policies imports only the domain APIs in the declared
graph. Access owns `accessapi.LoginEvaluator`, implemented by policies and bound
by app after construction. Access never imports policiesapi. The policies check
surface is supplied through `policies.Surfaces()` and served by expressions;
its authorization and compiler are the same ones used by policy management.

POL-05's management boundary stores policies under account-qualified ULIDs and
the policies feature gate. Omitted create engines use the intentional V1
default; an omitted update engine preserves the version locked by that write,
including after a transaction retry. Failed compilation commits no policy or
change event. The REST merge patch uses the same compiler and conditional-write
path as replacement. Compile diagnostics locate the submitted `body` with
one-based Unicode columns and an exclusive end across multiline byte spans;
locations in an internal preamble do not become user-source ranges. LOGIN
policies stay in the account's root space. These management operations and
their persisted searches do not deliver attachment selection, evaluation,
sampling, notification routing or Intent integration owned by later tickets.

POL-06 supplies the attachment boundary separately. Each policy/stack pair has
one account-qualified relation and an attachment ULID distinct from the policy.
Explicit attachment preserves that identity and clears its automatic marker;
automatic reconciliation cannot replace it. The account's policy attachment lock
precedes policy row locks. Policy saves reconcile labels transactionally, and
stack-change consumers read current labels and reach rather than replaying old
event payloads. LOGIN, NOTIFICATION and INTENT do not attach to stacks.

Access calls the optional `SpaceChangeGuard` under its tree and subtree locks
for parent or inheritance changes. Policies refuses lost manual reach and
enqueues automatic reconciliation in that same transaction. Jobs are unique per
accepted space version, so a change cannot be lost to an already-running job's
earlier snapshot. Inheritance-only updates lock descendants `FOR UPDATE`, which
waits for attachment writers' space holds. Stack guards refuse manual reach loss
and remove owned rows on deletion. Effective projections check current reach and
labels while durable cleanup is pending. Preview counts and limits are applied
inside the stack-owned query after account, reach and readable-space filtering;
its sample uses persisted stack update time, with a deterministic ID tie break.
This selection boundary does not claim workflow evaluation, module or Intent
attachment execution, samples or notification routing.

P13 emits `policy_evaluated` and `internal_error` through the transactional
outbox and enqueues existing VCS checks. P18 adds notification policy routing,
inbox delivery and other sinks; an emitted event is not proof of those sinks.
P13b implements account login, TASK/server INITIALIZATION and policy tooling.
P14 owns launcher INITIALIZATION and `exec`; P16 owns dependency execution; P17
owns module-version triggers and C55's module-version capture. P20 owns the
remaining vendors and full Terragrunt changes/outputs; P20b owns plugins;
P24c owns console components; P25 owns Intent execution and attachments.
Future input shapes do not imply those workflows are implemented.

The shared sanitization transform hashes value strings, not structural names:
resource addresses/types/provider names, actions, modes, versions and module
paths stay readable. DEFAULT covers before/after values, planned/prior values,
outputs and variables; both modes cover every configuration `constant_value`.
SMART additionally follows sensitive markers and sensitive variable declarations,
including nested modules and whole-value markers. Preserve numbers, booleans,
nulls and JSON shape. Malformed JSON, invalid mode, invalid value/marker shape
or an exhausted input bound fails closed; never upload the original as fallback.
A Terragrunt `units` envelope applies the transform independently to each unit's
`plan`; P13 must collect/upload that minimal JSON even though its runner currently
saves only the workspace. Artifact bounds remain 0008's; policy input bounds
remain this record's limits. The opt-out does not disable artifact sealing.

Required live behavior captures block their affected implementation tickets unless
a dated maintainer decision explicitly accepts an inference, as below.
The table in plan P13 names every gate, including remaining C9 policy deletion,
C46, C48, C50, C51, C52, C56 and C291. An inaccessible tier or support-only fact
stays unobserved; the approved inference below can unblock implementation. C49/C54/C57/C192 are recorded, with explicit REST replay
handoffs. Do not close a capture by converting an interim choice into a fixture.

### Maintainer-approved inferences for the remaining captures

On 2026-11-19 the maintainer instructed **use inference** for #643's inaccessible
cases. This unblocks the affected implementation; it does not turn an inference
into a captured Spacelift fact. C46, C48 and C51 retain their open evidence
questions and the authentic partial fixtures. C9's context/plugin remainder
stays with its existing owner.

- **C48: evaluation gates.** Infer WORKFLOW_POLICIES covers GIT_PUSH and TRIGGER.
  The free account's console disables Access, Push, Trigger and Notification;
  ACCESS_POLICIES and NOTIFICATION_POLICIES separately name the other two.
  Actual free-tier receipts demonstrate PLAN, APPROVAL and INITIALIZATION
  evaluation. TASK and LOGIN are outside the inferred WORKFLOW_POLICIES pair.
  Do not apply a blanket WORKFLOW_POLICIES refusal to policy creation, update
  or attachment: the live free-tier operations permit them. Installation-wide
  policies controls, ordinary authorization and the intentional ACCESS
  supersession still apply. This is an inferred type-to-feature mapping,
  pending an entitled-account comparison; NOTIFICATION retains its separate
  evaluation feature.
- **C46: ignored reasons.** Infer CUSTOM_POLICY for an explicit custom ignore.
  Infer CUSTOM_POLICY_NO_TRIGGER when the combined custom result schedules
  no run without an explicit ignore, including a tracked notrigger result or
  no positive action. Use DISABLED_BY_LABEL when no custom policy is attached
  and the default policy is disabled. The upstream label was not observed:
  infraweave deliberately selects `feature:disable_default_push_policy` for
  that control. Its name and the reason split are implementation choices
  under this authorization, not claims of observed upstream compatibility.
  A custom policy replaces the default; the label does not override a custom
  policy's result. Existing observed default push/filter behavior is retained.
- **C51: reset eligibility.** Follow the documented internal PolicyULID rule
  for interpreting upstream legacy behavior, without inferring that internal
  field from a public slug or ULID. Every newly created infraweave policy has
  its own stable owner ID and can reset its own ownership rows. The real
  new-slug capture supports keeping public ID syntax out of eligibility.
  Acquiring/migrating missing upstream legacy IDs is not implemented or
  claimed verified.

Acceptance tests must identify these expectations as inferred infraweave
behavior. A future contradictory real capture is a parity finding; update the
contract and affected implementation through a scoped ticket. Do not create a
synthetic upstream response to resolve one of these register records.

### P13b capture evidence, 2026-11-19/20

The [#644 fixture report](../../../test/fixtures/captures/C273/README.md) supplies
actual policy-page requests and active/inactive scheme session controls. C52
settles server initialization denial as READY → FAILED before worker preparation;
#660 owns the new transition/driver and exact-history implementation. The intended
TASK deny-before-run-creation behavior remains unobserved: V0/V1 controls on the
available account finish without TASK receipts. This does not retire TASK.

C53 establishes that input captures can be written with sample=false while
evaluation samples remain absent; changing sample=true does not itself replace
the latest account-level capture. Actual resource sample values use the shared
last-eight SHA-256 hash with no dummy plaintext copy. Older capture inputs remain
readable about 2.8 days later, and lists retain multiple records. The once-minute
newest-input design above is an infraweave choice, not an observed Spacelift
cadence or proof that older upstream inputs are erased. Exact retention, direct
non-admin sample reads and cross-space controls remain open in C53.

C50's live LOGIN flag experiment did not establish evaluation: even an explicit
test-key deny still issued a token, with no LOGIN samples. Keep the existing
no-run flag choice labelled as such. C47's unavailable legacy-space comparison
and the other unobserved cases remain capture questions; no synthetic success
or inferred response is added to these fixtures.

On 2026-11-20 the maintainer approved **documented inferences and continued
implementation** for #644's remaining LOGIN flags, TASK denial and capture
retention/privacy cases. Follow the accepted contracts above: no-run flags do
not create run-owned flag rows; a TASK deny refuses creation; samples/captures
use the declared sealing, access controls, budgets, expiry and replacement
rules. These are infraweave implementation expectations, not newly observed
Spacelift behavior. C47's legacy comparison and C50/C52/C53's missing
observations remain open evidence questions; they no longer block these
accepted implementations. Keep their authentic response bodies unchanged.

### Transactional evaluation boundary

POL-08 selects effective, account-qualified policy versions in one owned query
after holding the real target and its space. Current matching labels apply
before asynchronous relation cleanup, and stale automatic relations cannot
restore a lost match. Explicit relations still apply without a label match.
No policy row lock is taken after a caller's stack/run locks; the selected body,
engine and version are the evaluation snapshot.

The input builder runs once per attempt inside the joined transaction, only when
policies apply. Validate against the published typed contract before OPA decodes each
program's own input; invalid facts fail closed, while operational builder/store
failures roll back. An empty set returns NO_POLICIES and writes no evidence.
Module and Intent execution targets remain explicitly unavailable to P17/P25.

`policy.evaluated` and `policy.internal_error` carry safe metadata through the
existing outbox, with an immutable evaluation subject and target/run correlation
in the payload. Their event/fan-out jobs commit or roll back with the caller.
They contain no source, input, raw decision or print text. Requested sampling
evidence is a separate in-memory handoff excluded from JSON; P13b owns its sealed
persistence. A requested sample is not a retained sample, and an absent sampling
consumer does not block evaluation. Runs owns durable receipts and flags.

## Rationale

- **A library, not a sidecar or a binary per evaluation.** Login and approval sit on
  request paths; spawning `opa` per evaluation, as Atlantis and Terrakube do, costs a
  process per decision, and a sidecar adds a component for no gain
  ([27 section 9.4](../reference/27-reference-materials.md#94-do-not-copy)).
- **Prepared queries keyed by content** honour per-policy engine versions without
  reparsing and need no cross-replica invalidation
  ([27 section 9.3](../reference/27-reference-materials.md#93-borrow)).
- **Capabilities, not a runtime check.** Removing built-ins from the capability set makes
  their use a compile error at save, which is when Spacelift rejects them.
- **The preamble as a separate module** keeps the user's body byte-for-byte as stored, so
  error line numbers point at the user's own lines.
- **Fail closed.** A policy that cannot be evaluated must not let a plan apply or a user log
  in. Approval errors leave the run waiting rather than rejecting it, so a fixed policy can
  still approve on the next review.
- **Rejected: OPA bundles and the `sdk` package.** Policies are rows; bundle polling would
  add a second source of truth and a delay between save and enforcement.
- **Rejected: OPA decision logs.** Receipts and samples are the product's audit surface and
  must be sealed and bounded per hour; decision logs upload unsealed batches.
- **Departures from Spacelift**, for the deviations register:
  - `REGO_V1` is the default engine when a create omits it, as Spacelift's console opens
    every new policy on Rego V1
    ([32 section 16.1](../reference/32-spacelift-console-survey.md#161-the-policy-page-samples-simulation-and-templates));
    Spacelift's API also defaults to `REGO_V0`, as C49 records on 2026-11-19.
    The maintainer retains infraweave's V1 default intentionally (DV-028).
  - `net.lookup_ip_addr` is also removed, because DNS lookups from the server reach the
    network that Spacelift's five removals keep policies away from.
  - Evaluation errors have a stated, fail-closed outcome per type; Spacelift documents none.
  - `ACCESS` policies are never evaluated (decision 0021 holds their scope).
  - DV-031 is withdrawn: C192 records the last-eight-hex SHA-256 algorithm;
    infraweave adopts it by maintainer decision on 2026-11-19
    ([20 section 13](../reference/20-vendors.md#13-resource-sanitization)).
  - A Kubernetes Secret's values and a CloudFormation parameter's value are hashed
    whatever the stack's mode or label; Spacelift has no sanitization setting for either
    vendor, and what its policy input holds for them is open (C296, C297).
  - Under `GODEBUG=fips140=only`, the MD5 and SHA-1 built-ins are unavailable.
  - Input fields under Spacelift's `spacelift` key sit under `infraweave`, because no
    infraweave name says Spacelift (0001); the importer rewrites imported bodies.

## Constraints

- **Policy compatibility rests on input compatibility.** A Spacelift policy behaves the same
  only where infraweave's input matches the contract field for field. Fields infraweave
  cannot fill (for example `third_party_metadata.infracost` without an Infracost run) are
  absent, as Spacelift leaves them.
- **OPA upgrades change semantics rarely but not never.** OPA is kept at the latest release;
  the example-library corpus below runs on every upgrade.
- **A reject fails the run.** Spacelift's console fails a run rejected in `QUEUED`
  (T5 in [08](../reference/08-run-lifecycle.md#transition-table)), and fails a run that one
  approval policy rejects while another approves
  ([09 section 3.3](../reference/09-policies.md#33-approval)); infraweave fails the run on
  a reject in every state that evaluates approval policies, `PENDING_REVIEW` and
  `UNCONFIRMED` included. The [C291 fixture](../../../test/fixtures/captures/C291/README.md)
  directly verifies both states on 2026-11-19, including mixed approve/reject
  outcomes and the exact `Run rejected by approval policies` history note.
- **Flags from types with no run.** What such a flag does is unverified (C50); infraweave
  ignores such flags.
- Whether infraweave pins Spacelift's policy contract as a committed snapshot is PD-14,
  outside this record.

## Verification

- `internal/platform/rego` unit tests: compilation of every type's preamble under both engines;
  each removed built-in rejected at save, and the MD5 and SHA-1 built-ins rejected when
  enforcement is on; `sanitized` and `print`; the cache key; timeouts
  through a policy that loops; every combination rule of
  [09 section 3](../reference/09-policies.md#3-rules-outcomes-and-combination-per-type);
  the error table above; flag reset ordering and multiple owners; sample budgets.
- A corpus test runs the 68 templates and 62 test files at
  `8e8894fbd4cefffa3c261746bcdd70ef9fbb53a8` (C54) of the MIT example library
  ([09 section 10](../reference/09-policies.md#10-the-example-library-at-6ff5edb08bc8)) through
  `internal/platform/rego` and expects every library test to pass, after the importer's rewrite of
  the package, `input.spacelift` and `data.spacelift` in templates and tests alike, so the
  corpus also tests the rewrite.
- A contract test, `TestUnitPolicyContract`, compares the generated
  `policy-contract.json` with the field lists of
  [09 section 4](../reference/09-policies.md#4-input-documents) and with the keys and JSON
  types of every input document in `test/fixtures/policy-inputs`, and the decision
  structs with the keys of its `*.decision.json` documents.
- Gherkin acceptance (decision 0016): `policy/plan.feature` (deny fails, warn holds an
  autodeployed run in `UNCONFIRMED`, warn with approval sends a proposed run to
  `PENDING_REVIEW`), `policy/approval.feature`, `policy/push.feature`,
  `policy/trigger.feature`, `policy/login.feature`, and
  `policy/worker_initialization.feature` with a signed-run JWT, all against PostgreSQL and
  a real Terraform run.
- A sealing test reads a sample object directly from the object store and asserts that a
  known input value does not appear in it.
- Each vendor's sanitization tests run over `test/fixtures/vendor-documents`: no dummy
  secret of the recorded documents survives in a sanitized Pulumi or Kubernetes plan,
  each signature object reads `"[secret]"`, and the Ansible callback's records of the
  recorded project hold no module result, diff or dummy secret. `vendors/sanitization.feature` runs
  a plan policy over each vendor's input and compares through `sanitized`.

## References

- [09: Policies](../reference/09-policies.md).
- [27 section 9: OPA as the embedded policy engine](../reference/27-reference-materials.md#9-opa-as-the-embedded-policy-engine).
- [08 section 3: transitions](../reference/08-run-lifecycle.md#3-transitions).
- [26 section 7.3: policy types](../reference/26-scope-and-tiers.md#73-policy-types).
- [14 section 5.2: login policy rules](../reference/14-spaces-rbac-and-identity.md#52-login-policy-rules).
- OPA at `359db769deb0`: <https://github.com/open-policy-agent/opa>.
