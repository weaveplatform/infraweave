# P13 acceptance CI diagnosis

Dated 2026-11-21, for [PR #717](https://github.com/weaveplatform/infraweave/pull/717).
These are separate observed failures with targeted regressions. Corrected
exact-head CI and the protected main-only provider lab remain delivery gates.

## Provisioning Terraform inherited application FIPS settings

[Lab job 136769566166](https://github.com/weaveplatform/infraweave/actions/runs/44878879668/job/136769566166)
failed during provider installation, before infrastructure creation. Official
Terraform 1.17.2 panicked in OpenPGP signing-key parsing because the job's
`GODEBUG=fips140=only` prohibited the SHA-1 hashing used for key identifiers.
Its retained cleanup report verified empty state and no owned resources.

A paired real installation in the official Terraform ARM64 runner reproduced
the panic with the inherited switch, exit 11, then authenticated and installed
the locked Docker provider successfully with Terraform's default environment,
exit 0. Signature and package-checksum verification remained enabled.
The workflow now scopes certified FIPS settings to the infraweave helper build
and the controller's `--fips` acceptance/application processes. The
[lab proof](infraweave-lab-verification.json) records both probe outcomes.

## Public run responses mixed committed versions

[Plan job 136769566535](https://github.com/weaveplatform/infraweave/actions/runs/44878879668/job/136769566535)
failed the native OpenTofu owner-reset scenario. The response had `PLANNING`
version 13 alongside history containing `UNCONFIRMED` version 14 and the later
policy receipts and flags. The retained PostgreSQL dump showed the actual run
had committed version 14 at `07:13:49.080360`; the response retained the earlier
base row from `07:13:48.632120`. The flags poll completed at `07:13:49.139160`,
followed by an immediate state assertion. The run was not stuck, and extending
a polling deadline would not repair the inconsistent response.

The reader fetched its base row, history and policy evidence through separate
READ COMMITTED statements. A worker commit between those queries could combine
different versions in one response. The repair uses one existing serializable
transaction snapshot for standalone run and stack-run reads, joining an outer
transaction when present. Action responses preserve their existing transaction.

The regression uses real PostgreSQL and a controlled writer commit after either
the base row or history read, across run, stack-run and stack-run-list reads.
All six interleavings failed before the repair. After the repair they passed
five repetitions with certified FIPS, race detection and shuffled ordering;
existing read, search, failure and cancellation suites passed alongside them.
Each case also requires a fresh request to observe the newer committed version.
The original native Terraform/OpenTofu feature then passed under certified FIPS:
two scenarios / 170 steps, with actual workers and all steps passed in
58.231 seconds. Final exact-head CI remains required.
The [proof record](run-read-snapshot-verification.json) retains commands,
controlled interleaving counts, original timestamps and artifact checksums.

## FIPS job exhausted its unchanged execution budget

[Run 44874854659](https://github.com/weaveplatform/infraweave/actions/runs/44874854659)
reported that the FIPS job exceeded its 45-minute execution limit. Unit tests
passed in 10 minutes 37 seconds; full acceptance then ran for 34 minutes
25 seconds before cancellation. The retained log contained no assertion failure
or cryptographic panic. Cancellation skipped the failure-only evidence upload,
and buffered Go output did not reveal the unfinished scenario.

The job restored the same immutable 65 MB setup-go cache as ordinary Linux
workloads; that source-independent snapshot had not refreshed. The correction
adds a workload-specific incremental build cache, saves completed unit builds
before acceptance, and reports bounded fields from the existing progress trace
every 30 seconds. Three reporter tests verify partial records, bounded values
and exclusion of private fields. Module caching, test commands, scenario sets,
coverage thresholds and execution deadlines remain unchanged. Runtime improvement
must be measured by corrected CI; cache reuse is not passing test evidence.
