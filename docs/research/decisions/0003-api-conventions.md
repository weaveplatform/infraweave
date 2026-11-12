# 0003: API surfaces, idempotency keys, rate limits and cursor pagination

Status: Accepted

## Context

infraweave's served API (`api/api.openapi.yaml`) is scaffolded family by
family from the Spacelift reference ([0001](0001-rest-first-spacelift-surface.md)).
The server answers more than one API: the served document, the operator
API and the worker API, and routes beside them. Controls that differ by
surface leave one surface weaker than the rest, which an attacker who holds
that surface's credential, or a compromised client of it, finds first; and
each difference is one more thing to know when a fault is traced.

The reference also inherits three gaps from GraphQL. Each would be a
breaking change to retrofit once clients exist:

- **No safe retry.** A create that times out can't be retried without risking
  a duplicate stack, run or policy. Terraform providers and CI scripts retry
  on network errors.
- **No visible load contract.** A client can't tell how close it is to being
  throttled, and the operator can't shed one misbehaving caller without
  shedding everyone. Searches, batches and run triggers cost far more than
  reads but would be counted the same.
- **Unbounded lists.** 95 reference `GET` operations return bare arrays, such
  as `GET /stacks`. A large account receives everything at once, and the
  server must load it all to answer.

## Decision

### API surfaces

- **Surfaces.** One API listener serves every surface:

  | Surface | Prefix | Document | Credential |
  |---|---|---|---|
  | The API | `/api/v1` | `api/api.openapi.yaml` | API tokens and browser sessions ([0006](0006-authentication-and-tokens.md)) |
  | The operator API | `/operator/v1` | `api/operator.openapi.yaml` | Operator tokens ([0021](0021-capability-scope-and-supersession.md)) |
  | The worker API | `/worker/v1` | `api/worker.openapi.yaml` | Worker session tokens, and run tokens on the runner's routes ([0008](0008-run-execution-and-worker-protocol.md)) |
  | Routes beside the documents | the break-glass login, the OAuth authorization server, the fs driver's `/blobs/{token}`, the console at `/` ([0023](0023-console.md)) | none | Their own, as the exceptions below say |

  A surface added later, such as the state backend or an inbound webhook,
  joins the same listener behind the same controls, and records here any
  control it departs from.
- **Shared controls.** Every request passes the same chain, in this order,
  whichever surface it reaches:
  1. **Request ID and recovery.** Every answer carries `X-Request-Id`, and a
     panic answers a `500` problem that names the request and nothing else.
  2. **Access log.** Every request is logged, including one a later control
     refuses.
  3. **Authentication.** Each surface has its own authenticator, chosen by
     the path's prefix, and each route admits one surface's credential. A
     credential of another surface is no credential: `401`, problem
     `unauthenticated`, with `WWW-Authenticate: Bearer`.
  4. **The route table.** Every route is an operation of its surface's
     document or an entry beside them with a written reason. A route the
     router serves that the table lacks answers `403`, so a route cannot be
     mounted without deciding its controls.
  5. **Authorization and feature gates**
     ([0007](0007-authorization-spaces-and-roles.md),
     [0021](0021-capability-scope-and-supersession.md)), where the
     deviations below do not exempt the route. A refusal costs no budget.
  6. **The operator switch and rate limits,** keyed by the caller: the
     principal's `<kind>:<actor ID>`, or the client's address on a route
     that takes no credential.
  7. **Idempotency keys** on `POST` and `PATCH`.
  8. **Strict bodies.** Every JSON request body's schema sets
     `additionalProperties: false`, and the request is checked against it
     before the handler: an unknown member, a missing required one or a
     value outside the schema's bounds (`maxLength`, `minimum` and the like)
     answers `400`, problem `invalid-request`, with each fault's pointer
     ([0022](0022-api-errors-and-update-semantics.md)).
  9. **Bounded bodies.** A request body over `server.max_body_bytes`
     (1 MiB by default) answers `413`, problem `payload-too-large`, before
     any of it is buffered.
  10. **Bounded connections.** The listener bounds the time a client takes
      to send its headers and its body, and how long an idle connection is
      kept. A response that is held on purpose, such as the worker API's
      poll, extends its own deadline to its hold.
  11. **Uncacheable answers.** Every answer carries `Cache-Control: no-store`
      and `X-Content-Type-Options: nosniff`: answers carry credentials and a
      caller's own state, which no cache may keep.
  12. **Problem details.** Every error is an RFC 9457 problem whose type is
      in the problem register, so a client of any surface handles errors the
      same way.
