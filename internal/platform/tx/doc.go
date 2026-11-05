// Package tx runs a unit of work in one PostgreSQL transaction (decision
// 0005). Do begins the transaction, puts it on the context and commits when
// fn returns nil. A serialization failure (40001) or deadlock (40P01) rolls
// back and reruns fn, up to MaxRetries more times after 10 to 100 ms of
// jitter; any other error, or a panic, rolls back once. A Do inside another
// joins the outer transaction, so a store method composes into a caller's
// unit of work. Q returns the context's transaction or the given pool, In
// reports whether there is one, and AfterCommit defers work such as a
// notification until the commit succeeds.
//
// # Input and output
//
// It sends BEGIN, COMMIT and ROLLBACK through the Beginner it is given; fn
// sends everything else. A rollback runs even after ctx ends, so a cancelled
// request never leaves a session idle in a transaction. AfterCommit hooks run
// once, after the commit, with the caller's context.
//
// # Dependencies
//
// It imports pgx and pgconn for the transaction and SQLSTATE types, and the
// standard library otherwise. Store packages call it with platform/sql's
// pools; it imports no infraweave package.
package tx
