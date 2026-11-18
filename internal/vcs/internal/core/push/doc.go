// Package push applies the default branch/path behavior to verified VCS
// deliveries and supplies the account's event-run limit. Policy evaluation,
// ignored-trigger records and provider feedback are separate consumers.
//
// # Input and output
//
// Prepare receives normalized events and immutable delivery identity. It reads
// effective repository bindings and computes each complete push diff outside
// database transactions, bounded by the delivery's received-time deadline.
// Its plan stores full prepared facts once and enqueues small references: one
// child per delivery/stack, preserving the order of every applicable ref.
// The intake processor commits that plan, dispatched state and queue completion
// under the same queue lease fence.
//
// Evaluated rechecks the deadline, delivery, integration and stack ownership,
// holds the stack's creation lock before requesting run stops, and joins run
// creation to the transaction completing the child. Once-per-commit admission
// recognizes duplicates before reserving a delivery slot. A later cap refusal
// preserves earlier admitted events; unexpected failures roll back all effects.
// A tracked branch head can advance without a run, but the tracked commit moves
// only after successful lifecycle admission. Expired or purged work completes
// without creating runs. Refused stacks produce a fixed warning; duplicates are
// silent above debug. Provider diagnostics remain inside intake's safe failure
// boundary rather than becoming retained delivery errors.
//
// Limits reads nullable account settings without a replica cache. Set validates
// the operator bound and commits the setting with its caller-attributed audit
// while holding account ownership. Conditional slot reservation belongs to the
// run-creating transaction. Side effects are durable database/queue writes;
// diagnostic evaluation logs run only after the transaction commits.
//
// # Dependencies
//
// The service uses the platform transaction and queue contracts, accessapi for
// owner holds, stacksapi for effective source/creation locks and projections,
// and runsapi for creation and stop requests. VCS-owned stores and source ports
// retain prepared data and perform provider reads. Provider adapters and other
// domains' persistence remain outside this package.
package push
