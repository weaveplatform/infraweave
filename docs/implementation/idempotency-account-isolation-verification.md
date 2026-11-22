# Idempotency account isolation verification

Issue: [#726](https://github.com/weaveplatform/infraweave/issues/726).
Parent: [#636](https://github.com/weaveplatform/infraweave/issues/636).

## Response ownership

Authenticated account requests store replay responses under
`account/<account ID>/<kind>:<actor ID>`. A credential/session identifier is
excluded, so retries remain stable across token renewal, server replicas and
account re-entry. Different actors and accounts have different namespaces.

The full namespace and idempotency key remain the existing PostgreSQL primary
key and sealed-response AAD. Old actor-only rows retain their original AAD and
expire through the existing purge; account requests never fall back to them.
There is no migration or sealed-data rewrite. Rate limits, disabled callers and
audit continue to use the stable actor identity. Account-less/protocol requests
retain their existing caller/address identity and ignored-key controls.

Authorization and feature checks still run before replay. Removing a member's
last readable grant in A returns 404; valid B requests create/replay B's own
policy, even with the method, URI, request bytes and key previously used in A.

## Real public reproduction

At main `40e58d80f89327e19351581061bf02a603440808`, the first account-switch
scenario failed after B's signed member credential recovered A's policy space
`acme`, instead of B's `acme-dev`. Before fixing the namespace it ran 18 steps
(13 passed, one failed, four skipped). After the fix the unchanged scenario
passes all 18 steps.

The initial browser identity cookie and two admissions are fixture seeded, not
a live identity-provider login. Public account selection, RSA-sealed signed CLI
credential issuance, B's role binding, all policy writes and both server
replicas are real. PostgreSQL assertions check each response's owned resource
ID and exact policy counts; status codes alone do not establish isolation.

The expanded public feature passes **5 scenarios / 66 steps** with required
Docker dependencies and certified FIPS. It proves A → B → A, refreshed member
credentials, A's grant removal while B remains authorized, independent member
and API-key actors, refreshed API-key credentials, and exact body/dated
TASK/INITIALIZATION deprecation metadata on replay. The complete API regression
batch passes **60 scenarios / 361 steps**, including in-flight/fingerprint
controls, rate limits and disabled callers.

## Shared persistence controls

The real PostgreSQL test
`TestAccPostgresAccountNamespacesBindSealedResponsesWithoutReinterpretingLegacyRows`
creates legacy, A and B rows under one key, verifies each independently, copies
A's ciphertext into B, observes authenticated-open failure, and verifies A
remains readable. It never substitutes a mock sealer. Existing store contract
coverage checks expiry, takeover, failure release and shared replicas.

Application units, the replay store suite and kernel tests pass with race
detection, randomized order and certified FIPS. Required repository generation,
parity, coverage and full FIPS gates remain enforced in CI.
