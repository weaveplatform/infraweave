# Certified FIPS execution budgets

The required `fips-only` check aggregates complete, separately bounded suites.
`fips-unit` runs the existing race/shuffle unit command and emits a matrix from
`test/acceptance/internal/harness/partitions.go`. That matrix contains exactly
one native suite and every regular scenario partition. No new workload list or
tag filter is maintained in the workflow. Scheduled lab partitions retain their
existing explicit infrastructure jobs.

| Suite | Command | Go package deadline | Actions job budget |
| --- | --- | --- | --- |
| Unit | `make test FIPS=1` | existing Go default, 10 minutes | 20 minutes |
| Native/component/capture | `make accept-native FIPS=1` | 60 minutes | 75 minutes |
| Every scenario partition | `make accept FIPS=1 PARTITION=<name>` | 60 minutes each | 75 minutes each |
| Required aggregate | validate complete execution receipts and dependency results | no Go test | 5 minutes |

These match the regular acceptance partition budgets. The sixty-minute
deadline sits well above the longest partition, whose FIPS jobs took up to
fourteen minutes against the earlier ten-minute deadline, so a slow shared
runner never fails a passing suite. Native runs
every `TestAcc` family across `./...` except `TestAccFeatures`; each scenario job
runs `TestAccFeatures` alone with its catalogued selector. Existing capture replay
and intentional evidence-gap reporting remain intact. There is no C51-specific
timeout or runtime change. Explicit partitions always choose the feature-only
recipe, so a newly registered selector cannot silently fall back to the combined
native and feature command. Unknown selectors fail through the existing harness.

## Diagnosis

The failed [run 45049578019, job 137291510223](https://github.com/weaveplatform/infraweave/actions/runs/45049578019/job/137291510223)
tested `af9c8ab4299dd0e19bf3ba3e213bf68afe9dd23c`. The passing
[run 45051056087, job 137296035438](https://github.com/weaveplatform/infraweave/actions/runs/45051056087/job/137296035438)
tested `f50b9da4ef5f4bcd34824b3b5f6cab348dbcd64d`. Their relevant source changes
were documentation/register text, not a runtime or acceptance fix.

| Observed duration | Failed run | Passing run |
| --- | --- | --- |
| Certified unit step, including Go compilation | 372 seconds | 250 seconds |
| Combined acceptance step, including compilation/package setup | 1982 seconds | 1259 seconds |
| `test/acceptance` package execution | 1800.325 seconds, alarm | 1149.031 seconds, passed |

The failed package had already passed **1143 feature scenarios / 26991 steps**.
Its alarm named `TestAccCaptures` at 50 seconds and C51's subtest at **2 seconds**.
The package's thirty-minute budget had expired. This is not evidence that C51
hung. Go compilation before a test binary starts is outside that package alarm;
the gap between step and package durations also includes other packages, so it
must not be labelled pure compiler time. Current application builds performed
inside the harness do consume package time.

Thirty-second progress samples corroborate the phase boundary but are not exact
test timings: failed feature samples span 17:38:14–18:06:49 UTC and a capture sample
appears at 18:07:44; passing feature samples span 17:51:13–18:09:04 and a capture
sample appears at 18:09:50. Splitting by complete responsibilities prevents one
phase from consuming another's alarm without raising a blanket deadline.

## Complete aggregation and partial reruns

Each child retains a small `result.json` with its status, elapsed command time,
source head/base, actual checkout/tree, run ID and attempt. Successful feature
receipts require nonempty scenarios and every reported step passed. The native
receipt also requires the actual application's recorded certified FIPS mode.
Receipts retain only counts and execution/provenance facts, without scenario
text, credentials, Terraform state or handoffs. Existing sanitized failed-scenario
artifacts remain separate. Native and feature jobs retain bounded progress while
Go buffers output; each command still uses `count=1` and requires real Docker.

`fips-only` runs with `always()` and depends on both unit and matrix jobs. It
requires both dependency results to be `success` and exactly one selected receipt
for unit, native and every emitted catalogue partition. Missing, unexpected,
duplicate, failed, cancelled or skipped work rejects the aggregate. The ordinary
quality gate still requires `fips-only`; ordinary coverage inputs, all OS/race/
native checks and the 95% total / 90% package thresholds are unchanged.

Artifact names include shard and run attempt. A GitHub “rerun failed jobs” may
reuse successful unit or sibling jobs from an earlier attempt of the **same exact
head/base, checkout/tree and run**. The aggregate validates all receipts, rejects
duplicates for a shard/attempt, and selects the greatest attempt per shard. A
newer failure therefore overrides an older success. Different source trees or
runs cannot be combined. Aggregate `summary.json` retains selected attempts and
per-command timings for review. GitHub documents that reruns keep the original
SHA/ref and successful prerequisite outputs in its
[rerun contract](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/re-run-workflows-and-jobs).
The pinned [artifact downloader](https://github.com/actions/download-artifact/blob/9000827ccba6bdab643e8b6fd33ac0654aef8333/README.md)
collects matching artifacts from this workflow run into separate directories;
attempt-specific names preserve earlier successful receipts.

## Verification status

Local verification exercises the catalogue producer, actual feature selectors,
workflow wiring, fail-closed aggregation and partial-rerun controls. Final exact
PR-head Linux CI must execute all nine certified suites and retain the aggregate
timings/provenance before this fix is accepted. The earlier passing combined run
is diagnostic evidence, not proof of the new workflow. The independent local
Kubernetes lab change belongs to its own PR and is absent from this branch.

Local dirty-candidate verification on baseline
`4e2178396598d8c8394e2a4cb72dfa8558e3f003` passed:

- Complete `make accept-native FIPS=1` against real Docker, exit 0; the native/
  capture package took **69.829 seconds** and verified actual application mode
  `FIPS 140-3 mode only, Go Cryptographic Module v1.0.0`.
- Complete identity partition: **121 scenarios / 1948 steps**, all passed;
  its package took **120.566 seconds**.
- Certified FIPS/race workflow, catalogue and feature-partition unit checks;
  nine Python progress/receipt tests including actual CLI failure exit;
  configured Go lint with zero issues; documentation checks and diff checks.

The first local native invocation failed only while compiling the new matrix
tool: review changed its imports after that running Go command had loaded its
dependency snapshot. Its runtime/capture package passed. The complete frozen-source
invocation above then passed, including the matrix tool. This is separate from
the original CI alarm and is not offered as a rerun fix for that alarm. Retained
local logs are `/private/tmp/infraweave-762-native-final.log` and
`/private/tmp/infraweave-762-identity.log`. These local results do not certify the
remaining scenario shards or replace final exact-head CI.
