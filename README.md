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

`make parity` decides, for each product's reference operation, decision
0016's outcome: served, served with divergence, superseded or missing. A
served operation that differs from the reference in method, route or
parameters must record each difference in `x-infraweave-divergence`, and a
superseded one must not be served. `api/parity-baseline.<product>.json` is
the computed missing list, by tag. `TestUnitAPIParity` (Deploy) and
`TestUnitFlowsParity` fail on a broken rule, on a missing operation the
baseline does not list and on an entry the bundle no longer needs, so
coverage of the reference only goes up. `make verify-parity` runs the same
check and also fails when `docs/data/capabilities.json` claims a state the
acceptance results do not show.

One document holds Deploy and, from P18c, Flows under `/api/v1/flows/`
([decision 0018](docs/research/decisions/0018-flows-runtime.md#api-and-clients)).
`make generate` turns it into the strict server in
`internal/platform/gen/api` and the client in `pkg/client/api`. Go programs
call the API through `pkg/sdk`, which wraps that client with the API's
conventions: a bearer token, an `Idempotency-Key` on every `POST` and
`PATCH`, retries on `429` and on `503` with `Retry-After`, iterators over
`nextCursor`, and a typed error per problem slug
([decision 0001](docs/research/decisions/0001-rest-first-spacelift-surface.md#sdk-pkgsdk)).
Every acceptance step drives the server through it.

Bundling fails when any of these happens:

- `info.yaml` is missing, or holds keys other than its own;
- a fragment holds anything besides `paths` and `components`;
- a path, `operationId` or component is defined twice (the error names both files);
- the result is not valid OpenAPI 3.0.3.

### Running it

`infraweave serve` serves the API on the API listener under
`/api/v1`, and the probes on the admin listener only
([decision 0017](docs/research/decisions/0017-observability-and-health.md#health)):
`GET /livez` while the process serves HTTP, `GET /startupz` once the schema
is at this binary's version and the keyring has loaded, and `GET /readyz` while PostgreSQL answers and
shutdown has not begun. Add `?verbose` for the checks as JSON.
`infraweave scheduler` serves only the admin listener and runs the work
queue: the workers of each logical queue and the scheduler ticker, which
fires periodic tasks such as `queue.prune` once across every instance
([decision 0011](docs/research/decisions/0011-persistent-work-queue.md)).
Its `GET /readyz` also fails while its listening connection to PostgreSQL
is down. `infraweave all` runs both in one process; on shutdown the HTTP
drain comes first, then the ticker stops, then the workers finish or hand
their jobs back within `queue.shutdown_grace`. Every process mode logs JSON lines to stdout with
`time`, `level`, `msg`, `service`, `mode`, `version` and, inside a request,
`request_id`, and logs one `request` line per API request. A valid
`X-Request-Id` (1 to 128 of `A-Z a-z 0-9 . _ -`) is kept, any other is
replaced by a generated one, and the response echoes it. Secrets, cookies
and authorization headers are logged as `[redacted]`.
Every operation answers a `501` problem until its family is implemented, and
every error, the router's 404 and 405 included, is an
`application/problem+json` body whose `instance` is
`urn:infraweave:request:` and the response's `X-Request-Id`
([decision 0022](docs/research/decisions/0022-api-errors-and-update-semantics.md)).
A request that needs PostgreSQL while it is unreachable answers
`503 service-unavailable` with `Retry-After: 2`, which `pkg/sdk` retries;
any other unexpected failure answers `500`. Routes check no credentials yet:
authentication arrives with
[decision 0006](docs/research/decisions/0006-authentication-and-tokens.md)'s
phase.

| Setting | Flag | Environment | Default |
|---|---|---|---|
| `server.listen` | `--listen` | `INFRAWEAVE_SERVER_LISTEN` | `:8080` |
| `server.admin_listen` | `--admin-listen` | `INFRAWEAVE_SERVER_ADMIN_LISTEN` | `:9090` |
| `server.shutdown.ready_delay` | | `INFRAWEAVE_SERVER_SHUTDOWN_READY_DELAY` | `5s` |
| `server.shutdown.timeout` | `--shutdown-timeout` | `INFRAWEAVE_SERVER_SHUTDOWN_TIMEOUT` | `30s` |
| `server.rate_limit.standard.rate` | | `INFRAWEAVE_SERVER_RATE_LIMIT_STANDARD_RATE` | `10` (per second) |
| `server.rate_limit.standard.burst` | | `INFRAWEAVE_SERVER_RATE_LIMIT_STANDARD_BURST` | `100` |
| `server.rate_limit.expensive.rate` | | `INFRAWEAVE_SERVER_RATE_LIMIT_EXPENSIVE_RATE` | `1` (per second) |
| `server.rate_limit.expensive.burst` | | `INFRAWEAVE_SERVER_RATE_LIMIT_EXPENSIVE_BURST` | `10` |
| `server.rate_limit.disabled_callers` | | `INFRAWEAVE_SERVER_RATE_LIMIT_DISABLED_CALLERS` (comma-separated) | none |
| `server.idempotency.ttl` | | `INFRAWEAVE_SERVER_IDEMPOTENCY_TTL` | `24h` |
| `server.idempotency.lock` | | `INFRAWEAVE_SERVER_IDEMPOTENCY_LOCK` | `5m` |
| `database.url` | | `INFRAWEAVE_DATABASE_URL` | required |
| `secrets.kek.provider` | | `INFRAWEAVE_SECRETS_KEK_PROVIDER` | required by `serve`, `scheduler` and `all`: `local`, `awskms`, `gcpkms`, `azurekv` or `vault` |
| `secrets.kek.local.key_file` | | `INFRAWEAVE_SECRETS_KEK_LOCAL_KEY_FILE` | a file holding the KEK, base64 of 32 bytes; or the KEK itself in `INFRAWEAVE_SECRETS_KEK` |
| `secrets.kek.awskms.key_id`, `.region` | | `INFRAWEAVE_SECRETS_KEK_AWSKMS_KEY_ID` and so on | a KMS key ID, ARN or alias; the SDK's region; credentials from the AWS SDK's default chain |
| `secrets.kek.gcpkms.key_name` | | `INFRAWEAVE_SECRETS_KEK_GCPKMS_KEY_NAME` | a CryptoKey resource name; Application Default Credentials |
| `secrets.kek.azurekv.vault_url`, `.key_name`, `.key_version` | | `INFRAWEAVE_SECRETS_KEK_AZUREKV_VAULT_URL` and so on | an RSA key; the key's current version; `azidentity`'s default chain |
| `secrets.kek.vault.address`, `.mount`, `.key_name` | | `INFRAWEAVE_SECRETS_KEK_VAULT_ADDRESS` and so on | mount `transit`; a Transit key of an AEAD type, such as the default `aes256-gcm96` |
| `secrets.rotation_interval` | | `INFRAWEAVE_SECRETS_ROTATION_INTERVAL` | `8760h` (365 days): the age at which the daily `secrets.rotate` task rotates the data key; `0` never rotates |
| `secrets.kek.vault.auth.method`, `.mount`, `.token_file`, `.role`, `.jwt_file`, `.role_id`, `.secret_id_file` | | `INFRAWEAVE_SECRETS_KEK_VAULT_AUTH_METHOD` and so on | `token`, from `token_file` or `VAULT_TOKEN`; `kubernetes` with `role` and `jwt_file` (`/var/run/secrets/kubernetes.io/serviceaccount/token`); `approle` with `role_id` and `secret_id_file`; the auth mount defaults to the method's name |
| `objectstore.driver` | | `INFRAWEAVE_OBJECTSTORE_DRIVER` | required by `serve`, `scheduler` and `all`: `s3`, `gcs`, `azureblob` or `fs` |
| `objectstore.s3.bucket`, `.endpoint`, `.region`, `.path_style` | | `INFRAWEAVE_OBJECTSTORE_S3_BUCKET` and so on | region `us-east-1`; credentials from the AWS SDK's default chain |
| `objectstore.s3.encryption`, `.kms_key_id` | | `INFRAWEAVE_OBJECTSTORE_S3_ENCRYPTION`, `INFRAWEAVE_OBJECTSTORE_S3_KMS_KEY_ID` | `none` (`s3`, or `kms` with a key) |
| `objectstore.gcs.bucket` | | `INFRAWEAVE_OBJECTSTORE_GCS_BUCKET` | Application Default Credentials |
| `objectstore.azureblob.account_url`, `.container` | | `INFRAWEAVE_OBJECTSTORE_AZUREBLOB_ACCOUNT_URL`, `INFRAWEAVE_OBJECTSTORE_AZUREBLOB_CONTAINER` | `azidentity`'s default chain |
| `objectstore.fs.root`, `.url` | | `INFRAWEAVE_OBJECTSTORE_FS_ROOT`, `INFRAWEAVE_OBJECTSTORE_FS_URL` | a directory every replica shares, and the API listener's public URL for signed URLs |
| `objectstore.buckets.<store>` | | `INFRAWEAVE_OBJECTSTORE_BUCKETS_RUN_LOGS` and so on | the driver's bucket; not with `fs` |
| `objectstore.retention.<store>` | | `INFRAWEAVE_OBJECTSTORE_RETENTION_RUN_LOGS` and so on | `run-logs` and `run-observability` `1440h`, `run-artifacts` `720h`, `workspaces` and `policy-inputs` `168h`, `inbox` `48h`, `uploads` `24h`; `0` keeps objects |
| `objectstore.part_size` | | `INFRAWEAVE_OBJECTSTORE_PART_SIZE` | `16777216` (16 MiB; at least 5 MiB) |
| `objectstore.signed_url_ttl` | | `INFRAWEAVE_OBJECTSTORE_SIGNED_URL_TTL` | `15m` |
| `log.format` | | `INFRAWEAVE_LOG_FORMAT` | `json` (or `text`) |
| `log.level` | `--log-level` | `INFRAWEAVE_LOG_LEVEL` | `info` (`debug`, `warn`, `error`) |

A flag beats the environment, which beats the YAML file named by
`--config`. The file is strict: an unknown key fails startup. Values are
sealed under a keyring of data keys in PostgreSQL, each wrapped by the KEK
([decision 0013](docs/research/decisions/0013-secrets-at-rest.md)); the
first replica on an empty database creates the first key, and a replica
whose KEK does not unwrap the keys refuses to start, naming the key.
`infraweave secrets` runs the key operations and exits:

- `infraweave secrets generate-kek <file>` writes a new local KEK to a file
  that does not exist yet, readable only by its owner.
- `infraweave secrets rotate` makes a new active data key now. Every
  replica reloads its keyring and seals under the new key; a scheduler's
  `secrets.reseal` job reseals every value and object key under it and
  deletes the old key. A second rotation is refused until that is done.
- `infraweave secrets rewrap --to <file>` rewraps every data key from the
  configured KEK to the one a YAML file of `secrets.kek` settings names,
  such as `provider: vault` with a `vault:` mapping, touching no value.
  Restart the replicas with the new settings afterwards.
Objects live under one bucket with a prefix per store, or a store's own
bucket, and each has a row in `blob_objects`: `infraweave` expires them
itself and deletes them only from the `scheduler` role, so `serve`'s
identity needs no delete permission
([decision 0005](docs/research/decisions/0005-persistence-postgresql.md#object-storage)).
On `SIGINT`
or `SIGTERM`, `/readyz` answers 503 at once; the listeners keep accepting
for `server.shutdown.ready_delay`, so load balancers stop routing to the
process, then stop accepting and drain in-flight requests for up to
`server.shutdown.timeout`, and the process exits 0. A second signal exits 1
at once. `SIGHUP` re-reads `log.level`, and no other setting. `infraweave version` prints
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
  flight `409`. A `5xx` is not kept, so a retry runs again. A key still in
  flight after `server.idempotency.lock`, as when its replica crashed, is
  taken over by the next request with it. Searches ignore the key.
- **The operator switch.** A caller in `server.rate_limit.disabled_callers`,
  or in the `api_disabled_callers` table until its `until`, gets `503`
  `api-disabled`, and other callers are unaffected. The list is read at
  startup; a change to the table takes effect on every replica once
  `NOTIFY infraweave_invalidate, 'api_disabled_callers'` announces it.

Until authentication arrives the caller is the client's IP address, so
behind a proxy every client shares the proxy's budget. The buckets, keys
and switch live in PostgreSQL
([decision 0005](docs/research/decisions/0005-persistence-postgresql.md)),
so every replica on one database enforces one budget and sees the same
keys.

## Layout

| Path | What |
|---|---|
| `api/` | OpenAPI documents, embedded for tests and tools by `api/doc.go` |
| `cmd/<binary>/` | Entry points: thin shims over `internal/`, such as `cmd/infraweave`, one cobra file per process mode over `internal/app`; excluded from the coverage gate |
| `internal/` | Project-only packages, placed as [the repo layout](docs/implementation/repo-layout.md) says: the composition root in `internal/app`, shared value types in `internal/kernel`, infrastructure in `internal/platform` (`internal/platform/buildinfo` carries the version stamped in by `make build`), and the libraries behind `tools/` in `internal/devtools` |
| `pkg/` | Public, importable packages: the generated client `pkg/client/api` and the SDK over it, `pkg/sdk` |
| `tools/` | Developer tooling (`tools/specref`, `tools/openapi-bundle`); excluded from the coverage gate |
| `docs/` | Research and decision records |
| `test/acceptance/` | Gherkin features run by godog (`TestAccFeatures`) against the committed reference and the built `infraweave` binary, with PostgreSQL and the object store started by testcontainers from the digests in `images.go`; a failed scenario leaves its logs, configuration and database dump in `test/acceptance/out/<scenario>/` |
| `test/fixtures/captures/` | Redacted exchanges with Spacelift's real API, each with the REST calls that stand for it; `TestAccCaptures` replays them and `TestUnitCaptureRedaction` refuses credentials ([layout](test/fixtures/captures/README.md)) |
| `test/depguard/` | A module of its own that breaks each depguard rule once, for `make lint-fixtures` |

## Make targets

| Target | What |
|---|---|
| `make gate` | Everything CI runs: docs-check, vet, lint, lint-fixtures, test, accept, verify-parity, verify-reference, verify-generate, cover, vuln, build |
| `make docs-check` | Fails on a broken link under `docs/`, a research report without its header or sources, a decision without a valid status or index row, a stale `docs/…` path anywhere in the repository, a register under `docs/data/` that fails its schema or cites a missing ID, a reference spec tag with no record in `docs/data/capabilities.json`, a superseded `operations` entry that is not an operation of its product's reference, a superseded `enumValues` or `fields` entry that is not a value or field of its type in Deploy's GraphQL inventory, or a report with *(unverified)* claims that no record of `docs/data/open-questions.json` lists, or a Go package under `internal/` or `pkg/` without a `doc.go` that has its `# Input and output` and `# Dependencies` sections |
| `make test` | Every `TestUnit…` test in the module, with `-race -shuffle=on`, coverage to `cover/unit` |
| `make accept` | Every `TestAcc…` test: the Gherkin features (godog) and the suites beside the code, such as specref's fetcher tests; coverage, the subprocess's included, and `cucumber.json` to `cover/accept`. `PARTITION=core\|runs\|integrations` runs one CI partition, as the `accept-<partition>` jobs do: `core` runs every `TestAcc…` test and the other two only their scenarios, and each job reports how many scenarios it ran, or that its partition has none yet. Without Docker, scenarios that need it are skipped and listed; `INFRAWEAVE_ACCEPT_REQUIRE_DOCKER=1`, as CI sets, fails them instead. `INFRAWEAVE_POSTGRES=next` runs `@postgres` and the `sqltest` suites on the next PostgreSQL major instead of the floor, as the `postgres-next` job does with `PARTITION=core`; that job reports the version it ran and never blocks a merge. `INFRAWEAVE_ACCEPT_SEED` repeats the printed scenario order. `TestUnitTestNames` fails on a test named neither way ([decision 0016](docs/research/decisions/0016-testing-and-acceptance.md#test-kinds)) |
| `make scaffold TAG=Stacks FAMILY=stacks` | Copies one reference tag (Deploy's, or Flows' with `PRODUCT=flows`) into `api/src/api/paths-<family>.yaml` with the decision 0003 conventions, then bundles |
| `make parity` | Prints, for Deploy and then Flows, each reference operation's outcome (served, served with divergence, superseded, missing) and the infraweave-only operations by tag, and how they differ from `api/parity-baseline.<product>.json` |
| `make verify-parity` | Run by the `parity` job over the merged `cucumber.json` of every partition. Fails, for either product, on an unrecorded divergence, a served superseded operation, a stale baseline, or a `docs/data/capabilities.json` state that `cover/accept/cucumber.json` does not show |
| `make parity-baseline` | Rewrites both `api/parity-baseline.<product>.json` to accept the current parity report, keeping each recorded reason; review the diff |
| `make bundle` | Merges `api/src/<service>/` fragments into `api/<service>.openapi.yaml` |
| `make generate` | Bundles, then generates each strict chi server (`internal/platform/gen/<service>`), Go client (`pkg/client/<service>`) and 501 stubs |
| `make verify-generate` | Fails if the committed bundles or generated code are stale |
| `make reference-fetch` | Refreshes each product's snapshots at its lock's pins (needs network, and `terraform` for Deploy); `PRODUCTS=flows` narrows it to one |
| `make reference` | Regenerates each product's spec and `coverage.md` from its snapshots |
| `make reference-inventory` | Regenerates the GraphQL inventory register (`docs/data/graphql-inventory.json`) and report 29, which summarises it (`docs/research/reference/29-data-model-inventory.md`), from the introspection snapshot, and the Flows SDK register (`docs/data/flows-sdk-inventory.json`) from the SDK's identifiers |
| `make verify-reference` | Fails if either committed reference, report 29 or either inventory register is not exactly what the snapshots generate |
| `make cover` | Merges every `cover/*` directory and enforces `.testcoverage.yml` (≥95% total, ≥90% per package) |
| `make lint` / `make fmt` | golangci-lint with `.golangci.yml` over the whole tree, including depguard's [import rules](docs/implementation/repo-layout.md#depguard-rules) and the `time.Sleep` ban in tests |
| `make lint-fixtures` | Lints the `test/depguard` module, whose packages break each import rule once, and fails unless the findings are exactly `test/depguard/expected.txt` |
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
