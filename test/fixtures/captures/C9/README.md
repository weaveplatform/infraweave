# C9: attached policy deletion

Observed: an ordinary attached PLAN policy cannot be deleted with `force` omitted.
The API returns `policy still has attachments` and leaves both sides intact.
`force: true` deletes it and removes the stack attachment.

Partial capture: contextDelete and plugin-managed policy deletion remain unobserved.
All test policies returned `plugin: null`; this capture does not infer plugin behavior.

The final exchange verifies cleanup. POL-05's `TestAccAttachedPolicyDeletion`
checks refusal, preservation, forced cascade and transactional events against
real PostgreSQL with an authored attachment relation fixture. Full REST replay
awaits #646's attachment APIs; this does not claim an attachment workflow is
already served by policy management.
