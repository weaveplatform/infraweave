# 0017: Observability and health

Status: Proposed

## Context

Operators of a self-hosted platform need logs they can search, traces
that follow a run from the API call to the worker, metrics they can alert
on, and probes an orchestrator can trust. Spacelift's self-hosted product
sets the expectations infraweave's users bring
([24 section 3.6](../reference/24-self-hosted-reference.md#36-observability)):

- every component logs JSON to stdout and stderr;
- traces and metrics go to one vendor chosen by `OBSERVABILITY_VENDOR`
  (Datadog, AWS, OpenTelemetry over OTLP gRPC, or disabled), honoured by
  server, drain and workers;
- probes live on a separate instrumentation port, `8080`, with
  `GET /readiness` on the server and `GET /startup` on the drain
  ([24 section 2.1](../reference/24-self-hosted-reference.md#21-process-inventory));
- workers upload traces, CPU profiles and JSON logs to a run observability
  bucket.

Account-level metrics come from a separate process, `spacelift-promex`,
which answers each Prometheus scrape by querying the GraphQL API and
publishes a fixed set of gauges with defined failure semantics
([19 section 10](../reference/19-events-notifications-audit.md#10-the-prometheus-exporter)).
That exporter cannot talk to infraweave, which serves no GraphQL (decision
[0001](0001-rest-first-spacelift-surface.md)), yet dashboards and alerts
built on its metric names exist.

infraweave's server binary runs as `infraweave serve`, `infraweave
scheduler`, or both in `infraweave all`, and workers run `infraweave worker`
([0020](0020-reference-deployments-and-packaging.md)). Each role depends on
different things, so one probe cannot serve all of them.

## Decision

Telemetry lives in `internal/telemetry` (indicative; decision 0004 owns
placement). It never fails a request, a job or a run.

### Logging

- **Library and format.** `log/slog`, JSON to stdout, one object per line.
  `log.format: text` gives the human-readable handler for local work.
  `log.level` is `debug`, `info` (default), `warn` or `error`, and can be
  changed at runtime through `SIGHUP`, which re-reads only that key.
- **Standard fields** on every line, from a base handler and the request or
  job context:

| Field | Value |
|---|---|
| `time`, `level`, `msg` | slog's own, RFC 3339 with nanoseconds, UTC |
| `service` | `infraweave` |
| `role` | the subcommand: `serve`, `scheduler`, `worker`, `vcs-agent` or `flows-agent`; `serve,scheduler` for `infraweave all` |
| `version` | the build version from `internal/buildinfo` |
| `request_id` | on lines inside an HTTP request |
| `trace_id`, `span_id` | when a span is active |
| `principal` | the authenticated principal's ID, never a name or e-mail |
| `run_id`, `stack_id`, `module_id`, `worker_pool_id`, `job_kind` | when the context carries them |

- **Access log.** One `info` line per request after it completes: `method`,
  `route` (the chi route pattern, not the raw path), `status`, `bytes`,
  `duration_ms`, `remote_addr`, `user_agent`. Request and response bodies
  are never logged, at any level.
- **Request IDs.** The API accepts `X-Request-Id` when it is 1 to 128
  characters of `[A-Za-z0-9._-]`, and otherwise generates a random 128-bit
  ID in base32. The ID is echoed in the `X-Request-Id` response header,
  added to every log line of the request, and recorded as the span
  attribute `infraweave.request_id`. Problem bodies carry it in
  `instance` ([0022](0022-api-errors-and-update-semantics.md)).
- **Redaction.** A wrapping `slog.Handler` applies before any output:

| Rule | Effect |
|---|---|
| A value of a type that holds a secret (the sealed-value types of decision 0013, API key secrets, tokens, private keys) | its `LogValue` returns `[redacted]` (decision 0013); no code path can print the plaintext through slog |
| An attribute whose key, case-folded, is `authorization`, `cookie`, `set-cookie`, `password`, `secret`, `token`, `private_key`, `client_secret`, or ends in `_secret`, `_token` or `_password` | value replaced with `[REDACTED]` |
| A string value that matches the masking set of decision 0013 for the current run (secret environment variables and mounted files) | the matched substrings replaced as run logs replace them |
| Header maps | only an allowlist of headers is logged (`Content-Type`, `User-Agent`, `X-Request-Id`, `Idempotency-Key`) |

  Run logs are product data, stored and masked under decisions 0008 and
  0013; this section covers the process logs only.
- **Rate limiting.** Repeated warnings from one source (an unreachable
  exporter, a failing webhook endpoint) log once per minute per source with
  a `suppressed` count.

### Traces

- **SDK and export.** The OpenTelemetry Go SDK with the OTLP exporter.
  `telemetry.traces.exporter` is `none` (default) or `otlp`;
  `telemetry.otlp.endpoint` and `telemetry.otlp.protocol` (`grpc`, the
  default, or `http/protobuf`) select the collector. When a key is unset,
  the standard `OTEL_EXPORTER_OTLP_*` variables apply, so a Datadog Agent,
  an ADOT collector or Grafana Alloy receives traces without infraweave
  knowing which vendor it is.
- **Resource.** `service.name` is `infraweave-<role>` (or `infraweave` for
  `infraweave all`), plus `service.version`, `service.instance.id` (a
  random ID per process) and `deployment.environment.name` (the
  semantic-convention name that replaces the deprecated
  `deployment.environment`) from `telemetry.environment`.
- **Sampling.** Parent-based with a trace-ID ratio of
  `telemetry.traces.sample_ratio` (default `0.1`). An incoming
  `traceparent` from an API client is recorded as a span link, not used as
  the parent, unless `telemetry.traces.trust_incoming: true`: an
  unauthenticated caller must not decide what is sampled.
- **Spans.**

| Source | Spans |
|---|---|
| HTTP server | one per request, named by the chi route pattern, with the OpenTelemetry HTTP semantic conventions |
| HTTP clients | VCS providers, webhook deliveries, cloud token exchanges, AI providers, the registry's upstreams |
| PostgreSQL | a pgx tracer: one span per query, statement text without arguments |
| Object storage | one per operation, with bucket and key prefix, never the object |
| Work queue | one span per job run; the enqueuing transaction's trace context is stored with the job (decision 0011) and the job span links to it |
| Policy engine | one per evaluation, with policy type and decision outcome (decision 0009) |

- **Propagation into runs.** A run stores the trace context of the request
  or event that created it. The worker protocol (decision 0008) carries it
  as W3C `traceparent` and `tracestate` in the run's assignment. The
  launcher continues the trace with one span per run phase
  (`initializing`, `planning`, `applying` and the rest of report 08's
  phases) and passes `TRACEPARENT` and `TRACESTATE` into the runner
  container's environment, so tools that honour the OpenTelemetry
  environment carrier join the trace. The launcher exports its spans to its
  own OTLP endpoint (`launcher.telemetry.otlp.endpoint`); nothing is relayed
  through the server.

### Metrics

- **API.** Code records metrics through the OpenTelemetry metric API.
  Instrument names follow `infraweave.<area>.<name>`; instruments from
  OpenTelemetry instrumentation libraries keep their semantic-convention
  names (`http.server.request.duration` and the like).
- **Export.** The Prometheus exporter for the OpenTelemetry SDK serves
  `GET /metrics` on the admin listener (`:9090`), so
  `infraweave.queue.oldest_pending` appears as
  `infraweave_queue_oldest_pending_seconds`. `telemetry.metrics.exporter:
  otlp` pushes the same instruments to the OTLP endpoint as well.
- **infraweave's own metrics:**

| Instrument | Kind | Attributes |
|---|---|---|
| `infraweave.runs.transitions` | counter | `run_type`, `from`, `to` |
| `infraweave.runs.duration` | histogram, seconds | `run_type`, `final_state` |
| `infraweave.runs.pending` | gauge | `worker_pool_id` |
| `infraweave.workers.connected` | gauge | `worker_pool_id`, `status` (`idle`, `busy`, `drained`) |
| `infraweave.queue.jobs` | gauge | `kind`, `state` |
| `infraweave.queue.oldest_pending` | gauge, seconds | `kind` |
| `infraweave.queue.runs` | counter | `kind`, `outcome` |
| `infraweave.policy.evaluations` | counter | `type`, `outcome` |
| `infraweave.policy.duration` | histogram, seconds | `type` |
| `infraweave.state.locks` | gauge | none |
| `infraweave.ratelimit.rejections` | counter | `cost_class` |
| `infraweave.db.pool.connections` | gauge | `state` (`acquired`, `idle`, `total`) |
| `infraweave.telemetry.export_failures` | counter | `signal` (`traces`, `metrics`) |
| `infraweave.build.info` | gauge, always 1 | `version`, `commit`, `goversion` |

The records that own an area define its instruments under the same rules:
[0015](0015-events-notifications-webhooks-audit.md) the `infraweave.events.*`
and `infraweave.audit.*` instruments,
[0012](0012-vcs-integration-model.md) `infraweave.vcs.webhook.rejected`, and
[0021](0021-capability-scope-and-supersession.md) `infraweave.features.skipped`.

- **Cardinality.** Attribute values come from code constants or from sets
  the operator bounds (worker pools). Stack, run, user and request IDs are
  never metric attributes; they belong on spans and log lines.

### Spacelift's exporter metrics, built in

- **Endpoint.** `GET /metrics/spacelift` on the admin listener of the `serve`
  role serves every metric `spacelift-promex` publishes, with the same
  names, types, labels and units, listed in
  [19 section 10](../reference/19-events-notifications-audit.md#10-the-prometheus-exporter).
  A separate path keeps account-wide series off the per-process
  `/metrics`, so the operator scrapes them through one Service target
  instead of once per replica.
- **Sources.** Each collector is computed at scrape time from infraweave's
  own tables, by the same queries that answer the REST equivalents of the
  GraphQL fields promex reads:

| Collector | infraweave source |
|---|---|
| `publicworkerpool` | the shared pool of decision 0008 when one is configured; otherwise zeros, as Spacelift's self-hosted exporter reports |
| `workerpools` | worker pools and their registered workers: pending runs, busy workers, workers, drained workers |
| `usage` | the `usage` read: the trailing 30-day window as start and end, run minutes on private and shared pools times 60, and seats in use ([19 section 11](../reference/19-events-notifications-audit.md#11-usage-data-and-usage-export)) |
| `aggregates` | the `metrics` read: stacks by state, resources by drift, average stack size, and average and median run duration in nanoseconds, the unit Spacelift uses despite the name |

- **Configuration**, mirroring promex's flags:
  `telemetry.spacelift_metrics.enabled` (default `true`),
  `.collectors` (default all four), `.scrape_timeout` (default `5s`, one
  deadline for the whole scrape) and `.partial_scrapes` (default `false`).
  Collectors run concurrently.
- **Failure semantics** are promex's: in strict mode any failed collector
  answers HTTP 500 naming it; with `partial_scrapes` the healthy
  collectors' series and every health series are served with 200, and 500
  only when none succeeds. `spacelift_scrape_collector_success`,
  `_supported`, `_duration_seconds`, `spacelift_scrape_duration_seconds`
  and `spacelift_build_info` (carrying infraweave's version) are computed
  the same way. A collector disabled by configuration is absent, not
  unsupported.

### Health

The admin listener (`server.admin_listen`, default `:9090`) serves the
probes. It is never routed through ingress and needs no credentials; the
API listener serves no probe.

| Probe | Answers 200 when | Answers 503 when |
|---|---|---|
| `GET /livez` | the process serves HTTP | never while the process runs; a hung process fails by timeout |
| `GET /startupz` | every startup check of the process's roles has passed once | until then; it never returns to 503 |
| `GET /readyz` | every readiness check of the process's roles passes and shutdown has not begun | otherwise |

Checks per role:

| Role | Startup checks | Readiness checks |
|---|---|---|
| `serve` | config decoded; schema at the version this binary expects (decision 0005); signing keys loaded (decision 0006); object storage bucket reachable | PostgreSQL answers `SELECT 1` through the pool |
| `scheduler` | as `serve` for schema; every queue kind has a registered handler | PostgreSQL answers; the lease loop has completed a poll within twice its interval |
| `worker` | registered with the server with a valid pool token; the container runtime (Docker or Kubernetes) answers | the server answered a poll within three poll intervals; the runtime answers |

- Each check runs with a 2-second timeout, and results are cached for one
  second so that frequent probing does not load the database.
- Object storage is a startup check only. When it fails later, the
  operations that need it fail with their own errors and a degraded entry
  appears in `/readyz?verbose`, but the process stays ready: removing every
  replica from the load balancer would turn a storage outage into a total
  outage.
- `/readyz?verbose` and `/startupz?verbose` answer JSON
  `{"status": "ok"|"fail", "checks": {"<name>": {"status": …, "error": …}}}`.
  Without `verbose`, the body is `ok` or `fail`.

### Shutdown

On `SIGTERM` or `SIGINT` a server process:

1. sets `/readyz` to 503 at once;
2. waits `server.shutdown.ready_delay` (default `5s`) so load balancers
   stop routing to it;
3. stops accepting connections and drains in-flight requests for up to
   `server.shutdown.timeout` (default `30s`). Worker long-polls answer
   "no work" at once, so launchers reconnect to another replica
   (decision 0008);
4. the `scheduler` role stops leasing jobs and hands back the leases it holds
   without counting an attempt (decision 0011);
5. flushes traces and metrics, with a 5-second cap;
6. exits 0. A second signal exits at once with status 1.

The launcher's shutdown, including runs in flight, is decision 0008's; its
`/readyz` answers 503 from the first signal.

### Failure semantics

| Failure | Behaviour |
|---|---|
| OTLP endpoint unreachable or rejecting | spans and metric points are dropped after the batch queue (2048 spans) fills; `infraweave.telemetry.export_failures` counts it; a rate-limited warning is logged; requests are unaffected |
| OTLP misconfigured at start | the process starts with the exporter disabled and logs an error; telemetry never blocks startup |
| A log write fails | ignored; the process does not stop for stdout |
| `/metrics` scrape during load | served from in-memory instruments; never touches the database |
| `/metrics/spacelift` collector fails | promex's semantics, above |
| A readiness check times out | counts as failed for that probe |

## Rationale

- **slog and OpenTelemetry** are the standard library's logger and the
  vendor-neutral telemetry standard; every collector Spacelift's users
  deploy (Datadog Agent, ADOT, Grafana Alloy, the OpenTelemetry Collector)
  accepts OTLP, so one exporter covers them.
- **Redaction by type** is what makes it safe: a deny list of keys alone
  misses a secret logged under an unexpected name, while a sealed type
  whose `LogValue` hides it cannot be printed by accident.
- **Links for untrusted parents.** Honouring any client's `traceparent`
  would let a caller force sampling of every request and pollute traces.
- **Built-in Spacelift metrics** keep existing dashboards and alerts
  working without a second process, and computing them from the same reads
  as the REST API keeps the numbers consistent with what users see.
- **Readiness checks only what every request needs.** PostgreSQL backs
  every API operation; object storage backs some. Failing readiness on a
  partial dependency removes capacity that could still serve.
- **Rejected: a `/health` route on the API listener.** It would be public
  through ingress and invite probing from outside; orchestrators and load
  balancers reach the admin port directly.
- **Rejected: vendor switches like `OBSERVABILITY_VENDOR`.** Each vendor
  SDK adds dependencies and code paths to test; OTLP reaches all of them.
- **Departure from Spacelift:** no `Datadog` or `AWS` observability
  vendor; OTLP is the only export, and Datadog and X-Ray are reached
  through their OTLP-capable agents.
- **Departure from Spacelift:** Spacelift's exporter metrics are served by
  infraweave itself at `/metrics/spacelift`, not by a separate exporter
  process that queries the API with an API key.
- **Departure from Spacelift:** probes are `/livez`, `/readyz` and
  `/startupz` on `:9090`, not `/readiness` and `/startup` on `:8080`; port
  `8080` is infraweave's API listener.
- **Departure from Spacelift:** workers export traces over OTLP to the
  operator's collector instead of uploading traces, CPU profiles and logs
  to a run observability bucket.
- **Departure from Spacelift:** no per-request query logging at `info`;
  the access log records the route pattern, never the body.

## Constraints

- The `usage` collector's window is a trailing 30 days because infraweave
  has no billing period ([0021](0021-capability-scope-and-supersession.md));
  `spacelift_current_billing_period_*` keep their names for compatibility.
- Trace continuity inside the runner depends on the IaC tool honouring
  `TRACEPARENT`; infraweave makes no claim beyond the launcher's spans.
- The admin listener has no authentication; deployments restrict it with
  network policy ([0020](0020-reference-deployments-and-packaging.md)).
- Metric names are a compatibility surface once released: renaming one is
  a breaking change under Conventional Commits.

## Verification

- `TestUnitRedactHandler`: each rule in the redaction table, including a
  sealed value logged under an innocuous key.
- `TestUnitRequestID`: accepted, rejected and generated IDs, echoed in the
  header and present on log lines.
- `TestUnitSpaceliftCollectors`: each collector against fixed store
  contents, strict and partial failure modes, timeout, and the health
  series.
- `TestUnitSpaceliftMetricNames`: the names, types and label sets served
  equal those in promex's collectors at the commit report 19 cites, held as
  a fixture extracted under its MIT licence.
- `test/acceptance/features/observability_health.feature` (`@postgres`
  `@objectstore`): startup ordering; `/readyz` failing when PostgreSQL
  stops and recovering when it returns while `/livez` stays 200; the
  shutdown sequence with a request in flight.
- `test/acceptance/features/observability_metrics.feature` (`@postgres`):
  scrapes `/metrics` and `/metrics/spacelift` after creating pools, stacks
  and runs and checks the values.
- `test/acceptance/features/observability_tracing.feature` (`@postgres`
  `@objectstore` `@terraform`): an in-process OTLP receiver (a chi server
  for OTLP over HTTP) collects spans; a run's API request, job, launcher
  phases and runner environment share one trace ID.
- Each scenario's assertions follow [0016](0016-testing-and-acceptance.md).

## References

- [19 section 10](../reference/19-events-notifications-audit.md#10-the-prometheus-exporter): the exporter, its metrics and failure semantics.
- [24 section 2.1](../reference/24-self-hosted-reference.md#21-process-inventory) and [section 3.6](../reference/24-self-hosted-reference.md#36-observability): Spacelift's processes, probes and telemetry.
- [27 section 11](../reference/27-reference-materials.md#11-spacelifts-own-open-source-pieces): metrics from the public API, readiness on a separate port.
- <https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/>
- <https://opentelemetry.io/docs/specs/otel/context/env-carriers/>
- <https://pkg.go.dev/log/slog>
