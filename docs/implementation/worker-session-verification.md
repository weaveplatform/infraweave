# Worker session generation fencing

Implementation: [#766](https://github.com/weaveplatform/infraweave/issues/766).
The contract is recorded in [0006](../research/decisions/0006-authentication-and-tokens.md#token-kinds)
and [0008](../research/decisions/0008-run-execution-and-worker-protocol.md#the-worker-api).

A private pool reset formerly invalidated a session only while the worker row
still held the old certificate serial. A legitimate replacement-key registration
resumed that same worker ID and rewrote its serial, making the old bearer usable
again. The JWT now signs its original positive `iw_worker_serial`; authentication
preserves it through the request, and the worker service compares it with both
the registration and current pool before any mutation. Worker JWTs still have no
session table. OAuth scopes, runner tokens and API-token permissions retain their
separate contracts.

The rollout deliberately rejects legacy worker tokens without a generation.
The existing launcher `401` path re-registers using its persisted worker ID and
pool credential. A current private certificate preserves the ID, run and quota
occupancy; a rotated certificate must be installed before registration can
succeed. Renewals retain the original generation. Retrying the old token after
a lost renewal response remains valid until expiry if the generation is current.

Pool share locks precede worker update locks for registration and every
session mutation. Reset takes the conflicting pool write lock. Registration's
nonce, row change and token issuance commit together; poll admission's liveness,
exited-run handling and renewal also commit together. Failure rolls those
effects back. Claims, delivered signals and deregistration revalidate within
their own short transactions. Held polls retain no transaction and validate
again on timeout, supersession and every other successful response path.

## Local evidence

The original main-based reproduction at
`10bae4145eba95da8c95a91fa00d29d9af7f8f08` failed its last assertion: 23 of
24 steps passed, then an old-token poll returned `200` after replacement-key
resume, where `401` was required. The expanded
[reset feature](../../test/acceptance/features/workers/reset_session.feature)
passes all 55 steps with real services and certified FIPS. It verifies signed
generation propagation, same-ID replacement and same-certificate re-registration, renewal and
response-loss retry, stale poll/deregister refusal, actual stored liveness
readback before and after refusals, unchanged worker count and valid cleanup.

The original `features/workers` selection in partition `core` passes 17
scenarios / 507 steps in certified FIPS. That partition selects pool API and
reset-session scenarios, including billing-enabled private quota,
drain/cycle/search and certificate controls; it excludes the Docker protocol
and execution features. Its PostgreSQL dependency was cleaned up successfully. All 26 PostgreSQL adapter acceptance tests pass
in the same mode. Six new native tests use real transactions to control:

- reset and replacement registration between an unlocked read and claim;
- a standalone stale `Seen` blocked behind an uncommitted reset;
- token-issuer failure rolling back actual liveness changes;
- pool-before-worker lock acquisition and reset serialization;
- failed token issuance rolling back both registration nonce and resume;
- reset waiting for registration's nonce/worker/token transaction.

These fault-boundary tests inject the token issuer or scheduler only at the
controlled failure boundary. The real HTTP feature uses actual registration
proofs and server-issued JWTs. Signed ES256 unit tests additionally reject
missing, null, zero, negative, fractional, string and overflowing generations,
and reject the claim on other token kinds.

Targeted unit regressions pass with race detection, including launcher
re-registration and restart. Local statement coverage is token 97.7%, authn
95.5%, worker core 98.5%, protocol 95.3% and launcher 94.7%. Access HTTP's
unit-only package coverage is 87.1%; its changed authenticator functions are
100%. These targeted results do not replace the merged 95% total / 90% per
package gate or exact-head OS, FIPS and acceptance CI. Configured golangci-lint
passes for all changed packages.

## Public integration

The private fix was rebased onto the public-pool implementation at `8781301d`.
The integrated `features/workers` selection in partition `core` passes 25
scenarios / 664 steps in certified FIPS. Its report contains `pools`,
`public_pool`, `public_registration` and `reset_session`: private reset, pool
API and public credential registration/rotation evidence. It excludes
`public_execution` and `protocol`, so it supplies no Docker or Terraform
execution proof.

Separate nonzero runs against the same production code explicitly select
partition `runs` and each Docker feature:

| Feature | Passed | Execution evidence |
|---|---|---|
| `features/workers/public_execution.feature` | 3 scenarios / 75 steps | Actual launcher, pinned Terraform runner, plan/apply/FINISHED history, log and state version; resolved unlisted-image refusal; billing-enabled Free slot ceiling and waiting-run release |
| `features/workers/protocol.feature` | 3 scenarios / 65 steps | Actual private launcher specification fetch/open and Docker report; OFFLINE/lost-worker behavior; killed/restarted launcher retaining its worker and run before `lost_after` |

Both certified-FIPS reports contain exactly the named feature and all steps
passed; their Docker/PostgreSQL/Gitea/object-store dependencies cleaned up
successfully. The private protocol runner deliberately reports the documented
missing-`sh` failure, so that feature proves launcher/protocol lifecycle rather
than successful Terraform execution. The public feature supplies the successful
Terraform execution proof.

The assembled claim and repoll regressions also pass, including competing
workers across two replicas. The earlier private-only evidence above remains the before/incremental
record; exact-head CI is still required after integration.

The storage fence locks the concrete matching private or public pool row
`FOR SHARE` before the worker row. Public ownership remains SQL `NULL`, and
`PublicShare` still precedes public claims and registration. An additional
real PostgreSQL control admits two independent public workers concurrently
while one retains its fence, requires reset to wait for that transaction, and
checks stale liveness readback after reset. All 30 integrated PostgreSQL
adapter acceptance tests pass in certified FIPS. This proves the pool fence is
shared rather than a global exclusive claim lock.

Public-pool identity rotation remains owned by
[#742](https://github.com/weaveplatform/infraweave/issues/742): its changed
serial creates a fresh public worker identity. The integration retains that
rule, public quota/image capability checks and stale-public liveness refusal.
These combined local results do not close the separate public delivery ticket.
