// Package kernel holds the value types every domain API shares, so that two
// domains exchange a cursor, an ID or a run state without importing each
// other.
//
// It holds decision 0003's pagination values: Cursor, the keyset position a
// list cursor carries, and PageLimit, which clamps a requested page size to
// DefaultPageLimit and MaxPageLimit. It holds decision 0005's identifiers:
// ULID, generated monotonically within a millisecond, written as 26
// characters on the wire and as a uuid in PostgreSQL, whose text UUID
// gives, and Slug. It holds
// decision 0015's AuditEntry, which every domain records through
// platform/audit. It holds ErrUnavailable, the mark of a dependency that cannot be reached now, which
// a store sets and the HTTP layer answers with 503. It holds decision
// 0007's tenancy identifiers, OrganizationID, AccountID and MemberID, each a
// ULID of its own type so one cannot stand in for another, and decision
// 0006's Principal: its kind, the actor, and the organization, account and
// member it acts in. Caller is the principal's `<kind>:<actor ID>` key,
// which rate-limit buckets and idempotency keys use. Run states and the other
// shared types join it with the phases that need them.
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
