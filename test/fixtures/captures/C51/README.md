# C51: reset eligibility of a newly created slug-ID policy

Observed: the API-created APPROVAL policy has a slug-shaped public ID, sets a
flag in QUEUED, and successfully resets it at UNCONFIRMED. Its UNCONFIRMED sample
sees the earlier flag and emits reset_flag; the subsequent review evaluation's
sample has an empty run.flags list. Historical receipts retain the earlier flag.
The tests inspect the sampled inputs and decisions to verify actual removal.

Partial capture: no legacy policy lacking the internal PolicyULID, nor a policy
with a ULID-shaped public ID, was available. Public ID appearance is not an
eligibility test. Acquisition/migration of the internal PolicyULID remains open.

The final exchange verifies cleanup. The `policy-current-owner-reset` subset
replays creation, real tracked planning, historical flag retention, current
flag reset, review rejection and verified cleanup. The captured sample input
after reset stays explicitly deferred to #662; its public read surface belongs
to P13b. This subset does not supply the missing legacy-policy comparison.

Maintainer decision, 2026-11-19: use labelled inference for the missing case,
as specified in ADR 0009. Implementation is unblocked; the absent upstream
observation remains open. This fixture contains real exchanges only and does
not assert the inferred result as an observed response.
