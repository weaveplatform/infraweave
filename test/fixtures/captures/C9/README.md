# C9: attached policy deletion

Observed: an ordinary attached PLAN policy cannot be deleted with `force` omitted.
The API returns `policy still has attachments` and leaves both sides intact.
`force: true` deletes it and removes the stack attachment.

Partial capture: contextDelete and plugin-managed policy deletion remain unobserved.
All test policies returned `plugin: null`; this capture does not infer plugin behavior.

The final exchange verifies cleanup. POL-05's `TestAccAttachedPolicyDeletion`
checks refusal, preservation, forced cascade and transactional events against
real PostgreSQL with an authored attachment relation fixture. `TestAccCaptures/C9`
also creates the stack, policy and attachment through the running REST server,
checks refusal and preservation, forces deletion, verifies both projections are
gone, and deletes the stack with a final 404 cleanup check. Raw upstream evidence
is unchanged; the replayer checks the invariant using infraweave's status and
module contracts.
