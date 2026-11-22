# C292: partial pool views, with private priority behavior still open

This is a real read-only V5_FREE capture from Chrome DevTools MCP. It contains
nine completed GraphQL exchanges: eight UI-triggered pool overview, queue and
used-by requests, plus one explicitly authored read-only inventory query. The
five ui-*.txt files preserve actual overview, create-form, public queue,
used-by and first-step stack-wizard accessibility snapshots. Opening a form is
not a creation attempt. No resource mutation, private worker or run was made.

The public pool reports parallelism 1 and no busy, queued or schedulable work.
The private pool search and root attachable-pool list are empty. The overview
explicitly warns that private jobs are not scheduled on this plan. The create
form has name, CSR, space, description, drift-run limit and labels, with no cloud
selector. Both account runner-image defaults are the empty string: this does
not reveal the resolved vendor image. Public egress addresses are preserved as
returned. The stack's later wizard steps are disabled; assignment is unproven.

C292 asks about **private** priority actions and ordering. An empty public queue
and a suggestions enum do not answer that question. The record stays open;
no private-run mutation, queue movement, controller traffic, pod deadline,
worker initialization input or vendor metrics was captured. The complete
[question/owner inventory](../../../../docs/implementation/worker-pool-reference-observations.md)
keeps these boundaries explicit.

## Verification and handoff

The existing capture loader validates every observed pointer and redaction.
The observations are replayAfter #744 because public pool/search projections are
not implemented yet; no expected 501 response is substituted for their contract.
#744 must replace this deferral with real API/SDK assertions on equivalent
public projections, keeping reference limits distinct from infraweave's tier and
grants. #753 audits the complete operation matrix. Actual private prioritization
remains open and is owned by #740/#743/#744. Only the account name/subdomain and
browser/unrelated request material are omitted; GraphQL data stays unchanged.

The schema fingerprint is SHA-256 of the actual account's response to the
repository's standard IntrospectionQuery. It is provenance, not a claim that the
committed reference snapshot and current account schema are identical.