- **Bounds in the domains.** A domain bounds what it parses and stores,
  such as labels or a worker's registration, whether or not the document
  also bounds it, so a value that reaches the domain by another path is
  bounded too. Where both bound a value the two numbers match, and the
  domain's constant names the document's.
- **Deviations by intent.** A route departs from a shared control only as
  this table says, and the route table's entry for it says why in the code:

  | Routes | Control | Deviation | Why |
  |---|---|---|---|
  | The worker API | Authorization | No account action decides a route | The worker or the run is its own subject: a session token names one worker of one pool, a run token one run, and the service reads that worker's or run's rows alone |
  | The worker API | Feature gates | No capability gates a route | `private-workers` bounds registration; gating the protocol would strand a running run when an organization's tier drops |
  | The worker API | Idempotency keys | `Idempotency-Key` is ignored | Answers carry session tokens, which a replay from the key store would hand out again; a registration's `jti` refuses a replay; a poll or an exit is safe to repeat; a report repeated one state version behind answers its step again, an upload declared again with the same size and SHA-256 is signed again, and a chunk or artifact already recorded is skipped |
  | The operator API | Authorization | No account action decides a route | Operator tokens alone reach it, and an operator holds no role in any account |
  | Searches (`POST …/search`) | Idempotency keys | `Idempotency-Key` is ignored | A search changes nothing, so a retry is already safe |
  | The break-glass login | Authentication, feature gates | No credential, no capability; each sign-in is an `expensive` draw on the client's address | It is how a credential is obtained, and no tier may lock the break-glass admin out |
  | The OAuth authorization server | Authentication, feature gates, problem details | No credential, no capability; a registration is an `expensive` draw; the token endpoint answers RFC 6749 errors | A client holds no credential until it redeems one, and OAuth clients parse RFC 6749's error shape; the consent operations, which grant, are the API's and gated there |
  | The break-glass login and OAuth | Strict bodies | The handler decodes a form or OAuth body with its own size bound | Their bodies are forms or OAuth's own shapes, which no JSON schema describes |
  | The fs driver's `/blobs/{token}` | Authentication, feature gates, bounded bodies | The signed token is the credential; no capability; a `PUT` is bounded by the object store, not `server.max_body_bytes` | A bearer check would refuse every signed URL; the operation that signed it was gated; objects such as state files exceed any API body |
  | The console's files at `/` | Authentication, feature gates, uncacheable answers | No credential and no capability; hashed assets are cached for a year and `index.html` is `no-cache` | The files are the same for every caller and hold no credential, and a release must reach browsers on their next navigation ([0023](0023-console.md)) |
  | The admin listener | All | A separate listener with no controls | Probes and metrics must answer while the API sheds load; it is never routed through ingress, and deployments restrict it with network policy ([0017](0017-observability-and-health.md)) |

### Idempotency keys

