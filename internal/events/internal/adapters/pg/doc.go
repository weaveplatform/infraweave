// Package pg stores event delivery identities in PostgreSQL and bounds
// retention and account deletion.
//
// # Input and output
//
// Stream creates event_deliveries. Insert joins the active transaction and
// reports whether the target was new. Prune removes expired rows and Purge
// removes one account’s rows. Event and account references are plain columns;
// retained event identities intentionally outlive the outbox.
//
// # Dependencies
//
// It implements the events port Store over platform/tx and SQL. It imports
// kernel, queue for the no-transaction refusal, and SQL integrity and
// migration packages. It imports no other domain.
package pg
