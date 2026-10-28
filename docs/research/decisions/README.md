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
| 0005 | [Persistence: PostgreSQL, embedded migrations and object storage](0005-persistence-postgresql.md) | Proposed |
| 0006 | [Authentication and tokens](0006-authentication-and-tokens.md) | Proposed |
| 0007 | [Authorization: spaces and roles](0007-authorization-spaces-and-roles.md) | Proposed |
| 0008 | [Run execution and the worker protocol](0008-run-execution-and-worker-protocol.md) | Proposed |
| 0009 | [Policy engine: embedded OPA, Rego v0 and v1](0009-policy-engine-opa.md) | Proposed |
| 0010 | [Terraform and OpenTofu state backend](0010-state-backend.md) | Proposed |
| 0011 | [Persistent work queue and scheduler](0011-persistent-work-queue.md) | Proposed |
| 0012 | [VCS integration model](0012-vcs-integration-model.md) | Proposed |
| 0013 | [Secrets at rest, delivery to runs and log masking](0013-secrets-at-rest.md) | Proposed |
| 0014 | [Module and provider registry protocol](0014-module-and-provider-registry.md) | Proposed |
| 0015 | [Events: notifications, webhooks and audit](0015-events-notifications-webhooks-audit.md) | Proposed |
| 0016 | [Testing and acceptance against real dependencies](0016-testing-and-acceptance.md) | Proposed |
| 0017 | [Observability and health](0017-observability-and-health.md) | Proposed |
| 0018 | [Flows runtime](0018-flows-runtime.md) | Proposed |
| 0019 | [AI provider boundary](0019-ai-provider-boundary.md) | Proposed |
| 0020 | [Reference deployments and packaging](0020-reference-deployments-and-packaging.md) | Proposed |
| 0021 | [Capability scope and supersession](0021-capability-scope-and-supersession.md) | Proposed |
| 0022 | [Served API errors, update semantics and input strictness](0022-api-errors-and-update-semantics.md) | Proposed |