- **Header.** `POST` and `PATCH` operations accept an optional
  `Idempotency-Key` request header
  ([draft-ietf-httpapi-idempotency-key-header](https://datatracker.ietf.org/doc/draft-ietf-httpapi-idempotency-key-header/)).
  The key is an opaque string of 1 to 255 characters; clients typically send a
  UUID.
- **Scope and lifetime.** Keys are scoped to the caller, the authenticated
  principal that decision [0006](0006-authentication-and-tokens.md) defines. A key
  is kept for 24 hours by default; the lifetime is configurable.
- **Fingerprint.** A request is identified by its method, its path with the
  query string, and a SHA-256 of its body.
- **Outcomes for a key already seen:**

  | The earlier request with this key | Response |
  |---|---|
  | finished with the same fingerprint | The stored response (status, `Content-Type`, `Location`, body) is replayed with `Idempotency-Replayed: true` |
  | finished with a different fingerprint | `422`, problem `idempotency-key-reuse` |
  | is still in flight | `409`, problem `idempotency-key-in-flight`; the client retries later |

- **Which responses are stored.**
  - Any response the operation's handler produced with a status below 500 is
    stored, including validation failures: a retry would fail the same way.
  - A `5xx` response is not stored and the key is released, so a retry runs
    the operation again.
  - A request rejected before the handler runs never reaches the store. That
    covers rate limiting, a disabled caller, and the 409 and 422 above.
- **Methods that ignore the key.** `GET`, `HEAD`, `PUT` and `DELETE` are
  idempotent by definition, with the resource ID as the key, and ignore the
  header. So do searches (`POST …/search`): they change nothing, so a retry
  is already safe.
- **Optional.** A client that never sends a key gets exactly the behaviour it
  would without this feature.

### Rate limits

- **Buckets.** Each caller has one token bucket per cost class. The rate and
  burst of each class are configuration, not part of this record.
  - `standard`: reads and ordinary writes.
  - `expensive`: search, batch operations, run triggers and other fan-out
    work, with a smaller budget.
- **Classes in the spec.** Each operation names its class in
  `x-infraweave-cost`. A missing value means `standard`. The scaffolder marks
  searches and `/batch` paths `expensive`; curators mark the rest.
- **Response headers.** Every response carries the state of the bucket that
  the request drew from:

  | Header | Value |
  |---|---|
  | `X-RateLimit-Limit` | The bucket's capacity (its burst) |
  | `X-RateLimit-Remaining` | Whole tokens left after this request |
  | `X-RateLimit-Reset` | Seconds until the bucket is full again |

- **Throttling.** An empty bucket answers `429`, problem `rate-limited`, with
  `Retry-After` in seconds until one token is available.
- **Operator switch.** The operator can disable the API for a given caller.
  That caller gets `503`, problem `api-disabled`, with `Retry-After`. Other
  callers are unaffected. The switch is keyed by the caller of decision
  0006.

### Cursor pagination

- **Parameters.** Every list operation (a `GET` returning a collection) takes:
  - `limit`: page size, default 20, maximum 100. A value outside 1 to 100 is
    clamped, not rejected.
  - `cursor`: an opaque string from a previous page. Absent means the first
    page.
- **Response.** A `<Type>Page` object:
  - `items` holds the page;
  - `nextCursor` is present only when another page exists. Clients stop when
    it is absent. There is no total count, because counting costs as much as
    the list.
- **Queries.** The server reads keyset-style:
  `WHERE (sort_key, id) > (:cursor) ORDER BY sort_key, id LIMIT :limit + 1`.
  The extra row tells it whether `nextCursor` is needed. Every list therefore
  has a stable total order ending in the ID.
- **Cursors.** A cursor is base64url-encoded, versioned JSON holding the last
  row's sort key and ID. The version lets the encoding change without
  breaking clients mid-iteration. A cursor holds a position, not server state,
  so it never expires. A cursor that doesn't decode, or comes from another
  list, answers `400`, problem `invalid-cursor`.
- **Searches.** `POST …/search` keeps the reference's relay-style
  `SearchInput.first` and `after` with `pageInfo`, which is already
  cursor-based. `first` is clamped as `limit` is. `requestedPage` reads
  `FIRST` and `NEXT` forwards from `after`, and `PREVIOUS` and `LAST`
  backwards. A search cursor holds the last row's keys in the search's
  order and names that order, so a cursor read under another order answers
  `400 invalid-cursor`.

### Problem types

Each problem `type` is `urn:infraweave:problem:<slug>`. This record defines
five slugs: `idempotency-key-reuse` (422), `idempotency-key-in-flight` (409),
`rate-limited` (429), `api-disabled` (503) and `invalid-cursor` (400). The
problem register, [`docs/data/problems.json`](../../data/problems.json),
holds them with the rest of
[0022](0022-api-errors-and-update-semantics.md)'s list, each with its
status, title and meaning.

A URN rather than a URL: RFC 9457 lets clients match on the `type` without
dereferencing it, and infraweave has no documentation host to resolve one to.

### Routes in the records

- **A served route is written as its path in the document.** The served
  API's server URL is `/api/v1`, and records write its routes relative to
  it, as the OpenAPI document and the reference do: `PATCH /stacks/{stackId}`.
  The full path appears only where the URL path itself is the point, such as
  mounting the handler or the console's fallback prefixes
  ([0023](0023-console.md)).
- **Flows operations are written in full.** The served document's Flows
  operations sit under `/api/v1/flows/...`
  ([0018](0018-flows-runtime.md)), and records write them with that prefix
  (`GET /api/v1/flows/flows/{flowId}/events/stream`), because a relative
  `/flows/...` reads like the Flows surfaces outside the document.
- **Every other surface keeps its prefix.** Routes of the operator API
  (`/operator/v1/...`, [0021](0021-capability-scope-and-supersession.md)),
  the Flows surfaces outside OpenAPI (the agent WebSocket
  `/flows/agent/v1/connect`, `/flows/mcp`, `/flows/e/...` and
  `/flows/forms/...`), the state backend (`/state/v1/...`), the
  registry protocol, authentication, OAuth, inbound webhooks, the worker
  and VCS agent protocols, and the admin listener's probes
  ([0017](0017-observability-and-health.md)) are written as the full path
  the server answers. A bare path in a record is therefore always a route
  of the served document.

### Spec components

`api/src/api/components-common.yaml` defines:
- parameters `IdempotencyKey`, `Cursor` and `Limit`;
- headers `RateLimitLimit`, `RateLimitRemaining`, `RateLimitReset`,
  `RetryAfter` and `IdempotencyReplayed`;
- responses `TooManyRequests`, `IdempotencyConflict`, `IdempotencyMismatch`,
  `ApiDisabled` and `InvalidCursor`.

The scaffolder adds them to each operation and records each addition in
`x-infraweave-divergence`.

## Rationale

- **One set of controls, deviations by name.** A surface whose only client
  is our own still faces whoever holds its credential: a compromised
  launcher, or a stolen worker session token, sends whatever it likes. The
  same controls on every surface leave none weaker, and one table of
  deviations is where a reviewer looks for the exceptions.
- **Keys, not natural deduplication.** A create has no natural key that
  survives a retry: two stacks may legitimately share a name across spaces.
  Only the client knows whether two requests are the same intent.
- **Rejected: mandatory idempotency keys.** They make every one-off curl and
  script carry ceremony that only automated clients need. An optional key
  lets integrators adopt safety where retries happen.
- **Rejected: a key column on each resource table.** It spreads one concern
  across every family and only covers creates. A single key/value store with
  a TTL covers any `POST` or `PATCH`, including actions such as run triggers
  that create no row.
- **Classes, not per-route limits.** Two classes are enough to stop fan-out
  work from starving reads, and are simple to explain in a header.
  Per-route budgets would be configuration nobody can reason about.
- **`X-RateLimit-*` headers.** Integrators already understand them from
  GitHub and similar APIs. The IETF `RateLimit` fields are still a draft;
  adopting them later is additive.
- **Seconds, not a timestamp, in `X-RateLimit-Reset`.** A delta doesn't depend
  on the client's clock agreeing with the server's.
- **Rejected: offset pagination (`?page=` or `?offset=`).** The database
  reads and discards every skipped row, so page *n* costs O(*n*). Rows
  inserted or deleted between pages shift the window, so items repeat or
  disappear. A keyset cursor is O(`limit`) at any depth and stable under
  writes.
- **An envelope, not a `Link` header.** `nextCursor` in the body is typed in
  the generated client, and is visible in every tool that shows a response
  body. A `Link` header would leave the reference's array shape intact, but
  every client would have to parse a header to page.

## Constraints

- **Departures from the reference.** The envelope replaces the reference's
  arrays, and the extra headers and responses are additions. All are
  recorded per operation in `x-infraweave-divergence`. Parity is matched by
  `operationId` and route, so these don't count as missing operations.
- **Shared stores.** Keys, buckets and the operator switch live behind
  interfaces, and decision [0005](0005-persistence-postgresql.md#the-stores-of-0003)
  stores them in PostgreSQL, so every replica enforces one budget and sees
  one set of keys.
- **The caller is a principal.** The caller is `<kind>:<actor ID>` from
  decision 0006, so all sessions of one API key share one budget, and the
  operator switch targets a principal, not an address. A route that takes
  no credential keys on the client's address.
- **One chain.** The controls live in one router in front of every surface,
  not in each surface's handlers, so a surface cannot be mounted without
  them and a control's change reaches every surface at once.

## Verification

- `internal/app` checks that every operation of the three documents is in
  the route table with its surface's credential, that a route of one
  surface refuses another's credential, and that an unknown member, a
  missing required member and a value over a schema's bound are refused
  before the handler.
- `api/api_test.go` checks that every component named above exists in the
  bundled spec, that every problem response's example `type` is a slug of
  the problem register with its status and title, and that a response uses
  every slug the register gives to this record.
- Server behaviour (replay, 409, 422, 429, the operator switch, cursor
  round-trips) is verified by unit and Gherkin acceptance tests in the
  server's middleware packages (#15).

## References

- [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457): problem details.
- [draft-ietf-httpapi-idempotency-key-header](https://datatracker.ietf.org/doc/draft-ietf-httpapi-idempotency-key-header/).
- [RFC 9110 §10.2.3](https://www.rfc-editor.org/rfc/rfc9110#section-10.2.3): `Retry-After`.
- [02](../reference/02-graphql-to-rest-mapping.md): the reference's list and search shapes.
