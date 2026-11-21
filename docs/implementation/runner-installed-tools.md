# Installed Terraform-family runner tools

The maintainer's 2026-11-21 decision requires runnable Terraform-family CLIs in
public runner images. Revision 3 installs official stable Terraform 1.17.2 and
OpenTofu 1.13.9 at `/bin/terraform` and `/bin/tofu`, using the architecture-specific
checksums in `deploy/images/versions.json`. Both official archives' licenses are
retained under `/usr/share/licenses/<kind>/LICENSE`.

The offline contract runs the candidate with Docker networking disabled and no
tool mounts. It verifies the installed version and license, initializes a
provider-free project, plans, reads full JSON, applies, observes a no-change plan,
destroys and verifies an empty state. Separate online compatibility exercises the
older pinned CLI and Terragrunt. The existing launcher accepts
`skipVendorBinaryDownload` for the installed tool; requested per-run downloads
still mount read-only at the same binary path.

## Local candidate execution — 2026-11-21

All four final candidate builds and complete real tool contracts passed. AMD64
executes through Docker Desktop's architecture support on this ARM64 host.
These are local candidates; they are not claimed as published GHCR indexes.

| Kind | Architecture | Local image ID | Offline + compatibility |
|---|---|---|---|
| terraform | amd64 | `sha256:ec90db53053736581b97b7b82081d297a48c6d28cbaf38098e76b0f357e9a120` | passed |
| terraform | arm64 | `sha256:cb1e0f7f1ad0226113e331941b60e0e8e9b0078b88d6701af0565b32716526d0` | passed |
| opentofu | amd64 | `sha256:708cf6dc630263a8b0030ba20ae75b323f0357e8b72b6584b770fdcdd673edcc` | passed |
| opentofu | arm64 | `sha256:a6276ab6523d4e2a777e184777fb252f15a012ea81824964163915e081cc0a30` | passed |

Reproduce from this source with:

```sh
python3 deploy/images/imagectl.py build terraform --platform linux/arm64 --tag infraweave-local-cli:terraform
python3 deploy/images/imagectl.py smoke terraform infraweave-local-cli:terraform --platform linux/arm64
```

Use `opentofu` and `linux/amd64` for the other candidates. The required image
matrix runs the same commands for both platforms. New version tags omit the old
`tested-` prefix, because they now identify the installed tool. Retained shell-only
revision 1/2 releases stay immutable and usable for rollback. Consumer digest pins
still reference the last successfully published release until trusted main
publication verifies and signs the new indexes and its generated pin PR passes
CI. No experimental fixture producer is included in these images.

The [lab architecture](../diagrams/infraweave-lab-test-architecture.drawio.svg)
shows the intended Terraform batch deployments. Its infrastructure migration is
in progress; this image change does not claim that migration has finished.
