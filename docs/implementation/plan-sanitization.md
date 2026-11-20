# Worker plan sanitization

The worker transforms the JSON copy consumed by the server before sealing and
uploading `plan.json`. The binary plan and checkout remain in the pool-sealed
workspace for confirmation, targeted replan and apply. The server cannot open
that workspace. Sanitization and encryption have independent acceptance checks:
the tests open the actual recorded object using the server's artifact key, then
check the ciphertext separately.

The first handover resolves `NONE` from the explicit stack label
`feature:disable_resource_sanitization`, otherwise `SMART` from
`useSmartSanitization`, otherwise `DEFAULT`. That choice is frozen in the run's
existing resolved execution configuration. All three modes seal every artifact.
`NONE` intentionally retains the tool's values and is outside the sanitization
guarantee. Older resolved records with no mode use the safe `DEFAULT` choice.

The shared transform uses the final eight SHA-256 hex characters. DEFAULT hashes
applicable string values; SMART hashes marked sensitive strings and identical
copies throughout the selected plans. Structural resource addresses, keys,
indices, action reasons and replacement path names retain their meaning. Provider
identity/import values follow the same mode. Generated HCL is an opaque value
blob without sensitivity paths, and is hashed in DEFAULT and SMART, as are
configuration expression constants. Invalid input, exhausted bounds and failed
uploads fail planning; the runner never falls back to uploading original bytes.

Action `config_values` and deferred resource change values now follow the same
modes, with their sensitivity masks and identical copies handled before upload.
Unknown flags, action/provider names, addresses, reasons and replacement paths retain their
structural meaning; unknown future payload fields fail closed. The real stable
action producer and the recorded experimental resource compatibility case are
separate evidence classes, documented in
[report 35](../research/reference/35-modern-plan-payloads.md).

Nonempty `deferred_action_invocations` still fail DEFAULT/SMART before an artifact
is recorded because the mandatory real full-JSON/mask proof remains unavailable.
Absent/null/empty arrays remain valid; explicit NONE retains their sealed raw
payload. #675 stays open for that required proof and its remaining acceptance.
The execution catalogue continues to exclude experimental releases.

Terragrunt uploads `{"units":[{"path":".","plan":{}}]}` with each selected unit's
complete tool plan, in path order. The owned wrapper accepts only `units` at the
root and `path`/`plan` per entry, so extra value trees cannot bypass sanitization. `useRunAll: false` includes only the project
root unit, including the saved-plan show fallback used by releases that produce
JSON only with `--all`. The combined envelope is bounded at 64 MiB. SMART collects
sensitive strings across units before transforming any unit. Unit checks are
empty when present under DEFAULT/SMART. Terragrunt's apply continues to replan;
`skipReplan`, `skipReplanWhenRunAll` and resource-name prefixing remain deferred.

The typed policy projection reads the already opened artifact and retains
`resource_changes`, `checks` and the tool version. OpenTofu fills both the
`terraform` block and the `opentofu` block, whose version key is
`opentofu_version`. Terragrunt fills a block keyed by unit path. Every tool-written
resource change member is decoded strictly and retained, including imports,
identities, moves, instance indices and replacement paths. Unsupported future
change fields fail projection instead of disappearing. Other full-document
sections remain in the artifact. This phase supplies the builder and verifies it
on real uploaded bytes. POL-08 (#647) owns isolated decision combination and
POL-14 (#651) owns plan-gate integration. Structural changes/history/comment
inputs acquire no raw resource values. The full action/deferred sections remain
artifact-only under #675, including when NONE explicitly preserves their values.
They add no member to the published policy block.

## Worker v1 compatibility and rollout

`RunSpec.planSanitization` is an optional additive DEFAULT/SMART/NONE enum. A new
runner reading an older server's omitted member sanitizes DEFAULT and omits the
new receipt fields, preserving that server's strict request schemas. Unknown
modes fail closed. Existing semantic-version checks remain in force.

A server with this change also requires `plan-sanitization-v1` in the launcher's
signed registration `capabilities` claim. Missing capability is a 422 response
requiring a launcher upgrade. The Docker executor mounts the launcher's own
binary as runner, so a base runner image cannot silently select an older binary.

The server requires the matching `planSanitization` receipt on both declaration
and recording of `plan.json`. Other uploads have no receipt. This closes the
upgrade window for a worker that already has a session, a run token, or an object
PUT signed before the upgrade: it cannot index its raw plan without the receipt.
Capability and receipt are compatibility declarations by trusted pool workers,
not attestation against a malicious pool owner.

Upgrade launchers before upgrading servers. Already running older workers may
fail planning at the declaration/recording fence; upgrade them and retry those
runs. Existing stored artifacts are not rewritten. Replan creates a new,
sanitized artifact; a resumed apply continues to use its original pool-sealed
binary plan. Do not infer a sanitization guarantee for plans uploaded by older
releases before this change.

## Evidence

`features/vendors/sanitization.feature` runs actual pinned Terraform, OpenTofu
and Terragrunt containers against the real server, PostgreSQL, Gitea and RustFS.
It checks mode precedence, opened artifacts, typed projection, sealed bytes,
selected units, repeated replan sanitization, resumed apply, invalid sensitivity
markers and permanent upload refusal. `sanitization_fence.feature` separately
uses real protocol requests to exercise old-session and previously uploaded
object bypasses. `workers/pools.feature` verifies old registration rejection.
`sanitization_unsupported.feature` injects explicitly authored released-shape
payloads through custom show commands into the actual worker upload path. It
verifies the remaining deferred-action DEFAULT/SMART refusal, no recorded plan, safe logs,
sealed storage, and exact NONE payload preservation. These injected documents
are not claimed as tool-produced fixtures or upstream captures.
`sanitization_actions.feature` uses the actual stable producer for native action
modes, targeted replan, selected Terragrunt units and safe malformed-mask failures.
`sanitization_deferred.feature` uploads the unchanged experimental resource
fixture through a workflow file's show command; it proves artifact compatibility,
including exact NONE bytes, without claiming native experimental execution.

The six checked-in full plans from `internal/iac/plan/testdata` are also projected
and schema-validated without losing any resource-change key or JSON type.
Additional metadata tests are authored contract cases, not upstream captures.

Primary tool definitions: [Terraform JSON format](https://developer.hashicorp.com/terraform/internals/json-format),
[Terraform v1.17.2 jsonplan](https://github.com/hashicorp/terraform/blob/v1.17.2/internal/command/jsonplan/plan.go),
[OpenTofu jsonplan](https://github.com/opentofu/opentofu/blob/main/internal/command/jsonplan/plan.go).

ref: decision 0008
ref: decision 0009
