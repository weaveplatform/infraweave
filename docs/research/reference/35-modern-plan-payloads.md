# 35: Modern full-plan value families and the artifact boundary

Checked against: Terraform v1.17.2 and OpenTofu v1.13.9 release APIs and source, official Terraform JSON/action documentation, local provider v2.9.1 at `95ad8e4b1c13f74be02143846e13cc8f588bc096`, and the public Terraform release index read 2026-11-20. Actual local CLI probes use checksum-verified darwin_arm64 binaries; actual worker acceptance uses the existing signed Linux tool mirror pins.

Research baseline: **2026-11-20**. Experimental compatibility extension: **2026-11-21**, explicitly approved by the maintainer.

## 1. Full documents are wider than policy input

Terraform's full plan model declares `action_invocations`, `deferred_changes`
and `deferred_action_invocations` in addition to ordinary resource changes.
Their contents can carry raw values and sensitivity masks. A successful ordinary
plan says nothing about those additional value paths. [S1][S2][S3]

The published infraweave policy block remains `resource_changes`, `checks` and
the version, with OpenTofu aliases and one block per selected Terragrunt unit.
The full artifact retains the additional sections. Supporting their sanitization
does not add a policy-visible member, policy evaluation gate or action execution.
[Decision 0009](../decisions/0009-policy-engine-opa.md) owns that boundary.

## 2. Executed tool and version matrix

All projects use dummy data and plan only. No apply, action invocation or remote
resource creation occurs. Exact project files, raw JSON, failure logs, archive
checksums and source manifests are in
[`test/fixtures/plan-payloads`](../../../test/fixtures/plan-payloads/README.md).

| Producer or reader | Probe | Observed result |
| --- | --- | --- |
| Terraform 1.17.2, latest stable | Lifecycle action using local provider 2.9.1 | Actual `action_invocations` includes values, sensitivity and unknown masks, identity and trigger metadata |
| Terraform 1.17.2 | `plan -allow-deferral=true` | Refuses the flag because it requires an experimental build |
| Terraform 1.17.0-rc2 | Same experimental flag | Same refusal; a release candidate is not sufficient |
| Public Terraform 1.17.0-alpha20260827 | Unknown resource count, experimental flag | Plans and emits actual `deferred_changes` JSON, including `index_unknown: true` and a wildcard instance address |
| Same public alpha | Unknown action count or an action's deferred resource prerequisite | Both plan commands succeed, but `show -json` fails with `invalid action trigger event INVALID_EVENT`; no full JSON capture is claimed |
| Terraform 1.17.2 reading the alpha's saved plan | `show -json` | Refuses the different producer version; no cross-version workaround is claimed |
| OpenTofu 1.13.9, latest stable | Same action project; experimental flag | Rejects `action`/`action_trigger` blocks and the unknown flag; an ordinary builtin plan emits none of the three families |
| Official v1.17.2 source built with experiments enabled | Both deferred-action projects | Plans, but unchanged reader fails with `INVALID_EVENT` |
| Same exact source with the recorded graph-node metadata patch | Both projects | Plans and reads full JSON with a real `deferred_action_invocations` envelope; serializer unchanged. Patched-development compatibility only, not official-release evidence |

The public index identifies the recorded alpha as the newest published alpha at
this research baseline. It remains distinct from the latest stable release.
The execution catalogue intentionally excludes prereleases, and this work does
not change it. [S4][S5][S6]

The primary stable models declare deferred shapes even though the stable CLI
refuses the experimental option. Source declarations establish their field
names; the executed probes above establish what was actually emitted here.
The alpha's failed deferred-action read is an observed tool limitation, not
proof that the family is empty or equivalent to an ordinary action. The recorded
machine-readable plan UI succeeds but contains none of the full configuration,
sensitivity-mask or deferred-action family members, so it does not supply the
missing full document either. [S9]

## 3. Value paths, masks and preserved metadata

| Full artifact section | Value paths | Sensitivity and unknown paths | Current handling |
| --- | --- | --- | --- |
| `action_invocations[*]` | `config_values` | `config_sensitive`; `config_unknown` stays a boolean mask | Supported through real stable producer, shared transform and actual uploads |
| `deferred_changes[*]` | `resource_change.change.before`, `after`, resource identity/import values and generated configuration | Ordinary `before_sensitive`, `after_sensitive`, `after_unknown` paths inside `change` | Supported artifact compatibility against the actual experimental resource fixture; no experimental execution admission |
| `deferred_action_invocations[*]` | `action_invocation.config_values` when present; captured invocation has identity/trigger metadata only | Known nested action masks follow the existing action contract; the actual producer omits them | Supported artifact compatibility using approved patched-development full JSON; official-release production remains unproven |

DEFAULT hashes value strings; SMART first collects marked strings, then hashes
all identical copies in value payloads, including across selected units and
families. The marked-only stable project makes this observable: its input and
copy are unmarked, and only action expressions supply sensitivity. Numbers,
booleans, nulls, absent members and unknown slots retain their JSON meaning.
The final eight lowercase SHA-256 hex characters remain the shared algorithm.

Action address/type/name/provider names and invocation provenance remain
structural. Deferred reason, resource addresses, wildcard/unknown index,
provider names, action labels and replacement path names remain structural too.
Resource identity **attribute values** are value payloads, unlike provider names.
Generated HCL is an opaque value blob and is hashed in both sanitizing modes.

