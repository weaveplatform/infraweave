// Package outbox is the event emit path, shared by Deploy and Flows: a
// service appends an event in the transaction of the change it describes, so
// the event exists exactly when the change commits, and the event reaches
// every consumer of its kind at least once.
//
// Event is the envelope. Kinds holds each kind's JSON Schema per schema
// version (draft 2020-12); Append refuses an event of a kind and version
// with no schema (ErrKind), a document its schema rejects (ErrDocument),
// an event without an account, kind or subject (ErrEvent), or one made
// outside a transaction (ErrNoTransaction). Subscribe names a consumer of
// a kind and its Handler. Register adds the outbox's two job kinds:
// events.fanout (FanoutKind) enqueues one events.consume job
// (ConsumeKind) per consumer of the event's kind that has none yet, so a
// fan-out that runs twice reaches no consumer twice, and marks the event
// fanned out; events.consume runs the consumer's handler, on a job of its
// own so a failing consumer retries without holding up the others. Prune,
// which the scheduler's events.prune task runs, deletes events Config's
// retention after their fan-out and never one not yet fanned out.
//
// ref: decision 0015
//
// # Input and output
//
// Append writes the platform stream's events table, an events.fanout job
// and a notification on infraweave_events carrying <account>:<id>, all
// through the transaction on the context; PostgreSQL delivers the
// notification at commit. The handlers read events and write
// event_consumptions, events.consume jobs and fanned_out_at, each fan-out
// in one transaction. Documents are stored as the JSON given. Database
// errors that mean PostgreSQL is unreachable are marked with
// kernel.ErrUnavailable. It logs nothing; the workers log each job.
//
// # Dependencies
//
// It imports platform/queue for the jobs, platform/tx for the change's
// transaction, platform/sql and sql/notify for errors and the
// notification, internal/kernel for ULIDs, pgx for the no-rows error, and
// santhosh-tekuri/jsonschema for the schemas. Every domain that emits
// imports it, and internal/app registers its handlers in the scheduler.
package outbox
