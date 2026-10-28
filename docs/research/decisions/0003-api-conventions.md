# 0003: Idempotency keys, rate limits and cursor pagination

Status: Accepted

## Context

infraweave's served API (`api/deploy.openapi.yaml`) is scaffolded family by
family from the Spacelift reference ([0001](0001-rest-first-spacelift-surface.md)).
The reference inherits three gaps from GraphQL. Each would be a breaking
change to retrofit once clients exist:

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

### Idempotency keys

- **Header.** `POST` and `PATCH` operations accept an optional
  `Idempotency-Key` request header
  ([draft-ietf-httpapi-idempotency-key-header](https://datatracker.ietf.org/doc/draft-ietf-httpapi-idempotency-key-header/)).
  The key is an opaque string of 1–255 characters; clients typically send a
  UUID.
- **Scope and lifetime.** Keys are scoped to the caller: the authenticated
  principal once authentication exists, the client address until then. A key
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
  callers are unaffected. Until authentication exists the switch is keyed by
  client address.

### Cursor pagination

- **Parameters.** Every list operation (a `GET` returning a collection) takes:
  - `limit`: page size, default 20, maximum 100. A value outside 1–100 is
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
  cursor-based.

### Problem types

Each problem `type` is `urn:infraweave:problem:<slug>`. This is the only list
of slugs.

| Slug | Status | Meaning |
|---|---|---|
| `idempotency-key-reuse` | 422 | The key was used with a different request |
| `idempotency-key-in-flight` | 409 | The first request with this key hasn't finished |
| `rate-limited` | 429 | The caller's bucket for this cost class is empty |
| `api-disabled` | 503 | The operator has disabled the API for this caller |
| `invalid-cursor` | 400 | The cursor doesn't decode or belongs to another list |

A URN rather than a URL: RFC 9457 lets clients match on the `type` without
dereferencing it, and infraweave has no documentation host to resolve one to.

### Spec components

`api/src/deploy/components-common.yaml` defines:
- parameters `IdempotencyKey`, `Cursor` and `Limit`;
- headers `RateLimitLimit`, `RateLimitRemaining`, `RateLimitReset`,
  `RetryAfter` and `IdempotencyReplayed`;
- responses `TooManyRequests`, `IdempotencyConflict`, `IdempotencyMismatch`,
  `ApiDisabled` and `InvalidCursor`.

The scaffolder adds them to each operation and records each addition in
`x-infraweave-divergence`.

## Rationale

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
- **Single replica for now.** Keys and buckets live in memory behind
  interfaces until a persistence decision exists. Two replicas would each
  enforce their own budget and keep their own keys.
- **Caller identity is coarse.** Until authentication exists, the caller is
  the client address. Callers behind one NAT share a budget, and the operator
  switch can only target an address.

## Verification

- `api/deploy_test.go` checks that every component named above exists in the
  bundled spec, and that every problem response's example `type` is a slug
  from the table above.
- Server behaviour (replay, 409, 422, 429, the operator switch, cursor
  round-trips) is verified by unit and Gherkin acceptance tests in the
  server's middleware packages (#15).

## References

- [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457): problem details.
- [draft-ietf-httpapi-idempotency-key-header](https://datatracker.ietf.org/doc/draft-ietf-httpapi-idempotency-key-header/).
- [RFC 9110 §10.2.3](https://www.rfc-editor.org/rfc/rfc9110#section-10.2.3): `Retry-After`.
- [02](../02-graphql-to-rest-mapping.md): the reference's list and search shapes.
