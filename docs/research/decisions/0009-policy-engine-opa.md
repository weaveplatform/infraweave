# 0009: Policy engine: embedded OPA, Rego v0 and v1

Status: Proposed

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
- **Package `internal/policy`** owns compilation, the preamble, the capability set,
  custom built-ins, input building, combination, receipts and sampling. Every caller
  (run scheduler, VCS intake, authentication, event routing, the launcher) goes through
  `policy.Engine.Evaluate(ctx, Request) (Decision, error)`.
- **`policyRuntime.openPolicyAgentVersion`** answers the OPA module version linked into
  the binary, read from `runtime/debug.ReadBuildInfo`.

### Rego versions

| `engineType` | Parser and compiler setting |
|---|---|
| `REGO_V0` | `ast.RegoV0` |
| `REGO_V1` | `ast.RegoV1` |

- A create that omits `engineType` stores `REGO_V1`. An update that omits it keeps the
  stored value.
- Rego v2 is not offered until OPA releases it; any other engine value fails the schema's
  `enum` and answers `400`, problem `invalid-request` (decision
  [0022](0022-api-errors-and-update-semantics.md)).
- A policy may switch engine on update. The new body is compiled under the new engine
  before the update commits.

### Preamble, package and save-time validation

- **Every body is `package spacelift`.** Another package answers `422`, problem
  `validation-failed`, at save.
