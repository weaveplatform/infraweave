// Package intake durably accepts verified VCS deliveries before acknowledging
// them. Verification precedes filtering and storage; accepted deliveries and
// their processing jobs commit together. Large bodies use the platform's
// pending/live inbox lifecycle, with a fresh key per attempt so a duplicate
// cannot remove the winning body.
//
// Processing parses retained bytes and completes with a lease-fenced state
// change. Failed attempts keep safe metadata; the minute reconciliation sweep
// also settles final lease deaths and discarded jobs, using bounded pages and
// durable cursor continuations. Retention prunes rows hourly. Account purge
// drops their rows and schedules body deletion after integration deletion has
// fenced new intake.
//
// # Input and output
//
// Receive takes provider headers and bounded body bytes from the HTTP adapter,
// and reads the current and still-valid previous webhook secrets through Store.
// It stores safe headers, a retained HMAC, exact body bytes or an inbox key, and
// queue payloads containing IDs alone. Jobs parse through the provider's pure
// Webhooks capability; they make no VCS calls and create no runs or policies.
// Now is injected for secret overlap, received-time deadlines and retention.
//
// # Dependencies
//
// It imports internal/kernel, platform/objectstore, platform/queue, platform/tx
// and the domain's core/port. Stores, blobs, queue and provider capabilities are
// injected; no adapter or other domain implementation is imported.
//
// ref: decisions 0011, 0012 and 0013
package intake
