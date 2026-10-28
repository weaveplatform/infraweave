# 0014: Module and provider registry protocol

Status: Proposed

## Context

Spacelift runs a private Terraform registry with two halves. Modules are
stack-like entities whose tested commits become versions. Providers are
uploaded by CI and signed with the account's GPG keys. Terraform and
OpenTofu consume both through HashiCorp's remote service discovery and
registry protocols. Dependabot and Renovate consume modules the same way,
and so do runs inside Spacelift, using credentials Spacelift writes for
them ([15 section 9](../reference/15-module-and-provider-registry.md#9-registry-protocol)).

The docs describe how to consume the registry but not its endpoints. What
infraweave must answer comes from live captures, and those show where
Spacelift departs from HashiCorp's protocol. An unknown module answers 200
with an empty list, not 404. A version segment is parsed leniently. There
is no "latest" download
([15 section 9.3](../reference/15-module-and-provider-registry.md#93-module-endpoints)).
Report 15 records the version state machines, the numbering rules, sharing
and the GPG key rules
([sections 6](../reference/15-module-and-provider-registry.md#6-versions),
[7](../reference/15-module-and-provider-registry.md#7-sharing-and-availability)
and [10](../reference/15-module-and-provider-registry.md#10-the-provider-registry)).
Report 28 proposes the tables
([28 section 12.7](../reference/28-data-model.md#127-modules-and-providers)).
Several behaviours are still open captures: C130 to C138
([30](../reference/30-open-questions.md#module-and-provider-registry-report-15)).

This record fixes what infraweave serves on the protocol side, how
artefacts are stored, the version state machines, GPG handling and sharing.
How a `TESTING` run executes belongs to decision 0008. How the state
backend's `state.v2` and `tfe.v2*` services behave belongs to decision 0010.

## Decision

### Routes and packages

The protocol is served by the `serve` role
([0020](0020-reference-deployments-and-packaging.md)) and routed with chi
in `internal/registry`. Decision 0004 owns package placement. The routes
are described in their own OpenAPI document,
`api/src/registry/` bundled to `api/registry.openapi.yaml`, so that the
contract tests of [0016](0016-testing-and-acceptance.md) cover them like the
deploy API. The management operations (the Modules, Terraform Providers and
GPG Keys tags) live in the deploy API as usual.

| Method | Path | Answer |
|---|---|---|
| `GET` | `/.well-known/terraform.json` | Service discovery |
| `GET` | `/terraform-login`, `POST /terraform/token` | `login.v1` authorization code flow (decision 0006 issues the token) |
| `GET` | `/registry/modules/v1/{namespace}/{name}/{provider}/versions` | Module versions |
| `GET` | `/registry/modules/v1/{namespace}/{name}/{provider}/{version}/download` | 204 with `X-Terraform-Get` |
| `GET` | `/registry/providers/v1/{namespace}/{type}/versions` | Provider versions |
| `GET` | `/registry/providers/v1/{namespace}/{type}/{version}/download/{os}/{arch}` | Provider package metadata |
| `GET` | `/registry/files/{token}` | A signed file download, in `proxy` mode only |

Any other path under `/registry/modules/v1/` or `/registry/providers/v1/`,
including the module root, the namespace, `search` and the latest-version
download, answers 404 with `{"errors":[{"message":"not found"}]}`.

### Host and discovery

- **Host.** The registry host is the installation's own host name, taken
  from `server.public_url`. A module address is
  `<host>/<account>/<name>/<provider>` and a provider address is
  `<host>/<account>/<type>`. The namespace is the account name, as on
  Spacelift ([15 section 2.1](../reference/15-module-and-provider-registry.md#21-naming-and-addressing)).
- **Discovery document.** Served before authentication, with
  `Content-Type: application/json` and `Cache-Control: no-cache, no-store`.
  Every value is absolute and built from `server.public_url`:

  | Key | Value |
  |---|---|
  | `login.v1` | `{"authz": "<base>/terraform-login", "client": "terraform-cli", "grant_types": ["authz_code"], "token": "<base>/terraform/token"}` |
  | `modules.v1` | `<base>/registry/modules/v1/` |
  | `providers.v1` | `<base>/registry/providers/v1/` |
  | `state.v2`, `tfe.v2`, `tfe.v2.1`, `tfe.v2.2` | Added by decision 0010 |

- **One host, not two.** Spacelift splits a static discovery host from the
  API host ([15 section 9.1](../reference/15-module-and-provider-registry.md#91-host-names)).
  infraweave serves discovery and the protocol from one host.

### Authentication and authorization

| Credential | Where it comes from | How the registry reads it |
|---|---|---|
| Session token | `terraform login <host>`; a registry session independent of the web session | `Authorization: Bearer`, verified as decision 0006 defines |
| API key | `credentials "<host>" { token = "<base64(api:<id>:<secret>)>" }`, unpadded, as Spacelift documents | Decoded, exchanged for a token as decision 0006 defines, and the result cached in memory until 30 seconds before it expires |
| In-run token | Written into the run's `~/.terraformrc` by the runner (decision 0008) | `Authorization: Bearer`, as above |
| None | Public modules and public providers | Anonymous read |

- A module or provider is readable when the caller holds Read on its space,
  on a space that inherits from it, or on a space it is shared to
  (decision 0007).
- **Modules.** An unauthenticated or unauthorized `versions` request
  answers 200 with an empty `versions` array, the symptom Spacelift
  documents. A download answers 404.
- **Providers.** `versions` without credentials answers 401 with the
  `text/plain` body `unauthorized`, even for an unknown provider. With
  credentials but no access it answers 404, as for an unknown provider.
- **Public providers** answer without credentials. This is infraweave's
  choice while capture C135 is open, and it follows from `public` meaning
  "usable by anyone".

### Module protocol

- **Versions.** 200 with
  `{"modules":[{"source":"<ns>/<name>/<provider>","versions":[...]}]}`.
  Each version carries `version` plus the `root` and `submodules` provider
  dependencies from the version's parsed metadata, as Spacelift's capture
  shows. An unknown module answers 200 with an empty `versions` array.
- **Listed versions.** Only versions whose state is `ACTIVE` and which are
  not yanked. `PENDING` and `FAILED` versions are left out. So are yanked
  ones, which matches the gaps in Spacelift's captured list (C132, C133).
- **Download.** 204 with `X-Terraform-Get: <signed URL>` and no body.
- **Version segment.** Parsed leniently. `9.1` and `v9.1.0` resolve to
  `9.1.0`. A segment that names no listed version answers 404 with the
  `text/plain` body `404 page not found`. A segment that is not a version
  answers 400 with `invalid module version: "<segment>"`.
- **Archive.** A gzipped tarball of the module's project root at the
  version's commit, with entries at the archive root and no wrapping
  directory. It is built once, when the version is created, from the
  commit tarball that decision 0012's provider abstraction fetches (through
  a VCS agent when the repository is private). It is stored at
  `modules/<account>/<module ulid>/<version ulid>.tar.gz`.
- **`lastPulledAt`.** Updated on download, at most once a minute per
  version, so that a busy module does not write on every `terraform init`.

### Provider protocol

- **Versions.** 200 with `{"versions":[{"version","protocols","platforms":[{"os","arch"}]}]}`,
  listing `PUBLISHED` versions only.
- **Download.** 200 with `protocols`, `os`, `arch`,
  `filename` (`terraform-provider-<type>_<version>_<os>_<arch>.zip`),
  `download_url`, `shasums_url`, `shasums_signature_url`, `shasum` (the
  registered `archiveChecksum`) and
  `signing_keys.gpg_public_keys[]` (`key_id`, `ascii_armor`) from the
  version's GPG key. An unknown version or platform answers 404.

### Signed file URLs

Terraform downloads archives, checksums and signatures without credentials,
so every file URL the registry hands out is signed and short-lived.

| `registry.download.mode` | URL | Used when |
|---|---|---|
| `presigned` (default) | A presigned `GET` on the object store (decision 0005) | The store is S3-compatible |
| `proxy` | `<base>/registry/files/<token>`, where the token is a compact JWS (HS256) naming the object key and expiry, keyed by a key derived from decision 0013's master key | Local disk storage, or a store that workers cannot reach |

`registry.download.url_ttl` sets the lifetime, default 10 minutes, close to
the roughly ten minutes Spacelift's signed URLs live. An expired or forged
token answers 403. In `proxy` mode an unreachable store answers 503 with
`Retry-After: 5`.

### Module versions

| From | Event | To |
|---|---|---|
| none | A tracked commit with a new `module_version`, `versionCreate`, `versionPush`, or a push policy `module_version` with `track` | `PENDING` |
| `PENDING` | Every test run finishes as expected, or the module has no test cases | `ACTIVE` |
| `PENDING` | A test run ends otherwise, or the archive cannot be built | `FAILED` |
| `FAILED` | `versionTrigger` | `PENDING`, then as above (C130; infraweave's choice) |
| any | `versionYank(note)` | Same state; `yanked`, `yankedBy` and `yankNote` set; permanent |
| any | `versionAnnotate(notes)` | Same state; `notes` set |
| any | `versionDelete` | Deleted; refused with 409 `invalid-state` while an Intent module deployment uses it |

- **Test outcome.** A test case passes when its `TESTING` run reaches
  `FINISHED`. A `negative` test case passes when its run ends `FAILED` in
  planning or applying and its destroying phase succeeds. Report 15 leaves
  that inversion open, so this rule is infraweave's.
- **Number format.** `^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`.
  Pre-release and build suffixes are rejected.
- **No reuse.** A unique partial index on `module_versions (module, number)
  WHERE state IN ('PENDING', 'ACTIVE')` refuses a number that is in use or
  succeeded. A `FAILED` number may be reused, and the failed row is kept.
- **No skipping.** Once a module has a non-failed version, a new number
  `X.Y.Z` needs a predecessor among its non-failed versions, yanked ones
  included:

  | New number | Predecessor required |
  |---|---|
  | `X.Y.Z`, `Z > 0` | `X.Y.(Z-1)` |
  | `X.Y.0`, `Y > 0` | any `X.(Y-1).*` |
  | `X.0.0`, `X > 0` | any `(X-1).*.*` |

  A module's first version may take any number, so history can be imported
  tag by tag. The table is derived from the documented example
  ([15 section 6.4](../reference/15-module-and-provider-registry.md#64-numbering-rules)).
  A refused number answers 422 `validation-failed` (decision 0022),
  whose detail names the rule.
- **Metadata.** The server parses the archive with
  `github.com/hashicorp/terraform-config-inspect`, an MPL-2.0 library used
  unmodified as a dependency, which MPL permits. `tfconfig.LoadModule` reads
  one directory, so the server calls it on the project root and on each
  `modules/*` and `examples/*` directory, which yields the root module,
  submodules and examples with inputs, outputs, dependencies, provider
  dependencies and resources. A module that needs symbolic links resolved
  or a runner image to parse gets a `PARSE` run instead, executed under
  decision 0008, and `metadataParsingRun` names it.
- **Consumers.** The worker reports the module sources and versions that
  initialization resolved (from `.terraform/modules/modules.json`), and
  `module_version_consumers` records them for `Version.consumers`,
  `consumerCount` and the trigger policy's `stacks` input.
- **Events.** Every state change emits a `module_version` event for
  notification policies ([0015](0015-events-notifications-webhooks-audit.md)).
  Reaching `ACTIVE` also evaluates the module's trigger policies
  (decision 0009).

### Provider versions

| From | Event | To |
|---|---|---|
| none | `terraformProviderVersionCreate` | `DRAFT` |
| `DRAFT` | `terraformProviderVersionPublish` | `PUBLISHED` |
| `DRAFT` | `terraformProviderVersionDelete` | Deleted; the number is free again |
| `PUBLISHED` | `terraformProviderVersionRevoke` | `REVOKED`; still listed in the API, never served, number never reusable |
| `PUBLISHED`, `REVOKED` | delete | 409 `invalid-state` (for `REVOKED`, C136; infraweave's choice) |
| `DRAFT`, `REVOKED` | revoke | 409 `invalid-state` (C136; infraweave's choice) |

- **Create.** `signingKeyId` must name a registered, unrevoked key. The
  answer carries presigned `PUT` URLs and headers for `SHA256SUMS` and
  `SHA256SUMS.sig` at `providers/<account>/<type>/<version ulid>/`.
  On an S3-compatible store the headers include `x-amz-checksum-sha256`
  with the declared `shaSumsFileSHA` or `signatureFileSHA`, converted from
  hex to the base64 form S3 expects, so the store refuses a body that does
  not match.
- **Register platform.** Only `terraformProviderVersionRegisterPlatformV2`
  is served. The V1 form is superseded
  ([0021](0021-capability-scope-and-supersession.md)). It is idempotent per
  `(version, os, arch)` and answers a presigned URL with its headers.
  infraweave validates neither the OS, the architecture nor the protocol
  versions, because Terraform does.
- **Publish.** Publishing checks that both checksum files and every
  registered platform archive exist, and that each archive's SHA-256 is the
  declared `archiveChecksum`. A missing or mismatched file answers 409 `invalid-state` and
  the version stays `DRAFT`. Publishing does not check the GPG signature.
- **Verify.** `terraformProviderVersionVerify` checks the detached signature
  of `SHA256SUMS` against the version's key with
  `github.com/ProtonMail/go-crypto/openpgp`. It then checks that the file
  lists every registered archive with its checksum. It sets `verifiedAt`, or
  `verificationFailure` with the first failure. It never changes the status.
- **Documentation.** `terraformProviderVersionUploadDocs` takes a base64
  gzipped tarball. Files are classified by path, following the Terraform
  Registry's documentation layout, while capture C137 is open:

  | Path | Type |
  |---|---|
  | `index.md` at the root or under `docs/` | `OVERVIEW` |
  | `resources/*`, `docs/resources/*`, `website/docs/r/*` | `RESOURCE` |
  | `data-sources/*`, `docs/data-sources/*`, `website/docs/d/*` | `DATA_SOURCE` |
  | anything else | `UNKNOWN` |

### GPG keys

- **Public keys only.** `gpgKeyCreate` takes an ASCII-armoured public key.
  An armoured private key block is refused with 422 `validation-failed`, so a private key never
  reaches infraweave, which is the property Spacelift relies on by leaving
  GPG keys out of its Terraform provider. Public keys are not secret, so
  `gpg_keys` holds no sealed column.
- **Identity.** A key's `id` is the primary key's long key ID, 16 upper-case
  hex characters. `signingKeyId` may name the primary key or any subkey
  with the signing capability, resolved to the same row. Registering a key
  already registered in the account answers 409 `already-exists`.
- **Revoke.** Revoking blocks new versions signed with the key. Published
  versions keep serving it in `signing_keys`. `gpgKeyUpdate` changes only
  the description.
- **Who.** Only root space admins manage keys (decision 0007), and keys
  belong to the account, not a space.

### Sharing

| Layer | Rule |
|---|---|
| Space inheritance | Stacks in the module's space and in inheriting child spaces may consume it |
| Space shares | `moduleShare` replaces the full set of `spaces` and `accounts` in one transaction. Admin on the module's space and Write or `SPACE_SHARE_MODULE` on each target space are required |
| Account shares | The `accounts` entries name accounts of the same installation; an unknown name answers 422 `validation-failed` |
| Public | `modulePublish` is irreversible; the module is then readable anonymously. `terraformProviderSetVisibility` toggles a provider either way |

A consumer of a shared module reads only the `SharedModule` fields and the
source space's `id`, `name` and `parentSpace`, as Spacelift exposes them.

### Feature gates and limits

| `BillingTierFeature` | Gates |
|---|---|
| `TERRAFORM_PROVIDER_VERSIONS` | The Terraform Providers and GPG Keys operations, and the provider protocol |
| `PRIVATE_MODULE_SHARING` | `moduleShare` with a non-empty `accounts`, and `sharedModulesV2` entries from other accounts |

A disabled feature answers 403 `feature-disabled` on the deploy API
([0021](0021-capability-scope-and-supersession.md)). The provider protocol
answers 403 with the `text/plain` body `feature disabled`, which Terraform
prints in its error.

| Setting | Default |
|---|---|
| `registry.modules.max_archive_size` | 64 MiB |
| `registry.providers.max_archive_size` | 512 MiB per platform archive |
| `registry.providers.max_sums_size` | 1 MiB for `SHA256SUMS`, 64 KiB for the signature |
| `registry.providers.max_docs_size` | 10 MiB decoded, at most 1,000 files, 1 MiB each |
| `registry.download.url_ttl` | 10m |

Protocol requests draw from 0003's `standard` cost class.

### Storage

The tables are report 28's `modules`, `module_versions`,
`module_version_consumers`, `module_shares`, `terraform_providers`,
`provider_versions`, `provider_version_platforms` and `gpg_keys`, plus
`provider_version_docs (version, key)` holding each file's type and object
key. Archives, checksum files, signatures and documentation files live in
object storage under `modules/` and `providers/` (decision 0005), and the
tables hold their keys and checksums. Spacelift's self-hosted "Modules"
bucket keeps objects indefinitely. infraweave deletes a version's objects
only when the version itself is deleted.

## Rationale

- **Spacelift's protocol quirks, not HashiCorp's spec.** Terraform treats an
  empty list and a 404 the same way. Reproducing the captured answers keeps
  Dependabot, Renovate and scripts that probe the registry working as they
  do against Spacelift, and costs nothing.
- **Presigned object-store URLs by default.** They move archive bytes off
  the `serve` role, as Spacelift's CloudFront URLs do. The `proxy` mode covers
  local disk and networks where workers cannot reach the store.
- **Checksums enforced by the store.** A checksum header in the presigned
  `PUT` rejects a corrupt upload at the source, instead of a later
  `terraform init` failing on a hash mismatch.
- **Rejected: a separate registry service**, as Terrakube runs one
  ([27 section 4.1](../reference/27-reference-materials.md#41-architecture)). The
  registry shares the module's spaces, RBAC and runs, so splitting it out
  would duplicate authorization. OTF's in-process registry with signed
  downloads is the closer model
  ([27 section 12](../reference/27-reference-materials.md#12-cross-tool-comparison)).
- **Rejected: holding provider signing keys.** Signing on the server would
  let infraweave sign provider releases, but the private key would then
  live beside every other secret. Spacelift deliberately keeps it with the
  publisher.
- **Departure from Spacelift:** discovery and the protocol share one host,
  where Spacelift splits `spacelift.io` from `app.spacelift.io`.
- **Departure from Spacelift:** publishing a provider version checks that
  every uploaded file exists and matches its declared checksum. On
  Spacelift a failed verification blocks neither publishing nor use, so a
  bad upload surfaces only when Terraform installs the provider
  ([15 section 10.5](../reference/15-module-and-provider-registry.md#105-verification)).
- **Departure from Spacelift:** the protocol's 404 bodies are served as
  `application/json`, where Spacelift labels the same JSON `text/plain`.
- **Departure from Spacelift:** yanked versions are neither listed nor
  downloadable, even by an exact pin. This stays a departure until capture
  C132 shows what Spacelift does.

## Constraints

- Captures C130 to C138 may change the choices marked as infraweave's: the
  retest path, yanked downloads, draft revoke, documentation
  classification, the action gating verify and docs upload, and public
  provider access.
- `versionPush` creates a version from a tarball URL. Its purpose is
  undocumented (C131). infraweave fetches the URL with a 64 MiB cap and a
  30-second timeout and treats the result as the version's archive.
- `server.public_url` must be the name that clients write in `source`
  addresses. Changing it breaks existing addresses, because Terraform
  stores the host in the lock file.
- A module's archive is built from the project root only. A module that
  references files outside its project root with `../` fails the same way
  it would from any registry.
- Test cases run under decision 0008. A worker pool with no capacity leaves
  a version `PENDING` until the run's own timeout applies.

## Verification

- `test/acceptance/features/registry_protocol.feature` (`@postgres`,
  `@objectstore`): discovery document, every status and body in the module
  and provider tables above, lenient version parsing, and anonymous access
  to public modules and providers.
- `test/acceptance/features/registry_modules.feature` (`@postgres`,
  `@objectstore`, `@vcs`, `@terraform`, `@tofu`): a module in a Gitea
  repository with two test cases, one negative, reaches `ACTIVE`. A real
  `terraform init` and `tofu init` then resolve it through the API key
  token form. Yanking hides the version, and a shared space gains access.
- `test/acceptance/features/registry_providers.feature` (`@postgres`,
  `@objectstore`, `@terraform`): a GoReleaser-shaped fixture signed with a
  key generated in the test is published through the create, register,
  upload and publish sequence. `terraform init` then installs it with
  signature verification, and revoking the version removes it from
  `versions`.
- Unit tests in `internal/registry`: `TestUnitVersionNumberRules`
  (format, reuse, skipping, first version), `TestUnitVersionSegmentParse`,
  `TestUnitDocsClassification`, `TestUnitVerifyProviderVersion` (a good
  signature, tampered sums, a missing platform, a revoked key) and
  `TestUnitGPGKeyRejectsPrivateKey`.
- The Terraform provider parity oracle of
  [0016](0016-testing-and-acceptance.md) exercises `spacelift_module`,
  `spacelift_version` and `spacelift_terraform_provider` against
  infraweave's own provider.

## References

- [15](../reference/15-module-and-provider-registry.md), especially [section 13](../reference/15-module-and-provider-registry.md#13-self-hosted-differences) and [section 5](../reference/15-module-and-provider-registry.md#5-test-cases-and-test-runs).
- [26 section 3](../reference/26-scope-and-tiers.md#3-billingtierfeature): the gating values.
- [28 section 12.7](../reference/28-data-model.md#127-modules-and-providers): tables.
- [30](../reference/30-open-questions.md#module-and-provider-registry-report-15): captures C129 to C138.
- HashiCorp's [service discovery](https://developer.hashicorp.com/terraform/internals/remote-service-discovery), [module](https://developer.hashicorp.com/terraform/internals/module-registry-protocol) and [provider](https://developer.hashicorp.com/terraform/internals/provider-registry-protocol) registry protocols.
- [`hashicorp/terraform-config-inspect`](https://github.com/hashicorp/terraform-config-inspect) (MPL-2.0); [`ProtonMail/go-crypto`](https://github.com/ProtonMail/go-crypto) (BSD-3-Clause).