- **Preamble.** For each type, `internal/policy` holds a generated Rego v1 module in
  `package spacelift` that defines `result` as an object naming every rule of the type,
  `default` values for its boolean rules and an always-false placeholder for each set rule
  ([09 section 3](../reference/09-policies.md#3-rules-outcomes-and-combination-per-type)).
  The preamble is compiled beside the user's module, each parsed under its own Rego
  version. A body that redefines a preamble rule with another kind (a boolean `deny` on a
  plan policy, a second `default allow`) fails compilation, and the save answers `422`,
  problem `validation-failed`, with OPA's error and line in the `errors` member.
- **The query** is `data.spacelift.result`. The engine reads only the rules of the type
  from the result; anything else the policy defines is ignored.
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
| `sanitized(x)` | string to string | every type |
| `exec(cmd)` | array of strings to object `{status, stdout, stderr}` | the worker-side initialization policy only |

- **`sanitized`** applies the same hash the worker applies to string values in plan input
  under default sanitization
  ([20 section 13](../reference/20-vendors.md#13-resource-sanitization)): the first eight
  hexadecimal characters of the SHA-256 of the UTF-8 string. Policies compare through the
  helper, so the algorithm is infraweave's own and needs no byte-for-byte match with
  Spacelift's. Non-string values pass through unchanged.
- **`print`** is enabled through `rego.EnablePrintStatements` and a `rego.PrintHook` that
  collects output into the evaluation record, at most 64 KiB per evaluation. Print output
  never reaches the server log.
- **Built-in errors** use OPA's default (an erroring built-in makes its expression
  undefined), not `StrictBuiltinErrors`, so policies written for Spacelift behave the same.

### Compilation cache

- Each policy compiles to a `rego.PreparedEvalQuery` keyed by
  `(SHA-256 of body, engineType, policy type, preamble version)`. The key changes when the
  body changes, so replicas need no invalidation messages.
- The cache is an LRU of 2,048 entries per process; a miss compiles on the calling
  goroutine. Prepared queries are shared across goroutines, as OPA allows.
- Each attached policy is evaluated with its own prepared query and its own input copy,
  so helper rules never leak between policies
  ([09 section 2.4](../reference/09-policies.md#24-combining-several-policies)). Policies
  of one type for one target evaluate concurrently, at most eight at a time.

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
implemented once per type in `internal/policy/combine.go`: any `deny` wins for login and
plan; any `ignore` wins for push; every approval policy must approve and any reject
rejects; disagreeing push `priority` or `lock` values fail the evaluation; disagreeing
trigger priorities fall back to `normal` with a notification; login `deny_message` values
are de-duplicated, sorted, joined with `;` and cut at 1,024 characters.

### Evaluation points

Who calls the engine, when, and what the result does to the run of decision
[0008](0008-run-execution-and-worker-protocol.md):

| Type | Caller | When | Effect |
|---|---|---|---|
| `LOGIN` | `internal/authn` (decision [0006](0006-authentication-and-tokens.md)) | Session creation and API key exchange, under the `LOGIN_POLICIES` scheme of decision [0007](0007-authorization-spaces-and-roles.md) | Allow, admin, roles per space, teams, or denial with messages |
| `GIT_PUSH` | the `vcs.process_delivery` job (decision [0012](0012-vcs-integration-model.md)) | Per affected stack or module, before any run exists | Creates the run in `QUEUED` (T1), cancels runs, sets lock, priority, external dependencies, module version, commit status |
| `APPROVAL` | `internal/run` | On entering `QUEUED`, `UNCONFIRMED` and `PENDING_REVIEW`, and on each `runReview` | `QUEUED` to `READY` waits for approve (T2); reject gives T5, T24 or T29; approve in `PENDING_REVIEW` gives T23 |
| `PLAN` | `internal/run` | When the worker reports a finished plan, and on each targeted replan | Picks the next state: `FAILED` (T18), `UNCONFIRMED` (T20), `APPLYING` (T21), `PENDING_REVIEW` (T22) or `FINISHED` (T17) |
| `INITIALIZATION` (deprecated, server-side) | `internal/run` | Before `QUEUED` to `READY` | `deny` sends the run to `FAILED` with the messages |
| `TASK` (deprecated) | the task create handler | Before the task run is created | `deny` creates no run and answers `403`, problem `forbidden` (decision [0022](0022-api-errors-and-update-semantics.md)), with the messages in `detail` |
| `TRIGGER` | the `policy.trigger` job | A tracked run or task reaches a terminal state; a module version is released | Creates tracked runs on the named stacks |
| `NOTIFICATION` | event routing (decision 0015) | Every event decision 0015 defines | Routes to the inbox, Slack, webhooks, pull request comments |
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
  `request.timestamp_ns` and `spacelift.request.timestamp_ns` come from the server clock at
  that moment, which is the only clock a policy sees.

### The worker-side initialization policy

- The launcher (decision [0008](0008-run-execution-and-worker-protocol.md)) links the same
  `internal/policy` engine. It reads the policy file named by its configuration, compiles it
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
  a ULID, so every policy can reset.
- **Sampling.** When `sample` is true (login by default), the engine stores a record of
  input, body, engine type, result, outcome, error and print output.
  - Hourly quotas per account: 200 for `NOTIFICATION`, 200 for `GIT_PUSH`, 500 for all
    other types together, counted in `policy_sample_quota (account_id, hour, bucket)` with
    an atomic upsert. Over quota, the evaluation is recorded but not sampled.
  - The record is JSON, sealed with the streaming blob sealing of decision
    [0013](0013-secrets-at-rest.md) into the `policy-inputs` store (decision
    [0005](0005-persistence-postgresql.md)) under the key
    `<account>/<policy>/<evaluation ulid>`; the AAD is that key.
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

- Each type's input is a Go struct in `internal/policy/input` whose JSON names follow the
  per-type contract of [09 section 4](../reference/09-policies.md#4-input-documents),
  including absent versus `null` where the report records a difference.
- infraweave publishes the JSON Schema it generates from those structs at
  `GET /.well-known/policy-contract.json`, in the shape of Spacelift's contract.
- Plan input string values are sanitized on the worker before upload, per the stack's
  sanitization mode, so plaintext attribute values never reach the server's policy input
  or its samples.

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
  must be sealed and quota-limited; decision logs upload unsealed batches.
- **Departures from Spacelift**, for the deviations register:
  - `REGO_V1` is the default engine when a create omits it; Spacelift's provider defaults to
    `REGO_V0` and the API's own default is unverified.
  - `net.lookup_ip_addr` is also removed, because DNS lookups from the server reach the
    network that Spacelift's five removals keep policies away from.
  - Evaluation errors have a stated, fail-closed outcome per type; Spacelift documents none.
  - `ACCESS` policies are never evaluated (decision 0021 holds their scope).
  - `sanitized` uses infraweave's own truncation of SHA-256, since Spacelift's is
    unverified ([20 section 13](../reference/20-vendors.md#13-resource-sanitization)).

## Constraints

- **Policy compatibility rests on input compatibility.** A Spacelift policy behaves the same
  only where infraweave's input matches the contract field for field. Fields infraweave
  cannot fill (for example `third_party_metadata.infracost` without an Infracost run) are
  absent, as Spacelift leaves them.
- **OPA upgrades change semantics rarely but not never.** OPA is kept at the latest release;
  the example-library corpus below runs on every upgrade.
- **Unverified facts carried over:** whether a reject in `QUEUED` fails or cancels the run
  (T5 in [08](../reference/08-run-lifecycle.md#transition-table)), whether one approval
  policy's reject fails the run whatever the others decide
  ([09 section 3.3](../reference/09-policies.md#33-approval)), and what a flag set by a
  type with no run does. infraweave fails the run in both reject cases and ignores such
  flags.
- Whether infraweave pins Spacelift's policy contract as a committed snapshot is P14,
  outside this record.

## Verification

- `internal/policy` unit tests: compilation of every type's preamble under both engines;
  each removed built-in rejected at save; `sanitized` and `print`; the cache key; timeouts
  through a policy that loops; every combination rule of
  [09 section 3](../reference/09-policies.md#3-rules-outcomes-and-combination-per-type);
  the error table above; flag reset ordering and multiple owners; sample quotas.
- A corpus test runs the 67 templates and 61 tests of the MIT example library
  ([09 section 10](../reference/09-policies.md#10-the-example-library-at-6ff5edb08bc8)) through
  `internal/policy` and expects every library test to pass.
- A contract test compares the generated `policy-contract.json` with the field lists of
  [09 section 4](../reference/09-policies.md#4-input-documents).
- Gherkin acceptance (decision 0016): `policy_plan.feature` (deny fails, warn holds an
  autodeployed run in `UNCONFIRMED`, warn with approval sends a proposed run to
  `PENDING_REVIEW`), `policy_approval.feature`, `policy_push.feature`,
  `policy_trigger.feature`, `policy_login.feature`, and
  `policy_worker_initialization.feature` with a signed-run JWT, all against PostgreSQL and
  a real Terraform run.
- A sealing test reads a sample object directly from the object store and asserts that a
  known input value does not appear in it.

## References

- [09: Policies](../reference/09-policies.md).
- [27 section 9: OPA as the embedded policy engine](../reference/27-reference-materials.md#9-opa-as-the-embedded-policy-engine).
- [08 section 3: transitions](../reference/08-run-lifecycle.md#3-transitions).
- [26 section 7.3: policy types](../reference/26-scope-and-tiers.md#73-policy-types).
- [14 section 5.2: login policy rules](../reference/14-spaces-rbac-and-identity.md#52-login-policy-rules).
- OPA at `359db769deb0`: <https://github.com/open-policy-agent/opa>.
