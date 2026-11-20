// Package pg persists account-qualified policies and their owned stack relation
// in PostgreSQL. Reads retain the account boundary, searches filter readable
// spaces before paging and counting, and writes join the context's transaction.
// Stream owns the initial schema, Held declares cross-domain references, and
// Purge removes a deleted account's rows in resumable batches.
//
// # Input and output
//
// PostgreSQL through the supplied pool or transaction. Migration files are
// embedded in the binary. This package starts no background work.
//
// # Dependencies
//
// The domain's core, internal/kernel and its search package, platform/sql and
// its integrity, migrate and search packages, platform/tx, and pgx.
package pg
