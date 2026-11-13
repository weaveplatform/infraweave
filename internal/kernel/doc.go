// Package kernel holds the value types every domain API shares, so that two
// domains exchange a cursor, an ID or a run state without importing each
// other.
//
// It holds the pagination values: Cursor, the keyset position a list
// cursor carries, and PageLimit, which clamps a requested page size to
// DefaultPageLimit and MaxPageLimit. It holds the identifiers: ULID,
// generated monotonically within a millisecond, written as 26 characters
// on the wire and as a uuid in PostgreSQL, whose text UUID gives, and
// Slug, which SlugFrom makes from a name. It holds AuditEntry, which every domain records through
// platform/audit, and OrganizationAuditEntry, an entry of an
// organization's own trail, above any account. It holds ErrUnavailable,
// the mark of a dependency that cannot be reached now, which a store sets
// and the HTTP layer answers with 503, and Invalid, an input that rules
// refuse, naming each refused field as a FieldError with one of the fixed
// reasons and, for a fault in source text, its Range, which the HTTP layer
// answers with 422. It holds CheckLabels,
// the one rule every entity's labels follow, and Expect, the versions a
// conditional write accepts, with ErrStale, which a write whose entity
// has moved past them returns and the HTTP layer answers with 412. It
// holds the tenancy
// identifiers, OrganizationID, AccountID and MemberID, each a ULID of its
// own type so one cannot stand in for another, and Principal: its kind,
// the actor, and the organization, account and member it acts in. Caller
// is the principal's `<kind>:<actor ID>` key, which rate-limit buckets and
// idempotency keys use. It holds the run types: RunState with its terminal
// and passive flags, RunType and whether it blocks its stack, StackState,
// the projection of a stack's tracked run that StackStateOf derives, and
// RunPriority, with RunTier ordering a worker pool's waiting runs, and
// PlanDelta, what a run's plan changes, which a worker reports, the runs
// domain keeps and a stack shows of the run blocking it.
//
// ref: decisions 0003, 0005, 0006, 0007, 0008, 0015 and 0022
//
// # Input and output
//
// None. It parses and validates values and does nothing else: no network,
// file, environment or logging. NewULID reads the clock and crypto/rand,
// the only state it touches.
//
// # Dependencies
//
// It imports the standard library and github.com/oklog/ulid/v2, and no
// database driver, so any package may depend on it, domain api/ packages and
// the agent included. ULID meets pgx through database/sql's Scanner and
// driver.Valuer. The cursor's wire encoding lives in platform/httpkit/page.
package kernel
