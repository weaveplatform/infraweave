# infraweave

Self-hosted infrastructure-as-code orchestration with the feature set of
Spacelift (stacks, runs, policies, contexts, drift detection, worker pools)
behind a REST API described by OpenAPI 3 instead of GraphQL.

Everything starts from a machine-checked reference of the upstream surface:
`api/reference/spacelift/spacelift.openapi.yaml`. It holds a REST route for
every one of Spacelift's 665 GraphQL root fields, with the official Terraform
provider's attribute descriptions attached. See
[decision 0001](docs/research/decisions/0001-rest-first-spacelift-surface.md)
for why infraweave is REST-first.

## The Spacelift specification reference

| Piece | What |
|---|---|
| `internal/devtools/specref` | Builds one REST reference per Spacelift product. Deploy: parses the GraphQL introspection snapshot, the spacelift-api-bruno collection and the Terraform provider schema and maps root fields to REST routes. Flows: reads the RPC calls of `terraform-provider-flows` and `flowctl` and maps RPC paths to REST routes. Builds and validates each OpenAPI 3.0.3 document |
| `tools/specref` | `fetch` (network; terraform for Deploy); `generate`, `check`, `scaffold`, `inventory` and `parity` (offline). `--product` picks the Spacelift product, `deploy` by default or `flows`, and with it the default `--dir`, parity `--baseline` and inventory outputs |
| `api/reference/spacelift/` | Pinned snapshots, `sources.lock.yaml`, the hand-curated `rest-mapping.yaml`, and the generated spec and `coverage.md` |
| `docs/research/` | Research reports in `reference/` (landscape, mapping rules, provider), decision records in `decisions/`; see its [README](docs/research/README.md) |

Generation fails when any of these happens:

- a root field is neither routed nor excluded with a reason;
- two routes collide;
- a mutation gets no resource shape (`rpcBudget` is 0);
- `rest-mapping.yaml` names something upstream no longer has.

The weekly **OpenAPI specification update** workflow (`openapi-spec-update.yml`)
compares the pinned upstream commits with the newest ones. When either is
newer, it regenerates on a runner and opens a PR.

## The served API

infraweave's own API is authored as fragments in `api/src/api/`:
- `info.yaml` holds the document head;
- `components-*.yaml` and `paths-<family>.yaml` hold only `paths` and `components`.

`make bundle` (`tools/openapi-bundle` over `internal/devtools/openapibundle`)
merges the fragments into `api/api.openapi.yaml`, served under `/api/v1`. The
bundle is committed, and a unit test fails when it is stale.

`make parity` compares the bundle with each product's reference by
`operationId` and route. `api/parity-baseline.<product>.json` accepts the
reference operations the bundle does not serve unchanged: the missing ones by
tag, and each moved one with its reason. `TestUnitAPIParity` (Deploy) and
`TestUnitFlowsParity` fail on a reference operation the baseline does not
accept and on an entry the bundle no longer needs, so coverage of the
reference only goes up.

