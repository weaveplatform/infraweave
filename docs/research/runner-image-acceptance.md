# Public runner image acceptance

Date: 2026-11-20. Implementation branch: `feat/runner-images`.

The five public packages are `ghcr.io/weaveplatform/infraweave-runner-`
followed by `terraform`, `opentofu`, `pulumi`, `ansible`, or `test-support`.
The source repository association and public visibility were read from
GitHub's package API. Publication was driven by the actual GitHub workflow,
not established through local GHCR-looking tags.

## Published indexes

[Publication run 44832177530](https://github.com/weaveplatform/infraweave/actions/runs/44832177530)
passed all five image builds and the final verification job. It built
source `192a2cd587724331f2437db1c2d9c7ba1131e4e2`, recipe
`566b202514322a7480d56d5d097b335194e0e5336aceaac3478a48c6a0f4e97d`.
Its exact `runner-images-lock` artifact is committed as
`deploy/images/images.lock.json`; generated constants are shared with
the product and harness. The temporary feature-branch publishing trigger
was removed after verification, before the implementation PR.

Each image passed AMD64 execution on the native GitHub host and ARM64
execution under QEMU. The final job checked repository association,
public visibility, anonymous pulls of both platforms using empty Docker
auth directories, per-platform SPDX and SLSA data tied to the source/recipe,
then keyless signing and signature verification with the exact bootstrap
workflow identity. Main will verify and sign/promote these same digests
without rebuilding them.

The real tool contracts included:

- Terraform 1.17.2 and 1.6.6, and OpenTofu 1.13.9 and 1.6.3: init,
  saved plan, apply, output and unchanged plan, plus Terragrunt 1.2.2 plans.
- Pulumi 3.277.0: Node and Python programs using the real random 4.21.6
  provider on musl, local backend, preview, up, outputs, exported resource
  state, destroy and stack removal.
- ansible-core 2.22.0: actual local check, apply, repeated unchanged copy,
  ignored/rescued/terminal failure, effective per-task check mode, no_log
  command output, metadata-only records and recap. No cloud resources.
- Shell-free test support: UID/GID 1983 and workspace metadata, and actual
  refusal to execute `/bin/sh`.

## Native application paths

Local ARM64 Docker acceptance built current-source application and Linux
runner/forwarder executables and used real PostgreSQL, object storage and
Gitea. Local evidence remains under `/private/tmp/infraweave-images-*`.

| Check | Observed result |
|---|---|
| `runs/runner_image.feature` | 5 scenarios / 100 steps passed; actual container-image reads, Terraform/OpenTofu defaults, explicit override, missing-image failure and a real Spacelift-image run |
| `workers/protocol.feature` | 3 scenarios / 65 steps passed; actual shell-free failure, worker loss and restart/resume |
| Overlapping independent suites | Those suites ran concurrently. Image-selection suite finished cleanup at 22:49:04 UTC; protocol suite continued and passed at 22:50:01 UTC |
| `runs/runtime_config.feature:58` | 2 scenarios / 48 steps passed; real launchers downloaded/verified/mounted the requested older Terraform/OpenTofu releases |
| `workers/pools.feature:272` | 1 scenario / 19 steps passed; public default-image read, custom override, reset to the shared digest pin |
| `TestAccSharedImageLifecycle` with final lock | Passed in 16.98s; both owners' containers active, A closes, B's next forwarder/tool succeeds, cached fixture survives, retained Terraform digest → current digest → retained digest uses the actual requested container images |

The retained previous Terraform digest in
`test/fixtures/runner-images/previous.json` comes from successful signed
[publication 44831488158](https://github.com/weaveplatform/infraweave/actions/runs/44831488158).
It is explicitly a verified bootstrap preview, retained for the real
same-package warm-cache/rollback regression.

## Release failure and updater evidence

The real GHCR single-platform Terraform manifest was refused for missing
both-platform attestation evidence. The intended readable release tag
remained absent before/after and the consumer lock's SHA-256 was unchanged.
The first publisher exposed a verifier schema assumption: Buildx returns
unwrapped SLSA predicates. The corrected verifier was exercised against
all actual published SPDX/provenance records, including source/recipe
labels, before the successful publication runs.

The real upstream discovery read all release metadata pages from official
GitHub repositories, plus PyPI's ansible-core releases, and matched current
artifact checksums for both architectures. Two complete reconciliations
against real services on an isolated input copy made no change; the real
PR helper also exited without writing a branch or PR. Transaction tests
separately cover new stable backports without default downgrade, duplicate
no-ops, and an upstream failure after an earlier successful lookup leaving
all inputs/pins unchanged. Required CI runs those tests.

Targeted consumer, worker and acceptance unit tests, import-boundary tests,
configured Go lint and pinned-action workflow validation passed locally.
The full PR quality gate, merged coverage, parity and FIPS checks remain
the merge gate; post-merge main promotion and scheduled App PR integration
are verified through their actual workflow runs.
