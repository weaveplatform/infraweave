# C9: attached policy deletion

Observed: an ordinary attached PLAN policy cannot be deleted with `force` omitted.
The API returns `policy still has attachments` and leaves both sides intact.
`force: true` deletes it and removes the stack attachment.

Partial capture: contextDelete and plugin-managed policy deletion remain unobserved.
All test policies returned `plugin: null`; this capture does not infer plugin behavior.

The final exchange verifies cleanup. REST replay remains deferred to #645.
