// Package assembly constructs the events sinks hexagon after VCS. It
// subscribes events.sinks to the kinds VCS feedback names, registers the
// delivery-record migration and hourly pruning task, and exposes the deleted-
// account purge. It serves no HTTP operation.
//
// # Input and output
//
// New takes the primary database, VCS Feedback, delivery retention, an
// optional OpenTelemetry meter and clock. Stream and Held describe owned
// tables and account references.
//
// # Dependencies
//
// It imports its delivery core, persistence and metric adapters, vcsapi,
// kernel and the platform queue, transaction, migration, integrity, outbox
// and cron packages. Only internal/app and cmd construct it.
package assembly
