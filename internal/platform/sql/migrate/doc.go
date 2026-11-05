// Package migrate applies infraweave's embedded schema migrations (decision
// 0005). A Stream is one domain's files, or the platform's, named
// NNNNN_snake_case.sql and gap-free from 00001; Load checks them and reads
// their headers: -- infraweave:no-transaction on the first line runs a file
// outside a transaction, and -- infraweave:compatible-from NNNNN raises the
// oldest version of the stream a running binary may know.
//
// Up applies every pending file of the streams in their registration order,
// each in its own transaction with its record, under a session advisory lock
// so that replicas starting together apply each file once. Check, for
// database.migrate: check, refuses while files are pending. Inspect reports
// each stream's applied and pending files. All three refuse a schema the
// binary must not run on with ErrRefused: a changed file, a diverged schema,
// a stream whose compatible_from is newer than the binary, or a database
// below a stream's upgrade Floor, which would skip a major (decision 0020).
// Forward only: there are no down migrations.
//
// # Input and output
//
// Each call opens one connection of its own from the given settings, named
// ApplicationName, with no statement timeout: the lock waits for another
// runner, and an index build takes as long as its table; the context bounds
// both. Up creates and writes the bookkeeping tables schema_migrations and
// schema_state; Check and Inspect only read them. It logs nothing: callers
// report Status.Ran.
//
// # Dependencies
//
// It imports pgx and pgconn, and the standard library otherwise. internal/app
// registers the streams and calls it at start and from infraweave migrate;
// platform/sql/sqltest migrates its template with it.
package migrate
