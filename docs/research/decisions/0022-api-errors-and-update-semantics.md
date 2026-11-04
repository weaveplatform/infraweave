# 0022: Served API errors, update semantics and input strictness

Status: Accepted

## Context

[0003](0003-api-conventions.md) fixed five problem types, all about retries,
throttling and paging. Every other failure of the served API is still
unspecified, and three product questions in report 30 block the families that
the scaffolder is turning into served routes
([30, product decisions](../reference/30-open-questions.md#product-decisions)):

- **PD-01, errors.** Spacelift answers failures as GraphQL `errors` with no
  documented status codes and no stable codes in `extensions`
  ([01 section 8](../reference/01-spacelift-landscape.md#8-error-behaviour)).
  Its own clients match message text: the Terraform provider treats any read
  error containing `not found` as a deleted resource
  ([05, errors and missing objects](../reference/05-terraform-provider.md#errors-and-missing-objects)),
  and spacectl turns any error containing `unauthorized` into a re-login or
  missing-permission hint
  ([06 section 2.4](../reference/06-spacectl-and-clients.md#24-error-behaviour)).
- **PD-08, updates.** Spacelift's update mutations take a whole input object.
  `stackUpdate` requires `name`, `branch` and `repository`, and whether an
  omitted optional field keeps or resets its value is unverified
  ([07 section 9](../reference/07-stack-model.md#9-typing-traps)). Configuration
  elements have no update mutation at all, and whether `stackConfigAdd` with an
  existing `id` replaces in place is unverified
  ([13 section 1.4](../reference/13-configuration.md#14-operations)). The
  reference maps every `xUpdate` to `PUT`
  ([02, rules](../reference/02-graphql-to-rest-mapping.md#rules)).
- **PD-09, strictness.** Spacelift leaves several inputs loosely specified:
  unknown keys in `.spacelift/config.yml`
  ([07 section 7](../reference/07-stack-model.md#7-runtime-configuration-spaceliftconfigyml)),
  the length and characters of labels
  ([07 section 4](../reference/07-stack-model.md#4-labels)), and cron forms and
  timezone names
  ([18 section 2.6](../reference/18-scheduling-drift-dependencies.md#26-cron-and-timezone)).
  The captures that would settle them (C2, C3, C5, C24, C26, C30, C97, C155,
  C156) are open
  ([30, captures](../reference/30-open-questions.md#captures-needed)).

## Decision

### Errors: a status per class, problem details, Spacelift's words kept (PD-01, option b)

Every error response of the served API is `application/problem+json`
([RFC 9457](https://www.rfc-editor.org/rfc/rfc9457)) using the `Problem` schema
of `api/src/api/components-common.yaml`, with `type`
`urn:infraweave:problem:<slug>`. The status follows the class of the failure,
and the `title` is fixed per slug. Where a ported Spacelift client matches
text, the title carries that text, so the same substring test keeps working
against infraweave.

The problem register, [`docs/data/problems.json`](../../data/problems.json),
is the one complete list of slugs, each with its status, fixed `title`, when
it is sent and the record that defines it: 0003's five and this record's
rest.

Rules that apply to every problem:

- **Matched text.** `not found` and `unauthorized` appear in the register's titles
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
  ([0008](0008-run-execution-and-worker-protocol.md)) uses this register.

`internal/problem` holds the slug registry as Go constants. The problem
register is the source, and a test checks that the two agree. A
handler returns a typed error (`problem.NotFound(...)`,
`problem.InvalidState(state)`), and one chi middleware renders it; any other
error becomes `internal-error` and is logged with its cause.

### Updates: `PUT` replaces, `PATCH` merges (PD-08, option c)

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

### Input strictness (PD-09, a rule per input)

| Input | Rule |
|---|---|
| JSON request bodies | Strict. Input schemas set `additionalProperties: false`; an unknown field answers `400 invalid-request` with reason `unknown-field`. A field Spacelift documents as accepted and ignored (`StackInput.administrative`) stays in the served schema, `deprecated: true`, and is ignored |
| Query parameters | Unknown parameters are ignored; known ones are validated |
| `.infraweave/config.yml` | Known keys are typed strictly: a wrong type fails the run in `INITIALIZING` with the key's path in the log. Unknown keys are reported as warnings in the run log and in `Run.runtimeConfig`, and do not fail the run. The server setting `runs.runtime_config.unknown_keys` (`warn`, the default, or `reject`) makes them fatal for an installation that wants that |
| Labels | 1 to 255 characters of valid UTF-8, no control characters (Unicode category `Cc`), no leading or trailing whitespace. At most 100 labels per entity. A duplicate label in one request is `422 validation-failed`, reason `duplicate`. Labels are compared byte for byte, so `Prod` and `prod` differ. Prefixes with meaning (`autoattach:`, `feature:`, `ghenv:`) obey the same rules |
| Cron expressions | The grammar of [0011](0011-persistent-work-queue.md)'s `internal/cron` parser, which owns it: exactly five whitespace-separated fields (minute 0-59, hour 0-23, day of month 1-31, month 1-12, day of week 0-7, where 0 and 7 are Sunday); each field a comma list of items; an item is `*`, `n` or `a-b`, each optionally followed by `/step` with step at least 1 (`n/step` runs from n to the field maximum). Rejected as `validation-failed`: month and weekday names, `?`, `L`, `W`, `#`, `@` macros, a seconds or year field, out-of-range values, `a-b` with a > b, step 0. When day of month and day of week are both restricted, either matching fires (Vixie cron) |
| Timezones | IANA names resolved by Go's `time.LoadLocation`, with `time/tzdata` embedded so that a host without zone files (the distroless image of [0020](0020-reference-deployments-and-packaging.md)) still resolves them. `LoadLocation` reads `ZONEINFO` and the host's database before the embedded copy, so where a host has one, its tzdata version decides. Names are case-sensitive. Zones and links of the IANA database (`CET`, `US/Pacific`, `Etc/GMT+1`) resolve; abbreviations outside it (`PST`, `BST`) and numeric offsets (`+01:00`) are `validation-failed`. Absent means `UTC`. A timezone beside a one-off timestamp schedule is `validation-failed` |

Request validation runs in two layers: `kin-openapi`'s `openapi3filter` checks
every request against the bundled spec before the handler (giving
`invalid-request`), and handlers apply domain rules (giving
`validation-failed`).

## Rationale

- **Option b for PD-01.** A status per class lets any HTTP client, proxy and
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
- **Both `PUT` and `PATCH` (option c for PD-08).** `PUT` is the reference's
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

- **One register for both records.** 0003's slugs and this record's live in
  one problem register, each record marked with the decision that defines
  it, and `api/deploy_test.go` reads it for every problem example in the
  spec and for the slugs 0003's components must use.
- **Unverified Spacelift behaviour.** C2, C3 and C5 may show that Spacelift
  puts stable codes in `extensions` or answers specific statuses; a stable
  code would become an extension member here, not a change of slug. C24, C26,
  C30, C97, C155 and C156 may show Spacelift is looser than the rules above;
  each is a deliberate departure until then.
- **Spacelift clients still do not work unchanged** (0001). The titles help a
  port, not a re-point.
- **Tier gates.** When a feature answers `feature-disabled` or
  `feature-not-in-tier`, and which limits answer `limit-reached`, is
  decision 0021's.

## Verification

- `internal/problem/problem_test.go` (`TestUnitProblemRegistry`): every slug in the problem register has a
  constant with the same status and title, and every constant is in the register.
- `api/deploy_test.go` (`TestUnitDeployProblemTypes`): every problem example in
  the bundled spec uses a slug from the problem register with its status and title.
- `api/api_test.go`: every input schema sets `additionalProperties: false`;
  every update route has a matching `PATCH` with
  `application/merge-patch+json`; both are recorded in
  `x-infraweave-divergence`.
- Gherkin features under `test/acceptance/features/`:
  - `api_errors.feature`: a missing and a hidden stack both answer `404` with
    title `not found`; no token answers `401` with `WWW-Authenticate`; an
    unknown body field answers `400` with reason `unknown-field` and its
    pointer; a confirm on a finished run answers `409 invalid-state`.
  - `api_updates.feature`: `PUT` without an optional field resets it; `PATCH` keeps
    it; `PATCH` with a field set to `null` resets it to its default; `PUT` with
    `null` sets a nullable field whose default is not null to null; a secret
    survives both when absent; a stale `If-Match` answers `412`;
    `PUT` on a configuration element answers `201` then `200`.
  - `api_strictness.feature`: each rejected cron form and timezone in the table
    answers `422` with the field's pointer; with `ZONEINFO` pointing at an empty
    directory, an IANA name such as `Europe/London` still resolves, from the
    embedded `time/tzdata`; a run whose
    `.infraweave/config.yml` has an unknown key logs a warning and finishes, and
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
- `api/src/api/components-common.yaml`: the `Problem` schema.
