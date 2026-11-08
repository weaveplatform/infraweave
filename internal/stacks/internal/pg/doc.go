// Package pg is the stacks domain's PostgreSQL storage: Stream, its
// migration stream, whose stacks table keys each stack by its account and
// ULID, with a slug unique per account and the ID of the space it lives
// in, which carries no foreign key, since spaces belong to the access
// domain. Store reads a stack, stacksapi.ErrNotFound when the account lacks it,
// and answers whether a stack lives in a space. Every method joins the
// transaction its context carries.
//
// # Input and output
//
// It reads PostgreSQL through the DB it is given.
//
// # Dependencies
//
// It imports stacksapi, the domain's stacks package for its port,
// internal/kernel, platform/sql, platform/sql/migrate, platform/tx and
// pgx. The stacks domain's assembly constructs it.
package pg