One document holds Deploy and, from P18c, Flows under `/api/v1/flows/`
([decision 0018](docs/research/decisions/0018-flows-runtime.md#api-and-clients)).
`make generate` turns it into the strict server in
`internal/platform/gen/api` and the client in `pkg/client/api`.

Bundling fails when any of these happens:

- `info.yaml` is missing, or holds keys other than its own;
- a fragment holds anything besides `paths` and `components`;
- a path, `operationId` or component is defined twice (the error names both files);
- the result is not valid OpenAPI 3.0.3.

### Running it

`infraweave serve` serves the API on the API listener under
`/api/v1`, and the liveness probe `GET /livez` on the admin listener only
([decision 0017](docs/research/decisions/0017-observability-and-health.md#health)).
Every operation answers a `501` problem until its family is implemented, and
every error, the router's 404 and 405 included, is an
`application/problem+json` body. Routes check no credentials yet:
authentication arrives with
[decision 0006](docs/research/decisions/0006-authentication-and-tokens.md)'s
phase.

| Setting | Flag | Environment | Default |
|---|---|---|---|
| `server.listen` | `--listen` | `INFRAWEAVE_SERVER_LISTEN` | `:8080` |
| `server.admin_listen` | `--admin-listen` | `INFRAWEAVE_SERVER_ADMIN_LISTEN` | `:9090` |
| `server.shutdown.timeout` | `--shutdown-timeout` | `INFRAWEAVE_SERVER_SHUTDOWN_TIMEOUT` | `30s` |
| `server.rate_limit.standard.rate` | | `INFRAWEAVE_SERVER_RATE_LIMIT_STANDARD_RATE` | `10` (per second) |
| `server.rate_limit.standard.burst` | | `INFRAWEAVE_SERVER_RATE_LIMIT_STANDARD_BURST` | `100` |
| `server.rate_limit.expensive.rate` | | `INFRAWEAVE_SERVER_RATE_LIMIT_EXPENSIVE_RATE` | `1` (per second) |
| `server.rate_limit.expensive.burst` | | `INFRAWEAVE_SERVER_RATE_LIMIT_EXPENSIVE_BURST` | `10` |
| `server.rate_limit.disabled_callers` | | `INFRAWEAVE_SERVER_RATE_LIMIT_DISABLED_CALLERS` (comma-separated) | none |
| `server.idempotency.ttl` | | `INFRAWEAVE_SERVER_IDEMPOTENCY_TTL` | `24h` |

A flag beats the environment, which beats the YAML file named by
`--config`. The file is strict: an unknown key fails startup. On `SIGINT`
or `SIGTERM` the server stops accepting connections and drains in-flight
requests for up to `server.shutdown.timeout`. `infraweave version` prints
the build's version, commit and date.

Every request on the API listener applies
[decision 0003](docs/research/decisions/0003-api-conventions.md)'s
controls, whether its operation is implemented or a stub:

- **Rate limits.** Each caller has a token bucket per cost class: the
  operation's `x-infraweave-cost`, `standard` or `expensive`. A bucket holds
  `burst` requests and refills at `rate` a second. Every response carries
  `X-RateLimit-Limit`, `X-RateLimit-Remaining` and `X-RateLimit-Reset`, and
  an empty bucket answers `429` with `Retry-After`.
- **Idempotency keys.** A `POST` or `PATCH` with an `Idempotency-Key` runs
  once. A repeat replays the first response with `Idempotency-Replayed:
  true`; a key reused for another request answers `422`, one still in
  flight `409`. A `5xx` is not kept, so a retry runs again. Searches ignore
  the key.
- **The operator switch.** A caller in `server.rate_limit.disabled_callers`
  gets `503` `api-disabled`, and other callers are unaffected. The list is
  read at startup.

Until authentication arrives the caller is the client's IP address, so
behind a proxy every client shares the proxy's budget. Buckets and keys
are held in process memory: each replica enforces its own budget and keeps
its own keys, which is right for one replica only. A shared store arrives
with [decision 0005](docs/research/decisions/0005-persistence-postgresql.md)'s
phase.

## Layout

| Path | What |
|---|---|
| `api/` | OpenAPI documents, embedded for tests and tools by `api/doc.go` |
| `cmd/<binary>/` | Entry points: thin shims over `internal/`, such as `cmd/infraweave`, one cobra file per process mode over `internal/app`; excluded from the coverage gate |
| `internal/` | Project-only packages, placed as [the repo layout](docs/implementation/repo-layout.md) says: the composition root in `internal/app`, shared value types in `internal/kernel`, infrastructure in `internal/platform` (`internal/platform/buildinfo` carries the version stamped in by `make build`), and the libraries behind `tools/` in `internal/devtools` |
| `pkg/` | Public, importable packages |
| `tools/` | Developer tooling (`tools/specref`, `tools/openapi-bundle`); excluded from the coverage gate |
| `docs/` | Research and decision records |
| `test/acceptance/` | Gherkin features run by godog against the committed reference (excluded from `make test`) |

## Make targets

| Target | What |
|---|---|
| `make gate` | Everything CI runs: docs-check, vet, lint, test, accept, verify-reference, verify-generate, cover, vuln, build |
| `make docs-check` | Fails on a broken link under `docs/`, a research report without its header or sources, a decision without a valid status or index row, a stale `docs/…` path anywhere in the repository, a register under `docs/data/` that fails its schema or cites a missing ID, a reference spec tag with no record in `docs/data/capabilities.json`, or a report with *(unverified)* claims that no record of `docs/data/open-questions.json` lists, or a Go package under `internal/` or `pkg/` without a `doc.go` that has its `# Input and output` and `# Dependencies` sections |
| `make test` | Unit tests with `-race -shuffle=on`, coverage to `cover/unit` |
| `make accept` | Gherkin acceptance features (godog), coverage to `cover/accept` |
| `make scaffold TAG=Stacks FAMILY=stacks` | Copies one reference tag into `api/src/api/paths-<family>.yaml` with the decision 0003 conventions, then bundles |
| `make parity` | Prints, for Deploy and then Flows, the API's present, moved, missing and infraweave-only operations by reference tag, and how they differ from `api/parity-baseline.<product>.json` |
| `make parity-baseline` | Rewrites both `api/parity-baseline.<product>.json` to accept the current parity report, keeping each recorded reason; review the diff |
| `make bundle` | Merges `api/src/<service>/` fragments into `api/<service>.openapi.yaml` |
| `make generate` | Bundles, then generates each strict chi server (`internal/platform/gen/<service>`), Go client (`pkg/client/<service>`) and 501 stubs |
| `make verify-generate` | Fails if the committed bundles or generated code are stale |
| `make reference-fetch` | Refreshes the Spacelift snapshots at the lock's pins (needs network and `terraform`) |
| `make reference` | Regenerates the spec and `coverage.md` from the snapshots |
| `make reference-inventory` | Regenerates the GraphQL inventory register (`docs/data/graphql-inventory.json`) and report 29, which summarises it (`docs/research/reference/29-data-model-inventory.md`), from the introspection snapshot |
| `make verify-reference` | Fails if the committed reference, report 29 or the GraphQL inventory register is not exactly what the snapshots generate |
| `make cover` | Merges every `cover/*` directory and enforces `.testcoverage.yml` (≥95% total, ≥90% per package) |
| `make lint` / `make fmt` | golangci-lint with `.golangci.yml` |
| `make build` | Cross-compiles every `cmd/*` binary for six platforms, CGO disabled |

## Dependencies stay at latest

| Piece | What it moves | When |
|---|---|---|
| `deps-refresh.yml` | The `go` directive (latest stable Go), every module and every `go.mod` tool, and `.github/golangci-lint-version` | Daily and on demand |
| Dependabot | Go modules and GitHub Actions pins, one grouped PR per ecosystem | Daily |
| `auto-merge.yml` | Merges either kind of PR once the quality gate has passed on that exact commit | When the gate finishes |

## Licence notices

The committed Spacelift reference includes material from MIT-licensed
Spacelift repositories; see `api/reference/spacelift/NOTICE`.
