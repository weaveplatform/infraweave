// Package core owns policy management, bounded save-time compilation, owning
// spaces, conditional versions and readable account or inherited-space searches.
// Each write appends its policy change in the same transaction. Failed compiler,
// authorization, storage or event operations commit no partial policy write.
//
// # Input and output
//
// Transactions, compilation, store access, space and authorization lookups and
// event appends go through ports supplied by assembly. No clients or background
// tasks are started here. Attachment mutation and evaluation belong to later
// policy tickets; management does not imply those workflows are delivered.
//
// # Dependencies
//
// access/api, policies/api, internal/kernel and its search package, and
// platform/outbox, platform/rego and platform/tx.
package core
