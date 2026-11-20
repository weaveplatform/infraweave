# Native deferred-action producer diagnosis

These are **unavailable-producer probes**, not supported payload fixtures.
Every command used an unchanged, checksum-verified official Terraform binary
and the existing signed `hashicorp/local` 2.9.1 package. No apply or action
invocation executed, no cloud resources were created, and no sanitizer guard
changed. The only secret-like input is the known dummy
`dummy-action-secret-675`.

| Directory | Official binary | Native result |
| --- | --- | --- |
| `minimal` | 1.17.0-alpha20260827 | Minimal unknown action count: plan succeeds; saved-plan JSON read fails with `INVALID_EVENT` |
| `older-alpha` | 1.17.0-alpha20260812 | The original deferred-prerequisite project also plans successfully and fails saved-plan JSON read with the same error |
| `direct-count` | 1.17.0-alpha20260827 | Changing to direct invocation cannot find the unknown-count action instance; plan fails |
| `direct-prerequisite` | 1.17.0-alpha20260827 | Direct invocation rejects the action's unknown prerequisite as a partially applied configuration; plan fails |
| `verbose-plan` | 1.17.0-alpha20260827 | A plan-only native test succeeds and emits one renderer plan containing two deferred resources, but no `deferred_action_invocations` member |

Each directory retains its exact project/lockfile, raw command output and
`provenance.json`, including executed arguments, exit status, binary hash and
SHA-256 of each retained file. Saved binary plans remain uncommitted; their
hashes are recorded where planning succeeded. Empty output is retained when it
is part of an executed command's result. No full `plan.json` is manufactured.
The verbose test stream has its native renderer shape; it is not the full
`show -json` document and must not be wrapped or relabeled as one.

The captured executions reused the provider package already verified during
the earlier capture. To reproduce a directory independently, install its exact
binary from the parent manifest, run `terraform init -backend=false -input=false
-no-color`, and run the recorded commands/environment. `verbose-plan` includes
its test file with explicit `command = plan`.

The minimized failure is:

```sh
terraform plan -allow-deferral=true -input=false -no-color -out=tfplan
terraform show -json -no-color tfplan
```

The first command succeeds; the second exits 1 without a JSON document. This
isolates saved-plan round-trip behavior from sensitive values, masking and
infraweave's uploader.

The Aug27 binary corresponds to upstream commit
[`0fe280f9`](https://github.com/hashicorp/terraform/tree/0fe280f90274bcbf16b9d0e6631a5a80eb3e2fd5),
and Aug12 to
[`5e83043c`](https://github.com/hashicorp/terraform/tree/5e83043c788062d9bc735e6b86809ac6c0b2a409).
The alpha's [deferred-trigger constructor](https://github.com/hashicorp/terraform/blob/0fe280f90274bcbf16b9d0e6631a5a80eb3e2fd5/internal/terraform/node_resource_abstract_instance.go#L3158)
leaves the lifecycle event unset. Its [saved-plan writer and reader](https://github.com/hashicorp/terraform/blob/0fe280f90274bcbf16b9d0e6631a5a80eb3e2fd5/internal/plans/planfile/tfplan.go#L1347)
encode and reject the invalid event. Direct invocation [rejects unknown values
and provider deferrals](https://github.com/hashicorp/terraform/blob/0fe280f90274bcbf16b9d0e6631a5a80eb3e2fd5/internal/terraform/node_action_invoke.go#L255),
and the [test renderer](https://github.com/hashicorp/terraform/blob/0fe280f90274bcbf16b9d0e6631a5a80eb3e2fd5/internal/command/views/test.go#L594)
does not include the full deferred-action family. These source observations
explain the native results; they do not replace the missing payload capture.

The authentic full deferred-action JSON and positive DEFAULT/SMART/decrypted
upload assertions in [#675](https://github.com/weaveplatform/infraweave/issues/675)
remain unresolved. DEFAULT/SMART continue to refuse the family, and explicit
NONE remains sealed. The older alpha is a diagnostic comparison, with no
experimental version admission into the execution catalogue.
