// Package core owns policy management, bounded save-time compilation, owning
// spaces, conditional versions and readable account or inherited-space searches.
// Explicit and automatic stack attachments have independent identities; an
// explicit promotion wins over label changes. Space/stack guards preserve manual
// reach, and committed space versions and stack events reconcile automatic rows.
// Effective projections recheck current reach and labels before exposing source.
// Each write appends its policy change in the same transaction. Failed compiler,
// authorization, storage or event operations commit no partial policy write.
//
// # Input and output
//
// Transactions, compilation, store access, space and authorization lookups and
// event appends go through ports supplied by assembly. No clients or background
// tasks are started here. A space guard enqueues reconciliation in the access
// update's transaction. Evaluation selects immutable effective versions, lazily
// builds typed inputs, combines native OPA decisions and appends safe outbox
// metadata in the caller's transaction. Runs owns receipt/flag persistence;
// policy sampling owns sealed input storage, and workflow services own delivery.
//
// # Dependencies
//
// access/api, stacks/api, policies/api, internal/kernel and its search package,
// and platform/expr, platform/outbox, platform/queue, platform/rego and platform/tx.
package core
