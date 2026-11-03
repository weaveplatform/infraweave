# 0016: Testing and acceptance against real dependencies

Status: Accepted

## Context

infraweave's claim is behavioural parity with Spacelift behind its own REST
API. Three kinds of evidence back that claim, and each needs a test that
can fail:

- **The served API matches the reference.** The specification reference
  is a parity oracle for operation coverage, shapes and client contracts
  ([01 section 12](../reference/01-spacelift-landscape.md#12-the-specification-reference-as-parity-oracle)).
  Two features already check the reference and the served bundle
  (`test/acceptance/features/spec_reference.feature`,
  `deploy_spec.feature`).
- **The behaviour matches Spacelift.** Runs, policies, state, VCS
  feedback and event delivery are behaviours, not shapes. They only show
  against real PostgreSQL, real object storage, a real launcher and a real
  Terraform or OpenTofu binary.
- **The clients work.** infraweave's own Terraform provider and CLI
  replace Spacelift's (decision [0001](0001-rest-first-spacelift-surface.md)),
  and `terraform-schema.json` is the oracle for the provider
  ([05 section 7](../reference/05-terraform-provider.md#7-later-provider-parity-in-deployci)).

The template's gate applies: merged coverage of at least 95% in total and
90% per package across unit runs on three operating systems and the
acceptance runs, blocking lint, and acceptance against real dependencies in
Docker. GitHub's macOS runners have no Docker daemon, and its Windows
runners run Windows containers only, so neither can start the Linux
containers acceptance needs. Report 26
gives each capability an availability state
([26 section 1.2](../reference/26-scope-and-tiers.md#12-availability-states));
`implemented` needs a definition a test can decide.

## Decision

### Test kinds

| Kind | Name | Where | Runs on | Starts |
|---|---|---|---|---|
| Unit | `TestUnit<Name>` | beside the code, `_test.go` | ubuntu, macOS and Windows | nothing outside the test process |
| Acceptance | `TestAcc<Name>` | `test/acceptance/` and contract suites | ubuntu only | containers and subprocesses through testcontainers-go |

The kind belongs to the test function, not the package. A guard test,
`TestUnitTestNames`, parses every `_test.go` file and fails on a top-level
`Test` function that is neither `TestUnit…` nor `TestAcc…`, so `make test`
can select `-run '^TestUnit'` and `make accept` `-run '^TestAcc'`.

### Unit tests

- Standard library plus `go-cmp`; no assertion library. Table-driven, with
  hand-written fakes behind narrow interfaces.
- Always `-race -shuffle=on -count=1` (the existing `make test`).
- Time comes from an injected clock (`internal/clock`, indicative; decision
  0004 owns placement). Code that waits uses `testing/synctest`, so timers,
  backoff and lease expiry run in virtual time.
- HTTP fakes of external APIs are chi routers, per the repository rule:
  GitHub, GitLab, Bitbucket Cloud and Data Center, Azure DevOps, Slack,
  Teams, cloud STS endpoints and AI model providers each get a fake in a
  `…fake` package beside their client, with fault modes (5xx, timeout,
  malformed body, slow body, rate-limit response) selectable per request.
- SQL is not mocked. Code that talks to PostgreSQL or object storage keeps
  its decision logic in plain functions with unit tests, and its queries
  are covered by the contract suites below.

### Contract suites

Each storage interface has a contract package that any implementation must
pass:

| Suite | Implementations it runs against | Kind |
|---|---|---|
| `internal/store/storetest` | PostgreSQL at the version floor decision 0005 sets (blocking), and at the next major (reported, not blocking) | `TestAcc` |
| `internal/objectstore/objectstoretest` | Every object store driver of decision 0005: `s3` on RustFS, `gcs` on fake-gcs-server, `azureblob` on Azurite, and `fs` on local disk; all blocking | `TestAcc` for the three emulators, `TestUnit` for local disk |
| `internal/queue/queuetest` | the work queue of decision 0011 on PostgreSQL | `TestAcc` |

Each PostgreSQL case gets its own database, created with
`CREATE DATABASE … TEMPLATE iw_template` from a template the suite migrates
once per container, so cases run in parallel without sharing rows.

**The object store contract against emulators.** The suite covers decision
0005's contract: put, get, head, delete, multipart, checksums, signed URLs
and lifecycle. The emulators cover most of it; what one cannot show runs
against the real service in the nightly cloud job (below). Capabilities are
as each project documents them at RustFS 1.0.4, fake-gcs-server 1.56.1 and
Azurite 3.37.0:

| Emulator (licence) | Driver | What it supports that the contract needs | Gaps |
|---|---|---|---|
| [RustFS](https://github.com/rustfs/rustfs) (Apache-2.0; 1.0.0 released 2026-09-16) | `s3` | Object put, get, head and delete; multipart create, upload, complete and abort; presigned `GET` and `PUT`; SHA-256 checksum validation on `PutObject` and on multipart uploads; each checked by its s3-tests lane ([S3 compatibility matrix](https://github.com/rustfs/rustfs/blob/main/docs/architecture/s3-compatibility-matrix.md)) | Checksums on POST-form uploads do not yet pass, which infraweave does not use. AWS KMS server-side encryption and EKS Pod Identity credentials exist only on AWS |
| [fake-gcs-server](https://github.com/fsouza/fake-gcs-server) (BSD-2-Clause) | `gcs` | Media, multipart and resumable uploads, compose, delete, and verification of a declared MD5 or CRC32C (`fakestorage/upload.go`) | It accepts signed URLs without validating their signature or expiry ([README](https://github.com/fsouza/fake-gcs-server#using-with-signed-urls)), so the expired and altered URL cases run only against GCS. It has no IAM `signBlob`, so in CI the driver signs with a test service-account key, and the keyless path runs only against GCS |
| [Azurite](https://github.com/Azure/Azurite) (MIT) | `azureblob` | Block blobs with Put Block and Put Block List, account and service SAS, CRC-64 transactional checksums, and OAuth bearer tokens over HTTPS (`--oauth basic`) ([support matrix](https://github.com/Azure/Azurite#support-matrix)) | User delegation SAS, which the driver signs, is implemented in Azurite's source (`ServiceHandler.getUserDelegationKey`, `BlobSASAuthenticator`) but absent from its support matrix, and issue [#2420](https://github.com/Azure/Azurite/issues/2420) reports the key call failing. At `basic`, Azurite checks a token's issuer, audience and expiry but not its signature. The delegation-SAS cases run against Azurite in CI and against Azure nightly |
| local disk | `fs` | Everything; it is infraweave's own | none |

A case the emulator cannot decide is skipped on that emulator by name, with
the reason, and `TestUnitObjectstoreGaps` fails when a skipped case has no
counterpart in the nightly cloud job.

### Acceptance harness

- **Runner.** `TestAccFeatures` runs godog over
  `test/acceptance/features/<area>_<topic>.feature` with `Strict: true`.
  Steps that drive the API use the SDK, `pkg/sdk`
  ([0001](0001-rest-first-spacelift-surface.md)), so every scenario also
  exercises it.
- **The binary under test.** The harness builds `cmd/infraweave` once with
  `-cover -covermode=atomic` and runs it as a subprocess with `GOCOVERDIR`
  set to `cover/accept`, so `infraweave serve`, `scheduler`, `launcher` and `runner` all
  contribute coverage to the merge. In-process servers are not used for
  acceptance: they would skip flag parsing, config decoding and signal
  handling.
- **Isolation.** Each scenario gets a fresh database (template copy), a
  fresh bucket prefix and its own `infraweave all` process on a free
  port. Containers are shared across scenarios within one test binary.
  Scenarios run concurrently (godog `Concurrency` set to `GOMAXPROCS`),
  which this isolation permits.
- **Dependency tags.** A scenario names the real dependencies it needs; the
  harness starts each one lazily, once per test binary:

| Tag | What starts | Notes |
|---|---|---|
| (none) | nothing; the scenario reads committed files or runs the binary in a mode that needs no database, such as config validation | spec, config and CLI parsing scenarios |
| `@postgres` | `postgres` container, testcontainers-go `postgres` module | every scenario that stores anything |
| `@objectstore` | RustFS container, used through the `s3` driver | state, logs, uploads, module and provider archives |
| `@terraform` | a pinned Terraform release, fetched once and checked against its SHA-256 | a real run (below) |
| `@tofu` | a pinned OpenTofu release, checked the same way | a real run (below) |
| `@oidc` | an in-process chi OIDC issuer: discovery, JWKS, token minting with chosen claims | SSO login, OIDC API keys, verifying infraweave's own run tokens from its JWKS (decision 0006) |
| `@vcs` | Gitea container, with a repository seeded per scenario | real webhooks, commit statuses, pull request comments (decision 0012) |
| `@ai` | an in-process chi server replaying recorded model responses | AI integrations and Intent ([0019](0019-ai-provider-boundary.md)) |
| `@kubernetes` | a k3s container, testcontainers-go `k3s` module | Kubernetes workers (decision 0008) |
| `@flows-agent` | a test app built on `pkg/flows/appsdk` as a local OCI image, and an `infraweave flows-agent` subprocess that runs it in Docker | Flows engine, app and agent scenarios ([0018](0018-flows-runtime.md)) |

- **A real run.** `@terraform` and `@tofu` scenarios exercise the full
  path: the server, a private worker pool, `infraweave launcher` as a host
  subprocess talking to the Docker daemon, and the runner image built by
  the harness from the repository's image definition
  ([0020](0020-reference-deployments-and-packaging.md)) and tagged
  `infraweave-runner:acceptance`. The workspace arrives as a local-preview
  upload, or from Gitea when the scenario is also `@vcs`. Configurations
  use `terraform_data` and other built-ins, so no provider is downloaded;
  a scenario that needs a provider uses a filesystem mirror of pinned
  provider archives checked by SHA-256. State goes through infraweave's
  HTTP state backend (decision 0010) into RustFS. The scenario asserts the
  run's state transitions against report 08's state machine, the stored
  state version, the masked log, and the events emitted
  ([0015](0015-events-notifications-webhooks-audit.md)).
- **Images.** Every container image is pinned by digest in
  `test/acceptance/images.go`; `deps-refresh.yml` moves the pins to the
  newest release.

### Docker: skip locally, never in CI

- Without a reachable Docker daemon, the harness skips every scenario that
  carries a dependency tag other than `@oidc` and `@ai`, and prints the
  tags it skipped. Untagged, `@oidc` and `@ai` scenarios still run.
- CI sets `INFRAWEAVE_ACCEPT_REQUIRE_DOCKER=1`. With it set, an unreachable
  daemon fails `TestAccFeatures` before the first scenario, and any skipped
  scenario fails the run, so a CI pass always means every scenario ran.

### Parity checks against the specification reference

`specref` gains a `parity` subcommand, run by `make verify-parity` and by
an acceptance scenario in `spec_reference.feature`. For every operation in
`api/reference/spacelift/spacelift.openapi.yaml` it decides one outcome:

| Outcome | Condition |
|---|---|
| served | `api/deploy.openapi.yaml` has the same `operationId` with the same method and path |
| served with divergence | the same `operationId`, with each difference listed in `x-infraweave-divergence` |
| superseded | listed in report 26 section 7, under the rule of [0021](0021-capability-scope-and-supersession.md) |
| missing | none of the above |

It fails when a served operation differs from the reference without a
recorded divergence, when a superseded operation is served, or when an
operation is both. "Missing" is reported, not failed: it is the backlog.

### Behavioural coverage of operations

A scenario names the operations it exercises with tags
`@op:<operationId>`. `specref parity --states` combines the parity outcome
with the last acceptance result (godog's JSON report in
`cover/accept/cucumber.json`) and computes the state of each tag and
Terraform record of [capabilities.json](../../data/capabilities.json):

The three-scenario evidence rule decides `implemented`:

| State | Computed when |
|---|---|
| `implemented` | every operation is served, answers something other than 501, and is named by at least three passing scenarios: one success path, one denial (403 under decision 0007) and one failure path |
| `partial` | some operations meet the `implemented` condition |
| `stub` | every operation is routed but answers 501 |
| `missing` | at least one operation has no route |
| `superseded` | as in the parity table |

A Terraform resource or data source is `implemented` only when the
operations it calls are and its provider scenario (below) passes. The
register's states stay hand-maintained (report 30, PD-12);
`specref parity --states --check docs/data/capabilities.json` fails when a
recorded state disagrees with the computed one, so the register cannot
claim more than the tests show. The scope behind the states is
[0021](0021-capability-scope-and-supersession.md)'s.

### The Terraform provider parity oracle

Two checks, both against infraweave's own provider, whose source lives in
its own repository (decision [0001](0001-rest-first-spacelift-surface.md)):

1. **Schema parity.** `specref provider-parity` compares infraweave's
   `terraform providers schema -json` output with
   `api/reference/spacelift/terraform-schema.json`. Every Spacelift
   resource, data source and attribute must map to an infraweave one, by
   name or through `api/reference/provider-parity.yaml`, which records each
   rename, each type change and each omission with its reason (superseded
   per report 26, or a REST divergence). An unmapped name fails.
2. **Behaviour.** For each resource, `test/terraform/<resource>/` holds a
   configuration translated from the Spacelift provider's own acceptance
   configurations (MIT, attributed in `NOTICE`). A `TestAccProvider`
   scenario, tagged `@op:` with the operations the resource calls, builds
   the provider from source into a filesystem mirror and runs, against a
   live `infraweave all`: `apply`; `plan` with `-detailed-exitcode`,
   which must report no changes; `import` of each resource by the import ID
   report 05 records; another empty `plan`; `destroy`. The behaviours
   worth copying in
   [05 section 6](../reference/05-terraform-provider.md#6-behaviour-worth-copying)
   each get a scenario.

The CLI is checked the same way against the capability list in
[06 section 8](../reference/06-spacectl-and-clients.md#8-what-an-infraweave-cli-must-cover):
one `cli_<area>.feature` per row, driving the built CLI binary.

### Capture fixtures

Report 30 resolves a capture by checking it in as a fixture
([30, how to use this list](../reference/30-open-questions.md#how-to-use-this-list)).
The fixture layout is:

| Path | Content |
|---|---|
| `test/fixtures/captures/C<n>/meta.yaml` | capture date, account tier, introspection hash, the report 30 entry, who captured it, what was redacted |
| `…/C<n>/request.graphql`, `…/C<n>/response.json` | the GraphQL exchange as sent and received, redacted |
| `…/C<n>/expect.yaml` | the infraweave REST call that stands for it and the assertions on status, problem type and body fields that follow from the capture |

- `TestUnitCaptureRedaction` fails on anything in a fixture that looks like
  a credential (JWT, API key secret, AWS key, private key block) or an
  e-mail address outside `example.com`.
- `TestAccCaptures` replays every `expect.yaml` against a live server. A
  capture whose expectation fails is a parity bug, not a flaky test.
- A behaviour no capture has settled is tested against what infraweave
  decided, and the scenario names the decision record, so nothing presents
  a guess as Spacelift's behaviour.

The policy example library
([09 section 10](../reference/09-policies.md#10-the-example-library-at-6ff5edb08bc8))
is a corpus of the same kind: its tests run against the embedded engine of
decision 0009 as `TestUnitPolicyLibrary`.

### Determinism

| Rule | Enforcement |
|---|---|
| No `time.Sleep` in tests | forbidigo pattern on `_test.go` files in `.golangci.yml` |
| Waiting on asynchronous work polls a condition with a deadline, through one helper (`acceptance.eventually`), never a fixed delay | review; the helper reports what it waited for |
| Unit tests use virtual time | `internal/clock` and `testing/synctest` |
| No test depends on order | `-shuffle=on`; godog runs scenarios in random order with a printed seed |
| No shared mutable state across scenarios | per-scenario database, bucket prefix and server |
| No network beyond the containers | Terraform, OpenTofu and providers come from checksummed caches; Gitea replaces hosted VCS |
| A failing scenario leaves evidence | the harness writes the server's and launcher's logs and the database dump of that scenario to `test/acceptance/out/<scenario>/`, uploaded as a CI artefact on failure |

A test that fails intermittently is quarantined only by a tracking issue
and a `@quarantine` tag, which the gate still runs and reports; the
coverage it would contribute is not waived.

### Coverage

- Unit coverage comes from each OS job; acceptance coverage from every
  acceptance partition. `make cover` merges them with `go tool covdata` and
  enforces `.testcoverage.yml`: 95% in total, 90% per package.
- Exclusions stay as they are: `cmd/`, `tools/`, generated server code and
  `pkg/client`. Adding one needs an amendment to this record.
- Unreachable defensive branches are removed, not excluded.

### CI

| Job | Runner | Runs | Timeout | Required |
|---|---|---|---|---|
| `unit` | `ubuntu-latest`, `macos-latest`, `windows-latest` | `make test` | 15 min | yes |
| `accept-core` | `ubuntu-latest` | untagged, `@postgres`, `@objectstore`, `@oidc` | 20 min | yes |
| `accept-runs` | `ubuntu-latest` | `@terraform`, `@tofu`, `@kubernetes`, `@flows-agent` | 30 min | yes |
| `accept-integrations` | `ubuntu-latest` | `@vcs`, `@ai` | 20 min | yes |
| `provider-parity` | `ubuntu-latest` | `specref provider-parity` and `TestAccProvider` | 30 min | yes |
| `parity` | `ubuntu-latest` | `make verify-parity` and the state check | 5 min | yes |
| `fips-only` | `ubuntu-latest` | `make test` and every acceptance partition, with the binary under test built with `GOFIPS140=certified` and every process run with `GODEBUG=fips140=only` ([0013](0013-secrets-at-rest.md#fips-140-3)) | 45 min | yes |
| `console` | `ubuntu-latest` | `npm ci`, `tsc --strict`, ESLint, Vitest with its coverage thresholds and the production build in `web/console/` ([0023](0023-console.md)) | 15 min | yes |
| `console-e2e` | `ubuntu-latest` | the Playwright journeys against a real `infraweave all` built with `-cover` ([0023](0023-console.md)) | 30 min | yes |
| `coverage` | `ubuntu-latest` | `make cover` over every job above except `fips-only` and `console`, whose TypeScript coverage has its own thresholds | 10 min | yes |
| `postgres-next` | `ubuntu-latest` | `storetest`, `queuetest` and `accept-core` on the next PostgreSQL major, its beta or release candidate until it is generally available ([0005](0005-persistence-postgresql.md#engine-and-versions)) | 20 min | no |
| `nightly` | `ubuntu-latest`, scheduled | the full suite on the newest Terraform and OpenTofu releases | 60 min | no |
| `objectstore-cloud` | `ubuntu-latest`, scheduled nightly | `objectstoretest` against real Amazon S3, Google Cloud Storage and Azure Blob Storage | 20 min | no |

- **`fips-only` and coverage.** It runs the same tests as the jobs above
  under a stricter runtime, so its coverage adds nothing and is not merged.
- **The cloud object store job.**
  - **Credentials.** GitHub's OIDC token is federated to an AWS IAM role, a
    Google Cloud workload identity pool and a Microsoft Entra federated
    credential, each scoped to this repository's scheduled workflow on
    `main`. No access key, service-account key or storage key is stored.
  - **Buckets.** One test bucket per cloud, used only by this job, with a
    lifecycle rule that deletes every object one day after it is written.
    Each run writes under its own prefix and deletes it at the end; the rule
    clears what a failed run leaves.
  - **What it covers.** The whole contract, including the cases the
    emulators cannot decide: signed URL expiry and tampering on GCS, keyless
    signing through IAM `signBlob`, and user delegation SAS under Entra.
  - **Two identities per cloud.** A writer identity with the permissions
    [0020](0020-reference-deployments-and-packaging.md)'s least-privilege
    table gives `serve` runs the `objectstore.Writer` cases, and a deleter
    with `scheduler`'s runs the delete cases. A case that asserts the
    writer's delete is refused by the cloud proves that `serve` needs no
    delete permission ([0005](0005-persistence-postgresql.md#object-storage)),
    including after a checksum mismatch.
  - **Failures.** A failing run opens an issue for that cloud, or comments on
    the open one, with the failing cases and the run's link. A passing run
    leaves an open issue for a person to close.

- A scenario's partition is the first of `accept-runs`,
  `accept-integrations` and `accept-core` that lists one of its tags, so a
  `@postgres` `@terraform` scenario runs once, in `accept-runs`.
  `TestUnitFeaturePartitions` parses every feature and fails when a
  scenario carries a tag no partition lists.
- Every required job is in the `needs` of the `Quality gate` job and, when
  it produces coverage, of `coverage`.
- When a partition approaches its timeout, it splits by tag into two
  required jobs; scenarios are never dropped to fit.

## Rationale

- **Subprocess binaries, not in-process servers.** The deployable artefact
  is what users run; config strictness, process mode selection, migrations at
  start and signal handling are part of the contract. Go's `-cover` build
  makes subprocess coverage count toward the gate.
- **A real run in the suite.** Runs are infraweave's core behaviour. A fake
  launcher would prove the protocol to itself; a real launcher, runner
  image and Terraform binary prove what a user sees, including masking and
  state upload.
- **Template databases.** A fresh database per scenario costs one
  `CREATE DATABASE … TEMPLATE`, far less than migrating each time, and
  removes cross-scenario interference without serializing the suite.
- **`@op:` tags for states.** The recorded states would otherwise be a
  claim; tying them to passing scenarios makes each recorded state a test
  result, and the three-scenario rule stops a happy-path test from
  certifying an operation whose denials and failures are untested.
- **Rejected: sqlmock or an in-memory store for acceptance.** They accept
  SQL PostgreSQL rejects and miss locking behaviour the queue and state
  locks depend on.
- **Every driver in the blocking gate.** The maintainer's rule is that
  infraweave is not locked to Amazon S3, so the `gcs` and `azureblob`
  drivers carry the same weight as `s3`. Emulators keep every pull request
  hermetic and fast; the nightly cloud job checks what an emulator cannot,
  and stays out of the gate because real-cloud flakiness and credentials
  should not block merges.
- **RustFS for S3.** It is Apache-2.0, released 1.0.0 on 2026-09-16, and
  runs the S3 compatibility tests for the features the contract needs.
  - **Rejected: MinIO.** Its community edition stopped publishing images in
    October 2025, entered maintenance mode in December 2025, and its
    repository now states that it is no longer maintained
    ([minio/minio](https://github.com/minio/minio)), so a pin could never
    move to a fixed release.
  - **Rejected: LocalStack.** It emulates many AWS services, which the suite
    does not need, at a larger image size.
- **fake-gcs-server and Azurite** are the maintained open-source emulators
  for their services, and both speak the APIs the native SDKs call, so the
  drivers run unchanged against them.
- **OIDC federation for the cloud job.** Stored cloud keys in repository
  secrets would be long-lived credentials with write access to real
  buckets; federated tokens last minutes and only the scheduled workflow can
  obtain them.
- **Rejected: running acceptance against Spacelift itself.** It needs an
  authorized account for every CI run and would test Spacelift, not
  infraweave. Captures, taken once with permission and replayed, carry the
  same evidence (report 30).
- **Rejected: hosted GitHub or GitLab in acceptance.** They need
  credentials and network. Gitea gives real webhooks and statuses; the
  provider-specific shapes are covered by the chi fakes and recorded
  payloads.
- **Rejected: acceptance on macOS and Windows.** The macOS runners have no
  Docker daemon and the Windows runners cannot run Linux containers; the
  server's behaviour does not depend on the OS, and unit tests
  cover OS-specific code on all three.
- **Borrowed:** Temporal's in-process test server and River's job
  assertion helpers show the value of testing against the real engine
  ([27 section 10](../reference/27-reference-materials.md#10-work-queue-and-workflow-engines));
  Spacelift's `local-worker-pool` shows a launcher beside a Docker daemon
  as a local test worker
  ([27 section 11](../reference/27-reference-materials.md#11-spacelifts-own-open-source-pieces)).
- **Departure from Spacelift:** none in behaviour. This record governs how
  infraweave proves its behaviour, not what it serves.

## Constraints

- Acceptance needs Docker on Linux. Local runs on macOS and Windows need
  Docker Desktop or an equivalent for the tagged scenarios.
- Terraform's release licence permits running it in CI; the runner image's
  bundled tools are decision 0020's choice, and the suite tests both
  Terraform and OpenTofu regardless.
- Captures exist only where an authorized account was available (report 30,
  "Capture safety"). Until then, scenarios test infraweave's recorded
  choices.
- The Spacelift provider's acceptance configurations are translated, not
  copied verbatim, where they reference Spacelift-only resources; the
  translation is recorded in `provider-parity.yaml`.
- The `nightly`, `postgres-next` and `objectstore-cloud` jobs are
  informational: a newer Terraform, OpenTofu or PostgreSQL release, or a
  real cloud, that breaks a case opens an issue rather than blocking merges.
- The cloud job needs one lab account or project per cloud, its OIDC trust
  and its bucket, which the maintainer provisions; until they exist the job
  reports itself as not configured rather than passing.
- Under `GODEBUG=fips140=only`, a test helper or library that reaches for a
  refused algorithm fails the `fips-only` job like product code does; the
  fix is an approved algorithm, not an exemption.

## Verification

- `TestUnitTestNames`, `TestUnitFeaturePartitions` and
  `TestUnitCaptureRedaction` guard the rules above.
- `test/acceptance/features/acceptance_harness.feature` proves the harness
  itself: a scenario skipped without Docker, a failure with
  `INFRAWEAVE_ACCEPT_REQUIRE_DOCKER=1`, per-scenario database isolation,
  and evidence written on failure.
- `test/acceptance/features/runs_terraform.feature` and
  `runs_tofu.feature` carry the first real-run scenarios.
- `make verify-parity` and the state check run in the `parity` job; the
  existing `spec_reference.feature` and `deploy_spec.feature` keep running
  in `accept-core`.
- The `Quality gate` job lists every required job above in its `needs`, and
  none of the informational ones.
- `TestUnitObjectstoreGaps` ties each emulator skip to a case of the cloud
  job.

## References

- [01 section 12](../reference/01-spacelift-landscape.md#12-the-specification-reference-as-parity-oracle): the parity oracle.
- [05 section 7](../reference/05-terraform-provider.md#7-later-provider-parity-in-deployci): provider parity.
- [06 section 8](../reference/06-spacectl-and-clients.md#8-what-an-infraweave-cli-must-cover): CLI capabilities.
- [26 section 1.2](../reference/26-scope-and-tiers.md#12-availability-states): availability states.
- [30](../reference/30-open-questions.md#captures-needed): captures.
- <https://github.com/cucumber/godog>
- <https://github.com/testcontainers/testcontainers-go>
- Emulators: [RustFS](https://github.com/rustfs/rustfs),
  [fake-gcs-server](https://github.com/fsouza/fake-gcs-server),
  [Azurite](https://github.com/Azure/Azurite).
- GitHub OIDC: <https://docs.github.com/en/actions/security-for-github-actions/security-hardening-your-deployments/about-security-hardening-with-openid-connect>
- <https://pkg.go.dev/testing/synctest>
