# 0022: Served API errors, update semantics and input strictness

Status: Proposed

## Context

[0003](0003-api-conventions.md) fixed five problem types, all about retries,
throttling and paging. Every other failure of the served API is still
unspecified, and three product questions in report 30 block the families that
the scaffolder is turning into served routes
([30, product decisions](../reference/30-open-questions.md#product-decisions)):

- **P1, errors.** Spacelift answers failures as GraphQL `errors` with no
  documented status codes and no stable codes in `extensions`
  ([01 section 8](../reference/01-spacelift-landscape.md#8-error-behaviour)).
  Its own clients match message text: the Terraform provider treats any read
  error containing `not found` as a deleted resource
  ([05, errors and missing objects](../reference/05-terraform-provider.md#errors-and-missing-objects)),
  and spacectl turns any error containing `unauthorized` into a re-login or
  missing-permission hint
  ([06 section 2.4](../reference/06-spacectl-and-clients.md#24-error-behaviour)).
- **P8, updates.** Spacelift's update mutations take a whole input object.
  `stackUpdate` requires `name`, `branch` and `repository`, and whether an
  omitted optional field keeps or resets its value is unverified
  ([07 section 9](../reference/07-stack-model.md#9-typing-traps)). Configuration
  elements have no update mutation at all, and whether `stackConfigAdd` with an
  existing `id` replaces in place is unverified
  ([13 section 1.4](../reference/13-configuration.md#14-operations)). The
  reference maps every `xUpdate` to `PUT`
  ([02, rules](../reference/02-graphql-to-rest-mapping.md#rules)).
- **P9, strictness.** Spacelift leaves several inputs loosely specified:
  unknown keys in `.spacelift/config.yml`
  ([07 section 7](../reference/07-stack-model.md#7-runtime-configuration-spaceliftconfigyml)),
  the length and characters of labels
  ([07 section 4](../reference/07-stack-model.md#4-labels)), and cron forms and
  timezone names
  ([18 section 2.6](../reference/18-scheduling-drift-dependencies.md#26-cron-and-timezone)).
  The captures that would settle them (C2, C3, C5, C24, C26, C30, C97, C155,
  C156) are open
  ([30, platform captures](../reference/30-open-questions.md#platform-api-and-clients-reports-01-to-06),
  [stacks captures](../reference/30-open-questions.md#stacks-runs-and-policies-reports-07-to-09),
  [configuration captures](../reference/30-open-questions.md#configuration-and-state-reports-13-and-16),
  [scheduling captures](../reference/30-open-questions.md#scheduling-drift-and-dependencies-report-18)).

## Decision

### Errors: a status per class, problem details, Spacelift's words kept (P1, option b)

Every error response of the served API is `application/problem+json`
([RFC 9457](https://www.rfc-editor.org/rfc/rfc9457)) using the `Problem` schema
of `api/src/deploy/components-common.yaml`, with `type`
`urn:infraweave:problem:<slug>`. The status follows the class of the failure,
and the `title` is fixed per slug. Where a ported Spacelift client matches
text, the title carries that text, so the same substring test keeps working
against infraweave.

This table extends 0003's. 0003's five slugs keep their status and meaning
unchanged and are repeated here only so the served API has one complete list.

| Slug | Status | Fixed `title` | When |
|---|---|---|---|
| `invalid-request` | 400 | `invalid request` | The request does not match the operation's OpenAPI schema: malformed JSON, a wrong type, a missing required field, an unknown body field, a value outside an `enum`, `pattern`, `minLength` or `maxLength` |
| `invalid-cursor` | 400 | (0003) | 0003 |
| `unauthenticated` | 401 | `unauthorized: authentication required` | No credential, or one that is malformed, expired, revoked or for another audience. Sent with `WWW-Authenticate: Bearer` |
| `forbidden` | 403 | `unauthorized: permission denied` | The caller can read the resource but lacks the action it asked for |
| `feature-disabled` | 403 | `feature disabled` | The operator turned off the `BillingTierFeature` that gates the operation with `features.disabled`, or Flows with `flows.enabled: false` (decision 0021); the body carries the value as `feature`, absent for Flows |
| `installation-unsupported` | 403 | `installation unsupported` | The operation needs a service that only Spacelift operates, such as its billing or usage reporting (decision 0021) |
| `not-found` | 404 | `not found` | The route's resource, or one of its parents, does not exist or is not readable by the caller |
| `method-not-allowed` | 405 | `method not allowed` | The path exists without this method; sent with `Allow` |
| `already-exists` | 409 | `already exists` | A create collides with a unique key, such as a slug in the account |
| `invalid-state` | 409 | `invalid state` | An action that the resource's current state does not allow, such as confirming a run that is not `UNCONFIRMED`; the body carries `state` |
| `resource-locked` | 409 | `resource locked` | The target is locked by someone else, such as a stack locked by another user or another push policy key; the body carries `lockedBy` |
| `intent-project-locked` | 409 | `intent project locked` | Another session holds the Intent project's write lock (decision 0019) |
| `idempotency-key-in-flight` | 409 | (0003) | 0003 |
| `precondition-failed` | 412 | `precondition failed` | `If-Match` names an entity tag that is no longer current |
| `payload-too-large` | 413 | `payload too large` | The body exceeds `api.max_body_bytes` (default 1 MiB) |
| `unsupported-media-type` | 415 | `unsupported media type` | A body that is not `application/json`, or a `PATCH` that is not `application/merge-patch+json` |
| `validation-failed` | 422 | `validation failed` | The request parses against the schema, but a domain rule rejects it: a branch that does not exist, a cron expression outside the grammar below, a space the caller may not place the resource in, a cloud role whose trust test fails |
| `webhook-verification-failed` | 422 | `webhook verification failed` | Setting an enabled audit trail webhook whose verification delivery is not answered with a 2xx (decision 0015) |
| `idempotency-key-reuse` | 422 | (0003) | 0003 |
| `rate-limited` | 429 | (0003) | 0003 |
| `ai-budget-exhausted` | 429 | `ai budget exhausted` | The AI integration's usage budget for the period is spent; sent with `Retry-After` until the budget resets (decision 0019) |
| `internal-error` | 500 | `internal error` | An unexpected failure. The body never carries the cause; `instance` identifies the request |
| `not-implemented` | 501 | `not implemented` | A route that the spec serves but no handler implements yet |
| `upstream-error` | 502 | `upstream error` | A synchronous call to a system infraweave depends on failed: a VCS host, a cloud API, a VCS agent pool with no connected agent |
| `api-disabled` | 503 | (0003) | 0003 |
| `service-unavailable` | 503 | `service unavailable` | infraweave cannot serve the request now: the database is unreachable, the server is shutting down, the request deadline passed. Sent with `Retry-After` |

Rules that apply to every problem:

- **Matched text.** `not found` and `unauthorized` appear in the titles above
  exactly as the provider and spacectl match them. A `validation-failed` caused
  by a failed AWS role trust test starts its `detail` with
  `you need to configure trust relationship`, the text the provider retries on
  ([05, eventual consistency](../reference/05-terraform-provider.md#eventual-consistency-and-validation)).
  No other message text is a contract.
- **Hidden and missing are the same.** A resource the caller cannot read
  answers `404 not-found`, never `403`, so the API does not reveal what
  exists. `403 forbidden` is only for a resource the caller can read. Who can
  read what is [0007](0007-authorization-spaces-and-roles.md)'s.
- **Field errors.** `invalid-request` and `validation-failed` carry an
  `errors` extension member: an array of `{pointer, reason, message}`, where
  `pointer` is a JSON Pointer ([RFC 6901](https://www.rfc-editor.org/rfc/rfc6901))
  into the body (or `#/query/<name>`, `#/path/<name>`, `#/header/<name>`),
  `reason` is one of `required`, `type`, `enum`, `format`, `pattern`,
  `min-length`, `max-length`, `minimum`, `maximum`, `unknown-field`,
  `duplicate`, `not-found`, `rule`, and `message` is for people. Clients
  branch on `reason`, never on `message`.
- **Correlation.** `instance` is `urn:infraweave:request:<request ID>`. The same
  ID is in the `X-Request-Id` response header and in the server's log record;
  the request ID and trace propagation are decision 0017's.
- **Absent relations.** A sub-read of an optional relation that is unset (a
  stack with no worker pool, for example) answers `204 No Content`. `404` on
  such a route always means the parent is missing or hidden.
- **Protocol endpoints differ.** Routes whose client is defined by someone
  else answer what that client expects, not problem details: the Terraform
  `http` state backend ([0010](0010-state-backend.md)), the registry protocol
  (decision 0014), and inbound VCS webhooks
  ([0012](0012-vcs-integration-model.md)). The worker API
  ([0008](0008-run-execution-and-worker-protocol.md)) uses this table.

`internal/problem` holds the slug registry as Go constants. The table above is
the source, and a test checks that the two agree. A
handler returns a typed error (`problem.NotFound(...)`,
`problem.InvalidState(state)`), and one chi middleware renders it; any other
error becomes `internal-error` and is logged with its cause.

### Updates: `PUT` replaces, `PATCH` merges (P8, option c)

Every update route keeps the reference's `PUT` and gains a `PATCH` on the same
path.

| | `PUT /xs/{xId}` | `PATCH /xs/{xId}` |
|---|---|---|
| Body | The whole update input, `application/json` | A JSON Merge Patch ([RFC 7396](https://www.rfc-editor.org/rfc/rfc7396)), `application/merge-patch+json` |
| Absent optional field | Reset to the input's documented default | Kept |
| `null` | Reset to the default; for a nullable field, set to null | Removes the member, as RFC 7396 defines, so the merged input lacks the field and `PUT` resets it to its default. A merge patch cannot set a nullable field to null when its default is not null; `PUT` can |
| Arrays (labels, hooks, globs) | Replaced | Replaced whole; merge patch has no element operations |
| Write-only fields (secrets, private keys, tokens) | Absent means keep; a value replaces | Same |
| Required fields of the input | Must be present | May be absent; checked after the merge |
| `Idempotency-Key` | Ignored, as 0003 says for `PUT` | Accepted, as 0003 says for `PATCH` |
| Success | `200` with the resource | `200` with the resource |

- **One code path.** `PATCH` reads the resource's input view (the read model
  projected onto the update input, write-only fields left out), applies the
  merge patch, and runs the result through the `PUT` handler, so both methods
  validate and persist identically.
- **Lost updates.** Item `GET`, `PUT` and `PATCH` responses carry a strong
  `ETag` derived from the row's version column. `PUT`, `PATCH` and `DELETE`
  accept `If-Match`; a stale tag answers `412 precondition-failed`. Without
  `If-Match`, the last write wins, as in Spacelift.
- **Configuration elements.** `PUT /stacks/{stackId}/config/{configId}` (and the
  context and module equivalents) creates or replaces one element: `201` when
  created, `200` when replaced. The element ID is the variable name or file
  path ([13 section 8](../reference/13-configuration.md#8-typing-traps)), so the
  client already knows it. The reference's `POST /stacks/{stackId}/config` keeps
  the `stackConfigAdd` shape and the same upsert behaviour, answering `201` or
  `200` the same way, so a capture of C97 that shows replacement in place needs
  no change.
- **Recorded divergences.** Each added `PATCH` and item `PUT` is listed in the
  operation's `x-infraweave-divergence`, as 0003 does for its additions.

### Input strictness (P9, a rule per input)

| Input | Rule |
|---|---|
| JSON request bodies | Strict. Input schemas set `additionalProperties: false`; an unknown field answers `400 invalid-request` with reason `unknown-field`. A field Spacelift documents as accepted and ignored (`StackInput.administrative`) stays in the served schema, `deprecated: true`, and is ignored |
| Query parameters | Unknown parameters are ignored; known ones are validated |
| `.spacelift/config.yml` | Known keys are typed strictly: a wrong type fails the run in `INITIALIZING` with the key's path in the log. Unknown keys are reported as warnings in the run log and in `Run.runtimeConfig`, and do not fail the run. The server setting `runs.runtime_config.unknown_keys` (`warn`, the default, or `reject`) makes them fatal for an installation that wants that |
| Labels | 1 to 255 characters of valid UTF-8, no control characters (Unicode category `Cc`), no leading or trailing whitespace. At most 100 labels per entity. A duplicate label in one request is `422 validation-failed`, reason `duplicate`. Labels are compared byte for byte, so `Prod` and `prod` differ. Prefixes with meaning (`autoattach:`, `feature:`, `ghenv:`) obey the same rules |
| Cron expressions | The grammar of [0011](0011-persistent-work-queue.md)'s `internal/cron` parser, which owns it: exactly five whitespace-separated fields (minute 0-59, hour 0-23, day of month 1-31, month 1-12, day of week 0-7, where 0 and 7 are Sunday); each field a comma list of items; an item is `*`, `n` or `a-b`, each optionally followed by `/step` with step at least 1 (`n/step` runs from n to the field maximum). Rejected as `validation-failed`: month and weekday names, `?`, `L`, `W`, `#`, `@` macros, a seconds or year field, out-of-range values, `a-b` with a > b, step 0. When day of month and day of week are both restricted, either matching fires (Vixie cron) |
| Timezones | IANA names resolved by Go's `time.LoadLocation`, with `time/tzdata` embedded so that a host without zone files (the distroless image of [0020](0020-reference-deployments-and-packaging.md)) still resolves them. `LoadLocation` reads `ZONEINFO` and the host's database before the embedded copy, so where a host has one, its tzdata version decides. Names are case-sensitive. Zones and links of the IANA database (`CET`, `US/Pacific`, `Etc/GMT+1`) resolve; abbreviations outside it (`PST`, `BST`) and numeric offsets (`+01:00`) are `validation-failed`. Absent means `UTC`. A timezone beside a one-off timestamp schedule is `validation-failed` |

Request validation runs in two layers: `kin-openapi`'s `openapi3filter` checks
every request against the bundled spec before the handler (giving
`invalid-request`), and handlers apply domain rules (giving
`validation-failed`).

## Rationale

- **Option b for P1.** A status per class lets any HTTP client, proxy and
  retry library behave correctly without parsing a body: retry on `429`, `503`
  and `502`, re-authenticate on `401`, drop the resource on `404`. Keeping
  `not found` and `unauthorized` in the fixed titles costs nothing and keeps
  ported provider and spacectl code working with a one-line transport change.
  Fixed titles rather than free text keep the matched words stable.
- **Rejected: option a without Spacelift's words.** It forces every port to
  find and rewrite each string match before it works at all.
- **Rejected: option c, a GraphQL-style envelope beside the status.** 0001
  rules out a GraphQL facade; an `errors` array would make every client
  handle two error shapes.
- **Rejected: one generic `conflict` slug.** A duplicate slug, a run in the
  wrong state and a locked stack need different client reactions: pick another
  name, refresh and re-decide, or wait for the lock holder. Three slugs make the
  reaction mechanical.
- **`404` for hidden resources.** It matches Spacelift, where an invisible
  entity reads as null
  ([06 section 2.4](../reference/06-spacectl-and-clients.md#24-error-behaviour)),
  and does not leak existence across spaces.
- **400 against 422.** The split follows who can detect the error: the
  schema validator (400) or the domain (422). A client generated from the spec
  never sends a 400 request; a 422 always needs a person or a decision.
- **Both `PUT` and `PATCH` (option c for P8).** `PUT` is the reference's
  route and what a Terraform provider wants: it always sends the whole
  resource, as Spacelift's does
  ([07 section 9](../reference/07-stack-model.md#9-typing-traps)), and full
  replacement makes the server's result independent of what it held before.
  `PATCH` is what a CLI or a curl user wants: change one flag without
  re-sending eleven hook lists. Building `PATCH` on the `PUT` path keeps one
  validator.
- **Absent means default under `PUT`.** The alternative, absent means keep,
  makes `PUT` a partial update and leaves no way to reset a field. Spacelift's
  behaviour here is unverified (C26); if the capture shows it keeps omitted
  fields, ported clients are still safe, because they send every field.
- **Write-only fields are kept when absent.** A client cannot read a secret
  back, so a replace that cleared absent secrets would wipe credentials on
  every update. Spacelift's integration updates make such fields optional for
  the same reason ([11 section 4.2](../reference/11-vcs-integrations.md#42-operations)).
- **Rejected: JSON Patch (RFC 6902).** Index-addressed array edits are fragile
  for labels and hooks, and the operations language is more than the use case
  needs.
- **Strict bodies, lenient queries.** A misspelt body field silently ignored
  writes the wrong state; a misspelt query parameter on a read only returns a
  wider page. Strict bodies also match the repository's strict YAML rule for
  configuration.
- **Warn on unknown runtime-config keys.** The file lives in user repositories,
  is shared by every stack on the repository, and is written against
  Spacelift's evolving key set. Failing every stack's runs because one key is
  newer than infraweave would turn a schema lag into an outage. Typed keys stay
  strict because a wrong type is always a mistake.
- **A wider cron grammar than the template pattern.** Every expression the
  template schema accepts is accepted; the additions (`a-b/n`, lists of ranges)
  are standard and harmless. If Spacelift's API accepts more (C156), an import
  fails loudly with a pointer to the field instead of firing at the wrong time.
- **Departures from Spacelift.** Named for the deviations register: HTTP
  statuses and problem details in place of GraphQL `errors`; `PATCH` routes and
  item `PUT` for configuration elements; `ETag` and `If-Match`; `204` for an
  unset relation; rejection of unknown body fields; label limits Spacelift does
  not document; the cron and timezone rules where Spacelift's are unverified.

## Constraints

- **0003's single list.** 0003 states that its table is the only list of
  slugs, and `api/deploy_test.go` reads that table. Accepting this record
  amends that sentence to name this record's table as the complete list; the
  test then reads this table. Until then, no spec example may use a slug from
  this record.
- **Unverified Spacelift behaviour.** C2, C3 and C5 may show that Spacelift
  puts stable codes in `extensions` or answers specific statuses; a stable
  code would become an extension member here, not a change of slug. C24, C26,
  C30, C97, C155 and C156 may show Spacelift is looser than the rules above;
  each is a deliberate departure until then.
- **Spacelift clients still do not work unchanged** (0001). The titles help a
  port, not a re-point.
- **Tier gates.** When a disabled feature answers `feature-disabled` is
  decision 0021's; a further slug it needs is added to this table.

## Verification

- `internal/problem/problem_test.go` (`TestUnitProblemRegistry`): every slug in this record's table has a
  constant with the same status and title, and every constant is in the table.
- `api/deploy_test.go` (`TestUnitDeployProblemTypes`): every problem example in
  the bundled spec uses a slug from the table with its status.
- `api/deploy_test.go`: every input schema sets `additionalProperties: false`;
  every update route has a matching `PATCH` with
  `application/merge-patch+json`; both are recorded in
  `x-infraweave-divergence`.
- Gherkin features under `test/acceptance/features/`:
  - `api_errors.feature`: a missing and a hidden stack both answer `404` with
    title `not found`; no token answers `401` with `WWW-Authenticate`; an
    unknown body field answers `400` with reason `unknown-field` and its
    pointer; a confirm on a finished run answers `409 invalid-state`.
  - `api_updates.feature`: `PUT` without an optional field resets it; `PATCH` keeps
    it; a secret survives both when absent; a stale `If-Match` answers `412`;
    `PUT` on a configuration element answers `201` then `200`.
  - `api_strictness.feature`: each rejected cron form and timezone in the table
    answers `422` with the field's pointer; a run whose
    `.spacelift/config.yml` has an unknown key logs a warning and finishes, and
    fails under `unknown_keys: reject`.
- `internal/cron` table tests (0011) cover the grammar; this record's features
  only check that the API surfaces its errors.

## References

- [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457): problem details.
- [RFC 7396](https://www.rfc-editor.org/rfc/rfc7396): JSON Merge Patch.
- [RFC 9110 section 13.1.1](https://www.rfc-editor.org/rfc/rfc9110#section-13.1.1): `If-Match`.
- [01 section 8](../reference/01-spacelift-landscape.md#8-error-behaviour),
  [05, errors and missing objects](../reference/05-terraform-provider.md#errors-and-missing-objects),
  [06 section 2.4](../reference/06-spacectl-and-clients.md#24-error-behaviour):
  how Spacelift and its clients handle errors.
- [02, errors](../reference/02-graphql-to-rest-mapping.md#errors): the
  reference's `Problem` responses.
- `api/src/deploy/components-common.yaml`: the `Problem` schema.
