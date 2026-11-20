# Public runner environments

| Kind | GHCR package | Immutable release tag |
|---|---|---|
| Terraform / Terragrunt | `ghcr.io/weaveplatform/infraweave-runner-terraform` | `tested-<terraform-version>-r<N>` |
| OpenTofu | `ghcr.io/weaveplatform/infraweave-runner-opentofu` | `tested-<tofu-version>-r<N>` |
| Pulumi | `ghcr.io/weaveplatform/infraweave-runner-pulumi` | `<pulumi-version>-r<N>` |
| Ansible | `ghcr.io/weaveplatform/infraweave-runner-ansible` | `<ansible-core-version>-r<N>` |
| Acceptance test support | `ghcr.io/weaveplatform/infraweave-runner-test-support` | `r<N>` |

Each package is public, linked to this repository and provides Linux AMD64
and ARM64. Standard GHCR public pulls need no login. Project consumers use
the index digests in `images.lock.json`, also generated into
`internal/platform/runnerimages/images.gen.go`. A readable tag identifies
the cohort and image revision; the digest identifies its exact content.

Terraform and OpenTofu images contain the shell environment. The current
launcher downloads, verifies and mounts the run's requested executable
read-only. Terragrunt uses the Terraform image with its requested tools.
Pulumi includes its CLI/language hosts, Node/npm and Python/pip. Go/.NET
programs can use derived images. Ansible includes hash-pinned ansible-core
and dependencies, and the `infraweave` aggregate callback. The runner
enables it with `ANSIBLE_CALLBACKS_ENABLED=infraweave` and sets
`INFRAWEAVE_ANSIBLE_RECORD`. Its JSONL contains task metadata and recap
counts, without module results or diffs. Test support is a separate
shell-free filesystem; tests mount current-source application/forwarder
executables. These environments are independently usable while complete
Pulumi/Ansible stack backends retain their separate implementation owners.

## Build, validate, release

`versions.json` holds the pinned base, tested/baked tool versions, artifact
checksums, historical compatibility cohorts and image revision. Advance
the revision for a recipe, base or package rebuild. `imagectl.py generate`
updates Dockerfile pins and shared Go constants; `--check` detects drift.

```sh
python3 deploy/images/imagectl.py generate
python3 deploy/images/imagectl.py build terraform --platform linux/arm64 --tag local-image-check:terraform
python3 deploy/images/imagectl.py smoke terraform local-image-check:terraform --platform linux/arm64
```

The required quality gate builds each candidate once per platform in its
own daemon job, then runs the actual tool contract in that candidate.
ARM64 on an AMD64 host uses QEMU. Normal acceptance suites pull missing
committed digest pins, reuse cached images and remove owned containers.
Closing one suite retains the shared image cache for the next suite.

`runner-images.yml` publishes immutable previews from trusted main. It
requires real tool execution on both platforms, public visibility and
repository association, anonymous pulls using an empty Docker auth
configuration, SPDX SBOMs, SLSA provenance tied to the recipe/source, and
Sigstore signatures. A bounded real-GHCR promotion canary uses preview tags
to prove exact copies, duplicate idempotence and immutable-tag/evidence
refusal before main writes readable release tags. Its preview tags are
retained as verification evidence alongside the shared indexes. Main promotes
those exact indexes to readable tags
without rebuilding them. A successful release opens `deps/runner-image-pins`
using the org App, so required CI runs before consumers change. A failed
gate leaves consumer pins unchanged. Full release tags cannot be replaced.

For a transient verification, promotion or pin-PR failure, rerun the failed
jobs of the original publication run. Successful build jobs' metadata is
retained, so the retry uses the same tested digests and cannot replace a
partially promoted immutable tag with a rebuilt image. A changed recipe
needs a new revision; wait for the preceding publication/pin PR to finish.

Verify a released index with the main workflow identity:

```sh
cosign verify \
  --certificate-identity https://github.com/weaveplatform/infraweave/.github/workflows/runner-images.yml@refs/heads/main \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/weaveplatform/infraweave-runner-terraform@sha256:<index-digest>
```

Bootstrap previews record their feature-branch identity, then main signs
the same digest again before promotion. Package administrators must check
public visibility on first publication; the publisher refuses a private
or unlinked package. GitHub's current public-package terms are documented
in [GHCR](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
and [billing](https://docs.github.com/en/billing/concepts/product-billing/github-packages).

## Upstream updates and rollback

`runner-images-upstream.yml` discovers new stable Terraform, OpenTofu,
Terragrunt, Pulumi and ansible-core releases daily. `upstream.json` records
the bootstrap boundary and seen versions. Stable backports after that
boundary are included as additional compatibility/release cohorts; old
release history is not backfilled. The newest cohort remains the default.
Weekly/manual rebuilds refresh the base and packages and advance the image
revision. Input PRs use `deps/runner-image-inputs`; duplicate discoveries
produce no change. Failed lookups finish before build inputs are written.
An unpublished input change blocks another automatic update until its
publication and pin PR finish.

Published output records and digest pins are distinct from build inputs.
The pin PR cannot trigger a recipe rebuild loop. The App's input and pin
PRs use the same required quality gate and trusted-author auto-merge rules
as dependency updates. No mutable alias is used by the project.
An identical update already present in an open App PR makes no new commit,
push or CI restart.

To roll back, restore a retained `images.lock.json` and run
`python3 deploy/images/imagectl.py generate` in a reviewed PR. Warm hosts
select the restored exact digest. Existing explicit runtime, stack,
account and launcher image overrides keep their precedence and run-spec
resume semantics. Released digests/evidence are retained; no registry
garbage collection is enabled.

The image contracts cover current/older Terraform and OpenTofu plan/apply,
Terragrunt plans, real Node/Python Pulumi provider programs with local
state, and local Ansible check/apply/failure/no_log/rescue/recap. The
application acceptance suite additionally proves the launcher's signed
tool downloads and mounts, default/override selection, shared-owner
lifecycle, warm-cache selection and rollback.
