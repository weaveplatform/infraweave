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
  (`test/acceptance/features/api/reference_spec.feature`,
  `api/bundle.feature`).
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
| The `pg` store tests, on `internal/platform/sql/sqltest` | PostgreSQL at the version floor decision 0005 sets (blocking), and at the next major (reported, not blocking) | `TestAcc` |
| `internal/platform/objectstore/objectstoretest` | Every object store driver of decision 0005: `s3` on RustFS, `gcs` on fake-gcs-server, `azureblob` on Azurite, and `fs` on local disk; all blocking | `TestAcc` for the three emulators, `TestUnit` for local disk |
| `internal/queue/queuetest` | the work queue of decision 0011 on PostgreSQL | `TestAcc` |
| `internal/platform/crypt/kek/kektest` | Every KEK provider of decision 0013: `vault` on Vault, `awskms` on [moto](https://github.com/getmoto/moto)'s KMS (Apache-2.0), both blocking; `gcpkms` and `azurekv` on recorded HTTP fixtures, served by a chi server that matches each request; `local` in process | `TestAcc` for the two emulators, `TestUnit` for the rest |

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
| [fake-gcs-server](https://github.com/fsouza/fake-gcs-server) (BSD-2-Clause) | `gcs` | Media, multipart and resumable uploads, compose, delete, and verification of a declared MD5 or CRC32C (`fakestorage/upload.go`) | It accepts signed URLs without validating their signature, expiry, method or signed `Content-Length` ([README](https://github.com/fsouza/fake-gcs-server#using-with-signed-urls)), so the expired, altered, other-method and oversized URL cases run only against GCS. It has no IAM `signBlob`, so in CI the driver signs with a test service-account key, and the keyless path runs only against GCS |
| [Azurite](https://github.com/Azure/Azurite) (MIT) | `azureblob` | Block blobs with Put Block and Put Block List, account and service SAS, CRC-64 transactional checksums, and OAuth bearer tokens over HTTPS (`--oauth basic`) ([support matrix](https://github.com/Azure/Azurite#support-matrix)) | User delegation SAS, which the driver signs, is implemented in Azurite's source (`ServiceHandler.getUserDelegationKey`, `BlobSASAuthenticator`) but absent from its support matrix, and issue [#2420](https://github.com/Azure/Azurite/issues/2420) reports the key call failing. At `basic`, Azurite checks a token's issuer, audience and expiry but not its signature. So in CI the driver signs service SAS with Azurite's account key, which Azurite verifies with the SAS's permissions and expiry, a unit test covers the delegation key's reuse, and user delegation SAS runs against Azure nightly |
| local disk | `fs` | Everything; it is infraweave's own | none |

A case the emulator cannot decide is skipped on that emulator by name, with
the reason, and `TestUnitObjectstoreGaps` fails when a skipped case has no
counterpart in the nightly cloud job. A case the service itself cannot
bind, such as a SHA-256 in a Cloud Storage or Azure signed URL, is skipped
everywhere, and the reason names where the contract checks it instead.

### Acceptance harness

- **Runner.** `TestAccFeatures` runs godog over
  `test/acceptance/features/<area>/<topic>.feature` with `Strict: true`.
  Steps that drive the API use the SDK, `pkg/sdk`
  ([0001](0001-rest-first-spacelift-surface.md)), so every scenario also
  exercises it.
- **Layout.** An area's steps live in `test/acceptance/internal/steps/<area>`,
  beside its `features/<area>/` directory, and its `Register` adds them to
  every scenario. `test/acceptance/internal/harness` runs the scenarios
  and gives each one a `World`: its servers, database, tokens, answers and
  the settings sections its servers start with. The areas share a
  scenario only through the `World`. No area imports another, and the
  harness imports none; depguard's `acceptance-areas` rule holds both.
  The containers come from `test/deps`, which the tests beside the code
  share and which imports nothing of the suite (`test-deps`).
- **The binary under test.** The harness builds `cmd/infraweave` once with
  `-cover -covermode=atomic` and runs it as a subprocess with `GOCOVERDIR`
  set to `cover/accept`, so `infraweave serve`, `scheduler`, `launcher` and `runner` all
  contribute coverage to the merge. In-process servers are not used for
  acceptance: they would skip flag parsing, config decoding and signal
  handling.
- **Isolation.** Each scenario gets a fresh database (template copy),
  object storage of its own (an `fs` root, or with `@objectstore` a RustFS
  bucket its servers reach through the `s3` driver) and its own
  `infraweave all` process on a free
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
| `@terraform-plans` | the same real dependencies as its `@terraform` plan-artifact scenarios | selects the plan-artifact workload into its own required partition; it never replaces the actual dependency tags |
| `@tofu` | a pinned OpenTofu release, checked the same way | a real run (below) |
| `@terragrunt` | a pinned Terragrunt release, checked against its signed `SHA256SUMS`, beside `@terraform` or `@tofu` | a Terragrunt stack's run and its units' workspaces |
| `@oidc` | an in-process chi OIDC issuer: discovery, JWKS, token minting with chosen claims | SSO login, OIDC API keys, verifying infraweave's own run tokens from its JWKS (decision 0006) |
| `@vault` | Vault in dev mode, with the Transit engine mounted and a Transit key per scenario | the `vault` KEK provider: rewrapping the data keys to Vault, and servers that run on it ([0013](0013-secrets-at-rest.md)) |
| `@vcs` | Gitea container as the raw Git host, with a repository seeded per scenario; each provider's chi fake sends signed deliveries over HTTP and records received checks, statuses and comments | provider-shaped intake and feedback through the real server; raw Git stays under `sourcetest` (decision 0012) |
| `@vcs-lab` | nothing: the scenario reads the repositories the `vcs-lab` job created from its environment | the same scenarios against the real providers; only the `vcs-lab` job runs them, and every partition excludes the tag |
| `@ai` | an in-process chi server replaying recorded model responses | AI integrations and Intent ([0019](0019-ai-provider-boundary.md)) |
| `@docker` | the host's Docker daemon; the harness pulls the committed public Terraform/OpenTofu and minimal test-support digest pins, retains the shared cache, and mounts the current relay forwarder into the shell-free test-support environment with UID 1983, builds infraweave statically for Linux as the launchers' `launcher.docker.runner_binary`, and starts `infraweave launcher` as a subprocess, waiting on its admin `/startupz`; the launcher and its run containers reach the server through a relay on the host's loopback that records each call and can hold the runners' reports | the worker protocol: a claim, a sealed specification, a container that reports, an offline worker, a lost run and a restarted launcher that keeps its run, with `worker.offline_after` and `run.lost_after` set to seconds (decision 0008); real runs with `@terraform` or `@tofu` |
| `@kubernetes` | a k3s container, testcontainers-go `k3s` module, privileged, with its bundled components disabled so that it pulls no image, joined to the run containers' Docker network; the scenario's kubeconfig names it by its network alias | Kubernetes stacks' runs, and Kubernetes workers (decision 0008) |
| `@pulumi` | with `@docker`, the published Pulumi runner environment at its committed digest, and a host directory per scenario that `launcher.docker.extra_mounts` mounts for a `file://` Pulumi backend | Pulumi stacks' runs: a Pulumi YAML program with no provider, whose configuration and outputs, one of them secret, need no plugin download; `PULUMI_CONFIG_PASSPHRASE` is a write-only stack variable |
| `@aws` | moto's server, serving S3 and CloudFormation, which run containers reach through `AWS_ENDPOINT_URL` with dummy credentials | CloudFormation stacks' runs: a template of an `AWS::SSM::Parameter` and an `AWS::S3::Bucket`, a template with a nested stack, and a `before_init` hook that writes the entry template, as `sam package` would |
| `@ansible` | with `@docker`, the published Ansible runner environment at its committed digest | Ansible stacks' runs: a playbook against `localhost` with a local connection |
| `@flows-agent` | a test app built on `pkg/flows/appsdk` as a local OCI image, and an `infraweave flows-agent` subprocess that runs it in Docker | Flows engine, app and agent scenarios ([0018](0018-flows-runtime.md)) |

- **A real run.** `@terraform` and `@tofu` scenarios exercise the full
  path: the server, a private worker pool, `infraweave launcher` as a host
  subprocess talking to the Docker daemon, and the published runner image
  at its committed digest
  ([0020](0020-reference-deployments-and-packaging.md)). Most scenarios
  configure their vendor's pinned environment as the
  launcher's default; vendor-default scenarios leave all image settings
  absent and read the actual run container's image through Docker while the
  relay holds its report, then release it and assert the run finishes. The launcher downloads and verifies the stack's tool release
  itself, as in production, into a `launcher.cache_dir` the suite's
  launchers share, so each release is fetched once per suite run. The
  workspace arrives as a local-preview upload, or from Gitea when the
  scenario is also `@vcs`. Configurations
  use `terraform_data` and other built-ins, so no provider is downloaded;
  a scenario that needs a provider uses a filesystem mirror of pinned
  provider archives checked by SHA-256. State goes through infraweave's
  HTTP state backend (decision 0010) into RustFS. The scenario asserts the
  run's state transitions against report 08's state machine, the stored
  state version, the masked log, and the events emitted
  ([0015](0015-events-notifications-webhooks-audit.md)).
- **The state backend before the runner.** `state/backend.feature`
  (`@postgres`, `@objectstore`, `@vcs` and `@docker`, with `@terraform` or
  `@tofu`) proves the backend before the runner executes phases: the
  harness acts as the worker over `/worker/v1`, claims the run, opens its
  sealed specification with the pool key, checks out the run's commit from
  Gitea and reports each phase, and runs the pinned `terraform` or `tofu`
  binary in its vendor's published runner image at its committed digest,
  with the backend file and
  credentials the agent writes ([0010](0010-state-backend.md)). The relay
  can hold the backend's `UNLOCK` calls, so a lock stays held while a
  second process asks for it.
- **A public runner image.** `runs/runner_image.feature` (`@docker`,
  `@terraform`) names Spacelift's public `runner-terraform` image, pinned
  by digest in `test/deps/images.go`, as a stack's runner image, and runs a
  tracked run on it unchanged (decision 0008's image contract).
- **Images and tools.** Every container image the suite pulls is pinned by
  digest in `test/deps/images.go`, and the Terraform, OpenTofu, Terragrunt
  and kubectl releases by the SHA-256 of each Linux archive or binary in
  `test/deps/tools.go`; `deps-refresh.yml` moves the pins to the newest
  release. k3s is pinned at its newest release tag, which
  `deps-refresh.yml` reads from k3s's releases, since the image's `latest`
  tag trails them by more than a year
  ([33 section 1](../reference/33-vendor-tool-documents.md#1-versions)).
  The runner environments use the same committed immutable GHCR digest pins
  as product launchers. Their build inputs live in `deploy/images/versions.json`;
  `runner-images-upstream.yml` discovers stable tool releases daily and refreshes
  the base/packages weekly, through required-CI input PRs. A successful signed
  publication opens a separate consumer-pin PR. Candidate image jobs build once
  per platform and run real tool contracts; ordinary suites reuse released
  images and remove only owned containers. An archive is downloaded once into
  the user's cache directory and checked against its pin each time a test
  binary uses it.
- **The newest releases everywhere.** A scenario's stack sets no version,
  so its runs take the newest release the recorded release indexes list,
  and `TestUnitToolPins` holds each pin to its index's newest;
  `deps-refresh.yml` records the indexes whenever it moves a pin. A real
  launcher downloads its runs' tool from the suite's tool mirror, which
  serves the pinned archive with the release's `SHA256SUMS` and its
  signature, so the launcher verifies it against the vendor's key as it
  would a download from the vendor, and no scenario downloads a release.
- **Stress.** `INFRAWEAVE_ACCEPT_STRESS` puts a run of the suite under
  what a busy or badly synchronised host does to a run: a comma-separated
  list of `refuse=<share>`, `latency=<duration>` and `cpus=<CPUs>`. Docker
  Desktop runs every container in one virtual machine, whose clock no one
  container can skew, and the Go binaries ignore `libfaketime`, so the
  harness makes the stress itself:
  - an `@objectstore` scenario's servers reach the object store through a
    store relay on the host's loopback, which the URLs they sign name. It
    holds each request for the latency, refuses the share of signed
    uploads, and apart from them of the launcher's signed reads of a saved
    workspace or a local preview's archive, with the
    `403 RequestTimeTooSkewed` a store whose clock differs from the
    signer's answers, spread evenly: uploads from the fifth, and reads,
    one or two a scenario, from the first, while a scenario's first
    upload may be a step's own, which nothing signs afresh. It records
    each call into a failed scenario's evidence, `store-calls.json`. A
    log or a profile a step reads through a URL the API signed for it
    passes, since only its client could ask for that URL again. A run
    that passes leaves no other trace of its refusals, so every stressed
    run writes
    `store-refusals.json` beside the evidence: each scenario's count of
    the uploads and reads the relay could refuse and refused, and their
    total, which the run prints and the `stress` job's summary reports;
  - each run container, any container labelled `io.infraweave.run`, is
    capped at the CPUs with `docker update` as the daemon creates it, and
    each process mode runs with `GOMAXPROCS` at the CPUs rounded up.

  No step's assertion or `Eventually` budget changes under stress, so a
  budget too short for a busy host fails as a finding. Each failure is
  triaged as a product defect or a harness assumption, with its evidence.
  `make accept-stress PARTITION=runs` runs a partition under the stress,
  by default `refuse=0.2,latency=500ms,cpus=0.5`. Stress is a further run
  of a partition, not a partition of its own.

### Docker: skip locally, never in CI

- Without a reachable Docker daemon, the harness skips every scenario that
  carries a dependency tag other than `@oidc` and `@ai`, and prints the
  tags it skipped. Untagged, `@oidc` and `@ai` scenarios still run.
- CI sets `INFRAWEAVE_ACCEPT_REQUIRE_DOCKER=1`. With it set, an unreachable
  daemon fails `TestAccFeatures` before the first scenario, and any skipped
  scenario fails the run, so a CI pass always means every scenario ran.

### Parity checks against the specification reference

`specref` gains a `parity` subcommand, run by `make verify-parity` and by
an acceptance scenario in `api/reference_spec.feature`. For every operation in
`api/reference/spacelift/spacelift.openapi.yaml`, and in the Flows reference
`api/reference/spacelift-flows/flows.openapi.yaml`
([0018](0018-flows-runtime.md)), it decides one outcome:

| Outcome | Condition |
|---|---|
| served | `api/api.openapi.yaml` has the same `operationId` with the same method and path |
| served with divergence | the same `operationId`, with each difference listed in `x-infraweave-divergence` |
| superseded | listed in report 26 section 7, under the rule of [0021](0021-capability-scope-and-supersession.md): the `operations` member of its record in [supersessions.json](../../data/supersessions.json) names it |
| missing | none of the above |

It fails when a served operation differs from the reference without a
recorded divergence, when a superseded operation is served, or when an
operation is both. "Missing" is reported, not failed: it is the backlog.

A reference operation's served match has its `operationId`, or the name
decision [0001](0001-rest-first-spacelift-surface.md)'s rename table
([renames.json](../../data/renames.json)) gives it. The comparison covers the
method, the route (path parameter names aside) and the query, header and
cookie parameters, with whether each is required. A difference is recorded
by its name in `x-infraweave-divergence`: `method`, `path` or
`parameter:<name>`. The rename table records a rename's `operationId` and
path itself. Decision [0003](0003-api-conventions.md)'s conventions
(`cursor-pagination`, `idempotency-key`, `rate-limit`) record the parameters
they add, so an operation that differs only by them is served. Any other
recorded name, such as a response-shape change, makes the operation served
with divergence. A recorded `method`, `path` or `parameter:` name that the
operation does not differ in fails as stale. Operations with
`x-upstream: none` or the `Organization` tag are infraweave-only, outside
the parity surface.

### Behavioural coverage of operations

A scenario names the operations it exercises with tags
`@op:<operationId>`, by the reference's `operationId` or the one
infraweave serves, and its kind with `@case:success`, `@case:denial` or
`@case:failure`. A Terraform provider scenario also names its record with
`@provider:<record id>`, such as `@provider:resource:spacelift_stack`.
`specref parity --states` combines the parity outcome
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
operations it calls are the reference's `terraform-operations.json`, or the
record's `calls` where the provider calls a query another file declares. The
register's states stay hand-maintained (report 30, PD-12);
`specref parity --states --check` fails when a recorded state claims more
than the computed one, so the register cannot claim more than the tests
show. A recorded `superseded` is a scope decision, not a claim the tests
could show, so it never claims more. The scope behind the states is
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
one `cli/<area>.feature` per row, driving the built CLI binary.

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
- `expect.yaml` may also assert recorded `response.json` fields through an
  `observed` JSON-Pointer map. These checks run offline and cannot claim an
  infraweave endpoint is implemented. While that endpoint is pending,
  `replayAfter` links its implementation issue; deferred captures require
  nonempty observations and cannot supply REST calls. Remove the deferral and
  add the real REST calls when the linked implementation lands.
- `TestAccCaptures` verifies the recorded policy facts before starting Docker,
  then replays enabled REST expectations against a live server. Deferred REST
  cases are explicit skips naming the implementation issue, counted separately
  from successful replays. A failed enabled expectation is a parity bug.
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
| Waiting on asynchronous work polls a condition with a deadline, through one helper (`deps.Eventually`), never a fixed delay | review; the helper reports what it waited for |
| Unit tests use virtual time | `internal/clock` and `testing/synctest` |
| An acceptance scenario reaches a server's timer without waiting for it: time passes for one run, worker, stack's state versions or upload by moving its timestamps back, and for a signed URL by signing its token again with an earlier expiry, and the periodic task that reads them falls due | review; the runs and state steps age a run's, worker's, stack's versions' or upload's rows, and sign a URL's token again under the scenario's keyring, since a real server's clock cannot be moved |
| A stopped server's state locks are seeded directly | `state/force_unlock.feature` seeds lock rows: a live run releases its locks when its terminal event is read, so no live path leaves one for a task to break; the former holder's writable phase is arranged to reach the broken-lock checks |
| No test depends on order | `-shuffle=on`; godog runs scenarios in random order with a printed seed |
| No shared mutable state across scenarios | per-scenario database, bucket prefix and server |
| No network beyond the containers | Terraform, OpenTofu and providers come from checksummed caches; Gitea serves raw Git; provider fakes send signed HTTP deliveries and record feedback |
| A failing scenario leaves evidence | the harness writes the server's and launcher's logs and the database dump of that scenario to `test/acceptance/out/<scenario>/`, uploaded as a CI artefact on failure |

A test that fails intermittently is quarantined only by a tracking issue
and a `@quarantine` tag, which the gate still runs and reports; the
coverage it would contribute is not waived.

### Coverage

- Unit coverage comes from each OS job; acceptance coverage from every
  acceptance partition. `make cover` merges them with `go tool covdata` and
  enforces `.testcoverage.yml`: 95% in total, 90% per package.
- Exclusions cover `cmd/`, `tools/` and generated code only: the strict
  servers under `internal/platform/gen/`, the worker protocol client in
  `internal/agent/wire/`, `pkg/client/` and the `App` stubs in
  `pkg/flows/app/`. Adding one needs an amendment to this record.
- Unreachable defensive branches are removed, not excluded.

### CI

| Job | Runner | Runs | Timeout | Required |
|---|---|---|---|---|
| `unit` | `ubuntu-latest`, `macos-latest`, `windows-latest` | `make test` | 15 min | yes |
| `accept-core` | `ubuntu-latest` | untagged, `@postgres`, `@objectstore`, `@oidc`, `@vault` | 20 min | yes |
| `accept-vendors` | `ubuntu-latest` | `@pulumi`, `@aws`, `@kubernetes`, `@ansible` | 30 min | yes |
| `accept-terraform-plans` | `ubuntu-latest` | `@terraform-plans` plan artifact/sanitization scenarios, also carrying `@terraform` and their actual dependency tags | 30 min | yes |
| `accept-terraform` | `ubuntu-latest` | remaining `@terraform` | 30 min | yes |
| `accept-runs` | `ubuntu-latest` | remaining `@docker`, `@tofu`, `@terragrunt`, `@flows-agent`; `@kubernetes` until `accept-vendors` is implemented | 30 min | yes |
| `accept-integrations` | `ubuntu-latest` | `@vcs`, `@ai` | 20 min | yes |
| `provider-parity` | `ubuntu-latest` | `specref provider-parity` and `TestAccProvider` | 30 min | yes |
| `parity` | `ubuntu-latest` | `make verify-parity` and the state check | 5 min | yes |
| `fips-only` | `ubuntu-latest` | `make test` and every acceptance partition, with the binary under test built with `GOFIPS140=certified` and every process run with `GODEBUG=fips140=only` ([0013](0013-secrets-at-rest.md#fips-140-3)) | 45 min | yes |
| `console` | `ubuntu-latest` | `npm ci`, `tsc --strict`, ESLint, Vitest with its coverage thresholds and the production build in `web/console/` ([0023](0023-console.md)) | 15 min | yes |
| `console-e2e` | `ubuntu-latest` | the Playwright journeys against a real `infraweave all` built with `-cover` ([0023](0023-console.md)) | 30 min | yes |
| `coverage` | `ubuntu-latest` | `make cover` over every job above except `fips-only` and `console`, whose TypeScript coverage has its own thresholds | 10 min | yes |
| `postgres-next` | `ubuntu-latest` | the `sqltest` suites, `queuetest` and `accept-core` on the next PostgreSQL major, its beta or release candidate until it is generally available ([0005](0005-persistence-postgresql.md#engine-and-versions)), selected by `INFRAWEAVE_POSTGRES=next` | 20 min | no |
| `nightly` | `ubuntu-latest`, scheduled | the full suite on the newest Terraform, OpenTofu, Terragrunt and kubectl releases, with `-pulumi` and `-ansible` built on the newest Pulumi and ansible-core releases | 60 min | no |
| `stress` | `ubuntu-latest`, scheduled weekly and on demand | `make accept-stress PARTITION=runs`, reporting the relay's refusals and uploading a failed scenario's evidence | 90 min | no |
| `objectstore-cloud` | `ubuntu-latest`, scheduled nightly | `objectstoretest` against real Amazon S3, Google Cloud Storage and Azure Blob Storage | 20 min | no |
| `vendors-upstream` | `ubuntu-latest`, scheduled nightly | `indextest` against HashiCorp's, OpenTofu's, Terragrunt's and kubectl's real release indexes, which every pull request runs against fixtures recorded from them | 15 min | no |
| `vcs-lab` | `ubuntu-latest`, scheduled nightly | `sourcetest` and the `@vcs-lab` scenarios against GitHub, GitLab, Bitbucket Cloud, Bitbucket Data Center and Azure DevOps repositories created for the run and destroyed after it | 45 min | no |

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

- **The VCS lab job.**
  - **Repositories.** A Terraform root per provider under
    `test/terraform/vcs/` creates, for each run, two private repositories
    named `infraweave-lab-<Unix time>-<run>`. A script pushes one fixed
    history to each: a base commit no ref points at, an annotated tag,
    `main`'s head and a second branch. Every date in it is fixed, so its
    commit hashes are constants the tests compare against. The
    integration reaches both repositories; a run's token must reach only
    the first. GitHub, Bitbucket Cloud and Azure DevOps are the hosted
    services. Two providers run as containers the job starts:
    - **Bitbucket Data Center** has no hosted service, so the job starts
      Atlassian's image only when a licence is configured; otherwise it is
      optional and skipped, with the reason in the summary.
    - **GitLab.com** offers group and project access tokens only on paid
      tiers, so the job starts GitLab Community Edition, which offers
      them on every tier.

    The job destroys everything when the run ends, pass or fail. Before
    applying, it deletes any hosted lab repository more than a day old,
    reading the age from the name, since Azure DevOps records none.
  - **Credentials.** None of these providers federates an OIDC token that
    can create repositories. The job's credentials are therefore
    environment secrets of a `vcs-lab` environment that only `main` may
    use, each reaching only its lab, which holds nothing else:
    - an owner's token on a test GitHub organization, and a GitHub App
      the organization owns, installed on all its repositories and registered
      as the lab account's GitHub App integration;
    - an Atlassian account's API token on a Bitbucket Cloud test workspace;
    - a personal access token on an Azure DevOps test project;
    - the Bitbucket Data Center licence. The job makes the containers'
      administrator credentials and certificates for each run.
  - **Delivery paths (D21).** GitLab CE and optional Bitbucket Data Center
    deliver directly from their job containers. GitHub, Bitbucket Cloud
    and Azure DevOps use a short-lived, account-free `cloudflared` quick
    tunnel, opened before the server starts and closed when the job ends.
    Its URL is the server's public URL for that run. The latest release
    is pinned by checksum or digest and refreshed with the other tools.
    A tunnel that cannot open skips the affected provider and the summary
    names why. Repository hooks on the first repository use the
    integration's URL, secret and consumed event list. For GitHub, the job
    sets the lab App's hook through `PATCH /app/hook/config` with its JWT
    and restores it afterward.
  - **Lab reach.** The lab App asks for checks, pull requests, issues and
    deployments write and organization members read. It subscribes to
    `push`, `pull_request`, `pull_request_review`, `issue_comment`,
    `check_run`, `repository` and `organization`; installation events
    arrive without subscription. Bitbucket Cloud's token also has pull
    request and repository write scopes. The lab setup documents these
    one-time requirements without publishing account, organization or
    cloud account IDs.
  - **What it covers.** That the fakes still match the real services:
    - each provider's `Source`, through `sourcetest`;
    - a private stack through its integration, in the `@vcs-lab`
      scenarios;
    - the reach of each run's source credential, which a minted token
      gives to its own repository alone and a lent token gives to both;
    - from P12b, real signed pushes to the tracked and another branch,
      tracked commit movement, no extra run from opening a pull request,
      an individual check updated in place, and a stored pull request
      comment that starts nothing;
    - plan comments only under `feature:add_plan_pr_comment`, once per
      run, with the earlier GitHub comment minimized. Buttons remain a
      capture and deployments are proved by fakes.

    A provider whose secrets are unset is skipped, and the run's
    summary names the secrets it lacks, so a failed run is a provider
    that broke.
  - **Failures.** As the cloud object store job: an issue per provider.

- A scenario's partition is the first of `accept-terraform-plans`, `accept-terraform-vcs`, `accept-terraform`, `accept-runs`,
  `accept-integrations` and `accept-core` that lists one of its tags, so a
  `@postgres` `@terraform` scenario runs once, in `accept-terraform`.
  The planned `accept-vendors` precedes them when its vendors are
  implemented: a `@docker` `@pulumi` scenario belongs there.
  `TestUnitFeaturePartitions` fails when a scenario carries an unlisted
  tag. `TestUnitPartitionSelections` uses Godog's actual parser on every
  expanded scenario to prove exactly one owning job, no missing cases,
  and conjunction with focus tags. Comma alternatives distribute their
  exclusions and focus conditions into every arm.
  `TestUnitTerraformSplitCoverage` proves the disjoint union of the three
  Terraform jobs equals the complete original `@terraform` selection, using
  actual expanded scenarios rather than a fixed count. The plan-artifact
  workload's tag belongs only on existing Terraform-owned sanitization cases;
  protocol-only fence scenarios keep their integration partition.
  `@terraform-vcs` selects existing Terraform-owned native provider workflow
  cases; it retains their actual dependency tags and does not move OpenTofu-only
  cases out of their existing run partition.
  `TestUnitCIPartitionsRunAndCollectEveryRegisteredWorkload` checks the harness
  registry against required/nightly/stress matrices and the aggregate artifact
  gates, so adding a selector without a required job cannot silently lose work.
- Every required job is in the `needs` of the `Quality gate` job and, when
  it produces coverage, of `coverage`.
- When a partition approaches its timeout, it splits by tag into two
  required jobs; scenarios are never dropped to fit.
  The Terraform plan-artifact workload, native VCS workflows, other Terraform cases and remaining
  worker run cases each retain a ten-minute Go test deadline and thirty-minute
  Actions job budget. The Go deadline includes test cleanup: each partition
  needs margin for container teardown and coverage/report flushing after its
  scenario assertions finish. Their profiles and Cucumber results all join the
  same gates. Nightly acceptance and weekly/requested stress include all four
  workloads; FIPS continues to run the complete suite under its existing
  thirty-minute Go test deadline and forty-five-minute Actions job budget.
  Required acceptance also writes an incremental `out/progress.jsonl` when
  `INFRAWEAVE_ACCEPT_PROGRESS=1`. Scenario and step identities, elapsed times,
  run IDs and states survive a process timeout independently of the final
  Cucumber report. Request bodies, step text, policy source and raw errors are
  excluded; a suite-end record follows normal test and dependency cleanup.


### Policy phase evidence

Capture prerequisites block only the behaviors they settle; research may start
ahead of a phase's product gate. plan P13's table includes the policy portion of
C9, C46, C48, C50, C51, C52, C56 and C291. Recorded C49/C54/C57/C192 verify offline
now and name their REST implementation handoffs. A synthetic response cannot
resolve an observed behavior gate. P13 requires the existing real-provider
vcs-lab walkthrough on GitHub, GitLab, Bitbucket Cloud and Azure DevOps; Bitbucket
Data Center is optional only without a licence. Real tools/PostgreSQL/storage
and every provider fake also remain required. P13b follows P13 and RBAC #235;
its shared-runtime changes rerun affected lab scenarios. Each ticket keeps fixes
in one open PR through acceptance.

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
    does not need, at a larger image size, and its image needs a licence
    token to start, which no public pull request has. The KEK suite's
    KMS, and CloudFormation runs' S3 and CloudFormation, run on moto.
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
- **Hosted VCS providers nightly, not per pull request.** They need
  credentials, which pull requests from forks do not receive, and their
  availability and rate limits would make the blocking gate flaky. Gitea
  serves raw Git, while each provider's chi fake sends signed deliveries
  over HTTP and records received feedback in every pull request; the nightly `vcs-lab` job proves the
  fakes against repositories Terraform creates, so a provider that changes
  its API opens an issue within a day.
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
- The `nightly`, `stress`, `postgres-next`, `objectstore-cloud`,
  `vendors-upstream` and `vcs-lab` jobs are informational: a newer Terraform, OpenTofu or
  PostgreSQL release, a real cloud or VCS provider, or a release index that
  changes its format, that breaks a case opens an issue rather than
  blocking merges.
- The cloud job needs one lab account or project per cloud, its OIDC trust
  and its bucket, which the maintainer provisions; until they exist the job
  skips that cloud, and its summary says so. The VCS lab job
  likewise needs each hosted provider's test organization or workspace, the
  GitHub App, which the maintainer provides once. Bitbucket Data Center
  is optional and runs only when a licence is configured.
- Under `GODEBUG=fips140=only`, a test helper or library that reaches for a
  refused algorithm fails the `fips-only` job like product code does; the
  fix is an approved algorithm, not an exemption.

## Verification

- `test/acceptance/features/vcs/webhooks.feature` and
  `test/acceptance/features/vcs/feedback.feature` verify durable intake,
  push-created runs, checks and plan comments with each provider's fake.
  Their `@vcs-lab` examples verify signed deliveries and native feedback
  against the configured real providers in the `vcs-lab` workflow. Those
  examples belong only to the lab partition; Bitbucket Data Center is
  skipped, with a named reason, when no licence is configured.
- `TestUnitTestNames`, `TestUnitFeaturePartitions` and
  `TestUnitCaptureRedaction` guard the rules above.
- `test/acceptance/features/suite/harness.feature` proves the harness
  itself: a scenario skipped without Docker, a failure with
  `INFRAWEAVE_ACCEPT_REQUIRE_DOCKER=1`, per-scenario database isolation,
  and evidence written on failure.
- `test/acceptance/features/runs/terraform.feature` and
  `runs/tofu.feature` carry the first real-run scenarios, and
  `runs/runner_image.feature` a public runner image's.
  `runs/lifecycle.feature` takes every run type and action through its
  history and its `run_updated` events, and reaches the timers, the
  reap and the preparing timeout by moving time on for one run or
  worker; `TestAccCaptures` skips the six captures it stands in for as
  awaited.
- `test/acceptance/features/state/` holds the state management features:
  `backend.feature`, `import.feature`, `rollback.feature` and
  `external_access.feature`, each with real Terraform and OpenTofu;
  `urls.feature`, with no tool, which moves time on for a signed URL by
  signing its token again with an earlier expiry and for an upload by
  moving its row back; `retention.feature`, with real Terraform, which
  moves time on for a stack's state versions as the runs steps do for a
  run; and `terragrunt_units.feature`, with real Terragrunt and pinned
  Terraform and OpenTofu: all units with a workspace per unit, or the
  project root's unit alone in the default workspace. It checks runtime
  mode overrides, the omitted-key default, malformed runtime refusals and
  a missing root unit; single-unit plans use a saved-plan fallback when
  the pinned Terragrunt writes no JSON directory outside run-all.
  Its dependent-unit outlines use `network` and `app`, with `app` reading
  `network`'s output. Mocks allow `init`, `plan` and `validate`: explicit
  initialization needs mocks before the dependency has outputs. Planning
  reaches two additions without a cross-unit `show`, and confirmation
  applies both units with real dependency outputs, two state versions and
  no recorded stack outputs until P20. A commit whose mocks allow `init`
  and `validate`, but not `plan`, fails planning with Terragrunt's
  missing-outputs diagnostic and writes no state version. Both outlines
  run on Terraform and OpenTofu.
  `vendors/terragrunt.feature` holds a Terragrunt stack's
  settings and runs. `TestAccCaptures` skips C106, C107, C108, C109, C110,
  C112, C113 and C114 as awaited against them.
- `test/acceptance/features/vendors/` holds each vendor's runs:
  `pulumi.feature`, `cloudformation.feature`, `kubernetes.feature` and
  `ansible.feature`, each a tracked run with its changes, plan policy input
  and outputs, a proposed run, a task and, but for Ansible, a destroy run;
  and `sanitization.feature`, each vendor's plan policy input compared
  through `sanitized`. `TestAccCaptures` skips C32, C185, C188, C189,
  C295, C296, C297, C299 and C300 as awaited against them and
  `vendors/terragrunt.feature`. C192 is recorded; its offline hash check runs
  now, and REST simulation replay awaits #663.
- `TestUnitStoreRelay`, `TestUnitStoreRelayLatency`,
  `TestUnitWriteRefusals` and `TestUnitParseStress`
  (`test/acceptance/internal/harness`) prove the stress: a refusal share
  of 1 refuses every signed upload, and every signed read of a saved
  workspace or an uploaded archive, with a skewed store's `403`, a share
  of 0 refuses none and 0.2 every fifth upload from the fifth and every
  fifth read from the first, each counted, unsigned requests and a signed read of a log pass with their
  `Host`, the latency holds each request, and the counts land in
  `store-refusals.json` with their total.
- `make verify-parity` and the state check run in the `parity` job; the
  existing `api/reference_spec.feature` and `api/bundle.feature` keep running
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
