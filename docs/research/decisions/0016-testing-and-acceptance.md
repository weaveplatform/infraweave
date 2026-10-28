# 0016: Testing and acceptance against real dependencies

Status: Proposed

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
| `internal/store/storetest` | PostgreSQL at the version floor decision 0005 sets, and at the newest major | `TestAcc` |
| `internal/objectstore/objectstoretest` | MinIO (S3-compatible) and the local-disk backend | `TestAcc` for MinIO, `TestUnit` for local disk |
| `internal/queue/queuetest` | the work queue of decision 0011 on PostgreSQL | `TestAcc` |

Each PostgreSQL case gets its own database, created with
`CREATE DATABASE … TEMPLATE iw_template` from a template the suite migrates
once per container, so cases run in parallel without sharing rows.

### Acceptance harness

- **Runner.** `TestAccFeatures` runs godog over
  `test/acceptance/features/<area>_<topic>.feature` with `Strict: true`.
  Steps that drive the API use the generated client in `pkg/client`.
- **The binary under test.** The harness builds `cmd/infraweave` once with
  `-cover -covermode=atomic` and runs it as a subprocess with `GOCOVERDIR`
  set to `cover/accept`, so `infraweave serve`, `scheduler`, `worker` and `worker exec` all
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
| `@objectstore` | MinIO container | state, logs, uploads, module and provider archives |
| `@terraform` | a pinned Terraform release, fetched once and checked against its SHA-256 | a real run (below) |
| `@tofu` | a pinned OpenTofu release, checked the same way | a real run (below) |
| `@oidc` | an in-process chi OIDC issuer: discovery, JWKS, token minting with chosen claims | SSO login, OIDC API keys, verifying infraweave's own run tokens from its JWKS (decision 0006) |
| `@vcs` | Gitea container, with a repository seeded per scenario | real webhooks, commit statuses, pull request comments (decision 0012) |
| `@ai` | an in-process chi server replaying recorded model responses | AI integrations and Intent ([0019](0019-ai-provider-boundary.md)) |
| `@kubernetes` | a k3s container, testcontainers-go `k3s` module | Kubernetes workers (decision 0008) |
| `@flows-runtime` | the Flows runtime image built from the repository's definition, and an `infraweave flows-agent` subprocess | Flows engine, app and agent scenarios ([0018](0018-flows-runtime.md)) |

- **A real run.** `@terraform` and `@tofu` scenarios exercise the full
  path: the server, a private worker pool, `infraweave worker` as a host
  subprocess talking to the Docker daemon, and the runner image built by
  the harness from the repository's image definition
  ([0020](0020-reference-deployments-and-packaging.md)) and tagged
  `infraweave-runner:acceptance`. The workspace arrives as a local-preview
  upload, or from Gitea when the scenario is also `@vcs`. Configurations
  use `terraform_data` and other built-ins, so no provider is downloaded;
  a scenario that needs a provider uses a filesystem mirror of pinned
  provider archives checked by SHA-256. State goes through infraweave's
  HTTP state backend (decision 0010) into MinIO. The scenario asserts the
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
`cover/accept/cucumber.json`) and computes report 26's state per tag and
per Terraform row:

| State | Computed when |
|---|---|
| `implemented` | every operation is served, answers something other than 501, and is named by at least three passing scenarios: one success path, one denial (403 under decision 0007) and one failure path |
| `partial` | some operations meet the `implemented` condition |
| `stub` | every operation is routed but answers 501 |
| `missing` | at least one operation has no route |
| `superseded` | as in the parity table |

A Terraform resource or data source is `implemented` only when the
operations it calls are and its provider scenario (below) passes. Report
26's tables stay hand-maintained (report 30, P12);
`specref parity --states --check docs/research/reference/26-scope-and-tiers.md`
fails when a state cell disagrees with the computed one, so the report
cannot claim more than the tests show. The scope behind the states is
[0021](0021-capability-scope-and-supersession.md)'s.

### The Terraform provider parity oracle

Two checks, both against infraweave's own provider (decision 0004 owns
where it lives):

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
| `accept-runs` | `ubuntu-latest` | `@terraform`, `@tofu`, `@kubernetes`, `@flows-runtime` | 30 min | yes |
| `accept-integrations` | `ubuntu-latest` | `@vcs`, `@ai` | 20 min | yes |
| `provider-parity` | `ubuntu-latest` | `specref provider-parity` and `TestAccProvider` | 30 min | yes |
| `parity` | `ubuntu-latest` | `make verify-parity` and the state check | 5 min | yes |
| `coverage` | `ubuntu-latest` | `make cover` over every job above | 10 min | yes |
| `nightly` | `ubuntu-latest`, scheduled | the full suite on the newest PostgreSQL major and the newest Terraform and OpenTofu releases | 60 min | no |

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
  is what users run; config strictness, role selection, migrations at
  start and signal handling are part of the contract. Go's `-cover` build
  makes subprocess coverage count toward the gate.
- **A real run in the suite.** Runs are infraweave's core behaviour. A fake
  launcher would prove the protocol to itself; a real launcher, runner
  image and Terraform binary prove what a user sees, including masking and
  state upload.
- **Template databases.** A fresh database per scenario costs one
  `CREATE DATABASE … TEMPLATE`, far less than migrating each time, and
  removes cross-scenario interference without serialising the suite.
- **`@op:` tags for states.** Report 26's states would otherwise be a
  claim; tying them to passing scenarios makes the state column a test
  result, and the three-scenario rule stops a happy-path test from
  certifying an operation whose denials and failures are untested.
- **Rejected: sqlmock or an in-memory store for acceptance.** They accept
  SQL PostgreSQL rejects and miss locking behaviour the queue and state
  locks depend on.
- **Rejected: LocalStack for object storage.** MinIO is the S3-compatible
  store Spacelift's on-prem guide uses in its examples, and it is smaller.
  The same guide warns that upstream development of the open-source MinIO
  server has stopped
  ([24 section 6](../reference/24-self-hosted-reference.md#6-the-four-reference-deployments-compared)),
  so the digest pin can stop moving.
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
- The `nightly` job is informational: a newer PostgreSQL, Terraform or
  OpenTofu release that breaks a scenario opens an issue rather than
  blocking merges.

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
- The `Quality gate` job lists every required job above in its `needs`.

## References

- [01 section 12](../reference/01-spacelift-landscape.md#12-the-specification-reference-as-parity-oracle): the parity oracle.
- [05 section 7](../reference/05-terraform-provider.md#7-later-provider-parity-in-deployci): provider parity.
- [06 section 8](../reference/06-spacectl-and-clients.md#8-what-an-infraweave-cli-must-cover): CLI capabilities.
- [26 section 1.2](../reference/26-scope-and-tiers.md#12-availability-states): availability states.
- [30](../reference/30-open-questions.md#captures-needed): captures.
- <https://github.com/cucumber/godog>
- <https://github.com/testcontainers/testcontainers-go>
- <https://pkg.go.dev/testing/synctest>
