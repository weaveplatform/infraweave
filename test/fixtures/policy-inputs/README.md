# Policy input fixtures

Each `*.json` file here is one input document that Spacelift's policy engine
evaluated, as the console's policy sample view showed it on a trial account
Spacelift authorized for captures
([report 30, capture safety](../../../docs/research/reference/30-open-questions.md)),
then redacted. Each `*.decision.json` is the decision document the console's
simulation answered for an input. They are the observed counterpart of the
per-type contract in
[report 09, section 4](../../../docs/research/reference/09-policies.md#4-input-documents):
P13's `TestUnitPolicyContract` reads them and fails when a key they hold has
no field in infraweave's input structs, or a field's JSON type differs.
[Report 32, section 16](../../../docs/research/reference/32-spacelift-console-survey.md#16-driven-policies-runs-and-the-registry)
says what each run did.

| File | Type | Run and outcome | What it shows |
|---|---|---|---|
| `push-track.json` | `GIT_PUSH` | branch push, `track` | `in_progress`, `push`, `stack.tracked_commit`, `stack.worker_pool`, `vcs_integration`; no `stack.autoretry`; `terraform_workflow_tool` null on an OpenTofu stack |
| `push-cancel.json` | `GIT_PUSH` | branch push, `track` and `cancel` | an `in_progress` entry for the unconfirmed run the push cancels |
| `push-cancel.decision.json` | `GIT_PUSH` | simulation of `push-cancel.json` | every key of the push decision, `reset_flag` and `priority` included |
| `push-pull-request-opened.json` | `GIT_PUSH` | pull request opened, `propose` | `pull_request` with `action`, `diff`, `head`, `base`; no `vcs_integration` key |
| `approval-queued-undecided.json` | `APPROVAL` | tracked run in `QUEUED`, no review | `reviews.current` and `reviews.older` as objects of `approvals` and `rejections`; `run.flags` empty |
| `approval-queued-approve.json` | `APPROVAL` | tracked run in `QUEUED`, approved | a review entry: `author`, `author_roles`, `identity`, `note`, `request`, `session`, `state`, `state_version` |
| `approval-queued-reject.json` | `APPROVAL` | tracked run in `QUEUED`, rejected | a rejection review; the run then fails |
| `approval-unconfirmed-undecided.json` | `APPROVAL` | the same run in `UNCONFIRMED` | the `QUEUED` review moved to `reviews.older`; the plan policy's flag in `run.flags`; `run.changes` after planning |
| `plan-tracked-warn.json` | `PLAN` | tracked OpenTofu run, `warn` and a flag | the `opentofu` and `terraform` blocks side by side, the other vendors' keys null; `run.flags` empty although this policy raises a flag |
| `plan-tracked-warn.decision.json` | `PLAN` | simulation of `plan-tracked-warn.json` | the plan decision's keys: `deny`, `flag`, `info`, `reset_flag`, `sample`, `warn` |
| `plan-proposed-deny.decision.json` | `PLAN` | simulation with the input edited to a proposed run asking for a deny | a `deny` message beside the `warn` |
| `plan-trigger-policy-run.json` | `PLAN` | run a trigger policy created | `triggered_by` as `policy/<stack>/<run>`, `trigger` null, no flags carried over |
| `plan-dependency-run.json` | `PLAN` | run a stack dependency created | `triggered_by` as `dependencies/<stack>`, `trigger` naming the upstream run and stack, `spacelift.previous_run` with its flags; no output reference in `runtime_config.environment` |
| `plan-pulumi.json` | `PLAN` | tracked Pulumi run, `allow` | the `pulumi.steps` block; changes keyed by URN; hooks in `runtime_config` |
| `plan-ansible.json` | `PLAN` | tracked Ansible run in check mode, `allow` | the `ansible` recap block; changes with no address |
| `trigger-finished.json` | `TRIGGER` | tracked run `FINISHED`, `trigger` | `stacks[]` with `queued_runs` and no `worker_pool`, `stack.space` null, `run.states_history`, `workflow` |
| `trigger-discarded.json` | `TRIGGER` | the run a push policy discarded, `ignore` | the discard note in `states_history` |
| `notification-finished.json` | `NOTIFICATION` | tracked run `FINISHED`, `inbox` | `run_updated` with `policy_receipts`, `plan_policy_decision`, `timing` and `urls`; `webhook_endpoints` |

## C51 approval inputs captured on 2026-11-19

The three `approval-c51-*.json` inputs come from the expanded C51 real-service
capture prepared for #643, separately from the original 2026-11-17 trial inputs.
They are decoded copies of the already redacted sample JSON strings; no field
or value was synthesized. The source is the
[C51 response at `54d4812f`](https://github.com/weaveplatform/infraweave/blob/54d4812f907fa7d8cc8a176069c2ecd027e9141b/test/fixtures/captures/C51/response.json),
SHA-256 `bfdb6f983e02e68dbbfa73f7bf6897084f1dde469c4da13eafa0cb4e6c19b7ac`.
This immutable revision holds the expanded capture; the main-branch C51 record
may still describe the earlier observation until #643 merges.

| Input | Source JSON pointer | Observed state |
|---|---|---|
| `approval-c51-queued.json` | `/exchanges/3/response/data/stack/run/queued/input` | `QUEUED` |
| `approval-c51-unconfirmed.json` | `/exchanges/3/response/data/stack/run/gate/input` | `UNCONFIRMED`, with the queued review in `reviews.older` |
| `approval-c51-reset-after.json` | `/exchanges/6/response/data/stack/run/resetAfter/input` | `UNCONFIRMED` after the new policy resets its own flag |

These samples add human creator identity/federations, nullable teams, populated
review identity/roles/session objects and concrete run/runtime configuration
values to the contract checks. The capture's redaction uses `actor-a`,
`subject-a`, documentation IP addresses and stable opaque identifier replacements.
Its nanosecond timestamps retain the captured values; the timestamp shift
below describes the original 2026-11-17 set.

## Redaction

The documents keep every key, every array and every value's JSON type, so
their shape is exactly what Spacelift sent. What would identify the
account, its people or its repository is replaced with placeholders that
stay the same across every file, so a value that links two documents still
links them:

| Placeholder | Stands for |
|---|---|
| `01H00000000000000000000001` to `…0012` | run, policy, review and other ULIDs, numbered by first appearance |
| `0000000000000000000000000000000000000001` to `…0007` | commit SHAs |
| `00000000-0000-0000-0000-000000000001` and `…0002` | UUIDs |
| `account-a`, `user-a`, `user-b`, `owner-a`, `github-id-a` | the account, the login, a commit author's name, the repository owner, the GitHub node ID |
| `repo-a`, `vcs-url/owner-a/repo-a`, `branch-a` | the repository, its URL and a feature branch |
| `stack-a`, `stack-b`, `stack-pulumi`, `stack-ansible` | the stacks; `stack-b` depends on `stack-a` |
| `policy-push`, `policy-plan`, `policy-approval`, `policy-approval-2`, `policy-trigger`, `policy-notification` | the policies |
| `console-url` | the account's console URL |
| `192.0.2.1` | the reviewer's IP address (a documentation address) |

Nanosecond timestamps under `created_at`, `updated_at` and `timestamp_ns`
are shifted by one constant, so the earliest is 2026-01-01T00:00:00Z and
order and spacing are kept. The console's JSON viewer rounds nanosecond
values to double precision, so their last digits are not significant.
`vcs_integration.created_at` is negative in Spacelift's own documents and is
kept as sent, and so is `04da6b54-80e4-46f7-96ec-b56ff0331ba9` in
`plan-pulumi.json`, the constant Pulumi's engine writes for a value unknown
at preview.

Keys keep Spacelift's names, `spacelift` included: decision 0009 maps the
`spacelift` key to `infraweave` in the input structs, and the contract test
applies the same mapping before comparing.
