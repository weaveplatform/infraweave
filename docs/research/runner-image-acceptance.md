# Public runner image acceptance

Date: 2026-11-20. Implementation branch: `feat/runner-images`.

The five public packages are `ghcr.io/weaveplatform/infraweave-runner-`
followed by `terraform`, `opentofu`, `pulumi`, `ansible`, or `test-support`.
The source repository association and public visibility were read from
GitHub's package API. Publication was driven by the actual GitHub workflow,
not established through local GHCR-looking tags.

## Published indexes

[Publication run 44835002762](https://github.com/weaveplatform/infraweave/actions/runs/44835002762)
passed all five image builds and the final verification job. It built
source `ef54999966467d9c94e1f272cf8df34d48e2c9d3`, recipe
`a849048650d825b8758dbac14ca4c3a9f6886bf0e4988b206befc3b81f37e9ba`.
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

[Cached publication and promotion acceptance 44836916159](https://github.com/weaveplatform/infraweave/actions/runs/44836916159)
skipped every image build and reused these indexes. On actual GHCR preview
tags, promotion copied the exact indexes, duplicate promotion preserved
all digests, incomplete platform evidence was refused, and a distinct,
validly signed index could not replace an existing immutable tag. Failures
left all five preview tags and consumer pins unchanged. The same workflow's
repository-scoped credential read all official upstream release histories
and verified the selected artifact checksum lists. The temporary credential
preflight and branch publisher were then removed. Main keeps the bounded
preview-tag promotion acceptance before writing its readable release tags.

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
| `TestAccSharedImageLifecycle` with final lock | Passed in 12.27s; both owners' containers active, A closes twice, B's next forwarder and real Terraform plan succeed with a read-only tool mount; retained Terraform digest → current digest → retained digest uses actual container images; cleanup readback finds no owned containers |

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
The real PR helper was also exercised against a real bare Git remote, with
GitHub PR metadata isolated separately: identical pending inputs caused no
commit, branch switch, push or CI restart; a pushed commit whose PR creation
failed was reused when restoring the missing PR. A location-independence
regression proves that checkout directory names and output-pin/documentation
changes cannot change recipe identity, while Dockerfile edits do.

Targeted consumer, worker and acceptance unit tests, import-boundary tests,
configured Go lint and pinned-action workflow validation passed locally.
The full PR quality gate, merged coverage, parity and FIPS checks remain
the merge gate; post-merge main promotion and scheduled App PR integration
are verified through their actual workflow runs.
