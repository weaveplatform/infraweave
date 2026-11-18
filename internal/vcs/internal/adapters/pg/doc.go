// Package pg is the vcs domain's PostgreSQL storage: Store, the
// integrations, which seals each one's credential and webhook secret to
// its row and opens them only for Secrets, refuses a second default of a
// provider through vcs_integrations_default, searches them by the
// integration package's Fields, keeps each GitHub App manifest form until
// it is taken once or expires, and purges a deleted account's
// integrations and forms a batch at a time; Stream, the migration stream
// that creates them; Held, the columns that name other domains' rows; and
// SealedColumns, the three sealed secret columns. Tokens keeps the
// provider's ID of each token minted for a run in vcs_run_tokens, never
// the token, until the run's revocation forgets it; a purge deletes them
// with the account.
// Deliveries reads the current and valid previous webhook secrets without
// opening access credentials. It holds the integration while inserting a
// verified delivery, and retains its provider for jobs after integration
// deletion. Delivery/job/blob transitions share the caller's transaction;
// retention and account purge return body keys for deferred blob deletion.
// Pull request comment mappings retain native IDs, Azure thread IDs and Data
// Center versions. Bind, current-head holds, native-reference acknowledgement
// and minimization bookkeeping join the caller's transaction. Account purge
// removes these credential-free records in bounded batches.
//
// # Input and output
//
// PostgreSQL, through the pool its caller passes; the stream's migrations
// are embedded in the binary.
//
// # Dependencies
//
// It imports pgx, the domain's integration and source packages,
// internal/kernel and its search package, platform/crypt, platform/sql
// and its search, integrity and migrate packages, and platform/tx. The
// domain's assembly constructs it and internal/app registers its stream.
// Native repository maps, current installation IDs and membership tombstones
// are account/integration scoped and cascade with integration deletion.
package pg
