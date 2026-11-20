// Package assembly constructs policy management, persistence and contract
// publication, and exports its migration stream, integrity declarations, subject
// resolvers, space/stack guards, account purge, event-kind registration, durable
// consumers and job registrations to app. Stack projections use its owned port.
//
// # Input and output
//
// New takes the PostgreSQL pool, spaces, stacks and their preview port,
// authorization, bounded compiler and
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
// access/api, stacks/api, internal/kernel, platform/outbox, platform/queue,
// platform/rego, platform/sql's
// integrity and migrate packages, chi, and the policies domain's core/http/pg.
package assembly
