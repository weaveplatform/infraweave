# Account authorization scheme switching (#657)

`GET /authorization-scheme` reads the authenticated caller's account;
`POST /account/update-authorization-scheme` accepts the reference's `schema`
argument and requires account administration. A new account uses
`MANAGED_USERS`. The setter retains the shared strict-body, feature, rate-limit
and idempotency controls. An unknown enum is a schema-level `400 invalid-request`.

The account lock serializes changes and session insertion. Inside the
transaction the setter rechecks the exact caller's live session and current
administration, creates its finite `SCHEME_SWITCH` grant, changes strategy,
revokes peer sessions, moves the authorization version and records audit/outbox
evidence. PostgreSQL notifications and local snapshot eviction take effect at
commit. Failure rolls back all these effects. Writing the current strategy is
a no-op. Saved IAM and LOGIN policy configuration is retained in both directions.

The retained grant applies only to that session under either strategy; it
neither extends its lifetime nor transfers to another session of the same key
or user. Its explicit provenance is an infraweave extension to the session grant
enum. As documented in ADR 0007, the preserved caller remains administrative even
if its IAM binding is subsequently removed. This mechanism is an implementation
choice to satisfy caller preservation, not a claim that Spacelift exposes that
grant origin or uses this storage model.

## Evidence and boundaries

[C273](../../test/fixtures/captures/C273/README.md) records the actual upstream
scheme/peer-session behavior and restoration. It does not settle LOGIN policy
admission or the unsupported C50 flag cases. The accepted documented inferences
remain labelled; this change makes no new claim of upstream observation.

- [API acceptance](../../test/acceptance/features/authz/authorization_scheme.feature)
  uses real PostgreSQL, two running infraweave replicas, actual API-token
  exchanges, persisted browser sessions, and two accounts/organizations. It
  checks round trips, peer invalidation, caller administration, non-transfer of
  retained access, concurrent callers, strict bodies, real idempotency replay,
  active grant views and independently hashed inactive configuration rows.
- [Shared controls](../../test/acceptance/features/authz/authorization_scheme_controls.feature)
  prove literal `403` responses for both operations and a `503` read with the
  scenario's actual PostgreSQL stopped. The setter also has a non-admin `403`
  and malformed-request failures.
- [PostgreSQL component acceptance](../../internal/access/internal/adapters/pg/authorization_scheme_test.go)
  holds the real account lock until both callers are blocked, then verifies one
  survives. A second controlled interleaving proves a session committed while
  the switch waited for its share lock is still revoked: the switch uses fresh
  READ COMMITTED reads after that lock, avoiding a stale serializable snapshot.
  It independently checks rollback after a real outbox rejection,
  stale administration, mismatched session identity, revocation, expiry,
  collection and database unavailability. The authorization write-path audit
  includes the new session-grant writes and commit/rollback invalidations.
- [Evaluator checks](../../internal/access/internal/core/authz/authorization_scheme_test.go)
  distinguish active IAM grants, exact retained-session grants, account/actor
  isolation and stack bindings under both schemes. Live IAM binding tests remain
  intact for ordinary managed sessions.

LOGIN admission, policy evaluation and policy-produced session grants remain
#658. In particular this PR does not claim the later root-key admission exception,
policy edits/revocations, account-switcher filtering or the full P13b delivery
gate. #656's P13 delivery gate must pass before this change merges.

The PR records the exact tested revision, native scenario/step counts and CI
links. Full CI retains the 95% total/90% package coverage and certified FIPS gates.
