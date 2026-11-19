# C291: rejection in both post-plan review states

Resolved: a tracked run in UNCONFIRMED and a warned proposed run in PENDING_REVIEW
both transition to FAILED after a REJECT review. Both histories record exactly
`Run rejected by approval policies`. Each final evaluation has one rejecting
approval policy and a different approving policy. No infrastructure was applied.

The final exchange verifies cleanup. REST replay remains deferred to #650.
