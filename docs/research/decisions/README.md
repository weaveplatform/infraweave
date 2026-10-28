# Design decisions

These records set out infraweave's intended design. Each one gives:
- the context;
- the contract;
- the rationale;
- the constraints;
- the evidence that verifies it.

A decision's status records its design approval, not whether everything it
describes is implemented.

Use the [template](TEMPLATE.md) for new decisions and leave out sections that
would be empty. Integrate amendments into the relevant record, keeping its
number and filename so existing links still resolve.

Two rules keep the set readable:

- **Research reports hold facts and citations; decision records hold the
  decision and its rationale.** A record links to the section of the
  [research report](../README.md#document-map) that holds the facts it relies
  on. It never restates them.
- **Records never contain milestones or sequencing.** Ordering belongs in the
  implementation plan.

Status values: Proposed, Accepted, Superseded. `make docs-check` fails when a
record is missing from this index or its status here differs from its own.

| Decision | Design | Status |
|---|---|---|
| 0001 | [A REST-first surface for the Spacelift feature set](0001-rest-first-spacelift-surface.md) | Accepted |
| 0002 | [Committed reference sources and their licensing](0002-reference-sources-and-licensing.md) | Accepted |
| 0003 | [Idempotency keys, rate limits and cursor pagination](0003-api-conventions.md) | Accepted |
