// Package sql opens infraweave's PostgreSQL pools under decision 0005's
// database.* settings. Config is that section: the primary's url, an optional
// read_url for a replica, max_conns, and the statement and idle-in-transaction
// timeouts every session gets, migrate, what a process mode does with
// pending migrations, and iam_auth, the cloud whose IAM token is each new
// connection's password. Open validates it, connects both pools and
// refuses a server below FloorVersion (PostgreSQL 18) with ErrBelowFloor, so
// a feature the schema depends on never fails later as a syntax error. DB
// hands out the primary pool, the read pool (the primary when no replica is
// set) and, through ConnConfig, the primary's connection settings for one
// dedicated connection, with a fresh token under iam_auth: package notify's
// LISTEN connection and the migration runner's.
//
// # Input and output
//
// Open connects to the servers the URLs name and runs one query on each to
// read server_version_num. The timeouts reach the server as session
// parameters on every connection. Errors name the setting that failed and
// never repeat a connection string or a token. Under iam_auth it asks the
// cloud's default credential chain for a token before every new connection
// (AWS signs one locally; Azure and GCP fetch theirs over HTTPS and cache
// it), and the SDKs read their own environment variables and instance
// metadata; infraweave's settings come from app/config, which decodes
// Config.
//
// # Dependencies
//
// It imports pgx and pgxpool; the AWS SDK's config and rds/auth, Azure's
// azidentity, and golang.org/x/oauth2/google for the IAM tokens; and the
// standard library otherwise.
// app/config reads its Config, internal/app opens it, and platform/tx,
// sql/notify and sql/sqltest build on its pools.
package sql
