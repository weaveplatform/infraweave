# Server INITIALIZATION policy enforcement

Implementation: [#660](https://github.com/weaveplatform/infraweave/issues/660),
under [P13b](https://github.com/weaveplatform/infraweave/issues/636).

The server evaluates effective INITIALIZATION policies while claiming a READY
run, before PREPARING, worker assignment, run-token minting or tool startup.
[C52](../../test/fixtures/captures/C52/README.md) records the upstream boundary:
`QUEUED → READY → FAILED`, with the note
`job assignment failed: denied by 1 initialization policy`. This reconciles
the older planning phrase “before QUEUED→READY”; the documented transition is
now T46, READY→FAILED. Raw deny messages remain in the run's policy decisions.

The evaluator uses the recorded run/commit/session, current stack facts and
request timestamp. Rego V0 and V1 share the typed INITIALIZATION input and OPA
limits. Deny and invalid policy output fail closed; operational storage errors
roll back the transaction for retry. A task's `skipInitialization` controls tool
initialization and does not bypass this server gate. Multiple denials count
policies, with singular/plural notes and each policy's messages preserved.

The worker row serializes one worker's claims. Candidate selection probes without
locking the run, then takes nonblocking space→stack→run fences and revalidates
account, pool, state and version. A savepoint releases partial candidate locks
before another target is tried. The finite drift-limit fence precedes candidate
locks. A denied run commits its policy evidence and FAILED transition before the
same poll retries selection in a fresh transaction; it never marks the worker
busy or mints a token for that run. Repeated scheduling jobs cannot reevaluate
an assigned run.

## Deprecation notice

INITIALIZATION is API-only deprecated functionality from its infraweave adoption
on **2026-11-21 UTC**. Known policy management responses, including compiler
refusal, and single-run reads containing INITIALIZATION receipts carry:

```http
Deprecation: @1795219200
Link: <https://github.com/weaveplatform/infraweave/blob/main/docs/implementation/initialization-policy-verification.md#deprecation-notice>; rel="deprecation"
```

DV-136 records this explicit project date. It is not a claim about an upstream
deprecation date. No removal deadline is announced, so no `Sunset` is sent.
Move server admission checks to the supported PLAN or APPROVAL policy boundary
appropriate to the decision; their inputs and execution timing differ.
ACCESS, TASK_RUN and TERRAFORM_PLAN remain invalid policy types.

This server policy does not load launcher policy files, enable `exec` or sign
worker-side policy JWTs. That separate initialization boundary belongs to P14.
Sample storage and capture tooling remain owned by #661 and #662.

## Acceptance evidence

`policies/initialization.feature` drives real PostgreSQL, Gitea, worker polling,
the published runner image and actual Terraform. Its authored V0/V1 programs
cover tracked/proposed denial, both task initialization settings, malformed
decision output, combined denials and allowed Terraform execution despite
duplicate scheduler jobs. Refusals assert the C52 history, retained messages,
zero worker assignment/preparation and an idle worker. These fixtures replay
the captured boundary; they are not additional upstream captures.

`tiers/deprecation.feature` checks both live deprecated types through CRUD and
compiler refusal, plus validation of all three unsupported types. Existing
approval scheduling tests remain part of the full acceptance gate.

Real PostgreSQL claim tests check contended candidate skipping, two competing
claimants, account-qualified owner reads and savepoint lock release before the
outer transaction commits. Unit tests separately exercise operational port
failures; they are not presented as real OPA acceptance evidence. The complete
coverage, FIPS and acceptance checks must pass at the PR's final head before
this ticket is closed.