Known supported family fields and marker shapes are validated before any write.
Unknown future payload fields or malformed metadata fail explicitly, including
under NONE for supported families; errors do not echo values or unknown keys.
Deferred-action wrappers require a string reason and an action object. Known
action members share the action validation and mask rules; future fields fail
all modes. The actual capture has no nested action values/masks, and authored
mask controls remain a separate evidence class. No error falls back to uploading
original bytes. [S1][S2][S3]

## 4. Actual artifact and projection evidence

The first native stable action test reaches FAILED at the existing unsupported
family guard rather than UNCONFIRMED. Real PostgreSQL evidence records the
precise sanitization refusal. After adding action handling, actual producer
runs cover all three modes and targeted replan.

The real-service matrix runs the actual worker with PostgreSQL, Gitea, RustFS,
two server replicas and signed pinned Terraform/OpenTofu/Terragrunt tools. It
passes **40 scenarios / 906 steps** with seed `1795141849546595000`. It includes:

- native action DEFAULT/SMART/NONE and repeated targeted replan;
- native Terragrunt selected units, with an action's sensitive string copied
  unmarked into another unit;
- the unchanged tool-produced experimental resource JSON through a workflow
  file's show command in the real stable worker; NONE retains its exact bytes;
- opened actual recorded objects and typed projection, ciphertext checked
  separately, malformed masks without artifact receipts or value-bearing logs;
- existing ordinary plans, raw-workspace resume/apply, opt-out precedence,
  compatibility registration/session/previously signed-object fences and
  unsupported deferred-action refusals.

The experimental resource upload is explicitly a compatibility test, not a claim
that a stable worker produced or executed a deferred plan. The producer is the
recorded alpha; the test transports its document through the real uploader.
The strict typed/schema projection tests also cover every checked-in modern
plan's resource-change keys, JSON types and absent/null members while excluding
all three full-artifact families from the published policy block.

Reproduce the real boundary suite with:

```sh
GOWORK=off INFRAWEAVE_ACCEPT_REQUIRE_DOCKER=1 INFRAWEAVE_ACCEPT_TAGS=@plan-sanitization \
  go test -v ./test/acceptance -run '^TestAccFeatures$' -count=1 -timeout=15m
```

## 5. Approved experimental compatibility and remaining release limitation

The maintainer approved the documented experimental producer on 2026-11-21.
The fixture directory `terraform-deferred-actions-development` contains the
unchanged prerequisite and unknown-cardinality outputs, exact source commit,
minimal patch, compiler/binary/document hashes, provider lock and reproduction
script. The patch sets the event, failure mode and provider identity omitted by
`reportDeferredActionTriggers`; it does not modify the full-JSON serializer.
The unpatched official source with experiments enabled reproduces the same
`INVALID_EVENT` failure as the alpha. The observed failures remain recorded.

This accepted proof is bounded to full-artifact compatibility. Its invocation
contains metadata only; sensitivity and unknown masks occur in other sections
of the same real document. Stable-action mask compositions test the nested
contract separately and are labelled authored. The real-service compatibility matrix passed under certified FIPS:
**48 scenarios / 1,075 steps**, all passed with no skipped/undefined steps,
seed `1795241531880240000`. This includes seven new single-document/selected-unit
cases, repeated targeted uploads, all three modes, malformed future-field
refusals (including NONE), existing registration/receipt fencing and resumed raw
apply workspaces. The plan partition separately passed 38 scenarios / 841 steps;
the four selected-unit cases passed 76 steps. Sanitization, typed input and
acceptance/harness tests passed with race/shuffle/FIPS; configured lint, docs and
signature checks passed. These local candidate results are separate from the
mandatory final exact-head CI and the phase's native-provider lab. Unmodified official-release deferred-action production and experimental
action execution are explicitly unproven and are not enabled by this change.

## Sources

- [S1] [Terraform v1.17.2 full plan model and value encoding](https://github.com/hashicorp/terraform/blob/v1.17.2/internal/command/jsonplan/plan.go).
- [S2] [Terraform v1.17.2 action values, masks and deferred invocation envelope](https://github.com/hashicorp/terraform/blob/v1.17.2/internal/command/jsonplan/action_invocations.go).
- [S3] [Terraform v1.17.2 resource and deferred resource envelopes](https://github.com/hashicorp/terraform/blob/v1.17.2/internal/command/jsonplan/resource.go).
- [S4] [Official public Terraform release index](https://releases.hashicorp.com/terraform/index.json), [v1.17.2 release](https://github.com/hashicorp/terraform/releases/tag/v1.17.2), [v1.17.0-rc2 release](https://github.com/hashicorp/terraform/releases/tag/v1.17.0-rc2), and [public alpha directory](https://releases.hashicorp.com/terraform/1.17.0-alpha20260827/).
- [S5] [Terraform v1.17.2 experimental flag declaration](https://github.com/hashicorp/terraform/blob/v1.17.2/internal/command/arguments/extended.go).
- [S6] [OpenTofu v1.13.9 release](https://github.com/opentofu/opentofu/releases/tag/v1.13.9) and [full plan model](https://github.com/opentofu/opentofu/blob/v1.13.9/internal/command/jsonplan/plan.go).
- [S7] [Official local provider action documentation at the captured revision](https://github.com/hashicorp/terraform-provider-local/blob/95ad8e4b1c13f74be02143846e13cc8f588bc096/docs/actions/command.md).
- [S8] [Terraform JSON format](https://developer.hashicorp.com/terraform/internals/json-format) and [action block reference](https://developer.hashicorp.com/terraform/language/block/action).

- [S9] [Terraform machine-readable UI](https://developer.hashicorp.com/terraform/internals/machine-readable-ui).
