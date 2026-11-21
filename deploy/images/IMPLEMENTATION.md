# Runner image delivery

The accepted implementation uses one branch and PR for five public,
repository-linked GHCR packages named `infraweave-runner-<kind>`:
`terraform`, `opentofu`, `pulumi`, `ansible`, and `test-support`.

Terraform/OpenTofu include the pinned official stable CLI in `/bin`, checked
against the source-pinned architecture checksum at build time. An offline
plan/apply/destroy contract proves the installed CLI without a network or tool
mount. `skipVendorBinaryDownload` selects it; the existing verified read-only
mount path still supplies other requested versions and Terragrunt's tools.
Pulumi includes its pinned CLI, Node/npm and Python/pip. Ansible includes
hash-pinned ansible-core and the aggregate callback. Test support remains
a shell-free filesystem for the current test executable and forwarder.

`versions.json` is the build input; `images.lock.json` records successful
publication outputs. Generated Go constants provide the same immutable
digest references to launchers, account defaults and acceptance harnesses.
Normal acceptance runs pull missing released images, reuse the daemon's
cache, build the current application executable, and remove only owned
containers. Recipe validation builds candidates in its own image job.

Readable immutable tags are `<version>-r<N>` for Terraform/OpenTofu/Pulumi/Ansible
and `r<N>` for test support. Retained `tested-<version>-r1/r2` Terraform/OpenTofu
tags identify the earlier shell-only cohorts; their digest pins remain valid for
rollback. New publication inputs start at revision 3 and never replace those tags. Each is a
Linux AMD64/ARM64 index. Runner releases have an independent cadence from
application releases. Released digests and verification evidence remain
available for rollback. The project does not consume mutable aliases.

Publication must verify the recipe identity, both platforms' real tool
contracts, repository association, anonymous pulls, signatures, SPDX SBOMs
and provenance before promotion. Bootstrap is restricted to the approved
maintainer branch; that trigger is removed before merge. Main promotes
the verified bootstrap digests without rebuilding.

Daily stable upstream discovery covers Terraform, OpenTofu, Terragrunt,
Pulumi and ansible-core, beginning with current stable versions. Weekly
and manual rebuilds advance the image revision. Updates arrive through
CI-checked PRs; the resolved output lock cannot trigger a rebuild loop.

Completion evidence includes offline baked Terraform/OpenTofu execution,
older tool and Terragrunt compatibility, Pulumi Node/Python provider programs with local state, Ansible
check/apply/failure/no_log/recap, anonymous exact pulls of both platforms,
two overlapping suites with early owner cleanup, warm-cache pin movement
and rollback, and updater idempotence/failure checks. Existing quality,
coverage, parity and FIPS gates remain required.
