// Package assembly constructs policy management, persistence and contract
// publication, and exports its migration stream, integrity declarations, subject
// resolver, space guard, account purge and event-kind registration to app.
//
// # Input and output
//
// New takes the PostgreSQL pool, spaces, authorization, bounded compiler and
// transactional outbox. API supplies the strict management partial and Routes
// publishes the typed policy JSON contract on the guarded API listener.
//
// # Effects
//
// Construction opens no connections and starts no background work. Publication
// reuses immutable generated schema bytes. Purge deletes account-owned policies
// and cascades their owned stack relations in bounded, resumable batches.
//
// # Dependencies
//
// access/api, internal/kernel, platform/outbox, platform/rego, platform/sql's
// integrity and migrate packages, chi, and the policies domain's core/http/pg.
package assembly
