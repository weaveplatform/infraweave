# Definition of done

Every pull request passes the gate below, and every phase of the
[implementation plan](plan.md) meets the phase rules before its Build status
row reads Done. The checks are the repository's `make gate` targets and the CI
jobs in `.github/workflows/`; the test rules are
[decision 0016](../research/decisions/0016-testing-and-acceptance.md)'s, and the
package rules are [repo-layout.md](repo-layout.md)'s. `make gate` runs, in
order: `docs-check`, `vet`, `lint`, `test`, `accept`, `verify-reference`,
`verify-generate`, `cover`, `vuln`, `build`. A pull request runs `make gate`
locally before review; CI runs the same targets as separate jobs, and the
`Quality gate` job is the single required status check.

Rows marked "From P1b", "From P1c", "From P24" or "From P24b" name the phase
that adds the check; until then the row is a rule reviewers apply by hand.

## Every pull request

| Gate | Check |
|---|---|
| Docs | `make docs-check` runs `TestUnitDocs` in the docs check package: every relative link under `docs/` resolves, every `reference/NN-*.md` report has `Checked against`, `Research baseline` and `## Sources`, every decision has a valid `Status:` line, and the plan's phases carry `Decisions:`, `**Depends on:**`, `**Tasks**` and `**Exit criteria**`. It also runs inside `make test` on all three operating systems |
| Vet | `make vet`: `go vet ./...`, run by the `unit` job on each operating system |
| Lint | `make lint`: golangci-lint with `.golangci.yml` over the whole tree (`--new=false`), zero issues; the `lint` job pins the linter version from `.github/golangci-lint-version`. `gosec` runs inside it |
| Import boundaries | From P1b: depguard in `.golangci.yml` with the six rules in [repo-layout.md](repo-layout.md#depguard-rules); a forbidden import fails `make lint` |
| Determinism | From P1b: forbidigo rejects `time.Sleep` in `_test.go` files; waiting goes through `acceptance.eventually` or `testing/synctest` |
| Unit | `make test`: unit tests with `-race -shuffle=on -count=1` on `ubuntu-latest`, `macos-latest` and `windows-latest`, coverage written to `cover/unit`. From P1c, it selects `-run '^TestUnit'` across every package |
| Test names | From P1c: `TestUnitTestNames` fails on a top-level `Test` function that is neither `TestUnit…` nor `TestAcc…` |
| Acceptance | `make accept`: the godog features in `test/acceptance/features` on `ubuntu-latest`, coverage written to `cover/accept`. From P1c, `TestAccFeatures` runs the built `cmd/infraweave` as a subprocess against containers started by testcontainers-go, with `INFRAWEAVE_ACCEPT_REQUIRE_DOCKER=1`, split into the `accept-core`, `accept-runs` and `accept-integrations` jobs |
| Feature partitions | From P1c: `TestUnitFeaturePartitions` fails when a scenario carries a tag no acceptance partition lists |
| Contracts | A change to a storage or execution interface runs its contract suite: `sqltest`-backed store and module tests, `queuetest`, `objectstoretest`, `iactest` or `agenttest`, each from the phase that creates its interface |
| Reference drift | `make verify-reference` (CI job `reference`): the committed Spacelift reference, `coverage.md` and report 29 are exactly what the snapshots generate |
| Generated drift | `make verify-generate` (CI job `generated`): bundling, oapi-codegen and stubgen produce no diff in `api/*.openapi.yaml`, the generated servers or `pkg/client` |
| Parity | From P1c: `make verify-parity` (CI job `parity`) decides served, served with divergence, superseded or missing for every reference operation, and fails on an unrecorded divergence or a served superseded operation |
| States | From P1c: `specref parity --states --check` fails when a state cell in [report 26](../research/reference/26-scope-and-tiers.md) claims more than the passing `@op:` scenarios show |
| Captures | From P1c: `TestUnitCaptureRedaction` rejects credentials and non-`example.com` e-mail addresses in `test/fixtures/captures/`; `TestAccCaptures` replays every `expect.yaml` |
| Provider parity | From P24: `specref provider-parity` maps every Spacelift resource, data source and attribute through `api/reference/provider-parity.yaml`, and `TestAccProvider` applies, plans empty, imports and destroys each `test/terraform/<resource>/` configuration |
| CLI | From P24b: one `cli_<area>.feature` per capability row of [report 06](../research/reference/06-spacectl-and-clients.md), driving the built `infraweavectl` |
| Coverage | `make cover` (CI job `coverage`) merges every `cover/*` directory with `go tool covdata` and enforces `.testcoverage.yml`: at least 95% in total and 90% per package |
| Vulnerabilities | `make vuln`: govulncheck at the version pinned in `go.mod`'s tool block reports nothing reachable |
| Build | `make build` cross-compiles every `cmd/*` binary for linux, darwin and windows on amd64 and arm64 with CGO disabled and `-trimpath` |
| Quality gate | The `Quality gate` job succeeds only when every job in its `needs` succeeded; every required job, including each new acceptance partition, joins its `needs` and, when it produces coverage, the `coverage` job's |
| PR title | A Conventional Commits title, checked by `pr-title-validation.yml`, so release-please derives the version and changelog |
| Dependencies | `dependency-review.yml` passes. Actions are pinned by commit SHA with the version in a comment; `go.mod` names the full patch Go release |
| Spec first | A served operation lands as a fragment change (`make scaffold`, then `make generate`) before its handler; the operation keeps the reference's `operationId`, method and path unless a divergence is recorded |
| Divergences | Each difference from the reference is listed in the operation's `x-infraweave-divergence` and has a row in [deviations.md](deviations.md) |
| Capability ownership | A new, moved or deferred capability keeps an owning phase in [capability-roadmap.md](capability-roadmap.md); only evidenced supersession under [0021](../research/decisions/0021-capability-scope-and-supersession.md) removes one |
| Docs | A behaviour change updates the decision record it rests on and the research page it contradicts in the same pull request; a new choice gets a new numbered decision, starting Proposed |

## Every phase

A phase is complete when:

1. Every task in its section of [plan.md](plan.md) is merged, each through a
   pull request that passed the gate above.
2. Its exit criteria hold, and the phase's last pull request links the
   evidence: test names, feature files and the CI run.
3. Every operation the phase claims as implemented meets decision 0016's
   evidence rule: it is served in `api/deploy.openapi.yaml` (or its spec), it
   answers something other than 501, and at least three passing scenarios name
   it with `@op:<operationId>`: one success path, one 403 denial under
   [0007](../research/decisions/0007-authorization-spaces-and-roles.md), and
   one failure path. A Terraform resource or data source is implemented only
   when the operations it calls are and its provider scenario passes.
4. The parity report shrinks by the operations the phase serves, and no
   operation it serves is reported missing.
5. Report 26's state cells change only through the state check, never by hand
   ahead of the tests.
6. Its acceptance features live in `test/acceptance/features/<area>_<topic>.feature`
   and pass in CI with no scenario skipped.
7. Where an exit depends on a capture of report 30, the capture is checked in
   under `test/fixtures/captures/C<n>/` and replays; until then the scenario
   names the decision record it tests against, and the phase notes the open
   capture in its Build status row.
8. Its decision records still describe the code. A Proposed decision the phase
   implements is reviewed with the phase; a change of design amends the record
   in place.
9. Build status in [plan.md](plan.md) matches the GitHub issues: the phase's
   epic and every child issue are closed.

Rules that apply to every change in every phase:

- **Migrations** follow [0005](../research/decisions/0005-persistence-postgresql.md#migrations):
  forward only, `NNNNN_snake_case.sql`, gap-free per module stream, expand
  before contract across releases, large backfills as queue jobs, never in a
  migration.
- **Errors** are RFC 9457 problems with the slugs of
  [0003](../research/decisions/0003-api-conventions.md) and
  [0022](../research/decisions/0022-api-errors-and-update-semantics.md).
- **Secrets** are sealed through `internal/platform/crypt`
  ([0013](../research/decisions/0013-secrets-at-rest.md)); run logs pass
  through `internal/agent/mask`; nothing logs a credential.
- **Configuration** is strict YAML decoded with `UnmarshalExact`, with flag >
  environment > file precedence; only setting flags are bound.
- **HTTP**, including every test server that models an external API, uses chi.
- **Builds** run with `GOWORK=off`.
- **Comments** state what the code does and why, in the present tense, with no
  change history; history belongs in commit messages.
- **Commits and pull request titles** follow Conventional Commits.

## Coverage rules

- 95% in total and 90% per package are floors, measured on the merge of the
  unit runs on all three operating systems and every acceptance partition.
  `.testcoverage.yml` sets them, `make cover` enforces them, and the coverage
  job blocks the merge.
- Acceptance coverage counts: the harness builds `cmd/infraweave` with
  `-cover -covermode=atomic` and runs every role with `GOCOVERDIR` set, so
  `serve`, `scheduler`, `worker` and `worker exec` contribute.
- Exclusions are exactly `cmd/`, `tools/`, generated server code and
  `pkg/client/`; after [P1b](plan.md#p1b-module-layout-and-depguard) the
  generated paths are `internal/platform/gen/` and `internal/agent/wire/`.
  Adding an exclusion needs an amendment to decision 0016.
- Unreachable defensive branches are removed, not excluded.
- Code that needs PostgreSQL or object storage keeps its decisions in plain
  functions with unit tests; its queries are covered by `TestAcc` store,
  module and contract tests on ubuntu, which the merge counts. SQL is never
  mocked.
- Thin `api/` and `module/` packages
  ([0004](../research/decisions/0004-repository-architecture.md#constraints))
  keep their constructors and validation in the package, so each has
  behaviour to test; `module/` packages are covered by `module_test.go`.
- A new package aims for 95% on its own.

## Acceptance budget

Acceptance runs on `ubuntu-latest` only: the macOS runners have no Docker
daemon and the Windows runners cannot run Linux containers. From
[P1c](plan.md#p1c-test-harness-and-parity-tooling) the single `accept` job
splits into the partitions of decision 0016:

| Job | Runs | Timeout |
|---|---|---|
| `accept-core` | untagged, `@postgres`, `@objectstore`, `@oidc` | 20 min |
| `accept-runs` | `@terraform`, `@tofu`, `@kubernetes`, `@flows-runtime` | 30 min |
| `accept-integrations` | `@vcs`, `@ai` | 20 min |
| `provider-parity` | `specref provider-parity` and `TestAccProvider` | 30 min |
| `parity` | `make verify-parity` and the state check | 5 min |
| `coverage` | `make cover` over every job above | 10 min |
| `nightly` (scheduled, not required) | the full suite on the newest PostgreSQL major and the newest Terraform and OpenTofu releases | 60 min |

- **Placement.** A scenario runs in the first of `accept-runs`,
  `accept-integrations` and `accept-core` that lists one of its tags, so it
  runs once.
- **Over budget.** A partition that approaches its timeout splits by tag into
  two required jobs. Scenarios are never dropped, skipped or moved to
  `nightly` to fit.
- **Isolation.** Each scenario gets a fresh database copied from a migrated
  template (`CREATE DATABASE … TEMPLATE iw_template`), a fresh bucket prefix
  and its own `infraweave all` on a free port; containers are shared within a
  test binary, and scenarios run concurrently up to `GOMAXPROCS`. A scenario
  that needs a shared row is wrong, not slow.
- **Real runs.** `@terraform` and `@tofu` scenarios use `terraform_data` and
  other built-ins, or a checksummed filesystem mirror, so no run downloads
  from the network. Every image is pinned by digest in
  `test/acceptance/images.go`.
- **Docker.** Locally, without a reachable Docker daemon, the harness skips
  every scenario with a dependency tag other than `@oidc` and `@ai` and prints
  what it skipped. CI sets `INFRAWEAVE_ACCEPT_REQUIRE_DOCKER=1`, which fails
  the run on an unreachable daemon or any skipped scenario.
- **Flaky tests.** A test that fails intermittently is quarantined only with a
  tracking issue and a `@quarantine` tag; the gate still runs and reports it,
  and its coverage is not waived.
- **Evidence.** A failing scenario writes the server and launcher logs and
  the database dump to `test/acceptance/out/<scenario>/`, uploaded as a CI
  artefact.
- **Nightly.** A newer PostgreSQL, Terraform or OpenTofu release that breaks
  a scenario opens an issue rather than blocking merges.
