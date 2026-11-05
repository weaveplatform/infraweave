// Package sql opens infraweave's PostgreSQL pools under decision 0005's
// database.* settings. Config is that section: the primary's url, an optional
// read_url for a replica, max_conns, and the statement and idle-in-transaction
// timeouts every session gets. Open validates it, connects both pools and
// refuses a server below FloorVersion (PostgreSQL 18) with ErrBelowFloor, so
// a feature the schema depends on never fails later as a syntax error. DB
// hands out the primary pool, the read pool (the primary when no replica is
// set) and a copy of the primary's connection settings for the one LISTEN
// connection of package notify.
//
// # Input and output
//
// Open connects to the servers the URLs name and runs one query on each to
// read server_version_num. The timeouts reach the server as session
// parameters on every connection. Errors name the setting that failed and
// never repeat a connection string, which may carry a password. It reads no
// file and no environment variable: app/config decodes Config.
//
// # Dependencies
//
// It imports pgx and pgxpool, and the standard library otherwise.
// app/config reads its Config, internal/app opens it, and platform/tx,
// sql/notify and sql/sqltest build on its pools.
package sql
