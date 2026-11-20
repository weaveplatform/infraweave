# Modern full-plan payload evidence

Captured locally on 2026-11-20 using the checksum-verified official binaries in
`manifest.json`. Projects use only builtin `terraform_data` and the official
`hashicorp/local` 2.9.1 provider. Every value that resembles a secret is the
known dummy `dummy-action-secret-675`. No apply or action invocation ran, and no
remote resource was created.

| Directory | Producer and result |
| --- | --- |
| `terraform-actions` | Terraform 1.17.2; sensitive input variable; one actual lifecycle action invocation, unknown argument and nullable working directory |
| `terraform-actions-marked-only` | Terraform 1.17.2; input variable remains unmarked, and only the action's `sensitive()` expressions mark values; identical copies remain unmarked |
| `terraform-deferred` | Public experimental Terraform 1.17.0-alpha20260827 with `-allow-deferral=true`; one actual deferred resource with wildcard address, unknown index, reason and sensitivity/unknown masks |
| `opentofu` | OpenTofu 1.13.9; ordinary builtin plan; no action/deferred family is emitted |
| `terraform-deferred-action-unavailable` | The public alpha plans successfully, but its `show -json` fails with `invalid action trigger event INVALID_EVENT`; no JSON fixture exists here |

Each `plan.json` is unchanged CLI `show -json` output. Its adjacent project,
provider lockfile where applicable, provenance and manifest checksum preserve the
source. The unavailable case retains both failed projects, their real output and binary
plan hashes, rather than an authored replacement JSON document. Its recorded
`-json` plan UI stream contains no full action values/masks or family member. The binary plans and
downloaded tools are not committed.

For a successful project, using the exact recorded producer binary:

```sh
terraform init -backend=false -input=false -no-color
TF_VAR_secret=dummy-action-secret-675 terraform plan -input=false -no-color -out=tfplan
terraform show -json tfplan > plan.json
```

The experimental project additionally passes `-allow-deferral=true` to `plan`.
The OpenTofu project uses `tofu` for the same commands. Provider signatures and
checksums are verified by tool initialization; archive hashes and official URLs
for the host tools are recorded in `manifest.json`. Tool versions are distinct:
latest stable Terraform and the release candidate reject the experimental flag,
and stable `show` rejects a plan written by the alpha. The committed failure logs
record those actual probes. OpenTofu rejects both action block forms and the
`-allow-deferral` flag.

The stable action feature runs the actual producer in the actual worker.
`sanitization_deferred.feature` instead supplies the unchanged experimental JSON
through a workflow file's show command to the stable worker. That exercises
artifact compatibility, sealing, receipts and typed projection; it does not
admit experimental versions into the execution catalogue or prove deferred
execution. The fixtures are tool documents, not captured Spacelift policy inputs.

[Report 35](../../../docs/research/reference/35-modern-plan-payloads.md) records
field/version boundaries, primary sources and #675's remaining required
`deferred_action_invocations` capture and value/mask acceptance gap. Authored
malformed cases remain in unit/feature tests and are not classified as captures.
