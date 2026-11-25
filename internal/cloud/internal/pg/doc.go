// Package pg is the cloud domain's PostgreSQL store: aws_integrations,
// one row per AWS integration with its role ARN, region, session duration,
// custom external ID and labels, and aws_integration_attachments, which
// stacks each integration serves for read, write or both. Store implements
// core.Store; a unique key a write breaks answers core.NameTaken or
// core.Conflict, and the partial unique indexes keep a stack at one read
// and one write attachment. AzureStore implements core.AzureStore over
// azure_integrations, one row per Azure integration with its tenant, its
// Entra application's client and object IDs, its default subscription and
// its consent statement, and azure_integration_attachments, with each
// attachment's subscription, keyed and checked as the AWS tables are.
// Stream is the cloud migration stream, Held the
// columns that hold other domains' rows for the nightly orphan count, and
// Purge deletes a deleted account's rows in batches. Audit records an
// entry through platform/audit in the caller's transaction.
//
// # Input and output
//
// It reads and writes the cloud stream's tables and audit_trail_entries,
// through the pool or the transaction the context carries.
//
// # Dependencies
//
// It imports the domain's core package, internal/kernel, pgx,
// platform/audit, platform/sql with its integrity and migrate packages,
// and platform/tx.
package pg
