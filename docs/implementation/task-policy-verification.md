# TASK policy enforcement

Implementation ticket: [#659](https://github.com/weaveplatform/infraweave/issues/659),
under [P13b](https://github.com/weaveplatform/infraweave/issues/636).
This document records the admission boundary; the final delivery gate remains
open until the complete acceptance and CI evidence is recorded.

The public `taskCreate` path evaluates effective TASK policies under the stack's
creation lock, before inserting a run or scheduling work. The input contains the
actual command, request time/address, recorded session and current stack facts.
No run identity is manufactured for an evaluation that precedes the run.

A deny or evaluation failure returns `403 forbidden` with the policy messages.
Its evaluation events commit, while no task run, transition, scheduling job,
worker assignment or run-owned flag is written. Policy-event fanout jobs are
expected. An operational storage failure rolls the transaction back. Allowed
tasks continue through the existing creation and approval lifecycle.

## Deprecation notice

TASK policies are introduced as deprecated functionality in infraweave on
**2026-11-21 UTC**. Known TASK policy management responses and task-creation
responses that evaluated a TASK policy carry:

```http
Deprecation: @1795219200
Link: <https://github.com/weaveplatform/infraweave/blob/main/docs/implementation/task-policy-verification.md#deprecation-notice>; rel="deprecation"
```

This is an infraweave adoption date, not an asserted Spacelift deprecation date
(DV-136). The [upstream documentation](https://docs.spacelift.io/concepts/policy/deprecated/task-run-policy)
deprecates TASK policies in favor of approval policies without publishing a
start date. [RFC 9745](https://www.rfc-editor.org/rfc/rfc9745.html#section-2.1)
requires a structured date value. No removal deadline is invented, so these
responses carry no `Sunset` header. Ordinary tasks without TASK policies remain
supported and do not receive this policy notice.

Replace a TASK policy's deny predicate with an APPROVAL reject predicate and
approve when no rejection matches. The task command is available as
`input.run.command` and its creator session as `input.run.creator_session` in
the approval input. TASK_RUN remains an unsupported alias.

## Acceptance coverage

`@policy-task` in `test/acceptance/features/runs/tasks.feature` executes stored
V0 and V1 programs through two real servers, PostgreSQL and Gitea. It checks
explicit denial, invalid decision output, the allowed lifecycle, committed
evaluation events, zero denied-task creation effects and the dated notice.
`skipInitialization` does not bypass policy evaluation. Cross-replica idempotency
replays retain the dated notice and commit exactly one evaluation/task. Existing
ordinary tasks remain free of the policy deprecation notice. These are authored
acceptance programs, not claimed upstream captures. The accepted TASK denial
inference from #644 remains identified in ADR 0009.

The final evidence must also include real Terraform task execution, permission
and account isolation, bounded evaluation failures, rollback/idempotency and
the complete unchanged coverage/FIPS gates before this ticket is closed.

The local implementation proof passes nine scenarios / 260 steps: all six
`runs/tasks.feature` scenarios, both `tiers/deprecation.feature` scenarios, and
the TASK/APPROVAL worker scenario in `policies/approval_workers.feature`. The
worker uses the published runner image and executes `terraform version` after
an actual approval review. Domain and shared-idempotency tests also pass with
race detection and FIPS enforcement, including PostgreSQL storage, replay and
sealed-response checks. These focused results do not replace the complete CI
coverage, generation and operation-parity gates.
