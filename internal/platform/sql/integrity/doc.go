// Package integrity counts the IDs one domain's tables hold of another
// domain's rows after those rows are gone. No key joins two domains'
// tables, so a deleted account's or space's rows elsewhere go only when
// the holding domain removes them; a count that stays above zero names a
// holder that missed some.
//
// Key is how the owning domain finds an entity's row; Held is a column set
// of a holding domain's table that holds the entity. Count reads, for each
// Held, the distinct IDs no row of its entity's Key matches, one query per
// Held, and refuses a Held whose entity has no Key, or whose columns do
// not pair with it, as ErrUndeclared.
//
// ref: decision 0004
//
// # Input and output
//
// Count reads PostgreSQL through the querier it is given, naming tables
// and columns only as the declarations give them, quoted. A query that
// fails because PostgreSQL cannot be reached is marked
// kernel.ErrUnavailable.
//
// # Dependencies
//
// It imports pgx for quoting, platform/tx for the querier and platform/sql
// for Unavailable. Each domain's pg adapter declares its Keys or its Held
// columns, and internal/app counts them nightly.
package integrity
