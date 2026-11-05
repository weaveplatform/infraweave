// Package cron is decision 0011's scheduling: the one parser of five-field
// cron expressions, the next-fire computation in a time zone, and the
// ticker that fires due rows once across replicas.
//
// Parse reads minute, hour, day of month, month and day of week, each a
// comma list of `*`, `n` or `a-b` with an optional `/step`, and rejects
// everything else with ErrSyntax and the field at fault (decision 0022's
// PD-09). String renders the canonical form, which parses to an equal
// Schedule. Schedule.Next and NextOf compute the next firing on the local
// wall clock: day of month and day of week combine as either-matches when
// both are restricted, a time a spring-forward change skips fires once at
// the end of the gap, and a time an autumn change repeats fires at its
// first occurrence.
//
// A Ticker runs in every scheduler replica. Each tick it asks each Source,
// in a transaction of its own per batch of Batch rows, to lock its due rows
// with FOR UPDATE SKIP LOCKED, enqueue their fire jobs and store their next
// fire times, so two replicas never fire one due time twice and no leader
// is elected. PeriodicTasks is the Source of infraweave's own recurring
// work: Ensure registers a job kind with policy Ignore and an interval, and
// a due task fires once however many runs it missed.
//
// # Input and output
//
// Parse and Next read nothing but their arguments; time zones come from
// the tzdata that cmd/infraweave embeds, so results don't depend on the
// host. PeriodicTasks reads and writes the platform stream's
// periodic_tasks table and enqueues on the jobs table through the
// transaction on the context. Database errors that mean PostgreSQL is
// unreachable are marked with kernel.ErrUnavailable. The ticker logs its
// start and stop at info, fired rows at debug, and a failing tick at warn,
// once until a tick succeeds again.
//
// # Dependencies
//
// It imports pgx, platform/tx, platform/queue for EnqueueTx and job kinds,
// and platform/sql for the unavailable error; the ticker logs through
// log/slog. The scheduler mode in internal/app runs the ticker; later
// phases add their own sources, such as scheduled runs and drift detection.
package cron
